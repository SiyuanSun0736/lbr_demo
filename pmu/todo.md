我将更新任务进度，然后给出基于图像的简洁分析与建议。

Starting: *指出潜在问题和边界情况* (3/6)

下面是对你发来的这些图（PMU timeseries 输出）的分析与建议，重点指出异常、可能原因和可验证的检查项。

主要发现（要点）
- 数值量级不一致：  
  - `l1d.replacement`、`LLC-loads`、`dTLB-loads` 等为 1e7–1e8 量级；而 `l1d_pend_miss.pending` 显示 ~1e9 级别。大范围差异提示不同事件的语义/计数粒度不同，或某些事件为原始/架构特定计数（raw events）。
- 高波动与突发尖峰：多处时间点出现显著峰值（例如 20:27:44 附近的多次尖峰），说明短时间内负载或某些微架构行为突增（热点、缓存竞争、未对齐访问、频繁上下文切换等）。
- 比率计算异常/超出常理：某些 miss rate 或比率图显示 >100%（例如 LLC store miss rate 的峰值 ~700%），或 iTLB miss rate常处于很高百分比（60%+）。这通常表明分母（hits/参考事件）过小、事件配对不当，或单位/采样间隔未统一。
- 零值或缺失事件：部分子图（例如某些 store 计数）在若干时刻为 0，可能是该事件在当前 CPU/内核不可用，或在采样窗口内被内核多路复用得几近为 0。
- L1d pending 系列（replacement / fb_full / pending）单独成批且幅度巨大，可能表示局部存储缓冲（或相关结构）频繁发生饱和/延迟。

可能原因（按优先级）
1. 事件配对/单位不一致：脚本把不同来源或不同 semantics 的事件直接作比（如用一个事件做分母），导致比率>100%。例如 PERF_TYPE_RAW 与 PERF_TYPE_HW_CACHE 事件不可直接按同一基准比较。
2. 采样/多路复用噪声：内核 multiplex 后用 time_enabled/time_running 放大，但若 time_running 很小或采样不均匀，缩放会放大噪声并产生非常大的数值或尖峰。
3. 计数不可用或权限限制：部分事件被标为 unavailable（程序启动会打印），导致列中周期性为 0。
4. 计数窗口太短：interval 较短时，计数样本非常小，百分比计算对小整数敏感而变得不稳定。
5. 事件 ID/微架构依赖：使用的 raw event id（如 0x81D0 等）在不同 CPU 上语义不同，或统计项是累计/累加器而非“命中/访问”对。

可验证的检查（快速步骤）
- 检查程序启动输出，确认哪些 `counters[i].enabled` = unavailable。查看终端启动日志（程序会列出 opened/unavailable）。
- 在运行机器上执行 `perf list` 和 `perf stat -a`，确认事件是否存在以及语义说明。
- 在代码中临时打印每个 fd 的 `time_enabled` 与 `time_running`（或写入 CSV），检查是否存在极小的 `time_running` 导致极大 scale。
- 对尖峰时间点，检查系统负载（`top`/`pidstat`）与目标进程行为，确认是否有短期高负载或上下文切换。
- 用更长的 `-i`（例如 1000ms → 2000ms）重跑，观察波动是否平滑（若是，则多数噪声来自短窗口）。

改进建议（图表与采样）
- 在绘图脚本中：
  - 对百分比/比率进行下限保护（当分母 < 阈值时标记为 NaN 或跳过），避免 >100% 异常显示。  
  - 增加滑动平均（rolling mean）或绘制原始与平滑曲线以区分噪声/趋势。  
  - 在图例或注释显示 sample interval 和是否存在 unavailable events。
- 在采样程序中：
  - 在遇到非常小的 time_running 时记录警告并/或跳过该样本，避免 scale 放大噪声。  
  - 提供 `--events` 或配置文件以选择只画可靠的事件，避免把不可比的事件拿来直接计算比率。  
  - 可选地按秒化（counts / interval_s）显示速率而非原始计数，便于跨间隔比较。
- 验证事件语义：对 raw events，查阅 CPU 的 event list，确认 `l1d_pend_miss.pending` 的单位与其它计数是否相同。

结论与下一步（建议两项可选动作）
- 短期：先按“检查程序启动输出 + 打印 time_enabled/time_running”做验证，确认是否为 multiplex 缩放导致异常。需要我修改 pmu_timeseries.c 在写 CSV 时额外输出 time_enabled/time_running 字段吗？  
- 中期：在 `plot_pmu_timeseries.py` 中对比原始与 rolling-mean，并对比“每秒率”图以减少短时噪声。我可以为你实现图表脚本的护栏（分母阈值、滚动平均、每秒化），你想先我改哪个文件？