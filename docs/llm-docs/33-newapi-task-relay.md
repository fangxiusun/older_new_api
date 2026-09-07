# 33. New API 渠道 Task Relay：支持范围与 MiniMax-H3

> 当前实现以 `bec9fd21` 为基础，仅接入已确认接口和计费规则、返回视频直链的模型。
> 相关代码：`relay/channel/task/newapi/`。本说明取代文档 34、35 中的历史支持范围。

## 1. 明确支持的模型

| 渠道 Base URL | 标准模型 | 输入方式 | 分辨率 | 上游报价（每秒） | 相对 768P 倍率 |
| --- | --- | --- | --- | --- | --- |
| https://tokease.cn | MiniMax-H3 | 文生视频、图生视频、视频生视频、多参考图、多参考视频 | 768P | 0.5 | 1 |
| https://tokease.cn | MiniMax-H3 | 同上 | 2K | 0.8 | 1.6 |

依据为本次提供的上游 New API 接口样例、报价和渠道 URL。当前确认组合只有 **https://tokease.cn + MiniMax-H3**；其他站点的同名模型不自动复用此规则。

提交链路从渠道配置取得 `info.ChannelBaseUrl`，`TaskAdaptor.Init` 将它保存到 `a.baseURL`；轮询的 `FetchTask` 也直接收到渠道 Base URL。按解析后的完整主机名识别站点，支持域名大小写、尾斜杠和默认 443 端口。当前渠道应填写 HTTPS 站点根地址 `https://tokease.cn`，不附加 `/v1`、查询参数或中文标点。

未知渠道 URL 返回 HTTP 400、错误码 `unsupported_task_upstream`，在预扣和转发前终止；查询也不会向未知站点发送请求。

以下模型目前均不接入 New API task 转发：Sora、Kling、其他 Hailuo、Jimeng、Vidu、Doubao / Seedance、Wan / HappyHorse，以及未知模型。历史参考倍率不足以确认上游站点的计费，不能作为放行依据。`Seedance 2.0/uP` 样例仅说明图片和视频参考的字段形式，并未提供该模型的价格，因此仍拒绝。

未知模型返回 HTTP 400、错误码 `unsupported_task_model`，在预扣和请求上游之前终止；不再回退到 Sora 计费。各厂商原生渠道的支持范围不受此白名单影响。

请求校验与字段迁移、价格配置检查、倍率计算均使用渠道 URL 与映射后的标准模型共同组成的分支标识，当前为 `tokease.cn/MiniMax-H3`。以后接入其他站点或 Ali 等模型时，需要扩展 `resolveTaskUpstream` 的 URL 识别、在 `ModelList` 中登记标准模型，并实现 `parseTaskBillingRequest`、`validateTaskBillingConfig`、`estimateTaskBilling` 的对应组合分支；分辨率倍率同样按站点与模型配置。通用提交层只序列化对应分支处理后的请求。当前没有提前放行其他组合。

## 2. 非标准模型名与渠道映射

以 `/` 分段，识别完整标准模型名，或标准模型名后以 `-` / `_` 连接的后缀，例如：

- `MiniMax-H3`
- `vendor/MiniMax-H3`、`MiniMax-H3/vendor`
- `vendor/team/MiniMax-H3`、`MiniMax-H3/vendor/team`
- `vendor/team/MiniMax-H3/vendor/tier`
- `MiniMax-H3-2026-09`、`MiniMax-H3_custom_tier`
- `vendor/team/MiniMax-H3-custom_v2/vendor/tier`

`MiniMax-H30`、`not-MiniMax-H3` 等不会误匹配。标准名按大小写匹配；分辨率接受 `768p` / `2k` 并规范为 `768P` / `2K`。

模型识别发生在渠道模型映射之后，支持现有链式映射。内部用站点与标准模型组成的标识查倍率，**发给上游的 model 保留映射后的完整名称**；返回客户端的模型名仍为其原始名称。本地定价使用客户端请求的模型名，因此别名也需配置相应 `ModelPrice`。

这些装饰名称应指向相同的 H3 接口及分辨率计价规则，不根据任意子串推测其他模型身份。

## 3. 提交协议

客户端可使用 `POST /v1/video/generations`，也可通过 `POST /v1/videos` 提交相同的 H3 JSON 请求。上游统一使用：

```text
POST {channel_base_url}/v1/video/generations
Content-Type: application/json
Authorization: Bearer <本次选中的渠道密钥>
```

支持的请求字段：

| 字段 | 要求 |
| --- | --- |
| model | 必填；渠道映射后的名称必须能识别为 MiniMax-H3 |
| prompt | 必填、非空字符串 |
| duration | 必填、JSON 整数；1～`relaycommon.MaxTaskDurationSeconds`（当前 3600） |
| images | 可选，图片 URL 字符串数组，支持多参考图 |
| media | 可选，统一媒体条目数组，按 type 将图片、视频 URL 迁移到上游字段 |
| metadata | 对象，允许任意扩展字段，保留原始 JSON 值 |
| metadata.resolution | 必填，仅 768P / 2K |
| metadata.ratio | 可选，非空字符串，例如 16:9 |
| metadata.video_urls | 可选，视频 URL 字符串数组，支持多参考视频 |

不猜测缺省时长和分辨率。H3 的必填字段缺失、非法分辨率、非整数/越界时长返回 HTTP 400、`invalid_request`。顶层字段沿用 `TaskSubmitReq` 的解码和序列化，不设置额外字段白名单。

metadata 不设置字段白名单：`generate_audio`、`seed`、嵌套对象等扩展参数均可透传，保留显式 false、0、null 及大整数/小数精度。H3 始终使用顶层 duration 和 metadata.resolution 计费，其他 metadata 字段不替代这两个计费来源，也不额外增加倍率。已知的 ratio、video_urls 仍校验字段类型。

仅接受 JSON。Remix、原生 Ali 请求以及需要认证的 content 下载流程暂不支持，不新增相应转换或下载逻辑。

文生视频请求：

```json
{
  "model": "MiniMax-H3",
  "prompt": "黄昏海岸线，电影级光影，慢镜头推进，海浪拍打礁石",
  "duration": 5,
  "metadata": {
    "resolution": "768P",
    "ratio": "16:9"
  }
}
```

图生视频在顶层增加 `"images": ["https://cdn.example.com/image.png"]`；视频生视频在 metadata 中增加 `"video_urls": ["https://cdn.example.com/input.mp4"]`。多参考输入在对应数组中增加 URL，不额外乘以参考文件数量。

也可以使用项目统一的 Media 格式：

```json
{
  "model": "MiniMax-H3/provider",
  "prompt": "参考图片中的人物和视频中的运动轨迹",
  "duration": 5,
  "media": [
    {"type": "images", "url": "https://cdn.example.com/face.png"},
    {"type": "video_urls", "url": "https://cdn.example.com/input.mp4"}
  ],
  "metadata": {
    "resolution": "768P",
    "ratio": "16:9",
    "generate_audio": false
  }
}
```

| Media type | 上游目标字段 |
| --- | --- |
| images | 顶层 images 数组 |
| video_urls | metadata.video_urls 数组 |

type 忽略大小写和首尾空格。参考 Ali 的数组处理，先保留已有 images / metadata.video_urls，再按 Media 的顺序追加对应引用；迁移后不向上游发送 media 字段。空 URL 和不支持的媒体类型返回 HTTP 400。估算和构建不修改缓存请求，重试时不会重复追加引用。

构建请求时使用同一份已校验、迁移的参数，保留图片/视频 URL、prompt、metadata 扩展值和完整模型名；消除顶层和 metadata 直接字段的重复 JSON 键造成的取值歧义。

## 4. 计费配置与一致性

必须为**客户端请求的完整模型名**配置 `ModelPrice`，单位为 **768P 每秒基价**。不接受仅配置 token 倍率、未配置价格或通过 `TaskPricePatches` 跳过倍率的情况；这些请求返回 HTTP 400、`invalid_billing_config`。

```text
OtherRatios.seconds    = duration
OtherRatios.resolution = 768P: 1；2K: 1.6

费用 = ModelPrice × duration × resolution 倍率 × 分组倍率
```

当每秒基价与上游 0.5 的币种折算一致、分组倍率为 1 时：

| 参数 | 上游报价币种下的金额 |
| --- | --- |
| 5 秒，768P | 2.5 |
| 5 秒，2K | 4 |
| 8 秒，2K | 6.4 |

本项目 `ModelPrice` 以美元存储。给出的 0.5 / 0.8 未注明币种，所以代码不擅自写入全局价格：若为美元，768P 基价填 0.5；若为人民币，先按站点采用的人民币/美元汇率折算。不要将 5 秒整段视频的价格填作每秒基价。站点自行配置的加价、折扣和分组倍率仍然生效。

计费与实际发送请求共用 duration 和规范后的 resolution，不叠加默认 6 秒、参考图/视频数量、宽高比或未知参数倍率。沿用现有额度饱和检查、预扣、提交结算和失败退款链路。该接入按已确认的请求参数报价，不从视频文件或响应中推测额外用量。

## 5. 响应、轮询与视频直链

- 提交响应中的 `created_at` 可以是 RFC3339 字符串，轮询响应可以是 Unix 秒；不强制按 int64 解码，保留上游原始字段。
- 提交和轮询保存完整的上游 JSON：`completed_at`、`created_at`、`error`（含 null）、`id`、`model`、`object`、`output`、`progress`、`seconds`、`size`、`status` 及未知扩展字段均保留。New API 不经过其他视频渠道的通用响应裁剪，避免丢字段或大整数精度。
- 保存上游 task ID，向客户端返回本站公开 ID。
- 后台轮询使用 `GET {base}/v1/video/generations/{upstream_task_id}` 和任务提交时选中的密钥。视频直链免认证不意味着任务查询免认证，因此 `model.InitTask` 仍需保存该密钥。
- 支持 queued / pending、submitted、processing / in_progress、completed、failed / cancelled 等状态。
- 完成时读取 `output.video_url`，兼容 `metadata.url`，要求为绝对 HTTP(S) URL。保存到现有 `Task.PrivateData.ResultURL`，不构造上游认证 content 地址。
- 完成响应缺少直链或返回相对路径、data/file URI 时，后台解析报错，不标记为可下载成功；后续沿用现有重试和超时处理。

客户端查询：

| 路径 | 响应 |
| --- | --- |
| GET /v1/video/generations/{本站 task_id} | 项目现有通用任务信封，完整上游响应在 `data.data`，直链在 `data.result_url` |
| GET /v1/videos/{本站 task_id} | 顶层保留上游 video 对象及其扩展字段，直链在 `output.video_url` 和 `metadata.url` |

OpenAI video 格式在完整上游响应上覆盖本站 `id` / `task_id`、客户端模型名称、本地任务状态与进度；失败时可补充本站失败原因，成功时补充 `metadata.url`。其他字段不按固定 DTO 白名单截取，`output` / `metadata` 内的扩展字段也保留。

已有本站视频代理可使用保存的直链下载，不给 CDN 请求附加上游渠道密钥。本次没有修改下载控制器或恢复 Remix 链路。

## 6. 验证

回归覆盖渠道 URL 与模型联合匹配、未知及相似域名拦截、装饰名称与模型映射、HTTP 400 拦截且未向上游发送请求、明确基价要求、三种输入方式与多参考计费、metadata 扩展字段及数值精度、Media 映射与合并、重试不重复追加、请求/计费参数一致、字符串时间戳、实际提交/轮询 HTTP 路径、保存任务密钥及直链结果。完整响应测试经过真实轮询、SQLite 落库和两种查询接口，验证所有标准字段、扩展字段和大整数精度。

```powershell
$env:GIN_MODE = 'release'
go test -p 1 ./relay/channel/task/... ./relay ./model ./service
```

HTTP 集成测试保留 Tokease 渠道 URL 参与分派，仅在传输层将网络流量送到 httptest 模拟上游；没有使用真实渠道密钥或向厂商提交付费任务。
