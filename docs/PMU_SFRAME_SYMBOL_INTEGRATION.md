# PMU、SFrame 与符号解析的集成架构

## 概述

本文档详细描述了 LBR Demo 项目中三个核心组件的集成关系：
- **PMU (Performance Monitoring Unit)**: 性能监控单元，收集硬件性能计数器数据
- **SFrame (Simple Frame)**: 轻量级栈帧解析格式，用于栈回溯
- **符号解析 (Symbol Resolution)**: 将内存地址映射到可读的函数名称

## 系统架构

```
┌─────────────────────────────────────────────────────────────┐
│                      用户空间应用                             │
│                                                               │
│  ┌──────────────┐  ┌──────────────┐  ┌──────────────┐       │
│  │  PMU 监控     │  │  LBR 采集     │  │  符号解析     │       │
│  │  (pmu/)      │  │  (bpf/)      │  │  (internal/) │       │
│  └──────┬───────┘  └──────┬───────┘  └──────┬───────┘       │
│         │                 │                 │                │
│         │ 性能指标          │ LBR 数据         │ 地址映射        │
│         ▼                 ▼                 ▼                │
│  ┌──────────────────────────────────────────────────────┐   │
│  │            数据分析与可视化层 (shell/)                  │   │
│  │  • analyze_lbr_hotspots.py - 热点分析                  │   │
│  │  • visualize_lbr.py - 分支可视化                       │   │
│  │  • plot_pmu_all.py - PMU 数据可视化                    │   │
│  └──────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────┘
                            │
                            │ perf_event API
                            ▼
┌─────────────────────────────────────────────────────────────┐
│                      Linux 内核                               │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐          │
│  │ PMU 硬件     │  │ LBR 寄存器   │  │ 进程内存     │          │
│  └─────────────┘  └─────────────┘  └─────────────┘          │
└─────────────────────────────────────────────────────────────┘
```

## 组件详解

### 1. PMU (Performance Monitoring Unit)

#### 1.1 功能概述

PMU 组件负责收集 CPU 硬件性能计数器数据，提供对程序执行效率的深入洞察。

**监控指标:**
- **TLB 性能**: dTLB/iTLB loads, stores, misses
- **Cache 性能**: L1 数据/指令缓存, LLC (Last Level Cache)
- **内存访问**: 已退役的 load/store 指令
- **L1D Pending Miss**: replacement, fb_full, pending 事件

#### 1.2 实现机制

位置: [`pmu/pmu_monitor_all.c`](pmu/pmu_monitor_all.c)

**核心数据结构:**
```c
typedef struct {
    int fd;              // perf_event 文件描述符
    const char *name;    // 计数器名称
    uint64_t count;      // 计数值
    int enabled;         // 是否启用
} perf_counter_t;
```

**监控流程:**
```c
1. 打开 perf_event: perf_event_open() 系统调用
2. 配置计数器: struct perf_event_attr
3. 周期性读取: ioctl(fd, PERF_EVENT_IOC_RESET/REFRESH)
4. 记录数据: 写入 CSV 格式日志
```

#### 1.3 数据输出

输出文件: `pmu/pmu_monitor_all.log` (CSV 格式)

**格式示例:**
```csv
timestamp,dTLB-loads,dTLB-load-misses,L1-dcache-loads,L1-dcache-load-misses,...
1.000,1234567,123,9876543,987,...
2.000,1235000,125,9877000,990,...
```

### 2. SFrame (Simple Frame)

#### 2.1 功能概述

SFrame 是一种轻量级的栈帧描述格式，比 DWARF 更紧凑，专门设计用于高效的栈回溯操作。

**核心优势:**
- **紧凑**: 比 DWARF 格式占用更少的空间
- **快速**: 解析速度更快，适合实时分析
- **简单**: 数据结构简单，易于实现

#### 2.2 数据结构

位置: [`internal/sframe_resolver.go`](internal/sframe_resolver.go)

**SFrame 头部:**
```go
type SFrameHeader struct {
    Magic         uint16  // 魔数: 0xdee2
    Version       uint8   // 版本号
    Flags         uint8   // 标志位
    ABI           uint8   // ABI/架构标识符
    FixedFPOffset int8    // CFA fixed FP offset
    FixedRAOffset int8    // CFA fixed RA offset
    AuxHdrLen     uint8   // 辅助头长度
    NumFDEs       uint32  // FDE 数量
    NumFREs       uint32  // FRE 数量
    FRELen        uint32  // FRE 子节长度
    FDEOff        uint32  // FDE 子节偏移
    FREOff        uint32  // FRE 子节偏移
}
```

**SFrame 函数信息 (FDE):**
```go
type SFrameFunction struct {
    StartAddr   int32   // 函数起始地址
    Size        uint32  // 函数大小
    StartFREOff uint32  // 第一个 FRE 的偏移
    NumFREs     uint32  // FRE 数量
    FuncInfo    uint8   // FDE info word
    RepSize     uint8   // 重复块大小
    Padding     uint16  // 填充
}
```

**Frame Row Entry (FRE):**
```go
type SFrameFDE struct {
    StartOffset uint32  // 相对函数起始的偏移
    FDEInfo     uint8   // FDE 信息字节
    FREInfo     uint8   // FRE Info Word (CFA base reg, offset size等)
    RepSize     uint32  // 重复次数/大小
    CFAOffset   int32   // CFA 偏移量 (从 SP/BP 计算)
    FPOffset    int32   // 帧指针保存位置偏移
    RAOffset    int32   // 返回地址保存位置偏移
}
```

#### 2.3 栈回溯流程

```
1. 读取 ELF 文件的 .sframe 节
2. 解析 SFrameHeader
3. 根据地址查找对应的 SFrameFunction
4. 使用 FRE 信息计算栈帧:
   - CFA (Canonical Frame Address) = SP/FP + offset
   - 返回地址 = [CFA + RAOffset]
   - 帧指针 = [CFA + FPOffset]
5. 重复步骤 3-4 直到栈底
```

### 3. 符号解析 (Symbol Resolution)

#### 3.1 解析器类型

项目支持三种符号解析方式:

| 解析器 | 优势 | 劣势 | 使用场景 |
|--------|------|------|----------|
| **addr2line** | 精确、包含源码位置 | 需要调试符号、较慢 | 开发调试 |
| **DWARF** | 详细、标准格式 | 文件大、解析开销高 | 完整调试信息 |
| **SFrame** | 快速、轻量级 | 信息有限 | 生产环境、实时分析 |

#### 3.2 内核符号解析

位置: [`internal/disasm.go`](internal/disasm.go)

**符号表加载:**
```go
func LoadKallsyms() (*Symbols, error) {
    // 从 /proc/kallsyms 读取内核符号
    // 格式: <地址> <类型> <符号名>
    // 示例: ffffffff81234567 T __x64_sys_execve
}
```

**符号查找算法:**
```go
func (s *Symbols) Find(addr uint64) (string, uint64, bool) {
    // 二分查找: 找到 <= addr 的最大符号
    // 返回: (符号名, 偏移量, 是否找到)
    // 示例: Find(0xffffffff8123456a) -> ("__x64_sys_execve", 0x3, true)
}
```

#### 3.3 用户空间符号解析

位置: [`internal/usersym.go`](internal/usersym.go)

**解析流程:**
```
1. 读取 /proc/<pid>/maps 获取内存映射
2. 确定地址所属的可执行文件/库
3. 根据配置选择解析器:
   - addr2line: 调用外部工具
   - DWARF: 解析 .debug_info 节
   - SFrame: 解析 .sframe 节
4. 返回: 函数名 + 偏移量 + 源文件位置(如果可用)
```

## 三者集成关系

### 工作流程

```
┌─────────────────────────────────────────────────────────────┐
│ 1. PMU 数据收集                                               │
│    • 周期性采样性能计数器                                      │
│    • 记录 TLB miss, Cache miss, Memory access 等             │
│    • 输出: pmu_monitor_all.log                               │
└───────────────────────┬─────────────────────────────────────┘
                        │
                        │ 时间关联
                        ▼
┌─────────────────────────────────────────────────────────────┐
│ 2. LBR 数据采集                                               │
│    • eBPF 程序捕获 LBR (Last Branch Record)                  │
│    • 记录分支跳转: from_addr -> to_addr                       │
│    • 输出: lbr_output_*.log                                  │
└───────────────────────┬─────────────────────────────────────┘
                        │
                        │ 地址输入
                        ▼
┌─────────────────────────────────────────────────────────────┐
│ 3. 符号解析                                                   │
│    ┌─────────────┐  ┌─────────────┐  ┌─────────────┐        │
│    │  kallsyms   │  │   SFrame    │  │   DWARF     │        │
│    │  (内核)     │  │  (用户态)    │  │  (用户态)    │        │
│    └─────┬───────┘  └─────┬───────┘  └─────┬───────┘        │
│          │                │                │                │
│          └────────────────┴────────────────┘                │
│                           │                                 │
│                  地址 -> 函数名 + 偏移                        │
└───────────────────────────┬─────────────────────────────────┘
                            │
                            │ 可读符号
                            ▼
┌─────────────────────────────────────────────────────────────┐
│ 4. 数据关联与分析                                             │
│    • 将 PMU 指标与 LBR 分支关联                               │
│    • 识别性能热点函数                                         │
│    • 分析函数调用路径                                         │
│    • 输出: 可视化图表 + 分析报告                              │
└─────────────────────────────────────────────────────────────┘
```

### 关键集成点

#### 4.1 时间同步

PMU 和 LBR 数据通过时间戳进行关联:

```go
// cmd/main.go
timestamp := time.Now()
log.Printf("[%s] LBR entry: %s+%#x -> %s+%#x",
    timestamp.Format("15:04:05.000"),
    fromName, fromOffset, toName, toOffset)
```

#### 4.2 地址解析统一接口

[`cmd/main.go`](cmd/main.go) 中的 `processLbrData` 函数统一处理符号解析:

```go
func processLbrData(lbrMap *ebpf.Map, syms *lbr.Symbols, 
                    pidResolvers map[int]*lbr.SFrameResolver) {
    // 读取 LBR 数据
    entry := &data.Entries[i]
    
    // 判断地址空间
    isKernel := entry.From > 0xffff800000000000
    
    if isKernel {
        // 使用 kallsyms 解析内核地址
        fromName, fromOffset, _ = syms.Find(entry.From)
    } else {
        // 使用 SFrame/DWARF 解析用户地址
        if *useSFrame {
            resolver := getOrCreateResolver(pid)
            fromName, fromOffset = resolver.Resolve(entry.From)
        }
    }
}
```

#### 4.3 性能热点识别

[`shell/analyze_lbr_hotspots.py`](shell/analyze_lbr_hotspots.py) 结合 PMU 和 LBR 数据:

```python
def correlate_pmu_lbr(pmu_log, lbr_log):
    """关联 PMU 性能指标与 LBR 热点"""
    
    # 1. 解析 PMU 数据 - 找出性能问题时间段
    pmu_data = parse_pmu_log(pmu_log)
    high_miss_periods = find_high_miss_rate(pmu_data)
    
    # 2. 解析 LBR 数据 - 找出热点函数
    branches = parse_lbr_log(lbr_log)
    hotspots = analyze_hotspots(branches)
    
    # 3. 关联分析
    for period in high_miss_periods:
        # 找出该时间段的热点函数
        functions = get_functions_in_period(hotspots, period)
        print(f"高 cache miss 时段 {period}: 热点函数 {functions}")
```

## 使用场景示例

### 场景 1: 识别 Cache Miss 热点函数

**问题**: 应用程序 L1 cache miss 率高，需要找出原因

**解决步骤:**

```bash
# 1. 同时启动 PMU 监控和 LBR 采集
cd pmu
./pmu_monitor_all <target_pid> &
cd ..
sudo ./lbr-demo -pid <target_pid> -sframe -resolve

# 2. 等待采集足够数据 (如 30 秒)
sleep 30

# 3. 分析 PMU 数据，识别高 miss 率时段
cd shell
./plot_pmu_all.py ../pmu/pmu_monitor_all.log plots/

# 4. 分析 LBR 热点
./analyze_lbr_hotspots.py ../log/lbr_output_*.log

# 5. 可视化分支跳转
./visualize_lbr.py ../log/lbr_output_*.log
```

**输出结果:**
```
高 L1 cache miss 时段: 10:23:45 - 10:23:50
热点函数:
  - matrix_multiply+0x123: 出现 1234 次
  - data_process+0x456: 出现 987 次

分支模式:
  - 循环: matrix_multiply+0x150 <- matrix_multiply+0x180 (1200次)
  - 原因: 数据访问模式导致频繁 cache miss
```

### 场景 2: 优化函数调用路径

**问题**: 应用程序 iTLB miss 率高，怀疑代码布局问题

**分析流程:**

```python
# shell/analyze_lbr_hotspots.py
def analyze_itlb_correlation(pmu_log, lbr_log):
    """分析 iTLB miss 与代码跳转的关系"""
    
    # 1. 找出 iTLB miss 高的时段
    itlb_misses = extract_metric(pmu_log, "iTLB-load-misses")
    
    # 2. 分析该时段的跳转距离
    branches = parse_lbr_log(lbr_log)
    jump_distances = [abs(to - from) for from, to in branches]
    
    # 3. 识别长跳转
    long_jumps = [(f, t) for f, t in branches if abs(t-f) > 4096]
    
    print(f"长跳转 (>4KB): {len(long_jumps)} 次")
    print(f"可能导致 iTLB miss 增加")
```

### 场景 3: 实时性能监控

**应用**: 生产环境中监控关键服务的性能

**配置:**
```bash
# 使用 SFrame 进行轻量级符号解析
./lbr-demo -pid <service_pid> -sframe -logdir /var/log/perf/

# 周期性分析
*/5 * * * * /path/to/analyze_latest_logs.sh
```

**优势:**
- SFrame 解析速度快，开销低
- 不需要完整的调试符号
- 适合长期运行的生产环境

## 性能优化建议

### 1. 符号解析选择

```go
// 根据场景选择合适的解析器
if production {
    // 生产环境: 使用 SFrame
    useSFrame = true
    useDwarf = false
} else if debugging {
    // 调试环境: 使用 DWARF 或 addr2line
    useDwarf = true
    useAddr2line = true
}
```

### 2. PMU 采样频率

```c
// pmu/pmu_monitor_all.c
#define SAMPLE_INTERVAL_MS 1000  // 降低采样频率减少开销

// 根据场景调整
// 高精度分析: 100ms
// 一般监控: 1000ms
// 长期监控: 5000ms
```

### 3. LBR 缓冲区大小

```go
// internal/maps.go
const LBR_BUFFER_SIZE = 32  // 默认 32 条记录

// 调整建议:
// 深度分析: 64 条
// 一般使用: 32 条
// 低开销: 16 条
```

## 数据格式规范

### PMU 日志格式

```csv
timestamp,dTLB-loads,dTLB-load-misses,dTLB-stores,dTLB-store-misses,...
1.000,1234567,123,987654,98,...
```

### LBR 日志格式

```
[2024-02-13 10:23:45.123] PID: 1234, COMM: myapp
[#31] do_syscall_64+0x1a -> __x64_sys_read+0x0
[#30] __x64_sys_read+0x23 -> vfs_read+0x0
[#29] vfs_read+0x142 -> __vfs_read+0x1d
```

### 符号解析输出格式

```
格式: <function_name>+<offset>
示例: matrix_multiply+0x123
说明: 在 matrix_multiply 函数内偏移 0x123 字节处
```

## 故障排查

### 问题 1: 符号解析失败

**症状**: 输出显示原始地址而非函数名

**检查清单:**
```bash
# 1. 验证 kallsyms 可读性
cat /proc/kallsyms | head

# 2. 检查目标进程的内存映射
cat /proc/<pid>/maps

# 3. 验证 ELF 文件包含符号
readelf -s /path/to/binary | grep FUNC

# 4. 对于 SFrame,检查 .sframe 节
readelf -S /path/to/binary | grep sframe
```

### 问题 2: PMU 数据异常

**症状**: 性能计数器读取失败或数据为 0

**解决方案:**
```bash
# 1. 检查权限
echo -1 | sudo tee /proc/sys/kernel/perf_event_paranoid

# 2. 验证硬件支持
cat /proc/cpuinfo | grep -E "pmu|perf"

# 3. 检查是否被其他工具占用
ps aux | grep perf
```

### 问题 3: LBR 数据为空

**症状**: LBR map 中没有数据

**调试步骤:**
```bash
# 1. 验证 LBR 硬件支持
cat /sys/bus/event_source/devices/cpu/caps/branches

# 2. 检查 eBPF 程序加载
bpftool prog list

# 3. 查看 eBPF 日志
sudo cat /sys/kernel/debug/tracing/trace_pipe
```

## 参考资料

### 相关文档
- [SYMBOL_RESOLUTION.md](SYMBOL_RESOLUTION.md) - 符号解析详细文档
- [STACK_UNWINDING.md](STACK_UNWINDING.md) - 栈回溯文档
- [sframe.md](sframe.md) - SFrame 格式文档
- [pmu/README.md](../pmu/README.md) - PMU 使用指南

### 相关代码
- [`internal/sframe_resolver.go`](../internal/sframe_resolver.go) - SFrame 解析实现
- [`internal/dwarf_resolver.go`](../internal/dwarf_resolver.go) - DWARF 解析实现
- [`internal/disasm.go`](../internal/disasm.go) - 符号表管理
- [`cmd/main.go`](../cmd/main.go) - 主程序集成逻辑
- [`pmu/pmu_monitor_all.c`](../pmu/pmu_monitor_all.c) - PMU 监控实现

### 外部资源
- Linux perf_event API 文档
- Intel LBR 技术文档
- SFrame 格式规范
- DWARF 调试格式标准

## 总结

PMU、SFrame 和符号解析三者形成了一个完整的性能分析生态系统:

1. **PMU** 提供硬件级别的性能指标，告诉我们"发生了什么问题"
2. **LBR** 记录程序执行路径，告诉我们"代码如何运行"
3. **符号解析** 将机器地址转换为人类可读的函数名，告诉我们"问题在哪里"
4. **SFrame** 提供了轻量级的栈回溯能力，在性能和功能之间取得平衡

通过这三者的协同工作，开发者可以:
- 精确定位性能瓶颈
- 理解程序执行流程
- 优化代码布局和数据访问模式
- 在生产环境中进行低开销的性能监控
