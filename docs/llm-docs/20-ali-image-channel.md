# Ali（阿里云百炼）图片渠道设计文档

## 1. 概述

本文档详细描述 `relay/channel/ali` 包中 **图片生成（Image Generation）** 和 **图片编辑（Image Edit）** 功能的完整设计。
该模块作为阿里云百炼平台（DashScope）图片 API 的适配层，将 OpenAI 兼容的图片请求转换为阿里云原生格式，
并将阿里云的响应转换回 OpenAI 标准格式。

### 核心源码文件

| 文件 | 职责 |
|------|------|
| `relay/channel/ali/image.go` | 图片请求转换、异步轮询、响应转换的核心逻辑 |
| `relay/channel/ali/image_wan.go` | Wan 系列模型（旧格式）的图片编辑特殊处理 |
| `relay/channel/ali/adaptor.go` | 适配器入口：路由分发、请求/响应编排 |
| `relay/channel/ali/dto.go` | 所有阿里云原生 DTO 结构体定义 |
| `relay/channel/ali/constants.go` | 渠道名称、模型列表常量 |
| `relay/image_handler.go` | 通用图片请求处理编排层（所有渠道共享） |
| `dto/openai_image.go` | OpenAI 标准 ImageRequest/ImageResponse DTO |
| `setting/model_setting/qwen.go` | 同步/异步图片模型判定配置 |
| `relay/constant/relay_mode.go` | RelayMode 常量定义 |
| `router/relay-router.go` | HTTP 路由注册 |

---

## 2. 支持的模型

### 2.1 同步模型（Synchronous）

同步模型通过 `/api/v1/services/aigc/multimodal-generation/generation` 端点调用，
请求使用 **messages 格式**（类似 Chat Completion），响应直接返回图片结果。

判定逻辑：`model_setting.IsSyncImageModel(model)` — 模型名包含以下任一子串即为同步模型：

| 模型名子串 | 说明 |
|------------|------|
| `z-image` | 通义万相 z-image |
| `qwen-image` | Qwen 图片生成 |
| `wan2.6` | Wan 2.6 系列 |
| `wan2.7` | Wan 2.7 系列 |
| `qwen-image-edit` | Qwen 图片编辑 |
| `qwen-image-edit-max` | Qwen 图片编辑 Max |
| `qwen-image-edit-max-2026-01-16` | Qwen 图片编辑 Max 特定版本 |
| `qwen-image-edit-plus` | Qwen 图片编辑 Plus |
| `qwen-image-edit-plus-2025-12-15` | Qwen 图片编辑 Plus 特定版本 |
| `qwen-image-edit-plus-2025-10-30` | Qwen 图片编辑 Plus 特定版本 |

> **配置来源**: `setting/model_setting/qwen.go` 中的 `QwenSettings.SyncImageModels`，
> 可通过系统配置 `qwen.sync_image_models` 动态覆盖。

### 2.2 异步模型（Asynchronous）

异步模型通过 `/api/v1/services/aigc/text2image/image-synthesis` 端点调用，
请求头需设置 `X-DashScope-Async: enable`，响应返回 `task_id`，需轮询任务状态。

异步模型为 **不在同步列表中的所有其他模型**，典型示例：
- `wanx2.1-t2i-turbo`
- `wanx2.1-t2i-plus`
- 其他通义万相早期文生图模型

### 2.3 Wan 模型特殊分类

Wan 系列模型在图片编辑场景下有特殊路由逻辑：

| 函数 | 判定规则 | 说明 |
|------|----------|------|
| `isOldWanModel(name)` | 包含 `wan` 但**不包含** `wan2.6`/`wan2.7` | 旧格式 Wan，走 `/api/v1/services/aigc/image2image/image-synthesis` |
| `isWanModel(name)` | 包含 `wan` | 新格式 Wan（2.6/2.7），走 `/api/v1/services/aigc/image-generation/generation` |

---

## 3. API 端点与路由

### 3.1 统一网关入口（OpenAI 兼容格式）

客户端通过标准 OpenAI 图片 API 格式调用：

| 路由 | 方法 | 说明 | RelayMode |
|------|------|------|-----------|
| `/v1/images/generations` | POST | 图片生成 | `RelayModeImagesGenerations` |
| `/v1/images/edits` | POST | 图片编辑 | `RelayModeImagesEdits` |
| `/v1/edits` | POST | 图片编辑（别名） | `RelayModeImagesEdits` |

> 路由定义位于 `router/relay-router.go`，所有图片请求统一进入 `controller.Relay(c, types.RelayFormatOpenAIImage)`。

### 3.2 阿里云上游真实端点

根据模型类型和操作类型，适配器会路由到不同的阿里云 DashScope API：

| 场景 | 阿里云端点 | 请求头 |
|------|-----------|--------|
| **图片生成 - 同步模型** | `{baseURL}/api/v1/services/aigc/multimodal-generation/generation` | 无特殊异步头 |
| **图片生成 - 异步模型** | `{baseURL}/api/v1/services/aigc/text2image/image-synthesis` | `X-DashScope-Async: enable` |
| **图片编辑 - 旧 Wan** | `{baseURL}/api/v1/services/aigc/image2image/image-synthesis` | 无特殊异步头 |
| **图片编辑 - 新 Wan (2.6/2.7)** | `{baseURL}/api/v1/services/aigc/image-generation/generation` | `X-DashScope-Async: enable` |
| **图片编辑 - 同步模型** | `{baseURL}/api/v1/services/aigc/multimodal-generation/generation` | 无特殊异步头 |
| **图片编辑 - 其他** | `{baseURL}/api/v1/services/aigc/multimodal-generation/generation` | 无特殊异步头 |

> `baseURL` 为渠道配置的 `ChannelBaseUrl`，通常为 `https://dashscope.aliyuncs.com`。

### 3.3 异步任务轮询端点

异步模型提交后，系统自动轮询任务状态：

```
GET {baseURL}/api/v1/tasks/{task_id}
Authorization: Bearer {apiKey}
```

轮询策略：
- 初始等待 **5 秒**
- 每次轮询间隔 **10 秒**
- 最大轮询 **20 次**（总计约 205 秒）
- 终态：`SUCCEEDED` / `FAILED` / `CANCELED` / `UNKNOWN`

---

## 4. 请求格式详解

### 4.1 客户端请求（OpenAI 兼容格式）

#### 图片生成请求

```json
{
  "model": "wanx2.1-t2i-plus",
  "prompt": "一只可爱的猫咪",
  "n": 1,
  "size": "1024x1024",
  "response_format": "url",
  "quality": "standard"
}
```

| 字段 | 类型 | 必需 | 说明 |
|------|------|------|------|
| `model` | string | 是 | 模型名称 |
| `prompt` | string | 是 | 图片描述提示词 |
| `n` | uint | 否 | 生成数量，默认 1 |
| `size` | string | 否 | 图片尺寸，如 `1024x1024`（会自动转换为 `1024*1024`） |
| `response_format` | string | 否 | 返回格式：`url` 或 `b64_json` |
| `quality` | string | 否 | 图片品质 |
| `watermark` | bool | 否 | 是否添加水印 |

#### 图片编辑请求（JSON 格式）

```json
{
  "model": "qwen-image-edit-plus",
  "prompt": "将背景改为海滩",
  "n": 1,
  "response_format": "url",
  "image": "data:image/png;base64,iVBOR..."
}
```

#### 图片编辑请求（Multipart Form 格式）

```
POST /v1/images/edits
Content-Type: multipart/form-data

model: qwen-image-edit-plus
prompt: 将背景改为海滩
image: <file binary>
```

> 支持多个图片文件，字段名可以是 `image`、`image[]` 或 `image[0]`、`image[1]`...

#### 高级参数（通过 Extra 字段传递）

`ImageRequest` 支持通过 `extra` 字段传递阿里云特有参数：

```json
{
  "model": "wanx2.1-t2i-plus",
  "prompt": "风景画",
  "parameters": {
    "size": "1024*1024",
    "n": 1,
    "steps": "30",
    "scale": "7.5",
    "watermark": false,
    "prompt_extend": true,
    "seed": 42
  },
  "input": {
    "negative_prompt": "模糊, 低质量"
  }
}
```

当请求体中存在 `parameters` 字段时，将直接使用该字段覆盖默认参数；否则从标准 OpenAI 字段中提取。

### 4.2 转换后的阿里云请求

#### 同步模型请求结构

```json
{
  "model": "qwen-image",
  "input": {
    "messages": [
      {
        "role": "user",
        "content": [
          { "text": "一只可爱的猫咪" }
        ]
      }
    ]
  },
  "parameters": {
    "size": "1024*1024",
    "n": 1
  }
}
```

#### 异步模型请求结构

```json
{
  "model": "wanx2.1-t2i-plus",
  "input": {
    "prompt": "一只可爱的猫咪"
  },
  "parameters": {
    "size": "1024*1024",
    "n": 1
  }
}
```

#### 图片编辑 - 同步模型（Multipart Form）

```json
{
  "model": "qwen-image-edit-plus",
  "input": {
    "messages": [
      {
        "role": "user",
        "content": [
          { "image": "data:image/png;base64,iVBOR..." },
          { "text": "将背景改为海滩" }
        ]
      }
    ]
  },
  "parameters": {
    "n": 1
  }
}
```

#### 图片编辑 - 旧 Wan 模型

```json
{
  "model": "wanx2.1-vace-plus",
  "input": {
    "prompt": "将背景改为海滩",
    "images": ["data:image/png;base64,iVBOR..."]
  },
  "parameters": {
    "n": 1
  }
}
```

---

## 5. 响应格式详解

### 5.1 阿里云原始响应

#### 异步任务提交响应

```json
{
  "output": {
    "task_id": "a7745175-8eb7-4386-a0c4-7abf3a4dc6a7",
    "task_status": "PENDING"
  },
  "request_id": "a7745175-8eb7-4386-a0c4-7abf3a4dc6a7"
}
```

#### 异步任务完成响应（轮询获得）

```json
{
  "output": {
    "task_id": "a7745175-8eb7-4386-a0c4-7abf3a4dc6a7",
    "task_status": "SUCCEEDED",
    "results": [
      {
        "url": "https://dashscope-result-bj.oss-cn-beijing.aliyuncs.com/xxx/image.png",
        "b64_image": ""
      }
    ]
  },
  "usage": {
    "image_count": 1
  },
  "request_id": "a7745175-8eb7-4386-a0c4-7abf3a4dc6a7"
}
```

#### 同步模型响应（choices 格式）

```json
{
  "output": {
    "choices": [
      {
        "finish_reason": "stop",
        "message": {
          "role": "assistant",
          "content": [
            {
              "image": "https://dashscope-result-bj.oss-cn-beijing.aliyuncs.com/xxx/image.png"
            }
          ]
        }
      }
    ]
  },
  "usage": {
    "input_tokens": 15,
    "output_tokens": 0,
    "image_count": 1
  },
  "request_id": "xxx"
}
```

#### 任务状态值

| 状态 | 说明 |
|------|------|
| `PENDING` | 排队中 |
| `RUNNING` | 执行中 |
| `SUCCEEDED` | 成功 |
| `FAILED` | 失败 |
| `CANCELED` | 已取消 |
| `UNKNOWN` | 未知状态 |

### 5.2 转换后的 OpenAI 兼容响应

```json
{
  "created": 1719216000,
  "data": [
    {
      "url": "https://dashscope-result-bj.oss-cn-beijing.aliyuncs.com/xxx/image.png",
      "b64_json": "",
      "revised_prompt": ""
    }
  ],
  "metadata": { ... }
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `created` | int64 | 请求创建时间（Unix 时间戳） |
| `data` | array | 图片数据数组 |
| `data[].url` | string | 图片 URL（当 response_format 为 url 时） |
| `data[].b64_json` | string | Base64 编码图片（当 response_format 为 b64_json 时） |
| `data[].revised_prompt` | string | 修订后的提示词（部分模型返回） |
| `metadata` | object | 阿里云原始完整响应（用于调试） |

#### response_format 处理逻辑

- `url` 模式：直接使用阿里云返回的 URL
- `b64_json` 模式：
  - **异步模型**（results 格式）：优先使用 `b64_image` 字段，若为空则通过 `service.GetImageFromUrl(url)` 下载后转 base64
  - **同步模型**（choices 格式）：若返回的是 HTTP URL，则下载后转 base64；若已是 base64 则直接使用

---

## 6. 请求处理流程

### 6.1 图片生成流程

```
客户端请求 POST /v1/images/generations
        │
        ▼
  controller.Relay(c, RelayFormatOpenAIImage)
        │
        ▼
  relay.ImageHelper(c, info)
        │
        ├── 解析 ImageRequest
        ├── ModelMappedHelper（模型名映射）
        ├── GetAdaptor(apiType) → ali.Adaptor
        │
        ▼
  adaptor.ConvertImageRequest()
        │
        ├── isSyncImageModel? → 设置 a.IsSyncImageModel = true
        ├── oaiImage2AliImageRequest()
        │   ├── 检查 Extra.parameters → 直接使用或从标准字段提取
        │   ├── z-image + prompt_extend → 2x 计费倍率
        │   ├── n > 1 → 记录 n 倍率
        │   └── isSync? → messages 格式 : prompt 格式
        │
        ▼
  adaptor.DoRequest() → channel.DoApiRequest()
        │
        ├── GetRequestURL() → 根据 isSync 选择端点
        ├── SetupRequestHeader()
        │   ├── Authorization: Bearer {apiKey}
        │   ├── X-DashScope-Async: enable（仅异步）
        │   └── X-DashScope-Plugin: {plugin}（可选）
        └── HTTP POST 请求
        │
        ▼
  adaptor.DoResponse() → aliImageHandler()
        │
        ├── 解析阿里云响应
        ├── 检查错误（message 字段）
        │
        ├── [同步模型] 直接使用响应
        │
        ├── [异步模型] asyncTaskWait()
        │   ├── 等待 5 秒
        │   ├── 轮询 GET /api/v1/tasks/{task_id}
        │   ├── 检查 task_status
        │   └── 最多 20 次，间隔 10 秒
        │
        ▼
  responseAli2OpenAIImage()
        │
        ├── results 格式 → ResultToOpenAIImageDate()
        └── choices 格式 → ChoicesToOpenAIImageDate()
        │
        ▼
  返回 OpenAI 兼容 JSON 响应
```

### 6.2 图片编辑流程

```
客户端请求 POST /v1/images/edits
        │
        ▼
  adaptor.ConvertImageRequest() [RelayMode = ImagesEdits]
        │
        ├── isOldWanModel? → oaiFormEdit2WanxImageEdit()
        │   └── 旧 Wan 专用：images 数组 + prompt
        │
        ├── isWanModel (2.6/2.7)?
        │   └── 设置 X-DashScope-Async: enable
        │
        ├── Content-Type = multipart/form-data?
        │   ├── 是 → oaiFormEdit2AliImageEdit()
        │   │   ├── getImageBase64sFromForm() 解析图片文件
        │   │   ├── 转换为 data:image/xxx;base64,... 格式
        │   │   └── 构建 messages 格式请求
        │   │
        │   └── 否 → oaiImage2AliImageRequest()（JSON body）
        │
        ▼
  后续流程同图片生成
```

---

## 7. 计费机制

### 7.1 计费参数

图片计费基于以下参数组合：

| 参数 | 来源 | 说明 |
|------|------|------|
| `n` | `AliImageParameters.N` 或响应中的 `image_count` | 实际生成图片数量 |
| `prompt_extend` | `AliImageParameters.PromptExtend` | z-image 模型开启后 2 倍计费 |
| 模型价格 | 系统模型配置 | 每张图片基础价格 |

### 7.2 计费倍率记录

```go
// z-image 模型 prompt_extend 开启 → 2 倍
info.PriceData.AddOtherRatio("prompt_extend", 2)

// 图片数量（请求时预估）
info.PriceData.AddOtherRatio("n", float64(parameters.N))

// 图片数量（响应后校正）
info.PriceData.AddOtherRatio("n", float64(response.Usage.ImageCount))
```

### 7.3 Token 计数

系统将图片请求视为 1 token 的文本消费（`TotalTokens = 1, PromptTokens = 1`），
实际费用通过 `PriceData` 的 `OtherRatios` 机制计算。

---

## 8. DTO 结构体参考

### 8.1 请求结构体

```go
// AliImageRequest - 阿里云图片请求
type AliImageRequest struct {
    Model          string             `json:"model"`
    Input          any                `json:"input"`      // AliImageInput 或 WanImageInput
    Parameters     AliImageParameters `json:"parameters,omitempty"`
    ResponseFormat string             `json:"response_format,omitempty"`
}

// AliImageInput - 标准图片输入
type AliImageInput struct {
    Prompt         string       `json:"prompt,omitempty"`
    NegativePrompt string       `json:"negative_prompt,omitempty"`
    Messages       []AliMessage `json:"messages,omitempty"`
}

// AliImageParameters - 图片参数
type AliImageParameters struct {
    Size             string `json:"size,omitempty"`          // 如 "1024*1024"
    N                int    `json:"n,omitempty"`             // 生成数量
    Steps            string `json:"steps,omitempty"`         // 推理步数
    Scale            string `json:"scale,omitempty"`         // 引导系数
    Watermark        *bool  `json:"watermark,omitempty"`     // 水印
    PromptExtend     *bool  `json:"prompt_extend,omitempty"` // 提示词扩展
    ThinkingMode     *bool  `json:"thinking_mode,omitempty"`
    EnableSequential *bool  `json:"enable_sequential,omitempty"`
    BboxList         any    `json:"bbox_list,omitempty"`
    ColorPalette     any    `json:"color_palette,omitempty"`
    Seed             *int   `json:"seed,omitempty"`          // 随机种子
}

// WanImageInput - Wan 旧格式图片编辑输入
type WanImageInput struct {
    Prompt         string   `json:"prompt"`
    Images         []string `json:"images"`                  // 图片 URL 或 base64 数组
    NegativePrompt string   `json:"negative_prompt,omitempty"`
}

// WanImageParameters - Wan 模型参数
type WanImageParameters struct {
    N         int     `json:"n,omitempty"`
    Watermark *bool   `json:"watermark,omitempty"`
    Seed      int     `json:"seed,omitempty"`
    Strength  float64 `json:"strength,omitempty"`  // 修改幅度 0.0-1.0
}
```

### 8.2 响应结构体

```go
// AliResponse - 阿里云通用响应
type AliResponse struct {
    Output  AliOutput `json:"output"`
    Usage   AliUsage  `json:"usage"`
    AliError
}

// AliOutput - 响应输出
type AliOutput struct {
    TaskId       string       `json:"task_id,omitempty"`
    TaskStatus   string       `json:"task_status,omitempty"`
    Results      []TaskResult `json:"results,omitempty"`   // 异步模型结果
    Choices      []Choice     `json:"choices,omitempty"`   // 同步模型结果
    Message      string       `json:"message,omitempty"`
    Code         string       `json:"code,omitempty"`
    // ... 其他字段
}

// TaskResult - 异步任务结果
type TaskResult struct {
    B64Image string `json:"b64_image,omitempty"`
    Url      string `json:"url,omitempty"`
    Code     string `json:"code,omitempty"`
    Message  string `json:"message,omitempty"`
}

// AliUsage - 用量统计
type AliUsage struct {
    InputTokens  int `json:"input_tokens"`
    OutputTokens int `json:"output_tokens"`
    TotalTokens  int `json:"total_tokens"`
    ImageCount   int `json:"image_count,omitempty"`
}
```

---

## 9. 上下游代码依赖图

```
┌─────────────────────────────────────────────────────────┐
│                     客户端 (OpenAI 格式)                   │
│   POST /v1/images/generations                           │
│   POST /v1/images/edits                                 │
└──────────────────────┬──────────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────────┐
│  router/relay-router.go                                 │
│  → controller.Relay(c, RelayFormatOpenAIImage)          │
└──────────────────────┬──────────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────────┐
│  controller/relay.go                                    │
│  → relay.ImageHelper(c, info)                           │
└──────────────────────┬──────────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────────┐
│  relay/image_handler.go                                 │
│  → 解析 ImageRequest                                    │
│  → ModelMappedHelper (模型映射)                          │
│  → adaptor.ConvertImageRequest()                        │
│  → adaptor.DoRequest()                                  │
│  → adaptor.DoResponse()                                 │
│  → PostTextConsumeQuota (计费)                          │
└──────────────────────┬──────────────────────────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────────┐
│  relay/channel/ali/adaptor.go                           │
│  → GetRequestURL()     选择阿里云端点                    │
│  → SetupRequestHeader() 设置认证和异步头                  │
│  → ConvertImageRequest() 转换请求格式                    │
│  → DoRequest()         发送 HTTP 请求                    │
│  → DoResponse()        → aliImageHandler()              │
└──────────────────────┬──────────────────────────────────┘
                       │
          ┌────────────┼────────────┐
          ▼            ▼            ▼
┌──────────────┐ ┌──────────┐ ┌──────────────┐
│  image.go    │ │image_wan │ │   dto.go     │
│  请求转换     │ │.go       │ │  结构体定义   │
│  异步轮询     │ │ Wan 旧版  │ │  响应转换     │
│  响应转换     │ │ 图片编辑  │ │              │
└──────────────┘ └──────────┘ └──────────────┘
                       │
                       ▼
┌─────────────────────────────────────────────────────────┐
│  阿里云 DashScope API                                   │
│  https://dashscope.aliyuncs.com                         │
└─────────────────────────────────────────────────────────┘
```

### 关键配置依赖

| 配置项 | 文件 | 说明 |
|--------|------|------|
| 同步图片模型列表 | `setting/model_setting/qwen.go` | `QwenSettings.SyncImageModels` |
| 渠道模型列表 | `relay/channel/ali/constants.go` | `ModelList`（基础模型，不含图片模型） |
| API 类型映射 | `constant/api_type.go` | `APITypeAli` |
| RelayMode 映射 | `relay/constant/relay_mode.go` | `Path2RelayMode()` |

---

## 10. 使用指南

### 10.1 前置条件

1. 在系统管理后台创建 **Ali 渠道**，API 类型选择 `ali`
2. 配置渠道的 **Base URL**（默认 `https://dashscope.aliyuncs.com`）
3. 配置渠道的 **API Key**（阿里云百炼平台的 API Key）
4. 在渠道模型列表中添加需要使用的图片模型名称
5. 在系统模型配置中设置模型的 **价格** 或 **倍率**

### 10.2 调用示例

#### cURL - 图片生成

```bash
curl -X POST https://your-gateway/v1/images/generations \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "wanx2.1-t2i-plus",
    "prompt": "夕阳下的海边城市，赛博朋克风格",
    "n": 2,
    "size": "1024x1024",
    "response_format": "url"
  }'
```

#### cURL - 图片编辑（JSON）

```bash
curl -X POST https://your-gateway/v1/images/edits \
  -H "Authorization: Bearer sk-xxx" \
  -H "Content-Type: application/json" \
  -d '{
    "model": "qwen-image-edit-plus",
    "prompt": "将天空改为星空",
    "image": "data:image/png;base64,iVBOR...",
    "n": 1,
    "response_format": "url"
  }'
```

#### cURL - 图片编辑（Form）

```bash
curl -X POST https://your-gateway/v1/images/edits \
  -H "Authorization: Bearer sk-xxx" \
  -F "model=qwen-image-edit-plus" \
  -F "prompt=将天空改为星空" \
  -F "image=@/path/to/image.png"
```

#### Python - 使用 OpenAI SDK

```python
from openai import OpenAI

client = OpenAI(
    api_key="sk-xxx",
    base_url="https://your-gateway/v1"
)

# 图片生成
response = client.images.generate(
    model="wanx2.1-t2i-plus",
    prompt="一只穿着宇航服的猫",
    n=1,
    size="1024x1024",
    response_format="url"
)
print(response.data[0].url)

# 图片编辑
response = client.images.edit(
    model="qwen-image-edit-plus",
    prompt="添加彩虹",
    image=open("input.png", "rb"),
    n=1
)
print(response.data[0].url)
```

### 10.3 传递阿里云特有参数

通过请求体中的 `parameters` 和 `input` 字段（会被解析到 `Extra`）：

```json
{
  "model": "wanx2.1-t2i-plus",
  "prompt": "风景画",
  "parameters": {
    "size": "1024*1024",
    "n": 2,
    "steps": "30",
    "scale": "7.5",
    "prompt_extend": true,
    "seed": 42,
    "watermark": false
  },
  "input": {
    "negative_prompt": "模糊, 低质量, 变形"
  }
}
```

---

## 11. 错误处理

### 11.1 错误响应格式

```json
{
  "error": {
    "message": "错误描述",
    "type": "ali_error",
    "param": "",
    "code": "InvalidParameter"
  }
}
```

### 11.2 常见错误场景

| 场景 | 错误处理 |
|------|----------|
| 阿里云返回 `message` 非空 | 直接作为错误返回 |
| 异步任务状态为 `FAILED`/`CANCELED` | 返回 `ali_error` 类型错误 |
| 异步轮询超时（200+ 秒） | 返回 "aliAsyncTaskWait timeout" |
| 图片文件解析失败 | 返回 "image is required" |
| 请求格式转换失败 | 返回对应的 wrap 错误 |

---

## 12. 开发扩展指南

### 12.1 新增同步图片模型

1. 在 `setting/model_setting/qwen.go` 的 `defaultQwenSettings.SyncImageModels` 中添加模型名子串
2. 确保模型使用 messages 格式兼容 `/api/v1/services/aigc/multimodal-generation/generation`
3. 在渠道配置中添加模型名称

### 12.2 新增异步图片模型

1. 不需要修改代码（默认走异步路径）
2. 在渠道配置中添加模型名称
3. 确保模型兼容 `/api/v1/services/aigc/text2image/image-synthesis`

### 12.3 新增 Wan 系列模型

1. 在 `image_wan.go` 中的 `isOldWanModel()` 或 `isWanModel()` 函数中确认判定逻辑
2. 如需特殊请求格式，在 `image_wan.go` 中添加新的转换函数
3. 在 `adaptor.go` 的 `ConvertImageRequest()` 中添加路由分支

### 12.4 修改阿里云端点

修改 `adaptor.go` 中的 `GetRequestURL()` 函数，按 `RelayMode` 和模型类型选择端点。

### 12.5 添加新的计费参数

在 `image.go` 的 `oaiImage2AliImageRequest()` 或 `aliImageHandler()` 中使用
`info.PriceData.AddOtherRatio(key, value)` 记录计费倍率。