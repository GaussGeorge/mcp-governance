package main

import (
	"fmt"
	"time"
)

// ==================== 策略与负载模式枚举 ====================

type StrategyType string

const (
	StrategyNoGovernance    StrategyType = "no_governance"
	StrategyStaticRateLimit StrategyType = "static_rate_limit"
	StrategyRajomon         StrategyType = "rajomon"
)

type LoadPattern string

const (
	PatternStep    LoadPattern = "step"
	PatternSine    LoadPattern = "sine"
	PatternPoisson LoadPattern = "poisson"
)

func AllLoadPatterns() []LoadPattern {
	return []LoadPattern{PatternStep, PatternSine, PatternPoisson}
}

// StepPhase 阶梯负载中的一个阶段
type StepPhase struct {
	Name        string
	Duration    time.Duration
	Concurrency int
}

// ==================== 测试配置 ====================

// TestConfig 集成测试的完整配置
type TestConfig struct {
	Strategy    StrategyType
	LoadPattern LoadPattern
	OutputDir   string
	Verbose     bool
	RandomSeed  int64

	MCPBridgeURL string
	ProxyPort    int
	ToolName     string

	ToolArguments map[string]interface{}

	Duration         time.Duration
	RequestInterval  time.Duration
	RejectionBackoff time.Duration

	Budgets       []int
	BudgetWeights []float64

	StepPhases []StepPhase

	SineBase      int
	SineAmplitude int
	SinePeriod    time.Duration

	PoissonQPS float64

	RajomonPriceStep        int64
	RajomonLatencyThreshold time.Duration
	RajomonPriceUpdateRate  time.Duration
	RajomonPriceStrategy    string
	RajomonPriceAggregation string

	StaticRateLimitQPS float64
	StaticBurstSize    int

	HTTPClientTimeout  time.Duration
	HTTPMaxConnections int
}

// DefaultTestConfig 返回经过调优的默认测试配置
func DefaultTestConfig() *TestConfig {
	return &TestConfig{
		Strategy:    StrategyRajomon,
		LoadPattern: PatternStep,
		OutputDir:   "integration/output",
		Verbose:     false,
		RandomSeed:  42,

		MCPBridgeURL: "http://localhost:9000",
		ProxyPort:    8080,
		ToolName:     "calculate",

		ToolArguments: map[string]interface{}{
			"expression": "2 + 3 * 4 - 1",
		},

		Duration:         5 * time.Minute,
		RequestInterval:  10 * time.Millisecond,
		RejectionBackoff: 100 * time.Millisecond,

		Budgets:       []int{10, 50, 100},
		BudgetWeights: []float64{0.4, 0.35, 0.25},

		StepPhases: []StepPhase{
			{Name: "warmup", Duration: 30 * time.Second, Concurrency: 5},
			{Name: "low", Duration: 60 * time.Second, Concurrency: 15},
			{Name: "medium", Duration: 60 * time.Second, Concurrency: 40},
			{Name: "high", Duration: 60 * time.Second, Concurrency: 80},
			{Name: "overload", Duration: 60 * time.Second, Concurrency: 150},
			{Name: "recovery", Duration: 60 * time.Second, Concurrency: 5},
		},

		SineBase:      30,
		SineAmplitude: 25,
		SinePeriod:    60 * time.Second,

		PoissonQPS: 40.0,

		RajomonPriceStep:        15,
		RajomonLatencyThreshold: 150 * time.Millisecond,
		RajomonPriceUpdateRate:  200 * time.Millisecond,
		RajomonPriceStrategy:    "step",
		RajomonPriceAggregation: "maximal",

		StaticRateLimitQPS: 30.0,
		StaticBurstSize:    10,

		HTTPClientTimeout:  30 * time.Second,
		HTTPMaxConnections: 500,
	}
}

// QuickTestConfig 返回用于快速验证的短时配置
func QuickTestConfig() *TestConfig {
	cfg := DefaultTestConfig()
	cfg.StepPhases = []StepPhase{
		{Name: "warmup", Duration: 5 * time.Second, Concurrency: 3},
		{Name: "low", Duration: 10 * time.Second, Concurrency: 10},
		{Name: "medium", Duration: 10 * time.Second, Concurrency: 30},
		{Name: "high", Duration: 10 * time.Second, Concurrency: 60},
		{Name: "overload", Duration: 10 * time.Second, Concurrency: 120},
		{Name: "recovery", Duration: 10 * time.Second, Concurrency: 5},
	}
	cfg.Duration = 1 * time.Minute
	return cfg
}

// ProxyURL 返回 Go 代理服务器的完整 URL
func (cfg *TestConfig) ProxyURL() string {
	return fmt.Sprintf("http://localhost:%d", cfg.ProxyPort)
}
