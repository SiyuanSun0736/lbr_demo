#!/usr/bin/env python3
"""
Memory Instructions Retired Monitor Log Visualization Script
读取 mem_inst_monitor.log 文件并生成时间轴图表
"""

import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
from datetime import datetime
import sys
import os

def plot_mem_inst_data(log_file_path, output_file='mem_inst_monitor.png'):
    """
    读取内存指令监控日志并生成时间轴图表（分离子图）
    
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
    
    # 第一个子图: mem-loads 和 mem-stores
    ax1.plot(df['Timestamp'], df['mem-loads'], 
             color='blue', linewidth=2, marker='o', markersize=4, label='mem-loads')
    ax1.plot(df['Timestamp'], df['mem-stores'], 
             color='red', linewidth=2, marker='s', markersize=4, label='mem-stores')
    ax1.set_ylabel('Count', fontsize=12, fontweight='bold')
    ax1.set_title('Memory Instructions Retired Monitoring', fontsize=14, fontweight='bold')
    ax1.grid(True, alpha=0.3)
    ax1.legend(loc='upper left')
    
    # 第二个子图: all_loads 和 all_stores
    ax2.plot(df['Timestamp'], df['all_loads'], 
             color='green', linewidth=2, marker='^', markersize=4, label='all_loads (retired)')
    ax2.plot(df['Timestamp'], df['all_stores'], 
             color='orange', linewidth=2, marker='v', markersize=4, label='all_stores (retired)')
    ax2.set_ylabel('Count', fontsize=12, fontweight='bold')
    ax2.grid(True, alpha=0.3)
    ax2.legend(loc='upper left')
    
    # 第三个子图: any (all memory instructions)
    ax3.plot(df['Timestamp'], df['any'], 
             color='purple', linewidth=2, marker='d', markersize=4, label='any (all retired)')
    ax3.set_ylabel('Count', fontsize=12, fontweight='bold')
    ax3.set_xlabel('Time', fontsize=12, fontweight='bold')
    ax3.grid(True, alpha=0.3)
    ax3.legend(loc='upper left')
    
    # 格式化 x 轴时间显示
    ax3.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
    plt.setp(ax3.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    # 计算并显示统计信息
    print("\n统计信息:")
    print(f"mem-loads - 平均: {df['mem-loads'].mean():.2f}, "
          f"最大: {df['mem-loads'].max()}, "
          f"最小: {df['mem-loads'].min()}")
    print(f"mem-stores - 平均: {df['mem-stores'].mean():.2f}, "
          f"最大: {df['mem-stores'].max()}, "
          f"最小: {df['mem-stores'].min()}")
    print(f"all_loads - 平均: {df['all_loads'].mean():.2f}, "
          f"最大: {df['all_loads'].max()}, "
          f"最小: {df['all_loads'].min()}")
    print(f"all_stores - 平均: {df['all_stores'].mean():.2f}, "
          f"最大: {df['all_stores'].max()}, "
          f"最小: {df['all_stores'].min()}")
    print(f"any - 平均: {df['any'].mean():.2f}, "
          f"最大: {df['any'].max()}, "
          f"最小: {df['any'].min()}")
    
    # 计算比率
    total_any = df['any'].sum()
    if total_any > 0:
        loads_ratio = (df['all_loads'].sum() / total_any) * 100
        stores_ratio = (df['all_stores'].sum() / total_any) * 100
        print(f"\nLoad/Store 比率:")
        print(f"  Loads: {loads_ratio:.2f}%")
        print(f"  Stores: {stores_ratio:.2f}%")
    
    # 调整布局
    plt.tight_layout()
    
    # 保存图片
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"\n图表已保存到: {output_file}")
    
    # 显示图表
    plt.show()

def plot_mem_inst_combined(log_file_path, output_file='mem_inst_monitor_combined.png'):
    """
    读取内存指令监控日志并生成组合时间轴图表（多 Y 轴）
    
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
    
    # 绘制 mem-loads 和 mem-stores（左 Y 轴）
    color1 = 'tab:blue'
    ax1.set_xlabel('Time', fontsize=12, fontweight='bold')
    ax1.set_ylabel('Memory Operations (Cache)', fontsize=12, fontweight='bold', color=color1)
    line1 = ax1.plot(df['Timestamp'], df['mem-loads'], 
                     color='blue', linewidth=2, marker='o', markersize=4, 
                     label='mem-loads', alpha=0.7)
    line2 = ax1.plot(df['Timestamp'], df['mem-stores'], 
                     color='red', linewidth=2, marker='s', markersize=4, 
                     label='mem-stores', alpha=0.7)
    ax1.tick_params(axis='y', labelcolor=color1)
    ax1.grid(True, alpha=0.3)
    
    # 创建第二个 Y 轴绘制 retired instructions
    ax2 = ax1.twinx()
    color2 = 'tab:green'
    ax2.set_ylabel('Retired Memory Instructions', fontsize=12, fontweight='bold', color=color2)
    line3 = ax2.plot(df['Timestamp'], df['all_loads'], 
                     color='green', linewidth=2, marker='^', markersize=4, 
                     label='all_loads (retired)', alpha=0.7)
    line4 = ax2.plot(df['Timestamp'], df['all_stores'], 
                     color='orange', linewidth=2, marker='v', markersize=4, 
                     label='all_stores (retired)', alpha=0.7)
    ax2.tick_params(axis='y', labelcolor=color2)
    
    # 创建第三个 Y 轴绘制 any
    ax3 = ax1.twinx()
    ax3.spines['right'].set_position(('outward', 60))
    color3 = 'tab:purple'
    ax3.set_ylabel('Total Retired', fontsize=12, fontweight='bold', color=color3)
    line5 = ax3.plot(df['Timestamp'], df['any'], 
                     color='purple', linewidth=2, marker='d', markersize=4, 
                     label='any (all retired)', alpha=0.7)
    ax3.tick_params(axis='y', labelcolor=color3)
    
    # 设置标题
    ax1.set_title('Memory Instructions Retired Monitoring - Combined View', fontsize=14, fontweight='bold')
    
    # 合并图例
    lines = line1 + line2 + line3 + line4 + line5
    labels = [l.get_label() for l in lines]
    ax1.legend(lines, labels, loc='upper left', fontsize=10)
    
    # 格式化 x 轴时间显示
    ax1.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
    plt.setp(ax1.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    # 计算并显示统计信息
    print("\n统计信息:")
    print(f"mem-loads - 平均: {df['mem-loads'].mean():.2f}, "
          f"最大: {df['mem-loads'].max()}, "
          f"最小: {df['mem-loads'].min()}")
    print(f"all_loads - 平均: {df['all_loads'].mean():.2f}, "
          f"最大: {df['all_loads'].max()}, "
          f"最小: {df['all_loads'].min()}")
    print(f"any - 平均: {df['any'].mean():.2f}, "
          f"最大: {df['any'].max()}, "
          f"最小: {df['any'].min()}")
    
    # 计算比率
    total_any = df['any'].sum()
    if total_any > 0:
        loads_ratio = (df['all_loads'].sum() / total_any) * 100
        stores_ratio = (df['all_stores'].sum() / total_any) * 100
        print(f"\nLoad/Store 比率:")
        print(f"  Loads: {loads_ratio:.2f}%")
        print(f"  Stores: {stores_ratio:.2f}%")
    
    # 调整布局
    plt.tight_layout()
    
    # 保存图片
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"\n组合图表已保存到: {output_file}")
    
    # 显示图表
    plt.show()

def main():
    # 默认日志文件路径
    default_log_path = '../pmu/log/mem_inst_monitor.log'
    
    # 从命令行参数获取日志文件路径
    if len(sys.argv) > 1:
        log_path = sys.argv[1]
    else:
        log_path = default_log_path
    
    # 获取输出文件名
    if len(sys.argv) > 2:
        output_file = sys.argv[2]
    else:
        output_file = 'mem_inst_monitor.png'
    
    print(f"Memory Instructions Monitor 数据可视化")
    print(f"日志文件: {log_path}")
    print(f"输出文件: {output_file}\n")
    
    # 绘制分离图表
    print("=" * 50)
    print("生成分离子图...")
    print("=" * 50)
    plot_mem_inst_data(log_path, output_file)
    
    # 绘制组合图表
    print("\n" + "=" * 50)
    print("生成组合图表...")
    print("=" * 50)
    combined_output = output_file.replace('.png', '_combined.png')
    plot_mem_inst_combined(log_path, combined_output)

if __name__ == '__main__':
    main()
