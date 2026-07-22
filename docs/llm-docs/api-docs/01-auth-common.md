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


## UserAuth session 登录深挖

### session 存储模型

`UserAuth` 使用的是 `gin-contrib/sessions` 的 cookie store：

- `main.go` 中通过 `cookie.NewStore([]byte(common.SessionSecret))` 初始化 session store。
- session cookie 名称是 `session`。
- cookie 配置为 `Path=/`、`MaxAge=2592000`（30 天）、`HttpOnly=true`、`Secure=false`、`SameSite=Strict`。

这意味着普通登录态不是服务端保存一条 session 记录再按 session id 查询，而是把 session 内容编码进浏览器 cookie。服务端保存的是用于签名校验的 `SESSION_SECRET`，不维护每个用户 session 的服务端状态表。

底层实现来自 `gorilla/sessions` 与 `gorilla/securecookie`：

- 保存时使用 `securecookie.EncodeMulti(session.Name(), session.Values, ...)` 编码 session 内容并写入 cookie。
- 读取时使用 `securecookie.DecodeMulti(session.Name(), cookie.Value, &session.Values, ...)` 解码并校验 cookie。

因此，攻击者不能只靠手工构造 `id`、`username`、`role` 等字段来伪造登录态；没有 `SESSION_SECRET` 就无法生成可通过签名校验的 session cookie。篡改后的 cookie 解码失败，后续 `session.Get("username")` 等字段取不到有效值，`UserAuth` 会继续走访问令牌 fallback 或直接判定未登录。

### `SESSION_SECRET` 来源

`common.SessionSecret` 默认是进程启动时生成的随机 UUID：

- `common/constants.go:SessionSecret`
- `common/init.go:InitEnv`

如果环境变量 `SESSION_SECRET` 存在，则使用环境变量中的值；如果被设置为默认占位值 `random_string`，进程会直接退出并提示必须更换。

部署上需要注意：

- 单实例且未配置 `SESSION_SECRET` 时，进程重启会生成新的随机密钥，旧 session cookie 会全部失效。
- 多实例部署时必须配置一致且足够随机的 `SESSION_SECRET`，否则不同实例之间无法互相识别 session cookie。
- 一旦 `SESSION_SECRET` 泄露，攻击者就具备伪造有效 session cookie 的能力，应立即轮换密钥并让旧 cookie 失效。

### 用户名密码登录流程

用户名密码登录入口是 `POST /api/user/login`，路由在 `router/api-router.go`，处理函数是 `controller.Login`。

后端主要步骤：

1. 检查 `common.PasswordLoginEnabled`，禁用时直接返回失败。
2. 从 JSON 请求体解析 `username` 与 `password`。
3. 构造 `model.User{Username, Password}` 并调用 `user.ValidateAndFill()`。
4. `ValidateAndFill()` 用 `username = ? OR email = ?` 查询用户。
5. 使用 `common.ValidatePasswordAndHash` 对明文密码与数据库中的 bcrypt hash 做校验。
6. 同时要求用户 `status == common.UserStatusEnabled`，否则按用户名或密码错误处理。
7. 如果用户启用了 2FA，则先在 session 中写入 `pending_username` 与 `pending_user_id`，返回 `require_2fa=true`。
8. 如果没有启用 2FA，则调用 `setupLogin(&user, c)` 完成登录。

`setupLogin` 会执行：

1. 更新用户 `last_login_at`。
2. 向 session 写入 `id`、`username`、`role`、`status`、`group`。
3. 调用 `session.Save()`，让 session 内容编码并写回 `session` cookie。
4. 记录登录成功审计日志。
5. 返回脱敏后的用户基础信息给前端。

前端默认主题登录成功后还会调用 `/api/user/self` 拉取完整用户信息，并把用户信息与用户 id 存到本地状态/`localStorage`。后续 API 请求会自动带上：

```http
New-Api-User: <user_id>
```

注意：`New-Api-User` 不是认证凭证，只是后端要求的用户 id 一致性校验字段。真正证明登录态的是合法的 `session` cookie，或者 fallback 链路中的 `Authorization` 访问令牌。

### 2FA 登录流程

如果密码校验成功但用户启用了 2FA，`/api/user/login` 不会立刻写入正式登录 session，而是写入：

- `pending_username`
- `pending_user_id`

随后前端调用 `POST /api/user/login/2fa`。

`controller.Verify2FALogin` 会：

1. 从 session 读取 `pending_user_id`。
2. 根据用户 id 查询用户。
3. 读取用户的 2FA 配置。
4. 校验 TOTP 验证码；如果 TOTP 不通过，再尝试校验备用码。
5. 验证通过后删除 `pending_username` 与 `pending_user_id`。
6. 调用 `setupLogin(user, c)` 写入正式登录 session。

如果 `pending_user_id` 不存在或格式无效，会返回“会话已过期”或“会话数据无效”，要求重新登录。

### UserAuth session 认证流程

`UserAuth`、`AdminAuth`、`RootAuth` 都走 `middleware/auth.go:authHelper`，区别只是要求的最小角色不同。

session 认证路径如下：

1. `authHelper` 调用 `sessions.Default(c)` 获取当前请求的 session。
2. 读取 session 中的 `username`、`role`、`id`、`status`。
3. 如果 `username` 存在，则认为请求携带了 session 登录态。
4. 读取请求头 `New-Api-User`。
5. 将 `New-Api-User` 转成整数，并要求它与 session 中的 `id` 完全一致。
6. 检查 session 中的 `status`，如果是 `common.UserStatusDisabled` 则拒绝。
7. 检查 session 中的 `role` 是否满足当前中间件要求。
8. 检查 `username` 非空、`role` 是合法角色。
9. 写入 Gin context：`username`、`role`、`id`、`group`、`user_group`、`use_access_token=false`。
10. 调用后续 handler。

如果 session 中没有 `username`，`authHelper` 会尝试访问令牌链路：

1. 读取请求头 `Authorization`。
2. 调用 `model.ValidateAccessToken`。
3. `ValidateAccessToken` 去掉 `Bearer ` 前缀后，用 `users.access_token` 查询用户。
4. 查询成功后，同样要求 `New-Api-User` 与用户 id 一致。
5. 校验用户状态、角色、用户名与角色合法性。
6. 写入 Gin context，并设置 `use_access_token=true`。

因此 `UserAuth` 对接时有两种合法形态：

```http
Cookie: session=<signed_session_cookie>
New-Api-User: <user_id>
```

或者：

```http
Authorization: Bearer <user_access_token>
New-Api-User: <user_id>
```

### 过期、防伪造与重放边界

当前机制能防住：

- 手工构造 session cookie。
- 篡改 cookie 中的 `id`、`role`、`status` 等字段。
- 使用与当前 session 用户 id 不一致的 `New-Api-User`。
- 使用已超过 cookie/securecookie MaxAge 的旧 session。

当前机制不能天然解决：

- 已被窃取但尚未过期的合法 session cookie 被重放。
- 服务端按单个 session 主动吊销，因为没有服务端 session 状态表。
- 用户被禁用或降权后，已有 dashboard session 立即失效。

最后一点尤其需要注意：`UserAuth` 的 session 路径使用的是 cookie session 中缓存的 `status`、`role`、`group`，不是每次请求都回表查询最新用户状态。管理员禁用、降权或改组用户后，API Key / 访问令牌相关链路会更容易通过数据库或缓存刷新感知变化；但普通 dashboard session 在 cookie 有效期内可能继续按旧 session 值通过认证，直到用户重新登录、退出登录、cookie 过期，或服务端轮换 `SESSION_SECRET`。

### 安全建议

- 生产环境必须配置强随机 `SESSION_SECRET`，多实例保持一致。
- HTTPS 部署建议将 session cookie 的 `Secure` 配置改为 `true`，避免浏览器在 HTTP 请求中发送登录 cookie。
- 如果需要禁用/降权用户后立即让 dashboard session 失效，应引入服务端校验机制，例如：
  - `UserAuth` 每次从用户缓存/数据库读取最新 `status`、`role`、`group`；
  - 或在用户表中维护 `session_version` / `token_version`，session 内保存版本号，认证时比对；
  - 或改为服务端 session store，支持按用户维度删除 session。
- 对高敏操作继续使用二次验证 session，例如 `SecureVerificationRequired` 的 5 分钟验证窗口。
