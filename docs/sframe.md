# SFrame 符号解析

## 概述

SFrame (Simple Frame) 是一种轻量级的栈帧解析格式,用于在运行时进行高效的栈回溯。在 LBR Demo 项目中,SFrame 解析器用于将内存地址解析为可读的函数符号信息。

## 架构设计

### 核心组件

1. **符号解析器** ([`internal/sframe_resolver.go`](internal/sframe_resolver.go))
   - 负责解析 SFrame 格式的调试信息
   - 提供地址到符号的映射功能

2. **符号表** ([`internal/disasm.go`](internal/disasm.go))
   - 使用 [`Symbol`](internal/disasm.go) 结构存储符号信息
   - 通过 [`Symbols.Find`](internal/disasm.go) 方法查找地址对应的符号

3. **DWARF 解析器** ([`internal/dwarf_resolver.go`](internal/dwarf_resolver.go))
   - 作为备选方案,提供 DWARF 格式的符号解析

## 符号解析流程

### 1. 内核符号加载

项目使用 [`LoadKallsyms`](internal/disasm.go) 函数从 `/proc/kallsyms` 加载内核符号:

```go
// 加载内核符号表
syms, err := lbr.LoadKallsyms()
if err != nil {
    log.Printf("Warning: failed to load kallsyms: %v", err)
}
```

### 2. 符号查找

在 [`processLbrData`](cmd/main.go) 函数中,使用符号表解析 LBR 记录中的地址:

```go
// 查找源地址符号
fromName, fromOffset, _ := syms.Find(entry.From)

// 查找目标地址符号
toName, toOffset, _ := syms.Find(entry.To)
```

### 3. 符号格式化

通过 [`BranchEndpoint`](internal/stack.go) 结构格式化符号输出:

```go
type BranchEndpoint struct {
    Addr     uint64   // 原始地址
    FuncName string   // 函数名
    Offset   uint64   // 函数内偏移
}
```

## 数据结构

### Symbol 结构

```go
type Symbol struct {
    Addr uint64  // 符号地址
    Name string  // 符号名称
}
```

### Symbols 集合

```go
type Symbols struct {
    syms []Symbol  // 按地址排序的符号列表
}
```

## 使用示例

### 基本使用

```go
// 1. 加载符号表
syms, err := lbr.LoadKallsyms()
if err != nil {
    return fmt.Errorf("failed to load symbols: %w", err)
}

// 2. 查找地址对应的符号
addr := uint64(0xffffffff81234567)
funcName, offset, found := syms.Find(addr)
if found {
    fmt.Printf("%s+%#x\n", funcName, offset)
} else {
    fmt.Printf("%#x\n", addr)
}
```

### 与 LBR 集成

在 LBR 数据处理流程中使用符号解析:

```go
func processLbrData(lbrMap *ebpf.Map, syms *lbr.Symbols) {
    // 遍历 LBR 记录
    for i := 0; i < numEntries && i < 32; i++ {
        entry := &data.Entries[i]
        
        // 解析源地址
        fromName, fromOffset, _ := syms.Find(entry.From)
        
        // 解析目标地址
        toName, toOffset, _ := syms.Find(entry.To)
        
        // 添加到栈跟踪
        stack.AddEntry(lbr.BranchEntry{
            From: &lbr.BranchEndpoint{
                Addr:     entry.From,
                FuncName: fromName,
                Offset:   fromOffset,
            },
            To: &lbr.BranchEndpoint{
                Addr:     entry.To,
                FuncName: toName,
                Offset:   toOffset,
            },
        })
    }
}
```

## 符号查找算法

[`Find`](internal/disasm.go) 方法使用二分查找算法定位符号:

```go
func (s *Symbols) Find(addr uint64) (string, uint64, bool) {
    var found Symbol
    for i := range s.syms {
        if s.syms[i].Addr <= addr {
            found = s.syms[i]
        } else {
            break
        }
    }
    
    if found.Name == "" {
        return "", 0, false
    }
    
    return found.Name, addr - found.Addr, true
}
```

**算法特点:**
- 线性扫描已排序的符号列表
- 返回小于等于目标地址的最大符号
- 计算函数内偏移量

## 输出格式

符号解析后的输出格式:

```
[#31] __x64_sys_execve+0x1a -> do_execve+0x0
[#30] do_execve+0x23 -> do_execveat_common+0x1d
[#29] do_execveat_common+0x142 -> bprm_execve+0x0
```

格式说明:
- `[#XX]`: LBR 条目编号(从最新到最旧)
- `function+offset`: 函数名 + 十六进制偏移
- `->`: 分支方向(从源到目标)

## 相关文件

- [`internal/sframe_resolver.go`](internal/sframe_resolver.go) - SFrame 解析实现
- [`internal/dwarf_resolver.go`](internal/dwarf_resolver.go) - DWARF 解析实现
- [`internal/disasm.go`](internal/disasm.go) - 符号表加载和查找
- [`internal/stack.go`](internal/stack.go) - 栈跟踪输出格式化
- [`internal/usersym.go`](internal/usersym.go) - 用户空间符号解析

## 性能优化

### 符号表预加载

在程序启动时加载符号表,避免运行时开销:

```go
// 在 run() 函数中预加载
syms, err := lbr.LoadKallsyms()
if err != nil {
    log.Printf("Warning: failed to load kallsyms: %v", err)
}
```

### 内存映射优化

使用 `BPF_F_MMAPABLE` 标志优化 BPF map 访问:

```go
lbrBuffMapSpec.Flags |= unix.BPF_F_MMAPABLE
```

## 故障排查

### 符号未找到

如果符号查找失败,检查:

1. `/proc/kallsyms` 是否可读
2. 是否有足够的权限
3. 内核符号是否已加载

### 地址解析错误

如果地址解析结果不准确:

1. 验证符号表是否按地址排序
2. 检查是否使用正确的地址空间(内核/用户)
3. 确认 KASLR (Kernel Address Space Layout Randomization) 偏移

## 参考资料

- [QUICKSTART_SYMBOL.md](docs/QUICKSTART_SYMBOL.md) - 符号解析快速入门
- [SYMBOL_RESOLUTION.md](docs/SYMBOL_RESOLUTION.md) - 符号解析详细文档
- [STACK_UNWINDING.md](docs/STACK_UNWINDING.md) - 栈回溯文档