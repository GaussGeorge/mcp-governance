以下是模拟 Agent 场景测试的**全部测试步骤**：

---

## 一、环境准备

### 1.1 编译验证

```powershell
cd c:\\Users\\86158\\Desktop\\go_workspace\\ra-annotion-demo
go build ./agenttest/
go vet ./agenttest/
```

### 1.2 确认端口可用

Agent 场景测试使用端口：

- **9101** — 无治理服务器
- **9102** — 静态限流服务器
- **9103** — Rajomon 动态定价服务器

确保这三个端口未被占用。

---

## 二、测试步骤总览

| 步骤 | 测试模式 | 命令 | 用途 | 预计时长 |
| --- | --- | --- | --- | --- |
| 1 | quick | `-mode=quick` | 快速验证三策略对比 | ~1.5 分钟 |
| 2 | single | `-mode=single` | 逐个策略详细验证 | ~2.5 分钟/策略 |
| 3 | burst | `-mode=burst` | 突发负载三策略对比 | ~7 分钟 |
| 4 | poisson | `-mode=poisson` | 泊松负载三策略对比 | ~10 分钟 |
| 5 | sine | `-mode=sine` | 正弦负载三策略对比 | ~10 分钟 |
| 6 | full | `-mode=full -runs=3` | 论文级完整测试 | ~30 分钟 |
| 7 | 可视化 | `plot_agent.py` | 生成对比图表 | ~30 秒 |

---

## 三、详细测试步骤

### 步骤 1：快速验证测试（Quick）

**目的**：确认三种策略在 Agent 多步骤任务场景下均可正常运行，验证代码正确性。

**方式 A — CLI**：

```powershell
go run ./agenttest/ -mode=quick
```

**方式 B — go test**：

```powershell
cd agenttest
go test -v -run TestAgentQuickComparison -timeout 15m
```

**配置参数**（内部缩减）：

- Agent 数：50
- 阶段：warmup(10s, 2任务/s) → normal(15s, 5) → burst(15s, 20) → overload(20s, 40) → recovery(10s, 2)
- 总时长：~70 秒

**验证要点**：

- [x]  三种策略均有任务产出（`TotalTasks > 0`）
- [x]  Rajomon 体现预算差异化（budget 100 成功率 > budget 30 > budget 10）
- [x]  无治理策略在过载阶段出现大量 `step_failed`
- [x]  静态限流策略出现 `step_rejected`
- [x]  CSV 文件正常导出到 `agenttest/output/test_agent_quick/`

---

### 步骤 2：单策略详细测试（Single）

**目的**：逐一验证每种策略的详细行为特征。

**2a — 测试 Rajomon 动态定价**：

```powershell
go run ./agenttest/ -mode=single -strategy=rajomon -agents=100
```

**验证要点**：

- 预算差异化：budget 100 任务成功率显著高于 budget 10
- 过载阶段动态提价，自动排除低预算请求
- 恢复阶段价格回落，成功率恢复

**2b — 测试无治理策略**：

```powershell
go run ./agenttest/ -mode=single -strategy=no_governance -agents=100
```

**验证要点**：

- 过载阶段崩溃：步骤成功率大幅下降
- `step_failed` 为主要失败原因
- 所有预算组受损相当（无差异保护）

**2c — 测试静态限流策略**：

```powershell
go run ./agenttest/ -mode=single -strategy=static_rate_limit -agents=100 -rate-limit=30
```

**验证要点**：

- `step_rejected` 为主要失败原因
- 预算差异化弱（限流不区分预算）
- 成功通过的请求延迟稳定

**go test 等效方式**：

```powershell
cd agenttest
go test -v -run TestAgentSingleStrategy/rajomon -timeout 10m
go test -v -run TestAgentSingleStrategy/no_governance -timeout 10m
go test -v -run TestAgentSingleStrategy/static_rate_limit -timeout 10m
```

---

### 步骤 3：突发负载对比测试（Burst）

**目的**：在最核心的突发负载场景下对比三种策略。

```powershell
go run ./agenttest/ -mode=burst -agents=100 -runs=1
```

**go test 方式**：

```powershell
cd agenttest
go test -v -run TestAgentBurstLoad -timeout 15m
```

**默认负载阶段**：

| 阶段 | 持续时间 | 任务到达率 |
| --- | --- | --- |
| warmup | 20s | 2 任务/s |
| normal | 30s | 5 任务/s |
| burst | 30s | 30 任务/s |
| overload | 40s | 50 任务/s |
| recovery | 20s | 3 任务/s |

**验证要点**：

- 三策略对比表自动打印
- Rajomon 任务成功率 > 静态限流 > 无治理
- Rajomon 在过载阶段仍保护高预算 Agent
- 汇总 CSV 导出到 `agenttest/output/test_agent_burst/`

---

### 步骤 4：泊松负载对比测试（Poisson）

**目的**：验证平稳随机负载下的策略行为。

```powershell
go run ./agenttest/ -mode=poisson -agents=100 -runs=1
```

**go test 方式**：

```powershell
cd agenttest
go test -v -run TestAgentPoissonLoad -timeout 15m
```

**配置**：平均任务到达率 10 任务/s，持续 3 分钟。

**验证要点**：

- 泊松负载下三策略表现差异
- 无明显过载时，三策略差异可能较小
- 观察尾部延迟（P99）差异

---

### 步骤 5：正弦波负载对比测试（Sine）

**目的**：验证周期性波动负载下的策略适应能力。

```powershell
go run ./agenttest/ -mode=sine -agents=100 -runs=1
```

**配置**：基础率 10 + 振幅 15（范围 0~25 任务/s），周期 1 分钟，持续 3 分钟。

**验证要点**：

- 周期性高峰时，Rajomon 动态提价保护
- 波谷时价格回落，低预算 Agent 可通过
- 观察各策略的吞吐量稳定性

---

### 步骤 6：完整论文级测试（Full）

**目的**：生成可用于论文数据的完整测试结果，每策略重复 3 次取平均。

```powershell
go run ./agenttest/ -mode=full -runs=3 -agents=100
```

**执行流程**：

1. 依次运行三种策略 × 3 次 = 共 9 轮测试
2. 每轮间隔 2 秒冷却
3. 自动导出：
    - 步骤级 CSV（每步骤一行，包含时间戳、Agent ID、预算、工具类型、延迟、结果等）
    - 任务级 CSV（每任务一行，包含总步骤数、完成步骤数、总耗时、预算消耗等）
    - 汇总 CSV（每策略一行，包含所有聚合指标）

**验证要点**：

- 3 次运行结果的一致性（标准差较小）
- Rajomon 在所有运行中均体现预算差异化
- 汇总表中三策略指标可直接引用

---

### 步骤 7：参数调优实验（可选）

**7a — 调整 Rajomon 价格步长**：

```powershell
go run ./agenttest/ -mode=burst -price-step=2      # 价格步长 2（保守）
go run ./agenttest/ -mode=burst -price-step=10     # 价格步长 10（激进）
```

**7b — 调整延迟阈值**：

```powershell
go run ./agenttest/ -mode=burst -latency-threshold=1000us   # 更敏感
go run ./agenttest/ -mode=burst -latency-threshold=5000us   # 更宽松
```

**7c — 调整服务端容量**：

```powershell
go run ./agenttest/ -mode=burst -max-concurrency=20   # 容量缩小
go run ./agenttest/ -mode=burst -max-concurrency=100  # 容量放大
```

**7d — 调整任务复杂度**：

```powershell
go run ./agenttest/ -mode=burst -min-steps=3 -max-steps=8   # 更复杂的任务
```

---

### 步骤 8：生成可视化图表

```powershell
cd c:\\Users\\86158\\Desktop\\go_workspace\\ra-annotion-demo
python visualization/plot_agent.py --input agenttest/output/test_agent_burst --output visualization/figures/test_agent_burst
```

**生成的 8 张图表**：

1. **预算组任务成功率对比** — 验证预算差异化
2. **优先级组任务成功率对比** — 验证优先级保护
3. **阶段任务成功率对比** — 验证过载/恢复行为
4. **任务持续时间 CDF** — 尾部延迟分布
5. **步骤延迟 CDF** — 请求级延迟
6. **失败原因分布** — step_failed vs step_rejected vs budget_exhausted
7. **预算 vs 完成步骤数** — 预算利用效率
8. **工具类型成功率** — 不同开销工具的差异

---

## 四、输出文件结构

```
agenttest/output/
├── test_agent_quick/       ← 步骤1
│   ├── no_governance_burst_step_run1_*.csv
│   ├── no_governance_burst_task_run1_*.csv
│   ├── static_rate_limit_burst_step_run1_*.csv
│   ├── static_rate_limit_burst_task_run1_*.csv
│   ├── rajomon_burst_step_run1_*.csv
│   ├── rajomon_burst_task_run1_*.csv
│   └── summary_*.csv
├── test_agent_single/      ← 步骤2
├── test_agent_burst/       ← 步骤3
├── test_agent_poisson/     ← 步骤4
├── test_agent_sine/        ← 步骤5
└── test_agent_full/        ← 步骤6
    ├── *_step_run1_*.csv / *_step_run2_*.csv / *_step_run3_*.csv
    ├── *_task_run1_*.csv / *_task_run2_*.csv / *_task_run3_*.csv
    └── summary_*.csv
```

**CSV 字段说明**：

| CSV 类型 | 关键字段 |
| --- | --- |
| step 级 | timestamp, agent_id, task_id, step_index, tool_type, budget, priority, latency_ms, status, phase, price |
| task 级 | task_id, agent_id, budget, priority, total_steps, completed_steps, success, total_duration_ms, budget_spent |
| summary 级 | strategy, total_tasks, task_success_rate, budget_10/30/100_success_rate, p50/p95/p99_latency_ms, throughput_rps |

---

## 五、预期结果参考（基线）

基于已验证的测试运行（5 agents, max-concurrency=20）：

| 指标 | 无治理 | 静态限流 | Rajomon |
| --- | --- | --- | --- |
| 任务成功率 | ~12% | ~19% | **~42%** |
| 主要失败原因 | step_failed | step_rejected | budget_exhausted |
| Budget 10 成功率 | ~10% | ~16% | ~27% |
| Budget 100 成功率 | ~13% | ~21% | **~66%** |
| 过载阶段成功率 | ~6% | ~14% | ~30%+ |

**核心结论**：Rajomon 在过载下通过动态提价实现预算差异化保护，高预算 Agent 成功率远高于低预算，而无治理和静态限流无法提供此保障。

---

## 六、推荐测试顺序

```
步骤1 (quick)  ← 验证代码正确性，约2分钟
    ↓
步骤2 (single) ← 逐策略详细观察，约8分钟
    ↓
步骤3 (burst)  ← 核心对比实验，约7分钟
    ↓
步骤4 (poisson) + 步骤5 (sine) ← 补充实验
    ↓
步骤6 (full -runs=3) ← 论文数据
    ↓
步骤7 (参数调优) ← 消融实验
    ↓
步骤8 (可视化) ← 生成图表
```