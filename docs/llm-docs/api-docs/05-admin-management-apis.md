# 管理后台 API

本文件覆盖管理员和 Root 接口。除特别说明外，响应格式为：

```json
{
  "success": true,
  "message": "",
  "data": {}
}
```

管理接口大多返回 HTTP 200，即使业务失败也可能是 `success:false`。

## 权限边界

| 权限 | 说明 |
| --- | --- |
| `AdminAuth` | 管理员及 Root 可访问。会自动记录管理审计。 |
| `RootAuth` | 仅 Root 可访问，主要是系统设置、支付配置、渠道密钥等敏感操作。 |
| `SecureVerificationRequired` | 部分 Root 高敏接口需要二次安全验证，默认 5 分钟有效。 |

代码：

- `middleware/auth.go:AdminAuth`
- `middleware/auth.go:RootAuth`
- `middleware/secure_verification.go`
- `controller/audit.go`
- `middleware/audit.go`

## 用户管理

路由前缀：`/api/user`，管理员分组使用 `AdminAuth`。

| 接口 | 输入 | 输出 | 代码 |
| --- | --- | --- | --- |
| `GET /api/user/` | `p,page_size` | 用户分页 | `controller/user.go:GetAllUsers` |
| `GET /api/user/search?keyword=&group=&role=&status=` | 查询参数 | 用户分页 | `controller/user.go:SearchUsers` |
| `GET /api/user/:id` | `:id` | 用户详情 | `controller/user.go:GetUser` |
| `POST /api/user/` | `model.User` | 创建结果 | `controller/user.go:CreateUser` |
| `PUT /api/user/` | `model.User` | 更新结果 | `controller/user.go:UpdateUser` |
| `DELETE /api/user/:id` | `:id` | 删除结果 | `controller/user.go:DeleteUser` |
| `POST /api/user/manage` | `ManageRequest` | 启禁用、额度等管理动作 | `controller/user.go:ManageUser` |
| `GET /api/user/topup?keyword=` | 分页/搜索 | 全部充值记录 | `controller/topup.go:GetAllTopUps` |
| `POST /api/user/topup/complete` | `AdminCompleteTopupRequest` | 人工完成充值 | `controller/topup.go:AdminCompleteTopUp` |
| `DELETE /api/user/:id/bindings/:binding_type` | `binding_type=email/github/...` | 清除绑定 | `controller/user.go:AdminClearUserBinding` |
| `GET /api/user/:id/oauth/bindings` | `:id` | 用户 OAuth 绑定 | `controller/custom_oauth.go` |
| `DELETE /api/user/:id/oauth/bindings/:provider_id` | 参数 | 管理员解绑 | `controller/custom_oauth.go` |
| `DELETE /api/user/:id/reset_passkey` | `:id` | 重置 passkey | `controller/passkey.go:AdminResetPasskey` |
| `GET /api/user/2fa/stats` | 无 | 2FA 统计 | `controller/twofa.go:Admin2FAStats` |
| `DELETE /api/user/:id/2fa` | `:id` | 禁用用户 2FA | `controller/twofa.go:AdminDisable2FA` |

用户模型字段参考 `model/user.go:User`。重要字段：

- `role`：普通、管理员、Root。
- `status`：启用/禁用。
- `quota`、`used_quota`、`request_count`。
- `group`：用户默认分组。
- `setting`：用户设置 JSON 字符串。

Go 对接：可行。必须带 `Authorization` 用户访问令牌和 `New-Api-User`。

## 系统设置与选项

路由前缀：`/api/option`，整体 `RootAuth`。

| 接口 | 输入 | 输出 | 代码 |
| --- | --- | --- | --- |
| `GET /api/option/` | 无 | 所有可管理 option | `controller/option.go:GetOptions` |
| `PUT /api/option/` | `OptionUpdateRequest` | 更新结果 | `controller/option.go:UpdateOption` |
| `POST /api/option/payment_compliance` | `PaymentComplianceRequest` | 确认支付合规 | `controller/payment_compliance.go` |
| `GET /api/option/channel_affinity_cache` | 无 | 渠道亲和缓存统计 | `controller/channel_affinity_cache.go` |
| `DELETE /api/option/channel_affinity_cache?all=&rule_name=` | query | 清理缓存 | `controller/channel_affinity_cache.go` |
| `POST /api/option/rest_model_ratio` | 无 | 重置模型倍率 | `controller/pricing.go:ResetModelRatio` |
| `POST /api/option/migrate_console_setting` | 无 | 旧设置迁移 | `controller/console_migrate.go` |
| `GET /api/option/waffo-pancake/catalog` | query | Pancake 商品目录 | `controller/topup_waffo_pancake.go` |
| `POST /api/option/waffo-pancake/pair` | `createWaffoPancakePairRequest` | 创建配对 | 同上 |
| `POST /api/option/waffo-pancake/save` | `saveWaffoPancakeRequest` | 保存配置 | 同上 |
| `POST /api/option/waffo-pancake/subscription-product` | request | 创建订阅商品 | 同上 |
| `GET /api/option/waffo-pancake/subscription-product-options` | query | 商品选项 | 同上 |

Option 更新结构：

```json
{
  "key": "SomeOptionKey",
  "value": "json-or-string"
}
```

实际可更新键由 `model/option.go` 和 `setting/*` 决定。

## 渠道管理

路由前缀：`/api/channel`，整体 `AdminAuth`，其中少数接口额外要求 `RootAuth`。

| 接口 | 权限 | 输入 | 输出 | 代码 |
| --- | --- | --- | --- | --- |
| `GET /api/channel/` | Admin | `p,page_size,id_sort,sort_by,sort_order,tag_mode,group,status,type` | 渠道分页/标签模式 | `controller/channel.go:GetAllChannels` |
| `GET /api/channel/search?keyword=&group=&model=&status=&tag_mode=` | Admin | query | 搜索结果 | `controller/channel.go:SearchChannels` |
| `GET /api/channel/models` | Admin | `type` | 模型列表 | `controller/model.go:ChannelListModels` |
| `GET /api/channel/models_enabled` | Admin | 无 | 启用模型列表 | `controller/model.go:EnabledListModels` |
| `GET /api/channel/ops` | Admin | 无 | 渠道操作统计 | `controller/channel.go:GetChannelOps` |
| `GET /api/channel/:id` | Admin | `:id` | 渠道详情，默认不含 key | `controller/channel.go:GetChannel` |
| `POST /api/channel/:id/key` | Root + 安全验证 | `:id` | 渠道完整 key | `controller/channel.go:GetChannelKey` |
| `GET /api/channel/test` | Admin | query | 测试全部渠道 | `controller/channel-test.go:TestAllChannels` |
| `GET /api/channel/test/:id?model=&endpoint_type=&stream=` | Admin | query | 测试单渠道 | `controller/channel-test.go:TestChannel` |
| `GET /api/channel/update_balance` | Admin | 无 | 更新所有余额 | `controller/channel-billing.go` |
| `GET /api/channel/update_balance/:id` | Admin | `:id` | 更新单渠道余额 | `controller/channel-billing.go` |
| `POST /api/channel/` | Admin | `AddChannelRequest` | 创建渠道 | `controller/channel.go:AddChannel` |
| `PUT /api/channel/` | Admin | `model.Channel` | 更新渠道 | `controller/channel.go:UpdateChannel` |
| `DELETE /api/channel/disabled` | Admin | 无 | 删除禁用渠道 | `controller/channel.go:DeleteDisabledChannel` |
| `POST /api/channel/tag/disabled` | Admin | `{"tag":"..."}` | 禁用标签渠道 | `controller/channel.go:DisableTagChannels` |
| `POST /api/channel/tag/enabled` | Admin | `{"tag":"..."}` | 启用标签渠道 | `controller/channel.go:EnableTagChannels` |
| `PUT /api/channel/tag` | Admin | tag 批量编辑结构 | 更新标签渠道 | `controller/channel.go:EditTagChannels` |
| `DELETE /api/channel/:id` | Admin | `:id` | 删除渠道 | `controller/channel.go:DeleteChannel` |
| `POST /api/channel/batch` | Admin | `{"ids":[...]}` | 批量删除 | `controller/channel.go:DeleteChannelBatch` |
| `POST /api/channel/fix` | Admin | 无 | 修复 abilities | `controller/channel.go:FixChannelsAbilities` |
| `GET /api/channel/fetch_models/:id` | Admin | `:id` | 拉取上游模型 | `controller/channel.go:FetchUpstreamModels` |
| `POST /api/channel/fetch_models` | Root | request | 拉取模型 | `controller/channel.go:FetchModels` |
| `POST /api/channel/copy/:id?suffix=&reset_balance=` | Admin | query | 复制渠道 | `controller/channel.go:CopyChannel` |
| `POST /api/channel/batch/tag` | Admin | `{"ids":[...],"tag":"..."}` | 批量设标签 | `controller/channel.go:BatchSetChannelTag` |
| `GET /api/channel/tag/models?tag=` | Admin | tag | 标签模型 | `controller/channel.go:GetTagModels` |
| `POST /api/channel/multi_key/manage` | Admin | `MultiKeyManageRequest` | 多 key 状态管理 | `controller/channel.go:ManageMultiKeys` |

渠道输入核心结构来自 `model.Channel`：

```json
{
  "type": 1,
  "key": "upstream-key",
  "name": "openai-main",
  "models": "gpt-4o-mini,gpt-4.1",
  "group": "default,svip",
  "base_url": "https://api.openai.com",
  "weight": 10,
  "priority": 0,
  "auto_ban": 1,
  "model_mapping": "{}",
  "setting": "{}",
  "param_override": "{}",
  "header_override": "{}",
  "tag": "openai"
}
```

后端副作用：

- 创建渠道后调用 `channel.AddAbilities`，按 `channel.group x channel.models` 写入 `abilities`。
- 更新渠道后调用 `channel.UpdateAbilities`。
- 删除渠道会删除对应 abilities。
- `group` 支持逗号分隔多个分组。
- 多 key 渠道状态写入 `channel_info`。

相关模型：

- `model/channel.go:Channel`
- `model/ability.go:Ability`
- `dto/channel_settings.go`

## Ollama 与 Codex 渠道辅助

| 接口 | 权限 | 输入 | 代码 |
| --- | --- | --- | --- |
| `POST /api/channel/ollama/pull` | Admin | pull request | `controller/channel.go:OllamaPullModel` |
| `POST /api/channel/ollama/pull/stream` | Admin | pull request，流式 | `controller/channel.go:OllamaPullModelStream` |
| `DELETE /api/channel/ollama/delete` | Admin | delete request | `controller/channel.go:OllamaDeleteModel` |
| `GET /api/channel/ollama/version/:id` | Admin | `:id` | `controller/channel.go:OllamaVersion` |
| `POST /api/channel/:id/codex/refresh` | Admin | `:id` | `controller/channel.go:RefreshCodexChannelCredential` |
| `GET /api/channel/:id/codex/usage` | Admin | `:id` | `controller/codex_usage.go` |
| `GET /api/channel/:id/codex/usage/reset-credits` | Admin | `:id` | `controller/codex_usage.go` |
| `POST /api/channel/:id/codex/usage/reset` | Admin | `:id` | `controller/codex_usage.go` |

## 渠道上游模型更新

| 接口 | 权限 | 输入 | 输出 |
| --- | --- | --- | --- |
| `POST /api/channel/upstream_updates/detect` | Admin | `applyChannelUpstreamModelUpdatesRequest` 类似结构 | 单渠道检测 |
| `POST /api/channel/upstream_updates/detect_all` | Admin | body | 全部检测 |
| `POST /api/channel/upstream_updates/apply` | Admin | body | 单渠道应用 |
| `POST /api/channel/upstream_updates/apply_all` | Admin | body | 全部应用 |

代码：`controller/channel_upstream_update.go`。

## 兑换码管理

路由前缀：`/api/redemption`，`AdminAuth`。

| 接口 | 输入 | 输出 | 代码 |
| --- | --- | --- | --- |
| `GET /api/redemption/` | 分页 | 兑换码分页 | `controller/redemption.go:GetAllRedemptions` |
| `GET /api/redemption/search?keyword=` | query | 搜索结果 | `controller/redemption.go:SearchRedemptions` |
| `GET /api/redemption/:id` | `:id` | 详情 | `controller/redemption.go:GetRedemption` |
| `POST /api/redemption/` | `model.Redemption` | 创建结果 | `controller/redemption.go:AddRedemption` |
| `PUT /api/redemption/?status_only=1` | `model.Redemption` | 更新结果 | `controller/redemption.go:UpdateRedemption` |
| `DELETE /api/redemption/invalid` | 无 | 删除无效兑换码 | `controller/redemption.go:DeleteInvalidRedemption` |
| `DELETE /api/redemption/:id` | `:id` | 删除结果 | `controller/redemption.go:DeleteRedemption` |

输入字段：

```json
{
  "name": "batch",
  "quota": 100000,
  "count": 10,
  "expired_time": 0,
  "status": 1
}
```

模型：`model/redemption.go:Redemption`。

## 日志、数据与统计管理

| 接口 | 权限 | 查询参数 | 代码 |
| --- | --- | --- | --- |
| `GET /api/log/` | Admin | `type,start_timestamp,end_timestamp,username,token_name,model_name,channel,group,request_id,upstream_request_id` | `controller/log.go:GetAllLogs` |
| `GET /api/log/search` | Admin | 同上 | `controller/log.go:SearchAllLogs` |
| `DELETE /api/log/` | Admin | 历史日志清理参数 | `controller/log.go:DeleteHistoryLogs` |
| `GET /api/log/stat` | Admin | 统计参数 | `controller/log.go:GetLogsStat` |
| `GET /api/log/channel_affinity_usage_cache` | Admin | `rule_name,using_group,key_fp` | `controller/channel_affinity_cache.go` |
| `GET /api/data/` | Admin | `start_timestamp,end_timestamp,username` | `controller/usedata.go:GetAllQuotaDates` |
| `GET /api/data/users` | Admin | 时间范围 | `controller/usedata.go:GetQuotaDatesByUser` |
| `GET /api/data/flow` | Admin | 时间范围、username | `controller/usedata.go:GetAllFlowQuotaDates` |

### Root/Admin 查询用户消耗明细的实际能力

只使用 Root 或管理员账号，可以通过上述 `AdminAuth` 接口查询用户消耗数据，不需要目标用户的 `user_access_token`。

核心路径是 `GET /api/log/`：该接口返回 `model.Log` 请求级明细，消费日志类型为 `type=2`。管理员可用 `username`、`token_name`、`model_name`、`channel`、`group`、`request_id`、`upstream_request_id`、`start_timestamp`、`end_timestamp` 过滤。响应字段包含 `user_id`、`username`、`token_id`、`token_name`、`model_name`、`quota`、`prompt_tokens`、`completion_tokens`、`channel`、`channel_name`、`group`、`request_id` 等。该接口由 `controller/log.go:GetAllLogs` 调用 `model/log.go:GetAllLogs` 实现。

聚合数据来自 `quota_data` 表：

- `GET /api/data/`：未传 `username` 时按 `model_name + created_at` 聚合；传 `username` 时按 `user_id + username + model_name + created_at` 聚合。
- `GET /api/data/users`：按 `username + created_at` 聚合所有用户。
- `GET /api/data/flow`：按流向聚合。Root 会走 `getRootFlowQuotaData`，额外返回 `node_name`、`token_id` 并解析 `token_name`；普通 Admin 会走 `getAdminFlowQuotaData`，不返回 `token_id` 和 `node_name`。

任务类消耗可同时看任务表：

- `GET /api/mj/` 返回 Midjourney 任务，字段含 `user_id`、`channel_id`、`quota`、`status`、`submit_time` 等。
- `GET /api/task/` 返回通用异步任务，字段含 `user_id`、`username`、`channel_id`、`quota`、`platform`、`action`、`status` 等。

边界和注意事项：

- 管理员接口用管理员自己的 `Authorization: Bearer <user_access_token>` 和 `New-Api-User: <admin_user_id>` 调用。不要把 `New-Api-User` 改成目标用户 ID，`middleware/auth.go:authHelper` 会拒绝 ID 不一致的请求。
- `GET /api/log/search` 当前已废弃，固定返回失败信息。
- `GET /api/log/stat` 内部固定统计消费日志；`quota` 按查询时间范围统计，`rpm` 和 `tpm` 只统计最近 60 秒。
- 请求级明细依赖 `LogConsumeEnabled`。关闭期间 `RecordConsumeLog` 不写消费日志，之后无法从日志接口补出当时明细。
- 看板聚合依赖 `DataExportEnabled` 和 `DataExportInterval`。`quota_data` 是按小时聚合后的数据，不等同于逐请求日志。
- `/api/usage/token/` 和 `/api/log/token` 使用 `TokenAuthReadOnly`，需要目标模型 API Key，不接受管理员 access token。
- 现有 `/api/token/*` 是当前用户自助接口，管理员不能仅凭自己的 access token 读取其他用户完整 API Key；查询消耗应优先使用 `/api/log/`、`/api/data/*`。

## 分组与预填分组

| 接口 | 权限 | 输入 | 输出 |
| --- | --- | --- | --- |
| `GET /api/group/` | Admin | 无 | 当前分组配置 | `controller/group.go:GetGroups` |
| `GET /api/prefill_group/?type=` | Admin | `type` | 预填分组列表 | `controller/prefill_group.go:GetPrefillGroups` |
| `POST /api/prefill_group/` | Admin | `model.PrefillGroup` | 创建 |
| `PUT /api/prefill_group/` | Admin | `model.PrefillGroup` | 更新 |
| `DELETE /api/prefill_group/:id` | Admin | `:id` | 删除 |

分组配置来自：

- `setting/ratio_setting/group_ratio.go`
- `setting/user_usable_group.go`
- `setting/auto_group.go`

## Midjourney 与任务记录管理

| 接口 | 权限 | 查询参数 |
| --- | --- | --- |
| `GET /api/mj/` | Admin | `channel_id,mj_id,start_timestamp,end_timestamp` |
| `GET /api/task/` | Admin | `platform,task_id,status,action,channel_id,start_timestamp,end_timestamp` |

代码：

- `controller/midjourney.go`
- `controller/task.go`

## Vendor 与模型元数据

Vendor：

| 接口 | 权限 | 输入 |
| --- | --- | --- |
| `GET /api/vendors/` | Admin | 分页 |
| `GET /api/vendors/search?keyword=` | Admin | 搜索 |
| `GET /api/vendors/:id` | Admin | `:id` |
| `POST /api/vendors/` | Admin | `model.VendorMeta` |
| `PUT /api/vendors/` | Admin | `model.VendorMeta` |
| `DELETE /api/vendors/:id` | Admin | `:id` |

代码：`controller/vendor_meta.go`、`model/vendor_meta.go`。

模型元数据：

| 接口 | 权限 | 输入 |
| --- | --- | --- |
| `GET /api/models/` | Admin | 分页 |
| `GET /api/models/search?keyword=&vendor=` | Admin | 搜索 |
| `GET /api/models/:id` | Admin | `:id` |
| `POST /api/models/` | Admin | `model.Model` |
| `PUT /api/models/?status_only=true` | Admin | `model.Model` |
| `DELETE /api/models/:id` | Admin | `:id` |
| `GET /api/models/missing` | Admin | 无 |
| `GET /api/models/sync_upstream/preview?locale=` | Admin | 预览同步 |
| `POST /api/models/sync_upstream` | Admin | `syncRequest` |

模型字段来自 `model/model_meta.go:Model`：

```json
{
  "model_name": "gpt-4o-mini",
  "description": "",
  "icon": "",
  "tags": "chat,vision",
  "vendor_id": 1,
  "endpoints": "/v1/chat/completions",
  "status": 1,
  "sync_official": 1,
  "name_rule": 0
}
```

## 订阅管理

路由前缀：`/api/subscription/admin`，`AdminAuth`。

| 接口 | 输入 | 输出 |
| --- | --- | --- |
| `GET /api/subscription/admin/plans` | 无 | 全部套餐 |
| `POST /api/subscription/admin/plans` | `{"plan": model.SubscriptionPlan}` | 创建套餐 |
| `PUT /api/subscription/admin/plans/:id` | `{"plan": model.SubscriptionPlan}` | 更新套餐 |
| `PATCH /api/subscription/admin/plans/:id` | `{"enabled": true}` | 启停套餐 |
| `POST /api/subscription/admin/bind` | `{"user_id":1,"plan_id":1}` | 给用户绑定订阅 |
| `GET /api/subscription/admin/users/:id/subscriptions` | `:id` | 用户订阅列表 |
| `POST /api/subscription/admin/users/:id/subscriptions` | `{"plan_id":1}` | 给用户创建订阅 |
| `POST /api/subscription/admin/user_subscriptions/:id/invalidate` | `:id` | 立即作废订阅 |
| `DELETE /api/subscription/admin/user_subscriptions/:id` | `:id` | 删除订阅 |

代码：

- `controller/subscription.go`
- `model/subscription.go`

重要副作用：

- 套餐 `upgrade_group` 可在购买/绑定后修改用户分组。
- `downgrade_group` 或 `prev_user_group` 用于订阅过期/作废后回退。
- 订阅额度可参与模型请求预扣费。

## 自定义 OAuth Provider

路由前缀：`/api/custom-oauth-provider`，`RootAuth`。

| 接口 | 输入 | 输出 |
| --- | --- | --- |
| `POST /api/custom-oauth-provider/discovery` | `FetchCustomOAuthDiscoveryRequest` | OIDC discovery |
| `GET /api/custom-oauth-provider/` | 无 | provider 列表 |
| `GET /api/custom-oauth-provider/:id` | `:id` | provider 详情 |
| `POST /api/custom-oauth-provider/` | `CreateCustomOAuthProviderRequest` | 创建 |
| `PUT /api/custom-oauth-provider/:id` | `UpdateCustomOAuthProviderRequest` | 更新 |
| `DELETE /api/custom-oauth-provider/:id` | `:id` | 删除 |

代码：

- `controller/custom_oauth.go`
- `model/custom_oauth_provider.go`

## 性能与运维

管理员健康检查：

| 接口 | 权限 | 输入 | 输出 | 代码 |
| --- | --- | --- | --- | --- |
| `GET /api/status/test` | Admin | 无 | 数据库连接状态与 HTTP 统计 | `controller/misc.go:TestStatus` |

路由前缀：`/api/performance`，`RootAuth`。

| 接口 | 输入 | 输出 | 代码 |
| --- | --- | --- | --- |
| `GET /api/performance/stats` | 无 | 运行时/缓存/磁盘统计 | `controller/performance.go:GetPerformanceStats` |
| `DELETE /api/performance/disk_cache` | 无 | 清理磁盘缓存 | `controller/performance.go:ClearDiskCache` |
| `POST /api/performance/reset_stats` | 无 | 重置统计 | `controller/performance.go:ResetPerformanceStats` |
| `POST /api/performance/gc` | 无 | 触发 GC | `controller/performance.go:ForceGC` |
| `GET /api/performance/logs` | 无 | 日志文件列表 | `controller/performance.go:GetLogFiles` |
| `DELETE /api/performance/logs?mode=&value=` | query | 清理日志 | `controller/performance.go:CleanupLogFiles` |

## 倍率同步与 Billing Debug

倍率同步，`RootAuth`：

| 接口 | 输入 | 输出 |
| --- | --- | --- |
| `GET /api/ratio_sync/channels` | 无 | 可同步渠道 |
| `POST /api/ratio_sync/fetch` | `dto.UpstreamRequest` 等 | 拉取上游倍率 |

代码：`controller/ratio_sync.go`、`dto/ratio_sync.go`。

Billing debug，`AdminAuth`：

| 接口 | 输入 | 输出 |
| --- | --- | --- |
| `GET /api/debug/billing` | 无 | 当前开关状态 |
| `POST /api/debug/billing` | `{"enabled":true}` | 设置结果 |

代码：`controller/billing_debug.go`。

## 模型部署管理

路由前缀：`/api/deployments`，`AdminAuth`。该模块面向外部算力/部署平台。

| 接口 | 输入 | 输出 | 代码 |
| --- | --- | --- | --- |
| `GET /api/deployments/settings` | 无 | 部署设置 | `controller/deployment.go:GetModelDeploymentSettings` |
| `POST /api/deployments/settings/test-connection` | 连接参数 | 测试结果 | `controller/deployment.go:TestIoNetConnection` |
| `GET /api/deployments/` | `status` | 部署列表 | `controller/deployment.go:GetAllDeployments` |
| `GET /api/deployments/search?keyword=&status=` | query | 搜索 | `controller/deployment.go:SearchDeployments` |
| `POST /api/deployments/test-connection` | body | 测试连接 | `controller/deployment.go:TestIoNetConnection` |
| `GET /api/deployments/hardware-types` | 无 | 硬件类型 | `controller/deployment.go:GetHardwareTypes` |
| `GET /api/deployments/locations` | 无 | 位置列表 | `controller/deployment.go:GetLocations` |
| `GET /api/deployments/available-replicas?hardware_id=&gpu_count=` | query | 可用副本 | `controller/deployment.go:GetAvailableReplicas` |
| `POST /api/deployments/price-estimation` | 估价请求 | 价格估算 | `controller/deployment.go:GetPriceEstimation` |
| `GET /api/deployments/check-name?name=` | query | 名称可用性 | `controller/deployment.go:CheckClusterNameAvailability` |
| `POST /api/deployments/` | 创建部署请求 | 创建结果 | `controller/deployment.go:CreateDeployment` |
| `GET /api/deployments/:id` | `:id` | 详情 | `controller/deployment.go:GetDeployment` |
| `GET /api/deployments/:id/logs?container_id=&level=&stream=&cursor=&limit=&follow=&start_time=&end_time=` | query | 日志或流式日志 | `controller/deployment.go:GetDeploymentLogs` |
| `GET /api/deployments/:id/containers` | `:id` | 容器列表 | `controller/deployment.go:ListDeploymentContainers` |
| `GET /api/deployments/:id/containers/:container_id` | path | 容器详情 | `controller/deployment.go:GetContainerDetails` |
| `PUT /api/deployments/:id` | 更新请求 | 更新结果 | `controller/deployment.go:UpdateDeployment` |
| `PUT /api/deployments/:id/name` | 名称请求 | 更新名称 | `controller/deployment.go:UpdateDeploymentName` |
| `POST /api/deployments/:id/extend` | 延期请求 | 延期结果 | `controller/deployment.go:ExtendDeployment` |
| `DELETE /api/deployments/:id` | `:id` | 删除部署 | `controller/deployment.go:DeleteDeployment` |

Go 对接：可行。日志 `stream/follow` 场景需要按流式响应处理。





