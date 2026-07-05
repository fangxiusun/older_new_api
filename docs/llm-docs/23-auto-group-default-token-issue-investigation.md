# auto 分组未出现在令牌下拉与请求落到 default 的问题调查

## 1. 问题结论

你当前遇到的现象符合现有代码逻辑，不是渠道 5 的 `discount_80` 配置没有生效，而是请求根本没有进入 `auto` 分组选择链路。

你的配置摘要：

| 项 | 当前配置 |
| --- | --- |
| 当前用户身份 | root 用户，用户分组为 `default` |
| 用户可选分组 | `default`、`discount_95`、`discount_90`、`discount_85`、`discount_80` |
| 未选择为用户可选 | `vip`、`svip`，同时也没有 `auto` |
| `DefaultUseAutoGroup` | 已开启 |
| `AutoGroups` 顺序 | `discount_80`、`discount_85`、`discount_90`、`discount_95`、`default` |
| 渠道 5 | 分组 `discount_80`，模型 `qwen3.7-max-triple` |
| 新建令牌 | 令牌分组下拉没有 `auto`，只能不填写 |
| 请求结果 | 报错：`分组 default 下模型 qwen3.7-max-triple 无可用渠道` |

根因链路：

```text
UserUsableGroups 中没有 auto
  -> GET /api/user/self/groups 不返回 auto
  -> 经典前端令牌分组下拉没有 auto
  -> 新建令牌只能留空 tokens.group = ""
  -> TokenAuth 发现 token.group 为空，使用用户分组 users.group = default
  -> Distribute 按 default + qwen3.7-max-triple 选渠道
  -> 你的渠道 5 在 discount_80，不在 default
  -> 报错 default 下无可用渠道
```

一句话：`DefaultUseAutoGroup` 不会让「分组为空」的令牌自动走 `auto`。只有令牌本身的 `tokens.group = "auto"` 时，才会进入 `AutoGroups` 候选选择。

## 2. 为什么令牌下拉没有 auto

经典前端添加令牌时，分组选项来自：

```jsx
// web/classic/src/components/table/tokens/modals/EditTokenModal.jsx
const loadGroups = async () => {
  let res = await API.get(`/api/user/self/groups`);
  const { success, message, data } = res.data;
  if (success) {
    let localGroupOptions = Object.entries(data).map(([group, info]) => ({
      label: info.desc,
      value: group,
      ratio: info.ratio,
    }));
    if (statusState?.status?.default_use_auto_group) {
      if (localGroupOptions.some((group) => group.value === 'auto')) {
        localGroupOptions.sort((a, b) => (a.value === 'auto' ? -1 : 1));
      }
    }
    setGroups(localGroupOptions);
    // if (statusState?.status?.default_use_auto_group && formApiRef.current) {
    //   formApiRef.current.setValue('group', 'auto');
    // }
  }
};
```

关键点：

- 前端只调用 `/api/user/self/groups`，不会自己构造 `auto`。
- `DefaultUseAutoGroup` 只在 `auto` 已经存在时把它排到前面。
- 自动设置表单值为 `auto` 的代码是注释状态，不会执行。

后端 `/api/user/self/groups` 的返回逻辑在 `controller/group.go`：

```go
func GetUserGroups(c *gin.Context) {
    usableGroups := make(map[string]map[string]interface{})
    userGroup := ""
    userId := c.GetInt("id")
    userGroup, _ = model.GetUserGroup(userId, false)
    userUsableGroups := service.GetUserUsableGroups(userGroup)
    for groupName, _ := range ratio_setting.GetGroupRatioCopy() {
        if desc, ok := userUsableGroups[groupName]; ok {
            usableGroups[groupName] = map[string]interface{}{
                "ratio": service.GetUserGroupRatio(userGroup, groupName),
                "desc":  desc,
            }
        }
    }
    if _, ok := userUsableGroups["auto"]; ok {
        usableGroups["auto"] = map[string]interface{}{
            "ratio": "自动",
            "desc":  setting.GetUsableGroupDescription("auto"),
        }
    }
    // ...
}
```

这段代码说明：

- 普通分组只返回同时存在于 `GroupRatio` 和当前用户可用分组里的分组。
- `auto` 是特殊项，不要求存在于 `GroupRatio`。
- 但 `auto` 必须存在于 `service.GetUserUsableGroups(userGroup)` 的结果里，才会被额外返回。

你的「用户可见分组」里没有选择 `auto`，所以 `userUsableGroups["auto"]` 不存在，令牌下拉自然没有 `auto`。

## 3. 为什么分组为空的 API Key 会使用 default

令牌模型里，分组字段默认是空字符串：

```go
// model/token.go
type Token struct {
    Group           string `json:"group" gorm:"default:''"`
    CrossGroupRetry bool   `json:"cross_group_retry"`
}
```

添加令牌时，后端直接保存前端提交的 `token.Group`：

```go
// controller/token.go
cleanToken := model.Token{
    UserId:             c.GetInt("id"),
    Name:               token.Name,
    // ...
    Group:              token.Group,
    CrossGroupRetry:    token.CrossGroupRetry,
}
```

如果你在前端不填写令牌分组，保存到数据库的就是：

```text
tokens.group = ""
```

请求鉴权时，`middleware/auth.go` 的逻辑是：

```go
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

含义：

- `tokenGroup == ""` 时，不进入覆盖逻辑。
- `UsingGroup` 保持为用户分组。
- 你的用户分组是 `default`，所以本次请求的 `UsingGroup = default`。
- root 身份不会改变这条分发逻辑。只要走普通 Bearer token 请求，渠道选择仍按 `UsingGroup + model` 来查。

因此你的请求实际进入的选择条件是：

```text
group = default
model = qwen3.7-max-triple
```

而不是：

```text
group = auto
AutoGroups = discount_80 -> discount_85 -> discount_90 -> discount_95 -> default
```

## 4. 为什么报错是 default 下没有渠道

渠道选择在 `middleware/distributor.go` 中读取 `ContextKeyUsingGroup`：

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

由于本次 `usingGroup = default`，所以会走普通分组选择：

```go
// service/channel_select.go
channel, err = model.GetRandomSatisfiedChannel(param.TokenGroup, param.ModelName, param.GetRetry(), param.RequestPath)
```

内存缓存模式下，最终查的是：

```go
// model/channel_cache.go
channels := filterChannelsByRequestPath(group2model2channels[group][model], requestPath)
```

非内存缓存模式下，最终查的是 `abilities` 表：

```go
// model/ability.go
DB.Model(&Ability{}).
    Where(commonGroupCol+" = ? and model = ? and enabled = ?", group, model, true)
```

你的渠道 5 是：

```text
channels.group = discount_80
models 包含 qwen3.7-max-triple
```

它会生成或缓存成类似：

```text
abilities.group = discount_80
abilities.model = qwen3.7-max-triple
abilities.channel_id = 5
abilities.enabled = true
```

但是本次请求查的是：

```text
abilities.group = default
abilities.model = qwen3.7-max-triple
```

所以找不到渠道，错误信息就是：

```json
{
  "error": {
    "code": "model_not_found",
    "message": "分组 default 下模型 qwen3.7-max-triple 无可用渠道（distributor） ..."
  }
}
```

这个错误恰好证明本次请求没有走 `auto`，而是按 `default` 分发。

## 5. `DefaultUseAutoGroup` 的真实作用

`DefaultUseAutoGroup` 在当前代码中有两个实际影响。

### 5.1 前端只排序，不自动选中

经典前端令牌弹窗里：

```jsx
if (statusState?.status?.default_use_auto_group) {
  if (localGroupOptions.some((group) => group.value === 'auto')) {
    localGroupOptions.sort((a, b) => (a.value === 'auto' ? -1 : 1));
  }
}
// 自动 setValue('group', 'auto') 的代码已注释
```

所以它只会在 `auto` 已经存在于下拉选项中时，把 `auto` 放到前面。

### 5.2 后端只影响注册生成的初始令牌

注册新用户时，如果系统开启了生成默认令牌：

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

这不会修改已有令牌，也不会影响你手动在「令牌管理」里新建但分组留空的令牌。

## 6. 如何让你的场景按 discount_80 优先选择

你有三种可行配置路径。

### 6.1 推荐：让令牌显式选择 auto

目标链路：

```text
tokens.group = auto
  -> TokenAuth 通过 auto 权限校验
  -> Distribute 进入 auto 分支
  -> GetUserAutoGroup(default)
  -> 按 discount_80、discount_85、discount_90、discount_95、default 顺序找渠道
  -> 找到渠道 5
```

需要配置：

1. 在「系统设置 -> 分组与模型定价设置 -> 分组相关设置 -> 分组管理」中，让 `auto` 成为用户可选分组。
2. 保持 `discount_80`、`discount_85`、`discount_90`、`discount_95`、`default` 为用户可选分组。
3. 保持 `AutoGroups` 为：

```json
["discount_80", "discount_85", "discount_90", "discount_95", "default"]
```

4. 在「令牌管理 -> 添加令牌」中，把「令牌分组」选择为 `auto`。
5. 如需失败后跨候选分组继续重试，可以打开「跨分组重试」。

关于第 1 步有两个做法。

做法 A：可视化模式新增 `auto` 行并勾选「用户可选」：

```text
分组名称：auto
倍率：1
用户可选：勾选
描述：自动分组
```

做法 B：手动编辑 JSON，只在 `UserUsableGroups` 中加入 `auto`：

```json
{
  "default": "默认分组",
  "discount_95": "95 折",
  "discount_90": "90 折",
  "discount_85": "85 折",
  "discount_80": "80 折",
  "auto": "自动分组"
}
```

从后端代码看，`auto` 不必须存在于 `GroupRatio`，因为 `controller.GetUserGroups` 对 `auto` 做了特殊处理，`middleware.TokenAuth` 也允许 `auto` 不在 `GroupRatio` 中。可视化模式会把行同时写进 `GroupRatio` 和 `UserUsableGroups`，通常也能工作；但概念上更干净的是只把 `auto` 作为用户可选伪分组。

### 6.2 简单绕过：令牌直接选择 discount_80

因为你的用户可选分组已经包含 `discount_80`，令牌下拉应该可以选择 `discount_80`。

如果令牌分组直接选 `discount_80`：

```text
tokens.group = discount_80
  -> UsingGroup = discount_80
  -> 直接查 discount_80 + qwen3.7-max-triple
  -> 找到渠道 5
```

这个方案不会使用 `AutoGroups`，也没有自动兜底到其他折扣分组，但适合快速验证渠道 5 的能力是否正常。

### 6.3 供给侧修复：给 default 分组也配置渠道

如果你希望「令牌分组不填」也能使用该模型，那么需要让 `default` 分组下也有可用渠道。

做法包括：

- 把渠道 5 的分组改为 `discount_80,default`。
- 或新增一个支持 `qwen3.7-max-triple` 的 `default` 分组渠道。

这样空分组令牌会继续使用 `default`，但 `default + qwen3.7-max-triple` 能找到渠道。

## 7. 配置后如何验证

### 7.1 验证令牌下拉

配置 `auto` 为用户可选后，重新打开「令牌管理 -> 添加令牌」：

- 「令牌分组」下拉应出现 `auto`。
- 如果 `DefaultUseAutoGroup` 开启，`auto` 应排在更靠前的位置。
- 但仍需要手动选择 `auto`，当前经典前端不会自动填入。

### 7.2 验证令牌记录

新建或编辑令牌后，令牌记录应满足：

```text
tokens.group = auto
```

如果仍是空字符串，请求仍会使用用户分组 `default`。

### 7.3 验证请求行为

使用 `auto` 令牌请求时，预期分发链路是：

```text
auto
  -> discount_80
  -> 渠道 5
```

如果仍然报：

```text
分组 default 下模型 qwen3.7-max-triple 无可用渠道
```

说明这枚 Key 仍不是 `auto` 分组，或者请求使用的不是你刚更新的那枚 Key。

如果报：

```text
分组 auto(...) 下模型 qwen3.7-max-triple 无可用渠道
```

才说明已经进入 `auto` 链路，但候选分组下没有找到可用渠道，需要检查渠道能力。

### 7.4 验证渠道能力

渠道 5 修改分组或模型后，应确保 `abilities` 已重建并且缓存已刷新。代码里渠道更新会调用 `UpdateAbilities`，但如果历史数据不一致，可以使用系统里的能力修复功能或重新保存渠道。

需要确认能力关系等价于：

```text
group = discount_80
model = qwen3.7-max-triple
channel_id = 5
enabled = true
```

如果使用 MySQL / PostgreSQL / SQLite 查询，逻辑上可检查：

```sql
select *
from abilities
where `group` = 'discount_80'
  and model = 'qwen3.7-max-triple'
  and channel_id = 5
  and enabled = true;
```

注意：PostgreSQL 需要用 `"group"` 引号；MySQL 和 SQLite 使用反引号。

## 8. 对你当前配置的直接建议

按你的目标「优先用 `discount_80`，再逐级回退」，建议这样改：

1. 在用户可选分组中增加 `auto`，描述写「自动分组」。
2. 保持用户可选分组包含：

```text
auto
default
discount_95
discount_90
discount_85
discount_80
```

3. 保持自动分组列表：

```json
["discount_80", "discount_85", "discount_90", "discount_95", "default"]
```

4. 编辑现有 API Key 或新建 API Key，把「令牌分组」选择为 `auto`。
5. 再发同一个 curl。

如果只是想立刻验证渠道 5 是否能工作，可以先把令牌分组选成 `discount_80`，这会直接命中渠道 5，不依赖 `auto`。

## 9. 相关代码索引

| 关注点 | 文件 |
| --- | --- |
| 令牌分组选项加载 | `web/classic/src/components/table/tokens/modals/EditTokenModal.jsx` |
| `auto` 只排序不自动选择 | `web/classic/src/components/table/tokens/modals/EditTokenModal.jsx` |
| 用户可用分组接口 | `controller/group.go` |
| 用户可用分组合并 | `service/group.go` |
| 令牌分组为空时使用用户分组 | `middleware/auth.go` |
| `auto` 分组候选选择 | `service/channel_select.go` |
| 分发按 `UsingGroup + model` 选渠道 | `middleware/distributor.go` |
| 渠道能力缓存查询 | `model/channel_cache.go` |
| `abilities` 生成与查询 | `model/ability.go` |
| 令牌创建保存 `Group` | `controller/token.go` |

