# SFrame Resolver（简明说明）

SFrame Resolver 是为 lbr 项目实现的轻量级栈展开与符号解析组件，基于自定义的 SFrame（Simple Frame）格式，旨在在没有完整 DWARF 信息或在 BPF/uprobes 场景下提供快速、准确的栈回溯与符号映射。

---

## 概述

- **目的**: 在运行时或 uprobe 快照中，从任意 PC/SP/BP 恢复调用栈、并将地址解析为函数/符号信息。
- **核心优势**: 较 DWARF 更小的表结构（.sframe 节），更适合内核或嵌入式环境下的快速展开；支持主程序及共享库的联合解析。
- **适用场景**: BPF/uProbe 快照回溯、生产环境最小符号表展开、离线分析与轻量级监控工具。

---

## 设计与结构

- **核心类型**
  - `SFrameResolver`：解析器主体，保存 pid、可执行文件路径、基址、ELF 句柄、已加载的 SFrame 数据、符号列表、共享库映射以及对 `/proc/<pid>/mem` 的打开句柄。
  - `SFrameData`：存储解析后的 `.sframe` 节内容（Header、函数列表 FDE、FRE 原始数据等）。
  - `SFrameFunction` / `SFrameFDE`：分别表示函数索引项和单条 Frame Description Entry（FRE/FDE）信息。
  - `UnwindContext` / `StackFrame`：展开时的运行时上下文与单帧结果结构。
- **主要模块**
  - ELF / `.sframe` 解析：`parseSFrameDataFromELF`、`loadSFrameData`。
  - 符号加载：`loadSymbols`（含 PLT 处理）。
  - 映射管理：`loadBaseAddress`、`loadLibraryMappings`、`loadLibraryMapping`。
  - 展开逻辑：`unwindFrameWithSFrame`（优先）、`unwindFrameWithFP`（回退）、`doUnwindStack` 系列接口。
  - 内存与寄存器访问：`readMemory`、`readUint64WithCtx`、`GetRegisters`（ptrace），支持使用 BPF 快照优先读取栈数据以避免 TOCTOU。

---

## 工作流程（高层）

1. 创建 `SFrameResolver`：读取 `/proc/<pid>/exe` 打开 ELF，尝试解析 `.sframe` 节，加载符号表与进程映射，打开 `/proc/<pid>/mem` 作为回溯数据源。
2. 根据给定的 `UnwindContext`（PC/SP/BP/可选栈快照）执行 `UnwindStack` / `UnwindStackFromContext`：
   - 查找 PC 所在函数（主程序或共享库），获取对应 `SFrameFunction` / `SFrameData`。
   - 在函数的 FRE 列表中匹配合适的 FDE（支持 PCINC 与 PCMASK 两种匹配类型）。
   - 从 FDE 的 FRE Info 推断 CFA（基于 SP 或 FP）和保存位置，读取返回地址与 caller FP，再更新上下文。
   - 若 SFrame 信息不可用或匹配失败，回退使用帧指针方法（FP-based unwind）。
3. 生成 `[]StackFrame`，并通过 `PrintStackTrace` 可视化输出。

---

## 关键字段与概念

- **FRE Info Word**：字节位编码包含 CFA 基寄存器（SP/FP）、偏移个数与每项字节大小、是否 mangled RA 等信息，通过 `parseFREInfo` 解析。
- **FDE/FRE 类型**：
  - `PCINC`：通过比较 PC 与区间 [start, start+size) 匹配。
  - `PCMASK`：针对重复块（rep block）和掩码型匹配（用于循环/重复代码模式）。
  - `FLEX`：更复杂的自定义类型（当前主要回退到默认处理，TODO: 完整实现）。
- **V2 vs V3**：SFrame 有两种索引格式（V2: 20 bytes per entry，V3: 16 bytes + 每函数 FRE 前有 5 字节 attr）。实现中兼容两者，并提供按差值和逐条解析两种方式来计算每函数 FRE 字节长度以互相校验。

---

## 主要函数（API 快览）

- `NewSFrameResolver(pid int) (*SFrameResolver, error)`：构造器，初始化解析器并加载资源。
- `(r *SFrameResolver) ResolveAddress(addr uint64) (*AddrInfo, error)`：将地址解析为符号信息（符号名、偏移等）。
- `(r *SFrameResolver) UnwindStack(maxFrames int) ([]StackFrame, error)`：从当前进程状态回溯栈。
- `(r *SFrameResolver) UnwindStackFromContext(ctx *UnwindContext, maxFrames int) ([]StackFrame, error)`：从指定上下文回溯。
- `(r *SFrameResolver) UnwindStackWithSFrame...`：仅使用 SFrame 信息回溯（不回退到 FP）。
- `(r *SFrameResolver) Close() error`：释放文件句柄与 ELF 句柄。

---

## 使用示例（伪代码）

初始化与展开：

```go
r, _ := NewSFrameResolver(pid)
defer r.Close()
frames, _ := r.UnwindStack(32)
r.PrintStackTrace(frames)
```

从任意 PC/SP/BP 快照展开：

```go
ctx := NewUnwindContextFromRegs(pc, sp, bp)
frames, _ := r.UnwindStackFromContext(ctx, 16)
```

---

## 限制与待办（当前已在代码中标注）

- TODO: 修正 PC_MASK 的解析问题（注释中提到 PC_MASK 部分解析有问题）。
- TODO: PTL（或类似索引）解析存在问题，需要验证与修复。
- TODO: 完整实现 FLEX 类型的 FDE 解析（V3 的复杂场景，如 DRAP 等）。
- 对于缺失或不完整的 SFrame 数据，解析器回退到基于帧指针的回溯，故在无 FP 的程序（frame-pointer omitted）中可能失败。
- 需要更多单元测试和真实进程样本验证（含不同 ABI / big-endian 情况）。

---

## 测试与验证建议

- 使用包含 `.sframe` 的可执行与共享库，验证 V2 与 V3 两种格式解析结果一致性。
- 用已知调用栈的进程（可插桩）测试 SFrame 展开与 FP 展开对比。
- 在 uprobe/BPF 快照场景下测试 `readUint64WithCtx` 的快照优先逻辑，验证 TOCTOU 问题是否被规避。

---

## 结语

该 `SFrameResolver` 在 lbr 仓库中提供了一个面向运行时、兼顾性能与可移植性的栈展开实现。下一步建议把本 Markdown 保存为 `docs/SFrameResolver.md`（或 README 节点），并补充单元测试与示例用例。

## 未来的工作

- 使解析符号和unwind结构,优化程序结构
- glibc库和vmlinux等无sframe段的部分怎么处理,目前为退回fp

