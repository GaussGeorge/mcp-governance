// runner.go
// ==================== 测试运行器（测试编排核心） ====================
//
// 【文件在整体测试流程中的位置】
//
//	main.go 创建 TestRunner 后，调用其各种 Run 方法来执行测试。
//	TestRunner 是整个 loadtest 的"指挥官"，编排以下完整流水线：
//	  ① 启动 HTTP 服务器（调用 server.go 的 StartXxxServer）
//	  ② 创建负载生成器并运行（调用 loader.go 的 LoadGenerator.Run）
//	  ③ 收集请求结果（loader 返回 []RequestResult）
//	  ④ 计算统计指标（调用 metrics.go 的 CalculateMetrics）
//	  ⑤ 导出 CSV 文件（调用 result.go 的 WriteResultsToCSV/WriteSummaryToCSV）
//	  ⑥ 优雅关闭服务器（defer ServerInstance.Stop()）
//
// 【支持的运行模式】
//   - RunSingleStrategy    : 单策略运行（最底层方法，其他方法最终都调用它）
//   - RunAllStrategies     : 三种策略对比运行（循环调用 RunSingleStrategy）
//   - RunQuickTest         : 快速验证（缩短阶段时长后调用 RunAllStrategies）
//   - RunAblationStudy     : 消融实验（遍历对照组 × 负载模式，逐一运行）
//   - RunCrossPatternComparison : 三策略 × 三负载模式全量对比
//   - RunFullAblation      : 完整消融（Rajomon + 静态限流 + 容量）
package main

import (
	"fmt"
	"strings"
	"time"
)

// TestRunner 测试运行器
// 持有全局配置 cfg，所有测试方法都基于此配置运行。
// 消融实验时会临时复制配置并用 AblationGroup.ApplyTo() 覆盖部分参数。
type TestRunner struct {
	cfg *TestConfig // 全局测试配置（来自 main.go 解析的命令行参数 + 默认值）
}

// NewTestRunner 创建测试运行器实例
func NewTestRunner(cfg *TestConfig) *TestRunner {
	return &TestRunner{cfg: cfg}
}

// RunSingleStrategy 运行单策略测试（最底层的执行方法）
// 完整流水线：启动服务器 → 生成负载 → 收集指标 → 导出 CSV → 关闭服务器
// 【参数说明】
//
//	strategy : 要测试的治理策略
//	pattern  : 负载模式（step/sine/poisson）
//	runIndex : 当前是第几次运行（用于 CSV 文件名和日志区分）
//
// 【返回值】MetricsSummary 包含本次运行的全部统计指标
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
// 遍历三种策略（no_governance / static_rate_limit / rajomon），每种运行 runs 次
// 运行结束后：
//  1. 从每种策略取最后一次运行结果，打印对比表格
//  2. 将全部运行汇总导出为 CSV（summary_xxx.csv）
//
// 【参数说明】
//
//	pattern : 使用的负载模式（step/sine/poisson）
//	runs    : 每种策略重复运行次数（用于平均化结果）
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

// RunQuickTest 快速测试（CI 或本地验证用）
// 使用缩短的阶段时长（每阶段 5~10 秒，总计约 1 分钟），仅运行 step 模式各策略各 1 次
// 适用于快速检查代码改动后各策略的基本行为是否正常，不做统计显著性分析
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
// 消融实验核心思想：每次只改变一个参数（或一组参数），对比观察对性能指标的影响
// 支持 Rajomon 参数消融、静态限流参数消融、后端容量消融三类

// AblationResult 单个对照组的测试结果
// 一个 AblationResult = 一个参数组合 + 一种负载模式下的完整指标快照
type AblationResult struct {
	GroupName   string         // 对照组名称，如 "A1-高灵敏度" "B2-长窗口"
	Description string         // 对照组描述，说明参数变更内容
	Strategy    StrategyType   // 所属策略（rajomon / static_rate_limit 等）
	Pattern     LoadPattern    // 使用的负载模式（step / sine / poisson）
	Summary     MetricsSummary // 本次运行的全部统计指标
}

// RunAblationStudy 运行指定策略的消融对照实验（消融实验的核心调度方法）
// 执行流程：
//  1. 遍历 groups × patterns 的全排列（如 20 组 × 3 模式 = 60 次运行）
//  2. 对每个组合：从默认配置出发 → ApplyTo 覆盖参数 → 创建临时 Runner → RunSingleStrategy
//  3. 收集所有 AblationResult → 打印对照表格 → 导出 ablation_xxx.csv
//
// 【参数说明】
//
//	strategy : 要测试的策略
//	groups   : 消融对照组列表（来自 ablation_config.go）
//	patterns : 使用的负载模式列表，为空时默认使用全部三种
//	quick    : 是否使用缩短的阶段时长（加速测试）
func (tr *TestRunner) RunAblationStudy(strategy StrategyType, groups []AblationGroup, patterns []LoadPattern, quick bool) ([]AblationResult, error) {
	var results []AblationResult

	if len(patterns) == 0 {
		patterns = AllLoadPatterns()
	}

	// 快速测试配置
	quickStepPhases := []StepPhase{
		{Name: "warmup", Duration: 5 * time.Second, Concurrency: 3},
		{Name: "low", Duration: 10 * time.Second, Concurrency: 10},
		{Name: "medium", Duration: 10 * time.Second, Concurrency: 30},
		{Name: "high", Duration: 10 * time.Second, Concurrency: 60},
		{Name: "overload", Duration: 10 * time.Second, Concurrency: 120},
		{Name: "recovery", Duration: 10 * time.Second, Concurrency: 5},
	}
	quickDuration := 1 * time.Minute // sine/poisson 快速模式时长

	totalRuns := len(groups) * len(patterns)
	fmt.Printf("\n%s\n", strings.Repeat("=", 70))
	fmt.Printf("  消融对照实验: 策略=%s, %d 组 × %d 负载模式 = %d 次运行\n",
		strategy, len(groups), len(patterns), totalRuns)
	fmt.Printf("  负载模式: %v\n", patterns)
	fmt.Printf("%s\n", strings.Repeat("=", 70))

	runCount := 0
	for i, group := range groups {
		for _, pattern := range patterns {
			runCount++
			fmt.Printf("\n%s\n", strings.Repeat("-", 60))
			fmt.Printf("  [%d/%d] 对照组: %s | 负载模式: %s\n", runCount, totalRuns, group.Name, pattern)
			fmt.Printf("  说明: %s\n", group.Description)
			fmt.Printf("%s\n", strings.Repeat("-", 60))

			// 从默认配置开始，应用对照组参数
			cfg := *tr.cfg
			group.ApplyTo(&cfg)
			cfg.Strategy = strategy
			cfg.LoadPattern = pattern

			// 快速模式：缩短各负载模式的时长
			if quick {
				cfg.StepPhases = quickStepPhases
				cfg.Duration = quickDuration
			}

			tempRunner := NewTestRunner(&cfg)
			summary, err := tempRunner.RunSingleStrategy(strategy, pattern, 1)
			if err != nil {
				fmt.Printf("[对照组失败] %s / %s: %v\n", group.Name, pattern, err)
				continue
			}

			results = append(results, AblationResult{
				GroupName:   group.Name,
				Description: group.Description,
				Strategy:    strategy,
				Pattern:     pattern,
				Summary:     *summary,
			})

			// 运行之间等待，让系统恢复
			if runCount < totalRuns {
				fmt.Println("[等待] 2 秒后开始下一次运行...")
				time.Sleep(2 * time.Second)
			}
		}

		// 对照组之间额外等待
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

// RunRajomonAblation 运行 Rajomon 参数消融对照实验（全负载模式）
// 便捷方法：使用 RajomonAblationGroups() 的 20+ 组参数 × 3 种负载模式
// 测试 Rajomon 的灵敏度、窗口大小、价格范围、Token 预算分布、延迟阈值等参数的影响
func (tr *TestRunner) RunRajomonAblation(quick bool) ([]AblationResult, error) {
	return tr.RunAblationStudy(StrategyRajomon, RajomonAblationGroups(), AllLoadPatterns(), quick)
}

// RunRajomonAblationSinglePattern 运行 Rajomon 参数消融对照实验（指定单一负载模式）
// 当只需对某一种负载模式做消融分析时使用，减少运行总次数
func (tr *TestRunner) RunRajomonAblationSinglePattern(pattern LoadPattern, quick bool) ([]AblationResult, error) {
	return tr.RunAblationStudy(StrategyRajomon, RajomonAblationGroups(), []LoadPattern{pattern}, quick)
}

// RunStaticRateLimitAblation 运行静态限流参数消融对照实验（全负载模式）
// 便捷方法：使用 StaticRateLimitAblationGroups() 定义的参数组
// 测试不同令牌桶速率和突发量对限流效果的影响
func (tr *TestRunner) RunStaticRateLimitAblation(quick bool) ([]AblationResult, error) {
	return tr.RunAblationStudy(StrategyStaticRateLimit, StaticRateLimitAblationGroups(), AllLoadPatterns(), quick)
}

// RunCapacityAblation 运行后端容量消融对照实验（全部三种策略 × 全负载模式）
// 与其他消融不同：本方法同时对三种策略做容量消融，以便横向对比
// 不同处理容量对各策略在相同负载模式下的表现差异
func (tr *TestRunner) RunCapacityAblation(quick bool) ([]AblationResult, error) {
	var allResults []AblationResult
	strategies := []StrategyType{StrategyNoGovernance, StrategyStaticRateLimit, StrategyRajomon}

	for _, strategy := range strategies {
		results, err := tr.RunAblationStudy(strategy, CapacityAblationGroups(), AllLoadPatterns(), quick)
		if err != nil {
			fmt.Printf("[容量消融失败] %s: %v\n", strategy, err)
			continue
		}
		allResults = append(allResults, results...)
	}

	return allResults, nil
}

// RunCrossPatternComparison 运行三策略 × 三负载模式全量对比
// 外层循环遍历 3 种负载模式，内层调用 RunAllStrategies 遍历 3 种策略
// 共计 3 × 3 = 9 次独立运行，每次运行都输出单独的 CSV
// 适合做全面的策略 × 场景正交对比分析
func (tr *TestRunner) RunCrossPatternComparison(quick bool) ([]MetricsSummary, error) {
	var allSummaries []MetricsSummary

	for _, pattern := range AllLoadPatterns() {
		fmt.Printf("\n%s\n", strings.Repeat("=", 70))
		fmt.Printf("  负载模式: %s — 对比三种策略\n", pattern)
		fmt.Printf("%s\n", strings.Repeat("=", 70))

		if quick {
			tr.cfg.StepPhases = []StepPhase{
				{Name: "warmup", Duration: 5 * time.Second, Concurrency: 3},
				{Name: "low", Duration: 10 * time.Second, Concurrency: 10},
				{Name: "medium", Duration: 10 * time.Second, Concurrency: 30},
				{Name: "high", Duration: 10 * time.Second, Concurrency: 60},
				{Name: "overload", Duration: 10 * time.Second, Concurrency: 120},
				{Name: "recovery", Duration: 10 * time.Second, Concurrency: 5},
			}
			tr.cfg.Duration = 1 * time.Minute
		}

		summaries, err := tr.RunAllStrategies(pattern, 1)
		if err != nil {
			fmt.Printf("[负载模式 %s 失败] %v\n", pattern, err)
			continue
		}
		allSummaries = append(allSummaries, summaries...)
	}

	return allSummaries, nil
}

// RunFullAblation 运行完整的三阶段消融实验
// Phase 1: Rajomon 参数消融（20+ 组 × 3 模式）
// Phase 2: 静态限流参数消融（多组 × 3 模式）
// Phase 3: 后端容量消融（3 策略 × 多组 × 3 模式）
// 总运行次数可达 100+ 次，适合论文级的完整实验
func (tr *TestRunner) RunFullAblation(quick bool) error {
	fmt.Println("\n========== [Phase 1] Rajomon 参数消融（跨全部负载模式） ==========")
	_, err := tr.RunRajomonAblation(quick)
	if err != nil {
		return fmt.Errorf("Rajomon 消融失败: %w", err)
	}

	fmt.Println("\n\n========== [Phase 2] 静态限流参数消融（跨全部负载模式） ==========")
	_, err = tr.RunStaticRateLimitAblation(quick)
	if err != nil {
		return fmt.Errorf("静态限流消融失败: %w", err)
	}

	fmt.Println("\n\n========== [Phase 3] 后端容量消融（跨全部负载模式） ==========")
	_, err = tr.RunCapacityAblation(quick)
	if err != nil {
		return fmt.Errorf("容量消融失败: %w", err)
	}

	return nil
}

// printAblationTable 打印消融对照结果表格（终端输出）
// 表格列：对照组名 | 负载模式 | 吞吐量 | 拒绝率 | 预算10/50/100成功率 | P95延迟
// 用于在终端直观展示各对照组在不同预算层级下的公平性差异
func printAblationTable(results []AblationResult) {
	fmt.Printf("\n%s\n", strings.Repeat("=", 120))
	fmt.Println("  消融对照结果汇总")
	fmt.Printf("%s\n", strings.Repeat("=", 120))

	// 表头
	fmt.Printf("  %-28s %-8s %8s %8s %10s %10s %10s %10s\n",
		"对照组", "负载模式", "吞吐量", "拒绝率", "预算10", "预算50", "预算100", "P95(ms)")
	fmt.Printf("  %s\n", strings.Repeat("-", 113))

	for _, r := range results {
		b10 := r.Summary.BudgetSuccessRate[10]
		b50 := r.Summary.BudgetSuccessRate[50]
		b100 := r.Summary.BudgetSuccessRate[100]

		fmt.Printf("  %-28s %-8s %8.2f %8.4f %10.4f %10.4f %10.4f %10.2f\n",
			r.GroupName,
			string(r.Pattern),
			r.Summary.ThroughputRPS,
			r.Summary.RejectionRate,
			b10, b50, b100,
			float64(r.Summary.P95LatencyMs))
	}

	fmt.Printf("%s\n", strings.Repeat("=", 120))
}
