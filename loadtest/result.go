// result.go
// 请求结果数据类型和 CSV 导出模块
//
// 本文件定义两类内容：
//  1. RequestResult 结构体：每个 HTTP 请求的完整记录（loader.go 产生，流向 metrics.go + CSV）
//  2. 三个 CSV 导出函数：
//     - WriteResultsToCSV   : 原始请求记录 → 明细 CSV（每行一个请求）
//     - WriteSummaryToCSV   : 多次运行汇总   → 汇总 CSV（每行一次运行）
//     - WriteAblationToCSV  : 消融实验结果 → 消融 CSV（每行一个对照组 × 负载模式）
//
// CSV 文件统一写入 UTF-8 BOM 以兼容 Excel 中文显示，输出到 output/ 目录
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
// 每个请求在 loader.go 的 sendOneRequest() 中生成，通过 channel 收集到 results 切片
// 后续流向：
//
//	→ metrics.go 的 CalculateMetrics() 进行统计计算
//	→ result.go  的 WriteResultsToCSV()  导出为明细 CSV 文件
type RequestResult struct {
	Timestamp    int64  // 请求完成时的 Unix 时间戳（毫秒），用于计算测试时长和时序分析
	RequestID    int64  // 请求唯一标识，递增生成，用于排查和关联
	Phase        string // 所处负载阶段名称（仅 step 模式有值，如 "warmup"/"overload"）
	ClientBudget int    // 请求携带的 Token 预算值，用于公平性分析
	LatencyMs    int64  // 请求端到端总耗时（毫秒），包含网络 + 服务器处理
	StatusCode   int    // HTTP 响应状态码：200=成功，429=限流，503=过载，-1=网络连接失败
	ErrorCode    int    // JSON-RPC error.code：0=无错误，-32004=价格超预算，-32005=过载
	Price        string // 响应中返回的当前价格字符串（Rajomon 动态定价的实时值）
	TokenUsage   int    // Token 消耗量（当前未使用，预留字段）
	Rejected     bool   // 是否被网关拒绝（true = 过载/限流/价格不足）
	ErrorMsg     string // 错误信息详情（包含 JSON-RPC error.message 或网络错误描述）
}

// IsSuccess 判断请求是否成功
// 判定条件（三者同时满足）：
//  1. HTTP 状态码 == 200（服务器正常响应）
//  2. JSON-RPC ErrorCode == 0（无业务错误）
//  3. Rejected == false（未被网关拒绝）
func (r *RequestResult) IsSuccess() bool {
	return r.StatusCode == 200 && r.ErrorCode == 0 && !r.Rejected
}

// WriteResultsToCSV 将原始请求结果切片写入 CSV 明细文件
// 每行一个请求，包含时间戳、预算、延迟、状态码、价格等全部字段
// 文件名格式：{strategy}_{pattern}_run{N}_{timestamp}.csv
// 这些明细 CSV 是 visualization/ 下 Python 脚本的输入数据源
func WriteResultsToCSV(results []RequestResult, outputDir string, strategy StrategyType, pattern LoadPattern, runIndex int) (string, error) {
	// 确保输出目录存在
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("创建输出目录失败: %w", err)
	}

	// 构造文件名：strategy_pattern_run{N}_timestamp.csv
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("%s_%s_run%d_%s.csv", strategy, pattern, runIndex, timestamp)
	filePath := filepath.Join(outputDir, filename)

	f, err := os.Create(filePath)
	if err != nil {
		return "", fmt.Errorf("创建 CSV 文件失败: %w", err)
	}
	defer f.Close()

	// 写入 UTF-8 BOM（兼容 Excel 中文显示）
	f.Write([]byte{0xEF, 0xBB, 0xBF})

	w := csv.NewWriter(f)
	defer w.Flush()

	// 写入表头
	header := []string{
		"timestamp", "request_id", "phase", "client_budget",
		"latency_ms", "status_code", "error_code", "price",
		"token_usage", "rejected", "error_msg",
	}
	if err := w.Write(header); err != nil {
		return "", fmt.Errorf("写入 CSV 表头失败: %w", err)
	}

	// 写入数据行
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
// 每行一次运行，包含策略、模式、吞吐量、延迟百分位数、拒绝率、各预算组成功率
// 由 RunAllStrategies() 在所有策略运行完毕后调用
// 文件名格式：summary_{timestamp}.csv
func WriteSummaryToCSV(summaries []MetricsSummary, outputDir string) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("创建输出目录失败: %w", err)
	}

	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("summary_%s.csv", timestamp)
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

// WriteAblationToCSV 将消融对照结果写入 CSV 文件
// 每行一个对照组 × 负载模式的组合，包含组名、描述、所有指标
// 由 RunAblationStudy() 在全部对照组运行完毕后调用
// 文件名格式：ablation_{timestamp}.csv
func WriteAblationToCSV(results []AblationResult, outputDir string) (string, error) {
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return "", fmt.Errorf("创建输出目录失败: %w", err)
	}

	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("ablation_%s.csv", timestamp)
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
		"group_name", "description", "strategy", "pattern",
		"total_requests", "success_count", "rejected_count",
		"throughput_rps", "error_rate", "rejection_rate",
		"avg_latency_ms", "p50_latency_ms", "p95_latency_ms", "p99_latency_ms",
		"max_latency_ms",
		"budget_10_success_rate", "budget_50_success_rate", "budget_100_success_rate",
		"duration_seconds",
	}
	if err := w.Write(header); err != nil {
		return "", fmt.Errorf("写入 CSV 表头失败: %w", err)
	}

	for _, r := range results {
		s := r.Summary
		b10 := fmt.Sprintf("%.4f", s.BudgetSuccessRate[10])
		b50 := fmt.Sprintf("%.4f", s.BudgetSuccessRate[50])
		b100 := fmt.Sprintf("%.4f", s.BudgetSuccessRate[100])

		row := []string{
			r.GroupName, r.Description, string(r.Strategy), string(r.Pattern),
			strconv.FormatInt(s.TotalRequests, 10),
			strconv.FormatInt(s.SuccessCount, 10),
			strconv.FormatInt(s.RejectedCount, 10),
			fmt.Sprintf("%.2f", s.ThroughputRPS),
			fmt.Sprintf("%.4f", s.ErrorRate),
			fmt.Sprintf("%.4f", s.RejectionRate),
			fmt.Sprintf("%.2f", s.AvgLatencyMs),
			fmt.Sprintf("%.2f", s.P50LatencyMs),
			fmt.Sprintf("%.2f", s.P95LatencyMs),
			fmt.Sprintf("%.2f", s.P99LatencyMs),
			fmt.Sprintf("%.2f", s.MaxLatencyMs),
			b10, b50, b100,
			fmt.Sprintf("%.2f", s.DurationSeconds),
		}
		if err := w.Write(row); err != nil {
			return "", fmt.Errorf("写入 CSV 数据行失败: %w", err)
		}
	}

	return filePath, nil
}
