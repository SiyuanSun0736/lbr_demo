#!/usr/bin/env python3
"""
iTLB Monitor Log Visualization Script
读取 itlb_monitor.log 文件并生成时间轴图表
"""

import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
from datetime import datetime
import sys
import os

def plot_itlb_data(log_file_path, output_file='itlb_monitor.png'):
    """
    读取 iTLB 监控日志并生成时间轴图表（分离子图）
    
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
    fig, (ax1, ax2) = plt.subplots(2, 1, figsize=(12, 8), sharex=True)
    
    # 绘制 iTLB-loads
    ax1.plot(df['Timestamp'], df['iTLB-loads'], 
             color='blue', linewidth=2, marker='o', markersize=4, label='iTLB-loads')
    ax1.set_ylabel('iTLB Loads', fontsize=12, fontweight='bold')
    ax1.set_title('iTLB Monitoring - Loads and Misses Over Time', fontsize=14, fontweight='bold')
    ax1.grid(True, alpha=0.3)
    ax1.legend(loc='upper left')
    
    # 绘制 iTLB-load-misses
    ax2.plot(df['Timestamp'], df['iTLB-load-misses'], 
             color='red', linewidth=2, marker='s', markersize=4, label='iTLB-load-misses')
    ax2.set_ylabel('iTLB Load Misses', fontsize=12, fontweight='bold')
    ax2.set_xlabel('Time', fontsize=12, fontweight='bold')
    ax2.grid(True, alpha=0.3)
    ax2.legend(loc='upper left')
    
    # 格式化 x 轴时间显示
    ax2.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
    plt.setp(ax2.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    # 计算并显示统计信息
    print("\n统计信息:")
    print(f"iTLB-loads - 平均: {df['iTLB-loads'].mean():.2f}, "
          f"最大: {df['iTLB-loads'].max()}, "
          f"最小: {df['iTLB-loads'].min()}")
    print(f"iTLB-load-misses - 平均: {df['iTLB-load-misses'].mean():.2f}, "
          f"最大: {df['iTLB-load-misses'].max()}, "
          f"最小: {df['iTLB-load-misses'].min()}")
    
    # 计算 miss rate
    if df['iTLB-loads'].sum() > 0:
        miss_rate = (df['iTLB-load-misses'].sum() / df['iTLB-loads'].sum()) * 100
        print(f"总体 Miss Rate: {miss_rate:.2f}%")
    
    # 调整布局
    plt.tight_layout()
    
    # 保存图片
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"\n图表已保存到: {output_file}")
    
    # 显示图表
    plt.show()

def plot_itlb_combined(log_file_path, output_file='itlb_monitor_combined.png'):
    """
    读取 iTLB 监控日志并生成组合时间轴图表（所有曲线在一起）
    
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
    fig, ax1 = plt.subplots(figsize=(14, 8))
    
    # 绘制 iTLB-loads（左 Y 轴）
    color1 = 'tab:blue'
    ax1.set_xlabel('Time', fontsize=12, fontweight='bold')
    ax1.set_ylabel('iTLB Loads', fontsize=12, fontweight='bold', color=color1)
    line1 = ax1.plot(df['Timestamp'], df['iTLB-loads'], 
                     color=color1, linewidth=2, marker='o', markersize=4, 
                     label='iTLB-loads', alpha=0.7)
    ax1.tick_params(axis='y', labelcolor=color1)
    ax1.grid(True, alpha=0.3)
    
    # 创建第二个 Y 轴绘制 iTLB-load-misses
    ax2 = ax1.twinx()
    color2 = 'tab:red'
    ax2.set_ylabel('iTLB Load Misses', fontsize=12, fontweight='bold', color=color2)
    line2 = ax2.plot(df['Timestamp'], df['iTLB-load-misses'], 
                     color=color2, linewidth=2, marker='s', markersize=4, 
                     label='iTLB-load-misses', alpha=0.7)
    ax2.tick_params(axis='y', labelcolor=color2)
    
    # 设置标题
    ax1.set_title('iTLB Monitoring - Combined View', fontsize=14, fontweight='bold')
    
    # 合并图例
    lines = line1 + line2
    labels = [l.get_label() for l in lines]
    ax1.legend(lines, labels, loc='upper left', fontsize=10)
    
    # 格式化 x 轴时间显示
    ax1.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
    plt.setp(ax1.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    # 计算并显示统计信息
    print("\n统计信息:")
    print(f"iTLB-loads - 平均: {df['iTLB-loads'].mean():.2f}, "
          f"最大: {df['iTLB-loads'].max()}, "
          f"最小: {df['iTLB-loads'].min()}")
    print(f"iTLB-load-misses - 平均: {df['iTLB-load-misses'].mean():.2f}, "
          f"最大: {df['iTLB-load-misses'].max()}, "
          f"最小: {df['iTLB-load-misses'].min()}")
    
    # 计算 miss rate
    if df['iTLB-loads'].sum() > 0:
        miss_rate = (df['iTLB-load-misses'].sum() / df['iTLB-loads'].sum()) * 100
        print(f"总体 Miss Rate: {miss_rate:.2f}%")
    
    # 调整布局
    plt.tight_layout()
    
    # 保存图片
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"\n组合图表已保存到: {output_file}")
    
    # 显示图表
    plt.show()

def main():
    # 默认日志文件路径
    default_log_path = '../pmu/log/itlb_monitor.log'
    
    # 从命令行参数获取日志文件路径
    if len(sys.argv) > 1:
        log_path = sys.argv[1]
    else:
        log_path = default_log_path
    
    # 获取输出文件名
    if len(sys.argv) > 2:
        output_file = sys.argv[2]
    else:
        output_file = 'itlb_monitor.png'
    
    print(f"iTLB Monitor 数据可视化")
    print(f"日志文件: {log_path}")
    print(f"输出文件: {output_file}\n")
    
    # 绘制分离图表
    print("=" * 50)
    print("生成分离子图...")
    print("=" * 50)
    plot_itlb_data(log_path, output_file)
    
    # 绘制组合图表
    print("\n" + "=" * 50)
    print("生成组合图表...")
    print("=" * 50)
    combined_output = output_file.replace('.png', '_combined.png')
    plot_itlb_combined(log_path, combined_output)

if __name__ == '__main__':
    main()
