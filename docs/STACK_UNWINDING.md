# 栈回溯功能

## 概述

SFrame解析器现在支持栈回溯（Stack Unwinding）功能，可以从进程的当前执行点向上追溯整个调用栈。

## 功能特性

1. **进程内存读取**：通过 `/proc/pid/mem` 读取进程内存
2. **寄存器获取**：从 `/proc/pid/syscall` 获取寄存器状态（PC, SP, BP）
3. **栈帧展开**：基于帧指针（BP）逐层展开栈帧
4. **符号解析**：为每一帧解析函数名、文件名、行号等信息

## 数据结构

### StackFrame
表示一个栈帧：
```go
type StackFrame struct {
    PC   uint64     // 程序计数器(指令地址)
    SP   uint64     // 栈指针
    BP   uint64     // 基址指针
    Info *AddrInfo  // 符号信息
}
```

### UnwindContext
栈展开上下文：
```go
type UnwindContext struct {
    PC uint64  // 当前程序计数器
    SP uint64  // 当前栈指针
    BP uint64  // 当前基址指针
}
```

## API 使用

### 1. 创建解析器
```go
resolver, err := lbr.NewSFrameResolver(pid)
if err != nil {
    log.Fatal(err)
}
defer resolver.Close()
```

### 2. 执行栈回溯
```go
// 获取最多32帧
frames, err := resolver.UnwindStack(32)
if err != nil {
    log.Printf("栈回溯失败: %v", err)
    return
}
```

### 3. 打印栈跟踪
```go
resolver.PrintStackTrace(frames)
```

输出示例：
```
=== Stack Trace ===
#0  0x000055555555abcd in main.processData at main.go:123 [SP=0x7ffe1234, BP=0x7ffe1240]
#1  0x000055555555ef01 in main.handleRequest at main.go:89 [SP=0x7ffe1250, BP=0x7ffe1270]
#2  0x00007ffff7a12345 in pthread_start (libc.so.6) [SP=0x7ffe1280, BP=0x7ffe12a0]
===================
```

### 4. 手动遍历栈帧
```go
for i, frame := range frames {
    fmt.Printf("Frame %d:\n", i)
    fmt.Printf("  PC: 0x%x\n", frame.PC)
    fmt.Printf("  SP: 0x%x\n", frame.SP)
    fmt.Printf("  BP: 0x%x\n", frame.BP)
    
    if frame.Info != nil {
        fmt.Printf("  Function: %s\n", frame.Info.Function)
        if frame.Info.File != "" {
            fmt.Printf("  Location: %s:%d\n", frame.Info.File, frame.Info.Line)
        }
        if frame.Info.Library != "" {
            fmt.Printf("  Library: %s\n", frame.Info.Library)
        }
    }
}
```

## 实现原理

### x86-64 栈帧布局
```
高地址
+------------------+
| 参数7, 参数8...  |
+------------------+
| 返回地址         | <- BP+8
+------------------+
| 保存的BP         | <- BP (当前帧的基址指针)
+------------------+
| 局部变量         |
+------------------+
| ...              | <- SP (栈指针)
+------------------+
低地址
```

### 展开过程

1. **获取初始寄存器**
   - 从 `/proc/pid/syscall` 读取 PC, SP
   - 从栈内存读取 BP

2. **遍历栈帧**
   - 读取 `[BP]` 获取上一帧的 BP
   - 读取 `[BP+8]` 获取返回地址（上一帧的 PC）
   - 更新 SP = BP + 16

3. **符号解析**
   - 对每个 PC 调用 `ResolveAddress()`
   - 查找对应的函数名、文件、行号

4. **终止条件**
   - PC 或 BP 为 0
   - BP 不递增（检测循环）
   - 达到最大帧数限制

## 使用场景

1. **性能分析**：分析热点函数的调用链
2. **调试支持**：理解程序执行流程
3. **异常追踪**：定位错误发生的完整上下文
4. **性能剖析**：结合 LBR 数据分析调用关系

## 限制与注意事项

1. **需要进程权限**：需要读取 `/proc/pid/mem` 的权限
2. **依赖帧指针**：要求程序使用帧指针编译（`-fno-omit-frame-pointer`）
3. **暂停状态**：最好在进程暂停时获取寄存器，否则可能不准确
4. **优化影响**：高度优化的代码可能省略帧指针，导致展开失败

## 编译建议

为了确保栈回溯正常工作，建议使用以下编译选项：

```bash
# C/C++
gcc -fno-omit-frame-pointer -g your_program.c

# Go
go build -gcflags="-N -l" your_program.go
```

## 未来改进

- [ ] 支持无帧指针的栈展开（使用 DWARF CFI）
- [ ] 支持远程栈展开
- [ ] 添加内联函数展开
- [ ] 支持更多架构（ARM, RISC-V）
- [ ] 集成性能采样
