#!/usr/bin/env python3
"""
MCP 服务治理 — Agent 场景可视化
读取 agenttest/output/ 下的 CSV 数据，生成 Agent 场景的核心对比图：
  1. 按预算组的任务成功率对比（三种策略柱状图）
  2. 按优先级的任务成功率对比
  3. 各阶段任务成功率变化（折线图）
  4. 任务完成时间 CDF 对比
  5. 步骤延迟 CDF 对比
  6. 失败原因分布（堆叠柱状图）

用法:
    cd ra-annotion-demo
    python visualization/plot_agent.py [--data-dir agenttest/output/test_agent_quick]
"""

import argparse
import glob
import os
import sys

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import matplotlib.ticker as ticker
import numpy as np
import pandas as pd

# ==================== 配置 ====================

STRATEGIES = {
    "no_governance":     {"label": "无治理 (FIFO)",           "color": "#e74c3c", "marker": "o"},
    "static_rate_limit": {"label": "静态限流 (Token Bucket)", "color": "#3498db", "marker": "s"},
    "rajomon":           {"label": "Rajomon 动态定价",        "color": "#2ecc71", "marker": "D"},
}

PHASE_ORDER = ["warmup", "normal", "burst", "overload", "recovery"]
PHASE_LABELS = {
    "warmup":   "预热",
    "normal":   "正常",
    "burst":    "突发",
    "overload": "过载",
    "recovery": "恢复",
}

BUDGET_ORDER = [10, 30, 100]
PRIORITY_ORDER = ["high", "medium", "low"]
PRIORITY_LABELS = {"high": "高优先级", "medium": "中优先级", "low": "低优先级"}

plt.rcParams.update({
    "font.sans-serif": ["Microsoft YaHei", "SimHei", "DejaVu Sans"],
    "axes.unicode_minus": False,
    "figure.dpi": 150,
    "savefig.dpi": 150,
})


# ==================== 数据加载 ====================

def find_latest_csv(data_dir, pattern):
    """查找匹配模式的最新 CSV 文件"""
    files = glob.glob(os.path.join(data_dir, pattern))
    if not files:
        return None
    return max(files, key=os.path.getmtime)


def load_step_data(data_dir):
    """加载各策略的步骤级数据"""
    data = {}
    for strategy in STRATEGIES:
        f = find_latest_csv(data_dir, f"{strategy}_*_agent_steps_*.csv")
        if f:
            df = pd.read_csv(f, encoding="utf-8-sig")
            data[strategy] = df
            print(f"  加载 {strategy}: {len(df)} 条步骤记录 ← {os.path.basename(f)}")
    return data


def load_task_data(data_dir):
    """加载各策略的任务级数据"""
    data = {}
    for strategy in STRATEGIES:
        f = find_latest_csv(data_dir, f"{strategy}_*_agent_tasks_*.csv")
        if f:
            df = pd.read_csv(f, encoding="utf-8-sig")
            data[strategy] = df
            print(f"  加载 {strategy}: {len(df)} 条任务记录 ← {os.path.basename(f)}")
    return data


# ==================== 图表绘制 ====================

def plot_budget_task_success_rate(task_data, out_dir):
    """图1：按预算组的任务成功率对比（三策略柱状图）"""
    fig, ax = plt.subplots(figsize=(10, 6))

    x = np.arange(len(BUDGET_ORDER))
    width = 0.25
    offsets = [-width, 0, width]

    for i, (strategy, meta) in enumerate(STRATEGIES.items()):
        if strategy not in task_data:
            continue
        df = task_data[strategy]
        rates = []
        for budget in BUDGET_ORDER:
            subset = df[df["initial_budget"] == budget]
            if len(subset) > 0:
                rate = subset["task_success"].sum() / len(subset)
            else:
                rate = 0
            rates.append(rate * 100)

        bars = ax.bar(x + offsets[i], rates, width, label=meta["label"],
                      color=meta["color"], alpha=0.85, edgecolor="white")
        # 在柱状图上标注数值
        for bar, rate in zip(bars, rates):
            ax.text(bar.get_x() + bar.get_width()/2, bar.get_height() + 1,
                    f"{rate:.1f}%", ha="center", va="bottom", fontsize=9)

    ax.set_xlabel("Agent 初始预算 (Token)")
    ax.set_ylabel("任务成功率 (%)")
    ax.set_title("按预算组的任务成功率对比")
    ax.set_xticks(x)
    ax.set_xticklabels([f"预算 {b}" for b in BUDGET_ORDER])
    ax.set_ylim(0, 110)
    ax.legend(loc="upper left")
    ax.grid(axis="y", alpha=0.3)

    plt.tight_layout()
    path = os.path.join(out_dir, "agent_budget_task_success.png")
    plt.savefig(path)
    plt.close()
    print(f"  ✓ 保存: {path}")


def plot_priority_task_success_rate(task_data, out_dir):
    """图2：按优先级的任务成功率对比"""
    fig, ax = plt.subplots(figsize=(10, 6))

    x = np.arange(len(PRIORITY_ORDER))
    width = 0.25
    offsets = [-width, 0, width]

    for i, (strategy, meta) in enumerate(STRATEGIES.items()):
        if strategy not in task_data:
            continue
        df = task_data[strategy]
        rates = []
        for priority in PRIORITY_ORDER:
            subset = df[df["priority"] == priority]
            if len(subset) > 0:
                rate = subset["task_success"].sum() / len(subset)
            else:
                rate = 0
            rates.append(rate * 100)

        ax.bar(x + offsets[i], rates, width, label=meta["label"],
               color=meta["color"], alpha=0.85, edgecolor="white")

    ax.set_xlabel("任务优先级")
    ax.set_ylabel("任务成功率 (%)")
    ax.set_title("按优先级的任务成功率对比")
    ax.set_xticks(x)
    ax.set_xticklabels([PRIORITY_LABELS.get(p, p) for p in PRIORITY_ORDER])
    ax.set_ylim(0, 110)
    ax.legend(loc="upper left")
    ax.grid(axis="y", alpha=0.3)

    plt.tight_layout()
    path = os.path.join(out_dir, "agent_priority_task_success.png")
    plt.savefig(path)
    plt.close()
    print(f"  ✓ 保存: {path}")


def plot_phase_task_success_rate(task_data, out_dir):
    """图3：各阶段任务成功率变化（折线图）"""
    fig, ax = plt.subplots(figsize=(12, 6))

    for strategy, meta in STRATEGIES.items():
        if strategy not in task_data:
            continue
        df = task_data[strategy]

        phases_present = [p for p in PHASE_ORDER if p in df["phase"].values]
        if not phases_present:
            continue

        rates = []
        for phase in phases_present:
            subset = df[df["phase"] == phase]
            if len(subset) > 0:
                rate = subset["task_success"].sum() / len(subset)
            else:
                rate = 0
            rates.append(rate * 100)

        x = range(len(phases_present))
        ax.plot(x, rates, marker=meta["marker"], label=meta["label"],
                color=meta["color"], linewidth=2, markersize=8)

    # 背景色区分阶段
    phases_all = [p for p in PHASE_ORDER if any(
        p in task_data.get(s, pd.DataFrame(columns=["phase"])).get("phase", pd.Series()).values
        for s in STRATEGIES)]

    for i, phase in enumerate(phases_all):
        label = PHASE_LABELS.get(phase, phase)
        if phase == "overload":
            ax.axvspan(i - 0.5, i + 0.5, alpha=0.1, color="red")
        ax.text(i, -8, label, ha="center", fontsize=9)

    ax.set_xlabel("负载阶段")
    ax.set_ylabel("任务成功率 (%)")
    ax.set_title("各阶段任务成功率变化")
    ax.set_xticks(range(len(phases_all)))
    ax.set_xticklabels([])
    ax.set_ylim(-15, 110)
    ax.legend(loc="upper right")
    ax.grid(axis="y", alpha=0.3)

    plt.tight_layout()
    path = os.path.join(out_dir, "agent_phase_task_success.png")
    plt.savefig(path)
    plt.close()
    print(f"  ✓ 保存: {path}")


def plot_task_duration_cdf(task_data, out_dir):
    """图4：成功任务完成时间 CDF 对比"""
    fig, ax = plt.subplots(figsize=(10, 6))

    for strategy, meta in STRATEGIES.items():
        if strategy not in task_data:
            continue
        df = task_data[strategy]
        success_durations = df[df["task_success"] == True]["duration_ms"].sort_values()
        if len(success_durations) == 0:
            continue
        y = np.arange(1, len(success_durations) + 1) / len(success_durations)
        ax.plot(success_durations, y, label=meta["label"], color=meta["color"], linewidth=2)

    ax.set_xlabel("任务完成时间 (ms)")
    ax.set_ylabel("累积概率")
    ax.set_title("成功任务完成时间 CDF")
    ax.legend(loc="lower right")
    ax.grid(alpha=0.3)
    ax.set_xlim(left=0)

    plt.tight_layout()
    path = os.path.join(out_dir, "agent_task_duration_cdf.png")
    plt.savefig(path)
    plt.close()
    print(f"  ✓ 保存: {path}")


def plot_step_latency_cdf(step_data, out_dir):
    """图5：步骤延迟 CDF 对比"""
    fig, ax = plt.subplots(figsize=(10, 6))

    for strategy, meta in STRATEGIES.items():
        if strategy not in step_data:
            continue
        df = step_data[strategy]
        # 仅统计实际发送了请求的步骤（排除预算不足直接失败的）
        latencies = df[df["status_code"] > 0]["latency_ms"].sort_values()
        if len(latencies) == 0:
            continue
        y = np.arange(1, len(latencies) + 1) / len(latencies)
        ax.plot(latencies, y, label=meta["label"], color=meta["color"], linewidth=2)

    ax.set_xlabel("步骤延迟 (ms)")
    ax.set_ylabel("累积概率")
    ax.set_title("步骤请求延迟 CDF")
    ax.legend(loc="lower right")
    ax.grid(alpha=0.3)
    ax.set_xlim(left=0)

    plt.tight_layout()
    path = os.path.join(out_dir, "agent_step_latency_cdf.png")
    plt.savefig(path)
    plt.close()
    print(f"  ✓ 保存: {path}")


def plot_failure_reasons(task_data, out_dir):
    """图6：失败原因分布（堆叠柱状图）"""
    fig, ax = plt.subplots(figsize=(10, 6))

    failure_reasons_all = set()
    for strategy in STRATEGIES:
        if strategy in task_data:
            df = task_data[strategy]
            failed = df[df["task_success"] == False]
            failure_reasons_all.update(failed["failure_reason"].dropna().unique())

    if not failure_reasons_all:
        plt.close()
        return

    reasons = sorted(failure_reasons_all)
    reason_labels = {
        "budget_exhausted": "预算耗尽",
        "step_rejected": "步骤被拒绝",
        "step_failed": "步骤失败",
        "network_error": "网络错误",
    }

    x = np.arange(len(STRATEGIES))
    width = 0.6
    bottom = np.zeros(len(STRATEGIES))

    colors = plt.cm.Set3(np.linspace(0, 1, len(reasons)))

    for j, reason in enumerate(reasons):
        counts = []
        for strategy in STRATEGIES:
            if strategy in task_data:
                df = task_data[strategy]
                failed = df[df["task_success"] == False]
                count = len(failed[failed["failure_reason"] == reason])
            else:
                count = 0
            counts.append(count)

        label = reason_labels.get(reason, reason)
        ax.bar(x, counts, width, bottom=bottom, label=label, color=colors[j], edgecolor="white")
        bottom += counts

    ax.set_xlabel("治理策略")
    ax.set_ylabel("失败任务数")
    ax.set_title("失败原因分布")
    ax.set_xticks(x)
    ax.set_xticklabels([STRATEGIES[s]["label"] for s in STRATEGIES])
    ax.legend(loc="upper right")
    ax.grid(axis="y", alpha=0.3)

    plt.tight_layout()
    path = os.path.join(out_dir, "agent_failure_reasons.png")
    plt.savefig(path)
    plt.close()
    print(f"  ✓ 保存: {path}")


def plot_budget_vs_completed_steps(task_data, out_dir):
    """图7：不同预算组的平均完成步骤数"""
    fig, ax = plt.subplots(figsize=(10, 6))

    x = np.arange(len(BUDGET_ORDER))
    width = 0.25
    offsets = [-width, 0, width]

    for i, (strategy, meta) in enumerate(STRATEGIES.items()):
        if strategy not in task_data:
            continue
        df = task_data[strategy]
        avg_steps = []
        for budget in BUDGET_ORDER:
            subset = df[df["initial_budget"] == budget]
            if len(subset) > 0:
                avg = subset["completed_steps"].mean()
            else:
                avg = 0
            avg_steps.append(avg)

        ax.bar(x + offsets[i], avg_steps, width, label=meta["label"],
               color=meta["color"], alpha=0.85, edgecolor="white")

    ax.set_xlabel("Agent 初始预算 (Token)")
    ax.set_ylabel("平均完成步骤数")
    ax.set_title("不同预算组的平均完成步骤数")
    ax.set_xticks(x)
    ax.set_xticklabels([f"预算 {b}" for b in BUDGET_ORDER])
    ax.legend(loc="upper left")
    ax.grid(axis="y", alpha=0.3)

    plt.tight_layout()
    path = os.path.join(out_dir, "agent_budget_completed_steps.png")
    plt.savefig(path)
    plt.close()
    print(f"  ✓ 保存: {path}")


def plot_tool_type_success(step_data, out_dir):
    """图8：各工具类型的步骤成功率"""
    tool_types = ["simple_query", "calculation", "image_gen"]
    tool_labels = {"simple_query": "简单查询", "calculation": "复杂计算", "image_gen": "图片生成"}

    fig, ax = plt.subplots(figsize=(10, 6))

    x = np.arange(len(tool_types))
    width = 0.25
    offsets = [-width, 0, width]

    for i, (strategy, meta) in enumerate(STRATEGIES.items()):
        if strategy not in step_data:
            continue
        df = step_data[strategy]
        rates = []
        for tool in tool_types:
            subset = df[df["tool_type"] == tool]
            if len(subset) > 0:
                success = len(subset[(subset["status_code"] == 200) & (subset["rejected"] == False)])
                rate = success / len(subset)
            else:
                rate = 0
            rates.append(rate * 100)

        ax.bar(x + offsets[i], rates, width, label=meta["label"],
               color=meta["color"], alpha=0.85, edgecolor="white")

    ax.set_xlabel("工具类型")
    ax.set_ylabel("步骤成功率 (%)")
    ax.set_title("各工具类型的步骤成功率")
    ax.set_xticks(x)
    ax.set_xticklabels([tool_labels.get(t, t) for t in tool_types])
    ax.set_ylim(0, 110)
    ax.legend(loc="upper right")
    ax.grid(axis="y", alpha=0.3)

    plt.tight_layout()
    path = os.path.join(out_dir, "agent_tool_type_success.png")
    plt.savefig(path)
    plt.close()
    print(f"  ✓ 保存: {path}")


# ==================== 主函数 ====================

def main():
    parser = argparse.ArgumentParser(description="MCP Agent 场景可视化")
    parser.add_argument("--data-dir", default="agenttest/output/test_agent_quick",
                        help="CSV 数据目录")
    parser.add_argument("--out-dir", default=None,
                        help="图片输出目录（默认 visualization/figures/agent/）")
    args = parser.parse_args()

    data_dir = args.data_dir
    out_dir = args.out_dir or os.path.join("visualization", "figures", "agent")

    if not os.path.exists(data_dir):
        print(f"错误: 数据目录不存在: {data_dir}")
        print("请先运行 Agent 场景测试: go run ./agenttest/ -mode=quick")
        sys.exit(1)

    os.makedirs(out_dir, exist_ok=True)

    print("=" * 60)
    print("  MCP Agent 场景可视化")
    print("=" * 60)
    print(f"  数据目录: {data_dir}")
    print(f"  输出目录: {out_dir}")
    print()

    print("加载步骤级数据...")
    step_data = load_step_data(data_dir)
    print(f"\n加载任务级数据...")
    task_data = load_task_data(data_dir)

    if not step_data and not task_data:
        print("错误: 未找到任何 CSV 数据文件")
        sys.exit(1)

    print(f"\n生成图表...")
    print("-" * 40)

    if task_data:
        plot_budget_task_success_rate(task_data, out_dir)
        plot_priority_task_success_rate(task_data, out_dir)
        plot_phase_task_success_rate(task_data, out_dir)
        plot_task_duration_cdf(task_data, out_dir)
        plot_failure_reasons(task_data, out_dir)
        plot_budget_vs_completed_steps(task_data, out_dir)

    if step_data:
        plot_step_latency_cdf(step_data, out_dir)
        plot_tool_type_success(step_data, out_dir)

    print()
    print("=" * 60)
    print(f"  所有图表已保存到: {out_dir}")
    print("=" * 60)


if __name__ == "__main__":
    main()
