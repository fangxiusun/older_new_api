# Ali 渠道请求/应答字段映射详细参考

> 生成时间: 2026-06-17
> 本文档精确列出每条链路中「客户端发送的字段 → 系统内部处理 → 发送给上游 Ali 服务的字段 → 上游返回的字段 → 系统返回给客户端的字段」的完整映射关系，并标注对应的代码文件和行号。

---

## 目录

1. [文本 Chat Completions](#1-文本-chat-completions)
2. [Claude Messages 格式](#2-claude-messages-格式)
3. [Embeddings](#3-embeddings)
4. [Rerank](#4-rerank)
5. [图片生成（同步模型）](#5-图片生成同步模型)
6. [图片生成（异步模型）](#6-图片生成异步模型)
7. [图片编辑（非 Wan 模型）](#7-图片编辑非-wan-模型)
8. [图片编辑（Wan 模型）](#8-图片编辑wan-模型)
9. [视频任务提交](#9-视频任务提交)
10. [视频任务轮询](#10-视频任务轮询)
11. [视频任务查询返回](#11-视频任务查询返回)
12. [视频任务 Image/Images → Media 与 Size/Duration/Seconds → Metadata 迁移](#12-视频任务-imageimages--media-与-sizedurationseconds--metadata-迁移)

---

## 1. 文本 Chat Completions

### 1.1 客户端请求

客户端发送标准 OpenAI 格式请求到：
```
POST /v1/chat/completions
```

**客户端 Body 字段**（`dto/openai_request.go` GeneralOpenAIRequest）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `model` | string | 模型名，如 `qwen-turbo` |
| `messages` | []Message | 消息列表 |
| `stream` | *bool | 是否流式 |
| `stream_options` | *StreamOptions | 流选项 |
| `max_tokens` | *uint | 最大 token |
| `temperature` | *float64 | 温度 |
| `top_p` | *float64 | TopP |
| `stop` | any | 停止词 |
| `tools` | []ToolCallRequest | 工具列表 |
| `tool_choice` | any | 工具选择 |
| `response_format` | *ResponseFormat | 响应格式 |
| `seed` | *float64 | 随机种子 |

### 1.2 系统内部处理

**路由匹配**：`router/relay-router.go:107`
```go
httpRouter.POST("/chat/completions", func(c *gin.Context) {
    controller.Relay(c, types.RelayFormatOpenAI)
})
```

**Adaptor 分发**：`relay/relay_adaptor.go:GetAdaptor()` → `constant.ChannelTypeAli=17` → `ali.Adaptor{}`

**请求转换**：`relay/channel/ali/adaptor.go:140-146` ConvertOpenAIRequest()
```
调用 requestOpenAI2Ali(request)  →  relay/channel/ali/text.go:12-19
```

**requestOpenAI2Ali 转换逻辑**（`relay/channel/ali/text.go:12-19`）：

| 客户端字段 | 转换规则 | 上游字段 |
|-----------|---------|---------|
| `top_p` ≥ 1 | → 设为 0.999 | `top_p` = 0.999 |
| `top_p` ≤ 0 | → 设为 0.001 | `top_p` = 0.001 |
| 其他所有字段 | **原样透传** | 同名字段 |

> 结论：文本模型的请求基本是 **1:1 透传**，仅修正 TopP 范围。

### 1.3 发送给上游 Ali

**URL**（`relay/channel/ali/adaptor.go:91-99`）：
```
POST {baseURL}/compatible-mode/v1/chat/completions
```

**Header**（`relay/channel/ali/adaptor.go:130-143`）：

| Header | 值 | 代码行 |
|--------|---|--------|
| `Content-Type` | 客户端原始 Content-Type | L131 |
| `Accept` | 客户端原始 Accept 或 `text/event-stream`（流式时） | L132-134 |
| `Authorization` | `Bearer {apiKey}` | L136 |
| `X-DashScope-SSE` | `enable`（流式时） | L137-138 |

**Body**：与客户端请求基本一致，仅 TopP 被修正。

### 1.4 上游返回 → 返回给客户端

**响应处理**：`relay/channel/ali/adaptor.go:228-232`
```go
adaptor := openai.Adaptor{}
usage, err = adaptor.DoResponse(c, resp, info)
```

上游响应**直接透传**给客户端，因为阿里百炼的 `/compatible-mode/v1/chat/completions` 返回的就是 OpenAI 兼容格式。由 `openai.Adaptor.DoResponse()` 负责解析和转发。

---

## 2. Claude Messages 格式

### 2.1 客户端请求

```
POST /v1/messages
```
客户端发送 Claude 格式请求（`dto.ClaudeRequest`）。

### 2.2 系统内部处理

**路由**：`router/relay-router.go:103-105`

**请求转换**：`relay/channel/ali/adaptor.go:73-86` ConvertClaudeRequest()

**分支逻辑**：

| 条件 | 行为 | 目标 URL |
|------|------|----------|
| 模型名匹配 `ALI_ANTHROPIC_MESSAGES_MODELS` 环境变量（默认: `qwen,deepseek-v4,kimi,glm,minimax-m`） | **原样透传** Claude 格式 | `{baseURL}/apps/anthropic/v1/messages` (L96) |
| 不匹配 | 先转为 OpenAI 格式，再走 `ConvertOpenAIRequest` | `{baseURL}/compatible-mode/v1/chat/completions` (L98) |

**匹配函数**：`relay/channel/ali/adaptor.go:39-56`
- 环境变量 `ALI_ANTHROPIC_MESSAGES_MODELS`（L29）
- 默认值 `qwen,deepseek-v4,kimi,glm,minimax-m`（L30）
- 判断逻辑：模型名包含列表中任一模式（L45-47）

### 2.3 字段映射（透传路径）

| 客户端 Claude 字段 | 上游 Ali 字段 | 说明 |
|-------------------|--------------|------|
| `model` | `model` | 透传 |
| `messages` | `messages` | 透传 |
| `max_tokens` | `max_tokens` | 透传 |
| `stream` | `stream` | 透传 |
| `system` | `system` | 透传 |
| `temperature` | `temperature` | 透传 |
| `top_p` | `top_p` | 透传 |
| 所有其他字段 | 同名 | 原样发送 |

### 2.4 字段映射（转换路径）

经过 `service.ClaudeToOpenAIRequest()` 转为 OpenAI 格式后，与第 1 节文本 Chat 相同。

### 2.5 响应处理

**透传路径**（L215-218）：
```go
adaptor := claude.Adaptor{}
return adaptor.DoResponse(c, resp, info)
```
上游返回的 Claude 格式响应直接由 `claude.Adaptor` 解析。

**转换路径**（L221-224）：
```go
adaptor := openai.Adaptor{}
usage, err = adaptor.DoResponse(c, resp, info)
```
上游返回 OpenAI 格式响应。

---

## 3. Embeddings

### 3.1 客户端请求

```
POST /v1/embeddings
```

**客户端 Body**（`dto/embedding.go` EmbeddingRequest）：

| 字段 | 类型 | 必填 |
|------|------|------|
| `model` | string | 是 |
| `input` | any | 是 |
| `encoding_format` | string | 否 |
| `dimensions` | *int | 否 |

### 3.2 系统内部处理

**请求转换**：`relay/channel/ali/adaptor.go:171-173`
```go
func (a *Adaptor) ConvertEmbeddingRequest(...) (any, error) {
    return request, nil  // 直接返回，不做任何转换
}
```

### 3.3 发送给上游

**URL**（`relay/channel/ali/adaptor.go:102-103`）：
```
POST {baseURL}/compatible-mode/v1/embeddings
```

**Header**：与文本请求相同（Bearer Token + 标准头）

**Body**：与客户端请求 **完全一致**，无任何转换。

### 3.4 响应

上游返回 OpenAI 兼容格式，由 `openai.Adaptor.DoResponse()` 处理并透传。

---

## 4. Rerank

### 4.1 客户端请求

```
POST /v1/rerank
```

**客户端 Body**（`dto/rerank.go` RerankRequest）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `model` | string | 模型名 |
| `query` | string | 查询文本 |
| `documents` | []any | 文档列表 |
| `top_n` | *int | 返回前 N 个 |
| `return_documents` | *bool | 是否返回文档内容 |

### 4.2 请求转换

**转换函数**：`relay/channel/ali/rerank.go:16-33` ConvertRerankRequest()

| 客户端字段 | → | 上游 Ali 字段 | 转换规则 |
|-----------|---|--------------|---------|
| `model` | → | `model` | 直接复制 (L23) |
| `query` | → | `input.query` | 嵌套到 input 对象 (L25) |
| `documents` | → | `input.documents` | 嵌套到 input 对象 (L26) |
| `top_n` | → | `parameters.top_n` | 嵌套到 parameters 对象 (L29) |
| `return_documents` | → | `parameters.return_documents` | 若为 nil 则默认 true (L17-21, L30) |

**Ali 原生请求结构**（`relay/channel/ali/dto.go:220-233`）：
```go
type AliRerankRequest struct {
    Model      string              `json:"model"`
    Input      AliRerankInput      `json:"input"`
    Parameters AliRerankParameters `json:"parameters,omitempty"`
}
type AliRerankInput struct {
    Query     string `json:"query"`
    Documents []any  `json:"documents"`
}
type AliRerankParameters struct {
    TopN            *int  `json:"top_n,omitempty"`
    ReturnDocuments *bool `json:"return_documents,omitempty"`
}
```

### 4.3 发送给上游

**URL**（`relay/channel/ali/adaptor.go:104-105`）：
```
POST {baseURL}/api/v1/services/rerank/text-rerank/text-rerank
```

### 4.4 上游返回结构

**Ali 原生响应**（`relay/channel/ali/dto.go:235-244`）：
```go
type AliRerankResponse struct {
    Output struct {
        Results []dto.RerankResponseResult `json:"results"`
    } `json:"output"`
    Usage     AliUsage `json:"usage"`
    RequestId string   `json:"request_id"`
    AliError                            // Code, Message
}
```

### 4.5 响应转换

**转换函数**：`relay/channel/ali/rerank.go:35-75` RerankHandler()

| 上游 Ali 字段 | → | 客户端字段 | 代码行 |
|--------------|---|-----------|--------|
| `output.results` | → | `results` | L63 |
| `usage.total_tokens` | → | `usage.prompt_tokens` | L58 |
| (固定 0) | → | `usage.completion_tokens` | L59 |
| `usage.total_tokens` | → | `usage.total_tokens` | L60 |
| `code` (非空时) | → | 错误码 | L48-54 |
| `message` | → | 错误消息 | L50 |

**错误处理**：若上游返回 `code` 非空，构造 OpenAI 格式错误返回（L49-54）。

---

## 5. 图片生成（同步模型）

同步图片模型：`z-image`, `qwen-image`, `wan2.6`，以及通过 `model_setting.IsSyncImageModel()` 配置的模型。

### 5.1 客户端请求

```
POST /v1/images/generations
```

**客户端 Body**（`dto/openai_image.go` ImageRequest）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `model` | string | 模型名 |
| `prompt` | string | 提示词 |
| `n` | *uint | 生成数量 |
| `size` | string | 尺寸 |
| `response_format` | string | `url` 或 `b64_json` |
| `watermark` | *bool | 水印 |
| `Extra` | map[string]json.RawMessage | 额外参数（含 `parameters`、`input`） |

### 5.2 请求转换

**转换函数**：`relay/channel/ali/image.go:19-69` oaiImage2AliImageRequest()

| 客户端字段 | → | 上游 Ali 字段 | 转换规则 | 代码行 |
|-----------|---|--------------|---------|--------|
| `model` | → | `model` | 直接复制 | L23 |
| `response_format` | → | `response_format` | 直接复制 | L24 |
| `Extra["parameters"]` | → | `parameters` | JSON 反序列化 | L26-29 |
| `Extra["input"]` | → | `input` | JSON 反序列化 | L43-46 |
| `size` (当无 Extra) | → | `parameters.size` | `x` 替换为 `*` | L34 |
| `n` (当无 Extra) | → | `parameters.n` | 直接转换 | L35 |
| `watermark` | → | `parameters.watermark` | 直接复制 | L36 |
| `prompt` (同步模型) | → | `input.messages[0].content[0].text` | 包装为消息格式 | L53-62 |

**同步模型输入结构**（L53-62）：
```json
{
  "input": {
    "messages": [{
      "role": "user",
      "content": [{"text": "用户提示词"}]
    }]
  }
}
```

**异步模型输入结构**（L64-67）：
```json
{
  "input": {
    "prompt": "用户提示词"
  }
}
```

**计费相关**（L48-51）：
- 若 `z-image` 模型开启了 `prompt_extend`，添加 2 倍比率：`info.PriceData.AddOtherRatio("prompt_extend", 2)`
- 图片数量比率：`info.PriceData.AddOtherRatio("n", float64(parameters.N))`

### 5.3 发送给上游

**URL**（`relay/channel/ali/adaptor.go:109-110`）：
```
POST {baseURL}/api/v1/services/aigc/multimodal-generation/generation
```

**Header**（`relay/channel/ali/adaptor.go:130-143`）：
- `Authorization: Bearer {apiKey}`
- `Content-Type: application/json`
- **注意**：同步图片模型**不设置** `X-DashScope-Async: enable`（L149-151 对比 L148）

### 5.4 上游返回结构

**Ali 原生响应**（`relay/channel/ali/dto.go:139-147`）：
```go
type AliResponse struct {
    Output AliOutput `json:"output"`
    Usage  AliUsage  `json:"usage"`
    AliError
}
```

**AliOutput 中同步图片的关键字段**（`relay/channel/ali/dto.go:110-135`）：
```go
Choices []struct {
    Message struct {
        Content []AliMediaContent `json:"content"`
    } `json:"message"`
} `json:"choices"`
```

### 5.5 响应转换

**处理函数**：`relay/channel/ali/image.go:209-243` aliImageHandler()

同步模型走 `IsSyncImageModel=true` 分支（L219-222）：
```go
aliResponse = &aliTaskResponse
originRespBody = responseBody
```

**转换函数**：`relay/channel/ali/image.go:201-208` responseAli2OpenAIImage()

| 上游 Ali 字段 | → | 客户端字段 | 代码行 |
|--------------|---|-----------|--------|
| `output.choices[].message.content[].image` (HTTP URL) | → | `data[].url` | dto.go:150-159 |
| `output.choices[].message.content[].image` (base64) | → | `data[].b64_json` | dto.go:160-161 |
| `output.choices[].message.content[].text` | → | `data[].revised_prompt` | dto.go:162 |
| `usage.image_count` | → | 计费 n 倍率 | image.go:235-236 |

**返回给客户端的结构**（`dto/openai_image.go`）：
```json
{
  "created": 1234567890,
  "data": [{"url": "...", "b64_json": "...", "revised_prompt": "..."}],
  "metadata": { ... }  // 原始上游响应体
}
```

---

## 6. 图片生成（异步模型）

### 6.1 与同步模型的差异

| 项目 | 同步模型 | 异步模型 |
|------|---------|---------|
| 上游 URL | `/api/v1/services/aigc/multimodal-generation/generation` | `/api/v1/services/aigc/text2image/image-synthesis` |
| Header | 无 `X-DashScope-Async` | 设置 `X-DashScope-Async: enable` (adaptor.go:151) |
| 输入格式 | `messages` 格式 | `prompt` 字符串 |
| 响应处理 | 直接解析结果 | 需要轮询 task_id |

### 6.2 上游首次返回

**Ali 异步响应**（与同步使用同一结构，但关键字段不同）：
```json
{
  "output": {
    "task_id": "xxx",
    "task_status": "PENDING"
  },
  "request_id": "xxx"
}
```

### 6.3 轮询逻辑

**轮询函数**：`relay/channel/ali/image.go:86-119` asyncTaskWait()

| 参数 | 值 | 代码行 |
|------|---|--------|
| 首次等待 | 5 秒 | L100 |
| 每次等待 | 10 秒 | L98 |
| 最大轮询 | 20 次 | L97 |

**轮询请求**（`relay/channel/ali/image.go:74-84` updateTask()）：
```
GET {baseURL}/api/v1/tasks/{task_id}
Header: Authorization: Bearer {apiKey}
```

**轮询状态判断**（L108-115）：
- `SUCCEEDED` → 返回结果
- `FAILED` / `CANCELED` / `UNKNOWN` → 返回
- 其他（空状态）→ 返回（非任务型响应）

### 6.4 异步结果转换

成功后走 `output.results` 路径（`dto.go:181-199` ResultToOpenAIImageDate()）：

| 上游 Ali 字段 | → | 客户端字段 | 代码行 |
|--------------|---|-----------|--------|
| `results[].url` | → | `data[].url` | L190 |
| `results[].b64_image` | → | `data[].b64_json` | L188 |
| `results[].url` (当 b64_json 格式时) | → | 下载后转 base64 | L183-187 |

---

## 7. 图片编辑（非 Wan 模型）

### 7.1 客户端请求

```
POST /v1/images/edits
Content-Type: multipart/form-data
```

**Form 字段**：

| 字段 | 类型 | 说明 |
|------|------|------|
| `model` | string | 模型名 |
| `prompt` | string | 编辑提示词 |
| `image` / `image[]` / `image[N]` | file | 参考图片文件 |
| `n` | uint | 生成数量 |
| `response_format` | string | `url` 或 `b64_json` |

### 7.2 请求转换

**转换函数**：`relay/channel/ali/image.go:122-157` oaiFormEdit2AliImageEdit()

| 客户端字段 | → | 上游 Ali 字段 | 转换规则 | 代码行 |
|-----------|---|--------------|---------|--------|
| `model` | → | `model` | 直接复制 | L124 |
| `response_format` | → | `response_format` | 直接复制 | L125 |
| `image` 文件 | → | `input.messages[0].content[].image` | 读取文件 → base64 → data URL | L127-137 |
| `prompt` | → | `input.messages[0].content[].text` | 追加为最后一条 content | L138-142 |
| `n` | → | `parameters.n` | 转换 | L144 |
| `watermark` | → | `parameters.watermark` | 直接复制 | L145 |

**图片 base64 获取**：`relay/channel/ali/image.go:71-118` getImageBase64sFromForm()
- 支持字段名：`image`、`image[]`、`image[N]`（L82-100）
- 格式：`data:{mimeType};base64,{data}`（L115）

**上游请求结构**（非 Wan 模型）：
```json
{
  "model": "xxx",
  "input": {
    "messages": [{
      "role": "user",
      "content": [
        {"image": "data:image/png;base64,..."},
        {"text": "编辑提示词"}
      ]
    }]
  },
  "parameters": {"n": 1}
}
```

### 7.3 发送给上游

**URL**（`relay/channel/ali/adaptor.go:119-120`）：
```
POST {baseURL}/api/v1/services/aigc/multimodal-generation/generation
```

**Header**（`relay/channel/ali/adaptor.go:153-155`）：
- 设置 `Content-Type: application/json`

### 7.4 响应

与同步/异步图片生成相同，走 `aliImageHandler()` 函数。

---

## 8. 图片编辑（Wan 模型）

### 8.1 判断逻辑

`relay/channel/ali/image_wan.go:42-49`：
- `isOldWanModel()`：包含 "wan" 但不包含 "wan2.6"、"wan2.7" → 旧 Wan
- `isWanModel()`：包含 "wan" → 所有 Wan

### 8.2 旧 Wan 模型（wanx 系列）

**URL**（adaptor.go:115-116）：
```
POST {baseURL}/api/v1/services/aigc/image2image/image-synthesis
```

**转换函数**：`relay/channel/ali/image_wan.go:15-40` oaiFormEdit2WanxImageEdit()

| 客户端字段 | → | 上游 Ali 字段 | 代码行 |
|-----------|---|--------------|--------|
| `model` | → | `model` | L18 |
| `prompt` | → | `input.prompt` | L21 |
| `image` 文件 | → | `input.images[]` | L27 (base64 data URL 数组) |
| `n` | → | `parameters.n` | L35 |

**上游请求结构**：
```json
{
  "model": "wanx-xxx",
  "input": {
    "prompt": "...",
    "images": ["data:image/png;base64,..."]
  },
  "parameters": {"n": 1}
}
```

### 8.3 新 Wan 模型（wan2.6+, wan2.7+）

**URL**（adaptor.go:117-118）：
```
POST {baseURL}/api/v1/services/aigc/image-generation/generation
```

**Header**：设置 `X-DashScope-Async: enable`（adaptor.go:155）
> 注意：新 Wan 图片编辑是**异步**的（`a.IsSyncImageModel = false`，adaptor.go:170）。

请求体结构与旧 Wan 相同或走 oaiImage2AliImageRequest()。

---

## 9. 视频任务提交

### 9.1 客户端请求

```
POST /v1/video/generations
```

**客户端 Body**（`relay/common/relay_info.go:684-696` TaskSubmitReq）：

| 字段 | 类型 | 说明 |
|------|------|------|
| `prompt` | string | 提示词（必填） |
| `model` | string | 模型名 |
| `image` | string | 单张图片 URL |
| `images` | []string | 多张图片 URL |
| `size` | string | 尺寸/分辨率 |
| `duration` | int | 时长（秒） |
| `seconds` | string | 时长（秒，字符串格式） |
| `input_reference` | string | 参考图 URL |
| `mode` | string | 模式 |
| `metadata` | map[string]any | 额外参数（可传递阿里原生参数） |

### 9.2 系统内部流程

**完整提交链路**：`relay/relay_task.go:134-241` RelayTaskSubmit()

```
步骤 1: adaptor.Init(info)                          — L146
步骤 2: adaptor.ValidateRequestAndSetAction(c, info) — L147
步骤 3: ModelMappedHelper → 模型映射                  — L160
步骤 4: model.GenerateTaskID() → 生成公开 task ID     — L165
步骤 5: ModelPriceHelperPerCall → 基础价格计算        — L170
步骤 6: adaptor.EstimateBilling → OtherRatios        — L177
步骤 7: 应用 OtherRatios → quota = base × ∏(ratios)  — L184-190
步骤 8: PreConsumeBilling → 预扣费                    — L194
步骤 9: adaptor.BuildRequestBody → 构建上游请求       — L199
步骤 10: adaptor.DoRequest → 发送到阿里               — L204
步骤 11: adaptor.DoResponse → 解析响应                — L221
步骤 12: adaptor.AdjustBillingOnSubmit → 提交后调整   — L228
```

### 9.3 请求验证

**函数**：`relay/channel/task/ali/adaptor.go:121-123`
```go
func (a *TaskAdaptor) ValidateRequestAndSetAction(...) *dto.TaskError {
    return relaycommon.ValidateBasicTaskRequest(c, info, constant.TaskActionGenerate)
}
```

**ValidateBasicTaskRequest**：`relay/common/relay_utils.go:225-256`
- 支持 `multipart/form-data` 和 `application/json`
- 验证 `prompt` 非空
- 将 `image` 复制到 `images`（如果 `images` 为空）
- 存储到 context：`c.Set("task_request", req)`

### 9.4 EstimateBilling

**函数**：`relay/channel/task/ali/adaptor.go:348-370`

```
1. 从 context 获取 TaskSubmitReq                     — L349
2. 调用 convertToAliRequest() 构建阿里请求            — L354
3. 提取 Duration 作为 "seconds" OtherRatio            — L359-360
4. 调用 ProcessAliOtherRatios() 计算分辨率比率         — L362
5. 合并所有 OtherRatios 返回                          — L366-368
```

**ProcessAliOtherRatios**：`relay/channel/task/ali/adaptor.go:193-253`

按模型名和分辨率计算价格倍率：

| 模型 | 分辨率 | 比率 | 代码行 |
|------|--------|------|--------|
| `wan2.6-i2v` | 720P | 1 | L197 |
| `wan2.6-i2v` | 1080P | 1/0.6 ≈ 1.667 | L198 |
| `wan2.5-t2v-preview` | 480P | 1 | L201 |
| `wan2.5-t2v-preview` | 720P | 2 | L202 |
| `wan2.5-t2v-preview` | 1080P | 1/0.3 ≈ 3.333 | L203 |
| `wan2.5-i2v-preview` | 480P | 1 | L210 |
| `wan2.5-i2v-preview` | 720P | 2 | L211 |
| `wan2.5-i2v-preview` | 1080P | 1/0.3 ≈ 3.333 | L212 |
| ... | ... | ... | ... |

分辨率提取逻辑（L232-251）：
- 优先取 `aliReq.Parameters.Resolution`（如 "720P"）
- 否则从 `aliReq.Parameters.Size`（如 "1920*1080"）推断

### 9.5 构建上游请求

**转换函数**：`relay/channel/task/ali/adaptor.go:255-345` convertToAliRequest()

| 客户端字段 | → | 上游 Ali 字段 | 转换规则 | 代码行 |
|-----------|---|--------------|---------|--------|
| `req.Model` 或 `info.UpstreamModelName` | → | `model` | 模型映射后 | L260-261 |
| `req.Prompt` | → | `input.prompt` | 直接复制 | L263 |
| `req.InputReference` / `req.Images[0]` | → | `input.img_url` | 图生视频时 | L264, L300-302 |
| (默认 true) | → | `parameters.prompt_extend` | 智能改写 | L267 |
| (默认 false) | → | `parameters.watermark` | 无水印 | L268 |
| `req.Size` (含 `*`) | → | `parameters.size` | 如 "1920*1080" | L279 |
| `req.Size` (含 P/p) | → | `parameters.resolution` | 如 "720P" | L281-286 |
| (默认值) | → | `parameters.duration` | 默认 5 秒 | L330-337 |
| `req.Metadata.*` | → | 对应字段 | JSON 反序列化覆盖 | L339-342 |

**默认分辨率**（L289-320）：
- `t2v` 模型 + wan2.5：默认 "1920*1080"（L292）
- `t2v` 模型 + wan2.2：默认 "1920*1080"（L294）
- `i2v` 模型 + wan2.5：默认 "1080P"（L309）
- 其他：默认 "720P"（L319）

**默认时长**（L328-337）：
- `wan2.5` 系列：默认 5 秒（L330）
- `wan2.2` 系列：默认 5 秒（L333）
- 其他：默认 5 秒（L336）

### 9.6 发送给上游

**URL**：`relay/channel/task/ali/adaptor.go:125-140`
```
POST {baseURL}/api/v1/services/aigc/video-generation/video-synthesis
```

所有视频模型使用**同一端点**（L134-139）。

**Header**（`relay/channel/task/ali/adaptor.go:142-151`）：

| Header | 值 | 代码行 |
|--------|---|--------|
| `Content-Type` | `application/json` | L144 |
| `Accept` | `application/json` | L145 |
| `Authorization` | `Bearer {apiKey}` | L146 |

### 9.7 上游返回结构

**Ali 视频响应**（`relay/channel/task/ali/adaptor.go:52-59`）：
```go
type AliVideoResponse struct {
    Output    AliVideoOutput `json:"output"`
    RequestID string         `json:"request_id"`
    Code      string         `json:"code,omitempty"`
    Message   string         `json:"message,omitempty"`
    Usage     *AliUsage      `json:"usage,omitempty"`
}
```

**DoResponse 处理**：`relay/channel/task/ali/adaptor.go:378-403`

| 上游 Ali 字段 | → | 内部字段 | 代码行 |
|--------------|---|---------|--------|
| `output.task_id` | → | `upstreamTaskID`（存入数据库） | L392-393, L402 |
| `code` (非空) | → | TaskError | L389-391 |
| `output.task_id` (空) | → | TaskError | L393-395 |

**返回给客户端的 OpenAI 格式**（L386-401）：
```go
openAIResp := dto.NewOpenAIVideo()
openAIResp.ID = info.PublicTaskID     // 系统生成的公开 ID
openAIResp.TaskID = info.PublicTaskID
openAIResp.Model = model
openAIResp.Status = "queued"           // 初始状态
openAIResp.CreatedAt = timestamp
```

客户端收到：
```json
{
  "id": "task_xxxx",
  "task_id": "task_xxxx",
  "object": "video",
  "model": "wan2.5-i2v-preview",
  "status": "queued",
  "progress": 0,
  "created_at": 1234567890
}
```

---

## 10. 视频任务轮询

### 10.1 轮询入口

**主循环**：`service/task_polling.go:82-129` TaskPollingLoop()
- 每 15 秒执行一次（L84）
- 查询所有未完成任务（L88）
- 按平台分组后调用 `DispatchPlatformUpdate()`（L125）

### 10.2 FetchTask 请求

**函数**：`relay/channel/task/ali/adaptor.go:405-425`

```
GET {baseURL}/api/v1/tasks/{task_id}
Header: Authorization: Bearer {apiKey}
```

| 参数 | 值 | 代码行 |
|------|---|--------|
| URL 模板 | `%s/api/v1/tasks/%s` | L411 |
| Method | GET | L413 |
| Auth | Bearer Token | L418 |

### 10.3 ParseTaskResult

**函数**：`relay/channel/task/ali/adaptor.go:436-469`

| 上游 Ali 字段 | → | TaskInfo 字段 | 映射规则 | 代码行 |
|--------------|---|--------------|---------|--------|
| `output.task_status` = `PENDING` | → | `Status` = `QUEUED` | — | L447-448 |
| `output.task_status` = `RUNNING` | → | `Status` = `IN_PROGRESS` | — | L449-450 |
| `output.task_status` = `SUCCEEDED` | → | `Status` = `SUCCESS` | — | L451-454 |
| `output.task_status` = `FAILED` | → | `Status` = `FAILURE` | — | L455-463 |
| `output.task_status` = `CANCELED` | → | `Status` = `FAILURE` | — | L455 |
| `output.task_status` = `UNKNOWN` | → | `Status` = `FAILURE` | — | L455 |
| `output.video_url` | → | `Url` | 仅 SUCCEEDED 时 | L454 |
| `message` | → | `Reason` | 错误消息 | L457-458 |
| `output.message` | → | `Reason` | 错误消息 | L459-460 |

### 10.4 轮询后计费调整

**AdjustBillingOnComplete**：`relay/channel/task/ali/adaptor.go:371-376`
```go
func (a *TaskAdaptor) AdjustBillingOnComplete(_ *model.Task, _ *relaycommon.TaskInfo) int {
    return 0  // 保持预扣费金额不变
}
```

> 当前 Ali 视频任务**不进行**完成后计费调整。预扣费金额即最终扣费金额。

### 10.5 状态映射总表

| 阿里 task_status | → 内部 TaskStatus | → OpenAI Video Status |
|-----------------|-------------------|----------------------|
| `PENDING` | `QUEUED` | `queued` |
| `RUNNING` | `IN_PROGRESS` | `in_progress` |
| `SUCCEEDED` | `SUCCESS` | `completed` |
| `FAILED` | `FAILURE` | `failed` |
| `CANCELED` | `FAILURE` | `failed` |
| `UNKNOWN` | `FAILURE` | `failed` |

转换函数：`relay/channel/task/ali/adaptor.go:504-516` convertAliStatus()

---

## 11. 视频任务查询返回

### 11.1 客户端请求

```
GET /v1/video/generations/{task_id}
```
或
```
GET /v1/videos/{task_id}
```

### 11.2 处理流程

**路由**：`router/video-router.go:21-22`
```go
videoV1Router.GET("/video/generations/:task_id", controller.RelayTaskFetch)
videoV1Router.GET("/videos/:task_id", controller.RelayTaskFetch)
```

**控制器** → `relay/relay_task.go:268` RelayTaskFetch() → `videoFetchByIDRespBodyBuilder()`

### 11.3 ConvertToOpenAIVideo

**函数**：`relay/channel/task/ali/adaptor.go:471-502`

从数据库中的 `task.Data`（原始阿里响应）转换为 OpenAI Video 格式：

| 数据库中 Ali 字段 | → | 客户端 OpenAI 字段 | 代码行 |
|------------------|---|-------------------|--------|
| (task.TaskID) | → | `id` | L478 |
| `output.task_status` → convertAliStatus() | → | `status` | L479 |
| (task.Properties.OriginModelName) | → | `model` | L480 |
| (task.Progress) | → | `progress` | L481 |
| (task.CreatedAt) | → | `created_at` | L482 |
| (task.UpdatedAt) | → | `completed_at` | L483 |
| `output.video_url` | → | `metadata.url` | L486 |
| `code` | → | `error.code` | L489-493 |
| `message` | → | `error.message` | L492 |
| `output.code` | → | `error.code` | L494-498 |
| `output.message` | → | `error.message` | L497 |

**客户端最终收到的完整结构**：
```json
{
  "id": "task_xxxx",
  "object": "video",
  "model": "wan2.5-i2v-preview",
  "status": "completed",
  "progress": 100,
  "created_at": 1234567890,
  "completed_at": 1234567900,
  "metadata": {
    "url": "https://xxx.oss-cn-xxx.aliyuncs.com/video.mp4"
  }
}
```

---

## 12. 视频任务 Image/Images → Media 与 Size/Duration/Seconds → Metadata 迁移

为兼容仅提供标准化字段的调用格式（如 Sora 风格请求），在 `buildNewFormatRequest` /
`buildOldFormatRequest` 之前依次执行（`relay/channel/task/ali/adaptor.go`）：

1. `migrateTaskImagesToMedia(model, &taskReq)`：将 `Image` / `Images` 迁移到 `Media`。
   - `firstTaskImage` / `secondTaskImage`：将 `req.Image`、`req.Images` 按此顺序拼接，
     取第一个 / 第二个非空值。
   - **仅当 `req.Media` 为空时迁移**，已有 `Media` 的请求原样跳过；迁移完成后清空
     `Image` / `Images`。
   - 当前仅支持**图片参考**，不支持视频参考与视频编辑；`input_reference` 不参与迁移，
     仍被 build*Request 拒绝。

按模型的 Media 映射：

| 模型 | firstTaskImage | secondTaskImage |
| --- | --- | --- |
| wan2.6-i2v-flash / wan2.6-i2v / wan2.5-i2v-preview / wan2.2-i2v-flash / wan2.2-i2v-plus / wan2.1-i2v-turbo / wan2.1-i2v-plus | `{Type: img_url}` | — |
| wan2.2-kf2v-flash / wan2.1-kf2v-plus | `{Type: first_frame_url}` | `{Type: last_frame_url}` |
| wan2.7-i2v / wan2.7-i2v-2026-04-25 | 图片→`{Type: first_frame}`；视频→`{Type: first_clip}` | `{Type: last_frame}` |
| happyhorse-1.1-i2v / happyhorse-1.0-i2v | `{Type: first_frame}` | — |
| wan3.0-video / wan3.0-video-prime | `{Type: first_frame}` | `{Type: last_frame}` |
| 其他模型 | 不迁移（保持原有校验行为） | |

wan2.7 系的图片/视频判断（`looksLikeVideoURL`）：优先 data URI 的 MIME
（`data:video/*` → 视频，`data:image/*` → 图片），其次 URL 后缀
（`.mp4`/`.mov`/`.webm`/`.avi`/`.mkv`/`.m4v` → 视频），默认按图片处理。

2. `migrateTaskParamsToMetadata(&taskReq)`：将 `Size` → `metadata.size`、
   `Duration`（为 0 时用 `Seconds` 兜底）→ `metadata.duration`，**Metadata 已有对应
   键时不覆盖**；转移后清空原字段。两种格式通用（旧格式读取 `size`/`duration`，
   新格式读取 `duration`）。

迁移后 build*Request 不再因 `image`/`images`/`size`/`duration`/`seconds` 遗留字段报错；
`mode` 与 `input_reference` 仍被拒绝。

## 附录：关键 DTO 结构速查

### AliUsage（`relay/channel/ali/dto.go:149-154`）
```go
type AliUsage struct {
    InputTokens  int `json:"input_tokens"`
    OutputTokens int `json:"output_tokens"`
    TotalTokens  int `json:"total_tokens"`
    ImageCount   int `json:"image_count,omitempty"`
}
```

### AliVideoUsage（`relay/channel/task/ali/adaptor.go:76-80`）
```go
type AliUsage struct {  // 注意：task/ali 有自己的 AliUsage 定义
    Duration   dto.IntValue `json:"duration,omitempty"`
    VideoCount dto.IntValue `json:"video_count,omitempty"`
    SR         dto.IntValue `json:"SR,omitempty"`
}
```

### AliError（`relay/channel/ali/dto.go:100-104`）
```go
type AliError struct {
    Code      string `json:"code"`
    Message   string `json:"message"`
    RequestId string `json:"request_id"`
}
```

### TaskInfo（`relay/common/relay_info.go`）
```go
type TaskInfo struct {
    Code             int    `json:"code"`
    TaskID           string `json:"task_id"`
    Status           string `json:"status"`
    Reason           string `json:"reason,omitempty"`
    Url              string `json:"url,omitempty"`
    RemoteUrl        string `json:"remote_url,omitempty"`
    Progress         string `json:"progress,omitempty"`
    CompletionTokens int    `json:"completion_tokens,omitempty"`
    TotalTokens      int    `json:"total_tokens,omitempty"`
}
```
