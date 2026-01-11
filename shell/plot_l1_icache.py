#!/usr/bin/env python3
"""
L1 Instruction Cache Monitor Log Visualization Script
读取 l1_icache_monitor.log 文件并生成时间轴图表
"""

import pandas as pd
import matplotlib.pyplot as plt
import matplotlib.dates as mdates
from datetime import datetime
import sys
import os

def plot_l1_icache_data(log_file_path, output_file='l1_icache_monitor.png'):
    """
    读取 L1 Instruction Cache 监控日志并生成时间轴图表（分离子图）
    
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
    fig, ax = plt.subplots(figsize=(12, 6))
    
    # 绘制 L1-icache-load-misses
    ax.plot(df['Timestamp'], df['L1-icache-load-misses'], 
            color='red', linewidth=2, marker='s', markersize=4, label='L1-icache-load-misses')
    ax.set_ylabel('L1 Instruction Cache Load Misses', fontsize=12, fontweight='bold')
    ax.set_xlabel('Time', fontsize=12, fontweight='bold')
    ax.set_title('L1 Instruction Cache Monitoring - Load Misses Over Time', fontsize=14, fontweight='bold')
    ax.grid(True, alpha=0.3)
    ax.legend(loc='upper left')
    
    # 格式化 x 轴时间显示
    ax.xaxis.set_major_formatter(mdates.DateFormatter('%H:%M:%S'))
    plt.setp(ax.xaxis.get_majorticklabels(), rotation=45, ha='right')
    
    # 计算并显示统计信息
    print("\n统计信息:")
    print(f"L1-icache-load-misses - 平均: {df['L1-icache-load-misses'].mean():.2f}, "
          f"最大: {df['L1-icache-load-misses'].max()}, "
          f"最小: {df['L1-icache-load-misses'].min()}")
    
    # 调整布局
    plt.tight_layout()
    
    # 保存图片
    plt.savefig(output_file, dpi=300, bbox_inches='tight')
    print(f"\n图表已保存到: {output_file}")
    
    # 显示图表
    plt.show()

def main():
    # 默认日志文件路径
    default_log_path = '../pmu/log/l1_icache_monitor.log'
    
    # 从命令行参数获取日志文件路径
    if len(sys.argv) > 1:
        log_path = sys.argv[1]
    else:
        log_path = default_log_path
    
    # 获取输出文件名
    if len(sys.argv) > 2:
        output_file = sys.argv[2]
    else:
        output_file = 'l1_icache_monitor.png'
    
    print(f"L1 Instruction Cache Monitor 数据可视化")
    print(f"日志文件: {log_path}")
    print(f"输出文件: {output_file}\n")
    
    # 绘制图表
    print("=" * 50)
    print("生成图表...")
    print("=" * 50)
    plot_l1_icache_data(log_path, output_file)

if __name__ == '__main__':
    main()
