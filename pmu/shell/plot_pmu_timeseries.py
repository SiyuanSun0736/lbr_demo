#!/usr/bin/env python3
"""
PMU Timeseries Visualization Script
读取 ../log/pmu_timeseries.csv 并为各类性能指标生成时间序列图表。
图片自动保存至 plots/ 目录。

用法:
    python3 plot_pmu_timeseries.py
"""

import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
import numpy as np
import os
from datetime import datetime


# ─────────────────────────── 工具函数 ───────────────────────────

def load_csv(csv_path: str) -> pd.DataFrame:
    if not os.path.exists(csv_path):
        raise FileNotFoundError(f"错误: 找不到文件 {csv_path}")
    df = pd.read_csv(csv_path)
    if df.empty:
        raise ValueError("警告: 文件为空")
    df['timestamp'] = pd.to_datetime(df['timestamp'])
    print(f"成功读取 {len(df)} 条记录，时间范围: "
          f"{df['timestamp'].iloc[0]} ~ {df['timestamp'].iloc[-1]}")
    return df


def preprocess_time_columns(df: pd.DataFrame, tr_fraction: float = 0.1) -> pd.DataFrame:
    """基于 time_running 列清洗数据：
    - 计算采样间隔的中位数（秒），将 min_tr = tr_fraction * interval_ns 作为阈值
    - 对每个存在 `<metric>_time_running` 的 `<metric>`，若 time_running < min_tr 则将该 metric 置为 NaN
    - 为每个 metric 创建 `<metric>_bad_tr` 布尔列，标记被过滤的样本
    返回新的 DataFrame（不修改原 df）
    """
    df2 = df.copy()
    # 估算采样间隔（秒），容错回退到 1s
    diffs = df2['timestamp'].diff().dt.total_seconds().dropna()
    sample_sec = float(diffs.median()) if len(diffs) > 0 else 1.0
    min_tr_ns = sample_sec * 1e9 * tr_fraction
    print(f"样本间隔估算: {sample_sec:.3f}s, min time_running 阈值: {min_tr_ns:.0f} ns")

    for col in list(df2.columns):
        if col.endswith('_time_running'):
            base = col[:-len('_time_running')]
            bad_col = base + '_bad_tr'
            if base in df2.columns:
                mask = df2[col].fillna(0).astype(float) < min_tr_ns
                df2.loc[mask, base] = np.nan
                df2[bad_col] = mask
    return df2


def save_fig(fig: plt.Figure, output_dir: str, filename: str):
    os.makedirs(output_dir, exist_ok=True)
    path = os.path.join(output_dir, filename)
    fig.savefig(path, dpi=150, bbox_inches='tight')
    print(f"  已保存: {path}")
    plt.close(fig)


def fmt_axes_time(axes):
    """对所有子图格式化 x 轴时间戳"""
    for ax in np.array(axes).flat:
        ax.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
        plt.setp(ax.xaxis.get_majorticklabels(), rotation=40, ha='right')


def safe_ratio(numer: pd.Series, denom: pd.Series, percent: bool = True,
               min_denom: float = 5.0, clip_max: float = 100.0) -> pd.Series:
    """安全计算比率：
    - 当 denom < min_denom 时返回 NaN（避免被小分母放大噪声）
    - percent=True 时结果乘以 100
    - clip_max != None 时将结果裁剪到 [0, clip_max]
    """
    denom_filled = denom.fillna(0).astype(float)
    numer_filled = numer.fillna(0).astype(float)
    factor = 100.0 if percent else 1.0
    with np.errstate(divide='ignore', invalid='ignore'):
        ratio = np.where(denom_filled >= min_denom,
                         (numer_filled / denom_filled) * factor,
                         np.nan)
    s = pd.Series(ratio, index=numer.index)
    if clip_max is not None:
        s = s.clip(lower=0, upper=clip_max)
    return s


def miss_rate(hits: pd.Series, misses: pd.Series, min_denom: float = 5.0) -> pd.Series:
    """兼容旧接口的 miss rate 计算，返回百分比并裁剪到 0-100。"""
    return safe_ratio(misses, hits, percent=True, min_denom=min_denom, clip_max=100.0)


def print_stats(df: pd.DataFrame, cols: list):
    print("  统计信息:")
    for col in cols:
        if col in df.columns:
            s = df[col]
            print(f"    {col:40s} 均值={s.mean():>14.0f}  最大={s.max():>14.0f}  最小={s.min():>14.0f}")


# ─────────────────────── 1. TLB 指标 ───────────────────────────

def plot_tlb(df: pd.DataFrame, output_dir: str):
    print("\n[1/5] 绘制 TLB 指标...")

    fig, axes = plt.subplots(2, 2, figsize=(16, 10), sharex=True)
    fig.suptitle('TLB Performance Metrics', fontsize=15, fontweight='bold')
    ts = df['timestamp']

    ax = axes[0, 0]
    ax.plot(ts, df['dTLB-loads'], 'b-o', markersize=3, linewidth=1.5, label='dTLB-loads')
    ax.set_title('dTLB Loads', fontweight='bold')
    ax.set_ylabel('Count')
    ax.legend(); ax.grid(True, alpha=0.3)

    ax = axes[0, 1]
    ax.plot(ts, df['dTLB-stores'], 'g-o', markersize=3, linewidth=1.5, label='dTLB-stores')
    ax.set_title('dTLB Stores', fontweight='bold')
    ax.set_ylabel('Count')
    ax.legend(); ax.grid(True, alpha=0.3)

    ax = axes[1, 0]
    ax.plot(ts, df['iTLB-loads'], 'c-o', markersize=3, linewidth=1.5, label='iTLB-loads')
    ax.plot(ts, df['iTLB-load-misses'], color='orange', marker='s', markersize=3, linewidth=1.5, label='iTLB-load-misses')
    ax.set_title('iTLB Loads & Misses', fontweight='bold')
    ax.set_ylabel('Count'); ax.set_xlabel('Time')
    ax.legend(); ax.grid(True, alpha=0.3)

    ax = axes[1, 1]
    ax.plot(ts, miss_rate(df['dTLB-loads'],  df['dTLB-load-misses']),  'r-o', markersize=3, linewidth=1.5, label='dTLB Load Miss Rate')
    ax.plot(ts, miss_rate(df['dTLB-stores'], df['dTLB-store-misses']), 'm-s', markersize=3, linewidth=1.5, label='dTLB Store Miss Rate')
    ax.plot(ts, miss_rate(df['iTLB-loads'],  df['iTLB-load-misses']),  color='orange', marker='^', markersize=3, linewidth=1.5, label='iTLB Load Miss Rate')
    ax.set_title('TLB Miss Rates', fontweight='bold')
    ax.set_ylabel('Miss Rate (%)'); ax.set_xlabel('Time')
    ax.legend(); ax.grid(True, alpha=0.3)

    fmt_axes_time(axes)
    plt.tight_layout()
    save_fig(fig, output_dir, 'tlb_metrics.png')

    print_stats(df, ['dTLB-loads', 'dTLB-load-misses', 'dTLB-stores', 'dTLB-store-misses',
                     'iTLB-loads', 'iTLB-load-misses'])


# ─────────────────────── 2. L1 Cache 指标 ──────────────────────

def plot_l1_cache(df: pd.DataFrame, output_dir: str):
    print("\n[2/5] 绘制 L1 Cache 指标...")

    fig, axes = plt.subplots(2, 2, figsize=(16, 10), sharex=True)
    fig.suptitle('L1 Cache Performance Metrics', fontsize=15, fontweight='bold')
    ts = df['timestamp']

    ax = axes[0, 0]
    ax.plot(ts, df['L1-dcache-loads'], 'b-o', markersize=3, linewidth=1.5, label='L1-dcache-loads')
    ax.set_title('L1 D-Cache Loads', fontweight='bold')
    ax.set_ylabel('Count')
    ax.legend(); ax.grid(True, alpha=0.3)

    ax = axes[0, 1]
    ax.plot(ts, df['L1-dcache-stores'], 'g-o', markersize=3, linewidth=1.5, label='L1-dcache-stores')
    ax.set_title('L1 D-Cache Stores', fontweight='bold')
    ax.set_ylabel('Count')
    ax.legend(); ax.grid(True, alpha=0.3)

    ax = axes[1, 0]
    ax.plot(ts, df['L1-dcache-load-misses'], 'r-o', markersize=3, linewidth=1.5, label='L1-dcache-load-misses')
    ax.plot(ts, df['L1-icache-load-misses'], 'orange', marker='s', markersize=3, linewidth=1.5, label='L1-icache-load-misses')
    ax.set_title('L1 Cache Load Misses', fontweight='bold')
    ax.set_ylabel('Count'); ax.set_xlabel('Time')
    ax.legend(); ax.grid(True, alpha=0.3)

    ax = axes[1, 1]
    ax.plot(ts, miss_rate(df['L1-dcache-loads'], df['L1-dcache-load-misses']), 'r-o', markersize=3, linewidth=1.5, label='L1-dcache Load Miss Rate')
    ax.plot(ts, df['l1d.replacement'], 'purple', marker='^', markersize=3, linewidth=1.5, label='l1d.replacement')
    ax.set_title('L1 D-Cache Miss Rate & Replacement', fontweight='bold')
    ax.set_ylabel('Miss Rate (%) / Count'); ax.set_xlabel('Time')
    ax.legend(); ax.grid(True, alpha=0.3)

    fmt_axes_time(axes)
    plt.tight_layout()
    save_fig(fig, output_dir, 'l1_cache_metrics.png')

    print_stats(df, ['L1-dcache-loads', 'L1-dcache-load-misses', 'L1-dcache-stores',
                     'L1-icache-load-misses', 'l1d.replacement'])


# ─────────────── 3. L1d Pending Miss 指标 ──────────────────────

def plot_l1d_pending(df: pd.DataFrame, output_dir: str):
    print("\n[3/5] 绘制 L1d Pending Miss 指标...")

    fig, axes = plt.subplots(1, 3, figsize=(18, 5), sharex=True)
    fig.suptitle('L1D Pending Miss Metrics', fontsize=15, fontweight='bold')
    ts = df['timestamp']

    colors = ['steelblue', 'tomato', 'seagreen']
    cols   = ['l1d.replacement', 'l1d_pend_miss.fb_full', 'l1d_pend_miss.pending']
    titles = ['L1D Replacement', 'L1D Pend Miss: FB Full', 'L1D Pend Miss: Pending']

    for ax, col, title, color in zip(axes, cols, titles, colors):
        ax.plot(ts, df[col], color=color, marker='o', markersize=3, linewidth=1.5, label=col)
        ax.set_title(title, fontweight='bold')
        ax.set_ylabel('Count')
        ax.set_xlabel('Time')
        ax.legend(); ax.grid(True, alpha=0.3)

    fmt_axes_time(axes)
    plt.tight_layout()
    save_fig(fig, output_dir, 'l1d_pending_miss.png')

    print_stats(df, cols)


# ─────────────────────── 4. LLC 指标 ───────────────────────────

def plot_llc(df: pd.DataFrame, output_dir: str):
    print("\n[4/5] 绘制 LLC 指标...")

    fig, axes = plt.subplots(2, 2, figsize=(16, 10), sharex=True)
    fig.suptitle('LLC (Last Level Cache) Performance Metrics', fontsize=15, fontweight='bold')
    ts = df['timestamp']

    ax = axes[0, 0]
    ax.plot(ts, df['LLC-loads'], 'b-o', markersize=3, linewidth=1.5, label='LLC-loads')
    ax.set_title('LLC Loads', fontweight='bold')
    ax.set_ylabel('Count')
    ax.legend(); ax.grid(True, alpha=0.3)

    ax = axes[0, 1]
    ax.plot(ts, df['LLC-stores'], 'g-o', markersize=3, linewidth=1.5, label='LLC-stores')
    ax.set_title('LLC Stores', fontweight='bold')
    ax.set_ylabel('Count')
    ax.legend(); ax.grid(True, alpha=0.3)

    ax = axes[1, 0]
    ax.plot(ts, df['LLC-load-misses'],  'r-o', markersize=3, linewidth=1.5, label='LLC-load-misses')
    ax.plot(ts, df['LLC-store-misses'], 'm-s', markersize=3, linewidth=1.5, label='LLC-store-misses')
    ax.set_title('LLC Misses', fontweight='bold')
    ax.set_ylabel('Count'); ax.set_xlabel('Time')
    ax.legend(); ax.grid(True, alpha=0.3)

    ax = axes[1, 1]
    ax.plot(ts, miss_rate(df['LLC-loads'],  df['LLC-load-misses']),  'r-o', markersize=3, linewidth=1.5, label='LLC Load Miss Rate')
    ax.plot(ts, miss_rate(df['LLC-stores'], df['LLC-store-misses']), 'm-s', markersize=3, linewidth=1.5, label='LLC Store Miss Rate')
    ax.plot(ts, miss_rate(df['cache-references'], df['cache-misses']), 'orange', marker='^', markersize=3, linewidth=1.5, label='Cache Miss Rate')
    ax.set_title('LLC / Cache Miss Rates', fontweight='bold')
    ax.set_ylabel('Miss Rate (%)'); ax.set_xlabel('Time')
    ax.legend(); ax.grid(True, alpha=0.3)

    fmt_axes_time(axes)
    plt.tight_layout()
    save_fig(fig, output_dir, 'llc_metrics.png')

    print_stats(df, ['LLC-loads', 'LLC-load-misses', 'LLC-stores', 'LLC-store-misses',
                     'cache-references', 'cache-misses'])


# ─────────────────── 5. 内存访问指令指标 ────────────────────────

def plot_mem_inst(df: pd.DataFrame, output_dir: str):
    print("\n[5/5] 绘制内存访问指令指标...")

    fig, axes = plt.subplots(2, 2, figsize=(16, 10), sharex=True)
    fig.suptitle('Memory Access Instructions Metrics', fontsize=15, fontweight='bold')
    ts = df['timestamp']

    ax = axes[0, 0]
    ax.plot(ts, df['mem_inst_retired.all_loads'],  'b-o', markersize=3, linewidth=1.5, label='all_loads')
    ax.plot(ts, df['mem_inst_retired.all_stores'], 'g-s', markersize=3, linewidth=1.5, label='all_stores')
    ax.set_title('mem_inst_retired: Loads & Stores', fontweight='bold')
    ax.set_ylabel('Count')
    ax.legend(); ax.grid(True, alpha=0.3)

    ax = axes[0, 1]
    ax.plot(ts, df['mem_inst_retired.any'], 'purple', marker='o', markersize=3, linewidth=1.5, label='any')
    ax.set_title('mem_inst_retired: Any', fontweight='bold')
    ax.set_ylabel('Count')
    ax.legend(); ax.grid(True, alpha=0.3)

    ax = axes[1, 0]
    ax.plot(ts, df['mem-loads'],  'steelblue', marker='o', markersize=3, linewidth=1.5, label='mem-loads')
    ax.plot(ts, df['mem-stores'], 'tomato',    marker='s', markersize=3, linewidth=1.5, label='mem-stores')
    ax.set_title('mem-loads / mem-stores (perf event)', fontweight='bold')
    ax.set_ylabel('Count'); ax.set_xlabel('Time')
    ax.legend(); ax.grid(True, alpha=0.3)

    ax = axes[1, 1]
    # 使用安全比率计算，避免小分母导致 >100% 的异常值
    denom_series = df['mem_inst_retired.any'] if 'mem_inst_retired.any' in df.columns else pd.Series(0, index=df.index)
    load_ratio = safe_ratio(df['mem_inst_retired.all_loads'], denom_series, percent=True, min_denom=5.0)
    store_ratio = safe_ratio(df['mem_inst_retired.all_stores'], denom_series, percent=True, min_denom=5.0)
    ax.plot(ts, load_ratio,  'b-o', markersize=3, linewidth=1.5, label='Load / Any (%)')
    ax.plot(ts, store_ratio, 'g-s', markersize=3, linewidth=1.5, label='Store / Any (%)')
    ax.set_title('Load & Store Ratio in mem_inst_retired', fontweight='bold')
    ax.set_ylabel('Ratio (%)'); ax.set_xlabel('Time')
    ax.legend(); ax.grid(True, alpha=0.3)

    fmt_axes_time(axes)
    plt.tight_layout()
    save_fig(fig, output_dir, 'mem_inst_metrics.png')

    print_stats(df, ['mem_inst_retired.all_loads', 'mem_inst_retired.all_stores',
                     'mem_inst_retired.any', 'mem-loads', 'mem-stores'])


# ──────────────── 6. 综合概览 (All-in-One) ──────────────────────

def plot_overview(df: pd.DataFrame, output_dir: str):
    """一张大图展示所有关键指标的走势，便于对比"""
    print("\n[+] 绘制综合概览...")

    metrics = [
        ('dTLB-loads',                  'dTLB Loads',          'steelblue'),
        ('dTLB-load-misses',            'dTLB Load Misses',    'royalblue'),
        ('L1-dcache-loads',             'L1D Loads',           'seagreen'),
        ('L1-dcache-load-misses',       'L1D Load Misses',     'limegreen'),
        ('l1d_pend_miss.pending',       'L1D Pend Pending',    'darkorange'),
        ('LLC-loads',                   'LLC Loads',           'firebrick'),
        ('LLC-load-misses',             'LLC Load Misses',     'tomato'),
        ('mem_inst_retired.all_loads',  'mem_inst Loads',      'purple'),
        ('mem_inst_retired.all_stores', 'mem_inst Stores',     'mediumpurple'),
    ]

    fig, axes = plt.subplots(3, 3, figsize=(18, 13), sharex=True)
    fig.suptitle('PMU Timeseries – All Key Metrics Overview', fontsize=15, fontweight='bold')
    ts = df['timestamp']

    for ax, (col, title, color) in zip(axes.flat, metrics):
        if col in df.columns:
            ax.plot(ts, df[col], color=color, marker='o', markersize=2, linewidth=1.3, label=col)
        ax.set_title(title, fontweight='bold', fontsize=10)
        ax.set_ylabel('Count', fontsize=9)
        ax.legend(fontsize=8); ax.grid(True, alpha=0.3)

    fmt_axes_time(axes)
    plt.tight_layout()
    save_fig(fig, output_dir, 'overview.png')


# ─────────────────────────── 主入口 ─────────────────────────────

def main():
    script_dir = os.path.dirname(os.path.abspath(__file__))

    csv_path = os.path.normpath(os.path.join(script_dir, '../log/pmu_timeseries.csv'))
    # 将输出目录放在按时间命名的子目录中，方便归档
    timestamp = datetime.now().strftime('%Y%m%d_%H%M%S')
    output_dir = os.path.normpath(os.path.join(script_dir, 'plots', timestamp))

    print(f"输入文件: {csv_path}")
    print(f"输出目录: {output_dir}")

    df = load_csv(csv_path)
    # 根据 time_running 过滤不可靠样本
    df_filtered = preprocess_time_columns(df, tr_fraction=0.1)

    plot_tlb(df_filtered, output_dir)
    plot_l1_cache(df_filtered, output_dir)
    plot_l1d_pending(df_filtered, output_dir)
    plot_llc(df_filtered, output_dir)
    plot_mem_inst(df_filtered, output_dir)
    plot_overview(df_filtered, output_dir)

    print(f"\n全部完成！共生成 6 张图表，保存在: {output_dir}")


if __name__ == '__main__':
    main()
