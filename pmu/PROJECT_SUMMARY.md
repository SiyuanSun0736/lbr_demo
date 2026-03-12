# 项目总结

## 项目概述
本项目是一套基于 Linux perf_event 的 PMU（Performance Monitoring Unit）时序采集与分析工具。更多使用说明见项目 `README`：[README.md](README.md)

## 主要功能
- 采集与记录：多工具集（如 `pmu_timeseries`, `pmu_monitor_all` 等），支持系统级或指定 PID 的采样。
- 时序处理：输出 CSV 格式时间序列，包含每个采样点的 elapsed_ms 与各计数器的 time_enabled/time_running。
- 对比分析：对比工作负载与基线并输出对比文本/统计结果。
- 可视化：配套脚本生成 PNG 图表，便于观察指标随时间变化。

## 目录与关键文件
- 核心采样程序：[`pmu_timeseries.c`](pmu_timeseries.c#L1-L40)
- 综合监测：[`pmu_monitor_all.c`（或可执行 `pmu_monitor_all`）](README.md)
- 绘图脚本：位于 `shell/` 子目录（参见 `README.md` 中的可视化章节）
- 采样日志与对比结果：位于 `log/`，示例：[`log/pmu_timeseries_test_pmu_workload.csv`](log/pmu_timeseries_test_pmu_workload.csv#L1-L3)

## 实现细节（摘录）
- 采样输出为 CSV，首列为时间轴 `elapsed_ms`，随后为各计数器的值以及对应的 `*_time_enabled` 和 `*_time_running` 字段，方便检测 kernel 的 multiplex scaling。例如日志头部：

```csv
elapsed_ms,timestamp,dTLB-loads, ... ,mem_inst_retired.any,mem_inst_retired.any_time_enabled,mem_inst_retired.any_time_running
```

见示例文件（首行是 header，随后为每秒采样的数据）：[log/pmu_timeseries_test_pmu_workload.csv](log/pmu_timeseries_test_pmu_workload.csv#L1-L3)

- 程序在启动时会创建 `log/pmu_timeseries_YYYYMMDD_HHMMSS.csv` 并创建符号链接 `log/pmu_timeseries.csv` 指向最新文件；采样间隔可通过 `-i` 指定（默认 1000 ms）。实现片段见：[`pmu_timeseries.c`](pmu_timeseries.c#L1-L40)

## 使用示例（快速上手）
1. 编译：

```bash
make
```

2. 运行系统级采样（1s 间隔）：

```bash
sudo ./pmu_timeseries
```

3. 指定进程和 100 ms 间隔：

```bash
sudo ./pmu_timeseries 12345 -i 100
```

更多命令与绘图示例见：[README.md](README.md)

## 已有示例与结果说明
- 示例日志 `log/pmu_timeseries_test_pmu_workload.csv` 包含大量每秒采样点，可用于绘制时序曲线与计算速率（通过 elapsed_ms 差值归一化）。文件示例行见：[log/pmu_timeseries_test_pmu_workload.csv](log/pmu_timeseries_test_pmu_workload.csv#L2-L3)
- README 中包含综合监测输出示例（summary 形式），便于快速比对单次运行产生的 CSV 与文本摘要。[README.md](README.md)

## 结论与后续改进建议
- 已建立端到端采集→处理→可视化流程，适合做缓存/TLB/内存指令类事件的时间序列分析。
- 建议：
  - 增加统一配置文件（YAML/JSON）以统一管理采样与绘图参数；
  - 在处理脚本中加入对 `*_time_enabled`/`*_time_running` 的自动缩放与警告；
  - 提供一个示例报告生成脚本，自动从 `log/` 中读取最新数据并产出 PDF/HTML 报告。
- 增加算法,进行这几个指标的关联
- 实现与page_fault等的关联性
- 不只读取MSR信息,可以读取更多信息
- 捕获指令到指令间的MSR信息(使用perf_event和uprobe的尾调用)
- 考虑到打印的花费(进行程序优化)