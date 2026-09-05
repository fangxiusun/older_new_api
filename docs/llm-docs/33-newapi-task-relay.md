# 33. New API 渠道 Task Relay 实现（relay/channel/task/newapi）

> 状态：已实现
> 相关代码：`relay/channel/task/newapi/`、`relay/relay_adaptor.go`
> 参考文档：`18-video-api-implementation-analysis.md`、`19-kling-jimeng-design.md`、`11-billing-design.md`

## 1. 背景与设计思路

`ChannelTypeNewAPI`（值 `60`）表示上游也是一个 new-api 实例。此前该渠道类型只支持普通聊天中转（`relay/channel/newapi/adaptor.go`，聊天侧为近透传实现）；视频类异步任务（video task）没有对应的 TaskAdaptor，`GetTaskAdaptor` 中无 channel 60 的分支，导致 New API 渠道无法承载 `/v1/videos`、`/v1/video/generations` 等任务请求。

本实现的关键观察是：**上游 new-api 对外暴露与本项目完全一致的任务端点**（`router/video-router.go`）：

- `POST /v1/videos`、`POST /v1/videos/:id/remix`（OpenAI Sora 格式，支持 JSON 与 multipart）
- `POST /v1/video/generations`（统一/标准化格式）
- `GET /v1/videos/:task_id`（OpenAI 格式查询）

同时，原生格式请求（`/kling/...`、`/jimeng/...`、`/ali/...`）在进入 task relay 之前已被对应转换中间件（`middleware/kling_adapter.go` 等）统一改写为 `/v1/video/generations` 格式。因此 newapi 任务适配器可以做成**近透传**：请求侧原样转发（仅替换 model 应用模型映射），响应侧解析上游返回的 OpenAI video 对象并回写本站公开任务 ID。

## 2. 支持端点

| 客户端路径 | 方法 | 上游路径 | 说明 |
| --- | --- | --- | --- |
| `/v1/videos` | POST | `{base}/v1/videos` | Sora 格式提交（JSON/multipart） |
| `/v1/videos/:video_id/remix` | POST | `{base}/v1/videos/{OriginTaskID}/remix` | Remix，绑定原任务渠道 |
| `/v1/video/generations` | POST | `{base}/v1/video/generations` | 统一格式提交（含原生格式经中间件改写后） |
| `/v1/videos/:task_id` | GET | —（DB 查询 + `ConvertToOpenAIVideo`） | OpenAI 格式查询，不回源 |
| `/v1/video/generations/:task_id` | GET | —（通用 `TaskResponse` 路径） | 通用格式查询，不回源 |

无需新增路由或中间件：所有路由已存在于 `router/video-router.go`，平台按「渠道类型即平台」约定解析为 `"60"`（`relay/relay_adaptor.go` `GetTaskPlatform`）。

## 3. 适配器实现（relay/channel/task/newapi/adaptor.go）

结构体嵌入 `taskcommon.BaseBilling`（无 submit 修正、无完成修正），字段：`ChannelType`、`apiKey`、`baseURL`（`Init` 从 RelayInfo 读取）。

### 3.1 请求校验 `ValidateRequestAndSetAction`

按请求分派：

- remix（`info.Action == TaskActionRemix`）：解析 `TaskSubmitReq`，要求 prompt 非空；
- `/v1/videos` 前缀（Sora 格式提交）：`relaycommon.ValidateMultipartDirect`（支持 JSON 与 multipart，要求 model 字段）；
- 其余（统一格式，含原生格式改写后）：`relaycommon.ValidateBasicTaskRequest(c, info, TaskActionGenerate)`，解析结果存入 gin context `task_request`。

### 3.2 计费估算 `EstimateBilling`（仿照 sora，倍率可配置）

从 `task_request` 读取：

- `seconds`：`req.Seconds`（字符串）优先，其次 `req.Duration`；两者均无/非法时默认 4。越界值由校验层（`relay/helper/valid_request.go` 的 max-tokens/时长上限与 `ValidateBasicTaskRequest` 的 duration 校验）拦截。
- `size`：默认 `720x1280`。
- 两者经 `ProcessNewAPIOtherRatios(model, size, seconds)` 组装为 `{"seconds": <n>, "size": <ratio>}`，经 `info.PriceData.AddOtherRatio` 合并（非正数/NaN/Inf 被守卫拒绝）。
- **size 倍率表**：内置在适配器包内（`ratios.go` 的 `videoSizeRatio`，结构与 ali 渠道的 `aliRatios` 同构），按厂商分组给出常用模型示例倍率（sora/kling/jimeng/vidu/hailuo/seedance/wan/happyhorse）；查找顺序为模型精确匹配 → 最长前缀匹配 → 组内 `"*"` 通配 → 全局 `"*"` 条目 → 缺省 1。修改倍率需调整该表并重新编译（与 ali 渠道做法一致）。
- remix 返回 nil（倍率已在 `ResolveOriginTask` 中设置）。

### 3.3 请求构建

- `BuildRequestURL`：`baseURL + info.RequestURLPath`（remix 特例拼接 `OriginTaskID`），即路径透传，与聊天侧 `relay/channel/newapi/adaptor.go` 的 `GetFullRequestURL` 方式一致。
- `BuildRequestHeader`：`Authorization: Bearer <key>`，透传客户端 `Content-Type`。
- `BuildRequestBody`（与 sora 相同的三分支）：
  1. JSON：`common.Unmarshal` → 替换 `model` 为 `info.UpstreamModelName` → `common.Marshal`；
  2. multipart：重建表单，替换 `model` 字段，文件重嗅探 Content-Type；
  3. 其他：`common.NewReplayableBodyReader` 原样透传。
- `DoRequest`：`channel.DoTaskApiRequest`（带代理支持）。

### 3.4 响应处理 `DoResponse`

上游（同为 new-api）返回 OpenAI video 对象：

1. 解析 `responseTask`（兼容 `id` 与旧接口 `task_id` 字段）；
2. 上游 ID 作为 upstreamTaskID 返回（存入 `Task.PrivateData.UpstreamTaskID`）；
3. 回写 `info.PublicTaskID`（本站 `task_xxxx` 公开 ID）后 `c.JSON(200, ...)` 返回客户端；
4. 解析失败或 ID 为空 → `taskdto.TaskError`（提交失败触发退款）。

### 3.5 轮询 `FetchTask` / `ParseTaskResult`

- `FetchTask`：`GET {base}/v1/videos/{upstreamTaskID}`，Bearer 鉴权，`service.GetHttpClientWithProxy(proxy)`。
- `ParseTaskResult` 状态映射（fallback 用；后台轮询 `service/task_polling.go` 会优先按 new-api 的 `TaskResponse[model.Task]` 信封解析）：

| 上游 status | TaskStatus |
| --- | --- |
| `queued` / `pending` | `QUEUED` |
| `submitted` | `SUBMITTED` |
| `processing` / `in_progress` | `IN_PROGRESS` |
| `completed` | `SUCCESS` |
| `failed` / `cancelled` | `FAILURE`（Reason 取 `error.message`，缺省 "task failed"） |
| 其他 | 不变更（保持当前状态） |

- `ConvertToOpenAIVideo`：对存储的原始上游响应 `task.Data` 用 sjson 将 `id` 替换为本站公开任务 ID（供 `GET /v1/videos/:task_id` 返回）。

## 4. 注册点

`relay/relay_adaptor.go` `GetTaskAdaptor`：

```go
case constant.ChannelTypeNewAPI:
    return &tasknewapi.TaskAdaptor{}
```

平台标识为 `"60"`（渠道类型十进制字符串），任务记录 `Task.Platform = "60"`。

## 5. 财务结算链路

提交链（`relay/relay_task.go` `RelayTaskSubmit`）：

1. 校验 → 模型名（`OriginModelName`，缺失时合成 `60_generate`）→ `ModelMappedHelper` 模型映射；
2. 预生成 `info.PublicTaskID`；
3. 基础价：`helper.ModelPriceHelperPerCall`（按次模型价 × QuotaPerUnit × 分组倍率；倍率型模型则按比例预扣）；
4. `EstimateBilling` 返回 seconds/size OtherRatios，经 `AddOtherRatio` 守卫合并、`ApplyOtherRatiosToFloat` 应用（除非模型在 `constant.TaskPricePatches` 中）；
5. `service.PreConsumeBilling` 预扣费（仅首次尝试）；
6. `DoResponse` 成功后按最终 OtherRatios 重算并差额结算（`SettleBilling`）；失败走 `Refund`。

轮询结算（`service/task_polling.go`）：

- 适配器未覆盖 `AdjustBillingOnComplete`（BaseBilling 返回 0），完成后不改变预扣金额，与 sora 渠道行为一致——金额在提交时已按 seconds/size 确定并差额结算完毕；
- 超时任务由 `sweepTimedOutTasks` 统一 `RefundTaskQuota` 退款。

安全不变量：seconds 上游校验、`AddOtherRatio` 拒绝非正/NaN/Inf 倍率、配额换算全部走 `common/quota_math.go` 集中 helper，无本地裸转换。

## 6. 与相关适配器的差异对照

| 维度 | sora | kling/jimeng | newapi（本实现） |
| --- | --- | --- | --- |
| 上游 | OpenAI 官方 | 官方厂商 API | 另一个 new-api 实例 |
| 请求转换 | Sora 格式 | 厂商私有格式（JWT/HMAC 签名） | 透传（仅换 model + Bearer） |
| 提交响应 | OpenAI video 对象 | 厂商信封 `{code,data}` | OpenAI video 对象（同 sora 解析） |
| 轮询端点 | `GET /v1/videos/{id}` | 厂商任务查询接口 | `GET /v1/videos/{id}`（同 sora） |
| 计费 | seconds/size 倍率 | 厂商参数倍率 | seconds/size 倍率（同 sora） |
| ModelList | 内置 sora-2 系列 | 内置型号 | 空（随上游动态获取） |

## 7. 测试

`relay/channel/task/newapi/adaptor_test.go`（testify）覆盖：

- `ParseTaskResult` 全状态映射表测（含 error.message、无 error、未知状态、progress）；
- `EstimateBilling` 表测（默认 4s、显式 seconds、duration 回退、宽/高尺寸倍率、remix→nil）；
- `ProcessNewAPIOtherRatios` / `getVideoSizeRatio` 内置倍率表查找（模型精确、前缀、`"*"` 通配回退）；
- `DoResponse`（公开 ID 回写与响应体、旧 `task_id` 兼容回退、空 ID 报错）；
- `ConvertToOpenAIVideo`（id 替换）。


运行：`go test ./relay/channel/task/newapi/...`
