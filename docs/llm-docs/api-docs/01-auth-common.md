# 认证与通用协议

## 认证方式总览

系统存在两套主要认证链路。

| 认证方式 | 使用场景 | 凭证位置 | 关键代码 |
| --- | --- | --- | --- |
| 匿名 | 状态、公告、注册、登录、支付回调、OAuth 回调 | 无 | `router/api-router.go` |
| `UserAuth` | 登录后的普通用户后台接口 | session 或 `Authorization` 访问令牌，并要求 `New-Api-User` | `middleware/auth.go:authHelper` |
| `AdminAuth` | 管理员后台接口 | 同 `UserAuth`，且 `role >= RoleAdminUser` | `middleware/auth.go:AdminAuth` |
| `RootAuth` | Root 系统设置、敏感密钥查看 | 同 `UserAuth`，且 `role >= RoleRootUser` | `middleware/auth.go:RootAuth` |
| `TokenAuth` | 模型代理、任务代理、旧 dashboard billing | API Key，多种兼容传法 | `middleware/auth.go:TokenAuth` |
| `TokenAuthReadOnly` | 令牌用量、令牌日志查询 | API Key，只校验 key 存在和用户未禁用 | `middleware/auth.go:TokenAuthReadOnly` |
| `TokenOrUserAuth` | 文件/视频内容代理 | session 或 API Key 二选一 | `middleware/auth.go:TokenOrUserAuth` |
| 安全验证 | 查看渠道密钥等高敏操作 | session 内 5 分钟安全验证状态 | `middleware/secure_verification.go` |

## UserAuth / AdminAuth / RootAuth

`authHelper` 的实际逻辑：

1. 优先读取 session 中的 `username`、`role`、`id`、`status`。
2. 如果没有 session，则读取请求头 `Authorization`，调用 `model.ValidateAccessToken` 校验用户访问令牌。
3. 无论 session 还是访问令牌，都要求请求头 `New-Api-User` 存在，且与用户 id 相同。
4. 校验用户未禁用、角色满足最小权限。
5. 写入 Gin context：`username`、`role`、`id`、`group`、`user_group`、`use_access_token`。
6. `AdminAuth` / `RootAuth` 请求会自动进入管理审计兜底逻辑。

对接要求：

```http
Authorization: Bearer <user_access_token>
New-Api-User: <user_id>
```

或者浏览器 session 已登录时仍需由前端带上：

```http
New-Api-User: <user_id>
```

注意：用户后台访问令牌不是模型 API Key。它由 `GET /api/user/token` 生成，存入 `users.access_token`。

## TokenAuth

模型代理使用 `model.Token` 中的 API Key。后端兼容以下传法：

| 协议风格 | 传法 | 代码逻辑 |
| --- | --- | --- |
| OpenAI | `Authorization: Bearer sk-xxx` | 默认读取 |
| Claude | `x-api-key: sk-xxx` 且路径含 `/v1/messages` 或 `/v1/models` | 转成 Authorization |
| Gemini | `?key=sk-xxx` 或 `x-goog-api-key: sk-xxx`，路径含 `/v1beta/models`、`/v1beta/openai/models`、`/v1/models/` | 转成 Authorization |
| Midjourney 兼容 | `mj-api-secret: sk-xxx`，或 `Authorization` 为空/为 `midjourney-proxy` | 兜底读取 |
| Realtime WebSocket | `Sec-WebSocket-Protocol: realtime, openai-insecure-api-key.sk-xxx, openai-beta.realtime-v1` | 解析为 Authorization |

TokenAuth 的关键校验：

- 去掉 `Bearer `、`sk-` 前缀。
- 支持 `sk-<key>-<channel_id>` 指定渠道，只有管理员用户的 API Key 可用。
- `model.ValidateUserToken` 校验令牌存在、启用、未过期、有额度。
- 检查令牌 IP 白名单 `allow_ips`。
- 读取用户缓存，用户必须启用。
- 如果令牌设置了 `group`，必须属于用户可用分组，且分组未废弃；`auto` 是特例。
- 写入 `ContextKeyUsingGroup`、`ContextKeyTokenGroup`、`token_id`、`token_name`、额度、模型限制等上下文。

相关代码：

- `middleware/auth.go:TokenAuth`
- `middleware/auth.go:SetupContextForToken`
- `model/token.go:ValidateUserToken`
- `model/token.go:GetIpLimits`

## 渠道分发

多数模型代理接口在 `TokenAuth` 后进入 `middleware.Distribute()`：

1. 解析请求模型。
2. 基于用户/令牌分组、模型、能力表选择渠道。
3. 写入 `channel_id`、`channel_type`、`channel_name`、渠道 key 等 context。
4. `controller.Relay` 进行请求校验、敏感词检查、token 估算、预扣费、请求上游、结算、失败重试。

关键代码：

- `middleware/distributor.go`
- `service/channel_select.go`
- `controller/relay.go`
- `relay/common/relay_info.go`
- `model/ability.go`

## 通用响应格式

### 管理台 API

多数 `/api` controller 使用：

```json
{
  "success": true,
  "message": "",
  "data": {}
}
```

错误常见为 HTTP 200：

```json
{
  "success": false,
  "message": "错误信息"
}
```

代码：

- `common/gin.go:ApiSuccess`
- `common/gin.go:ApiError`
- `common/gin.go:ApiErrorMsg`
- `common/gin.go:ApiErrorI18n`

### 模型代理 OpenAI 格式

成功响应通常透传或转换为 OpenAI 格式，例如 chat completions：

```json
{
  "id": "chatcmpl-xxx",
  "object": "chat.completion",
  "created": 1234567890,
  "model": "model-name",
  "choices": [],
  "usage": {}
}
```

错误响应：

```json
{
  "error": {
    "message": "错误信息 (request id: ...)",
    "type": "new_api_error",
    "param": "",
    "code": "model_not_found"
  }
}
```

代码：

- `controller/relay.go:Relay`
- `types/error.go:OpenAIError`
- `types/error.go:NewAPIError.ToOpenAIError`

### Claude 错误格式

`POST /v1/messages` 使用 Claude relay 格式，错误为：

```json
{
  "type": "error",
  "error": {
    "type": "new_api_error",
    "message": "错误信息"
  }
}
```

代码：

- `controller/relay.go:Relay`
- `types/error.go:ClaudeError`
- `types/error.go:NewAPIError.ToClaudeError`

### Task 错误格式

任务型接口失败时：

```json
{
  "code": "get_channel_failed",
  "message": "错误信息",
  "data": null
}
```

代码：

- `dto/task.go:TaskError`
- `controller/relay.go:respondTaskError`

## 分页约定

多数列表接口使用 `common.GetPageQuery(c)`，常见查询参数：

| 参数 | 含义 |
| --- | --- |
| `p` | 页码，默认通常为 1 |
| `page_size` | 每页数量，默认由 `common.GetPageQuery` 决定 |

输出 `data` 通常是 `PageInfo`，含总数和 items。具体字段由 `common` 包定义。

## 请求体读取与 Content-Type

Relay 使用可重复读取请求体：

- JSON：`common.UnmarshalBodyReusable`
- 表单：`application/x-www-form-urlencoded`
- 文件/图片：`multipart/form-data`
- 大请求体：超过 `constant.MaxRequestBodyMB` 会返回 413

相关代码：

- `common/gin.go:GetRequestBody`
- `common/gin.go:UnmarshalBodyReusable`
- `common/gin.go:ParseMultipartFormReusable`
- `middleware/request_body_limit.go`

## 限流与安全中间件

| 中间件 | 作用 | 常见接口 |
| --- | --- | --- |
| `GlobalAPIRateLimit` | `/api` 全局限流 | 管理台 API |
| `CriticalRateLimit` | 登录、付款、敏感操作限流 | 登录、支付、密钥查看 |
| `SearchRateLimit` | 搜索接口限流 | token/log search |
| `ModelRequestRateLimit` | 模型请求限流 | `/v1`、`/v1beta` |
| `TurnstileCheck` | 人机验证 | 注册、登录、邮箱验证、签到 |
| `SecureVerificationRequired` | 高敏操作二次验证 | `POST /api/channel/:id/key` |
| `SystemPerformanceCheck` | 系统负载保护 | Relay 和任务接口 |

## Go 对接可行性

完全可行。

- 管理台 API：用标准 `net/http` 或 Resty 均可，注意同时带 `Authorization` 和 `New-Api-User`。
- 模型代理 API：优先直接使用 OpenAI/Anthropic/Gemini 官方 Go SDK，把 `base_url` 指向本系统即可；非标准任务接口用普通 HTTP JSON 对接。
- 流式：OpenAI SSE、Gemini `alt=sse`、Realtime WebSocket 都可用 Go 对接，但需要按对应协议读取。

