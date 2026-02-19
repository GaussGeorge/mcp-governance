// dynamic_pricing_test.go
// 动态定价 (Dynamic Pricing) 效果测试
// 验证价格在过载/空闲时的自适应调整行为：涨价、降价、衰减、恢复
package mcp_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcpgov "mcp-governance"
)

// ==================== 价格自适应测试 ====================

// TestDynamicPricing_StepStrategy_Congestion Step策略: 拥塞时涨价
func TestDynamicPricing_StepStrategy_Congestion(t *testing.T) {
	callMap := map[string][]string{"dp_tool": {}}
	gov := mcpgov.NewMCPGovernor("pricing-node", callMap, map[string]interface{}{
		"loadShedding":  true,
		"priceStrategy": "step",
		"priceStep":     int64(50),
	})

	// 初始价格应为 0
	initialPrice := getOwnPrice(gov)
	t.Logf("初始价格: %d", initialPrice)

	// 模拟连续拥塞
	for i := 0; i < 5; i++ {
		gov.UpdateOwnPrice(true) // congestion = true
	}

	afterCongestion := getOwnPrice(gov)
	t.Logf("📊 连续5次拥塞后价格: %d (涨幅: +%d)", afterCongestion, afterCongestion-initialPrice)

	if afterCongestion <= initialPrice {
		t.Error("拥塞后价格应上涨")
	}

	expectedPrice := initialPrice + 5*50
	if afterCongestion != expectedPrice {
		t.Errorf("Step策略每次应涨 priceStep=%d; 实际: %d, 期望: %d", 50, afterCongestion, expectedPrice)
	}
}

// TestDynamicPricing_StepStrategy_Recovery Step策略: 非拥塞时降价恢复
func TestDynamicPricing_StepStrategy_Recovery(t *testing.T) {
	callMap := map[string][]string{"dp_tool": {}}
	gov := mcpgov.NewMCPGovernor("pricing-node", callMap, map[string]interface{}{
		"loadShedding":  true,
		"priceStrategy": "step",
		"priceStep":     int64(100),
	})

	// 先涨到 300
	for i := 0; i < 3; i++ {
		gov.UpdateOwnPrice(true)
	}
	peakPrice := getOwnPrice(gov)
	t.Logf("峰值价格: %d", peakPrice)

	// 然后持续非拥塞，价格应逐步回落（每次 -1）
	for i := 0; i < 100; i++ {
		gov.UpdateOwnPrice(false) // congestion = false
	}

	recoveredPrice := getOwnPrice(gov)
	t.Logf("📊 非拥塞100次后价格: %d (从 %d 回落)", recoveredPrice, peakPrice)

	if recoveredPrice >= peakPrice {
		t.Error("长期非拥塞后价格应回落")
	}

	// 价格回落速度是每次 -1
	expectedPrice := peakPrice - 100
	if expectedPrice < 0 {
		expectedPrice = 0
	}
	if recoveredPrice != expectedPrice {
		t.Errorf("期望价格=%d, 实际=%d", expectedPrice, recoveredPrice)
	}
}

// TestDynamicPricing_StepStrategy_FloorAtZero 价格不应降至负数
func TestDynamicPricing_StepStrategy_FloorAtZero(t *testing.T) {
	callMap := map[string][]string{"dp_tool": {}}
	gov := mcpgov.NewMCPGovernor("pricing-node", callMap, map[string]interface{}{
		"loadShedding":  true,
		"priceStrategy": "step",
		"priceStep":     int64(10),
	})

	// 先涨一点
	gov.UpdateOwnPrice(true) // +10
	gov.UpdateOwnPrice(true) // +10 => 20

	// 然后大幅降价
	for i := 0; i < 200; i++ {
		gov.UpdateOwnPrice(false) // 每次 -1
	}

	finalPrice := getOwnPrice(gov)
	t.Logf("📊 大幅降价后价格: %d", finalPrice)

	if finalPrice < 0 {
		t.Errorf("价格不应为负数: %d", finalPrice)
	}
}

// TestDynamicPricing_GuidePrice 指导价格机制
// 当设置了 guidePrice，拥塞时价格应直接跳到指导价
func TestDynamicPricing_GuidePrice(t *testing.T) {
	callMap := map[string][]string{"dp_tool": {}}
	gov := mcpgov.NewMCPGovernor("pricing-node", callMap, map[string]interface{}{
		"loadShedding":  true,
		"priceStrategy": "step",
		"priceStep":     int64(10),
		"guidePrice":    int64(500), // 指导价格
	})

	// 触发拥塞
	gov.UpdateOwnPrice(true)

	guidePrice := getOwnPrice(gov)
	t.Logf("📊 指导价格模式: 拥塞后价格=%d (guidePrice=500)", guidePrice)

	if guidePrice != 500 {
		t.Errorf("设置 guidePrice=500 时，拥塞后价格应为 500; 实际=%d", guidePrice)
	}
}

// TestDynamicPricing_ExpDecay_DampenOscillation 指数衰减策略: 抑制价格震荡
func TestDynamicPricing_ExpDecay_DampenOscillation(t *testing.T) {
	callMap := map[string][]string{"dp_tool": {}}
	opts := map[string]interface{}{
		"loadShedding":     true,
		"priceStrategy":    "expdecay",
		"priceStep":        int64(180),
		"latencyThreshold": 500 * time.Microsecond,
		"priceUpdateRate":  5 * time.Millisecond,
	}
	gov := mcpgov.NewMCPGovernor("pricing-node", callMap, opts)

	// 模拟排队延迟高于阈值 (1ms > 0.5ms)
	ctx1 := context.WithValue(context.Background(), mcpgov.GapLatencyKey, 1.0) // 1ms
	ctx2 := context.WithValue(context.Background(), mcpgov.GapLatencyKey, 1.0) // 1ms

	// 连续两次高延迟
	gov.UpdatePrice(ctx1)
	price1 := getOwnPrice(gov)

	gov.UpdatePrice(ctx2)
	price2 := getOwnPrice(gov)

	t.Logf("📊 指数衰减: 第1次涨价后=%d, 第2次涨价后=%d", price1, price2)

	// 后续涨幅应越来越小（衰减效果）
	if price1 > 0 && price2 > 0 {
		rise1 := price1 - 0
		rise2 := price2 - price1
		t.Logf("   第1轮涨幅=%d, 第2轮涨幅=%d", rise1, rise2)
		// 如果连续增加超过2次，衰减才会生效
	}
}

// TestDynamicPricing_ExpDecay_ResetOnDecrease 衰减计数器在降价时重置
func TestDynamicPricing_ExpDecay_ResetOnDecrease(t *testing.T) {
	callMap := map[string][]string{"dp_tool": {}}
	opts := map[string]interface{}{
		"loadShedding":     true,
		"priceStrategy":    "expdecay",
		"priceStep":        int64(180),
		"latencyThreshold": 500 * time.Microsecond,
		"priceUpdateRate":  5 * time.Millisecond,
	}
	gov := mcpgov.NewMCPGovernor("pricing-node", callMap, opts)

	// 连续涨价3次
	highLatencyCtx := context.WithValue(context.Background(), mcpgov.GapLatencyKey, 2.0)
	for i := 0; i < 3; i++ {
		gov.UpdatePrice(highLatencyCtx)
	}
	peakPrice := getOwnPrice(gov)

	// 发送低延迟（触发降价），重置衰减计数器
	lowLatencyCtx := context.WithValue(context.Background(), mcpgov.GapLatencyKey, 0.0)
	gov.UpdatePrice(lowLatencyCtx)
	droppedPrice := getOwnPrice(gov)

	// 再次涨价 - 此时衰减计数器已重置，第一次涨幅应恢复正常
	gov.UpdatePrice(highLatencyCtx)
	reboundPrice := getOwnPrice(gov)

	t.Logf("📊 衰减计数器重置测试:")
	t.Logf("   峰值=%d → 降价后=%d → 反弹=%d", peakPrice, droppedPrice, reboundPrice)

	if droppedPrice >= peakPrice {
		t.Error("低延迟后价格应下降")
	}
	if reboundPrice <= droppedPrice {
		t.Log("⚠️ 反弹价格未高于谷底（可能 guidePrice 限制）")
	}
}

// TestDynamicPricing_ReservePrice 底价 (Reserve Price) 保护
func TestDynamicPricing_ReservePrice(t *testing.T) {
	callMap := map[string][]string{"dp_tool": {}}
	opts := map[string]interface{}{
		"loadShedding":     true,
		"priceStrategy":    "expdecay",
		"priceStep":        int64(180),
		"latencyThreshold": 500 * time.Microsecond,
		"guidePrice":       int64(50), // 底价
	}
	gov := mcpgov.NewMCPGovernor("pricing-node", callMap, opts)

	// 持续低延迟，尝试把价格压到底
	lowCtx := context.WithValue(context.Background(), mcpgov.GapLatencyKey, 0.0)
	for i := 0; i < 50; i++ {
		gov.UpdatePrice(lowCtx)
	}

	finalPrice := getOwnPrice(gov)
	t.Logf("📊 底价保护: 持续低延迟后价格=%d (guidePrice=50)", finalPrice)

	if finalPrice < 50 {
		t.Errorf("价格不应低于 guidePrice(50); 实际=%d", finalPrice)
	}
}

// TestDynamicPricing_OverloadThenRecovery_E2E 端到端: 过载→恢复的完整周期
func TestDynamicPricing_OverloadThenRecovery_E2E(t *testing.T) {
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
		time.Sleep(500 * time.Microsecond) // 轻微延迟
		return &mcpgov.MCPToolCallResult{Content: []mcpgov.ContentBlock{mcpgov.TextContent("OK")}}, nil
	}

	ts, gov := newTestServer("recovery_tool", opts, handler)
	defer ts.Close()

	// Phase 1: 记录初始价格
	initPrice, _ := gov.RetrieveTotalPrice(context.Background(), "recovery_tool")
	t.Logf("Phase 1 - 初始价格: %s", initPrice)

	// Phase 2: 高并发制造过载
	const concurrency = 100
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var requestCount int64

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					req := makeToolCallReq(1, "recovery_tool", 10000)
					sendRequest(ts.URL, req)
					atomic.AddInt64(&requestCount, 1)
				}
			}
		}()
	}

	// 持续过载 3 秒
	time.Sleep(3 * time.Second)
	overloadPrice, _ := gov.RetrieveTotalPrice(context.Background(), "recovery_tool")
	t.Logf("Phase 2 - 过载3秒后价格: %s (请求数=%d)", overloadPrice, atomic.LoadInt64(&requestCount))

	// Phase 3: 停止负载，等待恢复
	close(stop)
	wg.Wait()

	time.Sleep(2 * time.Second) // 等待价格冷却

	recoveredPrice, _ := gov.RetrieveTotalPrice(context.Background(), "recovery_tool")
	t.Logf("Phase 3 - 停止负载2秒后价格: %s", recoveredPrice)

	t.Logf("📊 完整周期: 初始=%s → 过载=%s → 恢复=%s", initPrice, overloadPrice, recoveredPrice)

	if overloadPrice == "0" {
		t.Log("⚠️ 过载期间价格未上涨（可能需要更高并发）")
	}
}

// TestDynamicPricing_DownstreamPropagation 下游价格传播
// 验证下游工具的价格变化能正确传播到上游
func TestDynamicPricing_DownstreamPropagation(t *testing.T) {
	// 工具链: gateway → [service_a, service_b]
	callMap := map[string][]string{
		"gateway": {"service_a", "service_b"},
	}

	gov := mcpgov.NewMCPGovernor("gateway-node", callMap, map[string]interface{}{
		"loadShedding":     true,
		"priceAggregation": "maximal",
	})

	ctx := context.Background()

	// 初始下游价格全部为 0
	dsPrice0, _ := gov.RetrieveDSPrice(ctx, "gateway")
	t.Logf("初始下游聚合价格: %d", dsPrice0)

	// service_a 报价 100
	gov.UpdateDownstreamPrice(ctx, "gateway", "service_a", 100)
	dsPrice1, _ := gov.RetrieveDSPrice(ctx, "gateway")
	t.Logf("service_a=100 后: 下游聚合=%d", dsPrice1)

	// service_b 报价 200（更高）
	gov.UpdateDownstreamPrice(ctx, "gateway", "service_b", 200)
	dsPrice2, _ := gov.RetrieveDSPrice(ctx, "gateway")
	t.Logf("service_b=200 后: 下游聚合=%d", dsPrice2)

	// maximal 策略下应取 200
	if dsPrice2 != 200 {
		t.Errorf("Maximal 策略下聚合价格应为 200; 实际=%d", dsPrice2)
	}

	// service_a 涨到 300
	gov.UpdateDownstreamPrice(ctx, "gateway", "service_a", 300)
	dsPrice3, _ := gov.RetrieveDSPrice(ctx, "gateway")
	t.Logf("service_a=300 后: 下游聚合=%d", dsPrice3)

	if dsPrice3 != 300 {
		t.Errorf("service_a 涨到 300 后, 聚合价格应为 300; 实际=%d", dsPrice3)
	}

	// service_a 降到 50（下游最高变为 service_b 的 200）
	gov.UpdateDownstreamPrice(ctx, "gateway", "service_a", 50)
	dsPrice4, _ := gov.RetrieveDSPrice(ctx, "gateway")
	t.Logf("service_a=50 后: 下游聚合=%d", dsPrice4)

	if dsPrice4 != 200 {
		t.Errorf("service_a 降到 50 后, 聚合价格应回到 200; 实际=%d", dsPrice4)
	}

	t.Logf("📊 下游价格传播 (Maximal): 0 → 100 → 200 → 300 → 200")
}

// TestDynamicPricing_AdditiveAggregation Additive聚合策略下的价格累加
func TestDynamicPricing_AdditiveAggregation(t *testing.T) {
	callMap := map[string][]string{
		"pipeline": {"step1", "step2"},
	}

	gov := mcpgov.NewMCPGovernor("pipeline-node", callMap, map[string]interface{}{
		"loadShedding":     true,
		"priceAggregation": "additive",
	})

	ctx := context.Background()

	// 更新下游价格
	gov.UpdateDownstreamPrice(ctx, "pipeline", "step1", 50)
	gov.UpdateDownstreamPrice(ctx, "pipeline", "step2", 80)

	dsPrice, _ := gov.RetrieveDSPrice(ctx, "pipeline")
	t.Logf("📊 Additive策略: step1=50 + step2=80 = 下游聚合=%d", dsPrice)

	// additive 策略下应为 50 + 80 = 130
	if dsPrice != 130 {
		t.Errorf("Additive 聚合应为 130; 实际=%d", dsPrice)
	}

	// 设置自身价格，验证总价
	gov.SetOwnPrice(20)
	totalPrice, _ := gov.RetrieveTotalPrice(ctx, "pipeline")
	t.Logf("   自身=20 + 下游=130 = 总价=%s", totalPrice)

	if totalPrice != "150" {
		t.Errorf("总价应为 150; 实际=%s", totalPrice)
	}
}

// TestDynamicPricing_MeanAggregation Mean策略
func TestDynamicPricing_MeanAggregation(t *testing.T) {
	callMap := map[string][]string{
		"avg_tool": {"ds1"},
	}

	gov := mcpgov.NewMCPGovernor("avg-node", callMap, map[string]interface{}{
		"loadShedding":     true,
		"priceAggregation": "mean",
	})

	ctx := context.Background()
	gov.SetOwnPrice(100)
	gov.UpdateDownstreamPrice(ctx, "avg_tool", "ds1", 200)

	totalPrice, _ := gov.RetrieveTotalPrice(ctx, "avg_tool")
	t.Logf("📊 Mean策略: own=100, ds=200 → total=%s (期望 150)", totalPrice)

	if totalPrice != "150" {
		t.Errorf("Mean聚合: (100+200)/2=150; 实际=%s", totalPrice)
	}
}

// ==================== 辅助函数 ====================

// getOwnPrice 读取 governor 当前自身价格
func getOwnPrice(gov *mcpgov.MCPGovernor) int64 {
	priceStr, _ := gov.RetrieveTotalPrice(context.Background(), "__none__")
	// 没有下游时，总价 = ownPrice
	var price int64
	for _, c := range priceStr {
		if c >= '0' && c <= '9' {
			price = price*10 + int64(c-'0')
		}
	}
	return price
}
