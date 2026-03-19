#!/usr/bin/env python3
"""
PMU Counter 筛选与层次化性能分析
=================================
1. counters 筛选：从原始 CSV 中选出适合 MPKI 和命中率分析的核心 counters
2. 全局 MPKI 降维路径：计算各缓存/TLB 层次的 MPKI，展示逐层缺失削减瀑布图
3. 局部命中率隔离路径：各缓存/TLB 层次独立命中率时序分析

用法:
    python3 analyze_mpki_hitrate.py -p <project>
    python3 analyze_mpki_hitrate.py -p pmu_workload
    python3 analyze_mpki_hitrate.py          # 使用默认 pmu_timeseries.csv
"""
import argparse
import os
import sys
import numpy as np
import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
from datetime import datetime
# ═══════════════════════════════════════════════════════════════
# 1. COUNTER 筛选表
# ═══════════════════════════════════════════════════════════════

# 原始 CSV 中所有 19 个原始 counter 列（不含 _time_enabled/_time_running 辅助列）及其分析用途
ALL_RAW_COUNTERS = [
    # TLB 相关
    "dTLB-loads",                   # dTLB 加载访问次数 → dTLB 命中率分母、dTLB-load MPKI 参考
    "dTLB-load-misses",             # dTLB 加载缺失数  → dTLB-load MPKI 分子
    "dTLB-stores",                  # dTLB 存储访问次数 → dTLB-store 命中率分母
    "dTLB-store-misses",            # dTLB 存储缺失数  → dTLB-store MPKI 分子
    "iTLB-loads",                   # iTLB 加载访问次数 → iTLB 命中率分母
    "iTLB-load-misses",             # iTLB 加载缺失数  → iTLB-load MPKI 分子

    # L1 数据缓存
    "L1-dcache-loads",              # L1D 加载访问次数 → L1D 命中率分母
    "L1-dcache-load-misses",        # L1D 加载缺失数  → L1D-load MPKI 分子
    "l1d.replacement",              # L1D 行替换次数  → 与 load-misses 交叉验证

    # L1D 缺失压力
    "l1d_pend_miss.fb_full",        # Fill Buffer 满等待次数 → 缺失拥塞指标（辅助）
    "l1d_pend_miss.pending",        # 累计待填充缺失周期数  → 平均缺失代价分析（辅助）

    # LLC
    "LLC-loads",                    # LLC 加载访问次数 → LLC 命中率分母
    "LLC-load-misses",              # LLC 加载缺失数  → LLC-load MPKI 分子（主内存流量）
    "LLC-stores",                   # LLC 存储访问次数 → LLC-store 命中率分母
    "LLC-store-misses",             # LLC 存储缺失数  → LLC-store MPKI 分子（写回流量）

    # 内存访问指令（退休）
    "mem_inst_retired.all_loads",   # 所有退休 load 指令数 → 归一化参考
    "mem_inst_retired.all_stores",  # 所有退休 store 指令数
    "mem_inst_retired.any",         # 所有退休内存指令数

    # 指令
    "inst_retired.any",             # 所有退休指令数 → MPKI 分母
]

# ── 核心筛选子集 ─────────────────────────────────────────────────
# 去掉辅助 / 冗余 counter，仅保留全局 MPKI 降维路径和局部命中率隔离路径必需的 counter。
#
# 排除原因：
#   l1d.replacement        → 与 L1D-load-misses 高度线性相关，可由后者推导，冗余
#   l1d_pend_miss.fb_full  → 拥塞压力辅助指标，不直接参与 MPKI/命中率主路径
#   l1d_pend_miss.pending  → 周期累计计数，量纲不同，不适合直接比较
#   mem_inst_retired.any   → mem_inst_retired.all_loads + all_stores 已覆盖
#   LLC-stores / LLC-store-misses → store 路径缺失量级远小于 load，不构成主分析路径
#
SELECTED_COUNTERS = [
    # ── MPKI 分母 ────────────────
    "inst_retired.any",             # 指令数（MPKI = misses/inst * 1000）

    # ── TLB 层（命中率 + MPKI）───
    "dTLB-loads",
    "dTLB-load-misses",
    "iTLB-loads",
    "iTLB-load-misses",

    # ── L1D 层（命中率 + MPKI）───
    "L1-dcache-loads",
    "L1-dcache-load-misses",

    # ── LLC 层（命中率 + MPKI）───
    "LLC-loads",
    "LLC-load-misses",

    # ── 内存指令维度（辅助归一化）─
    "mem_inst_retired.all_loads",
    "mem_inst_retired.all_stores",
]

# ── 辅助（可选）counter，需要时单独加载 ─────────────────────────
AUXILIARY_COUNTERS = [
    "dTLB-stores",
    "dTLB-store-misses",
    "l1d.replacement",
    "l1d_pend_miss.fb_full",
    "l1d_pend_miss.pending",
    "LLC-stores",
    "LLC-store-misses",
    "mem_inst_retired.any",
]


def select_counters(df: pd.DataFrame, include_auxiliary: bool = False) -> pd.DataFrame:
    """
    筛选 counter 列，返回仅包含必要 counter 的 DataFrame。

    Parameters
    ----------
    df              : 原始 load_csv 读入的 DataFrame（含时间列）
    include_auxiliary: True 时额外保留 AUXILIARY_COUNTERS 中存在的列

    Returns
    -------
    筛选后的 DataFrame，保留 elapsed_ms, timestamp 及选定 counter 列。
    """
    base_cols = ["elapsed_ms", "timestamp"]
    if "elapsed_sec" in df.columns:
        base_cols.append("elapsed_sec")

    target = SELECTED_COUNTERS.copy()
    if include_auxiliary:
        target += AUXILIARY_COUNTERS

    keep = base_cols + [c for c in target if c in df.columns]
    missing = [c for c in target if c not in df.columns]
    if missing:
        print(f"  [警告] 以下 counter 在 CSV 中不存在，已跳过: {missing}")

    return df[keep].copy()


# ═══════════════════════════════════════════════════════════════
# 工具函数
# ═══════════════════════════════════════════════════════════════

def load_csv(csv_path: str) -> pd.DataFrame:
    if not os.path.exists(csv_path):
        raise FileNotFoundError(f"找不到文件: {csv_path}")
    df = pd.read_csv(csv_path)
    if df.empty:
        raise ValueError(f"文件无数据行: {csv_path}")
    df["timestamp"] = pd.to_datetime(df["timestamp"])
    t0 = df["timestamp"].iloc[0]
    df["elapsed_sec"] = (df["timestamp"] - t0).dt.total_seconds()
    print(f"读取 {len(df)} 条记录  {df['timestamp'].iloc[0]} ~ {df['timestamp'].iloc[-1]}")
    return df


def safe_col(df: pd.DataFrame, col: str) -> pd.Series:
    """安全取列：不存在时返回全 NaN 序列，避免 KeyError。"""
    if col in df.columns:
        return df[col].astype(float)
    return pd.Series(np.nan, index=df.index, dtype=float)


def safe_ratio(numer: pd.Series, denom: pd.Series,
               percent: bool = True,
               min_denom: float = 10.0,
               clip_max: float = None) -> pd.Series:
    """
    安全比率计算：denom < min_denom 时返回 NaN，防止小分母放大噪声。
    percent=True → 结果 × 100。
    clip_max != None → 裁剪上限。
    """
    d = denom.fillna(0).astype(float)
    n = numer.fillna(0).astype(float)
    factor = 100.0 if percent else 1.0
    with np.errstate(divide="ignore", invalid="ignore"):
        r = np.where(d >= min_denom, (n / d) * factor, np.nan)
    s = pd.Series(r, index=numer.index)
    if clip_max is not None:
        s = s.clip(lower=0, upper=clip_max)
    return s


def mpki(misses: pd.Series, insts: pd.Series) -> pd.Series:
    """计算 MPKI：misses per kilo instructions。"""
    return safe_ratio(misses, insts, percent=False, min_denom=1000.0) * 1000.0


def local_hit_rate(accesses: pd.Series, misses: pd.Series) -> pd.Series:
    """局部命中率 (%)：(accesses - misses) / accesses * 100。"""
    hits = accesses.fillna(0) - misses.fillna(0)
    return safe_ratio(hits, accesses, percent=True, min_denom=10.0, clip_max=100.0)


def save_fig(fig: plt.Figure, output_dir: str, filename: str):
    os.makedirs(output_dir, exist_ok=True)
    path = os.path.join(output_dir, filename)
    fig.savefig(path, dpi=150, bbox_inches="tight")
    print(f"  已保存: {path}")
    plt.close(fig)


def _fmt_elapsed(axes):
    for ax in np.array(axes).flat:
        ax.set_xlabel("Elapsed (s)")


# ═══════════════════════════════════════════════════════════════
# 2. 全局 MPKI 降维路径
# ═══════════════════════════════════════════════════════════════

# 内存层次的标准顺序（从快到慢，缺失数量应逐层递减）
MPKI_HIERARCHY = [
    ("dTLB",  "dTLB-load-misses"),
    ("iTLB",  "iTLB-load-misses"),
    ("L1D",   "L1-dcache-load-misses"),
    ("LLC",   "LLC-load-misses"),
]


def compute_global_mpki(df: pd.DataFrame) -> pd.DataFrame:
    """
    计算各层次的 MPKI（每千条指令的缺失数），返回 MPKI 的时序 DataFrame。

    Columns（与 elapsed_sec 对齐）：
        elapsed_sec, timestamp
        mpki_dTLB, mpki_iTLB, mpki_L1D, mpki_LLC
    """
    insts = safe_col(df, "inst_retired.any")
    result = df[["elapsed_sec", "timestamp"]].copy()

    for name, miss_col in MPKI_HIERARCHY:
        result[f"mpki_{name}"] = mpki(safe_col(df, miss_col), insts)

    return result


def compute_global_mpki_reduction(df: pd.DataFrame) -> pd.DataFrame:
    """
    全局 MPKI 降维路径分析。

    在各层次 MPKI 的基础上，增加以下"降维"指标：
      - LLC_absorption_rate (%): L1D 缺失中被 LLC 吸收的比例
            = (L1D-load-misses - LLC-load-misses) / L1D-load-misses * 100
      - L1D_to_LLC_reduction: L1D MPKI 与 LLC MPKI 之差（每千条指令被 LLC 削减的缺失数）
      - dominant_level: 当前采样周期内 MPKI 最高的层次（字符串标签）

    Returns
    -------
    DataFrame，包含 elapsed_sec、timestamp、各层 mpki_*、
    LLC_absorption_rate、L1D_to_LLC_reduction、dominant_level 列。
    """
    insts  = safe_col(df, "inst_retired.any")
    l1d_miss = safe_col(df, "L1-dcache-load-misses")
    llc_miss = safe_col(df, "LLC-load-misses")

    result = compute_global_mpki(df)

    # LLC 对 L1D 缺失的吸收率（LLC 命中的 L1D-miss 占比）
    result["LLC_absorption_rate"] = safe_ratio(
        l1d_miss - llc_miss, l1d_miss,
        percent=True, min_denom=10.0, clip_max=100.0
    )

    # L1D MPKI 与 LLC MPKI 之差 = LLC 每千指令吸收的额外缺失
    result["L1D_to_LLC_reduction"] = result["mpki_L1D"] - result["mpki_LLC"]

    # 逐行标记 MPKI 最高的层次（哪层是当前瓶颈）
    mpki_cols = [f"mpki_{name}" for name, _ in MPKI_HIERARCHY]
    mpki_vals = result[mpki_cols]
    result["dominant_level"] = mpki_vals.idxmax(axis=1).str.replace("mpki_", "", regex=False)

    return result


def print_mpki_summary(mpki_df: pd.DataFrame):
    """打印全局 MPKI 降维路径统计摘要。"""
    print("\n── 全局 MPKI 降维路径摘要 ──")
    stat_cols = [c for c in mpki_df.columns if c.startswith("mpki_") or
                 c in ("LLC_absorption_rate", "L1D_to_LLC_reduction")]
    for col in stat_cols:
        s = mpki_df[col].dropna()
        if s.empty:
            continue
        unit = "%" if "rate" in col else "misses/Ki"
        print(f"  {col:<28s}  均值={s.mean():>10.3f} {unit}  "
              f"[min={s.min():.3f}, max={s.max():.3f}]")

    if "dominant_level" in mpki_df.columns:
        counts = mpki_df["dominant_level"].value_counts()
        print(f"\n  主导 MPKI 层次分布（采样点数）:")
        for level, cnt in counts.items():
            print(f"    {level}: {cnt}")


def plot_global_mpki_reduction(mpki_df: pd.DataFrame, output_dir: str):
    """
    绘制全局 MPKI 降维路径图（3 个子图）：
      子图 1：各层次 MPKI 时序曲线（瀑布视图）
      子图 2：LLC 吸收率 + L1D→LLC MPKI 削减量时序
      子图 3：各层次 MPKI 均值柱状图（总体降维对比）
    """
    print("\n[Plot] Global MPKI reduction...")
    ts = mpki_df["elapsed_sec"]

    fig, axes = plt.subplots(1, 3, figsize=(22, 6))
    fig.suptitle("Global MPKI Reduction Path",
                 fontsize=13, fontweight="bold")

    # ── 子图 1：各层 MPKI 时序 ────────────────────────────────
    ax = axes[0]
    colors = {"dTLB": "steelblue", "iTLB": "darkorange",
              "L1D":  "tomato",    "LLC":  "seagreen"}
    for name, _ in MPKI_HIERARCHY:
        col = f"mpki_{name}"
        if col in mpki_df.columns:
            s = mpki_df[col]
            ax.plot(ts, s, color=colors[name], marker="o", markersize=3,
                    linewidth=1.5, label=f"{name} MPKI")
    ax.set_title("MPKI Time Series by Level", fontweight="bold")
    ax.set_ylabel("MPKI (misses / 1000 insts)")
    ax.set_xlabel("Elapsed (s)")
    ax.legend(fontsize=9)
    ax.grid(True, alpha=0.3)

    # ── 子图 2：LLC 吸收率 & L1D→LLC 削减量 ──────────────────
    ax = axes[1]
    ax2 = ax.twinx()

    if "LLC_absorption_rate" in mpki_df.columns:
        ax.plot(ts, mpki_df["LLC_absorption_rate"],
                color="purple", marker="^", markersize=3, linewidth=1.5,
                label="LLC Absorption Rate (%)")
        ax.set_ylabel("LLC Absorption Rate (%)", color="purple")
        ax.tick_params(axis="y", labelcolor="purple")
        ax.set_ylim(0, 100)

    if "L1D_to_LLC_reduction" in mpki_df.columns:
        ax2.plot(ts, mpki_df["L1D_to_LLC_reduction"],
                 color="coral", marker="s", markersize=3, linewidth=1.5,
                 linestyle="--", label="L1D→LLC Reduction (MPKI)")
        ax2.set_ylabel("L1D→LLC MPKI Reduction", color="coral")
        ax2.tick_params(axis="y", labelcolor="coral")

    ax.set_title("LLC Absorption Rate & MPKI Reduction", fontweight="bold")
    ax.set_xlabel("Elapsed (s)")
    # 合并两轴图例
    lines1, labels1 = ax.get_legend_handles_labels()
    lines2, labels2 = ax2.get_legend_handles_labels()
    ax.legend(lines1 + lines2, labels1 + labels2, fontsize=9, loc="lower left")
    ax.grid(True, alpha=0.3)

    # ── 子图 3：各层 MPKI 均值柱状图 ─────────────────────────
    ax = axes[2]
    level_names = [name for name, _ in MPKI_HIERARCHY]
    means = [mpki_df[f"mpki_{n}"].mean() for n in level_names]
    bar_colors = [colors[n] for n in level_names]
    bars = ax.bar(level_names, means, color=bar_colors, edgecolor="white", width=0.5)
    for bar, val in zip(bars, means):
        if not np.isnan(val):
            ax.text(bar.get_x() + bar.get_width() / 2, bar.get_height() + 0.02,
                    f"{val:.2f}", ha="center", va="bottom", fontsize=9)
    ax.set_title("Mean MPKI by Level", fontweight="bold")
    ax.set_ylabel("Mean MPKI")
    ax.set_xlabel("Cache / TLB Level")
    ax.grid(True, alpha=0.3, axis="y")

    plt.tight_layout()
    save_fig(fig, output_dir, "global_mpki_reduction.png")


# ═══════════════════════════════════════════════════════════════
# 3. 局部命中率隔离路径
# ═══════════════════════════════════════════════════════════════

# 各层次"局部命中率"的计算依据：accesses 与 misses 均为该层次本地计数，
# 不跨层折算，因此可以独立反映每层的过滤能力。
HIT_RATE_LAYERS = [
    # (层次标签,        accesses 列,          misses 列)
    ("dTLB-load",   "dTLB-loads",           "dTLB-load-misses"),
    ("iTLB-load",   "iTLB-loads",           "iTLB-load-misses"),
    ("L1D-load",    "L1-dcache-loads",      "L1-dcache-load-misses"),
    ("LLC-load",    "LLC-loads",            "LLC-load-misses"),
]


def compute_local_hit_rate_isolation(df: pd.DataFrame) -> pd.DataFrame:
    """
    局部命中率隔离路径分析。

    对每个缓存/TLB 层次独立计算其本地命中率（%）：
        local_hit_rate = (accesses - misses) / accesses × 100

    "隔离"含义：每层的命中率基于自身的访问/缺失计数，互不依赖，
    从而可以单独定位哪一层的命中率出现异常。

    Returns
    -------
    DataFrame，包含 elapsed_sec、timestamp 及各层 hit_rate_* 列。
    """
    result = df[["elapsed_sec", "timestamp"]].copy()

    for name, acc_col, miss_col in HIT_RATE_LAYERS:
        accesses = safe_col(df, acc_col)
        misses   = safe_col(df, miss_col)
        result[f"hit_rate_{name}"] = local_hit_rate(accesses, misses)

    # 计算各层命中率的标准差（衡量时间稳定性）
    hr_cols = [f"hit_rate_{name}" for name, _, _ in HIT_RATE_LAYERS]
    result["hit_rate_spread"] = result[hr_cols].std(axis=1)

    # 逐行标记命中率最低的层次（当前最弱环节）
    hr_vals = result[hr_cols]
    result["weakest_layer"] = hr_vals.idxmin(axis=1).str.replace("hit_rate_", "", regex=False)

    return result


def print_hit_rate_summary(hr_df: pd.DataFrame):
    """打印局部命中率隔离路径统计摘要。"""
    print("\n── 局部命中率隔离路径摘要 ──")
    for name, _, _ in HIT_RATE_LAYERS:
        col = f"hit_rate_{name}"
        s = hr_df[col].dropna()
        if s.empty:
            continue
        print(f"  {col:<26s}  均值={s.mean():>7.2f}%  "
              f"[min={s.min():.2f}%, max={s.max():.2f}%, std={s.std():.2f}%]")

    if "weakest_layer" in hr_df.columns:
        counts = hr_df["weakest_layer"].value_counts()
        print(f"\n  最弱层次分布（命中率最低的层次，按采样点数）:")
        for layer, cnt in counts.items():
            print(f"    {layer}: {cnt}")


def plot_local_hit_rate_isolation(hr_df: pd.DataFrame, output_dir: str):
    """
    绘制局部命中率隔离路径图（3 个子图）：
      子图 1：各层命中率时序曲线（隔离视图）
      子图 2：命中率时序热力图（矩阵视图，便于一眼发现异常层）
      子图 3：各层命中率均值 + 标准差误差棒柱状图
    """
    print("\n[Plot] Local hit rate isolation...")
    ts = hr_df["elapsed_sec"]
    layer_names = [name for name, _, _ in HIT_RATE_LAYERS]
    hr_cols     = [f"hit_rate_{name}" for name in layer_names]

    colors = {
        "dTLB-load": "steelblue",
        "iTLB-load": "darkorange",
        "L1D-load":  "tomato",
        "LLC-load":  "seagreen",
    }

    fig, axes = plt.subplots(1, 3, figsize=(22, 6))
    fig.suptitle("Local Hit Rate Isolation Path",
                 fontsize=13, fontweight="bold")

    # ── 子图 1：各层命中率时序 ────────────────────────────────
    ax = axes[0]
    for name in layer_names:
        col = f"hit_rate_{name}"
        if col in hr_df.columns:
            ax.plot(ts, hr_df[col], color=colors[name], marker="o",
                    markersize=3, linewidth=1.5, label=f"{name} Hit%")
    ax.set_title("Local Hit Rate Time Series", fontweight="bold")
    ax.set_ylabel("Local Hit Rate (%)")
    ax.set_xlabel("Elapsed (s)")
    ax.set_ylim(0, 101)
    ax.legend(fontsize=9)
    ax.grid(True, alpha=0.3)

    # ── 子图 2：命中率矩阵热力图 ──────────────────────────────
    ax = axes[1]
    matrix = hr_df[hr_cols].T.values.astype(float)
    im = ax.imshow(matrix, aspect="auto", cmap="RdYlGn",
                   vmin=0, vmax=100,
                   extent=[ts.iloc[0], ts.iloc[-1], -0.5, len(layer_names) - 0.5])
    ax.set_yticks(range(len(layer_names)))
    ax.set_yticklabels(layer_names[::-1], fontsize=9)
    ax.set_title("Hit Rate Heatmap (Green=High, Red=Low)", fontweight="bold")
    ax.set_xlabel("Elapsed (s)")
    plt.colorbar(im, ax=ax, label="Hit Rate (%)", fraction=0.046, pad=0.04)

    # ── 子图 3：各层均值 + 标准差柱状图 ─────────────────────
    ax = axes[2]
    means = [hr_df[f"hit_rate_{n}"].mean() for n in layer_names]
    stds  = [hr_df[f"hit_rate_{n}"].std()  for n in layer_names]
    bar_colors = [colors[n] for n in layer_names]
    bars = ax.bar(layer_names, means, yerr=stds, capsize=5,
                  color=bar_colors, edgecolor="white", alpha=0.85, width=0.5)
    for bar, val in zip(bars, means):
        if not np.isnan(val):
            ax.text(bar.get_x() + bar.get_width() / 2, bar.get_height() + 0.3,
                    f"{val:.1f}%", ha="center", va="bottom", fontsize=9)
    ax.set_title("Mean Local Hit Rate (error bars = stddev)", fontweight="bold")
    ax.set_ylabel("Mean Hit Rate (%)")
    ax.set_xlabel("Cache / TLB Layer")
    ax.set_ylim(0, 105)
    ax.grid(True, alpha=0.3, axis="y")
    plt.setp(ax.xaxis.get_majorticklabels(), rotation=15, ha="right")

    plt.tight_layout()
    save_fig(fig, output_dir, "local_hit_rate_isolation.png")


# ═══════════════════════════════════════════════════════════════
# 4. 联合概览图
# ═══════════════════════════════════════════════════════════════

def plot_combined_overview(mpki_df: pd.DataFrame, hr_df: pd.DataFrame, output_dir: str):
    """
    输出一张 2×4 概览图，将 MPKI 降维路径与命中率隔离路径并列展示，
    便于快速定位性能瓶颈所在层次。
    """
    print("\n[Plot] Combined overview...")
    ts = mpki_df["elapsed_sec"]

    fig, axes = plt.subplots(2, 4, figsize=(26, 10))
    fig.suptitle("MPKI Reduction & Hit Rate Isolation Overview",
                 fontsize=13, fontweight="bold")

    # 第一行：各层 MPKI
    mpki_specs = [
        ("dTLB", "steelblue"),
        ("iTLB", "darkorange"),
        ("L1D",  "tomato"),
        ("LLC",  "seagreen"),
    ]
    for ax, (name, color) in zip(axes[0], mpki_specs):
        col = f"mpki_{name}"
        s = mpki_df[col] if col in mpki_df else pd.Series(np.nan, index=ts.index)
        ax.plot(ts, s, color=color, marker="o", markersize=3, linewidth=1.5)
        ax.fill_between(ts, 0, s.fillna(0), color=color, alpha=0.15)
        ax.set_title(f"{name} MPKI", fontweight="bold")
        ax.set_ylabel("misses / 1K insts")
        ax.set_xlabel("Elapsed (s)")
        ax.grid(True, alpha=0.3)

    # 第二行：各层命中率  
    hr_specs = [
        ("dTLB-load", "steelblue"),
        ("iTLB-load", "darkorange"),
        ("L1D-load",  "tomato"),
        ("LLC-load",  "seagreen"),
    ]
    for ax, (name, color) in zip(axes[1], hr_specs):
        col = f"hit_rate_{name}"
        s = hr_df[col] if col in hr_df else pd.Series(np.nan, index=ts.index)
        ax.plot(ts, s, color=color, marker="s", markersize=3, linewidth=1.5)
        ax.fill_between(ts, 0, s.fillna(0), color=color, alpha=0.15)
        ax.set_title(f"{name} Hit Rate", fontweight="bold")
        ax.set_ylabel("Hit Rate (%)")
        ax.set_xlabel("Elapsed (s)")
        ax.set_ylim(0, 101)
        ax.grid(True, alpha=0.3)

    plt.tight_layout()
    save_fig(fig, output_dir, "combined_overview.png")


# ═══════════════════════════════════════════════════════════════
# 主入口
# ═══════════════════════════════════════════════════════════════

def resolve_csv(script_dir: str, project: str | None) -> str:
    if project:
        return os.path.normpath(
            os.path.join(script_dir, f"../log/pmu_timeseries_test_{project}.csv"))
    return os.path.normpath(os.path.join(script_dir, "../log/pmu_timeseries.csv"))


def main():
    parser = argparse.ArgumentParser(
        description="PMU MPKI 降维路径 & 局部命中率隔离路径分析")
    parser.add_argument("-p", "--project", default=None,
                        help="项目名，对应 pmu_timeseries_test_{project}.csv；"
                             "不指定则使用 pmu_timeseries.csv")
    parser.add_argument("--csv", default=None,
                        help="直接指定 CSV 路径（优先于 -p）")
    parser.add_argument("--aux", action="store_true",
                        help="保留辅助 counter（l1d.replacement 等）")
    args = parser.parse_args()

    script_dir = os.path.dirname(os.path.abspath(__file__))

    csv_path = args.csv or resolve_csv(script_dir, args.project)
    label    = args.project or "default"

    print(f"\n=== PMU MPKI & Hit Rate Analysis ===")
    print(f"CSV  : {csv_path}")
    print(f"Label: {label}\n")

    # ── 读取 & 筛选 counter ─────────────────────────────────
    raw_df = load_csv(csv_path)
    df = select_counters(raw_df, include_auxiliary=args.aux)
    print(f"\n已选 counter 列（{len(SELECTED_COUNTERS)} 个核心）:")
    for c in SELECTED_COUNTERS:
        avail = "  ✓" if c in df.columns else "  ✗ (缺失)"
        print(f"  {c}{avail}")

    # ── 全局 MPKI 降维路径 ──────────────────────────────────
    mpki_df = compute_global_mpki_reduction(df)
    print_mpki_summary(mpki_df)

    # ── 局部命中率隔离路径 ──────────────────────────────────
    hr_df = compute_local_hit_rate_isolation(df)
    print_hit_rate_summary(hr_df)

    # ── 生成图表 ─────────────────────────────────────────────
    timestamp  = datetime.now().strftime("%Y%m%d_%H%M%S")
    output_dir = os.path.normpath(
        os.path.join(script_dir, f"plots/{timestamp}_{label}_mpki_hitrate"))

    plot_global_mpki_reduction(mpki_df, output_dir)
    plot_local_hit_rate_isolation(hr_df,  output_dir)
    plot_combined_overview(mpki_df, hr_df, output_dir)

    print(f"\n全部图表已保存至: {output_dir}")


if __name__ == "__main__":
    main()
