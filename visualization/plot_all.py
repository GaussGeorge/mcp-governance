#!/usr/bin/env python3
"""
MCP 服务治理 — 全量输出可视化
自动扫描 loadtest/output/ 下所有子目录的 CSV 数据，为每个数据集生成对应图表。

数据集与图表对应关系：
  test_single/       → 三策略 6 阶段吞吐量对比 + 延迟 CDF + 预算公平性
  test_quick/         → 快速对比：汇总雷达图 + summary 横向柱状图
  test_fairness/      → 过载公平性热力图
  test_price_dynamics/ → Rajomon 动态价格曲线 vs 流量

用法:
    cd ra-annotion-demo
    python visualization/plot_all.py
"""

import glob
import os
import sys

import matplotlib
matplotlib.use("Agg")
import matplotlib.pyplot as plt
import matplotlib.ticker as ticker
import numpy as np
import pandas as pd

# ==================== 全局配置 ====================

OUTPUT_BASE = os.path.join("loadtest", "output")
FIG_DIR = os.path.join("visualization", "figures")

STRATEGIES = {
    "no_governance":     {"label": "无治理 (FIFO)",           "color": "#e74c3c"},
    "static_rate_limit": {"label": "静态限流 (Token Bucket)", "color": "#3498db"},
    "rajomon":           {"label": "Rajomon 动态定价",        "color": "#2ecc71"},
}

PHASE_ORDER = ["warmup", "low", "medium", "high", "overload", "recovery"]
PHASE_LABELS = {
    "warmup": "预热", "low": "低负载", "medium": "中负载",
    "high": "高负载", "overload": "过载", "recovery": "恢复",
}
PHASE_COLORS = {
    "warmup": "#E8F5E9", "low": "#FFF3E0", "medium": "#FFF9C4",
    "high": "#FFE0B2", "overload": "#FFCDD2", "recovery": "#E3F2FD",
}

plt.rcParams.update({
    "font.sans-serif": ["Microsoft YaHei", "SimHei", "DejaVu Sans"],
    "axes.unicode_minus": False,
    "figure.dpi": 150,
    "savefig.dpi": 150,
})


# ==================== 工具函数 ====================

def find_latest_csv(directory: str, strategy: str) -> str | None:
    """查找某策略最新的 CSV 文件"""
    pattern = os.path.join(directory, f"{strategy}_step_run1_*.csv")
    files = sorted(glob.glob(pattern))
    return files[-1] if files else None


def load_strategy_data(directory: str) -> dict[str, pd.DataFrame]:
    """加载目录下三种策略的最新 CSV"""
    data = {}
    for key in STRATEGIES:
        path = find_latest_csv(directory, key)
        if path:
            df = pd.read_csv(path, encoding="utf-8-sig")
            df["success"] = (~df["rejected"]) & (df["error_code"] == 0) & (df["status_code"] == 200)
            data[key] = df
            print(f"  [加载] {STRATEGIES[key]['label']}: {os.path.basename(path)} ({len(df)} 行)")
    return data


def save_fig(fig, name: str, subdir: str = ""):
    """保存图表到 figures 子目录"""
    out_dir = os.path.join(FIG_DIR, subdir) if subdir else FIG_DIR
    os.makedirs(out_dir, exist_ok=True)
    path = os.path.join(out_dir, name)
    fig.savefig(path, bbox_inches="tight")
    plt.close(fig)
    print(f"  [保存] {path}")


# ==================== 图表绘制函数 ====================

# ---------- 1. 各阶段吞吐量对比 (分组柱状图) ----------

def plot_throughput_by_phase(data: dict[str, pd.DataFrame], subdir: str):
    fig, ax = plt.subplots(figsize=(12, 6))
    phases_present = [p for p in PHASE_ORDER if any(p in df["phase"].unique() for df in data.values())]
    x = np.arange(len(phases_present))
    width = 0.8 / max(len(data), 1)

    for i, (key, df) in enumerate(data.items()):
        meta = STRATEGIES[key]
        rps_values = []
        for phase in phases_present:
            pdf = df[df["phase"] == phase]
            if pdf.empty:
                rps_values.append(0)
                continue
            success_count = pdf["success"].sum()
            duration_s = (pdf["timestamp"].max() - pdf["timestamp"].min()) / 1000.0
            rps_values.append(success_count / max(duration_s, 1.0))

        bars = ax.bar(x + i * width, rps_values, width,
                      label=meta["label"], color=meta["color"], alpha=0.85,
                      edgecolor="white", linewidth=0.5)
        for bar, val in zip(bars, rps_values):
            if val > 0:
                ax.text(bar.get_x() + bar.get_width() / 2, bar.get_height() + 1,
                        f"{val:.0f}", ha="center", va="bottom", fontsize=7, fontweight="bold")

    ax.set_xlabel("负载阶段", fontsize=12)
    ax.set_ylabel("吞吐量 (成功 RPS)", fontsize=12)
    ax.set_title("各阶段吞吐量对比", fontsize=14, fontweight="bold")
    ax.set_xticks(x + width * (len(data) - 1) / 2)
    ax.set_xticklabels([PHASE_LABELS.get(p, p) for p in phases_present], fontsize=11)
    ax.legend(fontsize=10, loc="upper left")
    ax.grid(axis="y", alpha=0.3)
    ax.set_ylim(bottom=0)

    # 过载区间高亮
    if "overload" in phases_present:
        idx = phases_present.index("overload")
        ax.axvspan(idx - 0.35, idx + 0.35 + width * len(data), alpha=0.08, color="red")

    save_fig(fig, "throughput_by_phase.png", subdir)


# ---------- 2. 延迟 CDF 对比 ----------

def plot_latency_cdf(data: dict[str, pd.DataFrame], subdir: str):
    fig, (ax_main, ax_zoom) = plt.subplots(1, 2, figsize=(14, 6),
                                           gridspec_kw={"width_ratios": [2, 1]})

    for key, df in data.items():
        meta = STRATEGIES[key]
        success_df = df[df["success"] & (df["latency_ms"] > 0)]
        if success_df.empty:
            continue
        latencies = np.sort(success_df["latency_ms"].values)
        cdf = np.arange(1, len(latencies) + 1) / len(latencies)
        ax_main.plot(latencies, cdf * 100, label=meta["label"], color=meta["color"], linewidth=2)
        ax_zoom.plot(latencies, cdf * 100, label=meta["label"], color=meta["color"], linewidth=2)

    for ax, title, ylim in [
        (ax_main, "请求延迟 CDF 对比（全范围）", (0, 101)),
        (ax_zoom, "尾延迟放大 (P95~P100)", (94, 100.5)),
    ]:
        ax.set_xlabel("延迟 (ms)", fontsize=12)
        ax.set_ylabel("累积百分比 (%)", fontsize=12)
        ax.set_title(title, fontsize=13, fontweight="bold")
        ax.set_ylim(*ylim)
        ax.legend(fontsize=9, loc="lower right")
        ax.grid(alpha=0.3)
        for pct, ls in [(95, "--"), (99, ":")]:
            ax.axhline(y=pct, color="gray", linestyle=ls, alpha=0.5, linewidth=0.8)

    fig.suptitle("延迟 CDF 对比: 无治理尾延迟 vs 治理后平滑曲线",
                 fontsize=14, fontweight="bold")
    fig.tight_layout(rect=[0, 0, 1, 0.94])
    save_fig(fig, "latency_cdf.png", subdir)


# ---------- 3. 预算公平性柱状图 ----------

def plot_budget_fairness(data: dict[str, pd.DataFrame], subdir: str):
    fig, ax = plt.subplots(figsize=(10, 6))

    # 自动检测所有出现过的 budget 值
    all_budgets = sorted(set().union(*(df["client_budget"].unique() for df in data.values())))
    if not all_budgets:
        return
    x = np.arange(len(all_budgets))
    width = 0.8 / max(len(data), 1)

    for i, (key, df) in enumerate(data.items()):
        meta = STRATEGIES[key]
        stress_df = df[df["phase"].isin(["high", "overload"])]
        rates = []
        for b in all_budgets:
            bdf = stress_df[stress_df["client_budget"] == b]
            rates.append(bdf["success"].mean() if len(bdf) > 0 else 0)

        bars = ax.bar(x + i * width, [r * 100 for r in rates], width,
                      label=meta["label"], color=meta["color"], alpha=0.85,
                      edgecolor="white", linewidth=0.5)
        for bar, val in zip(bars, rates):
            ax.text(bar.get_x() + bar.get_width() / 2, bar.get_height() + 1,
                    f"{val:.1%}", ha="center", va="bottom", fontsize=8, fontweight="bold")

    ax.set_xlabel("客户端 Token 预算", fontsize=12)
    ax.set_ylabel("请求成功率 (%)", fontsize=12)
    ax.set_title("高负载/过载阶段各预算组成功率对比", fontsize=13, fontweight="bold")
    ax.set_xticks(x + width * (len(data) - 1) / 2)
    ax.set_xticklabels([f"Budget {b}" for b in all_budgets], fontsize=11)
    ax.legend(fontsize=10)
    ax.set_ylim(0, 115)
    ax.yaxis.set_major_formatter(ticker.FormatStrFormatter("%.0f%%"))
    ax.grid(axis="y", alpha=0.3)

    save_fig(fig, "budget_fairness.png", subdir)


# ---------- 4. 延迟时间线 (箱线图按阶段) ----------

def plot_latency_boxplot(data: dict[str, pd.DataFrame], subdir: str):
    phases_present = [p for p in PHASE_ORDER if any(p in df["phase"].unique() for df in data.values())]
    n_phases = len(phases_present)
    if n_phases == 0:
        return

    fig, axes = plt.subplots(1, n_phases, figsize=(3 * n_phases, 5), sharey=True)
    if n_phases == 1:
        axes = [axes]

    for j, phase in enumerate(phases_present):
        ax = axes[j]
        box_data = []
        labels = []
        colors = []
        for key, df in data.items():
            pdf = df[(df["phase"] == phase) & df["success"] & (df["latency_ms"] > 0)]
            if not pdf.empty:
                box_data.append(pdf["latency_ms"].values)
                labels.append(key.replace("_", "\n"))
                colors.append(STRATEGIES[key]["color"])

        if box_data:
            bp = ax.boxplot(box_data, tick_labels=labels, patch_artist=True, showfliers=False,
                            widths=0.6, medianprops=dict(color="black", linewidth=1.5))
            for patch, c in zip(bp["boxes"], colors):
                patch.set_facecolor(c)
                patch.set_alpha(0.7)

        ax.set_title(PHASE_LABELS.get(phase, phase), fontsize=11, fontweight="bold")
        ax.tick_params(axis="x", labelsize=7)
        ax.grid(axis="y", alpha=0.3)
        if j == 0:
            ax.set_ylabel("延迟 (ms)", fontsize=11)

    fig.suptitle("各阶段延迟分布 (箱线图, 不含离群值)", fontsize=14, fontweight="bold")
    fig.tight_layout(rect=[0, 0, 1, 0.93])
    save_fig(fig, "latency_boxplot.png", subdir)


# ---------- 5. 吞吐量时间线 (按秒) ----------

def plot_throughput_timeline(data: dict[str, pd.DataFrame], subdir: str):
    fig, ax = plt.subplots(figsize=(14, 5))

    for key, df in data.items():
        meta = STRATEGIES[key]
        sdf = df[df["success"]].copy()
        if sdf.empty:
            continue
        t0 = sdf["timestamp"].min()
        sdf["sec"] = ((sdf["timestamp"] - t0) / 1000).astype(int)
        rps = sdf.groupby("sec").size()
        ax.plot(rps.index, rps.values, label=meta["label"], color=meta["color"],
                linewidth=1.5, alpha=0.85)

    # 阶段背景色
    if len(data) > 0:
        ref_df = list(data.values())[0]
        t0 = ref_df["timestamp"].min()
        for phase in ref_df["phase"].unique():
            pdf = ref_df[ref_df["phase"] == phase]
            x0 = (pdf["timestamp"].min() - t0) / 1000
            x1 = (pdf["timestamp"].max() - t0) / 1000
            ax.axvspan(x0, x1, alpha=0.15, color=PHASE_COLORS.get(phase, "#FFFFFF"))
            ax.text((x0 + x1) / 2, ax.get_ylim()[1] if ax.get_ylim()[1] > 0 else 100,
                    PHASE_LABELS.get(phase, phase), ha="center", va="bottom", fontsize=8,
                    color="gray", fontstyle="italic")

    ax.set_xlabel("时间 (秒)", fontsize=12)
    ax.set_ylabel("成功 RPS", fontsize=12)
    ax.set_title("吞吐量实时时间线", fontsize=14, fontweight="bold")
    ax.legend(fontsize=10)
    ax.grid(alpha=0.3)
    ax.set_ylim(bottom=0)

    save_fig(fig, "throughput_timeline.png", subdir)


# ---------- 6. 价格动态曲线 (Rajomon 专属) ----------

def plot_price_dynamics(df: pd.DataFrame, subdir: str):
    df = df.sort_values("timestamp").copy()
    t0 = df["timestamp"].min()
    df["rel_sec"] = (df["timestamp"] - t0) / 1000.0
    df["time_bin"] = np.floor(df["rel_sec"]).astype(int)

    agg = df.groupby("time_bin").agg(
        rps=("request_id", "count"),
        max_price=("price", "max"),
        mean_price=("price", "mean"),
        phase=("phase", "first"),
        rejected_count=("rejected", "sum"),
    ).reset_index()

    fig, ax1 = plt.subplots(figsize=(14, 6))

    # 阶段背景色
    for phase in df["phase"].unique():
        pdf = df[df["phase"] == phase]
        ax1.axvspan(pdf["rel_sec"].min(), pdf["rel_sec"].max(),
                    facecolor=PHASE_COLORS.get(phase, "#FFFFFF"), alpha=0.4)
        mid = (pdf["rel_sec"].min() + pdf["rel_sec"].max()) / 2
        ax1.text(mid, ax1.get_ylim()[1] if ax1.get_ylim()[1] > 0 else 1, PHASE_LABELS.get(phase, phase),
                 ha="center", va="bottom", fontsize=9, color="gray")

    # 左轴: RPS 柱状图
    color_rps = "#4A90E2"
    ax1.set_xlabel("时间 (秒)", fontsize=12)
    ax1.set_ylabel("每秒请求数 (RPS)", color=color_rps, fontsize=12)
    ax1.bar(agg["time_bin"], agg["rps"], color=color_rps, alpha=0.6, label="RPS")
    ax1.tick_params(axis="y", labelcolor=color_rps)

    # 右轴: 价格折线
    ax2 = ax1.twinx()
    color_price = "#D0021B"
    ax2.set_ylabel("动态价格", color=color_price, fontsize=12)
    ax2.plot(agg["time_bin"], agg["max_price"], color=color_price, linewidth=2.5,
             marker=".", markersize=3, label="最高价格/秒")
    ax2.plot(agg["time_bin"], agg["mean_price"], color="#FF6B6B", linewidth=1.2,
             linestyle="--", alpha=0.7, label="平均价格/秒")
    ax2.tick_params(axis="y", labelcolor=color_price)

    # 图例合并
    lines1, labels1 = ax1.get_legend_handles_labels()
    lines2, labels2 = ax2.get_legend_handles_labels()
    ax1.legend(lines1 + lines2, labels1 + labels2, loc="upper left", fontsize=9)

    ax1.set_title("Rajomon 动态价格曲线 vs 流量变化", fontsize=14, fontweight="bold")
    fig.tight_layout()
    save_fig(fig, "price_dynamics.png", subdir)


# ---------- 7. 价格 vs 预算拒绝分析 (散点图) ----------

def plot_price_vs_rejection(df: pd.DataFrame, subdir: str):
    df = df.sort_values("timestamp").copy()
    t0 = df["timestamp"].min()
    df["rel_sec"] = (df["timestamp"] - t0) / 1000.0

    fig, ax = plt.subplots(figsize=(14, 6))

    # 按预算组绘制散点
    budgets = sorted(df["client_budget"].unique())
    cmap = plt.cm.viridis
    colors = [cmap(i / max(len(budgets) - 1, 1)) for i in range(len(budgets))]

    for budget, color in zip(budgets, colors):
        bdf = df[df["client_budget"] == budget]
        accepted = bdf[~bdf["rejected"]]
        rejected = bdf[bdf["rejected"]]
        ax.scatter(accepted["rel_sec"], accepted["client_budget"],
                   c=[color], s=8, alpha=0.3, marker="o")
        ax.scatter(rejected["rel_sec"], rejected["client_budget"],
                   c=[color], s=12, alpha=0.6, marker="x")

    # 价格叠加
    ax2 = ax.twinx()
    df_price = df.groupby(np.floor(df["rel_sec"]).astype(int))["price"].max()
    ax2.plot(df_price.index, df_price.values, color="red", linewidth=2, alpha=0.8, label="动态价格")
    ax2.set_ylabel("当前价格", color="red", fontsize=11)
    ax2.tick_params(axis="y", labelcolor="red")

    # 阶段背景
    for phase in df["phase"].unique():
        pdf = df[df["phase"] == phase]
        ax.axvspan(pdf["rel_sec"].min(), pdf["rel_sec"].max(),
                   facecolor=PHASE_COLORS.get(phase, "#FFFFFF"), alpha=0.3)

    ax.set_xlabel("时间 (秒)", fontsize=12)
    ax.set_ylabel("客户端预算", fontsize=12)
    ax.set_title("Rajomon: 预算 vs 价格拒绝分析 (o=通过, x=拒绝)", fontsize=13, fontweight="bold")
    ax2.legend(fontsize=9, loc="upper right")
    ax.grid(alpha=0.2)

    save_fig(fig, "price_vs_rejection.png", subdir)


# ---------- 8. 公平性热力图 ----------

def plot_fairness_heatmap(data: dict[str, pd.DataFrame], subdir: str):
    phases_present = [p for p in PHASE_ORDER if any(p in df["phase"].unique() for df in data.values())]
    if not phases_present:
        return

    # 仅对 rajomon 做热力图
    rajomon_df = data.get("rajomon")
    if rajomon_df is None:
        return

    budgets = sorted(rajomon_df["client_budget"].unique())
    matrix = np.zeros((len(budgets), len(phases_present)))

    for i, b in enumerate(budgets):
        for j, p in enumerate(phases_present):
            pdf = rajomon_df[(rajomon_df["client_budget"] == b) & (rajomon_df["phase"] == p)]
            matrix[i, j] = pdf["success"].mean() if len(pdf) > 0 else 0

    fig, ax = plt.subplots(figsize=(max(8, len(phases_present) * 1.5), max(4, len(budgets) * 0.8)))
    im = ax.imshow(matrix * 100, cmap="RdYlGn", aspect="auto", vmin=0, vmax=100)

    ax.set_xticks(range(len(phases_present)))
    ax.set_xticklabels([PHASE_LABELS.get(p, p) for p in phases_present], fontsize=10)
    ax.set_yticks(range(len(budgets)))
    ax.set_yticklabels([f"Budget {b}" for b in budgets], fontsize=10)

    # 注标数值
    for i in range(len(budgets)):
        for j in range(len(phases_present)):
            val = matrix[i, j] * 100
            color = "white" if val < 40 else "black"
            ax.text(j, i, f"{val:.1f}%", ha="center", va="center", fontsize=9,
                    fontweight="bold", color=color)

    plt.colorbar(im, ax=ax, label="成功率 (%)", shrink=0.8)
    ax.set_title("Rajomon 预算公平性热力图 (各阶段 × 各预算)", fontsize=13, fontweight="bold")
    fig.tight_layout()
    save_fig(fig, "fairness_heatmap.png", subdir)


# ---------- 9. Summary 汇总对比 (横向柱状图) ----------

def plot_summary(csv_path: str, subdir: str):
    df = pd.read_csv(csv_path, encoding="utf-8-sig")
    if df.empty:
        return

    metrics = {
        "throughput_rps": "吞吐量 (RPS)",
        "rejection_rate": "拒绝率",
        "p95_latency_ms": "P95 延迟 (ms)",
        "p99_latency_ms": "P99 延迟 (ms)",
        "budget_10_success_rate": "Budget10 成功率",
        "budget_100_success_rate": "Budget100 成功率",
    }

    available_metrics = {k: v for k, v in metrics.items() if k in df.columns}
    n = len(available_metrics)
    if n == 0:
        return

    fig, axes = plt.subplots(1, n, figsize=(4 * n, 4))
    if n == 1:
        axes = [axes]

    for ax, (col, label) in zip(axes, available_metrics.items()):
        values = df[col].values
        strats = df["strategy"].values
        colors = [STRATEGIES.get(s, {"color": "gray"})["color"] for s in strats]
        bars = ax.barh(range(len(strats)), values, color=colors, alpha=0.85, edgecolor="white")
        ax.set_yticks(range(len(strats)))
        ax.set_yticklabels([STRATEGIES.get(s, {"label": s})["label"] for s in strats], fontsize=9)
        ax.set_xlabel(label, fontsize=10)
        ax.grid(axis="x", alpha=0.3)
        # 标注数值
        for bar, val in zip(bars, values):
            fmt = f"{val:.2f}" if val < 10 else f"{val:.0f}"
            ax.text(bar.get_width() + 0.01 * max(values), bar.get_y() + bar.get_height() / 2,
                    fmt, va="center", fontsize=8, fontweight="bold")

    fig.suptitle("策略汇总指标对比", fontsize=14, fontweight="bold")
    fig.tight_layout(rect=[0, 0, 1, 0.93])
    save_fig(fig, "summary_comparison.png", subdir)


# ---------- 10. 拒绝率/错误率时间线 ----------

def plot_rejection_timeline(data: dict[str, pd.DataFrame], subdir: str):
    fig, ax = plt.subplots(figsize=(14, 5))

    for key, df in data.items():
        meta = STRATEGIES[key]
        df_copy = df.copy()
        t0 = df_copy["timestamp"].min()
        df_copy["sec"] = ((df_copy["timestamp"] - t0) / 1000).astype(int)
        per_sec = df_copy.groupby("sec").agg(
            total=("request_id", "count"),
            rejected=("rejected", "sum"),
        )
        per_sec["reject_rate"] = per_sec["rejected"] / per_sec["total"]
        ax.plot(per_sec.index, per_sec["reject_rate"] * 100,
                label=meta["label"], color=meta["color"], linewidth=1.5, alpha=0.85)

    # 阶段背景
    if len(data) > 0:
        ref_df = list(data.values())[0]
        t0 = ref_df["timestamp"].min()
        for phase in ref_df["phase"].unique():
            pdf = ref_df[ref_df["phase"] == phase]
            x0 = (pdf["timestamp"].min() - t0) / 1000
            x1 = (pdf["timestamp"].max() - t0) / 1000
            ax.axvspan(x0, x1, alpha=0.12, color=PHASE_COLORS.get(phase, "#FFFFFF"))

    ax.set_xlabel("时间 (秒)", fontsize=12)
    ax.set_ylabel("拒绝率 (%)", fontsize=12)
    ax.set_title("拒绝率实时时间线", fontsize=14, fontweight="bold")
    ax.legend(fontsize=10)
    ax.grid(alpha=0.3)
    ax.set_ylim(0, 105)

    save_fig(fig, "rejection_timeline.png", subdir)


# ==================== 数据集处理器 ====================

def process_test_single(directory: str):
    """处理 test_single: 三策略六阶段完整对比"""
    print(f"\n{'='*60}")
    print(f"  处理 test_single 数据集")
    print(f"{'='*60}")

    data = load_strategy_data(directory)
    if not data:
        print("  [跳过] 无数据")
        return

    subdir = "test_single"
    plot_throughput_by_phase(data, subdir)
    plot_latency_cdf(data, subdir)
    plot_budget_fairness(data, subdir)
    plot_latency_boxplot(data, subdir)
    plot_throughput_timeline(data, subdir)
    plot_rejection_timeline(data, subdir)
    plot_fairness_heatmap(data, subdir)


def process_test_quick(directory: str):
    """处理 test_quick: 快速对比 + summary"""
    print(f"\n{'='*60}")
    print(f"  处理 test_quick 数据集")
    print(f"{'='*60}")

    data = load_strategy_data(directory)
    if not data:
        print("  [跳过] 无数据")
        return

    subdir = "test_quick"
    plot_throughput_by_phase(data, subdir)
    plot_latency_cdf(data, subdir)
    plot_budget_fairness(data, subdir)
    plot_throughput_timeline(data, subdir)
    plot_rejection_timeline(data, subdir)

    # summary CSV
    summary_files = sorted(glob.glob(os.path.join(directory, "summary_*.csv")))
    if summary_files:
        print(f"  [加载] Summary: {os.path.basename(summary_files[-1])}")
        plot_summary(summary_files[-1], subdir)


def process_test_fairness(directory: str):
    """处理 test_fairness: 过载公平性分析"""
    print(f"\n{'='*60}")
    print(f"  处理 test_fairness 数据集")
    print(f"{'='*60}")

    data = load_strategy_data(directory)
    if not data:
        print("  [跳过] 无数据")
        return

    subdir = "test_fairness"
    plot_budget_fairness(data, subdir)
    plot_fairness_heatmap(data, subdir)
    plot_latency_cdf(data, subdir)
    plot_rejection_timeline(data, subdir)


def process_test_full_step(directory: str):
    """处理 test_full_step: 完整阶梯负载三策略全量对比 + summary"""
    print(f"\n{'='*60}")
    print(f"  处理 test_full_step 数据集")
    print(f"{'='*60}")

    data = load_strategy_data(directory)
    if not data:
        print("  [跳过] 无数据")
        return

    subdir = "test_full_step"
    plot_throughput_by_phase(data, subdir)
    plot_latency_cdf(data, subdir)
    plot_budget_fairness(data, subdir)
    plot_latency_boxplot(data, subdir)
    plot_throughput_timeline(data, subdir)
    plot_rejection_timeline(data, subdir)
    plot_fairness_heatmap(data, subdir)

    # Rajomon 价格动态分析
    if "rajomon" in data:
        rajomon_df = data["rajomon"]
        # 确保 price 列为数值类型
        rajomon_df = rajomon_df.copy()
        rajomon_df["price"] = pd.to_numeric(rajomon_df["price"], errors="coerce").fillna(0)
        plot_price_dynamics(rajomon_df, subdir)
        plot_price_vs_rejection(rajomon_df, subdir)

    # summary CSV
    summary_files = sorted(glob.glob(os.path.join(directory, "summary_*.csv")))
    if summary_files:
        print(f"  [加载] Summary: {os.path.basename(summary_files[-1])}")
        plot_summary(summary_files[-1], subdir)


def process_test_price_dynamics(directory: str):
    """处理 test_price_dynamics: Rajomon 价格动态"""
    print(f"\n{'='*60}")
    print(f"  处理 test_price_dynamics 数据集")
    print(f"{'='*60}")

    # 查找 rajomon CSV
    files = sorted(glob.glob(os.path.join(directory, "rajomon_*.csv")))
    if not files:
        print("  [跳过] 无 rajomon 数据")
        return

    path = files[-1]
    print(f"  [加载] {os.path.basename(path)}")
    df = pd.read_csv(path, encoding="utf-8-sig")
    df["success"] = (~df["rejected"]) & (df["error_code"] == 0) & (df["status_code"] == 200)

    subdir = "test_price_dynamics"
    plot_price_dynamics(df, subdir)
    plot_price_vs_rejection(df, subdir)


# ==================== 主函数 ====================

def main():
    print("=" * 60)
    print("  MCP 服务治理 — 全量输出可视化")
    print("=" * 60)

    os.makedirs(FIG_DIR, exist_ok=True)

    # 数据集名称 → 处理函数
    processors = {
        "test_single":         process_test_single,
        "test_quick":          process_test_quick,
        "test_full_step":      process_test_full_step,
        "test_fairness":       process_test_fairness,
        "test_price_dynamics": process_test_price_dynamics,
    }

    processed = 0
    for name, processor in processors.items():
        directory = os.path.join(OUTPUT_BASE, name)
        if os.path.isdir(directory):
            processor(directory)
            processed += 1
        else:
            print(f"\n  [跳过] {name}: 目录不存在")

    # 检查是否有未识别的子目录
    if os.path.isdir(OUTPUT_BASE):
        for item in os.listdir(OUTPUT_BASE):
            full_path = os.path.join(OUTPUT_BASE, item)
            if os.path.isdir(full_path) and item not in processors:
                print(f"\n  [提示] 未处理的子目录: {item}")
                # 通用处理：尝试加载三策略数据画基础图
                data = load_strategy_data(full_path)
                if data:
                    plot_throughput_by_phase(data, item)
                    plot_latency_cdf(data, item)
                    plot_budget_fairness(data, item)
                    processed += 1

    print(f"\n{'='*60}")
    print(f"  完成! 共处理 {processed} 个数据集")
    print(f"  图表输出目录: {FIG_DIR}/")
    print(f"{'='*60}")


if __name__ == "__main__":
    main()
