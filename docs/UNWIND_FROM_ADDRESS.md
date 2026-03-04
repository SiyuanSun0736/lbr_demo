# 从任意地址进行栈回溯

## 功能说明

SFrame栈回溯器现在支持从任意地址开始回溯，不再局限于进程的当前执行状态。这对于分析LBR（Last Branch Record）等历史执行数据特别有用。

## API说明

### 1. 从自定义上下文开始回溯

```go
// 创建自定义上下文（需要提供PC、SP、BP）
ctx := lbr.NewUnwindContextFromRegs(pc, sp, bp)

// 从该上下文开始回溯
frames, err := sframeResolver.UnwindStackFromContext(ctx, 32)
```

### 2. 从PC地址开始回溯（使用当前SP/BP作为参考）

```go
// 仅提供PC地址，SP和BP会尝试从当前进程状态获取
ctx, err := sframeResolver.NewUnwindContextFromPC(pc)
if err != nil {
    return err
}

frames, err := sframeResolver.UnwindStackFromContext(ctx, 32)
```

### 3. 专门使用SFrame或FP进行回溯

```go
// 仅使用SFrame（不回退到帧指针）
frames, err := sframeResolver.UnwindStackWithSFrameFromContext(ctx, 32)

// 仅使用帧指针
frames, err := sframeResolver.UnwindStackWithFPFromContext(ctx, 32)
```

## 使用场景

### 场景1: 从LBR分支历史进行回溯

```go
// 假设我们有一个LBR条目
lbrEntry := data.Entries[0]

// 从LBR的目标地址开始回溯
ctx, _ := sframeResolver.NewUnwindContextFromPC(lbrEntry.To)
frames, err := sframeResolver.UnwindStackFromContext(ctx, 16)
if err == nil {
    fmt.Printf("从LBR地址 0x%x 的栈回溯:\n", lbrEntry.To)
    sframeResolver.PrintStackTrace(frames)
}
```

### 场景2: 分析特定函数的调用路径

```go
// 假设我们想分析某个特定地址的调用栈
targetAddr := uint64(0x401234)

// 创建上下文（注意：SP和BP需要是有效值）
ctx := lbr.NewUnwindContextFromRegs(
    targetAddr,  // PC
    0x7fff1000,  // SP (需要是实际的栈指针)
    0x7fff1100,  // BP (需要是实际的帧指针)
)

frames, err := sframeResolver.UnwindStackFromContext(ctx, 32)
```

## 注意事项

1. **寄存器上下文的准确性**: 栈回溯的准确性高度依赖于提供的SP和BP值。如果这些值不准确，回溯可能会失败或产生错误结果。

2. **从LBR回溯的限制**: LBR只记录了PC（程序计数器），不包含SP和BP。因此从LBR地址回溯时：
   - 可以使用 `NewUnwindContextFromPC()` 尝试使用当前进程的SP/BP作为参考
   - 但这可能不准确，因为栈状态已经改变
   - 更可靠的方法是在LBR记录时同时保存寄存器状态

3. **内存访问权限**: 栈回溯需要读取进程内存，确保进程仍在运行且可访问。

4. **符号解析**: 无论从哪里开始回溯，都需要确保有相应的符号信息（SFrame数据、符号表等）。

## 改进建议

如果需要从LBR进行精确的栈回溯，建议在eBPF程序中同时记录：
- PC（程序计数器）
- SP（栈指针）
- BP（帧指针）

这样可以获得完整的上下文信息，进行准确的栈回溯。
