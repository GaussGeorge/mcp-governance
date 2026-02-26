"""
plot_dashboard.py
综合仪表板 - 将三大场景的核心指标汇聚到一张大图中

生成:
  - dashboard.png: 3x2 或 2x3 的子图组合
"""

import os
import numpy as np
import matplotlib.pyplot as plt
import matplotlib
from matplotlib.gridspec import GridSpec

from parse_test_output import ParsedOutput, get_sample_data

# 中文字体配置
matplotlib.rcParams['font.sans-serif'] = ['SimHei', 'Microsoft YaHei', 'DejaVu Sans']
matplotlib.rcParams['axes.unicode_minus'] = False
matplotlib.rcParams['figure.dpi'] = 120

COLORS = ['#4C72B0', '#DD8452', '#55A868', '#C44E52', '#8172B3',
          '#937860', '#DA8BC3', '#8C8C8C', '#CCB974', '#64B5CD']


def plot_dashboard(data: ParsedOutput, output_dir: str = "output"):
    """生成综合仪表板"""
    print("\n[*] 生成综合仪表板...")
    os.makedirs(output_dir, exist_ok=True)

    fig = plt.figure(figsize=(20, 14))
    gs = GridSpec(2, 3, figure=fig, hspace=0.35, wspace=0.3)

    fig.suptitle('MCP Governance Agent 测试可视化仪表板',
                 fontsize=18, fontweight='bold', y=0.98)

    # ==================== 子图1: 竞争公平性 ====================
    ax1 = fig.add_subplot(gs[0, 0])
    tr = data.competition.get("TestCompetition_EqualBudget_Fairness")
    if tr and tr.agents:
        names = sorted(tr.agents.keys())
        successes = [tr.agents[n].success_calls for n in names]
        bars = ax1.bar(names, successes, color=COLORS[0], edgecolor='white')
        ax1.set_title(f'竞争公平性 (Jain={tr.jain_fairness:.3f})', fontsize=12, fontweight='bold')
        ax1.set_ylabel('成功次数')
        ax1.tick_params(axis='x', rotation=20, labelsize=8)
        ax1.grid(axis='y', alpha=0.3)
    else:
        ax1.text(0.5, 0.5, '无公平性数据', ha='center', va='center', fontsize=12)
        ax1.set_title('竞争公平性', fontsize=12, fontweight='bold')

    # ==================== 子图2: 竞争升级 ====================
    ax2 = fig.add_subplot(gs[0, 1])
    tr = data.competition.get("TestCompetition_Escalation")
    if tr and tr.phases:
        counts = [p['agent_count'] for p in tr.phases]
        reject_rates = [p['reject_rate'] for p in tr.phases]
        ax2.plot(counts, reject_rates, 'o-', color=COLORS[3], linewidth=2.5, markersize=8)
        ax2.fill_between(counts, reject_rates, alpha=0.15, color=COLORS[3])
        for x, y in zip(counts, reject_rates):
            ax2.annotate(f'{y:.0f}%', (x, y), xytext=(0, 8),
                         textcoords='offset points', ha='center', fontsize=9, fontweight='bold')
        ax2.set_xlabel('Agent 数量')
        ax2.set_ylabel('拒绝率 (%)')
        ax2.set_xticks(counts)
    else:
        ax2.text(0.5, 0.5, '无升级数据', ha='center', va='center', fontsize=12)
    ax2.set_title('竞争升级 - 拒绝率', fontsize=12, fontweight='bold')
    ax2.grid(alpha=0.3)

    # ==================== 子图3: 策略效率对比 ====================
    ax3 = fig.add_subplot(gs[0, 2])
    tr = data.competition.get("TestCompetition_StrategyComparison")
    if not tr or not tr.strategies:
        tr = data.budget.get("TestBudget_StrategyEfficiency_Benchmark")

    if tr and tr.strategies:
        strats = [s for s in tr.strategies if 'efficiency' in s]
        if strats:
            names = [s['name'].replace('-agent', '') for s in strats]
            effs = [s['efficiency'] for s in strats]
            sorted_idx = np.argsort(effs)
            sorted_names = [names[i] for i in sorted_idx]
            sorted_effs = [effs[i] for i in sorted_idx]
            colors = [COLORS[i % len(COLORS)] for i in range(len(sorted_names))]
            bars = ax3.barh(sorted_names, sorted_effs, color=colors, edgecolor='white', height=0.5)
            for bar in bars:
                w = bar.get_width()
                ax3.text(w + 0.2, bar.get_y() + bar.get_height() / 2,
                         f'{w:.1f}', va='center', fontsize=9, fontweight='bold')
            ax3.set_xlabel('效率 (成功/千令牌)')
    else:
        ax3.text(0.5, 0.5, '无策略数据', ha='center', va='center', fontsize=12)
    ax3.set_title('策略效率排名', fontsize=12, fontweight='bold')
    ax3.grid(axis='x', alpha=0.3)

    # ==================== 子图4: 预算价格侵蚀 ====================
    ax4 = fig.add_subplot(gs[1, 0])
    tr = data.budget.get("TestBudget_PriceIncrease_BudgetErosion")
    if tr and tr.phases:
        phases = [p['phase'] for p in tr.phases]
        prices = [p.get('price', 0) for p in tr.phases]
        successes = [p.get('success', 0) for p in tr.phases]
        budgets = [p.get('budget_left', 0) for p in tr.phases]

        ax4.bar(phases, successes, color=COLORS[2], alpha=0.7, label='成功数')
        ax4_r = ax4.twinx()
        ax4_r.plot(phases, budgets, 'D-', color=COLORS[0], linewidth=2, markersize=6, label='剩余预算')
        ax4_r.set_ylabel('预算', fontsize=9, color=COLORS[0])
        ax4_r.tick_params(axis='y', labelcolor=COLORS[0], labelsize=8)

        ax4.set_xlabel('阶段 (价格↑)')
        ax4.set_ylabel('成功数')
        ax4.set_xticks(phases)
        ax4.set_xticklabels([f'{p}' for p in prices], fontsize=9)

        lines1, labels1 = ax4.get_legend_handles_labels()
        lines2, labels2 = ax4_r.get_legend_handles_labels()
        ax4.legend(lines1 + lines2, labels1 + labels2, fontsize=8, loc='upper right')
    else:
        ax4.text(0.5, 0.5, '无涨价数据', ha='center', va='center', fontsize=12)
    ax4.set_title('价格阶梯侵蚀预算', fontsize=12, fontweight='bold')
    ax4.grid(axis='y', alpha=0.3)

    # ==================== 子图5: 推理链场景总览 ====================
    ax5 = fig.add_subplot(gs[1, 1])
    chain_tests = data.chain
    if chain_tests:
        name_map = {
            'TestChain_Linear_Basic': '线性链',
            'TestChain_DependencyBreak': '依赖断裂',
            'TestChain_LongChain_Stability': '长链',
            'TestChain_MultiAgent_ParallelChains': '并行链',
            'TestChain_BatchExperiment': '批量实验',
            'TestChain_UnderCompetition': '竞争环境',
            'TestChain_FanOutFanIn': 'Fan-out',
        }
        display_names = []
        rates = []
        for test_name, tr in sorted(chain_tests.items()):
            if test_name in name_map:
                display_names.append(name_map[test_name])
                if tr.chain_total_count > 0:
                    rates.append(tr.chain_completed_count / tr.chain_total_count * 100)
                elif tr.chain_completion_rate > 0:
                    rates.append(tr.chain_completion_rate)
                else:
                    rates.append(0)

        if display_names:
            y = np.arange(len(display_names))
            colors = [COLORS[2] if r >= 80 else COLORS[1] if r >= 50 else COLORS[3] for r in rates]
            bars = ax5.barh(y, rates, color=colors, edgecolor='white', height=0.5)
            for bar in bars:
                w = bar.get_width()
                ax5.text(max(w + 1, 5), bar.get_y() + bar.get_height() / 2,
                         f'{w:.0f}%', va='center', fontsize=9, fontweight='bold')
            ax5.set_yticks(y)
            ax5.set_yticklabels(display_names, fontsize=9)
            ax5.set_xlim(0, 120)
            ax5.invert_yaxis()
            ax5.set_xlabel('完成率 (%)')
    else:
        ax5.text(0.5, 0.5, '无推理链数据', ha='center', va='center', fontsize=12)
    ax5.set_title('推理链场景 - 链完成率', fontsize=12, fontweight='bold')
    ax5.grid(axis='x', alpha=0.3)

    # ==================== 子图6: 关键指标卡片 ====================
    ax6 = fig.add_subplot(gs[1, 2])
    ax6.axis('off')

    # 收集关键数字
    total_tests = len(data.all_tests)
    comp_tests = len(data.competition)
    budget_tests = len(data.budget)
    chain_tests_count = len(data.chain)

    # 公平性
    fair_tr = data.competition.get("TestCompetition_EqualBudget_Fairness")
    jain = fair_tr.jain_fairness if fair_tr else 0

    # 批量完成率
    batch_tr = data.chain.get("TestChain_BatchExperiment")
    batch_rate = batch_tr.chain_completion_rate if batch_tr else 0

    info = (
        f"====== 测试概况 ======\n"
        f"\n"
        f"  总测试场景:   {total_tests}\n"
        f"  竞争场景:     {comp_tests}\n"
        f"  预算场景:     {budget_tests}\n"
        f"  推理链场景:   {chain_tests_count}\n"
        f"\n"
        f"====== 核心指标 ======\n"
        f"\n"
        f"  Jain 公平性:  {jain:.4f}\n"
        f"  批量链完成:   {batch_rate:.1f}%\n"
    )

    ax6.text(0.5, 0.5, info, transform=ax6.transAxes, ha='center', va='center',
             fontsize=12,
             bbox=dict(boxstyle='round,pad=1', facecolor='#f0f8ff', edgecolor='#4C72B0', linewidth=2))
    ax6.set_title('关键指标', fontsize=12, fontweight='bold')

    fig.savefig(os.path.join(output_dir, 'dashboard.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ dashboard.png")


if __name__ == '__main__':
    data = get_sample_data()
    plot_dashboard(data, "output")
    print("\n完成! 仪表板已保存到 output/ 目录")
