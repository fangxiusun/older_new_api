# API 文档目录

本文档目录用于沉淀系统对外 HTTP API 的代码级说明。调研入口来自 `router/main.go`，实际路由由 `router/api-router.go`、`router/relay-router.go`、`router/video-router.go`、`router/dashboard.go` 注册。

## 字段是否充足

你列出的 6 项是 API 文档的核心项：

1. 接口调用路径
2. 认证方式
3. 接口输入消息格式
4. 接口输出消息格式
5. 接口相关代码
6. Go 对接可行性

但为了让文档可直接用于外部系统对接，还需要补充这些字段：

- HTTP 方法：同一路径不同方法语义不同，例如 `GET /api/user/epay/notify` 与 `POST /api/user/epay/notify`。
- 权限角色：区分游客、普通用户、管理员、Root、API Key。
- Content-Type：JSON、multipart/form-data、SSE、WebSocket、表单回调等处理方式不同。
- 路径参数与查询参数：例如 `:id`、`:task_id`、`?p=`、`?page_size=`、`?alt=sse`。
- 通用响应包裹：管理台接口多为 `{success,message,data}`，模型代理接口多为 OpenAI/Claude/Gemini 原生格式。
- 错误格式与状态码：管理台很多业务错误返回 HTTP 200 + `success:false`，模型代理按 OpenAI/Claude 错误格式返回 4xx/5xx。
- 限流、中间件与副作用：鉴权、请求体大小限制、额度预扣费、日志、渠道选择、缓存、审计。
- 兼容性说明：哪些接口是 OpenAI/Claude/Gemini 官方兼容，哪些是系统私有 API。
- 示例：核心对接接口应提供 curl/Go 调用思路。

因此本目录后续文档统一按以下模板组织：

```text
接口：METHOD path
认证：匿名 / UserAuth / AdminAuth / RootAuth / TokenAuth / TokenOrUserAuth / 回调签名
输入：path/query/header/body/content-type
输出：成功响应、错误响应、流式或文件响应
处理逻辑：关键中间件、controller、service/model、额度与渠道副作用
代码位置：router/controller/dto/model/service/relay
Go 对接：是否可行、推荐方式、注意事项
```

## 文档分卷

- `01-auth-common.md`：认证方式、通用中间件、通用响应与错误格式。
- `02-relay-model-apis.md`：OpenAI/Claude/Gemini 兼容模型代理 API。
- `03-task-video-media-apis.md`：视频、任务、Midjourney、Suno、Kling、Jimeng、Ali 等任务型和多媒体代理 API。
- `04-dashboard-user-apis.md`：公开接口、登录注册、用户自助、令牌、充值、订阅、日志和数据接口。
- `05-admin-management-apis.md`：系统设置、渠道、用户、模型、兑换码、部署、性能等管理接口。
- `06-go-integration-notes.md`：Go 客户端对接方式、示例和风险点。

## 全局入口

代码入口：

- `router/main.go`：调用 `SetApiRouter`、`SetDashboardRouter`、`SetRelayRouter`、`SetVideoRouter`。
- `router/api-router.go`：`/api` 管理台和用户后台 API。
- `router/relay-router.go`：`/v1`、`/v1beta`、`/mj`、`/suno` 等模型代理 API。
- `router/video-router.go`：视频、Kling、Jimeng、Ali 视频相关 API。
- `router/dashboard.go`：兼容旧 OpenAI dashboard billing API。

全局中间件：

- `/api`：`RouteTag("api")`、gzip、请求体缓存清理、`GlobalAPIRateLimit`。
- Relay：CORS、解压、请求体缓存清理、统计、性能检查、API Key 认证、模型限流、渠道分发。
- 管理写接口：`AdminAuth` / `RootAuth` 内部会自动记录管理审计。

