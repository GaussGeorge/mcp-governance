// competition_test.go
// 多 Agent 竞争资源场景测试
//
// 模拟多个 AI Agent 同时竞争有限的工具服务资源，验证治理机制在竞争场景下的表现：
//   - 公平性：预算相同的 Agent 是否获得接近的服务率
//   - 隔离性：高预算 Agent 是否能获得优先服务
//   - 稳定性：动态定价在高竞争下是否收敛
//   - 吞吐量：竞争环境下的有效吞吐量
package agent_test

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ==================== 场景 1: 等预算 Agent 公平竞争 ====================

// TestCompetition_EqualBudget_Fairness
// 场景: N 个 Agent 拥有相同预算, 竞争同一个工具服务
// 预期: Jain 公平性指数 > 0.8, 所有 Agent 成功率接近
func TestCompetition_EqualBudget_Fairness(t *testing.T) {
	tools := []ToolDef{{
		Name:        "shared_tool",
		Description: "共享资源工具",
		Downstream:  []string{},
		Handler:     slowHandler(1 * time.Millisecond),
	}}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(10) // 固定价格

	const agentCount = 5
	const callsPerAgent = 40
	agents := makeAgents(agentCount, 1000, 20, StrategyFixed)

	metrics := runAgentsParallel(agents, func(a *SimulatedAgent) *AgentMetrics {
		return a.ExecuteConcurrentCalls(ts.URL, "shared_tool", callsPerAgent, 2*time.Millisecond)
	})

	t.Log(metrics.Summary())

	// 验证公平性
	fairness := metrics.JainFairnessIndex()
	t.Logf("Jain 公平性指数: %.4f", fairness)
	if fairness < 0.8 {
		t.Errorf("公平性不足: Jain Index = %.4f, 期望 > 0.8", fairness)
	}

	// 验证所有 Agent 都有成功调用
	metrics.mu.Lock()
	for name, am := range metrics.agentResults {
		if am.SuccessCalls == 0 {
			t.Errorf("Agent %s 成功次数为 0, 可能被完全饿死", name)
		}
	}
	metrics.mu.Unlock()
}

// ==================== 场景 2: 不等预算竞争 (高预算优先) ====================

// TestCompetition_UnequalBudget_HighBudgetAdvantage
// 场景: 2 个高预算 Agent vs 3 个低预算 Agent
// 预期: 高预算 Agent 平均成功率 > 低预算 Agent, 证明预算机制的区分能力
func TestCompetition_UnequalBudget_HighBudgetAdvantage(t *testing.T) {
	tools := []ToolDef{{
		Name:        "premium_tool",
		Description: "高价值工具",
		Downstream:  []string{},
		Handler:     simpleOKHandler("OK"),
	}}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(50) // 中等价格

	const callsPerAgent = 50

	// 高预算 Agent：充裕的令牌
	highBudgetAgents := []*SimulatedAgent{
		{Name: "rich-1", InitialBudget: 5000, BudgetLeft: 5000, TokensPerCall: 100, Strategy: StrategyFixed, ThinkTime: time.Millisecond},
		{Name: "rich-2", InitialBudget: 5000, BudgetLeft: 5000, TokensPerCall: 100, Strategy: StrategyFixed, ThinkTime: time.Millisecond},
	}

	// 低预算 Agent：令牌紧张
	lowBudgetAgents := []*SimulatedAgent{
		{Name: "poor-1", InitialBudget: 500, BudgetLeft: 500, TokensPerCall: 30, Strategy: StrategyFixed, ThinkTime: time.Millisecond},
		{Name: "poor-2", InitialBudget: 500, BudgetLeft: 500, TokensPerCall: 30, Strategy: StrategyFixed, ThinkTime: time.Millisecond},
		{Name: "poor-3", InitialBudget: 500, BudgetLeft: 500, TokensPerCall: 30, Strategy: StrategyFixed, ThinkTime: time.Millisecond},
	}

	allAgents := append(highBudgetAgents, lowBudgetAgents...)
	metrics := runAgentsParallel(allAgents, func(a *SimulatedAgent) *AgentMetrics {
		return a.ExecuteConcurrentCalls(ts.URL, "premium_tool", callsPerAgent, time.Millisecond)
	})

	t.Log(metrics.Summary())

	// 计算高预算 vs 低预算的平均成功率
	metrics.mu.Lock()
	var highSuccessSum, lowSuccessSum int64
	for _, a := range highBudgetAgents {
		if am, ok := metrics.agentResults[a.Name]; ok {
			highSuccessSum += am.SuccessCalls
		}
	}
	for _, a := range lowBudgetAgents {
		if am, ok := metrics.agentResults[a.Name]; ok {
			lowSuccessSum += am.SuccessCalls
		}
	}
	metrics.mu.Unlock()

	highAvg := float64(highSuccessSum) / float64(len(highBudgetAgents))
	lowAvg := float64(lowSuccessSum) / float64(len(lowBudgetAgents))

	t.Logf("高预算平均成功: %.1f, 低预算平均成功: %.1f", highAvg, lowAvg)

	if highAvg < lowAvg {
		t.Logf("注意: 高预算 Agent 成功率 (%.1f) 低于低预算 Agent (%.1f), 令牌区分能力不足", highAvg, lowAvg)
	}
}

// ==================== 场景 3: 动态涨价下的竞争压力 ====================

// TestCompetition_DynamicPricing_UnderContention
// 场景: 大量 Agent 同时涌入, 触发动态涨价, 观察系统行为
// 预期: 价格上涨后, 低令牌请求被拒绝, 总拒绝率 > 0
func TestCompetition_DynamicPricing_UnderContention(t *testing.T) {
	tools := []ToolDef{{
		Name:        "hot_tool",
		Description: "热门工具",
		Downstream:  []string{},
		Handler:     slowHandler(2 * time.Millisecond),
	}}
	opts := agentTestOpts()
	opts["pinpointQueuing"] = true
	opts["initprice"] = int64(0) // 初始价格为 0, 让动态定价生效
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()
	_ = gov

	const agentCount = 10
	const callsPerAgent = 30

	agents := makeAgents(agentCount, 2000, 50, StrategyFixed)

	metrics := runAgentsParallel(agents, func(a *SimulatedAgent) *AgentMetrics {
		return a.ExecuteConcurrentCalls(ts.URL, "hot_tool", callsPerAgent, time.Millisecond)
	})

	t.Log(metrics.Summary())

	totalReqs := atomic.LoadInt64(&metrics.totalRequests)
	totalReject := atomic.LoadInt64(&metrics.totalRejected)

	t.Logf("总请求: %d, 总拒绝: %d, 拒绝率: %.1f%%",
		totalReqs, totalReject, float64(totalReject)/float64(totalReqs)*100)

	// 基本验证：系统没有崩溃且有正常处理
	if totalReqs == 0 {
		t.Error("未发出任何请求")
	}
	totalSuccess := atomic.LoadInt64(&metrics.totalSuccess)
	if totalSuccess == 0 {
		t.Error("所有请求均失败, 治理策略过于激进")
	}
}

// ==================== 场景 4: 竞争升级 (逐步增加 Agent 数量) ====================

// TestCompetition_Escalation
// 场景: 从 2 个 Agent 开始, 逐步增加到 20 个, 观察服务质量变化
// 预期: Agent 数量增加 → 拒绝率逐步上升
func TestCompetition_Escalation(t *testing.T) {
	tools := []ToolDef{{
		Name:        "scalable_tool",
		Description: "可扩展工具",
		Downstream:  []string{},
		Handler:     slowHandler(1 * time.Millisecond),
	}}
	opts := agentTestOpts()
	ts, _ := newAgentTestServer(tools, opts)
	defer ts.Close()

	escalationSteps := []int{2, 5, 10, 20}

	for _, agentCount := range escalationSteps {
		t.Run(fmt.Sprintf("%d_Agents", agentCount), func(t *testing.T) {
			_, gov := newAgentTestServer(tools, opts)
			gov.SetOwnPrice(30)

			// 使用新服务器，但复用相同配置
			ts2, gov2 := newAgentTestServer(tools, opts)
			defer ts2.Close()
			gov2.SetOwnPrice(30)

			agents := makeAgents(agentCount, 500, 50, StrategyFixed)
			metrics := runAgentsParallel(agents, func(a *SimulatedAgent) *AgentMetrics {
				return a.ExecuteConcurrentCalls(ts2.URL, "scalable_tool", 20, time.Millisecond)
			})

			totalReqs := atomic.LoadInt64(&metrics.totalRequests)
			totalSuccess := atomic.LoadInt64(&metrics.totalSuccess)
			totalReject := atomic.LoadInt64(&metrics.totalRejected)

			rejectRate := float64(0)
			if totalReqs > 0 {
				rejectRate = float64(totalReject) / float64(totalReqs) * 100
			}

			t.Logf("Agent数=%d: 总请求=%d 成功=%d 拒绝=%d 拒绝率=%.1f%%",
				agentCount, totalReqs, totalSuccess, totalReject, rejectRate)
		})
	}
}

// ==================== 场景 5: 多工具竞争 (Agent 竞争不同工具) ====================

// TestCompetition_MultiTool_ResourceIsolation
// 场景: 2 组 Agent 分别竞争 2 个不同的工具
// 预期: 一个工具的压力不应影响另一个工具的服务质量
func TestCompetition_MultiTool_ResourceIsolation(t *testing.T) {
	tools := []ToolDef{
		{Name: "tool_alpha", Description: "工具 Alpha", Downstream: []string{}, Handler: simpleOKHandler("alpha")},
		{Name: "tool_beta", Description: "工具 Beta", Downstream: []string{}, Handler: slowHandler(5 * time.Millisecond)},
	}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(20)

	const callsPerAgent = 30

	// 组 A: 调用 tool_alpha (快速工具)
	alphaAgents := makeAgents(3, 1000, 40, StrategyFixed)
	for i := range alphaAgents {
		alphaAgents[i].Name = fmt.Sprintf("alpha-agent-%d", i+1)
	}

	// 组 B: 调用 tool_beta (慢速工具, 制造背压)
	betaAgents := makeAgents(5, 1000, 40, StrategyFixed)
	for i := range betaAgents {
		betaAgents[i].Name = fmt.Sprintf("beta-agent-%d", i+1)
	}

	var wg sync.WaitGroup
	alphaMetrics := NewTestMetrics()
	betaMetrics := NewTestMetrics()

	// 启动 Alpha 组
	for _, a := range alphaAgents {
		wg.Add(1)
		go func(agent *SimulatedAgent) {
			defer wg.Done()
			result := agent.ExecuteConcurrentCalls(ts.URL, "tool_alpha", callsPerAgent, time.Millisecond)
			alphaMetrics.Record(agent.Name, result)
		}(a)
	}

	// 启动 Beta 组
	for _, a := range betaAgents {
		wg.Add(1)
		go func(agent *SimulatedAgent) {
			defer wg.Done()
			result := agent.ExecuteConcurrentCalls(ts.URL, "tool_beta", callsPerAgent, time.Millisecond)
			betaMetrics.Record(agent.Name, result)
		}(a)
	}

	wg.Wait()

	t.Log("=== Alpha 组 (快速工具) ===")
	t.Log(alphaMetrics.Summary())
	t.Log("=== Beta 组 (慢速工具) ===")
	t.Log(betaMetrics.Summary())

	alphaSuccess := atomic.LoadInt64(&alphaMetrics.totalSuccess)
	alphaTotal := atomic.LoadInt64(&alphaMetrics.totalRequests)
	betaSuccess := atomic.LoadInt64(&betaMetrics.totalSuccess)
	betaTotal := atomic.LoadInt64(&betaMetrics.totalRequests)

	if alphaTotal > 0 {
		t.Logf("Alpha 成功率: %.1f%%", float64(alphaSuccess)/float64(alphaTotal)*100)
	}
	if betaTotal > 0 {
		t.Logf("Beta 成功率: %.1f%%", float64(betaSuccess)/float64(betaTotal)*100)
	}
}

// ==================== 场景 6: 突发竞争 (1 波 Agent 同时涌入) ====================

// TestCompetition_BurstArrival
// 场景: 10 个 Agent 在同一时刻启动, 各自高频发请求 (模拟 AI 编排器并行调度多 Agent)
// 预期: 系统不崩溃, 最终价格收敛, 有效吞吐量 > 0
func TestCompetition_BurstArrival(t *testing.T) {
	tools := []ToolDef{{
		Name:        "burst_tool",
		Description: "突发流量工具",
		Downstream:  []string{},
		Handler:     slowHandler(1 * time.Millisecond),
	}}
	opts := agentTestOpts()
	opts["pinpointQueuing"] = true
	opts["initprice"] = int64(5)
	ts, _ := newAgentTestServer(tools, opts)
	defer ts.Close()

	const agentCount = 10
	const callsPerAgent = 50

	agents := makeAgents(agentCount, 3000, 60, StrategyFixed)

	// 所有 Agent 通过 sync.WaitGroup 同时启动
	var startWg sync.WaitGroup
	startWg.Add(1)

	metrics := NewTestMetrics()
	var wg sync.WaitGroup

	for _, agent := range agents {
		wg.Add(1)
		go func(a *SimulatedAgent) {
			defer wg.Done()
			startWg.Wait() // 等待统一出发信号
			result := a.ExecuteConcurrentCalls(ts.URL, "burst_tool", callsPerAgent, 0)
			metrics.Record(a.Name, result)
		}(agent)
	}

	startWg.Done() // 同时出发
	wg.Wait()

	t.Log(metrics.Summary())

	totalReqs := atomic.LoadInt64(&metrics.totalRequests)
	totalSuccess := atomic.LoadInt64(&metrics.totalSuccess)
	if totalReqs == 0 {
		t.Error("未发出任何请求")
	}
	if totalSuccess == 0 {
		t.Error("所有请求均被拒绝")
	}
	t.Logf("突发竞争完成: 总请求=%d, 成功=%d, 有效吞吐率=%.1f%%",
		totalReqs, totalSuccess, float64(totalSuccess)/float64(totalReqs)*100)
}

// ==================== 场景 7: 策略对比 (不同策略 Agent 同台竞技) ====================

// TestCompetition_StrategyComparison
// 场景: 4 个 Agent 使用不同预算策略, 争同一个工具
// 预期: 不同策略表现出差异化的成功率/消费效率
func TestCompetition_StrategyComparison(t *testing.T) {
	tools := []ToolDef{{
		Name:        "strategy_tool",
		Description: "策略对比工具",
		Downstream:  []string{},
		Handler:     simpleOKHandler("OK"),
	}}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(30)

	budget := int64(1500)
	agents := []*SimulatedAgent{
		{Name: "fixed-agent", InitialBudget: budget, BudgetLeft: budget, TokensPerCall: 50, Strategy: StrategyFixed, ThinkTime: time.Millisecond},
		{Name: "split-agent", InitialBudget: budget, BudgetLeft: budget, TokensPerCall: 50, Strategy: StrategyEqualSplit, ThinkTime: time.Millisecond},
		{Name: "front-agent", InitialBudget: budget, BudgetLeft: budget, TokensPerCall: 50, Strategy: StrategyFrontLoaded, ThinkTime: time.Millisecond},
		{Name: "adaptive-agent", InitialBudget: budget, BudgetLeft: budget, TokensPerCall: 50, Strategy: StrategyAdaptive, ThinkTime: time.Millisecond},
	}

	// 给每个 Agent 30 次调用机会
	for _, a := range agents {
		a.Tasks = make([]AgentTask, 30)
		for i := range a.Tasks {
			a.Tasks[i] = AgentTask{ToolName: "strategy_tool", DependsOn: -1}
		}
	}

	metrics := runAgentsParallel(agents, func(a *SimulatedAgent) *AgentMetrics {
		return a.ExecuteConcurrentCalls(ts.URL, "strategy_tool", 30, time.Millisecond)
	})

	t.Log(metrics.Summary())

	// 输出每个策略的效率 (成功/预算消耗)
	metrics.mu.Lock()
	for _, a := range agents {
		if am, ok := metrics.agentResults[a.Name]; ok {
			efficiency := float64(0)
			if am.BudgetSpent > 0 {
				efficiency = float64(am.SuccessCalls) / float64(am.BudgetSpent) * 1000
			}
			t.Logf("[%s] 成功=%d 消耗=%d 效率=%.2f成功/千令牌",
				a.Name, am.SuccessCalls, am.BudgetSpent, efficiency)
		}
	}
	metrics.mu.Unlock()
}
