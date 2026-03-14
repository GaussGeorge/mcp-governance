#!/usr/bin/env python3
"""
MCP Governance Integration Test Visualization (Publication-Ready)
==================================================================
Generates publication-quality figures from integration/output/ CSV data.

Academic standards applied:
  - Colorblind-friendly: distinct line styles + markers for all line plots
  - Dual output: PDF (vector) + PNG (raster) for every figure
  - Phase annotation text: bold, dark color, print-safe
  - Separated rejection rate (governance 429) and error rate (backend 5xx)
  - No internal working notes in titles
  - Recovery time always non-negative (absolute Δt)

Figures:
  1. Throughput time-series  — stacked success/rejection/error + Rajomon price
  2. Latency CDF             — success-only, with sample-size caveat annotation
  3. Fairness grouped bar    — all-phase + overload-phase panels
  4. Phase performance table — per-phase metrics for appendix
  5. Price response (3-panel)— request rate / price / rejection rate subplots
  6. Phase comparison bars   — throughput / P95 / rejection% / error% (2×2)
  7. Summary comparison bars — 6 global metrics
  8. Recovery time bars      — absolute seconds to recover
  9. Overload protection     — inset inside throughput OR standalone stacked area
  10. Comprehensive table    — global-average table for appendix
"""

import argparse
import glob
import os
import sys

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt
import matplotlib.ticker as ticker
import matplotlib.patches as mpatches
import numpy as np
import pandas as pd

# ==================== Global Style ====================
plt.rcParams.update(
    {
        "font.sans-serif": ["Arial Unicode MS", "SimHei", "DejaVu Sans"],
        "axes.unicode_minus": False,
        "figure.dpi": 150,
        "savefig.dpi": 300,
        "font.size": 10,
        "axes.labelsize": 11,
        "axes.titlesize": 12,
        "legend.fontsize": 9,
        "xtick.labelsize": 9,
        "ytick.labelsize": 9,
    }
)

# --- Colorblind-friendly palette with distinct markers & line styles ---
STRATEGY_COLORS = {
    "no_governance": "#d62728",  # red
    "static_rate_limit": "#ff7f0e",  # orange
    "rajomon": "#1f77b4",  # blue  (NOT green, to avoid red-green)
}
STRATEGY_LINESTYLES = {
    "no_governance": "-",  # solid
    "static_rate_limit": "--",  # dashed
    "rajomon": "-.",  # dash-dot
}
STRATEGY_MARKERS = {
    "no_governance": "o",  # circle
    "static_rate_limit": "s",  # square
    "rajomon": "^",  # triangle
}
STRATEGY_LABELS = {
    "no_governance": "No Governance",
    "static_rate_limit": "Static Rate Limit",
    "rajomon": "Rajomon (Dynamic Pricing)",
}

PHASE_ORDER = ["warmup", "low", "medium", "high", "overload", "recovery"]
PHASE_COLORS = {
    "warmup": "#d5dbdb",
    "low": "#d5f5e3",
    "medium": "#fdebd0",
    "high": "#fadbd8",
    "overload": "#f5b7b1",
    "recovery": "#aed6f1",
}

# --- Classification error codes ---
REJECTION_CODES = {-32001, -32002, -32003, -32000}
BACKEND_ERROR_CODES = {-32603}


# ==================== Helpers ====================
def _savefig(fig, output_dir: str, name: str):
    """Save figure as both PNG and PDF (vector)."""
    fig.savefig(os.path.join(output_dir, f"{name}.png"), bbox_inches="tight")
    fig.savefig(os.path.join(output_dir, f"{name}.pdf"), bbox_inches="tight")
    plt.close(fig)
    print(f"  ✅ {name}.png / .pdf")


def _classify(row):
    ec = row.get("error_code", 0)
    sc = row.get("status_code", -1)
    if ec in REJECTION_CODES:
        return "rejected"
    if ec in BACKEND_ERROR_CODES or sc != 200:
        return "error"
    if ec == 0 and sc == 200:
        return "success"
    return "error"


def preprocess(df: pd.DataFrame) -> pd.DataFrame:
    df = df.copy()
    df["cls"] = df.apply(_classify, axis=1)
    df["is_success"] = df["cls"] == "success"
    df["is_rejected"] = df["cls"] == "rejected"
    df["is_error"] = df["cls"] == "error"
    return df


def _add_phase_bg(ax, df, phase_col="phase"):
    """Add phase shading + bold dark labels safe for B&W print."""
    if phase_col not in df.columns or "time_sec" not in df.columns:
        return
    for phase in PHASE_ORDER:
        pdf = df[df[phase_col] == phase]
        if len(pdf) == 0:
            continue
        t0, t1 = pdf["time_sec"].min(), pdf["time_sec"].max()
        ax.axvspan(t0, t1, alpha=0.13, color=PHASE_COLORS.get(phase, "#fff"), zorder=0)
        ax.axvline(x=t0, color="#555", linestyle=":", linewidth=0.7, alpha=0.6)
        y_top = ax.get_ylim()[1]
        ax.text(
            (t0 + t1) / 2,
            y_top * 0.96,
            phase,
            ha="center",
            va="top",
            fontsize=8,
            fontweight="bold",
            color="#333",
            alpha=0.8,
        )


# ==================== Data Loading ====================
def load_csv_files(input_dir: str) -> dict[str, pd.DataFrame]:
    dfs: dict[str, pd.DataFrame] = {}
    for pat in ["int_*_run*.csv", "*_run*.csv"]:
        files = sorted(glob.glob(os.path.join(input_dir, pat)))
        if files:
            break
    for f in files:
        bn = os.path.basename(f)
        strat = next(
            (s for s in ["no_governance", "static_rate_limit", "rajomon"] if s in bn),
            None,
        )
        if strat is None:
            continue
        try:
            df = preprocess(pd.read_csv(f, encoding="utf-8-sig"))
            dfs[strat] = (
                pd.concat([dfs[strat], df], ignore_index=True) if strat in dfs else df
            )
            c = df["cls"].value_counts()
            print(
                f"  ✅ {bn} ({len(df)} rows) — success={c.get('success',0)}  "
                f"rejected={c.get('rejected',0)}  error={c.get('error',0)}"
            )
        except Exception as e:
            print(f"  ❌ {bn}: {e}")
    return dfs


def load_summary(input_dir: str):
    for pat in ["int_summary_*.csv", "summary_*.csv"]:
        files = sorted(glob.glob(os.path.join(input_dir, pat)))
        if files:
            return pd.read_csv(files[-1], encoding="utf-8-sig")
    return None


# ==================== Fig 1: Throughput Time-Series ====================
def plot_throughput_timeseries(dfs, out):
    n = len(dfs)
    fig, axes = plt.subplots(n, 1, figsize=(14, 3.8 * n), sharex=True)
    if n == 1:
        axes = [axes]

    for ax, (strat, df) in zip(axes, dfs.items()):
        if "timestamp" not in df.columns:
            continue
        df = df.copy()
        df["time_sec"] = (df["timestamp"] - df["timestamp"].min()) / 1000.0
        df["tb"] = df["time_sec"].astype(int)

        g = df.groupby("tb").agg(
            total=("request_id", "count"),
            success=("is_success", "sum"),
            rejected=("is_rejected", "sum"),
            errors=("is_error", "sum"),
        )

        ax.fill_between(
            g.index, g["success"], alpha=0.40, color="#2ecc71", label="Success"
        )
        ax.fill_between(
            g.index,
            g["success"],
            g["success"] + g["rejected"],
            alpha=0.45,
            color="#f39c12",
            label="Rejected (governance)",
        )
        ax.fill_between(
            g.index,
            g["success"] + g["rejected"],
            g["success"] + g["rejected"] + g["errors"],
            alpha=0.45,
            color="#e74c3c",
            label="Error (backend overload)",
        )
        ax.plot(g.index, g["total"], color="#333", lw=0.8, alpha=0.5)

        _add_phase_bg(ax, df)

        # Rajomon price overlay
        if strat == "rajomon" and "price" in df.columns:
            pdf = df[
                df["price"].notna() & (df["price"] != "") & (df["price"] != "0")
            ].copy()
            if len(pdf):
                pdf["pn"] = pd.to_numeric(pdf["price"], errors="coerce")
                pavg = pdf.groupby("tb")["pn"].mean().dropna()
                if len(pavg):
                    ax2 = ax.twinx()
                    ax2.plot(
                        pavg.index, pavg.values, "k--", lw=1.5, label="Dynamic Price"
                    )
                    ax2.set_ylabel("Dynamic Price", fontsize=10)
                    ax2.legend(loc="upper right", fontsize=8)

        ax.set_ylabel("Requests / sec")
        ax.set_title(STRATEGY_LABELS.get(strat, strat), fontweight="bold")
        ax.legend(loc="upper left", fontsize=8)
        ax.grid(True, alpha=0.3)

    axes[-1].set_xlabel("Time (s)")
    fig.suptitle(
        "Throughput Time-Series — Success / Rejection / Error Breakdown",
        fontsize=14,
        fontweight="bold",
    )
    plt.tight_layout()
    _savefig(fig, out, "throughput_timeseries")


# ==================== Fig 2: Latency CDF ====================
def plot_latency_cdf(dfs, out):
    fig, ax = plt.subplots(figsize=(10, 6))
    annots = []

    for strat, df in dfs.items():
        sdf = df[df["is_success"] & (df["latency_ms"] > 0)]
        lats = sdf["latency_ms"].sort_values()
        if len(lats) == 0:
            continue
        cdf = np.arange(1, len(lats) + 1) / len(lats)
        c = STRATEGY_COLORS[strat]
        ls = STRATEGY_LINESTYLES[strat]
        mk = STRATEGY_MARKERS[strat]
        label = f"{STRATEGY_LABELS[strat]} (n={len(lats):,})"
        # thin out markers for readability
        markevery = max(1, len(lats) // 15)
        ax.plot(
            lats.values,
            cdf,
            color=c,
            ls=ls,
            marker=mk,
            markevery=markevery,
            markersize=5,
            lw=2,
            label=label,
        )
        for p in [50, 95, 99]:
            v = np.percentile(lats, p)
            annots.append((v, p / 100, c, f"P{p}={v:.0f}ms"))

    for pl, ls in [(0.5, "--"), (0.95, "-."), (0.99, ":")]:
        ax.axhline(y=pl, color="gray", ls=ls, alpha=0.4, lw=0.8)
        ax.text(
            ax.get_xlim()[0] * 1.05 if ax.get_xlim()[0] > 0 else 0.5,
            pl + 0.012,
            f"P{int(pl*100)}",
            fontsize=8,
            color="gray",
        )

    for v, y, c, t in annots:
        ax.annotate(
            t,
            xy=(v, y),
            fontsize=7,
            color=c,
            alpha=0.9,
            xytext=(10, 0),
            textcoords="offset points",
            arrowprops=dict(arrowstyle="-", color=c, alpha=0.3, lw=0.5),
        )

    # Sample-size caveat box (editor suggestion)
    ax.text(
        0.98,
        0.04,
        "Note: Static RL has far fewer successful requests\n"
        "due to aggressive rate-limiting (most requests rejected).\n"
        "Its low latency is at the cost of very low throughput.",
        transform=ax.transAxes,
        fontsize=7.5,
        va="bottom",
        ha="right",
        bbox=dict(
            boxstyle="round,pad=0.4",
            facecolor="#fff3e0",
            alpha=0.85,
            edgecolor="#e65100",
        ),
    )

    ax.set_xlabel("Latency (ms)")
    ax.set_ylabel("Cumulative Probability")
    ax.set_title(
        "Latency CDF — Successful Requests Only", fontweight="bold", fontsize=13
    )
    ax.legend(fontsize=9)
    ax.grid(True, alpha=0.3)
    ax.set_xscale("log")
    plt.tight_layout()
    _savefig(fig, out, "latency_cdf")


# ==================== Fig 3: Fairness Grouped Bar ====================
def plot_fairness_grouped_bar(dfs, out):
    budgets = [10, 50, 100]
    fig, axes = plt.subplots(1, 2, figsize=(16, 6))

    for ai, (title, pf) in enumerate(
        [
            ("All-Phase Success Rate", None),
            ("Overload-Phase Success Rate", "overload"),
        ]
    ):
        ax = axes[ai]
        xp = np.arange(len(budgets))
        bw = 0.25

        for i, (strat, df) in enumerate(dfs.items()):
            filt = df.copy()
            if pf and "phase" in df.columns:
                tmp = df[df["phase"] == pf]
                if len(tmp):
                    filt = tmp
            rates = []
            for b in budgets:
                bd = filt[filt["client_budget"] == b]
                rates.append(bd["is_success"].sum() / max(len(bd), 1) * 100)
            bars = ax.bar(
                xp + (i - 1) * bw,
                rates,
                bw,
                color=STRATEGY_COLORS.get(strat, "#333"),
                alpha=0.85,
                label=STRATEGY_LABELS.get(strat, strat),
            )
            for bar, r in zip(bars, rates):
                ax.text(
                    bar.get_x() + bar.get_width() / 2,
                    bar.get_height() + 0.5,
                    f"{r:.1f}%",
                    ha="center",
                    va="bottom",
                    fontsize=8,
                )

        ax.set_xlabel("Client Budget (tokens)")
        ax.set_ylabel("Success Rate (%)")
        ax.set_title(title, fontweight="bold")
        ax.set_xticks(xp)
        ax.set_xticklabels([f"Budget {b}" for b in budgets])
        ax.legend(fontsize=8)
        ax.grid(True, alpha=0.3, axis="y")
        ax.set_ylim(0, 115)

    fig.suptitle(
        "Fairness Analysis — Success Rate by Budget Group",
        fontsize=14,
        fontweight="bold",
    )
    plt.tight_layout()
    _savefig(fig, out, "fairness_grouped_bar")


# ==================== Fig 4: Phase Performance Table ====================
def plot_phase_table(dfs, out):
    rows = []
    for strat, df in dfs.items():
        if "phase" not in df.columns:
            continue
        for phase in PHASE_ORDER:
            pdf = df[df["phase"] == phase]
            if not len(pdf):
                continue
            tot = len(pdf)
            sc = int(pdf["is_success"].sum())
            rc = int(pdf["is_rejected"].sum())
            ec = int(pdf["is_error"].sum())
            dur = max((pdf["timestamp"].max() - pdf["timestamp"].min()) / 1000.0, 0.5)
            lats = pdf[pdf["is_success"] & (pdf["latency_ms"] > 0)]["latency_ms"]
            p95 = lats.quantile(0.95) if len(lats) else 0
            hb = pdf[pdf["client_budget"] == 100]
            hbs = hb["is_success"].sum() / max(len(hb), 1) * 100
            rows.append(
                {
                    "Strategy": STRATEGY_LABELS.get(strat, strat),
                    "Phase": phase,
                    "Throughput": f"{sc / dur:.1f}",
                    "P95 (ms)": f"{p95:.0f}",
                    "Reject %": f"{rc / tot * 100:.1f}",
                    "Error %": f"{ec / tot * 100:.1f}",
                    "High-Budget Succ%": f"{hbs:.1f}",
                }
            )
    if not rows:
        print("  ⚠️  No phase data — skipping table")
        return

    tdf = pd.DataFrame(rows)
    fig, ax = plt.subplots(figsize=(18, max(8, len(rows) * 0.35 + 2)))
    ax.axis("off")
    cols = list(tdf.columns)
    cells = tdf.values.tolist()
    sbg = {
        STRATEGY_LABELS["no_governance"]: "#fce4ec",
        STRATEGY_LABELS["static_rate_limit"]: "#fff8e1",
        STRATEGY_LABELS["rajomon"]: "#e3f2fd",
    }
    cc = [[sbg.get(r[0], "#fff")] * len(cols) for r in cells]
    tab = ax.table(
        cellText=cells, colLabels=cols, cellColours=cc, loc="center", cellLoc="center"
    )
    tab.auto_set_font_size(False)
    tab.set_fontsize(9)
    tab.scale(1.0, 1.5)
    for j in range(len(cols)):
        tab[0, j].set_text_props(fontweight="bold")
        tab[0, j].set_facecolor("#b0bec5")
    eci = cols.index("Error %")
    rci = cols.index("Reject %")
    for i, r in enumerate(cells):
        if float(r[eci]) > 0:
            tab[i + 1, eci].set_text_props(color="#d32f2f", fontweight="bold")
        if float(r[rci]) > 50:
            tab[i + 1, rci].set_text_props(color="#e65100", fontweight="bold")
    fig.suptitle(
        "Per-Phase Performance Comparison\n"
        "Reject = governance-layer admission control;  "
        "Error = backend overload / system failure",
        fontsize=12,
        fontweight="bold",
        y=0.98,
    )
    plt.tight_layout()
    _savefig(fig, out, "phase_comparison_table")


# ==================== Fig 5: Price Response (3-panel) ====================
def plot_price_response(dfs, out):
    """Three vertically-stacked subplots sharing the X axis, avoiding dual-Y ambiguity."""
    if "rajomon" not in dfs:
        print("  ⚠️  No Rajomon data — skipping price response")
        return

    df = dfs["rajomon"].copy()
    if "price" not in df.columns or "timestamp" not in df.columns:
        return

    df["time_sec"] = (df["timestamp"] - df["timestamp"].min()) / 1000.0
    df["tb"] = df["time_sec"].astype(int)

    req_rate = df.groupby("tb")["request_id"].count()
    rej_rate = df.groupby("tb")["is_rejected"].apply(lambda x: x.mean() * 100)

    pdf = df[df["price"].notna() & (df["price"] != "") & (df["price"] != "0")].copy()
    pdf["pn"] = pd.to_numeric(pdf["price"], errors="coerce")
    price_avg = pdf.groupby("tb")["pn"].mean().dropna()

    fig, (ax1, ax2, ax3) = plt.subplots(
        3, 1, figsize=(14, 9), sharex=True, gridspec_kw={"height_ratios": [1, 1, 0.8]}
    )

    # Panel 1: Request Rate
    ax1.fill_between(req_rate.index, req_rate.values, alpha=0.2, color="#3498db")
    ax1.plot(req_rate.index, req_rate.values, color="#3498db", lw=1.2)
    ax1.set_ylabel("Request Rate (req/s)")
    ax1.grid(True, alpha=0.3)
    _add_phase_bg(ax1, df)

    # Panel 2: Dynamic Price (separate Y-axis, no dual-axis ambiguity)
    if len(price_avg):
        ax2.plot(price_avg.index, price_avg.values, color="#e74c3c", lw=2)
        ax2.fill_between(price_avg.index, price_avg.values, alpha=0.1, color="#e74c3c")
    for budget, ls in [(10, ":"), (50, "--"), (100, "-.")]:
        ax2.axhline(y=budget, color="gray", ls=ls, alpha=0.4, lw=0.8)
        ax2.text(
            1, budget + 1.5, f"Budget {budget}", fontsize=7.5, color="gray", alpha=0.7
        )
    ax2.set_ylabel("Dynamic Price (tokens)")
    ax2.grid(True, alpha=0.3)
    _add_phase_bg(ax2, df)

    # Panel 3: Rejection Rate
    ax3.fill_between(rej_rate.index, rej_rate.values, alpha=0.15, color="#f39c12")
    ax3.plot(rej_rate.index, rej_rate.values, color="#f39c12", lw=1.2)
    ax3.set_ylabel("Rejection Rate (%)")
    ax3.set_xlabel("Time (s)")
    ax3.grid(True, alpha=0.3)
    _add_phase_bg(ax3, df)

    # Δt annotation on price panel
    if len(price_avg):
        med = req_rate.median()
        t1_cands = req_rate[req_rate > med * 1.5].index
        if len(t1_cands):
            t1 = t1_cands[0]
            t2_cands = price_avg[price_avg > 10].index
            if len(t2_cands):
                t2 = t2_cands[0]
                if t2 > t1:
                    dt = t2 - t1
                    ym = ax2.get_ylim()[1] * 0.85
                    ax2.annotate(
                        f"Δt = {dt:.0f}s",
                        xy=((t1 + t2) / 2, ym),
                        fontsize=10,
                        color="#8e24aa",
                        fontweight="bold",
                        ha="center",
                        bbox=dict(boxstyle="round,pad=0.3", fc="#f3e5f5", alpha=0.85),
                    )
                    ax2.annotate(
                        "",
                        xy=(t2, ym * 0.95),
                        xytext=(t1, ym * 0.95),
                        arrowprops=dict(arrowstyle="<->", color="#8e24aa", lw=1.5),
                    )

    fig.suptitle("Rajomon Dynamic Pricing Response", fontsize=14, fontweight="bold")
    plt.tight_layout()
    _savefig(fig, out, "price_response")


# ==================== Fig 6: Phase Comparison Bars (2×2) ====================
def plot_phase_comparison(dfs, out):
    fig, axes = plt.subplots(2, 2, figsize=(16, 11))
    axes = axes.flatten()
    cfgs = [
        ("Successful Throughput (RPS)", "throughput"),
        ("P95 Latency (ms) — Success Only", "p95"),
        ("Rejection Rate (%) — Governance", "rej"),
        ("Error Rate (%) — Backend Overload", "err"),
    ]
    bw = 0.25
    for ai, (title, key) in enumerate(cfgs):
        ax = axes[ai]
        x = np.arange(len(PHASE_ORDER))
        for i, (strat, df) in enumerate(dfs.items()):
            vals = []
            for phase in PHASE_ORDER:
                pf = df[df["phase"] == phase]
                if not len(pf):
                    vals.append(0)
                    continue
                if key == "throughput":
                    dur = max(
                        (pf["timestamp"].max() - pf["timestamp"].min()) / 1000.0, 0.5
                    )
                    vals.append(pf["is_success"].sum() / dur)
                elif key == "p95":
                    l = pf[pf["is_success"] & (pf["latency_ms"] > 0)]["latency_ms"]
                    vals.append(l.quantile(0.95) if len(l) else 0)
                elif key == "rej":
                    vals.append(pf["is_rejected"].sum() / len(pf) * 100)
                elif key == "err":
                    vals.append(pf["is_error"].sum() / len(pf) * 100)
            bars = ax.bar(
                x + (i - 1) * bw,
                vals,
                bw,
                color=STRATEGY_COLORS.get(strat, "#333"),
                alpha=0.85,
                label=STRATEGY_LABELS.get(strat, strat),
            )
            # Only annotate bars that are visually significant
            max_val = max(vals) if vals else 1
            for bar, v in zip(bars, vals):
                if v > 0 and v > max_val * 0.05:
                    ax.text(
                        bar.get_x() + bar.get_width() / 2,
                        bar.get_height(),
                        f"{v:.1f}" if v >= 1 else f"{v:.2f}",
                        ha="center",
                        va="bottom",
                        fontsize=7,
                    )
        ax.set_xlabel("Phase")
        ax.set_ylabel(title)
        ax.set_title(title, fontweight="bold")
        ax.set_xticks(x)
        ax.set_xticklabels(PHASE_ORDER, rotation=25)
        ax.grid(True, alpha=0.3, axis="y")
        if ai == 0:
            ax.legend(fontsize=8)

    fig.suptitle("Per-Phase Performance Comparison", fontsize=14, fontweight="bold")
    plt.tight_layout()
    _savefig(fig, out, "phase_comparison")


# ==================== Fig 7: Summary Comparison Bars ====================
def plot_summary_comparison(dfs, summary_df, out):
    rows = []
    for strat, df in dfs.items():
        tot = len(df)
        s = int(df["is_success"].sum())
        r = int(df["is_rejected"].sum())
        e = int(df["is_error"].sum())
        dur = max((df["timestamp"].max() - df["timestamp"].min()) / 1000.0, 0.5)
        sl = df[df["is_success"] & (df["latency_ms"] > 0)]["latency_ms"]
        rows.append(
            {
                "strategy": strat,
                "throughput_rps": s / dur,
                "p50_latency_ms": sl.quantile(0.5) if len(sl) else 0,
                "p95_latency_ms": sl.quantile(0.95) if len(sl) else 0,
                "p99_latency_ms": sl.quantile(0.99) if len(sl) else 0,
                "rejection_rate": r / tot if tot else 0,
                "error_rate": e / tot if tot else 0,
            }
        )
    rdf = pd.DataFrame(rows)

    fig, axes = plt.subplots(2, 3, figsize=(16, 10))
    axes = axes.flatten()
    metrics = [
        ("throughput_rps", "Throughput (RPS)", "{:.1f}"),
        ("p50_latency_ms", "P50 Latency (ms)", "{:.1f}"),
        ("p95_latency_ms", "P95 Latency (ms)", "{:.1f}"),
        ("p99_latency_ms", "P99 Latency (ms)", "{:.1f}"),
        ("rejection_rate", "Rejection Rate (Gov.)", "{:.2%}"),
        ("error_rate", "Error Rate (Backend)", "{:.2%}"),
    ]
    for ax, (col, lbl, fmt) in zip(axes, metrics):
        if col not in rdf.columns:
            ax.set_visible(False)
            continue
        strats = rdf["strategy"].tolist()
        vals = rdf[col].tolist()
        colors = [STRATEGY_COLORS.get(s, "#333") for s in strats]
        labels = [STRATEGY_LABELS.get(s, s) for s in strats]
        bars = ax.bar(range(len(strats)), vals, color=colors, alpha=0.85)
        ax.set_xticks(range(len(strats)))
        ax.set_xticklabels(labels, fontsize=8, rotation=15)
        ax.set_ylabel(lbl)
        ax.set_title(lbl, fontweight="bold", fontsize=11)
        ax.grid(True, alpha=0.3, axis="y")
        for bar, v in zip(bars, vals):
            ax.text(
                bar.get_x() + bar.get_width() / 2,
                bar.get_height(),
                fmt.format(v),
                ha="center",
                va="bottom",
                fontsize=9,
            )

    fig.suptitle(
        "Three-Strategy Comparison — Key Metrics", fontsize=14, fontweight="bold"
    )
    plt.tight_layout()
    _savefig(fig, out, "summary_comparison")


# ==================== Fig 8: Recovery Time ====================
def plot_recovery_time(dfs, out):
    fig, ax = plt.subplots(figsize=(10, 6))
    rec_times, strat_names = [], []

    for strat, df in dfs.items():
        if "phase" not in df.columns or "timestamp" not in df.columns:
            continue
        df = df.copy()
        df["time_sec"] = (df["timestamp"] - df["timestamp"].min()) / 1000.0

        # Baseline from warmup (or low) — success only
        warmup = df[(df["phase"] == "warmup") & df["is_success"]]
        if not len(warmup):
            warmup = df[(df["phase"] == "low") & df["is_success"]]
        if not len(warmup):
            continue
        lats = warmup[warmup["latency_ms"] > 0]["latency_ms"]
        if not len(lats):
            continue
        baseline_p95 = lats.quantile(0.95)
        threshold = baseline_p95 * 2.0

        recov = df[df["phase"] == "recovery"].copy()
        if not len(recov):
            continue
        rec_start = recov["time_sec"].min()
        recov["tb"] = recov["time_sec"].astype(int)
        bp95 = recov.groupby("tb")["latency_ms"].quantile(0.95)

        rt = None
        for t, p95 in bp95.items():
            if p95 <= threshold:
                rt = t - rec_start
                break
        if rt is None:
            rt = recov["time_sec"].max() - rec_start

        # Absolute value — recovery time must be ≥ 0
        rt = max(abs(rt), 0)
        rec_times.append(rt)
        strat_names.append(strat)

    if not rec_times:
        print("  ⚠️  No recovery data — skipping")
        return

    colors = [STRATEGY_COLORS.get(s, "#333") for s in strat_names]
    labels = [STRATEGY_LABELS.get(s, s) for s in strat_names]
    bars = ax.bar(range(len(strat_names)), rec_times, color=colors, alpha=0.85)
    ax.set_xticks(range(len(strat_names)))
    ax.set_xticklabels(labels, fontsize=10)
    ax.set_ylabel("Recovery Time (seconds)")
    ax.set_title(
        "System Recovery Time (P95 latency returns to ≤ 2× baseline)",
        fontweight="bold",
        fontsize=12,
    )
    ax.grid(True, alpha=0.3, axis="y")
    ax.set_ylim(bottom=0)

    for bar, v in zip(bars, rec_times):
        ax.text(
            bar.get_x() + bar.get_width() / 2,
            bar.get_height() + 0.1,
            f"{v:.1f}s",
            ha="center",
            va="bottom",
            fontsize=11,
            fontweight="bold",
        )

    plt.tight_layout()
    _savefig(fig, out, "recovery_time_comparison")


# ==================== Fig 9: Overload Protection Effectiveness ====================
def plot_overload_protection(dfs, out):
    n = len(dfs)
    fig, axes = plt.subplots(1, n, figsize=(6 * n, 5), sharey=True)
    if n == 1:
        axes = [axes]

    for ax, (strat, df) in zip(axes, dfs.items()):
        if "phase" not in df.columns:
            continue
        pf = df[df["phase"].isin(["high", "overload"])].copy()
        if not len(pf):
            continue
        pf["time_sec"] = (pf["timestamp"] - pf["timestamp"].min()) / 1000.0
        pf["tb"] = pf["time_sec"].astype(int)
        g = pf.groupby("tb").agg(
            success=("is_success", "sum"),
            rejected=("is_rejected", "sum"),
            errors=("is_error", "sum"),
            total=("request_id", "count"),
        )

        ax.fill_between(
            g.index, 0, g["success"], alpha=0.5, color="#2ecc71", label="Success"
        )
        ax.fill_between(
            g.index,
            g["success"],
            g["success"] + g["rejected"],
            alpha=0.5,
            color="#f39c12",
            label="Rejected (gov.)",
        )
        ax.fill_between(
            g.index,
            g["success"] + g["rejected"],
            g["success"] + g["rejected"] + g["errors"],
            alpha=0.5,
            color="#e74c3c",
            label="Error (backend)",
        )

        tot = len(pf)
        s = int(pf["is_success"].sum())
        r = int(pf["is_rejected"].sum())
        e = int(pf["is_error"].sum())
        ax.set_title(
            f"{STRATEGY_LABELS.get(strat, strat)}\n"
            f"Succ {s} ({s/max(tot,1)*100:.0f}%)  "
            f"Rej {r} ({r/max(tot,1)*100:.0f}%)  "
            f"Err {e} ({e/max(tot,1)*100:.0f}%)",
            fontsize=10,
            fontweight="bold",
        )
        ax.set_xlabel("Time (s)")
        ax.legend(fontsize=8, loc="upper left")
        ax.grid(True, alpha=0.3)

    axes[0].set_ylabel("Requests / sec")
    fig.suptitle(
        "Overload Protection Effectiveness (High + Overload Phases)",
        fontsize=13,
        fontweight="bold",
    )
    plt.tight_layout()
    _savefig(fig, out, "overload_protection_effectiveness")


# ==================== Fig 10: Comprehensive Table ====================
def plot_comprehensive_table(dfs, out):
    rows = []
    for strat, df in dfs.items():
        tot = len(df)
        s = int(df["is_success"].sum())
        r = int(df["is_rejected"].sum())
        e = int(df["is_error"].sum())
        dur = max((df["timestamp"].max() - df["timestamp"].min()) / 1000.0, 0.5)
        sl = df[df["is_success"] & (df["latency_ms"] > 0)]["latency_ms"]
        hb = df[df["client_budget"] == 100]
        hbs = hb["is_success"].sum() / max(len(hb), 1) * 100
        rows.append(
            [
                STRATEGY_LABELS.get(strat, strat),
                f"{s / dur:.1f}",
                f"{sl.quantile(0.5):.0f}" if len(sl) else "—",
                f"{sl.quantile(0.95):.0f}" if len(sl) else "—",
                f"{sl.quantile(0.99):.0f}" if len(sl) else "—",
                f"{r / tot * 100:.1f}" if tot else "0",
                f"{e / tot * 100:.1f}" if tot else "0",
                f"{hbs:.1f}",
            ]
        )

    cols = [
        "Strategy",
        "Throughput",
        "P50 (ms)",
        "P95 (ms)",
        "P99 (ms)",
        "Reject %",
        "Error %",
        "Hi-Budget Succ%",
    ]
    fig, ax = plt.subplots(figsize=(16, 3))
    ax.axis("off")
    sbg = {
        STRATEGY_LABELS["no_governance"]: "#fce4ec",
        STRATEGY_LABELS["static_rate_limit"]: "#fff8e1",
        STRATEGY_LABELS["rajomon"]: "#e3f2fd",
    }
    cc = [[sbg.get(r[0], "#fff")] * len(cols) for r in rows]
    tab = ax.table(
        cellText=rows, colLabels=cols, cellColours=cc, loc="center", cellLoc="center"
    )
    tab.auto_set_font_size(False)
    tab.set_fontsize(10)
    tab.scale(1.0, 1.8)
    for j in range(len(cols)):
        tab[0, j].set_text_props(fontweight="bold")
        tab[0, j].set_facecolor("#b0bec5")
    eci = cols.index("Error %")
    for i, r in enumerate(rows):
        if float(r[eci]) > 0:
            tab[i + 1, eci].set_text_props(color="#d32f2f", fontweight="bold")

    fig.suptitle(
        "Three-Strategy Comparison — Global Averages",
        fontsize=13,
        fontweight="bold",
        y=1.05,
    )
    plt.tight_layout()
    _savefig(fig, out, "comprehensive_comparison_table")


# ==================== Main ====================
def main():
    parser = argparse.ArgumentParser(description="MCP Integration Test Visualization")
    parser.add_argument("--input", default="integration/output", help="CSV input dir")
    parser.add_argument(
        "--output", default="integration/figures", help="Figure output dir"
    )
    args = parser.parse_args()
    os.makedirs(args.output, exist_ok=True)

    print("=" * 64)
    print("  MCP Integration Test Visualization (Publication-Ready)")
    print("=" * 64)
    print("\n  Classification:")
    print(
        "    Rejected : governance-layer admission control  (codes -32001/-32002/-32003/-32000)"
    )
    print(
        "    Error    : backend overload / system failure   (code -32603 or status≠200)"
    )
    print("    Success  : status=200 and error_code=0\n")

    print("📂 Loading CSV data …")
    dfs = load_csv_files(args.input)
    if not dfs:
        print("❌ No CSV data found.  Run tests first: ./integration/run.sh")
        sys.exit(1)

    print(f"\n📊 Loaded {len(dfs)} strategies")
    sdf = load_summary(args.input)
    if sdf is not None:
        print(f"📊 Summary CSV: {len(sdf)} rows")

    print("\n🎨 Generating figures (10 total, PNG + PDF) …\n")
    tasks = [
        (
            " 1/10 Throughput time-series",
            lambda: plot_throughput_timeseries(dfs, args.output),
        ),
        (" 2/10 Latency CDF", lambda: plot_latency_cdf(dfs, args.output)),
        (
            " 3/10 Fairness grouped bar",
            lambda: plot_fairness_grouped_bar(dfs, args.output),
        ),
        (" 4/10 Phase table", lambda: plot_phase_table(dfs, args.output)),
        (
            " 5/10 Price response (3-panel)",
            lambda: plot_price_response(dfs, args.output),
        ),
        (
            " 6/10 Phase comparison bars",
            lambda: plot_phase_comparison(dfs, args.output),
        ),
        (
            " 7/10 Summary comparison bars",
            lambda: plot_summary_comparison(dfs, sdf, args.output),
        ),
        (" 8/10 Recovery time", lambda: plot_recovery_time(dfs, args.output)),
        (
            " 9/10 Overload protection",
            lambda: plot_overload_protection(dfs, args.output),
        ),
        (
            "10/10 Comprehensive table",
            lambda: plot_comprehensive_table(dfs, args.output),
        ),
    ]
    ok = 0
    for name, fn in tasks:
        try:
            fn()
            ok += 1
        except Exception as exc:
            import traceback

            print(f"  ❌ {name}: {exc}")
            traceback.print_exc()

    print(f"\n✅ Done: {ok}/{len(tasks)} figures saved to {args.output}/")
    print("=" * 64)


if __name__ == "__main__":
    main()
