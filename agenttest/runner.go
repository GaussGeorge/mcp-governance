// runner.go
// Agent 场景测试运行器
// 编排服务器启动、Agent负载生成、指标收集、结果导出的完整流程
package main

import (
	"fmt"
	"strings"
	"time"
)

// AgentTestRunner Agent 场景测试运行器
type AgentTestRunner struct {
	cfg *AgentTestConfig
}

// NewAgentTestRunner 创建测试运行器
func NewAgentTestRunner(cfg *AgentTestConfig) *AgentTestRunner {
	return &AgentTestRunner{cfg: cfg}
}

// RunSingleStrategy 运行单策略的 Agent 场景测试
func (tr *AgentTestRunner) RunSingleStrategy(strategy StrategyType, pattern LoadPattern, runIndex int) (*AgentMetricsSummary, error) {
	cfg := *tr.cfg
	cfg.Strategy = strategy
	cfg.LoadPattern = pattern

	sep := strings.Repeat("*", 70)
	fmt.Printf("\n%s\n", sep)
	fmt.Printf("  Agent场景测试: 策略=%s, 模式=%s, 第 %d 次\n", strategy, pattern, runIndex)
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
		time.Sleep(500 * time.Millisecond)
	}()

	time.Sleep(200 * time.Millisecond)

	// 2. 创建 Agent 负载生成器并运行
	loader := NewAgentLoadGenerator(&cfg, serverInstance.URL())
	stepResults := loader.Run()

	fmt.Printf("[收集结果] 共 %d 条步骤记录\n", len(stepResults))

	// 3. 构建任务级汇总
	taskSummaries := BuildTaskSummaries(stepResults)
	fmt.Printf("[任务汇总] 共 %d 个任务\n", len(taskSummaries))

	// 4. 计算综合指标
	summary := CalculateAgentMetrics(stepResults, taskSummaries, strategy, pattern, runIndex)
	PrintAgentSummary(summary)

	// 5. 导出 CSV
	stepCSV, err := WriteStepResultsToCSV(stepResults, cfg.OutputDir, strategy, pattern, runIndex)
	if err != nil {
		fmt.Printf("[步骤CSV导出失败] %v\n", err)
	} else {
		fmt.Printf("[步骤CSV导出成功] %s\n", stepCSV)
	}

	taskCSV, err := WriteTaskSummaryToCSV(taskSummaries, cfg.OutputDir, strategy, pattern, runIndex)
	if err != nil {
		fmt.Printf("[任务CSV导出失败] %v\n", err)
	} else {
		fmt.Printf("[任务CSV导出成功] %s\n", taskCSV)
	}

	return &summary, nil
}

// RunAllStrategies 运行三种策略的对比测试
func (tr *AgentTestRunner) RunAllStrategies(pattern LoadPattern, runs int) ([]AgentMetricsSummary, error) {
	strategies := []StrategyType{StrategyNoGovernance, StrategyStaticRateLimit, StrategyRajomon}
	var allSummaries []AgentMetricsSummary

	for _, strategy := range strategies {
		for run := 1; run <= runs; run++ {
			summary, err := tr.RunSingleStrategy(strategy, pattern, run)
			if err != nil {
				fmt.Printf("[错误] 策略 %s 第 %d 次运行失败: %v\n", strategy, run, err)
				continue
			}
			allSummaries = append(allSummaries, *summary)

			if run < runs || strategy != strategies[len(strategies)-1] {
				fmt.Println("[等待] 2 秒后开始下一轮...")
				time.Sleep(2 * time.Second)
			}
		}
	}

	// 打印对比表格
	if len(allSummaries) > 0 {
		var comparison []AgentMetricsSummary
		seen := make(map[StrategyType]bool)
		for i := len(allSummaries) - 1; i >= 0; i-- {
			s := allSummaries[i]
			if !seen[s.Strategy] {
				seen[s.Strategy] = true
				comparison = append([]AgentMetricsSummary{s}, comparison...)
			}
		}
		PrintAgentComparisonTable(comparison)

		// 导出汇总 CSV
		summaryPath, err := WriteAgentMetricsSummaryToCSV(allSummaries, tr.cfg.OutputDir)
		if err != nil {
			fmt.Printf("[汇总CSV导出失败] %v\n", err)
		} else {
			fmt.Printf("[汇总CSV导出成功] %s\n", summaryPath)
		}
	}

	return allSummaries, nil
}

// RunQuickTest 快速测试（缩短阶段时长）
func (tr *AgentTestRunner) RunQuickTest() ([]AgentMetricsSummary, error) {
	tr.cfg.BurstPhases = []BurstPhase{
		{Name: "warmup", Duration: 10 * time.Second, TaskRate: 2},
		{Name: "normal", Duration: 15 * time.Second, TaskRate: 5},
		{Name: "burst", Duration: 15 * time.Second, TaskRate: 20},
		{Name: "overload", Duration: 20 * time.Second, TaskRate: 40},
		{Name: "recovery", Duration: 10 * time.Second, TaskRate: 2},
	}
	tr.cfg.Duration = 1 * time.Minute
	tr.cfg.NumAgents = 50

	return tr.RunAllStrategies(PatternBurst, 1)
}

// RunFullTest 完整测试（论文数据级别）
func (tr *AgentTestRunner) RunFullTest(runs int) ([]AgentMetricsSummary, error) {
	return tr.RunAllStrategies(PatternBurst, runs)
}
