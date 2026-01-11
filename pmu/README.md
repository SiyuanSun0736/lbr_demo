# PMU 性能监测工具集

这个项目使用 Linux 性能事件（perf_event）API 来监测各种 CPU 性能指标。

## 功能

### 综合监测工具 (pmu_monitor_all)
**推荐使用** - 一次性监测所有性能指标：
- **TLB 事件**: dTLB/iTLB loads/stores 及 misses
- **L1 Cache**: 数据缓存和指令缓存的 loads/stores/misses
- **L1D Pending Miss**: replacement, fb_full, pending 事件
- **LLC**: Last Level Cache loads/stores/misses
- **Cache 通用事件**: cache-references, cache-misses
- **内存指令**: 已退役的 load/store 指令

### 单独监测工具
- **l1_dcache**: L1 数据缓存监测
- **l1_icache**: L1 指令缓存监测
- **l1d_pend_miss**: L1D pending miss 事件监测
- **llc_cache**: Last Level Cache 监测
- **dtlb**: 数据 TLB 监测
- **itlb**: 指令 TLB 监测
- **mem_inst**: 内存指令退役事件监测

## 编译

```bash
make              # 编译所有工具
make pmu_monitor_all  # 只编译综合监测工具
make clean        # 删除编译文件
```

## 使用

### 综合监测（推荐）

监测当前进程的所有性能指标：
```bash
./pmu_monitor_all
```

监测指定进程：
```bash
./pmu_monitor_all <PID>
```

输出文件：`pmu_monitor_all.log` - 包含所有性能指标的CSV格式日志

### 单独工具使用

#### 监测当前进程
```bash
./l1_dcache      # L1 数据缓存
./dtlb           # 数据 TLB
./itlb           # 指令 TLB
# 等等...
```

### 监测指定进程（需要知道目标进程的PID）
```bash
./pmu_monitor_all <PID>
./l1_dcache <PID>
# 等等...
```

例如：
```bash
./pmu_monitor_all 1234
./l1_dcache 1234
```

## 数据可视化

使用 Python 脚本生成性能分析图表：

### 综合性能分析
```bash
cd ../shell
./plot_pmu_all.py                    # 使用默认路径
./plot_pmu_all.py ../pmu/pmu_monitor_all.log plots/  # 指定路径
```

生成的图表：
- `overview.png` - 所有指标总览
- `tlb_metrics.png` - TLB 性能指标
- `l1_cache_metrics.png` - L1 缓存性能指标
- `l1d_pending_miss.png` - L1D pending miss 事件
- `llc_metrics.png` - LLC 性能指标
- `memory_instructions.png` - 内存指令统计

### 单独指标分析
```bash
cd ../shell
./plot_dtlb.py        # dTLB 分析
./plot_itlb.py        # iTLB 分析
./plot_l1_dcache.py   # L1 数据缓存分析
./plot_l1_icache.py   # L1 指令缓存分析
./plot_llc.py         # LLC 分析
./plot_mem_inst.py    # 内存指令分析
```

## 要求

- Linux 内核 2.6.31 或更高版本（支持 perf_event）
- 需要适当的权限来读取性能计数器
- Python 3.x (用于数据可视化)
- Python 依赖: pandas, matplotlib

## 程序输出

程序会实时显示性能计数器的数值，每秒更新一次。按 `Ctrl+C` 停止监测。

### 综合监测输出示例
```
=== PMU Summary (2026-01-10 21:48:38) ===
TLB:
  dTLB-loads                    :      4883105
  dTLB-load-misses              :        28603
  iTLB-loads                    :        12112
  iTLB-load-misses              :        17476
  
Cache:
  L1-dcache-loads               :      1848449
  L1-dcache-load-misses         :       170248
  LLC-loads                     :        92468
  LLC-load-misses               :        21887
  
Memory Instructions:
  mem_inst_retired.all_loads    :      3752240
  mem_inst_retired.all_stores   :      1994352
```

## 注意事项

1. 某些系统可能需要 `sudo` 权限来运行此程序
2. 不同的 CPU 架构可能对事件的支持情况不同
3. 性能监测可能会对系统性能产生轻微影响

## 事件配置说明

程序配置了以下性能事件参数：

- `PERF_TYPE_HW_CACHE`: 使用硬件缓存事件
- `PERF_COUNT_HW_CACHE_L1D`: L1 数据缓存
- `PERF_COUNT_HW_CACHE_OP_READ`: 读操作
- `PERF_COUNT_HW_CACHE_OP_WRITE`: 写操作
- `PERF_COUNT_HW_CACHE_RESULT_ACCESS`: 访问命中
- `PERF_COUNT_HW_CACHE_RESULT_MISS`: 访问缺失

## 故障排除

### 权限错误
如果遇到权限错误，尝试使用 `sudo`:
```bash
sudo ./l1_dcache
```

### 不支持的事件
某些系统可能不支持某个特定事件。如果 `perf_event_open` 失败，检查您的 CPU 是否支持该事件。
