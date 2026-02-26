# visualization/ - Agent 测试数据可视化工具包

将 `agent_test/` 下 Go 测试输出的指标数据转换为 **Matplotlib** 图表，覆盖三大场景：多 Agent 竞争、预算管理、多步推理链。

> **已验证** (2026-02-26): `conda activate comosvc` 环境下全部 20 张图表生成成功，零警告零错误。

## 文件结构

```
visualization/
├── run_and_plot.py            # 主入口 (运行测试+绘图 / 样例预览 / 文件解析)
├── parse_test_output.py       # 数据解析器 (正则提取 go test -v 输出 → ParsedOutput)
├── plot_competition.py        # 竞争场景图表 (6 张)
├── plot_budget.py             # 预算场景图表 (7 张)
├── plot_reasoning_chain.py    # 推理链图表 (6 张)
├── plot_dashboard.py          # 综合仪表板 (1 张, 2×3 子图)
├── requirements.txt           # Python 依赖: matplotlib, numpy
├── __init__.py                # 包初始化
├── README.md                  # 本文件
└── output/                    # 图表输出目录 (自动创建)
    ├── competition_*.png
    ├── budget_*.png
    ├── chain_*.png
    └── dashboard.png
```

| 文件 | 作用 |
|------|------|
| `run_and_plot.py` | **主入口**。支持运行测试+绘图、样例数据预览、从文件解析三种模式 |
| `parse_test_output.py` | **数据解析器**。用正则表达式从 `go test -v` 输出中提取结构化指标，并提供内置样例数据 |
| `plot_competition.py` | **竞争场景图表** (6 张)：公平性、不等预算、竞争升级、多工具隔离、突发竞争、策略对比 |
| `plot_budget.py` | **预算场景图表** (7 张)：分层对比、策略基准、价格侵蚀、自适应vs固定、生命周期、边界条件、守恒验证 |
| `plot_reasoning_chain.py` | **推理链图表** (6 张)：场景总览、重试效果、并行链、策略对比、批量实验、竞争影响 |
| `plot_dashboard.py` | **综合仪表板** (1 张)：将三大场景核心指标汇聚为 2×3 的子图组合 |
| `requirements.txt` | Python 依赖：matplotlib, numpy |

## 环境准备

### 方式 1: 使用 conda 环境 (推荐)

```bash
# 激活已有的 comosvc 环境 (Python 3.12, 含 numpy)
conda activate comosvc
pip install matplotlib
```

### 方式 2: 直接 pip 安装

```bash
cd visualization
pip install -r requirements.txt
```

### 环境要求

| 依赖 | 最低版本 | 说明 |
|------|---------|------|
| Python | 3.8+ | 推荐 3.12 |
| matplotlib | 3.7.0+ | 图表绑定 |
| numpy | 1.24.0+ | 数值计算 |
| Go | 1.23.0+ | 仅在运行测试模式下需要 |

## 使用方式

### 快速预览（使用内置样例数据，无需运行 Go 测试）

```bash
cd visualization
python run_and_plot.py --sample
```

### 运行测试并生成图表

```bash
# 在项目根目录
cd visualization
python run_and_plot.py
```

### 从已有测试输出生成图表

```bash
# 先保存测试输出
go test ./agent_test/ -v -timeout 2m > test_output.txt 2>&1

# 再从文件生成图表
cd visualization
python run_and_plot.py --input ../test_output.txt
```

### 按类别生成

```bash
# 只生成竞争场景图表
python run_and_plot.py --sample --only competition

# 只生成预算管理图表
python run_and_plot.py --sample --only budget

# 只生成推理链图表
python run_and_plot.py --sample --only chain

# 只生成综合仪表板
python run_and_plot.py --sample --only dashboard
```

### 高级选项

```bash
# 指定输出目录
python run_and_plot.py --sample --output ./my_charts

# 过滤特定测试
python run_and_plot.py --filter "TestCompetition"

# 保存原始测试输出到文件
python run_and_plot.py --save-output

# 不生成仪表板
python run_and_plot.py --sample --no-dashboard
```

## 生成的图表清单

### 竞争场景 (6 张)
| 文件名 | 内容 |
|--------|------|
| `competition_fairness.png` | 等预算 Agent 公平竞争 - 成功/拒绝分布 + Jain 指数 |
| `competition_unequal_budget.png` | 高预算 vs 低预算 Agent 成功率对比 |
| `competition_escalation.png` | Agent 数量升级 (2→5→10→20) vs 拒绝率折线图 |
| `competition_multi_tool_isolation.png` | Alpha(快速) vs Beta(慢速) 工具隔离成功率 |
| `competition_strategy_comparison.png` | 4 策略成功数+效率双轴对比 |
| `competition_burst.png` | 10 Agent 突发竞争吞吐饼图 |

### 预算管理 (7 张)
| 文件名 | 内容 |
|--------|------|
| `budget_tiers.png` | 低/中/高预算成功率 + 调用数对比 |
| `budget_strategy_benchmark.png` | 4 策略效率基准柱状图 + 排名 |
| `budget_price_erosion.png` | 5 阶段价格阶梯侵蚀预算双轴图 |
| `budget_adaptive_vs_fixed.png` | 价格波动环境: 自适应 vs 固定策略 |
| `budget_lifecycle.png` | 10 阶段预算生命周期消耗曲线 |
| `budget_edge_cases.png` | 零预算/最小预算/超大预算边界对比 |
| `budget_conservation.png` | 全局令牌总量守恒堆叠验证图 |

### 推理链 (6 张)
| 文件名 | 内容 |
|--------|------|
| `chain_overview.png` | 11 个场景链完成率水平柱状图 |
| `chain_retry_effect.png` | 有/无重试的步骤完成对比 |
| `chain_parallel.png` | 5 Agent 并行推理链完成情况 |
| `chain_strategy_comparison.png` | 推理链下 4 策略步骤+效率对比 |
| `chain_batch_experiment.png` | N=20 批量实验统计 (饼图+指标卡+进度条) |
| `chain_competition_impact.png` | 竞争干扰对推理链完成率的影响 |

### 综合仪表板 (1 张)
| 文件名 | 内容 |
|--------|------|
| `dashboard.png` | 2×3 六宫格综合仪表板 |

## 数据流

```
go test ./agent_test/ -v     parse_test_output.py      plot_*.py          output/
┌────────────────────┐     ┌───────────────────┐    ┌──────────────┐    ┌──────────┐
│ t.Logf() 输出      │────>│ 正则提取结构化数据 │───>│ Matplotlib   │───>│ .png 图表│
│ Agent 指标行       │     │ ParsedOutput       │    │ 图表函数     │    │          │
│ 摘要行/阶段行      │     │  .competition{}    │    │              │    │          │
│ 策略效率行         │     │  .budget{}         │    │              │    │          │
│ 链完成行           │     │  .chain{}          │    │              │    │          │
└────────────────────┘     └───────────────────┘    └──────────────┘    └──────────┘
```

### 解析器支持的指标行格式

| 格式 | 正则匹配 | 示例 |
|------|---------|------|
| Agent 指标 | `[name] 调用=N 成功=N 拒绝=N ...` | `[agent-1] 调用=40 成功=35 拒绝=5 重试=2 预算消耗=800 链完成=true 步骤=3` |
| 摘要行 | `总请求: N, 成功: N, 拒绝: N` | `总请求: 200, 成功: 150, 拒绝: 50` |
| 阶段行 | `阶段N: 价格=N 调用=N ...` | `阶段1: 价格=10 调用=10 成功=10 拒绝=0 剩余预算=2700` |
| 策略效率 | `[策略名] 成功=N 消耗=N 效率=N` | `[Adaptive] 成功=28 消耗=900 效率=31.11` |
| Jain 公平性 | `Jain 公平性指数: N.NNNN` | `Jain 公平性指数: 0.9521` |
| 链完成 | `完成=N/N 链完成=true/false` | `完成=3/5 链完成=true` |
| 竞争升级 | `Agent数=N: 总请求=N ...` | `Agent数=10: 总请求=200 成功=150 拒绝=50 拒绝率=25.0%` |

## 测试运行输出示例

```
============================================================
  Agent 测试数据可视化工具
  MCP Governance - agent_test 测试结果图表生成器
============================================================

[1/2] 使用内置样例数据...
  加载了 21 个测试的样例数据

[2/2] 生成图表到: output/

[*] 生成竞争场景图表...
  ✓ competition_fairness.png
  ✓ competition_unequal_budget.png
  ✓ competition_escalation.png
  ✓ competition_multi_tool_isolation.png
  ✓ competition_strategy_comparison.png
  ✓ competition_burst.png

[*] 生成预算管理图表...
  ✓ budget_tiers.png              ...共 7 张

[*] 生成推理链图表...
  ✓ chain_overview.png            ...共 6 张

[*] 生成综合仪表板...
  ✓ dashboard.png

============================================================
  完成! 共生成 20 张图表
  输出目录: .../visualization/output
  耗时: 7.8s
============================================================
```
