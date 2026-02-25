// helpers_test.go
// Agent 场景测试辅助框架
// 提供模拟 AI Agent 的核心抽象：Agent、Task、Budget、Metrics 收集器
// 所有 agent_test 下的测试文件共享此辅助基础设施
package agent_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"

	mcpgov "mcp-governance"
)

// ==================== Agent 模拟器核心类型 ====================

// SimulatedAgent 模拟一个 AI Agent
// 每个 Agent 有自己的预算 (令牌池)、任务队列、以及行为策略
type SimulatedAgent struct {
	Name          string         // Agent 名称 (如 "agent-1")
	InitialBudget int64          // 初始预算 (总令牌数)
	BudgetLeft    int64          // 剩余预算 (原子操作)
	TokensPerCall int64          // 每次工具调用携带的令牌数 (0 表示用剩余全部预算)
	Strategy      BudgetStrategy // 预算分配策略
	Tasks         []AgentTask    // 任务队列 (按顺序执行)
	ThinkTime     time.Duration  // 模拟 Agent 思考时间 (两次调用间隔)

	// 统计指标
	metrics AgentMetrics
}

// BudgetStrategy Agent 预算分配策略
type BudgetStrategy int

const (
	// StrategyFixed 固定令牌：每次调用携带固定数量的令牌
	StrategyFixed BudgetStrategy = iota
	// StrategyEqualSplit 均分策略：将剩余预算均匀分配给剩余任务
	StrategyEqualSplit
	// StrategyFrontLoaded 前置策略：前期多投入, 后期少投入
	StrategyFrontLoaded
	// StrategyAdaptive 自适应策略：根据服务端返回的价格动态调整
	StrategyAdaptive
)

// AgentTask 表示 Agent 需要执行的一个工具调用任务
type AgentTask struct {
	ToolName   string                 // 调用的工具名称
	Arguments  map[string]interface{} // 工具参数
	DependsOn  int                    // 依赖的前序任务索引 (-1 表示无依赖)
	Required   bool                   // 是否为必须完成的任务 (失败则中断整个链)
	MaxRetries int                    // 最大重试次数
}

// AgentMetrics Agent 执行统计指标
type AgentMetrics struct {
	TotalCalls     int64         // 总调用次数
	SuccessCalls   int64         // 成功次数
	RejectedCalls  int64         // 被拒绝次数 (过载/令牌不足)
	RetriedCalls   int64         // 重试次数
	BudgetSpent    int64         // 已消耗预算
	ChainCompleted bool          // 推理链是否完整完成
	StepsCompleted int           // 已完成的步骤数
	TotalLatency   time.Duration // 总延迟
	Errors         []string      // 错误信息收集
}

// ==================== 全局指标收集器 ====================

// TestMetrics 测试全局指标收集器
type TestMetrics struct {
	mu            sync.Mutex
	agentResults  map[string]*AgentMetrics
	totalRequests int64
	totalSuccess  int64
	totalRejected int64
	startTime     time.Time
}

// NewTestMetrics 创建指标收集器
func NewTestMetrics() *TestMetrics {
	return &TestMetrics{
		agentResults: make(map[string]*AgentMetrics),
		startTime:    time.Now(),
	}
}

// Record 记录一个 Agent 的最终指标
func (m *TestMetrics) Record(agentName string, metrics *AgentMetrics) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.agentResults[agentName] = metrics
	atomic.AddInt64(&m.totalRequests, metrics.TotalCalls)
	atomic.AddInt64(&m.totalSuccess, metrics.SuccessCalls)
	atomic.AddInt64(&m.totalRejected, metrics.RejectedCalls)
}

// Summary 生成摘要字符串
func (m *TestMetrics) Summary() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	elapsed := time.Since(m.startTime)
	s := fmt.Sprintf("=== 测试摘要 (耗时 %v) ===\n", elapsed.Round(time.Millisecond))
	s += fmt.Sprintf("  总请求: %d, 成功: %d, 拒绝: %d\n", m.totalRequests, m.totalSuccess, m.totalRejected)
	if m.totalRequests > 0 {
		s += fmt.Sprintf("  成功率: %.1f%%\n", float64(m.totalSuccess)/float64(m.totalRequests)*100)
	}
	for name, am := range m.agentResults {
		s += fmt.Sprintf("  [%s] 调用=%d 成功=%d 拒绝=%d 重试=%d 预算消耗=%d 链完成=%v 步骤=%d\n",
			name, am.TotalCalls, am.SuccessCalls, am.RejectedCalls, am.RetriedCalls,
			am.BudgetSpent, am.ChainCompleted, am.StepsCompleted)
	}
	return s
}

// JainFairnessIndex 计算 Jain 公平性指数
// 输入：各 Agent 的成功调用次数。J(x) = (Σxi)² / (n * Σxi²), 范围 [1/n, 1]
func (m *TestMetrics) JainFairnessIndex() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.agentResults)
	if n == 0 {
		return 0
	}
	var sumX, sumX2 float64
	for _, am := range m.agentResults {
		x := float64(am.SuccessCalls)
		sumX += x
		sumX2 += x * x
	}
	if sumX2 == 0 {
		return 1.0 // 全部为 0，认为完全公平
	}
	return (sumX * sumX) / (float64(n) * sumX2)
}

// ==================== Agent 执行引擎 ====================

// allocateTokens 根据策略计算本次调用应携带的令牌数
func (a *SimulatedAgent) allocateTokens(taskIdx int, lastPrice int64) int64 {
	remaining := atomic.LoadInt64(&a.BudgetLeft)
	if remaining <= 0 {
		return 0
	}
	tasksLeft := len(a.Tasks) - taskIdx
	if tasksLeft <= 0 {
		tasksLeft = 1
	}

	switch a.Strategy {
	case StrategyFixed:
		tok := a.TokensPerCall
		if tok > remaining {
			tok = remaining
		}
		return tok

	case StrategyEqualSplit:
		tok := remaining / int64(tasksLeft)
		if tok <= 0 {
			tok = 1
		}
		return tok

	case StrategyFrontLoaded:
		// 前 30% 的任务获得 60% 的预算
		total := len(a.Tasks)
		frontCount := int(math.Ceil(float64(total) * 0.3))
		if taskIdx < frontCount {
			tok := (remaining * 60) / (100 * int64(frontCount-taskIdx))
			if tok > remaining {
				tok = remaining
			}
			return tok
		}
		tok := remaining / int64(tasksLeft)
		if tok <= 0 {
			tok = 1
		}
		return tok

	case StrategyAdaptive:
		// 根据上次返回的价格来决定: 至少给 price * 1.2
		if lastPrice > 0 {
			tok := int64(float64(lastPrice) * 1.2)
			if tok > remaining {
				tok = remaining
			}
			return tok
		}
		// 首次调用使用均分
		tok := remaining / int64(tasksLeft)
		if tok <= 0 {
			tok = 1
		}
		return tok
	}
	return remaining
}

// ExecuteChain 执行 Agent 的完整任务链
// 返回指标统计，按任务顺序逐步执行，支持重试和依赖检查
func (a *SimulatedAgent) ExecuteChain(serverURL string) *AgentMetrics {
	a.metrics = AgentMetrics{}
	stepResults := make([]bool, len(a.Tasks)) // 每步是否成功
	var lastPrice int64

	for i, task := range a.Tasks {
		// 依赖检查
		if task.DependsOn >= 0 && task.DependsOn < i {
			if !stepResults[task.DependsOn] {
				a.metrics.Errors = append(a.metrics.Errors,
					fmt.Sprintf("步骤 %d (%s) 因依赖步骤 %d 失败而跳过", i, task.ToolName, task.DependsOn))
				if task.Required {
					a.metrics.ChainCompleted = false
					return &a.metrics
				}
				continue
			}
		}

		// 模拟思考时间
		if i > 0 && a.ThinkTime > 0 {
			time.Sleep(a.ThinkTime)
		}

		// 重试循环
		success := false
		retries := task.MaxRetries
		if retries <= 0 {
			retries = 0
		}

		for attempt := 0; attempt <= retries; attempt++ {
			if attempt > 0 {
				a.metrics.RetriedCalls++
				time.Sleep(time.Duration(attempt) * 5 * time.Millisecond) // 指数退避
			}

			tokens := a.allocateTokens(i, lastPrice)
			if tokens <= 0 {
				a.metrics.Errors = append(a.metrics.Errors,
					fmt.Sprintf("步骤 %d (%s) 预算耗尽", i, task.ToolName))
				break
			}

			atomic.AddInt64(&a.BudgetLeft, -tokens)
			a.metrics.BudgetSpent += tokens
			a.metrics.TotalCalls++

			start := time.Now()
			resp := sendAgentRequest(serverURL, task.ToolName, tokens, a.Name)
			a.metrics.TotalLatency += time.Since(start)

			if resp == nil {
				a.metrics.Errors = append(a.metrics.Errors,
					fmt.Sprintf("步骤 %d (%s) 网络错误", i, task.ToolName))
				continue
			}

			if resp.Error != nil {
				a.metrics.RejectedCalls++
				// 从拒绝响应解析价格
				if data, ok := resp.Error.Data.(map[string]interface{}); ok {
					if p, ok := data["price"].(string); ok {
						fmt.Sscanf(p, "%d", &lastPrice)
					}
				}
				// 被拒绝时退还部分令牌 (模拟未实际消费)
				refund := tokens / 2
				atomic.AddInt64(&a.BudgetLeft, refund)
				a.metrics.BudgetSpent -= refund
				continue
			}

			// 成功
			a.metrics.SuccessCalls++
			success = true

			// 从成功响应解析价格
			if resultMap, ok := resp.Result.(map[string]interface{}); ok {
				if meta, ok := resultMap["_meta"].(map[string]interface{}); ok {
					if p, ok := meta["price"].(string); ok {
						fmt.Sscanf(p, "%d", &lastPrice)
					}
				}
			}
			break
		}

		stepResults[i] = success
		if success {
			a.metrics.StepsCompleted++
		} else if task.Required {
			a.metrics.ChainCompleted = false
			return &a.metrics
		}
	}

	a.metrics.ChainCompleted = (a.metrics.StepsCompleted == len(a.Tasks))
	return &a.metrics
}

// ExecuteConcurrentCalls 并发执行多次独立工具调用 (非链式)
// 适用于多 Agent 竞争场景
func (a *SimulatedAgent) ExecuteConcurrentCalls(serverURL string, toolName string, count int, interval time.Duration) *AgentMetrics {
	a.metrics = AgentMetrics{}
	var lastPrice int64

	for i := 0; i < count; i++ {
		if i > 0 && interval > 0 {
			time.Sleep(interval)
		}

		tokens := a.allocateTokens(i, lastPrice)
		if tokens <= 0 {
			a.metrics.Errors = append(a.metrics.Errors, "预算耗尽, 停止调用")
			break
		}

		atomic.AddInt64(&a.BudgetLeft, -tokens)
		a.metrics.BudgetSpent += tokens
		a.metrics.TotalCalls++

		start := time.Now()
		resp := sendAgentRequest(serverURL, toolName, tokens, a.Name)
		a.metrics.TotalLatency += time.Since(start)

		if resp == nil {
			continue
		}

		if resp.Error != nil {
			a.metrics.RejectedCalls++
			if data, ok := resp.Error.Data.(map[string]interface{}); ok {
				if p, ok := data["price"].(string); ok {
					fmt.Sscanf(p, "%d", &lastPrice)
				}
			}
			refund := tokens / 2
			atomic.AddInt64(&a.BudgetLeft, refund)
			a.metrics.BudgetSpent -= refund
		} else {
			a.metrics.SuccessCalls++
			if resultMap, ok := resp.Result.(map[string]interface{}); ok {
				if meta, ok := resultMap["_meta"].(map[string]interface{}); ok {
					if p, ok := meta["price"].(string); ok {
						fmt.Sscanf(p, "%d", &lastPrice)
					}
				}
			}
		}
	}
	return &a.metrics
}

// ==================== 服务端辅助函数 ====================

// agentTestOpts Agent 测试的默认服务端配置
func agentTestOpts() map[string]interface{} {
	return map[string]interface{}{
		"priceUpdateRate":  5000 * time.Microsecond,
		"tokenUpdateRate":  100000 * time.Microsecond,
		"latencyThreshold": 500 * time.Microsecond,
		"priceStep":        int64(180),
		"priceStrategy":    "expdecay",
		"lazyResponse":     false,
		"rateLimiting":     true,
		"loadShedding":     true,
		"priceFreq":        int64(1), // 每次都返回价格
	}
}

// newAgentTestServer 创建 Agent 测试专用服务端 (支持多工具注册)
func newAgentTestServer(tools []ToolDef, opts map[string]interface{}) (*httptest.Server, *mcpgov.MCPGovernor) {
	callMap := make(map[string][]string)
	for _, t := range tools {
		callMap[t.Name] = t.Downstream
	}
	gov := mcpgov.NewMCPGovernor("agent-test-node", callMap, opts)
	server := mcpgov.NewMCPServer("agent-test-service", gov)
	for _, t := range tools {
		server.RegisterTool(mcpgov.MCPTool{
			Name:        t.Name,
			Description: t.Description,
			InputSchema: map[string]interface{}{"type": "object"},
		}, t.Handler)
	}
	return httptest.NewServer(server), gov
}

// ToolDef 工具定义
type ToolDef struct {
	Name        string
	Description string
	Downstream  []string
	Handler     mcpgov.ToolCallHandler
}

// simpleOKHandler 返回成功的工具处理器
func simpleOKHandler(text string) mcpgov.ToolCallHandler {
	return func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		return &mcpgov.MCPToolCallResult{
			Content: []mcpgov.ContentBlock{mcpgov.TextContent(text)},
		}, nil
	}
}

// slowHandler 模拟耗时工具 (制造负载)
func slowHandler(delay time.Duration) mcpgov.ToolCallHandler {
	return func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		time.Sleep(delay)
		return &mcpgov.MCPToolCallResult{
			Content: []mcpgov.ContentBlock{mcpgov.TextContent("done")},
		}, nil
	}
}

// cpuBusyHandler 模拟 CPU 密集型工具
func cpuBusyHandler(duration time.Duration) mcpgov.ToolCallHandler {
	return func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		deadline := time.Now().Add(duration)
		x := 0.1
		for time.Now().Before(deadline) {
			for j := 0; j < 1000; j++ {
				x = math.Sin(x) + 0.1
			}
		}
		_ = x
		return &mcpgov.MCPToolCallResult{
			Content: []mcpgov.ContentBlock{mcpgov.TextContent("computed")},
		}, nil
	}
}

// sendAgentRequest 发送 Agent 的工具调用请求
func sendAgentRequest(serverURL string, toolName string, tokens int64, agentName string) *mcpgov.JSONRPCResponse {
	params := mcpgov.MCPToolCallParams{
		Name:      toolName,
		Arguments: map[string]interface{}{},
		Meta:      &mcpgov.GovernanceMeta{Tokens: tokens, Method: toolName, Name: agentName},
	}
	req, _ := mcpgov.NewJSONRPCRequest(1, "tools/call", params)
	body, _ := json.Marshal(req)
	httpResp, err := http.Post(serverURL, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil
	}
	defer httpResp.Body.Close()
	respBody, _ := io.ReadAll(httpResp.Body)
	var resp mcpgov.JSONRPCResponse
	json.Unmarshal(respBody, &resp)
	return &resp
}

// runAgentsParallel 并行启动多个 Agent 执行任务
func runAgentsParallel(agents []*SimulatedAgent, fn func(a *SimulatedAgent) *AgentMetrics) *TestMetrics {
	metrics := NewTestMetrics()
	var wg sync.WaitGroup

	for _, agent := range agents {
		wg.Add(1)
		go func(a *SimulatedAgent) {
			defer wg.Done()
			result := fn(a)
			metrics.Record(a.Name, result)
		}(agent)
	}

	wg.Wait()
	return metrics
}

// makeAgents 批量创建同质 Agent
func makeAgents(count int, budget int64, tokensPerCall int64, strategy BudgetStrategy) []*SimulatedAgent {
	agents := make([]*SimulatedAgent, count)
	for i := 0; i < count; i++ {
		agents[i] = &SimulatedAgent{
			Name:          fmt.Sprintf("agent-%d", i+1),
			InitialBudget: budget,
			BudgetLeft:    budget,
			TokensPerCall: tokensPerCall,
			Strategy:      strategy,
			ThinkTime:     2 * time.Millisecond,
		}
	}
	return agents
}
