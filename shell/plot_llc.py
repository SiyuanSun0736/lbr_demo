#!/usr/bin/env python3
"""
LLC (Last Level Cache) Monitor Log Visualization Script
读取 llc_cache_monitor.log 文件并生成时间轴图表
"""

import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
from datetime import datetime
import sys
import os

def plot_llc_data(log_file_path, output_file='llc_monitor.png'):
    """
    读取 LLC 监控日志并生成时间轴图表（分离子图）
    
    Args:
        log_file_path: 日志文件路径
        output_file: 输出图片文件名
    """
    # 检查文件是否存在
    if not os.path.exists(log_file_path):
        print(f"错误: 找不到日志文件 {log_file_path}")
        return
    
    # 读取 CSV 文件
    try:
        df = pd.read_csv(log_file_path)
        print(f"成功读取 {len(df)} 条记录")
    except Exception as e:
        print(f"读取文件失败: {e}")
        return
    
    # 检查数据是否为空
    if df.empty:
        print("警告: 日志文件为空")
        return
    
    # 解析时间戳
    df['Timestamp'] = pd.to_datetime(df['Timestamp'])
    
    # 创建图表 (3 个子图)
    fig, (ax1, ax2, ax3) = plt.subplots(3, 1, figsize=(14, 10), sharex=True)
    
    # 第一个子图: LLC Loads 和 Misses
    ax1.plot(df['Timestamp'], df['LLC-loads'], 
             color='blue', linewidth=2, marker='o', markersize=4, label='LLC-loads')
    ax1.plot(df['Timestamp'], df['LLC-load-misses'], 
             color='red', linewidth=2, marker='s', markersize=4, label='LLC-load-misses')
    ax1.set_ylabel('Count', fontsize=12, fontweight='bold')
    ax1.set_title('LLC (Last Level Cache) Monitoring - Loads and Stores', fontsize=14, fontweight='bold')
    ax1.grid(True, alpha=0.3)
    ax1.legend(loc='upper left')
    
    # 第二个子图: LLC Stores 和 Store Misses
    ax2.plot(df['Timestamp'], df['LLC-stores'], 
             color='green', linewidth=2, marker='^', markersize=4, label='LLC-stores')
    ax2.plot(df['Timestamp'], df['LLC-store-misses'], 
             color='orange', linewidth=2, marker='v', markersize=4, label='LLC-store-misses')
    ax2.set_ylabel('Count', fontsize=12, fontweight='bold')
    ax2.grid(True, alpha=0.3)
    ax2.legend(loc='upper left')
    
    # 第三个子图: Generic Cache Events
    ax3.plot(df['Timestamp'], df['cache-references'], 
             color='purple', linewidth=2, marker='d', markersize=4, label='cache-references')
    ax3.plot(df['Timestamp'], df['cache-misses'], 
             color='brown', linewidth=2, marker='p', markersize=4, label='cache-misses')
    ax3.set_ylabel('Count', fontsize=12, fontweight='bold')
    ax3.set_xlabel('Time', fontsize=12, fontweight='bold')
    ax3.grid(True, alpha=0.3)
    ax3.legend(loc='upper left')
    
    # 格式化 x 轴时间显示
    ax3.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
    plt.setp(ax3.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    # 计算并显示统计信息
    print("\n统计信息:")
    print(f"LLC-loads - 平均: {df['LLC-loads'].mean():.2f}, "
          f"最大: {df['LLC-loads'].max()}, "
          f"最小: {df['LLC-loads'].min()}")
    print(f"LLC-load-misses - 平均: {df['LLC-load-misses'].mean():.2f}, "
          f"最大: {df['LLC-load-misses'].max()}, "
          f"最小: {df['LLC-load-misses'].min()}")
    print(f"LLC-stores - 平均: {df['LLC-stores'].mean():.2f}, "
          f"最大: {df['LLC-stores'].max()}, "
          f"最小: {df['LLC-stores'].min()}")
    print(f"LLC-store-misses - 平均: {df['LLC-store-misses'].mean():.2f}, "
          f"最大: {df['LLC-store-misses'].max()}, "
          f"最小: {df['LLC-store-misses'].min()}")
    print(f"cache-references - 平均: {df['cache-references'].mean():.2f}, "
          f"最大: {df['cache-references'].max()}, "
          f"最小: {df['cache-references'].min()}")
    print(f"cache-misses - 平均: {df['cache-misses'].mean():.2f}, "
          f"最大: {df['cache-misses'].max()}, "
          f"最小: {df['cache-misses'].min()}")
    
    # 计算 miss rates
    if df['LLC-loads'].sum() > 0:
        load_miss_rate = (df['LLC-load-misses'].sum() / df['LLC-loads'].sum()) * 100
        print(f"LLC Load Miss Rate: {load_miss_rate:.2f}%")
    
    if df['LLC-stores'].sum() > 0:
        store_miss_rate = (df['LLC-store-misses'].sum() / df['LLC-stores'].sum()) * 100
        print(f"LLC Store Miss Rate: {store_miss_rate:.2f}%")
    
    if df['cache-references'].sum() > 0:
        cache_miss_rate = (df['cache-misses'].sum() / df['cache-references'].sum()) * 100
        print(f"Overall Cache Miss Rate: {cache_miss_rate:.2f}%")
    
    # 调整布局
    plt.tight_layout()
    
    # 保存图片
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"\n图表已保存到: {output_file}")
    
    # 显示图表
    plt.show()

def plot_llc_combined(log_file_path, output_file='llc_monitor_combined.png'):
    """
    读取 LLC 监控日志并生成组合时间轴图表（多 Y 轴）
    
    Args:
        log_file_path: 日志文件路径
        output_file: 输出图片文件名
    """
    # 检查文件是否存在
    if not os.path.exists(log_file_path):
        print(f"错误: 找不到日志文件 {log_file_path}")
        return
    
    # 读取 CSV 文件
    try:
        df = pd.read_csv(log_file_path)
        print(f"成功读取 {len(df)} 条记录")
    except Exception as e:
        print(f"读取文件失败: {e}")
        return
    
    # 检查数据是否为空
    if df.empty:
        print("警告: 日志文件为空")
        return
    
    # 解析时间戳
    df['Timestamp'] = pd.to_datetime(df['Timestamp'])
    
    # 创建图表
    fig, ax1 = plt.subplots(figsize=(16, 10))
    
    # 绘制 LLC-loads（左 Y 轴）
    color1 = 'tab:blue'
    ax1.set_xlabel('Time', fontsize=12, fontweight='bold')
    ax1.set_ylabel('LLC Operations (Loads & Stores)', fontsize=12, fontweight='bold', color=color1)
    line1 = ax1.plot(df['Timestamp'], df['LLC-loads'], 
                     color='blue', linewidth=2, marker='o', markersize=4, 
                     label='LLC-loads', alpha=0.7)
    line2 = ax1.plot(df['Timestamp'], df['LLC-stores'], 
                     color='green', linewidth=2, marker='^', markersize=4, 
                     label='LLC-stores', alpha=0.7)
    ax1.tick_params(axis='y', labelcolor=color1)
    ax1.grid(True, alpha=0.3)
    
    # 创建第二个 Y 轴绘制 LLC Misses
    ax2 = ax1.twinx()
    color2 = 'tab:red'
    ax2.set_ylabel('LLC Misses', fontsize=12, fontweight='bold', color=color2)
    line3 = ax2.plot(df['Timestamp'], df['LLC-load-misses'], 
                     color='red', linewidth=2, marker='s', markersize=4, 
                     label='LLC-load-misses', alpha=0.7)
    line4 = ax2.plot(df['Timestamp'], df['LLC-store-misses'], 
                     color='orange', linewidth=2, marker='v', markersize=4, 
                     label='LLC-store-misses', alpha=0.7)
    ax2.tick_params(axis='y', labelcolor=color2)
    
    # 创建第三个 Y 轴绘制 Generic Cache Events
    ax3 = ax1.twinx()
    ax3.spines['right'].set_position(('outward', 60))
    color3 = 'tab:purple'
    ax3.set_ylabel('Generic Cache Events', fontsize=12, fontweight='bold', color=color3)
    line5 = ax3.plot(df['Timestamp'], df['cache-references'], 
                     color='purple', linewidth=2, marker='d', markersize=4, 
                     label='cache-references', alpha=0.7)
    line6 = ax3.plot(df['Timestamp'], df['cache-misses'], 
                     color='brown', linewidth=2, marker='p', markersize=4, 
                     label='cache-misses', alpha=0.7)
    ax3.tick_params(axis='y', labelcolor=color3)
    
    # 设置标题
    ax1.set_title('LLC (Last Level Cache) Monitoring - Combined View', fontsize=14, fontweight='bold')
    
    # 合并图例
    lines = line1 + line2 + line3 + line4 + line5 + line6
    labels = [l.get_label() for l in lines]
    ax1.legend(lines, labels, loc='upper left', fontsize=10)
    
    # 格式化 x 轴时间显示
    ax1.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
    plt.setp(ax1.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    # 计算并显示统计信息
    print("\n统计信息:")
    print(f"LLC-loads - 平均: {df['LLC-loads'].mean():.2f}, "
          f"最大: {df['LLC-loads'].max()}, "
          f"最小: {df['LLC-loads'].min()}")
    print(f"LLC-load-misses - 平均: {df['LLC-load-misses'].mean():.2f}, "
          f"最大: {df['LLC-load-misses'].max()}, "
          f"最小: {df['LLC-load-misses'].min()}")
    
    # 计算 miss rates
    if df['LLC-loads'].sum() > 0:
        load_miss_rate = (df['LLC-load-misses'].sum() / df['LLC-loads'].sum()) * 100
        print(f"LLC Load Miss Rate: {load_miss_rate:.2f}%")
    
    if df['cache-references'].sum() > 0:
        cache_miss_rate = (df['cache-misses'].sum() / df['cache-references'].sum()) * 100
        print(f"Overall Cache Miss Rate: {cache_miss_rate:.2f}%")
    
    # 调整布局
    plt.tight_layout()
    
    # 保存图片
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"\n组合图表已保存到: {output_file}")
    
    # 显示图表
    plt.show()

def main():
    # 默认日志文件路径
    default_log_path = '../pmu/log/llc_cache_monitor.log'
    
    # 从命令行参数获取日志文件路径
    if len(sys.argv) > 1:
        log_path = sys.argv[1]
    else:
        log_path = default_log_path
    
    # 获取输出文件名
    if len(sys.argv) > 2:
        output_file = sys.argv[2]
    else:
        output_file = 'llc_monitor.png'
    
    print(f"LLC Monitor 数据可视化")
    print(f"日志文件: {log_path}")
    print(f"输出文件: {output_file}\n")
    
    # 绘制分离图表
    print("=" * 50)
    print("生成分离子图...")
    print("=" * 50)
    plot_llc_data(log_path, output_file)
    
    # 绘制组合图表
    print("\n" + "=" * 50)
    print("生成组合图表...")
    print("=" * 50)
    combined_output = output_file.replace('.png', '_combined.png')
    plot_llc_combined(log_path, combined_output)

if __name__ == '__main__':
    main()
