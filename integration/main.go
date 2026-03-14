// main.go
// ==================== MCP 集成测试入口（CLI 主程序） ====================
//
// 与 loadtest/main.go 对应, 但测试目标是真实 Python MCP 服务器 (通过 Bridge)。
//
// 前置条件:
//  1. 启动 Python MCP Bridge: cd mcp_server && python -m server.bridge --port 9000
//  2. 运行集成测试: go run ./integration/
//
// 用法示例:
//
//	go run ./integration/                                                     # 快速测试
//	go run ./integration/ -mode=quick                                         # 快速对比
//	go run ./integration/ -mode=full                                          # 完整测试
//	go run ./integration/ -mode=single -strategy=rajomon                      # 单策略
//	go run ./integration/ -mode=cross-pattern                                 # 全量对比
//	go run ./integration/ -tool=text_analyze -tool-args='{"text":"hello"}'    # 指定工具
//	go run ./integration/ -bridge-url=http://localhost:9001                    # 自定义 Bridge 地址
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"strings"
	"time"
)

func main() {
	// ==================== 命令行参数 ====================
	mode := flag.String("mode", "quick", "测试模式: quick/full/single/cross-pattern")
	strategy := flag.String("strategy", "no_governance", "单策略: no_governance / static_rate_limit / rajomon")
	pattern := flag.String("pattern", "step", "负载模式: step / sine / poisson")
	runs := flag.Int("runs", 1, "每种策略重复运行次数")
	output := flag.String("output", "integration/output", "CSV 输出目录")
	verbose := flag.Bool("verbose", false, "详细调试日志")
	seed := flag.Int64("seed", 42, "随机种子")

	// Python MCP Bridge 参数
	bridgeURL := flag.String("bridge-url", "http://localhost:9000", "Python MCP Bridge 地址")
	proxyPort := flag.Int("proxy-port", 8080, "Go 代理服务器端口")

	// 工具参数
	toolName := flag.String("tool", "calculate", "测试用工具名")
	toolArgsJSON := flag.String("tool-args", `{"expression": "2 + 3 * 4 - 1"}`, "工具参数 (JSON)")

	// Rajomon 参数
	priceStep := flag.Int64("price-step", 15, "Rajomon 价格步长")
	latencyThreshold := flag.Duration("latency-threshold", 150*time.Millisecond, "Rajomon 延迟阈值")

	// 静态限流参数
	rateLimit := flag.Float64("rate-limit", 30.0, "静态限流 QPS 阈值")

	// 跳过 Bridge 检查
	skipCheck := flag.Bool("skip-check", false, "跳过 Bridge 连通性检查")

	flag.Parse()

	// ==================== 初始化 ====================
	rand.Seed(*seed)

	// 跨平台一致性：显式设置 GOMAXPROCS
	if os.Getenv("GOMAXPROCS") == "" {
		runtime.GOMAXPROCS(4)
	}
	fmt.Printf("[平台信息] OS=%s, ARCH=%s, GOMAXPROCS=%d, NumCPU=%d\n",
		runtime.GOOS, runtime.GOARCH, runtime.GOMAXPROCS(0), runtime.NumCPU())

	printBanner()

	// 解析工具参数
	var toolArgs map[string]interface{}
	if err := json.Unmarshal([]byte(*toolArgsJSON), &toolArgs); err != nil {
		fmt.Printf("解析工具参数失败: %v\n", err)
		os.Exit(1)
	}

	// 构建配置
	cfg := DefaultTestConfig()
	cfg.OutputDir = *output
	cfg.Verbose = *verbose
	cfg.RandomSeed = *seed
	cfg.MCPBridgeURL = *bridgeURL
	cfg.ProxyPort = *proxyPort
	cfg.ToolName = *toolName
	cfg.ToolArguments = toolArgs
	cfg.RajomonPriceStep = *priceStep
	cfg.RajomonLatencyThreshold = *latencyThreshold
	cfg.StaticRateLimitQPS = *rateLimit

	// ==================== 创建运行器 ====================
	runner := NewTestRunner(cfg)

	// Bridge 连通性检查
	if !*skipCheck {
		if err := runner.CheckBridge(); err != nil {
			fmt.Printf("\n❌ %v\n", err)
			fmt.Println("\n请先启动 Python MCP Bridge:")
			fmt.Println("  cd mcp_server && python -m server.bridge --port 9000")
			os.Exit(1)
		}
		fmt.Println()
	}

	// ==================== 负载模式解析 ====================
	var loadPattern LoadPattern
	switch *pattern {
	case "step":
		loadPattern = PatternStep
	case "sine":
		loadPattern = PatternSine
	case "poisson":
		loadPattern = PatternPoisson
	default:
		fmt.Printf("未知负载模式: %s\n", *pattern)
		os.Exit(1)
	}

	// ==================== 模式分发 ====================
	switch *mode {
	case "quick":
		fmt.Println("模式: 快速验证测试 (真实 MCP 服务器)")
		fmt.Printf("目标: %s → %s\n\n", cfg.ToolName, cfg.MCPBridgeURL)

		_, err := runner.RunQuickTest()
		if err != nil {
			fmt.Printf("测试失败: %v\n", err)
			os.Exit(1)
		}

	case "full":
		fmt.Println("模式: 完整基准测试 (真实 MCP 服务器)")
		fmt.Printf("负载模式: %s, 重复次数: %d\n\n", loadPattern, *runs)

		if *runs < 1 {
			*runs = 3
		}

		_, err := runner.RunAllStrategies(loadPattern, *runs)
		if err != nil {
			fmt.Printf("测试失败: %v\n", err)
			os.Exit(1)
		}

	case "single":
		stratType := StrategyType(*strategy)
		fmt.Printf("模式: 单策略测试 → %s (真实 MCP 服务器)\n", stratType)
		fmt.Printf("负载模式: %s\n\n", loadPattern)

		_, err := runner.RunSingleStrategy(stratType, loadPattern, 1)
		if err != nil {
			fmt.Printf("测试失败: %v\n", err)
			os.Exit(1)
		}

	case "cross-pattern":
		fmt.Println("模式: 三策略 × 三负载模式全量对比 (真实 MCP 服务器)")
		fmt.Println()

		_, err := runner.RunCrossPatternComparison(true)
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
	fmt.Println("所有集成测试完成！CSV 结果已导出到:", cfg.OutputDir)
}

func printBanner() {
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println("  MCP 集成测试 — 真实 Python MCP 服务器 + Go 治理代理")
	fmt.Println("  三种策略对比: 无治理 vs 固定限流 vs Rajomon 动态定价")
	fmt.Println(strings.Repeat("=", 70))
	fmt.Printf("  运行时间: %s\n", time.Now().Format("2006-01-02 15:04:05"))
	fmt.Println(strings.Repeat("=", 70))
	fmt.Println()
}
