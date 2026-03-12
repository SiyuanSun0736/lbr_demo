# LBR Demo 项目概述（不含 two_pass 版本）

## 项目简介

本仓库是一个用于基于 Last Branch Record (LBR) 与 BPF 的性能分析、热点定位与栈展开（stack unwinding）的工具集合。它整合了 eBPF 程序、Go/C 运行时辅助、以及一组符号解析与 DWARF 协议的解析器，用于在用户态和内核态收集、解析和还原调用栈与符号信息，帮助分析程序的分支/指令级行为与热点。

## 目标和适用场景

- 采集 CPU 的 LBR/PMU 数据以识别热分支与热点函数。
- 在无法直接获得符号或只有地址的情况下，解析地址到符号并执行精确的栈展开。
- 支持对内核或用户态应用的离线分析与可视化（包含脚本与示例）。

适用于性能工程、低级调试、采样型分析以及需要基于硬件分支记录重建调用路径的场景。

## 目录结构（高层）

- `bpf/`：C 语言编写的 BPF 程序源（如 `bpf_lbr.c`, `bpf_uprobe.c`）及头文件。
- `cmd/`：Go 可执行命令入口（`main.go`, `lbr_x86_bpfel.go`, `uprobe_x86_bpfel.go` 等）。
- `internal/`：核心库实现，包括 DWARF 解析、符号解析、栈帧解析、地图管理等（`dwarf_resolver.go`, `sframe_resolver.go`, `usersym.go` 等）。
- `docs/`：使用说明与设计文档（如 SYMBOL_RESOLUTION、STACK_UNWINDING、UNWIND_FROM_ADDRESS 等）。
- `shell/`：分析/可视化脚本（Python），用于生成图表与可视化结果。
- `pmu/`：与 PMU、缓存与其他硬件事件相关的脚本与 C 程序。
- `test/`：测试用例与示例（`test_lbr.c`, `test_stack_unwinding` 等）。
- `log/`：运行与调试时产出的日志、分析结果样例。

## 主要组件与职责

- BPF 层（`bpf/`）
  - 负责在内核层面安全、高效地采样 LBR/UPROBE 数据并通过 maps 发送到用户态。
  - 包含针对 x86 架构的 ELF/字节序处理支持（如 `bpf_lbr.c`）。

- 命令/工具入口（`cmd/`）
  - 提供命令行工具用于加载 BPF 程序、控制采样、导出采样数据并驱动后续分析流程。
  - 入口工具通常封装了 BPF 加载、perf/map 读取、采样文件写出等功能。

- 符号解析（`usersym.go`, `dwarf_resolver.go` 等）
  - 将二进制地址解析到可读符号，包括从 ELF、DWARF 信息或外部符号表获取函数/源代码位置信息。
  - 支持对内核和用户态二进制的不同解析策略。

- 栈帧恢复与展开（`sframe_resolver.go`, `stack.go` 等）
  - 在采样得到的地址列表或 LBR 路径下，使用 DWARF/CFI 信息与汇编反汇编逻辑恢复栈帧链（frame）和调用关系。
  - 提供对不完整或缺失帧指针（frame pointer）情况下的鲁棒展开策略。

- 地图与持久化（`maps.go`）
  - 管理 BPF maps 与用户态缓存，负责从内核收集样本并写入分析阶段所需的中间格式或 CSV / 文本日志。

- 可视化与分析脚本（`shell/`）
  - 一组 Python 脚本用于读取分析输出并生成折线图、热度图、Top list 等结果，便于交互式查看与快速定位热点。

## 核心流程（采样到分析）

1. 使用 `cmd` 中的工具加载并附加 BPF 程序到目标（或使用 uprobe 方式附加用户态函数）。
2. BPF 程序在运行时采集 LBR 或采样点，并将地址、pid、tid、timestamp 等写入 BPF maps。
3. 用户态工具轮询/读取 maps，将原始样本落盘为日志文件（`log/` 下类似文件）。
4. 分析工具读取日志，通过 `usersym` 与 `dwarf_resolver` 将地址解析为函数与源代码位置。
5. 使用 `sframe_resolver` 对每个样本进行栈展开，重建调用路径并聚合统计（如热点函数、调用链频次）。
6. 最后将聚合结果传入 `shell/` 中的可视化脚本生成图表或 CSV 报表。

## 设计要点与实现细节

- 可移植的 BPF/ELF 解析：针对不同字节序与 ELF 格式做兼容处理，保证在 x86_64 等目标上可正确解析符号表与 DWARF。
- 鲁棒的栈展开策略：在现代优化编译器下，单纯依赖帧指针往往不可行，项目结合 DWARF CFI 信息和指令级反汇编来辅助恢复未保存寄存器的调用链。
- 性能与采样负担控制：BPF 程序尽量保持较小逻辑，仅进行采样和必要的上下文捕获，复杂的解析放在用户态批量处理。
- 离线可重复分析：保存的中间日志便于重复运行不同解析策略或参数（如不同的符号解析级别）进行对比分析。

## 使用指南（快速）

- 构建与安装：
  - 编译 BPF 程序（通常由 `Makefile` 或 `go build` + clang 完成），并使用 `cmd` 中的工具加载。
- 运行采样：
  - 使用提供的命令行工具开始采样（示例：`go run ./cmd -options` 或构建后的二进制），采样结果写入 `log/`。
- 分析：
  - 使用 `internal` 下的分析工具或 `shell/` 中的 Python 脚本处理 `log/` 下的样本生成报告与图表。
- 测试：
  - 查看 `test/` 中示例，通过运行 `make test` 或提供的脚本验证栈展开与符号解析功能。

（具体命令和参数请参见 `docs/` 中的 QUICKSTART 与各 Design 文档）

## 开发与调试建议

- 日志驱动开发：当栈展开失败时，先查看 `log/` 里对应的样本文件，确认输入地址与二进制是否匹配。
- 使用 `docs/STACK_UNWINDING.md` 与 `docs/SYMBOL_RESOLUTION.md` 了解项目的理论与实现细节，便于定位问题。
- 本地复现：`test/` 下包含的示例程序可用于本地复现与小范围调试，便于单步验证展开器和解析器。

## 已知限制与假设

- 假定目标平台提供 LBR/PMU 支持（如 Intel 的 LBR），在不支持 LBR 的平台上需依赖其他采样方式。
- 在高度优化或无 DWARF 信息的二进制中，符号解析与栈展开的准确性会下降，需结合符号表或源码级信息提升精度。

## 建议的后续工作（可选）

- 补充自动化测试，覆盖更多编译优化级别产生的栈布局场景。
- 增加对更多架构的兼容性测试（如 ARM64）。
- 提供交互式可视化前端，便于在浏览器中对调用链进行钻取和过滤。

## 参考文档与文件

- 设计与使用说明：`docs/QUICKSTART_SYMBOL.md`, `docs/SYMBOL_RESOLUTION.md`, `docs/STACK_UNWINDING.md`。
- BPF 源码：`bpf/bpf_lbr.c`, `bpf/bpf_uprobe.c`。
- 入口工具：`cmd/`。
- 核心实现：`internal/dwarf_resolver.go`, `internal/sframe_resolver.go`, `internal/usersym.go`。
