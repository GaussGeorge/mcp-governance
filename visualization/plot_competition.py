"""
plot_competition.py
多 Agent 竞争资源场景的可视化图表

生成图表:
  1. 公平性雷达图 - 各 Agent 成功次数分布 + Jain 指数标注
  2. 高低预算对比柱状图
  3. 竞争升级折线图 - Agent 数量 vs 拒绝率
  4. 多工具资源隔离对比图
  5. 突发竞争吞吐漏斗图
  6. 策略对比双轴图 - 成功数 + 效率
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

# 调色板
COLORS = ['#4C72B0', '#DD8452', '#55A868', '#C44E52', '#8172B3',
          '#937860', '#DA8BC3', '#8C8C8C', '#CCB974', '#64B5CD']


def plot_fairness(data: ParsedOutput, output_dir: str = "output"):
    """图1: 等预算公平性 - 各 Agent 成功次数柱状图 + Jain 指数"""
    tr = data.competition.get("TestCompetition_EqualBudget_Fairness")
    if not tr or not tr.agents:
        print("  [跳过] 无公平性测试数据")
        return

    fig, ax = plt.subplots(figsize=(10, 6))

    names = sorted(tr.agents.keys())
    successes = [tr.agents[n].success_calls for n in names]
    rejects = [tr.agents[n].rejected_calls for n in names]

    x = np.arange(len(names))
    width = 0.35

    bars1 = ax.bar(x - width / 2, successes, width, label='成功调用', color=COLORS[0], edgecolor='white')
    bars2 = ax.bar(x + width / 2, rejects, width, label='被拒绝', color=COLORS[3], edgecolor='white', alpha=0.7)

    ax.set_xlabel('Agent', fontsize=12)
    ax.set_ylabel('调用次数', fontsize=12)
    ax.set_title('等预算 Agent 公平竞争 - 成功/拒绝分布', fontsize=14, fontweight='bold')
    ax.set_xticks(x)
    ax.set_xticklabels(names, rotation=15)
    ax.legend(fontsize=11)

    # 标注 Jain 指数
    jain = tr.jain_fairness
    ax.text(0.98, 0.95, f'Jain 公平性指数: {jain:.4f}',
            transform=ax.transAxes, ha='right', va='top',
            fontsize=12, fontweight='bold',
            bbox=dict(boxstyle='round,pad=0.5', facecolor='lightyellow', edgecolor='orange', alpha=0.9))

    # 标注每个柱子的值
    for bar in bars1:
        ax.annotate(f'{int(bar.get_height())}',
                    xy=(bar.get_x() + bar.get_width() / 2, bar.get_height()),
                    xytext=(0, 3), textcoords='offset points', ha='center', fontsize=9)

    ax.grid(axis='y', alpha=0.3)
    plt.tight_layout()
    os.makedirs(output_dir, exist_ok=True)
    fig.savefig(os.path.join(output_dir, 'competition_fairness.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ competition_fairness.png")


def plot_unequal_budget(data: ParsedOutput, output_dir: str = "output"):
    """图2: 不等预算竞争 - 高/低预算 Agent 成功次数对比"""
    tr = data.competition.get("TestCompetition_UnequalBudget_HighBudgetAdvantage")
    if not tr or not tr.agents:
        print("  [跳过] 无不等预算测试数据")
        return

    fig, ax = plt.subplots(figsize=(10, 6))

    names = sorted(tr.agents.keys())
    successes = [tr.agents[n].success_calls for n in names]
    totals = [tr.agents[n].total_calls for n in names]

    colors = [COLORS[0] if 'rich' in n else COLORS[3] for n in names]

    bars = ax.bar(names, successes, color=colors, edgecolor='white', linewidth=1.2)

    # 标注
    for bar, total in zip(bars, totals):
        height = bar.get_height()
        rate = height / total * 100 if total > 0 else 0
        ax.annotate(f'{int(height)}\n({rate:.0f}%)',
                    xy=(bar.get_x() + bar.get_width() / 2, height),
                    xytext=(0, 5), textcoords='offset points', ha='center', fontsize=10)

    # 高/低预算均值线
    high_avg = np.mean([tr.agents[n].success_calls for n in names if 'rich' in n])
    low_avg = np.mean([tr.agents[n].success_calls for n in names if 'poor' in n])

    ax.axhline(y=high_avg, color=COLORS[0], linestyle='--', alpha=0.6, label=f'高预算均值: {high_avg:.1f}')
    ax.axhline(y=low_avg, color=COLORS[3], linestyle='--', alpha=0.6, label=f'低预算均值: {low_avg:.1f}')

    ax.set_xlabel('Agent', fontsize=12)
    ax.set_ylabel('成功调用次数', fontsize=12)
    ax.set_title('不等预算竞争 - 高预算 vs 低预算 Agent', fontsize=14, fontweight='bold')
    ax.legend(fontsize=11)
    ax.grid(axis='y', alpha=0.3)

    # 添加标签
    from matplotlib.patches import Patch
    legend_elements = [
        Patch(facecolor=COLORS[0], label=f'高预算 (均值 {high_avg:.1f})'),
        Patch(facecolor=COLORS[3], label=f'低预算 (均值 {low_avg:.1f})'),
    ]
    ax.legend(handles=legend_elements, fontsize=11, loc='upper right')

    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'competition_unequal_budget.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ competition_unequal_budget.png")


def plot_escalation(data: ParsedOutput, output_dir: str = "output"):
    """图3: 竞争升级 - Agent 数量增加 vs 拒绝率、成功率变化"""
    tr = data.competition.get("TestCompetition_Escalation")
    if not tr or not tr.phases:
        print("  [跳过] 无竞争升级测试数据")
        return

    fig, ax1 = plt.subplots(figsize=(10, 6))

    agent_counts = [p['agent_count'] for p in tr.phases]
    reject_rates = [p['reject_rate'] for p in tr.phases]
    successes = [p['success'] for p in tr.phases]
    totals = [p['total_requests'] for p in tr.phases]

    # 左轴: 拒绝率
    color1 = COLORS[3]
    line1 = ax1.plot(agent_counts, reject_rates, 'o-', color=color1, linewidth=2.5,
                     markersize=10, label='拒绝率 (%)', zorder=3)
    ax1.fill_between(agent_counts, reject_rates, alpha=0.15, color=color1)
    ax1.set_xlabel('并发 Agent 数量', fontsize=13, fontweight='bold')
    ax1.set_ylabel('拒绝率 (%)', fontsize=12, color=color1)
    ax1.tick_params(axis='y', labelcolor=color1)

    # 标注拒绝率
    for x, y in zip(agent_counts, reject_rates):
        ax1.annotate(f'{y:.1f}%', (x, y), textcoords='offset points',
                     xytext=(0, 12), ha='center', fontsize=10, fontweight='bold', color=color1)

    # 右轴: 成功数
    ax2 = ax1.twinx()
    color2 = COLORS[0]
    line2 = ax2.bar(agent_counts, successes, width=0.8, alpha=0.3, color=color2, label='成功请求数')
    ax2.set_ylabel('成功请求数', fontsize=12, color=color2)
    ax2.tick_params(axis='y', labelcolor=color2)

    ax1.set_title('竞争升级 - Agent 数量 vs 拒绝率', fontsize=14, fontweight='bold')
    ax1.set_xticks(agent_counts)

    # 合并图例
    lines1, labels1 = ax1.get_legend_handles_labels()
    lines2, labels2 = ax2.get_legend_handles_labels()
    ax1.legend(lines1 + lines2, labels1 + labels2, loc='upper left', fontsize=11)

    ax1.grid(axis='y', alpha=0.3)
    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'competition_escalation.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ competition_escalation.png")


def plot_multi_tool_isolation(data: ParsedOutput, output_dir: str = "output"):
    """图4: 多工具资源隔离对比"""
    tr = data.competition.get("TestCompetition_MultiTool_ResourceIsolation")
    if not tr or not tr.strategies:
        print("  [跳过] 无多工具隔离测试数据")
        return

    fig, ax = plt.subplots(figsize=(8, 5))

    names = [s['name'] for s in tr.strategies if 'success_rate' in s]
    rates = [s['success_rate'] for s in tr.strategies if 'success_rate' in s]

    if not names:
        print("  [跳过] 无多工具隔离成功率数据")
        plt.close(fig)
        return

    bars = ax.bar(names, rates, color=[COLORS[0], COLORS[1]], edgecolor='white', width=0.5)

    for bar in bars:
        height = bar.get_height()
        ax.annotate(f'{height:.1f}%', (bar.get_x() + bar.get_width() / 2, height),
                    xytext=(0, 5), textcoords='offset points', ha='center', fontsize=13, fontweight='bold')

    ax.set_ylabel('成功率 (%)', fontsize=12)
    ax.set_title('多工具资源隔离 - Alpha(快速) vs Beta(慢速)', fontsize=14, fontweight='bold')
    ax.set_ylim(0, 110)
    ax.axhline(y=100, color='gray', linestyle=':', alpha=0.5)
    ax.grid(axis='y', alpha=0.3)

    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'competition_multi_tool_isolation.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ competition_multi_tool_isolation.png")


def plot_strategy_comparison(data: ParsedOutput, output_dir: str = "output"):
    """图5: 策略对比 - 成功数 + 效率双轴图"""
    tr = data.competition.get("TestCompetition_StrategyComparison")
    if not tr or not tr.strategies:
        print("  [跳过] 无策略对比测试数据")
        return

    strats = [s for s in tr.strategies if 'efficiency' in s]
    if not strats:
        print("  [跳过] 无策略效率数据")
        return

    fig, ax1 = plt.subplots(figsize=(10, 6))

    names = [s['name'] for s in strats]
    successes = [s['success'] for s in strats]
    spents = [s['spent'] for s in strats]
    efficiencies = [s['efficiency'] for s in strats]

    x = np.arange(len(names))
    width = 0.35

    # 左轴: 成功数 + 消耗
    bars1 = ax1.bar(x - width / 2, successes, width, label='成功调用', color=COLORS[2], edgecolor='white')
    bars2 = ax1.bar(x + width / 2, [s / 50 for s in spents], width, label='消耗/50', color=COLORS[1], alpha=0.6, edgecolor='white')
    ax1.set_ylabel('次数', fontsize=12)
    ax1.set_xlabel('预算策略', fontsize=12)

    # 右轴: 效率
    ax2 = ax1.twinx()
    line = ax2.plot(x, efficiencies, 's-', color=COLORS[3], linewidth=2, markersize=10,
                    label='效率 (成功/千令牌)', zorder=5)
    ax2.set_ylabel('效率 (成功/千令牌)', fontsize=12, color=COLORS[3])
    ax2.tick_params(axis='y', labelcolor=COLORS[3])

    for i, eff in enumerate(efficiencies):
        ax2.annotate(f'{eff:.1f}', (x[i], eff), textcoords='offset points',
                     xytext=(0, 10), ha='center', fontsize=10, fontweight='bold', color=COLORS[3])

    ax1.set_xticks(x)
    ax1.set_xticklabels([n.replace('-agent', '') for n in names])
    ax1.set_title('预算策略对比 - 成功数与效率', fontsize=14, fontweight='bold')

    lines1, labels1 = ax1.get_legend_handles_labels()
    lines2, labels2 = ax2.get_legend_handles_labels()
    ax1.legend(lines1 + lines2, labels1 + labels2, loc='upper left', fontsize=10)

    ax1.grid(axis='y', alpha=0.3)
    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'competition_strategy_comparison.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ competition_strategy_comparison.png")


def plot_burst(data: ParsedOutput, output_dir: str = "output"):
    """图6: 突发竞争 - 吞吐饼图"""
    tr = data.competition.get("TestCompetition_BurstArrival")
    if not tr or tr.total_requests == 0:
        print("  [跳过] 无突发竞争测试数据")
        return

    fig, (ax1, ax2) = plt.subplots(1, 2, figsize=(12, 5))

    # 饼图: 成功 vs 拒绝
    success = tr.total_success
    rejected = tr.total_requests - success
    sizes = [success, rejected]
    labels = [f'成功\n{success}', f'拒绝\n{rejected}']
    colors = [COLORS[2], COLORS[3]]
    explode = (0.05, 0)

    wedges, texts, autotexts = ax1.pie(sizes, explode=explode, labels=labels, colors=colors,
                                        autopct='%1.1f%%', startangle=90,
                                        textprops={'fontsize': 12})
    for at in autotexts:
        at.set_fontweight('bold')
    ax1.set_title('突发竞争 - 请求处理结果', fontsize=13, fontweight='bold')

    # 指标卡片
    ax2.axis('off')
    info_text = (
        f"总请求数: {tr.total_requests}\n"
        f"成功数: {success}\n"
        f"拒绝数: {rejected}\n"
        f"有效吞吐率: {tr.success_rate:.1f}%\n"
        f"并发 Agent: 10\n"
        f"每 Agent 请求: 50"
    )
    ax2.text(0.5, 0.5, info_text, transform=ax2.transAxes, ha='center', va='center',
             fontsize=14,
             bbox=dict(boxstyle='round,pad=1', facecolor='lightyellow', edgecolor='gray'))
    ax2.set_title('关键指标', fontsize=13, fontweight='bold')

    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'competition_burst.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ competition_burst.png")


def plot_all_competition(data: ParsedOutput, output_dir: str = "output"):
    """生成全部竞争场景图表"""
    print("\n[*] 生成竞争场景图表...")
    os.makedirs(output_dir, exist_ok=True)
    plot_fairness(data, output_dir)
    plot_unequal_budget(data, output_dir)
    plot_escalation(data, output_dir)
    plot_multi_tool_isolation(data, output_dir)
    plot_strategy_comparison(data, output_dir)
    plot_burst(data, output_dir)


if __name__ == '__main__':
    data = get_sample_data()
    plot_all_competition(data, "output")
    print("\n完成! 图表已保存到 output/ 目录")
