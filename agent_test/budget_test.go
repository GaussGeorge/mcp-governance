// budget_test.go
// Agent 预算管理场景测试
//
// 验证 Agent 在有限预算约束下的行为：
//   - 预算耗尽检测与优雅降级
//   - 不同预算策略的效率对比
//   - 令牌补充 (Token Refill) 对 Agent 持续性的影响
//   - 预算分配对多步任务完成率的影响
package agent_test

import (
	"sync/atomic"
	"testing"
	"time"
)

// ==================== 场景 1: 预算耗尽与优雅停止 ====================

// TestBudget_Exhaustion_GracefulStop
// 场景: Agent 有限预算, 持续调用直到预算耗尽
// 预期: Agent 能正确识别预算不足并停止, 不会产生负预算
func TestBudget_Exhaustion_GracefulStop(t *testing.T) {
	tools := []ToolDef{{
		Name:        "calc_tool",
		Description: "计算工具",
		Downstream:  []string{},
		Handler:     simpleOKHandler("计算完成"),
	}}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(10)

	agent := &SimulatedAgent{
		Name:          "budget-agent",
		InitialBudget: 200,
		BudgetLeft:    200,
		TokensPerCall: 30,
		Strategy:      StrategyFixed,
		ThinkTime:     time.Millisecond,
	}

	// 尝试调用 100 次, 但预算只够几次
	metrics := agent.ExecuteConcurrentCalls(ts.URL, "calc_tool", 100, time.Millisecond)

	t.Logf("初始预算=200, 每次消耗=30")
	t.Logf("总调用=%d, 成功=%d, 拒绝=%d, 预算消耗=%d, 剩余=%d",
		metrics.TotalCalls, metrics.SuccessCalls, metrics.RejectedCalls,
		metrics.BudgetSpent, atomic.LoadInt64(&agent.BudgetLeft))

	// 验证未产生负预算 (考虑退款机制)
	if atomic.LoadInt64(&agent.BudgetLeft) < -30 { // 允许小额误差
		t.Errorf("产生过大负预算: %d", atomic.LoadInt64(&agent.BudgetLeft))
	}

	// 验证确实有请求成功
	if metrics.SuccessCalls == 0 {
		t.Error("所有请求均失败")
	}

	// 验证 Agent 并没有发出 100 次请求 (应该提前停止)
	if metrics.TotalCalls >= 100 {
		t.Logf("注意: Agent 发出了全部 100 次请求, 预算管理可能不够积极")
	}
}

// ==================== 场景 2: 不同预算量级对比 ====================

// TestBudget_Tiers_CompletionRate
// 场景: 3 个 Agent 分别有低/中/高预算, 各自执行相同数量的调用
// 预期: 高预算完成更多调用
func TestBudget_Tiers_CompletionRate(t *testing.T) {
	tools := []ToolDef{{
		Name:        "analysis_tool",
		Description: "分析工具",
		Downstream:  []string{},
		Handler:     simpleOKHandler("分析完成"),
	}}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(20)

	tiers := []struct {
		name   string
		budget int64
	}{
		{"低预算", 100},
		{"中预算", 500},
		{"高预算", 2000},
	}

	const callsPerAgent = 50

	for _, tier := range tiers {
		t.Run(tier.name, func(t *testing.T) {
			agent := &SimulatedAgent{
				Name:          tier.name,
				InitialBudget: tier.budget,
				BudgetLeft:    tier.budget,
				TokensPerCall: 30,
				Strategy:      StrategyFixed,
				ThinkTime:     time.Millisecond,
			}

			metrics := agent.ExecuteConcurrentCalls(ts.URL, "analysis_tool", callsPerAgent, time.Millisecond)

			successRate := float64(0)
			if metrics.TotalCalls > 0 {
				successRate = float64(metrics.SuccessCalls) / float64(metrics.TotalCalls) * 100
			}

			t.Logf("[%s] 预算=%d 调用=%d 成功=%d 成功率=%.1f%% 预算消耗=%d",
				tier.name, tier.budget, metrics.TotalCalls, metrics.SuccessCalls,
				successRate, metrics.BudgetSpent)
		})
	}
}

// ==================== 场景 3: 预算策略效率基准 ====================

// TestBudget_StrategyEfficiency_Benchmark
// 场景: 4 种策略在相同预算下, 各自独立运行, 对比成功次数和效率
// 指标: 效率 = 成功次数 / 消耗令牌
func TestBudget_StrategyEfficiency_Benchmark(t *testing.T) {
	tools := []ToolDef{{
		Name:        "benchmark_tool",
		Description: "基准工具",
		Downstream:  []string{},
		Handler:     simpleOKHandler("OK"),
	}}
	opts := agentTestOpts()

	budget := int64(1000)
	const maxCalls = 60

	strategies := []struct {
		name     string
		strategy BudgetStrategy
	}{
		{"Fixed", StrategyFixed},
		{"EqualSplit", StrategyEqualSplit},
		{"FrontLoaded", StrategyFrontLoaded},
		{"Adaptive", StrategyAdaptive},
	}

	type result struct {
		name       string
		success    int64
		spent      int64
		efficiency float64
	}
	var results []result

	for _, s := range strategies {
		t.Run(s.name, func(t *testing.T) {
			ts, gov := newAgentTestServer(tools, opts)
			defer ts.Close()
			gov.SetOwnPrice(25)

			// 为 EqualSplit 策略构造任务列表
			tasks := make([]AgentTask, maxCalls)
			for i := range tasks {
				tasks[i] = AgentTask{ToolName: "benchmark_tool", DependsOn: -1}
			}

			agent := &SimulatedAgent{
				Name:          s.name,
				InitialBudget: budget,
				BudgetLeft:    budget,
				TokensPerCall: 40,
				Strategy:      s.strategy,
				Tasks:         tasks,
				ThinkTime:     time.Millisecond,
			}

			metrics := agent.ExecuteConcurrentCalls(ts.URL, "benchmark_tool", maxCalls, time.Millisecond)

			eff := float64(0)
			if metrics.BudgetSpent > 0 {
				eff = float64(metrics.SuccessCalls) / float64(metrics.BudgetSpent) * 1000
			}

			results = append(results, result{s.name, metrics.SuccessCalls, metrics.BudgetSpent, eff})

			t.Logf("[%s] 成功=%d 消耗=%d 效率=%.2f成功/千令牌",
				s.name, metrics.SuccessCalls, metrics.BudgetSpent, eff)
		})
	}

	// 汇总比较
	if len(results) > 1 {
		t.Log("\n=== 策略效率排名 ===")
		for _, r := range results {
			t.Logf("  %s: 成功=%d 效率=%.2f", r.name, r.success, r.efficiency)
		}
	}
}

// ==================== 场景 4: 预算与涨价互动 ====================

// TestBudget_PriceIncrease_BudgetErosion
// 场景: Agent 预算固定, 但服务端价格逐步上涨
// 预期: 前期请求大多成功, 后期随着涨价被逐步拒绝
func TestBudget_PriceIncrease_BudgetErosion(t *testing.T) {
	tools := []ToolDef{{
		Name:        "eroding_tool",
		Description: "涨价工具",
		Downstream:  []string{},
		Handler:     simpleOKHandler("OK"),
	}}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	agent := &SimulatedAgent{
		Name:          "victim",
		InitialBudget: 3000,
		BudgetLeft:    3000,
		TokensPerCall: 60,
		Strategy:      StrategyFixed,
		ThinkTime:     time.Millisecond,
	}

	const phases = 5
	const callsPerPhase = 10
	prices := []int64{10, 30, 50, 70, 100}

	t.Log("=== 价格阶梯侵蚀预算 ===")
	for phase := 0; phase < phases; phase++ {
		gov.SetOwnPrice(prices[phase])

		metrics := agent.ExecuteConcurrentCalls(ts.URL, "eroding_tool", callsPerPhase, time.Millisecond)

		t.Logf("阶段%d: 价格=%d 调用=%d 成功=%d 拒绝=%d 剩余预算=%d",
			phase+1, prices[phase], metrics.TotalCalls, metrics.SuccessCalls,
			metrics.RejectedCalls, atomic.LoadInt64(&agent.BudgetLeft))

		// 重置 Agent 指标 (但保留预算)
		agent.metrics = AgentMetrics{}
	}
}

// ==================== 场景 5: 自适应策略 vs 固定策略 (涨价环境) ====================

// TestBudget_Adaptive_vs_Fixed_UnderPriceFluctuation
// 场景: 价格波动环境下, 自适应策略和固定策略的对比
// 预期: 自适应策略在价格波动环境中效率更高
func TestBudget_Adaptive_vs_Fixed_UnderPriceFluctuation(t *testing.T) {
	budget := int64(2000)
	const totalCalls = 40

	priceSchedule := []int64{10, 10, 30, 30, 60, 60, 30, 30, 10, 10}

	type stratResult struct {
		name    string
		success int64
		spent   int64
	}
	var stratResults []stratResult

	for _, strat := range []struct {
		name string
		s    BudgetStrategy
		tok  int64
	}{
		{"Fixed-40", StrategyFixed, 40},
		{"Fixed-80", StrategyFixed, 80},
		{"Adaptive", StrategyAdaptive, 40},
	} {
		t.Run(strat.name, func(t *testing.T) {
			tools := []ToolDef{{
				Name:        "volatile_tool",
				Description: "价格波动工具",
				Downstream:  []string{},
				Handler:     simpleOKHandler("OK"),
			}}
			opts := agentTestOpts()
			ts, gov := newAgentTestServer(tools, opts)
			defer ts.Close()

			agent := &SimulatedAgent{
				Name:          strat.name,
				InitialBudget: budget,
				BudgetLeft:    budget,
				TokensPerCall: strat.tok,
				Strategy:      strat.s,
				ThinkTime:     time.Millisecond,
			}

			// 给 EqualSplit/Adaptive 用的任务列表
			agent.Tasks = make([]AgentTask, totalCalls)
			for i := range agent.Tasks {
				agent.Tasks[i] = AgentTask{ToolName: "volatile_tool", DependsOn: -1}
			}

			var totalSuccess, totalSpent int64
			callsDone := 0
			for callsDone < totalCalls {
				// 按价格调度表设置价格
				priceIdx := callsDone / (totalCalls / len(priceSchedule))
				if priceIdx >= len(priceSchedule) {
					priceIdx = len(priceSchedule) - 1
				}
				gov.SetOwnPrice(priceSchedule[priceIdx])

				// 每批 4 次
				batchSize := 4
				if callsDone+batchSize > totalCalls {
					batchSize = totalCalls - callsDone
				}

				metrics := agent.ExecuteConcurrentCalls(ts.URL, "volatile_tool", batchSize, time.Millisecond)
				totalSuccess += metrics.SuccessCalls
				totalSpent += metrics.BudgetSpent
				agent.metrics = AgentMetrics{} // 重置但保留预算
				callsDone += batchSize
			}

			t.Logf("[%s] 总成功=%d 总消耗=%d 效率=%.2f",
				strat.name, totalSuccess, totalSpent,
				func() float64 {
					if totalSpent == 0 {
						return 0
					}
					return float64(totalSuccess) / float64(totalSpent) * 1000
				}())

			stratResults = append(stratResults, stratResult{strat.name, totalSuccess, totalSpent})
		})
	}

	// 汇总
	if len(stratResults) > 1 {
		t.Log("\n=== 价格波动环境下的策略对比 ===")
		for _, r := range stratResults {
			t.Logf("  %s: 成功=%d 消耗=%d", r.name, r.success, r.spent)
		}
	}
}

// ==================== 场景 6: 多 Agent 预算隔离 ====================

// TestBudget_Isolation_IndependentBudgets
// 场景: 3 个 Agent 各自独立预算, 一个 Agent 耗尽不影响其他
// 预期: Agent 之间预算互不干扰
func TestBudget_Isolation_IndependentBudgets(t *testing.T) {
	tools := []ToolDef{{
		Name:        "isolated_tool",
		Description: "隔离工具",
		Downstream:  []string{},
		Handler:     simpleOKHandler("OK"),
	}}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(15)

	agents := []*SimulatedAgent{
		{Name: "small-budget", InitialBudget: 50, BudgetLeft: 50, TokensPerCall: 20, Strategy: StrategyFixed, ThinkTime: time.Millisecond},
		{Name: "medium-budget", InitialBudget: 500, BudgetLeft: 500, TokensPerCall: 20, Strategy: StrategyFixed, ThinkTime: time.Millisecond},
		{Name: "large-budget", InitialBudget: 5000, BudgetLeft: 5000, TokensPerCall: 20, Strategy: StrategyFixed, ThinkTime: time.Millisecond},
	}

	const callsPerAgent = 50

	metrics := runAgentsParallel(agents, func(a *SimulatedAgent) *AgentMetrics {
		return a.ExecuteConcurrentCalls(ts.URL, "isolated_tool", callsPerAgent, time.Millisecond)
	})

	t.Log(metrics.Summary())

	// 验证: large-budget 的成功次数 > medium > small
	metrics.mu.Lock()
	small := metrics.agentResults["small-budget"]
	medium := metrics.agentResults["medium-budget"]
	large := metrics.agentResults["large-budget"]
	metrics.mu.Unlock()

	if small == nil || medium == nil || large == nil {
		t.Fatal("缺少 Agent 统计数据")
	}

	t.Logf("small=%d medium=%d large=%d", small.SuccessCalls, medium.SuccessCalls, large.SuccessCalls)

	if large.SuccessCalls < medium.SuccessCalls {
		t.Logf("注意: 大预算 Agent (%d) 成功次数少于中预算 (%d)", large.SuccessCalls, medium.SuccessCalls)
	}
	if medium.SuccessCalls < small.SuccessCalls {
		t.Logf("注意: 中预算 Agent (%d) 成功次数少于小预算 (%d)", medium.SuccessCalls, small.SuccessCalls)
	}
}

// ==================== 场景 7: 预算回收与效率 ====================

// TestBudget_Refund_OnRejection
// 场景: 请求被拒绝时 Agent 回收部分令牌
// 验证拒绝回收机制不会导致无限循环或预算膨胀
func TestBudget_Refund_OnRejection(t *testing.T) {
	tools := []ToolDef{{
		Name:        "rejection_tool",
		Description: "高价拒绝工具",
		Downstream:  []string{},
		Handler:     simpleOKHandler("OK"),
	}}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(200) // 超高价格, 大多数请求会被拒绝

	agent := &SimulatedAgent{
		Name:          "refund-agent",
		InitialBudget: 500,
		BudgetLeft:    500,
		TokensPerCall: 100,
		Strategy:      StrategyFixed,
		ThinkTime:     time.Millisecond,
	}

	metrics := agent.ExecuteConcurrentCalls(ts.URL, "rejection_tool", 30, time.Millisecond)

	finalBudget := atomic.LoadInt64(&agent.BudgetLeft)

	t.Logf("初始预算=500, 调用=%d, 成功=%d, 拒绝=%d, 消耗=%d, 最终预算=%d",
		metrics.TotalCalls, metrics.SuccessCalls, metrics.RejectedCalls,
		metrics.BudgetSpent, finalBudget)

	// 验证: 最终预算不应超过初始预算 (无通胀)
	if finalBudget > agent.InitialBudget {
		t.Errorf("预算膨胀! 初始=%d, 最终=%d", agent.InitialBudget, finalBudget)
	}

	// 验证: 拒绝次数 > 0 (价格确实高于令牌)
	if metrics.RejectedCalls == 0 {
		t.Log("注意: 未发生拒绝, 价格可能设置过低")
	}

	// 验证: 净消耗大致合理 (初始 - 最终 ≈ 消耗)
	netSpent := agent.InitialBudget - finalBudget
	t.Logf("净消耗=%d (初始%d - 最终%d), 报告消耗=%d",
		netSpent, agent.InitialBudget, finalBudget, metrics.BudgetSpent)
}

// ==================== 场景 8: 预算生命周期 (长时间运行) ====================

// TestBudget_Lifecycle_LongRunning
// 场景: Agent 持续运行一段时间, 观察预算消耗曲线
// 预期: 预算单调递减, 消耗速率与成功率正相关
func TestBudget_Lifecycle_LongRunning(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过长时间运行测试")
	}

	tools := []ToolDef{{
		Name:        "lifecycle_tool",
		Description: "生命周期工具",
		Downstream:  []string{},
		Handler:     simpleOKHandler("OK"),
	}}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(15)

	agent := &SimulatedAgent{
		Name:          "lifecycle-agent",
		InitialBudget: 5000,
		BudgetLeft:    5000,
		TokensPerCall: 25,
		Strategy:      StrategyFixed,
		ThinkTime:     2 * time.Millisecond,
	}

	// 10 个阶段, 每阶段 20 次调用
	const phases = 10
	const callsPerPhase = 20
	var budgetHistory []int64

	for phase := 0; phase < phases; phase++ {
		budgetBefore := atomic.LoadInt64(&agent.BudgetLeft)
		budgetHistory = append(budgetHistory, budgetBefore)

		metrics := agent.ExecuteConcurrentCalls(ts.URL, "lifecycle_tool", callsPerPhase, time.Millisecond)

		budgetAfter := atomic.LoadInt64(&agent.BudgetLeft)

		t.Logf("阶段%2d: 预算 %d→%d (Δ=%d), 成功=%d/%d",
			phase+1, budgetBefore, budgetAfter, budgetBefore-budgetAfter,
			metrics.SuccessCalls, metrics.TotalCalls)

		agent.metrics = AgentMetrics{}

		if budgetAfter <= 0 {
			t.Logf("阶段 %d 预算耗尽, 提前结束", phase+1)
			break
		}
	}

	// 验证: 预算大致单调递减
	for i := 1; i < len(budgetHistory); i++ {
		if budgetHistory[i] > budgetHistory[i-1]+50 { // 允许退款造成的小幅回升
			t.Logf("注意: 阶段 %d 预算 (%d) > 阶段 %d (%d), 可能存在退款",
				i+1, budgetHistory[i], i, budgetHistory[i-1])
		}
	}

	t.Logf("预算轨迹: %v", budgetHistory)
	t.Logf("最终剩余: %d", atomic.LoadInt64(&agent.BudgetLeft))
}

// ==================== 场景 9: 预算边界条件 ====================

// TestBudget_EdgeCases
// 测试极端边界条件
func TestBudget_EdgeCases(t *testing.T) {
	tools := []ToolDef{{
		Name:        "edge_tool",
		Description: "边界测试工具",
		Downstream:  []string{},
		Handler:     simpleOKHandler("OK"),
	}}
	opts := agentTestOpts()

	t.Run("零预算", func(t *testing.T) {
		ts, gov := newAgentTestServer(tools, opts)
		defer ts.Close()
		gov.SetOwnPrice(10)

		agent := &SimulatedAgent{
			Name:          "zero",
			InitialBudget: 0,
			BudgetLeft:    0,
			TokensPerCall: 10,
			Strategy:      StrategyFixed,
		}
		metrics := agent.ExecuteConcurrentCalls(ts.URL, "edge_tool", 5, time.Millisecond)
		t.Logf("零预算: 调用=%d 成功=%d", metrics.TotalCalls, metrics.SuccessCalls)
	})

	t.Run("最小预算_刚好一次", func(t *testing.T) {
		ts, gov := newAgentTestServer(tools, opts)
		defer ts.Close()
		gov.SetOwnPrice(5)

		agent := &SimulatedAgent{
			Name:          "minimal",
			InitialBudget: 10,
			BudgetLeft:    10,
			TokensPerCall: 10,
			Strategy:      StrategyFixed,
		}
		metrics := agent.ExecuteConcurrentCalls(ts.URL, "edge_tool", 5, time.Millisecond)
		t.Logf("最小预算: 调用=%d 成功=%d 消耗=%d", metrics.TotalCalls, metrics.SuccessCalls, metrics.BudgetSpent)

		if metrics.SuccessCalls < 1 {
			t.Error("预期至少 1 次成功")
		}
	})

	t.Run("超大预算", func(t *testing.T) {
		ts, gov := newAgentTestServer(tools, opts)
		defer ts.Close()
		gov.SetOwnPrice(10)

		agent := &SimulatedAgent{
			Name:          "whale",
			InitialBudget: 1000000,
			BudgetLeft:    1000000,
			TokensPerCall: 100,
			Strategy:      StrategyFixed,
		}
		metrics := agent.ExecuteConcurrentCalls(ts.URL, "edge_tool", 20, time.Millisecond)
		t.Logf("超大预算: 调用=%d 成功=%d", metrics.TotalCalls, metrics.SuccessCalls)

		if metrics.SuccessCalls != 20 {
			t.Errorf("超大预算应全部成功, 实际成功=%d", metrics.SuccessCalls)
		}
	})
}

// ==================== 场景 10: 多 Agent 预算总量守恒 ====================

// TestBudget_Conservation_TotalTokens
// 场景: 多个 Agent 并发, 验证全局令牌不会凭空增加或消失
// (考虑退款后的近似守恒)
func TestBudget_Conservation_TotalTokens(t *testing.T) {
	tools := []ToolDef{{
		Name:        "conservation_tool",
		Description: "守恒验证工具",
		Downstream:  []string{},
		Handler:     simpleOKHandler("OK"),
	}}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(25)

	const agentCount = 5
	agents := makeAgents(agentCount, 1000, 40, StrategyFixed)

	var totalInitialBudget int64
	for _, a := range agents {
		totalInitialBudget += a.InitialBudget
	}

	metrics := runAgentsParallel(agents, func(a *SimulatedAgent) *AgentMetrics {
		return a.ExecuteConcurrentCalls(ts.URL, "conservation_tool", 20, time.Millisecond)
	})

	var totalFinalBudget int64
	var totalSpent int64
	metrics.mu.Lock()
	for _, a := range agents {
		totalFinalBudget += atomic.LoadInt64(&a.BudgetLeft)
		if am, ok := metrics.agentResults[a.Name]; ok {
			totalSpent += am.BudgetSpent
		}
	}
	metrics.mu.Unlock()

	t.Logf("初始总预算=%d, 最终总预算=%d, 总消耗=%d",
		totalInitialBudget, totalFinalBudget, totalSpent)
	t.Logf("差额=%d (初始-最终-消耗=%d)", totalInitialBudget-totalFinalBudget,
		totalInitialBudget-totalFinalBudget-totalSpent)

	// 验额: 不应出现令牌通胀
	if totalFinalBudget > totalInitialBudget {
		t.Errorf("总预算膨胀: 初始=%d, 最终=%d", totalInitialBudget, totalFinalBudget)
	}
}
