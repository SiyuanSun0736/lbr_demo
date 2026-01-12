# LBR 数据解析工具使用指南

## 概述

本工具集提供了三种方式来解析和分析 LBR (Last Branch Record) 数据：

1. **符号解析** - 将地址映射到函数名和源代码位置
2. **热点分析** - 识别最频繁执行的代码路径
3. **可视化** - 生成图表帮助理解分支行为

## 工具列表

### 1. parse_lbr_symbols.py - 符号解析工具

将 LBR 中的内存地址转换为可读的函数名和源代码位置。

**功能:**
- 解析 LBR 日志文件
- 使用 `addr2line` 将地址映射到源代码
- 分析跳转统计信息
- 生成调用图 (Graphviz 格式)

**用法:**
```bash
# 基本用法（不进行符号解析）
python3 parse_lbr_symbols.py ../log/lbr_output_20260111_175111.log

# 带符号解析（需要二进制文件）
python3 parse_lbr_symbols.py ../log/lbr_output_20260111_175111.log ../test/test_lbr

# 查看生成的调用图
dot -Tpng lbr_callgraph.dot -o lbr_callgraph.png
```

**输出:**
- 跳转频率统计
- 跳转距离分析
- 符号解析结果（如果提供二进制文件）
- `lbr_callgraph.dot` - Graphviz 格式的调用图

**示例输出:**
```
进程: test_lbr (PID: 112060)
分支记录数: 32

最频繁的跳转:
  0x7fd44945b547 -> 0x7fd44945b590 (出现 5 次)
  0x7fd44945b598 -> 0x7fd4494643cc (出现 3 次)

跳转距离统计:
  平均距离: 12543 字节
  最小距离: 73 字节
  最大距离: 123456 字节

跳转类型分布:
  短跳转 (< 100字节): 8
  中等跳转 (100-1000字节): 12
  长跳转 (>= 1000字节): 12
```

---

### 2. analyze_lbr_hotspots.py - 热点分析工具

识别代码中的热点位置和常见执行模式。

**功能:**
- 识别最频繁访问的代码地址
- 检测循环模式（后向跳转）
- 分析函数调用模式
- 构建控制流图
- 计算代码覆盖范围
- 生成热力图数据

**用法:**
```bash
python3 analyze_lbr_hotspots.py ../log/lbr_output_20260111_175111.log
```

**输出:**
- 热点地址列表
- 分支模式统计
- 循环检测结果
- 函数调用分析
- 控制流信息
- 代码覆盖统计
- `lbr_heatmap.csv` - 热力图数据文件

**示例输出:**
```
热点地址分析
================================================================================

Top 20 最活跃的地址:
  0x00007fd44945b547: 出现   25 次 ( 3.91%)
  0x00007fd44945b590: 出现   20 次 ( 3.12%)
  0x00007fd44945b598: 出现   18 次 ( 2.81%)

循环检测
================================================================================

检测到 45 个后向跳转 (可能的循环)

最频繁的循环跳转:
  0x7fd44945b320 <- 0x7fd44945b547
    循环体大小: ~551 字节, 执行次数: 15

代码覆盖分析
================================================================================

访问的地址范围:
  最小地址: 0x00007fd44945b320
  最大地址: 0x00007fd44956d076
  地址范围: 122,198 字节 (119.33 KB)
  唯一地址: 64
  代码密度: 0.0524%
```

---

### 3. visualize_lbr.py - 可视化工具

生成各种图表来可视化 LBR 数据。

**功能:**
- 分支频率柱状图
- 跳转距离分布图
- 跳转方向饼图
- 热点地址图
- 地址热力图
- 控制流图

**用法:**
```bash
# 需要先安装 matplotlib
pip3 install matplotlib numpy

# 运行可视化
python3 visualize_lbr.py ../log/lbr_output_20260111_175111.log
```

**输出图表:**
- `lbr_branch_freq.png` - Top 30 最频繁的分支跳转
- `lbr_jump_distance.png` - 跳转距离分布（直方图和对数图）
- `lbr_jump_direction.png` - 向前/向后跳转比例
- `lbr_hotspot_addrs.png` - Top 20 热点地址
- `lbr_address_heatmap.png` - 地址间跳转热力图
- `lbr_cfg.png` - 简化的控制流图

---

## 典型分析流程

### 快速分析
```bash
# 1. 运行测试并收集 LBR 数据
cd test
sudo ./run_lbr_test.sh

# 2. 查看最新日志
ls -lt ../log/lbr_output_*.log | head -1

# 3. 快速热点分析
cd ../shell
python3 analyze_lbr_hotspots.py ../log/lbr_output_20260111_175111.log
```

### 深度分析
```bash
# 1. 符号解析
python3 parse_lbr_symbols.py ../log/lbr_output_*.log ../test/test_lbr

# 2. 热点分析
python3 analyze_lbr_hotspots.py ../log/lbr_output_*.log

# 3. 生成可视化图表
python3 visualize_lbr.py ../log/lbr_output_*.log

# 4. 生成调用图
dot -Tpng lbr_callgraph.dot -o lbr_callgraph.png

# 5. 查看所有结果
ls -lh *.png *.csv *.dot
```

---

## 解析数据的方法

### 方法 1: 地址到符号映射

使用 `addr2line` 工具将地址转换为源代码位置：

```bash
# 手动查询单个地址
addr2line -e test/test_lbr -f -C 0x7fd44945b547

# 批量查询
cat addresses.txt | while read addr; do
    addr2line -e test/test_lbr -f -C $addr
done
```

### 方法 2: 使用 objdump 反汇编

查看特定地址附近的汇编代码：

```bash
# 反汇编整个程序
objdump -d test/test_lbr > test_lbr.asm

# 查找特定地址
objdump -d test/test_lbr | grep -A 10 -B 5 "547:"
```

### 方法 3: 使用 perf 工具

如果有 perf 数据，可以结合分析：

```bash
# 查看 perf 报告
perf report -i perf.data

# 查看注释的源代码
perf annotate -i perf.data
```

### 方法 4: 内存映射分析

查看进程的内存布局：

```bash
# 查看进程内存映射（进程运行时）
cat /proc/<PID>/maps

# 找出地址所属的库或段
grep "7fd449" /proc/<PID>/maps
```

---

## 常见问题

### Q1: 为什么所有地址都显示为 `[user]`？

**原因:** 这些是用户态地址，不在内核符号表中。

**解决方法:**
1. 提供二进制文件给 `parse_lbr_symbols.py`
2. 确保二进制文件带调试符号（编译时使用 `-g` 选项）
3. 使用 `addr2line` 手动解析

### Q2: addr2line 返回 `??:0`

**原因:** 二进制文件缺少调试信息。

**解决方法:**
```bash
# 重新编译带调试符号
gcc -g -O0 -o test_lbr test_lbr.c

# 或使用现有符号
objdump -t test_lbr | grep <地址>
```

### Q3: 如何解析动态链接库中的地址？

**方法:**
1. 查看 `/proc/<PID>/maps` 找出库的基址
2. 计算偏移: `offset = 地址 - 库基址`
3. 使用 addr2line 解析: `addr2line -e /lib/libc.so.6 <offset>`

### Q4: 生成的图表太大或太小

**解决方法:** 修改脚本中的 `figsize` 参数或 `top_branches` 数量。

---

## 性能提示

1. **大日志文件:** 如果日志文件很大，可以先过滤：
   ```bash
   grep "LBR Stack" -A 33 lbr_output.log > filtered.log
   ```

2. **批量处理:** 分析多个日志文件：
   ```bash
   for log in ../log/lbr_output_*.log; do
       python3 analyze_lbr_hotspots.py "$log" > "${log%.log}_analysis.txt"
   done
   ```

3. **并行处理:** 使用 GNU parallel：
   ```bash
   ls ../log/lbr_output_*.log | parallel python3 analyze_lbr_hotspots.py {}
   ```

---

## 进阶用法

### 自定义分析脚本

可以基于提供的脚本进行扩展，例如：

```python
# 自定义分析示例
import re
from collections import Counter

def custom_analysis(log_file):
    # 提取特定模式
    pattern = re.compile(r'0x7fd449[0-9a-f]{6}')
    matches = []
    
    with open(log_file) as f:
        for line in f:
            matches.extend(pattern.findall(line))
    
    # 统计
    counter = Counter(matches)
    for addr, count in counter.most_common(10):
        print(f"{addr}: {count}")

if __name__ == "__main__":
    custom_analysis("../log/lbr_output_*.log")
```

### 与其他工具集成

```bash
# 导出为 JSON
python3 -c "
import json
import sys
from analyze_lbr_hotspots import parse_lbr_log

branches = parse_lbr_log(sys.argv[1])
data = [{'from': f, 'to': t} for f, t in branches]
print(json.dumps(data, indent=2))
" ../log/lbr_output_*.log > lbr_data.json

# 使用 jq 查询
cat lbr_data.json | jq '.[] | select(.from > .to)'
```

---

## 参考资料

- [Intel LBR 文档](https://www.intel.com/content/www/us/en/developer/articles/technical/last-branch-records.html)
- [eBPF 文档](https://ebpf.io/)
- [perf 工具](https://perf.wiki.kernel.org/)
- [Graphviz](https://graphviz.org/)

---

## 更新日志

- 2026-01-11: 创建初始版本
  - 添加符号解析工具
  - 添加热点分析工具
  - 添加可视化工具
