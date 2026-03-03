// metrics.go
// 指标计算与统计分析模块
//
// 本文件负责将 loader.go 产出的原始 RequestResult 切片转换为可用于对比和导出的统计指标。
// 计算分为三个层次：
//  1. 基础性能指标：吞吐量(RPS)、延迟百分位(P50/P95/P99)、错误率、拒绝率
//  2. 公平性指标：按预算分组的成功率（Rajomon 特有的评估维度）
//  3. 阶段指标：仅在 step 负载模式下，分阶段统计上述指标
//
// 数据流向：
//
//	[]RequestResult → CalculateMetrics() → MetricsSummary
//	MetricsSummary   → PrintSummary()         → 终端报告
//	[]MetricsSummary → PrintComparisonTable() → 多策略对比表
package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// MetricsSummary 一次测试运行的汇总指标（核心数据结构）
// 用于：
//   - 终端报告打印（PrintSummary / PrintComparisonTable）
//   - CSV 导出（WriteSummaryToCSV / WriteAblationToCSV）
//   - 作为 runner.go 中 RunSingleStrategy 的返回值
type MetricsSummary struct {
	Strategy StrategyType // 测试策略（no_governance / static_rate_limit / rajomon）
	Pattern  LoadPattern  // 负载模式（step / sine / poisson）
	RunIndex int          // 运行序号（同策略多次运行时区分）

	// 基础计数统计
	TotalRequests   int64   // 总请求数 = 成功 + 拒绝 + 错误
	SuccessCount    int64   // 成功请求数（HTTP 200 且无错误）
	ErrorCount      int64   // 错误请求数（网络错误、服务器内部错误等）
	RejectedCount   int64   // 被拒绝的请求数（过载/限流/价格超预算）
	DurationSeconds float64 // 测试实际持续时间（秒，由首尾请求时间戳计算）

	// 性能指标
	ThroughputRPS   float64 // 吞吐量：每秒成功请求数 = SuccessCount / DurationSeconds
	ErrorRate       float64 // 错误率 = ErrorCount / TotalRequests（0.0~1.0）
	RejectionRate   float64 // 拒绝率 = RejectedCount / TotalRequests（0.0~1.0）
	AvgLatencyMs    float64 // 平均延迟（毫秒）
	P50LatencyMs    float64 // P50 延迟（中位数）
	P95LatencyMs    float64 // P95 延迟（尾部延迟的主要指标）
	P99LatencyMs    float64 // P99 延迟（极端情况）
	MaxLatencyMs    float64 // 最大延迟
	MinLatencyMs    float64 // 最小延迟
	LatencyStddevMs float64 // 延迟标准差（衡量延迟稳定性）

	// 公平性指标：按 Token 预算分组的成功率
	// key = 客户端预算值（如 10/50/100），value = 该预算组的成功率
	// Rajomon 策略的核心评估维度：预算高的客户应该有更高成功率
	BudgetSuccessRate map[int]float64 // budget → success_rate

	// 阶段指标（仅 step 负载模式有效）
	// key = 阶段名称（warmup/low/medium/high/overload/recovery）
	PhaseMetrics map[string]*PhaseMetricsSummary
}

// PhaseMetricsSummary 单阶段的汇总指标（仅用于 step 负载模式）
// 在 step 负载模式下，每个阶段（warmup/low/medium/high/overload/recovery）
// 的并发度不同，分阶段统计可以观察服务器在不同压力下的行为变化
type PhaseMetricsSummary struct {
	PhaseName     string  // 阶段名称（warmup/low/medium/high/overload/recovery）
	TotalRequests int64   // 本阶段总请求数
	SuccessCount  int64   // 本阶段成功数
	RejectedCount int64   // 本阶段被拒绝数
	AvgLatencyMs  float64 // 本阶段平均延迟 (ms)
	P95LatencyMs  float64 // 本阶段 P95 延迟
	P99LatencyMs  float64 // 本阶段 P99 延迟
	ThroughputRPS float64 // 本阶段吞吐量
	ErrorRate     float64 // 本阶段错误率
	RejectionRate float64 // 本阶段拒绝率
}

// CalculateMetrics 从原始请求结果计算汇总指标（核心计算函数）
// 计算流程：
//  1. 遍历所有 results，统计成功/拒绝/错误计数，收集延迟数据
//  2. 按预算分组统计成功率（公平性分析）
//  3. 按阶段分组统计（step 模式专属）
//  4. 计算百分位数、标准差等派生指标
//
// 【参数】results 为 LoadGenerator.Run() 的输出，其余参数用于标记当前运行的上下文
func CalculateMetrics(results []RequestResult, strategy StrategyType, pattern LoadPattern, runIndex int) MetricsSummary {
	summary := MetricsSummary{
		Strategy:          strategy,
		Pattern:           pattern,
		RunIndex:          runIndex,
		BudgetSuccessRate: make(map[int]float64),
		PhaseMetrics:      make(map[string]*PhaseMetricsSummary),
	}

	if len(results) == 0 {
		return summary
	}

	summary.TotalRequests = int64(len(results))

	// 收集延迟数据和各类计数
	var latencies []float64              // 所有有效请求的延迟值（排除网络错误）
	budgetTotal := make(map[int]int64)   // 各预算组总请求数
	budgetSuccess := make(map[int]int64) // 各预算组成功请求数

	// 按阶段分组的数据（step 负载模式专属，其他模式下 Phase 为空）
	phaseResults := make(map[string][]RequestResult)

	var minTS, maxTS int64

	for i, r := range results {
		// 时间范围
		if i == 0 || r.Timestamp < minTS {
			minTS = r.Timestamp
		}
		if i == 0 || r.Timestamp > maxTS {
			maxTS = r.Timestamp
		}

		// 成功/失败/拒绝计数
		if r.IsSuccess() {
			summary.SuccessCount++
		} else if r.Rejected {
			summary.RejectedCount++
		} else {
			summary.ErrorCount++
		}

		// 延迟数据（仅统计非网络错误的请求，StatusCode==-1 表示连接失败）
		if r.StatusCode != -1 {
			latencies = append(latencies, float64(r.LatencyMs))
		}

		// 预算组统计
		budgetTotal[r.ClientBudget]++
		if r.IsSuccess() {
			budgetSuccess[r.ClientBudget]++
		}

		// 阶段分组
		if r.Phase != "" {
			phaseResults[r.Phase] = append(phaseResults[r.Phase], r)
		}
	}

	// 计算测试实际持续时间（从第一个请求到最后一个请求的时间差）
	summary.DurationSeconds = float64(maxTS-minTS) / 1000.0
	if summary.DurationSeconds <= 0 {
		summary.DurationSeconds = 1
	}

	// 计算吞吐量
	summary.ThroughputRPS = float64(summary.SuccessCount) / summary.DurationSeconds

	// 计算错误率和拒绝率
	if summary.TotalRequests > 0 {
		summary.ErrorRate = float64(summary.ErrorCount) / float64(summary.TotalRequests)
		summary.RejectionRate = float64(summary.RejectedCount) / float64(summary.TotalRequests)
	}

	// 计算延迟统计（需先排序以便计算百分位数）
	if len(latencies) > 0 {
		sort.Float64s(latencies)
		summary.MinLatencyMs = latencies[0]
		summary.MaxLatencyMs = latencies[len(latencies)-1]
		summary.AvgLatencyMs = mean(latencies)
		summary.P50LatencyMs = percentile(latencies, 50)
		summary.P95LatencyMs = percentile(latencies, 95)
		summary.P99LatencyMs = percentile(latencies, 99)
		summary.LatencyStddevMs = stddev(latencies)
	}

	// 计算各预算组成功率（公平性核心指标）
	// Rajomon 的设计目标：预算高的客户端应获得更高成功率
	for budget, total := range budgetTotal {
		if total > 0 {
			summary.BudgetSuccessRate[budget] = float64(budgetSuccess[budget]) / float64(total)
		}
	}

	// 计算各阶段指标
	for phase, pResults := range phaseResults {
		summary.PhaseMetrics[phase] = calculatePhaseMetrics(phase, pResults)
	}

	return summary
}

// calculatePhaseMetrics 计算单阶段指标（step 模式专属）
// 计算逻辑与 CalculateMetrics 类似，但范围缩小到单个阶段的 results
// 用于观察服务器在不同并发度下的行为变化（如 overload 阶段的拒绝率飙升）
func calculatePhaseMetrics(phase string, results []RequestResult) *PhaseMetricsSummary {
	pm := &PhaseMetricsSummary{
		PhaseName:     phase,
		TotalRequests: int64(len(results)),
	}

	if len(results) == 0 {
		return pm
	}

	var latencies []float64
	var minTS, maxTS int64

	for i, r := range results {
		if i == 0 || r.Timestamp < minTS {
			minTS = r.Timestamp
		}
		if i == 0 || r.Timestamp > maxTS {
			maxTS = r.Timestamp
		}

		if r.IsSuccess() {
			pm.SuccessCount++
		}
		if r.Rejected {
			pm.RejectedCount++
		}
		if r.StatusCode != -1 {
			latencies = append(latencies, float64(r.LatencyMs))
		}
	}

	durationSec := float64(maxTS-minTS) / 1000.0
	if durationSec <= 0 {
		durationSec = 1
	}

	pm.ThroughputRPS = float64(pm.SuccessCount) / durationSec

	if pm.TotalRequests > 0 {
		pm.ErrorRate = float64(pm.TotalRequests-pm.SuccessCount-pm.RejectedCount) / float64(pm.TotalRequests)
		pm.RejectionRate = float64(pm.RejectedCount) / float64(pm.TotalRequests)
	}

	if len(latencies) > 0 {
		sort.Float64s(latencies)
		pm.AvgLatencyMs = mean(latencies)
		pm.P95LatencyMs = percentile(latencies, 95)
		pm.P99LatencyMs = percentile(latencies, 99)
	}

	return pm
}

// ==================== 统计辅助函数 ====================
// 提供基础的描述性统计计算，输入均为已排序或未排序的 float64 切片

// mean 计算算术平均值
func mean(data []float64) float64 {
	if len(data) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range data {
		sum += v
	}
	return sum / float64(len(data))
}

// stddev 计算样本标准差（无偏估计，分母为 n-1）
// 用于衡量延迟的稳定性，标准差越小说明响应时间越稳定
func stddev(data []float64) float64 {
	if len(data) <= 1 {
		return 0
	}
	m := mean(data)
	sumSq := 0.0
	for _, v := range data {
		diff := v - m
		sumSq += diff * diff
	}
	return math.Sqrt(sumSq / float64(len(data)-1))
}

// percentile 计算百分位数（线性插值法）
// 【前置条件】sortedData 必须已按升序排列
// 【参数】p 为百分位数值（0~100），如 p=95 表示 P95
// 【算法】当计算位置落在两个数据点之间时，按比例做线性插值
func percentile(sortedData []float64, p float64) float64 {
	if len(sortedData) == 0 {
		return 0
	}
	if p <= 0 {
		return sortedData[0]
	}
	if p >= 100 {
		return sortedData[len(sortedData)-1]
	}

	rank := p / 100.0 * float64(len(sortedData)-1)
	lower := int(math.Floor(rank))
	upper := lower + 1
	if upper >= len(sortedData) {
		return sortedData[len(sortedData)-1]
	}

	weight := rank - float64(lower)
	return sortedData[lower]*(1-weight) + sortedData[upper]*weight
}

// ==================== 报告输出 ====================
// 将统计结果格式化打印到终端，方便实时观察测试进展

// PrintSummary 打印单次运行的详细汇总报告
// 包含：基础计数 → 性能指标 → 延迟分布 → 公平性（按预算分组） → 分阶段指标
func PrintSummary(summary MetricsSummary) {
	sep := strings.Repeat("=", 60)
	fmt.Println(sep)
	fmt.Printf("  策略: %s | 负载模式: %s | 运行 #%d\n", summary.Strategy, summary.Pattern, summary.RunIndex)
	fmt.Println(sep)

	fmt.Printf("  总请求数:     %d\n", summary.TotalRequests)
	fmt.Printf("  成功请求数:   %d\n", summary.SuccessCount)
	fmt.Printf("  错误请求数:   %d\n", summary.ErrorCount)
	fmt.Printf("  拒绝请求数:   %d\n", summary.RejectedCount)
	fmt.Printf("  测试时长:     %.2f 秒\n", summary.DurationSeconds)
	fmt.Println()

	fmt.Printf("  吞吐量 (RPS):  %.2f\n", summary.ThroughputRPS)
	fmt.Printf("  错误率:        %.4f (%.2f%%)\n", summary.ErrorRate, summary.ErrorRate*100)
	fmt.Printf("  拒绝率:        %.4f (%.2f%%)\n", summary.RejectionRate, summary.RejectionRate*100)
	fmt.Println()

	fmt.Printf("  延迟统计 (ms):\n")
	fmt.Printf("    平均:   %.2f\n", summary.AvgLatencyMs)
	fmt.Printf("    P50:    %.2f\n", summary.P50LatencyMs)
	fmt.Printf("    P95:    %.2f\n", summary.P95LatencyMs)
	fmt.Printf("    P99:    %.2f\n", summary.P99LatencyMs)
	fmt.Printf("    最大:   %.2f\n", summary.MaxLatencyMs)
	fmt.Printf("    最小:   %.2f\n", summary.MinLatencyMs)
	fmt.Printf("    标准差: %.2f\n", summary.LatencyStddevMs)
	fmt.Println()

	fmt.Printf("  公平性 (各预算组成功率):\n")
	budgets := make([]int, 0, len(summary.BudgetSuccessRate))
	for b := range summary.BudgetSuccessRate {
		budgets = append(budgets, b)
	}
	sort.Ints(budgets)
	for _, b := range budgets {
		rate := summary.BudgetSuccessRate[b]
		fmt.Printf("    预算 %3d:  %.4f (%.2f%%)\n", b, rate, rate*100)
	}

	// 按阶段打印
	if len(summary.PhaseMetrics) > 0 {
		fmt.Println()
		fmt.Printf("  各阶段指标:\n")
		fmt.Printf("  %-12s %8s %8s %8s %10s %10s %10s\n",
			"阶段", "请求数", "成功数", "拒绝数", "吞吐量", "P95(ms)", "拒绝率")
		fmt.Printf("  %s\n", strings.Repeat("-", 70))

		// 按常见顺序排列阶段
		phaseOrder := []string{"warmup", "low", "medium", "high", "overload", "recovery"}
		for _, pName := range phaseOrder {
			if pm, ok := summary.PhaseMetrics[pName]; ok {
				fmt.Printf("  %-12s %8d %8d %8d %10.2f %10.2f %10.4f\n",
					pm.PhaseName, pm.TotalRequests, pm.SuccessCount, pm.RejectedCount,
					pm.ThroughputRPS, pm.P95LatencyMs, pm.RejectionRate)
			}
		}
	}

	fmt.Println(sep)
}

// PrintComparisonTable 打印多策略对比表（横向对比）
// 上半部分：性能指标对比（吞吐量、延迟、错误率、拒绝率）
// 下半部分：公平性对比（预算10/50/100 三个典型组的成功率）
func PrintComparisonTable(summaries []MetricsSummary) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 100))
	fmt.Println("  三种策略对比汇总")
	fmt.Println(strings.Repeat("=", 100))

	fmt.Printf("  %-20s %10s %10s %10s %10s %10s %10s\n",
		"策略", "吞吐量", "P50(ms)", "P95(ms)", "P99(ms)", "错误率", "拒绝率")
	fmt.Printf("  %s\n", strings.Repeat("-", 90))

	for _, s := range summaries {
		fmt.Printf("  %-20s %10.2f %10.2f %10.2f %10.2f %10.4f %10.4f\n",
			s.Strategy, s.ThroughputRPS,
			s.P50LatencyMs, s.P95LatencyMs, s.P99LatencyMs,
			s.ErrorRate, s.RejectionRate)
	}

	fmt.Println()

	// 公平性对比
	fmt.Printf("  %-20s", "策略")
	budgets := []int{10, 50, 100}
	for _, b := range budgets {
		fmt.Printf(" %12s", fmt.Sprintf("预算%d成功率", b))
	}
	fmt.Println()
	fmt.Printf("  %s\n", strings.Repeat("-", 60))
	for _, s := range summaries {
		fmt.Printf("  %-20s", s.Strategy)
		for _, b := range budgets {
			if rate, ok := s.BudgetSuccessRate[b]; ok {
				fmt.Printf(" %12.4f", rate)
			} else {
				fmt.Printf(" %12s", "N/A")
			}
		}
		fmt.Println()
	}

	fmt.Println(strings.Repeat("=", 100))
}
