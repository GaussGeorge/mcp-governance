// loader.go
// ==================== 负载生成器核心逻辑（压测客户端） ====================
//
// 【文件在整体测试流程中的位置】
//
//	runner.go 启动完服务器后，创建 LoadGenerator 实例并调用 Run()。
//	LoadGenerator 是整个压测系统的"炮弹制造厂"：
//	① 根据负载模式（step/sine/poisson）决定何时、以多少并发发送请求
//	② 每个 worker（goroutine）循环调用 sendOneRequest() 发送 MCP JSON-RPC 请求
//	③ 将每个请求的结果（延迟、状态码、是否被拒绝等）记录到 results 切片中
//	④ Run() 返回后，runner.go 将 results 传给 metrics.go 计算统计指标
//
// 【请求构造细节】
//   - 协议格式：JSON-RPC 2.0（符合 MCP 规范）
//   - 方法名：固定为 "tools/call"
//   - 工具名：使用 cfg.ToolName（默认 "mock_tool"）
//   - Token 预算：仅 Rajomon 策略时在 _meta.tokens 中携带（从 cfg.Budgets 随机选取）
//   - HTTP 传输：POST 到 server.go 启动的 HTTP 服务器的 /mcp 端点
//
// 【并发模型】
//   - Step 模式：每个阶段创建 N 个 goroutine（worker），阶段结束后通过 context 取消
//   - Sine 模式：每秒根据正弦函数计算目标并发数，动态增减 worker
//   - Poisson 模式：以泊松过程间隔启动独立的 goroutine 发送单次请求
//
// 【无外部依赖】
//
//	不使用任何第三方压测工具（如 wrk、vegeta），全部用 Go 原生并发实现，
//	原因是需要精确控制请求中的 _meta.tokens 字段和预算分布。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// ==================== JSON-RPC 请求/响应类型（客户端侧） ====================
// 以下结构体定义了符合 MCP 协议（JSON-RPC 2.0）的请求和响应格式。
// 它们是 loader 与 server 之间通信的"契约"。
// 注意：这些类型独立于 mcp_protocol.go 中的服务端类型，是客户端视角的定义。

// clientJSONRPCRequest 客户端发送的 JSON-RPC 请求
// 对应 MCP 协议中 Client → Server 的消息格式
type clientJSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"` // 固定为 "2.0"
	ID      int64       `json:"id"`      // 请求唯一标识（原子递增生成）
	Method  string      `json:"method"`  // 固定为 "tools/call"（MCP 工具调用方法）
	Params  interface{} `json:"params"`  // 工具调用参数（clientToolCallParams 结构体）
}

// clientToolCallParams 客户端发送的工具调用参数
// 嵌套在 clientJSONRPCRequest.Params 中
type clientToolCallParams struct {
	Name      string                 `json:"name"`                // 要调用的工具名称（如 "mock_tool"）
	Arguments map[string]interface{} `json:"arguments,omitempty"` // 工具的业务参数
	Meta      *clientMeta            `json:"_meta,omitempty"`     // MCP 治理元数据（仅 Rajomon 策略时填充）
}

// clientMeta 客户端治理元数据
// 这是 Rajomon 动态定价的关键字段：客户端在此处"出价"
type clientMeta struct {
	Tokens int64  `json:"tokens,omitempty"` // Token 预算（客户端愿意为本次调用支付的价格上限）
	Name   string `json:"name,omitempty"`   // 客户端标识（用于日志追踪）
	Method string `json:"method,omitempty"` // 调用的工具名（冗余字段，便于服务端日志）
}

// clientJSONRPCResponse 客户端接收的 JSON-RPC 响应
// result 和 error 二选一：成功时有 result，失败时有 error
type clientJSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`          // 固定为 "2.0"
	ID      interface{}     `json:"id"`               // 对应请求的 ID
	Result  json.RawMessage `json:"result,omitempty"` // 成功响应（工具返回的内容）
	Error   *clientRPCError `json:"error,omitempty"`  // 错误响应（过载/限流/内部错误）
}

// clientRPCError JSON-RPC 错误
// 关键错误码：-32001(过载), -32002(限流), -32003(令牌不足)
type clientRPCError struct {
	Code    int             `json:"code"`           // 错误码（对照 mcp_protocol.go 中的常量定义）
	Message string          `json:"message"`        // 人类可读的错误描述
	Data    json.RawMessage `json:"data,omitempty"` // 附加数据（Rajomon 会在此返回当前价格）
}

// clientToolCallResult 解析后的工具调用成功结果
// 服务端通过 _meta.price 返回当前价格，供分析使用
type clientToolCallResult struct {
	Content []struct {
		Type string `json:"type"` // 内容类型（固定为 "text"）
		Text string `json:"text"` // 内容文本（Mock 工具返回的固定字符串）
	} `json:"content"`
	IsError bool `json:"isError"` // 是否为业务层错误
	Meta    *struct {
		Price string `json:"price"` // 服务端返回的当前价格（用于记录价格变化轨迹）
		Name  string `json:"name"`  // 服务端节点名称
	} `json:"_meta"`
}

// ==================== 负载生成器 ====================

// LoadGenerator 负载生成器（压测引擎核心）
// 职责：按照配置的负载模式，使用多 goroutine 并发向目标服务器发送请求，
// 并将每个请求的详细结果（延迟、错误码、价格等）收集到 results 切片中。
type LoadGenerator struct {
	cfg          *TestConfig     // 测试配置（决定发压行为的所有参数）
	serverURL    string          // 目标服务器 URL（如 "http://127.0.0.1:9003"）
	httpClient   *http.Client    // 复用的 HTTP 客户端（连接池配置为 500 连接，避免连接耗尽）
	results      []RequestResult // 所有请求的结果记录（线程安全写入，Run() 结束后返回）
	resultsMu    sync.Mutex      // 保护 results 切片的互斥锁（多 goroutine 并发写入）
	requestIDGen int64           // 请求 ID 原子计数器（确保每个请求有唯一标识）
	rng          *rand.Rand      // 随机数生成器（使用固定种子，确保可复现）
}

// NewLoadGenerator 创建负载生成器
// HTTP 客户端配置了 500 个连接池和 10 秒超时，足以支撑高并发场景
func NewLoadGenerator(cfg *TestConfig, serverURL string) *LoadGenerator {
	return &LoadGenerator{
		cfg:       cfg,
		serverURL: serverURL,
		httpClient: &http.Client{
			Timeout: cfg.HTTPClientTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        500,
				MaxIdleConnsPerHost: 500,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		results: make([]RequestResult, 0, 10000),
		rng:     rand.New(rand.NewSource(cfg.RandomSeed)),
	}
}

// pickBudget 按权重随机选择一个预算值
// 【实现原理】轮盘赌选择算法（Roulette Wheel Selection）。
// 将权重 [0.3, 0.4, 0.3] 累加为 [0.3, 0.7, 1.0]，
// 生成 [0, 1) 的随机数，落在哪个区间就选择对应的预算。
// 如果 BudgetWeights 为空或长度不匹配，则退化为均匀分布。
func (lg *LoadGenerator) pickBudget() int {
	budgets := lg.cfg.Budgets
	weights := lg.cfg.BudgetWeights

	// 无权重或权重长度不匹配 → 均匀随机
	if len(weights) == 0 || len(weights) != len(budgets) {
		return budgets[rand.Intn(len(budgets))]
	}

	// 基于权重的轮盘赌选择
	r := rand.Float64()
	cumulative := 0.0
	for i, w := range weights {
		cumulative += w
		if r < cumulative {
			return budgets[i]
		}
	}
	// 浮点误差兜底
	return budgets[len(budgets)-1]
}

// Run 运行负载测试，返回所有请求结果
// 这是 LoadGenerator 的唯一公开入口。根据 cfg.LoadPattern 分发到对应的实现：
//   - PatternStep    → runStepPattern()    : 多阶段顺序执行
//   - PatternSine    → runSinePattern()    : 正弦波并发控制
//   - PatternPoisson → runPoissonPattern() : 泊松过程到达
//
// 返回值 []RequestResult 包含每个请求的完整记录，供 metrics.go 分析。
func (lg *LoadGenerator) Run() []RequestResult {
	fmt.Printf("[负载生成器] 开始运行，策略=%s，模式=%s，目标=%s\n",
		lg.cfg.Strategy, lg.cfg.LoadPattern, lg.serverURL)

	switch lg.cfg.LoadPattern {
	case PatternStep:
		lg.runStepPattern()
	case PatternSine:
		lg.runSinePattern()
	case PatternPoisson:
		lg.runPoissonPattern()
	default:
		fmt.Printf("[负载生成器] 未知的负载模式: %s，使用阶梯模式\n", lg.cfg.LoadPattern)
		lg.runStepPattern()
	}

	lg.resultsMu.Lock()
	defer lg.resultsMu.Unlock()
	return lg.results
}

// ==================== 阶梯式负载 ====================
// 按照 cfg.StepPhases 定义的阶段顺序执行：
// 每个阶段创建 Concurrency 个 worker goroutine，持续发送请求直到 Duration 耗尽。
// 阶段之间是串行的：上一个阶段的所有 worker 退出后，才进入下一阶段。

func (lg *LoadGenerator) runStepPattern() {
	for _, phase := range lg.cfg.StepPhases {
		fmt.Printf("[阶段: %s] 并发=%d, 持续=%s\n", phase.Name, phase.Concurrency, phase.Duration)

		ctx, cancel := context.WithTimeout(context.Background(), phase.Duration)

		// 启动指定数量的 worker（每个 worker 是一个独立的 goroutine）
		// worker 内部会循环调用 sendOneRequest() 直到 ctx 被取消
		var wg sync.WaitGroup
		for i := 0; i < phase.Concurrency; i++ {
			wg.Add(1)
			go func(workerID int) {
				defer wg.Done()
				lg.worker(ctx, phase.Name)
			}(i)
		}

		// 等待阶段结束
		<-ctx.Done()
		cancel()
		wg.Wait()

		fmt.Printf("[阶段: %s] 完成，已收集 %d 条结果\n", phase.Name, len(lg.results))
	}
}

// ==================== 正弦波负载 ====================
// 并发数随时间呈正弦函数变化：concurrency = SineBase + SineAmplitude × sin(2π·t/SinePeriod)
// 每秒通过 ticker 重新计算目标并发数，然后通过 cancel-and-recreate 模式调整 worker 数量。
// 【注意】这种实现方式会在并发数变化时产生短暂的工作线程间断。

func (lg *LoadGenerator) runSinePattern() {
	startTime := time.Now()
	duration := lg.cfg.Duration

	fmt.Printf("[正弦波负载] 基础=%d, 振幅=%d, 周期=%s, 总时长=%s\n",
		lg.cfg.SineBase, lg.cfg.SineAmplitude, lg.cfg.SinePeriod, duration)

	// 每秒调整一次并发数
	adjustInterval := 1 * time.Second
	ticker := time.NewTicker(adjustInterval)
	defer ticker.Stop()

	var (
		currentCancel   context.CancelFunc
		currentWg       sync.WaitGroup
		lastConcurrency int
	)

	for {
		select {
		case <-ticker.C:
			elapsed := time.Since(startTime)
			if elapsed >= duration {
				// 测试结束：取消所有 worker 并等待退出
				if currentCancel != nil {
					currentCancel()
				}
				currentWg.Wait()
				return
			}

			// 计算当前目标并发数
			t := elapsed.Seconds()
			period := lg.cfg.SinePeriod.Seconds()
			concurrency := lg.cfg.SineBase + int(float64(lg.cfg.SineAmplitude)*math.Sin(2*math.Pi*t/period))
			if concurrency < 1 {
				concurrency = 1
			}

			// 并发数未变化，跳过调整
			if concurrency == lastConcurrency {
				continue
			}
			lastConcurrency = concurrency

			// 取消所有旧 worker 并等待它们退出
			if currentCancel != nil {
				currentCancel()
			}
			currentWg.Wait()

			// 启动新一批 worker，使用 context 控制生命周期
			ctx, cancel := context.WithCancel(context.Background())
			currentCancel = cancel

			phaseName := fmt.Sprintf("sine_c%d", concurrency)
			for i := 0; i < concurrency; i++ {
				currentWg.Add(1)
				go func() {
					defer currentWg.Done()
					lg.worker(ctx, phaseName)
				}()
			}
		}
	}
}

// ==================== 泊松到达负载 ====================
// 模拟符合泊松过程的请求到达：请求间隔时间服从指数分布 Exp(λ)。
// λ = cfg.PoissonQPS（平均每秒请求数）。
// 这是最贴近真实互联网流量的模型：请求到达时间完全随机，可能出现突发簇。
// 【实现方式】与 step/sine 不同，这里每个请求都创建一个独立的 goroutine。

func (lg *LoadGenerator) runPoissonPattern() {
	duration := lg.cfg.Duration
	lambda := lg.cfg.PoissonQPS // 平均每秒请求数

	fmt.Printf("[泊松负载] 平均 QPS=%.1f, 总时长=%s\n", lambda, duration)

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var wg sync.WaitGroup

	for {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		default:
			// 泊松过程：请求间隔服从指数分布
			interval := time.Duration(rand.ExpFloat64()/lambda*1e9) * time.Nanosecond
			time.Sleep(interval)

			// 每个请求启动一个 goroutine
			wg.Add(1)
			go func() {
				defer wg.Done()
				rejected := lg.sendOneRequest("poisson")
				if rejected && lg.cfg.RejectionBackoff > 0 {
					time.Sleep(lg.cfg.RejectionBackoff)
				}
			}()
		}
	}
}

// ==================== Worker 实现 ====================

// worker 在给定 context 有效期内持续发送请求
// 这是 step 和 sine 模式的工作单元。Worker 的生命周期由 ctx 控制：
//   - ctx 有效时：循环发送请求，请求间等待 cfg.RequestInterval（默认10ms）
//   - ctx 取消时：退出循环（阶段结束或正弦波并发数调整）
//
// 如果请求被拒绝（服务端返回过载错误），会额外等待 cfg.RejectionBackoff（默认100ms），
// 模拟真实客户端的退避重试行为，避免被拒请求立即重试加剧拥塞。
func (lg *LoadGenerator) worker(ctx context.Context, phase string) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
			rejected := lg.sendOneRequest(phase)
			if rejected && lg.cfg.RejectionBackoff > 0 {
				// 被拒绝后退避等待，模拟真实客户端行为，避免立即重试拉高请求数
				time.Sleep(lg.cfg.RejectionBackoff)
			} else {
				// 正常请求间隔
				time.Sleep(lg.cfg.RequestInterval)
			}
		}
	}
}

// sendOneRequest 发送一个 MCP tools/call 请求并记录结果
// 这是整个 loader 的核心函数，完成了从构造请求到解析响应的完整流程：
//  1. 原子递增生成请求 ID
//  2. 从预算池中随机选取一个 Token 预算值
//  3. 构造 JSON-RPC 请求（Rajomon 模式下附加 _meta.tokens）
//  4. 通过 HTTP POST 发送到 server.go 的 /mcp 端点
//  5. 解析 JSON-RPC 响应，提取错误码和动态价格
//  6. 将完整结果记录到 results 切片中
//
// 返回 true 表示请求失败/被拒绝（调用方据此决定是否退避）
func (lg *LoadGenerator) sendOneRequest(phase string) bool {
	// 生成请求 ID（原子操作，线程安全）
	reqID := atomic.AddInt64(&lg.requestIDGen, 1)

	// 按权重随机选择预算（模拟不同"财力"的客户端）
	budget := lg.pickBudget()

	// 构造 JSON-RPC 请求体
	params := clientToolCallParams{
		Name:      lg.cfg.ToolName,                         // 调用的工具名
		Arguments: map[string]interface{}{"input": "test"}, // 固定测试参数
	}

	// 【关键】仅 Rajomon 策略时，在 _meta 中携带 tokens（客户端出价）
	// 无治理和静态限流策略不需要 _meta，服务端不会读取这个字段
	if lg.cfg.Strategy == StrategyRajomon {
		params.Meta = &clientMeta{
			Tokens: int64(budget),
			Name:   "loadtest-client",
			Method: lg.cfg.ToolName,
		}
	}

	rpcReq := clientJSONRPCRequest{
		JSONRPC: "2.0",
		ID:      reqID,
		Method:  "tools/call",
		Params:  params,
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		lg.recordResult(RequestResult{
			Timestamp:    time.Now().UnixMilli(),
			RequestID:    reqID,
			Phase:        phase,
			ClientBudget: budget,
			LatencyMs:    0,
			StatusCode:   -1,
			ErrorMsg:     fmt.Sprintf("序列化请求失败: %v", err),
		})
		return true
	}

	// 发送 HTTP POST 请求到服务端的 /mcp 端点，并测量往返延迟
	start := time.Now()
	resp, err := lg.httpClient.Post(lg.serverURL+"/mcp", "application/json", bytes.NewReader(body))
	latency := time.Since(start).Milliseconds()

	result := RequestResult{
		Timestamp:    time.Now().UnixMilli(),
		RequestID:    reqID,
		Phase:        phase,
		ClientBudget: budget,
		LatencyMs:    latency,
	}

	if err != nil {
		result.StatusCode = -1
		result.ErrorMsg = fmt.Sprintf("网络错误: %v", err)
		lg.recordResult(result)
		return true
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		result.StatusCode = -1
		result.ErrorMsg = fmt.Sprintf("读取响应失败: %v", err)
		lg.recordResult(result)
		return true
	}

	result.StatusCode = resp.StatusCode

	// 解析 JSON-RPC 响应（判断是成功还是错误）
	var rpcResp clientJSONRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		result.ErrorMsg = fmt.Sprintf("解析响应失败: %v", err)
		lg.recordResult(result)
		return true
	}

	// 处理错误响应（JSON-RPC error 不为空）
	if rpcResp.Error != nil {
		result.ErrorCode = rpcResp.Error.Code
		result.ErrorMsg = rpcResp.Error.Message

		// 判断是否为治理层拒绝（区别于业务层错误）
		// 这些错误码定义在 mcp_protocol.go 中
		switch rpcResp.Error.Code {
		case -32001, -32002, -32003, -32000: // 过载、限流、令牌不足
			result.Rejected = true // 标记为"被治理层拒绝"（而非服务故障）
		}

		// 尝试从 error.data 中提取价格（Rajomon 在拒绝响应中也会返回当前价格，用于分析）
		if rpcResp.Error.Data != nil {
			var errorData map[string]interface{}
			if err := json.Unmarshal(rpcResp.Error.Data, &errorData); err == nil {
				if price, ok := errorData["price"]; ok {
					result.Price = fmt.Sprintf("%v", price)
				}
			}
		}
	} else if rpcResp.Result != nil {
		// 解析成功响应，提取 _meta.price（Rajomon 在成功响应中返回当前价格）
		var toolResult clientToolCallResult
		if err := json.Unmarshal(rpcResp.Result, &toolResult); err == nil {
			if toolResult.Meta != nil {
				result.Price = toolResult.Meta.Price
			}
		}
	}

	lg.recordResult(result)
	return result.Rejected || result.ErrorCode != 0 || result.StatusCode != 200
}

// recordResult 线程安全地记录请求结果
// 使用互斥锁保护 results 切片，因为多个 worker goroutine 会并发调用此方法
func (lg *LoadGenerator) recordResult(result RequestResult) {
	lg.resultsMu.Lock()
	lg.results = append(lg.results, result)
	lg.resultsMu.Unlock()
}
