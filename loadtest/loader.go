// loader.go
// 负载生成器核心逻辑
// 支持三种负载模式：阶梯式突发、正弦波动、泊松到达
// 通过 HTTP 客户端发送 JSON-RPC 2.0 格式的 MCP 请求
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

// clientJSONRPCRequest 客户端发送的 JSON-RPC 请求
type clientJSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

// clientToolCallParams 客户端发送的工具调用参数
type clientToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
	Meta      *clientMeta            `json:"_meta,omitempty"`
}

// clientMeta 客户端治理元数据
type clientMeta struct {
	Tokens int64  `json:"tokens,omitempty"`
	Name   string `json:"name,omitempty"`
	Method string `json:"method,omitempty"`
}

// clientJSONRPCResponse 客户端接收的 JSON-RPC 响应
type clientJSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *clientRPCError `json:"error,omitempty"`
}

// clientRPCError JSON-RPC 错误
type clientRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

// clientToolCallResult 解析后的工具调用结果
type clientToolCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	IsError bool `json:"isError"`
	Meta    *struct {
		Price string `json:"price"`
		Name  string `json:"name"`
	} `json:"_meta"`
}

// ==================== 负载生成器 ====================

// LoadGenerator 负载生成器
type LoadGenerator struct {
	cfg           *TestConfig
	serverURL     string
	httpClient    *http.Client
	results       []RequestResult
	resultsMu     sync.Mutex
	requestIDGen  int64
	rng           *rand.Rand
	stopCh        chan struct{}
	activeWorkers int64 // 当前活跃 worker 数
}

// NewLoadGenerator 创建负载生成器
func NewLoadGenerator(cfg *TestConfig, serverURL string) *LoadGenerator {
	return &LoadGenerator{
		cfg:       cfg,
		serverURL: serverURL,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        500,
				MaxIdleConnsPerHost: 500,
				IdleConnTimeout:     90 * time.Second,
			},
		},
		results: make([]RequestResult, 0, 10000),
		rng:     rand.New(rand.NewSource(cfg.RandomSeed)),
		stopCh:  make(chan struct{}),
	}
}

// Run 运行负载测试，返回所有请求结果
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

func (lg *LoadGenerator) runStepPattern() {
	for _, phase := range lg.cfg.StepPhases {
		fmt.Printf("[阶段: %s] 并发=%d, 持续=%s\n", phase.Name, phase.Concurrency, phase.Duration)

		ctx, cancel := context.WithTimeout(context.Background(), phase.Duration)

		// 启动指定数量的 worker
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

func (lg *LoadGenerator) runSinePattern() {
	startTime := time.Now()
	duration := lg.cfg.Duration

	fmt.Printf("[正弦波负载] 基础=%d, 振幅=%d, 周期=%s, 总时长=%s\n",
		lg.cfg.SineBase, lg.cfg.SineAmplitude, lg.cfg.SinePeriod, duration)

	// 每秒调整一次并发数
	adjustInterval := 1 * time.Second
	ticker := time.NewTicker(adjustInterval)
	defer ticker.Stop()

	var currentCancel context.CancelFunc
	var currentWg sync.WaitGroup

	for {
		select {
		case <-ticker.C:
			elapsed := time.Since(startTime)
			if elapsed >= duration {
				// 测试结束
				if currentCancel != nil {
					currentCancel()
					currentWg.Wait()
				}
				return
			}

			// 计算当前目标并发数
			t := elapsed.Seconds()
			period := lg.cfg.SinePeriod.Seconds()
			concurrency := lg.cfg.SineBase + int(float64(lg.cfg.SineAmplitude)*math.Sin(2*math.Pi*t/period))
			if concurrency < 1 {
				concurrency = 1
			}

			// 获取当前活跃 worker 数
			currentActive := int(atomic.LoadInt64(&lg.activeWorkers))
			diff := concurrency - currentActive

			if diff > 0 {
				// 需要增加 worker
				ctx, cancel := context.WithCancel(context.Background())
				_ = cancel // 这些 worker 会在收到外部 stop 时退出
				if currentCancel != nil {
					// 保留旧的，追加新的
					_ = ctx
				}

				for i := 0; i < diff; i++ {
					currentWg.Add(1)
					go func() {
						defer currentWg.Done()
						atomic.AddInt64(&lg.activeWorkers, 1)
						defer atomic.AddInt64(&lg.activeWorkers, -1)

						phaseName := fmt.Sprintf("sine_c%d", concurrency)
						lg.workerUntilStop(phaseName)
					}()
				}
				currentCancel = cancel
			}
			// 注意：减少 worker 由 stop 信号控制，这里简化处理
		}
	}
}

// ==================== 泊松到达负载 ====================

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

// workerUntilStop 持续发送请求直到收到停止信号
func (lg *LoadGenerator) workerUntilStop(phase string) {
	for {
		select {
		case <-lg.stopCh:
			return
		default:
			rejected := lg.sendOneRequest(phase)
			if rejected && lg.cfg.RejectionBackoff > 0 {
				time.Sleep(lg.cfg.RejectionBackoff)
			} else {
				time.Sleep(lg.cfg.RequestInterval)
			}
		}
	}
}

// sendOneRequest 发送一个 MCP tools/call 请求并记录结果
// 返回 true 表示请求被拒绝（过载/限流/错误）
func (lg *LoadGenerator) sendOneRequest(phase string) bool {
	// 生成请求 ID
	reqID := atomic.AddInt64(&lg.requestIDGen, 1)

	// 随机选择预算
	budget := lg.cfg.Budgets[rand.Intn(len(lg.cfg.Budgets))]

	// 构造 JSON-RPC 请求
	params := clientToolCallParams{
		Name:      lg.cfg.ToolName,
		Arguments: map[string]interface{}{"input": "test"},
	}

	// 对 Rajomon 策略，需要在 _meta 中携带 tokens
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

	// 发送 HTTP 请求
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

	// 解析 JSON-RPC 响应
	var rpcResp clientJSONRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		result.ErrorMsg = fmt.Sprintf("解析响应失败: %v", err)
		lg.recordResult(result)
		return true
	}

	// 处理错误响应
	if rpcResp.Error != nil {
		result.ErrorCode = rpcResp.Error.Code
		result.ErrorMsg = rpcResp.Error.Message

		// 判断是否为过载/限流拒绝
		switch rpcResp.Error.Code {
		case -32001, -32002, -32003, -32000: // 过载、限流、令牌不足
			result.Rejected = true
		}

		// 尝试从 error.data 中提取价格
		if rpcResp.Error.Data != nil {
			var errorData map[string]interface{}
			if err := json.Unmarshal(rpcResp.Error.Data, &errorData); err == nil {
				if price, ok := errorData["price"]; ok {
					result.Price = fmt.Sprintf("%v", price)
				}
			}
		}
	} else if rpcResp.Result != nil {
		// 解析成功响应
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
func (lg *LoadGenerator) recordResult(result RequestResult) {
	lg.resultsMu.Lock()
	lg.results = append(lg.results, result)
	lg.resultsMu.Unlock()
}
