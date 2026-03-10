// server.go
// ==================== 三种策略的服务端启动和管理 ====================
//
// 【文件在整体测试流程中的位置】
//
//	runner.go 在每次测试运行前调用本文件的 StartXxxServer() 函数启动 HTTP 服务器。
//	服务器注册一个 Mock 工具（mock_tool），处理来自 loader.go 的 JSON-RPC 请求。
//	测试结束后，runner.go 调用 ServerInstance.Stop() 关闭服务器并释放端口。
//
// 【三种服务端的核心差异】
//
//	① StartNoGovernanceServer   — 不做任何流控，但模拟了真实后端的资源耗尽行为
//	② StartStaticRateLimitServer — 使用令牌桶算法进行固定 QPS 限流
//	③ StartRajomonServer        — 使用 MCPGovernor 治理引擎进行动态定价
//	三者都注册相同名称（cfg.ToolName）的 Mock 工具，确保 loader 端代码无需修改。
//
// 【过载模拟机制（仅无治理模式）】
//
//	为了使对比实验有意义，无治理模式需要真实地"崩溃"。
//	实现方式是通过 atomic 计数器跟踪并发请求数：
//	- 并发 < 70% 容量：正常处理（基础延迟 + 随机波动）
//	- 并发 70%~100% 容量：延迟指数级增长（模拟 CPU/内存资源竞争）
//	- 并发 > 100% 容量：直接返回错误（模拟 OOM 或线程池耗尽）
//
// 【请求数据流】
//
//	loader.go 发送 HTTP POST → 本文件启动的 HTTP Server 接收
//	  → 对于 Rajomon：MCPGovernor 中间件拦截，检查 tokens vs price
//	    → tokens >= price → 放行到 Mock 工具处理函数
//	    → tokens < price  → 直接返回 -32001 过载错误
//	  → 对于无治理：直接进入 Mock 工具处理函数
//	  → 对于静态限流：令牌桶检查 QPS → 超限返回 429
package main

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	mcpgov "mcp-governance"
	nogovernance "mcp-governance/baseline/no_governance"
	staticratelimit "mcp-governance/baseline/static_rate_limit"
)

// ServerInstance 管理一个策略的 HTTP 服务器实例
// 封装了 Go 标准库的 http.Server，添加了策略标识和地址信息。
// 生命周期由 runner.go 控制：创建 → 运行 → defer Stop()
type ServerInstance struct {
	Strategy StrategyType // 该实例使用的治理策略类型
	Server   *http.Server // Go 标准库 HTTP 服务器实例
	Addr     string       // 实际绑定的地址（如 "127.0.0.1:9003"）
	Port     int          // 实际绑定的端口号
	listener net.Listener // TCP 监听器（用于关闭时释放端口）
}

// Stop 关闭服务器（等待最多 5 秒让正在处理的请求完成）
func (si *ServerInstance) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return si.Server.Shutdown(ctx)
}

// URL 返回服务器的完整 HTTP URL（供 loader.go 作为请求目标）
func (si *ServerInstance) URL() string {
	return fmt.Sprintf("http://%s", si.Addr)
}

// ==================== 无治理服务端 ====================

// StartNoGovernanceServer 启动无治理基线服务端
// 【核心特点】不做任何流控，但内置了真实的过载模拟机制。
// 【过载行为模型】（使三种策略的对比有意义）：
//   - 并发 < 70% MaxServerConcurrency：正常处理（baseDelay + jitter）
//   - 并发 70%~100%：延迟显著增加，公式为 extraDelay = baseDelay × scale × ((loadRatio-0.7)/0.3)²
//   - 并发 > 100%：直接返回 500 错误（模拟服务器资源耗尽/OOM）
//
// 【为什么需要这个模型】无治理模式若不崩溃，就无法证明治理策略的价值。
func StartNoGovernanceServer(cfg *TestConfig) (*ServerInstance, error) {
	server := nogovernance.NewMCPBaselineServer("no-governance-loadtest")

	// 并发请求计数器
	var concurrentRequests int64
	maxConcurrency := int64(cfg.MaxServerConcurrency)
	overloadScale := cfg.OverloadLatencyScale

	// 注册 Mock 工具
	server.RegisterTool(nogovernance.MCPTool{
		Name:        cfg.ToolName,
		Description: "负载测试用 Mock 工具（无治理）",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"input": map[string]interface{}{"type": "string"},
			},
		},
	}, func(ctx context.Context, params nogovernance.MCPToolCallParams) (*nogovernance.MCPToolCallResult, error) {
		// 原子递增并发计数
		current := atomic.AddInt64(&concurrentRequests, 1)
		defer atomic.AddInt64(&concurrentRequests, -1)

		// 超过最大容量：模拟服务器资源耗尽，返回错误
		if current > maxConcurrency {
			return nil, fmt.Errorf("服务器过载：当前并发 %d 超过容量 %d", current, maxConcurrency)
		}

		// 计算过载比例并增加延迟
		loadRatio := float64(current) / float64(maxConcurrency)
		extraDelay := time.Duration(0)
		if loadRatio > 0.7 {
			// 超过 70% 容量后，延迟随负载指数增长
			// 公式：extraDelay = baseDelay * scale * (loadRatio - 0.7)^2 / 0.09
			overloadFactor := math.Pow((loadRatio-0.7)/0.3, 2) * overloadScale
			extraDelay = time.Duration(float64(cfg.MockDelay) * overloadFactor)
		}

		// 模拟处理延迟：基础延迟 + 随机波动 + 过载额外延迟
		simulateProcessing(cfg.MockDelay+extraDelay, cfg.MockDelayVar)

		return &nogovernance.MCPToolCallResult{
			Content: []nogovernance.ContentBlock{
				{Type: "text", Text: "mock response from no-governance server"},
			},
		}, nil
	})

	return startHTTPServer(StrategyNoGovernance, server, cfg)
}

// ==================== 静态限流服务端 ====================

// StartStaticRateLimitServer 启动固定阈值限流服务端
// 【核心特点】使用经典的令牌桶算法进行 QPS 限制。
// 参数来自 cfg.StaticRateLimitQPS 和 cfg.StaticBurstSize。
// 【与 Rajomon 的关键区别】
//   - 静态限流的阈值是运维人员预设的固定值，无法感知实际后端负载
//   - Rajomon 的"价格"根据排队延迟动态调整，能自适应负载变化
//
// 【注意】静态限流服务端不需要过载模拟（令牌桶本身就会拒绝超限请求）
func StartStaticRateLimitServer(cfg *TestConfig) (*ServerInstance, error) {
	rateLimitCfg := &staticratelimit.RateLimitConfig{
		MaxQPS:    cfg.StaticRateLimitQPS,
		BurstSize: cfg.StaticBurstSize,
	}

	server := staticratelimit.NewMCPStaticRateLimitServer("static-ratelimit-loadtest", rateLimitCfg)

	// 注册 Mock 工具
	server.RegisterTool(staticratelimit.MCPTool{
		Name:        cfg.ToolName,
		Description: "负载测试用 Mock 工具（静态限流）",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"input": map[string]interface{}{"type": "string"},
			},
		},
	}, func(ctx context.Context, params staticratelimit.MCPToolCallParams) (*staticratelimit.MCPToolCallResult, error) {
		simulateProcessing(cfg.MockDelay, cfg.MockDelayVar)

		return &staticratelimit.MCPToolCallResult{
			Content: []staticratelimit.ContentBlock{
				{Type: "text", Text: "mock response from static-ratelimit server"},
			},
		}, nil
	})

	return startHTTPServer(StrategyStaticRateLimit, server, cfg)
}

// ==================== Rajomon 动态定价服务端 ====================

// StartRajomonServer 启动 Rajomon 动态定价服务端
// 【核心特点】集成了 mcp_governor.go 中的 MCPGovernor 治理引擎。
// 【请求处理流程】
//  1. loader.go 发送 JSON-RPC 请求，其中 _meta.tokens = 客户端预算
//  2. MCPGovernor 中间件提取 tokens，与当前系统"价格"比较
//  3. 如果 tokens >= price：放行请求到 Mock 工具处理函数
//  4. 如果 tokens < price：触发 Load Shedding，返回 -32001/-32003 错误
//  5. 响应中通过 _meta.price 返回当前价格，供 loader 记录
//
// 【参数说明】
//
//	govOptions 中的参数直接映射到 config.go 中 Rajomon 相关的配置项，
//	消融实验通过 AblationGroup.ApplyTo() 覆盖这些参数。
func StartRajomonServer(cfg *TestConfig) (*ServerInstance, error) {
	// 配置工具调用关系图（callMap）
	// 在真实的 MCP 场景中，一个工具可能依赖下游其他工具（串行调用链），
	// 价格需要聚合自身和下游的价格。这里 Mock 工具没有下游依赖，设为空切片。
	callMap := map[string][]string{
		cfg.ToolName: {},
	}

	// 创建 MCPGovernor 治理引擎实例
	// 这是整个 Rajomon 算法的核心入口（定义在项目根目录的 mcp_governor.go 中），
	// 它会在内部维护价格状态并周期性更新。
	govOptions := map[string]interface{}{
		"loadShedding":     true,                        // 开启负载削减
		"pinpointQueuing":  true,                        // 开启排队延迟检测
		"latencyThreshold": cfg.RajomonLatencyThreshold, // 延迟阈值
		"priceStep":        cfg.RajomonPriceStep,        // 价格步长
		"priceUpdateRate":  cfg.RajomonPriceUpdateRate,  // 价格更新频率
		"priceAggregation": cfg.RajomonPriceAggregation, // 价格聚合策略
		"initprice":        cfg.RajomonInitPrice,        // 初始价格
		"priceStrategy":    cfg.RajomonPriceStrategy,    // 价格策略
		"priceFreq":        int64(1),                    // 每个请求都返回价格
		"debug":            cfg.Verbose,                 // 调试日志
	}

	governor := mcpgov.NewMCPGovernor("rajomon-loadtest", callMap, govOptions)
	server := mcpgov.NewMCPServer("rajomon-loadtest", governor)

	// 注册 Mock 工具
	server.RegisterTool(mcpgov.MCPTool{
		Name:        cfg.ToolName,
		Description: "负载测试用 Mock 工具（Rajomon 动态定价）",
		InputSchema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"input": map[string]interface{}{"type": "string"},
			},
		},
	}, func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		simulateProcessing(cfg.MockDelay, cfg.MockDelayVar)

		return &mcpgov.MCPToolCallResult{
			Content: []mcpgov.ContentBlock{
				{Type: "text", Text: "mock response from rajomon server"},
			},
		}, nil
	})

	return startHTTPServer(StrategyRajomon, server, cfg)
}

// ==================== 辅助函数 ====================

// startHTTPServer 通用 HTTP 服务器启动逻辑（三种策略共用）
// 功能：绑定端口 → 创建 http.Server → 后台 goroutine 运行 → 等待就绪
// 端口策略：优先使用 GetServerPort() 返回的固定端口，若被占用则自动切换到随机端口（:0）
func startHTTPServer(strategy StrategyType, handler http.Handler, cfg *TestConfig) (*ServerInstance, error) {
	port := GetServerPort(strategy)
	addr := fmt.Sprintf("%s:%d", cfg.ServerAddr, port)

	// 使用动态端口绑定，防止端口冲突
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		// 如果端口被占用，尝试随机端口
		listener, err = net.Listen("tcp", fmt.Sprintf("%s:0", cfg.ServerAddr))
		if err != nil {
			return nil, fmt.Errorf("启动 %s 服务器失败: %w", strategy, err)
		}
	}

	actualAddr := listener.Addr().String()
	httpServer := &http.Server{
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	instance := &ServerInstance{
		Strategy: strategy,
		Server:   httpServer,
		Addr:     actualAddr,
		Port:     listener.Addr().(*net.TCPAddr).Port,
		listener: listener,
	}

	// 在后台启动服务器
	go func() {
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Printf("[服务器错误] %s: %v\n", strategy, err)
		}
	}()

	// 等待服务器就绪
	time.Sleep(100 * time.Millisecond)

	fmt.Printf("[服务器启动] %s 策略 → %s\n", strategy, actualAddr)
	return instance, nil
}

// simulateProcessing 模拟工具处理延迟
// 【为什么不直接 time.Sleep？】
//
//	纯 Sleep 不会消耗 CPU，Go 调度器不会产生排队延迟，
//	导致 Rajomon 的排队延迟检测机制（pinpointQueuing）无法生效。
//
// 【混合策略】将总延迟拆分为：
//   - 70% 休眠（释放 CPU，模拟 I/O 等待）
//   - 30% CPU 密集型计算（触发 Go 调度器的协程排队，使 queuingDelay.go 能检测到延迟）
//
// 这种混合方式使得测试更接近真实的工具处理行为。
func simulateProcessing(baseDelay, delayVar time.Duration) {
	// 基础休眠延迟
	jitter := time.Duration(0)
	if delayVar > 0 {
		jitter = time.Duration(rand.Int63n(int64(delayVar)))
	}
	totalDelay := baseDelay + jitter

	// 将总延迟拆分：70% 休眠 + 30% CPU 计算（触发调度器延迟）
	sleepPortion := time.Duration(float64(totalDelay) * 0.7)
	cpuPortion := time.Duration(float64(totalDelay) * 0.3)

	time.Sleep(sleepPortion)

	// CPU 密集型操作，使 Go 调度器产生排队延迟
	deadline := time.Now().Add(cpuPortion)
	x := 1.0
	for time.Now().Before(deadline) {
		for i := 0; i < 100; i++ {
			x = x*1.0000001 + 0.0000001
		}
	}
	_ = x
}
