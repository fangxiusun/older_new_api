# Ali 视频任务接口兼容合并说明

## 1. 背景

本次合并对比以下两个实现：

- GitHub 主线版本：`QuantumNous/new-api` 的 `relay/channel/task/ali/adaptor.go`。
- 当前分支版本：本仓库 `relay/channel/task/ali/adaptor.go`。

GitHub 主线使用统一的 `AliVideoRequest`，允许客户端通过任务通用字段
`image`、`images`、`input_reference`、`size`、`duration`、`seconds` 提交请求，
再由适配器转换为 Ali 请求。

当前分支按上游协议拆为两种请求：

- 旧格式：Wan 2.1 至 Wan 2.6，使用 `img_url`、`first_frame_url` 等字段。
- 新格式：Wan 2.7 与 HappyHorse，使用 `input.media` 及新参数结构。

合并目标不是恢复统一上游结构，而是在当前双协议转换之前兼容 GitHub 主线的客户端输入，
转换后继续复用当前分支已有的请求发送、响应转换、任务轮询与结算流程。

## 2. 合并方案

### 2.1 转换流程

```text
TaskSubmitReq
  |-- GitHub 风格：image/images/input_reference/size/duration/seconds
  |-- 当前风格：media + metadata
  v
兼容归一化和优先级合并
  |-- 旧模型 -> AliOldVideoRequest
  |-- 新模型 -> AliNewVideoRequest
  v
当前已有发送、轮询、状态转换和计费流程
```

### 2.2 字段优先级

标量字段发生冲突时按以下顺序覆盖，右侧优先级更高：

```text
模型默认值 < 顶层通用字段 < 扁平 metadata < metadata.parameters < media 中的同类标量
```

`metadata.input.media` 与顶层 `media` 都是显式列表，沿用当前实现按该顺序追加；只要任一列表
已有内容，就不会再根据 `image/images/input_reference` 自动补写 media。

输入字段的具体规则：

| 客户端字段 | 旧格式目标 | Wan 2.7 i2v 目标 |
| --- | --- | --- |
| `image` | `input.img_url` | `input.media[type=first_frame]` |
| `images[0]` | `input.img_url` 的后备值 | `input.media[type=first_frame]` 的后备值 |
| `images[1]` | 不自动使用 | `input.media[type=last_frame]` |
| `input_reference` | `input.img_url` 的后备值 | `input.media[type=first_frame]` 的后备值 |
| `size=720p` | `parameters.resolution=720P` | `parameters.resolution=720P` |
| `size=1280*720` | `parameters.size=1280*720` | 拒绝，新协议不接受像素尺寸 |
| `duration`/`seconds` | `parameters.duration` | `parameters.duration` |
| `metadata.input` | 覆盖输入字段 | 覆盖输入字段；显式 `media` 禁止自动补写 |
| `metadata.parameters` | 覆盖参数字段 | 覆盖参数字段 |

`metadata.model` 只能等于已完成模型映射后的上游模型，不能借 metadata 改写模型。

### 2.3 安全边界

顶层 `duration` 已由公共校验处理，但 metadata 可以绕过公共字段校验。因此旧、新两种转换均在最终请求构建阶段再次校验：

- 允许 `0`，由现有默认逻辑转换为 5 秒。
- 拒绝负数。
- 拒绝大于 `relaycommon.MaxTaskDurationSeconds` 的值。

这样保证构建上游请求与 `EstimateBilling` 使用同一个合法时长，避免只在计费处截断而仍向上游提交超大时长。

## 3. 两种算法差异

| 对比项 | GitHub 主线算法 | 当前分支算法 | 合并后的处理 |
| --- | --- | --- | --- |
| 上游 DTO | 单一请求结构 | 按模型拆分旧/新结构 | 保留双结构 |
| 顶层图片字段 | 所有模型均可填 `img_url` | 原先直接拒绝 | 旧模型转换为 `img_url`；Wan 2.7 i2v 转为 `media` |
| Wan 2.7 i2v | 统一结构末尾再归一化为 `media` | 要求客户端直接传 `media` | 两种输入生成相同的新格式请求 |
| HappyHorse 图片转换 | 统一结构可保留 `img_url` | 新格式 DTO 不含 `img_url` | 不臆造 media type，仍要求显式 `media` |
| 参数来源 | 顶层字段后由整个 metadata 覆盖 | 只读取扁平 metadata | 同时支持顶层、扁平和嵌套 `parameters` |
| 可选布尔/整数 | 部分非指针，显式零值可能被 `omitempty` 丢弃 | 使用指针保存显式 `false`/`0` | 保留当前指针实现 |
| 默认分辨率 | 旧模型按模型选择；Wan 2.7 默认逻辑依赖统一结构 | 旧模型按模型选择；新模型默认 1080P | 保留当前协议默认值 |
| kf2v 端点 | 固定 video-generation 端点 | kf2v 使用 image2video 端点 | 保留当前模型端点选择 |
| metadata 修改模型 | 整对象反序列化后检查 | 之前未检查 | 显式拒绝模型改写 |
| metadata 时长上限 | 计费处截断，构建仍可能发送超限值 | 同样存在旁路 | 构建阶段直接拒绝 |
| 响应透传 | 仅 OpenAI 兼容响应 | 支持 Ali 原生透传 | 后续流程不变，保留当前能力 |
| 任务 ID 查询 | 未进行路径转义 | 使用 `url.PathEscape` | 保留当前安全实现 |
| Usage | 只覆盖部分字段 | 覆盖输入/输出时长、ratio 等超集 | 保留当前超集及结算逻辑 |

## 4. 已确认的不一致及结论

### 4.1 可以消除的不一致

- 旧 Wan 的 `image`、`size`、`duration` 现在可转换成当前旧格式请求。
- Wan 2.7 i2v 的 `image`、`images`、`input_reference` 可转换成 `input.media`。
- Wan 2.7 i2v 的旧式 `metadata.input` 图片和音频字段可转换为 `first_frame`、`last_frame`、`driving_audio`。
- 顶层参数和当前 `metadata` 参数可以得到等价的最终请求。
- 嵌套 `metadata.parameters` 不再被忽略。
- 显式 `metadata.input.media` 优先，不会和自动生成的 media 混合。

### 4.2 有意保留的不一致

- HappyHorse 虽被当前代码归为新协议，但仓库内没有可验证的旧图片字段到 HappyHorse
  media type 的契约。为避免生成供应商不接受的类型，本次不自动转换，调用方应传显式 `media`。
- 新协议不接受 `1280*720` 这类像素尺寸。它只有 `resolution` 字段，因此明确报错，
  不做可能改变画面比例的猜测转换。
- 当前分支的模型端点选择、Ali 原生响应透传、Usage 超集和实际用量结算均优于统一实现，继续保留。

## 5. 测试覆盖

新增或更新的回归测试覆盖：

- GitHub 风格与当前风格的 Wan 2.7 i2v 请求等价性。
- GitHub 风格与当前风格的旧 Wan 请求等价性。
- `metadata.input` 和 `metadata.parameters` 的覆盖优先级。
- metadata 超限时长的拒绝。
- metadata 改写模型的拒绝。
- 旧、新请求默认值及 JSON 协议字段保持不变。

验证命令（PowerShell 7）：

```powershell
$env:GOMAXPROCS = '2'
go test -p 1 -vet=off ./relay/channel/task/ali
go vet -p 1 ./relay/channel/task/ali
git diff --check
```

验证结果：全部通过。低并发参数用于避免 Windows 环境中 Go 编译器与 vet 同时启动时耗尽分页文件。

## 6. Commit Log

建议 commit subject：

```text
feat(ali): 兼容两种视频任务接口转换
```

建议完整 commit message：

```text
feat(ali): 兼容两种视频任务接口转换

- 将 image、images、input_reference 转换为旧 Wan 或 Wan 2.7 media 请求
- 支持顶层参数、扁平 metadata 与 metadata.parameters 的确定性覆盖
- 保留现有双协议 DTO、模型端点、响应透传和任务结算流程
- 拒绝 metadata 模型改写及旁路提交的超限时长
- 增加两种转换算法等价性与安全边界回归测试
- 补充接口差异、兼容范围和合并取舍文档
```
