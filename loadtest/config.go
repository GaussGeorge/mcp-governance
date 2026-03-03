// config.go
// ==================== 负载测试配置定义 ====================
//
// 【文件在整体测试流程中的位置】
//
//	本文件是 loadtest 模块的"参数中心"。它定义了：
//	① 各种枚举类型（策略类型、负载模式）
//	② 核心配置结构体 TestConfig（贯穿整个测试生命周期的参数集合）
//	③ 消融实验对照组结构 AblationGroup 和参数覆盖机制
//	④ DefaultTestConfig() 返回经过调优的默认参数（基于消融实验结果）
//
// 【数据流向】
//
//	main.go 调用 DefaultTestConfig() 获取默认配置
//	→ 用命令行参数覆盖部分字段
//	→ 传给 runner.go 的 TestRunner
//	→ 进而传给 server.go（启动服务时读取服务端参数）和 loader.go（发压时读取负载参数）
//
// 【与 ablation_config.go 的关系】
//
//	ablation_config.go 中定义了各对照组的 AblationGroup 实例列表。
//	每个 AblationGroup 通过 ApplyTo() 方法将其非 nil 字段覆盖到 TestConfig 上。
package main

import "time"

// ==================== 策略类型 ====================
// 定义本测试框架支持的三种 MCP 服务治理策略。
// 策略类型会影响 server.go 中启动哪种服务器，以及 loader.go 中是否在请求中附加 _meta.tokens。

// StrategyType 治理策略枚举
type StrategyType string

const (
	StrategyNoGovernance    StrategyType = "no_governance"     // 无治理：不做任何流控，作为性能上限/下限基线
	StrategyStaticRateLimit StrategyType = "static_rate_limit" // 固定阈值限流：经典的令牌桶限流，QPS 超过阈值即拒绝
	StrategyRajomon         StrategyType = "rajomon"           // Rajomon 动态定价：根据排队延迟动态调整工具调用价格
)

// ==================== 负载模式 ====================
// 定义负载生成器（loader.go）支持的三种流量曲线形状。
// 不同的负载模式考验算法在不同场景下的适应能力。

// LoadPattern 负载模式枚举
type LoadPattern string

const (
	PatternStep    LoadPattern = "step"    // 阶梯式突发负载：流量分阶段跃迁（低→中→高→过载→恢复），测试系统的突变响应
	PatternSine    LoadPattern = "sine"    // 正弦波动负载：流量呈周期性平滑变化，测试算法对连续变化的跟踪能力
	PatternPoisson LoadPattern = "poisson" // 泊松到达负载：请求间隔服从指数分布（符合真实互联网流量特征），测试随机性鲁棒性
)

// ==================== 阶梯式负载阶段 ====================
// 阶梯式负载（PatternStep）由若干阶段（Phase）组成，每个阶段有固定的并发数和持续时间。
// 阶段之间并发数突变，模拟"突然涌入的流量"。

// StepPhase 阶梯式负载中的一个阶段
type StepPhase struct {
	Name        string        // 阶段名称 (如 "warmup", "low", "overload")，会出现在 CSV 的 phase 列中
	Duration    time.Duration // 持续时间（如 60s），阶段结束后立即切换到下一阶段
	Concurrency int           // 并发 worker 数量（每个 worker 是一个独立协程，持续发送请求）
}

// ==================== 测试配置（核心结构体） ====================
// TestConfig 贯穿整个测试生命周期，被 runner、server、loader、metrics 等模块引用。
// 参数设计原则：每个参数都提供合理的默认值（见 DefaultTestConfig），用户只需覆盖关心的部分。

// TestConfig 负载测试总配置
type TestConfig struct {
	// --- 基本配置 ---
	Strategy    StrategyType  // 当前运行使用的治理策略（由 runner 在运行时设置）
	LoadPattern LoadPattern   // 当前运行使用的负载模式
	Duration    time.Duration // 测试总时长（仅对 sine/poisson 模式有效，step 模式由各阶段时长决定）

	// --- 服务端配置（影响 server.go 中的 Mock 工具行为） ---
	ServerAddr           string        // 服务端监听地址（默认 127.0.0.1，仅本机访问）
	MockDelay            time.Duration // Mock 工具处理基础延迟（模拟真实工具的计算时间）
	MockDelayVar         time.Duration // Mock 工具处理延迟随机波动范围（增加真实感）
	MaxServerConcurrency int           // 服务端最大并发处理数（模拟物理资源上限，超过即返回错误）
	OverloadLatencyScale float64       // 过载时延迟放大倍数（超过70%容量时，额外延迟 = 基础延迟 × scale × 过载因子²）

	// --- 负载配置（影响 loader.go 中的请求构造） ---
	Budgets       []int     // 客户端预算列表：每次发请求时随机从中选取一个作为 _meta.tokens 值
	BudgetWeights []float64 // 各预算的选择权重（与 Budgets 一一对应，如 [0.3, 0.4, 0.3]，为空则均匀分布）
	ToolName      string    // 测试用工具名称（对应 server.go 中注册的 Mock 工具名）

	// --- 阶梯式负载配置（PatternStep 专用） ---
	StepPhases []StepPhase // 阶梯式负载的各阶段定义（按顺序执行）

	// --- 正弦波负载配置（PatternSine 专用） ---
	SineBase      int           // 正弦波基础并发数（波形的纵向偏移量）
	SineAmplitude int           // 正弦波振幅（并发数在 [Base-Amp, Base+Amp] 之间波动）
	SinePeriod    time.Duration // 正弦波周期（一个完整波形的时间长度）

	// --- 泊松分布配置（PatternPoisson 专用） ---
	PoissonQPS float64 // 泊松到达的平均 QPS（λ值，请求间隔 = ExpDistribution(1/λ)）

	// --- 静态限流配置（StrategyStaticRateLimit 专用） ---
	StaticRateLimitQPS float64 // 固定阈值限流的 QPS 上限
	StaticBurstSize    int     // 令牌桶的突发容量（允许瞬间通过的最大请求数）

	// --- Rajomon 动态定价配置（StrategyRajomon 专用） ---
	RajomonPriceStep        int64         // 价格调整步长：每次涨价/降价的幅度
	RajomonLatencyThreshold time.Duration // 延迟阈值：排队延迟超过此值时触发涨价
	RajomonPriceUpdateRate  time.Duration // 价格更新频率：多久重新计算一次价格
	RajomonPriceAggregation string        // 价格聚合策略："maximal"(取最大) / "additive"(累加) / "mean"(平均)
	RajomonInitPrice        int64         // 初始价格（系统启动时的工具调用价格）
	RajomonPriceStrategy    string        // 价格策略："step"(线性步进) / "expdecay"(指数衰减)

	// --- 客户端退避配置（影响 loader.go 中 worker 的重试行为） ---
	RejectionBackoff time.Duration // 请求被拒绝后的退避等待时间（避免瞬间重试导致雪崩）
	RequestInterval  time.Duration // 每次请求之间的固定间隔（控制单个 worker 的发送速率）

	// --- 输出配置 ---
	OutputDir string // CSV 文件输出目录
	RunID     string // 本次运行标识（用于区分多次运行的输出文件）
	Verbose   bool   // 是否输出详细日志（会打开 Rajomon 引擎的 debug 模式）

	// --- 随机种子 ---
	RandomSeed int64 // 固定随机种子，确保实验可复现（对负载模式和预算选择生效）
}

// DefaultTestConfig 返回经过调优的默认测试配置
// 其中 Rajomon 参数（priceStep=5, threshold=2000µs）基于消融实验的最优结果选取，
// 能在吞吐量（~215 RPS）和公平性（高预算成功率 74%）之间取得最佳平衡。
func DefaultTestConfig() *TestConfig {
	return &TestConfig{
		Strategy:    StrategyNoGovernance,
		LoadPattern: PatternStep,
		Duration:    5 * time.Minute,

		ServerAddr:           "127.0.0.1",
		MockDelay:            20 * time.Millisecond, // 模拟工具处理耗时 20ms（接近真实轻量级 API 延迟）
		MockDelayVar:         30 * time.Millisecond, // 随机波动 ±30ms（总延迟在 20~50ms 之间）
		MaxServerConcurrency: 50,                    // 模拟后端最大并发容量（超过50个并行请求即过载崩溃）
		OverloadLatencyScale: 8.0,                   // 过载时延迟放大到8倍（模拟CPU/内存资源竞争导致的性能恶化）

		Budgets:  []int{10, 50, 100}, // 三种客户端预算：穷(10) / 正常(50) / 富(100)
		ToolName: "mock_tool",        // Mock 工具名，server.go 中注册同名处理器

		// 默认阶梯式负载：6 个阶段，模拟从空闲到过载再恢复的完整生命周期
		// 并发数的最大值 (200) 远超 MaxServerConcurrency (50)，确保能触发过载
		StepPhases: []StepPhase{
			{Name: "warmup", Duration: 30 * time.Second, Concurrency: 5},     // 预热阶段：让系统稳定
			{Name: "low", Duration: 60 * time.Second, Concurrency: 20},       // 低负载：正常运行
			{Name: "medium", Duration: 60 * time.Second, Concurrency: 50},    // 中负载：接近容量
			{Name: "high", Duration: 60 * time.Second, Concurrency: 100},     // 高负载：超出容量2倍
			{Name: "overload", Duration: 60 * time.Second, Concurrency: 200}, // 过载阶段：超出容量4倍
			{Name: "recovery", Duration: 60 * time.Second, Concurrency: 10},  // 恢复阶段：观察系统回到正常
		},

		// 正弦波配置：并发在 [30-50, 30+50] = [-20→1, 80] 之间波动，周期 2 分钟
		SineBase:      30,
		SineAmplitude: 50,
		SinePeriod:    2 * time.Minute,

		// 泊松配置：平均 50 QPS（实际每秒请求数围绕 50 随机波动）
		PoissonQPS: 50,

		// 静态限流配置（StrategyStaticRateLimit 使用）
		StaticRateLimitQPS: 30.0, // 超过 30 QPS 的请求直接被令牌桶拒绝
		StaticBurstSize:    30,   // 突发容量 30（允许瞬间放行 30 个排队请求）

		// Rajomon 配置（基于消融实验 rajomon_relaxed 组的最优参数）
		// 这组参数在消融实验中表现最佳：
		// priceStep=5 + threshold=2000µs 实现了最高吞吐(215 RPS)和最佳公平性(Budget100=74%)
		RajomonPriceStep:        int64(5),                // 每次涨/降价 5 个单位（较保守，避免价格震荡）
		RajomonLatencyThreshold: 2000 * time.Microsecond, // 排队延迟超过 2ms 才触发涨价（宽松阈值，最大化吞吐）
		RajomonPriceUpdateRate:  100 * time.Millisecond,  // 每 100ms 重新评估一次价格
		RajomonPriceAggregation: "maximal",               // 取自身与下游价格的最大值（适用于并行工具调用）
		RajomonInitPrice:        0,                       // 初始价格为 0（系统空闲时不收费）
		RajomonPriceStrategy:    "step",                  // 线性步进定价（简单直观）

		// 客户端退避（影响 loader.go 中 worker 的行为）
		RejectionBackoff: 100 * time.Millisecond, // 被拒绝后等待 100ms 再重试（模拟真实客户端的退避策略）
		RequestInterval:  10 * time.Millisecond,  // 正常请求间固定间隔 10ms（单 worker 约 100 QPS）

		// 输出
		OutputDir: "output",
		RunID:     "",
		Verbose:   false,

		// 随机种子
		RandomSeed: 42,
	}
}

// GetServerPort 根据策略类型返回不同的端口号
// 三种策略使用不同端口，避免测试间端口冲突。
// 如果端口被占用，server.go 中的 startHTTPServer 会自动切换到随机端口。
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
// 消融实验的核心数据结构。每个 AblationGroup 代表一组特定的参数配置。
// 设计上使用指针类型（*int, *float64 等），这样非 nil 的字段表示"要覆盖"，
// 而 nil 字段表示"保持 DefaultTestConfig 的默认值"，实现了优雅的"单因素对照"。

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

// ApplyTo 将对照组中非 nil 的参数覆盖到 TestConfig 中
// 这是消融实验的核心机制：从一份默认配置出发，只修改当前对照组关心的参数，
// 保证了"控制变量法"的实验设计要求。
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

// 辅助函数：创建指针（Go 语言不能直接取字面量的地址，如 &5 是非法的）
// 这些函数使得 AblationGroup 的字段赋值可以写成 PriceStep: int64Ptr(5) 的简洁形式
func intPtr(v int) *int                          { return &v }
func int64Ptr(v int64) *int64                    { return &v }
func float64Ptr(v float64) *float64              { return &v }
func durationPtr(v time.Duration) *time.Duration { return &v }
func stringPtr(v string) *string                 { return &v }

// 注: 消融对照组的定义已移至 ablation_config.go，本文件只定义结构体和辅助函数
