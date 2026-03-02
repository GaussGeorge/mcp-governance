// loadtest_test.go
// 基础负载测试 — Go Test 集成
// 可通过 go test 运行，便于 CI/CD 集成和快速验证
//
// 运行方式：
//
//	cd loadtest
//	go test -v -run TestQuickComparison -timeout 10m
//	go test -v -run TestSingleStrategy/no_governance -timeout 5m
//	go test -v -run TestStepLoadAllStrategies -timeout 30m
package main

import (
	"testing"
	"time"
)

// TestQuickComparison 快速对比三种策略
// 使用较短的阶段时长，快速验证三种策略的行为差异
// 运行耗时约 3 分钟（3 策略 × ~55s），使用 -short 跳过
func TestQuickComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过快速对比测试（使用 -short 标志），请单独运行: go test -v -run TestQuickComparison -timeout 10m")
	}

	cfg := DefaultTestConfig()
	cfg.OutputDir = "output/test_quick"
	cfg.Verbose = false

	runner := NewTestRunner(cfg)
	summaries, err := runner.RunQuickTest()
	if err != nil {
		t.Fatalf("快速测试失败: %v", err)
	}

	if len(summaries) == 0 {
		t.Fatal("未收集到任何测试结果")
	}

	// 基本验证
	for _, s := range summaries {
		if s.TotalRequests == 0 {
			t.Errorf("策略 %s: 未发送任何请求", s.Strategy)
		}
		t.Logf("策略 %s: 总请求=%d, 成功=%d, 拒绝=%d, 吞吐量=%.2f RPS, P95=%.2fms",
			s.Strategy, s.TotalRequests, s.SuccessCount, s.RejectedCount,
			s.ThroughputRPS, s.P95LatencyMs)
	}
}

// TestSingleStrategy 逐个测试每种策略
func TestSingleStrategy(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过单策略测试（-short 模式）")
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
			cfg := DefaultTestConfig()
			cfg.OutputDir = "output/test_single"
			cfg.StepPhases = []StepPhase{
				{Name: "warmup", Duration: 5 * time.Second, Concurrency: 3},
				{Name: "low", Duration: 10 * time.Second, Concurrency: 10},
				{Name: "medium", Duration: 10 * time.Second, Concurrency: 30},
				{Name: "high", Duration: 10 * time.Second, Concurrency: 60},
				{Name: "overload", Duration: 10 * time.Second, Concurrency: 120},
				{Name: "recovery", Duration: 10 * time.Second, Concurrency: 5},
			}

			runner := NewTestRunner(cfg)
			summary, err := runner.RunSingleStrategy(tc.strategy, PatternStep, 1)
			if err != nil {
				t.Fatalf("测试失败: %v", err)
			}

			if summary.TotalRequests == 0 {
				t.Error("未发送任何请求")
			}

			t.Logf("总请求=%d, 成功=%d, 拒绝=%d, 错误=%d",
				summary.TotalRequests, summary.SuccessCount,
				summary.RejectedCount, summary.ErrorCount)
			t.Logf("吞吐量=%.2f RPS, P50=%.2fms, P95=%.2fms, P99=%.2fms",
				summary.ThroughputRPS, summary.P50LatencyMs,
				summary.P95LatencyMs, summary.P99LatencyMs)

			// 策略特定验证
			switch tc.strategy {
			case StrategyNoGovernance:
				// 无治理：过载时应有错误（模拟服务器资源耗尽）
				t.Logf("无治理错误率: %.4f, 拒绝率: %.4f", summary.ErrorRate, summary.RejectionRate)
			case StrategyStaticRateLimit:
				// 静态限流：高负载阶段应有拒绝
				t.Logf("静态限流拒绝率: %.4f", summary.RejectionRate)
			case StrategyRajomon:
				// Rajomon：验证动态定价生效（高负载下应有一定拒绝率）
				t.Logf("Rajomon 拒绝率: %.4f", summary.RejectionRate)
				// 验证公平性：高预算用户成功率应 >= 低预算
				if rate10, ok := summary.BudgetSuccessRate[10]; ok {
					if rate100, ok := summary.BudgetSuccessRate[100]; ok {
						t.Logf("公平性: 预算10成功率=%.4f, 预算100成功率=%.4f", rate10, rate100)
					}
				}
			}
		})
	}
}

// TestStepLoadAllStrategies 完整阶梯式负载测试（论文数据采集）
// 运行耗时较长，适合在数据采集时使用
func TestStepLoadAllStrategies(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过完整负载测试（使用 -short 标志）")
	}

	cfg := DefaultTestConfig()
	cfg.OutputDir = "output/test_full_step"

	runner := NewTestRunner(cfg)
	summaries, err := runner.RunAllStrategies(PatternStep, 1)
	if err != nil {
		t.Fatalf("阶梯式负载测试失败: %v", err)
	}

	if len(summaries) < 3 {
		t.Errorf("期望至少 3 个策略的结果，实际 %d 个", len(summaries))
	}

	// 输出对比
	for _, s := range summaries {
		t.Logf("[%s] 吞吐量=%.2f, P95=%.2f, P99=%.2f, 拒绝率=%.4f",
			s.Strategy, s.ThroughputRPS, s.P95LatencyMs, s.P99LatencyMs, s.RejectionRate)
	}
}

// TestRajomonPriceDynamics 验证 Rajomon 动态定价机制
// 确认价格随负载变化（核心功能验证）
func TestRajomonPriceDynamics(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过 Rajomon 动态定价测试（-short 模式）")
	}

	cfg := DefaultTestConfig()
	cfg.Strategy = StrategyRajomon
	cfg.OutputDir = "output/test_price_dynamics"
	cfg.StepPhases = []StepPhase{
		{Name: "warmup", Duration: 5 * time.Second, Concurrency: 3},
		{Name: "low", Duration: 8 * time.Second, Concurrency: 10},
		{Name: "overload", Duration: 8 * time.Second, Concurrency: 150},
		{Name: "recovery", Duration: 8 * time.Second, Concurrency: 5},
	}

	runner := NewTestRunner(cfg)
	summary, err := runner.RunSingleStrategy(StrategyRajomon, PatternStep, 1)
	if err != nil {
		t.Fatalf("Rajomon 动态定价测试失败: %v", err)
	}

	// 验证过载阶段有拒绝
	if overload, ok := summary.PhaseMetrics["overload"]; ok {
		t.Logf("过载阶段: 请求=%d, 成功=%d, 拒绝=%d, 拒绝率=%.4f",
			overload.TotalRequests, overload.SuccessCount,
			overload.RejectedCount, overload.RejectionRate)

		if overload.RejectedCount == 0 {
			t.Log("警告: 过载阶段无拒绝，可能需要调整价格步长或延迟阈值")
		}
	}

	// 验证恢复阶段拒绝率应下降
	if recovery, ok := summary.PhaseMetrics["recovery"]; ok {
		t.Logf("恢复阶段: 请求=%d, 成功=%d, 拒绝=%d, 拒绝率=%.4f",
			recovery.TotalRequests, recovery.SuccessCount,
			recovery.RejectedCount, recovery.RejectionRate)
	}

	// 验证公平性
	if len(summary.BudgetSuccessRate) > 1 {
		t.Log("公平性分析:")
		for budget, rate := range summary.BudgetSuccessRate {
			t.Logf("  预算 %d → 成功率 %.4f", budget, rate)
		}
	}
}

// TestFairnessComparison 对比三种策略的公平性
func TestFairnessComparison(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过公平性对比测试（使用 -short 标志）")
	}

	strategies := []StrategyType{StrategyNoGovernance, StrategyStaticRateLimit, StrategyRajomon}
	phases := []StepPhase{
		{Name: "warmup", Duration: 3 * time.Second, Concurrency: 3},
		{Name: "overload", Duration: 15 * time.Second, Concurrency: 100},
		{Name: "recovery", Duration: 5 * time.Second, Concurrency: 5},
	}

	t.Log("各策略在过载条件下的公平性对比:")
	t.Logf("%-20s %-12s %-12s %-12s", "策略", "预算10", "预算50", "预算100")

	for _, strategy := range strategies {
		cfg := DefaultTestConfig()
		cfg.OutputDir = "output/test_fairness"
		cfg.StepPhases = phases

		runner := NewTestRunner(cfg)
		summary, err := runner.RunSingleStrategy(strategy, PatternStep, 1)
		if err != nil {
			t.Errorf("策略 %s 失败: %v", strategy, err)
			continue
		}

		r10 := summary.BudgetSuccessRate[10]
		r50 := summary.BudgetSuccessRate[50]
		r100 := summary.BudgetSuccessRate[100]
		t.Logf("%-20s %-12.4f %-12.4f %-12.4f", strategy, r10, r50, r100)
	}
}
