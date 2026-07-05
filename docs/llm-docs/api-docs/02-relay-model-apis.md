# 模型代理 API

本文件覆盖主要“对外推理服务”接口。它们大多是兼容 OpenAI、Claude、Gemini 官方格式的代理接口。

## 通用处理链路

适用接口：

- `/v1/*`
- `/v1beta/models/*`
- `/mj/*`
- `/suno/*`
- 视频任务接口中的 relay 路径

处理顺序：

1. `router/relay-router.go` 或 `router/video-router.go` 注册路由。
2. `middleware.CORS`、`DecompressRequestMiddleware`、`BodyStorageCleanup`、`StatsMiddleware`。
3. `middleware.SystemPerformanceCheck`。
4. `middleware.TokenAuth` 认证 API Key。
5. `middleware.ModelRequestRateLimit`，部分接口启用。
6. `middleware.Distribute` 选择渠道。
7. `controller.Relay` 或 `controller.RelayTask` 处理请求。
8. `relay/*_handler.go` 和 `relay/channel/*` 适配上游。
9. 额度预扣、失败退款、用量结算、日志记录。

关键代码：

- `router/relay-router.go`
- `middleware/auth.go:TokenAuth`
- `middleware/distributor.go`
- `controller/relay.go:Relay`
- `relay/helper/valid_request.go`
- `relay/common/relay_info.go`
- `relay/relay_adaptor.go`
- `service/pre_consume_quota.go`
- `service/tiered_settle.go`

## Playground Chat Completions

接口：

```http
POST /pg/chat/completions
```

认证：`UserAuth` + `Distribute`，不是 `TokenAuth`。

用途：前端 playground 使用登录用户身份发起 OpenAI chat completions。后端会用当前用户临时构造一个 `model.Token` 上下文，然后复用 `controller.Relay`。

输入：

- 请求体整体沿用 `dto.GeneralOpenAIRequest`，至少需要 `model` 和 `messages`。
- `dto.PlayGroundRequest` 中保留 `model`、`group` 字段用于 playground 场景。

限制：

- 如果当前请求是通过用户 access token 进入，而不是 session 登录，`controller.Playground` 会返回 `access_denied`，错误消息为“暂不支持使用 access token”。

输出：OpenAI chat completions 格式或 OpenAI 错误格式。

处理代码：

- `router/relay-router.go`
- `controller/playground.go:Playground`
- `dto/playground.go`
- `controller/relay.go:Relay`

Go 对接：不推荐作为服务端集成入口。外部服务应使用 `POST /v1/chat/completions` + API Key；`/pg` 更偏前端登录态调试。

## 模型列表

| 接口 | 认证 | 输入 | 输出 | 处理代码 |
| --- | --- | --- | --- | --- |
| `GET /v1/models` | `TokenAuth` | Header 决定 OpenAI/Claude/Gemini 兼容模式 | OpenAI/Claude/Gemini 模型列表或模型详情 | `router/relay-router.go`、`controller/model.go:ListModels` |
| `GET /v1/models/:model` | `TokenAuth` | `:model` | 单模型详情 | `controller/model.go:RetrieveModel` |
| `GET /v1beta/models` | `TokenAuth` | Gemini API Key 传法也支持 | Gemini 模型列表 | `controller/model.go:ListModels` |
| `GET /v1beta/openai/models` | `TokenAuth` | Gemini-compatible OpenAI path | OpenAI 模型列表 | `controller/model.go:ListModels` |

认证兼容：

- OpenAI：`Authorization: Bearer sk-xxx`
- Claude：`x-api-key: sk-xxx` + `anthropic-version`
- Gemini：`x-goog-api-key: sk-xxx` 或 `?key=sk-xxx`

## OpenAI Chat Completions

接口：

```http
POST /v1/chat/completions
```

认证：`TokenAuth`，推荐：

```http
Authorization: Bearer sk-xxx
Content-Type: application/json
```

输入结构来自 `dto.GeneralOpenAIRequest`：

```json
{
  "model": "gpt-4o-mini",
  "messages": [
    {"role": "system", "content": "You are helpful."},
    {"role": "user", "content": "hello"}
  ],
  "stream": false,
  "temperature": 0.7,
  "max_tokens": 1024,
  "max_completion_tokens": 1024,
  "tools": [],
  "tool_choice": "auto"
}
```

关键字段：

- `model` 必填。
- `messages` 必填，除非使用 FIM 的 `prefix`/`suffix`。
- `stream` 为 `true` 时返回 SSE。
- 支持图像、音频、文件、视频 URL 形式的多模态 content：`image_url`、`input_audio`、`file`、`video_url`。
- 支持 `response_format`、`service_tier`、`store`、`prompt_cache_key`、`reasoning_effort`、`web_search_options` 等扩展字段。

校验逻辑：

- `relay/helper/valid_request.go:GetAndValidateTextRequest`
- `model` 不能为空。
- chat 模式 `messages` 不能为空，除非 `prefix` 或 `suffix` 存在。
- `max_tokens` 不能超过 `math.MaxInt32/2`。
- `web_search_options.search_context_size` 只能是 `high`、`medium`、`low`。

输出：

- 非流式：OpenAI chat completion 格式，结构参考 `dto.OpenAITextResponse`。
- 流式：SSE `data: {...}`，结构参考 `dto.ChatCompletionsStreamResponse`。
- 错误：`{"error": types.OpenAIError}`。

处理代码：

- 路由：`router/relay-router.go`
- 请求 DTO：`dto/openai_request.go:GeneralOpenAIRequest`
- 响应 DTO：`dto/openai_response.go`
- controller：`controller/relay.go:Relay`
- handler：`relay/relay_task.go` 以外的文本请求进入 `relay.TextHelper`

Go 对接：可行。OpenAI Go SDK 可把 BaseURL 指到系统 `/v1`，也可用 `net/http` 直接 POST。

## OpenAI Completions

接口：

```http
POST /v1/completions
```

输入来自 `dto.GeneralOpenAIRequest`：

```json
{
  "model": "text-model",
  "prompt": "hello",
  "stream": false,
  "max_tokens": 256
}
```

校验：

- `model` 必填。
- `prompt` 必填。

输出：OpenAI completions 格式或 SSE。代码路径同 chat completions，relay mode 由 `relay/constant` 根据 path 判断。

## OpenAI Responses API

接口：

```http
POST /v1/responses
POST /v1/responses/compact
```

认证：`TokenAuth`。

`/v1/responses` 输入来自 `dto.OpenAIResponsesRequest`：

```json
{
  "model": "gpt-4.1",
  "input": "hello",
  "stream": false,
  "max_output_tokens": 1024,
  "tools": [],
  "reasoning": {"effort": "medium"}
}
```

必填：

- `model`
- `input`

`/v1/responses/compact` 输入来自 `dto.OpenAIResponsesCompactionRequest`，`model` 必填。

输出：

- 非流式：`dto.OpenAIResponsesResponse`
- 流式：`dto.ResponsesStreamResponse`
- 错误：OpenAI 错误格式

处理代码：

- `relay/helper/valid_request.go:GetAndValidateResponsesRequest`
- `relay/helper/valid_request.go:GetAndValidateResponsesCompactionRequest`
- `relay/responses_handler.go`
- `relay/chat_completions_via_responses.go`

Go 对接：可行。新版 OpenAI SDK 若支持 Responses，可直接设置 BaseURL；否则用普通 JSON POST。

## OpenAI Images

接口：

```http
POST /v1/images/generations
POST /v1/images/edits
POST /v1/edits
```

认证：`TokenAuth`。

输入来自 `dto.ImageRequest`：

```json
{
  "model": "gpt-image-1",
  "prompt": "a cat",
  "n": 1,
  "size": "1024x1024",
  "quality": "auto",
  "response_format": "url",
  "stream": false
}
```

`/v1/images/edits` 支持 JSON，也支持 `multipart/form-data`。multipart 时读取字段：

- `model`
- `prompt`
- `n`
- `quality`
- `size`
- `stream`
- `image`
- `watermark`

校验/默认值：

- `model` 必填。
- `dall-e-2` / `dall-e` 限制 size 为 `256x256`、`512x512`、`1024x1024`。
- `dall-e-3` 限制 size 为 `1024x1024`、`1024x1792`、`1792x1024`，默认 `quality=standard`。
- `gpt-image-1` 默认 `quality=auto`。
- `n` 默认 1。

输出：`dto.ImageResponse`：

```json
{
  "created": 1234567890,
  "data": [
    {"url": "...", "b64_json": "...", "revised_prompt": "..."}
  ]
}
```

处理代码：

- `dto/openai_image.go`
- `relay/helper/valid_request.go:GetAndValidOpenAIImageRequest`
- `relay/image_handler.go`
- `relay/channel/openai/adaptor.go`

Go 对接：可行。生成图用 JSON；编辑图建议用 `multipart.Writer`。

## Embeddings

接口：

```http
POST /v1/embeddings
POST /v1/engines/:model/embeddings
```

输入来自 `dto.EmbeddingRequest`：

```json
{
  "model": "text-embedding-3-small",
  "input": ["hello", "world"],
  "encoding_format": "float",
  "dimensions": 1024
}
```

校验：

- `input` 必填。
- `/v1/engines/:model/embeddings` 可从路径参数补 `model`。

输出：`dto.EmbeddingResponse` 或上游兼容结构。

处理代码：

- `dto/embedding.go`
- `relay/helper/valid_request.go:GetAndValidateEmbeddingRequest`
- `relay/embedding_handler.go`

Go 对接：可行。OpenAI SDK 或普通 HTTP 均可。

## Audio

接口：

```http
POST /v1/audio/transcriptions
POST /v1/audio/translations
POST /v1/audio/speech
```

输入来自 `dto.AudioRequest`：

```json
{
  "model": "whisper-1",
  "input": "base64-or-text",
  "voice": "alloy",
  "response_format": "json",
  "speed": 1.0,
  "stream_format": "sse"
}
```

校验：

- `model` 必填。
- transcription/translation 默认 `response_format=json`。
- speech 也要求 `model`。
- `stream_format=sse` 时视为流式。

输出：

- 转录/翻译：`dto.AudioResponse` 或 `dto.WhisperVerboseJSONResponse`。
- TTS：音频二进制或上游格式，取决于渠道。

处理代码：

- `dto/audio.go`
- `relay/helper/valid_request.go:GetAndValidAudioRequest`
- `relay/audio_handler.go`

Go 对接：可行。TTS 二进制响应要按 `Content-Type` 保存或流式转发。

## Rerank

接口：

```http
POST /v1/rerank
```

输入来自 `dto.RerankRequest`：

```json
{
  "model": "rerank-model",
  "query": "what is api gateway",
  "documents": ["doc1", "doc2"],
  "top_n": 2,
  "return_documents": true
}
```

校验：

- `query` 不能为空。
- `documents` 不能为空。

输出：`dto.RerankResponse`：

```json
{
  "results": [
    {"index": 0, "relevance_score": 0.99, "document": "doc1"}
  ],
  "usage": {}
}
```

处理代码：

- `dto/rerank.go`
- `relay/helper/valid_request.go:GetAndValidateRerankRequest`
- `relay/rerank_handler.go`

Go 对接：可行，普通 JSON POST。

## Moderations

接口：

```http
POST /v1/moderations
```

输入来自 `dto.GeneralOpenAIRequest`：

```json
{
  "model": "omni-moderation-latest",
  "input": "text to moderate"
}
```

校验：

- `input` 必填。
- `model` 为空时默认 `text-moderation-latest`。

处理代码：

- `router/relay-router.go`
- `relay/helper/valid_request.go:GetAndValidateTextRequest`
- `relay.TextHelper`

## Claude Messages

接口：

```http
POST /v1/messages
```

认证：

```http
x-api-key: sk-xxx
anthropic-version: 2023-06-01
Content-Type: application/json
```

也可使用：

```http
Authorization: Bearer sk-xxx
```

输入来自 `dto.ClaudeRequest`：

```json
{
  "model": "claude-3-5-sonnet",
  "system": "You are helpful.",
  "messages": [
    {"role": "user", "content": "hello"}
  ],
  "max_tokens": 1024,
  "stream": false,
  "tools": []
}
```

必填：

- `model`
- `messages`

支持字段：

- `system` 可为字符串或媒体数组。
- `tools`、`tool_choice`、`thinking`、`mcp_servers`、`metadata`。
- `inference_geo`、`speed`、`service_tier` 默认由渠道设置决定是否透传。

输出：

- 成功：Claude Messages 格式，结构参考 `dto.ClaudeResponse`。
- 错误：`{"type":"error","error":{...}}`。

处理代码：

- `dto/claude.go`
- `relay/helper/valid_request.go:GetAndValidateClaudeRequest`
- `controller/relay.go:Relay`
- `relay/claude_handler.go`

Go 对接：可行。Anthropic Go SDK 若允许自定义 base URL，可直接用；否则普通 HTTP。

## Gemini Native API

接口：

```http
GET  /v1beta/models
POST /v1beta/models/*path
POST /v1/models/*path
```

典型路径：

```http
POST /v1beta/models/gemini-2.5-pro:generateContent
POST /v1beta/models/gemini-2.5-pro:streamGenerateContent
POST /v1beta/models/text-embedding-004:embedContent
POST /v1beta/models/text-embedding-004:batchEmbedContents
```

认证：

```http
x-goog-api-key: sk-xxx
```

或：

```http
?key=sk-xxx
Authorization: Bearer sk-xxx
```

输入来自 `dto.GeminiChatRequest`：

```json
{
  "contents": [
    {
      "role": "user",
      "parts": [{"text": "hello"}]
    }
  ],
  "generationConfig": {
    "temperature": 0.7,
    "maxOutputTokens": 1024
  }
}
```

兼容点：

- 支持 snake_case 与 camelCase：例如 `system_instruction` / `systemInstruction`。
- 流式判断：`?alt=sse` 或路径包含 `streamGenerateContent`。
- embedding 请求使用 `dto.GeminiEmbeddingRequest` / `dto.GeminiBatchEmbeddingRequest`。

输出：

- 文本：`dto.GeminiChatResponse`
- embedding：`dto.GeminiEmbeddingResponse` / `dto.GeminiBatchEmbeddingResponse`

处理代码：

- `dto/gemini.go`
- `relay/helper/valid_request.go:GetAndValidateGeminiRequest`
- `relay/helper/valid_request.go:GetAndValidateGeminiEmbeddingRequest`
- `relay/gemini_handler.go`

Go 对接：可行。Gemini SDK 如可配置 endpoint 和 API key header/query 可直接对接；否则普通 HTTP。

## Realtime WebSocket

接口：

```http
GET /v1/realtime
```

认证：

```http
Sec-WebSocket-Protocol: realtime, openai-insecure-api-key.sk-xxx, openai-beta.realtime-v1
```

处理：

- `TokenAuth` 从 `Sec-WebSocket-Protocol` 解析 `openai-insecure-api-key`。
- `controller.Relay` 升级为 WebSocket。
- `relay.WssHelper` 转发。

代码：

- `router/relay-router.go`
- `middleware/auth.go:TokenAuth`
- `controller/relay.go:Relay`
- `relay/websocket.go`

Go 对接：可行，使用 `gorilla/websocket` 或 `nhooyr.io/websocket`，注意子协议必须带上。

## 未实现接口

以下路由会返回 501：

- `POST /v1/images/variations`
- `GET /v1/files`
- `POST /v1/files`
- `DELETE /v1/files/:id`
- `GET /v1/files/:id`
- `GET /v1/files/:id/content`
- `POST /v1/fine-tunes`
- `GET /v1/fine-tunes`
- `GET /v1/fine-tunes/:id`
- `POST /v1/fine-tunes/:id/cancel`
- `GET /v1/fine-tunes/:id/events`
- `DELETE /v1/models/:model`

输出：

```json
{
  "error": {
    "message": "API not implemented",
    "type": "new_api_error",
    "param": "",
    "code": "api_not_implemented"
  }
}
```

代码：`controller/relay.go:RelayNotImplemented`。


