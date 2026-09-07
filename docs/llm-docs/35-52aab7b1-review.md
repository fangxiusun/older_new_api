# 35. 52aab7b1 起的代码审查与修复

> 历史审查记录：用户在 `bec9fd21` 中选择性保留了修复，本文不代表全部修复仍在当前代码中。当前 New API task 仅接入 MiniMax-H3 直链模型，支持范围见 [文档 33](33-newapi-task-relay.md)。

审查日期：2026-09-08。以下已确认问题均已在工作区修复，并补充相应回归验证。

## 1. 审查范围

- 起始提交（包含）：`52aab7b1f15892f00c4ec089de2c9f549c0423a7`。
- 比较基点：其父提交 `d7850df0e434b1217aa7c2cee7a88007c237018d`。
- 审查时 HEAD 即起始提交，没有更晚的提交；同时审查了会话开始时的未提交修改：New API 任务适配器、计费逻辑与倍率、Doubao 模型价格表及文档 34。
- 保留已有工作区修改，在此基础上修复；没有创建 Git 提交。未修改前端或 `relaykit/`。

## 2. P1：计费与核心任务链路

### 2.1 按上游实际采用的参数估算费用

**问题与触发条件：**

- Sora 请求 `seconds="12"`、`metadata.duration=1` 时，原估算被 metadata 覆盖成 1 秒，但 Sora 使用标准 `seconds`。
- Jimeng 的 `metadata.frames=241` 被按 `241/121` 计为约 1.99 倍；当前按秒基价下应为 `(241-1)/24=10` 秒，121 帧应为 5 秒。
- Kling 的标准 `mode` 未被纳入估算，带后缀的模型即使命中专属前缀，仍可能被通用倍率覆盖。
- Hailuo 标准尺寸、Vidu 默认 1080p、Doubao 的标准秒数优先级及视频输入价格表未被正确采用。
- Ali 同时使用通用尺寸倍率与分辨率倍率，可能重复乘算，也可能偏离模型专属价格表。

**修复：** 在 [billing.go](../../relay/channel/task/newapi/billing.go) 按各原生适配器的字段优先级和默认值取参数；统一实际秒数；Kling 使用最长前缀；Doubao 优先复用原生价格表；Ali 直接复用原生 `EstimateBilling`。保留 Sora 大尺寸的 `1.666667` 倍率。

**验证：** `TestBillingUsesEffectiveProviderParameters`、`TestEstimateTaskRatiosFamilyExtras`、`TestEstimateBillingSecondsAndSize`。

### 2.2 不能用合法 duration 掩盖非法 seconds

**问题与触发条件：** `duration=1`、`seconds="3601"` 可以绕过原先只验证一个字段的逻辑；溢出数字、非法字符串等解析失败还可能静默变成默认时长。metadata 中的时长、帧数同样可能绕过标准字段校验。

**修复：** [relay_utils.go](../../relay/common/relay_utils.go) 分别检查两个标准字段；[relay_info.go](../../relay/common/relay_info.go) 拒绝无法正确解析的 duration；New API 校验 metadata 中的计费数量，要求有限、非负、整数并有上限。时长复用 `MaxTaskDurationSeconds`，帧数上限由时长上限推导。错误在提交上游前返回 400。

计费金额继续经过 `AddOtherRatio`、`QuotaFromFloatChecked` 和现有饱和审计、预扣、结算与退款链路。

**验证：** `TestValidateRequestRejectsUnboundedBillingParameters` 及 `relay/common` 的时长解析、边界回归测试。

### 2.3 New API 下载和轮询缺少任务所属账户信息

**问题与触发条件：** 多密钥渠道提交成功后未保存本次选中的密钥，后台轮询回退到整个渠道密钥字符串，导致鉴权失败；下载控制器也没有 New API 专用分支，不能可靠访问上游需要鉴权的内容端点。

**修复：** [model/task.go](../../model/task.go) 保存实际选中的 New API 密钥，供既有轮询逻辑使用；[video_proxy.go](../../controller/video_proxy.go) 使用保存的上游 ID 和任务密钥请求 `/v1/videos/{id}/content`。

**验证：** `TestVideoProxyNewAPIUsesStoredTaskCredential` 使用两个渠道密钥、不同的本地和上游 ID，验证任务保存的密钥及真实 HTTP 下载路径、鉴权头和返回内容。

### 2.4 Remix 使用错误 ID，且丢失原任务计费倍率

**问题与触发条件：** 将本站 `task_original` 直接用于上游 Remix URL，上游无法找到实际的 `task_upstream`；原任务倍率提取后又被 `ModelPriceHelperPerCall` 重建的 `PriceData` 清空，导致时长和尺寸计费不足。多密钥重试还可能切换到不拥有原任务的账户。

**修复：** [relay_task.go](../../relay/relay_task.go) 使用 `GetUpstreamTaskID()`；价格重建后恢复原任务倍率；New API Remix 绑定原任务密钥，并校验密钥仍在渠道中且启用。保留真实多密钥标记和索引，供错误处理及禁用逻辑使用。

**验证：** `TestNewAPIRemixUsesOriginalTaskAndBillingRatios` 验证上游路径、账户、倍率继承和多密钥索引，并验证已禁用密钥被拒绝。

### 2.5 Wan 3.0 已迁移的图片没有进入上游请求

**问题与触发条件：** Wan 3.0 的图片被迁移为 `first_frame` / `last_frame`，但模型仍走旧格式构建器，旧构建器忽略这些媒体类型，实际发给上游的请求丢失图片。

**修复：** [ali/adaptor.go](../../relay/channel/task/ali/adaptor.go) 将 Wan 3.0 纳入新格式构建，使用映射后的模型选择协议。

**验证：** `TestBuildRequestBodyPreservesStandardImages` 覆盖 Wan 3.0 首尾帧及模型别名映射。

## 3. P2：参数迁移与接口兼容

### 3.1 图片别名、模型后缀与签名视频 URL 处理错误

**问题与触发条件：** 通用校验会将单个 `image` 同时写入 `images[0]`，迁移时重复拼接导致单图被当作首尾同帧；`wanx2.1` 模型名被误写为 `wan2.1`；精确匹配遗漏带日期后缀的模型；带查询参数的 `.mp4` URL 被当作图片。未迁移的图片还可能被静默忽略。

**修复：** 去除首项别名，修正模型族名称并支持 `-` 后缀，通过 URL 路径判断视频类型。显式 `media` 优先；未覆盖模型的图片参数继续报错，避免静默丢图。`input_reference` 沿用原有拒绝行为。

**验证：** `TestBuildRequestBodyPreservesStandardImages`、`TestAliRejectsUnmappedImages`。

### 3.2 Ali 标准尺寸迁移与估算污染缓存请求

**问题与触发条件：** `size="720P"` 被写入像素尺寸字段后无法生效；`1280x720` 与 `1280*720` 的处理不同，新格式又不读取像素尺寸。迁移直接修改共享 metadata map，使估算影响后续构建和重试。

**修复：** 先复制 metadata；按档位和像素尺寸分别迁移，统一分隔符。新格式转换为分辨率与宽高比，旧格式保留像素尺寸并补充分辨率，显式 metadata 参数优先。

**验证：** `TestAliStandardSizeMatchesBillingAndRequest`、`TestAliMigrationDoesNotMutateCachedMetadata`。

### 3.3 近透传破坏大整数、文件及响应扩展字段

**问题与触发条件：** 请求先转成 `map[string]interface{}` 再编码，会改变 `seed=9007199254740993`；multipart 文件名含引号时，手工拼接的头部不能正确解析，部分文件错误还被忽略。提交响应重新编码精简 DTO 会丢失 seconds、size、metadata 等字段；轮询落库也可能再次改变大整数。

**修复：** [newapi/adaptor.go](../../relay/channel/task/newapi/adaptor.go) 使用 sjson 仅改必要字段；multipart 使用标准 MIME 转义、完整复制并传播错误、清理临时文件。[task_polling.go](../../service/task_polling.go) 对无需嵌入视频脱敏的普通响应保留原字节。

**验证：** `TestBuildRequestBodyPreservesJSONNumbers`、`TestBuildRequestBodyPreservesMultipartFile`、`TestDoResponsePreservesVideoFields` 及轮询响应回归测试。

### 3.4 查询状态和原生 Ali 响应格式不符合接口约定

**问题与触发条件：** 本地任务超时失败并退款后，查询仍可能返回落库前上游的 `queued` 状态；成功响应可能保留上游内容 URL。原生 Ali 端点经 New API 渠道提交和查询时，返回 OpenAI video 格式，客户端读不到 `output.task_id` / `output.task_status`。

**修复：** 以本地最终状态、失败原因和公开 ID 生成查询响应，成功 URL 指向本站代理；增加 `AliVideoConverter` 并在提交、查询路径保留 Ali 响应格式。

**验证：** `TestConvertToOpenAIVideoUsesLocalTaskState`、`TestDoResponseKeepsNativeAliFormat`、`TestNewAPIAliFetchKeepsNativeResponseFormat`。

## 4. 全量验证中修复的既有测试夹具

[channel_affinity_usage_cache_test.go](../../service/channel_affinity_usage_cache_test.go) 在审查起点前已存在。其缓存键由 `time.Now().UnixNano()` 生成，在 Windows 上连续测试可能因时钟分辨率发生碰撞，导致计数累积，混合模式和不支持模式测试失败。

改为按 `t.Name()` 隔离键，并在测试前后清理对应缓存条目。仅修改测试夹具，未修改亲和性生产逻辑。历史来源通过 Git 检查，未单独运行父提交工作树的测试。

## 5. 验证结果

已通过：

```powershell
go test ./relay/channel/task/newapi ./relay/channel/task/ali ./relay/common
$env:GIN_MODE = 'release'
go test -p 1 ./...
git diff --check
```

默认包并行的全量测试曾在 AWS 包链接阶段遇到内存不足，最终限制包并行为 1 后全量通过，包含控制器、任务适配器、公共校验和 service 包。所有修改的 Go 文件已用 gofmt 格式化。

集成验证使用 `httptest` 和 SQLite；没有调用真实厂商 API，也没有运行 MySQL/PostgreSQL 实例验证。本次没有新增数据库方言语句或迁移。

## 6. 后续 review 建议与边界

1. **核对真实账单及基价单位。** 上游 new-api 的模型定价由其管理员配置，Kling、Hailuo、Vidu 等内置倍率只是本站参考估算。应对默认/高分辨率、长时长、视频输入等组合核对账单，特别是本次已有改动中新增的 Doubao 模型价格。按秒计费的基价应填每秒价格。
2. **补充用量和计费契约后再做完成时校准。** 当前 New API 适配器解析的 OpenAI video 响应不能提供完整的上游用量与账单，无法保证估算等于实际支出。未覆盖的参数会记录日志，但日志不能替代结算；未来应明确上游兼容扩展字段的单位和含义。
3. **区分新任务修复与历史数据。** 修复前创建且未保存密钥的多密钥任务，无法从现有任务记录可靠反推创建账户；现有兼容回退不能保证恢复这类历史任务的轮询或下载。
4. **保留本次回归场景。** 新增模型族或改变参数迁移时，同时验证实际上游请求和费用倍率，覆盖模型映射、metadata 冲突、默认值及边界值；避免只测内部倍率表。

详细参数映射见 [34-task-billing-passthrough-review.md](34-task-billing-passthrough-review.md)，任务链路说明见 [33-newapi-task-relay.md](33-newapi-task-relay.md)。
