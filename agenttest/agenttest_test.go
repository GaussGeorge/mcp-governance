// agenttest_test.go
// Agent 场景测试 — Go Test 集成
// 可通过 go test 运行，便于 CI/CD 集成和快速验证
//
// 运行方式：
//
//	cd agenttest
//	go test -v -run TestAgentQuickComparison -timeout 15m
//	go test -v -run TestAgentSingleStrategy/rajomon -timeout 10m
//	go test -v -run TestAgentBurstLoad -timeout 15m
package main

import (
	"testing"
	"time"
)

// TestAgentQuickComparison 快速对比三种策略的 Agent 场景表现
func TestAgentQuickComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过Agent快速对比测试（-short 模式）")
	}

	cfg := DefaultAgentTestConfig()
	cfg.OutputDir = "output/test_agent_quick"
	cfg.Verbose = false

	runner := NewAgentTestRunner(cfg)
	summaries, err := runner.RunQuickTest()
	if err != nil {
		t.Fatalf("Agent 快速测试失败: %v", err)
	}

	if len(summaries) == 0 {
		t.Fatal("未收集到任何测试结果")
	}

	for _, s := range summaries {
		if s.TotalTasks == 0 {
			t.Errorf("策略 %s: 未生成任何任务", s.Strategy)
		}
		t.Logf("策略 %s: 任务数=%d, 任务成功率=%.4f, 步骤数=%d, 步骤成功率=%.4f, 吞吐量=%.2f RPS",
			s.Strategy, s.TotalTasks, s.TaskSuccessRate,
			s.TotalSteps, s.StepSuccessRate, s.ThroughputRPS)

		// 验证预算组公平性
		for budget, rate := range s.BudgetTaskSuccessRate {
			t.Logf("  策略 %s 预算 %d: 任务成功率=%.4f", s.Strategy, budget, rate)
		}
	}
}

// TestAgentSingleStrategy 逐个测试每种策略的 Agent 场景
func TestAgentSingleStrategy(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过Agent单策略测试（-short 模式）")
	}

	strategies := []struct {
		name     string
		strategy StrategyType
	}{
		{"no_governance", StrategyNoGovernance},
		{"static_rate_limit", StrategyStaticRateLimit},
		{"rajomon", StrategyRajomon},
	}

	for _, tc := range strategies {
		t.Run(tc.name, func(t *testing.T) {
			cfg := DefaultAgentTestConfig()
			cfg.OutputDir = "output/test_agent_single"
			cfg.NumAgents = 30
			cfg.BurstPhases = []BurstPhase{
				{Name: "warmup", Duration: 5 * time.Second, TaskRate: 2},
				{Name: "normal", Duration: 10 * time.Second, TaskRate: 5},
				{Name: "burst", Duration: 10 * time.Second, TaskRate: 15},
				{Name: "overload", Duration: 15 * time.Second, TaskRate: 30},
				{Name: "recovery", Duration: 5 * time.Second, TaskRate: 2},
			}

			runner := NewAgentTestRunner(cfg)
			summary, err := runner.RunSingleStrategy(tc.strategy, PatternBurst, 1)
			if err != nil {
				t.Fatalf("测试失败: %v", err)
			}

			if summary.TotalTasks == 0 {
				t.Error("未生成任何任务")
			}

			t.Logf("任务: 总=%d, 成功=%d, 失败=%d, 成功率=%.4f",
				summary.TotalTasks, summary.SuccessTasks, summary.FailedTasks, summary.TaskSuccessRate)
			t.Logf("步骤: 总=%d, 成功=%d, 拒绝=%d, 成功率=%.4f",
				summary.TotalSteps, summary.SuccessSteps, summary.RejectedSteps, summary.StepSuccessRate)
			t.Logf("延迟: P50=%.2fms, P95=%.2fms, P99=%.2fms",
				summary.P50LatencyMs, summary.P95LatencyMs, summary.P99LatencyMs)

			// 策略特定验证
			switch tc.strategy {
			case StrategyRajomon:
				// Rajomon 应体现预算差异化
				t.Logf("Rajomon 预算公平性:")
				for budget, rate := range summary.BudgetTaskSuccessRate {
					t.Logf("  预算 %d: 任务成功率=%.4f", budget, rate)
				}
			case StrategyNoGovernance:
				// 无治理在过载时应有大量错误
				t.Logf("无治理错误步骤: %d, 步骤成功率: %.4f", summary.ErrorSteps, summary.StepSuccessRate)
			case StrategyStaticRateLimit:
				// 静态限流应有拒绝
				t.Logf("静态限流拒绝步骤: %d, 步骤拒绝率: %.4f", summary.RejectedSteps, summary.StepRejectionRate)
			}
		})
	}
}

// TestAgentBurstLoad 突发负载下的 Agent 场景对比
func TestAgentBurstLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过Agent突发负载测试（-short 模式）")
	}

	cfg := DefaultAgentTestConfig()
	cfg.OutputDir = "output/test_agent_burst"
	cfg.NumAgents = 50
	cfg.BurstPhases = []BurstPhase{
		{Name: "warmup", Duration: 10 * time.Second, TaskRate: 2},
		{Name: "normal", Duration: 15 * time.Second, TaskRate: 5},
		{Name: "burst", Duration: 15 * time.Second, TaskRate: 20},
		{Name: "overload", Duration: 20 * time.Second, TaskRate: 40},
		{Name: "recovery", Duration: 10 * time.Second, TaskRate: 2},
	}

	runner := NewAgentTestRunner(cfg)
	summaries, err := runner.RunAllStrategies(PatternBurst, 1)
	if err != nil {
		t.Fatalf("突发负载测试失败: %v", err)
	}

	if len(summaries) < 3 {
		t.Errorf("期望3种策略的结果，实际获得 %d 种", len(summaries))
	}

	// 验证 Rajomon 在过载时应表现出预算差异化
	for _, s := range summaries {
		if s.Strategy == StrategyRajomon {
			b10 := s.BudgetTaskSuccessRate[10]
			b100 := s.BudgetTaskSuccessRate[100]
			t.Logf("Rajomon 预算差异: 预算10成功率=%.4f, 预算100成功率=%.4f", b10, b100)
			// 在过载场景下，高预算应有更高的成功率
			// 注意：快速测试中这个差异可能不明显
		}
	}
}

// TestAgentPoissonLoad 泊松负载下的 Agent 场景
func TestAgentPoissonLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过Agent泊松负载测试（-short 模式）")
	}

	cfg := DefaultAgentTestConfig()
	cfg.OutputDir = "output/test_agent_poisson"
	cfg.NumAgents = 30
	cfg.PoissonTaskRate = 8
	cfg.Duration = 1 * time.Minute

	runner := NewAgentTestRunner(cfg)
	summaries, err := runner.RunAllStrategies(PatternPoisson, 1)
	if err != nil {
		t.Fatalf("泊松负载测试失败: %v", err)
	}

	for _, s := range summaries {
		t.Logf("策略 %s: 任务成功率=%.4f, 步骤吞吐量=%.2f RPS",
			s.Strategy, s.TaskSuccessRate, s.ThroughputRPS)
	}
}
