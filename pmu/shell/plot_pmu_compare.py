#!/usr/bin/env python3
"""
PMU Timeseries 对比可视化脚本
读取两个项目的 CSV，将各类性能指标绘制在同一坐标轴上进行对比。
图片自动保存至 plots/{timestamp}_{p1}_vs_{p2}/ 目录。

用法:
    python3 plot_pmu_compare.py -p1 <project1> -p2 <project2>
    python3 plot_pmu_compare.py -p1 dtlb -p2 baseline_sleep
    python3 plot_pmu_compare.py -p1 pmu_workload -p2 baseline_sleep

不指定项目名时使用 pmu_timeseries.csv（默认文件）。
"""

import argparse
import sys
import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
import numpy as np
import os
from datetime import datetime


# ─────────────────────────── 工具函数 ───────────────────────────

def load_csv(csv_path: str, label: str) -> pd.DataFrame:
    if not os.path.exists(csv_path):
        raise FileNotFoundError(f"错误: 找不到文件 {csv_path} [{label}]")
    try:
        df = pd.read_csv(csv_path)
    except pd.errors.EmptyDataError:
        raise ValueError(f"错误: 文件为空（无标题行）{csv_path}")
    if df.empty:
        raise ValueError(f"错误: 文件无数据行（仅有标题）{csv_path}")
    df['timestamp'] = pd.to_datetime(df['timestamp'])
    # 归一化时间轴：从 0 开始的秒数，便于两个数据集对齐对比
    t0 = df['timestamp'].iloc[0]
    df['elapsed_sec'] = (df['timestamp'] - t0).dt.total_seconds()
    print(f"[{label}] 读取 {len(df)} 条记录，时间范围: "
          f"{df['timestamp'].iloc[0]} ~ {df['timestamp'].iloc[-1]}")
    return df


def preprocess_time_columns(df: pd.DataFrame, tr_fraction: float = 0.1) -> pd.DataFrame:
    df2 = df.copy()
    diffs = df2['timestamp'].diff().dt.total_seconds().dropna()
    sample_sec = float(diffs.median()) if len(diffs) > 0 else 1.0
    min_tr_ns = sample_sec * 1e9 * tr_fraction

    for col in list(df2.columns):
        if col.endswith('_time_running'):
            base = col[:-len('_time_running')]
            if base in df2.columns:
                tr_mask = df2[col].fillna(0).astype(float) < min_tr_ns
                explicit_zero = df2[base].fillna(1.0) == 0.0
                apply_mask = tr_mask & ~explicit_zero
                df2.loc[apply_mask, base] = np.nan
    return df2


def safe_col(df: pd.DataFrame, col: str) -> pd.Series:
    if col in df.columns:
        return df[col]
    return pd.Series(np.nan, index=df.index, dtype=float)


def safe_ratio(numer: pd.Series, denom: pd.Series, percent: bool = True,
               min_denom: float = 5.0, clip_max: float = 100.0) -> pd.Series:
    denom_f = denom.fillna(0).astype(float)
    numer_f = numer.fillna(0).astype(float)
    factor = 100.0 if percent else 1.0
    with np.errstate(divide='ignore', invalid='ignore'):
        ratio = np.where(denom_f >= min_denom, (numer_f / denom_f) * factor, np.nan)
    s = pd.Series(ratio, index=numer.index)
    if clip_max is not None:
        s = s.clip(lower=0, upper=clip_max)
    return s


def miss_rate(hits: pd.Series, misses: pd.Series) -> pd.Series:
    return safe_ratio(misses, hits, percent=True, min_denom=5.0, clip_max=100.0)


def save_fig(fig: plt.Figure, output_dir: str, filename: str):
    os.makedirs(output_dir, exist_ok=True)
    path = os.path.join(output_dir, filename)
    fig.savefig(path, dpi=150, bbox_inches='tight')
    print(f"  已保存: {path}")
    plt.close(fig)


def fmt_axes_elapsed(axes):
    """x 轴显示已用秒数"""
    for ax in np.array(axes).flat:
        ax.set_xlabel('Elapsed (s)')


def _plot_pair(ax, df1, df2, col, label1, label2,
               color1='steelblue', color2='tomato',
               marker1='o', marker2='s', ylabel='Count'):
    s1 = safe_col(df1, col)
    s2 = safe_col(df2, col)
    if s1.notna().any():
        ax.plot(df1['elapsed_sec'], s1, color=color1, marker=marker1,
                markersize=3, linewidth=1.5, label=label1)
    if s2.notna().any():
        ax.plot(df2['elapsed_sec'], s2, color=color2, marker=marker2,
                markersize=3, linewidth=1.5, label=label2)
    ax.set_ylabel(ylabel)
    ax.legend(fontsize=8)
    ax.grid(True, alpha=0.3)


# ─────────────────────── 1. TLB 对比 ───────────────────────────

def compare_tlb(df1, df2, label1, label2, output_dir):
    print("\n[1/5] 对比 TLB 指标...")
    fig, axes = plt.subplots(2, 3, figsize=(20, 10), sharex=False)
    fig.suptitle(f'TLB Metrics Comparison: {label1} vs {label2}',
                 fontsize=14, fontweight='bold')

    pairs = [
        (axes[0, 0], 'dTLB-loads',         'dTLB Loads'),
        (axes[0, 1], 'dTLB-load-misses',   'dTLB Load Misses'),
        (axes[0, 2], 'dTLB-stores',        'dTLB Stores'),
        (axes[1, 0], 'dTLB-store-misses',  'dTLB Store Misses'),
        (axes[1, 1], 'iTLB-loads',         'iTLB Loads'),
        (axes[1, 2], 'iTLB-load-misses',   'iTLB Load Misses'),
    ]
    for ax, col, title in pairs:
        _plot_pair(ax, df1, df2, col, label1, label2)
        ax.set_title(title, fontweight='bold', fontsize=10)

    fmt_axes_elapsed(axes)
    plt.tight_layout()
    save_fig(fig, output_dir, 'compare_tlb.png')


# ─────────────────────── 2. L1 Cache 对比 ──────────────────────

def compare_l1_cache(df1, df2, label1, label2, output_dir):
    print("\n[2/5] 对比 L1 Cache 指标...")
    fig, axes = plt.subplots(2, 3, figsize=(20, 10), sharex=False)
    fig.suptitle(f'L1 Cache Metrics Comparison: {label1} vs {label2}',
                 fontsize=14, fontweight='bold')

    pairs = [
        (axes[0, 0], 'L1-dcache-loads',         'L1D Loads'),
        (axes[0, 1], 'L1-dcache-load-misses',   'L1D Load Misses'),
        (axes[0, 2], 'L1-dcache-stores',        'L1D Stores'),
        (axes[1, 0], 'L1-icache-load-misses',   'L1I Load Misses'),
        (axes[1, 1], 'l1d.replacement',         'L1D Replacement'),
    ]
    for ax, col, title in pairs:
        _plot_pair(ax, df1, df2, col, label1, label2)
        ax.set_title(title, fontweight='bold', fontsize=10)

    # Miss rate 对比
    ax = axes[1, 2]
    mr1 = miss_rate(safe_col(df1, 'L1-dcache-loads'), safe_col(df1, 'L1-dcache-load-misses'))
    mr2 = miss_rate(safe_col(df2, 'L1-dcache-loads'), safe_col(df2, 'L1-dcache-load-misses'))
    if mr1.notna().any():
        ax.plot(df1['elapsed_sec'], mr1, color='steelblue', marker='o',
                markersize=3, linewidth=1.5, label=label1)
    if mr2.notna().any():
        ax.plot(df2['elapsed_sec'], mr2, color='tomato', marker='s',
                markersize=3, linewidth=1.5, label=label2)
    ax.set_title('L1D Load Miss Rate', fontweight='bold', fontsize=10)
    ax.set_ylabel('Miss Rate (%)'); ax.legend(fontsize=8); ax.grid(True, alpha=0.3)

    fmt_axes_elapsed(axes)
    plt.tight_layout()
    save_fig(fig, output_dir, 'compare_l1_cache.png')


# ─────────────── 3. L1d Pending Miss 对比 ──────────────────────

def compare_l1d_pending(df1, df2, label1, label2, output_dir):
    print("\n[3/5] 对比 L1D Pending Miss 指标...")
    fig, axes = plt.subplots(1, 3, figsize=(18, 5), sharex=False)
    fig.suptitle(f'L1D Pending Miss Comparison: {label1} vs {label2}',
                 fontsize=14, fontweight='bold')

    cols_titles = [
        ('l1d.replacement',        'L1D Replacement'),
        ('l1d_pend_miss.fb_full',  'L1D Pend Miss: FB Full'),
        ('l1d_pend_miss.pending',  'L1D Pend Miss: Pending'),
    ]
    for ax, (col, title) in zip(axes, cols_titles):
        _plot_pair(ax, df1, df2, col, label1, label2)
        ax.set_title(title, fontweight='bold', fontsize=10)

    fmt_axes_elapsed(axes)
    plt.tight_layout()
    save_fig(fig, output_dir, 'compare_l1d_pending.png')


# ─────────────────────── 4. LLC 对比 ───────────────────────────

def compare_llc(df1, df2, label1, label2, output_dir):
    print("\n[4/5] 对比 LLC 指标...")
    fig, axes = plt.subplots(2, 3, figsize=(20, 10), sharex=False)
    fig.suptitle(f'LLC Metrics Comparison: {label1} vs {label2}',
                 fontsize=14, fontweight='bold')

    pairs = [
        (axes[0, 0], 'LLC-loads',          'LLC Loads'),
        (axes[0, 1], 'LLC-load-misses',    'LLC Load Misses'),
        (axes[0, 2], 'LLC-stores',         'LLC Stores'),
        (axes[1, 0], 'LLC-store-misses',   'LLC Store Misses'),
        (axes[1, 1], 'cache-references',   'Cache References'),
        (axes[1, 2], 'cache-misses',       'Cache Misses'),
    ]
    for ax, col, title in pairs:
        _plot_pair(ax, df1, df2, col, label1, label2)
        ax.set_title(title, fontweight='bold', fontsize=10)

    fmt_axes_elapsed(axes)
    plt.tight_layout()
    save_fig(fig, output_dir, 'compare_llc.png')


# ─────────────────── 5. 内存指令对比 ────────────────────────────

def compare_mem_inst(df1, df2, label1, label2, output_dir):
    print("\n[5/5] 对比内存访问指令指标...")
    fig, axes = plt.subplots(2, 3, figsize=(20, 10), sharex=False)
    fig.suptitle(f'Memory Instructions Comparison: {label1} vs {label2}',
                 fontsize=14, fontweight='bold')

    pairs = [
        (axes[0, 0], 'mem_inst_retired.all_loads',  'mem_inst: All Loads'),
        (axes[0, 1], 'mem_inst_retired.all_stores', 'mem_inst: All Stores'),
        (axes[0, 2], 'mem_inst_retired.any',        'mem_inst: Any'),
        (axes[1, 0], 'mem-loads',                   'mem-loads'),
        (axes[1, 1], 'mem-stores',                  'mem-stores'),
    ]
    for ax, col, title in pairs:
        _plot_pair(ax, df1, df2, col, label1, label2)
        ax.set_title(title, fontweight='bold', fontsize=10)

    # Load/Store 比例对比
    ax = axes[1, 2]
    for df, label, color, marker in [(df1, label1, 'steelblue', 'o'),
                                      (df2, label2, 'tomato', 's')]:
        denom = safe_col(df, 'mem_inst_retired.any')
        ratio = safe_ratio(safe_col(df, 'mem_inst_retired.all_loads'), denom,
                           percent=True, min_denom=5.0)
        if ratio.notna().any():
            ax.plot(df['elapsed_sec'], ratio, color=color, marker=marker,
                    markersize=3, linewidth=1.5, label=f'{label} Load/Any')
    ax.set_title('Load / Any Ratio', fontweight='bold', fontsize=10)
    ax.set_ylabel('Ratio (%)'); ax.legend(fontsize=8); ax.grid(True, alpha=0.3)

    fmt_axes_elapsed(axes)
    plt.tight_layout()
    save_fig(fig, output_dir, 'compare_mem_inst.png')


# ──────────────── 6. 综合概览对比 ──────────────────────────────

def compare_overview(df1, df2, label1, label2, output_dir):
    print("\n[+] 对比综合概览...")
    metrics = [
        ('dTLB-loads',                  'dTLB Loads'),
        ('dTLB-load-misses',            'dTLB Load Misses'),
        ('L1-dcache-loads',             'L1D Loads'),
        ('L1-dcache-load-misses',       'L1D Load Misses'),
        ('l1d_pend_miss.pending',       'L1D Pend Pending'),
        ('LLC-loads',                   'LLC Loads'),
        ('LLC-load-misses',             'LLC Load Misses'),
        ('mem_inst_retired.all_loads',  'mem_inst Loads'),
        ('mem_inst_retired.all_stores', 'mem_inst Stores'),
    ]

    fig, axes = plt.subplots(3, 3, figsize=(20, 14), sharex=False)
    fig.suptitle(f'Overview Comparison: {label1}  vs  {label2}',
                 fontsize=14, fontweight='bold')

    colors = [('steelblue', 'tomato')]
    for ax, (col, title) in zip(axes.flat, metrics):
        _plot_pair(ax, df1, df2, col, label1, label2)
        ax.set_title(title, fontweight='bold', fontsize=10)

    fmt_axes_elapsed(axes)
    plt.tight_layout()
    save_fig(fig, output_dir, 'compare_overview.png')


# ─────────────────────────── 主入口 ─────────────────────────────

def resolve_csv(script_dir: str, project: str | None) -> str:
    if project:
        return os.path.normpath(
            os.path.join(script_dir, f'../log/pmu_timeseries_test_{project}.csv'))
    return os.path.normpath(os.path.join(script_dir, '../log/pmu_timeseries.csv'))


def main():
    parser = argparse.ArgumentParser(description='PMU Timeseries 对比可视化')
    parser.add_argument('-p1', '--project1', default=None,
                        help='第一个项目名，对应 pmu_timeseries_test_{project}.csv；'
                             '不指定则使用 pmu_timeseries.csv')
    parser.add_argument('-p2', '--project2', default=None,
                        help='第二个项目名，同上')
    args = parser.parse_args()

    if args.project1 is None and args.project2 is None:
        parser.error('至少需要指定 -p1 或 -p2 中的一个')

    p1 = args.project1 or 'default'
    p2 = args.project2 or 'default'

    script_dir = os.path.dirname(os.path.abspath(__file__))
    csv1 = resolve_csv(script_dir, args.project1)
    csv2 = resolve_csv(script_dir, args.project2)

    timestamp = datetime.now().strftime('%Y%m%d_%H%M%S')
    output_dir = os.path.normpath(
        os.path.join(script_dir, 'plots', f'{timestamp}_{p1}_vs_{p2}'))

    print(f"项目 1 [{p1}]: {csv1}")
    print(f"项目 2 [{p2}]: {csv2}")
    print(f"输出目录      : {output_dir}\n")

    errors = []
    for csv_path, label in [(csv1, p1), (csv2, p2)]:
        try:
            load_csv(csv_path, label)
        except (FileNotFoundError, ValueError) as e:
            errors.append(str(e))
    if errors:
        for e in errors:
            print(e)
        sys.exit(1)

    df1 = preprocess_time_columns(load_csv(csv1, p1))
    df2 = preprocess_time_columns(load_csv(csv2, p2))

    compare_tlb(df1, df2, p1, p2, output_dir)
    compare_l1_cache(df1, df2, p1, p2, output_dir)
    compare_l1d_pending(df1, df2, p1, p2, output_dir)
    compare_llc(df1, df2, p1, p2, output_dir)
    compare_mem_inst(df1, df2, p1, p2, output_dir)
    compare_overview(df1, df2, p1, p2, output_dir)

    print(f"\n全部完成！共生成 6 张对比图，保存在: {output_dir}")


if __name__ == '__main__':
    main()
