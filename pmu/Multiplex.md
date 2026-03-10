## PMU 多路复用（Multiplexing）问题说明

### 背景：硬件计数器资源有限

现代 CPU 每个核心的物理 PMU 硬件计数器数量有限（通常 4~8 个）。当你想同时监控的事件数量超过可用计数器数量时，Linux 内核会对这些事件进行**时间切片（time-multiplexing）**：每隔一段时间轮换哪些事件占用物理计数器。

```
时间轴:  [===事件A===][===事件B===][===事件A===][===事件B===]
         ↑ 每个事件只在部分时间窗口内实际计数
```

### 关键概念：`time_enabled` vs `time_running`

| 字段 | 含义 |
|------|------|
| `time_enabled` | 该事件被"启用"（监控窗口）的总时间 |
| `time_running` | 该事件实际占用物理计数器的时间 |

若发生多路复用，则 `time_running < time_enabled`。

### 当前代码的问题

读取计数器时（pmu_timeseries.c）：

```c
read(counters[i].fd, &counters[i].count, sizeof(uint64_t));
```

这只读取了**原始计数值**，没有获取 `time_enabled`/`time_running` 信息，因为 `pe.read_format` 未设置：

```c
// 当前：什么都没设置，只能读到裸计数值
struct perf_event_attr pe;
memset(&pe, 0, sizeof(pe));
// 缺少：pe.read_format = PERF_FORMAT_TOTAL_TIME_ENABLED | PERF_FORMAT_TOTAL_TIME_RUNNING;
```

### 后果

假设在同一个 1000ms 采样窗口内：
- 事件 A（dTLB-loads）：`time_running = 600ms`，原始计数 = 6000
- 事件 B（LLC-loads）：`time_running = 300ms`，原始计数 = 1500

直接比较 6000 vs 1500 会得出错误结论，因为两者的**采样基准时间不同**。  
正确做法是缩放（scale）：
$$\text{scaled\_count} = \text{raw\_count} \times \frac{\text{time\_enabled}}{\text{time\_running}}$$

- 事件 A 缩放后：$6000 \times \frac{1000}{600} = 10000$
- 事件 B 缩放后：$1500 \times \frac{1000}{300} = 5000$

### 修复方案

需要做两处改动：

**1. 设置 `read_format`**（每次 `RESET_PE()` 后，或在初始化时统一设置）：
```c
pe.read_format = PERF_FORMAT_TOTAL_TIME_ENABLED | PERF_FORMAT_TOTAL_TIME_RUNNING;
```

**2. 用结构体读取，并做缩放**：
```c
struct {
    uint64_t value;
    uint64_t time_enabled;
    uint64_t time_running;
} rd;

read(counters[i].fd, &rd, sizeof(rd));

uint64_t scaled = rd.value;
if (rd.time_running > 0 && rd.time_running < rd.time_enabled)
    scaled = (uint64_t)((double)rd.value * rd.time_enabled / rd.time_running);
counters[i].count = scaled;
```

---

你现在的代码中监控了 24 个事件，而多数服务器只有 4~8 个通用计数器，**几乎必然发生多路复用**，因此这个问题在你的场景下影响尤为明显。是否需要我直接将修复应用到代码中？