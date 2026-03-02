# LBR Demo - Last Branch Record 分析工具

[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.18+-00ADD8.svg)](https://golang.org/)
[![eBPF](https://img.shields.io/badge/eBPF-powered-orange.svg)](https://ebpf.io/)

基于 eBPF 和 Linux Last Branch Record (LBR) 技术的程序执行路径分析工具。通过捕获 CPU 硬件的分支记录，实现对程序控制流的低开销追踪与分析。

---

## 目录

- [项目简介](#项目简介)
- [功能特性](#功能特性)
- [架构概览](#架构概览)
- [环境要求](#环境要求)
- [快速开始](#快速开始)
- [构建说明](#构建说明)
- [使用方式](#使用方式)
- [项目结构](#项目结构)
- [文档](#文档)
- [测试](#测试)

---

## 项目简介

LBR (Last Branch Record) 是 Intel x86 处理器提供的硬件特性，可以记录最近若干条分支跳转指令的来源地址与目标地址。本工具利用 Linux `perf_event` 接口与 eBPF 程序，在几乎零开销的情况下采集这些分支记录，并结合符号解析还原出函数级别的调用路径。

---

## 功能特性

- 🔍 **LBR 分支追踪**：利用 CPU 硬件 LBR 寄存器捕获程序执行路径
- 📊 **符号解析**：将裸地址映射为可读的函数符号名
- 🗂️ **栈回退 (Stack Unwinding)**：支持基于 SFrame/DWARF 的栈展开
- ⚡ **低开销采集**：基于 eBPF，运行时开销极低
- 📈 **PMU 性能监测**：集成 CPU PMU 事件统计分析
- 🛠️ **KernelScript 支持**：提供内核脚本扩展能力

---

## 架构概览

```
用户态 (Go)                     内核态 (eBPF)
┌─────────────────────┐        ┌─────────────────────┐
│  cmd/main.go        │◄──────►│  bpf/bpf_lbr.c      │
│  符号解析模块        │        │  LBR 事件采集        │
│  栈回退模块          │        │  perf_event 处理     │
│  PMU 统计模块        │        └─────────────────────┘
│  KernelScript       │
└─────────────────────┘
```

---

## 环境要求

| 依赖项 | 版本要求 |
|--------|---------|
| Linux 内核 | ≥ 5.8（支持 eBPF ring buffer） |
| Go | ≥ 1.18 |
| clang/llvm | ≥ 11 |
| libbpf | 已内置（submodule） |
| bpftool | 已内置（submodule） |
| CPU | Intel x86_64（支持 LBR 特性） |

> **注意**：运行时需要 `root` 权限或 `CAP_BPF` / `CAP_PERFMON` 能力。

---

## 快速开始

### 1. 克隆仓库

```bash
git clone --recurse-submodules https://github.com/SiyuanSun0736/lbr_demo.git
cd lbr_demo
```

### 2. 初始化子模块

```bash
git submodule update --init --recursive
```

### 3. 构建并运行

```bash
make run
```

---

## 构建说明

```bash
# 完整构建（生成 eBPF 骨架 + 编译主程序 + 编译示例）
make all

# 仅生成 eBPF Go 绑定代码
make generate

# 编译主程序
make build

# 编译示例程序
make build-examples

# 清理构建产物
make clean
```

构建产物：
- `lbr-demo` — 主程序二进制
- `examples/stack_unwinding/stack_unwinding` — 栈回退示例

---

## 使用方式

### 运行主程序

```bash
sudo ./lbr-demo [选项]
```

### 符号解析演示

```bash
bash demo_symbol_resolution.sh
```

### 运行栈回退示例

```bash
sudo ./examples/stack_unwinding/stack_unwinding
```

---

## 项目结构

```
lbr_demo/
├── bpf/                    # eBPF C 程序
│   ├── bpf_lbr.c           # LBR 事件采集核心逻辑
│   └── vmlinux.h           # 内核类型定义（BTF 生成）
├── cmd/                    # Go 主程序入口
│   ├── main.go             # 程序入口
│   └── lbr_x86_bpfel.go    # eBPF 自动生成的 Go 绑定
├── internal/               # 内部核心模块
├── pmu/                    # PMU 性能监测模块
├── ks/                     # KernelScript 模块
├── kernelscript/           # KernelScript 实现
├── shell/                  # Shell 工具脚本
├── log/                    # 日志模块
├── examples/               # 示例程序
│   └── stack_unwinding/    # 栈回退示例
├── test/                   # 测试程序
├── docs/                   # 文档
│   ├── QUICKSTART_SYMBOL.md
│   ├── PMU_SFRAME_SYMBOL_INTEGRATION.md
│   └── sframe.md
├── bpftool/                # bpftool 工具（submodule）
├── Makefile
├── go.mod
└── demo_symbol_resolution.sh
```

---

## 文档

| 文档 | 说明 |
|------|------|
| [docs/QUICKSTART_SYMBOL.md](docs/QUICKSTART_SYMBOL.md) | 符号解析快速入门 |
| [docs/PMU_SFRAME_SYMBOL_INTEGRATION.md](docs/PMU_SFRAME_SYMBOL_INTEGRATION.md) | PMU + SFrame 符号集成说明 |
| [docs/sframe.md](docs/sframe.md) | SFrame 栈帧格式说明 |
| [pmu/README.md](pmu/README.md) | PMU 性能监测工具使用说明 |
| [test/README.md](test/README.md) | 测试程序说明 |

---

## 测试

```bash
cd test
make        # 编译测试程序
make run    # 运行测试
```

测试覆盖场景包括：排序算法、数字分类等典型分支密集型函数，用于验证 LBR 捕获的正确性。

---

## 许可证

本项目遵循 MIT 许可证。其中：
- `bpftool/` 遵循 [BSD-2-Clause](bpftool/LICENSE.BSD-2-Clause) 和 [GPL-2.0](bpftool/LICENSE.GPL-2.0)
- `bpftool/libbpf/` 遵循 libbpf 项目许可证

---

## 致谢

- [libbpf](https://github.com/libbpf/libbpf) — eBPF 用户态库
- [bpftool](https://github.com/libbpf/bpftool) — BPF 工具集
- Linux `perf_event` 子系统