# MCP 集成测试 — 真实 Python MCP 服务器 + Go Rajomon 治理代理

## 概述

本目录实现了 **真实 MCP 服务器**与 **Rajomon 治理框架**的集成测试。与 `loadtest/` 使用 Mock 工具不同，这里的测试流量经过 Go 治理代理后，会被转发到真正的 Python MCP 工具服务器执行。

### 架构图

```
┌─────────────────┐     JSON-RPC      ┌──────────────────────┐     JSON-RPC      ┌─────────────────────┐
│                 │     over HTTP      │                      │     over HTTP      │                     │
│  Go 负载生成器   │ ──────────────→  │  Go 治理代理          │ ──────────────→  │  Python MCP Bridge   │
│  (loader.go)    │                    │  (proxy.go)          │                    │  (bridge.py)        │
│                 │  ← ────────────── │                      │  ← ────────────── │                     │
│  - step         │     JSON-RPC      │  策略:                │     JSON-RPC      │  工具:               │
│  - sine         │     response      │  1. 无治理 (直接转发)  │     response      │  - calculate        │
│  - poisson      │                    │  2. 静态限流 (令牌桶)  │                    │  - text_analyze     │
│                 │                    │  3. Rajomon (动态定价) │                    │  - get_weather      │
└─────────────────┘                    └──────────────────────┘                    │  - web_search ...   │
                                                                                   └─────────────────────┘
```

### 与 loadtest/ 的核心区别

| 维度     | loadtest/ (Mock)                | integration/ (真实)                 |
| -------- | ------------------------------- | ----------------------------------- |
| 工具处理 | `simulateProcessing()` 模拟延迟 | 调用真实 Python MCP 工具函数        |
| 延迟来源 | Go sleep + CPU burn             | Python 工具执行 + 网络 I/O          |
| 工具范围 | 单个 mock_tool                  | calculate, text_analyze, weather 等 |
| 外部依赖 | 无                              | Python MCP Server (bridge.py)       |
| 适用场景 | 治理算法验证                    | 论文实验数据采集                    |

## 快速开始

### 1. 启动 Python MCP Bridge

```bash
cd mcp_server
pip install uvicorn starlette
python -m server.bridge --port 9000
```

### 2. 运行集成测试

```bash
# 方法 A: 使用一键脚本 (自动启动/关闭 Bridge)
./integration/run.sh

# 方法 B: 手动运行
go run ./integration/ -mode=quick
```

### 3. 生成图表

```bash
python integration/plot_integration.py
```

## 测试模式

| 模式     | 命令                                                   | 说明               | 时间     |
| -------- | ------------------------------------------------------ | ------------------ | -------- |
| 快速验证 | `go run ./integration/`                                | 短时间三策略对比   | ~3 分钟  |
| 完整测试 | `go run ./integration/ -mode=full`                     | 标准阶段时长, 多轮 | ~30 分钟 |
| 单策略   | `go run ./integration/ -mode=single -strategy=rajomon` | 隔离调试           | ~5 分钟  |
| 全量对比 | `go run ./integration/ -mode=cross-pattern`            | 3 策略 × 3 模式    | ~30 分钟 |

## 命令行参数

```
通用:
  -mode         测试模式: quick/full/single/cross-pattern (默认: quick)
  -strategy     单策略模式: no_governance/static_rate_limit/rajomon
  -pattern      负载模式: step/sine/poisson (默认: step)
  -runs         重复次数 (默认: 1)
  -output       CSV 输出目录 (默认: integration/output)

连接:
  -bridge-url   Python Bridge 地址 (默认: http://localhost:9000)
  -proxy-port   Go 代理端口 (默认: 8080)
  -skip-check   跳过 Bridge 连通性检查

工具:
  -tool         测试用工具名 (默认: calculate)
  -tool-args    工具参数 JSON (默认: {"expression": "2 + 3 * 4 - 1"})

Rajomon:
  -price-step          价格步长 (默认: 5)
  -latency-threshold   延迟阈值 (默认: 2000µs)

限流:
  -rate-limit   静态限流 QPS (默认: 30)
```

## 文件说明

| 文件                  | 说明                                        |
| --------------------- | ------------------------------------------- |
| `main.go`             | CLI 入口, 参数解析与模式分发                |
| `config.go`           | 测试配置结构与默认值                        |
| `proxy.go`            | 三种策略的 Go 治理代理实现                  |
| `mcp_client.go`       | Python MCP Bridge HTTP 客户端               |
| `loader.go`           | 负载生成器 (step/sine/poisson)              |
| `runner.go`           | 测试编排器 (启动代理 → 生成负载 → 收集指标) |
| `metrics.go`          | 统计指标计算 (吞吐量, 延迟分位数, 公平性)   |
| `result.go`           | 请求结果与 CSV 导出                         |
| `run.sh`              | 一键启动脚本                                |
| `plot_integration.py` | 论文图表生成                                |

## 输出

### CSV 文件 (integration/output/)

- `int_{strategy}_{pattern}_run{N}_{timestamp}.csv` — 每请求明细
- `int_summary_{timestamp}.csv` — 汇总指标

### 图表 (integration/figures/)

1. **throughput_timeseries.png** — 吞吐量与拒绝率时间序列
2. **latency_cdf.png** — 延迟累积分布 (CDF)
3. **fairness_boxplot.png** — 公平性分析 (按预算分组)
4. **price_response.png** — Rajomon 动态价格响应曲线
5. **phase_comparison.png** — 各阶段性能对比
6. **summary_comparison.png** — 三策略综合对比
