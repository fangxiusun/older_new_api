# `caaf4abc` → `bb1d0a34` 对外 API 变更分析

## 1. 分析范围与结论口径

本文比较以下两个提交的最终代码树：

- 基准提交：`caaf4abc62094b9932eb52e2562410f638a8d1ee`
- 目标提交：`bb1d0a34b059df750f0e5effe76b05c3e65d2f93`
- 比较方向：基准提交 → 目标提交

目标提交是一个合并提交，其第一父提交就是本次基准提交。完整差异涉及 1,895 个文件，因此本文不按文件罗列，而是从外部调用者可观察的 HTTP 契约出发，交叉检查了路由、中间件、Controller、DTO、Relay 适配器、系统任务以及 `docs/openapi/api.json`、`docs/openapi/relay.json`。

本文中的“实际契约”以目标提交中的路由和处理代码为准。OpenAPI 存在漏项和历史错误，相关差异单独列出，不能把 OpenAPI 未收录理解为接口不存在。

## 2. 执行摘要

本次变更中，对客户端影响最大的不是 Relay 路径，而是面板鉴权体系：

1. 面板从 Gin Session Cookie 迁移为“15 分钟 Access JWT + 30 天 HttpOnly Refresh Cookie + 服务端登录 Session”。升级后旧面板登录全部失效，需要重新登录。
2. `New-Api-User` 请求头退出鉴权契约；面板 PAT 继续可用，并新增标准 `Bearer` 写法。
3. 登录、2FA、Passkey、OAuth、微信和 Telegram 登录统一返回 AuthBundle；客户端不能再只依赖旧的简化用户对象或旧 Session Cookie。
4. OAuth state、2FA pending、Passkey ceremony、Telegram bind 全部迁移为一次性 `flow_token`。
5. 敏感操作改为带 scope 的 `X-Security-Proof`，原有“在 Session 中记录已验证状态”的方式不再有效。
6. 渠道管理新增细粒度授权。普通 Admin 默认不再拥有创建/删除渠道、修改密钥/Base URL/代理/覆盖配置等敏感能力。
7. 日志清理由同步删除接口迁移为 Root 专用的异步系统任务；全渠道测试和全量模型探测也改为异步任务响应。
8. Relay 路径与 HTTP 方法集合没有变化，但 Responses 输入字段、输出 usage、图片流式响应、协议转换、工具调用以及输入上限发生了多项可观察变化。

兼容性分级：

| 变更 | 等级 | 主要影响 |
| --- | --- | --- |
| 面板 Session → JWT/Refresh Session | 破坏性 | 所有面板前端和自建面板客户端 |
| `/api/oauth/state` GET → POST | 破坏性 | OAuth 登录/绑定客户端 |
| Passkey finish 请求封装与 `flow_token` | 破坏性 | WebAuthn 客户端 |
| `/api/verify` 增加 `scope` 并改为 Proof Token | 破坏性 | 渠道密钥查看、Passkey 管理 |
| 渠道细粒度权限 | 行为收紧 | 非 Root 管理员与自动化脚本 |
| 同步任务 → 异步 System Task | 破坏性 | 日志清理、全渠道测试、全量模型探测 |
| Responses `incomplete_details.reasoning` → `reason` | 破坏性修正 | 严格解析 Responses 的客户端 |
| 新增 usage 字段 | 向后兼容为主 | 宽松 JSON 客户端无影响；严格 schema 需更新 |
| max-token、图片数量、视频时长上限 | 行为收紧 | 曾发送超大值的客户端 |

## 3. API 路径与方法变化

### 3.1 实际新增路径

| 方法与路径 | 鉴权 | 输入 | 成功输出摘要 |
| --- | --- | --- | --- |
| `POST /api/user/auth/refresh` | Refresh Cookie；Secure 模式校验 Origin | 空 body；可选 `X-Auth-Session` | 新 AuthBundle，轮换 Refresh Cookie |
| `POST /api/user/auth/logout` | Refresh Cookie 和/或面板 Access JWT；Secure 模式校验 Origin | 空 body；可选 `X-Auth-Session` | 撤销 SID；Bearer 分支可返回 `revoked_sid`、`cookie_cleared` |
| `GET /api/user/sessions` | 面板 Session JWT；PAT 不允许 | 无 | 当前鉴权版本的有效 Session 数组 |
| `DELETE /api/user/sessions/:sid` | 面板 Session JWT；PAT 不允许 | 路径 SID | `revoked_sid`、`current` |
| `POST /api/user/sessions/revoke-others` | 面板 Session JWT；PAT 不允许 | 空 body | `revoked_count` |
| `POST /api/oauth/telegram/bind/start` | 面板 Session JWT | 空 body | `flow_token`、`callback_url`、`expires_at` |
| `GET /api/oauth/telegram/bind/:flow_token` | Telegram 签名参数；流程绑定原面板 Session | Telegram widget query | 始终 302 到 `/oauth/telegram` |
| `POST /api/subscription/admin/plans/:id/subscriptions/reset` | AdminAuth | 可选 `advance_reset_time` | 订阅重置统计 |
| `POST /api/subscription/admin/users/:id/subscriptions/reset` | AdminAuth | 必填 `plan_id`，可选 `advance_reset_time` | 订阅重置统计 |
| `POST /api/channel/status/batch` | AdminAuth + `channel.operate` | `ids`、`status` | 实际状态变更数量 |
| `POST /api/channel/:id/status` | AdminAuth + `channel.operate` | `status` | 是否实际发生变更 |
| `GET /api/authz/catalog` | AdminAuth | 无 | 权限资源、动作、内置角色及基线授权矩阵 |
| `POST /api/system-task/log-cleanup` | RootAuth | query `target_timestamp` | System Task 对象 |
| `GET /api/system-task/list` | RootAuth | 可选 query `limit` | 最近任务数组 |
| `GET /api/system-task/current` | RootAuth | 必填 query `type` | 活跃任务或 `null` |
| `GET /api/system-task/:task_id` | RootAuth | 路径任务 ID | 指定任务；不存在为 404 |
| `GET /api/system-info/instances` | RootAuth | 无 | 系统实例数组 |
| `DELETE /api/system-info/stale-instances` | RootAuth | 无 | `deleted_count` |
| `DELETE /api/system-info/instances/:node_name` | RootAuth | 路径节点名 | 仅在实例已 stale 时删除 |

### 3.2 删除或被替换的路径

| 旧接口 | 新接口/结论 | 兼容性说明 |
| --- | --- | --- |
| `GET /api/user/logout` | `POST /api/user/auth/logout` | 方法、凭据、Cookie 和输出契约全部变化 |
| `DELETE /api/log/?target_timestamp=...` | `POST /api/system-task/log-cleanup?target_timestamp=...` | Admin → Root；同步计数 → 异步任务 |
| `GET /api/oauth/telegram/bind` | `POST .../bind/start` + `GET .../bind/:flow_token` | 改为两阶段、一次性且绑定 Session 的流程 |
| `POST /api/option/migrate_console_setting` | 无替代公开接口 | 旧控制台设置迁移入口删除 |

### 3.3 HTTP 方法发生实际变化

`/api/oauth/state` 从 `GET` 改为 `POST`。旧的 GET 调用不再匹配路由。

需要特别区分两个 OpenAPI 修正：

- `/api/oauth/email/bind` 在基准代码中实际已经是 `POST + JSON`，目标 OpenAPI 才把历史错误的 `GET + query` 修正为 POST。这不是运行时方法变化。
- `/api/oauth/wechat/bind` 在两个提交的实际代码中都是 `POST + JSON`，OpenAPI 中的 GET → POST 同样只是文档修正。

### 3.4 Relay 路径保持不变

`docs/openapi/relay.json` 中的路径/方法集合在两个提交间没有增删。包括 `/v1/chat/completions`、`/v1/responses`、`/v1/responses/compact`、图片、音频、Gemini、视频、Midjourney、Suno 等入口均保留原路径。Relay 的变化集中在请求字段、校验、上游转换和响应内容。

## 4. 面板鉴权与登录会话

### 4.1 认证凭据迁移

| 场景 | 基准提交 | 目标提交 |
| --- | --- | --- |
| 浏览器面板 | Gin `session` Cookie + `New-Api-User` | `Authorization: Bearer <Access JWT>` + Refresh Cookie |
| Access Token 有效期 | 由 Cookie Session 控制 | JWT 15 分钟 |
| 长期登录 | 旧 Session Cookie | `new_api_refresh`，最长 30 天 |
| 服务端会话控制 | 无独立设备 Session | `user_sessions`，支持设备列表、撤销和版本控制 |
| 面板 PAT | 原始 `Authorization: <pat>`，还要求 `New-Api-User` | `Authorization: Bearer <pat>` 或原始单值 PAT；不再要求用户 ID 头 |
| 旧 `New-Api-User` | 必填且必须与认证用户匹配 | 完全不参与鉴权，可从客户端删除 |

Refresh Cookie 属性为：

- 名称：`new_api_refresh`
- `HttpOnly`
- `SameSite=Strict`
- Path：`/api/user/auth`
- `Secure` 由 `SESSION_COOKIE_SECURE` 控制
- 服务端只保存 Refresh Secret 的 HMAC；每次 refresh 都轮换

旧 Gin `session` Cookie 不再用于身份认证，因此滚动升级后旧页面看似仍有 Cookie，也必须重新登录。

面板 PAT 只是一种面板 API 凭据，不是浏览器登录 Session。它不能调用 Session 管理接口、不能签发绑定 Session 的 Security Proof，也不能替代 Refresh Cookie。

### 4.2 统一登录输出 AuthBundle

密码、2FA、Passkey、标准 OAuth、微信和 Telegram 登录成功后统一为：

```json
{
  "success": true,
  "message": "",
  "data": {
    "access_token": "<jwt>",
    "token_type": "Bearer",
    "access_expires_at": 1780000000,
    "user": {
      "id": 1,
      "username": "alice",
      "permissions": {
        "admin_permissions": {
          "channel": {
            "read": true,
            "operate": true,
            "write": true,
            "sensitive_write": false,
            "secret_view": false
          }
        }
      }
    },
    "session": {
      "sid": "<uuid>",
      "current": true,
      "login_method": "password",
      "ip": "203.0.113.10",
      "user_agent": "...",
      "created_at": 1780000000,
      "last_active_at": 1780000000,
      "expires_at": 1782592000
    }
  }
}
```

基准提交登录成功只返回 `id`、`username`、`display_name`、`role`、`status`、`group`，并把登录状态写入 Gin Session。自建前端必须改为在内存中持有 Access JWT，并在 API 请求中发送 Bearer。

登录 Session 新增两个账户级限制：

- 活跃 Session 达到默认 50：`409 AUTH_SESSION_LIMIT`
- 默认 24 小时内签发达到 100：`429 AUTH_SESSION_ISSUANCE_LIMIT`

### 4.3 Refresh 与 Logout

Refresh 请求：

```http
POST /api/user/auth/refresh
Cookie: new_api_refresh=<sid.secret>
X-Auth-Session: <sid>   # 已知当前 SID 时建议发送
```

成功响应与登录 AuthBundle 相同，并通过 `Set-Cookie` 轮换 Refresh Token。并发刷新通过确定性后继 Token 处理；不可恢复的重放会撤销 Session。

Logout 可以只带 Cookie，也可以带面板 Bearer。若带 Bearer，成功数据可能为：

```json
{
  "success": true,
  "message": "",
  "data": {
    "revoked_sid": "<sid>",
    "cookie_cleared": true
  }
}
```

`X-Auth-Session` 与 Cookie 或 Bearer 中 SID 不一致时，refresh/logout 返回 `409 AUTH_SESSION_MISMATCH`，且不会误撤销另一标签页的 Session。

当 `SESSION_COOKIE_SECURE=true` 时，这两个 Cookie 端点要求精确 HTTPS Origin；失败返回：

```json
{
  "success": false,
  "code": "AUTH_ORIGIN_FORBIDDEN",
  "message": "origin is not allowed"
}
```

允许来源是请求自身精确 Origin 和 `SESSION_COOKIE_TRUSTED_URL` 中配置的精确 HTTPS Origin。该配置不是 CORS 白名单，不接受通配符、路径或域名后缀匹配。

### 4.4 Session 管理输出与错误

`GET /api/user/sessions` 返回最多 100 个、属于当前鉴权版本的有效 Session，当前 Session 优先。单项格式就是 AuthBundle 中的 `session`。

`DELETE /api/user/sessions/:sid`：

- 成功：`data.revoked_sid`、`data.current`
- 空 SID：400 `AUTH_SESSION_ID_REQUIRED`
- 不存在或不属于当前用户：404 `AUTH_SESSION_NOT_FOUND`
- 删除当前 Session 时，如 Refresh Cookie 对应该 SID，会同时清除 Cookie

PAT 调用三个 Session 管理接口会得到 403 `AUTH_SESSION_REQUIRED`。

### 4.5 鉴权错误契约

面板认证中间件开始返回稳定 `code`：

| HTTP | code | 含义 |
| --- | --- | --- |
| 401 | `AUTH_TOKEN_EXPIRED` | Access JWT 到期 |
| 401 | `AUTH_SESSION_REVOKED` | Session 已撤销、过期或鉴权版本失效 |
| 401 | `AUTH_UNAUTHORIZED` | 凭据缺失、无效或 Refresh 无效 |
| 401 | `AUTH_USER_DISABLED` | 用户已禁用 |
| 401 | `AUTH_USER_INVALID` | 用户快照不合法 |
| 403 | `AUTH_INSUFFICIENT_PRIVILEGE` | 未达到 Admin/Root 角色要求 |
| 409 | `AUTH_SESSION_MISMATCH` | 请求指定 SID 与实际凭据不一致 |
| 409 | `AUTH_REFRESH_RACE` | 无法恢复的 Refresh 并发冲突 |
| 500 | `AUTH_INTERNAL_ERROR` | Session/数据库/缓存内部错误 |

并非所有业务失败都已统一为该格式。细粒度 `RequirePermission` 拒绝返回 HTTP 403、`success:false`，当前不附稳定 `code`；部分旧 Controller 仍使用 HTTP 200 + `success:false` 的历史错误封装。客户端应首先看 HTTP 状态，再看 `success` 和可选 `code`。

### 4.6 Cache-Control 与 Turnstile

登录、2FA 登录、Passkey 登录/注册/验证、OAuth state/callback、Telegram bind、`/api/verify`、refresh/logout、获取管理 PAT 等敏感响应新增 `Cache-Control: no-store`。

Turnstile 成功状态不再写入旧 Gin Session。启用 Turnstile 后，每次访问挂载该中间件的接口都必须带本次有效的 `turnstile` query，例如登录、注册、发送验证码、密码重置邮件和签到；不能再依赖此前请求缓存的校验状态。

## 5. OAuth、2FA、Passkey 与 Security Proof

### 5.1 `/api/oauth/state`

新请求：

```http
POST /api/oauth/state
Content-Type: application/json
Authorization: Bearer <access-jwt>   # intent=bind 时必需

{
  "provider": "github",
  "intent": "login",
  "aff": "invite-code"
}
```

字段约束：

- `provider` 必须是已注册 provider
- `intent` 只能是 `login` 或 `bind`
- `aff` 可选，最长 32，只允许用于 `login`
- `bind` 必须有面板 Session JWT；PAT 不满足 Session 绑定要求

新响应：

```json
{
  "success": true,
  "message": "",
  "data": {
    "flow_token": "...",
    "expires_at": 1780000600
  }
}
```

旧接口是 GET，并返回简单 state。新 AuthFlow 有 10 分钟 TTL，绑定 provider、intent、用户和 Session，只能消费一次。标准 OAuth callback 路径仍是 `GET /api/oauth/:provider?state=...&code=...`；bind callback 必须对应创建流程的仍有效面板 Session。

### 5.2 邮箱与微信绑定

- `POST /api/oauth/email/bind`：JSON `{"email":"...","code":"..."}`，新增 `UserAuth`。邮箱会 trim + 小写规范化，并以大小写不敏感方式检查唯一性。
- `POST /api/oauth/wechat/bind`：JSON `{"code":"..."}`，新增 `UserAuth`。

这两个接口的运行时方法没有改变；目标 OpenAPI 只是修正了旧文档中的 GET 描述。调用方真正需要迁移的是认证方式：旧 Session Cookie → Access JWT/PAT。

### 5.3 Telegram 绑定

第一步：

```http
POST /api/oauth/telegram/bind/start
Authorization: Bearer <access-jwt>
```

```json
{
  "success": true,
  "data": {
    "flow_token": "...",
    "callback_url": "/api/oauth/telegram/bind/<flow_token>",
    "expires_at": 1780000300
  }
}
```

第二步由 Telegram widget 调用 `callback_url`，仍携带 Telegram 的 `id`、`auth_date`、`hash` 等签名 query。服务端会重新确认创建流程的用户和 Session 仍有效，并对签名 assertion 做一次性防重放。

完成接口始终返回 302：

- 成功：`/oauth/telegram?telegram_bind=success&flow_token=...`
- 失败：`/oauth/telegram?telegram_bind=error&flow_token=...&error_code=...`

稳定错误码包括：

- `TELEGRAM_BIND_DISABLED`
- `TELEGRAM_BIND_INVALID_REQUEST`
- `TELEGRAM_BIND_FLOW_INVALID`
- `TELEGRAM_BIND_SESSION_INVALID`
- `TELEGRAM_BIND_ALREADY_BOUND`
- `TELEGRAM_BIND_USER_DELETED`
- `TELEGRAM_BIND_USER_DISABLED`
- `TELEGRAM_BIND_INTERNAL_ERROR`

### 5.4 2FA 登录

密码正确但启用 2FA 时，不再把 pending 用户写入 Session，而是返回：

```json
{
  "success": true,
  "data": {
    "require_2fa": true,
    "flow_token": "...",
    "expires_at": 1780000300
  }
}
```

随后调用：

```json
POST /api/user/login/2fa
{
  "code": "123456",
  "flow_token": "..."
}
```

`flow_token` 有效期 5 分钟、一次性消费，并绑定密码验证时的 `auth_version`。成功返回统一 AuthBundle。

### 5.5 `/api/verify` 与 Security Proof

旧请求允许 `method=2fa|passkey`，成功在 Session 中写入一个无 scope 的“已验证”状态，并返回 `{verified, expires_at}`。

新接口只接受直接的 2FA 校验；Passkey 必须走专用 Passkey verify 流程：

```json
POST /api/verify
Authorization: Bearer <access-jwt>
Content-Type: application/json

{
  "method": "2fa",
  "code": "123456",
  "scope": "channel.key.read"
}
```

允许的 scope：

- `channel.key.read`
- `passkey.register`
- `passkey.delete`

成功输出：

```json
{
  "success": true,
  "data": {
    "proof_token": "...",
    "expires_at": 1780000300,
    "method": "2fa",
    "scope": "channel.key.read"
  }
}
```

调用敏感接口时发送：

```http
X-Security-Proof: <proof_token>
```

Proof 有效期 5 分钟，并绑定用户、SID、用户鉴权版本、Session 版本、验证方法和 scope。验证失败统一为 HTTP 403，可能的 `code`：

- `SECURITY_PROOF_REQUIRED`
- `SECURITY_PROOF_EXPIRED`
- `SECURITY_PROOF_SCOPE_MISMATCH`
- `SECURITY_PROOF_METHOD_MISMATCH`
- `SECURITY_PROOF_INVALID`

### 5.6 Passkey 请求/响应格式变化

所有 Passkey begin 成功数据都从仅有 `options` 扩展为：

```json
{
  "options": { "publicKey": {} },
  "flow_token": "...",
  "expires_at": 1780000300
}
```

所有 finish 不再直接把 WebAuthn credential 作为整个请求 body，而是封装为：

```json
{
  "flow_token": "...",
  "credential": {
    "id": "...",
    "rawId": "...",
    "type": "public-key",
    "response": {}
  }
}
```

具体接口：

- `POST /api/user/passkey/login/begin`：无登录鉴权；返回 5 分钟登录 flow
- `POST /api/user/passkey/login/finish`：上面的 finish 格式；成功返回 AuthBundle
- `POST /api/user/passkey/register/begin`：Session JWT；若用户已启用 2FA，还要求 scope=`passkey.register` 的 2FA Proof
- `POST /api/user/passkey/register/finish`：同样重新校验 Proof，再消费 flow；成功返回新的 Access JWT/Session
- `POST /api/user/passkey/verify/begin`：请求 `{"scope":"..."}`；返回 options/flow
- `POST /api/user/passkey/verify/finish`：finish 格式；成功直接返回该 scope 的 Passkey Proof
- `DELETE /api/user/passkey`：按用户安全因子要求验证 `passkey.delete` Proof；成功轮换当前鉴权 Session

Passkey 注册、删除、2FA 启用/禁用、2FA 备用码重生成以及密码修改都会推进鉴权版本，保留并升级当前 Session、撤销其他 Session，并在响应中返回新的 `access_token`、`token_type`、`access_expires_at`、`session`。客户端必须立即替换内存中的旧 JWT/SID。

## 6. 管理 API 与授权变化

### 6.1 渠道细粒度权限

所有渠道接口仍先经过 AdminAuth，再按动作检查权限：

| 权限 | 默认 Admin | 典型接口 |
| --- | --- | --- |
| `channel.read` | 允许 | 渠道列表、搜索、详情、模型、Codex usage、标签模型 |
| `channel.operate` | 允许 | 测试、更新余额、启停、修复、模型探测、部分多密钥操作 |
| `channel.write` | 允许 | 非敏感路由配置、模型/分组/标签、应用模型更新 |
| `channel.sensitive_write` | 不允许 | 创建/删除渠道、密钥/Base URL/代理/覆盖配置、Codex 凭据、Ollama 管理 |
| `channel.secret_view` | 不允许 | 权限目录中的保留动作；完整密钥接口当前仍额外限定 Root |

Root 为 superuser，拥有所有权限。普通 Admin 默认仅有 read/operate/write。Root 可以在创建或更新管理员用户时通过 `admin_permissions` 配置覆盖权限。

完整的路由分组如下：

- `channel.read`：`GET /api/channel/`、`/search`、`/models`、`/models_enabled`、`/ops`、`/:id`、`/:id/codex/usage`、`/:id/codex/usage/reset-credits`、`/tag/models`
- `channel.operate`：`GET /test`、`/test/:id`、`/update_balance`、`/update_balance/:id`、`/fetch_models/:id`；`POST /status/batch`、`/:id/status`、`/tag/disabled`、`/tag/enabled`、`/fix`、`/:id/codex/usage/reset`、`/multi_key/manage`、`/upstream_updates/detect`、`/upstream_updates/detect_all`
- `channel.write`：`PUT /api/channel/`、`PUT /tag`；`POST /batch/tag`、`/upstream_updates/apply`、`/upstream_updates/apply_all`
- `channel.sensitive_write`：`POST /api/channel/`、`/batch`、`/fetch_models`、`/:id/codex/refresh`、`/ollama/pull`、`/ollama/pull/stream`、`/copy/:id`；`DELETE /disabled`、`/:id`、`/ollama/delete`；`GET /ollama/version/:id`

另外有条件式二次检查：

- `PUT /api/channel/` 虽归类为 write，但修改 key、Base URL、proxy、Header/Param Override 等敏感字段时还要求 `sensitive_write`。
- `PUT /api/channel/tag` 修改 Param/Header Override 时还要求 `sensitive_write`。
- `/multi_key/manage` 的 `delete_key`、`delete_disabled_keys` 动作还要求 `sensitive_write`。
- `POST /api/channel/:id/key` 保持 RootAuth，并要求 `channel.key.read` Security Proof；给 Admin 分配 `secret_view` 并不能绕过 RootAuth。

### 6.2 权限目录与用户输出

`GET /api/authz/catalog` 返回：

```json
{
  "success": true,
  "data": {
    "resources": [
      {
        "resource": "channel",
        "label_key": "Channel Management",
        "actions": [
          {
            "action": "read",
            "label_key": "Read channels",
            "description_key": "View channel lists and details without secrets."
          }
        ]
      }
    ],
    "roles": [
      {
        "key": "admin",
        "name": "Admin",
        "built_in": true,
        "superuser": false,
        "grants": { "channel": { "read": true } }
      }
    ]
  }
}
```

用户相关响应变化：

- `/api/user/self` 和所有登录/refresh 的 `user.permissions` 新增 `admin_permissions`
- 管理员查看用户时，用户对象新增 `admin_permissions`
- 创建/更新用户请求可以带 `admin_permissions: {resource: {action: bool}}`，但只有 Root 可设置
- 非管理员用户会清理管理权限配置

`PUT /api/user/` 还收紧了输入：

- `username` trim 后不能为空
- 不再允许通过该通用更新接口修改角色；角色变化应走 `/api/user/manage` 的 promote/demote
- 管理员修改密码、禁用或降级等安全变化会撤销目标用户 Session

### 6.3 渠道状态接口与更新约束

`PUT /api/channel/` 如果请求 JSON 显式包含 `status`，现在按无效参数拒绝。状态切换必须使用新接口。

该通用更新接口还把以下字段明确视为服务端只读字段：`created_time`、`test_time`、`response_time`、`balance`、`balance_updated_time`、`used_quota`。客户端即使显式提交非零值，这些输入也会在持久化前被清除，数据库中的原值保持不变；它们与 `status` 不同，不会触发“参数错误”，而是被静默忽略，成功响应中返回服务端回读后的真实值。

渠道字段权限判定采用 fail-closed 策略：请求 JSON 中出现未被明确分类的新字段时，会先按敏感变更处理。只有 `channel.write`、没有 `channel.sensitive_write` 的管理员会因此被拒绝；即使调用者具备敏感写权限，未映射到渠道 DTO 的未知字段本身仍不会持久化。自动化客户端应只发送目标版本已定义的字段，避免把前端缓存对象或未来版本字段整体回传。

单渠道：

```json
POST /api/channel/123/status
{ "status": 2 }
```

成功 `data` 是 bool，表示数据库状态是否实际改变。

批量：

```json
POST /api/channel/status/batch
{ "ids": [1, 2, 3], "status": 1 }
```

成功 `data` 是实际改变数量。只接受：

- `1`：enabled
- `2`：manually disabled

不允许客户端直接设置 `3`（auto disabled）或 `0`。

`POST /api/channel/batch` 的成功 `data` 由“请求 ID 数量”改为“实际删除行数”。请求中包含不存在 ID 时，返回值可能小于 `ids.length`。

### 6.4 `POST /api/channel/fetch_models`

旧请求只有 `base_url`、`type`、`key`。新请求扩展为：

```json
{
  "channel_id": 12,
  "base_url": "https://provider.example.com",
  "type": 58,
  "key": "secret",
  "advanced_custom": "{\"advanced_routes\":[...]}",
  "header_override": "{\"X-Route-Key\":\"value\"}",
  "proxy": "http://proxy.example.com:8080"
}
```

`advanced_custom` 和 `header_override` 仍是“包含 JSON 对象的字符串”，不是直接嵌套对象；`proxy` 则是普通代理 URL 字符串。各字段的省略/空值语义需要按场景区分：

- 编辑已保存的 Advanced Custom 渠道（提供 `channel_id`）时，省略 `base_url`、`advanced_custom`、`header_override`、`proxy` 会继承保存值。
- 在该编辑预览场景中，显式空字符串可以清空本次预览使用的 `base_url`、`header_override` 或 `proxy`；但 `advanced_custom:""` 是无效输入，会返回 `advanced_custom is required`，不能用来清空配置。
- 提供 `channel_id` 后，请求中的 `key` 不用于覆盖保存密钥；模型发现使用已保存渠道及其当前可用密钥。被引用渠道不是 Advanced Custom 类型时请求失败。
- 新建 Advanced Custom 预览时必须提供 `type=58`（Advanced Custom）、非空 `advanced_custom`，并直接提供所需密钥；没有保存配置可供继承。
- 普通渠道不走上述继承逻辑：未提供 `base_url` 或显式传入空字符串时，都会回退到该渠道类型的内置默认 Base URL；`advanced_custom`、`header_override`、`proxy` 在普通渠道分支中不参与本次模型发现。

高级自定义渠道可以：

- 通过 `channel_id` 复用已保存的渠道配置、密钥和代理
- 通过 `advanced_custom` 预览尚未保存的配置
- 显式配置 incoming path=`/v1/models` 的模型发现路由
- 在开启上游模型更新检查时，必须存在 `/v1/models` route

### 6.5 Advanced Custom 配置

`advanced_routes[]` 新增可选 `models`，支持同一 `incoming_path` 按模型选择不同上游/转换器：

```json
{
  "incoming_path": "/v1/responses",
  "upstream_path": "/v1/chat/completions",
  "converter": "openai_responses_to_openai_chat_completions",
  "models": ["gpt-4.1", "re:^o[0-9]"],
  "auth": {
    "type": "header",
    "name": "Authorization",
    "value": "Bearer {api_key}"
  }
}
```

模型规则支持精确匹配和 `re:` 正则；空 `models` 是该 path 的 catch-all，必须排在该 path 的模型专用 route 之后。重复、无效正则、重复 catch-all、错误 converter/path 组合会在渠道校验时拒绝。

新增 converter：

- `openai_responses_to_openai_chat_completions`
- `openai_responses_to_gemini_generate_content`

`/v1/models` route 不允许 `models`、不允许 converter，且同一配置只能出现一次。

### 6.6 订阅重置

按套餐重置：

```json
POST /api/subscription/admin/plans/8/subscriptions/reset
{ "advance_reset_time": true }
```

按用户与套餐重置：

```json
POST /api/subscription/admin/users/42/subscriptions/reset
{
  "plan_id": 8,
  "advance_reset_time": false
}
```

`advance_reset_time` 省略时默认为 `true`。成功 `data`：

```json
{
  "plan_id": 8,
  "matched_count": 12,
  "reset_count": 10,
  "user_count": 9,
  "advance_reset_time": true
}
```

### 6.7 模型、用户和兑换码查询

- `GET /api/user/` 与 `GET /api/user/search` 新增 `sort_by`、`sort_order`
  - `sort_by`：`id|username|quota|group|created_at|last_login_at`
  - `sort_order`：`asc|desc`；非法值回退 `id desc`
- `GET /api/user/models` 新增/修正 `group` 语义
  - 省略：合并用户所有可用组模型并去重
  - 指定普通组：只返回该可用组模型
  - `group=auto`：按用户配置展开实际组并合并，不再错误只取一个组
  - 无权使用的组返回空数组
- Relay `GET /v1/models` 的 auto-group 合并也统一修正，避免多组模型遗漏
- `GET /api/models/` 与 `GET /api/models/search` 新增 `status`、`sync_official`
  - `status`：`enabled|disabled|1|0|其他整数`；空或 `all` 不过滤
  - `sync_official`：`yes|no|1|0|其他整数`；空或 `all` 不过滤
- `GET /api/models/search` 的 `data` 从通用分页对象改为明确的 `{items,total,page,page_size,vendor_counts}`
- `GET /api/redemption/search` 新增 `status=expired|1|2|3`，分别代表已过期、有效、禁用、已使用

## 7. 异步系统任务与实例 API

### 7.1 日志清理

旧接口同步删除并返回整数 `data=count`。新接口创建/复用 `log_cleanup` 任务，成功立即返回任务对象，清理进度通过系统任务查询。

System Task 通用格式：

```json
{
  "id": 123,
  "task_id": "systask_...",
  "type": "log_cleanup",
  "status": "running",
  "active_key": "log_cleanup",
  "payload": {},
  "state": {},
  "result": null,
  "error": "",
  "locked_by": "node-runner",
  "created_at": 1780000000,
  "updated_at": 1780000010
}
```

状态枚举：`pending|running|succeeded|failed`。

类型枚举：

- `log_cleanup`
- `channel_test`
- `model_update`
- `midjourney_poll`
- `async_task_poll`

`log_cleanup` 的 payload/state/result 分别包含：

- payload：`target_timestamp`、`batch_size`
- state：`total`、`processed`、`progress`、`remaining`
- result：`deleted_count`

`GET /api/system-task/list?limit=N` 默认 20，最大 100。`GET /api/system-task/current?type=...` 没有活动任务时成功返回 `data:null`。

### 7.2 已有渠道接口的同步 → 异步变化

`GET /api/channel/test` 过去启动一个进程内后台循环并只返回 `success:true`；现在入队 `channel_test`，返回：

```json
{
  "success": true,
  "data": {
    "task_id": "systask_...",
    "status": "pending"
  }
}
```

已有同类型 active task 时返回 409，`data` 包含已有任务的 `task_id`、`status`、`type`。

`POST /api/channel/upstream_updates/detect_all` 过去同步返回 `processed_channels`、`failed_channel_ids`、检测模型数和逐渠道结果；现在入队 `model_update` 并只返回 `task_id`、`status`。结果需要查询 System Task，完成摘要字段为：

- `checked_channels`
- `changed_channels`
- `detected_add_models`
- `detected_remove_models`
- `failed_channels`
- `auto_added_models`

已有 active `model_update` 同样返回 409 和已有任务信息。

### 7.3 系统实例

`GET /api/system-info/instances` 单项：

```json
{
  "node_name": "node-a",
  "status": "online",
  "stale_after_seconds": 90,
  "started_at": 1780000000,
  "last_seen_at": 1780000050,
  "info": {}
}
```

`status` 为 `online|stale`，stale 阈值固定返回为 90 秒。两个 DELETE 接口只允许清理 stale 实例，不能删除仍在线实例。

## 8. Relay API 输入变化

### 8.1 `/v1/responses`

新增可解析并按相应渠道规则转发的字段：

- `moderation`
- `prompt_cache_options`
- `client_metadata`
- `reasoning.mode`
- `reasoning.context`

已有可选标量仍保留指针语义：字段缺失会省略，显式 `0`、`0.0`、`false` 不应被 `omitempty` 静默丢弃。

### 8.2 `/v1/responses/compact`

新接受：

- `tools`
- `parallel_tool_calls`
- `reasoning`
- `service_tier`
- `prompt_cache_key`
- `prompt_cache_options`
- `prompt_cache_retention`
- `text`

实际 OpenAI/Codex 转发行为需要注意：`parallel_tool_calls`、`service_tier` 和三个 prompt-cache 字段会进入转换后的上游请求；`tools`、`reasoning`、`text` 当前只是为客户端兼容而解析，不会转发。不能仅因请求未报未知字段，就假设上游实际使用了这三项。

### 8.3 新的输入边界

所有 max-token 系字段上限统一为 `math.MaxInt32 / 2`（1,073,741,823）：

- OpenAI Chat/Completions：`max_tokens`、`max_completion_tokens`
- OpenAI Responses：`max_output_tokens`
- Claude：`max_tokens`、`max_tokens_to_sample`
- Gemini：`generationConfig.maxOutputTokens`

超限在 Relay 校验阶段返回 400，不会进入渠道选择或计费。

图片生成/编辑的 `n` 最大为 128：

- JSON 请求校验 `n > 128`
- multipart 同时拒绝负数、非整数和 `n > 128`
- 缺失或 0 仍归一为默认 1

视频/异步任务请求中的 `duration` 或 `seconds` 最大为 3,600 秒，负数或超过上限返回 400、错误类型 `invalid_seconds`。适配器从 `metadata` 等旁路读取计费乘数时也会做同上限钳制，防止绕过标准 DTO 校验。

## 9. Relay API 输出与流式行为

### 9.1 Responses 字段修正

`incomplete_details` 从错误字段名：

```json
{ "reasoning": "max_output_tokens" }
```

修正为 OpenAI 兼容字段：

```json
{ "reason": "max_output_tokens" }
```

这是必要但破坏性的 schema 修正。读取旧 `reasoning` 的客户端必须迁移到 `reason`。

### 9.2 usage 扩展

OpenAI usage 的 `prompt_tokens_details` / Responses `input_tokens_details` 新增：

```json
{
  "cache_write_tokens": 123
}
```

该值参与 cache-creation 计费；负数按 0 处理，同时存在旧 `cached_creation_tokens` 时取较大值，避免重复计费。

OpenAI、Claude、Gemini usage 都可能新增 `billing_usage`。示例：

```json
{
  "billing_usage": {
    "source": "gemini_chat",
    "semantic": "gemini",
    "estimated": true,
    "gemini_usage_metadata": {
      "promptTokenCount": 10,
      "candidatesTokenCount": 20,
      "totalTokenCount": 30
    }
  }
}
```

`source` 可能为 `claude_messages|gemini_chat|oai_chat|oai_responses`，`semantic` 可能为 `anthropic|gemini|openai`。该字段主要在发生协议转换、需要保留原生计费语义时出现。

普通 JSON 客户端会忽略未知字段；使用 `additionalProperties:false`、固定 generated struct 或响应签名校验的客户端需要显式加入这些字段。

Gemini 上游缺失或返回空 `usageMetadata` 时，目标提交会本地估算 token；若已有输出文本但 completion count 为 0，也会修补。因而客户端现在更可能看到非零 `usageMetadata`，并可能看到 `billing_usage.estimated=true`。

### 9.3 图片 `stream=true`

当客户端要求流式图片、但上游返回普通 JSON 成功响应时，网关现在转换为 SSE，而不是把 JSON 原样返回。每张图片发一个事件：

```text
event: image_generation.completed
data: {"type":"image_generation.completed","created_at":1780000000,"usage":{},"b64_json":"..."}

data: [DONE]
```

`url`、`revised_prompt`、`b64_json` 按上游实际字段携带。非 2xx 上游错误保持 JSON 错误，不伪装成 SSE。客户端中途断开时，网关对已实际发送图片数和计费数量的记录也更严格。

### 9.4 Ollama 工具调用

Ollama Chat 转 OpenAI Chat 的非流式与流式响应现在正确输出 `tool_calls`：

- 生成 `id=call_<index>`
- `type=function`
- arguments 转为 JSON 字符串
- 流式 tool call 带 `index`
- 出现工具调用时 `finish_reason=tool_calls`

此前可能只得到文本或错误的结束原因，依赖 function calling 的客户端会观察到行为改善。

### 9.5 OpenAI Realtime Header

对 `-realtime-preview` 旧模型继续向上游发送 `openai-beta: realtime=v1` 或 WebSocket 子协议 `openai-beta.realtime-v1`；对 GA Realtime 模型不再发送 Beta 标识，避免上游返回 `beta_api_shape_disabled`。

内置 OpenAI 模型清单还增加了 `gpt-realtime-2`、`gpt-realtime-2.1`、`gpt-realtime-2.1-mini`、`gpt-realtime-whisper`、`gpt-realtime-translate` 等条目。实际 `/v1/models` 仍受用户组、渠道能力和模型配置过滤，不保证每个部署都返回全部内置条目。

## 10. Relay 协议转换能力

目标提交引入统一的 request/response/stream converter registry，并新增以下外部可观察能力：

- OpenAI Responses 请求可转 OpenAI Chat Completions 上游，普通 JSON 和 SSE 再转回 Responses
- OpenAI Responses 请求可转 Gemini GenerateContent 上游，普通 JSON 和 SSE 再转回 Responses
- Chat → Responses 转换增强，覆盖 reasoning、工具调用、结束状态和 usage
- Claude Messages、Gemini、OpenAI Chat、OpenAI Responses 的转换由统一注册表选择，并保留转换步骤和原生计费 usage
- Advanced Custom 可显式选择两个新增 Responses converter
- Codex 渠道新增动态模型发现，渠道模型列表不再完全依赖静态常量

转换不是无损透传。尤其 Responses → Chat/Gemini 时，provider 不支持的 Responses 专属字段可能被忽略或近似映射；客户端应按最终 Responses 输出消费，不应假设上游协议和原请求一一对应。

## 11. 视频与任务类接口变化

### 11.1 视频内容代理认证

`GET /v1/videos/:task_id/content` 的路径不变，但面板认证从旧 Gin Session Cookie 改为面板 Access JWT。目标提交接受：

- 面板 Session Access JWT；或
- Relay API Token

不再接受旧面板 Session Cookie。面板 PAT 是面板凭据，不应当作该接口的 Relay Token 使用。

视频 URL 获取加强 SSRF 防护。无代理时采用受保护的解析/连接路径；使用渠道代理时仍在请求前做 URL 校验。过去可以访问而现在命中私网、端口、域名或 IP 规则的 URL，可能返回 403 `server_error`。

### 11.2 豆包 Seedance 与 Ali 视频参数

豆包任务适配器的 `metadata` 现在可映射 `safety_identifier`、`priority`，Seedance 2.0/fast 模型进入支持列表。1080P/4K、视频输入等因素会影响实际计费比率。

Ali wan2.7 新视频请求的 `metadata` 映射扩展，包括：

- `audio_url`
- `img_url`
- `first_frame_url`
- `last_frame_url`
- `media: [{type,url}]`
- `negative_prompt`
- `template`
- `resolution`
- `duration`
- `prompt_extend`
- `watermark`
- `audio`
- `seed`

这些字段主要影响标准任务请求到上游原生格式的转换；公共任务路径未改变。

## 12. 其他外部可观察变化

### 12.1 邮箱、注册与密码重置

- 验证码发送、注册、邮箱绑定、密码重置统一对 email 做 trim + 小写规范化
- 邮箱占用检查改为大小写不敏感，并在绑定/注册写入时再次检查，避免并发重复绑定
- 密码重置遇到不存在或历史重复邮箱时，对外统一表现为无效/过期重置链接，避免泄漏账号状态
- `PUT /api/user/self` 修改密码时必须有已设置的旧密码并正确提供 `original_password`；基准提交允许“账号尚无密码”时直接设置，目标提交会返回“密码未设置”类错误
- `POST /api/user/topup` 的兑换失败不再透出兑换码不存在、已使用等细分原因，统一返回兑换失败

### 12.2 只读 Token 接口

`/api/usage/token/` 和 `/api/log/token` 使用的 `TokenAuthReadOnly` 仍允许过期或额度耗尽 Token 查询自身只读数据，但目标提交开始明确拒绝 `status=disabled` 的 Token，返回 401。基准提交对 disabled Token 也放行。

### 12.3 限流

通用 IP/用户 Redis 限流从基于 List 的近似滑动窗口改为原子固定窗口，并使用新的 `rateLimit:v2` key 命名空间。固定窗口边界两侧理论上可各放行一轮，短时峰值可接近配置阈值的两倍。

通用限流产生的 429 现在带秒数形式的 `Retry-After`。专用中间件仍可能保留自己的 JSON 错误格式，客户端不应假设每个 429 都有完全相同的 body。

用户级模型成功请求限流仍是 List 近似滑动窗口，但时间戳改为 UTC。滚动升级期间旧本地时间和新 UTC 值可能在一个窗口内造成临时误放行或误拒绝。

### 12.4 状态、主题与支付回跳

- `GET /api/status` 的 `theme` 固定返回 `default`
- `GET /api/option/` 不再返回 `theme.frontend`
- 更新 `theme.frontend=classic` 会失败，只接受 `default`
- Classic 前端相关回跳路径删除，支付回跳改为新路由：
  - `/console/topup` → `/wallet`
  - `/console/log` → `/usage-logs`
  - 订阅 Epay 的成功/失败/等待 query 保留，但挂在 `/wallet`
- `GET /api/user/topup/info` 中 Stripe、Waffo Pancake、Waffo 的 `color` 从 Semi Design 的 `rgba(var(...))` 字符串改为十六进制颜色

支付接口 JSON 主体未因此改变，但浏览器和支付网关观察到的 `Location`/SuccessURL/CancelURL 已变化。

## 13. OpenAPI 文档覆盖情况

### 13.1 `docs/openapi/api.json`

目标 OpenAPI 已记录：

- refresh/logout
- Session 列表、删除、撤销其他 Session
- Telegram 两阶段绑定
- OAuth state POST
- System Task 日志清理创建接口
- 部分登录、Proof、Passkey 请求/响应变化

但代码中真实存在、目标 OpenAPI 尚未收录的新增接口包括：

- `/api/system-task/list`
- `/api/system-task/current`
- `/api/system-task/:task_id`
- `/api/system-info/instances`
- `/api/system-info/stale-instances`
- `/api/system-info/instances/:node_name`
- `/api/authz/catalog`
- 两个订阅重置接口
- 两个渠道状态接口

OpenAPI 也未完整表达已有接口的异步响应变化、细粒度权限、条件式敏感字段权限和部分管理查询参数。

### 13.2 `docs/openapi/relay.json`

Relay OpenAPI 路径/方法集合没有变化，但实际 DTO/适配器支持的 Responses 新字段、`billing_usage`、`cache_write_tokens`、图片 JSON→SSE 转换和各类边界校验未被完整描述。生成 SDK 时必须补充手工 schema 或直接以目标代码 DTO 契约为准。

## 14. 部署与数据迁移影响

鉴权升级会新增或使用：

- `user_sessions`
- `auth_flows`
- `external_identity_claims`
- `users.auth_version`
- Casbin/授权策略相关表
- System Task 与 System Instance 表

部署要求：

1. 所有节点必须配置相同且高强度的 `SESSION_SECRET`；更换会使 Access JWT、Refresh、AuthFlow、Security Proof 全部失效。
2. 多节点建议共享 Redis；独立 Redis 时 Session 撤销/版本传播存在最多约一个 `SYNC_FREQUENCY` 的缓存收敛窗口。
3. 共享 Redis 的节点还必须统一 `CRYPTO_SECRET`，否则缓存键摘要不一致。
4. 生产环境建议 `SESSION_COOKIE_SECURE=true` 并配置精确的 `SESSION_COOKIE_TRUSTED_URL`。
5. 反向代理部署应正确配置 `TRUSTED_PROXIES`。未配置时默认只信任回环和常见私网 CIDR；`none` 表示不信任任何代理头。
6. 旧面板 Session 无法迁移，发布后应明确通知用户重新登录。

## 15. 客户端迁移清单

### 面板/自建控制台

1. 删除 `New-Api-User`。
2. 登录后读取完整 AuthBundle，在内存保存 Access JWT 和 SID。
3. 所有面板请求发送 `Authorization: Bearer <jwt>`。
4. refresh/logout 使用 `credentials: include`，并在已知 SID 时发送 `X-Auth-Session`。
5. 遇到 `AUTH_TOKEN_EXPIRED` 先 refresh；遇到 `AUTH_SESSION_REVOKED` 或确认 Refresh 无效再清除登录态。
6. 密码、2FA、Passkey 变更成功后立即替换响应中的 JWT/SID。
7. OAuth state 改成 POST JSON；bind 流程带 Session JWT。
8. 2FA 和 Passkey 按 `flow_token` 驱动，不再依赖 Cookie 中的 pending 状态。
9. 敏感操作先按最终 scope 获取 Proof，再发送 `X-Security-Proof`。
10. 根据 `admin_permissions` 隐藏或禁用无权操作；同时仍以服务端 403 为最终判定。

### 管理自动化脚本

1. PAT 可继续使用，推荐改成 Bearer，并删除用户 ID 头。
2. PAT 不能做 Session 管理和 Security Proof；渠道密钥查看等流程必须使用浏览器 Session JWT。
3. 渠道状态不再放入 `PUT /api/channel/`，改用专用状态接口。
4. 日志清理改为 Root 的 System Task，轮询任务状态直到 terminal。
5. 全渠道测试和 detect-all 读取 `task_id`，不能再期待同步结果。
6. 批量删除渠道应把返回值解释为实际删除数。
7. 模型搜索分页 DTO 更新为显式字段，并处理 `vendor_counts`。

### Relay 客户端

1. 严格 schema 加入 `billing_usage`、`cache_write_tokens`。
2. Responses 改读 `incomplete_details.reason`。
3. 图片 `stream=true` 同时支持原生 SSE 和网关由 JSON 转换的 SSE，并处理 `[DONE]`。
4. max-token、图片 `n`、视频 duration 不得超过新上限。
5. 允许 Ollama 返回 `tool_calls` 和 `finish_reason=tool_calls`。
6. 不要假设 `/v1/responses/compact` 所有被接受字段都会转发，特别是 `tools`、`reasoning`、`text`。

## 16. 明确未变化的关键契约

- Relay 主路径和 HTTP 方法集合未增删。
- Relay API Token 的主流写法仍是 `Authorization: Bearer <sk-token>`；Anthropic/Gemini 兼容的 `x-api-key`、`x-goog-api-key`/`key` 入口仍保留。
- `/api/oauth/email/bind` 与 `/api/oauth/wechat/bind` 的实际方法原本就是 POST；变化是鉴权和 OpenAPI 修正。
- 绝大多数旧管理 API 仍使用 `{success,message,data}` 包装；本次没有一次性统一所有历史错误状态码。
- 数据库继续要求同时兼容 SQLite、MySQL 和 PostgreSQL；System Task、Session、授权等新表不是单一数据库特性。

---

综合判断：本次升级应按“面板认证协议大版本升级 + 管理授权收紧 + Relay 兼容能力扩展”处理，而不应视作普通的向后兼容合并。若存在自建面板、管理脚本或严格类型 Relay SDK，应在部署目标提交前完成联调和回归。
