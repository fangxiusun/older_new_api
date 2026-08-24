# New API — 接口说明文档

> 生成时间：2026-06-16
> 追踪方式：基于 `router/*.go` 和 `controller/*.go` 源码逐路由提取
> 所有管理 API 响应格式均为 `{ "success": bool, "message": string, "data": any }`

---

## 目录

1. [通用约定](#1-通用约定)
2. [中继 API（OpenAI 兼容）](#2-中继-apiopenai-兼容)
3. [业务 API — 公开端点](#3-业务-api--公开端点)
4. [业务 API — 用户认证端点](#4-业务-api--用户认证端点)
5. [业务 API — 管理员端点](#5-业务-api--管理员端点)
6. [Dashboard API（Token 认证）](#6-dashboard-apitoken-认证)
7. [视频 API](#7-视频-api)
8. [Webhook 端点](#8-webhook-端点)
9. [错误码参考](#9-错误码参考)

---

## 1. 通用约定

### 1.1 管理 API 响应格式

所有 `/api/*` 业务 API 统一使用以下 JSON 格式：

**成功响应**：
```json
{
  "success": true,
  "message": "",
  "data": { ... }
}
```

**错误响应**：
```json
{
  "success": false,
  "message": "错误描述（支持 i18n）",
  "data": null
}
```

对应函数：`common.ApiSuccess(c, data)` / `common.ApiError(c, err)` / `common.ApiErrorI18n(c, msgKey)`

### 1.2 中继 API 响应格式

`/v1/*` 中继 API 遵循 OpenAI 兼容格式：

**成功响应**：直接返回上游提供商的响应（OpenAI/Claude/Gemini 格式）

**错误响应**：
```json
{
  "error": {
    "message": "错误描述",
    "type": "error_type",
    "param": null,
    "code": "error_code"
  }
}
```

### 1.3 认证方式

| 认证方式 | 中间件 | 适用范围 | 传递方式 |
|---|---|---|---|
| **Session (Cookie)** | `UserAuth()` / `AdminAuth()` | `/api/*` 管理接口 | Cookie: `session` |
| **Access Token** | `UserAuth()` | `/api/*` 管理接口 | Header: `Authorization: Bearer <token>` |
| **API Token** | `TokenAuth()` | `/v1/*` 中继接口 | Header: `Authorization: Bearer sk-<key>` |
| **Token 只读** | `TokenAuthReadOnly()` | `/api/log/token` | Header: `Authorization: Bearer sk-<key>` |
| **Token 或用户** | `TokenOrUserAuth()` | `/v1/videos/:task_id/content` | 上述任一方式 |

### 1.4 分页参数

大多数列表接口支持以下分页查询参数：

| 参数 | 类型 | 默认值 | 说明 |
|---|---|---|---|
| `p` | int | 1 | 页码 |
| `size` | int | 10 | 每页条数 |
| `sort_by` | string | — | 排序字段 |
| `sort_order` | string | — | 排序方向（asc/desc） |

响应中 `data` 为分页对象：`{ "items": [...], "total": int, "page": int, "size": int }`

### 1.5 全局中间件

所有 `/api/*` 路由经过：
- `RouteTag("api")` — 路由标签
- `gzip.Gzip()` — GZIP 压缩
- `BodyStorageCleanup()` — 请求体清理
- `GlobalAPIRateLimit()` — 全局速率限制

### 1.6 处理函数文件路径约定

本文档中所有处理函数均位于 `controller/` 目录下，格式为 `controller/<文件名>.go`。

---

## 2. 中继 API（OpenAI 兼容）

**基础路径**：`/v1`
**认证方式**：`TokenAuth()` — Bearer Token (`sk-xxx`)
**额外中间件**：`SystemPerformanceCheck()` → `TokenAuth()` → `ModelRequestRateLimit()` → `Distribute()`

### 2.1 聊天补全

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/v1/chat/completions` | `controller.Relay(OpenAI)` | OpenAI 兼容聊天补全 |
| POST | `/v1/completions` | `controller.Relay(OpenAI)` | 旧版补全接口 |

**请求体**（`dto.GeneralOpenAIRequest`）：
```json
{
  "model": "gpt-4o",
  "messages": [{"role": "user", "content": "Hello"}],
  "stream": false,
  "temperature": 0.7,
  "top_p": 1.0,
  "max_tokens": 4096,
  "stream_options": {"include_usage": true}
}
```

**处理函数**：`controller/relay.go` → `relay/compatible_handler.go:TextHelper()`

### 2.2 Claude 消息

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/v1/messages` | `controller.Relay(Claude)` | Anthropic Claude 格式 |

**请求体**（`dto.ClaudeRequest`）：
```json
{
  "model": "claude-sonnet-4-20250514",
  "messages": [{"role": "user", "content": "Hello"}],
  "max_tokens": 4096,
  "stream": false
}
```

**处理函数**：`controller/relay.go` → `relay/claude_handler.go:ClaudeHelper()`

### 2.3 Responses API

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/v1/responses` | `controller.Relay(OpenAIResponses)` | OpenAI Responses API |
| POST | `/v1/responses/compact` | `controller.Relay(OpenAIResponsesCompaction)` | Responses 压缩模式 |

**处理函数**：`controller/relay.go` → `relay/responses_handler.go:ResponsesHelper()`

### 2.4 嵌入向量

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/v1/embeddings` | `controller.Relay(Embedding)` | 文本嵌入 |

**请求体**（`dto.EmbeddingRequest`）：
```json
{
  "model": "text-embedding-3-small",
  "input": "Hello world"
}
```

**处理函数**：`controller/relay.go` → `relay/embedding_handler.go:EmbeddingHelper()`

### 2.5 图片生成

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/v1/images/generations` | `controller.Relay(Image)` | DALL-E 等图片生成 |
| POST | `/v1/images/edits` | `controller.Relay(Image)` | 图片编辑 |
| POST | `/v1/edits` | `controller.Relay(Image)` | 旧版编辑接口 |

**处理函数**：`controller/relay.go` → `relay/image_handler.go:ImageHelper()`

### 2.6 音频

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/v1/audio/speech` | `controller.Relay(Audio)` | TTS 语音合成 |
| POST | `/v1/audio/transcriptions` | `controller.Relay(Audio)` | Whisper 语音转文字 |
| POST | `/v1/audio/translations` | `controller.Relay(Audio)` | Whisper 语音翻译 |

**处理函数**：`controller/relay.go` → `relay/audio_handler.go:AudioHelper()`

### 2.7 重排序

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/v1/rerank` | `controller.Relay(Rerank)` | 文本重排序 |

**处理函数**：`controller/relay.go` → `relay/rerank_handler.go:RerankHelper()`

### 2.8 Gemini 原生接口

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/v1/models/*path` | `controller.Relay(Gemini)` | Gemini 格式（兼容路径） |
| POST | `/v1/engines/:model/embeddings` | `controller.Relay(Gemini)` | Gemini 嵌入 |
| POST | `/v1beta/models/*path` | `controller.Relay(Gemini)` | Gemini 原生路径 |

**处理函数**：`controller/relay.go` → `relay/gemini_handler.go:GeminiHelper()`

### 2.9 WebSocket

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/v1/realtime` | `controller.Relay(OpenAIRealtime)` | WebSocket 实时通信 |

**处理函数**：`controller/relay.go` → WebSocket 升级后走 `relay` 层

### 2.10 模型列表

| 方法 | 路径 | Handler | 认证 | 说明 |
|---|---|---|---|---|
| GET | `/v1/models` | `controller.ListModels` | TokenAuth | 列出可用模型 |
| GET | `/v1/models/:model` | `controller.RetrieveModel` | TokenAuth | 查询单个模型 |
| GET | `/v1beta/models` | `controller.ListModels` | TokenAuth | Gemini 格式模型列表 |

**处理函数**：`controller/model.go`

### 2.11 Playground

| 方法 | 路径 | Handler | 认证 | 说明 |
|---|---|---|---|---|
| POST | `/pg/chat/completions` | `controller.Playground` | UserAuth + Distribute | Playground 聊天 |

**处理函数**：`controller/playground.go`

---

## 3. 业务 API — 公开端点

**基础路径**：`/api`
**认证方式**：无需认证

### 3.1 系统状态

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/status` | `controller.GetStatus` | 系统状态 |
| GET | `/api/status/test` | `controller.TestStatus` | 管理员测试状态（需 AdminAuth） |
| GET | `/api/uptime/status` | `controller.GetUptimeKumaStatus` | Uptime Kuma 状态 |

**`GET /api/status` 响应**：
```json
{
  "success": true,
  "data": {
    "version": "x.x.x",
    "setup": true
  }
}
```

**处理函数**：`controller/misc.go`

### 3.2 系统初始化

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/setup` | `controller.GetSetup` | 获取初始化状态 |
| POST | `/api/setup` | `controller.PostSetup` | 执行初始化向导 |

**`POST /api/setup` 请求体**：
```json
{
  "username": "root",
  "password": "newpassword"
}
```

**处理函数**：`controller/setup.go`

### 3.3 公共内容

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/notice` | `controller.GetNotice` | 获取公告 |
| GET | `/api/user-agreement` | `controller.GetUserAgreement` | 用户协议 |
| GET | `/api/privacy-policy` | `controller.GetPrivacyPolicy` | 隐私政策 |
| GET | `/api/about` | `controller.GetAbout` | 关于页面 |
| GET | `/api/home_page_content` | `controller.GetHomePageContent` | 首页内容 |
| GET | `/api/pricing` | `controller.GetPricing` | 定价信息（需 HeaderNavModuleAuth） |
| GET | `/api/rankings` | `controller.GetRankings` | 排行榜（需 HeaderNavModuleAuth） |
| GET | `/api/ratio_config` | `controller.GetRatioConfig` | 比率配置 |

**处理函数**：`controller/misc.go`, `controller/pricing.go`, `controller/rankings.go`

### 3.4 性能指标

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/perf-metrics/summary` | `controller.GetPerfMetricsSummary` | 性能指标摘要 |
| GET | `/api/perf-metrics` | `controller.GetPerfMetrics` | 性能指标详情 |

**认证**：`HeaderNavModulePublicOrUserAuth("pricing")`，由 option `HeaderNavModules.pricing.enabled/requireAuth` 决定是否可匿名；详见 `docs/llm-docs/29-perf-metrics-summary-auth.md`。

**查询参数**：`hours`（summary）；`model`、`group`、`hours`（detail）

**处理函数**：`controller/perf_metrics.go`

### 3.5 邮件验证 & 密码重置

| 方法 | 路径 | 中间件 | Handler | 说明 |
|---|---|---|---|---|
| GET | `/api/verification` | EmailRateLimit + Turnstile | `controller.SendEmailVerification` | 发送邮箱验证码 |
| GET | `/api/reset_password` | CriticalRateLimit + Turnstile | `controller.SendPasswordResetEmail` | 发送密码重置邮件 |
| POST | `/api/user/reset` | CriticalRateLimit | `controller.ResetPassword` | 重置密码 |

**`POST /api/user/reset` 请求体**：
```json
{
  "email": "user@example.com",
  "verification_code": "123456",
  "password": "new_password"
}
```

**处理函数**：`controller/user.go`

### 3.6 OAuth 登录

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/oauth/state` | `controller.GenerateOAuthCode` | 生成 OAuth state |
| GET | `/api/oauth/:provider` | `controller.HandleOAuth` | 统一 OAuth 回调（GitHub/Discord/OIDC/LinuxDo） |
| GET | `/api/oauth/wechat` | `controller.WeChatAuth` | 微信 OAuth |
| POST | `/api/oauth/wechat/bind` | `controller.WeChatBind` | 微信绑定 |
| GET | `/api/oauth/telegram/login` | `controller.TelegramLogin` | Telegram 登录 |
| GET | `/api/oauth/telegram/bind` | `controller.TelegramBind` | Telegram 绑定 |
| POST | `/api/oauth/email/bind` | `controller.EmailBind` | 邮箱绑定 |

**处理函数**：`controller/oauth.go`, `controller/github.go`, `controller/discord.go`, `controller/oidc.go`, `controller/wechat.go`, `controller/telegram.go`, `controller/custom_oauth.go`

### 3.7 支付回调（Webhook）

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/api/stripe/webhook` | `controller.StripeWebhook` | Stripe 支付回调 |
| POST | `/api/creem/webhook` | `controller.CreemWebhook` | Creem 支付回调 |
| POST | `/api/waffo/webhook` | `controller.WaffoWebhook` | Waffo 支付回调 |
| POST | `/api/waffo-pancake/webhook/:env` | `controller.WaffoPancakeWebhook` | Waffo Pancake 回调 |
| POST/GET | `/api/user/epay/notify` | `controller.EpayNotify` | 易支付回调 |

**处理函数**：`controller/topup_stripe.go`, `controller/topup_creem.go`, `controller/topup_waffo.go`, `controller/topup_waffo_pancake.go`, `controller/topup.go`

---

## 4. 业务 API — 用户认证端点

**基础路径**：`/api`
**认证方式**：`UserAuth()` — Session Cookie 或 Access Token

### 4.1 用户认证

| 方法 | 路径 | 中间件 | Handler | 说明 |
|---|---|---|---|---|
| POST | `/api/user/register` | CriticalRateLimit + Turnstile | `controller.Register` | 用户注册 |
| POST | `/api/user/login` | CriticalRateLimit + Turnstile | `controller.Login` | 用户登录 |
| POST | `/api/user/login/2fa` | CriticalRateLimit | `controller.Verify2FALogin` | 2FA 验证 |
| POST | `/api/user/passkey/login/begin` | CriticalRateLimit | `controller.PasskeyLoginBegin` | Passkey 登录开始 |
| POST | `/api/user/passkey/login/finish` | CriticalRateLimit | `controller.PasskeyLoginFinish` | Passkey 登录完成 |
| GET | `/api/user/logout` | — | `controller.Logout` | 用户登出 |

**`POST /api/user/register` 请求体**：
```json
{
  "username": "newuser",
  "password": "password123",
  "email": "user@example.com",
  "verification_code": "123456",
  "aff_code": "INVITE_CODE"
}
```

**`POST /api/user/login` 请求体**：
```json
{
  "username": "user",
  "password": "password123"
}
```

**`POST /api/user/login` 响应**（2FA 未启用时）：
```json
{
  "success": true,
  "data": {
    "id": 1,
    "username": "user",
    "role": 1
  }
}
```

**`POST /api/user/login` 响应**（2FA 启用时）：
```json
{
  "success": false,
  "message": "2FA verification required",
  "data": {
    "require_2fa": true
  }
}
```

**处理函数**：`controller/user.go`

### 4.2 用户信息

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/user/self` | `controller.GetSelf` | 获取当前用户信息 |
| PUT | `/api/user/self` | `controller.UpdateSelf` | 更新当前用户信息 |
| DELETE | `/api/user/self` | `controller.DeleteSelf` | 删除当前用户 |
| GET | `/api/user/models` | `controller.GetUserModels` | 获取用户可用模型列表 |
| PUT | `/api/user/setting` | `controller.UpdateUserSetting` | 更新用户个性化设置 |
| POST | `/api/user/verify` | `controller.UniversalVerify` | 通用安全验证 |

**`GET /api/user/self` 响应**：
```json
{
  "success": true,
  "data": {
    "id": 1,
    "username": "user",
    "display_name": "User",
    "role": 1,
    "status": 1,
    "email": "user@example.com",
    "quota": 1000000,
    "used_quota": 50000,
    "request_count": 100,
    "group": "default"
  }
}
```

**处理函数**：`controller/user.go`

### 4.3 Token 管理

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/token/` | `controller.GetAllTokens` | 获取当前用户所有 Token |
| GET | `/api/token/search` | `controller.SearchTokens` | 搜索 Token |
| GET | `/api/token/:id` | `controller.GetToken` | 获取单个 Token |
| GET | `/api/token/key` | `controller.GetTokenByKey` | 按 Key 查询 Token |
| POST | `/api/token/` | `controller.AddToken` | 创建 Token |
| PUT | `/api/token/` | `controller.UpdateToken` | 更新 Token |
| DELETE | `/api/token/:id` | `controller.DeleteToken` | 删除 Token |
| DELETE | `/api/token/batch` | `controller.DeleteTokenBatch` | 批量删除 |

**`POST /api/token/` 请求体**：
```json
{
  "name": "My API Key",
  "remain_quota": 1000000,
  "unlimited_quota": false,
  "expired_time": -1,
  "model_limits_enabled": true,
  "model_limits": "gpt-4o,claude-sonnet-4-20250514",
  "group": "default"
}
```

**`GET /api/token/` 响应**（列表中的 Token key 已脱敏）：
```json
{
  "success": true,
  "data": {
    "items": [
      {
        "id": 1,
        "name": "My API Key",
        "key": "sk-abcd**********efgh",
        "remain_quota": 1000000,
        "status": 1
      }
    ],
    "total": 1
  }
}
```

**处理函数**：`controller/token.go`

### 4.4 日志查询

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/log/self` | `controller.GetUserLogs` | 获取当前用户日志 |
| GET | `/api/log/self/search` | `controller.SearchUserLogs` | 搜索用户日志（需 SearchRateLimit） |
| GET | `/api/log/self/stat` | `controller.GetLogsSelfStat` | 用户日志统计 |
| GET | `/api/log/token` | `controller.GetLogByKey` | 按 Token Key 查询日志（需 TokenAuthReadOnly） |

**查询参数**：`p`, `size`, `model_name`, `type`, `start_timestamp`, `end_timestamp`, `token_id`

**处理函数**：`controller/log.go`

### 4.5 用量数据

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/data/self` | `controller.GetUserQuotaDates` | 用户用量统计 |

**处理函数**：`controller/usedata.go`

### 4.6 签到

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/user/checkin` | `controller.Checkin` | 用户签到 |
| GET | `/api/user/checkin/status` | `controller.GetCheckinStatus` | 签到状态 |

**处理函数**：`controller/checkin.go`

### 4.7 充值

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/topup/info` | `controller.GetTopUpInfo` | 获取充值配置（价格/最小金额等） |
| POST | `/api/topup/` | `controller.TopUp` | 发起充值（返回支付链接） |
| GET | `/api/topup/self` | `controller.GetUserTopUps` | 获取用户充值记录 |

**`POST /api/topup/` 请求体**：
```json
{
  "amount": 1000000,
  "payment_method": "stripe"
}
```

**处理函数**：`controller/topup.go`, `controller/topup_stripe.go`, `controller/topup_creem.go`, `controller/topup_waffo.go`

### 4.8 订阅

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/subscription/self` | `controller.GetUserSubscription` | 获取当前用户订阅 |
| GET | `/api/subscription/plans` | `controller.GetSubscriptionPlans` | 获取订阅计划列表 |
| POST | `/api/subscription/subscribe` | `controller.SubscribePlan` | 订阅计划 |

**处理函数**：`controller/subscription.go`, `controller/subscription_payment_stripe.go`, `controller/subscription_payment_creem.go`

### 4.9 任务查询

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/task/self` | `controller.GetUserTask` | 获取当前用户任务 |
| GET | `/api/mj/self` | `controller.GetUserMidjourney` | 获取当前用户 Midjourney 任务 |

**处理函数**：`controller/task.go`, `controller/midjourney.go`

### 4.10 2FA 管理

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/user/2fa/status` | `controller.Get2FAStatus` | 获取 2FA 状态 |
| POST | `/api/user/2fa/setup` | `controller.Setup2FA` | 初始化 2FA |
| POST | `/api/user/2fa/enable` | `controller.Enable2FA` | 启用 2FA |
| POST | `/api/user/2fa/disable` | `controller.Disable2FA` | 禁用 2FA |

**处理函数**：`controller/twofa.go`

---

## 5. 业务 API — 管理员端点

**基础路径**：`/api`
**认证方式**：`AdminAuth()` — 需要管理员或 Root 角色

### 5.1 用户管理

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/user/` | `controller.GetAllUsers` | 获取所有用户 |
| GET | `/api/user/search` | `controller.SearchUsers` | 搜索用户 |
| GET | `/api/user/:id` | `controller.GetUser` | 获取单个用户 |
| POST | `/api/user/` | `controller.CreateUser` | 创建用户 |
| PUT | `/api/user/` | `controller.UpdateUser` | 更新用户 |
| DELETE | `/api/user/:id` | `controller.DeleteUser` | 删除用户 |
| POST | `/api/user/token` | `controller.GenerateAccessToken` | 生成 Access Token |
| GET | `/api/user/aff` | `controller.GetAffCode` | 获取邀请码 |

**处理函数**：`controller/user.go`

### 5.2 渠道管理

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/channel/` | `controller.GetAllChannels` | 获取所有渠道 |
| GET | `/api/channel/search` | `controller.SearchChannels` | 搜索渠道 |
| GET | `/api/channel/:id` | `controller.GetChannel` | 获取单个渠道 |
| GET | `/api/channel/:id/key` | `controller.GetChannelKey` | 获取渠道 Key |
| GET | `/api/channel/tags/models` | `controller.GetTagModels` | 获取标签模型 |
| POST | `/api/channel/` | `controller.AddChannel` | 创建渠道 |
| PUT | `/api/channel/` | `controller.UpdateChannel` | 更新渠道 |
| DELETE | `/api/channel/:id` | `controller.DeleteChannel` | 删除渠道 |
| DELETE | `/api/channel/disabled` | `controller.DeleteDisabledChannel` | 删除所有禁用渠道 |
| DELETE | `/api/channel/batch` | `controller.DeleteChannelBatch` | 批量删除渠道 |
| POST | `/api/channel/test` | `controller.TestChannel` | 测试单个渠道 |
| POST | `/api/channel/testall` | `controller.TestAllChannels` | 测试所有渠道 |
| PUT | `/api/channel/balance` | `controller.UpdateChannelBalance` | 更新渠道余额 |
| PUT | `/api/channel/balance_all` | `controller.UpdateAllChannelsBalance` | 更新所有渠道余额 |

**处理函数**：`controller/channel.go`, `controller/channel-test.go`, `controller/channel-billing.go`

### 5.3 系统配置

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/option/` | `controller.GetOptions` | 获取所有配置项 |
| PUT | `/api/option/` | `controller.UpdateOption` | 更新配置项 |
| POST | `/api/option/` | `controller.UpdateOption` | 更新配置项 |

**处理函数**：`controller/option.go`

### 5.4 兑换码管理

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/redemption/` | `controller.GetAllRedemptions` | 获取所有兑换码 |
| GET | `/api/redemption/search` | `controller.SearchRedemptions` | 搜索兑换码 |
| GET | `/api/redemption/:id` | `controller.GetRedemption` | 获取单个兑换码 |
| POST | `/api/redemption/` | `controller.AddRedemption` | 创建兑换码 |
| PUT | `/api/redemption/` | `controller.UpdateRedemption` | 更新兑换码 |
| DELETE | `/api/redemption/:id` | `controller.DeleteRedemption` | 删除兑换码 |
| DELETE | `/api/redemption/invalid` | `controller.DeleteInvalidRedemption` | 删除无效兑换码 |

**处理函数**：`controller/redemption.go`

### 5.5 日志管理

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/log/` | `controller.GetAllLogs` | 获取所有日志 |
| GET | `/api/log/search` | `controller.SearchAllLogs` | 搜索日志 |
| GET | `/api/log/stat` | `controller.GetLogsStat` | 日志统计 |
| DELETE | `/api/log/` | `controller.DeleteHistoryLogs` | 删除历史日志 |
| GET | `/api/log/channel_affinity_usage_cache` | `controller.GetChannelAffinityUsageCacheStats` | 渠道亲和性缓存统计 |

**处理函数**：`controller/log.go`, `controller/channel_affinity_cache.go`

### 5.6 用量数据

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/data/` | `controller.GetAllQuotaDates` | 所有用量统计 |
| GET | `/api/data/users` | `controller.GetQuotaDatesByUser` | 按用户统计 |

**处理函数**：`controller/usedata.go`

### 5.7 分组管理

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/group/` | `controller.GetGroups` | 获取所有分组 |

**处理函数**：`controller/group.go`

### 5.8 预填充分组

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/prefill_group/` | `controller.GetPrefillGroups` | 获取所有预填充分组 |
| POST | `/api/prefill_group/` | `controller.CreatePrefillGroup` | 创建预填充分组 |
| PUT | `/api/prefill_group/` | `controller.UpdatePrefillGroup` | 更新预填充分组 |
| DELETE | `/api/prefill_group/:id` | `controller.DeletePrefillGroup` | 删除预填充分组 |

**处理函数**：`controller/prefill_group.go`

### 5.9 Midjourney & 任务

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/mj/` | `controller.GetAllMidjourney` | 获取所有 Midjourney 任务 |
| GET | `/api/task/` | `controller.GetAllTask` | 获取所有异步任务 |

**处理函数**：`controller/midjourney.go`, `controller/task.go`

### 5.10 供应商管理

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/vendors/` | `controller.GetAllVendors` | 获取所有供应商 |
| GET | `/api/vendors/search` | `controller.SearchVendors` | 搜索供应商 |
| GET | `/api/vendors/:id` | `controller.GetVendorMeta` | 获取单个供应商 |
| POST | `/api/vendors/` | `controller.CreateVendorMeta` | 创建供应商 |
| PUT | `/api/vendors/` | `controller.UpdateVendorMeta` | 更新供应商 |
| DELETE | `/api/vendors/:id` | `controller.DeleteVendorMeta` | 删除供应商 |

**处理函数**：`controller/vendor_meta.go`

### 5.11 模型管理

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/models/` | `controller.GetAllModelsMeta` | 获取所有模型元数据 |
| GET | `/api/models/search` | `controller.SearchModelsMeta` | 搜索模型 |
| GET | `/api/models/:id` | `controller.GetModelMeta` | 获取单个模型 |
| POST | `/api/models/` | `controller.CreateModelMeta` | 创建模型 |
| PUT | `/api/models/` | `controller.UpdateModelMeta` | 更新模型 |
| DELETE | `/api/models/:id` | `controller.DeleteModelMeta` | 删除模型 |
| GET | `/api/models/missing` | `controller.GetMissingModels` | 获取缺失模型 |
| GET | `/api/models/sync_upstream/preview` | `controller.SyncUpstreamPreview` | 同步上游预览 |
| POST | `/api/models/sync_upstream` | `controller.SyncUpstreamModels` | 同步上游模型 |

**处理函数**：`controller/model_meta.go`

### 5.12 部署管理（IONet）

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/deployments/` | `controller.GetAllDeployments` | 获取所有部署 |
| GET | `/api/deployments/search` | `controller.SearchDeployments` | 搜索部署 |
| GET | `/api/deployments/:id` | `controller.GetDeployment` | 获取单个部署 |
| POST | `/api/deployments/` | `controller.CreateDeployment` | 创建部署 |
| PUT | `/api/deployments/:id` | `controller.UpdateDeployment` | 更新部署 |
| PUT | `/api/deployments/:id/name` | `controller.UpdateDeploymentName` | 更新部署名称 |
| POST | `/api/deployments/:id/extend` | `controller.ExtendDeployment` | 续期部署 |
| DELETE | `/api/deployments/:id` | `controller.DeleteDeployment` | 删除部署 |
| GET | `/api/deployments/:id/logs` | `controller.GetDeploymentLogs` | 获取部署日志 |
| GET | `/api/deployments/:id/containers` | `controller.ListDeploymentContainers` | 列出容器 |
| GET | `/api/deployments/:id/containers/:cid` | `controller.GetContainerDetails` | 容器详情 |
| GET | `/api/deployments/settings` | `controller.GetModelDeploymentSettings` | 部署设置 |
| POST | `/api/deployments/settings/test-connection` | `controller.TestIoNetConnection` | 测试连接 |
| POST | `/api/deployments/test-connection` | `controller.TestIoNetConnection` | 测试连接 |
| GET | `/api/deployments/hardware-types` | `controller.GetHardwareTypes` | 硬件类型 |
| GET | `/api/deployments/locations` | `controller.GetLocations` | 可用区域 |
| GET | `/api/deployments/available-replicas` | `controller.GetAvailableReplicas` | 可用副本数 |
| POST | `/api/deployments/price-estimation` | `controller.GetPriceEstimation` | 价格估算 |
| GET | `/api/deployments/check-name` | `controller.CheckClusterNameAvailability` | 检查名称可用性 |

**处理函数**：`controller/deployment.go`

### 5.13 自定义 OAuth 管理

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/oauth/custom` | `controller.GetCustomOAuthProviders` | 获取自定义 OAuth 列表 |
| GET | `/api/oauth/custom/:id` | `controller.GetCustomOAuthProvider` | 获取单个配置 |
| POST | `/api/oauth/custom` | `controller.CreateCustomOAuthProvider` | 创建自定义 OAuth |
| PUT | `/api/oauth/custom/:id` | `controller.UpdateCustomOAuthProvider` | 更新自定义 OAuth |
| DELETE | `/api/oauth/custom/:id` | `controller.DeleteCustomOAuthProvider` | 删除自定义 OAuth |
| GET | `/api/oauth/bindings/user/:id` | `controller.GetUserOAuthBindingsByAdmin` | 管理员查看用户绑定 |
| GET | `/api/oauth/bindings/self` | `controller.GetUserOAuthBindings` | 用户查看自己的绑定 |

**处理函数**：`controller/custom_oauth.go`

### 5.14 Codex 使用量

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/codex/usage` | `controller.GetCodexChannelUsage` | 获取 Codex 渠道使用量 |

**处理函数**：`controller/codex_usage.go`

---

## 6. Dashboard API（Token 认证）

**基础路径**：`/dashboard` 和 `/v1/dashboard`
**认证方式**：`TokenAuth()` — Bearer Token
**用途**：OpenAI 兼容的 Billing Dashboard 接口

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/dashboard/billing/subscription` | `controller.GetSubscription` | 订阅信息 |
| GET | `/v1/dashboard/billing/subscription` | `controller.GetSubscription` | 订阅信息（v1 路径） |
| GET | `/dashboard/billing/usage` | `controller.GetUsage` | 使用量 |
| GET | `/v1/dashboard/billing/usage` | `controller.GetUsage` | 使用量（v1 路径） |

**处理函数**：`controller/billing.go`

---

## 7. 视频 API

### 7.1 OpenAI 兼容视频接口

**认证方式**：`TokenAuth()` + `Distribute()`

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/v1/video/generations` | `controller.RelayTask` | 视频生成（提交） |
| GET | `/v1/video/generations/:task_id` | `controller.RelayTaskFetch` | 查询视频任务 |
| POST | `/v1/videos` | `controller.RelayTask` | 视频生成（OpenAI 兼容） |
| GET | `/v1/videos/:task_id` | `controller.RelayTaskFetch` | 查询视频任务（OpenAI 兼容） |
| POST | `/v1/videos/:video_id/remix` | `controller.RelayTask` | 视频 Remix |
| GET | `/v1/videos/:task_id/content` | `controller.VideoProxy` | 视频内容代理（TokenOrUserAuth） |

**处理函数**：`controller/relay.go`, `controller/task_video.go`, `controller/video_proxy.go`

### 7.2 Kling 兼容接口

**认证方式**：`KlingRequestConvert()` + `TokenAuth()` + `Distribute()`

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/kling/v1/videos/text2video` | `controller.RelayTask` | Kling 文本转视频 |
| POST | `/kling/v1/videos/image2video` | `controller.RelayTask` | Kling 图片转视频 |
| GET | `/kling/v1/videos/text2video/:task_id` | `controller.RelayTaskFetch` | 查询 Kling 任务 |
| GET | `/kling/v1/videos/image2video/:task_id` | `controller.RelayTaskFetch` | 查询 Kling 任务 |

**处理函数**：`controller/relay.go`

### 7.3 Jimeng 兼容接口

**认证方式**：`JimengRequestConvert()` + `TokenAuth()` + `Distribute()`

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/jimeng/` | `controller.RelayTask` | 即梦视频生成 |

**处理函数**：`controller/relay.go`

### 7.4 Midjourney 接口

**认证方式**：`TokenAuth()` + `Distribute()`

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/mj/submit/imagine` | `controller.RelayMidjourney` | MJ 图片生成 |
| POST | `/mj/submit/describe` | `controller.RelayMidjourney` | MJ 图片描述 |
| POST | `/mj/submit/blend` | `controller.RelayMidjourney` | MJ 图片混合 |
| POST | `/mj/submit/change` | `controller.RelayMidjourney` | MJ 变换 |
| POST | `/mj/submit/simple-change` | `controller.RelayMidjourney` | MJ 简单变换 |
| POST | `/mj/submit/action` | `controller.RelayMidjourney` | MJ 动作 |
| POST | `/mj/submit/modal` | `controller.RelayMidjourney` | MJ 模态 |
| POST | `/mj/submit/shorten` | `controller.RelayMidjourney` | MJ 缩短 |
| POST | `/mj/submit/edits` | `controller.RelayMidjourney` | MJ 编辑 |
| POST | `/mj/submit/video` | `controller.RelayMidjourney` | MJ 视频 |
| GET | `/mj/task/:id/fetch` | `controller.RelayMidjourney` | 查询 MJ 任务 |
| GET | `/mj/task/:id/image-seed` | `controller.RelayMidjourney` | 获取图片种子 |
| POST | `/mj/task/list-by-condition` | `controller.RelayMidjourney` | 条件查询 MJ 任务 |
| POST | `/mj/insight-face/swap` | `controller.RelayMidjourney` | 人脸替换 |
| GET | `/mj/image/:id` | `relay.RelayMidjourneyImage` | MJ 图片代理（无认证） |

**处理函数**：`controller/midjourney.go`, `relay/mjproxy_handler.go`

### 7.5 Suno 接口

**认证方式**：`TokenAuth()` + `Distribute()`

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/suno/submit/:action` | `controller.RelayTask` | Suno 音乐生成 |
| POST | `/suno/fetch` | `controller.RelayTaskFetch` | 查询 Suno 任务 |
| GET | `/suno/fetch/:id` | `controller.RelayTaskFetch` | 查询 Suno 任务 |

**处理函数**：`controller/relay.go`

---

## 8. Webhook 端点

所有 Webhook 端点无需认证（由支付提供商签名验证保护）。

| 方法 | 路径 | Handler | 文件 |
|---|---|---|---|
| POST | `/api/stripe/webhook` | `controller.StripeWebhook` | `controller/topup_stripe.go` |
| POST | `/api/creem/webhook` | `controller.CreemWebhook` | `controller/topup_creem.go` |
| POST | `/api/waffo/webhook` | `controller.WaffoWebhook` | `controller/topup_waffo.go` |
| POST | `/api/waffo-pancake/webhook/:env` | `controller.WaffoPancakeWebhook` | `controller/topup_waffo_pancake.go` |
| POST/GET | `/api/user/epay/notify` | `controller.EpayNotify` | `controller/topup.go` |

---

## 9. 错误码参考

### 9.1 管理 API 错误

管理 API 通过 `success: false` + `message` 返回错误，无结构化错误码。

### 9.2 中继 API 错误码

**文件**：`types/error.go`

中继 API 返回 OpenAI 兼容的错误格式：

```json
{
  "error": {
    "message": "...",
    "type": "error_type",
    "code": "error_code"
  }
}
```

#### 请求错误

| 错误码 | HTTP 状态码 | 说明 |
|---|---|---|
| `invalid_request` | 400 | 请求参数无效 |
| `bad_request_body` | 400 | 请求体格式错误 |
| `read_request_body_failed` | 400/413 | 读取请求体失败 |
| `convert_request_failed` | 400 | 请求格式转换失败 |
| `sensitive_words_detected` | 400 | 检测到敏感词 |
| `access_denied` | 403 | 访问被拒绝 |

#### 认证/配额错误

| 错误码 | HTTP 状态码 | 说明 |
|---|---|---|
| `insufficient_user_quota` | 403 | 用户配额不足 |
| `pre_consume_token_quota_failed` | 403 | Token 预扣费失败 |

#### 渠道错误

| 错误码 | HTTP 状态码 | 说明 |
|---|---|---|
| `get_channel_failed` | 500 | 获取可用渠道失败 |
| `channel:no_available_key` | 500 | 渠道无可用 Key |
| `channel:model_mapped_error` | 500 | 模型映射错误 |
| `channel:aws_client_error` | 500 | AWS 客户端错误 |
| `channel:invalid_key` | 500 | 渠道 Key 无效 |
| `channel:param_override_invalid` | 400 | 参数覆盖无效 |
| `channel:header_override_invalid` | 400 | 请求头覆盖无效 |
| `channel:response_time_exceeded` | 500 | 响应时间超限 |

#### 中继处理错误

| 错误码 | HTTP 状态码 | 说明 |
|---|---|---|
| `invalid_api_type` | 500 | 无效的 API 类型 |
| `count_token_failed` | 500 | Token 计数失败 |
| `model_price_error` | 500 | 模型定价错误 |
| `json_marshal_failed` | 500 | JSON 序列化失败 |
| `do_request_failed` | 500 | 上游请求失败 |
| `gen_relay_info_failed` | 500 | 构建 RelayInfo 失败 |

#### 上游响应错误

| 错误码 | HTTP 状态码 | 说明 |
|---|---|---|
| `read_response_body_failed` | 500 | 读取上游响应失败 |
| `bad_response_status_code` | 502 | 上游返回异常状态码 |
| `bad_response` | 502 | 上游响应异常 |
| `bad_response_body` | 502 | 上游响应体异常 |
| `empty_response` | 502 | 上游返回空响应 |
| `model_not_found` | 404 | 模型不存在 |
| `prompt_blocked` | 400 | 提示词被上游阻止 |
| `aws_invoke_error` | 500 | AWS Bedrock 调用错误 |
| `violation_fee.grok.csam` | 400 | Grok CSAM 违规 |

#### 数据库错误

| 错误码 | HTTP 状态码 | 说明 |
|---|---|---|
| `query_data_error` | 500 | 数据库查询错误 |
| `update_data_error` | 500 | 数据库更新错误 |

### 9.3 HTTP 状态码使用约定

| 状态码 | 场景 |
|---|---|
| 200 | 成功 |
| 307 | 重定向（需要重试） |
| 400 | 请求参数错误 |
| 401 | 未认证 / Token 无效 |
| 403 | 权限不足 / 配额不足 / IP 不在白名单 |
| 404 | 资源不存在 |
| 413 | 请求体过大 |
| 429 | 速率限制 |
| 500 | 内部服务器错误 |
| 502 | 上游服务错误 |

---

## 补遗：06-api-reference.md 缺失路由

> 以下路由在初次生成时遗漏，基于 `router/api-router.go` 源码补充。

### 补遗 A：订阅管理（用户端）

**路径前缀**：`/api/subscription`
**认证**：`UserAuth()`

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/subscription/plans` | `controller.GetSubscriptionPlans` | 获取可用订阅计划列表 |
| GET | `/api/subscription/self` | `controller.GetSubscriptionSelf` | 获取当前用户订阅状态 |
| PUT | `/api/subscription/self/preference` | `controller.UpdateSubscriptionPreference` | 更新订阅偏好 |
| POST | `/api/subscription/balance/pay` | `controller.SubscriptionRequestBalancePay` | 余额购买订阅 |
| POST | `/api/subscription/epay/pay` | `controller.SubscriptionRequestEpay` | 易支付购买订阅 |
| POST | `/api/subscription/stripe/pay` | `controller.SubscriptionRequestStripePay` | Stripe 购买订阅 |
| POST | `/api/subscription/creem/pay` | `controller.SubscriptionRequestCreemPay` | Creem 购买订阅 |
| POST | `/api/subscription/waffo-pancake/pay` | `controller.SubscriptionRequestWaffoPancakePay` | Waffo Pancake 购买订阅 |

**处理函数**：`controller/subscription.go`, `controller/subscription_payment_epay.go`, `controller/subscription_payment_stripe.go`, `controller/subscription_payment_creem.go`, `controller/subscription_payment_waffo_pancake.go`

### 补遗 B：订阅管理（管理员端）

**路径前缀**：`/api/subscription/admin`
**认证**：`AdminAuth()`

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/subscription/admin/plans` | `controller.AdminListSubscriptionPlans` | 列出所有订阅计划 |
| POST | `/api/subscription/admin/plans` | `controller.AdminCreateSubscriptionPlan` | 创建订阅计划 |
| PUT | `/api/subscription/admin/plans/:id` | `controller.AdminUpdateSubscriptionPlan` | 更新订阅计划 |
| PATCH | `/api/subscription/admin/plans/:id` | `controller.AdminUpdateSubscriptionPlanStatus` | 更新计划状态 |
| POST | `/api/subscription/admin/bind` | `controller.AdminBindSubscription` | 管理员绑定订阅 |
| GET | `/api/subscription/admin/users/:id/subscriptions` | `controller.AdminListUserSubscriptions` | 查看用户订阅列表 |
| POST | `/api/subscription/admin/users/:id/subscriptions` | `controller.AdminCreateUserSubscription` | 管理员创建用户订阅 |
| POST | `/api/subscription/admin/user_subscriptions/:id/invalidate` | `controller.AdminInvalidateUserSubscription` | 作废用户订阅 |
| DELETE | `/api/subscription/admin/user_subscriptions/:id` | `controller.AdminDeleteUserSubscription` | 删除用户订阅 |

**处理函数**：`controller/subscription.go`

### 补遗 C：订阅支付回调（无认证）

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/api/subscription/epay/notify` | `controller.SubscriptionEpayNotify` | 易支付订阅回调 |
| GET | `/api/subscription/epay/notify` | `controller.SubscriptionEpayNotify` | 易支付订阅回调（GET） |
| GET | `/api/subscription/epay/return` | `controller.SubscriptionEpayReturn` | 易支付订阅返回页 |
| POST | `/api/subscription/epay/return` | `controller.SubscriptionEpayReturn` | 易支付订阅返回页（POST） |

**处理函数**：`controller/subscription_payment_epay.go`

### 补遗 D：签到（补充 POST）

**文档 4.6 节遗漏了 POST 签到接口。**

| 方法 | 路径 | 中间件 | Handler | 说明 |
|---|---|---|---|---|
| POST | `/api/user/checkin` | TurnstileCheck | `controller.DoCheckin` | 执行签到 |

### 补遗 E：用户管理（管理员补充）

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/api/user/manage` | `controller.ManageUser` | 管理员批量管理用户 |
| POST | `/api/user/topup/complete` | `controller.AdminCompleteTopUp` | 管理员手动完成充值 |
| GET | `/api/user/topup` | `controller.GetAllTopUps` | 获取所有充值记录 |
| DELETE | `/api/user/:id/2fa` | `controller.AdminDisable2FA` | 管理员禁用用户 2FA |
| DELETE | `/api/user/:id/bindings/:binding_type` | `controller.AdminClearUserBinding` | 清除用户绑定 |
| DELETE | `/api/user/:id/reset_passkey` | `controller.AdminResetPasskey` | 重置用户 Passkey |
| DELETE | `/api/user/:id/oauth/bindings/:provider_id` | `controller.UnbindCustomOAuthByAdmin` | 解绑自定义 OAuth |
| GET | `/api/user/:id/oauth/bindings` | `controller.GetUserOAuthBindingsByAdmin` | 查看用户 OAuth 绑定 |
| GET | `/api/user/2fa/stats` | `controller.Admin2FAStats` | 2FA 统计数据 |

**处理函数**：`controller/user.go`, `controller/twofa.go`, `controller/custom_oauth.go`

### 补遗 F：渠道管理（补充）

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/channel/test/:id` | `controller.TestChannel` | 测试指定渠道 |
| GET | `/api/channel/test` | `controller.TestAllChannels` | 测试所有渠道 |
| GET | `/api/channel/update_balance` | `controller.UpdateAllChannelsBalance` | 更新所有渠道余额 |
| GET | `/api/channel/update_balance/:id` | `controller.UpdateChannelBalance` | 更新指定渠道余额 |
| GET | `/api/channel/fetch_models/:id` | `controller.FetchUpstreamModels` | 获取上游模型列表 |
| GET | `/api/channel/models` | `controller.ChannelListModels` | 渠道模型列表 |
| GET | `/api/channel/models_enabled` | `controller.EnabledListModels` | 已启用模型列表 |
| GET | `/api/channel/tag/models` | `controller.GetTagModels` | 标签模型列表 |
| GET | `/api/channel/:id/codex/usage` | `controller.GetCodexChannelUsage` | 渠道 Codex 使用量 |
| DELETE | `/api/channel/batch` | `controller.DeleteChannelBatch` | 批量删除渠道 |
| DELETE | `/api/channel/disabled` | `controller.DeleteDisabledChannel` | 删除禁用渠道 |
| DELETE | `/api/channel/ollama/delete` | `controller.OllamaDeleteModel` | 删除 Ollama 模型 |
| GET | `/api/channel/ollama/version/:id` | `controller.OllamaVersion` | 获取 Ollama 版本 |

**处理函数**：`controller/channel.go`, `controller/channel-test.go`, `controller/channel-billing.go`, `controller/codex_usage.go`

### 补遗 G：Token 管理（补充）

| 方法 | 路径 | 中间件 | Handler | 说明 |
|---|---|---|---|---|
| POST | `/api/token/:id/key` | CriticalRateLimit + DisableCache | `controller.GetTokenKey` | 获取 Token 完整 Key |
| POST | `/api/token/batch/keys` | CriticalRateLimit + DisableCache | `controller.GetTokenKeysBatch` | 批量获取 Token Key |
| DELETE | `/api/token/batch` | — | `controller.DeleteTokenBatch` | 批量删除 Token |
| GET | `/api/token_usage` | — | `controller.GetTokenUsage` | Token 使用量统计 |

**处理函数**：`controller/token.go`

### 补遗 H：充值/支付（补充）

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/api/user/topup` | `controller.TopUp` | 发起充值（通用入口） |
| POST | `/api/user/pay` | `controller.RequestEpay` | 易支付充值 |
| POST | `/api/user/creem/pay` | `controller.RequestCreemPay` | Creem 充值 |
| POST | `/api/user/stripe/pay` | `controller.RequestStripePay` | Stripe 充值 |
| POST | `/api/user/waffo/pay` | `controller.RequestWaffoPay` | Waffo 充值 |
| POST | `/api/user/waffo-pancake/pay` | `controller.RequestWaffoPancakePay` | Waffo Pancake 充值 |
| POST | `/api/user/amount` | `controller.RequestAmount` | 请求充值金额 |
| POST | `/api/user/stripe/amount` | `controller.RequestStripeAmount` | Stripe 金额计算 |
| POST | `/api/user/waffo/amount` | `controller.RequestWaffoAmount` | Waffo 金额计算 |
| POST | `/api/user/waffo-pancake/amount` | `controller.RequestWaffoPancakeAmount` | Waffo Pancake 金额计算 |
| POST | `/api/user/aff_transfer` | `controller.TransferAffQuota` | 转移邀请奖励配额 |

**处理函数**：`controller/topup.go`, `controller/topup_stripe.go`, `controller/topup_creem.go`, `controller/topup_waffo.go`, `controller/topup_waffo_pancake.go`

### 补遗 I：Passkey 管理（补充）

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| POST | `/api/user/passkey/register/begin` | `controller.PasskeyRegisterBegin` | Passkey 注册开始 |
| POST | `/api/user/passkey/register/finish` | `controller.PasskeyRegisterFinish` | Passkey 注册完成 |
| POST | `/api/user/passkey/verify/begin` | `controller.PasskeyVerifyBegin` | Passkey 验证开始 |
| POST | `/api/user/passkey/verify/finish` | `controller.PasskeyVerifyFinish` | Passkey 验证完成 |

**处理函数**：`controller/passkey.go`

### 补遗 J：用户分组

| 方法 | 路径 | Handler | 说明 |
|---|---|---|---|
| GET | `/api/user/groups` | `controller.GetUserGroups` | 获取用户可用分组列表 |

**处理函数**：`controller/group.go`
