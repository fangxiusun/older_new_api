# `/api/perf-metrics` 接口字段说明

> 调查日期：2026-08-12  
> 覆盖范围：当前代码中 **仅有 2 个 HTTP 接口**，同属 `/api/perf-metrics` 路由组。  
> 认证说明另见：`docs/llm-docs/29-perf-metrics-summary-auth.md`

---

## 1. 接口清单

| # | 方法 | 路径 | Handler | 用途 |
| --- | --- | --- | --- | --- |
| 1 | `GET` | `/api/perf-metrics/summary` | `controller.GetPerfMetricsSummary` | 全模型性能摘要（列表/卡片用） |
| 2 | `GET` | `/api/perf-metrics` | `controller.GetPerfMetrics` | 单模型按分组的时序明细 |

路由定义：`router/api-router.go`

```go
perfMetricsRoute := apiRouter.Group("/perf-metrics")
perfMetricsRoute.Use(middleware.HeaderNavModulePublicOrUserAuth("pricing"))
{
    perfMetricsRoute.GET("/summary", controller.GetPerfMetricsSummary)
    perfMetricsRoute.GET("", controller.GetPerfMetrics)
}
```

**没有** 其他 `/api/perf-metrics/*` 子路径（无 create/update/delete/admin 接口）。  
写入路径是内部采集：`pkg/perf_metrics.Record` / `RecordRelaySample`，经 flush 写入 `perf_metrics` 表。

---

## 2. 通用约定

### 2.1 认证

两组接口共用：

```text
middleware.HeaderNavModulePublicOrUserAuth("pricing")
```

| 条件 | 行为 |
| --- | --- |
| `HeaderNavModules.pricing.enabled=true` 且 `requireAuth=false`（默认） | 可匿名；有凭证则尝试解析 |
| `requireAuth=true` 或 `enabled=false` | 必须登录，否则 401 |

### 2.2 响应外壳

成功：

```json
{
  "success": true,
  "data": { }
}
```

失败：

```json
{
  "success": false,
  "message": "错误信息"
}
```

| HTTP 状态 | 场景 |
| --- | --- |
| 200 | 成功 |
| 400 | 仅 detail：缺少 `model` |
| 401 | 认证失败（由 HeaderNav 中间件决定） |
| 500 | 查询 DB / 聚合失败，`message` 为 `err.Error()` |

### 2.3 时间窗口 `hours`

两接口都接受 query `hours`：

| 规则 | 行为 |
| --- | --- |
| 未传 / 空 | 默认 `24` |
| 非数字 | 忽略，仍用默认 `24`（controller 解析失败不报错） |
| `<= 0`（进入 service 后） | 归一为 `24` |
| `> 24*30`（720） | 截断为 `720`（最多约 30 天） |

时间范围：

```text
endTs   = now.Unix()
startTs = endTs - hours * 3600
```

查询包含：

1. DB 表 `perf_metrics` 中 `bucket_ts ∈ [startTs, endTs]` 的历史桶  
2. 内存 `hotBuckets` 中尚未 flush 的热数据  

### 2.4 指标计算公式

内部累计字段（不直接返回原始计数，除 summary 排序用到 `request_count` 且 JSON 忽略）：

| 内部字段 | 含义 |
| --- | --- |
| `request_count` | 请求次数 |
| `success_count` | 成功次数 |
| `total_latency_ms` | 总延迟（ms）累加 |
| `ttft_sum_ms` / `ttft_count` | 首 token 时延累加 / 样本数（仅流式且已发出响应） |
| `output_tokens` | 输出 token 累加 |
| `generation_ms` | 生成耗时累加（流式多为 FirstResponse→结束；否则≈总延迟） |

对外派生指标：

| 字段 | 公式 | 备注 |
| --- | --- | --- |
| `avg_latency_ms` | `total_latency_ms / request_count` | 整数除法截断；count≤0 时为 0 |
| `avg_ttft_ms` | `ttft_sum_ms / ttft_count` | 仅 detail 有；count≤0 时为 0 |
| `success_rate` | `success_count / request_count * 100` | 百分比 0–100 |
| `avg_tps` | `output_tokens / (generation_ms / 1000)` | tokens/秒；分母或 token≤0 时为 0 |

Summary 中 `success_rate` / `avg_tps` 会 `Round(x*100)/100`（保留 2 位小数）。  
Detail 中 `success_rate` / `avg_tps` **不做**该四舍五入（原样 float64）。

### 2.5 时间桶 `bucket_ts` / `ts`

由 `perf_metrics_setting.bucket_time` 决定桶宽：

| 配置值 | 秒数 |
| --- | --- |
| `minute` | 60 |
| `5min` | 300 |
| `hour`（默认） | 3600 |
| 其他 | 3600 |

```text
bucket_ts = ts - (ts % bucketSeconds)
```

### 2.6 分组过滤

| 接口 | 分组策略 |
| --- | --- |
| summary | 仅统计当前 `ratio_setting` 中存在的 group，外加固定包含 `"auto"` |
| detail | 返回后过滤：仅保留仍在 `ratio_setting` 中的 group，或 `group == "auto"` |

空 group 在采集时会被写成 `"default"`。

### 2.7 采集开关

`perf_metrics_setting.enabled=false` 时：

- 不再 `Record` 新样本  
- **接口仍可调用**；可能只读到历史 DB 数据或空结果  

这与鉴权无关。

---

## 3. `GET /api/perf-metrics/summary`

### 3.1 基本信息

| 项 | 值 |
| --- | --- |
| 完整路径 | `/api/perf-metrics/summary` |
| 方法 | `GET` |
| Handler | `controller/perf_metrics.go:GetPerfMetricsSummary` |
| 聚合实现 | `pkg/perf_metrics.QuerySummaryAll` |
| 前端封装 | `web/src/features/performance-metrics/api.ts:getPerfMetricsSummary` |
| 典型调用方 | 定价卡片、Dashboard 性能概览 |

### 3.2 请求参数（Query）

| 参数 | 类型 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- | --- |
| `hours` | int（字符串解析） | 否 | `24` | 回看小时数；非法值忽略；service 层限制 `1..720` 语义（`<=0→24`，`>720→720`） |

**无** `model` / `group` 参数。  
服务端自动：

```go
activeGroups := append(lo.Keys(ratio_setting.GetGroupRatioCopy()), "auto")
result, err := perfmetrics.QuerySummaryAll(hours, activeGroups)
```

### 3.3 成功响应结构

```json
{
  "success": true,
  "data": {
    "models": [
      {
        "model_name": "gpt-4o",
        "avg_latency_ms": 1250,
        "success_rate": 98.5,
        "avg_tps": 42.33,
        "recent_success_rates": [100, 97.5, 98.2]
      }
    ]
  }
}
```

#### 顶层

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `success` | boolean | 固定 `true` |
| `data` | object | `SummaryAllResult` |
| `data.models` | array | 模型摘要列表；无数据时为 `[]` |

#### `data.models[]`（`ModelSummary`）

| JSON 字段 | Go 类型 | 是否返回 | 说明 |
| --- | --- | --- | --- |
| `model_name` | string | 是 | 原始模型名（`OriginModelName`） |
| `avg_latency_ms` | int64 | 是 | 窗口内平均延迟（ms） |
| `success_rate` | float64 | 是 | 成功率百分比，保留 2 位小数 |
| `avg_tps` | float64 | 是 | 平均 tokens/s，保留 2 位小数 |
| `recent_success_rates` | []float64 | 有则返回 | 最近最多 **3 个**时间桶的成功率（升序时间）；`omitempty`，无桶时省略 |
| `request_count` | int64 | **否** | Go tag `json:"-"`，仅用于服务端按请求量降序排序，**不会出现在 JSON** |

排序规则：按内部 `RequestCount` **降序**（请求越多越靠前）。

#### `recent_success_rates` 细节

- 来源：按 `model_name + bucket_ts` 聚合后的桶  
- 取时间戳最大的最多 3 个桶，再按时间升序输出  
- 每个值：`Round(success_rate*100)/100`  
- 用途：前端画迷你趋势 / 健康度点阵

### 3.4 错误响应示例

```json
{
  "success": false,
  "message": "database error detail..."
}
```

HTTP 500。

### 3.5 调用示例

```powershell
# 匿名（pricing 公开时）
curl.exe -s "http://127.0.0.1:3000/api/perf-metrics/summary?hours=24"

# 带登录
curl.exe -s -H "Authorization: Bearer <token>" "http://127.0.0.1:3000/api/perf-metrics/summary?hours=72"
```

---

## 4. `GET /api/perf-metrics`

### 4.1 基本信息

| 项 | 值 |
| --- | --- |
| 完整路径 | `/api/perf-metrics` |
| 方法 | `GET` |
| Handler | `controller/perf_metrics.go:GetPerfMetrics` |
| 聚合实现 | `pkg/perf_metrics.Query` |
| 前端封装 | `web/src/features/performance-metrics/api.ts:getPerfMetrics` |
| 典型调用方 | 定价模型详情页性能图 |

### 4.2 请求参数（Query）

| 参数 | 类型 | 必填 | 默认 | 说明 |
| --- | --- | --- | --- | --- |
| `model` | string | **是** | — | 模型名；空则 400：`model is required` |
| `hours` | int | 否 | `24` | 同 summary 的 hours 规则 |
| `group` | string | 否 | 空=全部 | 若非空，只查该用户组；空则返回该模型下所有组，再经 active group 过滤 |

前端当前默认只传 `model` + `hours`，不传 `group`。

### 4.3 成功响应结构

```json
{
  "success": true,
  "data": {
    "model_name": "gpt-4o",
    "series_schema": "dbcd0a3c01b55203",
    "groups": [
      {
        "group": "default",
        "avg_ttft_ms": 320,
        "avg_latency_ms": 1180,
        "success_rate": 99.1,
        "avg_tps": 38.7,
        "series": [
          {
            "ts": 1723334400,
            "avg_ttft_ms": 300,
            "avg_latency_ms": 1100,
            "success_rate": 100,
            "avg_tps": 40.2
          }
        ]
      }
    ]
  }
}
```

#### 顶层 `data`（`QueryResult`）

| JSON 字段 | 类型 | 说明 |
| --- | --- | --- |
| `model_name` | string | 请求的模型名（原样回传） |
| `series_schema` | string | 时序 schema 稳定标记，当前固定 `"dbcd0a3c01b55203"`；用于客户端缓存/兼容，**勿因隐藏字段随意改** |
| `groups` | array | 按 group 名 **字典序** 排序；无数据时为 `[]` |

#### `data.groups[]`（`GroupResult`）

整段窗口内该 group 的汇总 + 时序：

| JSON 字段 | 类型 | 说明 |
| --- | --- | --- |
| `group` | string | 用户组名（如 `default` / `vip` / `auto`） |
| `avg_ttft_ms` | int64 | 窗口平均 TTFT（ms） |
| `avg_latency_ms` | int64 | 窗口平均总延迟（ms） |
| `success_rate` | float64 | 窗口成功率（%），**不强制两位小数** |
| `avg_tps` | float64 | 窗口平均 tokens/s |
| `series` | array | 按 `ts` **升序** 的时间桶点 |

#### `data.groups[].series[]`（`BucketPoint`）

| JSON 字段 | 类型 | 说明 |
| --- | --- | --- |
| `ts` | int64 | 桶起始 Unix 秒（`bucket_ts`） |
| `avg_ttft_ms` | int64 | 该桶平均 TTFT |
| `avg_latency_ms` | int64 | 该桶平均延迟 |
| `success_rate` | float64 | 该桶成功率（%） |
| `avg_tps` | float64 | 该桶平均 tokens/s |

### 4.4 错误响应

缺少 model：

```json
HTTP/1.1 400 Bad Request
{
  "success": false,
  "message": "model is required"
}
```

查询失败：

```json
HTTP/1.1 500 Internal Server Error
{
  "success": false,
  "message": "..."
}
```

### 4.5 调用示例

```powershell
# 某模型 24h 全部分组
curl.exe -s "http://127.0.0.1:3000/api/perf-metrics?model=gpt-4o&hours=24"

# 仅 default 组、7 天
curl.exe -s "http://127.0.0.1:3000/api/perf-metrics?model=gpt-4o&group=default&hours=168"
```

---

## 5. 两接口字段对照

| 维度 | summary | detail |
| --- | --- | --- |
| 范围 | 全部模型 | 单一模型 |
| 是否按 group 拆分 | 否（跨 group 汇总） | 是 |
| 是否返回时序 | 仅 `recent_success_rates`（最多 3 点成功率） | 完整 `series[]` |
| `avg_ttft_ms` | 无 | 有 |
| `series_schema` | 无 | 有 |
| `request_count` | 内部排序用，JSON 不返回 | 不返回 |
| `success_rate`/`avg_tps` 精度 | 四舍五入 2 位 | 原始 float |
| 必填参数 | 无 | `model` |

---

## 6. 数据从哪来

```text
Relay 完成
  → pkg/perf_metrics.RecordRelaySample / Record
  → 内存 hotBuckets（+ 可选 Redis）
  → flushLoop 周期 Upsert 到表 perf_metrics
  → API Query / QuerySummaryAll 合并 DB + hotBuckets 后返回
```

表结构（`model.PerfMetric`，表名 `perf_metrics`）：

| 列 | 说明 |
| --- | --- |
| `id` | 主键 |
| `model_name` | 模型名 |
| `group` | 用户组 |
| `bucket_ts` | 桶起始时间 |
| `request_count` | 请求数 |
| `success_count` | 成功数 |
| `total_latency_ms` | 延迟累加 |
| `ttft_sum_ms` / `ttft_count` | TTFT 累加 |
| `output_tokens` / `generation_ms` | TPS 计算原料 |

唯一冲突键：`(model_name, group, bucket_ts)`，flush 时累加更新。

相关设置 `perf_metrics_setting`（**不改变接口路径/字段，只影响采集**）：

| 配置 | 默认 | 作用 |
| --- | --- | --- |
| `enabled` | true | 是否采集 |
| `flush_interval` | 5（分钟） | 刷盘周期 |
| `bucket_time` | `hour` | 桶粒度 |
| `retention_days` | 0 | 历史保留；0 表示不按天清理或另有逻辑 |

---

## 7. 前端类型映射

文件：`web/src/features/performance-metrics/types.ts`

| 后端 | 前端 type |
| --- | --- |
| summary 响应 | `PerfSummaryAllData` / `PerfModelSummary` |
| detail 响应 | `PerformanceMetricsData` / `PerformanceGroup` / `PerformanceSeriesPoint` |

注意：前端类型里 `request_count?` 可选，但后端 summary **实际不序列化**该字段。

---

## 8. 代码索引

| 文件 | 职责 |
| --- | --- |
| `router/api-router.go` | 路由与认证中间件 |
| `controller/perf_metrics.go` | 参数解析、响应包装、active group 过滤 |
| `pkg/perf_metrics/types.go` | 请求/响应 DTO |
| `pkg/perf_metrics/metrics.go` | 采集、查询、聚合、公式 |
| `pkg/perf_metrics/flush.go` | 周期刷盘与清理 |
| `model/perf_metric.go` | 表模型与 SQL 聚合 |
| `setting/perf_metrics_setting/config.go` | 采集配置 |
| `web/src/features/performance-metrics/api.ts` | 前端调用 |
| `web/src/features/performance-metrics/types.ts` | 前端类型 |

---

## 9. 一句话总结

- 系列接口只有 **2 个 GET**：`/summary`（全模型摘要）与 `/`（单模型分组时序）。  
- 公共参数主要是 **`hours`**；detail 额外要 **`model`**，可选 **`group`**。  
- 返回的核心对外指标是 **`avg_latency_ms` / `success_rate` / `avg_tps`**，detail 另有 **`avg_ttft_ms` + `series`**。  
- 认证由 **`HeaderNavModules.pricing`** 控制，与 `perf_metrics_setting` 无关。
