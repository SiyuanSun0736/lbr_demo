#!/usr/bin/env python3
"""
dTLB Monitor Log Visualization Script
读取 dtlb_monitor.log 文件并生成时间轴图表
"""

import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
from datetime import datetime
import sys
import os

def plot_dtlb_data(log_file_path, output_file='dtlb_monitor.png'):
    """
    读取 dTLB 监控日志并生成时间轴图表（分离子图）
    
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
    
    # 创建图表 - 4个子图
    fig, ((ax1, ax2), (ax3, ax4)) = plt.subplots(2, 2, figsize=(16, 10), sharex=True)
    
    # 绘制 dTLB-loads
    ax1.plot(df['Timestamp'], df['dTLB-loads'], 
             color='blue', linewidth=2, marker='o', markersize=4, label='dTLB-loads')
    ax1.set_ylabel('dTLB Loads', fontsize=11, fontweight='bold')
    ax1.set_title('dTLB Loads', fontsize=12, fontweight='bold')
    ax1.grid(True, alpha=0.3)
    ax1.legend(loc='upper left')
    
    # 绘制 dTLB-load-misses
    ax2.plot(df['Timestamp'], df['dTLB-load-misses'], 
             color='red', linewidth=2, marker='s', markersize=4, label='dTLB-load-misses')
    ax2.set_ylabel('dTLB Load Misses', fontsize=11, fontweight='bold')
    ax2.set_title('dTLB Load Misses', fontsize=12, fontweight='bold')
    ax2.grid(True, alpha=0.3)
    ax2.legend(loc='upper left')
    
    # 绘制 dTLB-stores
    ax3.plot(df['Timestamp'], df['dTLB-stores'], 
             color='green', linewidth=2, marker='^', markersize=4, label='dTLB-stores')
    ax3.set_ylabel('dTLB Stores', fontsize=11, fontweight='bold')
    ax3.set_xlabel('Time', fontsize=11, fontweight='bold')
    ax3.set_title('dTLB Stores', fontsize=12, fontweight='bold')
    ax3.grid(True, alpha=0.3)
    ax3.legend(loc='upper left')
    
    # 绘制 dTLB-store-misses
    ax4.plot(df['Timestamp'], df['dTLB-store-misses'], 
             color='orange', linewidth=2, marker='d', markersize=4, label='dTLB-store-misses')
    ax4.set_ylabel('dTLB Store Misses', fontsize=11, fontweight='bold')
    ax4.set_xlabel('Time', fontsize=11, fontweight='bold')
    ax4.set_title('dTLB Store Misses', fontsize=12, fontweight='bold')
    ax4.grid(True, alpha=0.3)
    ax4.legend(loc='upper left')
    
    # 格式化 x 轴时间显示
    for ax in [ax3, ax4]:
        ax.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
        plt.setp(ax.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    # 添加总标题
    fig.suptitle('dTLB Monitoring - Loads, Stores and Misses Over Time', 
                 fontsize=14, fontweight='bold', y=0.995)
    
    # 计算并显示统计信息
    print("\n统计信息:")
    print(f"dTLB-loads - 平均: {df['dTLB-loads'].mean():.2f}, "
          f"最大: {df['dTLB-loads'].max()}, "
          f"最小: {df['dTLB-loads'].min()}")
    print(f"dTLB-load-misses - 平均: {df['dTLB-load-misses'].mean():.2f}, "
          f"最大: {df['dTLB-load-misses'].max()}, "
          f"最小: {df['dTLB-load-misses'].min()}")
    print(f"dTLB-stores - 平均: {df['dTLB-stores'].mean():.2f}, "
          f"最大: {df['dTLB-stores'].max()}, "
          f"最小: {df['dTLB-stores'].min()}")
    print(f"dTLB-store-misses - 平均: {df['dTLB-store-misses'].mean():.2f}, "
          f"最大: {df['dTLB-store-misses'].max()}, "
          f"最小: {df['dTLB-store-misses'].min()}")
    
    # 计算 miss rate
    if df['dTLB-loads'].sum() > 0:
        load_miss_rate = (df['dTLB-load-misses'].sum() / df['dTLB-loads'].sum()) * 100
        print(f"Load Miss Rate: {load_miss_rate:.2f}%")
    
    if df['dTLB-stores'].sum() > 0:
        store_miss_rate = (df['dTLB-store-misses'].sum() / df['dTLB-stores'].sum()) * 100
        print(f"Store Miss Rate: {store_miss_rate:.2f}%")
    
    # 调整布局
    plt.tight_layout()
    
    # 保存图片
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"\n图表已保存到: {output_file}")
    
    # 显示图表
    plt.show()

def plot_dtlb_combined(log_file_path, output_file='dtlb_monitor_combined.png'):
    """
    读取 dTLB 监控日志并生成组合时间轴图表（所有曲线在一起）
    
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
    
    # 创建图表 - 2个子图（loads和stores分开）
    fig, (ax1, ax2) = plt.subplots(2, 1, figsize=(14, 10), sharex=True)
    
    # 第一个子图：Loads 和 Load Misses
    color1 = 'tab:blue'
    ax1.set_ylabel('dTLB Loads', fontsize=12, fontweight='bold', color=color1)
    line1 = ax1.plot(df['Timestamp'], df['dTLB-loads'], 
                     color=color1, linewidth=2, marker='o', markersize=4, 
                     label='dTLB-loads', alpha=0.7)
    ax1.tick_params(axis='y', labelcolor=color1)
    ax1.grid(True, alpha=0.3)
    ax1.set_title('dTLB Loads and Load Misses', fontsize=13, fontweight='bold')
    
    ax1_twin = ax1.twinx()
    color2 = 'tab:red'
    ax1_twin.set_ylabel('dTLB Load Misses', fontsize=12, fontweight='bold', color=color2)
    line2 = ax1_twin.plot(df['Timestamp'], df['dTLB-load-misses'], 
                          color=color2, linewidth=2, marker='s', markersize=4, 
                          label='dTLB-load-misses', alpha=0.7)
    ax1_twin.tick_params(axis='y', labelcolor=color2)
    
    # 合并第一个图的图例
    lines1 = line1 + line2
    labels1 = [l.get_label() for l in lines1]
    ax1.legend(lines1, labels1, loc='upper left', fontsize=10)
    
    # 第二个子图：Stores 和 Store Misses
    color3 = 'tab:green'
    ax2.set_ylabel('dTLB Stores', fontsize=12, fontweight='bold', color=color3)
    ax2.set_xlabel('Time', fontsize=12, fontweight='bold')
    line3 = ax2.plot(df['Timestamp'], df['dTLB-stores'], 
                     color=color3, linewidth=2, marker='^', markersize=4, 
                     label='dTLB-stores', alpha=0.7)
    ax2.tick_params(axis='y', labelcolor=color3)
    ax2.grid(True, alpha=0.3)
    ax2.set_title('dTLB Stores and Store Misses', fontsize=13, fontweight='bold')
    
    ax2_twin = ax2.twinx()
    color4 = 'tab:orange'
    ax2_twin.set_ylabel('dTLB Store Misses', fontsize=12, fontweight='bold', color=color4)
    line4 = ax2_twin.plot(df['Timestamp'], df['dTLB-store-misses'], 
                          color=color4, linewidth=2, marker='d', markersize=4, 
                          label='dTLB-store-misses', alpha=0.7)
    ax2_twin.tick_params(axis='y', labelcolor=color4)
    
    # 合并第二个图的图例
    lines2 = line3 + line4
    labels2 = [l.get_label() for l in lines2]
    ax2.legend(lines2, labels2, loc='upper left', fontsize=10)
    
    # 格式化 x 轴时间显示
    ax2.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
    plt.setp(ax2.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    # 添加总标题
    fig.suptitle('dTLB Monitoring - Combined View', fontsize=14, fontweight='bold', y=0.995)
    
    # 计算并显示统计信息
    print("\n统计信息:")
    print(f"dTLB-loads - 平均: {df['dTLB-loads'].mean():.2f}, "
          f"最大: {df['dTLB-loads'].max()}, "
          f"最小: {df['dTLB-loads'].min()}")
    print(f"dTLB-load-misses - 平均: {df['dTLB-load-misses'].mean():.2f}, "
          f"最大: {df['dTLB-load-misses'].max()}, "
          f"最小: {df['dTLB-load-misses'].min()}")
    print(f"dTLB-stores - 平均: {df['dTLB-stores'].mean():.2f}, "
          f"最大: {df['dTLB-stores'].max()}, "
          f"最小: {df['dTLB-stores'].min()}")
    print(f"dTLB-store-misses - 平均: {df['dTLB-store-misses'].mean():.2f}, "
          f"最大: {df['dTLB-store-misses'].max()}, "
          f"最小: {df['dTLB-store-misses'].min()}")
    
    # 计算 miss rate
    if df['dTLB-loads'].sum() > 0:
        load_miss_rate = (df['dTLB-load-misses'].sum() / df['dTLB-loads'].sum()) * 100
        print(f"Load Miss Rate: {load_miss_rate:.2f}%")
    
    if df['dTLB-stores'].sum() > 0:
        store_miss_rate = (df['dTLB-store-misses'].sum() / df['dTLB-stores'].sum()) * 100
        print(f"Store Miss Rate: {store_miss_rate:.2f}%")
    
    # 调整布局
    plt.tight_layout()
    
    # 保存图片
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"\n组合图表已保存到: {output_file}")
    
    # 显示图表
    plt.show()

def main():
    # 默认日志文件路径
    default_log_path = '../pmu/log/dtlb_monitor.log'
    
    # 从命令行参数获取日志文件路径
    if len(sys.argv) > 1:
        log_path = sys.argv[1]
    else:
        log_path = default_log_path
    
    # 获取输出文件名
    if len(sys.argv) > 2:
        output_file = sys.argv[2]
    else:
        output_file = 'dtlb_monitor.png'
    
    print(f"dTLB Monitor 数据可视化")
    print(f"日志文件: {log_path}")
    print(f"输出文件: {output_file}\n")
    
    # 绘制分离图表
    print("=" * 50)
    print("生成分离子图...")
    print("=" * 50)
    plot_dtlb_data(log_path, output_file)
    
    # 绘制组合图表
    print("\n" + "=" * 50)
    print("生成组合图表...")
    print("=" * 50)
    combined_output = output_file.replace('.png', '_combined.png')
    plot_dtlb_combined(log_path, combined_output)

if __name__ == '__main__':
    main()
