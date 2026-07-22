# Classic 控制台 API 数据渲染与数值算法分析

## 1. 文档范围

本文分析 `web/classic` 中以下四个用户控制台页面：

| 页面 | 前端路由 | 页面入口 | 核心实现 |
|---|---|---|---|
| 账户充值 | `/console/topup` | `web/classic/src/pages/TopUp/index.js` | `components/topup/` |
| 使用日志 | `/console/log` | `web/classic/src/pages/Log/index.jsx` | `hooks/usage-logs/useUsageLogsData.jsx`、`components/table/usage-logs/` |
| 数据看板 | `/console` | `web/classic/src/pages/Dashboard/index.jsx` | `hooks/dashboard/`、`components/dashboard/` |
| 令牌管理 | `/console/token` | `web/classic/src/pages/Token/index.jsx` | `hooks/tokens/useTokensData.jsx`、`components/table/tokens/` |

四个路由都包在 `PrivateRoute` 中。日志、看板会根据当前用户角色切换普通用户接口与管理员接口；充值和令牌管理始终以当前登录用户为主体，充值账单弹窗对管理员额外切换到全站账单接口。

本文以当前代码行为为准，不把字段名相似但口径不同的数值合并解释。

## 2. 公共请求链路

### 2.1 Axios 实例和鉴权

公共实例位于 `web/classic/src/helpers/api.js`：

- `baseURL`：优先使用 `VITE_REACT_APP_SERVER_URL`，未配置时使用当前站点同源地址。
- 请求头 `New-API-User`：取本地登录用户 ID，后端 `UserAuth`/`AdminAuth` 会校验它与 Session 或 access token 中的用户 ID 一致。
- 请求头 `Cache-Control: no-store`。
- 同一 URL、同一 `config.params` 的并发 GET 默认会复用同一个 Promise；传 `disableDuplicate: true` 才绕过该机制。
- 响应拦截器不拆包，页面仍从 `res.data` 读取业务响应。

多数业务接口使用统一响应：

```json
{
  "success": true,
  "message": "",
  "data": {}
}
```

分页接口的 `data` 为：

```json
{
  "page": 1,
  "page_size": 20,
  "total": 100,
  "items": []
}
```

`p` 是从 1 开始的页码，不是数据库 offset。后端通过 `(page - 1) * page_size` 计算 offset，最大页大小为 100。令牌接口前端使用 `size`，日志和充值账单使用 `page_size`，后端分页解析器兼容两种名称。

### 2.2 状态配置和额度显示

`/api/status` 的数据经 `setStatusData()` 写入本地存储。本文涉及的主要键为：

| localStorage 键 | 来源字段 | 用途 |
|---|---|---|
| `quota_per_unit` | `status.quota_per_unit` | 内部额度与美元金额的换算基数 |
| `quota_display_type` | `status.quota_display_type` | `USD`、`CNY`、`CUSTOM`、`TOKENS` |
| `status` | 完整 status JSON | 汇率、自定义货币符号、服务器地址等 |
| `data_export_default_time` | 同名字段 | 看板默认时间粒度 |
| `chats` | 同名字段 | 令牌页的聊天跳转配置 |

`renderQuota(quota, digits)` 是余额、消耗、日志花费、令牌额度最常用的渲染函数：

```text
TOKENS: display = abbreviate(quota)
USD:    display = "$" + (quota / quota_per_unit).toFixed(digits)
CNY:    display = "¥" + (quota / quota_per_unit * usd_exchange_rate).toFixed(digits)
CUSTOM: display = custom_symbol
                  + (quota / quota_per_unit * custom_currency_exchange_rate).toFixed(digits)
```

补充规则：正额度换算后若因精度显示成 0，会显示当前精度下的最小正值，例如 `$0.01`；`renderNumber` 在数值达到 1 万、100 万、10 亿时分别使用 `k`、`M`、`B` 缩写。

`renderQuotaWithAmount(amount)` 的参数不是内部额度，而是“美元数量/充值数量”：

```text
TOKENS: display = abbreviate(amount * quota_per_unit)
其他:   display = currency_symbol + (amount * currency_rate).toFixed(2)
```

因此，阅读页面代码时必须区分：

- `quota`、`used_quota`、`remain_quota`：内部额度单位，使用 `renderQuota`。
- `topUpCount`、充值配置中的 `amount_options`：充值数量，使用 `renderQuotaWithAmount` 或支付金额接口。
- `money`、支付金额接口的 `data`：实际货币金额，不再除以 `quota_per_unit`。

## 3. 账户充值页面

### 3.1 首屏请求和状态流

组件挂载后会并行触发以下数据加载逻辑：

| 接口 | 作用 | 写入的前端状态 |
|---|---|---|
| `GET /api/user/self` | 最新账户数据 | `UserContext.user` |
| `GET /api/user/aff` | 当前用户邀请码 | `affLink = origin + /register?aff={data}` |
| `GET /api/user/topup/info` | 充值开关、支付方式、门槛、折扣、预设项 | `topupInfo`、`payMethods`、各支付开关及最小充值值 |
| `GET /api/subscription/plans` | 可购买订阅套餐 | `subscriptionPlans` |
| `GET /api/subscription/self` | 当前用户订阅和计费偏好 | `billingPreference`、`activeSubscriptions`、`allSubscriptions` |

页面并不依赖初始 `UserContext` 中可能过期的余额，而是主动重新请求 `/api/user/self`。

### 3.2 账户统计卡

账户统计卡没有二次求和，直接映射 `/api/user/self`：

| 页面数值 | API 字段 | 前端处理 |
|---|---|---|
| 当前余额 | `data.quota` | `renderQuota(quota)` |
| 历史消耗 | `data.used_quota` | `renderQuota(used_quota)` |
| 请求次数 | `data.request_count` | 原值，空值回退为 0 |

邀请统计卡同样直接映射：

| 页面数值 | API 字段 | 前端处理 |
|---|---|---|
| 可用收益 | `data.aff_quota` | `renderQuota(aff_quota)` |
| 总收益 | `data.aff_history_quota` | `renderQuota(aff_history_quota)` |
| 邀请人数 | `data.aff_count` | 原值，空值回退为 0 |

### 3.3 充值配置字段映射

`GET /api/user/topup/info` 的关键字段如下：

| API 字段 | 页面用途 |
|---|---|
| `enable_online_topup` | 是否显示易支付类在线充值 |
| `enable_stripe_topup` | 是否启用 Stripe |
| `enable_creem_topup` | 是否显示 Creem 产品 |
| `enable_waffo_topup` | 是否启用 Waffo |
| `enable_waffo_pancake_topup` | 是否启用 Waffo Pancake |
| `enable_redemption` | 是否显示兑换码区域 |
| `payment_compliance_confirmed` | 未确认时禁用支付/邀请额度划转相关能力 |
| `pay_methods` | 普通支付方式及 `name/type/color/icon/min_topup` |
| `waffo_pay_methods` | 前端转成 `type = waffo:{index}` 后并入支付按钮 |
| `min_topup` | 易支付最小充值数量 |
| `stripe_min_topup` | Stripe 最小充值数量 |
| `waffo_min_topup` | Waffo 最小充值数量 |
| `waffo_pancake_min_topup` | Waffo Pancake 最小充值数量 |
| `amount_options` | 自定义充值预设数量数组 |
| `discount` | 以充值数量为键的折扣映射，例如 `{ "100": 0.9 }` |
| `creem_products` | JSON 字符串，解析为产品数组 |
| `topup_link` | 管理员配置的外部充值链接 |

支付方式自己的 `min_topup` 优先于页面公共最小值。页面初始化公共 `minTopUp` 的选择顺序为：

```text
易支付已启用     -> min_topup
否则 Stripe 启用 -> stripe_min_topup
否则 Waffo 启用  -> waffo_min_topup
否则 Pancake 启用 -> waffo_pancake_min_topup
否则             -> 1
```

Creem 使用固定产品，不参与这个数值输入门槛选择。

### 3.4 预设充值数量和本地预览

如果后端返回非空 `amount_options`，每个预设构造成：

```js
{
  value: amount,
  discount: data.discount[amount] || 1.0
}
```

如果没有自定义预设，页面使用当前最小充值值乘以下列倍数：

```text
[1, 5, 10, 30, 50, 100, 300, 500]
```

点击预设时，页面先做一次本地估算：

```text
discount = preset.discount || discountMap[preset.value] || 1
localAmount = preset.value * status.price * discount
```

预设卡片还会按显示币种再次转换：

```text
originalPrice   = preset.value * status.price
discountedPrice = originalPrice * discount
save            = originalPrice - discountedPrice

USD:
  displayValue     = preset.value
  displayActualPay = discountedPrice / usd_exchange_rate
  displaySave      = save / usd_exchange_rate

CNY:
  displayValue     = preset.value * usd_exchange_rate
  displayActualPay = discountedPrice

CUSTOM:
  displayValue     = preset.value * custom_currency_exchange_rate
  displayActualPay = discountedPrice / usd_exchange_rate
                     * custom_currency_exchange_rate
```

折扣标签中文环境显示 `discount * 10` 折；如果翻译后的“折”包含 `off`，则显示 `(1 - discount) * 100` 的优惠百分比。

### 3.5 后端实际支付金额

用户选择支付方式时，`preTopUp(payment)` 会先调用该支付方式的金额接口；接口返回成功后，`data` 字符串经 `parseFloat` 覆盖本地 `amount`。因此确认弹窗中的最终“实付金额”以金额接口结果为准。

| 支付方式 | 金额预览接口 | 请求体 | 后端主要公式 |
|---|---|---|---|
| 易支付 | `POST /api/user/amount` | `{ amount }` | `normalizedAmount * Price * topupGroupRatio * discount` |
| Stripe | `POST /api/user/stripe/amount` | `{ amount }` | `normalizedAmount * StripeUnitPrice * topupGroupRatio * discount` |
| Waffo | `POST /api/user/waffo/amount` | `{ amount }` | `normalizedAmount * WaffoUnitPrice * topupGroupRatio * discount` |
| Waffo Pancake | `POST /api/user/waffo-pancake/amount` | `{ amount }` | `normalizedAmount * WaffoPancakeUnitPrice * topupGroupRatio * discount` |

其中：

```text
normalizedAmount = amount                         // USD/CNY/CUSTOM 展示模式
normalizedAmount = amount / quota_per_unit       // TOKENS 展示模式
topupGroupRatio   = 当前用户分组充值倍率，0 按 1 处理
discount          = AmountDiscount[原始 amount]，缺失或 <= 0 按 1
```

接口把结果格式化为两位小数字符串。页面显示固定为 `amount + " 元"`，并没有根据当前币种动态切换单位文案。

本地预设估算没有纳入 `topupGroupRatio`，也没有按网关选择不同的 `UnitPrice`。所以分组充值倍率不为 1、网关单价不一致时，预设卡片金额可能与选择支付方式后确认弹窗的金额不同；金额接口返回值是前端确认弹窗采用的值，最终扣款仍由对应网关下单配置决定。

### 3.6 支付确认弹窗算法

弹窗字段来源：

| 页面数值 | 来源/公式 |
|---|---|
| 充值数量 | `renderQuotaWithAmount(topUpCount)` |
| 实付金额 | 金额预览接口返回的 `amount` |
| 折扣百分比 | `round(discountRate * 100)%` |
| 原价 | `amount / discountRate` |
| 优惠 | `amount / discountRate - amount` |
| 支付方式 | 在合并后的 `payMethods` 中按 `type === payWay` 查找 |

只有 `0 < discountRate < 1` 且金额大于 0 时显示折扣明细。

确认支付使用的接口为：

| 支付方式 | 下单接口 | 成功结果处理 |
|---|---|---|
| 易支付 | `POST /api/user/pay` | 把 `data` 作为隐藏表单字段 POST 到响应中的 `url` |
| Stripe | `POST /api/user/stripe/pay` | 打开 `data.pay_link` |
| Creem | `POST /api/user/creem/pay` | 打开 `data.checkout_url` |
| Waffo | `POST /api/user/waffo/pay` | 打开 `data.payment_url` |
| Waffo Pancake | `POST /api/user/waffo-pancake/pay` | 校验为 HTTP/HTTPS 后当前页跳转到 `data.checkout_url` |

数值输入下单时使用 `parseInt(topUpCount)`，小数会被截断。

### 3.7 兑换、邀请划转和充值账单

兑换码：

- `POST /api/user/topup`，请求 `{ key: redemptionCode }`。
- 成功时 `data` 是增加的内部额度。
- 页面显示 `renderQuota(data)`，并在当前上下文中执行 `user.quota + data`，不立即重新请求用户数据。

邀请额度划转：

- 最低值为 `quota_per_unit` 个内部额度，即按默认配置通常相当于 1 美元。
- 最大值为 `user.aff_quota`。
- `POST /api/user/aff_transfer`，请求 `{ quota: transferAmount }`。
- 成功后重新请求 `/api/user/self`，所以余额和邀请余额都以后端值刷新。

充值账单：

- 普通用户：`GET /api/user/topup/self?p={page}&page_size={size}&keyword={tradeNo}`。
- 管理员：`GET /api/user/topup?...`，展示所有用户并增加 `user_id` 列。
- 普通用户接口只查询最近 30 天；管理员无该时间窗口限制。

账单字段映射：

| 页面列 | API 字段 | 处理 |
|---|---|---|
| 订单号 | `trade_no` | 可复制 |
| 支付方式 | `payment_method` | Stripe/Creem/Waffo/支付宝/微信名称映射 |
| 充值额度 | `amount` | 原值；若 `amount == 0` 且订单号以 `sub` 开头，显示“订阅套餐” |
| 支付金额 | `money` | `¥${money.toFixed(2)}` |
| 状态 | `status` | `success/pending/failed/expired` 映射徽章 |
| 创建时间 | `create_time` | 秒级时间戳格式化 |

### 3.8 同页订阅数据

充值页在存在可购买套餐时默认切换到订阅 Tab。

`GET /api/subscription/self` 映射：

- `billing_preference`：`subscription_first`、`wallet_first`、`subscription_only`、`wallet_only`。
- `subscriptions`：生效订阅列表。
- `all_subscriptions`：包含过期/作废订阅的完整列表。

主要派生数值：

```text
已过期数量 = allSubscriptions.length - activeSubscriptions.length
剩余天数   = max(0, ceil((end_time - nowSeconds) / 86400))
已用百分比 = amount_total > 0
             ? round(amount_used / amount_total * 100)
             : 0
剩余额度   = amount_total > 0
             ? max(0, amount_total - amount_used)
             : 0
```

套餐价格以 `plan.price_amount * 当前货币 rate` 显示；套餐总额度和订阅实例额度仍通过 `renderQuota` 换算。每个套餐的已购次数通过 `all_subscriptions` 中相同 `plan_id` 的数量统计。

## 4. 使用日志页面

### 4.1 默认查询和接口选择

默认时间范围：本地当天 `00:00:00` 到当前时间加 1 小时。提交前通过 `Date.parse(value) / 1000` 转为秒级时间戳。

普通用户列表：

```text
GET /api/log/self/
  ?p={page}
  &page_size={pageSize}
  &type={logType}
  &token_name={tokenName}
  &model_name={modelName}
  &start_timestamp={startSeconds}
  &end_timestamp={endSeconds}
  &group={group}
  &request_id={requestId}
```

管理员列表使用 `GET /api/log/`，额外传 `username` 和 `channel`。

`type` 映射为：

| 值 | 含义 |
|---:|---|
| 0 | 全部/未知过滤值，后端不加类型条件 |
| 1 | 充值 |
| 2 | 消费 |
| 3 | 管理 |
| 4 | 系统 |
| 5 | 错误 |
| 6 | 退款 |

后端对 `token_name`、`group`、`request_id` 使用精确匹配；`model_name`、管理员 `username` 在包含 `%` 时可按受限通配模式搜索，否则也是精确匹配。

### 4.2 分页响应映射

列表成功后：

```text
logs       = data.items
activePage = data.page
pageSize   = data.page_size
logCount   = data.total
```

每条记录还会被前端加工：

- `timestamp2string = timestamp2string(created_at)`。
- `key = id`。
- `other` 通过 `getLogOther()` 解析；对象原样使用，字符串用 `JSON.parse`，非法 JSON 返回 `null`。
- 根据 `other` 和记录类型构造展开详情 `expandData[key]`。

普通用户接口在后端返回前还会：

- 清空 `channel_name`。
- 从 `other` 删除 `admin_info`、`audit_info`、`stream_status`。
- 把数据库日志 ID 改写为当前分页内的顺序号 `startIdx + index + 1`。

因此普通用户列表中的 `id/key` 不是稳定的数据库日志主键。

### 4.3 顶部统计数值

普通用户调用 `GET /api/log/self/stat`，管理员调用 `GET /api/log/stat`，过滤参数与列表基本一致。响应：

```json
{
  "data": {
    "quota": 123,
    "rpm": 4,
    "tpm": 560
  }
}
```

页面映射：

| 页面数值 | API 字段 | 口径 |
|---|---|---|
| 消耗额度 | `quota` | `renderQuota(quota)` |
| RPM | `rpm` | 最近 60 秒消费日志条数 |
| TPM | `tpm` | 最近 60 秒消费日志的 `prompt_tokens + completion_tokens` |

后端实际统计行为需要特别注意：

1. `quota` 固定统计 `type = 2` 的消费日志，并应用所选开始/结束时间。
2. RPM/TPM 也固定统计 `type = 2`，但时间条件固定为服务器当前时间最近 60 秒，不应用页面所选开始/结束时间。
3. 传入的 `type` 参数当前没有参与 `SumUsedQuota` 的 SQL，因此切换充值、管理、错误等日志类型不会改变统计口径，它仍显示消费额度和实时 RPM/TPM。
4. 用户名、令牌名、模型名、渠道、分组过滤仍会同时应用于对应统计查询。

### 4.4 表格字段映射和派生算法

| 页面列 | API/`other` 字段 | 渲染规则 |
|---|---|---|
| 时间 | `created_at` | 本地时间 `YYYY-MM-DD HH:mm:ss` |
| 渠道 | `channel`、`channel_name` | 仅管理员显示；颜色按 `channel % 15` 选择 |
| 用户 | `username`、`user_id` | 仅管理员显示，点击头像再请求 `/api/user/{user_id}` |
| 令牌 | `token_name` | 类型 0/2/5/6 时显示，可复制 |
| 分组 | `group` | 优先记录顶层字段，空时回退 `other.group` |
| 类型 | `type` | 按 1 至 6 映射标签，其他显示未知 |
| 模型 | `model_name` | 若发生模型映射，`other.upstream_model_name` 作为“实际模型” |
| 用时/首字 | `use_time`、`other.frt`、`is_stream` | 见下方公式 |
| 输入 | `prompt_tokens` | 原值，并补充缓存读写摘要 |
| 输出 | `completion_tokens` | 大于 0 才显示 |
| 花费 | `quota` | 一般 `renderQuota(quota, 6)`；订阅抵扣时只显示标签 |
| IP | `ip` | 消费/错误显示；管理员充值日志也显示 |
| 重试 | `other.admin_info.use_channel` | 管理员显示渠道调用链 `a->b->c` |
| 详情 | `content`、`other` | 生成计费摘要或原始文本摘要 |

用时算法：

```text
总用时 = parseInt(use_time) 秒
首字用时 = (parseFloat(other.frt) / 1000).toFixed(1) 秒
```

颜色阈值：总用时 `< 101` 秒为绿，`101~299` 秒为橙，`>= 300` 秒为红；首字 `< 3` 秒为绿，`3~9.9` 秒为橙，`>= 10` 秒为红。

输入 Token 的缓存摘要：

```text
cacheRead = max(Number(other.cache_tokens), 0)

如果 cache_creation_tokens_5m 或 cache_creation_tokens_1h 大于 0：
  cacheWrite = cache_creation_tokens_5m + cache_creation_tokens_1h
否则：
  cacheWrite = cache_creation_tokens
```

这里不会把缓存读写 Token 加回 `prompt_tokens`；页面提示明确说明 Anthropic `/v1/messages` 的输入列只展示非缓存输入。

### 4.5 `other` 字段驱动的扩展详情

常见映射如下：

| 条件/字段 | 展开内容 |
|---|---|
| `request_id` | Request ID |
| `other.ws` 或 `other.audio` | 语音输入/输出、文字输入/输出 |
| `other.cache_tokens` | 缓存 Tokens |
| `other.cache_creation_tokens` | 缓存创建 Tokens |
| `other.is_model_mapped` | 请求并计费模型、实际模型 |
| `other.request_path` | 请求路径 |
| `other.reasoning_effort` | Reasoning Effort |
| `other.po[]` | 参数覆盖条数及详情弹窗 |
| `other.stream_status` | 管理员可见的流结束状态和软错误 |
| `other.admin_info.use_channel` | 管理员可见的重试渠道链 |
| `other.admin_info.local_count_tokens` | 本地计费，否则显示上游返回 |
| `other.admin_info.channel_affinity` | 管理员可见的渠道亲和规则、分组、Key 摘要及缓存统计入口 |

计费摘要选择：

- `other.billing_mode === "tiered_expr"`：使用阶梯表达式渲染器。
- `other.violation_fee` 或对应 code/marker：显示违规扣费，额度优先取 `other.fee_quota`，否则取记录 `quota`。
- `other.claude`：使用 Claude 计费摘要。
- 其他消费记录：使用 OpenAI 风格计费摘要。
- 异步任务且 `model_price === -1`：使用任务计费过程。
- 音频/实时请求：使用音频计费过程。

订阅抵扣日志满足 `other.billing_source === "subscription"`：

```text
最终抵扣 = other.subscription_consumed
           ?? (subscription_pre_consumed + subscription_post_delta)
订阅剩余 = subscription_remain / subscription_total
```

花费列不显示 `$0`，而显示“订阅抵扣”标签；悬浮提示仍用当前记录 `quota` 展示等价成本。

## 5. 数据看板页面

### 5.1 数据来源

看板有三类独立数据源：

| 接口 | 角色 | 用途 |
|---|---|---|
| `GET /api/user/self` | 所有人 | 当前余额、历史消耗、历史请求次数 |
| `GET /api/data/self` | 普通用户 | 当前用户按模型/小时聚合的用量数据 |
| `GET /api/data/` | 管理员 | 全站或指定用户名的按模型/小时用量数据 |
| `GET /api/data/users` | 管理员 | 用户维度排行和趋势 |
| `GET /api/uptime/status` | 所有人 | 服务可用率面板 |

主用量接口参数：

```text
start_timestamp={seconds}
end_timestamp={seconds}
default_time={hour|day|week}
username={管理员可选}
```

普通用户接口限制查询时间跨度不超过 30 天；管理员主接口当前没有同样限制。

### 5.2 后端 `QuotaData` 字段口径

消费日志写入数据看板缓存时：

```text
created_at = 原请求时间向下取整到整点
count      = 1
quota      = 本次消费内部额度
token_used = prompt_tokens + completion_tokens
```

缓存和数据库以用户、用户名、模型、小时、分组、令牌、渠道、节点为维度累加。读取给前端时再聚合：

- 普通用户：按 `user_id + username + model_name + created_at` 求和。
- 管理员全站：按 `model_name + created_at` 求和。
- 管理员指定用户名：按 `user_id + username + model_name + created_at` 求和。
- 用户排行接口：按 `username + created_at` 求和。

前端实际使用的字段：

```json
{
  "username": "alice",
  "model_name": "gpt-4.1",
  "created_at": 1710000000,
  "token_used": 1234,
  "count": 2,
  "quota": 5000
}
```

### 5.3 顶部八个统计数值

| 页面数值 | 来源 | 算法 |
|---|---|---|
| 当前余额 | `/api/user/self.data.quota` | `renderQuota` |
| 历史消耗 | `/api/user/self.data.used_quota` | `renderQuota` |
| 请求次数 | `/api/user/self.data.request_count` | 原值 |
| 统计次数 | 用量数组 | `sum(item.count)` |
| 统计额度 | 用量数组 | `renderQuota(sum(item.quota))` |
| 统计 Tokens | 用量数组 | `sum(item.token_used).toLocaleString()` |
| 平均 RPM | 用量数组和查询区间 | `统计次数 / 区间分钟数`，保留 3 位小数 |
| 平均 TPM | 用量数组和查询区间 | `统计 Tokens / 区间分钟数`，保留 3 位小数 |

区间分钟数：

```text
timeDiff = (Date.parse(end_timestamp) - Date.parse(start_timestamp)) / 60000
```

这里的“平均 RPM/TPM”是整个所选区间的平均值，与日志页“最近 60 秒”的 RPM/TPM 不是同一指标。

### 5.4 前端时间分桶

后端当前忽略 `default_time` 参数，返回的 `created_at` 仍是小时粒度。前端通过 `timestamp2string1` 生成字符串键后再次聚合：

| 粒度 | 时间键 |
|---|---|
| `hour` | `MM-DD HH:00`，跨年时带年份 |
| `day` | `MM-DD`，跨年时带年份 |
| `week` | 当前记录日期到 `+6 天` 的区间字符串 |

前端总量和趋势聚合：

```text
timeQuota[timeKey]  += item.quota
timeTokens[timeKey] += item.token_used
timeCount[timeKey]  += item.count

quotaTrend  = 每个 timeKey 的额度和
tokensTrend = 每个 timeKey 的 Token 和
countTrend  = 每个 timeKey 的调用次数和
rpmTrend    = timeCount / intervalMinutes
tpmTrend    = timeTokens / intervalMinutes
```

`intervalMinutes` 为小时 60、天 1440、周 10080。只有时间点至少为 2 个时才生成 RPM/TPM 小趋势图。

当前 `week` 实现没有先把时间戳归一到自然周起点。同一天的小时会合并到同一个“当天至后 6 天”键，但相邻日期会产生相互重叠的 7 天标签。因此它表现为按天聚合后附加滑动式周区间名称，不是标准自然周汇总。

### 5.5 图表字段和算法

#### 模型消耗分布

维度是 `timeKey + model_name`：

```text
rawQuota(time, model) = sum(item.quota)
Usage                 = rawQuota / quota_per_unit，保留 4 位小数
TimeSum               = 同一 timeKey 下所有模型 rawQuota 之和
```

图表为堆叠柱状图，标题总计为 `renderQuota(totalQuota, 2)`；tooltip 用 `rawQuota` 重新执行 `renderQuota`，而不是直接展示 `Usage`。

#### 调用趋势

每个时间点、每个模型生成：

```text
Count = sum(item.count)，没有调用时补 0
```

#### 调用次数分布和排行

```text
modelTotal[model] = 所有时间点 count 之和
```

- 饼图直接使用全部模型的 `modelTotal`。
- 排行按 Count 降序。
- 超过 20 个模型时，保留前 20，剩余模型合并为“其他”。

#### 管理员用户消耗排行和趋势

`/api/data/users` 返回用户名/小时聚合数据，前端：

```text
userTotal[username] = sum(item.quota)
topUsers            = 按 userTotal 降序取前 10
userTrend(time,user) = top 10 用户在该 timeKey 的 quota 和，无数据补 0
```

排行和趋势图标题中的“总计”是前 10 名用户额度之和，不是接口中所有用户额度之和。管理员即使在主图中按 `username` 筛选，`/api/data/users` 请求也不携带用户名，因此用户排行/趋势仍展示全站用户。

### 5.6 空数据和补点行为

主接口返回空数组时，前端会追加一条：

```json
{
  "count": 0,
  "model_name": "无数据",
  "quota": 0,
  "created_at": "当前时间"
}
```

当图表时间点少于 7 个时，以数据中最后时间为基准，按当前粒度生成固定 7 个时间点。每个时间点会为所有模型补 0，因此图表保持连续，但这些补点不会改变顶部总量。

## 6. 令牌管理页面

### 6.1 列表、搜索和分页

普通列表：

```text
GET /api/token/?p={page}&size={pageSize}
```

搜索：

```text
GET /api/token/search
  ?keyword={namePattern}
  &token={keyPattern}
  &p={page}
  &size={pageSize}
```

成功后统一映射：

```text
tokens      = data.items || []
tokenCount  = data.total || 0
activePage  = data.page || 1
pageSize    = data.page_size || 当前 pageSize
showKeys    = {}
```

后端按 ID 倒序返回。搜索默认是精确匹配；只有用户显式输入 `%` 才是模糊搜索，并有以下限制：连续 `%` 被拒绝、最多 2 个 `%`、去除 `%` 后至少 2 个字符。密钥搜索会先去掉 `sk-` 前缀。

列表和搜索响应中的 `key` 已由后端脱敏：

```text
长度 <= 4: 全部替换为 *
长度 5~8: 保留前 2 后 2，中间 ****
长度 > 8: 保留前 4 后 4，中间 **********
```

### 6.2 表格字段映射

| 页面列 | API 字段 | 处理 |
|---|---|---|
| 名称 | `name` | 原值 |
| 状态 | `status` | 1 启用、2 禁用、3 过期、4 耗尽，其他未知 |
| 剩余额度/总额度 | `remain_quota`、`used_quota`、`unlimited_quota` | 见下方公式 |
| 分组 | `group`、`cross_group_retry` | `auto` 显示智能熔断；其他显示分组标签和倍率 |
| 密钥 | `key` | 默认脱敏值；按需获取真实值 |
| 可用模型 | `model_limits_enabled`、`model_limits` | 逗号拆分并按模型厂商归类成图标 |
| IP 限制 | `allow_ips` | 按换行拆分；首个直接显示，其余显示 `+N` |
| 创建时间 | `created_time` | 秒级时间戳格式化 |
| 最后使用时间 | `accessed_time` | 0/空显示 `-` |
| 过期时间 | `expired_time` | `-1` 显示永不过期，否则格式化 |

非启用状态的整行背景使用禁用色。

### 6.3 令牌额度算法

有限额度令牌：

```text
used    = parseInt(used_quota) || 0
remain  = parseInt(remain_quota) || 0
total   = used + remain
percent = total > 0 ? remain / total * 100 : 0
```

主标签显示：

```text
renderQuota(remain) / renderQuota(total)
```

进度条表示“剩余比例”，不是“已用比例”。颜色规则：

- `percent === 100`：绿色。
- `percent <= 10`：红色。
- `percent <= 30`：警告色。
- 其他：组件默认色。

`unlimited_quota === true` 时只显示“无限额度”，悬浮层仍展示 `renderQuota(used_quota)`，不计算剩余百分比。

### 6.4 分组倍率

页面另行请求：

```text
GET /api/user/self/groups
```

响应结构是 `{ groupName: { desc, ratio } }`。前端只提取 `ratio` 构成 `groupRatios[groupName]`，普通分组旁显示 `{ratio}x`。

`group === "auto"` 时不显示固定倍率，而显示“智能熔断”；`cross_group_retry === true` 时追加“跨分组”。

### 6.5 真实密钥的按需获取

列表不直接下发真实密钥：

- 单个显示/复制：`POST /api/token/{id}/key`，读取 `data.key`。
- 批量复制：`POST /api/token/batch/keys`，请求 `{ ids }`，读取 `data.keys`。
- 批量密钥接口最多允许 100 个 ID。

前端缓存真实密钥到 `resolvedTokenKeys[id]`，并缓存同一 ID 的进行中 Promise，避免重复请求。显示和复制时在后端返回值前添加 `sk-`。

“复制连接信息”生成：

```json
{
  "_type": "newapi_channel_conn",
  "key": "sk-真实密钥",
  "url": "status.server_address 或 window.location.origin"
}
```

### 6.6 创建/编辑表单的数据转换

编辑时先请求：

```text
GET /api/token/{id}
```

后端仍返回脱敏 `key`，但编辑表单不需要真实密钥。字段转换：

```text
expired_time != -1:
  秒级时间戳 -> 本地日期时间字符串

model_limits 非空:
  逗号字符串 -> 数组

remain_amount:
  quotaToDisplayAmount(remain_quota)，保留 6 位小数
```

`quotaToDisplayAmount`：

```text
TOKENS: remain_amount = remain_quota
USD:    remain_amount = remain_quota / quota_per_unit
CNY:    remain_amount = remain_quota / quota_per_unit * usd_exchange_rate
CUSTOM: remain_amount = remain_quota / quota_per_unit
                         * custom_currency_exchange_rate
```

提交时反向转换：

```text
unlimited_quota:
  remain_quota = 0

TOKENS:
  remain_quota = round(remain_amount)
USD:
  remain_quota = round(remain_amount * quota_per_unit)
CNY:
  remain_quota = round(remain_amount / usd_exchange_rate * quota_per_unit)
CUSTOM:
  remain_quota = round(remain_amount / custom_currency_exchange_rate
                       * quota_per_unit)

expired_time != -1:
  remain_time = ceil(Date.parse(value) / 1000)

model_limits:
  数组 -> 逗号字符串
model_limits_enabled:
  model_limits 字符串长度 > 0
```

编辑请求为 `PUT /api/token/`，携带当前 `id`。创建请求为 `POST /api/token/`。

批量创建不是后端批量接口：前端按 `tokenCount` 循环逐个 POST。第一个令牌使用输入名称；后续令牌，或名称留空时，使用 `{baseName}-{6位随机字母数字}`。

### 6.7 状态操作和批量删除

| 操作 | 接口 | 请求/响应处理 |
|---|---|---|
| 启用 | `PUT /api/token/?status_only=true` | `{ id, status: 1 }` |
| 禁用 | 同上 | `{ id, status: 2 }` |
| 删除单个 | `DELETE /api/token/{id}/` | 成功后刷新当前页 |
| 删除批量 | `POST /api/token/batch` | `{ ids }`，`data` 为实际删除数量 |

后端启用时还会拒绝重新启用尚未改变过期时间的过期令牌，以及剩余额度仍为 0 的耗尽令牌。

## 7. 四页数值口径对照

| 数值名称 | 页面 | 数据来源 | 时间口径/算法 |
|---|---|---|---|
| 当前余额 | 充值、看板 | `user.quota` | 账户当前累计值 |
| 历史消耗 | 充值、看板 | `user.used_quota` | 账户历史累计值 |
| 请求次数 | 充值、看板 | `user.request_count` | 账户历史累计值 |
| 消耗额度 | 日志 | `/api/log/*/stat.quota` | 当前筛选条件和时间范围内的消费日志额度和 |
| 日志 RPM/TPM | 日志 | `/api/log/*/stat` | 服务器最近 60 秒，不使用页面所选时间区间 |
| 统计次数/额度/Tokens | 看板 | `/api/data*` 数组求和 | 当前看板所选时间范围 |
| 平均 RPM/TPM | 看板 | 前端计算 | 所选范围总量 / 所选范围分钟数 |
| 令牌总额度 | 令牌 | `used_quota + remain_quota` | 令牌自己的累计上限，不等于账户余额 |
| 令牌进度百分比 | 令牌 | `remain / (used + remain)` | 剩余比例 |
| 充值实付金额 | 充值 | 网关金额接口 | 数量、单价、用户分组充值倍率和折扣共同计算 |

## 8. 已确认的实现注意点

1. 充值预设卡片是本地估算，确认弹窗会用网关金额接口覆盖；两者在分组倍率或网关单价不为默认值时可能不一致。数值输入框首次加载和手工修改时固定调用 `/api/user/amount`，只有点击具体支付方式后才切换到该网关金额接口。
2. 日志页的 `type` 只过滤列表，顶部统计目前固定为消费口径；RPM/TPM 还是最近 60 秒实时值。
3. 看板后端忽略 `default_time`，天/周粒度由前端字符串分桶实现；周粒度不是标准自然周聚合。
4. 看板管理员用户名筛选只影响主模型图，不影响用户排行和用户趋势。
5. 用户排行图标题“总计”只累计前 10 名用户。
6. 普通用户日志的 `id` 是分页顺序号，不能用作稳定日志标识；需要关联请求时应使用 `request_id`。
7. 令牌列表只返回脱敏密钥，任何需要真实密钥的功能都必须调用受保护的按需密钥接口。
8. 令牌进度条展示的是剩余额度百分比，命名或重做 UI 时不要误改成已用百分比。

## 9. 关键代码索引

- 路由：`web/classic/src/App.jsx`
- Axios：`web/classic/src/helpers/api.js`
- 额度与货币：`web/classic/src/helpers/render.jsx`、`web/classic/src/helpers/quota.js`
- 充值：`web/classic/src/components/topup/index.jsx`、`RechargeCard.jsx`、`SubscriptionPlansCard.jsx`
- 日志：`web/classic/src/hooks/usage-logs/useUsageLogsData.jsx`、`components/table/usage-logs/UsageLogsColumnDefs.jsx`
- 看板：`web/classic/src/hooks/dashboard/useDashboardData.js`、`useDashboardCharts.jsx`、`useDashboardStats.jsx`、`helpers/dashboard.jsx`
- 令牌：`web/classic/src/hooks/tokens/useTokensData.jsx`、`components/table/tokens/TokensColumnDefs.jsx`、`modals/EditTokenModal.jsx`
- 后端路由：`router/api-router.go`
- 后端日志：`controller/log.go`、`model/log.go`
- 后端看板：`controller/usedata.go`、`model/usedata.go`
- 后端充值：`controller/topup*.go`、`model/topup.go`
- 后端令牌：`controller/token.go`、`model/token.go`
