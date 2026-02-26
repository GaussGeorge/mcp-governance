"""
plot_budget.py
Agent 预算管理场景的可视化图表

生成图表:
  1. 预算耗尽过程图 - 调用次数、成功/拒绝分布
  2. 预算分层对比 - 低/中/高预算成功率
  3. 4 策略效率基准雷达图
  4. 价格阶梯侵蚀预算 - 阶段折线图
  5. 自适应 vs 固定策略对比
  6. 预算生命周期曲线
  7. 边界条件对比
  8. 令牌守恒验证
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


def plot_budget_tiers(data: ParsedOutput, output_dir: str = "output"):
    """图1: 预算分层对比 - 低/中/高预算的成功率和消耗"""
    tr = data.budget.get("TestBudget_Tiers_CompletionRate")
    if not tr or not tr.strategies:
        print("  [跳过] 无预算分层数据")
        return

    tiers = [s for s in tr.strategies if 'success_rate' in s]
    if not tiers:
        print("  [跳过] 无预算分层成功率数据")
        return

    fig, (ax1, ax2) = plt.subplots(1, 2, figsize=(14, 6))

    names = [t['name'] for t in tiers]
    budgets = [t['budget'] for t in tiers]
    success_rates = [t['success_rate'] for t in tiers]
    successes = [t['success'] for t in tiers]
    calls = [t['calls'] for t in tiers]

    # 左图: 成功率柱状图
    bar_colors = [COLORS[3], COLORS[1], COLORS[2]]
    bars = ax1.bar(names, success_rates, color=bar_colors, edgecolor='white', width=0.5)
    for bar, rate in zip(bars, success_rates):
        ax1.annotate(f'{rate:.1f}%', (bar.get_x() + bar.get_width() / 2, bar.get_height()),
                     xytext=(0, 6), textcoords='offset points', ha='center', fontsize=13, fontweight='bold')
    ax1.set_ylabel('成功率 (%)', fontsize=12)
    ax1.set_title('预算层级 vs 成功率', fontsize=14, fontweight='bold')
    ax1.set_ylim(0, 115)
    ax1.grid(axis='y', alpha=0.3)

    # 右图: 成功数和调用数堆叠
    x = np.arange(len(names))
    width = 0.35
    ax2.bar(x - width / 2, calls, width, label='总调用', color=COLORS[7], alpha=0.5)
    ax2.bar(x + width / 2, successes, width, label='成功数', color=COLORS[2])
    ax2.set_xticks(x)
    ax2.set_xticklabels([f'{n}\n(预算={b})' for n, b in zip(names, budgets)])
    ax2.set_ylabel('次数', fontsize=12)
    ax2.set_title('预算层级 - 调用数 vs 成功数', fontsize=14, fontweight='bold')
    ax2.legend(fontsize=11)
    ax2.grid(axis='y', alpha=0.3)

    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'budget_tiers.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ budget_tiers.png")


def plot_strategy_benchmark(data: ParsedOutput, output_dir: str = "output"):
    """图2: 4 策略效率基准 - 雷达图 + 柱状对比"""
    tr = data.budget.get("TestBudget_StrategyEfficiency_Benchmark")
    if not tr or not tr.strategies:
        print("  [跳过] 无策略效率基准数据")
        return

    strats = [s for s in tr.strategies if 'efficiency' in s]
    if not strats:
        print("  [跳过] 无策略效率数据")
        return

    fig, (ax1, ax2) = plt.subplots(1, 2, figsize=(14, 6))

    names = [s['name'] for s in strats]
    successes = [s['success'] for s in strats]
    spents = [s['spent'] for s in strats]
    efficiencies = [s['efficiency'] for s in strats]

    # 左图: 柱状对比 (成功数 & 消耗)
    x = np.arange(len(names))
    width = 0.3
    ax1.bar(x - width, successes, width, label='成功调用', color=COLORS[2], edgecolor='white')
    ax1.bar(x, [s / 40 for s in spents], width, label='消耗 (÷40)', color=COLORS[1], alpha=0.7, edgecolor='white')
    ax1.bar(x + width, efficiencies, width, label='效率 (成功/千令牌)', color=COLORS[0], edgecolor='white')

    ax1.set_xticks(x)
    ax1.set_xticklabels(names)
    ax1.set_title('策略效率基准', fontsize=14, fontweight='bold')
    ax1.legend(fontsize=10)
    ax1.grid(axis='y', alpha=0.3)

    # 右图: 效率排名水平柱状图
    sorted_idx = np.argsort(efficiencies)
    sorted_names = [names[i] for i in sorted_idx]
    sorted_eff = [efficiencies[i] for i in sorted_idx]
    colors = [COLORS[i % len(COLORS)] for i in range(len(sorted_names))]

    bars = ax2.barh(sorted_names, sorted_eff, color=colors, edgecolor='white', height=0.5)
    for bar in bars:
        w = bar.get_width()
        ax2.text(w + 0.3, bar.get_y() + bar.get_height() / 2, f'{w:.1f}',
                 va='center', fontsize=11, fontweight='bold')

    ax2.set_xlabel('效率 (成功/千令牌)', fontsize=12)
    ax2.set_title('策略效率排名', fontsize=14, fontweight='bold')
    ax2.grid(axis='x', alpha=0.3)

    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'budget_strategy_benchmark.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ budget_strategy_benchmark.png")


def plot_price_erosion(data: ParsedOutput, output_dir: str = "output"):
    """图3: 价格阶梯侵蚀预算 - 双轴折线图"""
    tr = data.budget.get("TestBudget_PriceIncrease_BudgetErosion")
    if not tr or not tr.phases:
        print("  [跳过] 无价格侵蚀数据")
        return

    fig, ax1 = plt.subplots(figsize=(10, 6))

    phases = [p['phase'] for p in tr.phases]
    prices = [p.get('price', 0) for p in tr.phases]
    successes = [p.get('success', 0) for p in tr.phases]
    rejects = [p.get('rejected', 0) for p in tr.phases]
    budgets = [p.get('budget_left', 0) for p in tr.phases]

    # 左轴: 成功数 & 拒绝数 堆叠柱状
    width = 0.4
    ax1.bar(phases, successes, width, label='成功', color=COLORS[2], edgecolor='white')
    ax1.bar(phases, rejects, width, bottom=successes, label='拒绝', color=COLORS[3], alpha=0.7, edgecolor='white')
    ax1.set_xlabel('阶段', fontsize=12)
    ax1.set_ylabel('调用次数', fontsize=12)

    # 在柱子上标注价格
    for p, price in zip(phases, prices):
        ax1.annotate(f'{price}', (p, successes[phases.index(p)] + rejects[phases.index(p)]),
                     xytext=(0, 5), textcoords='offset points', ha='center', fontsize=10,
                     fontweight='bold', color=COLORS[1])

    # 右轴: 剩余预算折线
    ax2 = ax1.twinx()
    line = ax2.plot(phases, budgets, 'D-', color=COLORS[0], linewidth=2.5, markersize=8, label='剩余预算')
    ax2.fill_between(phases, budgets, alpha=0.1, color=COLORS[0])
    ax2.set_ylabel('剩余预算 (令牌)', fontsize=12, color=COLORS[0])
    ax2.tick_params(axis='y', labelcolor=COLORS[0])

    ax1.set_title('价格阶梯侵蚀预算', fontsize=14, fontweight='bold')
    ax1.set_xticks(phases)
    ax1.set_xticklabels([f'阶段{p}\n(价格{pr})' for p, pr in zip(phases, prices)])

    lines1, labels1 = ax1.get_legend_handles_labels()
    lines2, labels2 = ax2.get_legend_handles_labels()
    ax1.legend(lines1 + lines2, labels1 + labels2, loc='upper right', fontsize=10)

    ax1.grid(axis='y', alpha=0.3)
    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'budget_price_erosion.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ budget_price_erosion.png")


def plot_adaptive_vs_fixed(data: ParsedOutput, output_dir: str = "output"):
    """图4: 自适应 vs 固定策略在价格波动环境下的对比"""
    tr = data.budget.get("TestBudget_Adaptive_vs_Fixed_UnderPriceFluctuation")
    if not tr or not tr.strategies:
        print("  [跳过] 无策略对比数据")
        return

    fig, (ax1, ax2) = plt.subplots(1, 2, figsize=(13, 6))

    names = [s['name'] for s in tr.strategies]
    successes = [s['success'] for s in tr.strategies]
    spents = [s['spent'] for s in tr.strategies]

    # 左图: 成功数对比
    colors = [COLORS[1], COLORS[0], COLORS[2]][:len(names)]
    bars = ax1.bar(names, successes, color=colors, edgecolor='white', width=0.5)
    for bar in bars:
        h = bar.get_height()
        ax1.annotate(f'{int(h)}', (bar.get_x() + bar.get_width() / 2, h),
                     xytext=(0, 5), textcoords='offset points', ha='center', fontsize=13, fontweight='bold')
    ax1.set_ylabel('成功调用次数', fontsize=12)
    ax1.set_title('价格波动环境 - 成功次数', fontsize=14, fontweight='bold')
    ax1.grid(axis='y', alpha=0.3)

    # 右图: 消耗对比 + 效率标注
    bars2 = ax2.bar(names, spents, color=colors, edgecolor='white', width=0.5, alpha=0.7)
    for bar, succ in zip(bars2, successes):
        h = bar.get_height()
        eff = succ / h * 1000 if h > 0 else 0
        ax2.annotate(f'{int(h)}\n效率={eff:.1f}', (bar.get_x() + bar.get_width() / 2, h),
                     xytext=(0, 5), textcoords='offset points', ha='center', fontsize=10)
    ax2.set_ylabel('消耗令牌', fontsize=12)
    ax2.set_title('价格波动环境 - 令牌消耗', fontsize=14, fontweight='bold')
    ax2.grid(axis='y', alpha=0.3)

    # 顶部标注价格调度表
    fig.text(0.5, 0.02, '价格调度: [10, 10, 30, 30, 60, 60, 30, 30, 10, 10]',
             ha='center', fontsize=11, fontstyle='italic', color='gray')

    plt.tight_layout(rect=[0, 0.05, 1, 1])
    fig.savefig(os.path.join(output_dir, 'budget_adaptive_vs_fixed.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ budget_adaptive_vs_fixed.png")


def plot_lifecycle(data: ParsedOutput, output_dir: str = "output"):
    """图5: 预算生命周期曲线"""
    tr = data.budget.get("TestBudget_Lifecycle_LongRunning")
    if not tr:
        print("  [跳过] 无生命周期数据")
        return

    fig, ax1 = plt.subplots(figsize=(10, 6))

    # 使用 budget_trajectory 或 phases
    if tr.budget_trajectory:
        trajectory = tr.budget_trajectory
        phases_x = list(range(1, len(trajectory) + 1))
    elif tr.phases:
        phases_x = [p['phase'] for p in tr.phases]
        trajectory = [p.get('budget_before', 0) for p in tr.phases]
    else:
        print("  [跳过] 无预算轨迹数据")
        plt.close(fig)
        return

    # 预算曲线
    ax1.plot(phases_x, trajectory, 'o-', color=COLORS[0], linewidth=2.5, markersize=8, label='剩余预算')
    ax1.fill_between(phases_x, trajectory, alpha=0.15, color=COLORS[0])

    ax1.set_xlabel('阶段', fontsize=12)
    ax1.set_ylabel('剩余预算 (令牌)', fontsize=12)
    ax1.set_title('预算生命周期 - 消耗曲线', fontsize=14, fontweight='bold')

    # 标注每阶段消耗
    if tr.phases:
        ax2 = ax1.twinx()
        success_per_phase = [p.get('success', 0) for p in tr.phases[:len(phases_x)]]
        ax2.bar(phases_x, success_per_phase, alpha=0.25, color=COLORS[2], width=0.5, label='每阶段成功数')
        ax2.set_ylabel('每阶段成功数', fontsize=12, color=COLORS[2])
        ax2.tick_params(axis='y', labelcolor=COLORS[2])

        lines1, labels1 = ax1.get_legend_handles_labels()
        lines2, labels2 = ax2.get_legend_handles_labels()
        ax1.legend(lines1 + lines2, labels1 + labels2, loc='upper right', fontsize=11)
    else:
        ax1.legend(fontsize=11)

    ax1.set_xticks(phases_x)
    ax1.grid(alpha=0.3)

    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'budget_lifecycle.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ budget_lifecycle.png")


def plot_edge_cases(data: ParsedOutput, output_dir: str = "output"):
    """图6: 边界条件对比"""
    tr = data.budget.get("TestBudget_EdgeCases")
    if not tr or not tr.strategies:
        print("  [跳过] 无边界条件数据")
        return

    edges = [s for s in tr.strategies if 'calls' in s]
    if not edges:
        print("  [跳过] 无边界条件调用数据")
        return

    fig, ax = plt.subplots(figsize=(9, 5))

    names = [e['name'] for e in edges]
    calls = [e['calls'] for e in edges]
    successes = [e['success'] for e in edges]

    x = np.arange(len(names))
    width = 0.3

    bars1 = ax.bar(x - width / 2, calls, width, label='总调用', color=COLORS[7], alpha=0.5)
    bars2 = ax.bar(x + width / 2, successes, width, label='成功', color=COLORS[2])

    for bar in bars2:
        h = bar.get_height()
        ax.annotate(f'{int(h)}', (bar.get_x() + bar.get_width() / 2, h),
                    xytext=(0, 4), textcoords='offset points', ha='center', fontsize=12, fontweight='bold')

    ax.set_xticks(x)
    ax.set_xticklabels(names)
    ax.set_ylabel('次数', fontsize=12)
    ax.set_title('预算边界条件测试', fontsize=14, fontweight='bold')
    ax.legend(fontsize=11)
    ax.grid(axis='y', alpha=0.3)

    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'budget_edge_cases.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ budget_edge_cases.png")


def plot_conservation(data: ParsedOutput, output_dir: str = "output"):
    """图7: 令牌总量守恒验证"""
    tr = data.budget.get("TestBudget_Conservation_TotalTokens")
    if not tr or not tr.strategies:
        print("  [跳过] 无守恒验证数据")
        return

    s = tr.strategies[0]
    if 'initial' not in s:
        print("  [跳过] 无守恒初始/最终数据")
        return

    fig, ax = plt.subplots(figsize=(8, 6))

    initial = s['initial']
    final = s['final']
    spent = s['spent']

    # 堆叠柱状图: 初始 = 剩余 + 消耗
    ax.bar(['初始预算'], [initial], color=COLORS[0], edgecolor='white', label='总预算')
    ax.bar(['分解'], [final], color=COLORS[2], edgecolor='white', label='剩余预算')
    ax.bar(['分解'], [spent], bottom=[final], color=COLORS[1], edgecolor='white', label='已消耗')

    # 差额
    diff = initial - final - spent
    if diff != 0:
        ax.bar(['分解'], [abs(diff)], bottom=[final + spent], color=COLORS[3],
               edgecolor='white', alpha=0.5, label=f'差额: {diff}')

    ax.annotate(f'{initial}', (0, initial), xytext=(0, 5), textcoords='offset points',
                ha='center', fontsize=13, fontweight='bold')
    ax.annotate(f'剩余: {final}', (1, final / 2), ha='center', fontsize=11, color='white', fontweight='bold')
    ax.annotate(f'消耗: {spent}', (1, final + spent / 2), ha='center', fontsize=11, color='white', fontweight='bold')

    ax.set_ylabel('令牌数', fontsize=12)
    ax.set_title('全局令牌总量守恒验证', fontsize=14, fontweight='bold')
    ax.legend(fontsize=11)
    ax.grid(axis='y', alpha=0.3)

    # 守恒判定
    status = "[PASS] 守恒" if diff == 0 else f"[DIFF] 差额={diff} (退款机制)"
    ax.text(0.5, 0.02, status, transform=ax.transAxes, ha='center', fontsize=12,
            fontstyle='italic', color='green' if diff == 0 else 'orange')

    plt.tight_layout()
    fig.savefig(os.path.join(output_dir, 'budget_conservation.png'), bbox_inches='tight')
    plt.close(fig)
    print("  ✓ budget_conservation.png")


def plot_all_budget(data: ParsedOutput, output_dir: str = "output"):
    """生成全部预算管理图表"""
    print("\n[*] 生成预算管理图表...")
    os.makedirs(output_dir, exist_ok=True)
    plot_budget_tiers(data, output_dir)
    plot_strategy_benchmark(data, output_dir)
    plot_price_erosion(data, output_dir)
    plot_adaptive_vs_fixed(data, output_dir)
    plot_lifecycle(data, output_dir)
    plot_edge_cases(data, output_dir)
    plot_conservation(data, output_dir)


if __name__ == '__main__':
    data = get_sample_data()
    plot_all_budget(data, "output")
    print("\n完成! 图表已保存到 output/ 目录")
