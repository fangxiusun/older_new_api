# 34. Task 渠道计费参数透传审查与 newapi 渠道计费迁移

> 历史分析：当前 New API task 已收紧为已确认的 MiniMax-H3 白名单。本文的其他家族参考估算不再用于转发准入，当前接口、价格配置与拒绝规则以 [文档 33](33-newapi-task-relay.md) 为准。

> 状态：已实现并完成代码审查修复（newapi 渠道部分）
> 审查范围：`relay/channel/task/` 下全部 task 适配器
> 相关代码：`relay/channel/task/newapi/billing.go`、`ratios.go`
> 相关文档：`33-newapi-task-relay.md`、`11-billing-design.md`、`32-preconsume-indepth.md`

## 1. 审查目的与方法

newapi 渠道（`ChannelTypeNewAPI`）的上游是另一个 new-api 实例：客户端请求体（含
`metadata`）被**整体透传**，上游将按它自己的渠道配置对同样的参数计费。本站无法控制
上游定价，只能：

1. 按透传的计费参数做**等比估算**（预扣费与提交时差额结算）；
2. 对估算覆盖不到的计费参数记录 **WARN 日志**（提示无法正确计费）。

为此逐个审查了 `relay/channel/task/` 下各适配器：计费估算（`EstimateBilling`）读取的
参数是否与实际透传给上游的取值一致、上游是否回传用量、完成时是否重算
（`AdjustBillingOnComplete`）。newapi 优先复用原生估算或价格表；其余家族根据原生适配器
实际读取的字段进行参考价估算（`billing.go`），不代表已知上游站点的定价。

## 2. 审查结果汇总

结算链路回顾（`service/task_polling.go` `settleTaskBillingOnComplete`）：
按次计费任务跳过差额结算 → 优先 `AdjustBillingOnComplete`（仅 ali 实现）→
其次 `TaskInfo.TotalTokens > 0` 触发按倍率模型重算 → 否则**保持预扣额度**。
失败任务统一全额退款（`RefundTaskQuota`）。

| 适配器 | EstimateBilling | 估算/透传一致性 | 上游用量回传 | AdjustBillingOnComplete | 结论 |
| --- | --- | --- | --- | --- | --- |
| ali | seconds / resolution / audio | ✅ 同一构建函数 | `usage.duration` → CompletionTokens | ✅ usage.duration/video_count/SR 缩放 | 计费闭环；新格式 `audio_setting`/`ratio` 透传未计费（风险低-中） |
| doubao | video_input（分辨率+视频输入） | ✅ metadata 同名透传 | `usage.total_tokens` → TotalTokens（仅倍率模型生效） | ❌ | duration/seconds 未进估算，固定价模型依赖 token 重算失效（风险中） |
| hailuo | ❌（本次已在新api渠道迁移） | — | ❌ 无任何用量 | ❌ | metadata duration/resolution 透传但原渠道不计费（风险高） |
| jimeng | ❌（本次已在新api渠道迁移） | — | ❌ 无任何用量 | ❌ | metadata `frames`、duration=10 透传但不计费（风险高） |
| kling | ❌（本次已在新api渠道迁移） | — | `final_unit_deduction` → TotalTokens（缺失时保持预扣） | ❌ | mode=pro / duration=10 透传，仅当上游回传扣费且模型配置倍率时纠正（风险高） |
| gemini (Veo) | seconds + resolution | ✅ 同一 Resolve 函数 | ❌ | ❌ | metadata 自报时长/分辨率，3600s 钳制兜底（风险中，估算与透传一致） |
| vertex (Veo) | 同 gemini | ✅ | ❌ | ❌ | 同 gemini |
| sora | seconds + size | ✅ 原样透传 | ❌ | ❌ | 依赖上游校验 seconds（风险低） |
| suno | ❌（无时长/尺寸参数，按次） | — | ❌（批量轮询路径不结算） | ❌ | 按次固定价，无参数型绕过（风险低） |
| vidu | ❌（本次已在新api渠道迁移） | — | `credits` 已解析但被忽略 | ❌ | 时长/分辨率/metadata 注入均不计费（**风险最高**） |
| **newapi** | ✅（本次实现，见 §3） | ✅ 请求体整体透传 | ❌（上游 OpenAI video 对象无 usage 字段） | ❌ | 上游按自身配置计费；本站按透传参数估算 + warn（见 §4） |

## 3. 按模型分类的计费参数与新api渠道迁移映射

各原生渠道的计费相关参数均会随请求体（标准字段或 `metadata`）透传到上游 new-api，
newapi 渠道在 `relay/channel/task/newapi/billing.go` 中按模型家族迁移了对应的估算口径：

| 模型家族（前缀匹配） | 计费参数（透传来源） | 基准 | newapi 渠道 OtherRatios |
| --- | --- | --- | --- |
| sora-2 / 未识别模型 | 标准 `seconds` → `duration` → 默认 4 秒；标准 `size` | 每秒 | `seconds`、`size`；保留 Sora 大尺寸倍率 1.666667 |
| `kling-v*`（可灵） | `metadata.mode` → 标准 `mode` → std；`metadata.duration` → 标准 `duration` → 5 秒 | 每秒 | `seconds`、`size=1`、`mode`；按最长前缀匹配参考倍率 |
| `MiniMax-Hailuo-*` / `MiniMax-H3` / `T2V-*` / `I2V-*` / `S2V-*`（海螺） | `metadata.resolution` → 标准 `size` 对应档位 → 模型默认；`metadata.duration` → 标准 `duration` → 6 秒 | 每秒 | `seconds`、`size=1`、`resolution` |
| `jimeng_*`（即梦） | `metadata.frames`；缺省时标准 `duration=10` 对应 241 帧，其余对应 121 帧 | 每秒 | `seconds=(frames-1)/24`、`size=1`，显式和默认 121 帧均为 5 秒 |
| `vidu*`（Vidu） | `metadata.resolution` → 标准 `size` → 默认 1080p；`metadata.duration` → 标准 `duration` → 5 秒 | 每秒 | `seconds`、`size=1`、`resolution`；默认 1080p 也应用倍率 2 |
| `doubao-*`（Seedance） | 标准 `seconds` → `metadata.duration` → 5 秒；`metadata.resolution` 与 `content` 视频输入 | 每秒参考估算 | `seconds`、`size=1`；优先使用 `doubao.GetVideoInputRatio` 的 `video_input`，无价格表时使用参考分辨率倍率 |
| `wan*` / `wanx*` / `happyhorse-*`（阿里通义系） | 复用阿里原生请求构建与 `EstimateBilling`，包括标准字段迁移、metadata 优先级和默认值 | 与原生 Ali 一致 | `seconds`、`resolution-<档位>`、适用模型的 `audio`；不重复乘算 size |

这里的秒数倍率要求本地基价采用相应单位。不要将“默认生成 5 秒 / 6 秒”误作倍率 1。
`metadata.durationSeconds` 不参与上述家族的实际原生参数构建，不得覆盖有效时长。
标准 `seconds` / `duration` 分别验证；metadata 时长也必须是 0～3600 的整数，帧数上限由
`MaxTaskDurationSeconds * 24 + 1` 推导。负数、小数、整数溢出等输入返回 400。

> ⚠️ 表中倍率为参考官方定价换算的内置默认值，如与上游实际定价不符，
> 需调整 `ratios.go` / `billing.go` 中的表并重新编译（与 ali 渠道 aliRatios 同构）。

## 4. 无法正确计费的 WARN 日志

newapi 渠道在提交时的 `EstimateBilling` 中检测请求 `metadata`，命中以下任一情况即输出：

```
[billing] WARN: newapi 渠道模型 <model> 的请求 metadata 包含未被计费估算覆盖的参数
[<keys>]，上游将按其自身配置计费，本站无法正确计费，仅按预扣估算结算
```

触发条件（`familyBillingExtras`）：

| 家族 | 未覆盖即 warn 的 metadata 键 |
| --- | --- |
| 所有家族 | `audio_setting`、`ratio`、`service_tier`、`priority`、`generate_audio`、`bgm`、`draft`、`durationSeconds` |
| 非 Ali 家族 | 额外关注 `audio`；Ali 旧格式已将该参数纳入估算 |
| 非 Kling 家族 | 额外关注 `mode` |
| sora / jimeng | 额外关注 `resolution` 与 metadata 中的 `duration` |
| 非 Jimeng 家族 | 额外关注 `frames` |
| doubao 无模型价格表 | `content` 含视频输入时单独提示 |

日志通道为 `common.SysLog`（系统日志，估算每次提交至多一条）。

## 5. 遗留风险与后续建议

1. **上游用量不可得**：newapi 上游的任务查询接口（OpenAI video 对象）不返回 usage，
   本站无法在完成时校准，只能依赖预扣估算。WARN 仅覆盖当前已列出的参数，不保证发现所有偏差。
2. **上游定价不可知**：上游 new-api 对同一模型的计费由上游管理员配置，本站倍率表
   只是参考换算。上线前应以实际账单核对基价单位和常用参数组合；不要直接把整段视频价格填作每秒基价。
3. **原生渠道自身的缺口**（本次未扩展这些渠道的结算策略）：hailuo/jimeng/vidu 无估算、
   无用量回传；doubao duration 未进估算；kling 依赖上游回传 final_unit_deduction；
   ali 新格式 `audio_setting`/`ratio` 未计费。如需闭环可在各原生适配器内参照本文 §3
   的口径补充 EstimateBilling。
4. **gemini/vertex 的 metadata 自报时长**：估算与透传一致，无偏差型绕过，但客户端
   自报计费参数是结构性风险，仅有 3600s 钳制兜底。

## 6. 测试

`relay/channel/task/newapi/*_test.go` 覆盖：

- `TestDetectTaskFamily`：模型 → 家族前缀识别表测；
- `TestEstimateTaskRatiosFamilyExtras`：各家族的额外倍率（kling mode/时长覆盖、
  hailuo/vidu resolution、doubao 原生价格表、Ali 原生估算、jimeng 帧数换算）、Sora 尺寸倍率；
- 标准字段与 metadata 冲突时的优先级、默认值、最长模型前缀；
- 计费参数越界、小数、负数及整数表示溢出时的请求拒绝。

运行：`go test ./relay/channel/task/newapi/...`
