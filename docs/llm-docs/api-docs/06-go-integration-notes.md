# Go 对接说明

本系统的 API 可以用 Go 对接。建议按接口类型选择不同方式。

## 模型代理：优先使用官方 SDK

OpenAI 兼容接口推荐直接使用支持自定义 BaseURL 的 SDK。

示例，普通 HTTP 方式：

```go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

func main() {
	body := map[string]any{
		"model": "gpt-4o-mini",
		"messages": []map[string]string{
			{"role": "user", "content": "hello"},
		},
		"stream": false,
	}
	payload, _ := json.Marshal(body)

	req, _ := http.NewRequest(http.MethodPost, "https://your-domain.example/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer sk-xxx")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	fmt.Println(resp.StatusCode, string(data))
}
```

注意：

- API Key 是 `model.Token` 的 key，不是后台用户 access token。
- `sk-` 前缀可带可不带，后端会去掉。
- 管理员 API Key 支持 `sk-<key>-<channel_id>` 指定渠道，普通用户不支持。
- 流式响应需按 SSE 逐行读取 `data:`。

## Claude 对接

```go
req.Header.Set("x-api-key", "sk-xxx")
req.Header.Set("anthropic-version", "2023-06-01")
req.Header.Set("Content-Type", "application/json")
```

路径使用：

```text
POST https://your-domain.example/v1/messages
```

错误响应不是 OpenAI 包裹，而是：

```json
{
  "type": "error",
  "error": {
    "type": "new_api_error",
    "message": "..."
  }
}
```

## Gemini 对接

三种认证方式均可：

```http
x-goog-api-key: sk-xxx
```

```text
?key=sk-xxx
```

```http
Authorization: Bearer sk-xxx
```

路径示例：

```text
POST /v1beta/models/gemini-2.5-pro:generateContent
POST /v1beta/models/gemini-2.5-pro:streamGenerateContent?alt=sse
```

Go 中普通 JSON POST 即可；流式时按 SSE 读取。

## 管理台 API 对接

管理台 API 与模型 API Key 不同，需要用户访问令牌和用户 ID：

```go
req.Header.Set("Authorization", "Bearer <user_access_token>")
req.Header.Set("New-Api-User", "<user_id>")
req.Header.Set("Content-Type", "application/json")
```

`user_access_token` 获取方式：

1. 浏览器登录后调用 `GET /api/user/token`。
2. 后端返回并保存到 `users.access_token`。

这一步可以完全由 Go 后端客户端实现，不要求前端参与。可选路径有两种：

1. 已经有 `user_access_token`：直接调用 `GET /api/user/token`，请求头带 `Authorization: Bearer <user_access_token>` 和 `New-Api-User: <user_id>`。该接口会重新生成并覆盖 `users.access_token`。
2. 只有用户名密码：Go 客户端先用同一个 `http.Client` + `cookiejar.Jar` 调 `POST /api/user/login` 获得 session，再带同一个 cookie jar 调 `GET /api/user/token`。如果账号启用了 2FA，登录接口会返回 `require_2fa=true`，还需要继续调用 `POST /api/user/login/2fa` 完成登录。

限制与注意：

- `POST /api/user/login` 路由挂了 `TurnstileCheck`。如果系统开启 Turnstile，Go 客户端必须在 query 中带可验证的 `turnstile` token；纯后端程序无法自行“生成”这个人机校验 token。
- 如果是稳定的服务端集成，更推荐一次性生成并安全保存 `user_access_token`，后续直接使用访问令牌，不要频繁自动登录。
- `GET /api/user/token` 会轮换访问令牌；并发系统中要避免多个进程同时刷新，导致旧 token 失效。

管理员是否能获取其他用户的 `user_access_token`：

- 通过现有 HTTP API，普通管理员和 Root 都不能直接读取其他用户已有的 `user_access_token`。
- `GET /api/user/token` 没有用户 id 参数，`controller.GenerateAccessToken` 固定使用 `c.GetInt("id")`，也就是当前认证用户，只会给当前用户生成并返回新的 access token。
- `UserAuth` 会强制校验 `New-Api-User` 与当前认证用户 id 一致，所以管理员不能用自己的 session/access token 再把 `New-Api-User` 改成目标用户 id 来获取目标用户 token。
- 管理员用户列表、用户详情等接口即使查询到了 `model.User`，`AccessToken` 字段也带有 `json:"-"`，不会序列化到 API 响应。
- 管理员创建/更新用户接口也不会写入 `access_token`：创建接口会构造 `cleanUser`，更新接口最终走 `User.Edit`，只更新用户名、显示名、分组、备注和可选密码。
- 如果管理员拥有目标用户管理权限，理论上可以修改低权限用户密码，再以该用户身份登录并调用 `GET /api/user/token` 生成新的 access token；但这会改变用户密码、产生审计/登录痕迹，也不是读取原有 token。开启 Turnstile、2FA 或 Passkey 时，这条间接路径还会受到相应验证限制。
- 如果直接拥有数据库权限，当然可以读取 `users.access_token` 字段；这不属于当前 HTTP API 能力边界。

相关代码：`router/api-router.go`、`middleware/auth.go:authHelper`、`controller/user.go:GenerateAccessToken`、`controller/user.go:GetUser`、`controller/user.go:CreateUser`、`controller/user.go:UpdateUser`、`model/user.go:User`、`model/user.go:ValidateAccessToken`。

互信系统下的自动获取方案：

先明确生命周期：`users.access_token` 不是创建用户后天然存在。Root 初始化路径 `controller/setup.go:PostSetup` 和 `model/main.go:createRootAccountIfNeed` 都设置 `AccessToken: nil`；普通用户创建路径 `model/user.go:Insert` 中 `user.SetAccessToken(...)` 已被注释。只有调用 `GET /api/user/token`，或可信代码直接写入 `users.access_token`，该字段才会有值。生成后它会持久保存在数据库里，不是临时 session token；再次调用 `GET /api/user/token` 会生成新值并覆盖旧值。

推荐方案按需求分两类：

1. 只需要系统级管理能力：创建一个专用集成用户，赋予 Admin 或 Root 角色，由该集成用户调用一次 `GET /api/user/token` 生成自己的 `user_access_token`，新系统长期保存并使用它调用管理接口。这是最稳妥的方式，不需要获取普通用户的 token。
2. 必须以“目标用户本人”身份调用 `/api/user/self`、`/api/token` 等用户自助接口：现有公开 HTTP API 不提供管理员代取/代生成目标用户 access token 的能力。互信场景建议新增一个仅内网可访问的“内部确保 access token”接口，或由新系统直接读写数据库。

推荐的内部接口方案：

- 在当前系统新增类似 `POST /api/internal/users/:id/access_token/ensure` 的接口，只允许新系统调用。
- 鉴权建议使用内网 ACL + mTLS 或 HMAC 签名 + 时间戳/nonce，必要时再叠加 RootAuth；不要暴露到公网。
- 处理逻辑：按 `:id` 查询目标用户；如果 `users.access_token` 为空则生成唯一随机 token 并写入；如果已有 token，则按请求参数决定“返回现有 token”或“轮换生成新 token”。
- 返回 token 后，新系统用 `Authorization: Bearer <target_user_access_token>` + `New-Api-User: <target_user_id>` 调用用户自助接口。
- 所有调用必须写审计日志，记录调用方系统、目标用户、是否轮换、用途和请求 id。

可选的数据库直连方案：

- 新系统连接同一个数据库，读取 `users.access_token`。
- 如果字段为空，则生成一个足够长、不可预测且全表唯一的随机字符串，写入 `users.access_token`。
- 之后按目标用户身份请求时，带 `Authorization: Bearer <token>` 和 `New-Api-User: <target_user_id>`。
- `ValidateAccessToken` 会直接按 `access_token` 查 `users` 表，因此不依赖额外缓存；写库后下一次请求即可生效。
- 这种方案绕过当前系统的 controller、审计和业务校验，必须由数据库权限、操作审计、加密存储和轮换策略兜底。优先级低于内部接口方案。

不推荐方案：管理员改目标用户密码后登录再调用 `GET /api/user/token`。它虽然在权限允许时可能可行，但会改变用户密码、影响用户登录、留下明显审计痕迹，还会受到 Turnstile、2FA、Passkey 等登录验证限制。

安全建议：

- 对接系统优先使用专用集成用户，而不是批量持有真实用户 access token。
- 如果必须持有真实用户 token，应加密存储，限定用途，支持轮换和吊销。
- 当前 `user_access_token` 本身没有过期时间字段，泄露后风险较高；建议内部接口支持强制轮换，并记录最近使用时间或接入额外的访问控制。
- 不要把 `user_access_token` 与模型 API Key 混用：前者用于后台 `/api`，后者是 `model.Token` 的 `sk-...`。

## Root/Admin 获取用户消耗明细

结论：只使用 Root 账号或管理员账号，可以获取用户消耗明细，不需要获取普通用户的 `user_access_token`。推荐走管理员全站日志和数据接口，而不是模拟目标用户身份。

### 可用接口

| 目标 | 接口 | 权限 | 说明 |
| --- | --- | --- | --- |
| 请求级消费明细 | `GET /api/log/` | `AdminAuth` | 全站日志分页，可按用户、令牌名、模型、渠道、分组、请求 ID、时间范围过滤。 |
| 请求级消费汇总 | `GET /api/log/stat` | `AdminAuth` | 返回消费额度 `quota`，以及最近 60 秒 RPM/TPM。 |
| 看板聚合数据 | `GET /api/data/` | `AdminAuth` | 按模型和小时聚合；传 `username` 时按指定用户聚合。 |
| 按用户聚合数据 | `GET /api/data/users` | `AdminAuth` | 按 `username + created_at` 聚合所有用户消耗。 |
| 流向聚合数据 | `GET /api/data/flow` | `AdminAuth` | 按用户、分组、模型、渠道聚合；Root 额外返回 `token_id`、`node_name`，并解析 `token_name`。 |
| Midjourney 任务记录 | `GET /api/mj/` | `AdminAuth` | 全站 MJ 任务分页，含 `user_id`、`channel_id`、`quota`。 |
| 通用异步任务记录 | `GET /api/task/` | `AdminAuth` | 全站视频/任务分页，含 `user_id`、`channel_id`、`quota`，并补充用户名。 |

后台 API 认证仍然使用管理员自己的访问令牌：

```http
Authorization: Bearer <admin_or_root_user_access_token>
New-Api-User: <admin_or_root_user_id>
```

不要把 `New-Api-User` 改成目标普通用户 ID。`middleware/auth.go:authHelper` 会校验请求头用户 ID 必须等于当前认证用户 ID；管理员查看别人的消耗，靠的是 `AdminAuth` 的全站接口，而不是冒充目标用户。

### 请求级明细能看到什么

`GET /api/log/` 底层查询 `model.GetAllLogs`，返回 `model.Log`。关键字段包括：

```json
{
  "id": 123,
  "user_id": 8,
  "created_at": 1760000000,
  "type": 2,
  "content": "...",
  "username": "alice",
  "token_name": "prod-key",
  "model_name": "gpt-4o-mini",
  "quota": 120,
  "prompt_tokens": 100,
  "completion_tokens": 50,
  "use_time": 2,
  "is_stream": false,
  "channel": 5,
  "channel_name": "openai-main",
  "token_id": 10,
  "group": "default",
  "ip": "",
  "request_id": "...",
  "upstream_request_id": "...",
  "other": "{}"
}
```

常用查询：

```http
GET /api/log/?type=2&username=alice&start_timestamp=1760000000&end_timestamp=1760086400
GET /api/log/?type=2&username=alice&model_name=qwen3.7-max-triple
GET /api/log/?type=2&username=alice&token_name=prod-key
GET /api/log/?type=2&channel=5&group=discount_80
GET /api/log/?request_id=202607051523471179780018268d9d6S2g76AQM
```

注意：

- `type=2` 是消费日志，常量为 `model.LogTypeConsume`。
- 管理员接口按 `username` 过滤，不按 `user_id` 过滤；响应里会返回 `user_id`。
- `model_name` 和 `username` 支持受控 `%` 模糊匹配，逻辑在 `applyExplicitLogTextFilter`。
- `GET /api/log/search` 当前已废弃，固定返回「该接口已废弃」，不要用于新对接。
- `GET /api/log/stat` 虽然接收 `type` 参数，但 `model.SumUsedQuota` 内部固定统计消费日志；RPM/TPM 只统计最近 60 秒。

### 聚合数据的权限差异

`GET /api/data/flow` 会把当前登录用户角色传给 `model.GetFlowQuotaData`：

- Root：走 `getRootFlowQuotaData`，返回 `user_id`、`username`、`node_name`、`token_id`、`token_name`、`use_group`、`model_name`、`channel_id`、`channel_name`、`count`、`quota`、`token_used`。
- Admin：走 `getAdminFlowQuotaData`，返回 `user_id`、`username`、`use_group`、`model_name`、`channel_id`、`channel_name`、`count`、`quota`、`token_used`，但不返回 `token_id` 和 `node_name`。
- 普通用户：只能走 `/api/data/flow/self`，只看自己的数据。

如果你需要「按令牌 ID」统计用户消耗，Root 的 `/api/data/flow` 更合适；普通 Admin 可以通过 `/api/log/` 的明细字段看到 `token_id`，但 `/api/data/flow` 聚合结果不会给 Admin 暴露 `token_id`。

### 与 API Key 查询接口的区别

`GET /api/usage/token/` 和 `GET /api/log/token` 使用 `TokenAuthReadOnly`，认证材料是目标模型 API Key（`sk-...`），不是管理员 access token。它们适合「已持有某个 API Key，查询这个 key 自己的用量/最近日志」。

仅凭 Root/Admin 的后台 access token，不能通过现有 `/api/token` 用户自助接口直接读取其他用户完整 API Key：

- `/api/token/*` 路由使用 `UserAuth`，所有查询都以 `c.GetInt("id")` 作为当前用户。
- `model.GetTokenByIds`、`model.GetTokenKeysByIds` 都强制 `id + user_id` 匹配。
- 管理员虽然能通过 `/api/log/` 看到 `token_id`、`token_name`，但不能用这些接口换出其他用户的完整 key。

因此，获取用户消耗明细的推荐路径是管理员日志/聚合接口；不要为了查询消耗而获取用户 access token 或用户 API Key。

### 数据完整性条件

权限够不代表历史数据一定完整：

- 请求级消费明细依赖 `common.LogConsumeEnabled`。`model.RecordConsumeLog` 和 `model.RecordTaskBillingLog` 在该开关关闭时不会记录消费日志。
- 看板聚合数据依赖 `common.DataExportEnabled`。消费日志写入时会异步调用 `LogQuotaData`，再由 `SaveQuotaDataCache` 按 `DataExportInterval` 落库到 `quota_data`。
- `quota_data` 按小时聚合，不是请求级明细；如果需要逐请求审计，以 `/api/log/` 为准。
- 用户表上的 `used_quota`、`request_count` 是累计值，可从管理员用户接口看到，但它们不是明细。

相关代码：

- `router/api-router.go`：`/api/log`、`/api/data`、`/api/mj`、`/api/task` 路由权限。
- `controller/log.go`、`model/log.go`：日志查询、统计、消费日志字段。
- `controller/usedata.go`、`model/usedata.go`、`model/usedata_flow.go`：看板和流向聚合。
- `controller/task.go`、`model/task.go`：通用异步任务记录。
- `controller/midjourney.go`、`model/midjourney.go`：Midjourney 任务记录。
- `model/option.go`：`LogConsumeEnabled`、`DataExportEnabled` 配置项。

响应判断：

```go
type APIResponse[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}
```

注意：

- 很多业务错误 HTTP 状态仍是 200，应判断 `success`。
- `AdminAuth` / `RootAuth` 要求用户 role 足够。
- 部分 Root 高敏接口还需要先调用 `POST /api/verify` 完成安全验证，并保持同一个 cookie jar/session。这个过程可以由 Go 客户端自动编排，但验证材料仍必须真实存在：2FA 可用 TOTP/备用码自动提交；Passkey 需要完成 WebAuthn 断言，通常需要浏览器或实体认证器参与。

## Cookie Session 与访问令牌

如果是服务端到服务端集成，推荐用访问令牌方式，不推荐模拟浏览器 session。

如果必须调用 `POST /api/channel/:id/key`：

1. 使用同一个 HTTP client 和 cookie jar 登录或保持 session。
2. 调用 `POST /api/verify` 完成安全验证。
3. 5 分钟内调用 `POST /api/channel/:id/key`。

`POST /api/verify` 输入结构：

```json
{
  "method": "2fa",
  "code": "123456"
}
```

或：

```json
{
  "method": "passkey"
}
```

2FA 自动化可行：

- Go 客户端先登录并保留 cookie jar。
- 用 TOTP secret 在后端生成当前 6 位验证码，或使用未用过的备用码。
- 调用 `POST /api/verify`，后端会在 session 中写入 `secure_verified_at`。
- 5 分钟内用同一个 cookie jar 调用高敏接口。

Passkey 自动化受限：

- 需要先调用 `POST /api/user/passkey/verify/begin` 获取 WebAuthn challenge。
- 再由认证器完成签名后调用 `POST /api/user/passkey/verify/finish`。
- `verify/finish` 会在 session 中写入短暂的 `secure_passkey_ready_at`。
- 最后调用 `POST /api/verify` 且 `method=passkey`，把 ready 标记转换成 5 分钟的 `secure_verified_at`。
- 因为 WebAuthn 依赖私钥、origin/RP ID、用户验证和认证器交互，普通 Go 后端客户端通常不能无用户参与地完成；除非你专门接入了可由后端控制的 FIDO2/Passkey 认证器流程。

单纯带用户 access token 不足以完成高敏接口安全验证，因为验证状态存在 session。可行的 Go 实现必须维护 cookie jar，并在同一个 session 内完成登录、验证和高敏接口调用。

## 任务接口对接

任务提交通常返回 task id，上游结果需要轮询：

```text
POST /v1/videos
GET  /v1/videos/:task_id
GET  /v1/videos/:task_id/content
```

建议：

- 提交后保存本地业务 id 与返回 task id。
- 轮询间隔不要过短。
- 结果文件使用流式下载，避免一次性读入超大视频。

## 错误处理建议

模型代理错误：

```go
type OpenAIErrorResponse struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Param   string `json:"param"`
		Code    any    `json:"code"`
	} `json:"error"`
}
```

任务错误：

```go
type TaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}
```

管理台错误：

```go
type AdminError struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}
```

## 额度与副作用

外部系统对接时要知道这些副作用：

- 模型请求会预扣费，失败后按错误类型退款或收取违规费用。
- 成功请求会写日志、更新用户用量、令牌用量、渠道用量。
- 任务型接口提交成功后会写本地任务表。
- 渠道失败可能触发自动禁用。
- 订阅用户可能优先消耗订阅额度，取决于用户 `billing_preference`。

关键代码：

- `controller/relay.go`
- `service/pre_consume_quota.go`
- `service/tiered_settle.go`
- `service/task_billing.go`
- `model/log.go`
- `model/task.go`

## 推荐客户端封装

建议拆成两个 client：

1. `RelayClient`：只处理 API Key、OpenAI/Claude/Gemini/任务接口。
2. `AdminClient`：处理用户 access token、`New-Api-User`、cookie jar、安全验证、后台接口响应包裹。

这样可以避免把后台认证和模型认证混在一起。







