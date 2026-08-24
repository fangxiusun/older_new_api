# 公开、用户后台与计费 API

本文件覆盖 `/api` 下的公开接口、登录注册、用户自助、令牌、充值、订阅、日志、数据查询，以及旧 dashboard billing 兼容接口。

## 公开与站点配置接口

| 接口 | 认证 | 输入 | 输出 | 代码 |
| --- | --- | --- | --- | --- |
| `GET /api/setup` | 匿名 | 无 | 是否已初始化 | `controller/setup.go:GetSetup` |
| `POST /api/setup` | 匿名 + body limit | `SetupRequest` | 初始化结果 | `controller/setup.go:PostSetup` |
| `GET /api/status` | 匿名 | 无 | 系统状态、配置开关 | `controller/misc.go:GetStatus` |
| `GET /api/uptime/status` | 匿名 | 无 | Uptime Kuma 状态 | `controller/uptime_kuma.go` |
| `GET /api/notice` | 匿名 | 无 | 公告 | `controller/misc.go:GetNotice` |
| `GET /api/user-agreement` | 匿名 | 无 | 用户协议 | `controller/misc.go:GetUserAgreement` |
| `GET /api/privacy-policy` | 匿名 | 无 | 隐私政策 | `controller/misc.go:GetPrivacyPolicy` |
| `GET /api/about` | 匿名 | 无 | 关于内容 | `controller/misc.go:GetAbout` |
| `GET /api/home_page_content` | 匿名 | 无 | 首页内容 | `controller/misc.go:GetHomePageContent` |
| `GET /api/pricing` | Header nav 权限 | 无 | 模型定价 | `controller/pricing.go:GetPricing` |
| `GET /api/rankings?period=week` | Header nav 权限 | `period` | 排行榜快照 | `controller/rankings.go` |
| `GET /api/ratio_config` | Critical 限流 | 无 | 倍率配置 | `controller/ratio_config.go` |

`/api/perf-metrics` 是否需要登录，取决于系统 option `HeaderNavModules.pricing`（中间件 `HeaderNavModulePublicOrUserAuth("pricing")`）：

- 默认 / `enabled=true` 且 `requireAuth=false`：可匿名（`TryUserAuth`）
- `requireAuth=true` 或 `enabled=false`：必须登录（`UserAuth`，未登录 401）
- 详细调查见 `docs/llm-docs/29-perf-metrics-summary-auth.md`

| 接口 | 输入 | 输出 | 代码 |
| --- | --- | --- | --- |
| `GET /api/perf-metrics/summary?hours=24` | `hours` | 性能摘要 | `controller/perf_metrics.go:GetPerfMetricsSummary` |
| `GET /api/perf-metrics?model=&group=&hours=24` | `model`、`group`、`hours` | 性能明细 | `controller/perf_metrics.go:GetPerfMetrics` |

Go 对接：可行，均为普通 GET/POST JSON。

## 邮箱、密码重置与 OAuth

| 接口 | 认证 | 输入 | 输出 | 代码 |
| --- | --- | --- | --- | --- |
| `GET /api/verification?email=` | 匿名 + 邮件限流 + Turnstile | `email` | 发送验证码结果 | `controller/misc.go:SendEmailVerification` |
| `GET /api/reset_password?email=` | 匿名 + Critical + Turnstile | `email` | 发送重置邮件结果 | `controller/misc.go:SendPasswordResetEmail` |
| `POST /api/user/reset` | 匿名 + Critical | `PasswordResetRequest` | 重置结果 | `controller/misc.go:ResetPassword` |
| `GET /api/oauth/state?aff=` | 匿名 + Critical | `aff` 可选 | OAuth state | `controller/oauth.go:GenerateOAuthCode` |
| `GET /api/oauth/:provider` | 匿名 + Critical | `state`、`code`、`error` | OAuth 登录/回调 | `controller/oauth.go:HandleOAuth` |
| `POST /api/oauth/email/bind` | 匿名 + Critical | `emailBindRequest` | 邮箱绑定 | `controller/user.go:EmailBind` |
| `GET /api/oauth/wechat?code=` | 匿名 + Critical | `code` | 微信登录 | `controller/wechat.go:WeChatAuth` |
| `POST /api/oauth/wechat/bind` | 匿名 + Critical | `wechatBindRequest` | 微信绑定 | `controller/wechat.go:WeChatBind` |
| `GET /api/oauth/telegram/login` | 匿名 + Critical | Telegram query | Telegram 登录 | `controller/telegram.go` |
| `GET /api/oauth/telegram/bind` | 匿名 + Critical | Telegram query | Telegram 绑定 | `controller/telegram.go` |

## 用户登录、注册、Passkey 与 2FA

| 接口 | 认证 | 输入 | 输出 | 代码 |
| --- | --- | --- | --- | --- |
| `POST /api/user/register` | 匿名 + Turnstile + Critical | `model.User` 的注册字段 | 登录态/用户信息 | `controller/user.go:Register` |
| `POST /api/user/login` | 匿名 + Turnstile + Critical | `LoginRequest` | 登录态/用户信息，可能要求 2FA | `controller/user.go:Login` |
| `POST /api/user/login/2fa` | 匿名 + Critical | `Verify2FARequest` | 完成 2FA 登录 | `controller/twofa.go:Verify2FALogin` |
| `POST /api/user/passkey/login/begin` | 匿名 + Critical | 用户名等 | WebAuthn options | `controller/passkey.go` |
| `POST /api/user/passkey/login/finish` | 匿名 + Critical | WebAuthn assertion | 登录结果 | `controller/passkey.go` |
| `GET /api/user/logout` | 可匿名 | 无 | 清 session | `controller/user.go:Logout` |

登录请求结构：

```json
{
  "username": "alice",
  "password": "password"
}
```

用户认证后的 Passkey：

| 接口 | 输入 | 输出 |
| --- | --- | --- |
| `GET /api/user/passkey` | 无 | passkey 状态 |
| `POST /api/user/passkey/register/begin` | 无/用户上下文 | 注册 options |
| `POST /api/user/passkey/register/finish` | WebAuthn attestation | 注册结果 |
| `POST /api/user/passkey/verify/begin` | 无/用户上下文 | 验证 options |
| `POST /api/user/passkey/verify/finish` | WebAuthn assertion | 验证结果 |
| `DELETE /api/user/passkey` | 无 | 删除 passkey |

2FA：

| 接口 | 输入 | 输出 |
| --- | --- | --- |
| `GET /api/user/2fa/status` | 无 | 是否启用、是否已设置 |
| `POST /api/user/2fa/setup` | `Setup2FARequest` | secret、二维码 |
| `POST /api/user/2fa/enable` | `Verify2FARequest` | 备份码/启用结果 |
| `POST /api/user/2fa/disable` | `Verify2FARequest` | 禁用结果 |
| `POST /api/user/2fa/backup_codes` | `Verify2FARequest` | 新备份码 |

Go 对接：可行，但 Passkey/WebAuthn 更适合浏览器前端完成签名流程，Go 服务端对接要实现 WebAuthn 客户端数据传递。

## 当前用户接口

均需 `UserAuth`。

| 接口 | 输入 | 输出 | 代码 |
| --- | --- | --- | --- |
| `GET /api/user/self` | 无 | 当前用户信息 | `controller/user.go:GetSelf` |
| `PUT /api/user/self` | `model.User` 部分字段 | 更新结果 | `controller/user.go:UpdateSelf` |
| `DELETE /api/user/self` | 无 | 删除当前账号 | `controller/user.go:DeleteSelf` |
| `GET /api/user/token` | 无 | 用户后台 access token | `controller/user.go:GenerateAccessToken` |
| `GET /api/user/self/groups` | 无 | 当前用户可用分组 | `controller/group.go:GetUserGroups` |
| `GET /api/user/groups` | 匿名可访问，但登录时会按用户组过滤 | 分组列表 | `controller/group.go:GetUserGroups` |
| `GET /api/user/models` | 无 | 当前用户可用模型 | `controller/user.go:GetUserModels` |
| `GET /api/models` | UserAuth | Dashboard 模型列表 | `controller/model.go:DashboardListModels` |
| `GET /api/user/aff` | 无 | 邀请码 | `controller/user.go:GetAffCode` |
| `POST /api/user/aff_transfer` | `TransferAffQuotaRequest` | 邀请额度转余额结果 | `controller/user.go:TransferAffQuota` |
| `PUT /api/user/setting` | `UpdateUserSettingRequest` | 用户设置更新结果 | `controller/user.go:UpdateUserSetting` |
| `GET /api/user/checkin?month=YYYY-MM` | `month` | 签到状态 | `controller/checkin.go:GetCheckinStatus` |
| `POST /api/user/checkin` | Turnstile | 签到结果 | `controller/checkin.go:DoCheckin` |
| `GET /api/user/oauth/bindings` | 无 | OAuth 绑定列表 | `controller/custom_oauth.go:GetUserOAuthBindings` |
| `DELETE /api/user/oauth/bindings/:provider_id` | `:provider_id` | 解绑结果 | `controller/custom_oauth.go:UnbindCustomOAuth` |

用户模型结构参考 `model/user.go:User`。注意：

- `password` 不应在外部持久化明文。
- `access_token` 不通过 JSON 输出。
- `group` 决定用户默认请求分组。
- `setting` 是 JSON 字符串，结构参考 `dto/user_settings.go`。

## 令牌 API

均需 `UserAuth`，用于管理模型 API Key。

| 接口 | 输入 | 输出 | 代码 |
| --- | --- | --- | --- |
| `GET /api/token/` | `p`、`page_size` | 当前用户令牌分页，key 掩码 | `controller/token.go:GetAllTokens` |
| `GET /api/token/search?keyword=&token=` | 搜索参数 | 分页结果，key 掩码 | `controller/token.go:SearchTokens` |
| `GET /api/token/:id` | `:id` | 单个令牌，key 掩码 | `controller/token.go:GetToken` |
| `POST /api/token/:id/key` | `:id` | 完整 key | `controller/token.go:GetTokenKey` |
| `POST /api/token/` | `model.Token` | 创建结果，不返回 key | `controller/token.go:AddToken` |
| `PUT /api/token/?status_only=1` | `model.Token` | 更新后的令牌，key 掩码 | `controller/token.go:UpdateToken` |
| `DELETE /api/token/:id` | `:id` | 删除结果 | `controller/token.go:DeleteToken` |
| `POST /api/token/batch` | `{"ids":[1,2]}` | 删除数量 | `controller/token.go:DeleteTokenBatch` |
| `POST /api/token/batch/keys` | `{"ids":[1,2]}` | `{"keys":{"1":"sk-..."}}` | `controller/token.go:GetTokenKeysBatch` |

创建/更新令牌输入字段来自 `model.Token`：

```json
{
  "name": "my key",
  "expired_time": -1,
  "remain_quota": 100000,
  "unlimited_quota": false,
  "model_limits_enabled": false,
  "model_limits": "gpt-4o-mini,gpt-4.1-mini",
  "allow_ips": "127.0.0.1\n10.0.0.0/8",
  "group": "default",
  "cross_group_retry": false,
  "status": 1
}
```

后端行为：

- `name` 最长 50。
- 非无限额度时 `remain_quota` 不能小于 0，也不能超过上限。
- 用户令牌数量受 `operation_setting.GetMaxUserTokens()` 限制。
- 创建时后端生成 `key`，并强制设置 `user_id`、`created_time`、`accessed_time`。
- 列表和详情接口会掩码 key，只有 `POST /api/token/:id/key` 和批量 keys 接口返回完整 key。

TokenAuth 使用这些字段：

- `group`：覆盖用户默认分组。
- `cross_group_retry`：仅对 auto 分组有效。
- `allow_ips`：IP 白名单。
- `model_limits_enabled` / `model_limits`：限制可请求模型。

## Token ReadOnly 查询

| 接口 | 认证 | 输出 |
| --- | --- | --- |
| `GET /api/usage/token/` | `TokenAuthReadOnly` | 当前 key 的额度、模型限制、过期时间 |
| `GET /api/log/token` | `TokenAuthReadOnly` | 当前 key 关联日志 |

`GET /api/usage/token/` 输出：

```json
{
  "code": true,
  "message": "ok",
  "data": {
    "object": "token_usage",
    "name": "my key",
    "total_granted": 1000,
    "total_used": 100,
    "total_available": 900,
    "unlimited_quota": false,
    "model_limits": {},
    "model_limits_enabled": false,
    "expires_at": 0
  }
}
```

## 充值与支付

用户自助接口均需 `UserAuth`。

| 接口 | 输入 | 输出 | 代码 |
| --- | --- | --- | --- |
| `GET /api/user/topup/info` | 无 | 充值配置 | `controller/topup.go:GetTopUpInfo` |
| `GET /api/user/topup/self?keyword=` | 搜索/分页 | 当前用户充值记录 | `controller/topup.go:GetUserTopUps` |
| `POST /api/user/topup` | `topUpRequest`，通常含兑换码 | 充值结果 | `controller/user.go:TopUp` |
| `POST /api/user/pay` | `EpayRequest` | 易支付支付参数/URL | `controller/topup.go:RequestEpay` |
| `POST /api/user/amount` | `AmountRequest` | 金额换算 | `controller/topup.go:RequestAmount` |
| `POST /api/user/stripe/pay` | `StripePayRequest` | Stripe checkout | `controller/topup_stripe.go` |
| `POST /api/user/stripe/amount` | 金额请求 | Stripe 金额换算 | `controller/topup_stripe.go` |
| `POST /api/user/creem/pay` | `CreemPayRequest` | Creem checkout | `controller/topup_creem.go` |
| `POST /api/user/waffo/amount` | `WaffoPayRequest` | Waffo 金额换算 | `controller/topup_waffo.go` |
| `POST /api/user/waffo/pay` | `WaffoPayRequest` | Waffo 支付 | `controller/topup_waffo.go` |
| `POST /api/user/waffo-pancake/amount` | `WaffoPancakePayRequest` | Pancake 金额换算 | `controller/topup_waffo_pancake.go` |
| `POST /api/user/waffo-pancake/pay` | `WaffoPancakePayRequest` | Pancake 支付 | `controller/topup_waffo_pancake.go` |

支付回调接口匿名：

| 接口 | 输入 | 代码 |
| --- | --- | --- |
| `POST /api/user/epay/notify` | 易支付表单/回调参数 | `controller/topup.go:EpayNotify` |
| `GET /api/user/epay/notify` | 易支付 query | `controller/topup.go:EpayNotify` |
| `POST /api/stripe/webhook` | Stripe webhook body/signature | `controller/topup_stripe.go:StripeWebhook` |
| `POST /api/creem/webhook` | Creem webhook | `controller/topup_creem.go:CreemWebhook` |
| `POST /api/waffo/webhook` | Waffo webhook | `controller/topup_waffo.go:WaffoWebhook` |
| `POST /api/waffo-pancake/webhook/:env` | `:env` 区分 test/prod | `controller/topup_waffo_pancake.go:WaffoPancakeWebhook` |

Go 对接：用户支付发起可行；webhook 通常由支付平台回调，不建议第三方业务伪造调用。

## 订阅 API

用户订阅接口需 `UserAuth`。

| 接口 | 输入 | 输出 | 代码 |
| --- | --- | --- | --- |
| `GET /api/subscription/plans` | 无 | 已启用订阅套餐 | `controller/subscription.go:GetSubscriptionPlans` |
| `GET /api/subscription/self` | 无 | 当前用户订阅和计费偏好 | `controller/subscription.go:GetSubscriptionSelf` |
| `PUT /api/subscription/self/preference` | `{"billing_preference":"..."}` | 更新后的偏好 | `controller/subscription.go:UpdateSubscriptionPreference` |
| `POST /api/subscription/balance/pay` | `{"plan_id":1}` | 余额购买结果 | `controller/subscription.go:SubscriptionRequestBalancePay` |
| `POST /api/subscription/epay/pay` | `{"plan_id":1}` | 易支付订阅支付 | `controller/subscription_payment_epay.go` |
| `POST /api/subscription/stripe/pay` | `{"plan_id":1}` | Stripe 订阅支付 | `controller/subscription_payment_stripe.go` |
| `POST /api/subscription/creem/pay` | `{"plan_id":1}` | Creem 订阅支付 | `controller/subscription_payment_creem.go` |
| `POST /api/subscription/waffo-pancake/pay` | `{"plan_id":1}` | Pancake 订阅支付 | `controller/subscription_payment_waffo_pancake.go` |

匿名订阅回调：

| 接口 | 输入 | 代码 |
| --- | --- | --- |
| `POST /api/subscription/epay/notify` | 易支付回调 | `controller/subscription_payment_epay.go` |
| `GET /api/subscription/epay/notify` | 易支付回调 | `controller/subscription_payment_epay.go` |
| `GET /api/subscription/epay/return` | 支付返回 | `controller/subscription_payment_epay.go` |
| `POST /api/subscription/epay/return` | 支付返回 | `controller/subscription_payment_epay.go` |

套餐输出字段来自 `model.SubscriptionPlan`，核心字段：

- `title`、`subtitle`
- `price_amount`、`currency`
- `duration_unit`、`duration_value`、`custom_seconds`
- `total_amount`
- `upgrade_group`、`downgrade_group`
- `quota_reset_period`
- `allow_balance_pay`
- `allow_wallet_overflow`

## 安全验证

接口：

```http
POST /api/verify
```

认证：`UserAuth` + `CriticalRateLimit`。

输入来自 `controller/secure_verification.go:UniversalVerifyRequest`，用于密码、2FA、passkey 等统一安全验证。

输出：

```json
{
  "success": true,
  "message": "",
  "data": {
    "verified": true,
    "method": "password"
  }
}
```

通过后 session 写入 `secure_verified_at`，有效期 5 分钟。`POST /api/channel/:id/key` 等高敏接口会要求该状态。

## 日志、数据、Midjourney 与任务记录

日志接口：

| 接口 | 认证 | 查询参数 |
| --- | --- | --- |
| `GET /api/log/self` | UserAuth | `type,start_timestamp,end_timestamp,token_name,model_name,group,request_id,upstream_request_id` |
| `GET /api/log/self/search` | UserAuth + SearchRateLimit | 同上 |
| `GET /api/log/self/stat` | UserAuth | `type,start_timestamp,end_timestamp,token_name,model_name,channel,group` |

数据接口：

| 接口 | 认证 | 查询参数 |
| --- | --- | --- |
| `GET /api/data/self` | UserAuth | `start_timestamp,end_timestamp` |
| `GET /api/data/flow/self` | UserAuth | `start_timestamp,end_timestamp` |

任务和 Midjourney 记录：

| 接口 | 认证 | 查询参数 |
| --- | --- | --- |
| `GET /api/mj/self` | UserAuth | `mj_id,start_timestamp,end_timestamp` |
| `GET /api/task/self` | UserAuth | `platform,task_id,status,action,start_timestamp,end_timestamp` |

代码：

- `controller/log.go`
- `controller/usedata.go`
- `controller/midjourney.go`
- `controller/task.go`

## 旧 dashboard billing 兼容接口

这些接口在根路径注册，不在 `/api` 下。

| 接口 | 认证 | 输出 | 代码 |
| --- | --- | --- | --- |
| `GET /dashboard/billing/subscription` | `TokenAuth` | OpenAI dashboard subscription 兼容格式 | `controller/billing.go:GetSubscription` |
| `GET /v1/dashboard/billing/subscription` | `TokenAuth` | 同上 | `router/dashboard.go` |
| `GET /dashboard/billing/usage` | `TokenAuth` | usage 兼容格式 | `controller/billing.go:GetUsage` |
| `GET /v1/dashboard/billing/usage` | `TokenAuth` | usage 兼容格式 | `router/dashboard.go` |

Go 对接：可行，主要用于兼容已有 OpenAI dashboard 查询客户端。

