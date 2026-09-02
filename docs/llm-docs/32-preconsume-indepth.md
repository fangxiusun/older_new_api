# 预扣费（Pre-Consume）机制深度解析

> 适用代码基线：`main` 分支（2026-08）。核心文件：
> - `service/billing.go` — 预扣费入口与结算封装
> - `service/billing_session.go` — BillingSession 生命周期
> - `service/funding_source.go` — 钱包 / 订阅资金来源抽象
> - `service/quota.go` — `PreConsumeTokenQuota` / `PostConsumeQuota`
> - `model/quota_reserve.go` — 原子预扣（Redis Lua + 数据库条件更新）
> - `model/subscription.go` — 订阅预扣事务
> - `model/user.go`、`model/token.go` — 额度字段与缓存

---

## 1. 概述

预扣费（Pre-Consume）指在请求转发到上游 AI 提供商**之前**，按预估消耗额度先行扣减用户额度，防止用户透支。请求完成后由结算（Settle）按实际消耗多退少补，失败时由退款（Refund）全额退回。

整个生命周期由 `BillingSession`（`service/billing_session.go:26`）统一管理，实现 `relaycommon.BillingSettler` 接口，会话实例挂在 `RelayInfo.Billing` 上供后续阶段使用。

```
PreConsumeBilling (service/billing.go:20)
  └─ NewBillingSession (service/billing_session.go:747)
       ├─ 按用户计费偏好选择资金来源（wallet / subscription）
       └─ session.preConsume (service/billing_session.go:462)
            ├─ 信任额度旁路检查（shouldTrust）
            ├─ 第 1 步：预扣令牌额度  PreConsumeTokenQuota → TryReserveTokenQuota
            └─ 第 2 步：预扣资金来源  funding.PreConsume
                 ├─ WalletFunding       → TryReserveUserQuota
                 └─ SubscriptionFunding → PreConsumeUserSubscription（事务）
```

---

## 2. 预扣费是否写数据库？

**会写，且是"Redis 缓存先行 + 数据库随后落库"的双写路径**（订阅路径例外，纯数据库事务）。

### 2.1 钱包路径（WalletFunding）

调用链：`WalletFunding.PreConsume`（`service/funding_source.go:43`）→ `model.TryReserveUserQuota`（`model/quota_reserve.go:165`）。

1. **Redis 启用时**：
   - 先执行 Lua 脚本 `userQuotaReserveScript`（`model/quota_reserve.go:20`），在用户缓存 hash 上原子地"校验 Id/Schema → 读 Quota → 判断余额 → `HINCRBY Quota -amount`"。
   - 缓存未命中（miss）时先 `GetUserCache` 水合再重试一次。
   - **缓存预扣成功后**调用 `persistUserQuotaDelta`（`model/quota_reserve.go:108`）落库：
     - 批量落库模式（`common.BatchUpdateEnabled`）：`addNewRecord(BatchUpdateTypeUserQuota, id, delta)` 入队，由批量刷新协程异步写库；
     - 直写模式：`UPDATE users SET quota = quota - ? WHERE id = ?`，要求 `RowsAffected == 1`。
   - **落库失败补偿**：若落库失败，反向 `HINCRBY` 补回缓存并返回错误（`quota_reserve.go:191-198`），保证缓存与库不漂移。
2. **Redis 未启用或异常时**：降级为数据库条件更新 `reserveUserQuotaDB`（`model/quota_reserve.go:144`）：

   ```sql
   UPDATE users SET quota = quota - ? WHERE id = ? AND quota >= ?
   ```

   以 `RowsAffected == 1` 表示抢到额度，`== 0` 表示余额不足（`ErrInsufficientWalletQuota`），未发生任何扣减。

### 2.2 令牌路径（Token 预扣）

调用链：`PreConsumeTokenQuota`（`service/quota.go:399`）→ `model.TryReserveTokenQuota`（`model/quota_reserve.go:203`）。结构与钱包路径完全对称：

- Redis Lua 脚本 `tokenQuotaReserveScript`（`model/quota_reserve.go:42`）：`RemainQuota -= amount`、`UsedQuota += amount`、更新 `AccessedTime`。
- 落库 `persistTokenQuotaDelta`（`model/quota_reserve.go:123`）：
  ```sql
  UPDATE tokens
     SET remain_quota = remain_quota - ?,
         used_quota   = used_quota + ?,
         accessed_time = ?
   WHERE id = ?
  ```
- 无 Redis 降级为 `reserveTokenQuotaDB`（`model/quota_reserve.go:151`），同样带 `WHERE remain_quota >= ?` 条件。
- **无限额度令牌**（`unlimited=true`）跳过余额校验，但仍走 `DecreaseTokenQuota` 维护 used/remain 记账。

Playground 请求（`relayInfo.IsPlayground`）不做令牌预扣（`service/quota.go:403`）。

### 2.3 订阅路径（SubscriptionFunding）

调用链：`SubscriptionFunding.PreConsume`（`service/funding_source.go:112`）→ `model.PreConsumeUserSubscription`（`model/subscription.go:1298`）。**订阅路径不经过 Redis 缓存，是纯数据库事务**：

```go
DB.Transaction(func(tx *gorm.DB) error {
    // 1. 按 request_id 查已有预扣记录（幂等重放检查）
    // 2. lockForUpdate 锁定该用户全部活跃订阅行（status='active' AND end_time > now）
    // 3. 逐个候选订阅：按计划做周期重置，检查 AmountTotal - AmountUsed >= amount
    // 4. 写入 SubscriptionPreConsumeRecord（request_id 幂等键）
    // 5. sub.AmountUsed += amount; tx.Save(&sub)
})
```

事务内做两处写：

1. **`user_subscriptions` 表**：`amount_used` 增加 `amount`（周期重置时还可能更新 `amount_used=0`、`last_reset_time`、`next_reset_time`）。
2. **`subscription_pre_consume_records` 表**（`SubscriptionPreConsumeRecord`）：新增一条 `status="consumed"` 的预扣记录，字段：

| 字段 | 含义 |
|---|---|
| `request_id` | 请求 ID，幂等键（唯一防重放） |
| `user_id` | 用户 ID |
| `user_subscription_id` | 命中的订阅 ID |
| `pre_consumed` | 预扣额度 |
| `status` | `consumed` / `refunded` |

这条记录是订阅退款 `RefundSubscriptionPreConsume`（`model/subscription.go:1402`）按 `request_id` 幂等退还的依据。

---

## 3. 涉及的数据库表与字段汇总

全部位于**主数据库**（SQLite / MySQL ≥ 5.7.8 / PostgreSQL ≥ 9.6 三者兼容）：

| 表 | 字段 | 预扣时变动 | 写入方式 |
|---|---|---|---|
| `users` | `quota` | `quota - 预扣额` | Redis 先扣缓存 → 批量/直写落库；或无 Redis 条件更新 |
| `tokens` | `remain_quota` | 减少预扣额 | 同上 |
| `tokens` | `used_quota` | 增加预扣额 | 同上 |
| `tokens` | `accessed_time` | 更新为当前时间 | 同上 |
| `user_subscriptions` | `amount_used` | 增加预扣额 | 数据库事务（`SELECT ... FOR UPDATE` 行锁） |
| `subscription_pre_consume_records` | 新增行（request_id, user_id, user_subscription_id, pre_consumed, status） | 插入 | 数据库事务 |

此外，结算 / 退款阶段会反向写这些字段（见 §6），并写 `logs` 消费日志（`RecordConsumeLog`）。

---

## 4. 预扣额度如何确定

- 常规请求：按模型倍率 / 分组倍率预估（`relay/helper/price.go` 计算的 `preConsumedQuota`），且在请求前若额度符合信任条件可跳过预扣。
- **信任额度旁路**（`shouldTrust`，`service/billing_session.go:687`）：令牌额度与用户钱包额度均大于 `common.GetTrustQuota()` 时，`effectiveQuota = 0`，即不预扣。仅对钱包生效，**订阅永远不走信任旁路**；异步任务（`ForcePreConsume=true`，如视频生成）强制全额预扣。
- `preConsumedQuota < 0` 或存在额度饱和标记（`relayInfo.QuotaClamp`）时直接 400 拒绝（`service/billing.go:21-36`）。

---

## 5. 并行（并发）预扣费会发生什么？

并发安全是本设计的核心目标，`service/quota.go:406` 的注释明确写道："原子预扣：检查与扣减在同一操作中完成，并发请求不可能同时通过检查后超扣。" 三个资金通道的实现方式不同，但结论一致：

### 5.1 钱包：Redis Lua 单线程原子性

`userQuotaReserveScript` 把"读余额 → 比较 → 扣减"放在一个 Lua 脚本内，Redis 对脚本执行是串行的，两个并发请求不可能同时通过检查后各自扣减。余额不足的一方返回 0（insufficient），映射为 `ErrInsufficientWalletQuota`。

无 Redis 时依赖数据库的原子条件更新（`WHERE quota >= ?`），由行锁保证同一时刻只有一个事务能更新该行，`RowsAffected == 0` 即失败。

关键点（`quota_reserve.go:163` 注释）：**缓存命中时以缓存余额为准**，而不是数据库余额。因为批量落库模式下数据库 `quota` 会滞后，若用数据库余额校验会放大并发超扣风险。

### 5.2 令牌：与钱包完全相同的模式

Lua 脚本或 `WHERE remain_quota >= ?` 条件更新。并发下额度不足的请求直接 403（"token quota is not enough"），`remain_quota` 不会变负。

### 5.3 订阅：数据库事务 + 行锁串行化

`PreConsumeUserSubscription` 在事务内用 `lockForUpdate`（`model/main.go` 共享 helper，MySQL/PostgreSQL 发 `FOR UPDATE`，SQLite 跳过）锁定该用户全部活跃订阅行：

- 并发请求在行锁上串行化，后到者看到前者已累加的 `amount_used`，额度不够即报 `subscription quota insufficient`；
- `amount_total - amount_used >= amount` 的判断与 `amount_used` 更新在同一事务内，不会双扣或超扣；
- 同一 `request_id` 重放（网络重试等）命中已存在的预扣记录直接返回原结果；若该记录已是 `refunded` 状态则拒绝，防止"退款后再重放预扣"套利。

### 5.4 并发场景下的失败与回滚语义

`preConsume` 两步之间不是原子的（先令牌、后资金），因此失败时显式回滚（`service/billing_session.go:524-547`）：

- 资金来源预扣失败 → `model.IncreaseTokenQuota` 退还已扣的令牌额度，`tokenConsumed` 归零；
- 若回滚本身也失败，记录 `SysLog` 供人工对账。

补充扣减（`Reserve`，发送前发现预估不足时追加，`service/billing_session.go:351`）也有对应的 `rollbackFundingReserve`。

### 5.5 并发结论

| 并发请求情形 | 结果 |
|---|---|
| 多请求同时预扣同一用户钱包 | 每个请求原子拿到自己的扣减；余额耗尽后后续请求 403，**不会把 `users.quota` 扣成负数** |
| 多请求同时预扣同一令牌 | 同上，`remain_quota` 不为负 |
| 多请求同时预扣同一订阅 | 行锁串行化，额度不足者失败；`amount_used` 精确累加 |
| 同一 request_id 重放 | 订阅路径幂等返回原预扣记录；钱包/令牌为非幂等增量操作，依赖上层不重试（`WalletFunding.Refund` 注释明确"IncreaseUserQuota 不能重试，否则会多退额度"） |
| 缓存预扣成功但落库失败 | 反向补回缓存并返回失败，请求不继续 |
| 批量落库模式下的瞬时滞后 | 校验以 Redis 缓存余额为准，不放大超扣 |

---

## 6. 结算与退款（与预扣的闭环）

- **结算 `SettleBilling`**（`service/billing.go:51`）→ `BillingSession.Settle(actualQuota)`（`service/billing_session.go:42`）：
  - `delta = actualQuota - preConsumedQuota`；
  - `delta > 0` 补扣（钱包 `DecreaseUserQuota` 无条件扣、可欠费为负；订阅 `PostConsumeUserSubscriptionDelta`）；`delta < 0` 退回；
  - 令牌额度同步调整（`DecreaseTokenQuota` / `IncreaseTokenQuota`）；
  - `fundingSettled` 标记防止资金与令牌两步不一致时 Refund 误退资金。
- **退款 `Refund`**（`service/billing_session.go:182`）：请求失败时异步（`gopool.Go`）退回全部预扣：钱包 `IncreaseUserQuota`（非幂等，不可重试）、订阅 `RefundSubscriptionPreConsume`（事务 + request_id 幂等，可重试 3 次）、令牌 `IncreaseTokenQuota`。整体幂等（`refunded` / `settled` 标记）。
- **兜底路径**：无 BillingSession 时（按次计费等），`SettleBilling` 回退到旧的 `PostConsumeQuota`（`service/quota.go:426`），逻辑语义相同。

---

## 7. 计费偏好与回退

`NewBillingSession` 按用户设置 `BillingPreference`（`common.NormalizeBillingPreference` 归一化）决定资金来源：

| 偏好 | 行为 |
|---|---|
| `subscription_only` | 只走订阅，失败即失败 |
| `wallet_only` | 只走钱包，失败即失败 |
| `wallet_first` | 先钱包；钱包余额不足（`ErrorCodeInsufficientUserQuota`）自动回退订阅 |
| `subscription_first`（默认） | 有活跃订阅先走订阅；订阅额度不足时检查订阅计划是否允许钱包溢出（`UserActiveSubscriptionsAllowWalletOverflow`），允许则回退钱包，否则 403 |

钱包入口处有前置余额检查（`tryWallet`，`service/billing_session.go:768`）：`userQuota - preConsumedQuota < 0` 直接拒绝，随后 `TryReserveUserQuota` 的原子扣减再做最终防线。

---

## 8. 审计与可观测性

- 全链路埋点 `logger.BillingDebugMap`（stage 化调试日志，见 `docs/llm-docs/13-billing-debug-log-plan.md`）。
- 额度饱和（clamp）事件通过 `*Checked` 配额换算函数捕获，挂到 `relayInfo.QuotaClamp`，最终嵌套进消费日志 `other.admin_info.quota_saturation`（仅管理员可见）并发出 `LogWarn`（见 `service/log_info_generate.go` 的 `attachQuotaSaturation`）。
- 落库失败补偿、令牌调整失败等异常均写 `common.SysLog` / `SysError` 供对账。

---

## 9. 关键不变量（Invariants）速查

1. **不超扣**：钱包/令牌走"检查+扣减"原子操作；订阅走事务行锁。
2. **不为负**：数据库余额列不会被预扣扣成负数（结算补扣的欠费是显式设计，仅钱包 `Settle` 正差额路径允许）。
3. **不双扣**：订阅按 `request_id` 幂等；钱包/令牌增量操作禁止重试。
4. **可回滚**：两步预扣任一失败，已完成步骤显式回滚；回滚失败留 `SysLog` 审计。
5. **缓存与库一致**：缓存扣成功必落库；落库失败必补缓存。
6. **幂等关闭**：`Refund` / `Settle` 均有状态标记防重复执行。
