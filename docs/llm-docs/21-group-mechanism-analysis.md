# 分组机制深度调研

## 1. 调研结论

当前系统里的「分组」不是单一概念，而是三层组合：

| 类型 | 数据来源 | 核心含义 | 主要作用 |
| --- | --- | --- | --- |
| 用户分组 | `users.group` | 用户的基础身份分层 | 决定默认请求分组、可用目标分组、特殊倍率、充值倍率、订阅升级/回退 |
| 渠道分组 | `channels.group` 与 `abilities.group` | 渠道在哪些目标分组下可服务哪些模型 | 决定模型是否有可用渠道、渠道选择、模型列表、定价展示 |
| 令牌分组 | `tokens.group` | API Token 对用户基础分组的请求覆盖 | 决定本次请求实际使用哪个目标分组，支持 `auto` 自动分组 |

运行时真正参与渠道选择和计费的是 `UsingGroup`（上下文键名为 `group`）：

```text
用户分组 users.group
  └─ TokenAuth 读取用户缓存，写入 ContextKeyUserGroup
      └─ 如果 tokens.group 非空，则校验权限后覆盖为 UsingGroup
          └─ Distribute 按 UsingGroup + model 选择渠道
              └─ auto 会解析成具体 autoGroup
                  └─ 计费时按最终 UsingGroup 取 GroupRatio 或 GroupGroupRatio
```

最重要的实际结论：

- `GroupRatio` 定义系统有哪些正式目标分组，并提供默认计费倍率；但它本身不等于「用户有权限使用该分组」。
- `UserUsableGroups` 和 `group_ratio_setting.group_special_usable_group` 决定某个用户分组能使用哪些目标分组。
- `channels.group` 是渠道能力归属，最终会展开为 `abilities` 表；请求是否能找到渠道，主要看 `abilities(group, model, channel_id, enabled)`。
- `tokens.group` 为空时使用用户分组；非空时覆盖用户分组；值为 `auto` 时不是实际渠道分组，而是自动选择候选分组。
- `auto` 令牌要能通过鉴权，必须在用户可用分组里存在 `auto`；`AutoGroups` 只定义自动选择候选，不会自动授权 `auto`。
- 特殊倍率 `GroupGroupRatio[userGroup][usingGroup]` 优先于普通 `GroupRatio[usingGroup]`，因此令牌覆盖分组后，仍然会按原始用户分组计算特殊价格。

## 2. 核心数据库结构

### 2.1 用户分组：`users.group`

代码位置：[model/user.go](../../model/user.go)

```go
type User struct {
    // ...
    Group string `json:"group" gorm:"type:varchar(64);default:'default'"`
}
```

含义：用户自己的基础分组，默认是 `default`。它会被用户缓存保存：

```go
type UserBase struct {
    Id    int    `json:"id"`
    Group string `json:"group"`
    // ...
}

func (user *UserBase) WriteContext(c *gin.Context) {
    common.SetContextKey(c, constant.ContextKeyUserGroup, user.Group)
    // ...
}
```

实际来源：

- 新用户注册和管理员创建用户时，如果未显式设置，走 GORM 默认值 `default`。
- 管理员编辑用户时，可更新 `group`。
- 订阅计划购买后可把用户升级到 `subscription_plans.upgrade_group`，过期或取消时回退到 `downgrade_group` 或购买前的 `prev_user_group`。

订阅相关字段：

```go
type SubscriptionPlan struct {
    UpgradeGroup   string `json:"upgrade_group" gorm:"type:varchar(64);default:''"`
    DowngradeGroup string `json:"downgrade_group" gorm:"type:varchar(64);default:''"`
}

type UserSubscription struct {
    UpgradeGroup  string `json:"upgrade_group" gorm:"type:varchar(64);default:''"`
    PrevUserGroup string `json:"prev_user_group" gorm:"type:varchar(64);default:''"`
    DowngradeGroup string `json:"downgrade_group" gorm:"type:varchar(64);default:''"`
}
```

### 2.2 渠道分组：`channels.group` 与 `abilities.group`

代码位置：[model/channel.go](../../model/channel.go)、[model/ability.go](../../model/ability.go)

```go
type Channel struct {
    // ...
    Models string `json:"models"`
    Group  string `json:"group" gorm:"type:varchar(64);default:'default'"`
}

type Ability struct {
    Group     string `json:"group" gorm:"type:varchar(64);primaryKey;autoIncrement:false"`
    Model     string `json:"model" gorm:"type:varchar(255);primaryKey;autoIncrement:false"`
    ChannelId int    `json:"channel_id" gorm:"primaryKey;autoIncrement:false;index"`
    Enabled   bool   `json:"enabled"`
    Priority  *int64 `json:"priority" gorm:"bigint;default:0;index"`
    Weight    uint   `json:"weight" gorm:"default:0;index"`
}
```

`channels.group` 可以是逗号分隔的多个分组。渠道创建或更新时，会将 `channel.Models × channel.Group` 展开成 `abilities`：

```go
func (channel *Channel) AddAbilities(tx *gorm.DB) error {
    models_ := strings.Split(channel.Models, ",")
    groups_ := strings.Split(channel.Group, ",")
    for _, model := range models_ {
        for _, group := range groups_ {
            ability := Ability{
                Group:     group,
                Model:     model,
                ChannelId: channel.Id,
                Enabled:   channel.Status == common.ChannelStatusEnabled,
                Priority:  channel.Priority,
                Weight:    uint(channel.GetWeight()),
                Tag:       channel.Tag,
            }
            // ...
        }
    }
}
```

注意：`AddAbilities` 对 `strings.Split(channel.Group, ",")` 的结果没有 `TrimSpace`，所以配置成 `default, vip` 可能在 `abilities.group` 里留下 `" vip"`。管理界面和筛选有部分地方会 trim，但能力表生成这里不 trim，建议配置为 `default,vip`。

### 2.3 令牌分组：`tokens.group`

代码位置：[model/token.go](../../model/token.go)

```go
type Token struct {
    // ...
    Group           string `json:"group" gorm:"default:''"`
    CrossGroupRetry bool   `json:"cross_group_retry"` // 跨分组重试，仅 auto 分组有效
}
```

语义：

- `""`：令牌不覆盖用户分组，请求使用 `users.group`。
- `"default"`、`"vip"` 等：请求使用该令牌分组，覆盖用户分组。
- `"auto"`：伪分组，不直接匹配渠道，而是在 `AutoGroups` 候选中选择实际目标分组。

令牌创建和更新直接保存前端传入的 `group`：

```go
cleanToken := model.Token{
    UserId:          c.GetInt("id"),
    Group:           token.Group,
    CrossGroupRetry: token.CrossGroupRetry,
}
```

## 3. 配置项清单

分组相关配置主要持久化在 `options` 表，结构是 `key/value`：

```go
type Option struct {
    Key   string `json:"key" gorm:"primaryKey"`
    Value string `json:"value"`
}
```

初始化和数据库覆盖位置：[model/option.go](../../model/option.go)

| 配置 Key | 类型 | 默认值来源 | 实际作用 |
| --- | --- | --- | --- |
| `GroupRatio` | `map[string]float64` | `setting/ratio_setting/group_ratio.go` | 定义正式目标分组和普通计费倍率 |
| `GroupGroupRatio` | `map[userGroup]map[usingGroup]float64` | `setting/ratio_setting/group_ratio.go` | 用户分组使用某目标分组时的特殊倍率 |
| `UserUsableGroups` | `map[string]string` | `setting/user_usable_group.go` | 全局可用目标分组及描述 |
| `group_ratio_setting.group_special_usable_group` | `map[userGroup]map[group]desc` | `setting/ratio_setting/group_ratio.go` | 按用户分组增删可用目标分组 |
| `AutoGroups` | `[]string` | `setting/auto_group.go` | `auto` 令牌可尝试的候选目标分组 |
| `DefaultUseAutoGroup` | `bool` | `setting/auto_group.go` | 启用注册生成默认令牌时，是否把默认令牌设为 `auto` |
| `TopupGroupRatio` | `map[string]float64` | `common/topup-ratio.go` | 按用户分组影响充值支付金额 |
| `ModelRequestRateLimitGroup` | `map[string][2]int` | `setting/rate_limit.go` | 按令牌分组或用户分组覆盖模型请求限流 |

典型配置示例：

```json
{
  "GroupRatio": {
    "default": 1,
    "vip": 0.8,
    "svip": 0.6
  },
  "UserUsableGroups": {
    "default": "默认分组",
    "vip": "VIP 分组",
    "auto": "自动分组"
  },
  "AutoGroups": ["default", "vip"],
  "GroupGroupRatio": {
    "svip": {
      "vip": 0.5
    }
  },
  "group_ratio_setting.group_special_usable_group": {
    "default": {
      "-:svip": "移除 SVIP",
      "+:trial": "试用分组"
    }
  }
}
```

### 3.1 `GroupRatio`

代码位置：[setting/ratio_setting/group_ratio.go](../../setting/ratio_setting/group_ratio.go)

```go
var defaultGroupRatio = map[string]float64{
    "default": 1,
    "vip":     1,
    "svip":    1,
}

func GetGroupRatio(name string) float64 {
    ratio, ok := groupRatioMap.Get(name)
    if !ok {
        common.SysLog("group ratio not found: " + name)
        return 1
    }
    return ratio
}
```

实际作用：

- 为 `/api/group/` 提供「所有正式分组」列表。
- 为计费提供普通分组倍率。
- 用于校验 `tokens.group`：令牌分组如果不是 `auto`，必须存在于 `GroupRatio`。
- 用于校验订阅计划的 `upgrade_group` / `downgrade_group`。

### 3.2 `UserUsableGroups` 与特殊可用分组

基础配置：

```go
var userUsableGroups = map[string]string{
    "default": "默认分组",
    "vip":     "vip分组",
}
```

服务层合并逻辑：

```go
func GetUserUsableGroups(userGroup string) map[string]string {
    groupsCopy := setting.GetUserUsableGroupsCopy()
    if userGroup != "" {
        specialSettings, b := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Get(userGroup)
        if b {
            for specialGroup, desc := range specialSettings {
                if strings.HasPrefix(specialGroup, "-:") {
                    delete(groupsCopy, strings.TrimPrefix(specialGroup, "-:"))
                } else if strings.HasPrefix(specialGroup, "+:") {
                    groupsCopy[strings.TrimPrefix(specialGroup, "+:")] = desc
                } else {
                    groupsCopy[specialGroup] = desc
                }
            }
        }
        if _, ok := groupsCopy[userGroup]; !ok {
            groupsCopy[userGroup] = "用户分组"
        }
    }
    return groupsCopy
}
```

实际作用：

- 决定令牌是否允许设置到某个目标分组。
- 决定 Playground 是否允许临时切换分组。
- 决定 `auto` 候选分组是否对该用户有效。
- 决定价格页和用户分组接口展示哪些分组。

`auto` 的关键点：令牌分组为 `auto` 时，也会先经过 `GetUserUsableGroups(userGroup)["auto"]` 校验。因此仅配置 `AutoGroups` 不够；还需要让用户可用分组中包含 `auto`。

### 3.3 `AutoGroups`

代码位置：[setting/auto_group.go](../../setting/auto_group.go)

```go
var autoGroups = []string{
    "default",
}

func GetAutoGroups() []string {
    return autoGroups
}
```

用户实际可用的自动候选组是 `AutoGroups` 与 `GetUserUsableGroups(userGroup)` 的交集：

```go
func GetUserAutoGroup(userGroup string) []string {
    groups := GetUserUsableGroups(userGroup)
    autoGroups := make([]string, 0)
    for _, group := range setting.GetAutoGroups() {
        if _, ok := groups[group]; ok {
            autoGroups = append(autoGroups, group)
        }
    }
    return autoGroups
}
```

## 4. 请求运行时链路

### 4.1 TokenAuth：确定用户分组、令牌分组和 UsingGroup

代码位置：[middleware/auth.go](../../middleware/auth.go)

关键逻辑：

```go
userCache.WriteContext(c)

userGroup := userCache.Group
tokenGroup := token.Group
if tokenGroup != "" {
    if _, ok := service.GetUserUsableGroups(userGroup)[tokenGroup]; !ok {
        abortWithOpenAiMessage(c, http.StatusForbidden, fmt.Sprintf("无权访问 %s 分组", tokenGroup))
        return
    }
    if !ratio_setting.ContainsGroupRatio(tokenGroup) {
        if tokenGroup != "auto" {
            abortWithOpenAiMessage(c, http.StatusForbidden, fmt.Sprintf("分组 %s 已被弃用", tokenGroup))
            return
        }
    }
    userGroup = tokenGroup
}
common.SetContextKey(c, constant.ContextKeyUsingGroup, userGroup)
```

因此：

- 用户分组始终保存在 `ContextKeyUserGroup`。
- 令牌原始分组保存在 `ContextKeyTokenGroup`。
- 渠道选择使用 `ContextKeyUsingGroup`。
- 若令牌分组非空，`UsingGroup` 会变成令牌分组。

### 4.2 Distribute：按分组和模型选择渠道

代码位置：[middleware/distributor.go](../../middleware/distributor.go)

```go
usingGroup := common.GetContextKeyString(c, constant.ContextKeyUsingGroup)
channel, selectGroup, err = service.CacheGetRandomSatisfiedChannel(&service.RetryParam{
    Ctx:         c,
    ModelName:   modelRequest.Model,
    TokenGroup:  usingGroup,
    RequestPath: c.Request.URL.Path,
    Retry:       common.GetPointer(0),
})
```

普通分组选择：

```go
channel, err = model.GetRandomSatisfiedChannel(param.TokenGroup, param.ModelName, param.GetRetry(), param.RequestPath)
```

内存缓存路径：

```go
channels := filterChannelsByRequestPath(group2model2channels[group][model], requestPath)
```

数据库路径：

```go
DB.Model(&Ability{}).
    Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, model, true)
```

这说明渠道分组的最终生效点是 `abilities.group`，不是直接查询 `channels.group`。

### 4.3 auto 分组选择

代码位置：[service/channel_select.go](../../service/channel_select.go)

```go
if param.TokenGroup == "auto" {
    autoGroups := GetUserAutoGroup(userGroup)
    for i := startGroupIndex; i < len(autoGroups); i++ {
        autoGroup := autoGroups[i]
        channel, _ = model.GetRandomSatisfiedChannel(autoGroup, param.ModelName, priorityRetry, param.RequestPath)
        if channel == nil {
            common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroupIndex, i+1)
            param.SetRetry(0)
            continue
        }
        common.SetContextKey(param.Ctx, constant.ContextKeyAutoGroup, autoGroup)
        selectGroup = autoGroup
        // ...
        break
    }
}
```

`CrossGroupRetry` 只在 `auto` 场景有意义。它控制重试时是否从当前候选分组切到下一个候选分组。

### 4.4 计费：最终倍率如何确定

代码位置：[relay/helper/price.go](../../relay/helper/price.go)

```go
func HandleGroupRatio(ctx *gin.Context, relayInfo *relaycommon.RelayInfo) types.GroupRatioInfo {
    groupRatioInfo := types.GroupRatioInfo{
        GroupRatio:        1.0,
        GroupSpecialRatio: -1,
    }

    autoGroup, exists := ctx.Get("auto_group")
    if exists {
        relayInfo.UsingGroup = autoGroup.(string)
    }

    userGroupRatio, ok := ratio_setting.GetGroupGroupRatio(relayInfo.UserGroup, relayInfo.UsingGroup)
    if ok {
        groupRatioInfo.GroupSpecialRatio = userGroupRatio
        groupRatioInfo.GroupRatio = userGroupRatio
        groupRatioInfo.HasSpecialRatio = true
    } else {
        groupRatioInfo.GroupRatio = ratio_setting.GetGroupRatio(relayInfo.UsingGroup)
    }

    return groupRatioInfo
}
```

计费公式按模式不同分支，但分组倍率都是最终乘数：

- 普通倍率模式：`quota = tokens × modelRatio × groupRatio`，补全、缓存、图片、音频再叠加各自倍率。
- 固定价格模式：`quota = modelPrice × QuotaPerUnit × groupRatio`。
- 分层表达式模式：先计算表达式成本，再乘 `groupRatio`。
- 工具调用附加费也乘 `groupRatio`。

消费日志使用最终 `relayInfo.UsingGroup`：

```go
model.RecordConsumeLog(ctx, relayInfo.UserId, model.RecordConsumeLogParams{
    // ...
    Group: relayInfo.UsingGroup,
})
```

注意：错误日志在 [controller/relay.go](../../controller/relay.go) 中读取 `c.GetString("group")`。`auto` 的具体分组只写到 `auto_group` 并更新 `relayInfo.UsingGroup`，没有同步回 `c["group"]`，因此 auto 请求的部分错误日志可能记录为 `auto`，消费日志记录为最终实际分组。

## 5. 三类分组的实际作用

### 5.1 用户分组

用户分组是「用户身份层」。

实际作用：

1. 默认请求分组：当令牌没有设置 `group` 时，使用 `users.group`。
2. 权限边界：令牌分组必须在 `GetUserUsableGroups(users.group)` 结果中。
3. 特殊计费：`GroupGroupRatio[userGroup][usingGroup]` 可覆盖目标分组倍率。
4. 自动分组候选过滤：`GetUserAutoGroup(userGroup)` 只返回用户可用的 `AutoGroups`。
5. 价格展示：`/api/pricing` 会按用户分组过滤可见价格，并替换特殊倍率。
6. 充值金额：充值时按 `TopupGroupRatio[users.group]` 调整支付金额。
7. 订阅权益：订阅购买可以升级用户分组，过期/取消时回退。
8. 限流兜底：令牌分组为空时，模型请求限流按用户分组找 `ModelRequestRateLimitGroup`。

用户分组不直接决定渠道，除非令牌分组为空。真正选择渠道时使用的是 `UsingGroup`。

### 5.2 渠道分组

渠道分组是「供给能力层」。

实际作用：

1. 生成能力表：渠道保存后，把 `channels.group` 展开到 `abilities.group`。
2. 决定可用渠道：请求按 `UsingGroup + model` 找 enabled ability。
3. 决定模型列表：`/v1/models` 会从目标分组下的 enabled ability 聚合模型。
4. 决定价格页可见模型：定价页基于 enabled ability 汇总 `EnableGroup`。
5. 参与渠道优先级和权重选择：同一分组同一模型下按 `priority` 选层级，再按 `weight` 随机。
6. 影响渠道管理筛选：后台渠道列表用 `channels.group` 的逗号匹配过滤。

渠道分组不代表用户权限。即使存在 `vip` 渠道，如果用户的可用分组里没有 `vip`，令牌也不能切到 `vip`。

### 5.3 令牌分组

令牌分组是「请求覆盖层」。

实际作用：

1. 覆盖用户分组：`tokens.group` 非空时，本次请求 `UsingGroup = tokens.group`。
2. 限制模型列表：`/v1/models` 读取 `ContextKeyTokenGroup`，令牌分组为空则用用户分组，`auto` 则合并自动候选分组模型。
3. 控制渠道选择：`Distribute` 使用令牌覆盖后的 `UsingGroup` 选择渠道。
4. 控制计费倍率：计费时按 `UsingGroup` 取倍率，同时可被 `GroupGroupRatio[userGroup][usingGroup]` 覆盖。
5. 支持 `auto`：在多个候选目标分组中自动选择有可用渠道的分组。
6. 支持跨分组重试：`tokens.cross_group_retry` 在 `auto` 场景下控制重试是否切换候选分组。
7. 影响限流：模型请求限流优先取 `ContextKeyTokenGroup`，令牌分组为空才取用户分组。

令牌分组为 `auto` 时需要同时满足：

- `tokens.group = "auto"`。
- `GetUserUsableGroups(users.group)` 中存在 `auto`。
- `AutoGroups` 中至少有一个候选分组。
- 候选分组也必须在用户可用分组中。
- 候选分组下存在目标模型的 enabled ability。

## 6. 对外接口和前端展示

| 接口 | 代码 | 返回或用途 |
| --- | --- | --- |
| `GET /api/group/` | `controller.GetGroups` | 返回 `GroupRatio` 的 key 列表 |
| `GET /api/user/groups` | `controller.GetUserGroups` | 返回当前用户可用分组、描述和倍率 |
| `GET /api/user/self/groups` | `controller.GetUserGroups` | 同上 |
| `GET /api/pricing` | `controller.GetPricing` | 按用户可用分组过滤价格、返回 `group_ratio`、`usable_group`、`auto_groups` |
| `GET /v1/models` | `controller.ListModels` | 按令牌分组/auto 候选分组返回可用模型 |

前端系统设置页会保存这些字段：

- `GroupRatio`
- `TopupGroupRatio`
- `UserUsableGroups`
- `GroupGroupRatio`
- `AutoGroups`
- `DefaultUseAutoGroup`
- `group_ratio_setting.group_special_usable_group`

位置包括：

- [web/default/src/features/system-settings/models/group-ratio-form.tsx](../../web/default/src/features/system-settings/models/group-ratio-form.tsx)
- [web/default/src/features/system-settings/models/group-ratio-visual-editor.tsx](../../web/default/src/features/system-settings/models/group-ratio-visual-editor.tsx)
- [web/default/src/features/system-settings/models/ratio-settings-card.tsx](../../web/default/src/features/system-settings/models/ratio-settings-card.tsx)

## 7. 常见配置场景

### 7.1 让 VIP 用户默认使用 VIP 渠道

需要同时配置：

1. `GroupRatio` 包含 `vip`。
2. 用户 `users.group = "vip"`。
3. 渠道 `channels.group` 包含 `vip`，且该渠道支持目标模型。
4. 令牌 `tokens.group` 为空，或者也设置为 `vip`。

如果令牌设置为 `default`，即使用户是 `vip`，请求仍会使用 `default`。

### 7.2 允许普通用户手动选择 VIP 分组

需要：

1. `GroupRatio` 包含 `vip`。
2. `UserUsableGroups` 或 `GroupSpecialUsableGroup["default"]` 包含 `vip`。
3. `tokens.group = "vip"` 或 Playground 请求指定 `group = "vip"`。
4. `vip` 分组下有渠道 ability。

### 7.3 启用 auto 分组

需要：

```json
{
  "UserUsableGroups": {
    "default": "默认分组",
    "vip": "VIP 分组",
    "auto": "自动分组"
  },
  "AutoGroups": ["default", "vip"]
}
```

并且令牌设置：

```json
{
  "group": "auto",
  "cross_group_retry": true
}
```

`DefaultUseAutoGroup = true` 只会在启用注册生成默认令牌时，让该默认令牌使用 `auto`，不会自动补齐 `UserUsableGroups["auto"]`。

## 8. 容易混淆的点

1. `GroupRatio` 是「正式分组和倍率」，`UserUsableGroups` 是「可用权限和描述」，两者不是同一件事。
2. `tokens.group` 覆盖的是请求分组，不会改变 `users.group`。
3. `GroupGroupRatio` 的第一层 key 是用户原始分组，第二层 key 是实际使用分组。
4. `auto` 不是 `channels.group` 的实际值；实际渠道选择会落到 `AutoGroups` 中的某个具体分组。
5. `ModelRequestRateLimitGroup` 对 auto 请求使用的是令牌分组 `auto`，不是最终 auto 解析出的具体分组。
6. 管理员指定具体渠道时，可跳过普通渠道分组选择；普通用户不允许指定渠道。
7. 消费日志记录最终使用分组，错误日志在 auto 场景下可能记录为 `auto`。
8. 修改渠道的 `models` 或 `group` 后必须重建 abilities；项目的 `channel.Update()` 和按 tag 编辑逻辑已经会调用 `UpdateAbilities()`。

## 9. 代码索引

| 模块 | 文件 | 关键点 |
| --- | --- | --- |
| 分组倍率 | `setting/ratio_setting/group_ratio.go` | `GroupRatio`、`GroupGroupRatio`、特殊可用分组 |
| 自动分组 | `setting/auto_group.go` | `AutoGroups`、`DefaultUseAutoGroup` |
| 可用分组 | `setting/user_usable_group.go` | `UserUsableGroups` |
| 分组服务 | `service/group.go` | 合并用户可用分组、计算 auto 候选、取用户专属倍率 |
| 令牌鉴权 | `middleware/auth.go` | 令牌分组权限校验和 `UsingGroup` 确定 |
| 渠道分发 | `middleware/distributor.go` | 按 `UsingGroup + model` 选择渠道 |
| auto 选择 | `service/channel_select.go` | auto 候选分组和跨分组重试 |
| 渠道能力 | `model/ability.go` | `abilities` 生成、查询和优先级/权重选择 |
| 渠道缓存 | `model/channel_cache.go` | `group2model2channels` 内存索引 |
| 计费倍率 | `relay/helper/price.go` | `HandleGroupRatio` 和预扣费价格计算 |
| 文本结算 | `service/text_quota.go` | 最终扣费和消费日志 |
| 音频/实时结算 | `service/quota.go` | 实时、音频用量结算 |
| 日志 | `model/log.go` | `logs.group` 写入和查询 |
| 模型列表 | `controller/model.go` | 按令牌分组或 auto 候选分组返回模型 |
| 定价接口 | `controller/pricing.go` | 按用户可用分组过滤价格 |
| 充值倍率 | `common/topup-ratio.go` | `TopupGroupRatio` |
| 请求限流 | `middleware/model-rate-limit.go`、`setting/rate_limit.go` | 按令牌分组或用户分组覆盖限流 |
| 订阅分组 | `model/subscription.go` | 订阅升级和回退用户分组 |


