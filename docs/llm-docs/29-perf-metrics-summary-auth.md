# `/api/perf-metrics/summary` 认证与跳过条件调查

> 调查日期：2026-08-12  
> 结论摘要：该接口**不是固定强制登录**，也**不是固定公开**；是否需要认证取决于系统选项 `HeaderNavModules` 中 **`pricing` 模块**的 `enabled` / `requireAuth`。同一套程序在不同部署上表现不同，几乎总是因为两边的 `HeaderNavModules.pricing` 配置不同。

---

## 1. 接口与路由

| 项 | 值 |
| --- | --- |
| 方法 / 路径 | `GET /api/perf-metrics/summary` |
| 查询参数 | `hours`（可选，默认 `24`） |
| Handler | `controller.GetPerfMetricsSummary` |
| 路由定义 | `router/api-router.go` |
| 认证中间件 | `middleware.HeaderNavModulePublicOrUserAuth("pricing")` |

相关路由代码：

```go
perfMetricsRoute := apiRouter.Group("/perf-metrics")
perfMetricsRoute.Use(middleware.HeaderNavModulePublicOrUserAuth("pricing"))
{
    perfMetricsRoute.GET("/summary", controller.GetPerfMetricsSummary)
    perfMetricsRoute.GET("", controller.GetPerfMetrics)
}
```

同组接口：

- `GET /api/perf-metrics/summary`
- `GET /api/perf-metrics`

二者共用同一认证策略，**没有单独的 perf-metrics 开关**。

---

## 2. 认证决策逻辑

中间件实现：`middleware/header_nav.go` 的 `HeaderNavModulePublicOrUserAuth`。

```go
func HeaderNavModulePublicOrUserAuth(module string) gin.HandlerFunc {
    return func(c *gin.Context) {
        access := getHeaderNavAccess(module)
        if !access.Enabled || access.RequireAuth {
            UserAuth()(c)   // 必须登录
            return
        }
        TryUserAuth()(c)    // 可匿名；有凭证则尝试解析
    }
}
```

对 `module = "pricing"` 的含义：

| `pricing.enabled` | `pricing.requireAuth` | 未登录访问结果 | 实际走的中间件 |
| --- | --- | --- | --- |
| `true` | `false` | **允许**（公开） | `TryUserAuth` |
| `true` | `true` | **401 Unauthorized** | `UserAuth` |
| `false` | `false` / `true` | **401 Unauthorized**（模块关闭也要求登录） | `UserAuth` |
| 配置为空 / 缺失 | — | **允许**（默认公开） | `TryUserAuth` |

默认值（代码 fallback）：

```go
fallback := headerNavAccess{
    Enabled:     true,
    RequireAuth: false,
}
```

因此：

- **可以跳过认证**的条件只有一种：`HeaderNavModules.pricing.enabled == true` 且 `requireAuth == false`（或未配置，走默认）。
- 一旦 `requireAuth == true`，或 `enabled == false`，未登录必 401。
- 注意：这里的“模块关闭”对 perf-metrics **不是返回 403**，而是**强制改成 UserAuth**。这与定价页接口 `HeaderNavModuleAuth("pricing")` 不同（后者关闭时返回 403）。

### 与 `/api/pricing` 的差异

| 接口 | 中间件 | 模块关闭时 | `requireAuth=true` 时 |
| --- | --- | --- | --- |
| `GET /api/pricing` | `HeaderNavModuleAuth("pricing")` | 403 `pricing is disabled` | 必须登录 |
| `GET /api/perf-metrics/*` | `HeaderNavModulePublicOrUserAuth("pricing")` | 必须登录（401） | 必须登录 |
| `GET /api/rankings` | `HeaderNavModuleAuth("rankings")` | 403 | 必须登录 |

perf-metrics 故意绑定 **pricing** 模块，因为前端定价页会拉取性能摘要展示。

---

## 3. 配置项：真正控制开关的是什么

### 3.1 配置 Key

- **Option 键名**：`HeaderNavModules`
- **存储位置**：系统 options（运行时 `common.OptionMap["HeaderNavModules"]`）
- **对外可见**：`GET /api/status` 响应字段 `HeaderNavModules`
- **后台编辑入口**：系统设置 → 站点/维护相关「顶栏导航 / Header Navigation」  
  前端组件：`web/src/features/system-settings/maintenance/header-navigation-section.tsx`  
  保存时调用 `updateOption({ key: 'HeaderNavModules', value: serializedJson })`

### 3.2 JSON 结构（与认证相关的部分）

```json
{
  "home": true,
  "console": true,
  "pricing": {
    "enabled": true,
    "requireAuth": false
  },
  "rankings": {
    "enabled": true,
    "requireAuth": false
  },
  "docs": true,
  "about": true
}
```

与本接口直接相关的字段：

| 字段 | 作用 |
| --- | --- |
| `pricing.enabled` | `false` 时，perf-metrics 未登录不可访问 |
| `pricing.requireAuth` | `true` 时，perf-metrics 必须登录 |

**不是**以下配置：

- `perf_metrics_setting.enabled`：只控制是否采集/落库性能指标，**不控制接口鉴权**
- `perf_metrics_setting.flush_interval` / `bucket_time` / `retention_days`：采集参数
- 环境变量、反向代理、JWT 全局开关：本路由本身没有额外 env 旁路

### 3.3 兼容旧格式

`HeaderNavModules.pricing` 支持旧版布尔值：

```json
{ "pricing": false }
```

等价于 `enabled=false`，对 perf-metrics 会强制 `UserAuth`（未登录 401）。

对象格式优先：

```json
{ "pricing": { "enabled": true, "requireAuth": true } }
```

---

## 4. 为什么“同一程序，一个部署要登录，另一个不要”

最常见原因：

1. **部署 A**：`HeaderNavModules` 为空，或 `pricing.requireAuth=false` → 匿名可访问 summary  
2. **部署 B**：管理后台把「模型定价」设成「需要登录」，即 `pricing.requireAuth=true` → 匿名 401  
3. 或部署 B 关闭了定价模块：`pricing.enabled=false` → 同样匿名 401  

次要干扰因素（通常不是根因，但会放大差异感）：

- 前端 pricing 页在 `requireAuth=true` 时会先跳登录，间接表现为“必须登录才能看到性能数据”
- 一边已登录浏览器带 cookie / Authorization，另一边裸 curl，造成“接口本身不需要认证”的错觉
- `perf_metrics_setting.enabled=false` 时接口仍可访问，只是数据为空或无采集；这与 401 不是一回事

---

## 5. 如何确认当前部署是否需要认证

### 5.1 读状态

```http
GET /api/status
```

查看返回 JSON 中的 `HeaderNavModules`，解析 `pricing`：

```text
enabled=true  && requireAuth=false  → 可匿名
enabled=true  && requireAuth=true   → 必须登录
enabled=false                       → 必须登录
空字符串 / 缺省                     → 可匿名（默认）
```

### 5.2 直接打接口

匿名：

```powershell
curl.exe -i "http://<host>/api/perf-metrics/summary?hours=24"
```

- `200` + `"success": true`：当前配置允许跳过认证  
- `401`：当前配置要求认证  

带登录态（session cookie 或用户 Access Token / PAT）：

```powershell
curl.exe -i -H "Authorization: Bearer <token>" "http://<host>/api/perf-metrics/summary?hours=24"
```

### 5.3 对照自动化测试

`middleware/header_nav_test.go` 已覆盖本中间件行为，例如：

- 空配置 → 公开 200
- `requireAuth=true` → 未登录 401
- `enabled=false` → 未登录 401；已登录 200

---

## 6. 如何让接口跳过 / 强制认证

### 6.1 管理后台（推荐）

1. 进入系统设置中的 **Header Navigation / 顶栏导航**  
2. 找到 **Pricing / 模型定价**  
3. 设置：
   - **允许匿名访问 summary**：`Enabled = 开`，`Require Auth = 关`
   - **强制登录**：`Require Auth = 开`（或关闭 Pricing 模块）
4. 保存后会写入 option `HeaderNavModules`

前端默认值（`web/src/features/system-settings/maintenance/config.ts`）：

```ts
pricing: {
  enabled: true,
  requireAuth: false,
}
```

### 6.2 直接写 Option（运维）

通过管理端 `PUT/POST options` 一类接口（项目现有 option 更新能力）写入：

**公开：**

```json
{
  "pricing": {
    "enabled": true,
    "requireAuth": false
  }
}
```

**强制登录：**

```json
{
  "pricing": {
    "enabled": true,
    "requireAuth": true
  }
}
```

注意应保留完整 `HeaderNavModules` 对象，避免覆盖掉 `home/console/rankings/...` 其他导航项。

---

## 7. Handler 本身不做二次鉴权

`controller/perf_metrics.go` 的 `GetPerfMetricsSummary`：

- 只解析 `hours`
- 查询所有活跃 group 的性能摘要
- **不读取用户身份、不按用户过滤、不做 admin 校验**

因此一旦中间件放行（含匿名 `TryUserAuth`），返回的是**全局聚合指标**，不是当前用户私有数据。

安全含义：

- 把 pricing 设为公开，就等于把全站模型性能摘要公开
- 若部署不希望外泄延迟/成功率等运营数据，应设置 `pricing.requireAuth=true`

---

## 8. 决策流程图

```text
请求 GET /api/perf-metrics/summary
        │
        ▼
读取 OptionMap["HeaderNavModules"].pricing
        │
        ├─ 空/解析失败 ─────────────────────────────► TryUserAuth（可匿名）
        │
        ├─ enabled=true 且 requireAuth=false ───────► TryUserAuth（可匿名）
        │
        └─ enabled=false 或 requireAuth=true ───────► UserAuth（必须登录）
                                                    │
                                                    ├─ 无有效凭证 → 401
                                                    └─ 有有效凭证 → 进入 Handler
```

---

## 9. 相关代码索引

| 文件 | 作用 |
| --- | --- |
| `router/api-router.go` | 挂载 `/api/perf-metrics` 与中间件 |
| `middleware/header_nav.go` | `HeaderNavModulePublicOrUserAuth` / 解析 `HeaderNavModules` |
| `middleware/header_nav_test.go` | 公开/强制登录/模块关闭用例 |
| `middleware/auth.go` | `UserAuth` / `TryUserAuth` |
| `controller/perf_metrics.go` | summary / detail 业务处理 |
| `controller/misc.go` | `/api/status` 输出 `HeaderNavModules` |
| `web/src/features/system-settings/maintenance/config.ts` | 默认值与序列化 |
| `web/src/features/system-settings/maintenance/header-navigation-section.tsx` | 后台配置 UI |
| `web/src/lib/nav-modules.ts` | 前端读取模块访问策略 |
| `web/src/features/pricing/components/model-card-grid.tsx` | 定价页消费 summary |

---

## 10. 一句话结论

- **需要认证吗？** 取决于 `HeaderNavModules.pricing`，默认**不需要**。  
- **可以跳过吗？** 可以。当 `pricing.enabled=true` 且 `pricing.requireAuth=false`（或未配置）时匿名可访问。  
- **哪个配置控制？** 系统 option **`HeaderNavModules` → `pricing.requireAuth` / `pricing.enabled`**，不是 `perf_metrics_setting`。  
- **两套部署表现不一致：** 先对比两边 `GET /api/status` 的 `HeaderNavModules.pricing`。
