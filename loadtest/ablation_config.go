// ablation_config.go
// ==================== 消融实验参数配置（Ablation Study Configuration） ====================
//
// 【文件在整体测试流程中的位置】
//
//	本文件是消融实验的"参数仓库"，集中定义了所有对照组的具体参数。
//	它不包含任何执行逻辑，仅返回 []AblationGroup 切片供 runner.go 使用。
//	调用链：main.go -mode=ablation → runner.go RunAblationStudy() → 本文件的各函数获取对照组列表
//
// 【消融实验设计原则】
//
//	科学实验中的"控制变量法"：每个对照组只改变一个参数维度，其余保持默认。
//	这样实验结果的差异可以归因于那唯一被改变的参数。
//
// 【Rajomon 参数维度编号规则】（便于在 CSV 和图表中快速定位）
//
//	A 组: PriceStrategy    — 价格策略 (step 线性步进 / expdecay 指数衰减)
//	B 组: PriceAggregation — 聚合策略 (maximal 短板效应 / additive 累加 / mean 平均)
//	C 组: PriceStep        — 价格调整步长 (控制涨价速度，值越大涨价越快)
//	D 组: LatencyThreshold — 排队延迟阈值 (控制过载检测灵敏度，值越小越敏感)
//	E 组: PriceUpdateRate  — 价格更新频率 (控制定价反应速度)
//	F 组: BudgetDistribution — 预算分布 (均匀分布 / 极端贫富差距)
//	X 组: Cross-dimensional — 跨维度组合 (探索参数间的交互效应)
//
// 【负载模式维度】
//
//	每个对照组会分别在三种负载模式下运行（共 N组 × 3模式 次测试）：
//	- step:    阶梯式突发（warmup→low→medium→high→overload→recovery）
//	- sine:    正弦波动（持续周期性变化）
//	- poisson: 泊松到达（随机突发，模拟真实流量）
package main

import "time"

// AllLoadPatterns 返回所有负载模式的枚举列表
// 供 runner.go 在"全模式消融"时遍历使用
func AllLoadPatterns() []LoadPattern {
	return []LoadPattern{PatternStep, PatternSine, PatternPoisson}
}

// ==================== Rajomon 动态定价消融对照组 ====================
//
// 设计原则：以 baseline 为基准，每组只改变一个维度（单因素对照），
// 再加上若干跨维度组合，探索参数之间的交互效应。
//
// 编号规则:
//   A 组: 价格策略 (PriceStrategy)
//   B 组: 聚合策略 (PriceAggregation)
//   C 组: 价格步长 (PriceStep)
//   D 组: 延迟阈值 (LatencyThreshold)
//   E 组: 更新频率 (PriceUpdateRate)
//   F 组: 预算分布 (Budget Distribution)
//   X 组: 跨维度组合 (Cross-dimensional)

// RajomonAblationGroups 返回 Rajomon 动态定价算法的全部消融对照组
// 共包含 1 个基线 + 若干个单因素对照 + 若干个跨维度组合 = 约 20 个对照组
// 每个对照组会在 runner.go 中被 ApplyTo() 到 DefaultTestConfig 上，生成独立的测试配置
func RajomonAblationGroups() []AblationGroup {
	return []AblationGroup{

		// ==================== 基线配置 ====================
		{
			Name:             "rajomon_baseline",
			Description:      "[基线] step策略 + maximal聚合 + priceStep=5 + threshold=2000µs",
			PriceStrategy:    stringPtr("step"),
			PriceAggregation: stringPtr("maximal"),
			PriceStep:        int64Ptr(5),
			LatencyThreshold: durationPtr(2000 * time.Microsecond),
			PriceUpdateRate:  durationPtr(100 * time.Millisecond),
		},

		// ==================== A 组: 价格策略维度 ====================
		{
			// A1: 指数衰减策略 — 价格越高涨得越慢，抑制价格震荡
			Name:             "rajomon_A1_expdecay",
			Description:      "[价格策略] expdecay — 指数衰减抑制震荡，其余参数同基线",
			PriceStrategy:    stringPtr("expdecay"),
			PriceAggregation: stringPtr("maximal"),
			PriceStep:        int64Ptr(5),
			LatencyThreshold: durationPtr(2000 * time.Microsecond),
			PriceUpdateRate:  durationPtr(100 * time.Millisecond),
		},

		// ==================== B 组: 聚合策略维度 ====================
		{
			// B1: 累加聚合 — 每个环节的成本累加，模拟串行调用链
			Name:             "rajomon_B1_additive",
			Description:      "[聚合策略] additive累加 — 串行场景，价格压力叠加",
			PriceAggregation: stringPtr("additive"),
			PriceStrategy:    stringPtr("step"),
			PriceStep:        int64Ptr(5),
			LatencyThreshold: durationPtr(2000 * time.Microsecond),
		},
		{
			// B2: 平均聚合 — 平滑价格波动
			Name:             "rajomon_B2_mean",
			Description:      "[聚合策略] mean平均 — 平滑价格波动，降低峰值冲击",
			PriceAggregation: stringPtr("mean"),
			PriceStrategy:    stringPtr("step"),
			PriceStep:        int64Ptr(5),
			LatencyThreshold: durationPtr(2000 * time.Microsecond),
		},

		// ==================== C 组: 价格步长维度 ====================
		{
			// C1: 极小步长 — 价格温和上涨，更多请求通过，高吞吐
			Name:             "rajomon_C1_step_tiny",
			Description:      "[步长] priceStep=2 — 极小步长，涨价极慢，最大化吞吐",
			PriceStep:        int64Ptr(2),
			LatencyThreshold: durationPtr(2000 * time.Microsecond),
		},
		{
			// C2: 中等步长 — 平衡点
			Name:             "rajomon_C2_step_medium",
			Description:      "[步长] priceStep=12 — 中等步长，平衡吞吐与公平性",
			PriceStep:        int64Ptr(12),
			LatencyThreshold: durationPtr(2000 * time.Microsecond),
		},
		{
			// C3: 大步长 — 快速涨价，低预算迅速被挤出
			Name:             "rajomon_C3_step_large",
			Description:      "[步长] priceStep=20 — 大步长，快速涨价，强制区分预算",
			PriceStep:        int64Ptr(20),
			LatencyThreshold: durationPtr(2000 * time.Microsecond),
		},
		{
			// C4: 超大步长 — 极端涨价
			Name:             "rajomon_C4_step_extreme",
			Description:      "[步长] priceStep=30 — 超大步长，一旦过载即刻拒绝低预算",
			PriceStep:        int64Ptr(30),
			LatencyThreshold: durationPtr(2000 * time.Microsecond),
		},

		// ==================== D 组: 延迟阈值维度 ====================
		{
			// D1: 极严格阈值 — 轻微排队即涨价
			Name:             "rajomon_D1_threshold_strict",
			Description:      "[阈值] threshold=500µs — 极严格，轻微排队即触发涨价",
			LatencyThreshold: durationPtr(500 * time.Microsecond),
			PriceStep:        int64Ptr(5),
		},
		{
			// D2: 宽松阈值 — 允许较多排队才涨价
			Name:             "rajomon_D2_threshold_relaxed",
			Description:      "[阈值] threshold=5000µs — 宽松，高容忍度，逼近无治理吞吐",
			LatencyThreshold: durationPtr(5000 * time.Microsecond),
			PriceStep:        int64Ptr(5),
		},
		{
			// D3: 超宽松阈值 — 几乎不涨价
			Name:             "rajomon_D3_threshold_ultra",
			Description:      "[阈值] threshold=10000µs — 超宽松，仅极端过载才涨价",
			LatencyThreshold: durationPtr(10000 * time.Microsecond),
			PriceStep:        int64Ptr(5),
		},

		// ==================== E 组: 更新频率维度 ====================
		{
			// E1: 快速更新 — 对负载变化反应灵敏
			Name:             "rajomon_E1_update_fast",
			Description:      "[频率] 50ms更新 — 快速反应负载变化，减少延迟波动",
			PriceUpdateRate:  durationPtr(50 * time.Millisecond),
			PriceStep:        int64Ptr(5),
			LatencyThreshold: durationPtr(2000 * time.Microsecond),
		},
		{
			// E2: 慢速更新 — 平滑价格抖动但反应滞后
			Name:             "rajomon_E2_update_slow",
			Description:      "[频率] 300ms更新 — 平滑价格抖动，但反应滞后",
			PriceUpdateRate:  durationPtr(300 * time.Millisecond),
			PriceStep:        int64Ptr(5),
			LatencyThreshold: durationPtr(2000 * time.Microsecond),
		},

		// ==================== F 组: 预算分布维度 ====================
		{
			// F1: 极端贫富差距 — 90%低预算 + 10%高预算
			Name:             "rajomon_F1_skewed_budget",
			Description:      "[预算] 90%低预算(10) + 10%高预算(100) — 考验金牌用户保护",
			PriceStep:        int64Ptr(5),
			LatencyThreshold: durationPtr(2000 * time.Microsecond),
			Budgets:          []int{10, 100},
			BudgetWeights:    []float64{0.9, 0.1},
		},
		{
			// F2: 五级预算梯度 — 更细粒度的价值区分
			Name:             "rajomon_F2_gradient_budget",
			Description:      "[预算] 五级梯度(5/20/50/100/200) — 测试细粒度价值区分",
			PriceStep:        int64Ptr(5),
			LatencyThreshold: durationPtr(2000 * time.Microsecond),
			Budgets:          []int{5, 20, 50, 100, 200},
			BudgetWeights:    []float64{0.3, 0.25, 0.2, 0.15, 0.1},
		},

		// ==================== X 组: 跨维度组合 ====================
		{
			// X1: expdecay + 快速更新 — 最佳震荡抑制组合
			Name:             "rajomon_X1_expdecay_fast",
			Description:      "[组合] expdecay + 50ms更新 — 最佳震荡抑制，平滑P95",
			PriceStrategy:    stringPtr("expdecay"),
			PriceUpdateRate:  durationPtr(50 * time.Millisecond),
			PriceStep:        int64Ptr(8),
			LatencyThreshold: durationPtr(2000 * time.Microsecond),
		},
		{
			// X2: 大步长 + 严格阈值 — 最激进的拒绝策略
			Name:             "rajomon_X2_aggressive",
			Description:      "[组合] priceStep=25 + threshold=800µs — 极速涨价，最强保护",
			PriceStep:        int64Ptr(25),
			LatencyThreshold: durationPtr(800 * time.Microsecond),
		},
		{
			// X3: additive + 大步长 — 累加定价压力
			Name:             "rajomon_X3_additive_steep",
			Description:      "[组合] additive聚合 + priceStep=15 — 串行链条累加高压",
			PriceAggregation: stringPtr("additive"),
			PriceStep:        int64Ptr(15),
			LatencyThreshold: durationPtr(2000 * time.Microsecond),
		},
		{
			// X4: expdecay + additive — 非线性 + 累加
			Name:             "rajomon_X4_expdecay_additive",
			Description:      "[组合] expdecay + additive — 非线性定价与累加聚合",
			PriceStrategy:    stringPtr("expdecay"),
			PriceAggregation: stringPtr("additive"),
			PriceStep:        int64Ptr(8),
			LatencyThreshold: durationPtr(2000 * time.Microsecond),
		},
		{
			// X5: 小步长 + 超宽松阈值 — 极限吞吐探索
			Name:             "rajomon_X5_max_throughput",
			Description:      "[组合] priceStep=2 + threshold=8000µs — 极限吞吐，接近无治理",
			PriceStep:        int64Ptr(2),
			LatencyThreshold: durationPtr(8000 * time.Microsecond),
		},
		{
			// X6: expdecay + mean + 贫富差距 — 综合高级配置
			Name:             "rajomon_X6_advanced",
			Description:      "[组合] expdecay + mean聚合 + 极端预算 — 高级综合场景",
			PriceStrategy:    stringPtr("expdecay"),
			PriceAggregation: stringPtr("mean"),
			PriceStep:        int64Ptr(10),
			LatencyThreshold: durationPtr(2000 * time.Microsecond),
			PriceUpdateRate:  durationPtr(50 * time.Millisecond),
			Budgets:          []int{10, 100},
			BudgetWeights:    []float64{0.9, 0.1},
		},
	}
}

// ==================== 静态限流消融对照组 ====================
// 通过调整 QPS 阈值和突发容量，观察传统限流策略在不同参数下的表现差异。
// 用于与 Rajomon 的消融结果进行横向对比：静态限流能否通过调参达到类似效果？

func StaticRateLimitAblationGroups() []AblationGroup {
	return []AblationGroup{
		{
			Name:               "static_strict",
			Description:        "严格限流: QPS=20, Burst=20 — 低通量高保护",
			StaticRateLimitQPS: float64Ptr(20.0),
			StaticBurstSize:    intPtr(20),
		},
		{
			Name:               "static_default",
			Description:        "默认限流: QPS=30, Burst=30 — 标准配置",
			StaticRateLimitQPS: float64Ptr(30.0),
			StaticBurstSize:    intPtr(30),
		},
		{
			Name:               "static_moderate",
			Description:        "适中限流: QPS=60, Burst=60 — 提升通量",
			StaticRateLimitQPS: float64Ptr(60.0),
			StaticBurstSize:    intPtr(60),
		},
		{
			Name:               "static_relaxed",
			Description:        "宽松限流: QPS=100, Burst=100 — 高通量，接近无限流",
			StaticRateLimitQPS: float64Ptr(100.0),
			StaticBurstSize:    intPtr(100),
		},
	}
}

// ==================== 后端容量消融对照组 ====================
// 改变后端服务器的最大并发容量（MaxServerConcurrency），
// 观察三种策略在"小容量/标准/大容量"后端上的表现。
// 核心问题：Rajomon 的优势是否随后端容量变化而改变？

func CapacityAblationGroups() []AblationGroup {
	return []AblationGroup{
		{
			Name:                 "capacity_small",
			Description:          "小容量后端: maxConcurrency=30 — 资源紧张",
			MaxServerConcurrency: intPtr(30),
			OverloadLatencyScale: float64Ptr(10.0),
		},
		{
			Name:                 "capacity_default",
			Description:          "默认容量后端: maxConcurrency=50 — 标准配置",
			MaxServerConcurrency: intPtr(50),
			OverloadLatencyScale: float64Ptr(8.0),
		},
		{
			Name:                 "capacity_large",
			Description:          "大容量后端: maxConcurrency=100 — 资源充裕",
			MaxServerConcurrency: intPtr(100),
			OverloadLatencyScale: float64Ptr(5.0),
		},
	}
}
