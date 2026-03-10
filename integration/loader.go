// loader.go
// ==================== 负载生成器 ====================
//
// 与 loadtest/loader.go 功能相同：生成三种负载模式 (step/sine/poisson) 的请求流。
// 区别在于目标服务器是 Go 治理代理 (proxy.go), 而非直接的 Mock 服务器。
// 代理层负责将请求转发到真实 Python MCP 服务器。
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

// ==================== 客户端 JSON-RPC 类型 ====================

type clientJSONRPCRequest struct {
	JSONRPC string               `json:"jsonrpc"`
	ID      int64                `json:"id"`
	Method  string               `json:"method"`
	Params  clientToolCallParams `json:"params"`
}

type clientToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
	Meta      *clientMeta            `json:"_meta,omitempty"`
}

type clientMeta struct {
	Tokens int64  `json:"tokens,omitempty"`
	Name   string `json:"name,omitempty"`
	Method string `json:"method,omitempty"`
}

type clientJSONRPCResponse struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      interface{}      `json:"id"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *clientRPCError  `json:"error,omitempty"`
}

type clientRPCError struct {
	Code    int              `json:"code"`
	Message string           `json:"message"`
	Data    json.RawMessage  `json:"data,omitempty"`
}

type clientToolCallResult struct {
	Content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content,omitempty"`
	Meta *struct {
		Price string `json:"price,omitempty"`
		Name  string `json:"name,omitempty"`
	} `json:"_meta,omitempty"`
}

// ==================== 负载生成器 ====================

// LoadGenerator 负载生成器核心结构
type LoadGenerator struct {
	cfg          *TestConfig
	serverURL    string // Go 代理服务器的 URL
	httpClient   *http.Client
	requestIDGen int64
	results      []RequestResult
	resultsMu    sync.Mutex
}

// NewLoadGenerator 创建负载生成器实例
func NewLoadGenerator(cfg *TestConfig, serverURL string) *LoadGenerator {
	return &LoadGenerator{
		cfg:       cfg,
		serverURL: serverURL,
		httpClient: &http.Client{
			Timeout: cfg.HTTPClientTimeout,
			Transport: &http.Transport{
				MaxIdleConns:        cfg.HTTPMaxConnections,
				MaxIdleConnsPerHost: cfg.HTTPMaxConnections,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// Run 运行负载生成器并返回所有请求结果
func (lg *LoadGenerator) Run() []RequestResult {
	switch lg.cfg.LoadPattern {
	case PatternStep:
		lg.runStepPattern()
	case PatternSine:
		lg.runSinePattern()
	case PatternPoisson:
		lg.runPoissonPattern()
	default:
		fmt.Printf("[错误] 未知负载模式: %s\n", lg.cfg.LoadPattern)
	}
	return lg.results
}

// pickBudget 按权重随机选择预算值
func (lg *LoadGenerator) pickBudget() int {
	if len(lg.cfg.Budgets) == 0 {
		return 50
	}

	weights := lg.cfg.BudgetWeights
	if len(weights) != len(lg.cfg.Budgets) {
		return lg.cfg.Budgets[rand.Intn(len(lg.cfg.Budgets))]
	}

	r := rand.Float64()
	cumulative := 0.0
	for i, w := range weights {
		cumulative += w
		if r < cumulative {
			return lg.cfg.Budgets[i]
		}
	}
	return lg.cfg.Budgets[len(lg.cfg.Budgets)-1]
}

// ==================== 阶梯负载 ====================

func (lg *LoadGenerator) runStepPattern() {
	for _, phase := range lg.cfg.StepPhases {
		fmt.Printf("[阶段: %s] 并发=%d, 持续=%s\n", phase.Name, phase.Concurrency, phase.Duration)

		ctx, cancel := context.WithTimeout(context.Background(), phase.Duration)

		var wg sync.WaitGroup
		for i := 0; i < phase.Concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				lg.worker(ctx, phase.Name)
			}()
		}

		<-ctx.Done()
		cancel()
		wg.Wait()

		fmt.Printf("[阶段: %s] 完成, 已收集 %d 条结果\n", phase.Name, len(lg.results))
	}
}

// ==================== 正弦波负载 ====================

func (lg *LoadGenerator) runSinePattern() {
	startTime := time.Now()
	duration := lg.cfg.Duration

	fmt.Printf("[正弦波负载] 基础=%d, 振幅=%d, 周期=%s, 总时长=%s\n",
		lg.cfg.SineBase, lg.cfg.SineAmplitude, lg.cfg.SinePeriod, duration)

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
				if currentCancel != nil {
					currentCancel()
				}
				currentWg.Wait()
				return
			}

			t := elapsed.Seconds()
			period := lg.cfg.SinePeriod.Seconds()
			concurrency := lg.cfg.SineBase + int(float64(lg.cfg.SineAmplitude)*math.Sin(2*math.Pi*t/period))
			if concurrency < 1 {
				concurrency = 1
			}

			if concurrency == lastConcurrency {
				continue
			}
			lastConcurrency = concurrency

			if currentCancel != nil {
				currentCancel()
			}
			currentWg.Wait()

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

func (lg *LoadGenerator) runPoissonPattern() {
	duration := lg.cfg.Duration
	lambda := lg.cfg.PoissonQPS

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
			interval := time.Duration(rand.ExpFloat64()/lambda*1e9) * time.Nanosecond
			time.Sleep(interval)

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

// ==================== Worker & 请求发送 ====================

func (lg *LoadGenerator) worker(ctx context.Context, phase string) {
	for {
		select {
		case <-ctx.Done():
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

// sendOneRequest 发送一个 MCP tools/call 请求到 Go 代理
func (lg *LoadGenerator) sendOneRequest(phase string) bool {
	reqID := atomic.AddInt64(&lg.requestIDGen, 1)
	budget := lg.pickBudget()

	// 构造请求参数
	params := clientToolCallParams{
		Name:      lg.cfg.ToolName,
		Arguments: lg.cfg.ToolArguments,
	}

	// Rajomon 策略: 在 _meta 中携带 tokens
	if lg.cfg.Strategy == StrategyRajomon {
		params.Meta = &clientMeta{
			Tokens: int64(budget),
			Name:   "integration-client",
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
			StatusCode:   -1,
			ErrorMsg:     fmt.Sprintf("序列化请求失败: %v", err),
		})
		return true
	}

	// 发送 HTTP POST 请求到代理服务器
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

	if rpcResp.Error != nil {
		result.ErrorCode = rpcResp.Error.Code
		result.ErrorMsg = rpcResp.Error.Message

		switch rpcResp.Error.Code {
		case -32001, -32002, -32003, -32000:
			// 治理层主动拒绝 (动态定价拒绝/静态限流/过载保护/令牌不足)
			// 这些是治理策略的"主动决策"，对应 HTTP 429 语义
			result.Rejected = true
		case -32603:
			// 后端服务过载 (Python Bridge ConcurrencyLimiter 拒绝)
			// 这是"系统错误"而非治理决策，对应 HTTP 5xx 语义
			// 不设置 Rejected = true，让其计入 ErrorCount
			// 无治理策略下此错误应被视为系统崩溃的表现
		}

		// 提取价格信息
		if rpcResp.Error.Data != nil {
			var errorData map[string]interface{}
			if err := json.Unmarshal(rpcResp.Error.Data, &errorData); err == nil {
				if price, ok := errorData["price"]; ok {
					result.Price = fmt.Sprintf("%v", price)
				}
			}
		}
	} else if rpcResp.Result != nil {
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
