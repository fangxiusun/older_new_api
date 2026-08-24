# 扣费落库链路与模拟扣费接口设计

> 文档基于当前代码实现核对（以 `service/billing*.go`、`service/text_quota.go`、`service/task_billing.go`、`model/user.go`、`model/token.go`、`model/log.go`、`model/subscription.go`、`controller/relay.go` 为准）。
>
> 可与 `11-billing-design.md`、`13-billing-debug-log-plan.md` 配合阅读：本文聚焦「钱从哪扣、落到哪张表、如何模拟」。

---

## 1. 目标与范围

### 1.1 本文回答什么

1. 当前系统一次请求/任务的**扣费生命周期**是怎样的。
2. 预扣、结算、退款、统计分别**写哪些表 / 缓存**。
3. 如何实现一个**可模拟扣费**的管理接口：既能试算价格，也能按真实链路验证落库，且不污染生产数据。

### 1.2 不在本文范围

- 分层表达式语法细节（见 `pkg/billingexpr/expr.md`）。
- 各上游 Provider 的 usage 解析细节。
- 充值支付回调（`TopUp`）本身，只在「资金入账」对比时提及。

---

## 2. 核心概念（代码事实）

| 概念 | 代码位置 | 说明 |
|------|----------|------|
| **Quota** | `common/constants.go`：`QuotaPerUnit = 500000` | 内部额度单位；约等于 `$0.002 = 1 Quota` 的历史约定 |
| **预扣费 PreConsume** | `service.PreConsumeBilling` → `BillingSession.preConsume` | 请求前按估算额度冻结用户/订阅/令牌余额 |
| **结算 Settle** | `service.SettleBilling` → `BillingSession.Settle` | 按实际额度做 `delta = actual - preConsumed` 补扣或退还 |
| **退款 Refund** | `BillingSession.Refund` / `RefundTaskQuota` | 请求失败或任务失败时退还预扣 |
| **资金来源 FundingSource** | `service/funding_source.go` | `wallet` 或 `subscription` |
| **信任额度旁路** | `BillingSession.shouldTrust` / `common.GetTrustQuota()` | 钱包用户余额与令牌余额均 `> 10 * QuotaPerUnit` 时，预扣可变为 0；订阅强制不信任；异步任务 `ForcePreConsume=true` 强制全额预扣 |
| **批量写库** | `model/utils.go` + `BatchUpdateEnabled` | 用户额度 / used_quota / token / channel 可先入内存聚合，再周期刷库 |
| **消费日志** | `model.RecordConsumeLog` / `RecordTaskBillingLog` | 写入 `LOG_DB.logs`（可与主库分离） |

---

## 3. 扣费生命周期总览

系统存在三条主链路，落库点不同，但资金调整复用同一套 `Decrease*/Increase*` / 订阅 delta 原语。

```text
A. 同步 Relay（chat/completions、Claude、Gemini、embedding、audio、image token 计费等）
   controller/relay.go
     → helper.ModelPriceHelper          // 估算 QuotaToPreConsume
     → service.PreConsumeBilling        // 建会话 + 预扣
     → relay/*Helper                    // 上游调用
     → service.PostTextConsumeQuota     // 算实际额度 + Settle + 记 used + 写 logs
     → [失败] BillingSession.Refund

B. 按次计费（部分 MJ / 任务提交时的固定价）
   ModelPriceHelperPerCall / PriceData.Quota
     → PreConsumeBilling 或直接 PostConsumeQuota
     → RecordConsumeLog / RecordTaskBillingLog

C. 异步任务（视频等）
   relay/relay_task.go
     → PreConsumeBilling(ForcePreConsume=true)
     → task.Insert（持久化 task.Quota + BillingContext）
     → 轮询成功：RecalculateTaskQuota / RecalculateTaskQuotaByTokens
     → 轮询失败：RefundTaskQuota
```

### 3.1 同步 Relay 时序（落库视角）

```text
[1 估价]
  EstimateRequestToken → ModelPriceHelper
  产出 PriceData.QuotaToPreConsume（可能含 tiered 预估）

[2 预扣 PreConsumeBilling]
  NewBillingSession（按用户计费偏好选 wallet/subscription，可能回退）
  preConsume:
    a) 可选信任旁路 → effectiveQuota=0
    b) PreConsumeTokenQuota → DecreaseTokenQuota
    c) funding.PreConsume
         wallet        → DecreaseUserQuota(userId, amount, db=false)
         subscription  → PreConsumeUserSubscription(requestId, ...)
  同步 RelayInfo.FinalPreConsumedQuota / BillingSource / Subscription*

[3 上游成功后结算 PostTextConsumeQuota]
  calculateTextQuotaSummary / TryTieredSettle → summary.Quota
  UpdateUserUsedQuotaAndRequestCount(userId, summary.Quota)   // used_quota + request_count
  UpdateChannelUsedQuota(channelId, summary.Quota)            // channel.used_quota
  SettleBilling(actual=summary.Quota)
    → funding.Settle(delta)
    → Decrease/IncreaseTokenQuota(delta)
  RecordConsumeLog(...)                                       // logs 表
  [可选] LogQuotaData                                         // 导出统计

[4 上游/本地失败]
  defer: Billing.Refund
    → funding.Refund + IncreaseTokenQuota(tokenConsumed)
  可能额外 ChargeViolationFeeIfNeeded（违规费走 PostConsumeQuota + RecordConsumeLog）
```

### 3.2 异步任务时序（落库视角）

```text
提交:
  ForcePreConsume=true（禁用信任旁路）
  PreConsumeBilling → 资金/令牌已扣
  task.Quota = 预扣额度
  task.PrivateData 持久化 BillingSource / SubscriptionId / TokenId / BillingContext / NodeName
  task.Insert()

成功完成:
  adaptor.AdjustBillingOnComplete 或 RecalculateTaskQuotaByTokens
  RecalculateTaskQuota:
    taskAdjustFunding(delta)
    taskAdjustTokenQuota(delta)
    task.UpdateQuota(actual)
    UpdateUserUsedQuotaAndRequestCount / UpdateChannelUsedQuota（仅 delta>0）
    RecordTaskBillingLog（Consume 或 Refund）

失败:
  RefundTaskQuota:
    taskAdjustFunding(-quota)
    taskAdjustTokenQuota(-quota)
    RecordTaskBillingLog(LogTypeRefund)
    task.Quota=0 并 UpdateQuota（失败则保留非零 quota 供对账重试）
```

---

## 4. 资金来源与会话状态机

### 4.1 FundingSource 接口

```go
// service/funding_source.go
type FundingSource interface {
    Source() string          // "wallet" | "subscription"
    PreConsume(amount int) error
    Settle(delta int) error  // >0 补扣, <0 退还
    Refund() error
}
```

| 实现 | PreConsume | Settle | Refund |
|------|------------|--------|--------|
| `WalletFunding` | `DecreaseUserQuota(id, amount, false)`，记录 `consumed` | `Decrease/IncreaseUserQuota` | `IncreaseUserQuota(consumed)`（**非幂等，不可重试**） |
| `SubscriptionFunding` | `PreConsumeUserSubscription(requestId, ...)`（内部用构造时 amount；参数 amount 被忽略） | `PostConsumeUserSubscriptionDelta(subId, delta)` | `RefundSubscriptionPreConsume(requestId)`（**有 requestId 幂等，可重试**） |

### 4.2 BillingSession 关键字段

| 字段 | 含义 |
|------|------|
| `preConsumedQuota` | 实际预扣额度（信任旁路可为 0） |
| `tokenConsumed` | 令牌侧实际扣减量 |
| `extraReserved` | 发送前补充预扣（订阅回滚时单独处理） |
| `trusted` | 是否信任旁路 |
| `fundingSettled` | 资金侧 Settle 已成功；此后禁止 Refund 资金，避免双退 |
| `settled` / `refunded` | 终态保护 |

### 4.3 计费偏好选择（`NewBillingSession`）

| preference | 行为 |
|------------|------|
| `subscription_only` | 仅订阅 |
| `wallet_only` | 仅钱包 |
| `wallet_first` | 钱包不足（`ErrorCodeInsufficientUserQuota`）回退订阅 |
| `subscription_first`（默认） | 无活跃订阅 → 钱包；订阅不足且允许 overflow → 钱包，否则报错 |

---

## 5. 扣费落库明细（最重要）

### 5.1 落库总表

| 步骤 | 调用 | 主库表 | 日志库 | 缓存 | 备注 |
|------|------|--------|--------|------|------|
| 令牌预扣 | `DecreaseTokenQuota` | `tokens.remain_quota` / `used_quota` | - | token 缓存 | 可 batch |
| 钱包预扣 | `DecreaseUserQuota(..., false)` | `users.quota` | - | `cacheDecrUserQuota` | 可 batch |
| 订阅预扣 | `PreConsumeUserSubscription` | `user_subscriptions.amount_used` + `subscription_pre_consume_records` | - | 计划信息缓存 | 事务 + requestId 幂等 |
| 差额资金 | `funding.Settle` | 同上 users 或 user_subscriptions | - | 同步 cache | |
| 差额令牌 | `Decrease/IncreaseTokenQuota` | `tokens` | - | token 缓存 | |
| 用户统计 | `UpdateUserUsedQuotaAndRequestCount` | `users.used_quota`, `request_count` | - | - | 可 batch；**与余额扣减分离** |
| 渠道统计 | `UpdateChannelUsedQuota` | `channels.used_quota` | - | - | 可 batch |
| 消费日志 | `RecordConsumeLog` | - | `logs`（`LOG_DB`） | - | `LogConsumeEnabled` 可关 |
| 任务日志 | `RecordTaskBillingLog` | - | `logs` | - | 成功补扣/退款都写 |
| 导出聚合 | `LogQuotaData` | 导出相关表/结构 | - | - | `DataExportEnabled` |
| 任务快照 | `task.Insert/UpdateQuota` | `tasks.quota` + `private_data` | - | - | 异步对账锚点 |
| 失败退款 | `Refund` / `RefundTaskQuota` | users/tokens/subscription + 可能 logs(Refund) | 可能 | cache | 钱包退款不可盲目重试 |

> **关键不变量**：`users.quota`（余额）与 `users.used_quota`（累计消费）是两条独立写入路径。  
> Settle 只动余额；`UpdateUserUsedQuotaAndRequestCount` 记统计。预扣阶段**不**增加 `used_quota`。

### 5.2 用户余额写路径

```text
IncreaseUserQuota / DecreaseUserQuota (model/user.go)
  1) 异步 cacheIncr/cacheDecrUserQuota（Redis 若启用）
  2) 若 db=false 且 BatchUpdateEnabled:
       addNewRecord(BatchUpdateTypeUserQuota, id, ±quota)  // 内存聚合
     否则:
       UPDATE users SET quota = quota ± ? WHERE id = ?
```

- 业务扣费路径普遍传 `db=false`，依赖批量更新降低写放大。
- 管理端 `ManageUser add_quota` 传 `db=true`，**立即落库**。

### 5.3 令牌额度写路径

```text
PreConsumeTokenQuota
  → 校验 remain_quota（非 unlimited）
  → DecreaseTokenQuota(tokenId, key, quota)

Decrease/IncreaseTokenQuota
  → 可选 BatchUpdateTypeTokenQuota
  → 最终 increaseTokenQuota（内部用 gorm.Expr 加减）
  → 维护 token key 缓存
```

Playground 请求跳过令牌扣减。

### 5.4 订阅额度写路径

```text
PreConsumeUserSubscription(requestId, userId, modelName, quotaType, amount)
  - 以 requestId 查/建 SubscriptionPreConsumeRecord（幂等）
  - 锁定可用 user_subscription，增加 amount_used
  - 返回 UserSubscriptionId / PreConsumed / AmountTotal / AmountUsedAfter

PostConsumeUserSubscriptionDelta(subId, delta)
  - amount_used += delta（可正可负）

RefundSubscriptionPreConsume(requestId)
  - 按预扣记录回滚，幂等
```

### 5.5 消费日志落库

`RecordConsumeLog`（同步链路）：

| 字段 | 来源 |
|------|------|
| `user_id` / `username` | 参数 + context |
| `type` | `LogTypeConsume` |
| `quota` | **实际消费额度**（不是 delta） |
| `prompt_tokens` / `completion_tokens` | usage |
| `model_name` / `token_name` / `token_id` / `channel_id` / `group` | relay 上下文 |
| `request_id` / `upstream_request_id` | context |
| `other` | JSON：倍率、价格、缓存、tiered、admin_info（含 quota_saturation / usage_billing_path）等 |
| `ip` | 用户设置 `RecordIpLog` 时才写 |

写入：`createLog` → `LOG_DB.Create(log)`。  
`LOG_DB` 可与主 `DB` 相同，也可独立日志库（含 ClickHouse 分支）。

任务侧 `RecordTaskBillingLog`：

- 补扣：`LogTypeConsume`，`quota = delta`
- 退款：`LogTypeRefund`，`quota = |delta|` 或全额预扣
- other 带 `task_id`、`pre_consumed_quota`、`actual_quota` 等

### 5.6 批量更新（Batch Update）

`model/utils.go`：

| Type | 聚合内容 | 刷库动作 |
|------|----------|----------|
| `UserQuota` | ±quota | 与 used/request 合并写用户 |
| `UsedQuota` | +quota | 同上 |
| `RequestCount` | +1 | 同上 |
| `TokenQuota` | ±quota | `increaseTokenQuota` |
| `ChannelUsedQuota` | +quota | `updateChannelUsedQuota` |

周期：`BatchUpdateInterval` 秒。  
含义：模拟扣费若只读主库立刻查询，可能看到**尚未刷盘**的中间态；验证落库需关闭 batch 或主动触发/等待刷盘。

### 5.7 与「管理改额」的区别

| 路径 | 是否走 BillingSession | 是否写 consume log | 是否动 used_quota |
|------|----------------------|--------------------|-------------------|
| Relay 真实扣费 | 是 | 是（通常） | 是（成功结算后） |
| `ManageUser add_quota` | 否 | 审计 log，非消费 log | 否 |
| 渠道测试 `channel-test` | 部分直接 `RecordConsumeLog`，**不完整复用生产会话** | 是 | 视实现 |
| 违规费 `ChargeViolationFeeIfNeeded` | `PostConsumeQuota` 旧路径 | 是 | 是 |

---

## 6. 额度计算公式（结算输入）

模拟接口必须复用下列计算，而不是重新发明公式。

### 6.1 预扣估算

- 入口：`relay/helper.ModelPriceHelper` / `ModelPriceHelperPerCall`
- 输出：`PriceData.QuotaToPreConsume` 或 `PriceData.Quota`
- 模式：
  - **倍率**：`(prompt + estimatedCompletion) * modelRatio * groupRatio * otherRatios`
  - **固定价**：`modelPrice * QuotaPerUnit * groupRatio * otherRatios`
  - **tiered_expr**：表达式估算后再 `/1e6 * QuotaPerUnit * groupRatio`
- 安全：一律经 `common.QuotaFromFloat*` / `QuotaFromFloatStrict`，禁止裸 `int(float64)`。

### 6.2 文本实际结算

- 入口：`calculateTextQuotaSummary` + 可选 `TryTieredSettle`
- 再叠加 tool surcharge（web_search / file_search / image_generation_call 等）
- `summary.TotalTokens == 0` 时：仍会 `SettleBilling`，但**跳过** used_quota/channel used 更新，并在日志 content 标注上游无计费信息（预扣可能在 Settle 中被退回，取决于 actualQuota）

### 6.3 任务实际结算

- 固定价/适配器调整：`RecalculateTaskQuota(actualQuota, reason)`
- token 重算：`totalTokens * modelRatio * groupRatio * otherMultiplier`（仅配置了倍率时）

---

## 7. 模拟扣费接口设计

### 7.1 设计目标

1. **不改动生产扣费热路径语义**；模拟逻辑调用现有纯函数/service，避免复制公式。
2. 支持三种模式，满足「只看价格」与「验证落库」两种诉求。
3. 默认安全：只允许管理员；真实写库模式需显式确认 + 审计。
4. 输出与真实 `logs.other` / BillingDebug 字段对齐，便于对照线上问题。
5. 兼容 SQLite / MySQL / PostgreSQL；不引入新的数据库专有语法。

### 7.2 模式定义

| mode | 是否扣余额 | 是否写 logs | 是否更新 used_quota/channel | 用途 |
|------|------------|-------------|------------------------------|------|
| `estimate` | 否 | 否 | 否 | 纯试算：预扣估值 + 给定 usage 的应付额度 |
| `dry_run` | 否 | 否 | 否 | 完整走会话状态机**内存模拟**（含 trust/funding 选择），但 FundingSource 换成 Noop 或事务回滚 |
| `commit` | 是 | 是（可标记） | 是 | 真实落库模拟；仅测试用户/测试令牌；强制写 `other.simulate=true` |

**推荐默认：`estimate`。**  
`commit` 必须额外 `confirm=true`，并限制目标用户（建议仅 `role=普通用户` 且用户名/标签匹配测试账号策略）。

### 7.3 API 形态

```http
POST /api/debug/billing/simulate
Authorization: Admin / Root
Content-Type: application/json
```

#### Request

```json
{
  "mode": "estimate",
  "confirm": false,
  "scene": "text",
  "user_id": 1001,
  "token_id": 2002,
  "group": "default",
  "model_name": "gpt-4o",
  "channel_id": 0,
  "billing_preference": "",
  "force_pre_consume": false,
  "disable_trust": true,
  "request_format": "openai",
  "prompt_tokens": 1200,
  "estimated_completion_tokens": 800,
  "usage": {
    "prompt_tokens": 1100,
    "completion_tokens": 640,
    "cache_tokens": 200,
    "cache_creation_tokens": 0,
    "cache_creation_tokens_5m": 0,
    "cache_creation_tokens_1h": 0,
    "image_tokens": 0,
    "audio_tokens": 0,
    "usage_semantic": "openai"
  },
  "per_call": false,
  "model_price_override": null,
  "other_ratios": {
    "seconds": 5,
    "resolution": 1.0
  },
  "tool_calls": {
    "web_search": 0,
    "file_search": 0,
    "image_generation": 0
  },
  "write_log": true,
  "log_content_prefix": "[simulate]",
  "request_id": ""
}
```

字段说明：

| 字段 | 必填 | 说明 |
|------|------|------|
| `mode` | 是 | `estimate` / `dry_run` / `commit` |
| `scene` | 是 | `text` / `per_call` / `task` |
| `user_id` | 条件 | `dry_run/commit` 必填；`estimate` 可只给 group |
| `token_id` | 条件 | `commit` 建议必填，以验证令牌扣减 |
| `model_name` | 是 | 用于取倍率/价格/表达式 |
| `usage` | 条件 | 结算试算需要；只估预扣时可省略 |
| `disable_trust` | 否 | 默认 true，避免高余额用户测不出预扣 |
| `force_pre_consume` | 否 | 模拟异步任务时 true |
| `confirm` | `commit` 必填 true | 防误触 |

#### Response（建议结构）

```json
{
  "success": true,
  "message": "",
  "data": {
    "mode": "estimate",
    "scene": "text",
    "request_id": "sim-...",
    "pricing": {
      "billing_mode": "ratio",
      "use_price": false,
      "free_model": false,
      "model_ratio": 1.25,
      "completion_ratio": 4.0,
      "group_ratio": 1.0,
      "cache_ratio": 0.5,
      "model_price": 0,
      "other_ratios": {},
      "quota_per_unit": 500000,
      "quota_to_pre_consume": 3500,
      "actual_quota": 4200,
      "delta": 700,
      "tiered": null
    },
    "funding": {
      "preference": "subscription_first",
      "selected_source": "wallet",
      "trusted": false,
      "would_pre_consume": 3500,
      "subscription_id": 0
    },
    "persist_plan": [
      {
        "step": "token_pre_consume",
        "table": "tokens",
        "op": "remain_quota -= 3500",
        "applied": false
      },
      {
        "step": "wallet_pre_consume",
        "table": "users",
        "op": "quota -= 3500",
        "applied": false
      },
      {
        "step": "settle_wallet_delta",
        "table": "users",
        "op": "quota -= 700",
        "applied": false
      },
      {
        "step": "settle_token_delta",
        "table": "tokens",
        "op": "remain_quota -= 700",
        "applied": false
      },
      {
        "step": "user_used_quota",
        "table": "users",
        "op": "used_quota += 4200; request_count += 1",
        "applied": false
      },
      {
        "step": "channel_used_quota",
        "table": "channels",
        "op": "used_quota += 4200",
        "applied": false
      },
      {
        "step": "consume_log",
        "table": "logs",
        "op": "insert type=consume quota=4200",
        "applied": false
      }
    ],
    "balances_before": {
      "user_quota": 1000000,
      "token_remain_quota": 500000,
      "token_unlimited": false
    },
    "balances_after": null,
    "log_other_preview": {
      "model_ratio": 1.25,
      "group_ratio": 1.0,
      "completion_ratio": 4.0,
      "simulate": true,
      "simulate_mode": "estimate"
    },
    "warnings": [
      "BatchUpdateEnabled=true: commit 后短时间主库可能尚未看到额度变化"
    ]
  }
}
```

### 7.4 服务分层设计

建议新增（命名可按项目风格微调）：

```text
controller/billing_simulate.go
  → SimulateBilling(c)
service/billing_simulate.go
  → BuildSimulateRelayInfo(...)
  → EstimateSimulateQuota(...)          // 复用 ModelPriceHelper 计算思路 / calculateTextQuotaSummary
  → PlanPersistSteps(...)               // 只生成落库计划，不执行
  → ExecuteSimulateCommit(...)          // mode=commit
  → NewSimulateFundingSource(...)       // dry_run 用
router/api-router.go
  → /api/debug/billing/simulate  (AdminAuth)
```

**禁止**在 controller 内复制倍率公式；计算必须进入现有：

- 预扣：抽取 `helper.ModelPriceHelper` 所需的最小 `RelayInfo` 装配，或新增 `service.EstimatePreConsumeQuota(input) (PriceData, error)` 把「无 gin 上下文」的估价能力收口。
- 结算：优先调用 `calculateTextQuotaSummary`（若不可导出，则同包新增 `SimulateTextQuotaSummary` 包装，或把纯计算下沉到可测函数）。
- tiered：直接 `TryTieredSettle`。
- 工具费：复用 `tool_billing` / text surcharge 路径。

### 7.5 `estimate` 实现要点

1. 装配轻量 `RelayInfo`：
   - `UserId/TokenId/UsingGroup/OriginModelName/PriceData/TieredBillingSnapshot/BillingRequestInput`
2. 用给定 `prompt_tokens` + 可选 meta 计算 `QuotaToPreConsume`。
3. 若提供 `usage`，计算 `actual_quota` 与 `delta`。
4. 读取用户计费偏好，**只预测**会选 wallet 还是 subscription（可调用与 `NewBillingSession` 相同的分支函数；建议把偏好选择抽成纯函数 `ResolveFundingPreference(...)` 以便单测）。
5. 生成 `persist_plan`（全部 `applied=false`）。
6. 不开启 DB 写；不对 token/user 加锁。

### 7.6 `dry_run` 实现要点

目标：验证会话状态机，而不是验证 SQL。

可选两种实现，**推荐 A**：

**A. Noop Funding + 内存 TokenLedger**

```go
type NoopWalletFunding struct{ WouldConsume, WouldSettleDelta int }
// PreConsume/Settle/Refund 只记录，不改库

type MemoryTokenLedger struct{ Remain int; Unlimited bool }
```

跑完整：

```text
PreConsumeBilling 等价流程 → SettleBilling 等价流程 → 假设失败再 Refund
```

输出 before/after **模拟余额**。

**B. 真实 DB 事务回滚**

- 仅 SQLite/MySQL/PostgreSQL 均支持的短事务；
- 订阅预扣带 requestId 副作用与异步 cache 更新，**回滚很难做干净**（Redis cache、batch store、gopool 异步）。
- 因此不推荐作为默认。

### 7.7 `commit` 实现要点（真实模拟落库）

仅用于受控测试环境。

```text
1. 校验 admin + confirm + 目标用户白名单/测试标记
2. disable_trust 默认 true；或显式 ForcePreConsume
3. 生成 request_id = "sim-" + uuid（写入 context，保证订阅幂等键不与真实流量冲突）
4. 走真实:
     PreConsumeBilling
     SettleBilling(actual)
     UpdateUserUsedQuotaAndRequestCount
     UpdateChannelUsedQuota（若 channel_id>0）
     RecordConsumeLog（other.simulate=true, simulate_mode=commit, operator_id=...）
5. 返回 balances_before/after（after 在 BatchUpdateEnabled 时提示延迟）
6. recordManageAuditFor(action="billing.simulate_commit", ...)
```

**强烈建议的安全阀：**

- 配置项 `billing_simulate.commit_enabled` 默认 false；或仅 `GIN_MODE != release` 且环境变量 `BILLING_SIMULATE_COMMIT=1`。
- 单次 `actual_quota` 上限（例如不超过 `GetTrustQuota()` 或可配 `MaxSimulateQuota`）。
- 拒绝 root 用户、管理员用户作为扣费目标。
- 可选 `auto_refund=true`：commit 成功后立刻按 actual 全额退回并写 `LogTypeRefund`，实现「走真实写路径但净额为 0」；仍会留下 logs/used_quota 痕迹，需文档说明。

### 7.8 与现有调试接口的关系

已有：

```text
GET/POST /api/debug/billing   # 开关 BillingDebug 日志
```

本设计新增：

```text
POST /api/debug/billing/simulate
GET  /api/debug/billing/simulate/modes   # 可选：返回支持的 scene/mode/安全阀状态
```

二者正交：simulate 负责结构化试算/落库验证；billing debug 负责热路径日志。

### 7.9 任务场景（scene=task）扩展

Request 增加：

```json
{
  "scene": "task",
  "task": {
    "pre_consumed_quota": 10000,
    "actual_quota": 12000,
    "total_tokens": 0,
    "reason": "simulate adaptor adjust"
  }
}
```

`estimate/dry_run`：只演示 `RecalculateTaskQuota` 的 delta 与 `persist_plan`（含 tasks.quota 回写、RecordTaskBillingLog）。  
`commit`：可创建临时 task 行或要求传入已有 `task_id`；**首版可只支持 text/per_call，task 二期再做**，避免与轮询器竞态。

---

## 8. 推荐实现分期

### Phase 1（低风险，优先）

- `POST /api/debug/billing/simulate` 仅 `mode=estimate`
- 支持 `scene=text|per_call`
- 返回 pricing + persist_plan + log_other_preview
- 单测：固定 ratio/price/tiered 三组表驱动，断言 actual_quota

### Phase 2

- `mode=dry_run` + Noop Funding/Memory Token
- 覆盖 trust 旁路、subscription_first 回退、delta=0/正/负

### Phase 3

- `mode=commit`（默认关闭）+ 审计 + 额度上限 + 可选 auto_refund
- 文档与运行手册：如何在 staging 验证 batch 刷盘

### Phase 4

- `scene=task` 与失败退款模拟
- 前端「计费调试台」：输入 usage，展示落库计划与美元换算

---

## 9. 代码落点清单

| 文件 | 变更 |
|------|------|
| `router/api-router.go` | 注册 debug 路由 |
| `controller/billing_simulate.go` | 参数校验、权限、响应 |
| `service/billing_simulate.go` | 核心编排 |
| `service/billing_session.go`（可选） | 抽出 `ResolveFundingPreference` / 信任判断便于复用 |
| `relay/helper/price.go` 或 `service/pricing_estimate.go` | 无完整请求体时的估价入口 |
| `model/log.go`（可选） | 无需改表；靠 `other.simulate` 标记 |
| `docs/llm-docs/11-billing-design.md` | 增加指向本文的链接（可选） |
| 测试 | `service/billing_simulate_test.go` |

**不需要**新增 DB migration：模拟标记放 `logs.other` JSON 即可。

---

## 10. 测试与验收

### 10.1 单测（必做）

| 用例 | 期望 |
|------|------|
| ratio 文本：p=1000,c=500,cr=0 | actual 与 `calculateTextQuotaSummary` 一致 |
| use_price 固定价 | `price * QuotaPerUnit * group` |
| tiered_expr | 与 `TryTieredSettle` 一致 |
| cache / image / audio 分项 | 与生产 summary 一致 |
| estimate 永不改 `users.quota` | DB 前后快照相等 |
| persist_plan 步骤顺序 | 与第 5 章落库顺序一致 |

### 10.2 集成验收（staging）

1. `estimate`：同一模型对真实一次 chat 的 log.quota，误差为 0（同 usage）。  
2. `dry_run`：高余额用户 `disable_trust=false` 时 `would_pre_consume=0`。  
3. `commit`（若开启）：  
   - `users.quota` 最终减少 `actual_quota`（等待 batch 或关 batch）；  
   - `users.used_quota` + `actual_quota`；  
   - `tokens.remain_quota` 同步减少；  
   - `logs` 出现 `other.simulate=true`；  
   - 审计日志可查操作者。

### 10.3 回归红线

- 不得让模拟接口在默认配置下写生产余额。
- 不得在业务 `encoding/json` 直调；controller 用 `common.DecodeJson`。
- quota 转换必须走 `common.QuotaFromFloat*`。
- 钱包 `IncreaseUserQuota` 退款路径不得在模拟重试逻辑里循环调用。

---

## 11. 风险与对策

| 风险 | 对策 |
|------|------|
| 复制公式导致与生产漂移 | 只包装现有函数，单测对照 |
| commit 误扣真实用户 | 默认关闭 + confirm + 白名单 + 上限 |
| BatchUpdate 造成「没落库」误判 | 响应 warnings；文档说明；测试关 batch |
| 订阅幂等 requestId 冲突 | 强制 `sim-` 前缀 |
| 异步 cache 与 DB 不一致 | commit 后可选 `CacheUpdateUserQuota`；或只在 staging 关 Redis 验主库 |
| 与 channel-test 计费分叉 | 长期应让 channel-test 也复用同一估价/结算函数 |

---

## 12. 附录：一次成功文本请求的最小落库序列

假设非信任用户、钱包计费、`pre=3000`、`actual=4200`、非 Playground、`BatchUpdateEnabled=false`：

```text
1. UPDATE tokens  SET remain_quota = remain_quota - 3000, used_quota = used_quota + 3000
2. UPDATE users   SET quota = quota - 3000
   ---------- 上游成功 ----------
3. UPDATE users   SET used_quota = used_quota + 4200, request_count = request_count + 1
4. UPDATE channels SET used_quota = used_quota + 4200
5. UPDATE users   SET quota = quota - 1200                 -- Settle delta
6. UPDATE tokens  SET remain_quota = remain_quota - 1200  -- Settle delta
7. INSERT logs (type=consume, quota=4200, prompt/completion, other=...)
```

若 `actual=2500`（低于预扣）：

```text
5'. UPDATE users  SET quota = quota + 500
6'. UPDATE tokens SET remain_quota = remain_quota + 500
7'. INSERT logs (quota=2500)   -- 日志记实际，不是 delta
```

若请求失败：

```text
R1. UPDATE users  SET quota = quota + 3000      -- funding.Refund
R2. UPDATE tokens SET remain_quota = remain_quota + 3000
-- 通常无 consume log；除非违规费另行 PostConsumeQuota
```

---

## 13. 结论

当前扣费不是「单点 UPDATE」，而是：

1. **会话化资金冻结**（钱包/订阅 + 令牌）；  
2. **结算只写余额差额**；  
3. **统计 used_quota 与消费 logs 另路径写入**；  
4. **异步任务把预扣快照落在 tasks 上，完成后再对账**。

模拟扣费接口应以 `estimate → dry_run → commit` 三档渐进实现：先保证与生产公式一致的可观测试算，再提供无副作用状态机验证，最后在强安全阀下做真实落库演练。这样既能支撑排障与定价验收，又不会破坏现有计费不变量。
