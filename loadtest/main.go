// main.go
// ==================== MCP 服务治理 — 基础负载测试入口（CLI 主程序） ====================
//
// 【文件在整体测试流程中的位置】
//
//	本文件是 loadtest 模块的唯一入口（包含 main() 函数）。
//	它负责：① 解析命令行参数 → ② 构建 TestConfig → ③ 创建 TestRunner → ④ 分发到对应的测试模式。
//	测试执行的后续流程为：runner.go(编排) → server.go(启服务) → loader.go(发压) → metrics.go(算指标) → result.go(导CSV)
//
// 【支持的测试模式 (mode)】
//   - quick          : 快速验证测试，使用缩短的阶段时长（~3分钟），用于日常开发验证。
//   - full           : 完整基准测试，标准阶段时长，支持多轮重复（-runs=3），用于生成论文级实验数据。
//   - single         : 单策略测试，隔离运行某一种策略（-strategy=rajomon），配合 -verbose 调试。
//   - cross-pattern  : 全量对比测试，三种策略 × 三种负载模式 = 9 次运行，生成完整的对比矩阵。
//   - ablation       : 消融对照实验，系统性探索各参数维度对治理效果的影响，支持子目标选择。
//
// 【用法示例】
//
//	go run ./loadtest/                                                    # 快速测试（验证正确性）
//	go run ./loadtest/ -mode=full                                         # 完整测试（论文数据）
//	go run ./loadtest/ -mode=single -strategy=rajomon                     # 单策略测试
//	go run ./loadtest/ -mode=quick                                        # 快速对比测试
//	go run ./loadtest/ -mode=cross-pattern                                # 三策略 × 三负载模式全量对比
//	go run ./loadtest/ -mode=ablation                                     # 快速消融对照（全部目标）
//	go run ./loadtest/ -mode=ablation -ablation-target=rajomon            # Rajomon 参数消融
//	go run ./loadtest/ -mode=ablation -ablation-target=rajomon -ablation-pattern=step  # 仅 step 模式
//	go run ./loadtest/ -mode=ablation -ablation-target=static             # 静态限流参数消融
//	go run ./loadtest/ -mode=ablation -ablation-target=capacity           # 后端容量消融
//
// 【输出】
//   - 控制台打印实时进度和汇总报告（包括吞吐量、延迟分位数、公平性等）
//   - CSV 文件输出到 loadtest/output/ 目录（可通过 -output 参数自定义路径）
//
// 【关键依赖】
//   - config.go          : TestConfig 结构体定义、DefaultTestConfig() 默认参数
//   - runner.go          : TestRunner 测试编排器
//   - ablation_config.go : 消融实验对照组参数定义
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strings"
	"time"
)

func main() {
	// ==================== 第一步：定义并解析命令行参数 ====================
	// 使用 Go 标准库 flag 包定义所有可配置参数
	// 这些参数会覆盖 config.go 中 DefaultTestConfig() 返回的默认值

	// --- 通用参数 ---
	mode := flag.String("mode", "quick", "测试模式: quick/full/single/cross-pattern/ablation")
	strategy := flag.String("strategy", "no_governance", "单策略模式的策略: no_governance / static_rate_limit / rajomon")
	pattern := flag.String("pattern", "step", "负载模式: step(阶梯) / sine(正弦) / poisson(泊松)")
	runs := flag.Int("runs", 1, "每种策略重复运行次数")
	output := flag.String("output", "loadtest/output", "CSV 输出目录")
	verbose := flag.Bool("verbose", false, "是否输出详细调试日志")
	seed := flag.Int64("seed", 42, "随机种子（确保可复现）")

	// --- Rajomon 动态定价算法参数 ---
	priceStep := flag.Int64("price-step", 10, "Rajomon 价格步长")
	latencyThreshold := flag.Duration("latency-threshold", 100*time.Microsecond, "Rajomon 延迟阈值")

	// --- 静态限流参数 ---
	rateLimit := flag.Float64("rate-limit", 30.0, "静态限流 QPS 阈值")

	// --- Mock 工具延迟 ---
	mockDelay := flag.Duration("mock-delay", 20*time.Millisecond, "Mock 工具基础处理延迟")

	// --- 消融实验参数 ---
	ablationTarget := flag.String("ablation-target", "all", "消融对照目标: all(全部) / rajomon / static / capacity")
	ablationPattern := flag.String("ablation-pattern", "all", "消融负载模式: all(三种全跑) / step / sine / poisson")

	// 执行参数解析（将命令行输入映射到上述变量）
	flag.Parse()

	// ==================== 第二步：初始化随机种子 ====================
	// 固定种子可确保在相同参数下的测试结果可复现（对比实验的基本要求）
	rand.Seed(*seed)

	// ==================== 跨平台一致性设置 ====================
	// 显式设置 GOMAXPROCS 确保不同平台(Windows/macOS/Linux)的并发调度行为一致
	if os.Getenv("GOMAXPROCS") == "" {
		runtime.GOMAXPROCS(4)
	}
	fmt.Printf("[平台信息] OS=%s, ARCH=%s, GOMAXPROCS=%d, NumCPU=%d\n",
		runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), runtime.NumCPU())

	// ==================== 第三步：打印欢迎横幅 ====================
	printBanner()

	// ==================== 第四步：构建测试配置 ====================
	// 从 config.go 的 DefaultTestConfig() 获取一份默认配置，
	// 然后用命令行参数覆盖其中部分字段。
	// 这种"默认值 + 覆盖"模式使得绝大多数参数无需在命令行中指定。
	cfg := DefaultTestConfig()
	cfg.OutputDir = *output
	cfg.Verbose = *verbose
	cfg.RandomSeed = *seed
	cfg.RajomonPriceStep = *priceStep
	cfg.RajomonLatencyThreshold = *latencyThreshold
	cfg.StaticRateLimitQPS = *rateLimit
	cfg.MockDelay = *mockDelay

	// ==================== 第五步：创建测试运行器 ====================
	// TestRunner 是测试的核心编排器（定义在 runner.go），
	// 它封装了"启动服务 → 生成负载 → 收集指标 → 导出 CSV"的完整流水线。
	runner := NewTestRunner(cfg)

	// ==================== 第六步：解析负载模式（pattern）并分发到对应测试模式（mode） ====================
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

	// ==================== 模式分发（switch-case）====================
	// 根据用户选择的 mode 调用 runner 的不同方法
	// 每个分支最终都调用 runner.RunSingleStrategy() 或其封装方法
	switch *mode {
	case "quick":
		// 【快速验证模式】
		// 调用 runner.RunQuickTest()，内部会将阶段时长缩短为 5~10 秒，
		// 然后对三种策略各跑一次阶梯式负载，总耗时约 3 分钟。
		// 适用场景：日常开发后快速检查系统行为是否正确。
		fmt.Println("模式: 快速验证测试")
		fmt.Println("目的: 验证三种策略的行为差异，确保系统稳定")
		fmt.Println()

		_, err := runner.RunQuickTest()
		if err != nil {
			fmt.Printf("测试失败: %v\n", err)
			os.Exit(1)
		}

	case "full":
		// 【完整基准测试模式】
		// 调用 runner.RunAllStrategies()，使用 DefaultTestConfig 中的标准阶段时长
		// （预热30s → 低60s → 中60s → 高60s → 过载60s → 恢复60s），
		// 每种策略运行 -runs 次取均值，总耗时约 30-60 分钟。
		// 适用场景：论文数据采集，需要多轮重复以消除随机性。
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
		// 【单策略测试模式】
		// 仅运行用户通过 -strategy 指定的那一种策略。
		// 适用场景：调试某个特定策略的行为，配合 -verbose 查看治理引擎的内部日志。
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
		// 【跨负载模式全量对比模式】
		// 调用 runner.RunCrossPatternComparison()，内部对每种负载模式
		// （step/sine/poisson）分别运行三种策略，共 3×3=9 次运行。
		// 适用场景：生成完整的"策略 × 负载模式"对比矩阵，用于论文图表。
		fmt.Println("模式: 跨负载模式全量对比（3策略 × 3负载模式 = 9次运行）")
		fmt.Println()

		_, err := runner.RunCrossPatternComparison(true)
		if err != nil {
			fmt.Printf("测试失败: %v\n", err)
			os.Exit(1)
		}

	case "ablation":
		// 【消融对照实验模式 (Ablation Study)】
		// 消融实验是科学研究中常用的方法：保持其他条件不变，只改变一个因素，
		// 观察该因素对结果的影响。
		// 本模式支持三种消融目标 (-ablation-target)：
		//   - rajomon  : Rajomon 算法的 6 个参数维度（见 ablation_config.go 的 A~F 组）
		//   - static   : 静态限流的 QPS/BurstSize 参数
		//   - capacity : 后端服务器的最大并发容量
		// 每个目标下有多个对照组，每组 × 每种负载模式 = 一次独立运行。
		// 所有结果通过 result.go 的 WriteAblationToCSV() 导出为统一的 CSV。
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

// printBanner 打印程序启动时的欢迎横幅
// 包含框架名称、对比目标和运行时间，方便在日志中定位运行起始点
func printBanner() {
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println("  MCP 服务治理 — 基础负载测试框架")
	fmt.Println("  三种策略对比: 无治理 vs 固定限流 vs Rajomon 动态定价")
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("  运行时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println()
}
