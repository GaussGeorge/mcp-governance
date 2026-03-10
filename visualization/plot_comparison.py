#!/usr/bin/env python3
"""
MCP 服务治理 — 三策略对比可视化
读取 loadtest/output/test_single/ 下最新一轮 CSV 数据，生成三张核心对比图：
  1. 各阶段吞吐量对比 (Rajomon vs 静态限流 vs 无治理)
  2. Rajomon 预算公平性柱状图 (Budget 10 / 50 / 100 成功率)
  3. 延迟 CDF 对比 (无治理灾难性尾延迟 vs 治理后平滑曲线)

用法:
    cd ra-annotion-demo
    python visualization/plot_comparison.py
    # 图片输出到 visualization/ 目录
"""

import glob
import os
import sys

import matplotlib
matplotlib.use("Agg")  # 无 GUI 后端，适用于服务器/CI
import matplotlib.pyplot as plt
import matplotlib.ticker as ticker
import numpy as np
import pandas as pd

# ==================== 配置 ====================

# CSV 数据目录（相对于项目根目录运行）
DATA_DIR = os.path.join("loadtest", "output", "test_single")
# 图片输出目录
OUT_DIR = "visualization"

# 策略显示名称与颜色
STRATEGIES = {
    "no_governance":     {"label": "无治理 (FIFO)",     "color": "#e74c3c", "marker": "o"},
    "static_rate_limit": {"label": "静态限流 (Token Bucket)", "color": "#3498db", "marker": "s"},
    "rajomon":           {"label": "Rajomon 动态定价",  "color": "#2ecc71", "marker": "D"},
}

# 阶段显示顺序
PHASE_ORDER = ["warmup", "low", "medium", "high", "overload", "recovery"]
PHASE_LABELS = {
    "warmup":   "预热",
    "low":      "低负载",
    "medium":   "中负载",
    "high":     "高负载",
    "overload": "过载",
    "recovery": "恢复",
}

# 全局字体设置：优先使用中文字体
plt.rcParams.update({
    "font.sans-serif": ["Microsoft YaHei", "SimHei", "DejaVu Sans"],
    "axes.unicode_minus": False,
    "figure.dpi": 150,
    "savefig.dpi": 150,
})


# ==================== 数据加载 ====================

def find_latest_csv(strategy: str) -> str:
    """查找某策略最新的 CSV 文件"""
    pattern = os.path.join(DATA_DIR, f"{strategy}_step_run1_*.csv")
    files = sorted(glob.glob(pattern))
    if not files:
        print(f"[错误] 未找到 {strategy} 的 CSV 文件: {pattern}")
        sys.exit(1)
    return files[-1]  # 按文件名排序，最后一个是最新的


def load_data() -> dict[str, pd.DataFrame]:
    """加载三种策略的最新 CSV 数据"""
    data = {}
    for key in STRATEGIES:
        path = find_latest_csv(key)
        print(f"[加载] {STRATEGIES[key]['label']}: {os.path.basename(path)}")
        df = pd.read_csv(path, encoding="utf-8-sig")
        # 标记成功请求
        df["success"] = (~df["rejected"]) & (df["error_code"] == 0) & (df["status_code"] == 200)
        data[key] = df
    return data


# ==================== 图1: 各阶段吞吐量对比 ====================

def plot_throughput(data: dict[str, pd.DataFrame]):
    """
    柱状图：各阶段每种策略的吞吐量 (成功 RPS)
    重点展示 Rajomon 在过载阶段仍能维持较高吞吐
    """
    fig, ax = plt.subplots(figsize=(12, 6))

    # 计算各阶段吞吐量
    phase_rps = {}  # {strategy: {phase: rps}}
    for key, df in data.items():
        phase_rps[key] = {}
        for phase in PHASE_ORDER:
            pdf = df[df["phase"] == phase]
            if pdf.empty:
                phase_rps[key][phase] = 0
                continue
            success_count = pdf["success"].sum()
            # 用时间戳范围估算持续时间
            duration_s = (pdf["timestamp"].max() - pdf["timestamp"].min()) / 1000.0
            if duration_s <= 0:
                duration_s = 1.0
            phase_rps[key][phase] = success_count / duration_s

    # 绘制分组柱状图
    x = np.arange(len(PHASE_ORDER))
    width = 0.25

    for i, (key, meta) in enumerate(STRATEGIES.items()):
        rps_values = [phase_rps[key].get(p, 0) for p in PHASE_ORDER]
        bars = ax.bar(x + i * width, rps_values, width,
                      label=meta["label"], color=meta["color"], alpha=0.85,
                      edgecolor="white", linewidth=0.5)
        # 在柱顶标注数值
        for bar, val in zip(bars, rps_values):
            if val > 0:
                ax.text(bar.get_x() + bar.get_width() / 2, bar.get_height() + 2,
                        f"{val:.0f}", ha="center", va="bottom", fontsize=7, fontweight="bold")

    ax.set_xlabel("负载阶段", fontsize=12)
    ax.set_ylabel("吞吐量 (成功 RPS)", fontsize=12)
    ax.set_title("三种治理策略各阶段吞吐量对比", fontsize=14, fontweight="bold")
    ax.set_xticks(x + width)
    ax.set_xticklabels([PHASE_LABELS.get(p, p) for p in PHASE_ORDER], fontsize=11)
    ax.legend(fontsize=10, loc="upper left")
    ax.grid(axis="y", alpha=0.3)
    ax.set_ylim(bottom=0)

    # 在过载阶段添加标注框
    overload_idx = PHASE_ORDER.index("overload")
    ax.axvspan(overload_idx - 0.3, overload_idx + 0.9, alpha=0.08, color="red")
    ax.text(overload_idx + 0.35, ax.get_ylim()[1] * 0.92, "!! 过载区间",
            ha="center", fontsize=10, color="#c0392b", fontstyle="italic")

    out_path = os.path.join(OUT_DIR, "fig1_throughput_comparison.png")
    fig.savefig(out_path)
    plt.close(fig)
    print(f"[输出] {out_path}")


# ==================== 图2: Rajomon 预算公平性柱状图 ====================

def plot_budget_fairness(data: dict[str, pd.DataFrame]):
    """
    分组柱状图：三种策略下不同预算组 (10/50/100) 的成功率
    重点展示 Rajomon 的价值区分能力（高预算 > 低预算）
    """
    fig, ax = plt.subplots(figsize=(10, 6))

    budgets = [10, 50, 100]
    x = np.arange(len(budgets))
    width = 0.25

    for i, (key, meta) in enumerate(STRATEGIES.items()):
        df = data[key]
        # 只看过载+高负载阶段的数据（治理效果最明显的区间）
        stress_df = df[df["phase"].isin(["high", "overload"])]
        rates = []
        for b in budgets:
            bdf = stress_df[stress_df["client_budget"] == b]
            if len(bdf) == 0:
                rates.append(0)
            else:
                rates.append(bdf["success"].mean())

        bars = ax.bar(x + i * width, [r * 100 for r in rates], width,
                      label=meta["label"], color=meta["color"], alpha=0.85,
                      edgecolor="white", linewidth=0.5)
        for bar, val in zip(bars, rates):
            ax.text(bar.get_x() + bar.get_width() / 2, bar.get_height() + 1,
                    f"{val:.1%}", ha="center", va="bottom", fontsize=9, fontweight="bold")

    ax.set_xlabel("客户端 Token 预算", fontsize=12)
    ax.set_ylabel("请求成功率 (%)", fontsize=12)
    ax.set_title("高负载/过载阶段各预算组成功率对比\n(Rajomon 价值驱动 vs 一刀切)", fontsize=13, fontweight="bold")
    ax.set_xticks(x + width)
    ax.set_xticklabels([f"Budget {b}" for b in budgets], fontsize=11)
    ax.legend(fontsize=10)
    ax.set_ylim(0, 110)
    ax.yaxis.set_major_formatter(ticker.FormatStrFormatter("%.0f%%"))
    ax.grid(axis="y", alpha=0.3)

    # 添加说明文字
    ax.text(0.98, 0.02,
            "Rajomon: 高预算优先保障\n静态限流 & 无治理: 无差异化",
            transform=ax.transAxes, fontsize=9, va="bottom", ha="right",
            bbox=dict(boxstyle="round,pad=0.4", facecolor="#f0f0f0", alpha=0.8))

    out_path = os.path.join(OUT_DIR, "fig2_budget_fairness.png")
    fig.savefig(out_path)
    plt.close(fig)
    print(f"[输出] {out_path}")


# ==================== 图3: 延迟 CDF 对比 ====================

def plot_latency_cdf(data: dict[str, pd.DataFrame]):
    """
    CDF 曲线：三种策略的延迟累积分布
    重点展示无治理的灾难性尾延迟 vs 治理后的平滑低延迟
    """
    fig, (ax_main, ax_zoom) = plt.subplots(1, 2, figsize=(14, 6),
                                           gridspec_kw={"width_ratios": [2, 1]})

    for key, meta in STRATEGIES.items():
        df = data[key]
        # 只看成功的请求延迟（被拒绝的请求延迟为0或极低，不具参考价值）
        success_df = df[df["success"] & (df["latency_ms"] > 0)]
        if success_df.empty:
            continue
        latencies = np.sort(success_df["latency_ms"].values)
        cdf = np.arange(1, len(latencies) + 1) / len(latencies)

        ax_main.plot(latencies, cdf * 100, label=meta["label"],
                     color=meta["color"], linewidth=2, alpha=0.9)
        ax_zoom.plot(latencies, cdf * 100, label=meta["label"],
                     color=meta["color"], linewidth=2, alpha=0.9)

    # --- 主图：全范围 ---
    ax_main.set_xlabel("延迟 (ms)", fontsize=12)
    ax_main.set_ylabel("累积百分比 (%)", fontsize=12)
    ax_main.set_title("请求延迟 CDF 对比（全范围）", fontsize=13, fontweight="bold")
    ax_main.legend(fontsize=10, loc="lower right")
    ax_main.grid(alpha=0.3)
    ax_main.set_ylim(0, 101)

    # P95/P99 参考线
    for pct, ls in [(95, "--"), (99, ":")]:
        ax_main.axhline(y=pct, color="gray", linestyle=ls, alpha=0.5, linewidth=0.8)
        ax_main.text(ax_main.get_xlim()[1] * 0.02, pct + 0.5, f"P{pct}",
                     fontsize=8, color="gray")

    # --- 放大图：P95-P100 尾部 ---
    ax_zoom.set_xlabel("延迟 (ms)", fontsize=12)
    ax_zoom.set_ylabel("累积百分比 (%)", fontsize=12)
    ax_zoom.set_title("尾延迟放大 (P95~P100)", fontsize=13, fontweight="bold")
    ax_zoom.set_ylim(94, 100.5)
    ax_zoom.legend(fontsize=9, loc="lower right")
    ax_zoom.grid(alpha=0.3)

    for pct, ls in [(95, "--"), (99, ":")]:
        ax_zoom.axhline(y=pct, color="gray", linestyle=ls, alpha=0.5, linewidth=0.8)
        ax_zoom.text(ax_zoom.get_xlim()[1] * 0.02, pct + 0.1, f"P{pct}",
                     fontsize=8, color="gray")

    fig.suptitle("无治理的灾难性尾延迟 vs 动态定价的平滑低延迟",
                 fontsize=14, fontweight="bold")
    fig.tight_layout(rect=[0, 0, 1, 0.94])  # 为 suptitle 留出顶部空间

    out_path = os.path.join(OUT_DIR, "fig3_latency_cdf.png")
    fig.savefig(out_path)
    plt.close(fig)
    print(f"[输出] {out_path}")


# ==================== 主函数 ====================

def main():
    print("=" * 60)
    print("  MCP 服务治理 — 三策略对比可视化")
    print("=" * 60)

    # 确保输出目录存在
    os.makedirs(OUT_DIR, exist_ok=True)

    # 加载数据
    data = load_data()

    # 打印基础统计
    print("\n--- 基础统计 ---")
    for key, df in data.items():
        total = len(df)
        success = df["success"].sum()
        rejected = df["rejected"].sum()
        print(f"  {STRATEGIES[key]['label']}: 总请求={total}, 成功={success}, "
              f"拒绝={rejected}, 成功率={success/total:.2%}")

    print()

    # 生成三张图
    plot_throughput(data)
    plot_budget_fairness(data)
    plot_latency_cdf(data)

    print("\n✅ 全部可视化完成！图片已保存到 visualization/ 目录")


if __name__ == "__main__":
    main()
