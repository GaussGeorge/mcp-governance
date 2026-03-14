# MCP 服务治理 — 统一实验配置参考

> 本文件记录三种策略（无治理 / 静态限流 / Rajomon 动态定价）在 **loadtest**（Mock 服务器）和 **integration**（真实 Python MCP 服务器）两套测试框架下的完整参数配置，确保实验公平性和跨平台一致性。

---

## 1. 公平性修正记录

| 问题 | 修正前 | 修正后 | 影响 |
|------|--------|--------|------|
| integration `StaticBurstSize` 过小 | `10` (仅为 QPS 的 1/3) | `30` (等于 QPS) | 静态限流策略不再被不公平削弱 |
| `BudgetWeights` 不一致 | loadtest 无权重（均匀）, integration `[0.4, 0.35, 0.25]` | 统一为 `[0.3, 0.4, 0.3]` | 预算分布在两套测试中一致 |
| loadtest 后端过载模型不一致 | 仅无治理有并发追踪+过载延迟 | 三种策略共享相同的 `backendSimulator` | 后端行为统一，治理层是唯一变量 |
| HTTP 客户端超时不一致 | loadtest 硬编码 10s, integration 30s | loadtest 改为可配置，默认 30s | 请求超时行为一致 |
| `GOMAXPROCS` 未固定 | 依赖运行时 CPU 核数 | 固定为 4（可通过环境变量覆盖） | 不同机器的调度行为一致 |

---

## 2. 三种策略对比参数

### 2.1 共享参数（对所有策略相同）

#### Loadtest（Mock 服务器）

| 参数 | 值 | 说明 |
|------|------|------|
| `Budgets` | `[10, 50, 100]` | 三种客户端预算等级 |
| `BudgetWeights` | `[0.3, 0.4, 0.3]` | 低30%、中40%、高30% |
| `MockDelay` | `20ms` | 工具处理基础延迟 |
| `MockDelayVar` | `30ms` | 延迟随机波动范围 (总延迟 20-50ms) |
| `MaxServerConcurrency` | `50` | 后端最大并发容量（所有策略共享） |
| `OverloadLatencyScale` | `8.0` | 过载延迟放大倍数（所有策略共享） |
| `RequestInterval` | `10ms` | 单 worker 请求间隔 |
| `RejectionBackoff` | `100ms` | 被拒后退避时间 |
| `HTTPClientTimeout` | `30s` | HTTP 请求超时 |
| `RandomSeed` | `42` | 固定随机种子 |

#### Step 负载阶段（Loadtest）

| 阶段 | 时长 | 并发数 | 与容量比 |
|------|------|--------|----------|
| warmup | 30s | 5 | 10% |
| low | 60s | 20 | 40% |
| medium | 60s | 50 | 100% |
| high | 60s | 100 | 200% |
| overload | 60s | 200 | 400% |
| recovery | 60s | 10 | 20% |

#### Integration（真实 Python MCP 服务器）

| 参数 | 值 | 说明 |
|------|------|------|
| `Budgets` | `[10, 50, 100]` | 三种客户端预算等级 |
| `BudgetWeights` | `[0.3, 0.4, 0.3]` | 低30%、中40%、高30% |
| `MCPBridgeURL` | `http://localhost:9000` | Python MCP Bridge 地址 |
| `HTTPClientTimeout` | `30s` | HTTP 请求超时 |
| `HTTPMaxConnections` | `500` | HTTP 连接池大小 |
| `RequestInterval` | `10ms` | 单 worker 请求间隔 |
| `RejectionBackoff` | `100ms` | 被拒后退避时间 |
| `RandomSeed` | `42` | 固定随机种子 |

#### Step 负载阶段（Integration）

| 阶段 | 时长 | 并发数 |
|------|------|--------|
| warmup | 30s | 5 |
| low | 60s | 15 |
| medium | 60s | 40 |
| high | 60s | 80 |
| overload | 60s | 150 |
| recovery | 60s | 5 |

> **注意**：Integration 并发量低于 Loadtest，因为 Python Bridge 容量较小（max_concurrent=20）。

---

### 2.2 策略特有参数

#### 无治理 (No Governance)

| 测试框架 | 特有参数 | 说明 |
|----------|---------|------|
| Loadtest | 无额外参数 | 共享 `backendSimulator`，所有请求直接到达后端 |
| Integration | 无额外参数 | 直接转发到 Python Bridge |

**行为**：不做任何准入控制。高并发时后端退化（延迟飙升 + 错误）。

#### 静态限流 (Static Rate Limit)

| 参数 | Loadtest | Integration | 说明 |
|------|----------|-------------|------|
| `StaticRateLimitQPS` | `30.0` | `30.0` | 令牌桶速率上限 |
| `StaticBurstSize` | `30` | `30` | 突发容量（等于 QPS） |

**行为**：令牌桶算法。超过 QPS 的请求立即被拒绝（错误码 -32002）。

#### Rajomon 动态定价

| 参数 | Loadtest | Integration | 说明 |
|------|----------|-------------|------|
| `priceStep` | `5` | `15` | 价格调整步长 |
| `latencyThreshold` | `2000µs (2ms)` | `150ms` | 延迟阈值（触发涨价） |
| `priceUpdateRate` | `100ms` | `200ms` | 价格更新频率 |
| `priceStrategy` | `"step"` | `"step"` | 价格策略 |
| `priceAggregation` | `"maximal"` | `"maximal"` | 价格聚合方式 |
| `initPrice` | `0` | `0` | 初始价格 |
| **过载检测方式** | `pinpointQueuing` | `pinpointLatency` | ⚠️ 关键差异 |

> **为什么 Rajomon 参数不同？**
>
> Loadtest 使用 `pinpointQueuing`（Go runtime 调度延迟），阈值在微秒级别（2ms）。
> Integration 使用 `pinpointLatency`（HTTP 响应延迟），阈值在毫秒级别（150ms）。
> 这是因为检测机制不同导致的合理参数差异，**不是公平性问题**。

---

## 3. 后端行为模型

### 3.1 Loadtest — 共享 BackendSimulator

所有三种策略的工具处理器使用相同的 `backendSimulator`：

```
并发 < 70% 容量:  正常处理 (baseDelay + jitter)
并发 70%-100%:    延迟指数增长 extraDelay = baseDelay × scale × ((ratio-0.7)/0.3)²
并发 > 100%:      返回错误 (模拟资源耗尽)
```

治理层的作用是**减少到达后端的并发数**，使后端保持在健康范围内。

### 3.2 Integration — Python Bridge ConcurrencyLimiter

三种策略共用同一个 Python Bridge 进程，内置 `ConcurrencyLimiter`：

| 参数 | 默认值 | 环境变量 |
|------|--------|----------|
| `max_concurrent` | `20` | `BRIDGE_MAX_CONCURRENT` |
| `base_delay_ms` | `50` | `BRIDGE_BASE_DELAY_MS` |
| `delay_jitter_ms` | `15` | `BRIDGE_DELAY_JITTER_MS` |
| `overload_scale` | `8.0` | `BRIDGE_OVERLOAD_SCALE` |

**过载行为与 Loadtest 的 BackendSimulator 相同**（二次延迟增长 + 容量溢出拒绝）。

---

## 4. 跨平台一致性

### 4.1 已固定的参数

| 设置 | 值 | 说明 |
|------|------|------|
| `GOMAXPROCS` | `4` | Go 并发处理器数（可通过环境变量 `GOMAXPROCS` 覆盖） |
| `RandomSeed` | `42` | 确保负载模式和预算选择可复现 |
| `HTTPClientTimeout` | `30s` | 统一超时 |

### 4.2 已知的平台差异

| 差异项 | Windows | macOS M1 | 影响 |
|--------|---------|----------|------|
| 定时器精度 | ~15.6ms | ~1ms | `time.Sleep` 精度较低，mock 延迟波动更大 |
| CPU 架构 | AMD64 | ARM64 | `simulateProcessing` CPU 部分性能不同 |
| Go 调度器 | 线程模型差异 | 更高效的 goroutine 调度 | `pinpointQueuing` 检测到的延迟分布不同 |
| 网络栈 | Windows TCP/IP | BSD 网络栈 | localhost 通信延迟微小差异 |
| Python asyncio | ProactorEventLoop | kqueue | Bridge 并发处理性能差异 |

### 4.3 缓解跨平台差异的建议

1. **使用相对指标对比**：不要比较绝对 RPS/延迟值，而是比较三种策略之间的**相对差异**
2. **设置环境变量**：运行测试前统一设置：

```bash
# macOS / Linux
export GOMAXPROCS=4
export BRIDGE_MAX_CONCURRENT=20
export BRIDGE_BASE_DELAY_MS=50
export BRIDGE_DELAY_JITTER_MS=15
export BRIDGE_OVERLOAD_SCALE=8.0

# Windows PowerShell
$env:GOMAXPROCS = "4"
$env:BRIDGE_MAX_CONCURRENT = "20"
$env:BRIDGE_BASE_DELAY_MS = "50"
$env:BRIDGE_DELAY_JITTER_MS = "15"
$env:BRIDGE_OVERLOAD_SCALE = "8.0"
```

3. **多次运行取均值**：使用 `-runs=3` 或更多，减少单次运行的随机波动
4. **使用 conda 一致环境**：

```bash
conda activate mcp-env
cd mcp_server && python -m server.bridge --port 9000 --workers 1
```

---

## 5. 运行命令参考

### 5.1 启动 Python Bridge

```bash
conda activate mcp-env
cd mcp_server
python -m server.bridge --port 9000 --workers 1
```

### 5.2 Loadtest（Mock 服务器，无需 Python）

```bash
# 快速验证
go run ./loadtest/ -mode=quick

# 完整测试（论文数据）
go run ./loadtest/ -mode=full -runs=3

# 消融实验
go run ./loadtest/ -mode=ablation -ablation-target=rajomon
```

### 5.3 Integration（真实 MCP 服务器）

```bash
# 快速验证
go run ./integration/ -mode=quick

# 完整测试
go run ./integration/ -mode=full -runs=3

# 指定策略
go run ./integration/ -mode=single -strategy=rajomon
```

---

## 6. 消融实验参数组（Loadtest 专用）

### A 组 — 价格策略

| 组名 | `priceStrategy` | 其他参数 |
|------|-----------------|----------|
| A1-step | `step` | 默认 |
| A2-expdecay | `expdecay` | 默认 |

### B 组 — 价格聚合

| 组名 | `priceAggregation` |
|------|-------------------|
| B1-maximal | `maximal` |
| B2-additive | `additive` |
| B3-mean | `mean` |

### C 组 — 价格步长

| 组名 | `priceStep` |
|------|------------|
| C1 | `2` |
| C2 | `5` (默认) |
| C3 | `12` |
| C4 | `20` |
| C5 | `30` |

### D 组 — 延迟阈值

| 组名 | `latencyThreshold` |
|------|-------------------|
| D1 | `500µs` |
| D2 | `2000µs` (默认) |
| D3 | `5000µs` |
| D4 | `10000µs` |

### E 组 — 更新频率

| 组名 | `priceUpdateRate` |
|------|------------------|
| E1 | `50ms` |
| E2 | `100ms` (默认) |
| E3 | `300ms` |

### F 组 — 预算分布

| 组名 | `Budgets` | `BudgetWeights` |
|------|-----------|----------------|
| F1-极端偏斜 | `[5, 200]` | `[0.9, 0.1]` |
| F2-均匀 | `[10, 50, 100]` | `[0.3, 0.4, 0.3]` (默认) |

### 静态限流消融

| 组名 | `StaticRateLimitQPS` | `StaticBurstSize` |
|------|---------------------|-------------------|
| strict | `20` | `20` |
| default | `30` | `30` |
| moderate | `60` | `60` |
| relaxed | `100` | `100` |

---

*最后更新: 2026-03-14*
