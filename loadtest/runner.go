// runner.go
// 测试运行器
// 编排服务器启动、负载生成、指标收集、结果导出的完整流程
package main

import (
	"fmt"
	"strings"
	"time"
)

// TestRunner 测试运行器
type TestRunner struct {
	cfg *TestConfig
}

// NewTestRunner 创建测试运行器
func NewTestRunner(cfg *TestConfig) *TestRunner {
	return &TestRunner{cfg: cfg}
}

// RunSingleStrategy 运行单策略测试
// 启动对应策略的服务器 → 生成负载 → 收集指标 → 导出结果
func (tr *TestRunner) RunSingleStrategy(strategy StrategyType, pattern LoadPattern, runIndex int) (*MetricsSummary, error) {
	cfg := *tr.cfg
	cfg.Strategy = strategy
	cfg.LoadPattern = pattern

	sep := strings.Repeat("*", 60)
	fmt.Printf("\n%s\n", sep)
	fmt.Printf("  运行测试: 策略=%s, 模式=%s, 第 %d 次\n", strategy, pattern, runIndex)
	fmt.Printf("%s\n\n", sep)

	// 1. 启动服务器
	var serverInstance *ServerInstance
	var err error

	switch strategy {
	case StrategyNoGovernance:
		serverInstance, err = StartNoGovernanceServer(&cfg)
	case StrategyStaticRateLimit:
		serverInstance, err = StartStaticRateLimitServer(&cfg)
	case StrategyRajomon:
		serverInstance, err = StartRajomonServer(&cfg)
	default:
		return nil, fmt.Errorf("未知策略: %s", strategy)
	}

	if err != nil {
		return nil, fmt.Errorf("启动服务器失败: %w", err)
	}
	defer func() {
		fmt.Printf("[关闭服务器] %s\n", strategy)
		serverInstance.Stop()
		// 等待端口释放
		time.Sleep(500 * time.Millisecond)
	}()

	// 等待服务器完全就绪
	time.Sleep(200 * time.Millisecond)

	// 2. 创建负载生成器并运行
	loader := NewLoadGenerator(&cfg, serverInstance.URL())
	results := loader.Run()

	fmt.Printf("[收集结果] 共 %d 条请求记录\n", len(results))

	// 3. 计算指标
	summary := CalculateMetrics(results, strategy, pattern, runIndex)
	PrintSummary(summary)

	// 4. 导出 CSV
	csvPath, err := WriteResultsToCSV(results, cfg.OutputDir, strategy, pattern, runIndex)
	if err != nil {
		fmt.Printf("[CSV 导出失败] %v\n", err)
	} else {
		fmt.Printf("[CSV 导出成功] %s\n", csvPath)
	}

	return &summary, nil
}

// RunAllStrategies 运行所有策略的对比测试
// 对每种策略运行指定次数，取平均结果
func (tr *TestRunner) RunAllStrategies(pattern LoadPattern, runs int) ([]MetricsSummary, error) {
	strategies := []StrategyType{StrategyNoGovernance, StrategyStaticRateLimit, StrategyRajomon}
	var allSummaries []MetricsSummary

	for _, strategy := range strategies {
		for run := 1; run <= runs; run++ {
			summary, err := tr.RunSingleStrategy(strategy, pattern, run)
			if err != nil {
				fmt.Printf("[错误] 策略 %s 第 %d 次运行失败: %v\n", strategy, run, err)
				continue
			}
			allSummaries = append(allSummaries, *summary)

			// 运行之间等待，让系统恢复
			if run < runs || strategy != strategies[len(strategies)-1] {
				fmt.Println("[等待] 2 秒后开始下一轮...")
				time.Sleep(2 * time.Second)
			}
		}
	}

	// 打印对比表格
	if len(allSummaries) > 0 {
		// 取每种策略的最后一次运行做对比
		var comparison []MetricsSummary
		seen := make(map[StrategyType]bool)
		for i := len(allSummaries) - 1; i >= 0; i-- {
			s := allSummaries[i]
			if !seen[s.Strategy] {
				seen[s.Strategy] = true
				comparison = append([]MetricsSummary{s}, comparison...)
			}
		}
		PrintComparisonTable(comparison)

		// 导出汇总 CSV
		summaryPath, err := WriteSummaryToCSV(allSummaries, tr.cfg.OutputDir)
		if err != nil {
			fmt.Printf("[汇总 CSV 导出失败] %v\n", err)
		} else {
			fmt.Printf("[汇总 CSV 导出成功] %s\n", summaryPath)
		}
	}

	return allSummaries, nil
}

// RunQuickTest 快速测试：缩短阶段时长，用于验证正确性
func (tr *TestRunner) RunQuickTest() ([]MetricsSummary, error) {
	// 使用较短的阶段时长
	tr.cfg.StepPhases = []StepPhase{
		{Name: "warmup", Duration: 5 * time.Second, Concurrency: 3},
		{Name: "low", Duration: 10 * time.Second, Concurrency: 10},
		{Name: "medium", Duration: 10 * time.Second, Concurrency: 30},
		{Name: "high", Duration: 10 * time.Second, Concurrency: 60},
		{Name: "overload", Duration: 10 * time.Second, Concurrency: 120},
		{Name: "recovery", Duration: 10 * time.Second, Concurrency: 5},
	}
	tr.cfg.Duration = 1 * time.Minute

	return tr.RunAllStrategies(PatternStep, 1)
}

// ==================== 消融对照组测试 (Ablation Study) ====================

// AblationResult 单个对照组的测试结果
type AblationResult struct {
	GroupName   string
	Description string
	Strategy    StrategyType
	Summary     MetricsSummary
}

// RunAblationStudy 运行指定策略的消融对照实验
// 对每个对照组应用不同参数，运行相同的负载测试，收集对比数据
func (tr *TestRunner) RunAblationStudy(strategy StrategyType, groups []AblationGroup, quick bool) ([]AblationResult, error) {
	var results []AblationResult

	// 使用快速测试的阶段配置
	if quick {
		tr.cfg.StepPhases = []StepPhase{
			{Name: "warmup", Duration: 5 * time.Second, Concurrency: 3},
			{Name: "low", Duration: 10 * time.Second, Concurrency: 10},
			{Name: "medium", Duration: 10 * time.Second, Concurrency: 30},
			{Name: "high", Duration: 10 * time.Second, Concurrency: 60},
			{Name: "overload", Duration: 10 * time.Second, Concurrency: 120},
			{Name: "recovery", Duration: 10 * time.Second, Concurrency: 5},
		}
	}

	fmt.Printf("\n%s\n", strings.Repeat("=", 70))
	fmt.Printf("  消融对照实验: 策略=%s, 共 %d 组\n", strategy, len(groups))
	fmt.Printf("%s\n", strings.Repeat("=", 70))

	for i, group := range groups {
		fmt.Printf("\n%s\n", strings.Repeat("-", 60))
		fmt.Printf("  对照组 [%d/%d]: %s\n", i+1, len(groups), group.Name)
		fmt.Printf("  说明: %s\n", group.Description)
		fmt.Printf("%s\n", strings.Repeat("-", 60))

		// 从默认配置开始，应用对照组参数
		cfg := *tr.cfg
		group.ApplyTo(&cfg)
		cfg.Strategy = strategy
		cfg.LoadPattern = PatternStep

		// 创建临时 runner 用这组配置
		tempRunner := NewTestRunner(&cfg)
		summary, err := tempRunner.RunSingleStrategy(strategy, PatternStep, 1)
		if err != nil {
			fmt.Printf("[对照组失败] %s: %v\n", group.Name, err)
			continue
		}

		results = append(results, AblationResult{
			GroupName:   group.Name,
			Description: group.Description,
			Strategy:    strategy,
			Summary:     *summary,
		})

		// 对照组之间等待，让系统恢复
		if i < len(groups)-1 {
			fmt.Println("[等待] 3 秒后开始下一个对照组...")
			time.Sleep(3 * time.Second)
		}
	}

	// 打印消融对照表格
	if len(results) > 0 {
		printAblationTable(results)

		// 导出 CSV
		csvPath, err := WriteAblationToCSV(results, tr.cfg.OutputDir)
		if err != nil {
			fmt.Printf("[消融结果 CSV 导出失败] %v\n", err)
		} else {
			fmt.Printf("[消融结果 CSV 导出成功] %s\n", csvPath)
		}
	}

	return results, nil
}

// RunRajomonAblation 运行 Rajomon 参数消融对照实验
func (tr *TestRunner) RunRajomonAblation(quick bool) ([]AblationResult, error) {
	return tr.RunAblationStudy(StrategyRajomon, RajomonAblationGroups(), quick)
}

// RunStaticRateLimitAblation 运行静态限流参数消融对照实验
func (tr *TestRunner) RunStaticRateLimitAblation(quick bool) ([]AblationResult, error) {
	return tr.RunAblationStudy(StrategyStaticRateLimit, StaticRateLimitAblationGroups(), quick)
}

// RunCapacityAblation 运行后端容量消融对照实验（跑全部三种策略）
func (tr *TestRunner) RunCapacityAblation(quick bool) ([]AblationResult, error) {
	var allResults []AblationResult
	strategies := []StrategyType{StrategyNoGovernance, StrategyStaticRateLimit, StrategyRajomon}

	for _, strategy := range strategies {
		results, err := tr.RunAblationStudy(strategy, CapacityAblationGroups(), quick)
		if err != nil {
			fmt.Printf("[容量消融失败] %s: %v\n", strategy, err)
			continue
		}
		allResults = append(allResults, results...)
	}

	return allResults, nil
}

// RunFullAblation 运行完整的消融实验（Rajomon + 静态限流 + 容量）
func (tr *TestRunner) RunFullAblation(quick bool) error {
	fmt.Println("\n========== [Phase 1] Rajomon 参数消融 ==========")
	_, err := tr.RunRajomonAblation(quick)
	if err != nil {
		return fmt.Errorf("Rajomon 消融失败: %w", err)
	}

	fmt.Println("\n\n========== [Phase 2] 静态限流参数消融 ==========")
	_, err = tr.RunStaticRateLimitAblation(quick)
	if err != nil {
		return fmt.Errorf("静态限流消融失败: %w", err)
	}

	fmt.Println("\n\n========== [Phase 3] 后端容量消融 ==========")
	_, err = tr.RunCapacityAblation(quick)
	if err != nil {
		return fmt.Errorf("容量消融失败: %w", err)
	}

	return nil
}

// printAblationTable 打印消融对照结果表格
func printAblationTable(results []AblationResult) {
	fmt.Printf("\n%s\n", strings.Repeat("=", 100))
	fmt.Println("  消融对照结果汇总")
	fmt.Printf("%s\n", strings.Repeat("=", 100))

	// 表头
	fmt.Printf("  %-25s %8s %8s %10s %10s %10s %10s\n",
		"对照组", "吞吐量", "拒绝率", "预算10", "预算50", "预算100", "P95(ms)")
	fmt.Printf("  %s\n", strings.Repeat("-", 93))

	for _, r := range results {
		b10 := r.Summary.BudgetSuccessRate[10]
		b50 := r.Summary.BudgetSuccessRate[50]
		b100 := r.Summary.BudgetSuccessRate[100]

		fmt.Printf("  %-25s %8.2f %8.4f %10.4f %10.4f %10.4f %10.2f\n",
			r.GroupName,
			r.Summary.ThroughputRPS,
			r.Summary.RejectionRate,
			b10, b50, b100,
			float64(r.Summary.P95LatencyMs))
	}

	fmt.Printf("%s\n", strings.Repeat("=", 100))
}
