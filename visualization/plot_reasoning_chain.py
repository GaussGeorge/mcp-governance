"""
plot_reasoning_chain.py
Agent 多步推理链场景的可视化图表

生成图表:
  1. 推理链基础完成热力图 - 各测试场景的链完成情况
  2. 重试效果对比 - 有/无重试的步骤完成数
  3. 链中动态涨价影响 - 步骤完成 vs 价格变化
  4. 多 Agent 并行推理链 - 各 Agent 链完成情况
  5. 策略对比 - 推理链下 4 策略的效率
  6. 批量实验统计分布
  7. 竞争对推理链的影响
  8. 综合完成率总览
"""

import os
import numpy as np
import matplotlib.pyplot as plt
import matplotlib
from typing import Optional

from parse_test_output import ParsedOutput, get_sample_data

# 中文字体配置
matplotlib.rcParams['font.sans-serif'] = ['SimHei', 'Microsoft YaHei', 'DejaVu Sans']
matplotlib.rcParams['axes.unicode_minus'] = False
matplotlib.rcParams['figure.dpi'] = 120

COLORS = ['#4C72B0', '#DD8452', '#55A868', '#C44E52', '#8172B3',
          '#937860', '#DA8BC3', '#8C8C8C', '#CCB974', '#64B5CD']


def plot_chain_overview(data: ParsedOutput, output_dir: str = "output"):
    """图1: 推理链场景总览 - 各场景链完成率热力图"""
    chain_tests = data.chain
    if not chain_tests:
        print("  [跳过] 无推理链测试数据")
        return

    fig, ax = plt.subplots(figsize=(12, 6))

    # 收集各测试的完成信息
    test_names = []
    completions = []
    totals = []

    name_map = {
        'TestChain_Linear_Basic': '基础线性链',
        'TestChain_DependencyBreak': '依赖断裂',
        'TestChain_WithRetries': '带重试',
        'TestChain_LongChain_Stability': '10步长链',
        'TestChain_DynamicPricing_MidChain': '链中涨价',
        'TestChain_MultiAgent_ParallelChains': '多Agent并行链',
        'TestChain_BranchingWithOptionalSteps': '分支推理',
        'TestChain_BudgetStrategy_Comparison': '策略对比',
        'TestChain_BatchExperiment': '批量实验',
        'TestChain_UnderCompetition': '竞争干扰',
        'TestChain_FanOutFanIn': 'Fan-out/Fan-in',
    }

    for test_name, tr in sorted(chain_tests.items()):
        display_name = name_map.get(test_name, test_name.replace('TestChain_', ''))
        test_names.append(display_name)

        if tr.chain_total_count > 0:
            completions.append(tr.chain_completed_count)
            totals.append(tr.chain_total_count)
        else:
            completions.append(1 if tr.chain_completion_rate > 0 else 0)
            totals.append(1)

    # 计算完成率
    rates = [c / t * 100 if t > 0 else 0 for c, t in zip(completions, totals)]

    # 水平柱状图
    y = np.arange(len(test_names))
    colors = []
    for r in rates:
        if r >= 80:
            colors.append(COLORS[2])  # 绿色
        elif r >= 50:
            colors.append(COLORS[1])  # 橙色
        else:
            colors.append(COLORS[3])  # 红色

    bars = ax.barh(y, rates, color=colors, edgecolor='white', height=0.6)

    for bar, rate, comp, total in zip(bars, rates, completions, totals):
        w = bar.get_width()
        label = f'{rate:.0f}% ({comp}/{total})'
        ax.text(max(w + 1, 5), bar.get_y() + bar.get_height() / 2,
                label, va='center', fontsize=10, fontweight='bold')

    ax.set_yticks(y)
    ax.set_yticklabels(test_names)
    ax.set_xlabel('链完成率 (%)', fontsize=12)
    ax.set_title('推理链场景总览 - 链完成率', fontsize=14, fontweight='bold')
    ax.set_xlim(0, 120)
    ax.axvline(x=80, color='green', linestyle=':', alpha=0.4, label='80% 阈值')
    ax.axvline(x=50, color='orange', linestyle=':', alpha=0.4, label='50% 阈值')
    ax.legend(fontsize=10, loc='lower right')
    ax.grid(axis='x', alpha=0.3)
    ax.invert_yaxis()

    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'chain_overview.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ chain_overview.png")


def plot_retry_effect(data: ParsedOutput, output_dir: str = "output"):
    """图2: 重试效果对比"""
    tr = data.chain.get("TestChain_WithRetries")
    if not tr or not tr.strategies:
        print("  [跳过] 无重试对比数据")
        return

    retries = tr.strategies
    if len(retries) < 2:
        print("  [跳过] 重试对比数据不足")
        return

    fig, ax = plt.subplots(figsize=(8, 5))

    names = [r['name'] for r in retries]
    steps = [r.get('steps', 0) for r in retries]
    calls = [r.get('calls', 0) for r in retries]
    completed = [r.get('chain_completed', False) for r in retries]

    x = np.arange(len(names))
    width = 0.3

    bars1 = ax.bar(x - width / 2, steps, width, label='完成步骤', color=COLORS[2])
    bars2 = ax.bar(x + width / 2, calls, width, label='总调用 (含重试)', color=COLORS[1], alpha=0.7)

    # 标注链完成状态
    for i, (comp, step) in enumerate(zip(completed, steps)):
        symbol = 'PASS' if comp else 'FAIL'
        color = 'green' if comp else 'red'
        ax.annotate(f'{symbol}', (x[i] - width / 2, step),
                    xytext=(0, 8), textcoords='offset points', ha='center',
                    fontsize=16, fontweight='bold', color=color)

    ax.set_xticks(x)
    ax.set_xticklabels(names)
    ax.set_ylabel('次数/步数', fontsize=12)
    ax.set_title('重试机制对推理链完成的影响', fontsize=14, fontweight='bold')
    ax.legend(fontsize=11)
    ax.grid(axis='y', alpha=0.3)

    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'chain_retry_effect.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ chain_retry_effect.png")


def plot_parallel_chains(data: ParsedOutput, output_dir: str = "output"):
    """图3: 多 Agent 并行推理链"""
    tr = data.chain.get("TestChain_MultiAgent_ParallelChains")
    if not tr or not tr.agents:
        print("  [跳过] 无多Agent并行链数据")
        return

    fig, ax = plt.subplots(figsize=(10, 6))

    names = sorted(tr.agents.keys())
    steps = [tr.agents[n].steps_completed for n in names]
    completed = [tr.agents[n].chain_completed for n in names]
    spent = [tr.agents[n].budget_spent for n in names]

    x = np.arange(len(names))
    colors = [COLORS[2] if c else COLORS[3] for c in completed]

    bars = ax.bar(x, steps, color=colors, edgecolor='white', width=0.5)

    # 标注
    for i, (bar, comp, sp) in enumerate(zip(bars, completed, spent)):
        h = bar.get_height()
        status = '链完成' if comp else '链中断'
        ax.annotate(f'{status}\n消耗={sp}', (bar.get_x() + bar.get_width() / 2, h),
                    xytext=(0, 5), textcoords='offset points', ha='center', fontsize=9)

    # 总链长标线
    chain_len = 3  # plan → execute → verify
    ax.axhline(y=chain_len, color='gray', linestyle='--', alpha=0.5, label=f'链总长={chain_len}')

    ax.set_xticks(x)
    ax.set_xticklabels(names, rotation=15)
    ax.set_ylabel('完成步骤数', fontsize=12)
    ax.set_title(f'多 Agent 并行推理链 (链完成率: {tr.chain_completed_count}/{tr.chain_total_count})',
                 fontsize=14, fontweight='bold')
    ax.legend(fontsize=11)
    ax.grid(axis='y', alpha=0.3)

    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'chain_parallel.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ chain_parallel.png")


def plot_chain_strategy_comparison(data: ParsedOutput, output_dir: str = "output"):
    """图4: 推理链模式下 4 策略对比"""
    tr = data.chain.get("TestChain_BudgetStrategy_Comparison")
    if not tr or not tr.strategies:
        print("  [跳过] 无推理链策略对比数据")
        return

    strats = [s for s in tr.strategies if 'efficiency' in s]
    if not strats:
        print("  [跳过] 无推理链策略效率数据")
        return

    fig, (ax1, ax2) = plt.subplots(1, 2, figsize=(13, 6))

    names = [s['name'] for s in strats]
    steps = [s['success'] for s in strats]
    spents = [s['spent'] for s in strats]
    effs = [s['efficiency'] for s in strats]

    # 左图: 完成步骤 + 消耗
    x = np.arange(len(names))
    width = 0.35

    colors_left = [COLORS[0], COLORS[1], COLORS[2], COLORS[4]][:len(names)]
    bars = ax1.bar(x, steps, width * 2, color=colors_left, edgecolor='white')

    for bar, sp in zip(bars, spents):
        h = bar.get_height()
        ax1.annotate(f'{int(h)} 步\n消耗={sp}', (bar.get_x() + bar.get_width() / 2, h),
                     xytext=(0, 5), textcoords='offset points', ha='center', fontsize=10)

    ax1.set_xticks(x)
    ax1.set_xticklabels(names)
    ax1.set_ylabel('完成步骤数', fontsize=12)
    ax1.set_title('推理链 - 策略步骤完成数', fontsize=14, fontweight='bold')
    ax1.axhline(y=5, color='gray', linestyle='--', alpha=0.5, label='链总长=5')
    ax1.legend(fontsize=10)
    ax1.grid(axis='y', alpha=0.3)

    # 右图: 效率对比 (步/千令牌)
    bars2 = ax2.bar(x, effs, width * 2, color=colors_left, edgecolor='white', alpha=0.8)
    for bar in bars2:
        h = bar.get_height()
        ax2.annotate(f'{h:.1f}', (bar.get_x() + bar.get_width() / 2, h),
                     xytext=(0, 5), textcoords='offset points', ha='center', fontsize=12, fontweight='bold')

    ax2.set_xticks(x)
    ax2.set_xticklabels(names)
    ax2.set_ylabel('效率 (步/千令牌)', fontsize=12)
    ax2.set_title('推理链 - 策略效率对比', fontsize=14, fontweight='bold')
    ax2.grid(axis='y', alpha=0.3)

    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'chain_strategy_comparison.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ chain_strategy_comparison.png")


def plot_batch_experiment(data: ParsedOutput, output_dir: str = "output"):
    """图5: 批量实验统计"""
    tr = data.chain.get("TestChain_BatchExperiment")
    if not tr:
        print("  [跳过] 无批量实验数据")
        return

    fig, axes = plt.subplots(1, 3, figsize=(15, 5))

    # 子图 1: 链完成率饼图
    ax1 = axes[0]
    if tr.chain_total_count > 0:
        completed = tr.chain_completed_count
        failed = tr.chain_total_count - completed
        sizes = [completed, failed]
        labels = [f'完成\n{completed}', f'未完成\n{failed}']
        colors = [COLORS[2], COLORS[3]]
        wedges, texts, autotexts = ax1.pie(sizes, labels=labels, colors=colors,
                                            autopct='%1.1f%%', startangle=90,
                                            textprops={'fontsize': 11})
        for at in autotexts:
            at.set_fontweight('bold')
    ax1.set_title(f'链完成率 (N={tr.chain_total_count})', fontsize=13, fontweight='bold')

    # 子图 2: 平均步骤完成仪表
    ax2 = axes[1]
    ax2.axis('off')
    chain_len = 5
    info = (
        f"批量实验 (N={tr.chain_total_count})\n"
        f"----------------\n"
        f"链完成率: {tr.chain_completion_rate:.1f}%\n"
        f"平均步骤: {tr.avg_steps:.1f} / {chain_len}\n"
        f"平均消耗: {tr.avg_budget_spent:.0f} 令牌\n"
        f"----------------\n"
        f"链长度: {chain_len}\n"
        f"服务端价格: 15"
    )
    ax2.text(0.5, 0.5, info, transform=ax2.transAxes, ha='center', va='center',
             fontsize=13,
             bbox=dict(boxstyle='round,pad=0.8', facecolor='lightyellow', edgecolor='gray'))
    ax2.set_title('实验参数与结果', fontsize=13, fontweight='bold')

    # 子图 3: 步骤完成率进度条
    ax3 = axes[2]
    step_rate = tr.avg_steps / chain_len * 100
    budget_rate = tr.avg_budget_spent / 400 * 100  # 预算 400

    metrics = ['链完成', '平均步骤', '预算利用']
    values = [tr.chain_completion_rate, step_rate, budget_rate]
    bar_colors = []
    for v in values:
        if v >= 80:
            bar_colors.append(COLORS[2])
        elif v >= 50:
            bar_colors.append(COLORS[1])
        else:
            bar_colors.append(COLORS[3])

    bars = ax3.barh(metrics, values, color=bar_colors, edgecolor='white', height=0.5)
    for bar in bars:
        w = bar.get_width()
        ax3.text(w + 1, bar.get_y() + bar.get_height() / 2,
                 f'{w:.1f}%', va='center', fontsize=11, fontweight='bold')

    ax3.set_xlim(0, 120)
    ax3.set_xlabel('%', fontsize=12)
    ax3.set_title('关键指标达成率', fontsize=13, fontweight='bold')
    ax3.grid(axis='x', alpha=0.3)

    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'chain_batch_experiment.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ chain_batch_experiment.png")


def plot_competition_impact(data: ParsedOutput, output_dir: str = "output"):
    """图6: 竞争环境对推理链完成的影响"""
    # 对比: 无竞争 vs 有竞争的链完成率
    no_comp = data.chain.get("TestChain_MultiAgent_ParallelChains")
    with_comp = data.chain.get("TestChain_UnderCompetition")

    if not no_comp and not with_comp:
        print("  [跳过] 无竞争影响对比数据")
        return

    fig, ax = plt.subplots(figsize=(8, 5))

    names = []
    rates = []
    colors = []

    if no_comp and no_comp.chain_total_count > 0:
        rate = no_comp.chain_completed_count / no_comp.chain_total_count * 100
        names.append('无干扰\n(多Agent并行链)')
        rates.append(rate)
        colors.append(COLORS[2])

    if with_comp and with_comp.chain_total_count > 0:
        rate = with_comp.chain_completed_count / with_comp.chain_total_count * 100
        names.append('有干扰Agent\n(竞争环境)')
        rates.append(rate)
        colors.append(COLORS[3])

    if not names:
        print("  [跳过] 无有效竞争对比数据")
        plt.close(fig)
        return

    bars = ax.bar(names, rates, color=colors, edgecolor='white', width=0.4)

    for bar in bars:
        h = bar.get_height()
        ax.annotate(f'{h:.0f}%', (bar.get_x() + bar.get_width() / 2, h),
                    xytext=(0, 6), textcoords='offset points', ha='center', fontsize=14, fontweight='bold')

    ax.set_ylabel('链完成率 (%)', fontsize=12)
    ax.set_title('竞争干扰对推理链完成率的影响', fontsize=14, fontweight='bold')
    ax.set_ylim(0, 120)
    ax.grid(axis='y', alpha=0.3)

    # 下降幅度标注
    if len(rates) == 2:
        drop = rates[0] - rates[1]
        ax.annotate(f'↓ 下降 {drop:.0f}%',
                    xy=(0.5, (rates[0] + rates[1]) / 2),
                    fontsize=13, ha='center', color=COLORS[3], fontweight='bold',
                    arrowprops=dict(arrowstyle='->', color=COLORS[3]))

    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'chain_competition_impact.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ chain_competition_impact.png")


def plot_all_chain(data: ParsedOutput, output_dir: str = "output"):
    """生成全部推理链图表"""
    print("\n[*] 生成推理链图表...")
    os.makedirs(output_dir, exist_ok=True)
    plot_chain_overview(data, output_dir)
    plot_retry_effect(data, output_dir)
    plot_parallel_chains(data, output_dir)
    plot_chain_strategy_comparison(data, output_dir)
    plot_batch_experiment(data, output_dir)
    plot_competition_impact(data, output_dir)


if __name__ == '__main__':
    data = get_sample_data()
    plot_all_chain(data, "output")
    print("\n完成! 图表已保存到 output/ 目录")
