// reasoning_chain_test.go
// Agent 多步推理链场景测试
//
// 模拟 AI Agent 的真实工具调用模式：多步推理链 (Tool Chain)
// Agent 需要按序调用多个工具，每步依赖前步结果：
//   - LLM 思考 → 调工具A → 用结果思考 → 调工具B → ... → 输出最终结论
//   - 链中任一步骤失败，后续步骤可能无法继续（Required 依赖）
//   - 服务端价格在链执行期间可能动态变化
//   - 不同长度的推理链的鲁棒性差异
package agent_test

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

// ==================== 场景 1: 基础线性推理链 ====================

// TestChain_Linear_Basic
// 场景: Agent 依次调用 search → analyze → summarize 3 个工具
// 预期: 预算充足时 3 步全部完成
func TestChain_Linear_Basic(t *testing.T) {
	tools := []ToolDef{
		{Name: "search", Description: "搜索工具", Downstream: []string{}, Handler: simpleOKHandler("搜索结果")},
		{Name: "analyze", Description: "分析工具", Downstream: []string{}, Handler: simpleOKHandler("分析结果")},
		{Name: "summarize", Description: "总结工具", Downstream: []string{}, Handler: simpleOKHandler("总结完成")},
	}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(10) // 低价格, 保证能通过

	agent := &SimulatedAgent{
		Name:          "chain-agent",
		InitialBudget: 500,
		BudgetLeft:    500,
		TokensPerCall: 50,
		Strategy:      StrategyFixed,
		ThinkTime:     5 * time.Millisecond, // 模拟 LLM 思考时间
		Tasks: []AgentTask{
			{ToolName: "search", DependsOn: -1, Required: true, MaxRetries: 1},
			{ToolName: "analyze", DependsOn: 0, Required: true, MaxRetries: 1},
			{ToolName: "summarize", DependsOn: 1, Required: true, MaxRetries: 1},
		},
	}

	metrics := agent.ExecuteChain(ts.URL)

	t.Logf("线性链: 步骤完成=%d/%d 链完成=%v 调用=%d 成功=%d",
		metrics.StepsCompleted, len(agent.Tasks), metrics.ChainCompleted,
		metrics.TotalCalls, metrics.SuccessCalls)

	if !metrics.ChainCompleted {
		t.Errorf("预算充足时推理链应完整完成, 实际完成=%d步", metrics.StepsCompleted)
		if len(metrics.Errors) > 0 {
			t.Logf("错误: %v", metrics.Errors)
		}
	}
}

// ==================== 场景 2: 依赖断裂 (中间步骤失败) ====================

// TestChain_DependencyBreak
// 场景: 3 步推理链, 第 2 步因价格过高被拒, 第 3 步因依赖失败而跳过
// 预期: 链未完成, 只有第 1 步成功
func TestChain_DependencyBreak(t *testing.T) {
	tools := []ToolDef{
		{Name: "step1", Description: "步骤1", Downstream: []string{}, Handler: simpleOKHandler("ok")},
		{Name: "step2", Description: "步骤2(高价)", Downstream: []string{}, Handler: simpleOKHandler("ok")},
		{Name: "step3", Description: "步骤3", Downstream: []string{}, Handler: simpleOKHandler("ok")},
	}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(200) // 高价, 第 2 步会被拒 (因为 Agent 只给 30 令牌)

	agent := &SimulatedAgent{
		Name:          "break-agent",
		InitialBudget: 300,
		BudgetLeft:    300,
		TokensPerCall: 30, // 远低于价格 200
		Strategy:      StrategyFixed,
		ThinkTime:     2 * time.Millisecond,
		Tasks: []AgentTask{
			{ToolName: "step1", DependsOn: -1, Required: true, MaxRetries: 0},
			{ToolName: "step2", DependsOn: 0, Required: true, MaxRetries: 0},
			{ToolName: "step3", DependsOn: 1, Required: true, MaxRetries: 0},
		},
	}

	metrics := agent.ExecuteChain(ts.URL)

	t.Logf("依赖断裂: 步骤完成=%d/%d 链完成=%v",
		metrics.StepsCompleted, len(agent.Tasks), metrics.ChainCompleted)

	if metrics.ChainCompleted {
		t.Error("高价环境下推理链不应完整完成")
	}

	// 第 1 步应被拒 (价格 200 > 令牌 30), 所以链从一开始就断了
	t.Logf("拒绝次数=%d, 错误: %v", metrics.RejectedCalls, metrics.Errors)
}

// ==================== 场景 3: 带重试的推理链 ====================

// TestChain_WithRetries
// 场景: 推理链中某步骤间歇性失败, Agent 通过重试恢复
// 预期: 有重试时链完成率 > 无重试
func TestChain_WithRetries(t *testing.T) {
	tools := []ToolDef{
		{Name: "fetch", Description: "获取数据", Downstream: []string{}, Handler: simpleOKHandler("data")},
		{Name: "process", Description: "处理数据", Downstream: []string{}, Handler: simpleOKHandler("processed")},
		{Name: "output", Description: "输出结果", Downstream: []string{}, Handler: simpleOKHandler("result")},
	}
	opts := agentTestOpts()

	t.Run("无重试", func(t *testing.T) {
		ts, gov := newAgentTestServer(tools, opts)
		defer ts.Close()
		gov.SetOwnPrice(40) // 中等价格

		agent := &SimulatedAgent{
			Name:          "no-retry",
			InitialBudget: 200,
			BudgetLeft:    200,
			TokensPerCall: 45,
			Strategy:      StrategyFixed,
			ThinkTime:     2 * time.Millisecond,
			Tasks: []AgentTask{
				{ToolName: "fetch", DependsOn: -1, Required: true, MaxRetries: 0},
				{ToolName: "process", DependsOn: 0, Required: true, MaxRetries: 0},
				{ToolName: "output", DependsOn: 1, Required: true, MaxRetries: 0},
			},
		}
		metrics := agent.ExecuteChain(ts.URL)
		t.Logf("无重试: 完成=%d/%d 链完成=%v 调用=%d", metrics.StepsCompleted, 3, metrics.ChainCompleted, metrics.TotalCalls)
	})

	t.Run("有重试", func(t *testing.T) {
		ts, gov := newAgentTestServer(tools, opts)
		defer ts.Close()
		gov.SetOwnPrice(40)

		agent := &SimulatedAgent{
			Name:          "with-retry",
			InitialBudget: 400,
			BudgetLeft:    400,
			TokensPerCall: 45,
			Strategy:      StrategyFixed,
			ThinkTime:     2 * time.Millisecond,
			Tasks: []AgentTask{
				{ToolName: "fetch", DependsOn: -1, Required: true, MaxRetries: 2},
				{ToolName: "process", DependsOn: 0, Required: true, MaxRetries: 2},
				{ToolName: "output", DependsOn: 1, Required: true, MaxRetries: 2},
			},
		}
		metrics := agent.ExecuteChain(ts.URL)
		t.Logf("有重试: 完成=%d/%d 链完成=%v 调用=%d 重试=%d",
			metrics.StepsCompleted, 3, metrics.ChainCompleted, metrics.TotalCalls, metrics.RetriedCalls)
	})
}

// ==================== 场景 4: 长推理链稳定性 ====================

// TestChain_LongChain_Stability
// 场景: 10 步推理链, 每步都依赖上一步
// 预期: 预算充足时全部完成; 数据记录每步的延迟和预算消耗
func TestChain_LongChain_Stability(t *testing.T) {
	const chainLen = 10

	tools := make([]ToolDef, chainLen)
	tasks := make([]AgentTask, chainLen)
	for i := 0; i < chainLen; i++ {
		name := fmt.Sprintf("step_%d", i)
		tools[i] = ToolDef{
			Name:        name,
			Description: fmt.Sprintf("链步骤 %d", i),
			Downstream:  []string{},
			Handler:     slowHandler(1 * time.Millisecond),
		}
		dep := -1
		if i > 0 {
			dep = i - 1
		}
		tasks[i] = AgentTask{
			ToolName:   name,
			DependsOn:  dep,
			Required:   true,
			MaxRetries: 1,
		}
	}

	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(10)

	agent := &SimulatedAgent{
		Name:          "long-chain-agent",
		InitialBudget: 2000,
		BudgetLeft:    2000,
		TokensPerCall: 30,
		Strategy:      StrategyFixed,
		ThinkTime:     3 * time.Millisecond,
		Tasks:         tasks,
	}

	metrics := agent.ExecuteChain(ts.URL)

	t.Logf("长链 (%d步): 完成=%d 链完成=%v 调用=%d 延迟=%v 预算消耗=%d",
		chainLen, metrics.StepsCompleted, metrics.ChainCompleted,
		metrics.TotalCalls, metrics.TotalLatency, metrics.BudgetSpent)

	if !metrics.ChainCompleted {
		t.Errorf("预算充足时长链应全部完成, 实际完成 %d/%d", metrics.StepsCompleted, chainLen)
		if len(metrics.Errors) > 0 {
			for _, e := range metrics.Errors {
				t.Logf("  错误: %s", e)
			}
		}
	}
}

// ==================== 场景 5: 推理链中价格动态变化 ====================

// TestChain_DynamicPricing_MidChain
// 场景: Agent 执行 5 步链, 第 3 步时服务端涨价
// 预期: 前 2 步成功, 第 3 步可能因涨价失败
func TestChain_DynamicPricing_MidChain(t *testing.T) {
	const chainLen = 5

	tools := make([]ToolDef, chainLen)
	tasks := make([]AgentTask, chainLen)
	for i := 0; i < chainLen; i++ {
		name := fmt.Sprintf("chain_step_%d", i)
		tools[i] = ToolDef{Name: name, Description: name, Downstream: []string{}, Handler: simpleOKHandler("ok")}
		dep := -1
		if i > 0 {
			dep = i - 1
		}
		tasks[i] = AgentTask{ToolName: name, DependsOn: dep, Required: true, MaxRetries: 0}
	}

	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	// 初始低价
	gov.SetOwnPrice(10)

	// 使用自适应策略
	agent := &SimulatedAgent{
		Name:          "dynamic-chain-agent",
		InitialBudget: 300,
		BudgetLeft:    300,
		TokensPerCall: 40,
		Strategy:      StrategyAdaptive,
		ThinkTime:     5 * time.Millisecond,
		Tasks:         tasks,
	}

	// 在后台定时涨价
	done := make(chan struct{})
	go func() {
		time.Sleep(15 * time.Millisecond) // 前 2 步低价
		gov.SetOwnPrice(80)               // 涨价
		time.Sleep(30 * time.Millisecond)
		gov.SetOwnPrice(150) // 再涨
		<-done
	}()

	metrics := agent.ExecuteChain(ts.URL)
	close(done)

	t.Logf("动态涨价链: 完成=%d/%d 链完成=%v 调用=%d 拒绝=%d",
		metrics.StepsCompleted, chainLen, metrics.ChainCompleted,
		metrics.TotalCalls, metrics.RejectedCalls)

	if len(metrics.Errors) > 0 {
		for _, e := range metrics.Errors {
			t.Logf("  错误: %s", e)
		}
	}
}

// ==================== 场景 6: 多 Agent 并行推理链 ====================

// TestChain_MultiAgent_ParallelChains
// 场景: 5 个 Agent 同时执行各自的 3 步推理链, 竞争共享工具
// 预期: 部分 Agent 的链完整完成, 其余因竞争可能部分失败
func TestChain_MultiAgent_ParallelChains(t *testing.T) {
	tools := []ToolDef{
		{Name: "plan", Description: "规划", Downstream: []string{}, Handler: slowHandler(2 * time.Millisecond)},
		{Name: "execute", Description: "执行", Downstream: []string{}, Handler: slowHandler(3 * time.Millisecond)},
		{Name: "verify", Description: "验证", Downstream: []string{}, Handler: simpleOKHandler("verified")},
	}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(15)

	const agentCount = 5
	agents := make([]*SimulatedAgent, agentCount)
	for i := 0; i < agentCount; i++ {
		agents[i] = &SimulatedAgent{
			Name:          fmt.Sprintf("chain-agent-%d", i+1),
			InitialBudget: 500,
			BudgetLeft:    500,
			TokensPerCall: 30,
			Strategy:      StrategyEqualSplit,
			ThinkTime:     3 * time.Millisecond,
			Tasks: []AgentTask{
				{ToolName: "plan", DependsOn: -1, Required: true, MaxRetries: 1},
				{ToolName: "execute", DependsOn: 0, Required: true, MaxRetries: 1},
				{ToolName: "verify", DependsOn: 1, Required: true, MaxRetries: 1},
			},
		}
	}

	metrics := runAgentsParallel(agents, func(a *SimulatedAgent) *AgentMetrics {
		return a.ExecuteChain(ts.URL)
	})

	t.Log(metrics.Summary())

	// 统计链完成率
	completedChains := 0
	metrics.mu.Lock()
	for _, am := range metrics.agentResults {
		if am.ChainCompleted {
			completedChains++
		}
	}
	metrics.mu.Unlock()

	t.Logf("链完成率: %d/%d (%.0f%%)", completedChains, agentCount,
		float64(completedChains)/float64(agentCount)*100)
}

// ==================== 场景 7: 分支推理链 (可选步骤) ====================

// TestChain_BranchingWithOptionalSteps
// 场景: 推理链中有可选步骤, 即使可选步骤失败, 主链仍可完成
// 结构: step0(必须) → step1(可选) → step2(必须, 依赖 step0)
func TestChain_BranchingWithOptionalSteps(t *testing.T) {
	tools := []ToolDef{
		{Name: "main_query", Description: "主查询", Downstream: []string{}, Handler: simpleOKHandler("data")},
		{Name: "enrich", Description: "数据增强(可选)", Downstream: []string{}, Handler: simpleOKHandler("enriched")},
		{Name: "conclude", Description: "结论", Downstream: []string{}, Handler: simpleOKHandler("conclusion")},
	}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(10)

	agent := &SimulatedAgent{
		Name:          "branch-agent",
		InitialBudget: 300,
		BudgetLeft:    300,
		TokensPerCall: 40,
		Strategy:      StrategyFixed,
		ThinkTime:     2 * time.Millisecond,
		Tasks: []AgentTask{
			{ToolName: "main_query", DependsOn: -1, Required: true, MaxRetries: 1},
			{ToolName: "enrich", DependsOn: 0, Required: false, MaxRetries: 0},  // 可选
			{ToolName: "conclude", DependsOn: 0, Required: true, MaxRetries: 1}, // 依赖 step0, 不依赖 step1
		},
	}

	metrics := agent.ExecuteChain(ts.URL)

	t.Logf("分支链: 完成=%d/%d 链完成=%v", metrics.StepsCompleted, 3, metrics.ChainCompleted)

	// 主链 (step0 → step2) 应该完成
	if !metrics.ChainCompleted {
		t.Logf("分支链未完成, 错误: %v", metrics.Errors)
	}
}

// ==================== 场景 8: 推理链预算策略对比 ====================

// TestChain_BudgetStrategy_Comparison
// 场景: 不同预算策略下, 相同 5 步推理链的完成率和效率对比
func TestChain_BudgetStrategy_Comparison(t *testing.T) {
	const chainLen = 5

	tools := make([]ToolDef, chainLen)
	baseTasks := make([]AgentTask, chainLen)
	for i := 0; i < chainLen; i++ {
		name := fmt.Sprintf("step%d", i)
		tools[i] = ToolDef{Name: name, Description: name, Downstream: []string{}, Handler: simpleOKHandler("ok")}
		dep := -1
		if i > 0 {
			dep = i - 1
		}
		baseTasks[i] = AgentTask{ToolName: name, DependsOn: dep, Required: true, MaxRetries: 1}
	}

	opts := agentTestOpts()

	strategies := []struct {
		name   string
		strat  BudgetStrategy
		budget int64
	}{
		{"Fixed", StrategyFixed, 300},
		{"EqualSplit", StrategyEqualSplit, 300},
		{"FrontLoaded", StrategyFrontLoaded, 300},
		{"Adaptive", StrategyAdaptive, 300},
	}

	for _, s := range strategies {
		t.Run(s.name, func(t *testing.T) {
			ts, gov := newAgentTestServer(tools, opts)
			defer ts.Close()
			gov.SetOwnPrice(20)

			tasks := make([]AgentTask, chainLen)
			copy(tasks, baseTasks)

			agent := &SimulatedAgent{
				Name:          s.name,
				InitialBudget: s.budget,
				BudgetLeft:    s.budget,
				TokensPerCall: 40,
				Strategy:      s.strat,
				ThinkTime:     2 * time.Millisecond,
				Tasks:         tasks,
			}

			metrics := agent.ExecuteChain(ts.URL)

			eff := float64(0)
			if metrics.BudgetSpent > 0 {
				eff = float64(metrics.StepsCompleted) / float64(metrics.BudgetSpent) * 1000
			}

			t.Logf("[%s] 完成=%d/%d 链完成=%v 消耗=%d 效率=%.2f步/千令牌",
				s.name, metrics.StepsCompleted, chainLen, metrics.ChainCompleted,
				metrics.BudgetSpent, eff)
		})
	}
}

// ==================== 场景 9: 大规模推理链批量实验 ====================

// TestChain_BatchExperiment
// 场景: 同一配置下运行 N 次推理链, 收集统计分布
// 适用于生成可重现的实验数据
func TestChain_BatchExperiment(t *testing.T) {
	const (
		chainLen   = 5
		batchSize  = 20
		budget     = 400
		tokPerCall = 50
		price      = 15
	)

	tools := make([]ToolDef, chainLen)
	for i := 0; i < chainLen; i++ {
		name := fmt.Sprintf("exp_step_%d", i)
		tools[i] = ToolDef{Name: name, Description: name, Downstream: []string{}, Handler: simpleOKHandler("ok")}
	}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(int64(price))

	var completedCount int64
	var totalSteps int64
	var totalBudgetSpent int64

	for run := 0; run < batchSize; run++ {
		tasks := make([]AgentTask, chainLen)
		for i := 0; i < chainLen; i++ {
			dep := -1
			if i > 0 {
				dep = i - 1
			}
			tasks[i] = AgentTask{
				ToolName:   fmt.Sprintf("exp_step_%d", i),
				DependsOn:  dep,
				Required:   true,
				MaxRetries: 1,
			}
		}

		agent := &SimulatedAgent{
			Name:          fmt.Sprintf("batch-%d", run),
			InitialBudget: budget,
			BudgetLeft:    budget,
			TokensPerCall: tokPerCall,
			Strategy:      StrategyFixed,
			ThinkTime:     time.Millisecond,
			Tasks:         tasks,
		}

		metrics := agent.ExecuteChain(ts.URL)

		if metrics.ChainCompleted {
			atomic.AddInt64(&completedCount, 1)
		}
		atomic.AddInt64(&totalSteps, int64(metrics.StepsCompleted))
		atomic.AddInt64(&totalBudgetSpent, metrics.BudgetSpent)
	}

	completionRate := float64(completedCount) / float64(batchSize) * 100
	avgSteps := float64(totalSteps) / float64(batchSize)
	avgSpent := float64(totalBudgetSpent) / float64(batchSize)

	t.Logf("=== 批量实验结果 (N=%d, 链长=%d) ===", batchSize, chainLen)
	t.Logf("  服务端价格: %d", price)
	t.Logf("  每次令牌: %d, 预算: %d", tokPerCall, budget)
	t.Logf("  链完成率: %.1f%% (%d/%d)", completionRate, completedCount, batchSize)
	t.Logf("  平均步骤完成: %.1f/%d", avgSteps, chainLen)
	t.Logf("  平均预算消耗: %.0f/%d", avgSpent, budget)

	// 基本断言
	if completedCount == 0 {
		t.Error("批量实验中无一完成, 配置可能有误")
	}
}

// ==================== 场景 10: 推理链与竞争结合 ====================

// TestChain_UnderCompetition
// 场景: 多个 Agent 同时执行推理链, 并且有额外的干扰 Agent 制造负载
// 预期: 推理链 Agent 受到干扰后完成率下降
func TestChain_UnderCompetition(t *testing.T) {
	tools := []ToolDef{
		{Name: "research", Description: "调研", Downstream: []string{}, Handler: slowHandler(2 * time.Millisecond)},
		{Name: "draft", Description: "起草", Downstream: []string{}, Handler: simpleOKHandler("draft")},
		{Name: "review", Description: "审查", Downstream: []string{}, Handler: simpleOKHandler("reviewed")},
	}
	opts := agentTestOpts()
	opts["pinpointQueuing"] = true
	opts["initprice"] = int64(5)
	ts, _ := newAgentTestServer(tools, opts)
	defer ts.Close()

	// 3 个推理链 Agent
	chainAgents := make([]*SimulatedAgent, 3)
	for i := range chainAgents {
		chainAgents[i] = &SimulatedAgent{
			Name:          fmt.Sprintf("chain-%d", i+1),
			InitialBudget: 500,
			BudgetLeft:    500,
			TokensPerCall: 40,
			Strategy:      StrategyAdaptive,
			ThinkTime:     3 * time.Millisecond,
			Tasks: []AgentTask{
				{ToolName: "research", DependsOn: -1, Required: true, MaxRetries: 2},
				{ToolName: "draft", DependsOn: 0, Required: true, MaxRetries: 1},
				{ToolName: "review", DependsOn: 1, Required: true, MaxRetries: 1},
			},
		}
	}

	// 5 个干扰 Agent (高频率调用 research 制造负载)
	disruptors := makeAgents(5, 2000, 40, StrategyFixed)
	for i := range disruptors {
		disruptors[i].Name = fmt.Sprintf("disruptor-%d", i+1)
	}

	// 并行执行
	allMetrics := NewTestMetrics()
	done := make(chan struct{})

	// 启动干扰 Agent
	for _, d := range disruptors {
		go func(a *SimulatedAgent) {
			result := a.ExecuteConcurrentCalls(ts.URL, "research", 30, 0) // 高频
			allMetrics.Record(a.Name, result)
		}(d)
	}

	// 启动推理链 Agent
	for _, ca := range chainAgents {
		go func(a *SimulatedAgent) {
			result := a.ExecuteChain(ts.URL)
			allMetrics.Record(a.Name, result)
		}(ca)
	}

	// 等所有 Agent 完成 (简单超时)
	time.Sleep(3 * time.Second)
	close(done)

	// 等一下让后台 goroutine 结束
	time.Sleep(200 * time.Millisecond)

	t.Log(allMetrics.Summary())

	// 统计推理链完成情况
	completedChains := 0
	allMetrics.mu.Lock()
	for _, ca := range chainAgents {
		if am, ok := allMetrics.agentResults[ca.Name]; ok && am.ChainCompleted {
			completedChains++
		}
	}
	allMetrics.mu.Unlock()

	t.Logf("竞争环境下链完成率: %d/%d", completedChains, len(chainAgents))
}

// ==================== 场景 11: 并行分支推理 (Fan-out + Fan-in) ====================

// TestChain_FanOutFanIn
// 场景: Agent 先调一个工具, 然后并行调 3 个工具, 最后汇总
// 结构: init → [branch_a, branch_b, branch_c] → merge
// 这里用串行模拟并行 (每个 branch 不依赖其他 branch, 但都依赖 init)
func TestChain_FanOutFanIn(t *testing.T) {
	tools := []ToolDef{
		{Name: "init_query", Description: "初始查询", Downstream: []string{}, Handler: simpleOKHandler("init")},
		{Name: "branch_a", Description: "分支 A", Downstream: []string{}, Handler: simpleOKHandler("a-result")},
		{Name: "branch_b", Description: "分支 B", Downstream: []string{}, Handler: simpleOKHandler("b-result")},
		{Name: "branch_c", Description: "分支 C", Downstream: []string{}, Handler: simpleOKHandler("c-result")},
		{Name: "merge", Description: "合并结果", Downstream: []string{}, Handler: simpleOKHandler("merged")},
	}
	opts := agentTestOpts()
	ts, gov := newAgentTestServer(tools, opts)
	defer ts.Close()

	gov.SetOwnPrice(10)

	agent := &SimulatedAgent{
		Name:          "fanout-agent",
		InitialBudget: 800,
		BudgetLeft:    800,
		TokensPerCall: 40,
		Strategy:      StrategyEqualSplit,
		ThinkTime:     2 * time.Millisecond,
		Tasks: []AgentTask{
			{ToolName: "init_query", DependsOn: -1, Required: true, MaxRetries: 1},
			{ToolName: "branch_a", DependsOn: 0, Required: false, MaxRetries: 0}, // 可选分支
			{ToolName: "branch_b", DependsOn: 0, Required: false, MaxRetries: 0}, // 可选分支
			{ToolName: "branch_c", DependsOn: 0, Required: false, MaxRetries: 0}, // 可选分支
			{ToolName: "merge", DependsOn: 0, Required: true, MaxRetries: 1},     // 只依赖 init
		},
	}

	metrics := agent.ExecuteChain(ts.URL)

	t.Logf("Fan-out/Fan-in: 完成=%d/%d 链完成=%v 消耗=%d",
		metrics.StepsCompleted, 5, metrics.ChainCompleted, metrics.BudgetSpent)

	if !metrics.ChainCompleted {
		t.Logf("链未完成, 错误: %v", metrics.Errors)
	}
}
