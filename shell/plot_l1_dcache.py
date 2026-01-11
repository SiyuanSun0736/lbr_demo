#!/usr/bin/env python3
"""
L1 D-Cache Monitor Log Visualization Script
读取 l1_dcache_monitor.log 文件并生成时间轴图表
"""

import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
from datetime import datetime
import sys
import os

def plot_l1_dcache_data(log_file_path, output_file='l1_dcache_monitor.png'):
    """
    读取 L1 D-Cache 监控日志并生成时间轴图表（分离子图）
    
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
    
    # 创建图表 - 3个子图
    fig, (ax1, ax2, ax3) = plt.subplots(3, 1, figsize=(14, 10), sharex=True)
    
    # 绘制 L1-dcache-loads
    ax1.plot(df['Timestamp'], df['L1-dcache-loads'], 
             color='blue', linewidth=2, marker='o', markersize=4, label='L1-dcache-loads')
    ax1.set_ylabel('L1 D-Cache Loads', fontsize=12, fontweight='bold')
    ax1.set_title('L1 D-Cache Loads', fontsize=13, fontweight='bold')
    ax1.grid(True, alpha=0.3)
    ax1.legend(loc='upper left')
    
    # 绘制 L1-dcache-load-misses
    ax2.plot(df['Timestamp'], df['L1-dcache-load-misses'], 
             color='red', linewidth=2, marker='s', markersize=4, label='L1-dcache-load-misses')
    ax2.set_ylabel('L1 D-Cache Load Misses', fontsize=12, fontweight='bold')
    ax2.set_title('L1 D-Cache Load Misses', fontsize=13, fontweight='bold')
    ax2.grid(True, alpha=0.3)
    ax2.legend(loc='upper left')
    
    # 绘制 L1-dcache-stores
    ax3.plot(df['Timestamp'], df['L1-dcache-stores'], 
             color='green', linewidth=2, marker='^', markersize=4, label='L1-dcache-stores')
    ax3.set_ylabel('L1 D-Cache Stores', fontsize=12, fontweight='bold')
    ax3.set_xlabel('Time', fontsize=12, fontweight='bold')
    ax3.set_title('L1 D-Cache Stores', fontsize=13, fontweight='bold')
    ax3.grid(True, alpha=0.3)
    ax3.legend(loc='upper left')
    
    # 格式化 x 轴时间显示
    ax3.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
    plt.setp(ax3.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    # 添加总标题
    fig.suptitle('L1 D-Cache Monitoring - Loads, Stores and Misses Over Time', 
                 fontsize=14, fontweight='bold', y=0.995)
    
    # 计算并显示统计信息
    print("\n统计信息:")
    print(f"L1-dcache-loads - 平均: {df['L1-dcache-loads'].mean():.2f}, "
          f"最大: {df['L1-dcache-loads'].max()}, "
          f"最小: {df['L1-dcache-loads'].min()}")
    print(f"L1-dcache-load-misses - 平均: {df['L1-dcache-load-misses'].mean():.2f}, "
          f"最大: {df['L1-dcache-load-misses'].max()}, "
          f"最小: {df['L1-dcache-load-misses'].min()}")
    print(f"L1-dcache-stores - 平均: {df['L1-dcache-stores'].mean():.2f}, "
          f"最大: {df['L1-dcache-stores'].max()}, "
          f"最小: {df['L1-dcache-stores'].min()}")
    
    # 计算 miss rate
    if df['L1-dcache-loads'].sum() > 0:
        miss_rate = (df['L1-dcache-load-misses'].sum() / df['L1-dcache-loads'].sum()) * 100
        print(f"Load Miss Rate: {miss_rate:.2f}%")
    
    # 调整布局
    plt.tight_layout()
    
    # 保存图片
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"\n图表已保存到: {output_file}")
    
    # 显示图表
    plt.show()

def plot_l1_dcache_combined(log_file_path, output_file='l1_dcache_monitor_combined.png'):
    """
    读取 L1 D-Cache 监控日志并生成组合时间轴图表（所有曲线在一起）
    
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
    
    # 绘制 L1-dcache-loads 和 stores（左 Y 轴）
    color1 = 'tab:blue'
    ax1.set_xlabel('Time', fontsize=12, fontweight='bold')
    ax1.set_ylabel('L1 D-Cache Access Count', fontsize=12, fontweight='bold', color='black')
    line1 = ax1.plot(df['Timestamp'], df['L1-dcache-loads'], 
                     color=color1, linewidth=2, marker='o', markersize=4, 
                     label='L1-dcache-loads', alpha=0.7)
    
    color3 = 'tab:green'
    line3 = ax1.plot(df['Timestamp'], df['L1-dcache-stores'], 
                     color=color3, linewidth=2, marker='^', markersize=4, 
                     label='L1-dcache-stores', alpha=0.7)
    
    ax1.tick_params(axis='y')
    ax1.grid(True, alpha=0.3)
    
    # 创建第二个 Y 轴绘制 L1-dcache-load-misses
    ax2 = ax1.twinx()
    color2 = 'tab:red'
    ax2.set_ylabel('L1 D-Cache Load Misses', fontsize=12, fontweight='bold', color=color2)
    line2 = ax2.plot(df['Timestamp'], df['L1-dcache-load-misses'], 
                     color=color2, linewidth=2, marker='s', markersize=4, 
                     label='L1-dcache-load-misses', alpha=0.7)
    ax2.tick_params(axis='y', labelcolor=color2)
    
    # 设置标题
    ax1.set_title('L1 D-Cache Monitoring - Combined View', fontsize=14, fontweight='bold')
    
    # 合并图例
    lines = line1 + line3 + line2
    labels = [l.get_label() for l in lines]
    ax1.legend(lines, labels, loc='upper left', fontsize=10)
    
    # 格式化 x 轴时间显示
    ax1.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
    plt.setp(ax1.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    # 计算并显示统计信息
    print("\n统计信息:")
    print(f"L1-dcache-loads - 平均: {df['L1-dcache-loads'].mean():.2f}, "
          f"最大: {df['L1-dcache-loads'].max()}, "
          f"最小: {df['L1-dcache-loads'].min()}")
    print(f"L1-dcache-load-misses - 平均: {df['L1-dcache-load-misses'].mean():.2f}, "
          f"最大: {df['L1-dcache-load-misses'].max()}, "
          f"最小: {df['L1-dcache-load-misses'].min()}")
    print(f"L1-dcache-stores - 平均: {df['L1-dcache-stores'].mean():.2f}, "
          f"最大: {df['L1-dcache-stores'].max()}, "
          f"最小: {df['L1-dcache-stores'].min()}")
    
    # 计算 miss rate
    if df['L1-dcache-loads'].sum() > 0:
        miss_rate = (df['L1-dcache-load-misses'].sum() / df['L1-dcache-loads'].sum()) * 100
        print(f"Load Miss Rate: {miss_rate:.2f}%")
    
    # 调整布局
    plt.tight_layout()
    
    # 保存图片
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"\n组合图表已保存到: {output_file}")
    
    # 显示图表
    plt.show()

def main():
    # 默认日志文件路径
    default_log_path = '../pmu/log/l1_dcache_monitor.log'
    
    # 从命令行参数获取日志文件路径
    if len(sys.argv) > 1:
        log_path = sys.argv[1]
    else:
        log_path = default_log_path
    
    # 获取输出文件名
    if len(sys.argv) > 2:
        output_file = sys.argv[2]
    else:
        output_file = 'l1_dcache_monitor.png'
    
    print(f"L1 D-Cache Monitor 数据可视化")
    print(f"日志文件: {log_path}")
    print(f"输出文件: {output_file}\n")
    
    # 绘制分离图表
    print("=" * 50)
    print("生成分离子图...")
    print("=" * 50)
    plot_l1_dcache_data(log_path, output_file)
    
    # 绘制组合图表
    print("\n" + "=" * 50)
    print("生成组合图表...")
    print("=" * 50)
    combined_output = output_file.replace('.png', '_combined.png')
    plot_l1_dcache_combined(log_path, combined_output)

if __name__ == '__main__':
    main()
