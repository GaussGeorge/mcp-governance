// config.go
// 负载测试配置定义
// 定义测试参数、负载模式、预算组等配置
package main

import "time"

// ==================== 策略类型 ====================

// StrategyType 治理策略枚举
type StrategyType string

const (
	StrategyNoGovernance    StrategyType = "no_governance"     // 无治理
	StrategyStaticRateLimit StrategyType = "static_rate_limit" // 固定阈值限流
	StrategyRajomon         StrategyType = "rajomon"           // Rajomon 动态定价
)

// ==================== 负载模式 ====================

// LoadPattern 负载模式枚举
type LoadPattern string

const (
	PatternStep    LoadPattern = "step"    // 阶梯式突发负载
	PatternSine    LoadPattern = "sine"    // 正弦波动负载
	PatternPoisson LoadPattern = "poisson" // 泊松到达负载
)

// ==================== 阶梯式负载阶段 ====================

// StepPhase 阶梯式负载中的一个阶段
type StepPhase struct {
	Name        string        // 阶段名称 (如 "预热", "阶跃1", "过载")
	Duration    time.Duration // 持续时间
	Concurrency int           // 并发数
}

// ==================== 测试配置 ====================

// TestConfig 负载测试总配置
type TestConfig struct {
	// 基本配置
	Strategy    StrategyType  // 治理策略
	LoadPattern LoadPattern   // 负载模式
	Duration    time.Duration // 测试总时长（仅对 sine/poisson 模式有效）

	// 服务端配置
	ServerAddr           string        // 服务端监听地址
	MockDelay            time.Duration // Mock 工具处理基础延迟
	MockDelayVar         time.Duration // Mock 工具处理延迟随机波动范围
	MaxServerConcurrency int           // 服务端最大并发处理数（模拟资源容量上限）
	OverloadLatencyScale float64       // 过载时延迟放大倍数（超过70%容量时生效）

	// 负载配置
	Budgets       []int     // 客户端预算列表（Token 值）
	BudgetWeights []float64 // 各预算的选择权重（与 Budgets 一一对应，为空则均匀分布）
	ToolName      string    // 测试用工具名称

	// 阶梯式负载配置
	StepPhases []StepPhase // 阶梯式负载的各阶段

	// 正弦波负载配置
	SineBase      int           // 正弦波基础并发数
	SineAmplitude int           // 正弦波振幅
	SinePeriod    time.Duration // 正弦波周期

	// 泊松分布配置
	PoissonQPS float64 // 泊松到达的平均 QPS

	// 静态限流配置
	StaticRateLimitQPS float64 // 固定阈值限流的 QPS 上限
	StaticBurstSize    int     // 突发容量

	// Rajomon 动态定价配置
	RajomonPriceStep        int64         // 价格调整步长
	RajomonLatencyThreshold time.Duration // 延迟阈值
	RajomonPriceUpdateRate  time.Duration // 价格更新频率
	RajomonPriceAggregation string        // 价格聚合策略
	RajomonInitPrice        int64         // 初始价格
	RajomonPriceStrategy    string        // 价格策略 (step / expdecay)

	// 客户端退避配置
	RejectionBackoff time.Duration // 请求被拒绝后的退避等待时间
	RequestInterval  time.Duration // 每次请求之间的固定间隔

	// 输出配置
	OutputDir string // 输出目录
	RunID     string // 本次运行标识
	Verbose   bool   // 是否输出详细日志

	// 随机种子
	RandomSeed int64 // 固定随机种子，确保可复现
}

// DefaultTestConfig 返回默认测试配置
func DefaultTestConfig() *TestConfig {
	return &TestConfig{
		Strategy:    StrategyNoGovernance,
		LoadPattern: PatternStep,
		Duration:    5 * time.Minute,

		ServerAddr:           "127.0.0.1",
		MockDelay:            20 * time.Millisecond,
		MockDelayVar:         30 * time.Millisecond,
		MaxServerConcurrency: 50,  // 模拟后端最大并发容量
		OverloadLatencyScale: 8.0, // 过载时延迟放大到8倍

		Budgets:  []int{10, 50, 100},
		ToolName: "mock_tool",

		// 默认阶梯式负载：预热 → 低 → 中 → 高 → 过载 → 恢复
		StepPhases: []StepPhase{
			{Name: "warmup", Duration: 30 * time.Second, Concurrency: 5},
			{Name: "low", Duration: 60 * time.Second, Concurrency: 20},
			{Name: "medium", Duration: 60 * time.Second, Concurrency: 50},
			{Name: "high", Duration: 60 * time.Second, Concurrency: 100},
			{Name: "overload", Duration: 60 * time.Second, Concurrency: 200},
			{Name: "recovery", Duration: 60 * time.Second, Concurrency: 10},
		},

		// 正弦波配置
		SineBase:      30,
		SineAmplitude: 50,
		SinePeriod:    2 * time.Minute,

		// 泊松配置
		PoissonQPS: 50,

		// 静态限流配置
		StaticRateLimitQPS: 30.0,
		StaticBurstSize:    30,

		// Rajomon 配置（基于消融实验 rajomon_relaxed 组的最优参数）
		// priceStep=5 + threshold=2000µs 实现最高吞吐(215 RPS)和最佳公平性(Budget100=74%)
		RajomonPriceStep:        int64(5),
		RajomonLatencyThreshold: 2000 * time.Microsecond,
		RajomonPriceUpdateRate:  100 * time.Millisecond,
		RajomonPriceAggregation: "maximal",
		RajomonInitPrice:        0,
		RajomonPriceStrategy:    "step",

		// 客户端退避
		RejectionBackoff: 100 * time.Millisecond, // 被拒绝后等待100ms再重试
		RequestInterval:  10 * time.Millisecond,  // 请求间固定间隔10ms

		// 输出
		OutputDir: "output",
		RunID:     "",
		Verbose:   false,

		// 随机种子
		RandomSeed: 42,
	}
}

// GetServerPort 根据策略类型返回不同的端口号
func GetServerPort(strategy StrategyType) int {
	switch strategy {
	case StrategyNoGovernance:
		return 9001
	case StrategyStaticRateLimit:
		return 9002
	case StrategyRajomon:
		return 9003
	default:
		return 9001
	}
}

// ==================== 消融对照组 (Ablation Study) ====================

// AblationGroup 定义一组参数配置，用于对照实验
// 每个对照组只修改少量参数，其余继承 DefaultTestConfig 的默认值
type AblationGroup struct {
	Name        string // 对照组名称（简短标识，如 "rajomon_conservative"）
	Description string // 对照组描述（功能说明）

	// === 公共参数（影响所有策略的后端容量模拟） ===
	// MaxServerConcurrency: 后端最大并发容量
	//   - 较低值(30-50)：模拟小规模服务，过载更容易发生
	//   - 较高值(80-200)：模拟大规模服务，能承受更多流量
	MaxServerConcurrency *int
	// OverloadLatencyScale: 过载时延迟放大倍数（无治理策略使用）
	OverloadLatencyScale *float64

	// === Rajomon 动态定价参数 ===
	// PriceStep: 价格调整步长 — 控制价格上涨速度
	//   - 较小值(1-5)：价格缓慢上涨，更多请求放行 → 高吞吐但可能过载
	//   - 较大值(15-30)：价格快速上涨，迅速拒绝低预算请求 → 低吞吐但高预算成功率高
	PriceStep *int64
	// LatencyThreshold: 排队延迟阈值 — 控制过载检测灵敏度
	//   - 较小值(200-500µs)：非常敏感，轻微负载即触发涨价 → 保守但安全
	//   - 较大值(1-5ms)：高容忍度，允许更多流量通过 → 激进但有风险
	LatencyThreshold *time.Duration
	// PriceUpdateRate: 价格更新频率 — 控制定价反应速度
	//   - 较快(50ms)：快速响应负载变化 → 减少延迟波动
	//   - 较慢(200ms)：平滑价格波动 → 减少抖动但反应滞后
	PriceUpdateRate *time.Duration
	// PriceStrategy: 价格策略 "step"(简单步进) 或 "expdecay"(指数衰减)
	PriceStrategy *string
	// PriceAggregation: 价格聚合策略 "maximal"(短板效应) / "additive"(累加) / "mean"(平均)
	//   - maximal: 取自身与下游价格的最大值（适用于并行调用，瓶颈决定成本）
	//   - additive: 自身 + 下游价格累加（适用于串行调用链）
	//   - mean: 取平均值（平滑波动）
	PriceAggregation *string

	// === 预算分布参数（用于极端压力测试） ===
	// Budgets: 自定义预算列表（覆盖默认值）
	Budgets []int
	// BudgetWeights: 各预算的选择权重（与 Budgets 一一对应）
	//   - 例如 [0.9, 0.1] 表示 90% 低预算、10% 高预算
	BudgetWeights []float64

	// === 静态限流参数 ===
	// StaticRateLimitQPS: 固定限流的 QPS 上限
	//   - 较低值(20-30)：严格限流，大量请求被拒绝
	//   - 较高值(60-100)：宽松限流，更多请求放行但可能导致后端过载
	StaticRateLimitQPS *float64
	// StaticBurstSize: 令牌桶突发容量
	StaticBurstSize *int
}

// ApplyTo 将对照组参数覆盖到 TestConfig 中
func (ag *AblationGroup) ApplyTo(cfg *TestConfig) {
	if ag.MaxServerConcurrency != nil {
		cfg.MaxServerConcurrency = *ag.MaxServerConcurrency
	}
	if ag.OverloadLatencyScale != nil {
		cfg.OverloadLatencyScale = *ag.OverloadLatencyScale
	}
	if ag.PriceStep != nil {
		cfg.RajomonPriceStep = *ag.PriceStep
	}
	if ag.LatencyThreshold != nil {
		cfg.RajomonLatencyThreshold = *ag.LatencyThreshold
	}
	if ag.PriceUpdateRate != nil {
		cfg.RajomonPriceUpdateRate = *ag.PriceUpdateRate
	}
	if ag.PriceStrategy != nil {
		cfg.RajomonPriceStrategy = *ag.PriceStrategy
	}
	if ag.PriceAggregation != nil {
		cfg.RajomonPriceAggregation = *ag.PriceAggregation
	}
	if ag.StaticRateLimitQPS != nil {
		cfg.StaticRateLimitQPS = *ag.StaticRateLimitQPS
	}
	if ag.StaticBurstSize != nil {
		cfg.StaticBurstSize = *ag.StaticBurstSize
	}
	if ag.Budgets != nil {
		cfg.Budgets = ag.Budgets
	}
	if ag.BudgetWeights != nil {
		cfg.BudgetWeights = ag.BudgetWeights
	}
}

// 辅助函数：创建指针（Go 不能直接取字面量地址）
func intPtr(v int) *int                          { return &v }
func int64Ptr(v int64) *int64                    { return &v }
func float64Ptr(v float64) *float64              { return &v }
func durationPtr(v time.Duration) *time.Duration { return &v }
func stringPtr(v string) *string                 { return &v }

// 注: 消融对照组的定义已移至 ablation_config.go
