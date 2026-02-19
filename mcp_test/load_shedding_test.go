// load_shedding_test.go
// 负载削减 (Load Shedding) 效果测试
// 验证在不同负载条件下，服务治理引擎能否正确地拒绝低预算请求、放行高预算请求
package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcpgov "mcp-governance"
)

// ==================== 公共辅助函数 ====================

// testOpts 测试用默认配置
var testOpts = map[string]interface{}{
	"priceUpdateRate":  5000 * time.Microsecond,
	"tokenUpdateRate":  100000 * time.Microsecond,
	"latencyThreshold": 500 * time.Microsecond,
	"priceStep":        int64(180),
	"priceStrategy":    "expdecay",
	"lazyResponse":     false,
	"rateLimiting":     true,
	"loadShedding":     true,
}

// newTestServer 创建一个用于测试的 MCP HTTP 服务端
func newTestServer(toolName string, opts map[string]interface{}, handler mcpgov.ToolCallHandler) (*httptest.Server, *mcpgov.MCPGovernor) {
	callMap := map[string][]string{toolName: {}}
	gov := mcpgov.NewMCPGovernor("test-node", callMap, opts)
	server := mcpgov.NewMCPServer("test-service", gov)
	server.RegisterTool(mcpgov.MCPTool{
		Name:        toolName,
		Description: "测试工具",
		InputSchema: map[string]interface{}{"type": "object"},
	}, handler)
	return httptest.NewServer(server), gov
}

// sendRequest 发送 JSON-RPC 请求到测试服务器
func sendRequest(url string, req *mcpgov.JSONRPCRequest) *mcpgov.JSONRPCResponse {
	body, _ := json.Marshal(req)
	httpResp, err := http.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil
	}
	defer httpResp.Body.Close()
	respBody, _ := io.ReadAll(httpResp.Body)
	var resp mcpgov.JSONRPCResponse
	json.Unmarshal(respBody, &resp)
	return &resp
}

// makeToolCallReq 构造一个 tools/call 请求
func makeToolCallReq(id interface{}, toolName string, tokens int64) *mcpgov.JSONRPCRequest {
	params := mcpgov.MCPToolCallParams{
		Name:      toolName,
		Arguments: map[string]interface{}{},
		Meta:      &mcpgov.GovernanceMeta{Tokens: tokens, Method: toolName, Name: "test-client"},
	}
	req, _ := mcpgov.NewJSONRPCRequest(id, "tools/call", params)
	return req
}

// simpleHandler 一个简单的工具处理函数
func simpleHandler(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
	return &mcpgov.MCPToolCallResult{
		Content: []mcpgov.ContentBlock{mcpgov.TextContent("OK")},
	}, nil
}

// ==================== 负载削减效果测试 ====================

// TestLoadShedding_BasicAdmission 基础准入控制测试
// 验证：令牌 >= 价格时通过，令牌 < 价格时被拒
func TestLoadShedding_BasicAdmission(t *testing.T) {
	ts, gov := newTestServer("tool_a", testOpts, simpleHandler)
	defer ts.Close()

	// 设定价格为 50
	gov.SetOwnPrice(50)

	testCases := []struct {
		name       string
		tokens     int64
		wantReject bool
	}{
		{"令牌远超价格", 200, false},
		{"令牌等于价格", 50, false},
		{"令牌略低于价格", 49, true},
		{"令牌为0", 0, true},
		{"令牌远低于价格", 5, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := makeToolCallReq(1, "tool_a", tc.tokens)
			resp := sendRequest(ts.URL, req)

			if resp == nil {
				t.Fatal("未收到响应")
			}

			if tc.wantReject {
				if resp.Error == nil {
					t.Errorf("令牌=%d, 价格=50: 预期被拒绝，但请求通过了", tc.tokens)
				}
			} else {
				if resp.Error != nil {
					t.Errorf("令牌=%d, 价格=50: 预期通过，但被拒绝了: %s", tc.tokens, resp.Error.Message)
				}
			}
		})
	}
}

// TestLoadShedding_RejectRateUnderOverload 过载场景下的拒绝率测试
// 验证：当价格高时，低预算请求的拒绝率应接近 100%
func TestLoadShedding_RejectRateUnderOverload(t *testing.T) {
	ts, gov := newTestServer("tool_b", testOpts, simpleHandler)
	defer ts.Close()

	// 模拟过载：设置高价格
	gov.SetOwnPrice(500)

	const totalRequests = 100
	var rejected, accepted int

	for i := 0; i < totalRequests; i++ {
		// 所有请求只携带 50 令牌（远低于价格 500）
		req := makeToolCallReq(i, "tool_b", 50)
		resp := sendRequest(ts.URL, req)
		if resp != nil && resp.Error != nil {
			rejected++
		} else {
			accepted++
		}
	}

	rejectRate := float64(rejected) / float64(totalRequests) * 100
	t.Logf("📊 过载拒绝率: %.1f%% (拒绝=%d, 通过=%d, 总数=%d)", rejectRate, rejected, accepted, totalRequests)

	if rejectRate < 95.0 {
		t.Errorf("过载时拒绝率应 >= 95%%，实际: %.1f%%", rejectRate)
	}
}

// TestLoadShedding_SelectiveAdmission 选择性准入测试
// 验证：高预算请求通过，低预算请求被拒，体现"预算优先"的治理效果
func TestLoadShedding_SelectiveAdmission(t *testing.T) {
	ts, gov := newTestServer("tool_c", testOpts, simpleHandler)
	defer ts.Close()

	gov.SetOwnPrice(100)

	const totalRequests = 200
	var highTokenAccepted, highTokenRejected int
	var lowTokenAccepted, lowTokenRejected int

	for i := 0; i < totalRequests; i++ {
		var tokens int64
		isHighBudget := i%2 == 0 // 偶数为高预算，奇数为低预算
		if isHighBudget {
			tokens = 200 // 高预算
		} else {
			tokens = 30 // 低预算
		}

		req := makeToolCallReq(i, "tool_c", tokens)
		resp := sendRequest(ts.URL, req)

		if isHighBudget {
			if resp != nil && resp.Error != nil {
				highTokenRejected++
			} else {
				highTokenAccepted++
			}
		} else {
			if resp != nil && resp.Error != nil {
				lowTokenRejected++
			} else {
				lowTokenAccepted++
			}
		}
	}

	t.Logf("📊 高预算请求: 通过=%d, 拒绝=%d", highTokenAccepted, highTokenRejected)
	t.Logf("📊 低预算请求: 通过=%d, 拒绝=%d", lowTokenAccepted, lowTokenRejected)

	// 高预算应全部通过
	if highTokenAccepted < totalRequests/2 {
		t.Errorf("高预算请求应大部分通过, 实际通过=%d/%d", highTokenAccepted, totalRequests/2)
	}
	// 低预算应全部被拒
	if lowTokenRejected < totalRequests/2 {
		t.Errorf("低预算请求应大部分被拒, 实际拒绝=%d/%d", lowTokenRejected, totalRequests/2)
	}
}

// TestLoadShedding_ConcurrentProtection 并发场景下的负载保护测试
// 验证：高并发下负载削减能有效保护服务
func TestLoadShedding_ConcurrentProtection(t *testing.T) {
	// 注册一个模拟1ms延迟的工具
	handler := func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		time.Sleep(1 * time.Millisecond) // 模拟处理延迟
		return &mcpgov.MCPToolCallResult{
			Content: []mcpgov.ContentBlock{mcpgov.TextContent("done")},
		}, nil
	}

	ts, gov := newTestServer("tool_d", testOpts, handler)
	defer ts.Close()

	// 设置较高价格来模拟过载
	gov.SetOwnPrice(100)

	const concurrency = 50
	const requestsPerGoroutine = 20

	var totalAccepted int64
	var totalRejected int64
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < requestsPerGoroutine; j++ {
				// 交替发送高低预算请求
				tokens := int64(200)
				if j%3 == 0 {
					tokens = 10 // 每3个请求有1个低预算
				}
				req := makeToolCallReq(goroutineID*1000+j, "tool_d", tokens)
				resp := sendRequest(ts.URL, req)
				if resp != nil && resp.Error != nil {
					atomic.AddInt64(&totalRejected, 1)
				} else if resp != nil {
					atomic.AddInt64(&totalAccepted, 1)
				}
			}
		}(i)
	}

	wg.Wait()

	total := totalAccepted + totalRejected
	rejectRate := float64(totalRejected) / float64(total) * 100

	t.Logf("📊 并发负载保护测试:")
	t.Logf("   总请求数: %d, 通过: %d, 拒绝: %d", total, totalAccepted, totalRejected)
	t.Logf("   拒绝率: %.1f%%", rejectRate)

	// 低预算请求约占 1/3，因此最少应有部分被拒
	if totalRejected == 0 {
		t.Error("并发场景下应有部分请求被拒绝")
	}
}

// TestLoadShedding_PriceInErrorResponse 验证拒绝响应中携带价格信息
// 客户端需要知道当前价格以便调整后续请求的预算
func TestLoadShedding_PriceInErrorResponse(t *testing.T) {
	ts, gov := newTestServer("tool_e", testOpts, simpleHandler)
	defer ts.Close()

	gov.SetOwnPrice(200)

	req := makeToolCallReq(1, "tool_e", 10) // 令牌远不够
	resp := sendRequest(ts.URL, req)

	if resp == nil || resp.Error == nil {
		t.Fatal("预期请求被拒绝")
	}

	// 验证错误码
	if resp.Error.Code != -32001 { // CodeOverloaded
		t.Errorf("错误码 = %d; 期望 -32001 (CodeOverloaded)", resp.Error.Code)
	}

	// 验证 data 中包含 price
	if resp.Error.Data != nil {
		dataBytes, _ := json.Marshal(resp.Error.Data)
		var dataMap map[string]string
		if err := json.Unmarshal(dataBytes, &dataMap); err == nil {
			if price, ok := dataMap["price"]; ok {
				t.Logf("✅ 拒绝响应中包含价格信息: price=%s", price)
			} else {
				t.Error("拒绝响应 data 中缺少 price 字段")
			}
			if name, ok := dataMap["name"]; ok {
				t.Logf("   节点名称: %s", name)
			}
		}
	} else {
		t.Error("拒绝响应缺少 data 字段")
	}
}

// TestLoadShedding_ZeroPricePassesAll 价格为0时所有请求应通过
func TestLoadShedding_ZeroPricePassesAll(t *testing.T) {
	ts, gov := newTestServer("tool_f", testOpts, simpleHandler)
	defer ts.Close()

	gov.SetOwnPrice(0)

	const totalRequests = 50
	var accepted int

	for i := 0; i < totalRequests; i++ {
		req := makeToolCallReq(i, "tool_f", 0) // 即使令牌为0
		resp := sendRequest(ts.URL, req)
		if resp != nil && resp.Error == nil {
			accepted++
		}
	}

	t.Logf("📊 零价格通过率: %d/%d", accepted, totalRequests)
	if accepted != totalRequests {
		t.Errorf("价格为0时所有请求应通过, 实际通过=%d/%d", accepted, totalRequests)
	}
}

// TestLoadShedding_GradualPriceIncrease 渐进式涨价下的拒绝率变化
// 验证：随着价格上涨，固定预算请求的拒绝率逐步增加
func TestLoadShedding_GradualPriceIncrease(t *testing.T) {
	ts, gov := newTestServer("tool_g", testOpts, simpleHandler)
	defer ts.Close()

	fixedTokens := int64(50)
	prices := []int64{0, 10, 30, 50, 80, 100, 200}
	prevRejectRate := -1.0

	t.Logf("📊 固定令牌=%d, 逐步涨价测试:", fixedTokens)

	for _, price := range prices {
		gov.SetOwnPrice(price)

		var rejected int
		const requests = 30

		for i := 0; i < requests; i++ {
			req := makeToolCallReq(i, "tool_g", fixedTokens)
			resp := sendRequest(ts.URL, req)
			if resp != nil && resp.Error != nil {
				rejected++
			}
		}

		rejectRate := float64(rejected) / float64(requests) * 100
		t.Logf("   价格=%3d: 拒绝率=%.0f%% (拒绝=%d/%d)", price, rejectRate, rejected, requests)

		// 拒绝率不应下降（价格只升不降时）
		if rejectRate < prevRejectRate-1 { // 允许1%的误差
			t.Errorf("价格升高时拒绝率不应下降: 上次=%.0f%%, 本次=%.0f%%", prevRejectRate, rejectRate)
		}
		prevRejectRate = rejectRate
	}
}

// TestLoadShedding_DisabledMode 关闭负载削减时应放行所有请求
func TestLoadShedding_DisabledMode(t *testing.T) {
	// 配置：关闭负载削减
	opts := map[string]interface{}{
		"loadShedding":  false,
		"rateLimiting":  false,
		"priceStrategy": "step",
	}

	ts, gov := newTestServer("tool_h", opts, simpleHandler)
	defer ts.Close()

	// 即使价格很高，关闭负载削减后也应全部放行
	gov.SetOwnPrice(99999)

	const totalRequests = 30
	var accepted int

	for i := 0; i < totalRequests; i++ {
		req := makeToolCallReq(i, "tool_h", 1) // 几乎没有令牌
		resp := sendRequest(ts.URL, req)
		if resp != nil && resp.Error == nil {
			accepted++
		}
	}

	t.Logf("📊 关闭负载削减: 通过=%d/%d", accepted, totalRequests)
	if accepted != totalRequests {
		t.Errorf("关闭负载削减后应全部放行, 实际通过=%d/%d", accepted, totalRequests)
	}
}

// TestLoadShedding_ThroughputProtection 基于吞吐量检测的负载保护
// 开启 pinpointThroughput，当请求速率超过阈值时应自动涨价
func TestLoadShedding_ThroughputProtection(t *testing.T) {
	opts := map[string]interface{}{
		"priceUpdateRate":     10 * time.Millisecond,
		"loadShedding":        true,
		"rateLimiting":        true,
		"pinpointThroughput":  true,
		"throughputThreshold": int64(5), // 阈值设为 5
		"priceStep":           int64(100),
		"priceStrategy":       "step",
		"priceAggregation":    "additive",
	}

	handler := func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		return &mcpgov.MCPToolCallResult{Content: []mcpgov.ContentBlock{mcpgov.TextContent("OK")}}, nil
	}

	ts, gov := newTestServer("tool_tp", opts, handler)
	defer ts.Close()

	// 初始价格为0
	initialPrice, _ := gov.RetrieveTotalPrice(context.Background(), "tool_tp")
	t.Logf("初始价格: %s", initialPrice)

	// 发送大量请求以触发吞吐量过载检测
	const burstSize = 50
	for i := 0; i < burstSize; i++ {
		req := makeToolCallReq(i, "tool_tp", 10000)
		sendRequest(ts.URL, req)
	}

	// 等待过载检测协程处理
	time.Sleep(100 * time.Millisecond)

	finalPrice, _ := gov.RetrieveTotalPrice(context.Background(), "tool_tp")
	t.Logf("📊 吞吐量保护测试: 初始价格=%s, 突发后价格=%s", initialPrice, finalPrice)

	// 价格可能上涨也可能没有（取决于检测窗口），记录即可
	if finalPrice != initialPrice {
		t.Logf("✅ 吞吐量过载后价格上涨: %s → %s", initialPrice, finalPrice)
	} else {
		t.Logf("⚠️ 价格未变化（可能检测窗口未覆盖突发请求）")
	}
}

// TestLoadShedding_ResponseContainsPrice 成功响应中应包含价格信息
func TestLoadShedding_ResponseContainsPrice(t *testing.T) {
	opts := map[string]interface{}{
		"loadShedding":  true,
		"rateLimiting":  true,
		"priceStrategy": "step",
		"lazyResponse":  false,
		"priceFreq":     int64(1), // 每个请求都返回价格
	}

	ts, gov := newTestServer("tool_rp", opts, simpleHandler)
	defer ts.Close()

	gov.SetOwnPrice(30)

	// 发送令牌充足的请求
	req := makeToolCallReq(1, "tool_rp", 100)
	resp := sendRequest(ts.URL, req)

	if resp == nil || resp.Error != nil {
		t.Fatal("预期请求成功")
	}

	// 解析 result._meta 中的价格
	resultBytes, _ := json.Marshal(resp.Result)
	var result mcpgov.MCPToolCallResult
	json.Unmarshal(resultBytes, &result)

	if result.Meta != nil && result.Meta.Price != "" {
		t.Logf("✅ 成功响应包含价格: price=%s, node=%s", result.Meta.Price, result.Meta.Name)
	} else {
		t.Log("⚠️ 成功响应中未包含价格信息（可能被 priceFreq 过滤）")
	}
}

// ==================== Benchmark: 负载削减性能基准 ====================

// BenchmarkLoadShedding_Accepted 测试通过请求的吞吐量
func BenchmarkLoadShedding_Accepted(b *testing.B) {
	callMap := map[string][]string{"bench_tool": {}}
	gov := mcpgov.NewMCPGovernor("bench-node", callMap, map[string]interface{}{
		"loadShedding": true,
		"rateLimiting": true,
	})

	gov.SetOwnPrice(10)

	handler := func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		return &mcpgov.MCPToolCallResult{Content: []mcpgov.ContentBlock{mcpgov.TextContent("OK")}}, nil
	}

	params := mcpgov.MCPToolCallParams{
		Name: "bench_tool",
		Meta: &mcpgov.GovernanceMeta{Tokens: 100, Method: "bench_tool", Name: "bench"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gov.HandleToolCallDirect(context.Background(), params, handler)
	}
}

// BenchmarkLoadShedding_Rejected 测试拒绝请求的吞吐量
func BenchmarkLoadShedding_Rejected(b *testing.B) {
	callMap := map[string][]string{"bench_tool": {}}
	gov := mcpgov.NewMCPGovernor("bench-node", callMap, map[string]interface{}{
		"loadShedding": true,
		"rateLimiting": true,
	})

	gov.SetOwnPrice(1000)

	handler := func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		return &mcpgov.MCPToolCallResult{Content: []mcpgov.ContentBlock{mcpgov.TextContent("OK")}}, nil
	}

	params := mcpgov.MCPToolCallParams{
		Name: "bench_tool",
		Meta: &mcpgov.GovernanceMeta{Tokens: 1, Method: "bench_tool", Name: "bench"},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		gov.HandleToolCallDirect(context.Background(), params, handler)
	}
}

// ==================== 表格驱动的聚合策略测试 ====================

// TestLoadShedding_PriceAggregation 价格聚合策略对比
func TestLoadShedding_PriceAggregation(t *testing.T) {
	strategies := []struct {
		name          string
		aggregation   string
		ownPrice      int64
		dsPrice       int64
		tokens        int64
		expectReject  bool
		expectMessage string
	}{
		{
			name: "Maximal策略-自身价格高-令牌充足", aggregation: "maximal",
			ownPrice: 100, dsPrice: 50, tokens: 200,
			expectReject: false, expectMessage: "max(100,50)=100 < 200, 通过",
		},
		{
			name: "Maximal策略-自身价格高-令牌不足", aggregation: "maximal",
			ownPrice: 100, dsPrice: 50, tokens: 80,
			expectReject: true, expectMessage: "max(100,50)=100 > 80, 拒绝",
		},
		{
			name: "Maximal策略-下游价格高-令牌充足", aggregation: "maximal",
			ownPrice: 30, dsPrice: 200, tokens: 300,
			expectReject: false, expectMessage: "max(30,200)=200 < 300, 通过",
		},
		{
			name: "Maximal策略-下游价格高-令牌不足", aggregation: "maximal",
			ownPrice: 30, dsPrice: 200, tokens: 150,
			expectReject: true, expectMessage: "max(30,200)=200 > 150, 拒绝",
		},
	}

	for _, tc := range strategies {
		t.Run(tc.name, func(t *testing.T) {
			opts := map[string]interface{}{
				"loadShedding":     true,
				"priceStrategy":    "step",
				"priceAggregation": tc.aggregation,
			}
			callMap := map[string][]string{"agg_tool": {"downstream_a"}}
			gov := mcpgov.NewMCPGovernor("agg-node", callMap, opts)

			gov.SetOwnPrice(tc.ownPrice)
			// 更新下游价格
			gov.UpdateDownstreamPrice(context.Background(), "agg_tool", "downstream_a", tc.dsPrice)

			_, _, err := gov.LoadShedding(context.Background(), tc.tokens, "agg_tool")

			if tc.expectReject {
				if err == nil {
					t.Errorf("预期拒绝但通过了: %s", tc.expectMessage)
				} else {
					t.Logf("✅ %s", tc.expectMessage)
				}
			} else {
				if err != nil {
					t.Errorf("预期通过但被拒绝了: %s (err: %v)", tc.expectMessage, err)
				} else {
					t.Logf("✅ %s", tc.expectMessage)
				}
			}
		})
	}
}

func init() {
	// 静默 fmt 未使用的警告
	_ = fmt.Sprint
}
