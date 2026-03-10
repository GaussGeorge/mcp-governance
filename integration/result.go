// result.go
// ==================== 请求结果与 CSV 导出 ====================
//
// 与 loadtest/result.go 相同的数据结构和 CSV 导出逻辑。
package main

import (
	"encoding/csv"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// RequestResult 单次请求的详细结果记录
type RequestResult struct {
	Timestamp    int64  `json:"timestamp"`
	RequestID    int64  `json:"request_id"`
	Phase        string `json:"phase"`
	ClientBudget int    `json:"client_budget"`
	LatencyMs    int64  `json:"latency_ms"`
	StatusCode   int    `json:"status_code"`
	ErrorCode    int    `json:"error_code"`
	Price        string `json:"price"`
	TokenUsage   int    `json:"token_usage"`
	Rejected     bool   `json:"rejected"`
	ErrorMsg     string `json:"error_msg"`
}

// IsSuccess 判断请求是否成功
func (r *RequestResult) IsSuccess() bool {
	return r.StatusCode == 200 && r.ErrorCode == 0 && !r.Rejected
}

// WriteResultsToCSV 将原始请求结果写入 CSV 文件
func WriteResultsToCSV(results []RequestResult, outputDir string, strategy StrategyType, pattern LoadPattern, runIndex int) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("创建输出目录失败: %w", err)
	}

	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("int_%s_%s_run%d_%s.csv", strategy, pattern, runIndex, timestamp)
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
		"timestamp", "request_id", "phase", "client_budget",
		"latency_ms", "status_code", "error_code", "price",
		"token_usage", "rejected", "error_msg",
	}
	if err := w.Write(header); err != nil {
		return "", fmt.Errorf("写入 CSV 表头失败: %w", err)
	}

	for _, r := range results {
		row := []string{
			strconv.FormatInt(r.Timestamp, 10),
			strconv.FormatInt(r.RequestID, 10),
			r.Phase,
			strconv.Itoa(r.ClientBudget),
			strconv.FormatInt(r.LatencyMs, 10),
			strconv.Itoa(r.StatusCode),
			strconv.Itoa(r.ErrorCode),
			r.Price,
			strconv.Itoa(r.TokenUsage),
			strconv.FormatBool(r.Rejected),
			r.ErrorMsg,
		}
		if err := w.Write(row); err != nil {
			return "", fmt.Errorf("写入 CSV 数据行失败: %w", err)
		}
	}

	return filePath, nil
}

// WriteSummaryToCSV 将多次运行的汇总指标写入 CSV
func WriteSummaryToCSV(summaries []MetricsSummary, outputDir string) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("创建输出目录失败: %w", err)
	}

	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("int_summary_%s.csv", timestamp)
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
		"total_requests", "success_count", "error_count", "rejected_count",
		"throughput_rps", "error_rate", "rejection_rate",
		"avg_latency_ms", "p50_latency_ms", "p95_latency_ms", "p99_latency_ms",
		"max_latency_ms", "latency_stddev_ms",
		"budget_10_success_rate", "budget_50_success_rate", "budget_100_success_rate",
		"duration_seconds",
	}
	if err := w.Write(header); err != nil {
		return "", fmt.Errorf("写入 CSV 表头失败: %w", err)
	}

	for _, s := range summaries {
		b10 := fmt.Sprintf("%.4f", s.BudgetSuccessRate[10])
		b50 := fmt.Sprintf("%.4f", s.BudgetSuccessRate[50])
		b100 := fmt.Sprintf("%.4f", s.BudgetSuccessRate[100])

		row := []string{
			string(s.Strategy), string(s.Pattern), strconv.Itoa(s.RunIndex),
			strconv.FormatInt(s.TotalRequests, 10),
			strconv.FormatInt(s.SuccessCount, 10),
			strconv.FormatInt(s.ErrorCount, 10),
			strconv.FormatInt(s.RejectedCount, 10),
			fmt.Sprintf("%.2f", s.ThroughputRPS),
			fmt.Sprintf("%.4f", s.ErrorRate),
			fmt.Sprintf("%.4f", s.RejectionRate),
			fmt.Sprintf("%.2f", s.AvgLatencyMs),
			fmt.Sprintf("%.2f", s.P50LatencyMs),
			fmt.Sprintf("%.2f", s.P95LatencyMs),
			fmt.Sprintf("%.2f", s.P99LatencyMs),
			fmt.Sprintf("%.2f", s.MaxLatencyMs),
			fmt.Sprintf("%.2f", s.LatencyStddevMs),
			b10, b50, b100,
			fmt.Sprintf("%.2f", s.DurationSeconds),
		}
		if err := w.Write(row); err != nil {
			return "", fmt.Errorf("写入 CSV 数据行失败: %w", err)
		}
	}

	return filePath, nil
}
