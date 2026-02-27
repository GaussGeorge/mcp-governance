// server.go
// 三种策略的服务端启动和管理
// 每种策略创建独立的 HTTP 服务器，注册相同的 Mock 工具处理函数
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
type ServerInstance struct {
	Strategy StrategyType
	Server   *http.Server
	Addr     string
	Port     int
	listener net.Listener
}

// Stop 优雅关闭服务器
func (si *ServerInstance) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return si.Server.Shutdown(ctx)
}

// URL 返回服务器的完整 URL
func (si *ServerInstance) URL() string {
	return fmt.Sprintf("http://%s", si.Addr)
}

// ==================== 无治理服务端 ====================

// StartNoGovernanceServer 启动无治理基线服务端
// 添加并发容量限制，模拟真实后端资源耗尽行为：
//   - 并发 < 70% 容量：正常处理
//   - 并发 70%~100% 容量：延迟显著增加（模拟资源竞争）
//   - 并发 > 容量：返回 500 错误（模拟服务器崩溃/资源耗尽）
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
func StartRajomonServer(cfg *TestConfig) (*ServerInstance, error) {
	// 配置工具调用关系（Mock 工具没有下游依赖）
	callMap := map[string][]string{
		cfg.ToolName: {},
	}

	// 创建 MCPGovernor 治理引擎
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

// startHTTPServer 通用 HTTP 服务器启动逻辑
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
// 使用 CPU 密集型操作 + Sleep 的混合方式，使排队延迟检测能够生效
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
