// poisson_burst_test.go
// 基于泊松分布的突发流量压力测试 (v3)
//
// 背景：现实中的流量并非均匀到达，而是呈"突发"(Bursty)特征。
// 泊松过程 (Poisson Process) 是建模"随机到达"最经典的数学工具：
//   - 事件到达间隔服从指数分布 (Exponential Distribution)
//   - 单位时间内到达数量服从泊松分布 (Poisson Distribution)
//   - 参数 λ (lambda) 表示平均到达速率 (requests per second)
//
// 设计要点——为什么旧版所有拒绝率都为 0：
//
//	问题 1: time.Sleep 不产生调度竞争
//	  handler 中用 time.Sleep 模拟延迟 → goroutine 退出运行队列 →
//	  /sched/latencies 几乎为 0 → pinpointQueuing 永远不涨价。
//	  修复：使用 CPU 忙等 (busyWork) 替代 time.Sleep。
//
//	问题 2: checkBoth 的 AND 逻辑
//	  同时开启 pinpointQueuing + pinpointThroughput → 走 checkBoth() →
//	  必须吞吐量 AND 排队延迟 *同时* 超标才涨价 → 条件过于苛刻。
//	  修复：只启用 pinpointThroughput，走 throughputCheck()，单条件判定。
//
//	问题 3: Increment() 只在 additive 路径调用
//	  默认 priceAggregation="maximal" 时，LoadShedding 不调用 Increment() →
//	  throughputCounter 永远为 0 → throughputCheck 永远判定不过载。
//	  修复：必须使用 priceAggregation="additive"。
//
//	问题 4: 令牌预算过高
//	  tokens=5000, initprice=0 → 价格涨到 4999 都不会拒绝。
//	  修复：令牌设为 50-100，初始价格 > 0。
//
//	问题 5: 8 核 CPU 不易产生调度竞争
//	  busyWork(500μs) 在 8 核上只占 ~10% CPU，排队延迟几乎为 0。
//	  修复：使用 runtime.GOMAXPROCS(2) 限制 CPU 线程，制造调度瓶颈。
package mcp_test

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mcpgov "mcp-governance"
)

// ==================== 辅助函数 ====================

// busyWork 通过 CPU 密集计算制造真实的调度压力。
// 与 time.Sleep 不同，busyWork 让 goroutine 持续占用 CPU 时间片，
// 其他 goroutine 必须等待调度器分配 CPU，从而产生可被
// runtime/metrics /sched/latencies:seconds 捕获到的排队延迟。
func busyWork(duration time.Duration) {
	start := time.Now()
	x := 1.0
	for time.Since(start) < duration {
		for i := 0; i < 100; i++ {
			x = math.Sin(x) + math.Cos(x)
		}
	}
	_ = x
}

// poissonSender 按泊松过程 (指数间隔) 发送 HTTP 请求
func poissonSender(
	url string,
	toolName string,
	tokens int64,
	lambda float64,
	accepted *int64,
	rejected *int64,
	wg *sync.WaitGroup,
	stop <-chan struct{},
) {
	defer wg.Done()
	for {
		select {
		case <-stop:
			return
		default:
			intervalSec := rand.ExpFloat64() / lambda
			interval := time.Duration(intervalSec * float64(time.Second))
			if interval > 200*time.Millisecond {
				interval = 200 * time.Millisecond
			}
			time.Sleep(interval)

			req := makeToolCallReq(1, toolName, tokens)
			resp := sendRequest(url, req)
			if resp != nil && resp.Error != nil {
				atomic.AddInt64(rejected, 1)
			} else if resp != nil {
				atomic.AddInt64(accepted, 1)
			}
		}
	}
}

// poissonSample 生成泊松分布随机数 (Knuth 算法 / 正态近似)
func poissonSample(mu float64) int {
	if mu > 30 {
		sample := mu + math.Sqrt(mu)*rand.NormFloat64()
		if sample < 0 {
			return 0
		}
		return int(math.Round(sample))
	}
	L := math.Exp(-mu)
	k := 0
	p := 1.0
	for {
		k++
		p *= rand.Float64()
		if p < L {
			break
		}
	}
	return k - 1
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

// phaseSample 阶段采样
type phaseSample struct {
	name       string
	lambda     float64
	duration   time.Duration
	accepted   int64
	rejected   int64
	finalPrice string
	actualRPS  float64
}

// ==================== 可靠触发治理的配置方案 ====================

// makeThroughputOpts 创建基于吞吐量检测的服务端配置
//
// 路径选择：pinpointThroughput=true, pinpointQueuing=false
//
//	→ 走 throughputCheck()（不走 checkBoth 的 AND 逻辑）
//	→ 只需 GetCount() > throughputThreshold 即涨价
//
// 必须配合 priceAggregation="additive"：
//
//	Increment() 只在 LoadShedding(additive 分支) 中被调用
func makeThroughputOpts(initprice int64, threshold int64) map[string]interface{} {
	return map[string]interface{}{
		"priceUpdateRate":     5 * time.Millisecond, // 检测窗口 5ms
		"pinpointThroughput":  true,                 // 只开吞吐量检测
		"pinpointQueuing":     false,                // 不开排队延迟 (避免 checkBoth AND)
		"throughputThreshold": threshold,            // 每 5ms 窗口超过此数就涨价
		"priceStep":           int64(10),            // 每次涨 10
		"priceStrategy":       "step",               // step: 简单递增
		"priceAggregation":    "additive",           // 必须 additive: Increment() 才会被调用
		"loadShedding":        true,
		"rateLimiting":        true,
		"lazyResponse":        false,
		"priceFreq":           int64(1),
		"initprice":           initprice,
	}
}

// makeQueuingOpts 创建基于排队延迟检测的配置 (需配合 GOMAXPROCS 使用)
//
// 路径选择：pinpointQueuing=true, pinpointThroughput=false
//
//	→ 走 queuingCheck()
//	→ 读取 /sched/latencies 检测 goroutine 调度延迟
//
// 注意：必须同时 GOMAXPROCS(1-2) + busyWork handler 才能产生可观的调度延迟
func makeQueuingOpts(initprice int64) map[string]interface{} {
	return map[string]interface{}{
		"priceUpdateRate":    5 * time.Millisecond,
		"latencyThreshold":   50 * time.Microsecond, // 极低阈值: 50μs
		"priceStep":          int64(180),
		"priceStrategy":      "expdecay",
		"pinpointQueuing":    true,
		"pinpointThroughput": false,
		"loadShedding":       true,
		"rateLimiting":       true,
		"lazyResponse":       false,
		"priceFreq":          int64(1),
		"initprice":          initprice,
	}
}

// busyHandler 让 handler 做 CPU 忙等 (制造调度压力)
func busyHandler(d time.Duration) mcpgov.ToolCallHandler {
	return func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
		busyWork(d)
		return &mcpgov.MCPToolCallResult{
			Content: []mcpgov.ContentBlock{mcpgov.TextContent("OK")},
		}, nil
	}
}

// ==================== 测试用例 ====================

// TestPoisson_ThroughputDriven 吞吐量驱动的泊松突发测试
//
// 策略：pinpointThroughput + additive + low threshold
// 这是最可靠的触发路径：只要单位时间内请求数超标就涨价，不依赖 CPU 调度延迟。
// 泊松到达的不均匀性会导致某些 5ms 窗口内请求聚集，更容易触发涨价。
func TestPoisson_ThroughputDriven(t *testing.T) {
	handler := busyHandler(200 * time.Microsecond)

	// 不同 λ 等级
	lambdas := []float64{50, 200, 500, 2000}
	tokens := int64(50) // 低令牌: 价格涨到 51 就会被拒绝

	t.Log("📊 吞吐量驱动泊松测试 (令牌=50, 初始价格=5, 阈值=3/5ms窗口):")
	t.Log("   λ(RPS) | 持续 | 通过 | 拒绝 | 实际RPS | 拒绝率 | 最终价格")
	t.Log("   -------|------|------|------|---------|--------|--------")

	for _, lambda := range lambdas {
		ts, gov := newTestServer("tput_poisson", makeThroughputOpts(5, 3), handler)

		var accepted, rejected int64
		var wg sync.WaitGroup
		stop := make(chan struct{})

		workers := 8
		perWorkerLambda := lambda / float64(workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go poissonSender(ts.URL, "tput_poisson", tokens, perWorkerLambda, &accepted, &rejected, &wg, stop)
		}

		duration := 3 * time.Second
		time.Sleep(duration)
		close(stop)
		wg.Wait()

		price, _ := gov.RetrieveTotalPrice(context.Background(), "tput_poisson")
		total := accepted + rejected
		rps := float64(total) / duration.Seconds()
		rejectRate := float64(0)
		if total > 0 {
			rejectRate = float64(rejected) / float64(total) * 100
		}

		t.Logf("   %6.0f | %v | %4d | %4d | %7.1f | %5.1f%% | %s",
			lambda, duration, accepted, rejected, rps, rejectRate, price)

		ts.Close()
	}

	t.Log("")
	t.Log("   💡 预期: λ 越高 → 5ms窗口内请求聚集越多 → 超过阈值(3)次数越多 → 价格越高 → 拒绝越多")
}

// TestPoisson_QueuingDriven 排队延迟驱动的泊松突发测试 (GOMAXPROCS=2)
//
// 策略: pinpointQueuing + GOMAXPROCS(2) + busyWork handler
// 限制到 2 个 CPU 线程 → 即使中等负载也会产生 goroutine 调度竞争 →
// /sched/latencies 上升 → queuingCheck 检测到过载 → 涨价 → 拒绝低预算请求
func TestPoisson_QueuingDriven(t *testing.T) {
	// 限制 GOMAXPROCS 制造调度瓶颈
	prev := runtime.GOMAXPROCS(2)
	defer runtime.GOMAXPROCS(prev)
	t.Logf("GOMAXPROCS 已限制为 2 (原值: %d)", prev)

	handler := busyHandler(500 * time.Microsecond) // 每个请求占用 500μs CPU

	lambdas := []float64{100, 500, 1500, 3000}
	tokens := int64(40)

	t.Log("📊 排队延迟驱动泊松测试 (GOMAXPROCS=2, 令牌=40, busyWork=500μs):")
	t.Log("   λ(RPS) | 持续 | 通过 | 拒绝 | 实际RPS | 拒绝率 | 最终价格")
	t.Log("   -------|------|------|------|---------|--------|--------")

	for _, lambda := range lambdas {
		ts, gov := newTestServer("queuing_poisson", makeQueuingOpts(5), handler)

		var accepted, rejected int64
		var wg sync.WaitGroup
		stop := make(chan struct{})

		workers := 8
		perWorkerLambda := lambda / float64(workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go poissonSender(ts.URL, "queuing_poisson", tokens, perWorkerLambda, &accepted, &rejected, &wg, stop)
		}

		duration := 3 * time.Second
		time.Sleep(duration)
		close(stop)
		wg.Wait()

		price, _ := gov.RetrieveTotalPrice(context.Background(), "queuing_poisson")
		total := accepted + rejected
		rps := float64(total) / duration.Seconds()
		rejectRate := float64(0)
		if total > 0 {
			rejectRate = float64(rejected) / float64(total) * 100
		}

		t.Logf("   %6.0f | %v | %4d | %4d | %7.1f | %5.1f%% | %s",
			lambda, duration, accepted, rejected, rps, rejectRate, price)

		ts.Close()
	}

	t.Log("")
	t.Log("   💡 预期: GOMAXPROCS=2 下 busyWork 争抢 CPU → 调度延迟 > 50μs → 涨价 → 拒绝")
}

// TestPoisson_VariableRate 非齐次泊松过程 (NHPP) 突发测试
// λ 随时间变化: 正常(30) → 爬升(200) → 峰值(1500) → 骤降(50) → 恢复(20)
func TestPoisson_VariableRate(t *testing.T) {
	handler := busyHandler(200 * time.Microsecond)

	phases := []struct {
		name     string
		lambda   float64
		duration time.Duration
	}{
		{"🟢 正常期", 30, 2 * time.Second},
		{"🟡 爬升期", 200, 2 * time.Second},
		{"🔴 突发峰值", 1500, 3 * time.Second},
		{"🟡 骤降期", 50, 2 * time.Second},
		{"🟢 恢复期", 20, 3 * time.Second},
	}

	// throughput 策略: 阈值=5 → 每 5ms 超过 5 个请求就涨价
	ts, gov := newTestServer("poisson_nhpp", makeThroughputOpts(5, 5), handler)
	defer ts.Close()

	tokens := int64(60)
	workers := 8
	results := make([]phaseSample, 0, len(phases))

	t.Log("📊 非齐次泊松过程 (NHPP) 突发测试:")
	t.Log("   曲线: 正常(30) → 爬升(200) → 峰值(1500) → 骤降(50) → 恢复(20)")
	t.Log("")
	t.Log("   阶段         | λ(RPS) | 通过 | 拒绝 | 实际RPS | 拒绝率 | 价格")
	t.Log("   -------------|--------|------|------|---------|--------|------")

	for _, phase := range phases {
		var accepted, rejected int64
		var wg sync.WaitGroup

		if phase.lambda > 0 {
			stop := make(chan struct{})
			perWorkerLambda := phase.lambda / float64(workers)
			for i := 0; i < workers; i++ {
				wg.Add(1)
				go poissonSender(ts.URL, "poisson_nhpp", tokens, perWorkerLambda, &accepted, &rejected, &wg, stop)
			}
			time.Sleep(phase.duration)
			close(stop)
			wg.Wait()
		} else {
			time.Sleep(phase.duration)
		}

		price, _ := gov.RetrieveTotalPrice(context.Background(), "poisson_nhpp")
		total := accepted + rejected
		rps := float64(total) / phase.duration.Seconds()
		rejectRate := float64(0)
		if total > 0 {
			rejectRate = float64(rejected) / float64(total) * 100
		}

		sample := phaseSample{
			name: phase.name, lambda: phase.lambda, duration: phase.duration,
			accepted: accepted, rejected: rejected, finalPrice: price, actualRPS: rps,
		}
		results = append(results, sample)

		t.Logf("   %-12s | %6.0f | %4d | %4d | %7.1f | %5.1f%% | %s",
			phase.name, phase.lambda, accepted, rejected, rps, rejectRate, price)
	}

	t.Log("")
	t.Log("   === 治理行为验证 ===")
	if len(results) >= 3 {
		normalTotal := max64(results[0].accepted+results[0].rejected, 1)
		burstTotal := max64(results[2].accepted+results[2].rejected, 1)
		normalReject := float64(results[0].rejected) / float64(normalTotal) * 100
		burstReject := float64(results[2].rejected) / float64(burstTotal) * 100
		t.Logf("   正常期拒绝率: %.1f%%, 突发期拒绝率: %.1f%%", normalReject, burstReject)
		if burstReject > normalReject {
			t.Log("   ✅ 突发期拒绝率高于正常期 — 治理引擎响应了流量变化")
		}
	}
	if len(results) >= 5 {
		t.Logf("   峰值价格: %s → 恢复期价格: %s", results[2].finalPrice, results[4].finalPrice)
	}
}

// TestPoisson_CompoundBurst 复合泊松突发 (Compound Poisson)
// 外层泊松: 突发事件到达 (λ_burst=15/s)
// 内层泊松: 每次突发并行请求数 (μ=12)
// 模拟："AI Agent 发起一次规划任务 → 瞬间并行调用 N 个工具"
func TestPoisson_CompoundBurst(t *testing.T) {
	handler := busyHandler(200 * time.Microsecond)
	ts, gov := newTestServer("poisson_compound", makeThroughputOpts(5, 3), handler)
	defer ts.Close()

	const (
		testDuration = 8 * time.Second
		lambdaBurst  = 15.0
		muBatchSize  = 12.0
		tokens       = int64(60)
	)

	var accepted, rejected, totalBursts int64
	stop := make(chan struct{})
	var wg sync.WaitGroup

	type priceSnapshot struct {
		t     time.Duration
		price string
	}
	var priceMu sync.Mutex
	priceHistory := make([]priceSnapshot, 0)

	startTime := time.Now()
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				price, _ := gov.RetrieveTotalPrice(context.Background(), "poisson_compound")
				priceMu.Lock()
				priceHistory = append(priceHistory, priceSnapshot{t: time.Since(startTime), price: price})
				priceMu.Unlock()
			}
		}
	}()

	// 复合泊松发射器
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				burstInterval := time.Duration(rand.ExpFloat64() / lambdaBurst * float64(time.Second))
				if burstInterval > 1*time.Second {
					burstInterval = 1 * time.Second
				}
				time.Sleep(burstInterval)

				batchSize := poissonSample(muBatchSize)
				if batchSize < 1 {
					batchSize = 1
				}
				if batchSize > 50 {
					batchSize = 50
				}
				atomic.AddInt64(&totalBursts, 1)

				var batchWg sync.WaitGroup
				for j := 0; j < batchSize; j++ {
					batchWg.Add(1)
					go func() {
						defer batchWg.Done()
						req := makeToolCallReq(1, "poisson_compound", tokens)
						resp := sendRequest(ts.URL, req)
						if resp != nil && resp.Error != nil {
							atomic.AddInt64(&rejected, 1)
						} else if resp != nil {
							atomic.AddInt64(&accepted, 1)
						}
					}()
				}
				batchWg.Wait()
			}
		}
	}()

	time.Sleep(testDuration)
	close(stop)
	wg.Wait()

	total := accepted + rejected
	rejectRate := float64(0)
	if total > 0 {
		rejectRate = float64(rejected) / float64(total) * 100
	}

	t.Log("📊 复合泊松突发测试:")
	t.Logf("   λ_burst=%.0f/s, μ_batch=%.0f, 令牌=%d, 阈值=3/5ms", lambdaBurst, muBatchSize, tokens)
	t.Logf("   突发事件: %d, 总请求: %d (通过=%d, 拒绝=%d)", totalBursts, total, accepted, rejected)
	t.Logf("   拒绝率: %.1f%%, 等效RPS: %.1f", rejectRate, float64(total)/testDuration.Seconds())
	t.Log("")
	t.Log("   价格轨迹:")
	t.Log("   时间     | 价格")
	t.Log("   ---------|------")
	priceMu.Lock()
	for _, snap := range priceHistory {
		t.Logf("   %7.1fs  | %s", snap.t.Seconds(), snap.price)
	}
	priceMu.Unlock()

	if rejectRate > 0 {
		t.Log("   ✅ 复合泊松突发成功触发治理拒绝")
	} else if total > 50 {
		t.Log("   ⚠️ 拒绝率为0, 可能需要更大批次或更低阈值")
	}
}

// TestPoisson_SpikeAmplitude 对比测试：不同突发振幅 (burstiness)
// 固定等效 RPS ≈ 100，但改变突发聚集程度
func TestPoisson_SpikeAmplitude(t *testing.T) {
	handler := busyHandler(200 * time.Microsecond)

	profiles := []struct {
		name        string
		lambdaBurst float64
		muBatch     float64
	}{
		{"均匀 (100×1)", 100, 1},
		{"轻微突发 (50×2)", 50, 2},
		{"中等突发 (20×5)", 20, 5},
		{"强烈突发 (10×10)", 10, 10},
		{"极端突发 (5×20)", 5, 20},
	}

	t.Log("📊 突发振幅对比 (等效RPS≈100, 令牌=30, 阈值=3/5ms):")
	t.Log("   形态              | λ_burst | μ_batch | 通过 | 拒绝 | RPS   | 拒绝率 | 最终价格 | 峰值价格")
	t.Log("   ------------------|---------|---------|------|------|-------|--------|----------|--------")

	for _, profile := range profiles {
		ts, gov := newTestServer("spike_tool", makeThroughputOpts(5, 3), handler)

		var accepted, rejected int64
		var peakPrice int64
		stop := make(chan struct{})
		var wg sync.WaitGroup

		// 峰值追踪
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(50 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					p := getOwnPrice(gov)
					for {
						cur := atomic.LoadInt64(&peakPrice)
						if p <= cur || atomic.CompareAndSwapInt64(&peakPrice, cur, p) {
							break
						}
					}
				}
			}
		}()

		// 复合泊松
		wg.Add(1)
		go func(lb, mb float64) {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					burstInterval := time.Duration(rand.ExpFloat64() / lb * float64(time.Second))
					if burstInterval > 1*time.Second {
						burstInterval = 1 * time.Second
					}
					time.Sleep(burstInterval)
					batchSize := poissonSample(mb)
					if batchSize < 1 {
						batchSize = 1
					}
					if batchSize > 80 {
						batchSize = 80
					}
					var batchWg sync.WaitGroup
					for j := 0; j < batchSize; j++ {
						batchWg.Add(1)
						go func() {
							defer batchWg.Done()
							req := makeToolCallReq(1, "spike_tool", 30)
							resp := sendRequest(ts.URL, req)
							if resp != nil && resp.Error != nil {
								atomic.AddInt64(&rejected, 1)
							} else if resp != nil {
								atomic.AddInt64(&accepted, 1)
							}
						}()
					}
					batchWg.Wait()
				}
			}
		}(profile.lambdaBurst, profile.muBatch)

		time.Sleep(4 * time.Second)
		close(stop)
		wg.Wait()

		finalPrice, _ := gov.RetrieveTotalPrice(context.Background(), "spike_tool")
		total := accepted + rejected
		rps := float64(total) / 4.0
		rejectRate := float64(0)
		if total > 0 {
			rejectRate = float64(rejected) / float64(total) * 100
		}

		t.Logf("   %-19s | %7.0f | %7.0f | %4d | %4d | %5.1f | %5.1f%% | %8s | %d",
			profile.name, profile.lambdaBurst, profile.muBatch,
			accepted, rejected, rps, rejectRate, finalPrice, atomic.LoadInt64(&peakPrice))
		ts.Close()
	}

	t.Log("")
	t.Log("   💡 同样平均 RPS，越突发 → 瞬时聚集越多 → 价格飙升越快 → 拒绝率越高")
}

// TestPoisson_ClientTokenRefill 客户端泊松令牌补充 + 服务端泊松流量 (双重随机系统)
func TestPoisson_ClientTokenRefill(t *testing.T) {
	handler := busyHandler(200 * time.Microsecond)
	ts, serverGov := newTestServer("poisson_refill", makeThroughputOpts(10, 3), handler)
	defer ts.Close()

	// 客户端: 泊松令牌补充，初始少、补充慢
	clientCallMap := map[string][]string{"poisson_refill": {"test-node"}}
	clientGov := mcpgov.NewMCPGovernor("client", clientCallMap, map[string]interface{}{
		"rateLimiting":    true,
		"tokensLeft":      int64(20),             // 初始很少
		"tokenUpdateRate": 50 * time.Millisecond, // 平均每 50ms 补充一次
		"tokenUpdateStep": int64(3),              // 每次 3 个 (慢速补充)
		"tokenRefillDist": "poisson",             // 🔑 泊松分布补充
		"maxToken":        int64(100),
	})
	clientGov.UpdateDownstreamPrice(context.Background(), "poisson_refill", "test-node", 0)

	const testDuration = 5 * time.Second
	var accepted, rejected, rateLimited int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	lambda := 500.0

	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			perWorkerLambda := lambda / 4.0
			for {
				select {
				case <-stop:
					return
				default:
					intervalSec := rand.ExpFloat64() / perWorkerLambda
					interval := time.Duration(intervalSec * float64(time.Second))
					if interval > 200*time.Millisecond {
						interval = 200 * time.Millisecond
					}
					time.Sleep(interval)

					params := &mcpgov.MCPToolCallParams{
						Name: "poisson_refill", Arguments: map[string]interface{}{},
					}
					err := clientGov.ClientMiddleware(context.Background(), params)
					if err != nil {
						atomic.AddInt64(&rateLimited, 1)
						continue
					}

					req := makeToolCallReq(1, "poisson_refill", params.Meta.Tokens)
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

	type tokenSnap struct {
		t      time.Duration
		tokens int64
	}
	var tokenMu sync.Mutex
	tokenHistory := make([]tokenSnap, 0)
	startTime := time.Now()

	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				tokenMu.Lock()
				tokenHistory = append(tokenHistory, tokenSnap{t: time.Since(startTime), tokens: clientGov.GetTokensLeft()})
				tokenMu.Unlock()
			}
		}
	}()

	time.Sleep(testDuration)
	close(stop)
	wg.Wait()

	total := accepted + rejected
	rejectRate := float64(0)
	if total > 0 {
		rejectRate = float64(rejected) / float64(total) * 100
	}
	serverPrice, _ := serverGov.RetrieveTotalPrice(context.Background(), "poisson_refill")
	totalAttempts := total + rateLimited

	t.Log("📊 泊松令牌补充 + 泊松请求 (双重随机):")
	t.Logf("   客户端: poisson补充, 间隔=50ms, 步长=3, 初始=20")
	t.Logf("   请求: λ=%.0f RPS 泊松到达", lambda)
	t.Log("")
	t.Logf("   总尝试: %d (客户端限流=%d, 到达服务端=%d)", totalAttempts, rateLimited, total)
	t.Logf("   服务端: 通过=%d, 拒绝=%d (拒绝率=%.1f%%)", accepted, rejected, rejectRate)
	t.Logf("   服务端价格: %s", serverPrice)
	t.Log("")
	t.Log("   令牌余额:")
	t.Log("   时间     | 余额")
	t.Log("   ---------|------")
	tokenMu.Lock()
	for _, s := range tokenHistory {
		t.Logf("   %7.1fs  | %d", s.t.Seconds(), s.tokens)
	}
	tokenMu.Unlock()

	if rateLimited > 0 || rejected > 0 {
		t.Logf("   ✅ 多层限流生效: 客户端限流=%d, 服务端拒绝=%d", rateLimited, rejected)
	}
}

// TestPoisson_SustainedBurst 持续高 λ 泊松冲击下的系统稳定性
func TestPoisson_SustainedBurst(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过持续突发测试 (-short)")
	}

	handler := busyHandler(200 * time.Microsecond)
	ts, gov := newTestServer("poisson_sustained", makeThroughputOpts(5, 3), handler)
	defer ts.Close()

	const testDuration = 10 * time.Second
	const lambda = 1000.0
	const workers = 10
	const tokens = int64(60)

	var accepted, rejected int64
	stop := make(chan struct{})
	var wg sync.WaitGroup

	type snapshot struct {
		t        time.Duration
		price    string
		accepted int64
		rejected int64
	}
	var snapMu sync.Mutex
	snapshots := make([]snapshot, 0)
	startTime := time.Now()

	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				price, _ := gov.RetrieveTotalPrice(context.Background(), "poisson_sustained")
				a, r := atomic.LoadInt64(&accepted), atomic.LoadInt64(&rejected)
				snapMu.Lock()
				snapshots = append(snapshots, snapshot{t: time.Since(startTime), price: price, accepted: a, rejected: r})
				snapMu.Unlock()
			}
		}
	}()

	perWorkerLambda := lambda / float64(workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go poissonSender(ts.URL, "poisson_sustained", tokens, perWorkerLambda, &accepted, &rejected, &wg, stop)
	}

	time.Sleep(testDuration)
	close(stop)
	wg.Wait()

	total := accepted + rejected
	rejectRate := float64(0)
	if total > 0 {
		rejectRate = float64(rejected) / float64(total) * 100
	}

	t.Log("📊 持续泊松突发 (10秒稳定性):")
	t.Logf("   λ=%.0f, workers=%d, 令牌=%d, 阈值=3/5ms", lambda, workers, tokens)
	t.Logf("   总请求: %d (通过=%d, 拒绝=%d, 拒绝率=%.1f%%)", total, accepted, rejected, rejectRate)
	t.Log("")
	t.Log("   时间   | 累计通过   | 累计拒绝   | 价格")
	t.Log("   -------|-----------|-----------|------")

	snapMu.Lock()
	prevA, prevR := int64(0), int64(0)
	for _, s := range snapshots {
		dA, dR := s.accepted-prevA, s.rejected-prevR
		t.Logf("   %5.1fs  | %5d(+%3d) | %5d(+%3d) | %s",
			s.t.Seconds(), s.accepted, dA, s.rejected, dR, s.price)
		prevA, prevR = s.accepted, s.rejected
	}
	snapMu.Unlock()

	if rejectRate > 0 && rejectRate < 95 {
		t.Logf("   ✅ 拒绝率 %.1f%% — 治理引擎在持续压力下正常工作", rejectRate)
	} else if rejectRate >= 95 {
		t.Logf("   ⚠️ 拒绝率过高 (%.1f%%), 价格可能持续飙升", rejectRate)
	} else if total > 100 && rejectRate == 0 {
		t.Log("   ⚠️ 持续高负载下拒绝率为0")
	}
}

func init() {
	_ = fmt.Sprintf
}
