# SSE 客户端中断与服务端计费行为调研

> 调研问题：当 SSE（Server-Sent Events）流式任务被用户端中断，导致计费/usage 内容未完整传送到用户端时，**服务端是否仍继续计费？**
>
> 结论先行：**会继续计费（按网关已掌握的 partial usage 结算），不会因为客户端断开而自动全额退预扣。**  
> 同时会尽快关闭上游连接，尽量减少继续生成；但已产生/已接收部分仍会计入扣费。
>
> 核对代码基准：`relay/helper/stream_scanner.go`、`relay/helper/common.go`、`relay/channel/openai/relay-openai.go`、`relay/channel/claude/relay-claude.go`、`relay/channel/gemini/relay-gemini.go`、`relay/channel/openai/relay_responses.go`、`relay/compatible_handler.go`、`controller/relay.go`、`service/text_quota.go`、`service/log_info_generate.go`、`relay/common/stream_status.go`。

---

## 1. 问题界定

本文「SSE 任务」主要指同步 Relay 的流式响应（`stream=true` / `text/event-stream`），例如：

- OpenAI Chat Completions 流式
- Claude Messages 流式（含转 OpenAI 格式）
- Gemini 流式
- OpenAI Responses 流式
- 其他走 `helper.StreamScannerHandler` 的上游 SSE 适配

**不包含**（行为不同，文末仅简要对比）：

- 异步视频任务轮询（`tasks` 表两阶段计费）
- 管理端进度 SSE（如渠道测试进度推送）
- 纯 WebSocket Realtime（另有增量扣费，见第 8 节）

「计费内容没传到用户端」通常指：

1. 客户端中途取消/关连接/刷新页面；
2. 上游 usage 往往在流末尾（或独立 final chunk）才出现；
3. 客户端看不到完整 `[DONE]` / usage chunk / 最终计费字段；
4. 但网关侧已经读到部分 delta，或已能本地估算 token。

---

## 2. 一句话结论

| 问题 | 答案 |
|------|------|
| 客户端断开后，服务端是否还结算扣费？ | **是**。主流式 handler 在 `StreamScannerHandler` 返回后仍计算 `usage` 并调用 `PostTextConsumeQuota` / `SettleBilling`。 |
| 是否因为「用户没收到 usage」就全额退预扣？ | **否**。全额 `Refund` 仅在 `controller/relay.go` 判定 `newAPIError != nil` 时触发；客户端断开通常**不**产生该错误。 |
| 是否会继续无限读上游并按完整生成计费？ | **尽量不会**。检测到 `c.Request.Context().Done()` 后立刻 `cleanup()` 关闭 `resp.Body`，停止继续消费上游 SSE，减少后续 token。 |
| 账单记什么？ | 优先用已收到的上游 usage；没有则用**已接收文本的本地 token 估算** + 预估 prompt tokens。日志 `other.stream_status.end_reason = client_gone`。 |
| 用户端是否一定看到计费字段？ | **不保证**。向客户端写 final usage/`[DONE]` 时若 context 已取消，`StringData` 会失败并常被忽略。 |

---

## 3. 端到端时序（客户端中断）

```text
客户端 ──stream=true──▶ new-api
                          │
                          ├─ PreConsumeBilling(估算预扣)
                          ├─ 选择渠道，DoRequest 上游
                          ├─ DoResponse → StreamScannerHandler
                          │     循环：读上游 SSE → dataHandler
                          │           → 累计 responseText / partial usage
                          │           → StringData/ObjectData 写回客户端
                          │
客户端断开 ◀──────────────┤  c.Request.Context().Done()
                          │
                          ├─ StreamStatus.EndReason = client_gone
                          ├─ cleanup(): close(resp.Body)  // 停止继续读上游
                          ├─ 流式 handler 仍返回 usage（可能 partial / 本地估算）
                          ├─ err == nil
                          ├─ PostTextConsumeQuota(usage)
                          │     UpdateUserUsedQuotaAndRequestCount (若 TotalTokens>0)
                          │     SettleBilling(actualQuota)   // 差额补扣或退还预扣差额
                          │     RecordConsumeLog(... stream_status=client_gone)
                          └─ controller defer: 因 newAPIError==nil，不 Refund 全额预扣
```

关键分叉：**「流中断」≠「请求失败需退款」**。  
中断被建模为流结束原因 `client_gone`，计费仍走成功结算路径。

---

## 4. 流式基础设施：断开如何被感知

### 4.1 `StreamScannerHandler`（核心）

文件：`relay/helper/stream_scanner.go`

主等待循环：

```go
select {
case <-ticker.C:
    // 空闲超时 StreamingTimeout（默认 300s）
    info.StreamStatus.SetEndReason(StreamEndReasonTimeout, nil)
case <-stopChan:
    // 其他 goroutine 触发停止（[DONE]、handler stop、panic 等）
case <-c.Request.Context().Done():
    // 客户端断开：立即 cleanup 关闭上游 resp.Body
    info.StreamStatus.SetEndReason(StreamEndReasonClientGone, c.Request.Context().Err())
}
cleanup()
```

注释写明设计意图（代码原文逻辑）：

> 客户端断开后立即 cleanup 关闭上游 `resp.Body`，解除 scanner 阻塞并让上游停止生成，**避免为已放弃的请求继续消费上游 token**。

单测 `TestStreamScannerHandler_ClientDisconnectClosesUpstream` 验证：

- 断开后 handler 能返回；
- 上游 pipe 写会失败（body 已关）；
- 断开后新 chunk 不再处理；
- `EndReason == client_gone`。

### 4.2 写回客户端时的断开检测

文件：`relay/helper/common.go`

```go
func requestContextDone(c *gin.Context) bool {
    return c != nil && c.Request != nil && c.Request.Context().Err() != nil
}

func StringData(...) error {
    if requestContextDone(c) {
        return fmt.Errorf("request context done: %w", c.Request.Context().Err())
    }
    // Render SSE data + Flush
}
```

`ObjectData` / `PingData` / `FlushWriter` / Claude/Responses 写路径同样先检查 context。

含义：

- **客户端已断开时，后续 SSE 写入会失败**（用户看不到后续/usage chunk）；
- 多数适配器把写失败记为 **soft error**（`sr.Error`）或直接 `_ = ObjectData(...)` 忽略，**不升级为 DoResponse 的 fatal NewAPIError**。

### 4.3 流结束原因枚举

文件：`relay/common/stream_status.go`

| EndReason | 含义 | `IsNormalEnd()` |
|-----------|------|-----------------|
| `done` | 收到 `[DONE]` 或 handler Done | 是 |
| `eof` | 上游正常 EOF | 是 |
| `handler_stop` | 业务主动 Stop | 是 |
| **`client_gone`** | **客户端断开** | **否** |
| `timeout` | 流空闲超时 | 否 |
| `scanner_error` / `panic` / `ping_fail` | 异常 | 否 |

`IsNormalEnd()==false` 只影响日志 `stream_status.status`（记为 `error`），**不阻断计费**。

### 4.4 日志落点

`service/log_info_generate.go` → `appendStreamStatus`：

```json
"stream_status": {
  "status": "error",
  "end_reason": "client_gone",
  "end_error": "context canceled",
  "error_count": 1,
  "errors": ["request context done: ..."]
}
```

便于事后区分「正常完成」与「用户取消仍扣费」。

---

## 5. 计费结算：为什么断开仍扣费

### 5.1 控制器退款条件（唯一全额退预扣入口）

文件：`controller/relay.go`

```go
// 成功路径预扣
newAPIError = service.PreConsumeBilling(...)

defer func() {
    if newAPIError != nil {
        if relayInfo.Billing != nil {
            relayInfo.Billing.Refund(c)   // 全额退预扣
        }
        service.ChargeViolationFeeIfNeeded(...)
    }
}()
```

只有 **整次 Relay 以 NewAPIError 失败** 才 Refund。  
流式客户端断开若 handler 返回 `(usage, nil)`，则：

- **不 Refund**
- 走 `PostTextConsumeQuota` → `SettleBilling(actual)`  
  - `actual < pre`：退还差额  
  - `actual > pre`：补扣差额  
  - `actual == pre`：不动

### 5.2 兼容 OpenAI 文本流（最常见）

`relay/compatible_handler.go`：

```go
usage, newApiErr := adaptor.DoResponse(c, httpResp, info)
if newApiErr != nil {
    return newApiErr
}
service.PostTextConsumeQuota(c, info, usage.(*dto.Usage), nil)
```

`relay/channel/openai/relay-openai.go` → `OaiStreamHandler`：

1. `StreamScannerHandler` 读流；每 chunk `processTokenData` 累积文本；
2. 写客户端失败仅 `sr.Error`（软错误），**不中止计费累计**；
3. 流结束后：
   - 若最后一帧带合法 usage → 用上游 usage；
   - 否则 `ResponseText2Usage(已接收文本, estimatePromptTokens)` 本地估算；
4. `HandleFinalResponse` 尝试补发 usage/`[DONE]`（客户端已断则写失败被忽略）；
5. **`return usage, nil`** —— 明确不因 client_gone 返回错误。

因此：**用户看不到 usage，服务端仍按 partial/本地估算结算。**

### 5.3 usage 来源优先级（OpenAI Chat 流）

| 优先级 | 条件 | 来源 |
|--------|------|------|
| 1 | 流中/末帧含 ValidUsage | 上游 usage（完整生成时常见，中断时经常拿不到） |
| 2 | 音频模型特殊路径 | 倒数第二帧 usage |
| 3 | 回退 | `ResponseText2Usage`：`PromptTokens=预估输入`，`CompletionTokens=已接收文本本地计 token` |

`service/usage_helpr.go`：

```go
func ResponseText2Usage(...) *dto.Usage {
    // 标记 local count
    usage.PromptTokens = promptTokens          // 通常为请求时估算
    usage.CompletionTokens = EstimateTokenByModel(modeName, responseText)
    usage.TotalTokens = Prompt + Completion
    return usage
}
```

中断越早：

- completion 越小（只计已推到网关的文本）；
- prompt 仍可能按完整输入估算计费（输入在请求阶段已确定）；
- 若完全无输出文本，仍可能只扣输入侧。

### 5.4 `PostTextConsumeQuota` 对 0 token 的处理

`service/text_quota.go`：

```go
if summary.TotalTokens == 0 {
    summary.Quota = 0
    // 不更新 used_quota / channel used
} else {
    UpdateUserUsedQuotaAndRequestCount(...)
    UpdateChannelUsedQuota(...)
}
SettleBilling(ctx, relayInfo, summary.Quota) // 始终调用
```

含义：

- `TotalTokens==0` → `actualQuota=0` → Settle 会把预扣**退光**（等价于最终不扣）；
- 但 OpenAI 回退路径几乎总会带上 `estimatePromptTokens`，**TotalTokens 通常 > 0**，因此多数中断仍会扣至少输入成本；
- Gemini 在 `ReceivedResponseCount==0` 时可能返回空 usage，更接近「几乎全退预扣」的边缘情形。

### 5.5 固定价格（UsePrice）模型

按次/固定价模式下，`calculateTextQuotaSummary` 以 `modelPrice * QuotaPerUnit * groupRatio` 为主。  
若 `TotalTokens==0` 仍会强制 `Quota=0`；一旦有任意 token（含仅 prompt 估算），可能按**整次固定价**结算，而不是按生成比例。  
这对「中途取消是否按次全额」有产品含义，排障时需结合具体模型计费模式。

---

## 6. 多协议对照

### 6.1 Claude 流

`ClaudeStreamHandler`：

- 同样基于 `StreamScannerHandler`；
- 写失败多数只打日志，不把 client_gone 变成 API error；
- 结束后 `HandleStreamFinalResponse`：若 usage 不完整，用 `ResponseText2Usage` 补 completion/prompt；
- **返回 `(claudeInfo.Usage, nil)`** → 上层照常 `PostTextConsumeQuota`。

### 6.2 Gemini 流

`geminiStreamHandler`：

- 有 usage metadata 则用上游；
- 否则：
  - `ReceivedResponseCount > 0`：本地文本估算；
  - `ReceivedResponseCount == 0`：空 usage（后续更可能 `Quota=0` 并退预扣）；
- **返回 `(usage, nil)`**。

### 6.3 OpenAI Responses 流

`OaiResponsesStreamHandler`：

- usage 主要来自 `response.completed`（通常靠后）；
- 客户端早断时往往拿不到 completed；
- 回退：对已累积 `output_text.delta` 做 `CountTextToken`，prompt 用 estimate；
- **返回 `(usage, nil)`**。

### 6.4 其他走 StreamScanner 的渠道

Baidu / Cohere / Dify / XAI / Cloudflare 等模式一致：

1. Scanner 统一处理 client_gone + 关上游；
2. 适配器组装 best-effort usage；
3. 上层成功路径结算。

**未发现**任何适配器在 `EndReason==client_gone` 时跳过 `PostTextConsumeQuota` 的统一分支。

---

## 7. 「计费内容未到用户端」的具体场景拆解

### 场景 A：用户读到一半取消

| 项目 | 行为 |
|------|------|
| 用户看到 | 部分 delta，无完整答案，可能无 usage |
| 上游 | body 关闭，争取停止继续生成 |
| 服务端 usage | 本地统计已接收文本 + 估算 prompt；或已收到的 partial 上游 usage |
| 扣费 | **结算 partial actual**；预扣多退少补 |
| 日志 | `stream_status.end_reason=client_gone` |

### 场景 B：几乎刚连上就断开（尚未收到任何 data）

| 项目 | 行为 |
|------|------|
| 用户看到 | 基本无内容 |
| OpenAI 路径 | `ResponseText2Usage("", estimatePrompt)` → 可能仍扣 **输入 token** |
| Gemini 路径 | 可能空 usage → Quota=0 → 退预扣 |
| 是否全额 Refund 会话 | 否（仍走 Settle，不是 defer Refund） |

### 场景 C：上游已发完，客户端在收 usage/`[DONE]` 前断开

| 项目 | 行为 |
|------|------|
| 网关 | 可能已拿到最后一帧 usage（取决于断开时机与 dataChan 处理） |
| 若已解析到 ValidUsage | 按**完整上游 usage** 扣费（即使用户没看到） |
| 若未解析到 | 回退本地文本估算（可能略低于真实 completion） |

### 场景 D：写客户端失败但上游仍在推（竞态窗口）

- 主循环靠 `Request.Context().Done()` 与 cleanup 收敛；
- 已进入 `dataChan` 的 chunk 可能仍被 handler 处理并计入 token；
- 新的上游数据在 body 关闭后停止；
- 测试保证断开后**不会**再处理新的 upstream second chunk。

### 场景 E：流空闲超时（非用户主动断，但用户侧也可能表现为「没结束」）

- `EndReason=timeout`；
- 同样通常返回 usage 并结算，而不是全额 Refund（除非上层另设 error）。

---

## 8. Realtime WebSocket 对照（非 SSE，但易混淆）

`relay/channel/openai/relay_realtime.go`：

- 会话中按上游 usage 事件 **增量** `PreWssConsumeQuota`；
- 客户端 `clientClosed` 后，若缓冲区还有未结算 usage，会再扣一次；
- 最终 `PostWssConsumeQuota`。

结论类似：**用户关连接不会自动免单**，已发生的 realtime usage 仍结算。

---

## 9. 与异步 Task 的差异（避免误判）

| | SSE 流式 Chat | 异步视频 Task |
|--|---------------|---------------|
| 客户端断开 | HTTP 请求 context cancel | 通常只是停止轮询 UI，任务仍在服务端 |
| 预扣 | 请求级 BillingSession | `ForcePreConsume` + `tasks.quota` |
| 完成结算 | 请求结束立刻 Settle | 轮询 SUCCESS 后 `RecalculateTaskQuota` |
| 失败退款 | 仅 `newAPIError` 时 Refund | 任务失败 `RefundTaskQuota` |

用户取消「页面上的生成中 SSE」≠ 取消「已提交的异步任务」。

---

## 10. 资金与落库结果（客户端中断成功结算时）

与正常完成相同骨架，只是 `actual` 可能更小，且 log 带 `client_gone`：

```text
1. 预扣：users.quota / tokens / subscription  -= preConsumed（或信任旁路为 0）
2. 流中断：StreamStatus=client_gone，关闭上游 body
3. 计算 actual（partial 或本地估算）
4. Settle：对 delta=actual-pre 调整余额与令牌
5. 若 TotalTokens>0：users.used_quota += actual，channels.used_quota += actual
6. INSERT logs（type=consume, quota=actual, other.stream_status.end_reason=client_gone）
```

**不会**因为用户没收到计费 SSE 就跳过步骤 4–6。

---

## 11. 产品与实现含义

### 11.1 当前策略可概括为

> **按网关侧可证实的消耗计费；用户是否完整接收响应不影响应付额度。**  
> 同时尽早切断上游，避免为无人监听的连接继续烧上游 token。

这与多数 AI 网关/云厂商习惯一致：输入已送达上游、输出已生成的部分通常仍计费；传输层取消不构成自动 credit。

### 11.2 已知偏差与风险

1. **本地 token 估算 vs 上游真实 usage**  
   中断时常用 `ResponseText2Usage`，可能与上游账单不一致（多/少都可能）。
2. **Prompt 仍可能全额计入**  
   即使用户几乎没看到输出。
3. **固定价模型**  
   一旦 TotalTokens>0，可能按次全额，对「取消」不友好。
4. **用户体验**  
   客户端看不到 usage，但余额减少，易被理解为「断线仍扣费 bug」——从代码看是**有意的成功结算路径**。
5. **上游是否真正停止**  
   关闭 `resp.Body` 是 best-effort；个别上游在 TCP 断开后仍可能短时间继续计费（超出本仓库控制）。

### 11.3 若产品要求「用户取消不扣费 / 只扣已展示」

需要**显式改代码**，当前不存在开关。可能方向（仅设计提示，非现状）：

| 策略 | 改动点 | 风险 |
|------|--------|------|
| client_gone 全额退预扣 | `OaiStreamHandler` 等在 client_gone 时返回 error，或 PostText 前判断 EndReason | 上游已产生成本无法回收；易被刷「生成完再断」 |
| client_gone 只扣已接收 completion，prompt 打折/免 | `PostTextConsumeQuota` 增加 stream end 策略 | 规则复杂，与上游账单更不一致 |
| 仅当 ReceivedResponseCount==0 时退款 | 较温和 | 已流出首 token 仍扣 |
| 把 client_gone 写入可对账字段并提供客服退款工具 | 运维向 | 不改自动策略 |

任何改动必须同时考虑：预扣 Settle、订阅幂等、信任旁路、本地估算标记（`ContextKeyLocalCountTokens`）、日志 `stream_status`。

---

## 12. 排障手册

### 12.1 如何确认一笔「用户说取消了还扣费」是否符合现状

1. 查 `logs.other.stream_status.end_reason` 是否为 `client_gone` / `timeout`。  
2. 看 `other` 是否含本地计 token 标记 / `admin_info.usage_billing_path`。  
3. 对比 `prompt_tokens`、`completion_tokens` 是否明显小于正常完整请求。  
4. 确认 `quota` 是 actual 而非「未退预扣残账」：中断后应只有一条 consume 结算，不应再依赖 defer Refund。  
5. 若 `BatchUpdateEnabled`，余额显示可能短暂延迟，以日志为准。

### 12.2 相关配置

| 配置 | 作用 | 与中断计费关系 |
|------|------|----------------|
| `STREAMING_TIMEOUT`（默认 300） | 上流空闲超时 | 超时同样会结算 partial |
| GeneralSetting PingInterval | SSE ping 保活 | ping 写失败可 stop，但通常仍结算 |
| `ForcePreConsume` | 异步任务强制预扣 | 流式 chat 默认 false，可走信任旁路 |

### 12.3 代码阅读入口（建议顺序）

1. `relay/helper/stream_scanner.go` — 断开与关上游  
2. `relay/channel/openai/relay-openai.go` — `OaiStreamHandler` 返回 usage,nil  
3. `relay/compatible_handler.go` — 成功即 `PostTextConsumeQuota`  
4. `controller/relay.go` — 仅 error 时 Refund  
5. `service/text_quota.go` — Settle 与 TotalTokens==0  
6. `service/log_info_generate.go` — `stream_status` 入库  

---

## 13. 结论表

| 断言 | 成立？ | 依据 |
|------|--------|------|
| 用户中断 SSE 后服务端仍可能扣费 | **是** | handler 返回 nil error + PostTextConsumeQuota |
| 用户没收到 usage chunk 就不扣费 | **否** | 写失败不阻断结算；usage 可本地估算 |
| 中断会全额退还预扣 | **否**（除非 actual 被算成 0） | Refund 只绑定 newAPIError；Settle(0) 才等价退光 |
| 中断后会尽量停止上游继续生成 | **是** | client_gone 时 close resp.Body |
| 日志可区分该类扣费 | **是** | `other.stream_status.end_reason=client_gone` |
| 所有协议行为完全一致 | **大致一致，细节有别** | Gemini 零 chunk 更易空 usage；固定价更易全额 |

**最终结论（直接回答调研问题）：**

> 当 SSE 流被用户端中断、计费/usage 内容未完整传到用户端时，**服务端默认仍然继续计费**：以网关已接收内容与可获得的 usage（或本地 token 估算）做 `SettleBilling`，并写入消费日志；**不会**仅因客户端未收到计费字段而自动免单。系统会尽快切断上游连接以限制损失，但已产生的可计量部分会落库扣费。

---

## 14. 附录：最小复现观察点

1. 对任意 stream 模型发起长输出请求。  
2. 输出若干 chunk 后客户端 abort（关闭 TCP / 取消 fetch）。  
3. 服务端日志应出现：`stream ended: reason=client_gone ...`。  
4. `logs` 中该 `request_id` 仍有 consume 记录，`other.stream_status.end_reason=client_gone`。  
5. 用户余额减少额约等于 partial actual（通常 ≥ 输入成本，≤ 完整生成费用）。  
6. 若开启 BillingDebug，可见 `settle_billing_start` / `text_quota_calculated` 仍执行。
