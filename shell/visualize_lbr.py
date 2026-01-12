#!/usr/bin/env python3
"""
LBR 可视化工具
生成分支跳转的可视化图表
"""

import re
import sys
import matplotlib.pyplot as plt
import matplotlib.patches as mpatches
from collections import Counter, defaultdict
import numpy as np

def parse_lbr_log(log_file):
    """解析LBR日志"""
    branches = []
    
    with open(log_file, 'r') as f:
        for line in f:
            match = re.search(r'\[user\]\+0x([0-9a-f]+)\s+->\s+\[user\]\+0x([0-9a-f]+)', line)
            if match:
                from_addr = int(match.group(1), 16)
                to_addr = int(match.group(2), 16)
                branches.append((from_addr, to_addr))
    
    return branches

def plot_branch_frequency(branches, output_file="lbr_branch_freq.png"):
    """绘制分支频率图"""
    branch_counter = Counter(branches)
    top_branches = branch_counter.most_common(30)
    
    labels = [f"0x{f:x}\n→\n0x{t:x}" for (f, t), _ in top_branches]
    counts = [count for _, count in top_branches]
    
    plt.figure(figsize=(16, 8))
    bars = plt.bar(range(len(counts)), counts, color='steelblue', edgecolor='black')
    
    # 为最热的几个分支添加高亮
    for i in range(min(5, len(bars))):
        bars[i].set_color('orangered')
    
    plt.xlabel('分支跳转', fontsize=12)
    plt.ylabel('频率', fontsize=12)
    plt.title('Top 30 最频繁的分支跳转', fontsize=14, fontweight='bold')
    plt.xticks(range(len(labels)), labels, rotation=45, ha='right', fontsize=8)
    plt.grid(axis='y', alpha=0.3)
    plt.tight_layout()
    plt.savefig(output_file, dpi=150, bbox_inches='tight')
    print(f"分支频率图已保存到: {output_file}")
    plt.close()

def plot_jump_distance(branches, output_file="lbr_jump_distance.png"):
    """绘制跳转距离分布图"""
    distances = [abs(t - f) for f, t in branches]
    
    fig, (ax1, ax2) = plt.subplots(1, 2, figsize=(14, 5))
    
    # 直方图
    ax1.hist(distances, bins=50, color='skyblue', edgecolor='black', alpha=0.7)
    ax1.set_xlabel('跳转距离 (字节)', fontsize=11)
    ax1.set_ylabel('频率', fontsize=11)
    ax1.set_title('跳转距离分布 (直方图)', fontsize=12, fontweight='bold')
    ax1.grid(axis='y', alpha=0.3)
    
    # 对数尺度
    ax2.hist(distances, bins=50, color='lightcoral', edgecolor='black', alpha=0.7)
    ax2.set_xlabel('跳转距离 (字节)', fontsize=11)
    ax2.set_ylabel('频率 (对数尺度)', fontsize=11)
    ax2.set_yscale('log')
    ax2.set_title('跳转距离分布 (对数尺度)', fontsize=12, fontweight='bold')
    ax2.grid(axis='y', alpha=0.3)
    
    plt.tight_layout()
    plt.savefig(output_file, dpi=150, bbox_inches='tight')
    print(f"跳转距离图已保存到: {output_file}")
    plt.close()

def plot_address_heatmap(branches, output_file="lbr_address_heatmap.png"):
    """绘制地址热力图"""
    # 收集所有地址
    all_addrs = sorted(set([a for b in branches for a in b]))
    
    if len(all_addrs) < 2:
        print("地址数量太少,跳过热力图")
        return
    
    # 创建地址到索引的映射
    addr_to_idx = {addr: i for i, addr in enumerate(all_addrs)}
    
    # 创建矩阵
    size = min(len(all_addrs), 100)  # 限制大小
    matrix = np.zeros((size, size))
    
    for from_addr, to_addr in branches:
        if from_addr in addr_to_idx and to_addr in addr_to_idx:
            from_idx = addr_to_idx[from_addr]
            to_idx = addr_to_idx[to_addr]
            if from_idx < size and to_idx < size:
                matrix[from_idx, to_idx] += 1
    
    plt.figure(figsize=(12, 10))
    plt.imshow(matrix, cmap='hot', interpolation='nearest', aspect='auto')
    plt.colorbar(label='跳转频率')
    plt.xlabel('目标地址索引', fontsize=11)
    plt.ylabel('源地址索引', fontsize=11)
    plt.title('LBR 地址跳转热力图', fontsize=13, fontweight='bold')
    plt.tight_layout()
    plt.savefig(output_file, dpi=150, bbox_inches='tight')
    print(f"地址热力图已保存到: {output_file}")
    plt.close()

def plot_jump_direction(branches, output_file="lbr_jump_direction.png"):
    """绘制跳转方向分析"""
    forward = sum(1 for f, t in branches if t > f)
    backward = sum(1 for f, t in branches if t < f)
    same = sum(1 for f, t in branches if t == f)
    
    labels = ['向前跳转', '向后跳转', '同地址']
    sizes = [forward, backward, same]
    colors = ['lightgreen', 'lightcoral', 'lightgray']
    explode = (0.05, 0.05, 0)
    
    plt.figure(figsize=(10, 8))
    plt.pie(sizes, labels=labels, colors=colors, autopct='%1.1f%%',
            startangle=90, explode=explode, shadow=True, textprops={'fontsize': 12})
    plt.title('跳转方向分布', fontsize=14, fontweight='bold')
    
    # 添加图例
    total = sum(sizes)
    legend_labels = [f'{label}: {size} ({size/total*100:.1f}%)' 
                     for label, size in zip(labels, sizes)]
    plt.legend(legend_labels, loc='upper right', fontsize=10)
    
    plt.tight_layout()
    plt.savefig(output_file, dpi=150, bbox_inches='tight')
    print(f"跳转方向图已保存到: {output_file}")
    plt.close()

def plot_hotspot_addresses(branches, output_file="lbr_hotspot_addrs.png"):
    """绘制热点地址"""
    all_addrs = Counter()
    for from_addr, to_addr in branches:
        all_addrs[from_addr] += 1
        all_addrs[to_addr] += 1
    
    top_addrs = all_addrs.most_common(20)
    
    addrs = [f"0x{addr:x}" for addr, _ in top_addrs]
    counts = [count for _, count in top_addrs]
    
    plt.figure(figsize=(12, 8))
    bars = plt.barh(range(len(counts)), counts, color='teal', edgecolor='black')
    
    # 渐变色
    colors = plt.cm.YlOrRd(np.linspace(0.3, 0.9, len(bars)))
    for bar, color in zip(bars, colors):
        bar.set_color(color)
    
    plt.yticks(range(len(addrs)), addrs, fontsize=9)
    plt.xlabel('访问频率', fontsize=11)
    plt.ylabel('地址', fontsize=11)
    plt.title('Top 20 热点地址', fontsize=13, fontweight='bold')
    plt.grid(axis='x', alpha=0.3)
    plt.tight_layout()
    plt.savefig(output_file, dpi=150, bbox_inches='tight')
    print(f"热点地址图已保存到: {output_file}")
    plt.close()

def plot_control_flow_graph(branches, output_file="lbr_cfg.png"):
    """绘制简化的控制流图"""
    # 构建边的权重
    edge_weights = Counter(branches)
    top_edges = edge_weights.most_common(30)
    
    # 提取节点
    nodes = set()
    for (f, t), _ in top_edges:
        nodes.add(f)
        nodes.add(t)
    
    node_list = sorted(nodes)
    node_to_y = {node: i for i, node in enumerate(node_list)}
    
    fig, ax = plt.subplots(figsize=(14, 10))
    
    # 绘制边
    for (from_addr, to_addr), weight in top_edges:
        if from_addr in node_to_y and to_addr in node_to_y:
            y1 = node_to_y[from_addr]
            y2 = node_to_y[to_addr]
            
            # 线宽表示频率
            linewidth = min(weight / 5 + 0.5, 5)
            
            # 颜色表示方向
            color = 'green' if to_addr > from_addr else 'red'
            alpha = min(weight / max(edge_weights.values()), 0.8)
            
            ax.plot([0, 1], [y1, y2], color=color, linewidth=linewidth, 
                   alpha=alpha, zorder=1)
    
    # 绘制节点
    for node, y in node_to_y.items():
        ax.scatter([0.5], [y], s=200, c='blue', zorder=2, edgecolors='black')
        ax.text(0.5, y, f'0x{node:x}', ha='center', va='center',
               fontsize=7, color='white', weight='bold')
    
    ax.set_xlim(-0.2, 1.2)
    ax.set_ylim(-1, len(node_list))
    ax.set_title('控制流图 (Top 30 分支)', fontsize=13, fontweight='bold')
    ax.axis('off')
    
    # 添加图例
    green_patch = mpatches.Patch(color='green', label='向前跳转')
    red_patch = mpatches.Patch(color='red', label='向后跳转')
    ax.legend(handles=[green_patch, red_patch], loc='upper right')
    
    plt.tight_layout()
    plt.savefig(output_file, dpi=150, bbox_inches='tight')
    print(f"控制流图已保存到: {output_file}")
    plt.close()

def main():
    if len(sys.argv) < 2:
        print("用法: python3 visualize_lbr.py <lbr_log_file>")
        print("\n示例:")
        print("  python3 visualize_lbr.py ../log/lbr_output_*.log")
        sys.exit(1)
    
    log_file = sys.argv[1]
    
    print(f"可视化日志文件: {log_file}\n")
    
    # 解析分支数据
    branches = parse_lbr_log(log_file)
    
    if not branches:
        print("错误: 日志中没有找到分支数据")
        sys.exit(1)
    
    print(f"找到 {len(branches)} 个分支记录\n")
    print("生成可视化图表...\n")
    
    # 生成各种图表
    plot_branch_frequency(branches)
    plot_jump_distance(branches)
    plot_jump_direction(branches)
    plot_hotspot_addresses(branches)
    plot_address_heatmap(branches)
    plot_control_flow_graph(branches)
    
    print("\n" + "=" * 80)
    print("可视化完成! 已生成以下图表:")
    print("  - lbr_branch_freq.png     : 分支频率图")
    print("  - lbr_jump_distance.png   : 跳转距离分布")
    print("  - lbr_jump_direction.png  : 跳转方向分析")
    print("  - lbr_hotspot_addrs.png   : 热点地址")
    print("  - lbr_address_heatmap.png : 地址热力图")
    print("  - lbr_cfg.png             : 控制流图")
    print("=" * 80)

if __name__ == "__main__":
    main()
