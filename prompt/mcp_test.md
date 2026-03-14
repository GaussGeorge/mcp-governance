针对你的三种治理策略（无治理、固定阈值限流、Rajomon 动态定价），我为你设计了一套**严谨、可复现的基础 MCP 请求测试方案**，涵盖测试指标、数据收集、预期结果对比和具体实现步骤。

---

## 一、测试指标（Metrics）

从四个维度定义量化指标，用于评估系统行为和治理效果：

### 1. 性能维度

| 指标             | 定义                       | 计算公式                      |
| ---------------- | -------------------------- | ----------------------------- |
| **吞吐量**       | 单位时间内成功处理的请求数 | `成功请求数 / 时间窗口（秒）` |
| **平均延迟**     | 所有成功请求的处理时间均值 | `sum(延迟)/成功请求数`        |
| **P95/P99 延迟** | 95%/99%分位延迟            | 排序后取分位值                |
| **延迟标准差**   | 延迟的波动程度             | `sqrt(方差)`                  |
| **错误率**       | 非 2xx 响应的比例          | `(错误请求数)/总请求数`       |

### 2. 治理效果维度

| 指标               | 定义                              | 计算公式                            |
| ------------------ | --------------------------------- | ----------------------------------- |
| **拒绝率**         | 被网关主动拒绝（429）的请求比例   | `(429请求数)/总请求数`              |
| **动态价格变化**   | 价格随时间的实时值                | 从响应 Header 或 Prometheus 采集    |
| **公平性**         | 不同预算组用户的成功率            | `各预算组的成功请求数/该组总请求数` |
| **过载保护有效性** | 系统崩溃前能承受的最大负载        | 观察吞吐量拐点                      |
| **恢复时间**       | 负载回落后延迟/价格恢复正常的时间 | 时间序列分析                        |

### 3. 成本维度

| 指标            | 定义                              | 计算公式                           |
| --------------- | --------------------------------- | ---------------------------------- |
| **每请求成本**  | 每个请求消耗的 Token 数           | 从响应 Header `X-Token-Usage` 获取 |
| **成本-收益比** | 成功请求数 / 总 Token 消耗        | `成功数 / sum(Token)`              |
| **预算利用率**  | 用户实际消耗 Token 占其预算的比例 | `消耗Token/初始预算`               |

### 4. 资源维度

| 指标           | 定义                          | 计算公式                         |
| -------------- | ----------------------------- | -------------------------------- |
| **CPU 利用率** | 网关和 Mock 服务的 CPU 使用率 | 从 Docker stats 或 cadvisor 获取 |
| **内存利用率** | 网关和 Mock 服务的内存使用    | 同上                             |

---

## 二、需要收集的数据（Data Collection）

### 1. 负载生成器日志（每请求记录）

| 字段             | 说明                          | 来源                        |
| ---------------- | ----------------------------- | --------------------------- |
| `timestamp`      | 请求结束时间（毫秒精度）      | 系统时间                    |
| `request_id`     | 唯一标识                      | UUID                        |
| `client_budget`  | 请求携带的 Token 预算         | 从请求 Header 记录          |
| `latency_ms`     | 请求总耗时（毫秒）            | `time.Since(start)`         |
| `status_code`    | HTTP 状态码                   | 响应                        |
| `price_returned` | 响应头中的 Price 值（若有）   | 响应 Header                 |
| `token_consumed` | 响应头中的 Token 消耗（若有） | 响应 Header `X-Token-Usage` |
| `is_rejected`    | 是否被网关拒绝（status=429）  | 布尔值                      |

### 2. 系统指标（Prometheus）

确保网关已暴露以下指标：

- `rajomon_requests_total{status="accepted/rejected", handler}`
- `rajomon_request_duration_seconds{handler}`
- `rajomon_token_usage{handler}`
- `rajomon_current_price`
- `rajomon_composite_cost`

同时通过 cadvisor 或`docker stats`收集容器 CPU/内存，导出为时间序列。

### 3. 测试配置记录

- 治理策略（无/固定阈值/Rajomon）
- 固定阈值限流的 QPS 值
- Rajomon 参数（α、阈值、权重）
- 负载模式参数（并发数、持续时间、突发倍数）
- 运行次数（至少 3 次取平均）

---

## 三、预期结果对比（Expected Results Comparison）

| 对比维度         | 无治理                               | 固定阈值限流                           | Rajomon 动态定价                                                             |
| ---------------- | ------------------------------------ | -------------------------------------- | ---------------------------------------------------------------------------- |
| **低负载时**     | 正常，延迟低                         | 正常，无拦截                           | 正常，价格低，无拦截                                                         |
| **突发负载初期** | 延迟开始升高                         | 未超阈值，正常；超阈值则拦截           | 价格开始上涨，低预算请求少量被拒                                             |
| **高负载持续**   | **延迟飙升，大量超时/500，可能崩溃** | 超过阈值后所有请求被拒绝（吞吐量跌零） | **价格稳定在高位，拦截低预算请求，高预算请求仍有部分成功，成功请求延迟平稳** |
| **负载回落**     | 延迟缓慢下降，可能仍有积压           | 恢复后重新允许请求                     | **价格快速下降，请求逐渐恢复正常**                                           |
| **公平性**       | 所有用户同等受损                     | 无区分，阈值后全部拒绝                 | **高预算用户成功率显著高于低预算**                                           |
| **资源利用率**   | CPU 飙升，可能耗尽                   | CPU 受控，但空闲时浪费                 | CPU 平稳，利用率高                                                           |

**关键验证点**：

- Rajomon 动态定价下，吞吐量曲线应呈现“削峰填谷”特征，而非直接断崖式下跌。
- 价格曲线应与负载/成本曲线强相关，呈现“负载高 → 成本高 → 价格上涨 → 拒绝增加 → 负载稳定”的负反馈震荡。
- 延迟的 P99 应在高负载下保持可控（例如<500ms），而非无限增长。

---

## 四、基础测试方案设计（Test Plan Design）

### 1. 测试环境

- **部署方式**：使用 `docker-compose` 启动网关、Mock 后端、Prometheus。
- **硬件**：同一台机器或固定资源（避免资源竞争）。
- **网络**：localhost 或内部网络，消除网络延迟干扰。

### 2. 负载生成器实现（Go 示例框架）

```go
// stress_test/main.go
package main

import (
    "encoding/csv"
    "fmt"
    "math/rand"
    "net/http"
    "os"
    "strconv"
    "sync"
    "time"
)

type RequestResult struct {
    Timestamp    int64
    ClientBudget int
    LatencyMs    int64
    StatusCode   int
    Price        int
    TokenUsage   int
    Rejected     bool
}

func worker(id int, url string, budgets []int, results chan<- RequestResult, wg *sync.WaitGroup) {
    defer wg.Done()
    client := &http.Client{Timeout: 10 * time.Second}
    for {
        // 随机选择一个预算
        budget := budgets[rand.Intn(len(budgets))]
        req, _ := http.NewRequest("GET", url, nil)
        req.Header.Set("Token", strconv.Itoa(budget))

        start := time.Now()
        resp, err := client.Do(req)
        latency := time.Since(start).Milliseconds()

        result := RequestResult{
            Timestamp:    time.Now().UnixMilli(),
            ClientBudget: budget,
            LatencyMs:    latency,
            StatusCode:   0,
            Rejected:     false,
        }

        if err != nil {
            // 网络错误
            result.StatusCode = -1
        } else {
            result.StatusCode = resp.StatusCode
            if priceStr := resp.Header.Get("Price"); priceStr != "" {
                result.Price, _ = strconv.Atoi(priceStr)
            }
            if tokenStr := resp.Header.Get("X-Token-Usage"); tokenStr != "" {
                result.TokenUsage, _ = strconv.Atoi(tokenStr)
            }
            result.Rejected = (resp.StatusCode == http.StatusTooManyRequests)
            resp.Body.Close()
        }

        results <- result
        // 控制请求速率（可选）
        time.Sleep(time.Duration(rand.Intn(50)) * time.Millisecond)
    }
}

func runLoad(url string, duration time.Duration, concurrency int, budgets []int) []RequestResult {
    var wg sync.WaitGroup
    results := make(chan RequestResult, 10000)
    for i := 0; i < concurrency; i++ {
        wg.Add(1)
        go worker(i, url, budgets, results, &wg)
    }

    // 运行指定时长
    time.Sleep(duration)

    // 停止所有worker（这里简单处理，实际需要更的停止机制）
    // 此处用关闭通道等待，但实际最好用context取消。这里简化。
    close(stop) // 需要实现stop机制
    wg.Wait()
    close(results)

    var all []RequestResult
    for r := range results {
        all = append(all, r)
    }
    return all
}

func main() {
    // 配置参数
    url := "http://localhost:8080/mcp/chat"
    budgets := []int{10, 20, 50}
    concurrency := 50
    duration := 5 * time.Minute

    // 运行负载
    results := runLoad(url, duration, concurrency, budgets)

    // 写入CSV
    f, _ := os.Create("results.csv")
    defer f.Close()
    w := csv.NewWriter(f)
    w.Write([]string{"timestamp","budget","latency_ms","status","price","token_usage","rejected"})
    for _, r := range results {
        w.Write([]string{
            strconv.FormatInt(r.Timestamp, 10),
            strconv.Itoa(r.ClientBudget),
            strconv.FormatInt(r.LatencyMs, 10),
            strconv.Itoa(r.StatusCode),
            strconv.Itoa(r.Price),
            strconv.Itoa(r.TokenUsage),
            strconv.FormatBool(r.Rejected),
        })
    }
    w.Flush()
}
```

### 3. 负载模式设计

为了全面评估，建议设计三种负载模式，每种模式下运行三次取平均：

#### 模式 A：阶梯式突发负载

| 阶段   | 时间  | 并发数 | 说明     |
| ------ | ----- | ------ | -------- |
| 预热   | 1 min | 10     | 系统预热 |
| 阶跃 1 | 2 min | 30     | 中等负载 |
| 阶跃 2 | 2 min | 80     | 高负载   |
| 阶跃 3 | 2 min | 150    | 过载     |
| 恢复   | 2 min | 10     | 负载回落 |

#### 模式 B：正弦波动负载（模拟日周期）

并发数随时间按正弦函数变化：`base + amplitude * sin(2π * t / period)`。例如周期 5 分钟，振幅 50，基础 20。

#### 模式 C：泊松到达负载

使用泊松过程生成请求间隔，平均 QPS 设为固定值（如 50），观察随机性下的表现。

### 4. 实验流程

1. **准备**：确保所有服务运行，Prometheus 正在采集。
2. **基线测试**：设置网关为“透传模式”（无治理），运行三种负载模式，收集数据。
3. **固定阈值测试**：配置限流阈值（例如根据基线测试的最大吞吐量设定为 80%），运行相同负载。
4. **Rajomon 测试**：启用动态定价（参数预设），运行相同负载。
5. **重复**：每种策略重复 3 次，取平均值，确保结果可复现。

### 5. 数据分析与图表

- **时间序列图**：绘制吞吐量、拒绝率、P99 延迟、价格随时间变化（同一图，双 Y 轴）。
- **CDF 图**：比较三种策略下的延迟分布。
- **箱线图**：不同预算组在各策略下的成功率。
- **散点图**：价格与成本的关系。

所有图表使用 Python（matplotlib/seaborn）或 Excel 生成，标注清晰，用于论文。

---

## 五、注意事项

- **随机种子固定**：在负载生成器中固定随机种子，确保可复现。
- **系统预热**：每个测试前让系统运行至少 1 分钟，排除冷启动影响。
- **资源隔离**：避免其他进程干扰，建议在专用机器或容器中运行。
- **数据清洗**：排除预热阶段和测试结束后的边缘数据。

完成以上测试后，你将获得一组丰富、严谨的实验数据，足以支撑论文的实验章节。如果需要具体的 Grafana 面板配置或数据分析脚本，我可以进一步提供。
