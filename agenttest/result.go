// result.go
// 结果数据类型和 CSV 导出
// 支持请求级（StepResult）和任务级（TaskSummary）的数据导出
package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"
)

// ==================== 任务级汇总 ====================

// TaskSummary 单个任务的汇总结果（后处理生成）
type TaskSummary struct {
	TaskID         string
	AgentID        string
	Priority       string
	InitialBudget  int
	TaskSuccess    bool
	TotalSteps     int
	CompletedSteps int
	TotalTokens    int
	DurationMs     int64 // 任务总耗时
	FailureReason  string
	Phase          string // 任务开始时的阶段
}

// BuildTaskSummaries 从步骤结果中构建任务级汇总
func BuildTaskSummaries(stepResults []StepResult) []TaskSummary {
	// 按 TaskID 分组
	taskSteps := make(map[string][]StepResult)
	for _, r := range stepResults {
		taskSteps[r.TaskID] = append(taskSteps[r.TaskID], r)
	}

	var summaries []TaskSummary

	for taskID, steps := range taskSteps {
		// 按 StepIndex 排序
		sort.Slice(steps, func(i, j int) bool {
			return steps[i].StepIndex < steps[j].StepIndex
		})

		summary := TaskSummary{
			TaskID:     taskID,
			TotalSteps: 0, // 将从步骤推断
		}

		if len(steps) > 0 {
			summary.AgentID = steps[0].AgentID
			summary.Priority = steps[0].Priority
			summary.InitialBudget = steps[0].BudgetBefore
			summary.Phase = steps[0].Phase
		}

		// 统计
		var minTS, maxTS int64
		completedSteps := 0
		totalTokens := 0
		taskSuccess := true
		failReason := ""

		for i, s := range steps {
			if i == 0 || s.Timestamp < minTS {
				minTS = s.Timestamp
			}
			if i == 0 || s.Timestamp > maxTS {
				maxTS = s.Timestamp
			}

			if s.IsSuccess() {
				completedSteps++
				totalTokens += s.TokenUsed
			} else {
				taskSuccess = false
				if s.StatusCode == -2 {
					failReason = "budget_exhausted"
				} else if s.Rejected {
					failReason = "step_rejected"
				} else if s.StatusCode == -1 {
					failReason = "network_error"
				} else {
					failReason = "step_failed"
				}
			}
		}

		summary.TotalSteps = len(steps) // 实际记录的步骤数
		summary.CompletedSteps = completedSteps
		summary.TotalTokens = totalTokens
		summary.TaskSuccess = taskSuccess
		summary.FailureReason = failReason
		summary.DurationMs = maxTS - minTS

		summaries = append(summaries, summary)
	}

	// 按 TaskID 排序
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].TaskID < summaries[j].TaskID
	})

	return summaries
}

// ==================== CSV 导出 ====================

// WriteStepResultsToCSV 将步骤级结果写入 CSV
func WriteStepResultsToCSV(results []StepResult, outputDir string, strategy StrategyType, pattern LoadPattern, runIndex int) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("创建输出目录失败: %w", err)
	}

	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("%s_%s_agent_steps_run%d_%s.csv", strategy, pattern, runIndex, timestamp)
	filePath := filepath.Join(outputDir, filename)

	f, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("创建 CSV 文件失败: %w", err)
	}
	defer f.Close()

	// UTF-8 BOM
	f.Write([]byte{0xEF, 0xBB, 0xBF})

	w := csv.NewWriter(f)
	defer w.Flush()

	header := []string{
		"timestamp", "agent_id", "task_id", "step_index", "tool_type",
		"budget_before", "latency_ms", "status_code", "error_code",
		"price", "token_used", "rejected", "priority", "phase", "error_msg",
	}
	if err := w.Write(header); err != nil {
		return "", fmt.Errorf("写入表头失败: %w", err)
	}

	for _, r := range results {
		row := []string{
			strconv.FormatInt(r.Timestamp, 10),
			r.AgentID,
			r.TaskID,
			strconv.Itoa(r.StepIndex),
			r.ToolType,
			strconv.Itoa(r.BudgetBefore),
			strconv.FormatInt(r.LatencyMs, 10),
			strconv.Itoa(r.StatusCode),
			strconv.Itoa(r.ErrorCode),
			r.Price,
			strconv.Itoa(r.TokenUsed),
			strconv.FormatBool(r.Rejected),
			r.Priority,
			r.Phase,
			r.ErrorMsg,
		}
		if err := w.Write(row); err != nil {
			return "", fmt.Errorf("写入数据行失败: %w", err)
		}
	}

	return filePath, nil
}

// WriteTaskSummaryToCSV 将任务级汇总写入 CSV
func WriteTaskSummaryToCSV(summaries []TaskSummary, outputDir string, strategy StrategyType, pattern LoadPattern, runIndex int) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("创建输出目录失败: %w", err)
	}

	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("%s_%s_agent_tasks_run%d_%s.csv", strategy, pattern, runIndex, timestamp)
	filePath := filepath.Join(outputDir, filename)

	f, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("创建 CSV 文件失败: %w", err)
	}
	defer f.Close()

	f.Write([]byte{0xEF, 0xBB, 0xBF})

	w := csv.NewWriter(f)
	defer w.Flush()

	header := []string{
		"task_id", "agent_id", "priority", "initial_budget",
		"task_success", "total_steps", "completed_steps",
		"total_tokens", "duration_ms", "failure_reason", "phase",
	}
	if err := w.Write(header); err != nil {
		return "", fmt.Errorf("写入表头失败: %w", err)
	}

	for _, s := range summaries {
		row := []string{
			s.TaskID,
			s.AgentID,
			s.Priority,
			strconv.Itoa(s.InitialBudget),
			strconv.FormatBool(s.TaskSuccess),
			strconv.Itoa(s.TotalSteps),
			strconv.Itoa(s.CompletedSteps),
			strconv.Itoa(s.TotalTokens),
			strconv.FormatInt(s.DurationMs, 10),
			s.FailureReason,
			s.Phase,
		}
		if err := w.Write(row); err != nil {
			return "", fmt.Errorf("写入数据行失败: %w", err)
		}
	}

	return filePath, nil
}

// WriteAgentMetricsSummaryToCSV 将指标汇总写入 CSV
func WriteAgentMetricsSummaryToCSV(summaries []AgentMetricsSummary, outputDir string) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("创建输出目录失败: %w", err)
	}

	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("agent_summary_%s.csv", timestamp)
	filePath := filepath.Join(outputDir, filename)

	f, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("创建 CSV 文件失败: %w", err)
	}
	defer f.Close()

	f.Write([]byte{0xEF, 0xBB, 0xBF})

	w := csv.NewWriter(f)
	defer w.Flush()

	header := []string{
		"strategy", "pattern", "run_index",
		"total_tasks", "success_tasks", "failed_tasks",
		"task_success_rate",
		"total_steps", "success_steps", "rejected_steps",
		"step_success_rate", "step_rejection_rate",
		"avg_latency_ms", "p50_latency_ms", "p95_latency_ms", "p99_latency_ms",
		"throughput_rps",
		"budget_10_task_rate", "budget_30_task_rate", "budget_100_task_rate",
		"priority_high_task_rate", "priority_medium_task_rate", "priority_low_task_rate",
		"avg_task_duration_ms", "avg_task_tokens",
		"duration_seconds",
	}
	if err := w.Write(header); err != nil {
		return "", fmt.Errorf("写入表头失败: %w", err)
	}

	for _, s := range summaries {
		b10 := fmt.Sprintf("%.4f", s.BudgetTaskSuccessRate[10])
		b30 := fmt.Sprintf("%.4f", s.BudgetTaskSuccessRate[30])
		b100 := fmt.Sprintf("%.4f", s.BudgetTaskSuccessRate[100])
		pHigh := fmt.Sprintf("%.4f", s.PriorityTaskSuccessRate[PriorityHigh])
		pMed := fmt.Sprintf("%.4f", s.PriorityTaskSuccessRate[PriorityMedium])
		pLow := fmt.Sprintf("%.4f", s.PriorityTaskSuccessRate[PriorityLow])

		row := []string{
			string(s.Strategy), string(s.Pattern), strconv.Itoa(s.RunIndex),
			strconv.Itoa(s.TotalTasks), strconv.Itoa(s.SuccessTasks), strconv.Itoa(s.FailedTasks),
			fmt.Sprintf("%.4f", s.TaskSuccessRate),
			strconv.FormatInt(s.TotalSteps, 10),
			strconv.FormatInt(s.SuccessSteps, 10),
			strconv.FormatInt(s.RejectedSteps, 10),
			fmt.Sprintf("%.4f", s.StepSuccessRate),
			fmt.Sprintf("%.4f", s.StepRejectionRate),
			fmt.Sprintf("%.2f", s.AvgLatencyMs),
			fmt.Sprintf("%.2f", s.P50LatencyMs),
			fmt.Sprintf("%.2f", s.P95LatencyMs),
			fmt.Sprintf("%.2f", s.P99LatencyMs),
			fmt.Sprintf("%.2f", s.ThroughputRPS),
			b10, b30, b100,
			pHigh, pMed, pLow,
			fmt.Sprintf("%.2f", s.AvgTaskDurationMs),
			fmt.Sprintf("%.2f", s.AvgTaskTokens),
			fmt.Sprintf("%.2f", s.DurationSeconds),
		}
		if err := w.Write(row); err != nil {
			return "", fmt.Errorf("写入数据行失败: %w", err)
		}
	}

	return filePath, nil
}
