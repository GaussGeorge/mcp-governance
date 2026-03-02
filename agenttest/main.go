// main.go
// MCP 服务治理 — Agent 场景负载测试入口
//
// 用法：
//
//	go run ./agenttest/                                 # 快速测试（验证正确性）
//	go run ./agenttest/ -mode=full                      # 完整测试（论文数据）
//	go run ./agenttest/ -mode=single -strategy=rajomon  # 单策略测试
//	go run ./agenttest/ -mode=quick                     # 快速对比测试
//	go run ./agenttest/ -mode=burst                     # 突发负载对比
//	go run ./agenttest/ -mode=poisson                   # 泊松负载对比
//	go run ./agenttest/ -mode=sine                      # 正弦负载对比
//
// 输出：
//   - 控制台打印实时进度和汇总报告
//   - CSV 文件输出到 agenttest/output/ 目录（步骤级 + 任务级）
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"
)

func main() {
	// 命令行参数
	mode := flag.String("mode", "quick", "测试模式: quick/full/single/burst/poisson/sine")
	strategy := flag.String("strategy", "rajomon", "单策略模式的策略: no_governance / static_rate_limit / rajomon")
	pattern := flag.String("pattern", "burst", "负载模式: burst(突发) / poisson(泊松) / sine(正弦)")
	runs := flag.Int("runs", 1, "每种策略重复运行次数")
	output := flag.String("output", "agenttest/output", "CSV 输出目录")
	verbose := flag.Bool("verbose", false, "是否输出详细调试日志")
	seed := flag.Int64("seed", 42, "随机种子")

	// Agent 配置
	numAgents := flag.Int("agents", 100, "Agent 数量")
	minSteps := flag.Int("min-steps", 1, "任务最小步骤数")
	maxSteps := flag.Int("max-steps", 5, "任务最大步骤数")

	// Rajomon 参数
	priceStep := flag.Int64("price-step", 5, "Rajomon 价格步长")
	latencyThreshold := flag.Duration("latency-threshold", 2000*time.Microsecond, "Rajomon 延迟阈值")

	// 静态限流参数
	rateLimit := flag.Float64("rate-limit", 30.0, "静态限流 QPS 阈值")

	// 服务端容量
	maxConcurrency := flag.Int("max-concurrency", 50, "服务端最大并发容量")

	flag.Parse()

	rand.Seed(*seed)

	printBanner()

	// 构建配置
	cfg := DefaultAgentTestConfig()
	cfg.OutputDir = *output
	cfg.Verbose = *verbose
	cfg.RandomSeed = *seed
	cfg.NumAgents = *numAgents
	cfg.MinSteps = *minSteps
	cfg.MaxSteps = *maxSteps
	cfg.RajomonPriceStep = *priceStep
	cfg.RajomonLatencyThreshold = *latencyThreshold
	cfg.StaticRateLimitQPS = *rateLimit
	cfg.MaxServerConcurrency = *maxConcurrency

	runner := NewAgentTestRunner(cfg)

	// 解析负载模式
	var loadPattern LoadPattern
	switch *pattern {
	case "burst":
		loadPattern = PatternBurst
	case "poisson":
		loadPattern = PatternPoisson
	case "sine":
		loadPattern = PatternSine
	default:
		fmt.Printf("未知负载模式: %s\n", *pattern)
		os.Exit(1)
	}

	switch *mode {
	case "quick":
		fmt.Println("模式: 快速验证测试（Agent场景）")
		fmt.Println("目的: 验证三种策略在Agent多步骤任务下的行为差异")
		fmt.Println()

		cfg.OutputDir = *output + "/test_agent_quick"
		runner = NewAgentTestRunner(cfg)
		_, err := runner.RunQuickTest()
		if err != nil {
			fmt.Printf("测试失败: %v\n", err)
			os.Exit(1)
		}

	case "full":
		fmt.Println("模式: 完整Agent场景测试（论文数据）")
		fmt.Printf("负载模式: burst(突发), 重复次数: %d\n", *runs)
		fmt.Println()

		cfg.OutputDir = *output + "/test_agent_full"
		runner = NewAgentTestRunner(cfg)
		if *runs < 1 {
			*runs = 3
		}
		_, err := runner.RunFullTest(*runs)
		if err != nil {
			fmt.Printf("测试失败: %v\n", err)
			os.Exit(1)
		}

	case "single":
		stratType := StrategyType(*strategy)
		fmt.Printf("模式: 单策略Agent测试 → %s\n", stratType)
		fmt.Printf("负载模式: %s\n", loadPattern)
		fmt.Println()

		cfg.OutputDir = *output + "/test_agent_single"
		runner = NewAgentTestRunner(cfg)
		_, err := runner.RunSingleStrategy(stratType, loadPattern, 1)
		if err != nil {
			fmt.Printf("测试失败: %v\n", err)
			os.Exit(1)
		}

	case "burst":
		fmt.Println("模式: 突发负载Agent对比测试")
		fmt.Println()

		cfg.OutputDir = *output + "/test_agent_burst"
		runner = NewAgentTestRunner(cfg)
		_, err := runner.RunAllStrategies(PatternBurst, *runs)
		if err != nil {
			fmt.Printf("测试失败: %v\n", err)
			os.Exit(1)
		}

	case "poisson":
		fmt.Println("模式: 泊松负载Agent对比测试")
		fmt.Println()

		cfg.OutputDir = *output + "/test_agent_poisson"
		cfg.LoadPattern = PatternPoisson
		runner = NewAgentTestRunner(cfg)
		_, err := runner.RunAllStrategies(PatternPoisson, *runs)
		if err != nil {
			fmt.Printf("测试失败: %v\n", err)
			os.Exit(1)
		}

	case "sine":
		fmt.Println("模式: 正弦负载Agent对比测试")
		fmt.Println()

		cfg.OutputDir = *output + "/test_agent_sine"
		cfg.LoadPattern = PatternSine
		runner = NewAgentTestRunner(cfg)
		_, err := runner.RunAllStrategies(PatternSine, *runs)
		if err != nil {
			fmt.Printf("测试失败: %v\n", err)
			os.Exit(1)
		}

	default:
		fmt.Printf("未知模式: %s\n", *mode)
		flag.PrintDefaults()
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("所有Agent场景测试完成！CSV 结果已导出到:", cfg.OutputDir)
}

func printBanner() {
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println("  MCP 服务治理 — Agent 场景负载测试")
	fmt.Println("  模拟多步骤任务的Agent行为，验证动态定价机制")
	fmt.Println("  三种策略对比: 无治理 vs 固定限流 vs Rajomon 动态定价")
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("  运行时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println()
}
