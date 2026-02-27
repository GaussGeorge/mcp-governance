# Rajomon — MCP 服务治理引擎

> 基于 Rajomon 动态定价思想的 MCP (Model Context Protocol) 工具调用过载控制框架

[![Go Version](https://img.shields.io/badge/Go-1.23%2B-blue)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green)](#)

---

## 概述

在 AI Agent 大规模调用 MCP 工具的场景下，服务端极易因突发流量而过载。**Rajomon** 将经济学中的动态定价机制引入服务治理：

- 每次 `tools/call` 请求都有一个随负载实时变化的 **价格 (price)**
- AI Agent 携带 **令牌预算 (tokens)** 发起请求
- 当 `tokens ≥ price` 时放行，否则触发 **负载削减 (Load Shedding)** 快速拒绝
- 高预算的关键请求优先保障，低预算请求在过载时被优雅降级

这使得系统在过载时仍能维持 **价值驱动的差异化服务质量**。

## 核心架构

```
                        AI Agent (Client)
                   ┌─────────────────────────┐
                   │  ClientMiddleware        │
                   │  · 注入 _meta.tokens     │
                   │  · 客户端限流 (Rate Limit)│
                   └────────────┬────────────┘
                                │ HTTP POST · JSON-RPC 2.0
                                ▼
                        MCPServer (HTTP)
                   ┌─────────────────────────┐
                   │  ServeHTTP              │
                   │  · initialize / ping     │
                   │  · tools/list           │
                   │  · tools/call ──────────┼──┐
                   └─────────────────────────┘  │
                                                ▼
                        MCPGovernor (治理引擎)
                   ┌─────────────────────────────────────┐
                   │  ① LoadShedding (准入控制)           │
                   │     tokens ≥ price → 放行             │
                   │     tokens < price → 拒绝 (<1ms)      │
                   │  ② 执行工具处理函数                    │
                   │  ③ 响应附加当前 price                  │
                   └──────────────┬──────────────────────┘
                                  │
            ┌─────────────────────┼─────────────────────┐
            ▼                     ▼                     ▼
     queuingCheck          throughputCheck         latencyCheck
   Go 调度器延迟检测          吞吐量检测             业务延迟检测
            └─────────────────────┼─────────────────────┘
                                  ▼
                        动态价格更新
                   (Step / ExpDecay 策略)
```

## 模块说明

### 治理引擎 · `mcp_governor.go` / `mcp_init.go`

`MCPGovernor` 是框架的核心结构体：

| 能力 | 说明 |
|------|------|
| **LoadShedding** | 服务端准入控制 — `tokens ≥ price` 则放行，否则立即拒绝 |
| **RateLimiting** | 客户端限流 — 令牌不足时阻止请求发出，支持阻塞等待模式 |
| **HandleToolCall** | 中间件入口 — 串联准入控制 → 工具执行 → 价格响应 |
| **ClientMiddleware** | 客户端中间件 — 自动注入 `_meta.tokens` |
| **价格聚合** | 三种策略：`maximal` (取最大)、`additive` (累加)、`mean` (均值) |

### 动态定价 · `tokenAndPrice.go`

| 策略 | 机制 |
|------|------|
| **Step** | 拥塞时按固定步长 `priceStep` 涨价，非拥塞时等步长降价 |
| **ExpDecay** | 指数衰减涨价 — 连续涨价幅度递减，抑制价格震荡 |
| **底价 (Reserve Price)** | 价格下限保护，防止跌至 0 |
| **指导价 (Guide Price)** | 价格向预设目标收敛 |
| **下游传播** | 多下游工具价格聚合 → 总价格 = 自身 + 下游（按聚合策略） |

### 过载检测 · `overloadDetection.go` / `queuingDelay.go`

四种后台协程持续监控系统负载：

| 检测器 | 信号源 | 原理 |
|--------|--------|------|
| `latencyCheck` | 业务处理延迟 | 窗口内平均延迟 > 阈值 → 过载 |
| `queuingCheck` | Go runtime 调度器排队延迟 | 读取 `runtime/metrics` 直方图，计算增量最大排队时间 |
| `throughputCheck` | 吞吐量计数器 | 窗口内请求数超过阈值 → 涨价 |
| `checkBoth` | 吞吐量 + 排队延迟 | 联合判定 |

`queuingDelay.go` 提供直方图差分计算工具，从 Go runtime 的 `/sched/latencies:seconds` 指标中提取增量排队延迟的中位数、分位数和最大值。

### MCP 传输层 · `mcp_transport.go` / `mcp_protocol.go`

基于 HTTP POST + JSON-RPC 2.0 的完整 MCP 通信层：

| 方法 | 功能 |
|------|------|
| `initialize` | MCP 握手，协商协议版本与能力 |
| `tools/list` | 返回注册的工具列表及其 Schema |
| `tools/call` | 工具调用入口，集成治理中间件 |
| `ping` | 健康检查 |

自定义错误码：`-32001` (过载)、`-32002` (限流)、`-32003` (令牌不足)。

## 基线对照

项目提供两个基线实现，用于与 Rajomon 进行对比实验：

| 方案 | 目录 | 限流方式 | 价值区分 |
|------|------|---------|---------|
| **FIFO 无治理** | `baseline/no_governance/` | 无 | 无 |
| **静态限流** | `baseline/static_rate_limit/` | 令牌桶固定 QPS | 无 |
| **Rajomon** | 根目录 | 动态定价 + Load Shedding | **有** — 高预算优先 |

**对比结论**：

| 维度 | FIFO 无治理 | 静态限流 | Rajomon |
|------|-----------|---------|---------|
| 低负载 | 正常 | 正常 | 正常（价格低） |
| 高负载 | 延迟爆炸 | 超阈值一刀切拒绝 | 价格上涨，低预算优先拒绝 |
| 过载 | 可能崩溃 | 严格按 QPS 上限 | 高预算仍可通过 |
| 恢复 | 缓慢 | 立即 | 价格逐步回落 |
| 公平性 | 无区分 | 无区分 | **高预算 > 低预算** |

## 项目结构

```
ra-annotion-demo/
├── mcp_governor.go          # 治理引擎核心 — LoadShedding / RateLimiting / HandleToolCall
├── mcp_init.go              # MCPGovernor 初始化与选项解析
├── mcp_protocol.go          # MCP 协议类型定义 (JSON-RPC 2.0)
├── mcp_transport.go         # MCP HTTP 传输层 (MCPServer)
├── tokenAndPrice.go         # 令牌分配与动态定价 (Step / ExpDecay)
├── overloadDetection.go     # 过载检测协程 (延迟 / 排队 / 吞吐量)
├── queuingDelay.go          # Go runtime 调度器排队延迟直方图分析
├── logger.go                # 调试日志工具
├── baseline/
│   ├── no_governance/       # 基线：FIFO 无治理
│   └── static_rate_limit/   # 基线：令牌桶固定限流
├── loadtest/                # 负载测试框架 (阶梯 / 正弦 / 泊松)
│   ├── main.go              # CLI 入口
│   ├── config.go            # 测试配置
│   ├── server.go            # 三种策略的服务端启动
│   ├── loader.go            # 负载生成器
│   ├── runner.go            # 测试编排器
│   ├── metrics.go           # 指标计算与统计
│   ├── result.go            # 结果类型与 CSV 导出
│   └── output/              # CSV 输出目录
└── prompt/                  # 测试用 Prompt 模板
```

## 环境要求

- **Go** 1.23.0+
- **OS** Windows / Linux / macOS

## 快速开始

```bash
# 克隆并初始化
cd ra-annotion-demo
go mod tidy

# 运行单元测试
go test ./... -v -timeout 5m

# 快速负载对比测试
go run ./loadtest/ -mode=quick

# 完整基准测试（论文数据采集，3 轮取均值）
go run ./loadtest/ -mode=full -runs=3 -pattern=step

# 单策略调试
go run ./loadtest/ -mode=single -strategy=rajomon -verbose

# 消融实验
go run ./loadtest/ -mode=ablation -ablation-target=rajomon
```

### 关键参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-mode` | `quick` | `quick` / `full` / `single` / `ablation` |
| `-strategy` | `no_governance` | `no_governance` / `static_rate_limit` / `rajomon` |
| `-pattern` | `step` | `step` (阶梯) / `sine` (正弦) / `poisson` (泊松) |
| `-price-step` | `10` | Rajomon 价格步长 |
| `-latency-threshold` | `500µs` | Rajomon 排队延迟阈值 |
| `-rate-limit` | `30.0` | 静态限流 QPS 上限 |
| `-mock-delay` | `20ms` | Mock 工具基础处理延迟 |
| `-runs` | `1` | 每种策略重复次数 |
| `-verbose` | `false` | 调试日志开关 |

## 使用示例

```go
package main

import (
    "context"
    "net/http"
    mcpgov "mcp-governance"
)

func main() {
    // 定义工具调用关系
    callMap := map[string][]string{
        "get_weather": {},                          // 无下游依赖
        "plan_trip":   {"get_weather", "search"},   // 依赖两个下游
    }

    // 创建治理引擎
    gov := mcpgov.NewMCPGovernor("server-1", callMap, map[string]interface{}{
        "loadShedding":     true,
        "pinpointQueuing":  true,
        "latencyThreshold": 500 * time.Microsecond,
        "priceStep":        int64(10),
    })

    // 创建 MCP 服务端
    server := mcpgov.NewMCPServer("weather-service", gov)

    // 注册工具
    server.RegisterTool(mcpgov.MCPTool{
        Name:        "get_weather",
        Description: "查询城市天气",
        InputSchema: map[string]interface{}{
            "type": "object",
            "properties": map[string]interface{}{
                "city": map[string]string{"type": "string"},
            },
        },
    }, func(ctx context.Context, params mcpgov.MCPToolCallParams) (*mcpgov.MCPToolCallResult, error) {
        city := params.Arguments["city"].(string)
        return &mcpgov.MCPToolCallResult{
            Content: []mcpgov.ContentBlock{mcpgov.TextContent(city + ": 晴天 25°C")},
        }, nil
    })

    // 启动 HTTP 服务
    http.ListenAndServe(":8080", server)
}
```

## 许可证

