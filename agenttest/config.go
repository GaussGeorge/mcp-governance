// config.go
// Agent 场景测试配置
// 定义 Agent 行为模型参数、工具类型、预算分布、优先级等
package main

import "time"

// ==================== 策略类型 ====================

type StrategyType string

const (
	StrategyNoGovernance    StrategyType = "no_governance"
	StrategyStaticRateLimit StrategyType = "static_rate_limit"
	StrategyRajomon         StrategyType = "rajomon"
)

// ==================== 优先级 ====================

type Priority string

const (
	PriorityHigh   Priority = "high"
	PriorityMedium Priority = "medium"
	PriorityLow    Priority = "low"
)

// ==================== 工具类型定义 ====================

// ToolTypeDef 定义一种工具类型的模拟参数
type ToolTypeDef struct {
	Name        string        // 工具名称
	Delay       time.Duration // 模拟处理延迟
	DelayVar    time.Duration // 延迟随机波动
	TokenCost   int           // Token 消耗
	Description string        // 说明
}

// DefaultToolTypes 返回默认的三种工具类型
func DefaultToolTypes() []ToolTypeDef {
	return []ToolTypeDef{
		{
			Name:        "simple_query",
			Delay:       50 * time.Millisecond,
			DelayVar:    20 * time.Millisecond,
			TokenCost:   5,
			Description: "简单查询",
		},
		{
			Name:        "calculation",
			Delay:       100 * time.Millisecond,
			DelayVar:    30 * time.Millisecond,
			TokenCost:   10,
			Description: "复杂计算",
		},
		{
			Name:        "image_gen",
			Delay:       500 * time.Millisecond,
			DelayVar:    100 * time.Millisecond,
			TokenCost:   50,
			Description: "高成本图片生成",
		},
	}
}

// ==================== 负载模式 ====================

type LoadPattern string

const (
	PatternBurst   LoadPattern = "burst"   // 突发负载：短时间内任务到达率激增
	PatternPoisson LoadPattern = "poisson" // 泊松负载：固定平均率的随机到达
	PatternSine    LoadPattern = "sine"    // 正弦负载：周期性波动
)

// ==================== 突发负载阶段 ====================

// BurstPhase 突发负载中的一个阶段
type BurstPhase struct {
	Name     string        // 阶段名称
	Duration time.Duration // 持续时间
	TaskRate float64       // 任务到达率（任务/秒）
}

// ==================== 测试配置 ====================

// AgentTestConfig Agent 场景测试总配置
type AgentTestConfig struct {
	// 基本配置
	Strategy    StrategyType // 治理策略
	LoadPattern LoadPattern  // 负载模式
	Duration    time.Duration

	// 服务端配置
	ServerAddr           string
	MaxServerConcurrency int     // 服务端最大并发处理数
	OverloadLatencyScale float64 // 过载延迟放大倍数

	// 工具类型配置
	ToolTypes []ToolTypeDef // 工具类型列表

	// Agent 配置
	NumAgents        int       // Agent 总数
	Budgets          []int     // 预算选项
	BudgetWeights    []float64 // 预算权重（与 Budgets 一一对应）
	Priorities       []Priority
	PriorityWeights  []float64     // 优先级权重
	MinSteps         int           // 任务最小步骤数
	MaxSteps         int           // 任务最大步骤数
	StepThinkTimeMin time.Duration // 步骤间最小思考时间
	StepThinkTimeMax time.Duration // 步骤间最大思考时间

	// 突发负载配置
	BurstPhases []BurstPhase

	// 泊松配置
	PoissonTaskRate float64 // 平均任务到达率（任务/秒）

	// 正弦波配置
	SineBaseRate  float64       // 基础任务到达率
	SineAmplitude float64       // 振幅
	SinePeriod    time.Duration // 周期

	// 静态限流配置
	StaticRateLimitQPS float64
	StaticBurstSize    int

	// Rajomon 动态定价配置
	RajomonPriceStep        int64
	RajomonLatencyThreshold time.Duration
	RajomonPriceUpdateRate  time.Duration
	RajomonPriceAggregation string
	RajomonInitPrice        int64
	RajomonPriceStrategy    string

	// 输出配置
	OutputDir string
	RunID     string
	Verbose   bool

	// 随机种子
	RandomSeed int64
}

// DefaultAgentTestConfig 返回默认配置
func DefaultAgentTestConfig() *AgentTestConfig {
	return &AgentTestConfig{
		Strategy:    StrategyNoGovernance,
		LoadPattern: PatternBurst,
		Duration:    3 * time.Minute,

		ServerAddr:           "127.0.0.1",
		MaxServerConcurrency: 50,
		OverloadLatencyScale: 8.0,

		ToolTypes: DefaultToolTypes(),

		NumAgents:        100,
		Budgets:          []int{10, 30, 100},
		BudgetWeights:    []float64{0.20, 0.30, 0.50},
		Priorities:       []Priority{PriorityHigh, PriorityMedium, PriorityLow},
		PriorityWeights:  []float64{0.20, 0.30, 0.50},
		MinSteps:         1,
		MaxSteps:         5,
		StepThinkTimeMin: 0,
		StepThinkTimeMax: 200 * time.Millisecond,

		// 突发负载：低 → 突发 → 高持续 → 恢复
		BurstPhases: []BurstPhase{
			{Name: "warmup", Duration: 20 * time.Second, TaskRate: 2},
			{Name: "normal", Duration: 30 * time.Second, TaskRate: 5},
			{Name: "burst", Duration: 30 * time.Second, TaskRate: 30},
			{Name: "overload", Duration: 40 * time.Second, TaskRate: 50},
			{Name: "recovery", Duration: 20 * time.Second, TaskRate: 3},
		},

		PoissonTaskRate: 10,

		SineBaseRate:  10,
		SineAmplitude: 15,
		SinePeriod:    1 * time.Minute,

		StaticRateLimitQPS: 30.0,
		StaticBurstSize:    30,

		RajomonPriceStep:        5,
		RajomonLatencyThreshold: 2000 * time.Microsecond,
		RajomonPriceUpdateRate:  100 * time.Millisecond,
		RajomonPriceAggregation: "maximal",
		RajomonInitPrice:        0,
		RajomonPriceStrategy:    "step",

		OutputDir:  "output",
		RunID:      "",
		Verbose:    false,
		RandomSeed: 42,
	}
}

// GetServerPort 根据策略返回端口
func GetServerPort(strategy StrategyType) int {
	switch strategy {
	case StrategyNoGovernance:
		return 9101
	case StrategyStaticRateLimit:
		return 9102
	case StrategyRajomon:
		return 9103
	default:
		return 9101
	}
}

// 辅助指针函数
func intPtr(v int) *int                          { return &v }
func int64Ptr(v int64) *int64                    { return &v }
func float64Ptr(v float64) *float64              { return &v }
func durationPtr(v time.Duration) *time.Duration { return &v }
func stringPtr(v string) *string                 { return &v }
