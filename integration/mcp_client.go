// mcp_client.go
// ==================== MCP Bridge HTTP 客户端 ====================
//
// 封装与 Python MCP Bridge 服务器 (bridge.py) 的通信。
// 通过 JSON-RPC 2.0 over HTTP POST 调用真实 Python MCP 工具。
//
// 本模块在整个架构中的位置：
//
//	Go 负载生成器 → Go 治理代理 (proxy.go) → 本模块 → Python MCP Bridge → Python 工具函数
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	mcpgov "mcp-governance"
)

// MCPBridgeClient 是连接 Python MCP Bridge 的 HTTP 客户端
type MCPBridgeClient struct {
	bridgeURL  string       // Python Bridge 的 URL (如 http://localhost:9000)
	httpClient *http.Client // 复用的 HTTP 客户端
	requestID  int64        // 原子递增的请求 ID
}

// NewMCPBridgeClient 创建一个新的 MCP Bridge 客户端
func NewMCPBridgeClient(bridgeURL string, timeout time.Duration) *MCPBridgeClient {
	return &MCPBridgeClient{
		bridgeURL: bridgeURL,
		httpClient: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        200,
				MaxIdleConnsPerHost: 200,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}
}

// CallTool 向 Python MCP Bridge 发送 tools/call 请求
// 这是代理层转发真实工具调用的核心方法
func (c *MCPBridgeClient) CallTool(ctx context.Context, toolName string, arguments map[string]interface{}) (*mcpgov.MCPToolCallResult, error) {
	reqID := atomic.AddInt64(&c.requestID, 1)

	// 构造 JSON-RPC 请求 (与 Go MCPServer 接收的格式一致)
	rpcReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  "tools/call",
		"params": map[string]interface{}{
			"name":      toolName,
			"arguments": arguments,
		},
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("序列化请求失败: %w", err)
	}

	// 创建带 context 的 HTTP 请求
	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.bridgeURL+"/mcp", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("创建 HTTP 请求失败: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	// 发送请求
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 读取响应
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %w", err)
	}

	// 解析 JSON-RPC 响应
	var rpcResp struct {
		JSONRPC string           `json:"jsonrpc"`
		ID      interface{}      `json:"id"`
		Result  json.RawMessage  `json:"result,omitempty"`
		Error   *json.RawMessage `json:"error,omitempty"`
	}

	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, fmt.Errorf("解析 JSON-RPC 响应失败: %w", err)
	}

	// 处理错误响应
	if rpcResp.Error != nil {
		var rpcErr struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		}
		json.Unmarshal(*rpcResp.Error, &rpcErr)
		return nil, fmt.Errorf("MCP Bridge 错误 %d: %s", rpcErr.Code, rpcErr.Message)
	}

	// 解析成功响应为 MCPToolCallResult
	var toolResult mcpgov.MCPToolCallResult
	if err := json.Unmarshal(rpcResp.Result, &toolResult); err != nil {
		return nil, fmt.Errorf("解析工具调用结果失败: %w", err)
	}

	return &toolResult, nil
}

// HealthCheck 检查 Python MCP Bridge 是否可用
func (c *MCPBridgeClient) HealthCheck(ctx context.Context) error {
	httpReq, err := http.NewRequestWithContext(ctx, "GET", c.bridgeURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("创建健康检查请求失败: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("健康检查请求失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("健康检查返回非 200 状态码: %d", resp.StatusCode)
	}

	return nil
}

// ListTools 获取 Python MCP Bridge 上可用的工具列表
func (c *MCPBridgeClient) ListTools(ctx context.Context) ([]string, error) {
	reqID := atomic.AddInt64(&c.requestID, 1)

	rpcReq := map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      reqID,
		"method":  "tools/list",
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, "POST", c.bridgeURL+"/mcp", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var rpcResp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return nil, err
	}

	var names []string
	for _, t := range rpcResp.Result.Tools {
		names = append(names, t.Name)
	}
	return names, nil
}
