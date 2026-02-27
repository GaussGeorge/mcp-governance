# loadtest — MCP 服务治理负载测试框架

> 三种治理策略（无治理 / 静态限流 / Rajomon 动态定价）的自动化对比测试与消融实验工具

---

## 概述

本模块提供一套完整的负载测试流水线：**启动服务 → 生成负载 → 收集指标 → CSV 导出**，用于量化对比三种 MCP 治理策略在不同负载模式下的表现。

支持的实验类型：

- **快速验证** — 短时长阶梯测试，确认系统行为正确
- **完整基准** — 标准时长、多轮重复，生成论文级实验数据
- **单策略调试** — 隔离运行某策略，配合 `-verbose` 查看治理细节
- **消融实验** — Rajomon 参数消融 / 静态限流参数消融 / 后端容量消融

## 目录结构

```
loadtest/
├── main.go              # CLI 入口 — 参数解析、模式分发
├── config.go            # 配置定义 — 策略类型、负载模式、消融分组
├── server.go            # 服务端启动 — 三种策略的 Mock 工具注册
├── loader.go            # 负载生成器 — 阶梯 / 正弦 / 泊松三种模式
├── runner.go            # 测试编排 — 多策略循环、消融组执行
├── metrics.go           # 指标计算 — 吞吐量、延迟分位数、公平性
├── result.go            # 结果导出 — 请求记录与 CSV 文件
├── loadtest_test.go     # Go Test 入口
└── output/              # CSV 输出目录（运行后自动生成）
```

## 快速开始

### 1. 快速验证（推荐首次运行）

```bash
go run ./loadtest/ -mode=quick
```

三种策略各跑一轮短时阶梯测试，控制台输出对比报告。

### 2. 完整基准测试

```bash
go run ./loadtest/ -mode=full -runs=3 -pattern=step
```

每种策略重复 3 次取均值。阶段配置：预热(30s) → 低(60s) → 中(60s) → 高(60s) → 过载(60s) → 恢复(60s)。

### 3. 单策略调试

```bash
go run ./loadtest/ -mode=single -strategy=rajomon -verbose
go run ./loadtest/ -mode=single -strategy=static_rate_limit
go run ./loadtest/ -mode=single -strategy=no_governance
```

### 4. 消融实验

```bash
# Rajomon 参数消融（priceStep × latencyThreshold）
go run ./loadtest/ -mode=ablation -ablation-target=rajomon

# 静态限流 QPS 阈值消融
go run ./loadtest/ -mode=ablation -ablation-target=static

# 后端容量消融（maxConcurrency）
go run ./loadtest/ -mode=ablation -ablation-target=capacity

# 全部消融
go run ./loadtest/ -mode=ablation -ablation-target=all
```

### 5. Go Test

```bash
cd loadtest
go test -v -run TestQuickComparison   -timeout 10m
go test -v -run TestSingleStrategy    -timeout 10m
go test -v -run TestRajomonPriceDynamics -timeout 5m
go test -v -run TestStepLoadAllStrategies -timeout 30m
```

## CLI 参数

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-mode` | `quick` | `quick` / `full` / `single` / `ablation` |
| `-strategy` | `no_governance` | 单策略模式下的目标策略 |
| `-pattern` | `step` | 负载模式：`step` / `sine` / `poisson` |
| `-runs` | `1` | 每种策略重复运行次数 |
| `-output` | `loadtest/output` | CSV 输出目录 |
| `-verbose` | `false` | 输出治理引擎调试日志 |
| `-seed` | `42` | 随机种子（可复现） |
| `-price-step` | `10` | Rajomon 价格步长 |
| `-latency-threshold` | `500µs` | Rajomon 排队延迟阈值 |
| `-rate-limit` | `30.0` | 静态限流 QPS 上限 |
| `-mock-delay` | `20ms` | Mock 工具基础处理延迟 |
| `-ablation-target` | `all` | 消融目标：`all` / `rajomon` / `static` / `capacity` |

## 负载模式

### 阶梯式 (Step)

模拟突发流量的等级递增：

| 阶段 | 快速模式 | 完整模式 | 并发数 |
|------|---------|---------|--------|
| warmup | 5s | 30s | 5 |
| low | 10s | 60s | 20 |
| medium | 10s | 60s | 50 |
| high | 10s | 60s | 100 |
| overload | 10s | 60s | 200 |
| recovery | 10s | 60s | 10 |

### 正弦波 (Sine)

模拟周期性波动：$concurrency = base + amplitude \cdot \sin\left(\frac{2\pi \cdot t}{period}\right)$

默认：基础 30、振幅 50、周期 2 分钟。

### 泊松到达 (Poisson)

请求间隔服从指数分布：$interval \sim Exp(1/\lambda)$，默认 $\lambda = 50$ QPS。

## 收集的指标

### 性能维度

| 指标 | 说明 |
|------|------|
| 吞吐量 (RPS) | 每秒成功请求数 |
| 延迟 | Avg / P50 / P95 / P99 / Max / StdDev |
| 错误率 | 非成功响应占比 |

### 治理效果维度

| 指标 | 说明 |
|------|------|
| 拒绝率 | 被治理引擎主动拒绝的请求比例 |
| 公平性 | 不同预算组 (Budget 10 / 50 / 100) 的成功率 |
| 动态价格 | 响应中附带的实时价格值 |

### 按阶段统计

阶梯模式下自动输出每个阶段的独立指标。

## 输出文件

### 单次运行

```
{strategy}_{pattern}_run{N}_{timestamp}.csv
```

每条记录对应一次请求的完整信息。

### 汇总

```
summary_{timestamp}.csv
```

### CSV 字段

| 字段 | 类型 | 说明 |
|------|------|------|
| `timestamp` | int64 | 请求完成时间 (Unix ms) |
| `request_id` | int64 | 请求唯一标识 |
| `phase` | string | 所处负载阶段 |
| `client_budget` | int | Token 预算 |
| `latency_ms` | int64 | 请求耗时 (ms) |
| `status_code` | int | HTTP 状态码 (200=成功, -1=网络错误) |
| `error_code` | int | JSON-RPC 错误码 |
| `price` | string | 服务端返回的当前价格 |
| `token_usage` | int | Token 消耗量 |
| `rejected` | bool | 是否被拒绝 |
| `error_msg` | string | 错误信息 |

## 预期结果

| 维度 | 无治理 | 固定限流 | Rajomon |
|------|--------|---------|---------|
| 低负载 | 正常 | 正常 | 正常（价格低） |
| 高负载 | 延迟飙升 | 超阈值一刀切拒绝 | 价格上涨，低预算优先拒绝 |
| 过载 | 可能崩溃 | 严格按 QPS 上限 | 高预算仍可通过 |
| 恢复 | 缓慢 | 立即 | 价格逐步回落 |
| 公平性 | 无区分 | 无区分 | **高预算成功率 > 低预算** |

## 参数调优

**Rajomon 拒绝率过高 / 恢复过慢**：
- 减小 `-price-step`（如 3）→ 涨价更平缓
- 增大 `-latency-threshold`（如 2ms）→ 更高容忍度
- 增大客户端预算（代码中 `Budgets` 改为 `[50, 100, 200]`）

**静态限流拒绝率过高**：
- 增大 `-rate-limit`（如 50.0）→ 提高 QPS 上限
