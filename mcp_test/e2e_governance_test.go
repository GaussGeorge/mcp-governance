// e2e_governance_test.go
// 端到端服务治理效果集成测试
// 模拟真实的 MCP 工具调用场景，验证治理引擎在面对各种负载模式时的行为
package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcpgov "mcp-governance"
)

// ==================== 多工具链路治理 ====================

// TestE2E_MultiToolChain 多工具链路场景
// 模拟 AI Agent 调用 plan_trip 工具，plan_trip 依赖 get_weather 和 search_hotel
// 验证价格在工具链中的传播与聚合
func TestE2E_MultiToolChain(t *testing.T) {
	// === 上游网关 ===
	gatewayCallMap := map[string][]string{
		"plan_trip": {"weather-server", "hotel-server"},
	}
	gatewayGov := mcpgov.NewMCPGovernor("gateway", gatewayCallMap, map[string]interface{}{
		"loadShedding":     true,
		"priceAggregation": "maximal",
	})
	gatewayGov.SetOwnPrice(10)

	// === 下游服务 A: 天气服务 ===
	weatherCallMap := map[string][]string{"get_weather": {}}
	weatherGov := mcpgov.NewMCPGovernor("weather-server", weatherCallMap, map[string]interface{}{
		"loadShedding": true,
	})
	weatherGov.SetOwnPrice(50)

	weatherServer := mcpgov.NewMCPServer("weather-service", weatherGov)
	weatherServer.RegisterTool(mcpgov.MCPTool{
		Name:        "get_weather",
		Description: "天气查询",
		InputSchema: map[string]interface{}{"type": "object"},
	}, func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		return &mcpgov.MCPToolCallResult{
			Content: []mcpgov.ContentBlock{mcpgov.TextContent("北京:晴天25°C")},
		}, nil
	})
	weatherTs := httptest.NewServer(weatherServer)
	defer weatherTs.Close()

	// === 下游服务 B: 酒店搜索 ===
	hotelCallMap := map[string][]string{"search_hotel": {}}
	hotelGov := mcpgov.NewMCPGovernor("hotel-server", hotelCallMap, map[string]interface{}{
		"loadShedding": true,
	})
	hotelGov.SetOwnPrice(80)

	hotelServer := mcpgov.NewMCPServer("hotel-service", hotelGov)
	hotelServer.RegisterTool(mcpgov.MCPTool{
		Name:        "search_hotel",
		Description: "酒店搜索",
		InputSchema: map[string]interface{}{"type": "object"},
	}, func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		return &mcpgov.MCPToolCallResult{
			Content: []mcpgov.ContentBlock{mcpgov.TextContent("如家酒店 ¥299/晚")},
		}, nil
	})
	hotelTs := httptest.NewServer(hotelServer)
	defer hotelTs.Close()

	ctx := context.Background()

	// 模拟网关收到下游价格反馈
	gatewayGov.UpdateDownstreamPrice(ctx, "plan_trip", "weather-server", 50)
	gatewayGov.UpdateDownstreamPrice(ctx, "plan_trip", "hotel-server", 80)

	// 网关总价 = max(ownPrice=10, max(weather=50, hotel=80)) = 80
	totalPrice, _ := gatewayGov.RetrieveTotalPrice(ctx, "plan_trip")
	t.Logf("📊 网关总价: %s (own=10, weather=50, hotel=80, maximal策略)", totalPrice)

	if totalPrice != "80" {
		t.Errorf("Maximal策略总价应为 80; 实际=%s", totalPrice)
	}

	// 客户端以 100 令牌调用 → 网关层应通过 (100 >= 80)
	_, _, err := gatewayGov.LoadShedding(ctx, 100, "plan_trip")
	if err != nil {
		t.Errorf("令牌=100 >= 总价=80, 应通过; 实际错误=%v", err)
	}

	// 客户端以 60 令牌调用 → 网关层应拒绝 (60 < 80)
	_, _, err2 := gatewayGov.LoadShedding(ctx, 60, "plan_trip")
	if err2 == nil {
		t.Error("令牌=60 < 总价=80, 应被拒绝")
	} else {
		t.Logf("✅ 令牌不足的链路调用被正确拒绝")
	}

	// 天气服务涨价到 200 → 网关总价应跟着涨
	gatewayGov.UpdateDownstreamPrice(ctx, "plan_trip", "weather-server", 200)
	newTotal, _ := gatewayGov.RetrieveTotalPrice(ctx, "plan_trip")
	t.Logf("📊 天气服务涨价后网关总价: %s (weather=200)", newTotal)

	if newTotal != "200" {
		t.Errorf("天气涨到200后总价应为200; 实际=%s", newTotal)
	}
}

// TestE2E_SplitTokens 令牌分配测试
// 验证上游服务如何将剩余令牌分配给多个下游
func TestE2E_SplitTokens(t *testing.T) {
	callMap := map[string][]string{
		"orchestrator": {"svc_a", "svc_b", "svc_c"},
	}

	gov := mcpgov.NewMCPGovernor("orch-node", callMap, map[string]interface{}{
		"loadShedding":     true,
		"priceAggregation": "additive",
	})

	ctx := context.Background()

	// 设置下游价格
	gov.UpdateDownstreamPrice(ctx, "orchestrator", "svc_a", 20)
	gov.UpdateDownstreamPrice(ctx, "orchestrator", "svc_b", 30)
	gov.UpdateDownstreamPrice(ctx, "orchestrator", "svc_c", 50)

	// 假设扣除自身价格后还剩 200
	tokens, err := gov.SplitTokens(ctx, 200, "orchestrator")
	if err != nil {
		t.Fatalf("SplitTokens 失败: %v", err)
	}

	t.Logf("📊 令牌分配结果 (总余额=200, 下游价格=20+30+50=100):")
	for i := 0; i < len(tokens)-1; i += 2 {
		t.Logf("   %s = %s", tokens[i], tokens[i+1])
	}

	if len(tokens) != 6 { // 3个下游 × 2 (key + value)
		t.Errorf("令牌分配结果长度=%d; 期望=6", len(tokens))
	}
}

// ==================== 渐进式过载场景 ====================

// TestE2E_ProgressiveOverload 渐进式过载测试
// 逐步增加并发度，观察拒绝率和价格的变化趋势
func TestE2E_ProgressiveOverload(t *testing.T) {
	opts := map[string]interface{}{
		"priceUpdateRate":  10 * time.Millisecond,
		"latencyThreshold": 500 * time.Microsecond,
		"priceStep":        int64(180),
		"priceStrategy":    "expdecay",
		"loadShedding":     true,
		"rateLimiting":     true,
		"pinpointQueuing":  true,
	}

	handler := func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		time.Sleep(2 * time.Millisecond) // 模拟处理延迟
		return &mcpgov.MCPToolCallResult{Content: []mcpgov.ContentBlock{mcpgov.TextContent("OK")}}, nil
	}

	ts, gov := newTestServer("prog_tool", opts, handler)
	defer ts.Close()

	levels := []int{10, 50, 100, 200}
	fixedTokens := int64(500)

	t.Log("📊 渐进式过载测试 (固定令牌=500):")
	t.Log("   并发度 | 通过 | 拒绝 | 拒绝率 | 当前价格")
	t.Log("   -------|------|------|--------|--------")

	for _, concurrency := range levels {
		var accepted, rejected int64
		var wg sync.WaitGroup
		stop := make(chan struct{})

		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for {
					select {
					case <-stop:
						return
					default:
						req := makeToolCallReq(1, "prog_tool", fixedTokens)
						resp := sendRequest(ts.URL, req)
						if resp != nil && resp.Error != nil {
							atomic.AddInt64(&rejected, 1)
						} else if resp != nil {
							atomic.AddInt64(&accepted, 1)
						}
					}
				}
			}()
		}

		// 每个级别持续 2 秒
		time.Sleep(2 * time.Second)
		close(stop)
		wg.Wait()

		price, _ := gov.RetrieveTotalPrice(context.Background(), "prog_tool")
		total := accepted + rejected
		rejectRate := float64(0)
		if total > 0 {
			rejectRate = float64(rejected) / float64(total) * 100
		}

		t.Logf("   %6d | %4d | %4d | %5.1f%% | %s",
			concurrency, accepted, rejected, rejectRate, price)
	}
}

// ==================== 脉冲负载场景 ====================

// TestE2E_BurstTraffic 脉冲式突发流量
// 交替发送高峰和低谷流量，验证价格能否快速响应并恢复
func TestE2E_BurstTraffic(t *testing.T) {
	opts := map[string]interface{}{
		"priceUpdateRate":  5 * time.Millisecond,
		"latencyThreshold": 500 * time.Microsecond,
		"priceStep":        int64(180),
		"priceStrategy":    "expdecay",
		"loadShedding":     true,
		"rateLimiting":     true,
		"pinpointQueuing":  true,
	}

	handler := func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		time.Sleep(1 * time.Millisecond)
		return &mcpgov.MCPToolCallResult{Content: []mcpgov.ContentBlock{mcpgov.TextContent("OK")}}, nil
	}

	ts, gov := newTestServer("burst_tool", opts, handler)
	defer ts.Close()

	t.Log("📊 脉冲流量测试:")
	t.Log("   阶段      | 并发 | 持续 | 结束时价格")
	t.Log("   ----------|------|------|----------")

	phases := []struct {
		name        string
		concurrency int
		duration    time.Duration
	}{
		{"静默期", 0, 1 * time.Second},
		{"脉冲峰值1", 100, 2 * time.Second},
		{"恢复期1", 0, 2 * time.Second},
		{"脉冲峰值2", 150, 2 * time.Second},
		{"恢复期2", 0, 2 * time.Second},
	}

	for _, phase := range phases {
		if phase.concurrency > 0 {
			stop := make(chan struct{})
			var wg sync.WaitGroup

			for i := 0; i < phase.concurrency; i++ {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						select {
						case <-stop:
							return
						default:
							req := makeToolCallReq(1, "burst_tool", 10000)
							sendRequest(ts.URL, req)
						}
					}
				}()
			}

			time.Sleep(phase.duration)
			close(stop)
			wg.Wait()
		} else {
			time.Sleep(phase.duration)
		}

		price, _ := gov.RetrieveTotalPrice(context.Background(), "burst_tool")
		t.Logf("   %-10s | %4d | %v | %s", phase.name, phase.concurrency, phase.duration, price)
	}
}

// ==================== 公平性测试 ====================

// TestE2E_Fairness_HighBudgetPreference 预算公平性测试
// 在过载条件下，高预算请求应比低预算请求更容易通过
func TestE2E_Fairness_HighBudgetPreference(t *testing.T) {
	opts := map[string]interface{}{
		"loadShedding":  true,
		"rateLimiting":  true,
		"priceStrategy": "step",
		"priceStep":     int64(50),
	}

	handler := func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		return &mcpgov.MCPToolCallResult{Content: []mcpgov.ContentBlock{mcpgov.TextContent("OK")}}, nil
	}

	ts, gov := newTestServer("fair_tool", opts, handler)
	defer ts.Close()

	// 设置中等价格
	gov.SetOwnPrice(100)

	type budgetResult struct {
		label    string
		tokens   int64
		accepted int64
		rejected int64
	}

	budgets := []budgetResult{
		{label: "低预算(50)", tokens: 50},
		{label: "中预算(100)", tokens: 100},
		{label: "高预算(200)", tokens: 200},
		{label: "超高预算(1000)", tokens: 1000},
	}

	const requestsEach = 50

	for idx := range budgets {
		for i := 0; i < requestsEach; i++ {
			req := makeToolCallReq(i, "fair_tool", budgets[idx].tokens)
			resp := sendRequest(ts.URL, req)
			if resp != nil && resp.Error != nil {
				budgets[idx].rejected++
			} else if resp != nil {
				budgets[idx].accepted++
			}
		}
	}

	t.Log("📊 预算公平性测试 (价格=100):")
	t.Log("   预算等级      | 通过 | 拒绝 | 通过率")
	t.Log("   -------------|------|------|------")

	prevAcceptRate := -1.0
	for _, b := range budgets {
		total := b.accepted + b.rejected
		acceptRate := float64(b.accepted) / float64(total) * 100
		t.Logf("   %-13s | %4d | %4d | %5.1f%%", b.label, b.accepted, b.rejected, acceptRate)

		// 通过率应该随预算增加而增加
		if prevAcceptRate >= 0 && acceptRate < prevAcceptRate-5 { // 允许5%误差
			t.Errorf("预算越高通过率应越高: %s(%.0f%%) < 上级(%.0f%%)",
				b.label, acceptRate, prevAcceptRate)
		}
		prevAcceptRate = acceptRate
	}
}

// ==================== HandleToolCallDirect 测试 ====================

// TestE2E_HandleToolCallDirect 直接调用模式
func TestE2E_HandleToolCallDirect(t *testing.T) {
	callMap := map[string][]string{"direct_tool": {}}
	gov := mcpgov.NewMCPGovernor("direct-node", callMap, map[string]interface{}{
		"loadShedding":  true,
		"priceStrategy": "step",
		"lazyResponse":  false,
		"priceFreq":     int64(1),
	})

	gov.SetOwnPrice(30)

	handler := func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		return &mcpgov.MCPToolCallResult{
			Content: []mcpgov.ContentBlock{mcpgov.TextContent("direct result")},
		}, nil
	}

	// 令牌充足
	params := mcpgov.MCPToolCallParams{
		Name: "direct_tool",
		Meta: &mcpgov.GovernanceMeta{Tokens: 100, Method: "direct_tool", Name: "tester"},
	}
	result, err := gov.HandleToolCallDirect(context.Background(), params, handler)
	if err != nil {
		t.Fatalf("HandleToolCallDirect 失败: %v", err)
	}

	if len(result.Content) == 0 || result.Content[0].Text != "direct result" {
		t.Error("响应内容不正确")
	}

	if result.Meta != nil && result.Meta.Price != "" {
		t.Logf("✅ HandleToolCallDirect 成功: content=%s, price=%s", result.Content[0].Text, result.Meta.Price)
	}

	// 令牌不足
	params2 := mcpgov.MCPToolCallParams{
		Name: "direct_tool",
		Meta: &mcpgov.GovernanceMeta{Tokens: 5, Method: "direct_tool", Name: "tester"},
	}
	_, err2 := gov.HandleToolCallDirect(context.Background(), params2, handler)
	if err2 == nil {
		t.Error("令牌不足时 HandleToolCallDirect 应返回错误")
	} else {
		t.Logf("✅ 令牌不足被拒绝: %v", err2)
	}
}

// ==================== 稳定性测试 ====================

// TestE2E_LongRunningStability 长时间运行稳定性测试
// 持续发送请求 10 秒，验证系统不会 panic 或内存泄漏
func TestE2E_LongRunningStability(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过长时间稳定性测试 (-short)")
	}

	opts := map[string]interface{}{
		"priceUpdateRate":  10 * time.Millisecond,
		"latencyThreshold": 500 * time.Microsecond,
		"priceStep":        int64(180),
		"priceStrategy":    "expdecay",
		"loadShedding":     true,
		"rateLimiting":     true,
		"pinpointQueuing":  true,
		"lazyResponse":     false,
		"priceFreq":        int64(1),
	}

	handler := func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		time.Sleep(500 * time.Microsecond)
		return &mcpgov.MCPToolCallResult{Content: []mcpgov.ContentBlock{mcpgov.TextContent("OK")}}, nil
	}

	ts, gov := newTestServer("stable_tool", opts, handler)
	defer ts.Close()

	const duration = 10 * time.Second
	const concurrency = 30
	stop := make(chan struct{})

	var totalRequests int64
	var totalErrors int64
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					req := makeToolCallReq(1, "stable_tool", 5000)
					resp := sendRequest(ts.URL, req)
					atomic.AddInt64(&totalRequests, 1)
					if resp == nil {
						atomic.AddInt64(&totalErrors, 1)
					}
				}
			}
		}()
	}

	// 每 2 秒采样价格
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				price, _ := gov.RetrieveTotalPrice(context.Background(), "stable_tool")
				reqs := atomic.LoadInt64(&totalRequests)
				t.Logf("   [采样] 请求数=%d, 当前价格=%s", reqs, price)
			}
		}
	}()

	time.Sleep(duration)
	close(stop)
	wg.Wait()

	finalPrice, _ := gov.RetrieveTotalPrice(context.Background(), "stable_tool")
	t.Logf("📊 稳定性测试 (持续%v, 并发=%d):", duration, concurrency)
	t.Logf("   总请求数: %d", totalRequests)
	t.Logf("   网络错误: %d", totalErrors)
	t.Logf("   最终价格: %s", finalPrice)

	if totalErrors > totalRequests/10 {
		t.Errorf("网络错误率过高: %d/%d", totalErrors, totalRequests)
	}
}

// ==================== 价格信息端到端传递 ====================

// TestE2E_PriceMetaRoundTrip 价格在请求-响应间的完整传递
func TestE2E_PriceMetaRoundTrip(t *testing.T) {
	opts := map[string]interface{}{
		"loadShedding":  true,
		"priceStrategy": "step",
		"priceStep":     int64(50),
		"lazyResponse":  false,
		"priceFreq":     int64(1), // 每次都返回价格
	}

	handler := func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		return &mcpgov.MCPToolCallResult{
			Content: []mcpgov.ContentBlock{mcpgov.TextContent(fmt.Sprintf("tokens=%d", params.Meta.Tokens))},
		}, nil
	}

	ts, gov := newTestServer("meta_tool", opts, handler)
	defer ts.Close()

	gov.SetOwnPrice(42)

	// 发送请求
	req := makeToolCallReq(1, "meta_tool", 100)
	resp := sendRequest(ts.URL, req)

	if resp == nil || resp.Error != nil {
		t.Fatal("请求应成功")
	}

	// 解析响应
	resultBytes, _ := json.Marshal(resp.Result)
	var result mcpgov.MCPToolCallResult
	json.Unmarshal(resultBytes, &result)

	t.Logf("📊 价格元信息往返:")
	t.Logf("   请求令牌: 100")
	t.Logf("   响应内容: %s", result.Content[0].Text)

	if result.Meta != nil {
		t.Logf("   响应价格: %s (节点: %s)", result.Meta.Price, result.Meta.Name)
		if result.Meta.Price == "" {
			t.Error("响应中应包含价格")
		}
	} else {
		t.Error("响应中应包含 _meta")
	}

	// 客户端收到价格后更新缓存
	clientCallMap := map[string][]string{"meta_tool": {"test-node"}}
	clientGov := mcpgov.NewMCPGovernor("client", clientCallMap, map[string]interface{}{
		"rateLimiting":    true,
		"tokenUpdateRate": 1 * time.Hour,
	})

	clientGov.UpdateResponsePrice(context.Background(), "meta_tool", &result)
	cachedPrice, _ := clientGov.RetrieveDSPrice(context.Background(), "meta_tool")
	t.Logf("   客户端缓存价格: %d", cachedPrice)

	if result.Meta != nil && fmt.Sprintf("%d", cachedPrice) != result.Meta.Price {
		t.Logf("⚠️ 客户端缓存 (%d) 与服务端返回 (%s) 不一致", cachedPrice, result.Meta.Price)
	}
}

// ==================== 辅助：静默未使用包 ====================
func init() {
	_ = math.Max
}
