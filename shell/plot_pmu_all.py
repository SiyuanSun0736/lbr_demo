#!/usr/bin/env python3
"""
PMU Comprehensive Monitor Log Visualization Script
读取 pmu_monitor_all.log 文件并生成全面的性能指标图表
"""

import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
from datetime import datetime
import sys
import os
import numpy as np

def plot_tlb_metrics(df, output_dir='plots'):
    """
    绘制 TLB 相关指标
    """
    os.makedirs(output_dir, exist_ok=True)
    
    fig, axes = plt.subplots(2, 2, figsize=(16, 10))
    fig.suptitle('TLB Performance Metrics', fontsize=16, fontweight='bold')
    
    # dTLB loads and misses
    ax = axes[0, 0]
    ax.plot(df['Timestamp'], df['dTLB-loads'], 'b-o', label='dTLB-loads', markersize=3, linewidth=1.5)
    ax.plot(df['Timestamp'], df['dTLB-load-misses'], 'r-s', label='dTLB-load-misses', markersize=3, linewidth=1.5)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_title('dTLB Loads', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # dTLB stores and misses
    ax = axes[0, 1]
    ax.plot(df['Timestamp'], df['dTLB-stores'], 'g-o', label='dTLB-stores', markersize=3, linewidth=1.5)
    ax.plot(df['Timestamp'], df['dTLB-store-misses'], 'm-s', label='dTLB-store-misses', markersize=3, linewidth=1.5)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_title('dTLB Stores', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # iTLB loads and misses
    ax = axes[1, 0]
    ax.plot(df['Timestamp'], df['iTLB-loads'], 'c-o', label='iTLB-loads', markersize=3, linewidth=1.5)
    ax.plot(df['Timestamp'], df['iTLB-load-misses'], 'orange', marker='s', label='iTLB-load-misses', markersize=3, linewidth=1.5)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_xlabel('Time', fontweight='bold')
    ax.set_title('iTLB Loads', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # TLB miss rates
    ax = axes[1, 1]
    dtlb_load_miss_rate = (df['dTLB-load-misses'] / df['dTLB-loads'].replace(0, np.nan)) * 100
    dtlb_store_miss_rate = (df['dTLB-store-misses'] / df['dTLB-stores'].replace(0, np.nan)) * 100
    itlb_miss_rate = (df['iTLB-load-misses'] / df['iTLB-loads'].replace(0, np.nan)) * 100
    
    ax.plot(df['Timestamp'], dtlb_load_miss_rate, 'r-o', label='dTLB Load Miss Rate', markersize=3, linewidth=1.5)
    ax.plot(df['Timestamp'], dtlb_store_miss_rate, 'm-s', label='dTLB Store Miss Rate', markersize=3, linewidth=1.5)
    ax.plot(df['Timestamp'], itlb_miss_rate, 'orange', marker='^', label='iTLB Miss Rate', markersize=3, linewidth=1.5)
    ax.set_ylabel('Miss Rate (%)', fontweight='bold')
    ax.set_xlabel('Time', fontweight='bold')
    ax.set_title('TLB Miss Rates', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    for ax in axes.flat:
        ax.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
        plt.setp(ax.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    plt.tight_layout()
    output_file = os.path.join(output_dir, 'tlb_metrics.png')
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"TLB 指标图表已保存到: {output_file}")
    plt.close()

def plot_l1_cache_metrics(df, output_dir='plots'):
    """
    绘制 L1 Cache 相关指标
    """
    os.makedirs(output_dir, exist_ok=True)
    
    fig, axes = plt.subplots(2, 2, figsize=(16, 10))
    fig.suptitle('L1 Cache Performance Metrics', fontsize=16, fontweight='bold')
    
    # L1 D-cache loads
    ax = axes[0, 0]
    ax.plot(df['Timestamp'], df['L1-dcache-loads'], 'b-o', label='L1-dcache-loads', markersize=3, linewidth=1.5)
    ax.plot(df['Timestamp'], df['L1-dcache-load-misses'], 'r-s', label='L1-dcache-load-misses', markersize=3, linewidth=1.5)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_title('L1 Data Cache Loads', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # L1 D-cache stores
    ax = axes[0, 1]
    ax.plot(df['Timestamp'], df['L1-dcache-stores'], 'g-o', label='L1-dcache-stores', markersize=3, linewidth=1.5)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_title('L1 Data Cache Stores', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # L1 I-cache
    ax = axes[1, 0]
    ax.plot(df['Timestamp'], df['L1-icache-load-misses'], 'orange', marker='s', label='L1-icache-load-misses', markersize=3, linewidth=1.5)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_xlabel('Time', fontweight='bold')
    ax.set_title('L1 Instruction Cache Load Misses', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # L1 D-cache miss rate
    ax = axes[1, 1]
    l1d_miss_rate = (df['L1-dcache-load-misses'] / df['L1-dcache-loads'].replace(0, np.nan)) * 100
    ax.plot(df['Timestamp'], l1d_miss_rate, 'r-o', label='L1-dcache Miss Rate', markersize=3, linewidth=1.5)
    ax.set_ylabel('Miss Rate (%)', fontweight='bold')
    ax.set_xlabel('Time', fontweight='bold')
    ax.set_title('L1 Data Cache Miss Rate', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    for ax in axes.flat:
        ax.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
        plt.setp(ax.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    plt.tight_layout()
    output_file = os.path.join(output_dir, 'l1_cache_metrics.png')
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"L1 Cache 指标图表已保存到: {output_file}")
    plt.close()

def plot_l1d_pending_miss(df, output_dir='plots'):
    """
    绘制 L1D Pending Miss 指标
    """
    os.makedirs(output_dir, exist_ok=True)
    
    fig, axes = plt.subplots(1, 3, figsize=(18, 5))
    fig.suptitle('L1D Pending Miss Events', fontsize=16, fontweight='bold')
    
    # l1d.replacement
    ax = axes[0]
    ax.plot(df['Timestamp'], df['l1d.replacement'], 'b-o', markersize=3, linewidth=1.5)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_xlabel('Time', fontweight='bold')
    ax.set_title('L1D Replacement', fontweight='bold')
    ax.grid(True, alpha=0.3)
    
    # l1d_pend_miss.fb_full
    ax = axes[1]
    ax.plot(df['Timestamp'], df['l1d_pend_miss.fb_full'], 'r-s', markersize=3, linewidth=1.5)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_xlabel('Time', fontweight='bold')
    ax.set_title('L1D Pend Miss FB Full', fontweight='bold')
    ax.grid(True, alpha=0.3)
    
    # l1d_pend_miss.pending
    ax = axes[2]
    ax.plot(df['Timestamp'], df['l1d_pend_miss.pending'], 'g-^', markersize=3, linewidth=1.5)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_xlabel('Time', fontweight='bold')
    ax.set_title('L1D Pend Miss Pending', fontweight='bold')
    ax.grid(True, alpha=0.3)
    
    for ax in axes.flat:
        ax.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
        plt.setp(ax.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    plt.tight_layout()
    output_file = os.path.join(output_dir, 'l1d_pending_miss.png')
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"L1D Pending Miss 指标图表已保存到: {output_file}")
    plt.close()

def plot_llc_metrics(df, output_dir='plots'):
    """
    绘制 LLC (Last Level Cache) 相关指标
    """
    os.makedirs(output_dir, exist_ok=True)
    
    fig, axes = plt.subplots(2, 2, figsize=(16, 10))
    fig.suptitle('Last Level Cache (LLC) Performance Metrics', fontsize=16, fontweight='bold')
    
    # LLC loads and misses
    ax = axes[0, 0]
    ax.plot(df['Timestamp'], df['LLC-loads'], 'b-o', label='LLC-loads', markersize=3, linewidth=1.5)
    ax.plot(df['Timestamp'], df['LLC-load-misses'], 'r-s', label='LLC-load-misses', markersize=3, linewidth=1.5)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_title('LLC Loads', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # LLC stores and misses
    ax = axes[0, 1]
    ax.plot(df['Timestamp'], df['LLC-stores'], 'g-o', label='LLC-stores', markersize=3, linewidth=1.5)
    ax.plot(df['Timestamp'], df['LLC-store-misses'], 'm-s', label='LLC-store-misses', markersize=3, linewidth=1.5)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_title('LLC Stores', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # Generic cache events
    ax = axes[1, 0]
    ax.plot(df['Timestamp'], df['cache-references'], 'c-o', label='cache-references', markersize=3, linewidth=1.5)
    ax.plot(df['Timestamp'], df['cache-misses'], 'orange', marker='s', label='cache-misses', markersize=3, linewidth=1.5)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_xlabel('Time', fontweight='bold')
    ax.set_title('Generic Cache Events', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # LLC miss rates
    ax = axes[1, 1]
    llc_load_miss_rate = (df['LLC-load-misses'] / df['LLC-loads'].replace(0, np.nan)) * 100
    llc_store_miss_rate = (df['LLC-store-misses'] / df['LLC-stores'].replace(0, np.nan)) * 100
    cache_miss_rate = (df['cache-misses'] / df['cache-references'].replace(0, np.nan)) * 100
    
    ax.plot(df['Timestamp'], llc_load_miss_rate, 'r-o', label='LLC Load Miss Rate', markersize=3, linewidth=1.5)
    ax.plot(df['Timestamp'], llc_store_miss_rate, 'm-s', label='LLC Store Miss Rate', markersize=3, linewidth=1.5)
    ax.plot(df['Timestamp'], cache_miss_rate, 'orange', marker='^', label='Cache Miss Rate', markersize=3, linewidth=1.5)
    ax.set_ylabel('Miss Rate (%)', fontweight='bold')
    ax.set_xlabel('Time', fontweight='bold')
    ax.set_title('LLC Miss Rates', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    for ax in axes.flat:
        ax.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
        plt.setp(ax.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    plt.tight_layout()
    output_file = os.path.join(output_dir, 'llc_metrics.png')
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"LLC 指标图表已保存到: {output_file}")
    plt.close()

def plot_memory_instructions(df, output_dir='plots'):
    """
    绘制内存指令相关指标
    """
    os.makedirs(output_dir, exist_ok=True)
    
    fig, axes = plt.subplots(2, 2, figsize=(16, 10))
    fig.suptitle('Memory Instructions Retired Metrics', fontsize=16, fontweight='bold')
    
    # Generic mem-loads and mem-stores
    ax = axes[0, 0]
    ax.plot(df['Timestamp'], df['mem-loads'], 'b-o', label='mem-loads', markersize=3, linewidth=1.5)
    ax.plot(df['Timestamp'], df['mem-stores'], 'r-s', label='mem-stores', markersize=3, linewidth=1.5)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_title('Generic Memory Operations', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # Retired loads
    ax = axes[0, 1]
    ax.plot(df['Timestamp'], df['mem_inst_retired.all_loads'], 'c-o', label='all_loads', markersize=3, linewidth=1.5)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_title('Retired Load Instructions', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # Retired stores
    ax = axes[1, 0]
    ax.plot(df['Timestamp'], df['mem_inst_retired.all_stores'], 'g-s', label='all_stores', markersize=3, linewidth=1.5)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_xlabel('Time', fontweight='bold')
    ax.set_title('Retired Store Instructions', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # All retired memory instructions
    ax = axes[1, 1]
    ax.plot(df['Timestamp'], df['mem_inst_retired.any'], 'm-^', label='any', markersize=3, linewidth=1.5)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_xlabel('Time', fontweight='bold')
    ax.set_title('All Retired Memory Instructions', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    for ax in axes.flat:
        ax.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
        plt.setp(ax.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    plt.tight_layout()
    output_file = os.path.join(output_dir, 'memory_instructions.png')
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"内存指令指标图表已保存到: {output_file}")
    plt.close()

def plot_overview(df, output_dir='plots'):
    """
    绘制总览图表
    """
    os.makedirs(output_dir, exist_ok=True)
    
    fig, axes = plt.subplots(3, 2, figsize=(16, 12))
    fig.suptitle('PMU Comprehensive Overview', fontsize=16, fontweight='bold')
    
    # TLB Overview
    ax = axes[0, 0]
    ax.plot(df['Timestamp'], df['dTLB-load-misses'], 'r-o', label='dTLB-load-misses', markersize=2, linewidth=1)
    ax.plot(df['Timestamp'], df['iTLB-load-misses'], 'b-s', label='iTLB-load-misses', markersize=2, linewidth=1)
    ax.set_ylabel('Misses', fontweight='bold')
    ax.set_title('TLB Misses Overview', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # L1 Cache Overview
    ax = axes[0, 1]
    ax.plot(df['Timestamp'], df['L1-dcache-load-misses'], 'r-o', label='L1-dcache-load-misses', markersize=2, linewidth=1)
    ax.plot(df['Timestamp'], df['L1-icache-load-misses'], 'b-s', label='L1-icache-load-misses', markersize=2, linewidth=1)
    ax.set_ylabel('Misses', fontweight='bold')
    ax.set_title('L1 Cache Misses Overview', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # LLC Overview
    ax = axes[1, 0]
    ax.plot(df['Timestamp'], df['LLC-load-misses'], 'r-o', label='LLC-load-misses', markersize=2, linewidth=1)
    ax.plot(df['Timestamp'], df['LLC-store-misses'], 'g-s', label='LLC-store-misses', markersize=2, linewidth=1)
    ax.set_ylabel('Misses', fontweight='bold')
    ax.set_title('LLC Misses Overview', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # Cache References and Misses
    ax = axes[1, 1]
    ax.plot(df['Timestamp'], df['cache-references'], 'c-o', label='cache-references', markersize=2, linewidth=1)
    ax.plot(df['Timestamp'], df['cache-misses'], 'orange', marker='s', label='cache-misses', markersize=2, linewidth=1)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_title('Overall Cache Activity', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # Memory Instructions Overview
    ax = axes[2, 0]
    ax.plot(df['Timestamp'], df['mem_inst_retired.all_loads'], 'b-o', label='loads', markersize=2, linewidth=1)
    ax.plot(df['Timestamp'], df['mem_inst_retired.all_stores'], 'r-s', label='stores', markersize=2, linewidth=1)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_xlabel('Time', fontweight='bold')
    ax.set_title('Retired Memory Instructions', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    # L1D Pending Miss Overview
    ax = axes[2, 1]
    ax.plot(df['Timestamp'], df['l1d.replacement'], 'g-o', label='l1d.replacement', markersize=2, linewidth=1)
    ax.plot(df['Timestamp'], df['l1d_pend_miss.pending'], 'm-s', label='pending', markersize=2, linewidth=1)
    ax.set_ylabel('Count', fontweight='bold')
    ax.set_xlabel('Time', fontweight='bold')
    ax.set_title('L1D Events', fontweight='bold')
    ax.legend()
    ax.grid(True, alpha=0.3)
    
    for ax in axes.flat:
        ax.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
        plt.setp(ax.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    plt.tight_layout()
    output_file = os.path.join(output_dir, 'overview.png')
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"总览图表已保存到: {output_file}")
    plt.close()

def print_statistics(df):
    """
    打印统计信息
    """
    print("\n" + "=" * 80)
    print("性能指标统计信息")
    print("=" * 80)
    
    # TLB Statistics
    print("\nTLB 统计:")
    print(f"  dTLB-loads: 平均={df['dTLB-loads'].mean():.0f}, 最大={df['dTLB-loads'].max()}, 最小={df['dTLB-loads'].min()}")
    print(f"  dTLB-load-misses: 平均={df['dTLB-load-misses'].mean():.0f}, 最大={df['dTLB-load-misses'].max()}, 最小={df['dTLB-load-misses'].min()}")
    if df['dTLB-loads'].sum() > 0:
        miss_rate = (df['dTLB-load-misses'].sum() / df['dTLB-loads'].sum()) * 100
        print(f"  dTLB Load Miss Rate: {miss_rate:.4f}%")
    
    print(f"\n  iTLB-loads: 平均={df['iTLB-loads'].mean():.0f}, 最大={df['iTLB-loads'].max()}, 最小={df['iTLB-loads'].min()}")
    print(f"  iTLB-load-misses: 平均={df['iTLB-load-misses'].mean():.0f}, 最大={df['iTLB-load-misses'].max()}, 最小={df['iTLB-load-misses'].min()}")
    if df['iTLB-loads'].sum() > 0:
        miss_rate = (df['iTLB-load-misses'].sum() / df['iTLB-loads'].sum()) * 100
        print(f"  iTLB Load Miss Rate: {miss_rate:.4f}%")
    
    # L1 Cache Statistics
    print("\nL1 Cache 统计:")
    print(f"  L1-dcache-loads: 平均={df['L1-dcache-loads'].mean():.0f}, 最大={df['L1-dcache-loads'].max()}, 最小={df['L1-dcache-loads'].min()}")
    print(f"  L1-dcache-load-misses: 平均={df['L1-dcache-load-misses'].mean():.0f}, 最大={df['L1-dcache-load-misses'].max()}, 最小={df['L1-dcache-load-misses'].min()}")
    if df['L1-dcache-loads'].sum() > 0:
        miss_rate = (df['L1-dcache-load-misses'].sum() / df['L1-dcache-loads'].sum()) * 100
        print(f"  L1-dcache Miss Rate: {miss_rate:.4f}%")
    
    print(f"\n  L1-icache-load-misses: 平均={df['L1-icache-load-misses'].mean():.0f}, 最大={df['L1-icache-load-misses'].max()}, 最小={df['L1-icache-load-misses'].min()}")
    
    # LLC Statistics
    print("\nLLC 统计:")
    print(f"  LLC-loads: 平均={df['LLC-loads'].mean():.0f}, 最大={df['LLC-loads'].max()}, 最小={df['LLC-loads'].min()}")
    print(f"  LLC-load-misses: 平均={df['LLC-load-misses'].mean():.0f}, 最大={df['LLC-load-misses'].max()}, 最小={df['LLC-load-misses'].min()}")
    if df['LLC-loads'].sum() > 0:
        miss_rate = (df['LLC-load-misses'].sum() / df['LLC-loads'].sum()) * 100
        print(f"  LLC Load Miss Rate: {miss_rate:.4f}%")
    
    print(f"\n  cache-references: 平均={df['cache-references'].mean():.0f}, 最大={df['cache-references'].max()}, 最小={df['cache-references'].min()}")
    print(f"  cache-misses: 平均={df['cache-misses'].mean():.0f}, 最大={df['cache-misses'].max()}, 最小={df['cache-misses'].min()}")
    if df['cache-references'].sum() > 0:
        miss_rate = (df['cache-misses'].sum() / df['cache-references'].sum()) * 100
        print(f"  Overall Cache Miss Rate: {miss_rate:.4f}%")
    
    # Memory Instructions Statistics
    print("\n内存指令统计:")
    print(f"  mem_inst_retired.all_loads: 平均={df['mem_inst_retired.all_loads'].mean():.0f}, 最大={df['mem_inst_retired.all_loads'].max()}, 最小={df['mem_inst_retired.all_loads'].min()}")
    print(f"  mem_inst_retired.all_stores: 平均={df['mem_inst_retired.all_stores'].mean():.0f}, 最大={df['mem_inst_retired.all_stores'].max()}, 最小={df['mem_inst_retired.all_stores'].min()}")
    print(f"  mem_inst_retired.any: 平均={df['mem_inst_retired.any'].mean():.0f}, 最大={df['mem_inst_retired.any'].max()}, 最小={df['mem_inst_retired.any'].min()}")
    
    print("\n" + "=" * 80)

def main():
    # 默认日志文件路径
    default_log_path = '../pmu/log/pmu_monitor_all.log'
    
    # 从命令行参数获取日志文件路径
    if len(sys.argv) > 1:
        log_path = sys.argv[1]
    else:
        log_path = default_log_path
    
    # 输出目录
    if len(sys.argv) > 2:
        output_dir = sys.argv[2]
    else:
        output_dir = 'plots'
    
    print("=" * 80)
    print("PMU 综合监测数据可视化")
    print("=" * 80)
    print(f"日志文件: {log_path}")
    print(f"输出目录: {output_dir}\n")
    
    # 检查文件是否存在
    if not os.path.exists(log_path):
        print(f"错误: 找不到日志文件 {log_path}")
        return
    
    # 读取 CSV 文件
    try:
        df = pd.read_csv(log_path)
        print(f"成功读取 {len(df)} 条记录\n")
    except Exception as e:
        print(f"读取文件失败: {e}")
        return
    
    # 检查数据是否为空
    if df.empty:
        print("警告: 日志文件为空")
        return
    
    # 解析时间戳
    df['Timestamp'] = pd.to_datetime(df['Timestamp'])
    
    # 替换 "N/A" 为 NaN 并转换为数值类型
    for col in df.columns:
        if col != 'Timestamp':
            df[col] = pd.to_numeric(df[col], errors='coerce').fillna(0)
    
    # 打印统计信息
    print_statistics(df)
    
    # 生成各类图表
    print("\n生成图表...")
    print("-" * 80)
    
    plot_overview(df, output_dir)
    plot_tlb_metrics(df, output_dir)
    plot_l1_cache_metrics(df, output_dir)
    plot_l1d_pending_miss(df, output_dir)
    plot_llc_metrics(df, output_dir)
    plot_memory_instructions(df, output_dir)
    
    print("-" * 80)
    print(f"\n所有图表已保存到目录: {output_dir}/")
    print("\n可视化完成！")
    print("=" * 80)

if __name__ == '__main__':
    main()
