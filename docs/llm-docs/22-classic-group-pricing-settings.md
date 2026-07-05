# 经典前端分组相关设置调研

## 1. 调研范围

本文聚焦经典前端页面：

```text
系统设置 -> 分组与模型定价设置 -> 分组相关设置
```

对应代码入口：

- `web/classic/src/pages/Setting/index.jsx:115`：系统设置侧边项标题「分组与模型定价设置」。
- `web/classic/src/components/settings/RatioSetting.jsx:104`：`Tabs.TabPane` 渲染「分组相关设置」。
- `web/classic/src/pages/Setting/Ratio/GroupRatioSettings.jsx:54`：该页只编辑 6 个 option key。

本页涉及的设置项是：

| 页面区域 | 页面字段 | 实际 option key | 类型 |
| --- | --- | --- | --- |
| 分组管理 | 分组名称 / 倍率 / 用户可选 / 描述 | `GroupRatio` + `UserUsableGroups` | JSON object |
| 自动分组 | 默认使用 auto 分组 | `DefaultUseAutoGroup` | bool |
| 自动分组 | 自动分组 auto，从第一个开始选择 | `AutoGroups` | JSON string array |
| 分组特殊倍率 | 用户分组 -> 使用分组 -> 倍率 | `GroupGroupRatio` | JSON object |
| 分组特殊可用分组 | 用户分组 -> 添加/移除/追加目标分组 | `group_ratio_setting.group_special_usable_group` | JSON object |

注意：`TopupGroupRatio`（充值分组倍率）虽然也是分组相关配置，但它在经典前端的「支付设置」页，代码位于 `web/classic/src/pages/Setting/Payment/SettingsGeneralPayment.jsx:193`，不属于本页「分组相关设置」。

## 2. 前端页面和保存机制

### 2.1 页面入口

`RatioSetting` 从 `/api/option/` 拉取所有可展示配置，并把 JSON 字符串格式化后传给各个 tab：

```jsx
// web/classic/src/components/settings/RatioSetting.jsx
const getOptions = async () => {
  const res = await API.get('/api/option/');
  // ...
  data.forEach((item) => {
    if (item.value.startsWith('{') || item.value.startsWith('[')) {
      item.value = JSON.stringify(JSON.parse(item.value), null, 2);
    }
    if (['DefaultUseAutoGroup', 'ExposeRatioEnabled'].includes(item.key)) {
      newInputs[item.key] = toBoolean(item.value);
    } else {
      newInputs[item.key] = item.value;
    }
  });
};
```

相关行：

- `web/classic/src/components/settings/RatioSetting.jsx:41`：初始化 `GroupRatio`、`GroupGroupRatio`、`AutoGroups` 等字段。
- `web/classic/src/components/settings/RatioSetting.jsx:68`：把 `DefaultUseAutoGroup` 从字符串转成 bool。
- `web/classic/src/components/settings/RatioSetting.jsx:104`：加载 `GroupRatioSettings`。

### 2.2 保存接口

`GroupRatioSettings` 只关心以下 key：

```jsx
const OPTION_KEYS = [
  'GroupRatio',
  'UserUsableGroups',
  'GroupGroupRatio',
  'group_ratio_setting.group_special_usable_group',
  'AutoGroups',
  'DefaultUseAutoGroup',
];
```

保存时，前端会比较当前值和原始值，只提交发生变化的 key：

```jsx
const updateArray = compareObjects(inputs, inputsRow);
const requestQueue = updateArray.map((item) => {
  const value =
    typeof inputs[item.key] === 'boolean'
      ? String(inputs[item.key])
      : inputs[item.key];
  return API.put('/api/option/', { key: item.key, value });
});
```

相关行：

- `web/classic/src/pages/Setting/Ratio/GroupRatioSettings.jsx:54`：本页 option key 列表。
- `web/classic/src/pages/Setting/Ratio/GroupRatioSettings.jsx:114`：逐项调用 `PUT /api/option/`。
- `controller/option.go:101`：后端 `UpdateOption` 接收 `{ key, value }`。
- `model/option.go:18`：数据库表模型 `options`，主键为 `key`，值为 `value`。
- `model/option.go:205`：`model.UpdateOption` 先写数据库，再刷新内存配置。

数据库层面，这些配置都落在 `options` 表：

```go
type Option struct {
    Key   string `json:"key" gorm:"primaryKey"`
    Value string `json:"value"`
}
```

典型数据库行示例：

```text
key                                           value
--------------------------------------------  --------------------------------------------------
GroupRatio                                    {"default":1,"vip":0.5}
UserUsableGroups                              {"standard":"标准价格","premium":"高级套餐"}
GroupGroupRatio                               {"vip":{"standard":0.8}}
AutoGroups                                    ["default","vip"]
DefaultUseAutoGroup                           true
group_ratio_setting.group_special_usable_group {"vip":{"+:exclusive":"专属分组","-:standard":"remove"}}
```

## 3. 配置加载和内存落点

系统启动时，`model.InitOptionMap` 会先把默认值放入 `common.OptionMap`，再调用 `loadOptionsFromDatabase` 用数据库里的 `options` 覆盖默认值：

- `model/option.go:30`：初始化 `OptionMap`。
- `model/option.go:119`：写入默认 `AutoGroups`。
- `model/option.go:120`：写入默认 `DefaultUseAutoGroup`。
- `model/option.go:144`：写入默认 `GroupRatio`。
- `model/option.go:145`：写入默认 `GroupGroupRatio`。
- `model/option.go:146`：写入默认 `UserUsableGroups`。
- `model/option.go:178`：导出所有注册的分层配置，生成类似 `group_ratio_setting.group_special_usable_group` 的扁平 key。
- `model/option.go:187`：从数据库加载 option。

普通 key 在 `model.updateOptionMap` 的 `switch` 里更新内存变量：

- `model/option.go:387`：`AutoGroups` -> `setting.UpdateAutoGroupsByJsonString`。
- `model/option.go:523`：`GroupRatio` -> `ratio_setting.UpdateGroupRatioByJSONString`。
- `model/option.go:525`：`GroupGroupRatio` -> `ratio_setting.UpdateGroupGroupRatioByJSONString`。
- `model/option.go:527`：`UserUsableGroups` -> `setting.UpdateUserUsableGroupsByJSONString`。
- `model/option.go:357`：`DefaultUseAutoGroup` -> `setting.DefaultUseAutoGroup`。

`group_ratio_setting.group_special_usable_group` 不是普通 `switch` key。它走分层配置：

- `setting/ratio_setting/group_ratio.go:56`：`config.GlobalConfig.Register("group_ratio_setting", &groupRatioSetting)`。
- `model/option.go:573`：`handleConfigUpdate` 识别带点号的 key。
- `model/option.go:593`：通过 `config.UpdateConfigFromMap` 更新注册配置对象。
- `setting/config/config.go:286`：`ExportAllConfigs` 用 `模块名.配置项` 形式导出扁平 key。

这意味着前端保存：

```text
group_ratio_setting.group_special_usable_group
```

实际更新的是：

```go
ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup
```

## 4. 设置项逐项分析

### 4.1 分组管理：`GroupRatio` 与 `UserUsableGroups`

经典前端在可视化模式中把两个 JSON 合并成一张表：

| 表格列 | 写入位置 | 实际含义 |
| --- | --- | --- |
| 分组名称 | `GroupRatio` 的 key；勾选用户可选时也是 `UserUsableGroups` 的 key | 系统里的目标分组名称 |
| 倍率 | `GroupRatio[group]` | 该目标分组的基础计费倍率 |
| 用户可选 | 是否写入 `UserUsableGroups[group]` | 用户创建令牌时是否能自主选择该分组 |
| 描述 | `UserUsableGroups[group]` | 前端下拉框显示描述 |

前端实现：

- `web/classic/src/pages/Setting/Ratio/components/GroupTable.jsx:28`：解析 `GroupRatio` 和 `UserUsableGroups`。
- `web/classic/src/pages/Setting/Ratio/components/GroupTable.jsx:46`：序列化回两个 JSON。
- `web/classic/src/pages/Setting/Ratio/components/GroupTable.jsx:52`：每行都写入 `GroupRatio`。
- `web/classic/src/pages/Setting/Ratio/components/GroupTable.jsx:53`：只有勾选「用户可选」才写入 `UserUsableGroups`。

后端默认值：

```go
// setting/ratio_setting/group_ratio.go
var defaultGroupRatio = map[string]float64{
    "default": 1,
    "vip":     1,
    "svip":    1,
}

// setting/user_usable_group.go
var userUsableGroups = map[string]string{
    "default": "默认分组",
    "vip":     "vip分组",
}
```

实际作用分两层。

第一层，`GroupRatio` 定义正式目标分组和基础倍率：

- `setting/ratio_setting/group_ratio.go:71`：`ContainsGroupRatio` 判断分组是否存在。
- `setting/ratio_setting/group_ratio.go:84`：`GetGroupRatio` 获取分组倍率，不存在时回退为 `1` 并写系统日志。
- `controller/option.go:227`：保存 `GroupRatio` 前会校验倍率不能小于 `0`。

第二层，`UserUsableGroups` 定义普通用户创建令牌时可见、可用的分组：

- `setting/user_usable_group.go:16`：获取全局用户可选分组副本。
- `service/group.go:10`：`GetUserUsableGroups(userGroup)` 在全局可选分组基础上叠加特殊可用规则。
- `controller/group.go:31`：`GET /api/user/self/groups` 返回当前用户可用分组。
- `web/classic/src/components/table/tokens/modals/EditTokenModal.jsx:137`：令牌弹窗调用 `/api/user/self/groups`。
- `web/classic/src/components/table/tokens/modals/EditTokenModal.jsx:386`：令牌分组下拉框使用这些分组。

运行时鉴权再次校验令牌分组：

```go
// middleware/auth.go
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
```

对应行：

- `middleware/auth.go:396`：令牌分组必须在当前用户可用分组中。
- `middleware/auth.go:401`：非 `auto` 令牌分组还必须存在于 `GroupRatio`。

关键结论：

- `GroupRatio` 只说明「系统存在这个目标分组」和「基础倍率是多少」。
- `UserUsableGroups` 才决定普通用户是否能在令牌创建页选择该分组。
- 如果某个分组只存在于 `GroupRatio`，没有进入 `UserUsableGroups`，普通用户默认看不到，也不能自己创建该分组令牌。
- `service.GetUserUsableGroups` 会把用户自身的 `users.group` 补回可用列表，避免用户当前所属分组不在全局可选表时完全不可用。

### 4.2 自动分组候选：`AutoGroups`

页面字段「自动分组 auto，从第一个开始选择」写入 `AutoGroups`。

前端实现：

- `web/classic/src/pages/Setting/Ratio/components/AutoGroupList.jsx:22`：解析 JSON 字符串数组。
- `web/classic/src/pages/Setting/Ratio/components/AutoGroupList.jsx:35`：序列化为 `["default","vip"]`；空列表会保存为空字符串。
- `web/classic/src/pages/Setting/Ratio/components/AutoGroupList.jsx:76`：支持上移、下移，顺序即优先级。
- `web/classic/src/pages/Setting/Ratio/components/AutoGroupList.jsx:130`：允许创建不在当前 `GroupRatio` 下拉里的候选名。

后端配置：

```go
// setting/auto_group.go
var autoGroups = []string{
    "default",
}

func UpdateAutoGroupsByJsonString(jsonString string) error {
    autoGroups = make([]string, 0)
    return common.Unmarshal([]byte(jsonString), &autoGroups)
}

func GetAutoGroups() []string {
    return autoGroups
}
```

运行时自动候选不是直接使用 `AutoGroups` 全量，而是先按用户可用分组过滤：

```go
// service/group.go
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

渠道选择时，如果令牌分组是 `auto`：

- `service/channel_select.go:91`：`AutoGroups` 为空直接报错 `auto groups is not enabled`。
- `service/channel_select.go:94`：调用 `GetUserAutoGroup(userGroup)`，只保留当前用户可用的自动候选。
- `service/channel_select.go:111`：按候选顺序调用 `model.GetRandomSatisfiedChannel(autoGroup, modelName, priorityRetry, requestPath)`。
- `service/channel_select.go:124`：选中后把具体分组写入 `ContextKeyAutoGroup`。
- `relay/helper/price.go:45`：计费前读取 `auto_group`，把 `relayInfo.UsingGroup` 改成最终选中的具体分组。

关键结论：

- `AutoGroups` 是 `auto` 令牌的候选优先级列表，不是授权列表。
- `auto` 本身也要能通过令牌鉴权。也就是说，用户可用分组中需要包含 `auto`，否则 `tokens.group = "auto"` 会在 `middleware/auth.go:396` 被拒绝。
- 自动候选还要同时满足「用户可用」和「对应渠道有该分组 + 模型能力」。
- `AutoGroups` 中的顺序会影响优先选择哪个渠道分组。
- 清空自动分组时，建议在手动模式保存为合法空数组 `[]`。当前可视化组件空列表会序列化为空字符串，而后端 `setting.UpdateAutoGroupsByJsonString` 走标准 JSON 反序列化，空字符串不是合法 JSON。

### 4.3 默认使用 auto 分组：`DefaultUseAutoGroup`

页面字段「默认使用 auto 分组」写入 bool option `DefaultUseAutoGroup`。

前端：

- `web/classic/src/pages/Setting/Ratio/GroupRatioSettings.jsx:204`：可视化模式开关。
- `web/classic/src/pages/Setting/Ratio/GroupRatioSettings.jsx:409`：手动模式开关。
- `web/classic/src/components/settings/RatioSetting.jsx:68`：从 option 字符串转 bool。

经典前端令牌弹窗读取 `/api/status` 里的 `default_use_auto_group`：

- `controller/misc.go:93`：`GET /api/status` 返回 `default_use_auto_group`。
- `web/classic/src/components/table/tokens/modals/EditTokenModal.jsx:145`：如果开启，令牌分组下拉里把 `auto` 排到前面。
- `web/classic/src/components/table/tokens/modals/EditTokenModal.jsx:151`：自动把表单分组设为 `auto` 的代码目前是注释状态。

后端真正会使用该配置的位置是用户注册时生成初始令牌：

```go
// controller/user.go
if constant.GenerateDefaultToken {
    token := model.Token{
        // ...
    }
    if setting.DefaultUseAutoGroup {
        token.Group = "auto"
    }
    if err := token.Insert(); err != nil {
        // ...
    }
}
```

对应行：

- `controller/user.go:239`：注册时判断是否生成默认令牌。
- `controller/user.go:258`：`DefaultUseAutoGroup` 为 true 时，把初始令牌 `Group` 设为 `auto`。

关键结论：

- 在当前经典前端里，它不会自动把「手动新建令牌」的表单值设为 `auto`，只会把 `auto` 选项排到前面。
- 后端实际作用是：启用注册生成默认令牌时，新注册用户的初始令牌会被设为 `auto`。
- 它不会自动把 `auto` 加入 `UserUsableGroups`，也不会自动配置 `AutoGroups` 候选。缺少这些配置时，`auto` 令牌仍可能无法通过鉴权或无法选到渠道。

### 4.4 分组特殊倍率：`GroupGroupRatio`

页面字段「分组特殊倍率」写入 `GroupGroupRatio`。

JSON 结构是：

```json
{
  "vip": {
    "standard": 0.8,
    "premium": 0.3
  }
}
```

含义是：

```text
用户分组 vip 使用 standard 目标分组时，计费倍率改为 0.8
用户分组 vip 使用 premium 目标分组时，计费倍率改为 0.3
```

前端实现：

- `web/classic/src/pages/Setting/Ratio/components/GroupGroupRatioRules.jsx:34`：把嵌套 JSON 展平成规则行。
- `web/classic/src/pages/Setting/Ratio/components/GroupGroupRatioRules.jsx:50`：规则行再嵌套回 `userGroup -> usingGroup -> ratio`。
- `web/classic/src/pages/Setting/Ratio/components/GroupGroupRatioRules.jsx:60`：序列化为空时保存空字符串，否则保存格式化 JSON。

后端：

```go
// setting/ratio_setting/group_ratio.go
func GetGroupGroupRatio(userGroup, usingGroup string) (float64, bool) {
    gp, ok := groupGroupRatioMap.Get(userGroup)
    if !ok {
        return -1, false
    }
    ratio, ok := gp[usingGroup]
    if !ok {
        return -1, false
    }
    return ratio, true
}
```

计费时优先级：

```go
// relay/helper/price.go
userGroupRatio, ok := ratio_setting.GetGroupGroupRatio(relayInfo.UserGroup, relayInfo.UsingGroup)
if ok {
    groupRatioInfo.GroupSpecialRatio = userGroupRatio
    groupRatioInfo.GroupRatio = userGroupRatio
    groupRatioInfo.HasSpecialRatio = true
} else {
    groupRatioInfo.GroupRatio = ratio_setting.GetGroupRatio(relayInfo.UsingGroup)
}
```

对应行：

- `setting/ratio_setting/group_ratio.go:93`：读取特殊倍率。
- `relay/helper/price.go:53`：特殊倍率优先于普通分组倍率。
- `relay/helper/price.go:61`：未命中特殊倍率时才使用 `GroupRatio[UsingGroup]`。
- `service/group.go:59`：价格页 / 用户分组接口展示倍率时也使用同样优先级。
- `controller/pricing.go:50`：价格页按当前用户分组覆盖展示 `group_ratio`。

关键结论：

- 第一层 key 是用户原始分组（`users.group` / `RelayInfo.UserGroup`）。
- 第二层 key 是本次实际使用的目标分组（`UsingGroup`，可能来自用户分组、令牌分组，或 `auto` 最终选中的具体分组）。
- 一旦命中特殊倍率，扣费和日志里的 `group_ratio` 都使用该特殊值；日志还会记录 `user_group_ratio`。
- 该设置只影响计费倍率，不授予用户使用某个分组的权限。
- 清空规则时，建议在手动模式保存为 `{}`。当前可视化组件空规则会序列化为空字符串，而 `types.LoadFromJsonString` 会先清空内存 map 再反序列化；空字符串会返回 JSON 错误。

### 4.5 分组特殊可用分组：`group_ratio_setting.group_special_usable_group`

页面字段「分组特殊可用分组」写入分层配置 key：

```text
group_ratio_setting.group_special_usable_group
```

JSON 结构示例：

```json
{
  "vip": {
    "+:exclusive": "专属分组",
    "-:standard": "remove",
    "trial": "试用分组"
  }
}
```

前端将操作映射为 3 种 key：

| 页面操作 | JSON key | 后端行为 |
| --- | --- | --- |
| 添加 | `+:groupName` | 在默认可用列表基础上新增该分组 |
| 移除 | `-:groupName` | 从默认可用列表中删除该分组 |
| 追加 | `groupName` | 直接追加该分组，效果近似添加 |

前端实现：

- `web/classic/src/pages/Setting/Ratio/components/GroupSpecialUsableRules.jsx:46`：解析 `+:`、`-:` 前缀。
- `web/classic/src/pages/Setting/Ratio/components/GroupSpecialUsableRules.jsx:52`：把页面操作转回原始 key。
- `web/classic/src/pages/Setting/Ratio/components/GroupSpecialUsableRules.jsx:91`：序列化为嵌套 JSON。
- `web/classic/src/pages/Setting/Ratio/GroupRatioSettings.jsx:248`：写回 `group_ratio_setting.group_special_usable_group`。

后端配置结构：

```go
// setting/ratio_setting/group_ratio.go
type GroupRatioSetting struct {
    GroupRatio              *types.RWMap[string, float64]            `json:"group_ratio"`
    GroupGroupRatio         *types.RWMap[string, map[string]float64] `json:"group_group_ratio"`
    GroupSpecialUsableGroup *types.RWMap[string, map[string]string]  `json:"group_special_usable_group"`
}
```

运行时在 `service.GetUserUsableGroups` 中生效：

```go
func GetUserUsableGroups(userGroup string) map[string]string {
    groupsCopy := setting.GetUserUsableGroupsCopy()
    if userGroup != "" {
        specialSettings, b := ratio_setting.GetGroupRatioSetting().GroupSpecialUsableGroup.Get(userGroup)
        if b {
            for specialGroup, desc := range specialSettings {
                if strings.HasPrefix(specialGroup, "-:") {
                    groupToRemove := strings.TrimPrefix(specialGroup, "-:")
                    delete(groupsCopy, groupToRemove)
                } else if strings.HasPrefix(specialGroup, "+:") {
                    groupToAdd := strings.TrimPrefix(specialGroup, "+:")
                    groupsCopy[groupToAdd] = desc
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

对应行：

- `service/group.go:10`：以全局 `UserUsableGroups` 为基础。
- `service/group.go:13`：按用户分组读取特殊可用规则。
- `service/group.go:16`：`-:` 移除默认可用分组。
- `service/group.go:20`：`+:` 添加分组。
- `service/group.go:24`：无前缀直接添加分组。
- `service/group.go:31`：把用户自身分组补回可用列表。

关键结论：

- 这是按「用户分组」定制令牌可选分组列表的机制。
- 它会影响用户创建令牌下拉框、令牌鉴权、价格页过滤、用户可用模型列表和 `auto` 候选过滤。
- 它不会修改 `GroupRatio`。如果通过特殊可用分组给用户添加了一个目标分组，但 `GroupRatio` 没有这个分组，普通令牌请求会在 `middleware/auth.go:401` 被认为「分组已被弃用」。唯一例外是 `auto`。
- 因此新增可用目标分组时，通常要同时保证 `GroupRatio` 中存在该分组。
- 清空规则时，建议保存为 `{}`。该项虽然走分层配置，不在普通 `switch` 里，但底层仍按 JSON map 更新；合法空对象比空字符串更稳定。

## 5. 与运行时功能的关系

### 5.1 用户创建令牌

链路：

```text
经典前端 EditTokenModal
  -> GET /api/user/self/groups
  -> controller.GetUserGroups
  -> service.GetUserUsableGroups(user.Group)
  -> GroupRatio 过滤 + GroupGroupRatio 展示倍率
```

相关代码：

- `web/classic/src/components/table/tokens/modals/EditTokenModal.jsx:137`：加载用户可用分组。
- `controller/group.go:26`：`GetUserGroups`。
- `controller/group.go:31`：按用户分组计算可用分组。
- `controller/group.go:32`：只遍历 `GroupRatio` 中的正式分组。
- `controller/group.go:36`：返回 `service.GetUserGroupRatio` 计算后的展示倍率。

### 5.2 API 令牌鉴权

链路：

```text
TokenAuth
  -> 读取用户缓存得到 users.group
  -> 读取 tokens.group
  -> 若 tokens.group 非空：先校验用户是否可用该分组
  -> 再校验分组是否存在于 GroupRatio，auto 除外
  -> 写入 ContextKeyUsingGroup
```

相关代码：

- `middleware/auth.go:389`：读取用户分组和令牌分组。
- `middleware/auth.go:396`：校验令牌分组是否在用户可用分组中。
- `middleware/auth.go:401`：校验非 `auto` 分组是否仍存在于 `GroupRatio`。
- `middleware/auth.go:408`：写入 `ContextKeyUsingGroup`。

### 5.3 渠道选择

链路：

```text
Distribute
  -> 读取 ContextKeyUsingGroup
  -> 非 auto：按 UsingGroup + model 选渠道
  -> auto：按 GetUserAutoGroup(userGroup) 逐个候选分组选渠道
```

相关代码：

- `middleware/distributor.go:84`：读取 `ContextKeyUsingGroup`。
- `middleware/distributor.go:132`：调用 `CacheGetRandomSatisfiedChannel`。
- `service/channel_select.go:91`：处理 `auto` 分组。
- `service/channel_select.go:111`：按候选分组和模型选择渠道。

### 5.4 计费倍率

链路：

```text
ModelPriceHelper / ModelPriceHelperPerCall
  -> HandleGroupRatio
  -> 若 auto 已选中具体分组，UsingGroup 改为该具体分组
  -> 先查 GroupGroupRatio[userGroup][usingGroup]
  -> 未命中再查 GroupRatio[usingGroup]
  -> 预扣费和最终扣费都乘以该倍率
```

相关代码：

- `relay/helper/price.go:39`：`HandleGroupRatio`。
- `relay/helper/price.go:45`：`auto` 最终分组覆盖 `UsingGroup`。
- `relay/helper/price.go:53`：特殊倍率优先。
- `relay/helper/price.go:120`：按模型倍率计费时乘 `groupRatio`。
- `relay/helper/price.go:126`：固定价格计费时乘 `groupRatio`。
- `relay/helper/price.go:311`：分层表达式计费时也乘 `groupRatio`。
- `service/text_quota.go:85`：最终文本扣费继续使用 `summary.GroupRatio`。

### 5.5 价格页和用户可用模型

价格页：

- `controller/pricing.go:41`：先复制全量 `GroupRatio`。
- `controller/pricing.go:50`：登录用户命中特殊倍率时覆盖展示倍率。
- `controller/pricing.go:58`：计算当前用户可用分组。
- `controller/pricing.go:61`：剔除当前用户不可用的分组倍率。
- `controller/pricing.go:74`：返回 `auto_groups`。

用户可用模型：

- `controller/user.go:587`：`GetUserModels` 先取当前用户可用分组。
- 后续按这些分组汇总可用模型，间接受 `UserUsableGroups` 和特殊可用分组影响。

## 6. 典型配置组合

### 6.1 开放两个可选档位

目标：普通用户创建令牌时可选 `standard` 和 `premium`，`premium` 半价。

```json
{
  "GroupRatio": {
    "standard": 1,
    "premium": 0.5
  },
  "UserUsableGroups": {
    "standard": "标准价格",
    "premium": "高级套餐，半价优惠"
  }
}
```

效果：

- 令牌创建下拉框显示 `standard`、`premium`。
- 使用 `premium` 令牌请求时，计费乘以 `0.5`。
- 渠道侧仍必须有支持 `premium` 分组和对应模型的渠道能力，否则会找不到渠道。

### 6.2 vip 用户看到额外专属分组

目标：所有用户默认可选 `standard`、`premium`；`vip` 用户额外可选 `exclusive`，且看不到 `standard`。

```json
{
  "GroupRatio": {
    "standard": 1,
    "premium": 0.5,
    "exclusive": 0.3
  },
  "UserUsableGroups": {
    "standard": "标准价格",
    "premium": "高级套餐"
  },
  "group_ratio_setting.group_special_usable_group": {
    "vip": {
      "+:exclusive": "专属分组",
      "-:standard": "remove"
    }
  }
}
```

效果：

- 普通用户可选 `standard`、`premium`。
- `vip` 用户可选 `premium`、`exclusive`，不可选 `standard`。
- `exclusive` 已存在于 `GroupRatio`，因此令牌鉴权不会被判定为废弃分组。

### 6.3 auto 令牌按顺序兜底

目标：用户可以创建 `auto` 令牌，系统先尝试 `premium`，再尝试 `standard`。

```json
{
  "GroupRatio": {
    "standard": 1,
    "premium": 0.5
  },
  "UserUsableGroups": {
    "auto": "自动选择",
    "standard": "标准价格",
    "premium": "高级套餐"
  },
  "AutoGroups": ["premium", "standard"],
  "DefaultUseAutoGroup": true
}
```

效果：

- 用户可创建 `auto` 令牌，因为 `UserUsableGroups` 中包含 `auto`。
- 请求时，调度先尝试 `premium` 分组渠道，再尝试 `standard` 分组渠道。
- `DefaultUseAutoGroup` 只影响注册生成初始令牌，以及经典前端令牌弹窗中 `auto` 的排序；它不会自动补齐 `UserUsableGroups["auto"]`。

### 6.4 vip 用户使用 standard 令牌也打折

目标：`standard` 基础倍率为 `1`，但 `vip` 用户使用 `standard` 令牌时按 `0.8` 计费。

```json
{
  "GroupRatio": {
    "standard": 1
  },
  "UserUsableGroups": {
    "standard": "标准价格"
  },
  "GroupGroupRatio": {
    "vip": {
      "standard": 0.8
    }
  }
}
```

效果：

- 普通用户使用 `standard`：倍率 `1`。
- `vip` 用户使用 `standard`：倍率 `0.8`。
- 这只影响计费，不影响该用户是否能选择 `standard`；权限仍由 `UserUsableGroups` 和特殊可用规则决定。

## 7. 易混点和实际结论

1. `GroupRatio` 和 `UserUsableGroups` 不是同一个东西。前者定义正式分组和基础倍率，后者定义用户能不能选择该分组。
2. 「用户可选」只控制写不写入 `UserUsableGroups`。不勾选时，分组仍然可以存在于 `GroupRatio`，也可以被管理员分配为用户分组或用于特殊规则。
3. `group_ratio_setting.group_special_usable_group` 是按用户分组改造可用分组列表，不是倍率配置。
4. `GroupGroupRatio` 是按用户分组覆盖计费倍率，不授予访问权限。
5. `auto` 是伪令牌分组，不是实际渠道分组。实际渠道分组来自 `AutoGroups` 中第一个可用候选。
6. 要让 `auto` 能用，至少需要：用户可用分组包含 `auto`，`AutoGroups` 中有候选，候选分组对当前用户可用，渠道能力里有候选分组 + 模型。
7. `DefaultUseAutoGroup` 在当前经典前端不会自动选中令牌表单的 `auto`，相关代码是注释状态；后端只在注册生成初始令牌时把令牌设为 `auto`。
8. 删除 `GroupRatio` 中某个分组后，已有令牌如果仍使用该分组，会被 `middleware/auth.go:401` 拦截为「分组已被弃用」。
9. 清空 JSON 配置时，建议手动写合法空 JSON：对象类用 `{}`，数组类用 `[]`，不要保存空字符串。

## 8. 代码索引

| 功能 | 代码位置 |
| --- | --- |
| 系统设置入口 | `web/classic/src/pages/Setting/index.jsx:115` |
| 分组相关设置 tab | `web/classic/src/components/settings/RatioSetting.jsx:104` |
| 分组相关设置主组件 | `web/classic/src/pages/Setting/Ratio/GroupRatioSettings.jsx:54` |
| 分组管理可视化表格 | `web/classic/src/pages/Setting/Ratio/components/GroupTable.jsx:46` |
| 自动分组列表 | `web/classic/src/pages/Setting/Ratio/components/AutoGroupList.jsx:22` |
| 特殊倍率规则 | `web/classic/src/pages/Setting/Ratio/components/GroupGroupRatioRules.jsx:60` |
| 特殊可用分组规则 | `web/classic/src/pages/Setting/Ratio/components/GroupSpecialUsableRules.jsx:91` |
| option 表模型 | `model/option.go:18` |
| option 保存与内存刷新 | `model/option.go:205` |
| 分层配置更新 | `model/option.go:573` |
| 分组倍率配置 | `setting/ratio_setting/group_ratio.go:12` |
| 用户可选分组配置 | `setting/user_usable_group.go:8` |
| 自动分组配置 | `setting/auto_group.go:7` |
| 用户可用分组计算 | `service/group.go:10` |
| 令牌分组鉴权 | `middleware/auth.go:396` |
| auto 渠道选择 | `service/channel_select.go:91` |
| 计费倍率应用 | `relay/helper/price.go:39` |
| 用户分组接口 | `controller/group.go:26` |
| 价格页分组过滤 | `controller/pricing.go:41` |

## 9. 与整体分组机制文档的关系

本文是页面级调研，解释经典前端「分组相关设置」每个控件最终写入什么、如何生效。整体概念和三类分组（用户分组、渠道分组、令牌分组）的系统级关系，见 `docs/llm-docs/21-group-mechanism-analysis.md`。


