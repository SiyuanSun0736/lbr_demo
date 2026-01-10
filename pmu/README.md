# L1 数据缓存性能监测工具

这个项目使用 Linux 性能事件（perf_event）API 来监测 L1 数据缓存的性能指标。

## 功能

监测以下三个性能事件：
- **L1-dcache-loads**: L1 数据缓存加载次数
- **L1-dcache-load-misses**: L1 数据缓存加载缺失次数
- **L1-dcache-stores**: L1 数据缓存存储次数

## 编译

```bash
make
```

或者使用清理选项：

```bash
make clean    # 删除编译文件
make all      # 重新编译
```

## 使用

### 监测当前进程
```bash
./l1_dcache
```

### 监测指定进程（需要知道目标进程的PID）
```bash
./l1_dcache <PID>
```

例如：
```bash
./l1_dcache 1234
```

## 要求

- Linux 内核 2.6.31 或更高版本（支持 perf_event）
- 需要适当的权限来读取性能计数器

## 程序输出

程序会实时显示三个性能计数器的数值，每秒更新一次。按 `Ctrl+C` 停止监测。

示例输出：
```
L1-dcache-loads: 1234567890  | L1-dcache-load-misses: 56789      | L1-dcache-stores: 987654321
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
