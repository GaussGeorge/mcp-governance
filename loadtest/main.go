// main.go
// MCP 服务治理负载测试入口
//
// 用法：
//
//	go run ./loadtest/                        # 快速测试（验证正确性）
//	go run ./loadtest/ -mode=full             # 完整测试（论文数据）
//	go run ./loadtest/ -mode=single -strategy=rajomon  # 单策略测试
//	go run ./loadtest/ -mode=quick            # 快速对比测试
//	go run ./loadtest/ -mode=cross-pattern           # 三策略 × 三负载模式全量对比
//	go run ./loadtest/ -mode=ablation                  # 快速消融对照（全部）
//	go run ./loadtest/ -mode=ablation -ablation-target=rajomon   # Rajomon 参数消融
//	go run ./loadtest/ -mode=ablation -ablation-target=rajomon -ablation-pattern=step  # 仅step模式
//	go run ./loadtest/ -mode=ablation -ablation-target=static    # 静态限流参数消融
//	go run ./loadtest/ -mode=ablation -ablation-target=capacity  # 后端容量消融
//
// 输出：
//   - 控制台打印实时进度和汇总报告
//   - CSV 文件输出到 loadtest/output/ 目录
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
	mode := flag.String("mode", "quick", "测试模式: quick/full/single/cross-pattern/ablation")
	strategy := flag.String("strategy", "no_governance", "单策略模式的策略: no_governance / static_rate_limit / rajomon")
	pattern := flag.String("pattern", "step", "负载模式: step(阶梯) / sine(正弦) / poisson(泊松)")
	runs := flag.Int("runs", 1, "每种策略重复运行次数")
	output := flag.String("output", "loadtest/output", "CSV 输出目录")
	verbose := flag.Bool("verbose", false, "是否输出详细调试日志")
	seed := flag.Int64("seed", 42, "随机种子（确保可复现）")

	// Rajomon 参数
	priceStep := flag.Int64("price-step", 10, "Rajomon 价格步长")
	latencyThreshold := flag.Duration("latency-threshold", 100*time.Microsecond, "Rajomon 延迟阈值")

	// 静态限流参数
	rateLimit := flag.Float64("rate-limit", 30.0, "静态限流 QPS 阈值")

	// Mock 延迟
	mockDelay := flag.Duration("mock-delay", 20*time.Millisecond, "Mock 工具基础处理延迟")

	// 消融实验参数
	ablationTarget := flag.String("ablation-target", "all", "消融对照目标: all(全部) / rajomon / static / capacity")
	ablationPattern := flag.String("ablation-pattern", "all", "消融负载模式: all(三种全跑) / step / sine / poisson")

	flag.Parse()

	// 固定随机种子
	rand.Seed(*seed)

	// 打印 Banner
	printBanner()

	// 构建配置
	cfg := DefaultTestConfig()
	cfg.OutputDir = *output
	cfg.Verbose = *verbose
	cfg.RandomSeed = *seed
	cfg.RajomonPriceStep = *priceStep
	cfg.RajomonLatencyThreshold = *latencyThreshold
	cfg.StaticRateLimitQPS = *rateLimit
	cfg.MockDelay = *mockDelay

	// 创建运行器
	runner := NewTestRunner(cfg)

	// 根据模式运行
	var loadPattern LoadPattern
	switch *pattern {
	case "step":
		loadPattern = PatternStep
	case "sine":
		loadPattern = PatternSine
	case "poisson":
		loadPattern = PatternPoisson
	default:
		fmt.Printf("未知的负载模式: %s\n", *pattern)
		os.Exit(1)
	}

	switch *mode {
	case "quick":
		// 快速测试：使用较短阶段，每种策略运行 1 次
		fmt.Println("模式: 快速验证测试")
		fmt.Println("目的: 验证三种策略的行为差异，确保系统稳定")
		fmt.Println()

		_, err := runner.RunQuickTest()
		if err != nil {
			fmt.Printf("测试失败: %v\n", err)
			os.Exit(1)
		}

	case "full":
		// 完整测试：标准阶段时长，每种策略运行 3 次
		fmt.Println("模式: 完整基准测试（论文数据）")
		fmt.Printf("负载模式: %s, 重复次数: %d\n", loadPattern, *runs)
		fmt.Println()

		if *runs < 1 {
			*runs = 3
		}

		_, err := runner.RunAllStrategies(loadPattern, *runs)
		if err != nil {
			fmt.Printf("测试失败: %v\n", err)
			os.Exit(1)
		}

	case "single":
		// 单策略测试
		stratType := StrategyType(*strategy)
		fmt.Printf("模式: 单策略测试 → %s\n", stratType)
		fmt.Printf("负载模式: %s\n", loadPattern)
		fmt.Println()

		_, err := runner.RunSingleStrategy(stratType, loadPattern, 1)
		if err != nil {
			fmt.Printf("测试失败: %v\n", err)
			os.Exit(1)
		}

	case "cross-pattern":
		// 三策略 × 三负载模式全量对比
		fmt.Println("模式: 跨负载模式全量对比（3策略 × 3负载模式 = 9次运行）")
		fmt.Println()

		_, err := runner.RunCrossPatternComparison(true)
		if err != nil {
			fmt.Printf("测试失败: %v\n", err)
			os.Exit(1)
		}

	case "ablation":
		// 消融对照实验
		fmt.Println("模式: 消融对照实验 (Ablation Study)")
		fmt.Printf("目标: %s, 负载模式: %s\n", *ablationTarget, *ablationPattern)
		fmt.Println("目的: 对比不同参数配置 × 不同负载模式对策略效果的影响")
		fmt.Println()

		quick := true // 消融实验默认使用快速测试配置

		// 解析消融实验的负载模式
		var ablationPatterns []LoadPattern
		switch *ablationPattern {
		case "all":
			ablationPatterns = AllLoadPatterns()
		case "step":
			ablationPatterns = []LoadPattern{PatternStep}
		case "sine":
			ablationPatterns = []LoadPattern{PatternSine}
		case "poisson":
			ablationPatterns = []LoadPattern{PatternPoisson}
		default:
			fmt.Printf("未知消融负载模式: %s（可选: all, step, sine, poisson）\n", *ablationPattern)
			os.Exit(1)
		}

		switch *ablationTarget {
		case "rajomon":
			fmt.Printf(">>> 运行 Rajomon 参数消融（%d 组 × %d 负载模式）\n",
				len(RajomonAblationGroups()), len(ablationPatterns))
			_, err := runner.RunAblationStudy(StrategyRajomon, RajomonAblationGroups(), ablationPatterns, quick)
			if err != nil {
				fmt.Printf("消融实验失败: %v\n", err)
				os.Exit(1)
			}
		case "static":
			fmt.Printf(">>> 运行静态限流参数消融（%d 组 × %d 负载模式）\n",
				len(StaticRateLimitAblationGroups()), len(ablationPatterns))
			_, err := runner.RunAblationStudy(StrategyStaticRateLimit, StaticRateLimitAblationGroups(), ablationPatterns, quick)
			if err != nil {
				fmt.Printf("消融实验失败: %v\n", err)
				os.Exit(1)
			}
		case "capacity":
			fmt.Println(">>> 运行后端容量消融对照组（三策略 × 容量变体）")
			strategies := []StrategyType{StrategyNoGovernance, StrategyStaticRateLimit, StrategyRajomon}
			for _, s := range strategies {
				_, err := runner.RunAblationStudy(s, CapacityAblationGroups(), ablationPatterns, quick)
				if err != nil {
					fmt.Printf("[容量消融失败] %s: %v\n", s, err)
				}
			}
		case "all":
			fmt.Println(">>> 运行完整消融实验（Rajomon + 静态限流 + 后端容量，跨全部负载模式）")
			err := runner.RunFullAblation(quick)
			if err != nil {
				fmt.Printf("消融实验失败: %v\n", err)
				os.Exit(1)
			}
		default:
			fmt.Printf("未知消融目标: %s（可选: all, rajomon, static, capacity）\n", *ablationTarget)
			os.Exit(1)
		}

	default:
		fmt.Printf("未知模式: %s\n", *mode)
		flag.PrintDefaults()
		os.Exit(1)
	}

	fmt.Println()
	fmt.Println("所有测试完成！CSV 结果已导出到:", cfg.OutputDir)
}

func printBanner() {
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println("  MCP 服务治理 — 基础负载测试框架")
	fmt.Println("  三种策略对比: 无治理 vs 固定限流 vs Rajomon 动态定价")
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("  运行时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println()
}
