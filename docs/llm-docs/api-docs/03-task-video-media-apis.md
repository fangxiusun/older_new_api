# 任务、视频与多媒体代理 API

任务型接口与普通文本 relay 的主要区别是：先提交任务，系统保存本地任务记录，再通过任务 id 查询结果。

## 通用任务链路

处理代码：

- 路由：`router/video-router.go`、`router/relay-router.go`
- controller：`controller/relay.go:RelayTask`、`controller/relay.go:RelayTaskFetch`
- 任务转发：`relay/relay_task.go`
- DTO：`dto/task.go`、`dto/video.go`
- 任务模型：`model/task.go`
- 任务计费：`service/task_billing.go`

提交任务时：

1. `TokenAuth` 认证 API Key。
2. `Distribute` 根据模型和分组选渠道。
3. `relay.ResolveOriginTask` 解析原始任务请求。
4. `relay.RelayTaskSubmit` 请求上游。
5. 成功后 `service.SettleBilling` 结算。
6. 写入本地 `tasks` 表，保存上游 task id、平台、渠道、计费上下文等。

任务错误格式：

```json
{
  "code": "get_channel_failed",
  "message": "当前分组上游负载已饱和，请稍后再试",
  "data": null
}
```

## OpenAI 兼容视频接口

| 接口 | 认证 | 输入 | 输出 | 代码 |
| --- | --- | --- | --- | --- |
| `POST /v1/videos` | `TokenAuth` + `Distribute` | OpenAI videos create 格式或 `dto.VideoRequest` 兼容字段 | 任务提交结果 | `router/video-router.go`、`controller.RelayTask` |
| `GET /v1/videos/:task_id` | `TokenAuth` + `Distribute` | `:task_id` | 任务详情/上游结果 | `controller.RelayTaskFetch` |
| `GET /v1/videos/:task_id/content` | `TokenOrUserAuth` | `:task_id` | 视频文件/重定向/二进制流 | `controller/video_proxy.go:VideoProxy` |
| `POST /v1/videos/:video_id/remix` | `TokenAuth` + `Distribute` | `:video_id` + body | remix 任务提交结果 | `relay/relay_task.go` |

通用输入结构参考 `dto.VideoRequest`：

```json
{
  "model": "video-model",
  "prompt": "a running robot",
  "image": "https://example.com/input.png",
  "duration": 5,
  "width": 1280,
  "height": 720,
  "fps": 30,
  "seed": 123,
  "n": 1,
  "response_format": "url",
  "metadata": {
    "negative_prompt": "blur"
  }
}
```

Go 对接：可行。提交和查询用 JSON；content 接口按二进制流处理。

## 系统视频生成接口

| 接口 | 认证 | 输入 | 输出 |
| --- | --- | --- | --- |
| `POST /v1/video/generations` | `TokenAuth` + `Distribute` | `dto.VideoRequest` | `dto.VideoResponse` 或任务响应 |
| `GET /v1/video/generations/:task_id` | `TokenAuth` + `Distribute` | `:task_id` | `dto.VideoTaskResponse` 或上游结果 |

代码：

- `router/video-router.go`
- `dto/video.go`
- `controller/relay.go:RelayTask`
- `controller/relay.go:RelayTaskFetch`

## Kling 官方兼容接口

| 接口 | 认证 | 输入 | 输出 | 中间件 |
| --- | --- | --- | --- | --- |
| `POST /kling/v1/videos/text2video` | API Key | Kling text2video 官方格式 | Kling/任务响应 | `KlingRequestConvert` |
| `POST /kling/v1/videos/image2video` | API Key | Kling image2video 官方格式 | Kling/任务响应 | `KlingRequestConvert` |
| `GET /kling/v1/videos/text2video/:task_id` | API Key | `:task_id` | 查询结果 | `KlingRequestConvert` |
| `GET /kling/v1/videos/image2video/:task_id` | API Key | `:task_id` | 查询结果 | `KlingRequestConvert` |

关键代码：

- `router/video-router.go`
- `middleware/kling_adapter.go`
- `controller/relay.go:RelayTask`
- `controller/swag_video.go` 中保留了请求结构说明：`KlingText2VideoRequest`、`KlingImage2VideoRequest`

Go 对接：可行。按 Kling 官方 JSON 格式请求即可，API Key 换成本系统令牌。

## Jimeng 官方兼容接口

接口：

```http
POST /jimeng/
```

认证：`TokenAuth` + `Distribute`。

中间件：`middleware.JimengRequestConvert()`。

输入：即梦官方 API 格式，例如 query/body 中包含 `Action=CVSync2AsyncSubmitTask` 或 `Action=CVSync2AsyncGetResult` 语义，由中间件转换。

代码：

- `router/video-router.go`
- `middleware/jimeng_adapter.go`
- `controller/relay.go:RelayTask`

Go 对接：可行。按官方接口形态发起请求，但域名换成本系统。

## Ali 官方兼容视频接口

| 接口 | 认证 | 输入 | 输出 |
| --- | --- | --- | --- |
| `POST /ali/v1/services/aigc/video-generation/video-synthesis` | `TokenAuth` + `Distribute` | 阿里云视频生成官方格式 | 阿里原生格式 |
| `POST /ali/v1/services/aigc/image2video/video-synthesis` | `TokenAuth` + `Distribute` | 阿里云图生视频官方格式 | 阿里原生格式 |
| `GET /ali/v1/tasks/:task_id` | `TokenAuth` + `Distribute` | `:task_id` | 阿里原生任务查询格式 |

中间件：`middleware.AliRequestConvert()`。

代码：

- `router/video-router.go`
- `middleware/ali_adapter.go`
- `relay/channel/task/ali/*`

Go 对接：可行。按阿里官方 JSON/HTTP 结构对接，认证换成本系统 API Key。

## Suno 接口

| 接口 | 认证 | 输入 | 输出 |
| --- | --- | --- | --- |
| `POST /suno/submit/:action` | `TokenAuth` + `Distribute` | Suno 任务提交请求，`:action` 决定动作 | `dto.TaskResponse` |
| `POST /suno/fetch` | `TokenAuth` + `Distribute` | `dto.FetchReq`，如 `{"ids":["..."]}` | Suno 任务列表 |
| `GET /suno/fetch/:id` | `TokenAuth` + `Distribute` | `:id` | 单任务结果 |

代码：

- `router/relay-router.go`
- `relay/channel/task/suno/adaptor.go`
- `dto/suno.go`
- `dto/task.go`

Go 对接：可行。普通 JSON HTTP。

## Midjourney 兼容接口

Midjourney 提供两组路径：

- `/mj/...`
- `/:mode/mj/...`

其中 `/:mode/mj` 可兼容某些代理的路径前缀。

### 图片读取

```http
GET /mj/image/:id
GET /:mode/mj/image/:id
```

认证：无 `TokenAuth`，直接由 `relay.RelayMidjourneyImage` 处理图片读取。

代码：`relay/mjproxy_handler.go:RelayMidjourneyImage`。

### 提交与查询

| 接口 | 方法 | 输入 |
| --- | --- | --- |
| `/mj/submit/action` | POST | `dto.MidjourneyRequest` |
| `/mj/submit/shorten` | POST | `dto.MidjourneyRequest` |
| `/mj/submit/modal` | POST | `dto.MidjourneyRequest` |
| `/mj/submit/imagine` | POST | `dto.MidjourneyRequest` |
| `/mj/submit/change` | POST | `dto.MidjourneyRequest` |
| `/mj/submit/simple-change` | POST | `dto.MidjourneyRequest` |
| `/mj/submit/describe` | POST | `dto.MidjourneyRequest` |
| `/mj/submit/blend` | POST | `dto.MidjourneyRequest` |
| `/mj/submit/edits` | POST | `dto.MidjourneyRequest` |
| `/mj/submit/video` | POST | `dto.MidjourneyRequest` |
| `/mj/task/:id/fetch` | GET | `:id` |
| `/mj/task/:id/image-seed` | GET | `:id` |
| `/mj/task/list-by-condition` | POST | 查询条件 JSON |
| `/mj/insight-face/swap` | POST | `dto.SwapFaceRequest` |
| `/mj/submit/upload-discord-images` | POST | `dto.MidjourneyRequest` |

所有上述接口也存在 `/:mode/mj/...` 版本。

认证：`TokenAuth` + `Distribute`，也兼容 `mj-api-secret` 头。

输入结构：

```json
{
  "prompt": "a cat",
  "customId": "",
  "botType": "",
  "notifyHook": "",
  "action": "",
  "index": 1,
  "state": "",
  "taskId": "",
  "base64Array": [],
  "content": "",
  "maskBase64": ""
}
```

输出：

```json
{
  "code": 1,
  "description": "success",
  "properties": {},
  "result": "task-id"
}
```

错误时 controller 会转换为：

```json
{
  "description": "错误描述",
  "type": "upstream_error",
  "code": 4
}
```

代码：

- `router/relay-router.go:registerMjRouterGroup`
- `controller/relay.go:RelayMidjourney`
- `relay/mjproxy_handler.go`
- `dto/midjourney.go`
- `model/midjourney.go`

Go 对接：可行。普通 JSON HTTP；图片读取接口按图片流处理。

