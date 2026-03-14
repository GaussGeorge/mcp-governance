// proxy.go
// ==================== Go 治理代理服务器 ====================
//
// 本模块实现三种策略的代理服务器，作为 Go 负载生成器与 Python MCP 服务器之间的中间层。
// 核心区别于 loadtest/server.go：
//   - loadtest/server.go 使用 Mock 工具处理器 (simulateProcessing)
//   - proxy.go 使用 MCPBridgeClient 将请求转发给真实的 Python MCP 服务器
//
// 架构：
//
//	Go 负载生成器 --HTTP/JSON-RPC--> Go 代理 (本模块) --HTTP/JSON-RPC--> Python MCP Bridge
//
// 三种代理策略：
//  1. 无治理: 直接转发 (请求到达 → 转发 Python → 返回结果)
//  2. 静态限流: 令牌桶限流 + 转发 (请求到达 → 检查速率 → 转发 Python → 返回结果)
//  3. Rajomon 动态定价: MCPGovernor + 转发 (请求到达 → 价格检查 → 转发 Python → 返回结果)
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	mcpgov "mcp-governance"
)

// ProxyInstance 代理服务器实例
type ProxyInstance struct {
	server   *http.Server
	listener net.Listener
	strategy StrategyType
}

// URL 返回代理服务器的完整 URL
func (p *ProxyInstance) URL() string {
	return fmt.Sprintf("http://%s", p.listener.Addr().String())
}

// Stop 关闭代理服务器
func (p *ProxyInstance) Stop() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p.server.Shutdown(ctx)
}

// ==================== 策略 1: 无治理代理 ====================

// StartNoGovernanceProxy 启动无治理的代理服务器
// 直接将所有请求转发到 Python MCP Bridge, 不做任何准入控制。
// Python Bridge 内置了资源限制模拟 (ConcurrencyLimiter)，
// 高并发时会自然产生延迟升高和错误，与 loadtest/server.go 行为一致。
func StartNoGovernanceProxy(cfg *TestConfig) (*ProxyInstance, error) {
	client := NewMCPBridgeClient(cfg.MCPBridgeURL, cfg.HTTPClientTimeout)

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		var req mcpgov.JSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONResponse(w, mcpgov.NewErrorResponse(nil, -32700, "JSON 解析错误", err.Error()))
			return
		}

		switch req.Method {
		case "initialize":
			writeJSONResponse(w, mcpgov.NewSuccessResponse(req.ID, mcpgov.MCPInitializeResult{
				ProtocolVersion: "2024-11-05",
				ServerInfo:      mcpgov.Implementation{Name: "no-governance-proxy", Version: "1.0.0"},
				Capabilities:    mcpgov.ServerCapabilities{Tools: &mcpgov.ToolsCapability{ListChanged: false}},
			}))
		case "tools/list":
			tools, _ := client.ListTools(r.Context())
			writeJSONResponse(w, mcpgov.NewSuccessResponse(req.ID, map[string]interface{}{"tools": tools}))
		case "tools/call":
			// 无治理：直接转发，依赖 Python Bridge 的 ConcurrencyLimiter 产生真实的过载行为
			var params mcpgov.MCPToolCallParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				writeJSONResponse(w, mcpgov.NewErrorResponse(req.ID, -32602, "无效参数", err.Error()))
				return
			}

			result, err := client.CallTool(r.Context(), params.Name, params.Arguments)
			if err != nil {
				writeJSONResponse(w, mcpgov.NewErrorResponse(req.ID, -32603, err.Error(), nil))
				return
			}

			writeJSONResponse(w, mcpgov.NewSuccessResponse(req.ID, result))
		case "ping":
			writeJSONResponse(w, mcpgov.NewSuccessResponse(req.ID, map[string]interface{}{}))
		default:
			writeJSONResponse(w, mcpgov.NewErrorResponse(req.ID, -32601, "方法未找到", nil))
		}
	})

	return startHTTPProxy(mux, cfg.ProxyPort, StrategyNoGovernance)
}

// ==================== 策略 2: 静态限流代理 ====================

// tokenBucket 令牌桶限流器
type tokenBucket struct {
	mu       sync.Mutex
	tokens   float64
	maxTokens float64
	rate     float64    // 每秒令牌生成速率
	lastTime time.Time
}

func newTokenBucket(rate float64, burst int) *tokenBucket {
	return &tokenBucket{
		tokens:    float64(burst),
		maxTokens: float64(burst),
		rate:      rate,
		lastTime:  time.Now(),
	}
}

func (tb *tokenBucket) allow() bool {
	tb.mu.Lock()
	defer tb.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(tb.lastTime).Seconds()
	tb.lastTime = now

	// 补充令牌
	tb.tokens += elapsed * tb.rate
	if tb.tokens > tb.maxTokens {
		tb.tokens = tb.maxTokens
	}

	if tb.tokens >= 1.0 {
		tb.tokens -= 1.0
		return true
	}
	return false
}

// StartStaticRateLimitProxy 启动静态限流代理服务器
// 使用令牌桶算法控制请求速率, 超过速率的请求直接被拒绝
func StartStaticRateLimitProxy(cfg *TestConfig) (*ProxyInstance, error) {
	client := NewMCPBridgeClient(cfg.MCPBridgeURL, cfg.HTTPClientTimeout)
	bucket := newTokenBucket(cfg.StaticRateLimitQPS, cfg.StaticBurstSize)

	mux := http.NewServeMux()
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		var req mcpgov.JSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONResponse(w, mcpgov.NewErrorResponse(nil, -32700, "JSON 解析错误", err.Error()))
			return
		}

		switch req.Method {
		case "initialize":
			writeJSONResponse(w, mcpgov.NewSuccessResponse(req.ID, mcpgov.MCPInitializeResult{
				ProtocolVersion: "2024-11-05",
				ServerInfo:      mcpgov.Implementation{Name: "static-rate-limit-proxy", Version: "1.0.0"},
				Capabilities:    mcpgov.ServerCapabilities{Tools: &mcpgov.ToolsCapability{ListChanged: false}},
			}))
		case "tools/call":
			// 令牌桶检查
			if !bucket.allow() {
				writeJSONResponse(w, mcpgov.NewErrorResponse(req.ID, -32002,
					"请求被限流, 超过速率限制", map[string]string{"limit": fmt.Sprintf("%.0f QPS", cfg.StaticRateLimitQPS)}))
				return
			}

			// 通过限流检查, 转发到 Python MCP Bridge
			var params mcpgov.MCPToolCallParams
			if err := json.Unmarshal(req.Params, &params); err != nil {
				writeJSONResponse(w, mcpgov.NewErrorResponse(req.ID, -32602, "无效参数", err.Error()))
				return
			}

			result, err := client.CallTool(r.Context(), params.Name, params.Arguments)
			if err != nil {
				writeJSONResponse(w, mcpgov.NewErrorResponse(req.ID, -32603, err.Error(), nil))
				return
			}

			writeJSONResponse(w, mcpgov.NewSuccessResponse(req.ID, result))
		case "ping":
			writeJSONResponse(w, mcpgov.NewSuccessResponse(req.ID, map[string]interface{}{}))
		default:
			writeJSONResponse(w, mcpgov.NewErrorResponse(req.ID, -32601, "方法未找到", nil))
		}
	})

	return startHTTPProxy(mux, cfg.ProxyPort, StrategyStaticRateLimit)
}

// ==================== 策略 3: Rajomon 动态定价代理 ====================

// StartRajomonProxy 启动 Rajomon 动态定价代理服务器
// 核心：使用 MCPGovernor 进行基于动态定价的准入控制
// 请求通过价格检查后, 才被转发到 Python MCP Bridge
//
// 重要设计说明：
//   使用 pinpointLatency (业务延迟检测) 而非 pinpointQueuing (Go runtime 调度延迟检测)。
//   原因：代理服务器只是转发 HTTP 请求到 Python Bridge，Go goroutine 不会产生显著的
//   调度延迟（网络 I/O 等待由 netpoller 处理，不占用 CPU）。
//   真正的瓶颈在 Python Bridge 的 ConcurrencyLimiter，其延迟反映在 HTTP 响应时间中。
//   因此，我们在工具处理函数中测量请求延迟，并通过 AddObservedDelay 报告给 governor，
//   由 latencyCheck() 后台协程据此动态调整价格。
func StartRajomonProxy(cfg *TestConfig) (*ProxyInstance, error) {
	client := NewMCPBridgeClient(cfg.MCPBridgeURL, cfg.HTTPClientTimeout)

	// 工具调用关系图 (单工具场景, 无下游依赖)
	callMap := map[string][]string{
		cfg.ToolName: {},
	}

	// 配置 MCPGovernor 选项
	// 使用 pinpointLatency 模式：基于实际观察到的 HTTP 响应延迟来驱动价格调整
	govOptions := map[string]interface{}{
		"loadShedding":     true,
		"pinpointLatency":  true,                      // 基于业务延迟检测，而非 Go runtime 排队延迟
		"latencyThreshold": cfg.RajomonLatencyThreshold,
		"priceStep":        cfg.RajomonPriceStep,
		"priceUpdateRate":  cfg.RajomonPriceUpdateRate,
		"priceStrategy":    cfg.RajomonPriceStrategy,
		"priceAggregation": cfg.RajomonPriceAggregation,
		"priceFreq":        int64(1),
	}

	governor := mcpgov.NewMCPGovernor("rajomon-proxy", callMap, govOptions)
	mcpServer := mcpgov.NewMCPServer("rajomon-proxy", governor)

	// 注册工具处理器 — 关键差异点:
	// loadtest 使用 simulateProcessing() 模拟延迟
	// 这里使用 MCPBridgeClient 调用真实 Python 工具
	// 同时测量请求延迟并报告给 governor，驱动 latencyCheck() 价格调整
	mcpServer.RegisterTool(
		mcpgov.MCPTool{
			Name:        cfg.ToolName,
			Description: "通过 Python MCP Bridge 调用的真实工具",
			InputSchema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{},
			},
		},
		func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
			// 测量请求延迟并报告给 governor
			start := time.Now()
			result, err := client.CallTool(ctx, params.Name, params.Arguments)
			elapsed := time.Since(start)

			// 无论成功或失败，都向 governor 报告延迟
			// latencyCheck() 会将累积延迟与 (阈值 × 请求数) 比较
			governor.AddObservedDelay(elapsed)

			return result, err
		},
	)

	// 创建 HTTP 处理器
	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpServer)

	return startHTTPProxy(mux, cfg.ProxyPort, StrategyRajomon)
}

// ==================== 辅助函数 ====================

// startHTTPProxy 启动 HTTP 代理服务器 (通用)
func startHTTPProxy(handler http.Handler, port int, strategy StrategyType) (*ProxyInstance, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		// 端口被占用, 尝试随机端口
		listener, err = net.Listen("tcp", ":0")
		if err != nil {
			return nil, fmt.Errorf("无法监听端口: %w", err)
		}
	}

	server := &http.Server{
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	go server.Serve(listener)

	fmt.Printf("[代理已启动] 策略=%s, 地址=%s\n", strategy, listener.Addr().String())

	return &ProxyInstance{
		server:   server,
		listener: listener,
		strategy: strategy,
	}, nil
}

// writeJSONResponse 将 JSON-RPC 响应写入 HTTP response
func writeJSONResponse(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
