#!/usr/bin/env python3
"""
L1D Pending Miss Monitor Log Visualization Script
读取 l1d_pend_miss_monitor.log 文件并生成时间轴图表
"""

import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
from datetime import datetime
import sys
import os

def plot_l1d_pend_miss_data(log_file_path, output_file='l1d_pend_miss_monitor.png'):
    """
    读取 L1D Pending Miss 监控日志并生成时间轴图表（分离子图）
    
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
    fig, (ax1, ax2, ax3) = plt.subplots(3, 1, figsize=(12, 10), sharex=True)
    
    # 绘制 l1d.replacement
    ax1.plot(df['Timestamp'], df['l1d.replacement'], 
             color='blue', linewidth=2, marker='o', markersize=4, label='l1d.replacement')
    ax1.set_ylabel('L1D Replacement', fontsize=12, fontweight='bold')
    ax1.set_title('L1D Pending Miss Monitoring Over Time', fontsize=14, fontweight='bold')
    ax1.grid(True, alpha=0.3)
    ax1.legend(loc='upper left')
    
    # 绘制 l1d_pend_miss.fb_full
    ax2.plot(df['Timestamp'], df['l1d_pend_miss.fb_full'], 
             color='red', linewidth=2, marker='s', markersize=4, label='l1d_pend_miss.fb_full')
    ax2.set_ylabel('FB Full', fontsize=12, fontweight='bold')
    ax2.grid(True, alpha=0.3)
    ax2.legend(loc='upper left')
    
    # 绘制 l1d_pend_miss.pending
    ax3.plot(df['Timestamp'], df['l1d_pend_miss.pending'], 
             color='green', linewidth=2, marker='^', markersize=4, label='l1d_pend_miss.pending')
    ax3.set_ylabel('Pending Misses', fontsize=12, fontweight='bold')
    ax3.set_xlabel('Time', fontsize=12, fontweight='bold')
    ax3.grid(True, alpha=0.3)
    ax3.legend(loc='upper left')
    
    # 格式化 x 轴时间显示
    ax3.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
    plt.setp(ax3.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    # 计算并显示统计信息
    print("\n统计信息:")
    print(f"l1d.replacement - 平均: {df['l1d.replacement'].mean():.2f}, "
          f"最大: {df['l1d.replacement'].max()}, "
          f"最小: {df['l1d.replacement'].min()}")
    print(f"l1d_pend_miss.fb_full - 平均: {df['l1d_pend_miss.fb_full'].mean():.2f}, "
          f"最大: {df['l1d_pend_miss.fb_full'].max()}, "
          f"最小: {df['l1d_pend_miss.fb_full'].min()}")
    print(f"l1d_pend_miss.pending - 平均: {df['l1d_pend_miss.pending'].mean():.2f}, "
          f"最大: {df['l1d_pend_miss.pending'].max()}, "
          f"最小: {df['l1d_pend_miss.pending'].min()}")
    
    # 调整布局
    plt.tight_layout()
    
    # 保存图片
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"\n图表已保存到: {output_file}")
    
    # 显示图表
    plt.show()

def plot_l1d_pend_miss_combined(log_file_path, output_file='l1d_pend_miss_monitor_combined.png'):
    """
    读取 L1D Pending Miss 监控日志并生成组合时间轴图表（所有曲线在一起）
    
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
    fig, ax = plt.subplots(figsize=(14, 8))
    
    # 绘制所有三条曲线
    ax.plot(df['Timestamp'], df['l1d.replacement'], 
            color='tab:blue', linewidth=2, marker='o', markersize=4, 
            label='l1d.replacement', alpha=0.7)
    ax.plot(df['Timestamp'], df['l1d_pend_miss.fb_full'], 
            color='tab:red', linewidth=2, marker='s', markersize=4, 
            label='l1d_pend_miss.fb_full', alpha=0.7)
    ax.plot(df['Timestamp'], df['l1d_pend_miss.pending'], 
            color='tab:green', linewidth=2, marker='^', markersize=4, 
            label='l1d_pend_miss.pending', alpha=0.7)
    
    # 设置标签和标题
    ax.set_xlabel('Time', fontsize=12, fontweight='bold')
    ax.set_ylabel('Event Count', fontsize=12, fontweight='bold')
    ax.set_title('L1D Pending Miss Monitoring - Combined View', fontsize=14, fontweight='bold')
    ax.grid(True, alpha=0.3)
    ax.legend(loc='upper left', fontsize=10)
    
    # 格式化 x 轴时间显示
    ax.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
    plt.setp(ax.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    # 计算并显示统计信息
    print("\n统计信息:")
    print(f"l1d.replacement - 平均: {df['l1d.replacement'].mean():.2f}, "
          f"最大: {df['l1d.replacement'].max()}, "
          f"最小: {df['l1d.replacement'].min()}")
    print(f"l1d_pend_miss.fb_full - 平均: {df['l1d_pend_miss.fb_full'].mean():.2f}, "
          f"最大: {df['l1d_pend_miss.fb_full'].max()}, "
          f"最小: {df['l1d_pend_miss.fb_full'].min()}")
    print(f"l1d_pend_miss.pending - 平均: {df['l1d_pend_miss.pending'].mean():.2f}, "
          f"最大: {df['l1d_pend_miss.pending'].max()}, "
          f"最小: {df['l1d_pend_miss.pending'].min()}")
    
    # 调整布局
    plt.tight_layout()
    
    # 保存图片
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"\n组合图表已保存到: {output_file}")
    
    # 显示图表
    plt.show()

def main():
    # 默认日志文件路径
    default_log_path = '../pmu/log/l1d_pend_miss_monitor.log'
    
    # 从命令行参数获取日志文件路径
    if len(sys.argv) > 1:
        log_path = sys.argv[1]
    else:
        log_path = default_log_path
    
    # 获取输出文件名
    if len(sys.argv) > 2:
        output_file = sys.argv[2]
    else:
        output_file = 'l1d_pend_miss_monitor.png'
    
    print(f"L1D Pending Miss Monitor 数据可视化")
    print(f"日志文件: {log_path}")
    print(f"输出文件: {output_file}\n")
    
    # 绘制分离图表
    print("=" * 50)
    print("生成分离子图...")
    print("=" * 50)
    plot_l1d_pend_miss_data(log_path, output_file)
    
    # 绘制组合图表
    print("\n" + "=" * 50)
    print("生成组合图表...")
    print("=" * 50)
    combined_output = output_file.replace('.png', '_combined.png')
    plot_l1d_pend_miss_combined(log_path, combined_output)

if __name__ == '__main__':
    main()
