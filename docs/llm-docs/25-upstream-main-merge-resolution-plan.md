# upstream/main 合并实施与逐文件处理方案

> 文档性质：合并建议稿，不代表已解决任何冲突。
>
> 编写基线：2026-07-22，分支 integrate/upstream-main-20260722。
>
> 本文中的命令和代码均为后续操作建议。编写本文时未修改、暂存或提交任何业务代码。

## 1. 当前状态快照

当前集成分支已经重新执行：

    git merge --no-ff upstream/main

合并尚未完成，Git 状态如下：

| 项目 | 当前值 |
|---|---|
| 当前分支 | integrate/upstream-main-20260722 |
| 本地合并前提交 | caaf4abc62094b9932eb52e2562410f638a8d1ee |
| 上游合并目标 | 1721144221ec5c94dd87891a7ae1bee228e7bb63 |
| 共同祖先 | 0b7ae4ea794682830e0216ec51ffb8c7b923ab6d |
| 直接冲突文件 | 8 个 |
| 合并提交 | 尚未生成 |

当前 8 个直接冲突：

1. .gitignore
2. relay/channel/task/ali/adaptor.go
3. relay/channel/task/ali/constants.go
4. relay/helper/price.go
5. router/api-router.go
6. service/pre_consume_quota.go
7. service/task_billing.go
8. service/text_quota.go

当前不应再次运行 sync-upstream.ps1，也不应对整个仓库执行统一的 ours 或 theirs。前者会因为工作区不干净而停止，后者会分别丢失上游架构或本地业务能力。

## 2. 总体合并原则

本次不是普通的逐行冲突处理。上游在认证、授权、计费安全、任务结算和前端目录上都做了结构性调整，应采用以下原则：

1. 以上游架构为骨架。
   - 保留 BillingSession、QuotaClamp、BillingUsage、Authz、拆分后的 channel router。
   - 接受 web/default 迁移为 web 的新目录结构。

2. 将本地功能作为业务能力重新移植。
   - 阿里云原生视频路由和响应透传。
   - Wan 2.6、Wan 2.7、HappyHorse 1.0/1.1 模型。
   - 新旧视频请求协议和 media 字段。
   - 基于实际 Usage 的异步任务差额结算。
   - 独立计费调试日志。

3. 不能恢复上游已经替换掉的旧架构。
   - 不恢复旧的 PreConsumeQuota 主流程。
   - 不把 channel 路由重新内联到 api-router.go。
   - 不恢复非严格的 quota float-to-int 转换。

4. 所有计费合并必须遵循 pkg/billingexpr/expr.md 和上游新增的 billing safety invariants。
   - 用户可控的 duration、seconds、批次数量必须有上限。
   - 禁止裸 int(float64(...)) 或 int(math.Round(...))。
   - 预扣费阶段使用 Strict 转换；结算阶段使用 Checked 转换并保留 QuotaClamp 审计信息。

5. 先解决架构，再补本地功能，最后处理构建和文档。
   - 如果先把本地实现直接拼回去，很容易在后续上游架构适配时重复返工。

## 3. 建议的处理阶段

建议按以下顺序处理，不建议一次性编辑所有冲突：

### 阶段 A：结构性冲突

1. router/api-router.go
2. service/pre_consume_quota.go
3. .gitignore

### 阶段 B：计费安全冲突

1. relay/helper/price.go
2. service/text_quota.go
3. service/task_billing.go
4. 自动合并的 service/quota.go
5. 自动合并的 service/billing.go、service/billing_session.go、service/task_polling.go

### 阶段 C：阿里任务适配

1. relay/channel/task/ali/constants.go
2. relay/channel/task/ali/adaptor.go
3. relay/common/relay_info.go
4. middleware/ali_adapter.go
5. router/video-router.go
6. relay/relay_task.go

### 阶段 D：前端和构建迁移

1. build.ps1
2. build.sh
3. AGENTS.md
4. CLAUDE.md
5. docs/llm-docs 下涉及 classic、web/default 的文档

### 阶段 E：验证和提交

逐组暂存、逐组检查，最后再产生合并提交。

## 4. 直接冲突文件逐项建议

## 4.1 .gitignore

### 冲突原因

本地新增：

- /docs/reference/
- .gitignore
- .tmp

上游新增：

- .local-tests/
- service/relayconvert/chat_responses_live_local_test.go
- service/openaicompat/chat_responses_live_local_test.go

### 推荐结果

保留双方真正有效的忽略项：

- /docs/reference/
- .tmp
- .local-tests/
- 两个上游本地探针测试文件

不建议保留单独的 .gitignore 忽略项。该文件已经被 Git 跟踪，这条规则对已跟踪文件无效，也容易让后续维护者误解。

### 验收标准

- 文件中不存在冲突标记。
- .tmp 和 .local-tests 不出现在 git status 的未跟踪文件中。
- 不误忽略 docs/llm-docs。

## 4.2 router/api-router.go

### 冲突原因

本地版本将完整的 /api/channel 路由注册内联在 SetApiRouter 中，并新增 /api/debug/billing。

上游版本已经将 channel 路由迁移到 router/channel-router.go，并新增 router/authz-router.go：

    registerChannelRoutes(apiRouter)
    registerAuthzRoutes(apiRouter)

新的 channel router 不只是代码拆分，还引入了细粒度权限：

- authz.ChannelRead
- authz.ChannelWrite
- authz.ChannelOperate
- authz.ChannelSensitiveWrite

### 推荐结果

1. 完整保留上游的：

       registerChannelRoutes(apiRouter)
       registerAuthzRoutes(apiRouter)

2. 删除冲突块中本地重复的 channelRoute 整段注册。

3. 只把 billing debug 路由重新挂载到 SetApiRouter。

建议形态：

    billingDebugRoute := apiRouter.Group("/debug/billing")
    billingDebugRoute.Use(middleware.AdminAuth())
    {
        billingDebugRoute.GET("", controller.GetBillingDebug)
        billingDebugRoute.POST("", controller.SetBillingDebug)
    }

权限需要额外做一次产品决策：

- 如果普通管理员允许临时打开计费调试日志，保留 AdminAuth。
- 如果该开关属于高风险运维能力，建议 POST 额外要求 RootAuth 或为其定义独立 Authz 权限。

不建议把 billing debug 路由塞进 channelPermissionRoutes，因为它不是 channel 资源。

### 必须检查的隐性问题

- 不得同时保留内联 channelRoute 和 registerChannelRoutes，否则会重复注册 Gin 路由并在启动时 panic。
- 上游 channel-router.go 已包含本地原有的绝大部分路由，并额外增加批量状态和权限控制，不需要逐条复制。
- POST /channel/fetch_models 在上游改为权限控制，不应恢复旧的 RootAuth-only 实现。

### 验收标准

- /api/channel 的每个 Method + Path 只注册一次。
- /api/authz/catalog 存在。
- /api/debug/billing 的 GET、POST 权限符合最终决策。
- router/channel_router_test.go 通过。

## 4.3 service/pre_consume_quota.go

### 冲突类型

这是 modify/delete 冲突：

- 本地修改了该文件，加入 return_pre_consumed 调试日志。
- 上游删除了整个文件。

### 上游架构变化

上游已经把预扣、结算、退款生命周期集中到 BillingSession：

- service.PreConsumeBilling
- service.NewBillingSession
- BillingSession.preConsume
- BillingSession.Settle
- BillingSession.Refund
- BillingSession.Reserve

对上游树进行符号检查后，已经没有 PreConsumeQuota 和 ReturnPreConsumedQuota 的调用者。

### 推荐结果

接受上游删除，不恢复 service/pre_consume_quota.go。

本地需要保留的不是旧函数，而是其观测能力。return_pre_consumed 事件应由以下位置覆盖：

- BillingSession.Refund：记录退款开始、资金来源、预扣额度和 token 额度。
- BillingSession.preConsume：记录实际预扣额度和信任额度旁路。
- BillingSession.Settle：记录预扣与实际结算差额。

当前本地 service/billing_session.go 已经加入这些日志，因此不需要为了一个日志事件复活旧文件。

### 不推荐方案

- 选择 ours 保留该文件。
- 重新让 relay handler 调用 ReturnPreConsumedQuota。
- 同时维护 BillingSession 和旧 PreConsumeQuota 两套退款入口。

这些方案可能造成重复退款、重复扣费或不同资金来源的状态不同步。

### 验收标准

- service/pre_consume_quota.go 最终不存在。
- rg 搜索不到 PreConsumeQuota 和 ReturnPreConsumedQuota 的业务调用。
- BillingSession 的预扣、结算、退款测试通过。
- 钱包和订阅两种资金来源都只退款一次。

## 4.4 relay/helper/price.go

### 冲突原因

本地增加了：

- ratio_loaded 调试日志。
- tiered_expr_result 调试日志。
- 一处本地 preConsumedQuota 赋值。

上游增加了：

- billingexpr.QuotaRoundStrict。
- 额度溢出、NaN 和越界时的错误返回。
- QuotaClamp 审计链路。

### 推荐结果

1. 保留上游 QuotaRoundStrict 和错误返回。
2. 保留本地日志，但日志必须放在计算结果已经验证之后。
3. 不恢复旧的 billingexpr.QuotaRound。
4. 不增加第二次 preConsumedQuota 赋值。

建议形态仅用于说明顺序：

    quotaBeforeGroup := rawCost / 1_000_000 * common.QuotaPerUnit
    preConsumedQuota, err := billingexpr.QuotaRoundStrict(
        quotaBeforeGroup * groupRatioInfo.GroupRatio,
    )
    if err != nil {
        return types.PriceData{}, err
    }

    logger.BillingDebugMap(c, map[string]interface{}{
        "stage":              "tiered_expr_result",
        "raw_cost":           rawCost,
        "matched_tier":       trace.MatchedTier,
        "quota_before_group": quotaBeforeGroup,
        "group_ratio":        groupRatioInfo.GroupRatio,
        "pre_consumed_quota": preConsumedQuota,
    })

对于普通按次价格路径，先确认上游当前在哪里统一生成 preConsumedQuota，再把 ratio_loaded 日志放到该值确定的位置。不能只凭冲突块恢复本地赋值，否则可能绕过上游的新校验。

### 验收标准

- 不存在裸 float-to-int quota 转换。
- tiered 预扣遇到溢出时返回错误，而不是产生负额度。
- 调试日志中的 pre_consumed_quota 与 PriceData 最终值一致。
- relay/helper/price_test.go 和 pkg/billingexpr 测试通过。

## 4.5 service/text_quota.go

### 冲突原因

本地版本直接用原始 usage 生成 textQuotaSummary 并记录调试日志。

上游版本先执行：

    billingUsage := effectiveBillingUsage(usage)

再使用标准化后的 billingUsage 参与结算。该步骤用于保持 OpenAI、Claude、Gemini 等不同 Usage 语义在转换后的正确计费。

### 推荐结果

必须保留上游的 billingUsage 规范化，然后基于同一个 summary 记录本地日志：

    billingUsage := effectiveBillingUsage(usage)
    summary := calculateTextQuotaSummary(ctx, relayInfo, billingUsage)

    logger.BillingDebugMap(ctx, map[string]interface{}{
        "stage":             "text_quota_calculated",
        "model":             summary.ModelName,
        "prompt_tokens":     summary.PromptTokens,
        "completion_tokens": summary.CompletionTokens,
        "cache_tokens":      summary.CacheTokens,
        "image_tokens":      summary.ImageTokens,
        "audio_tokens":      summary.AudioTokens,
        "model_ratio":       summary.ModelRatio,
        "completion_ratio":  summary.CompletionRatio,
        "cache_ratio":       summary.CacheRatio,
        "group_ratio":       summary.GroupRatio,
        "model_price":       summary.ModelPrice,
        "quota":             summary.Quota,
    })

日志和最终结算必须共享同一个 summary，避免日志显示原始 Usage，而实际扣费使用标准化 Usage。

### 必须保留的上游能力

- effectiveBillingUsage。
- BillingUsage.Source 和 Semantic。
- QuotaClamp 记录。
- tiered expression 的 token normalization。
- input_tokens_total 等管理员审计字段。

### 验收标准

- OpenAI、Claude、Gemini 转换后的计费不重复计算 cache、image、audio token。
- 日志中的 quota 等于实际结算 quota。
- service/text_quota_test.go 全部通过。
- quota saturation 测试通过。

## 4.6 service/task_billing.go

### 冲突原因

本地为 RecalculateTaskQuota 增加了 task_recalculate 调试日志。

上游把函数签名扩展为：

    func RecalculateTaskQuota(
        ctx context.Context,
        task *model.Task,
        actualQuota int,
        reason string,
        clamps ...*common.QuotaClamp,
    )

clamps 用于把任务结算时发生的额度饱和写入管理员可见的 admin_info。

### 推荐结果

保留上游函数签名和完整实现，只在函数入口的基本参数验证后加入本地日志。

日志建议包括：

- task_id
- actual_quota
- pre_consumed
- delta
- reason
- clamp kind（存在时）

不要为了日志把签名退回四参数版本。可变参数保证旧调用方仍能编译，同时允许新调用方传递 QuotaClamp。

### 额外安全建议

- task 为 nil 时不能为了记录 task.TaskID 发生 panic。
- actualQuota <= 0 的既有语义应保持。
- 记录 clamp 时不要把完整内部错误对象序列化到普通用户日志。
- 异步差额结算的退款或补扣必须继续保持 CAS/幂等保护。

### 验收标准

- RecalculateTaskQuotaByTokens 能把 clamp 传入 RecalculateTaskQuota。
- 旧四参数调用仍可工作。
- 重复轮询同一完成任务不会重复补扣或退款。
- service/task_billing_test.go 和 service/task_polling_test.go 通过。

## 4.7 relay/channel/task/ali/constants.go

### 冲突原因

上游新增了有限范围的 Wan 2.7 支持，本地新增了更完整的模型集合：

- Wan 2.7：i2v、t2v、r2v、videoedit、vace
- Wan 2.6
- Wan 2.5
- Wan 2.2
- Wanx 2.1
- HappyHorse 1.0
- HappyHorse 1.1

当前冲突工作树中 wan2.7-i2v 和 wan2.7-t2v 已经重复出现，且文件开头存在 BOM。

### 推荐结果

1. 以本地完整模型集合为候选全集。
2. 合入上游新模型时进行去重。
3. 模型列表、isNewFormatModel 和 ProcessAliOtherRatios 的模型键必须一致。
4. 删除 UTF-8 BOM，使首行严格为 package ali。
5. 如果某个模型只出现在 ModelList、没有请求构建或计费规则，应先补齐能力再开放。

### 建议增加的表驱动校验

对每个模型记录：

| 字段 | 说明 |
|---|---|
| model | 模型名 |
| protocol | old 或 new |
| action | t2v、i2v、r2v、edit 等 |
| default resolution | 默认分辨率 |
| default duration | 默认时长 |
| supports audio | 是否支持音频 |
| billing ratio key | 对应计费倍率 |

这样可以避免 ModelList、请求构建和计费表分别维护后逐渐漂移。

### 验收标准

- ModelList 无重复值。
- 每个暴露模型都能选择唯一的请求协议。
- 每个计费模型都能解析分辨率和时长。
- 文件无 BOM。

## 4.8 relay/channel/task/ali/adaptor.go

这是本次风险最高的冲突文件，不建议在冲突标记中直接拼接双方代码。建议先确定一个统一设计，再重组文件。

### 4.8.1 双方能力差异

上游侧主要能力：

- AliVideoRequest、AliVideoInput、AliVideoParameters。
- Wan 2.7 i2v 的 input.media 协议。
- normalizeWan27I2VInput。
- firstTaskImage、secondTaskImage。
- metadata duration 的 MaxTaskDurationSeconds 防御。
- 新增 adaptor_test.go 回归测试。

本地侧主要能力：

- AliOldVideoRequest 与 AliNewVideoRequest 分流。
- Wan 2.1 至 Wan 2.7、HappyHorse 的完整字段映射。
- prompt_extend、watermark、seed 等可选值使用指针。
- 原生 input、parameters 透传。
- 分辨率和音频倍率。
- 基于 Ali Usage 的任务完成后差额结算。
- billing debug 日志。

### 4.8.2 推荐的统一结构

推荐保留本地“旧协议/新协议分流”这一业务结构，但吸收上游的安全校验和输入归一化。

请求构建主流程建议保持单一入口：

    BuildRequestBody
      -> resolve upstream model
      -> validate model/protocol
      -> old protocol: buildOldFormatRequest
      -> new protocol: buildNewFormatRequest
      -> validate duration and media
      -> common.Marshal

不要同时保留上游 convertToAliRequest 和本地 buildOldFormatRequest/buildNewFormatRequest 两套完整构建链。两套链并存会造成 EstimateBilling 和真实请求使用不同默认值。

### 4.8.3 类型处理建议

1. 新旧请求结构可以分开，但 media item 最好只有一个稳定类型。
   - 当前冲突中同时存在 AliVideoMedia 和 AliMediaItem。
   - 推荐统一成一个类型，或提供显式转换函数，避免字段演进时两边不一致。

2. 客户端可选标量必须使用指针：
   - *bool：prompt_extend、watermark、audio。
   - *int：seed，以及确实需要区分“未传”和“显式 0”的数值。

3. 显式 false 和 0 必须能够从原生 Ali 请求经过 middleware、metadata、builder 后仍然发给上游。

4. metadata 中 map[string]interface{} 的数字通常会变为 float64，toInt 必须检查：
   - 是否为整数。
   - 是否超出 int 范围。
   - 是否超出业务上限。

### 4.8.4 duration 安全建议

上游明确要求任何计费乘数都必须有边界。

建议：

1. 请求构建阶段对 metadata duration 执行范围校验。
2. 超出 relaycommon.MaxTaskDurationSeconds 时返回 400，而不是让请求进入计费。
3. EstimateBilling 中仍保留 min(duration, MaxTaskDurationSeconds) 作为防御层。
4. 实际 Usage 中的 duration 同样视为不可信输入，转换 quota 时使用 Checked API。

不能只在预估计费时 clamp，然后把超大 duration 原样发给上游，否则可能出现实际成本高于预扣额度。

### 4.8.5 ProcessAliOtherRatios 合并建议

当前冲突中存在两个同名函数：

- ProcessAliOtherRatios(aliReq *AliVideoRequest)
- ProcessAliOtherRatios(model, size, resolution string, audio *bool)

推荐保留后者的纯参数形式，因为它能被预估计费和实际 Usage 重算复用：

    ProcessAliOtherRatios(model, size, resolution string, audio *bool)

合并时需要：

- 吸收上游新增的 Wan 2.7 模型倍率。
- 保留本地 HappyHorse 和旧 Wan 模型倍率。
- 保留最长前缀匹配，但优先精确匹配。
- 对空 resolution 明确定义行为，不能默认为字符串 P。
- 对未知 size 返回可识别错误。
- 不允许 0、负数、NaN 或 Inf 进入 OtherRatios。

### 4.8.6 EstimateBilling 合并建议

建议只调用和 BuildRequestBody 相同的 builder，从 builder 结果提取：

- model
- size
- resolution
- duration
- audio

预估时长必须受 MaxTaskDurationSeconds 约束。

当前本地日志可以保留，但不能改变 OtherRatios 的内容。

### 4.8.7 AdjustBillingOnComplete 合并建议

本地基于 Ali Usage 重新计算实际 quota 的思路可以保留，但当前实现包含类似：

    int(float64(task.Quota) * ratio)

这与上游 billing safety invariants 冲突，必须改成 Checked 转换。

建议的结算接口形态：

1. 使用 common.QuotaFromFloatChecked 或 common.QuotaRoundChecked。
2. 获得 actualQuota 和 clamp。
3. 将 clamp 传递给 RecalculateTaskQuota，而不是只返回 int 后丢失审计信息。

如果现有 TaskPollingAdaptor.AdjustBillingOnComplete 只能返回 int，则需要评估两种方案：

- 扩展接口返回 quota + clamp；影响面更大，但语义完整。
- 将 clamp 暂存到 task result 或 billing context，再由 settleTaskBillingOnComplete 取出。

不建议在 adaptor 内只写日志后丢弃 clamp，因为管理员消费日志中需要 admin_info.quota_saturation。

### 4.8.8 Usage 字段合并建议

本地 AliUsage 包含：

- input_video_duration
- output_video_duration
- duration
- SR
- ratio
- video_count

上游至少包含：

- duration
- video_count
- SR

建议保留超集，但明确实际计费采用哪个时长字段以及回退顺序。例如：

1. output_video_duration
2. duration
3. 预估 duration

回退规则必须用测试锁定，避免供应商返回多个字段时重复乘算。

### 4.8.9 建议测试矩阵

至少覆盖：

- wan2.7-i2v：image、images、input_reference、input.media。
- wan2.7-t2v：无媒体输入。
- HappyHorse 1.0/1.1：t2v、i2v、r2v。
- 旧 Wan：img_url、first_frame_url、last_frame_url、audio_url。
- prompt_extend=false、watermark=false、seed=0 的显式传递。
- duration 负数、0、正常值、超过上限。
- 未知 size 和 resolution。
- metadata 试图修改 model。
- 实际 Usage 小于、等于、大于预估值。
- quota 计算溢出时生成 clamp，而不是负额度。

## 5. 自动合并成功但仍需人工复核的文件

Git 没报冲突不代表语义兼容。以下文件同时受到本地功能和上游架构影响。

## 5.1 relay/common/relay_info.go

本地增加了 TaskSubmitReq.Media 及媒体辅助方法；上游增加了 RelayInfo.QuotaClamp 等计费安全字段。

建议：

- 两类字段都保留。
- Media 的 Type、URL、Mime JSON tag 保持稳定。
- HasImage 应兼容 Images 和 media[type=image]。
- 检查自定义 UnmarshalJSON 是否保留显式 0、false 和 metadata。
- 文件中新增 strings 使用后确保 import 正确。

## 5.2 relay/relay_task.go

本地增加 Ali 原生响应透传，上游增加任务 quota clamp 记录。

建议：

- 两类逻辑都保留。
- 删除文件开头 BOM。
- 不使用 isAliPassthrough.(bool) 的无保护断言；建议使用带 ok 的类型断言，避免上下文值类型异常时 panic。
- 透传前检查 originTask.Data，并验证 output 是否为对象。
- task_id 替换只能修改返回副本，不能改变数据库中的原始 Data。
- 上游 noteTaskQuotaClamp 逻辑不能被透传分支绕过。

## 5.3 service/billing.go

本地只增加 settle_billing_start 日志，上游增加 QuotaClamp 的预扣拒绝。

建议：

- 保留上游对 relayInfo.QuotaClamp 的检查。
- 日志记录放在 nil 校验之后。
- 日志增加 user_id、billing_source、pre_consumed 时必须避免 relayInfo 为 nil。
- 调试日志不能影响返回错误或结算顺序。

## 5.4 service/billing_session.go

本地加入了预扣、结算、退款日志，上游 BillingSession 是最终架构。

建议重点复核：

- s.funding 是否可能为空；日志调用 s.funding.Source() 前要有构造不变量或空值保护。
- 日志必须在锁内读取一致状态，但不能在持锁状态执行耗时 I/O。
- Refund 的 refunded 标志仍需在异步退款前设置，防止重复调度。
- 资金来源结算成功、token 调整失败时，不能因为新增日志改变 fundingSettled 的状态机。
- 日志不记录 token key、渠道 key、上游密钥。

## 5.5 service/funding_source.go

本地只增加 WalletFunding 和 SubscriptionFunding 的调试日志。

建议：

- 保持 WalletFunding.Refund 不自动重试，因为 IncreaseUserQuota 非幂等。
- 保持 SubscriptionFunding.Refund 的 requestId 幂等重试。
- 不在日志中输出订阅敏感数据或完整请求 ID 之外的认证信息。
- 日志不能移动 consumed、preConsumed 等状态写入顺序。

## 5.6 service/quota.go

这是自动合并文件中明确需要修正的一项。

当前本地 PostConsumeQuota 在空值判断之前记录：

    relayInfo.BillingSource
    relayInfo.UserId

如果 relayInfo 为 nil，会在正式业务判断前 panic。

建议先确定函数契约：

- 如果 relayInfo 永远不得为空，在函数开头显式返回错误，再记录日志。
- 如果历史调用允许 nil，则日志字段必须使用安全默认值，不能直接解引用。

同时保留上游：

- QuotaClamp。
- calculateAudioQuota 的 Checked 结果。
- admin_info.quota_saturation 审计。

## 5.7 service/task_polling.go

本地增加 task_settle_on_complete 日志，上游增加任务轮询和 CAS 回归保护。

建议：

- 在读取 task.PrivateData.BillingContext 前确认 task 和 PrivateData 的初始化契约。
- 保留按次计费任务跳过差额结算的规则。
- AdjustBillingOnComplete 产生 clamp 后要能传到 RecalculateTaskQuota。
- 确保同一完成状态只结算一次。

## 5.8 service/violation_fee.go

本地新增违规扣费调试日志，上游未与其直接冲突。

建议：

- 保留上游 quota conversion helper。
- 记录 fee_quota，但不记录敏感请求内容。
- PostConsumeQuota 返回失败时不能更新 user used quota 或 channel used quota。
- 增加钱包和订阅两种 BillingSource 的测试。

## 5.9 pkg/billingexpr 与 common/quota_math.go

这些是上游新增计费安全基础设施，不建议为适配本地旧逻辑而修改。

本地代码应向这些 API 迁移：

- common.QuotaFromFloat
- common.QuotaFromFloatChecked
- common.QuotaRound
- common.QuotaRoundChecked
- common.QuotaRoundStrict
- billingexpr.QuotaRoundStrict

规则：

- 预扣费：Strict，越界即失败。
- 结算和上游 Usage：Checked，既饱和又返回 clamp，供审计链路使用。

## 6. 本地新增能力文件的处理建议

## 6.1 common/billing_debug.go

建议保留。

需要确认：

- BILLING_DEBUG 环境变量只影响进程启动时的初始状态。
- API 热切换不持久化，进程重启后回到环境变量状态。
- 这一行为应写入运维文档。
- atomic.Int32 适合当前布尔开关，不需要数据库。

## 6.2 controller/billing_debug.go

建议保留，但检查：

- POST 权限是否应提升到 RootAuth。
- 错误响应是否符合当前统一 API 响应格式。
- Enabled 使用 bool 能正确接收显式 false。
- 不需要把开关写入 options 数据库，除非产品明确要求持久化。

## 6.3 logger/billing_debug.go

建议保留，但做以下复核：

- map 遍历顺序不稳定，不应让测试断言完整日志字符串顺序。
- gin.Context 和 context.Context 两套入口应保持调用者 file:line 正确。
- 禁止记录请求正文、密钥、Authorization、上游 token。
- 高频请求开启调试后会产生大量同步输出，应在文档中标注性能风险。
- 可考虑限制单条日志长度，防止异常字段导致日志膨胀。

## 6.4 middleware/ali_adapter.go

建议保留并适配上游。

必须处理：

- 删除文件开头 BOM。
- 继续使用 common.Marshal、common.UnmarshalBodyReusable，不能直接调用 encoding/json。
- 原生 parameters 中的 duration、seed、bool 必须保留显式 0/false。
- 对 duration 实施和 adaptor 一致的上限校验。
- media 中每个 URL 应进入现有 SSRF/URL 校验策略；不能因为放在 metadata 中绕过保护。
- GET 路径改写后确认 middleware.Distribute 获取到正确 RelayMode。
- 错误响应当前使用 OpenAI 格式，需要确认 Ali 原生接口是否要求 Ali 格式错误体。

## 6.5 router/video-router.go

建议保留 Ali 原生路由，但删除 BOM。

需要核对：

- /ali/v1/services/aigc/video-generation/video-synthesis
- /ali/v1/services/aigc/image2video/video-synthesis
- /ali/v1/tasks/:task_id

确认 middleware 顺序仍为：

1. RouteTag
2. AliRequestConvert
3. TokenAuth
4. Distribute

如果上游对 TokenAuth 或 Distribute 的上下文键有新要求，AliRequestConvert 必须在不破坏这些键的前提下重写路径和 Body。

## 6.6 sync-upstream.ps1

建议原样保留。

注意该脚本的 Branch 参数同时表示本地分支和上游分支，因此它适合在 main 上合并 upstream/main，不适合直接用于 integrate/upstream-main-20260722。

本次集成分支应继续使用手工 git merge 流程。是否扩展脚本为 SourceBranch/TargetBranch 两个参数，可以作为合并完成后的独立任务，不应混入本次冲突解决。

## 7. 前端目录迁移与构建脚本

## 7.1 上游目标结构

上游已经统一为：

    web/package.json
    web/src
    web/dist

Go 嵌入位置：

    //go:embed web/dist

Dockerfile 也只复制：

    /build/web/dist -> ./web/dist

## 7.2 build.ps1

该文件虽然没有 Git 冲突，但当前逻辑已经失效，必须在本次合并完成前调整。

建议：

1. 删除 web/default/dist 和 web/classic/dist 的清理逻辑。
2. 只清理 web/dist。
3. 只在 web 目录执行一次 bun install。
4. 只在 web 目录执行一次 bun run build。
5. SkipFrontend 只检查 web/dist/index.html。
6. FrontendOnly 的行为保持不变。
7. 不再生成 classic 产物。

目标流程：

    Push-Location "web"
    try {
        & bun.cmd install --frozen-lockfile
        & bun.cmd run build
    } finally {
        Pop-Location
    }

仍需保留现有：

- VITE_REACT_APP_VERSION。
- DISABLE_ESLINT_PLUGIN。
- Windows/Linux 双目标 Go 构建。
- finally 中环境变量清理。

## 7.3 build.sh

与 build.ps1 保持相同语义：

- 单一 web/dist。
- 单次 bun install。
- 单次 bun run build。
- skip-frontend 只检查 web/dist/index.html。

虽然 Windows 开发默认使用 pwsh，但 build.sh 仍可作为 Linux 构建入口保留。

## 7.4 classic 前端决策

推荐接受上游删除 classic，原因：

- 上游构建、嵌入、路由和测试都已转向单一 web。
- 继续保留 classic 会形成长期双前端维护成本。
- .agents/skills/classic-to-default-sync/SKILL.md 的上游删除也与该决策一致。

如果业务明确要求保留 classic，则应作为独立工作流：

1. 不在本次 merge conflict 中零散恢复 web/classic。
2. 单独建立 retain-classic 分支。
3. 明确 classic 的构建产物目录和 Go 路由选择机制。
4. 重新验证认证、Authz、API 字段和新后端行为。

## 8. 项目规则和文档处理

## 8.1 AGENTS.md

上游更新包含重要规则：

- web/default 改为 web。
- 新的数据库锁 helper。
- 主库和日志库的数据库类型判断。
- 完整的 billing safety invariants。

建议保留这些上游规则，同时确保本地治理规则继续存在：

- 受保护的 new-api、QuantumNous 项目标识不得移除或替换。
- JSON 统一使用 common wrapper。
- SQLite、MySQL、PostgreSQL 同时兼容。
- Pull Request 模板和 AI-assisted 声明规则。

不要简单覆盖为 ours。上游新增的计费安全规则正是本次 Ali 和 billing 合并所依赖的约束。

## 8.2 CLAUDE.md

通常与 AGENTS.md 保持同一套项目约定。建议检查目录路径、计费安全规则和受保护信息是否一致，避免两个规则文件产生冲突指令。

## 8.3 web/AGENTS.md

接受从 web/default/AGENTS.md 到 web/AGENTS.md 的迁移，并以后者作为前端规则来源。

## 8.4 docs/llm-docs

本地文档可以保留，但以下主题会因上游变化而过时：

- web/default 路径。
- classic 前端相关设计。
- 旧的 PreConsumeQuota 调用链。
- 旧 channel 路由结构。
- 未包含 QuotaClamp、BillingUsage 的计费流程图。

建议在代码合并和测试稳定后统一修订，不要在解决代码冲突时同步大面积改文档。

## 9. 建议的实际操作顺序

以下命令仅作为后续执行清单。

### 9.1 每解决一个文件先检查

    git diff --name-only --diff-filter=U
    rg -n "^<<<<<<<|^=======|^>>>>>>>" <已处理文件>

### 9.2 显式暂存，不使用 git add -A

建议按组暂存，避免把尚未审核的大量文件和本文档意外混在一起：

    git add .gitignore
    git add router/api-router.go
    git rm service/pre_consume_quota.go

计费组完成后：

    git add relay/helper/price.go
    git add service/text_quota.go
    git add service/task_billing.go
    git add service/quota.go

Ali 组完成后：

    git add relay/channel/task/ali/adaptor.go
    git add relay/channel/task/ali/constants.go
    git add relay/common/relay_info.go
    git add middleware/ali_adapter.go
    git add router/video-router.go
    git add relay/relay_task.go

实际执行前应再次确认路径，不要机械复制。

### 9.3 冲突清零检查

    git diff --name-only --diff-filter=U
    git ls-files -u
    rg -n "^<<<<<<<|^=======|^>>>>>>>" .

三条命令都不应再输出有效冲突。

### 9.4 暂存区检查

    git diff --cached --check
    git diff --cached --stat
    git diff --cached --name-status

重点检查：

- 是否意外恢复 web/classic。
- 是否意外删除本地 docs/llm-docs。
- 是否丢失 billing debug 文件。
- 是否删除或替换受保护的项目标识。
- 是否出现 BOM 或行尾空白。

## 10. 验证矩阵

## 10.1 Go 格式和静态检查

只对实际修改的 Go 文件执行 gofmt。

建议检查：

    gofmt -d relay/channel/task/ali/adaptor.go
    gofmt -d relay/channel/task/ali/constants.go
    gofmt -d relay/helper/price.go
    gofmt -d router/api-router.go
    gofmt -d service/task_billing.go
    gofmt -d service/text_quota.go

## 10.2 定向测试

    go test ./relay/channel/task/ali
    go test ./relay/helper
    go test ./service
    go test ./router
    go test ./pkg/billingexpr
    go test ./common

## 10.3 全量测试

    go test ./...

## 10.4 前端

PowerShell 方案：

    Push-Location .\web
    try {
        bun install --frozen-lockfile
        bun run typecheck
        bun run lint
        bun run build
    } finally {
        Pop-Location
    }

## 10.5 本地构建脚本

build.ps1 调整完成后：

    pwsh -NoProfile -File .\build.ps1

至少确认：

- web/dist/index.html 生成。
- Windows 可执行文件生成。
- Linux 可执行文件生成。
- Go embed 能找到 web/dist。

## 10.6 数据库

本次上游包含认证、授权、用户会话和系统任务模型变化，需要至少验证：

- SQLite 新库初始化。
- SQLite 旧库升级。
- MySQL 5.7.8+ migration。
- PostgreSQL 9.6+ migration。
- Authz 默认角色和 Casbin 数据初始化。
- 用户 session、external identity、system task 表。

不能只因为 go test ./... 通过就认为数据库兼容已经验证。

## 10.7 API 回归

建议至少覆盖：

- 登录、刷新、退出。
- 管理员 channel 列表、修改、测试和复制。
- Authz catalog。
- billing debug 开关权限。
- OpenAI/Claude/Gemini 文本计费。
- Ali 原生提交、查询和响应透传。
- 异步任务完成后的补扣、退款和重复轮询。

## 11. 合并提交前验收标准

只有同时满足以下条件，才建议运行 git merge --continue：

1. git diff --name-only --diff-filter=U 无输出。
2. git ls-files -u 无输出。
3. 仓库不存在冲突标记。
4. 所有修改过的 Go 文件通过 gofmt 检查。
5. 定向测试通过。
6. go test ./... 通过。
7. bun typecheck、lint、build 通过。
8. build.ps1 不再引用 web/default 或 web/classic。
9. Ali duration、count、quota 转换符合上游安全约束。
10. 计费日志不会对 nil relayInfo 解引用。
11. classic 的保留或删除已经明确决策。
12. 项目名称、组织归属、许可证和 attribution 未被删除或替换。

## 12. 提交和主分支合并建议

测试全部通过后再完成当前合并：

    git merge --continue

建议合并提交说明保留上游信息，并补充本地集成范围，例如：

    merge: 同步 upstream/main 并适配本地 Ali 与计费扩展

    - 合入 upstream/main 的认证、授权、计费安全和前端重构
    - 迁移本地 Ali 原生视频与 HappyHorse 支持
    - 保留 billing debug 与实际 Usage 差额结算
    - 调整构建脚本以适配单一 web 前端

集成分支验证完成后：

    git switch main
    git merge --ff-only integrate/upstream-main-20260722
    git push origin main

在 push 前建议再次查看：

    git log --graph --oneline --decorate -20
    git diff origin/main..main --stat

## 13. 回退策略

合并提交生成前，如果发现整体方向错误，可使用：

    git merge --abort

合并提交生成并已推送后，不建议重写共享历史。应通过新的 revert 提交回退 merge commit，并在回退前确认 merge parent：

    git show --summary <merge-commit>
    git revert -m 1 <merge-commit>

不要在已经推送的共享 main 上使用 reset --hard 或强制推送。

## 14. 最终建议摘要

本次合并的正确方向不是“本地和上游各保留一半”，而是：

1. 接受上游的新架构和安全不变量。
2. 将本地 Ali、HappyHorse、原生透传和调试能力重新接入新架构。
3. 删除已经被 BillingSession 替代的旧预扣文件。
4. 保留 Authz 路由拆分，不恢复内联 channel router。
5. 把所有本地裸 quota 转换迁移到 Strict/Checked helper。
6. 接受单一 web 前端，并调整本地构建脚本。
7. 通过定向测试、全量测试、前端构建和数据库迁移共同验收。

其中风险最高的三个区域依次是：

1. relay/channel/task/ali/adaptor.go 的协议与实际 Usage 计费整合。
2. service 和 relay/helper 中的 quota 安全链路。
3. web/default、web/classic 到单一 web 的构建迁移。

建议按本文阶段顺序逐项完成，并在每个阶段形成一个可审查的暂存区差异后再继续下一阶段。
