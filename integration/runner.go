// runner.go
// ==================== 集成测试运行器（编排核心） ====================
//
// 与 loadtest/runner.go 对应, 但增加了:
//  1. Python MCP Bridge 连通性检查
//  2. 代理服务器启动/关闭管理
//  3. 真实 MCP 工具调用的编排
package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// TestRunner 集成测试运行器
type TestRunner struct {
	cfg *TestConfig
}

// NewTestRunner 创建测试运行器
func NewTestRunner(cfg *TestConfig) *TestRunner {
	return &TestRunner{cfg: cfg}
}

// CheckBridge 检查 Python MCP Bridge 是否可用
func (tr *TestRunner) CheckBridge() error {
	client := NewMCPBridgeClient(tr.cfg.MCPBridgeURL, 5*time.Second)

	fmt.Printf("[连通性检查] 正在检测 Python MCP Bridge: %s\n", tr.cfg.MCPBridgeURL)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 健康检查
	if err := client.HealthCheck(ctx); err != nil {
		return fmt.Errorf("Python MCP Bridge 不可用: %w\n  请先启动 Bridge: cd mcp_server && python -m server.bridge", err)
	}
	fmt.Println("[连通性检查] ✅ 健康检查通过")

	// 工具列表检查
	tools, err := client.ListTools(ctx)
	if err != nil {
		return fmt.Errorf("获取工具列表失败: %w", err)
	}
	fmt.Printf("[连通性检查] ✅ 可用工具 (%d): %s\n", len(tools), strings.Join(tools, ", "))

	// 验证目标工具存在
	toolFound := false
	for _, t := range tools {
		if t == tr.cfg.ToolName {
			toolFound = true
			break
		}
	}
	if !toolFound {
		return fmt.Errorf("目标工具 '%s' 在 Bridge 中不存在, 可用工具: %v", tr.cfg.ToolName, tools)
	}
	fmt.Printf("[连通性检查] ✅ 目标工具 '%s' 已确认可用\n", tr.cfg.ToolName)

	// 工具调用测试
	result, err := client.CallTool(ctx, tr.cfg.ToolName, tr.cfg.ToolArguments)
	if err != nil {
		return fmt.Errorf("工具调用测试失败: %w", err)
	}
	if len(result.Content) > 0 {
		text := result.Content[0].Text
		if len(text) > 80 {
			text = text[:80] + "..."
		}
		fmt.Printf("[连通性检查] ✅ 工具调用成功: %s\n", text)
	}

	return nil
}

// RunSingleStrategy 运行单策略测试
func (tr *TestRunner) RunSingleStrategy(strategy StrategyType, pattern LoadPattern, runIndex int) (*MetricsSummary, error) {
	cfg := *tr.cfg
	cfg.Strategy = strategy
	cfg.LoadPattern = pattern

	sep := strings.Repeat("*", 60)
	fmt.Printf("\n%s\n", sep)
	fmt.Printf("  集成测试: 策略=%s, 模式=%s, 第 %d 次\n", strategy, pattern, runIndex)
	fmt.Printf("  目标工具: %s → Python MCP Bridge: %s\n", cfg.ToolName, cfg.MCPBridgeURL)
	fmt.Printf("%s\n\n", sep)

	// 1. 启动代理服务器
	var proxy *ProxyInstance
	var err error

	switch strategy {
	case StrategyNoGovernance:
		proxy, err = StartNoGovernanceProxy(&cfg)
	case StrategyStaticRateLimit:
		proxy, err = StartStaticRateLimitProxy(&cfg)
	case StrategyRajomon:
		proxy, err = StartRajomonProxy(&cfg)
	default:
		return nil, fmt.Errorf("未知策略: %s", strategy)
	}

	if err != nil {
		return nil, fmt.Errorf("启动代理服务器失败: %w", err)
	}
	defer func() {
		fmt.Printf("[关闭代理] %s\n", strategy)
		proxy.Stop()
		time.Sleep(500 * time.Millisecond)
	}()

	// 等待代理完全就绪
	time.Sleep(300 * time.Millisecond)

	// 2. 创建负载生成器并运行
	loader := NewLoadGenerator(&cfg, proxy.URL())
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

// RunAllStrategies 运行三种策略的对比测试
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

			if run < runs || strategy != strategies[len(strategies)-1] {
				fmt.Println("[等待] 3 秒后开始下一轮 (让 Python 服务器恢复)...")
				time.Sleep(3 * time.Second)
			}
		}
	}

	if len(allSummaries) > 0 {
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

		summaryPath, err := WriteSummaryToCSV(allSummaries, tr.cfg.OutputDir)
		if err != nil {
			fmt.Printf("[汇总 CSV 导出失败] %v\n", err)
		} else {
			fmt.Printf("[汇总 CSV 导出成功] %s\n", summaryPath)
		}
	}

	return allSummaries, nil
}

// RunQuickTest 快速验证测试
func (tr *TestRunner) RunQuickTest() ([]MetricsSummary, error) {
	tr.cfg.StepPhases = []StepPhase{
		{Name: "warmup", Duration: 5 * time.Second, Concurrency: 3},
		{Name: "low", Duration: 10 * time.Second, Concurrency: 10},
		{Name: "medium", Duration: 10 * time.Second, Concurrency: 25},
		{Name: "high", Duration: 10 * time.Second, Concurrency: 50},
		{Name: "overload", Duration: 10 * time.Second, Concurrency: 100},
		{Name: "recovery", Duration: 10 * time.Second, Concurrency: 5},
	}
	tr.cfg.Duration = 1 * time.Minute

	return tr.RunAllStrategies(PatternStep, 1)
}

// RunCrossPatternComparison 三策略 × 三负载模式全量对比
func (tr *TestRunner) RunCrossPatternComparison(quick bool) ([]MetricsSummary, error) {
	var allSummaries []MetricsSummary

	for _, pattern := range AllLoadPatterns() {
		fmt.Printf("\n%s\n", strings.Repeat("=", 70))
		fmt.Printf("  负载模式: %s — 对比三种策略 (真实 MCP 集成)\n", pattern)
		fmt.Printf("%s\n", strings.Repeat("=", 70))

		if quick {
			tr.cfg.StepPhases = []StepPhase{
				{Name: "warmup", Duration: 5 * time.Second, Concurrency: 3},
				{Name: "low", Duration: 10 * time.Second, Concurrency: 10},
				{Name: "medium", Duration: 10 * time.Second, Concurrency: 25},
				{Name: "high", Duration: 10 * time.Second, Concurrency: 50},
				{Name: "overload", Duration: 10 * time.Second, Concurrency: 100},
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
