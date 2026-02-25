# MCP 服务治理引擎 (MCP Governance)

动态定价思想，将过载控制应用于 **MCP (Model Context Protocol)** 工具调用场景的服务治理框架。

## 核心思想

- 每个 MCP 工具调用 (`tools/call`) 都有一个动态"**价格**" (price)
- 客户端在请求的 `_meta.tokens` 中携带"**令牌**" (预算)
- 服务端根据当前负载（排队延迟 / 吞吐量）**动态调整价格**
- 当 `tokens < price` 时，触发**负载削减 (Load Shedding)**，主动拒绝请求以保护服务
- 客户端通过**限流 (Rate Limiting)** 在令牌不足时阻止请求发出

## 项目结构与文件说明

### 核心源码文件

| 文件 | 作用 |
|------|------|
| `mcp_protocol.go` | **MCP 协议类型定义**。基于 JSON-RPC 2.0 标准，定义了请求/响应结构体 (`JSONRPCRequest`, `JSONRPCResponse`)、MCP 工具调用参数 (`MCPToolCallParams`)、治理元数据 (`GovernanceMeta`, `ResponseMeta`)、错误码常量以及辅助构造函数。这是整个项目的协议基础层。 |
| `mcp_governor.go` | **服务治理引擎核心实现**。包含 `MCPGovernor` 结构体定义及其核心方法：`LoadShedding` (服务端负载削减/准入控制)、`RateLimiting` (客户端限流检查)、`HandleToolCall` (JSON-RPC 中间件入口)、`HandleToolCallDirect` (直接调用模式)、`ClientMiddleware` (客户端治理中间件)、`UpdateResponsePrice` (响应价格更新)。支持三种价格聚合策略：`maximal` (最大值)、`additive` (累加)、`mean` (平均值)。 |
| `mcp_init.go` | **MCPGovernor 初始化配置**。提供 `NewMCPGovernor` 构造函数，解析各种配置选项（限流、负载削减、过载检测模式、价格策略等），启动后台检测协程与令牌补充协程，初始化价格表。同时包含令牌的原子操作方法 (`GetTokensLeft`, `DeductTokens`, `AddTokens`) 和令牌补充逻辑 (`tokenRefill`，支持固定/均匀/泊松三种分布)。 |
| `mcp_transport.go` | **MCP HTTP 传输层实现**。提供 `MCPServer` 结构体，实现了 `http.Handler` 接口，支持通过 HTTP POST + JSON-RPC 2.0 进行 MCP 通信。路由处理 `initialize`（握手）、`tools/list`（工具列表）、`tools/call`（工具调用，集成治理中间件）、`ping`（健康检查）等标准 MCP 方法。 |
| `tokenAndPrice.go` | **令牌分配与价格管理**。包含 `SplitTokens` (将剩余令牌按策略分配给下游工具)、`RetrieveDSPrice` (获取下游聚合价格)、`RetrieveTotalPrice` (计算总价格)、`SetOwnPrice`、`UpdateOwnPrice` (Step 策略涨降价)、`UpdatePrice` (ExpDecay 指数衰减策略) 和 `UpdateDownstreamPrice` (下游价格更新与传播)。 |
| `overloadDetection.go` | **过载检测引擎**。提供四种过载检测后台协程：`latencyCheck` (基于业务延迟)、`queuingCheck` (基于 Go runtime 调度器排队延迟，读取 `/sched/latencies:seconds` 直方图)、`throughputCheck` (基于吞吐量计数器)、`checkBoth` (联合吞吐量+排队延迟)。同时包含 `overloadDetection` 辅助判定函数和吞吐量计数器的原子操作。 |
| `queuingDelay.go` | **排队延迟直方图分析**。提供直方图统计函数：`medianBucket` (中位数 P50)、`percentileBucket` (任意分位数)、`maximumBucket` (最大值)、`GetHistogramDifference` (两个直方图的差分计算)、`maximumQueuingDelayms` (优化版差分最大值)、`readHistogram` (从 `runtime/metrics` 读取调度器延迟直方图)。这些工具函数支撑了基于排队延迟检测过载的核心能力。 |
| `logger.go` | **日志工具**。提供 `logger` (调试日志，受 `debug` 开关控制) 和 `recordPrice` (价格追踪日志，受 `trackPrice` 开关控制) 两个格式化输出函数。 |

### 单元测试文件 (根目录)

| 文件 | 作用 |
|------|------|
| `mcp_governor_test.go` | **治理引擎核心单元测试**。测试令牌准入控制 (低令牌拒绝、高令牌通过、混合流量)、下游价格存储与检索 (Maximal 策略)、`LoadShedding` 扣费逻辑、JSON-RPC 2.0 消息格式正确性。 |
| `mcp_transport_test.go` | **HTTP 传输层集成测试**。通过 `httptest` 启动真实 HTTP 服务端测试：MCP Initialize 握手、Tools List 列表、工具调用治理流程（令牌充足/不足/未注册工具）、高并发下的价格自适应、Ping 健康检查、无效方法名处理。 |

### 集成测试文件 (`mcp_test/` 目录)

| 文件 | 作用 |
|------|------|
| `load_shedding_test.go` | **负载削减 (Load Shedding) 效果测试**。验证基础准入控制、过载拒绝率、选择性准入（高/低预算分流）、并发保护、拒绝响应携带价格信息、零价格放行、渐进涨价下拒绝率变化、关闭负载削减模式、基于吞吐量的保护、成功响应价格信息、价格聚合策略对比（Maximal 表格驱动测试）。还包含性能基准测试 `BenchmarkLoadShedding_Accepted/Rejected`。同时提供了公共辅助函数 (`newTestServer`, `sendRequest`, `makeToolCallReq`, `simpleHandler`) 供所有 `mcp_test` 包内的测试共用。 |
| `rate_limiting_test.go` | **客户端限流 (Rate Limiting) 效果测试**。验证令牌扣除逻辑、固定速率令牌补充、令牌添加、并发令牌操作安全性、客户端中间件 `_meta` 注入、限流阻止低令牌请求、退避机制、`RateLimiting` 方法直接测试、客户端限流 + 服务端负载削减联动端到端测试、`UpdateResponsePrice` 价格缓存更新。 |
| `dynamic_pricing_test.go` | **动态定价 (Dynamic Pricing) 效果测试**。验证 Step 策略（拥塞涨价 / 非拥塞降价 / 价格不为负）、指导价格 (guidePrice) 机制、指数衰减策略（抑制震荡 / 衰减计数器重置）、底价 (Reserve Price) 保护、过载→恢复完整周期端到端测试、下游价格传播 (Maximal / Additive / Mean 三种聚合策略)。 |
| `e2e_governance_test.go` | **端到端服务治理集成测试**。模拟真实 MCP 场景：多工具链路治理（网关 → 天气服务 + 酒店服务，价格传播与聚合）、令牌分配 (`SplitTokens` 测试)、渐进式过载（逐步提高并发度观察拒绝率）、脉冲式突发流量（交替高峰/低谷）、预算公平性测试（高预算优先通过）、`HandleToolCallDirect` 直接调用模式、长时间运行稳定性测试 (10秒持续负载)、价格元信息往返 (Request→Response→ClientCache 完整链路)。 |
| `poisson_burst_test.go` | **泊松突发流量压力测试**。基于泊松过程 (Poisson Process) 建模真实不均匀流量，覆盖两条过载检测路径：`throughputCheck`（吞吐量驱动）和 `queuingCheck`（排队延迟驱动，配合 `GOMAXPROCS(2)` 制造调度瓶颈）。包含六大测试场景：吞吐量驱动泊松到达（不同 λ 下拒绝率从 0% 到 91%）、排队延迟驱动（`GOMAXPROCS=2` + CPU 忙等）、非齐次泊松过程 NHPP（λ 随时间变化：正常→爬升→峰值→骤降→恢复）、复合泊松突发（外层突发事件 + 内层批量请求，模拟 AI Agent 并行工具调用）、突发振幅对比（固定等效 RPS 下不同聚集程度的治理效果差异）、客户端泊松令牌补充（双重随机系统：`tokenRefillDist="poisson"` + 泊松请求到达）。同时提供了公共辅助函数 (`busyWork`, `poissonSender`, `poissonSample`, `busyHandler`, `makeThroughputOpts`, `makeQueuingOpts`) 供泊松测试使用。 |

### Agent 场景测试文件 (`agent_test/` 目录)

通过代码模拟 AI Agent 的真实工具调用行为，验证治理引擎在 Agent 场景下的表现。测试框架基于 `SimulatedAgent` 抽象，支持 4 种预算分配策略（Fixed 固定 / EqualSplit 均分 / FrontLoaded 前置 / Adaptive 自适应），可执行链式推理和并发竞争两种模式。

| 文件 | 作用 |
|------|------|
| `helpers_test.go` | **Agent 模拟框架（公共基础设施）**。定义 `SimulatedAgent`（含预算、策略、任务队列）、`AgentTask`（工具调用任务，支持依赖关系和重试）、`AgentMetrics`（统计指标）、`TestMetrics`（全局指标收集器，含 Jain 公平性指数）。提供 `ExecuteChain`（依赖链式执行）和 `ExecuteConcurrentCalls`（并发独立调用）两种执行引擎，以及 `runAgentsParallel`（多 Agent 并行启动）、`makeAgents`（批量创建）、`simpleOKHandler` / `slowHandler` / `cpuBusyHandler`（模拟工具处理器）等辅助函数。 |
| `competition_test.go` | **多 Agent 竞争资源场景测试（7 个场景）**。验证等预算公平性（Jain 指数）、不等预算区分能力（高预算优先）、动态涨价竞争压力、竞争升级（2→5→10→20 Agent 阶梯）、多工具资源隔离、突发竞争（10 Agent 同时涌入）、不同预算策略同台竞技效率对比。 |
| `budget_test.go` | **Agent 预算管理场景测试（10 个场景）**。验证预算耗尽优雅停止、低/中/高预算完成率分层、4 种策略效率基准、价格阶梯侵蚀预算、自适应 vs 固定策略在价格波动环境下的对比、多 Agent 预算隔离、拒绝退款不膨胀、预算生命周期（长时间消耗曲线）、边界条件（零预算/最小预算/超大预算）、全局令牌总量守恒。 |
| `reasoning_chain_test.go` | **Agent 多步推理链场景测试（11 个场景）**。模拟 LLM Agent 的 "思考→调工具→思考→调工具" 链式调用模式。验证基础线性链（search→analyze→summarize）、依赖断裂（中间步骤失败→后续跳过）、重试恢复、10 步长链稳定性、链中动态涨价、多 Agent 并行推理链、分支推理（可选步骤不影响主链）、链模式下策略对比、批量实验（N=20 收集统计分布）、竞争+推理链结合（干扰 Agent 对链完成率的影响）、Fan-out/Fan-in 并行分支汇总。 |

#### 多 Agent 竞争资源测试 (`agent_test/competition_test.go`)

| 测试函数 | 说明 |
|---------|------|
| `TestCompetition_EqualBudget_Fairness` | 5 个等预算 Agent 竞争同一工具，验证 Jain 公平性指数 > 0.8 |
| `TestCompetition_UnequalBudget_HighBudgetAdvantage` | 2 高预算 vs 3 低预算 Agent，验证高预算平均成功率更高 |
| `TestCompetition_DynamicPricing_UnderContention` | 10 Agent 涌入触发动态涨价，验证系统不崩溃且有效吞吐 > 0 |
| `TestCompetition_Escalation` | 从 2→5→10→20 Agent 逐步升级，观察拒绝率随竞争加剧的变化 |
| `TestCompetition_MultiTool_ResourceIsolation` | 2 组 Agent 分别竞争 2 个不同工具，验证跨工具负载隔离 |
| `TestCompetition_BurstArrival` | 10 Agent 同一时刻启动 (sync.WaitGroup)，高频发请求验证稳定性 |
| `TestCompetition_StrategyComparison` | 4 种策略 Agent 同台竞技，对比成功次数和效率 (成功/千令牌) |

#### Agent 预算管理测试 (`agent_test/budget_test.go`)

| 测试函数 | 说明 |
|---------|------|
| `TestBudget_Exhaustion_GracefulStop` | 有限预算持续调用直到耗尽，验证不产生负预算 |
| `TestBudget_Tiers_CompletionRate` | 低/中/高 3 档预算完成率对比 |
| `TestBudget_StrategyEfficiency_Benchmark` | Fixed/EqualSplit/FrontLoaded/Adaptive 4 策略效率基准 |
| `TestBudget_PriceIncrease_BudgetErosion` | 5 阶段价格阶梯 (10→30→50→70→100) 对预算的侵蚀效果 |
| `TestBudget_Adaptive_vs_Fixed_UnderPriceFluctuation` | 价格波动环境下自适应策略 vs 固定策略对比 |
| `TestBudget_Isolation_IndependentBudgets` | 3 Agent 独立预算 (50/500/5000)，一个耗尽不影响其他 |
| `TestBudget_Refund_OnRejection` | 拒绝退款机制验证：退款不导致预算膨胀 |
| `TestBudget_Lifecycle_LongRunning` | 10 阶段长时间运行，记录预算消耗曲线 |
| `TestBudget_EdgeCases` | 边界条件：零预算 / 最小预算(刚好一次) / 超大预算 |
| `TestBudget_Conservation_TotalTokens` | 多 Agent 并发后全局令牌总量守恒验证 |

#### Agent 多步推理链测试 (`agent_test/reasoning_chain_test.go`)

| 测试函数 | 说明 |
|---------|------|
| `TestChain_Linear_Basic` | 基础 3 步线性链 (search→analyze→summarize)，低价全部完成 |
| `TestChain_DependencyBreak` | 高价环境下依赖断裂：前步被拒→后步跳过 |
| `TestChain_WithRetries` | 对比有重试 vs 无重试的链完成率 |
| `TestChain_LongChain_Stability` | 10 步长链稳定性，预算充足时全部完成 |
| `TestChain_DynamicPricing_MidChain` | 5 步链执行中途服务端涨价 (10→80→150) |
| `TestChain_MultiAgent_ParallelChains` | 5 Agent 同时执行各自 3 步推理链，竞争共享工具 |
| `TestChain_BranchingWithOptionalSteps` | 可选步骤失败不影响主链完成 |
| `TestChain_BudgetStrategy_Comparison` | 4 种策略在 5 步推理链上的完成率和效率对比 |
| `TestChain_BatchExperiment` | N=20 批量实验，收集链完成率、平均步骤数、平均预算消耗统计 |
| `TestChain_UnderCompetition` | 3 推理链 Agent + 5 干扰 Agent 同时运行，观察链完成率下降 |
| `TestChain_FanOutFanIn` | Fan-out/Fan-in 模式：init → [3 并行分支] → merge |

## 环境要求

- **Go**: 1.23.0+
- **操作系统**: Windows / Linux / macOS

## 如何运行测试

### 1. 运行全部测试

```bash
# 在项目根目录执行
cd ra-annotion-demo

# 运行全部测试（包括根目录和 mcp_test/ 子目录）
go test ./... -v
```

### 2. 分模块运行测试

```bash
# 只运行根目录下的核心单元测试（治理引擎 + HTTP 传输层）
go test -v

# 只运行 mcp_test/ 目录下的集成测试
go test ./mcp_test/ -v

# 只运行 agent_test/ 目录下的 Agent 场景测试
go test ./agent_test/ -v -timeout 2m
```

### 3. 按测试类别运行

```bash
# --- 负载削减 (Load Shedding) 相关测试 ---
go test ./mcp_test/ -v -run "TestLoadShedding"

# --- 客户端限流 (Rate Limiting) 相关测试 ---
go test ./mcp_test/ -v -run "TestRateLimiting"

# --- 动态定价 (Dynamic Pricing) 相关测试 ---
go test ./mcp_test/ -v -run "TestDynamicPricing"

# --- 端到端集成测试 ---
go test ./mcp_test/ -v -run "TestE2E"

# --- 泊松突发流量压力测试 ---
go test ./mcp_test/ -v -run "TestPoisson" -timeout 2m

# --- Agent 多 Agent 竞争资源测试 ---
go test ./agent_test/ -v -run "TestCompetition"

# --- Agent 预算管理测试 ---
go test ./agent_test/ -v -run "TestBudget"

# --- Agent 多步推理链测试 ---
go test ./agent_test/ -v -run "TestChain"
```

### 4. 运行单个测试用例

```bash
# 运行某个具体测试（以"基础准入控制测试"为例）
go test ./mcp_test/ -v -run "TestLoadShedding_BasicAdmission"

# 运行高并发价格自适应测试
go test -v -run "TestMCPServer_HighConcurrency"

# 运行多工具链路场景
go test ./mcp_test/ -v -run "TestE2E_MultiToolChain"
```

### 5. 运行性能基准测试 (Benchmark)

```bash
# 运行负载削减性能基准
go test ./mcp_test/ -bench "BenchmarkLoadShedding" -benchmem

# 运行所有基准测试
go test ./mcp_test/ -bench . -benchmem
```

### 6. 跳过长时间运行的测试

```bash
# 使用 -short 标志跳过长时间稳定性测试
go test ./... -v -short
```

### 7. 设置超时时间

部分测试（如高并发测试、稳定性测试）执行时间较长，建议合理设置超时：

```bash
# 设置 2 分钟超时（默认为 10 分钟）
go test ./... -v -timeout 2m

# 仅跑快速测试（排除 E2E 和高并发）
go test ./... -v -run "Test(LoadShedding_Basic|RateLimiting_Token|DynamicPricing_Step|HandleToolCall|JSONRPC)"
```

## 测试列表速查

### 核心单元测试 (根目录)

| 测试函数 | 说明 |
|---------|------|
| `TestHandleToolCall_RejectsLowTokens` | 低令牌请求被拒绝 |
| `TestHandleToolCall_AllowsHighTokens` | 高令牌请求通过 |
| `TestHandleToolCall_MixedTokens` | 混合流量（半拒半通） |
| `TestDownstreamPrice_StorageAndRetrieval` | 下游价格存储与检索 |
| `TestLoadShedding_ReturnsCorrectPrice` | LoadShedding 扣费逻辑 |
| `TestJSONRPCProtocol_MessageFormat` | JSON-RPC 消息格式验证 |
| `TestMCPServer_Initialize` | MCP 握手 |
| `TestMCPServer_ToolsList` | 工具列表 |
| `TestMCPServer_ToolCallGovernance` | HTTP 工具调用治理 |
| `TestMCPServer_HighConcurrency` | 高并发压力测试 |
| `TestMCPServer_Ping` | 健康检查 |
| `TestMCPServer_InvalidMethod` | 无效方法名拒绝 |

### 负载削减测试 (`mcp_test/load_shedding_test.go`)

| 测试函数 | 说明 |
|---------|------|
| `TestLoadShedding_BasicAdmission` | 基础准入控制 (5种令牌场景) |
| `TestLoadShedding_RejectRateUnderOverload` | 过载拒绝率 (≥95%) |
| `TestLoadShedding_SelectiveAdmission` | 高/低预算选择性准入 |
| `TestLoadShedding_ConcurrentProtection` | 50并发负载保护 |
| `TestLoadShedding_PriceInErrorResponse` | 拒绝响应携带价格 |
| `TestLoadShedding_ZeroPricePassesAll` | 零价格全部放行 |
| `TestLoadShedding_GradualPriceIncrease` | 渐进涨价拒绝率递增 |
| `TestLoadShedding_DisabledMode` | 关闭负载削减全放行 |
| `TestLoadShedding_ThroughputProtection` | 吞吐量触发涨价 |
| `TestLoadShedding_ResponseContainsPrice` | 成功响应包含价格 |
| `TestLoadShedding_PriceAggregation` | Maximal 聚合策略对比 |

### 限流测试 (`mcp_test/rate_limiting_test.go`)

| 测试函数 | 说明 |
|---------|------|
| `TestRateLimiting_TokenDeduction` | 令牌扣除逻辑 |
| `TestRateLimiting_TokenRefill_Fixed` | 固定速率令牌补充 |
| `TestRateLimiting_TokenAdd` | 令牌添加 |
| `TestRateLimiting_ConcurrentTokenOps` | 并发令牌安全性 |
| `TestClientMiddleware_InjectMeta` | 中间件注入 _meta |
| `TestClientMiddleware_RateLimitBlock` | 限流阻止请求 |
| `TestClientMiddleware_BackoffMechanism` | 退避机制 |
| `TestRateLimiting_Check` | RateLimiting 方法 |
| `TestRateLimiting_EndToEnd_WithServer` | 客户端+服务端联动 |
| `TestRateLimiting_UpdateResponsePrice` | 价格缓存更新 |

### 动态定价测试 (`mcp_test/dynamic_pricing_test.go`)

| 测试函数 | 说明 |
|---------|------|
| `TestDynamicPricing_StepStrategy_Congestion` | Step策略拥塞涨价 |
| `TestDynamicPricing_StepStrategy_Recovery` | Step策略非拥塞降价 |
| `TestDynamicPricing_StepStrategy_FloorAtZero` | 价格不降为负 |
| `TestDynamicPricing_GuidePrice` | 指导价格机制 |
| `TestDynamicPricing_ExpDecay_DampenOscillation` | 指数衰减抑制震荡 |
| `TestDynamicPricing_ExpDecay_ResetOnDecrease` | 衰减计数器重置 |
| `TestDynamicPricing_ReservePrice` | 底价保护 |
| `TestDynamicPricing_OverloadThenRecovery_E2E` | 过载→恢复完整周期 |
| `TestDynamicPricing_DownstreamPropagation` | 下游价格传播 (Maximal) |
| `TestDynamicPricing_AdditiveAggregation` | Additive 聚合累加 |
| `TestDynamicPricing_MeanAggregation` | Mean 聚合平均值 |

### 端到端测试 (`mcp_test/e2e_governance_test.go`)

| 测试函数 | 说明 |
|---------|------|
| `TestE2E_MultiToolChain` | 多工具链路 (网关→天气+酒店) |
| `TestE2E_SplitTokens` | 令牌分配给多下游 |
| `TestE2E_ProgressiveOverload` | 渐进式过载 (10→50→100→200并发) |
| `TestE2E_BurstTraffic` | 脉冲式突发流量 |
| `TestE2E_Fairness_HighBudgetPreference` | 预算公平性 |
| `TestE2E_HandleToolCallDirect` | 直接调用模式 |
| `TestE2E_LongRunningStability` | 10秒长时间稳定性 |
| `TestE2E_PriceMetaRoundTrip` | 价格元信息完整往返 |

### 泊松突发流量测试 (`mcp_test/poisson_burst_test.go`)

| 测试函数 | 说明 |
|---------|------|
| `TestPoisson_ThroughputDriven` | 吞吐量驱动泊松到达：不同 λ (50/200/500/2000) 下，验证 `throughputCheck` 路径的拒绝率随 λ 递增 (0%→56%→91%) |
| `TestPoisson_QueuingDriven` | 排队延迟驱动泊松到达：`GOMAXPROCS(2)` + CPU 忙等 (500μs)，验证 `queuingCheck` 路径在调度瓶颈下的涨价与拒绝 |
| `TestPoisson_VariableRate` | 非齐次泊松过程 (NHPP)：λ 随时间变化 (30→200→1500→50→20)，验证治理引擎对流量变化的动态响应（突发期 85%+ 拒绝 → 恢复期 0%） |
| `TestPoisson_CompoundBurst` | 复合泊松突发：外层泊松事件 (λ=15/s) + 内层泊松批量 (μ=12)，模拟 AI Agent 并行工具调用，观察价格脉冲轨迹 |
| `TestPoisson_SpikeAmplitude` | 突发振幅对比：固定等效 RPS≈100，从均匀 (100×1) 到极端突发 (5×20)，验证越突发→峰值价格越高→拒绝率越高 |
| `TestPoisson_ClientTokenRefill` | 双重随机系统：客户端 `tokenRefillDist="poisson"` 泊松令牌补充 + 服务端泊松流量到达，验证多层限流联动效果 |
| `TestPoisson_SustainedBurst` | 持续泊松冲击 (λ=1000, 10秒)：验证治理引擎在长时间持续高负载下的稳定性，每秒输出价格与拒绝率快照 |

## 架构概览

```
                    ┌──────────────────────────────────────┐
                    │           AI Agent (Client)           │
                    │  ClientMiddleware: 令牌注入 + 限流检查  │
                    └───────────────┬──────────────────────┘
                                    │  JSON-RPC 2.0 (tools/call)
                                    │  _meta: {tokens: N}
                                    ▼
                    ┌──────────────────────────────────────┐
                    │         MCPServer (HTTP 层)           │
                    │  ServeHTTP → handleToolsCall          │
                    └───────────────┬──────────────────────┘
                                    │
                                    ▼
                    ┌──────────────────────────────────────┐
                    │       MCPGovernor (治理引擎)          │
                    │                                      │
                    │  1. HandleToolCall (中间件入口)        │
                    │  2. LoadShedding (准入控制)            │
                    │     tokens >= price → 放行             │
                    │     tokens <  price → 拒绝             │
                    │  3. 调用实际工具处理函数                 │
                    │  4. 响应 _meta 附加当前 price           │
                    └───────────────┬──────────────────────┘
                                    │
              ┌─────────────────────┼─────────────────────┐
              ▼                     ▼                     ▼
  ┌──────────────────┐  ┌──────────────────┐  ┌──────────────────┐
  │  queuingCheck    │  │ throughputCheck   │  │  latencyCheck    │
  │ (调度器延迟检测)  │  │  (吞吐量检测)     │  │  (业务延迟检测)   │
  └────────┬─────────┘  └────────┬─────────┘  └────────┬─────────┘
           │                     │                     │
           └─────────────────────┼─────────────────────┘
                                 ▼
                    ┌──────────────────────────────────────┐
                    │  UpdateOwnPrice / UpdatePrice         │
                    │  动态调整 price (Step / ExpDecay)      │
                    └──────────────────────────────────────┘
```

## License


