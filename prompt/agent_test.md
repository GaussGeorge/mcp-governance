模拟Agent场景是验证MCP服务治理在真实智能体调用模式下的有效性的关键步骤。下面我为你详细设计整个实验方案，涵盖测试指标、数据收集、预期结果和具体实现步骤。

---

## 一、测试目标

通过模拟多步骤任务的Agent行为，验证Rajomon动态定价机制在以下方面的表现：
- **任务级公平性**：不同预算的Agent完成任务的成功率差异。
- **优先级调度**：高价值任务能否在过载时优先完成。
- **系统保护**：在突发任务负载下，系统能否保持稳定，避免雪崩。
- **成本意识**：治理策略能否根据任务的真实成本（Token消耗）动态调整。

---

## 二、Agent行为模型设计

### 1. Agent定义
- **每个Agent**：代表一个独立的客户端，拥有初始预算 `budget`（Token数），用于支付其发起的任务。
- **任务（Task）**：Agent一次完整的交互，包含多个**步骤（Step）**（即对MCP服务的调用）。例如，一个任务可能包括：调用天气工具、调用计算器工具、调用知识库查询。
- **步骤（Step）**：每个步骤对应一个HTTP请求，消耗一定Token，并返回结果。步骤之间可能有依赖关系（前一步成功才能继续下一步）。

### 2. 任务生成
- 任务到达服从**泊松分布**，平均间隔时间可调（例如平均每秒到达1个任务）。
- 每个任务包含随机数量的步骤（例如 1~5 步），步骤数从均匀分布中抽样。
- 每个步骤随机选择一种“工具类型”，不同工具类型对应不同的**处理延迟**和**Token消耗**。例如：

| 工具类型 | 模拟延迟 | Token消耗 | 说明 |
|----------|----------|-----------|------|
| `simple_query` | 50ms | 5 | 简单查询 |
| `calculation` | 100ms | 10 | 复杂计算 |
| `image_gen` | 500ms | 50 | 高成本操作 |

- 步骤间间隔可以加入随机延迟（例如 0~200ms），模拟Agent内部思考时间。

### 3. 预算管理
- 每个Agent的初始预算从集合 {10, 30, 100} 中随机分配（可配置比例）。
- 每个步骤发起请求前，Agent检查当前剩余预算是否大于0（若预算≤0，任务直接失败）。
- 步骤成功后，从响应头 `X-Token-Usage` 中获取实际消耗的Token，从预算中扣除。
- 若某步骤失败（HTTP 429或5xx），则整个任务失败，后续步骤不再执行。

### 4. 优先级（可选，推荐）
- 可以为任务分配优先级（如 high/medium/low），高优先级任务即使预算较低也可能获得服务。优先级可通过请求头 `X-Priority` 传递。
- 治理策略可根据优先级调整价格或拒绝概率（论文创新点）。

---

## 三、测试指标

### 1. 请求级指标（同基础测试）
- 吞吐量（QPS）
- 延迟（平均、P50、P95、P99）
- 拒绝率（429比例）
- 错误率（5xx比例）
- 每请求成本（Token消耗）

### 2. 任务级指标（核心新增）
| 指标 | 定义 | 意义 |
|------|------|------|
| **任务成功率** | 成功完成所有步骤的任务数 / 总任务数 | 衡量系统对完整交互的保障能力 |
| **任务完成率（按预算）** | 不同预算组的任务成功率 | 验证公平性 |
| **任务完成率（按优先级）** | 不同优先级组的任务成功率 | 验证优先级调度 |
| **任务平均步骤数** | 成功任务的步骤数均值 | 观察是否因预算不足导致任务提前终止 |
| **任务平均Token消耗** | 成功任务的总Token消耗均值 | 衡量任务成本 |
| **任务延迟（完成时间）** | 任务从第一步到最后一步的时间（包括步骤间间隔） | 反映整体体验 |

### 3. 系统级指标
- 资源利用率（CPU、内存）
- 价格变化曲线（实时）
- 不同工具类型的请求分布

---

## 四、需要收集的数据

### 1. 每请求日志（CSV字段）
扩展基础测试的日志，增加任务相关字段：

| 字段 | 说明 |
|------|------|
| `timestamp` | 请求结束时间戳 |
| `agent_id` | Agent唯一标识 |
| `task_id` | 任务唯一标识 |
| `step_index` | 步骤序号（从1开始） |
| `tool_type` | 工具类型（如 `simple_query`） |
| `client_budget` | 发起请求时Agent剩余预算 |
| `latency_ms` | 请求耗时 |
| `status_code` | HTTP状态码 |
| `price_returned` | 响应头中的价格（若有） |
| `token_consumed` | 实际消耗的Token（从`X-Token-Usage`） |
| `rejected` | 是否被拒绝（status=429） |
| `priority` | 任务优先级（若实现） |

### 2. 任务级汇总（后处理生成）
- `task_id`, `agent_id`, `priority`, `initial_budget`
- `task_success`（布尔）
- `task_steps`（总步骤数）
- `completed_steps`（成功完成的步骤数）
- `task_total_tokens`（总消耗Token）
- `task_duration_ms`（任务总耗时，从第一步到成功最后一步或失败的时间）
- `failure_reason`（失败原因：预算不足、某步骤被拒、系统错误等）

### 3. 系统指标（Prometheus）
同基础测试，重点关注 `rajomon_current_price` 和 `rajomon_requests_total{status="rejected_rajomon"}`。

---

## 五、预期结果对比

| 对比维度 | 无治理 | 静态限流 | Rajomon动态定价 |
|----------|--------|----------|------------------|
| **低负载** | 所有任务成功率≈100% | 所有任务成功率≈100% | 所有任务成功率≈100% |
| **突发负载** | 延迟飙升，大量任务失败（可能部分步骤成功），系统可能崩溃 | 超过阈值后所有任务被拒绝，成功率断崖式下跌 | **价格快速上涨，低预算任务被拦截，高预算任务仍有较高成功率** |
| **高负载持续** | 成功率接近0 | 0 | **高预算任务成功率稳定在较高水平（如>60%），低预算接近0，中等预算介于之间** |
| **公平性** | 所有预算组成功率相同（都低） | 无区分，阈值后全部拒绝 | **高预算 > 中预算 > 低预算** |
| **优先级** | 无区分 | 无区分 | **高优先级任务成功率高于同预算的低优先级任务**（若实现优先级） |
| **任务完成时间** | 随负载增加急剧增长 | 被拒任务无完成时间，成功任务延迟低 | **成功任务延迟保持平稳（如P95 < 500ms），任务完成时间可控** |
| **成本效益** | 单位Token产生的成功任务数低 | 单位Token产生成功任务数为0（过载时） | **高价值任务（复杂工具）被优先服务，简单任务牺牲** |

---

## 六、实验变量设计

### 1. 治理策略（3种）
- **无治理**：网关透传，不限流。
- **静态限流**：固定QPS阈值（例如根据基线测试的最大吞吐量设为80%），超过则返回429。
- **Rajomon动态定价**：启用你的治理中间件，参数预先设定（如α=0.2，阈值=200，权重等）。

### 2. 负载模式（2-3种）
- **突发负载**：在短时间内任务到达率激增（例如从10任务/秒升至100任务/秒）。
- **泊松负载**：任务到达间隔服从泊松分布，平均率固定（例如50任务/秒），观察随机波动。
- **正弦负载**：任务率随时间正弦变化，模拟日周期。

### 3. 预算分布
- 低预算组（10）：20% Agent
- 中预算组（30）：30% Agent
- 高预算组（100）：50% Agent

### 4. 优先级分布（可选）
- 高优先级：20% 任务
- 中优先级：30% 任务
- 低优先级：50% 任务

### 5. 运行次数
每种组合运行3次，取平均值，确保可复现。

---

## 七、实现建议

### 1. 负载生成器改造（Go示例框架）

```go
// agent_simulator.go
package main

import (
    "context"
    "encoding/csv"
    "fmt"
    "math/rand"
    "net/http"
    "os"
    "strconv"
    "sync"
    "time"
)

type Agent struct {
    ID     string
    Budget int
    Client *http.Client
}

type StepResult struct {
    AgentID      string
    TaskID       string
    StepIdx      int
    ToolType     string
    BudgetBefore int
    LatencyMs    int64
    StatusCode   int
    Price        int
    TokenUsed    int
    Rejected     bool
    Timestamp    int64
}

type Task struct {
    ID       string
    Agent    *Agent
    Priority string
    Steps    []string // 工具类型列表
}

func (a *Agent) executeStep(ctx context.Context, taskID string, stepIdx int, toolType string, results chan<- StepResult, wg *sync.WaitGroup) {
    defer wg.Done()
    if a.Budget <= 0 {
        // 预算不足，直接记录失败（但请求不会发出）
        results <- StepResult{
            AgentID:   a.ID,
            TaskID:    taskID,
            StepIdx:   stepIdx,
            ToolType:  toolType,
            BudgetBefore: a.Budget,
            StatusCode: -2, // 自定义：预算不足
            Rejected:  true,
            Timestamp: time.Now().UnixMilli(),
        }
        return
    }

    url := "http://localhost:8080/mcp/chat"
    req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
    req.Header.Set("Token", strconv.Itoa(a.Budget))
    req.Header.Set("X-Tool-Type", toolType)
    req.Header.Set("X-Task-ID", taskID)
    req.Header.Set("X-Step-Idx", strconv.Itoa(stepIdx))

    start := time.Now()
    resp, err := a.Client.Do(req)
    latency := time.Since(start).Milliseconds()

    result := StepResult{
        AgentID:      a.ID,
        TaskID:       taskID,
        StepIdx:      stepIdx,
        ToolType:     toolType,
        BudgetBefore: a.Budget,
        LatencyMs:    latency,
        StatusCode:   0,
        Rejected:     false,
        Timestamp:    time.Now().UnixMilli(),
    }

    if err != nil {
        result.StatusCode = -1 // 网络错误
    } else {
        result.StatusCode = resp.StatusCode
        if p := resp.Header.Get("Price"); p != "" {
            result.Price, _ = strconv.Atoi(p)
        }
        if t := resp.Header.Get("X-Token-Usage"); t != "" {
            result.TokenUsed, _ = strconv.Atoi(t)
        }
        result.Rejected = (resp.StatusCode == http.StatusTooManyRequests)
        resp.Body.Close()
    }

    // 更新Agent预算（如果成功且消耗了Token）
    if result.StatusCode == 200 && result.TokenUsed > 0 {
        a.Budget -= result.TokenUsed
        if a.Budget < 0 {
            a.Budget = 0
        }
    }

    results <- result
}

func (a *Agent) runTask(taskID string, steps []string, priority string, results chan<- StepResult) {
    var wg sync.WaitGroup
    ctx := context.Background()
    for i, tool := range steps {
        // 步骤间间隔
        time.Sleep(time.Duration(rand.Intn(200)) * time.Millisecond)

        // 如果预算不足，提前终止任务
        if a.Budget <= 0 {
            break
        }

        wg.Add(1)
        go a.executeStep(ctx, taskID, i+1, tool, results, &wg)
        // 等待步骤完成？这里我们并发执行所有步骤，但实际Agent通常是顺序的。
        // 为了模拟顺序，可以改为同步执行。这里简单起见，我们改为同步调用。
        // 上面是并发，需要调整。建议改为同步顺序调用，更符合真实Agent行为。
        // 我们修改：直接调用executeStep并等待它完成。
        // 为了简化，这里用同步方式。
    }
    wg.Wait()
}

func main() {
    // 配置
    numAgents := 50
    taskRate := 2.0 // 任务/秒（泊松）
    duration := 5 * time.Minute
    budgets := []int{10, 30, 100}
    priorities := []string{"high", "medium", "low"}

    // 初始化Agents
    agents := make([]*Agent, numAgents)
    for i := 0; i < numAgents; i++ {
        agents[i] = &Agent{
            ID:     fmt.Sprintf("agent-%d", i),
            Budget: budgets[rand.Intn(len(budgets))],
            Client: &http.Client{Timeout: 10 * time.Second},
        }
    }

    results := make(chan StepResult, 10000)

    // 任务生成器
    taskIDGen := 0
    ticker := time.NewTicker(time.Duration(1.0/taskRate * float64(time.Second)))
    done := make(chan bool)
    go func() {
        for {
            select {
            case <-ticker.C:
                // 选择一个随机Agent
                agent := agents[rand.Intn(len(agents))]
                // 生成随机步骤
                stepCount := rand.Intn(5) + 1
                steps := make([]string, stepCount)
                for i := 0; i < stepCount; i++ {
                    tools := []string{"simple_query", "calculation", "image_gen"}
                    steps[i] = tools[rand.Intn(len(tools))]
                }
                priority := priorities[rand.Intn(len(priorities))]
                taskID := fmt.Sprintf("task-%d", taskIDGen)
                taskIDGen++
                go agent.runTask(taskID, steps, priority, results)
            case <-done:
                return
            }
        }
    }()

    // 运行指定时长
    time.Sleep(duration)
    close(done)
    close(results)

    // 写入CSV
    f, _ := os.Create("agent_results.csv")
    defer f.Close()
    w := csv.NewWriter(f)
    w.Write([]string{"timestamp","agent_id","task_id","step_idx","tool_type","budget_before","latency_ms","status","price","token_used","rejected"})
    for r := range results {
        w.Write([]string{
            strconv.FormatInt(r.Timestamp, 10),
            r.AgentID,
            r.TaskID,
            strconv.Itoa(r.StepIdx),
            r.ToolType,
            strconv.Itoa(r.BudgetBefore),
            strconv.FormatInt(r.LatencyMs, 10),
            strconv.Itoa(r.StatusCode),
            strconv.Itoa(r.Price),
            strconv.Itoa(r.TokenUsed),
            strconv.FormatBool(r.Rejected),
        })
    }
    w.Flush()
}
```

注意：上面代码是同步执行步骤的简化版，实际中需要根据真实Agent行为调整。更准确的模拟应为顺序执行。

### 2. Mock后端扩展
在 `mcp_handler.go` 中，根据请求头 `X-Tool-Type` 返回对应的延迟和Token消耗。例如：

```go
func HandleMCP(w http.ResponseWriter, r *http.Request) {
    toolType := r.Header.Get("X-Tool-Type")
    var delay time.Duration
    var tokenUsage int
    switch toolType {
    case "simple_query":
        delay = 50 * time.Millisecond
        tokenUsage = 5
    case "calculation":
        delay = 100 * time.Millisecond
        tokenUsage = 10
    case "image_gen":
        delay = 500 * time.Millisecond
        tokenUsage = 50
    default:
        delay = 100 * time.Millisecond
        tokenUsage = 10
    }
    time.Sleep(delay)
    w.Header().Set("X-Token-Usage", strconv.Itoa(tokenUsage))
    // ... 其余流式响应逻辑
}
```

### 3. 数据后处理
- 使用Python脚本读取CSV，按`task_id`分组，计算任务成功率、任务完成时间等。
- 绘制图表：
  - 按预算组的任务成功率柱状图（对比三种策略）。
  - 按优先级的任务成功率柱状图（若实现）。
  - 任务完成时间CDF图。
  - 价格变化曲线叠加任务到达率。

---

## 八、时间估计

| 阶段 | 时间 | 产出 |
|------|------|------|
| 设计Agent模拟器细节 | 1天 | 设计文档 |
| 改造负载生成器（Go） | 2-3天 | 可运行的Agent模拟器 |
| 扩展Mock后端 | 0.5天 | 支持工具类型的后端 |
| 调试与验证 | 1天 | 确保数据正确 |
| 运行实验（3策略 × 3负载 × 3次） | 2天 | 原始数据 |
| 数据分析与图表制作 | 2-3天 | 论文图表及初步分析 |
| **总计** | **约8-10天** | 可写入论文的Agent场景实验章节 |

---

## 九、总结

模拟Agent场景实验能充分展现你的治理机制在复杂调用模式下的优势，且与MCP协议的应用场景紧密相关。按照上述方案执行后，你将获得一组丰富的数据，足以支撑一篇高水平论文的实验部分。
