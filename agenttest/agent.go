// agent.go
// Agent 和 Task 模型定义
// Agent 代表独立客户端，拥有预算；Task 为多步骤的完整交互
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// ==================== JSON-RPC 请求/响应类型（客户端侧） ====================

type clientJSONRPCRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int64       `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params"`
}

type clientToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments,omitempty"`
	Meta      *clientMeta            `json:"_meta,omitempty"`
}

type clientMeta struct {
	Tokens   int64  `json:"tokens,omitempty"`
	Name     string `json:"name,omitempty"`
	Method   string `json:"method,omitempty"`
	Priority string `json:"priority,omitempty"`
}

type clientJSONRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      interface{}     `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *clientRPCError `json:"error,omitempty"`
}

type clientRPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

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

// ==================== Agent 模型 ====================

// Agent 代表一个独立客户端
type Agent struct {
	ID            string
	InitialBudget int
	Budget        int // 当前剩余预算（动态扣减）
	mu            sync.Mutex
	Client        *http.Client
}

// NewAgent 创建一个 Agent
func NewAgent(id string, budget int) *Agent {
	return &Agent{
		ID:            id,
		InitialBudget: budget,
		Budget:        budget,
		Client: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 100,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// GetBudget 线程安全获取当前预算
func (a *Agent) GetBudget() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.Budget
}

// DeductBudget 线程安全扣减预算
func (a *Agent) DeductBudget(amount int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Budget -= amount
	if a.Budget < 0 {
		a.Budget = 0
	}
}

// ResetBudget 重置为初始预算（新任务时使用）
func (a *Agent) ResetBudget() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Budget = a.InitialBudget
}

// ==================== Task 模型 ====================

// Task 代表一次完整的多步骤交互
type Task struct {
	ID       string
	AgentID  string
	Priority Priority
	Steps    []TaskStep // 步骤列表（按顺序执行）
}

// TaskStep 任务的单个步骤
type TaskStep struct {
	ToolType string // 工具类型名称
}

// ==================== Step 结果 ====================

// StepResult 单步骤执行结果
type StepResult struct {
	Timestamp    int64  // 请求完成时间（Unix 毫秒）
	AgentID      string // Agent ID
	TaskID       string // 任务 ID
	StepIndex    int    // 步骤序号（从 1 开始）
	ToolType     string // 工具类型
	BudgetBefore int    // 发起请求时的剩余预算
	LatencyMs    int64  // 请求耗时（毫秒）
	StatusCode   int    // HTTP 状态码
	ErrorCode    int    // JSON-RPC 错误码
	Price        string // 响应中返回的动态价格
	TokenUsed    int    // 实际 Token 消耗
	Rejected     bool   // 是否被拒绝
	ErrorMsg     string // 错误信息
	Priority     string // 任务优先级
	Phase        string // 所处负载阶段
}

// IsSuccess 判断步骤是否成功
func (r *StepResult) IsSuccess() bool {
	return r.StatusCode == 200 && r.ErrorCode == 0 && !r.Rejected
}

// ==================== Task 执行引擎 ====================

// TaskExecutor 执行任务的引擎
type TaskExecutor struct {
	cfg          *AgentTestConfig
	serverURL    string
	requestIDGen int64
	resultsCh    chan StepResult
}

// NewTaskExecutor 创建任务执行器
func NewTaskExecutor(cfg *AgentTestConfig, serverURL string, resultsCh chan StepResult) *TaskExecutor {
	return &TaskExecutor{
		cfg:       cfg,
		serverURL: serverURL,
		resultsCh: resultsCh,
	}
}

// ExecuteTask 顺序执行一个任务的所有步骤
// 返回 (完成的步骤数, 任务是否成功, 失败原因)
func (te *TaskExecutor) ExecuteTask(agent *Agent, task *Task, phase string) (int, bool, string) {
	completedSteps := 0

	for i, step := range task.Steps {
		stepIdx := i + 1

		// 步骤间思考时间（第一步不等待）
		if i > 0 {
			thinkMin := te.cfg.StepThinkTimeMin
			thinkMax := te.cfg.StepThinkTimeMax
			if thinkMax > thinkMin {
				thinkTime := thinkMin + time.Duration(rand.Int63n(int64(thinkMax-thinkMin)))
				time.Sleep(thinkTime)
			}
		}

		// 检查预算
		currentBudget := agent.GetBudget()
		if currentBudget <= 0 {
			// 预算不足，记录并终止任务
			te.resultsCh <- StepResult{
				Timestamp:    time.Now().UnixMilli(),
				AgentID:      agent.ID,
				TaskID:       task.ID,
				StepIndex:    stepIdx,
				ToolType:     step.ToolType,
				BudgetBefore: currentBudget,
				StatusCode:   -2, // 预算不足
				Rejected:     true,
				ErrorMsg:     "预算不足，任务提前终止",
				Priority:     string(task.Priority),
				Phase:        phase,
			}
			return completedSteps, false, "budget_exhausted"
		}

		// 执行步骤
		result := te.executeStep(agent, task, stepIdx, step.ToolType, phase)

		if !result.IsSuccess() {
			// 步骤失败，终止任务
			failReason := "step_failed"
			if result.Rejected {
				failReason = "step_rejected"
			} else if result.StatusCode == -1 {
				failReason = "network_error"
			} else if result.StatusCode == -2 {
				failReason = "budget_exhausted"
			}
			return completedSteps, false, failReason
		}

		// 步骤成功，扣减预算
		if result.TokenUsed > 0 {
			agent.DeductBudget(result.TokenUsed)
		}
		completedSteps++
	}

	return completedSteps, true, ""
}

// executeStep 执行单个步骤并发送结果到 channel
func (te *TaskExecutor) executeStep(agent *Agent, task *Task, stepIdx int, toolType string, phase string) StepResult {
	reqID := atomic.AddInt64(&te.requestIDGen, 1)
	currentBudget := agent.GetBudget()

	// 查找工具的 Token 消耗
	tokenCost := 10 // 默认
	for _, t := range te.cfg.ToolTypes {
		if t.Name == toolType {
			tokenCost = t.TokenCost
			break
		}
	}

	// 构造 JSON-RPC 请求
	params := clientToolCallParams{
		Name: toolType,
		Arguments: map[string]interface{}{
			"input":    "agent-test",
			"task_id":  task.ID,
			"step_idx": stepIdx,
		},
	}

	// 对 Rajomon 策略，在 _meta 中携带 tokens 和 priority
	if te.cfg.Strategy == StrategyRajomon {
		params.Meta = &clientMeta{
			Tokens:   int64(currentBudget),
			Name:     agent.ID,
			Method:   toolType,
			Priority: string(task.Priority),
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
		result := StepResult{
			Timestamp:    time.Now().UnixMilli(),
			AgentID:      agent.ID,
			TaskID:       task.ID,
			StepIndex:    stepIdx,
			ToolType:     toolType,
			BudgetBefore: currentBudget,
			StatusCode:   -1,
			ErrorMsg:     fmt.Sprintf("序列化请求失败: %v", err),
			Priority:     string(task.Priority),
			Phase:        phase,
		}
		te.resultsCh <- result
		return result
	}

	// 发送 HTTP 请求
	start := time.Now()
	resp, err := agent.Client.Post(te.serverURL+"/mcp", "application/json", bytes.NewReader(body))
	latency := time.Since(start).Milliseconds()

	result := StepResult{
		Timestamp:    time.Now().UnixMilli(),
		AgentID:      agent.ID,
		TaskID:       task.ID,
		StepIndex:    stepIdx,
		ToolType:     toolType,
		BudgetBefore: currentBudget,
		LatencyMs:    latency,
		Priority:     string(task.Priority),
		Phase:        phase,
	}

	if err != nil {
		result.StatusCode = -1
		result.ErrorMsg = fmt.Sprintf("网络错误: %v", err)
		te.resultsCh <- result
		return result
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		result.StatusCode = -1
		result.ErrorMsg = fmt.Sprintf("读取响应失败: %v", err)
		te.resultsCh <- result
		return result
	}

	result.StatusCode = resp.StatusCode

	// 解析 JSON-RPC 响应
	var rpcResp clientJSONRPCResponse
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		result.ErrorMsg = fmt.Sprintf("解析响应失败: %v", err)
		te.resultsCh <- result
		return result
	}

	// 处理错误响应
	if rpcResp.Error != nil {
		result.ErrorCode = rpcResp.Error.Code
		result.ErrorMsg = rpcResp.Error.Message

		switch rpcResp.Error.Code {
		case -32001, -32002, -32003, -32000:
			result.Rejected = true
		}

		// 从 error.data 中提取价格
		if rpcResp.Error.Data != nil {
			var errorData map[string]interface{}
			if err := json.Unmarshal(rpcResp.Error.Data, &errorData); err == nil {
				if price, ok := errorData["price"]; ok {
					result.Price = fmt.Sprintf("%v", price)
				}
			}
		}
	} else if rpcResp.Result != nil {
		// 解析成功响应中的 Token 消耗和价格
		var toolResult clientToolCallResult
		if err := json.Unmarshal(rpcResp.Result, &toolResult); err == nil {
			if toolResult.Meta != nil {
				result.Price = toolResult.Meta.Price
			}
		}
		// 成功时记录 Token 消耗（如果响应没有返回，用工具定义的默认值）
		result.TokenUsed = tokenCost

		// 尝试从响应头获取实际 Token 消耗
		if tokenHeader := resp.Header.Get("X-Token-Usage"); tokenHeader != "" {
			if parsed, err := strconv.Atoi(tokenHeader); err == nil {
				result.TokenUsed = parsed
			}
		}
	}

	te.resultsCh <- result
	return result
}
