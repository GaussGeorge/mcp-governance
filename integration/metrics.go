// metrics.go
// ==================== 指标计算与统计分析 ====================
//
// 与 loadtest/metrics.go 完全相同的指标计算逻辑。
// 将 loader.go 产出的 []RequestResult 转换为 MetricsSummary。
package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// MetricsSummary 一次测试运行的汇总指标
type MetricsSummary struct {
	Strategy StrategyType
	Pattern  LoadPattern
	RunIndex int

	TotalRequests   int64
	SuccessCount    int64
	ErrorCount      int64
	RejectedCount   int64
	DurationSeconds float64

	ThroughputRPS   float64
	ErrorRate       float64
	RejectionRate   float64
	AvgLatencyMs    float64
	P50LatencyMs    float64
	P95LatencyMs    float64
	P99LatencyMs    float64
	MaxLatencyMs    float64
	MinLatencyMs    float64
	LatencyStddevMs float64

	BudgetSuccessRate map[int]float64
	PhaseMetrics      map[string]*PhaseMetricsSummary
}

// PhaseMetricsSummary 单阶段指标
type PhaseMetricsSummary struct {
	PhaseName     string
	TotalRequests int64
	SuccessCount  int64
	RejectedCount int64
	AvgLatencyMs  float64
	P95LatencyMs  float64
	P99LatencyMs  float64
	ThroughputRPS float64
	ErrorRate     float64
	RejectionRate float64
}

// CalculateMetrics 从原始请求结果计算汇总指标
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

	var latencies []float64
	budgetTotal := make(map[int]int64)
	budgetSuccess := make(map[int]int64)
	phaseResults := make(map[string][]RequestResult)

	var minTS, maxTS int64

	for i, r := range results {
		if i == 0 || r.Timestamp < minTS {
			minTS = r.Timestamp
		}
		if i == 0 || r.Timestamp > maxTS {
			maxTS = r.Timestamp
		}

		if r.IsSuccess() {
			summary.SuccessCount++
		} else if r.Rejected {
			summary.RejectedCount++
		} else {
			summary.ErrorCount++
		}

		if r.StatusCode != -1 {
			latencies = append(latencies, float64(r.LatencyMs))
		}

		budgetTotal[r.ClientBudget]++
		if r.IsSuccess() {
			budgetSuccess[r.ClientBudget]++
		}

		if r.Phase != "" {
			phaseResults[r.Phase] = append(phaseResults[r.Phase], r)
		}
	}

	summary.DurationSeconds = float64(maxTS-minTS) / 1000.0
	if summary.DurationSeconds <= 0 {
		summary.DurationSeconds = 1
	}

	summary.ThroughputRPS = float64(summary.SuccessCount) / summary.DurationSeconds

	if summary.TotalRequests > 0 {
		summary.ErrorRate = float64(summary.ErrorCount) / float64(summary.TotalRequests)
		summary.RejectionRate = float64(summary.RejectedCount) / float64(summary.TotalRequests)
	}

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

	for budget, total := range budgetTotal {
		if total > 0 {
			summary.BudgetSuccessRate[budget] = float64(budgetSuccess[budget]) / float64(total)
		}
	}

	for phase, pResults := range phaseResults {
		summary.PhaseMetrics[phase] = calculatePhaseMetrics(phase, pResults)
	}

	return summary
}

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

	if len(summary.PhaseMetrics) > 0 {
		fmt.Println()
		fmt.Printf("  各阶段指标:\n")
		fmt.Printf("  %-12s %8s %8s %8s %10s %10s %10s\n",
			"阶段", "请求数", "成功数", "拒绝数", "吞吐量", "P95(ms)", "拒绝率")
		fmt.Printf("  %s\n", strings.Repeat("-", 70))

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

func PrintComparisonTable(summaries []MetricsSummary) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 100))
	fmt.Println("  三种策略对比汇总 (真实 MCP 服务器集成测试)")
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
