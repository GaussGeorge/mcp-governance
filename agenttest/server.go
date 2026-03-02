// server.go
// Agent 场景测试的服务端
// 三种策略的 HTTP 服务器，支持多种工具类型（不同延迟和 Token 消耗）
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

func (si *ServerInstance) Stop() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return si.Server.Shutdown(ctx)
}

func (si *ServerInstance) URL() string {
	return fmt.Sprintf("http://%s", si.Addr)
}

// ==================== 无治理服务端 ====================

func StartNoGovernanceServer(cfg *AgentTestConfig) (*ServerInstance, error) {
	server := nogovernance.NewMCPBaselineServer("no-governance-agent-test")

	var concurrentRequests int64
	maxConcurrency := int64(cfg.MaxServerConcurrency)
	overloadScale := cfg.OverloadLatencyScale

	// 为每种工具类型注册一个 Mock 处理器
	for _, tool := range cfg.ToolTypes {
		toolDef := tool // 闭包捕获
		server.RegisterTool(nogovernance.MCPTool{
			Name:        toolDef.Name,
			Description: fmt.Sprintf("Agent测试工具: %s (%s)", toolDef.Name, toolDef.Description),
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"input":    map[string]interface{}{"type": "string"},
					"task_id":  map[string]interface{}{"type": "string"},
					"step_idx": map[string]interface{}{"type": "integer"},
				},
			},
		}, func(ctx context.Context, params nogovernance.MCPToolCallParams) (*nogovernance.MCPToolCallResult, error) {
			current := atomic.AddInt64(&concurrentRequests, 1)
			defer atomic.AddInt64(&concurrentRequests, -1)

			// 超过最大容量：模拟资源耗尽
			if current > maxConcurrency {
				return nil, fmt.Errorf("服务器过载：当前并发 %d 超过容量 %d", current, maxConcurrency)
			}

			// 计算过载延迟
			loadRatio := float64(current) / float64(maxConcurrency)
			extraDelay := time.Duration(0)
			if loadRatio > 0.7 {
				overloadFactor := math.Pow((loadRatio-0.7)/0.3, 2) * overloadScale
				extraDelay = time.Duration(float64(toolDef.Delay) * overloadFactor)
			}

			simulateProcessing(toolDef.Delay+extraDelay, toolDef.DelayVar)

			return &nogovernance.MCPToolCallResult{
				Content: []nogovernance.ContentBlock{
					{Type: "text", Text: fmt.Sprintf("mock response from %s (no-governance)", toolDef.Name)},
				},
			}, nil
		})
	}

	return startHTTPServer(StrategyNoGovernance, server, cfg)
}

// ==================== 静态限流服务端 ====================

func StartStaticRateLimitServer(cfg *AgentTestConfig) (*ServerInstance, error) {
	rateLimitCfg := &staticratelimit.RateLimitConfig{
		MaxQPS:    cfg.StaticRateLimitQPS,
		BurstSize: cfg.StaticBurstSize,
	}

	server := staticratelimit.NewMCPStaticRateLimitServer("static-ratelimit-agent-test", rateLimitCfg)

	for _, tool := range cfg.ToolTypes {
		toolDef := tool
		server.RegisterTool(staticratelimit.MCPTool{
			Name:        toolDef.Name,
			Description: fmt.Sprintf("Agent测试工具: %s (%s)", toolDef.Name, toolDef.Description),
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"input":    map[string]interface{}{"type": "string"},
					"task_id":  map[string]interface{}{"type": "string"},
					"step_idx": map[string]interface{}{"type": "integer"},
				},
			},
		}, func(ctx context.Context, params staticratelimit.MCPToolCallParams) (*staticratelimit.MCPToolCallResult, error) {
			simulateProcessing(toolDef.Delay, toolDef.DelayVar)

			return &staticratelimit.MCPToolCallResult{
				Content: []staticratelimit.ContentBlock{
					{Type: "text", Text: fmt.Sprintf("mock response from %s (static-ratelimit)", toolDef.Name)},
				},
			}, nil
		})
	}

	return startHTTPServer(StrategyStaticRateLimit, server, cfg)
}

// ==================== Rajomon 动态定价服务端 ====================

func StartRajomonServer(cfg *AgentTestConfig) (*ServerInstance, error) {
	// 构造工具调用关系图（工具之间无下游依赖）
	callMap := make(map[string][]string)
	for _, tool := range cfg.ToolTypes {
		callMap[tool.Name] = []string{}
	}

	govOptions := map[string]interface{}{
		"loadShedding":     true,
		"pinpointQueuing":  true,
		"latencyThreshold": cfg.RajomonLatencyThreshold,
		"priceStep":        cfg.RajomonPriceStep,
		"priceUpdateRate":  cfg.RajomonPriceUpdateRate,
		"priceAggregation": cfg.RajomonPriceAggregation,
		"initprice":        cfg.RajomonInitPrice,
		"priceStrategy":    cfg.RajomonPriceStrategy,
		"priceFreq":        int64(1),
		"debug":            cfg.Verbose,
	}

	governor := mcpgov.NewMCPGovernor("rajomon-agent-test", callMap, govOptions)
	server := mcpgov.NewMCPServer("rajomon-agent-test", governor)

	for _, tool := range cfg.ToolTypes {
		toolDef := tool
		server.RegisterTool(mcpgov.MCPTool{
			Name:        toolDef.Name,
			Description: fmt.Sprintf("Agent测试工具: %s (%s)", toolDef.Name, toolDef.Description),
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"input":    map[string]interface{}{"type": "string"},
					"task_id":  map[string]interface{}{"type": "string"},
					"step_idx": map[string]interface{}{"type": "integer"},
				},
			},
		}, func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
			simulateProcessing(toolDef.Delay, toolDef.DelayVar)

			return &mcpgov.MCPToolCallResult{
				Content: []mcpgov.ContentBlock{
					{Type: "text", Text: fmt.Sprintf("mock response from %s (rajomon)", toolDef.Name)},
				},
			}, nil
		})
	}

	return startHTTPServer(StrategyRajomon, server, cfg)
}

// ==================== 辅助函数 ====================

func startHTTPServer(strategy StrategyType, handler http.Handler, cfg *AgentTestConfig) (*ServerInstance, error) {
	port := GetServerPort(strategy)
	addr := fmt.Sprintf("%s:%d", cfg.ServerAddr, port)

	listener, err := net.Listen("tcp", addr)
	if err != nil {
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

	go func() {
		if err := httpServer.Serve(listener); err != nil && err != http.ErrServerClosed {
			fmt.Printf("[服务器错误] %s: %v\n", strategy, err)
		}
	}()

	time.Sleep(100 * time.Millisecond)
	fmt.Printf("[服务器启动] %s 策略 → %s\n", strategy, actualAddr)
	return instance, nil
}

// simulateProcessing 模拟工具处理延迟（同 loadtest）
func simulateProcessing(baseDelay, delayVar time.Duration) {
	jitter := time.Duration(0)
	if delayVar > 0 {
		jitter = time.Duration(rand.Int63n(int64(delayVar)))
	}
	totalDelay := baseDelay + jitter

	// 70% 休眠 + 30% CPU 计算
	sleepPortion := time.Duration(float64(totalDelay) * 0.7)
	cpuPortion := time.Duration(float64(totalDelay) * 0.3)

	time.Sleep(sleepPortion)

	deadline := time.Now().Add(cpuPortion)
	x := 1.0
	for time.Now().Before(deadline) {
		for i := 0; i < 100; i++ {
			x = x*1.0000001 + 0.0000001
		}
	}
	_ = x
}
