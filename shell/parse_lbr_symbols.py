#!/usr/bin/env python3
"""
LBR 符号解析工具
将 LBR 日志中的地址转换为函数名和源代码位置
"""

import re
import subprocess
import sys
from pathlib import Path
from collections import defaultdict, Counter

def parse_lbr_log(log_file):
    """解析LBR日志文件"""
    entries = []
    current_record = None
    
    with open(log_file, 'r') as f:
        for line in f:
            # 匹配记录头
            if line.startswith('=== PID:'):
                match = re.search(r'PID: (\d+), TID: (\d+), COMM: (\S+), Entries: (\d+)', line)
                if match:
                    current_record = {
                        'pid': int(match.group(1)),
                        'tid': int(match.group(2)),
                        'comm': match.group(3),
                        'num_entries': int(match.group(4)),
                        'branches': []
                    }
            
            # 匹配LBR条目: [#00] [user]+0x7fd44945b547 -> [user]+0x7fd44945b590
            elif current_record and re.match(r'\[#\d+\]', line):
                match = re.search(r'\[user\]\+0x([0-9a-f]+)\s+->\s+\[user\]\+0x([0-9a-f]+)', line)
                if match:
                    from_addr = int(match.group(1), 16)
                    to_addr = int(match.group(2), 16)
                    current_record['branches'].append({
                        'from': from_addr,
                        'to': to_addr
                    })
            
            # 记录结束
            elif current_record and current_record['branches']:
                entries.append(current_record)
                current_record = None
    
    if current_record and current_record['branches']:
        entries.append(current_record)
    
    return entries

def get_process_maps(pid):
    """读取进程内存映射"""
    maps_file = f"/proc/{pid}/maps"
    try:
        with open(maps_file, 'r') as f:
            maps = []
            for line in f:
                # 格式: 地址范围 权限 偏移 设备 inode 路径
                parts = line.split()
                if len(parts) >= 6:
                    addr_range = parts[0].split('-')
                    maps.append({
                        'start': int(addr_range[0], 16),
                        'end': int(addr_range[1], 16),
                        'perms': parts[1],
                        'offset': int(parts[2], 16),
                        'path': ' '.join(parts[5:])
                    })
            return maps
    except FileNotFoundError:
        print(f"警告: 无法读取 /proc/{pid}/maps (进程可能已退出)")
        return []

def addr_to_symbol(addr, binary_path):
    """使用 addr2line 将地址转换为源代码位置"""
    try:
        result = subprocess.run(
            ['addr2line', '-e', binary_path, '-f', '-C', hex(addr)],
            capture_output=True,
            text=True,
            timeout=2
        )
        if result.returncode == 0:
            lines = result.stdout.strip().split('\n')
            if len(lines) >= 2:
                func_name = lines[0]
                location = lines[1]
                if func_name != '??' and location != '??:0':
                    return func_name, location
    except Exception as e:
        pass
    return None, None

def analyze_lbr_data(entries):
    """分析LBR数据"""
    print("=" * 80)
    print("LBR 数据分析报告")
    print("=" * 80)
    
    for record in entries:
        print(f"\n进程: {record['comm']} (PID: {record['pid']}, TID: {record['tid']})")
        print(f"分支记录数: {len(record['branches'])}")
        
        # 统计跳转频率
        jump_counts = Counter()
        for branch in record['branches']:
            jump_counts[(branch['from'], branch['to'])] += 1
        
        print("\n最频繁的跳转:")
        for (from_addr, to_addr), count in jump_counts.most_common(10):
            print(f"  0x{from_addr:x} -> 0x{to_addr:x} (出现 {count} 次)")
        
        # 分析跳转距离
        distances = []
        for branch in record['branches']:
            dist = abs(int(branch['to']) - int(branch['from']))
            distances.append(dist)
        
        if distances:
            print(f"\n跳转距离统计:")
            print(f"  平均距离: {sum(distances) / len(distances):.0f} 字节")
            print(f"  最小距离: {min(distances)} 字节")
            print(f"  最大距离: {max(distances)} 字节")
        
        # 分类跳转类型
        short_jumps = sum(1 for d in distances if d < 100)
        medium_jumps = sum(1 for d in distances if 100 <= d < 1000)
        long_jumps = sum(1 for d in distances if d >= 1000)
        
        print(f"\n跳转类型分布:")
        print(f"  短跳转 (< 100字节): {short_jumps}")
        print(f"  中等跳转 (100-1000字节): {medium_jumps}")
        print(f"  长跳转 (>= 1000字节): {long_jumps}")

def resolve_symbols(entries, binary_path):
    """解析符号信息"""
    print("\n" + "=" * 80)
    print("符号解析 (使用 addr2line)")
    print("=" * 80)
    
    for record in entries:
        print(f"\n进程: {record['comm']} (PID: {record['pid']})")
        
        # 收集所有唯一地址
        all_addrs = set()
        for branch in record['branches']:
            all_addrs.add(branch['from'])
            all_addrs.add(branch['to'])
        
        # 解析前10个分支的符号
        print("\n前10个分支的符号解析:")
        for i, branch in enumerate(record['branches'][:10]):
            from_func, from_loc = addr_to_symbol(branch['from'], binary_path)
            to_func, to_loc = addr_to_symbol(branch['to'], binary_path)
            
            print(f"  [#{i:02d}]")
            if from_func:
                print(f"    FROM: {from_func} ({from_loc})")
            else:
                print(f"    FROM: 0x{branch['from']:x}")
            
            if to_func:
                print(f"    TO:   {to_func} ({to_loc})")
            else:
                print(f"    TO:   0x{branch['to']:x}")

def create_call_graph(entries, output_file="lbr_callgraph.dot"):
    """创建调用图（Graphviz格式）"""
    from collections import defaultdict
    
    edges = defaultdict(int)
    
    for record in entries:
        for branch in record['branches']:
            edge = (branch['from'], branch['to'])
            edges[edge] += 1
    
    # 生成DOT文件
    with open(output_file, 'w') as f:
        f.write("digraph LBR_CallGraph {\n")
        f.write("  rankdir=LR;\n")
        f.write("  node [shape=box];\n\n")
        
        # 只显示最频繁的边
        top_edges = sorted(edges.items(), key=lambda x: x[1], reverse=True)[:50]
        
        for (from_addr, to_addr), count in top_edges:
            f.write(f'  "0x{from_addr:x}" -> "0x{to_addr:x}" [label="{count}"];\n')
        
        f.write("}\n")
    
    print(f"\n调用图已保存到: {output_file}")
    print(f"使用以下命令生成图片: dot -Tpng {output_file} -o lbr_callgraph.png")

def main():
    if len(sys.argv) < 2:
        print("用法: python3 parse_lbr_symbols.py <lbr_log_file> [binary_path]")
        print("\n示例:")
        print("  python3 parse_lbr_symbols.py ../log/lbr_output_*.log")
        print("  python3 parse_lbr_symbols.py ../log/lbr_output_*.log ./test_lbr")
        sys.exit(1)
    
    log_file = sys.argv[1]
    binary_path = sys.argv[2] if len(sys.argv) > 2 else None
    
    print(f"解析日志文件: {log_file}")
    
    # 解析LBR日志
    entries = parse_lbr_log(log_file)
    
    if not entries:
        print("错误: 日志中没有找到LBR数据")
        sys.exit(1)
    
    print(f"找到 {len(entries)} 条LBR记录\n")
    
    # 分析数据
    analyze_lbr_data(entries)
    
    # 如果提供了二进制文件，解析符号
    if binary_path and Path(binary_path).exists():
        resolve_symbols(entries, binary_path)
    else:
        print("\n提示: 提供二进制文件路径以进行符号解析")
        print(f"用法: {sys.argv[0]} {log_file} <binary_path>")
    
    # 生成调用图
    create_call_graph(entries)
    
    print("\n" + "=" * 80)
    print("分析完成!")
    print("=" * 80)

if __name__ == "__main__":
    main()
