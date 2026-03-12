# LBR Demo 项目概述（包含 two-pass 模式说明）

## 项目简介

本仓库是一个基于 Last Branch Record (LBR) 与 eBPF 的性能分析工具，提供从内核/硬件采样到用户态符号解析与栈展开的完整流程。工具链包含 BPF 程序、Go 命令行入口、以及用于符号解析与栈展开的库和脚本，用于快速定位热点与还原调用链。

## two-pass 模式总览（重点）

- **目标**: 在不影响被测程序运行性能的前提下，先用轻量方式快速定位“热点地址”，再仅对这些热点进行更昂贵但精确的采样与栈展开，从而在保证准确性的同时降低总体开销。
- **阶段划分**:
  - Phase 1（热点发现）: 使用 BPF 快速采样并统计地址频次，保持解析/展开开销最低以延长采样窗口并提高统计质量。
  - 切换触发: 通过用户中断（如脚本发送的 `SIGINT`）或其他策略触发从 Phase 1 到 Phase 2 的过渡。
  - Phase 2（精确采样）: 对 Phase 1 输出的 Top-N 热点地址逐一挂载 uprobe（或其它触发点），捕获寄存器快照与栈内存，并使用 `SFrame`/DWARF 等机制执行精确栈展开与符号解析。

- **优势**: 把昂贵的栈展开与符号解析限制在真正的热点地址上，既提高了分析效率，也减少了对目标系统的干扰。

## 相关实现位置

- 入口程序与开关: [cmd/main.go](cmd/main.go)（包含 `-two-pass`、`-top-n`、`-sframe` 等命令行参数）。
- two-pass 演示脚本: [test/test_two_pass.sh](test/test_two_pass.sh) — 自动演示 Phase1 -> 发送 `SIGINT` -> Phase2 的完整流程，并验证输出（生成 `phase1_addr_stats_*.csv` 和 `phase2_unwind_*.csv`）。
- BPF 源码: `bpf/`（如 `bpf_lbr.c`, `bpf_uprobe.c`），负责内核层采样与 uprobe 快照。
- 符号解析与栈展开核心: `internal/sframe_resolver.go`, `internal/dwarf_resolver.go`, `internal/usersym.go`。

## two-pass 流程详述

1. 启动（Phase 1）: 使用二进制或脚本以 `-two-pass` 参数启动工具，BPF 程序在内核侧以低开销方式采样，用户态程序仅统计地址频次并将统计结果写入 `log/`（如 `phase1_addr_stats_*.csv`）。
2. 等待/采集窗口: Phase 1 持续一段短时间（可配置，示例脚本默认 10s），以便收集代表性的热点地址。
3. 触发切换: 脚本或用户发送 `SIGINT` 给运行中的采样进程，触发程序进入 Phase 2（或按实现的其它策略触发）。
4. Phase 2 执行: 程序读取 Phase 1 的 Top-N 热点地址，动态为这些地址挂载 uprobe；当 uprobe 触发时，BPF 捕获寄存器与栈快照，将数据交给用户态处理器并使用 `SFrame`/DWARF 做精确栈展开，最终将调用栈结果写入 `log/`（例如 `phase2_unwind_*.csv`）。
5. 验证与退出: Phase 2 完成后程序自动退出或按策略停止；脚本会校验日志与 CSV 是否生成并包含关键日志项（例如 “Phase 1 结束”、“Phase 2 开始”、“uprobe 已挂载”、“栈展开结果已写入”）。

## 示例运行（快速）

使用提供的测试脚本自动化执行 two-pass 流程：

```bash
sudo ./test/test_two_pass.sh
```

脚本会完成编译、启动被测二进制、启动 `lbr-demo`（two-pass 模式）、在 Phase 1 等待后发送 `SIGINT` 切换到 Phase 2，并最终检查 `log/` 下输出文件。

## 设计与实现要点

- Phase 1 保持轻量：尽量只做地址计数与最小必要信息记录，避免昂贵解析逻辑。
- Phase 2 精准展开：在热点地址上启用 uprobe 捕获完整寄存器和栈上下文，使用 `SFrame` 与 DWARF 信息做精确恢复。
- 配置灵活：Top-N、Phase1 时长、是否启用 `sframe` 等均可通过命令行参数调整以适配不同场景。

## 关键文件与路径

- [cmd/main.go](cmd/main.go)
- [test/test_two_pass.sh](test/test_two_pass.sh)
- `bpf/bpf_lbr.c`, `bpf/bpf_uprobe.c`
- `internal/sframe_resolver.go`, `internal/dwarf_resolver.go`, `internal/usersym.go`

## 建议的后续工作

- 把本文件作为 README 的一部分或合并到文档目录，方便用户快速理解 two-pass 流程。 
- 增加更多 two-pass 的自动化测试用例与 CI 步骤，覆盖不同 Top-N/采样时长场景。 
- 提供英文版或更详细的运行示例与故障排查指南。
