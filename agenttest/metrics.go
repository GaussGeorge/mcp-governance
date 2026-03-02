// metrics.go
// Agent 场景的指标计算与统计分析
// 包含请求级指标和任务级指标
package main

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// AgentMetricsSummary Agent 场景测试的综合指标
type AgentMetricsSummary struct {
	Strategy StrategyType
	Pattern  LoadPattern
	RunIndex int

	// ==================== 任务级指标 ====================
	TotalTasks      int
	SuccessTasks    int
	FailedTasks     int
	TaskSuccessRate float64

	// 按预算组的任务成功率
	BudgetTaskSuccessRate map[int]float64
	// 按优先级的任务成功率
	PriorityTaskSuccessRate map[Priority]float64

	// 任务完成统计
	AvgCompletedSteps float64 // 成功任务的平均步骤数
	AvgTaskDurationMs float64 // 成功任务的平均完成时间
	AvgTaskTokens     float64 // 成功任务的平均 Token 消耗
	P50TaskDurationMs float64
	P95TaskDurationMs float64

	// 失败原因分布
	FailureReasons map[string]int

	// ==================== 请求级指标 ====================
	TotalSteps        int64
	SuccessSteps      int64
	RejectedSteps     int64
	ErrorSteps        int64
	StepSuccessRate   float64
	StepRejectionRate float64

	// 延迟统计
	AvgLatencyMs    float64
	P50LatencyMs    float64
	P95LatencyMs    float64
	P99LatencyMs    float64
	MaxLatencyMs    float64
	MinLatencyMs    float64
	LatencyStddevMs float64

	// 吞吐量
	ThroughputRPS   float64
	DurationSeconds float64

	// 按工具类型的统计
	ToolTypeStats map[string]*ToolTypeMetric

	// 按阶段的任务成功率
	PhaseTaskSuccessRate map[string]float64
}

// ToolTypeMetric 单工具类型的统计
type ToolTypeMetric struct {
	Name          string
	TotalCalls    int64
	SuccessCalls  int64
	RejectedCalls int64
	AvgLatencyMs  float64
	SuccessRate   float64
}

// CalculateAgentMetrics 综合计算 Agent 场景的指标
func CalculateAgentMetrics(stepResults []StepResult, taskSummaries []TaskSummary,
	strategy StrategyType, pattern LoadPattern, runIndex int) AgentMetricsSummary {

	summary := AgentMetricsSummary{
		Strategy:                strategy,
		Pattern:                 pattern,
		RunIndex:                runIndex,
		BudgetTaskSuccessRate:   make(map[int]float64),
		PriorityTaskSuccessRate: make(map[Priority]float64),
		FailureReasons:          make(map[string]int),
		ToolTypeStats:           make(map[string]*ToolTypeMetric),
		PhaseTaskSuccessRate:    make(map[string]float64),
	}

	// ==================== 任务级指标 ====================
	summary.TotalTasks = len(taskSummaries)

	budgetTotal := make(map[int]int)
	budgetSuccess := make(map[int]int)
	priorityTotal := make(map[Priority]int)
	prioritySuccess := make(map[Priority]int)
	phaseTotal := make(map[string]int)
	phaseSuccess := make(map[string]int)

	var successDurations []float64
	var successTokens []float64
	var successStepCounts []float64

	for _, ts := range taskSummaries {
		if ts.TaskSuccess {
			summary.SuccessTasks++
			successDurations = append(successDurations, float64(ts.DurationMs))
			successTokens = append(successTokens, float64(ts.TotalTokens))
			successStepCounts = append(successStepCounts, float64(ts.CompletedSteps))
		} else {
			summary.FailedTasks++
			if ts.FailureReason != "" {
				summary.FailureReasons[ts.FailureReason]++
			}
		}

		// 按预算组
		budgetTotal[ts.InitialBudget]++
		if ts.TaskSuccess {
			budgetSuccess[ts.InitialBudget]++
		}

		// 按优先级
		p := Priority(ts.Priority)
		priorityTotal[p]++
		if ts.TaskSuccess {
			prioritySuccess[p]++
		}

		// 按阶段
		if ts.Phase != "" {
			phaseTotal[ts.Phase]++
			if ts.TaskSuccess {
				phaseSuccess[ts.Phase]++
			}
		}
	}

	if summary.TotalTasks > 0 {
		summary.TaskSuccessRate = float64(summary.SuccessTasks) / float64(summary.TotalTasks)
	}

	// 各预算组成功率
	for budget, total := range budgetTotal {
		if total > 0 {
			summary.BudgetTaskSuccessRate[budget] = float64(budgetSuccess[budget]) / float64(total)
		}
	}

	// 各优先级成功率
	for priority, total := range priorityTotal {
		if total > 0 {
			summary.PriorityTaskSuccessRate[priority] = float64(prioritySuccess[priority]) / float64(total)
		}
	}

	// 各阶段成功率
	for phase, total := range phaseTotal {
		if total > 0 {
			summary.PhaseTaskSuccessRate[phase] = float64(phaseSuccess[phase]) / float64(total)
		}
	}

	// 成功任务统计
	if len(successDurations) > 0 {
		summary.AvgTaskDurationMs = mean(successDurations)
		sort.Float64s(successDurations)
		summary.P50TaskDurationMs = percentile(successDurations, 50)
		summary.P95TaskDurationMs = percentile(successDurations, 95)
	}
	if len(successTokens) > 0 {
		summary.AvgTaskTokens = mean(successTokens)
	}
	if len(successStepCounts) > 0 {
		summary.AvgCompletedSteps = mean(successStepCounts)
	}

	// ==================== 请求级指标 ====================
	summary.TotalSteps = int64(len(stepResults))

	var latencies []float64
	var minTS, maxTS int64

	toolTotal := make(map[string]int64)
	toolSuccess := make(map[string]int64)
	toolRejected := make(map[string]int64)
	toolLatencies := make(map[string][]float64)

	for i, r := range stepResults {
		if i == 0 || r.Timestamp < minTS {
			minTS = r.Timestamp
		}
		if i == 0 || r.Timestamp > maxTS {
			maxTS = r.Timestamp
		}

		if r.IsSuccess() {
			summary.SuccessSteps++
		} else if r.Rejected {
			summary.RejectedSteps++
		} else {
			summary.ErrorSteps++
		}

		if r.StatusCode != -1 && r.StatusCode != -2 {
			latencies = append(latencies, float64(r.LatencyMs))
		}

		// 工具类型统计
		toolTotal[r.ToolType]++
		if r.IsSuccess() {
			toolSuccess[r.ToolType]++
		}
		if r.Rejected {
			toolRejected[r.ToolType]++
		}
		if r.StatusCode != -1 && r.StatusCode != -2 {
			toolLatencies[r.ToolType] = append(toolLatencies[r.ToolType], float64(r.LatencyMs))
		}
	}

	// 时间与吞吐量
	summary.DurationSeconds = float64(maxTS-minTS) / 1000.0
	if summary.DurationSeconds <= 0 {
		summary.DurationSeconds = 1
	}
	summary.ThroughputRPS = float64(summary.SuccessSteps) / summary.DurationSeconds

	// 成功率
	if summary.TotalSteps > 0 {
		summary.StepSuccessRate = float64(summary.SuccessSteps) / float64(summary.TotalSteps)
		summary.StepRejectionRate = float64(summary.RejectedSteps) / float64(summary.TotalSteps)
	}

	// 延迟统计
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

	// 工具类型汇总
	for toolName, total := range toolTotal {
		tm := &ToolTypeMetric{
			Name:          toolName,
			TotalCalls:    total,
			SuccessCalls:  toolSuccess[toolName],
			RejectedCalls: toolRejected[toolName],
		}
		if total > 0 {
			tm.SuccessRate = float64(tm.SuccessCalls) / float64(total)
		}
		if lats := toolLatencies[toolName]; len(lats) > 0 {
			tm.AvgLatencyMs = mean(lats)
		}
		summary.ToolTypeStats[toolName] = tm
	}

	return summary
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

// PrintAgentSummary 打印 Agent 场景汇总报告
func PrintAgentSummary(s AgentMetricsSummary) {
	sep := strings.Repeat("=", 70)
	fmt.Println(sep)
	fmt.Printf("  Agent场景测试: 策略=%s | 负载模式=%s | 运行#%d\n", s.Strategy, s.Pattern, s.RunIndex)
	fmt.Println(sep)

	// 任务级指标
	fmt.Println("\n  ████ 任务级指标 ████")
	fmt.Printf("  总任务数:       %d\n", s.TotalTasks)
	fmt.Printf("  成功任务数:     %d\n", s.SuccessTasks)
	fmt.Printf("  失败任务数:     %d\n", s.FailedTasks)
	fmt.Printf("  任务成功率:     %.4f (%.2f%%)\n", s.TaskSuccessRate, s.TaskSuccessRate*100)
	fmt.Println()

	fmt.Printf("  成功任务统计:\n")
	fmt.Printf("    平均步骤数:   %.2f\n", s.AvgCompletedSteps)
	fmt.Printf("    平均耗时:     %.2f ms\n", s.AvgTaskDurationMs)
	fmt.Printf("    平均Token消耗: %.2f\n", s.AvgTaskTokens)
	fmt.Printf("    P50任务耗时:  %.2f ms\n", s.P50TaskDurationMs)
	fmt.Printf("    P95任务耗时:  %.2f ms\n", s.P95TaskDurationMs)
	fmt.Println()

	// 公平性：按预算组
	fmt.Printf("  按预算组的任务成功率:\n")
	budgets := make([]int, 0, len(s.BudgetTaskSuccessRate))
	for b := range s.BudgetTaskSuccessRate {
		budgets = append(budgets, b)
	}
	sort.Ints(budgets)
	for _, b := range budgets {
		rate := s.BudgetTaskSuccessRate[b]
		fmt.Printf("    预算 %3d:  %.4f (%.2f%%)\n", b, rate, rate*100)
	}
	fmt.Println()

	// 按优先级
	fmt.Printf("  按优先级的任务成功率:\n")
	for _, p := range []Priority{PriorityHigh, PriorityMedium, PriorityLow} {
		if rate, ok := s.PriorityTaskSuccessRate[p]; ok {
			fmt.Printf("    %-8s: %.4f (%.2f%%)\n", p, rate, rate*100)
		}
	}
	fmt.Println()

	// 失败原因
	if len(s.FailureReasons) > 0 {
		fmt.Printf("  失败原因分布:\n")
		for reason, count := range s.FailureReasons {
			fmt.Printf("    %-20s: %d\n", reason, count)
		}
		fmt.Println()
	}

	// 请求级指标
	fmt.Println("  ████ 请求级指标 ████")
	fmt.Printf("  总步骤数:     %d\n", s.TotalSteps)
	fmt.Printf("  成功步骤:     %d\n", s.SuccessSteps)
	fmt.Printf("  拒绝步骤:     %d\n", s.RejectedSteps)
	fmt.Printf("  错误步骤:     %d\n", s.ErrorSteps)
	fmt.Printf("  步骤成功率:   %.4f (%.2f%%)\n", s.StepSuccessRate, s.StepSuccessRate*100)
	fmt.Printf("  步骤拒绝率:   %.4f (%.2f%%)\n", s.StepRejectionRate, s.StepRejectionRate*100)
	fmt.Printf("  吞吐量 (RPS): %.2f\n", s.ThroughputRPS)
	fmt.Printf("  测试时长:     %.2f 秒\n", s.DurationSeconds)
	fmt.Println()

	fmt.Printf("  延迟统计 (ms):\n")
	fmt.Printf("    平均:   %.2f\n", s.AvgLatencyMs)
	fmt.Printf("    P50:    %.2f\n", s.P50LatencyMs)
	fmt.Printf("    P95:    %.2f\n", s.P95LatencyMs)
	fmt.Printf("    P99:    %.2f\n", s.P99LatencyMs)
	fmt.Printf("    最大:   %.2f\n", s.MaxLatencyMs)
	fmt.Printf("    标准差: %.2f\n", s.LatencyStddevMs)
	fmt.Println()

	// 工具类型统计
	if len(s.ToolTypeStats) > 0 {
		fmt.Printf("  工具类型统计:\n")
		fmt.Printf("  %-15s %8s %8s %8s %10s %10s\n",
			"工具", "总调用", "成功", "拒绝", "成功率", "平均延迟")
		fmt.Printf("  %s\n", strings.Repeat("-", 65))
		for _, tm := range s.ToolTypeStats {
			fmt.Printf("  %-15s %8d %8d %8d %10.4f %10.2fms\n",
				tm.Name, tm.TotalCalls, tm.SuccessCalls, tm.RejectedCalls,
				tm.SuccessRate, tm.AvgLatencyMs)
		}
	}

	// 阶段统计
	if len(s.PhaseTaskSuccessRate) > 0 {
		fmt.Println()
		fmt.Printf("  各阶段任务成功率:\n")
		phaseOrder := []string{"warmup", "normal", "burst", "overload", "recovery", "poisson"}
		for _, pName := range phaseOrder {
			if rate, ok := s.PhaseTaskSuccessRate[pName]; ok {
				fmt.Printf("    %-12s: %.4f (%.2f%%)\n", pName, rate, rate*100)
			}
		}
		// 打印剩余的阶段（如正弦波的动态阶段名）
		for pName, rate := range s.PhaseTaskSuccessRate {
			found := false
			for _, p := range phaseOrder {
				if p == pName {
					found = true
					break
				}
			}
			if !found {
				fmt.Printf("    %-12s: %.4f (%.2f%%)\n", pName, rate, rate*100)
			}
		}
	}

	fmt.Println(sep)
}

// PrintAgentComparisonTable 打印三策略的 Agent 场景对比表
func PrintAgentComparisonTable(summaries []AgentMetricsSummary) {
	fmt.Println()
	fmt.Println(strings.Repeat("=", 120))
	fmt.Println("  Agent 场景三策略对比汇总")
	fmt.Println(strings.Repeat("=", 120))

	// 任务级对比
	fmt.Println("\n  ---- 任务级指标 ----")
	fmt.Printf("  %-20s %8s %8s %10s %12s %12s %12s\n",
		"策略", "总任务", "成功率", "平均耗时(ms)", "预算10", "预算30", "预算100")
	fmt.Printf("  %s\n", strings.Repeat("-", 96))

	for _, s := range summaries {
		b10 := s.BudgetTaskSuccessRate[10]
		b30 := s.BudgetTaskSuccessRate[30]
		b100 := s.BudgetTaskSuccessRate[100]
		fmt.Printf("  %-20s %8d %8.4f %12.2f %12.4f %12.4f %12.4f\n",
			s.Strategy, s.TotalTasks, s.TaskSuccessRate, s.AvgTaskDurationMs,
			b10, b30, b100)
	}

	// 优先级对比
	fmt.Println("\n  ---- 优先级调度效果 ----")
	fmt.Printf("  %-20s %12s %12s %12s\n", "策略", "高优先级", "中优先级", "低优先级")
	fmt.Printf("  %s\n", strings.Repeat("-", 60))
	for _, s := range summaries {
		pH := s.PriorityTaskSuccessRate[PriorityHigh]
		pM := s.PriorityTaskSuccessRate[PriorityMedium]
		pL := s.PriorityTaskSuccessRate[PriorityLow]
		fmt.Printf("  %-20s %12.4f %12.4f %12.4f\n", s.Strategy, pH, pM, pL)
	}

	// 请求级对比
	fmt.Println("\n  ---- 请求级指标 ----")
	fmt.Printf("  %-20s %10s %10s %10s %10s %10s\n",
		"策略", "吞吐量", "P50(ms)", "P95(ms)", "步骤成功率", "步骤拒绝率")
	fmt.Printf("  %s\n", strings.Repeat("-", 74))
	for _, s := range summaries {
		fmt.Printf("  %-20s %10.2f %10.2f %10.2f %10.4f %10.4f\n",
			s.Strategy, s.ThroughputRPS, s.P50LatencyMs, s.P95LatencyMs,
			s.StepSuccessRate, s.StepRejectionRate)
	}

	fmt.Println(strings.Repeat("=", 120))
}
