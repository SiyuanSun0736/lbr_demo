#!/usr/bin/env python3
"""
LBR 热点分析工具
识别最频繁执行的代码路径和热点函数
"""

import re
import sys
from collections import defaultdict, Counter
from pathlib import Path

def parse_lbr_log(log_file):
    """解析LBR日志"""
    branches = []
    
    with open(log_file, 'r') as f:
        for line in f:
            # 匹配LBR条目
            match = re.search(r'\[user\]\+0x([0-9a-f]+)\s+->\s+\[user\]\+0x([0-9a-f]+)', line)
            if match:
                from_addr = int(match.group(1), 16)
                to_addr = int(match.group(2), 16)
                branches.append((from_addr, to_addr))
    
    return branches

def analyze_hotspots(branches):
    """分析热点地址"""
    # 统计每个地址作为源地址和目标地址的频率
    from_counter = Counter(b[0] for b in branches)
    to_counter = Counter(b[1] for b in branches)
    
    # 所有地址的总频率
    all_addrs = Counter()
    for from_addr, to_addr in branches:
        all_addrs[from_addr] += 1
        all_addrs[to_addr] += 1
    
    print("=" * 80)
    print("热点地址分析 (最频繁访问的代码位置)")
    print("=" * 80)
    
    print("\nTop 20 最活跃的地址:")
    for addr, count in all_addrs.most_common(20):
        percentage = (count / (len(branches) * 2)) * 100
        print(f"  0x{addr:016x}: 出现 {count:4d} 次 ({percentage:5.2f}%)")
    
    print("\nTop 10 最常作为跳转源的地址:")
    for addr, count in from_counter.most_common(10):
        print(f"  0x{addr:016x}: {count:4d} 次")
    
    print("\nTop 10 最常作为跳转目标的地址:")
    for addr, count in to_counter.most_common(10):
        print(f"  0x{addr:016x}: {count:4d} 次")

def analyze_patterns(branches):
    """分析分支模式"""
    print("\n" + "=" * 80)
    print("分支模式分析")
    print("=" * 80)
    
    # 统计每种跳转的频率
    branch_counter = Counter(branches)
    
    print(f"\n总分支数: {len(branches)}")
    print(f"唯一分支数: {len(branch_counter)}")
    
    print("\nTop 20 最频繁的分支跳转:")
    for (from_addr, to_addr), count in branch_counter.most_common(20):
        percentage = (count / len(branches)) * 100
        distance = abs(to_addr - from_addr)
        direction = "向前" if to_addr > from_addr else "向后"
        print(f"  0x{from_addr:x} -> 0x{to_addr:x}")
        print(f"    频率: {count:4d} 次 ({percentage:5.2f}%), 距离: {distance:6d} 字节 ({direction})")

def analyze_loops(branches):
    """检测循环模式"""
    print("\n" + "=" * 80)
    print("循环检测")
    print("=" * 80)
    
    # 检测后向跳转（可能是循环）
    backward_jumps = [(f, t) for f, t in branches if t < f]
    
    print(f"\n检测到 {len(backward_jumps)} 个后向跳转 (可能的循环)")
    
    # 统计最频繁的后向跳转
    backward_counter = Counter(backward_jumps)
    
    print("\n最频繁的循环跳转:")
    for (from_addr, to_addr), count in backward_counter.most_common(10):
        distance = from_addr - to_addr
        print(f"  0x{to_addr:x} <- 0x{from_addr:x}")
        print(f"    循环体大小: ~{distance} 字节, 执行次数: {count}")

def analyze_call_return(branches):
    """分析函数调用和返回模式"""
    print("\n" + "=" * 80)
    print("函数调用模式分析")
    print("=" * 80)
    
    # 长距离跳转可能是函数调用
    long_jumps = [(f, t) for f, t in branches if abs(t - f) > 1000]
    
    print(f"\n检测到 {len(long_jumps)} 个长距离跳转 (可能的函数调用)")
    
    # 统计调用频率
    call_counter = Counter(long_jumps)
    
    print("\n最频繁的函数调用:")
    for (from_addr, to_addr), count in call_counter.most_common(10):
        distance = abs(to_addr - from_addr)
        print(f"  0x{from_addr:x} -> 0x{to_addr:x}")
        print(f"    距离: {distance} 字节, 调用次数: {count}")

def build_control_flow(branches):
    """构建控制流图"""
    print("\n" + "=" * 80)
    print("控制流分析")
    print("=" * 80)
    
    # 构建邻接表
    cfg = defaultdict(set)
    for from_addr, to_addr in branches:
        cfg[from_addr].add(to_addr)
    
    print(f"\n基本块数量: {len(cfg)}")
    
    # 找出扇出最大的地址（分支密集的代码）
    fanout = [(addr, len(targets)) for addr, targets in cfg.items()]
    fanout.sort(key=lambda x: x[1], reverse=True)
    
    print("\nTop 10 扇出最大的地址 (分支密集代码):")
    for addr, count in fanout[:10]:
        print(f"  0x{addr:016x}: {count} 个不同的跳转目标")
        for target in list(cfg[addr])[:5]:
            print(f"    -> 0x{target:016x}")

def analyze_coverage(branches):
    """分析代码覆盖率"""
    print("\n" + "=" * 80)
    print("代码覆盖分析")
    print("=" * 80)
    
    # 收集所有访问的地址
    all_addrs = set()
    for from_addr, to_addr in branches:
        all_addrs.add(from_addr)
        all_addrs.add(to_addr)
    
    # 计算地址范围
    if all_addrs:
        min_addr = min(all_addrs)
        max_addr = max(all_addrs)
        addr_range = max_addr - min_addr
        
        print(f"\n访问的地址范围:")
        print(f"  最小地址: 0x{min_addr:016x}")
        print(f"  最大地址: 0x{max_addr:016x}")
        print(f"  地址范围: {addr_range:,} 字节 ({addr_range / 1024:.2f} KB)")
        print(f"  唯一地址: {len(all_addrs)}")
        
        # 估算代码密度
        density = len(all_addrs) / addr_range * 100 if addr_range > 0 else 0
        print(f"  代码密度: {density:.4f}%")

def generate_heatmap_data(branches, output_file="lbr_heatmap.csv"):
    """生成热力图数据"""
    branch_counter = Counter(branches)
    
    with open(output_file, 'w') as f:
        f.write("from_addr,to_addr,count\n")
        for (from_addr, to_addr), count in branch_counter.items():
            f.write(f"0x{from_addr:x},0x{to_addr:x},{count}\n")
    
    print(f"\n热力图数据已保存到: {output_file}")

def main():
    if len(sys.argv) < 2:
        print("用法: python3 analyze_lbr_hotspots.py <lbr_log_file>")
        print("\n示例:")
        print("  python3 analyze_lbr_hotspots.py ../log/lbr_output_*.log")
        sys.exit(1)
    
    log_file = sys.argv[1]
    
    print(f"分析日志文件: {log_file}\n")
    
    # 解析分支数据
    branches = parse_lbr_log(log_file)
    
    if not branches:
        print("错误: 日志中没有找到分支数据")
        sys.exit(1)
    
    print(f"找到 {len(branches)} 个分支记录\n")
    
    # 执行各种分析
    analyze_hotspots(branches)
    analyze_patterns(branches)
    analyze_loops(branches)
    analyze_call_return(branches)
    build_control_flow(branches)
    analyze_coverage(branches)
    
    # 生成数据文件
    generate_heatmap_data(branches)
    
    print("\n" + "=" * 80)
    print("分析完成!")
    print("=" * 80)

if __name__ == "__main__":
    main()
