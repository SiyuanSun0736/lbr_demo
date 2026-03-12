package lbr

import "fmt"

// unwindFrameWithFP 展开一个栈帧
func (r *SFrameResolver) unwindFrameWithFP(ctx *UnwindContext) error {
	// 基于帧指针(BP)的栈展开
	// x86-64 标准栈帧布局：
	// [BP]     -> 上一个BP
	// [BP+8]   -> 返回地址(PC)
	// [BP+16]  -> 局部变量...

	if ctx.BP == 0 {
		return fmt.Errorf("null base pointer")
	}

	// 验证当前BP地址的合理性
	if ctx.BP < 0x1000 {
		return fmt.Errorf("invalid current BP address: 0x%x", ctx.BP)
	}

	// 读取保存的BP
	newBP, err := r.readUint64WithCtx(ctx.BP, ctx)
	if err != nil {
		return fmt.Errorf("failed to read saved BP at 0x%x: %w", ctx.BP, err)
	}

	// 读取返回地址
	retAddr, err := r.readUint64WithCtx(ctx.BP+8, ctx)
	if err != nil {
		return fmt.Errorf("failed to read return address at 0x%x: %w", ctx.BP+8, err)
	}

	debugLog("[DEBUG] unwindFrameWithFP: 读取 newBP=0x%x, retAddr=0x%x (from BP=0x%x)\n", newBP, retAddr, ctx.BP)

	// 验证新的值是否合理
	if retAddr == 0 {
		debugLog("[DEBUG] unwindFrameWithFP: 返回地址为0，到达栈底\n")
		return fmt.Errorf("reached end of stack (null return address)")
	}

	if newBP == 0 {
		debugLog("[DEBUG] unwindFrameWithFP: 新BP为0，到达栈底\n")
		return fmt.Errorf("reached end of stack (null BP)")
	}

	// 验证返回地址的合理性（应该是一个有效的代码地址）
	if retAddr < 0x1000 {
		return fmt.Errorf("invalid return address: 0x%x", retAddr)
	}

	// 检查BP是否在合理范围内
	// 栈向下增长（从高地址到低地址），所以旧的栈帧在更高的地址
	// 因此 newBP 应该 > oldBP
	oldBP := ctx.BP
	if newBP <= oldBP {
		return fmt.Errorf("invalid BP progression: newBP(0x%x) <= oldBP(0x%x)", newBP, oldBP)
	}

	// 检查BP增长是否合理（不应该跳跃太大）
	if newBP-oldBP > 0x100000 { // 1MB 的栈帧太大了
		return fmt.Errorf("unreasonable BP jump: 0x%x bytes (newBP=0x%x, oldBP=0x%x)", newBP-oldBP, newBP, oldBP)
	}

	// 更新上下文
	ctx.PC = retAddr
	ctx.BP = newBP
	// 对于FP-based展开，调用者的SP应该等于当前BP+16
	// 因为: [BP] = 旧BP, [BP+8] = retAddr, [BP+16] = 调用者的栈顶
	ctx.SP = ctx.BP

	debugLog("[DEBUG] unwindFrameWithFP: 更新后 PC=0x%x, BP=0x%x, SP=0x%x\n", ctx.PC, ctx.BP, ctx.SP)
	return nil
}

// UnwindStackWithFPFromContext 从指定上下文开始，仅使用帧指针执行栈回溯
func (r *SFrameResolver) UnwindStackWithFPFromContext(ctx *UnwindContext, maxFrames int) ([]StackFrame, error) {
	if maxFrames <= 0 {
		maxFrames = 32
	}

	// 验证上下文
	if ctx.PC == 0 || ctx.SP == 0 {
		return nil, fmt.Errorf("invalid context: PC=0x%x, SP=0x%x", ctx.PC, ctx.SP)
	}

	// 创建上下文副本
	contextCopy := &UnwindContext{
		PC: ctx.PC,
		SP: ctx.SP,
		BP: ctx.BP,
	}

	return r.doUnwindStackWithFP(contextCopy, maxFrames)
}

// UnwindStackWithFP 从进程当前状态开始，仅使用帧指针执行栈回溯
func (r *SFrameResolver) UnwindStackWithFP(maxFrames int) ([]StackFrame, error) {
	if maxFrames <= 0 {
		maxFrames = 32
	}

	// 获取初始寄存器状态
	ctx, err := r.GetRegisters()
	if err != nil {
		return nil, fmt.Errorf("failed to get registers: %w", err)
	}

	return r.doUnwindStackWithFP(ctx, maxFrames)
}

// doUnwindStackWithFP 执行实际的FP栈回溯逻辑
func (r *SFrameResolver) doUnwindStackWithFP(ctx *UnwindContext, maxFrames int) ([]StackFrame, error) {

	frames := make([]StackFrame, 0, maxFrames)

	for i := 0; i < maxFrames; i++ {
		if ctx.PC == 0 {
			break
		}

		// 创建当前栈帧
		frame := StackFrame{
			PC: ctx.PC,
			SP: ctx.SP,
			BP: ctx.BP,
		}

		// 解析符号信息
		if info, err := r.ResolveAddress(ctx.PC); err == nil {
			frame.Info = info
		}

		frames = append(frames, frame)
		debugLog("[DEBUG] UnwindStackWithFP: Frame %d: PC=0x%x, SP=0x%x, BP=0x%x\n",
			i, frame.PC, frame.SP, frame.BP)

		// 仅使用FP展开
		if err := r.unwindFrameWithFP(ctx); err != nil {
			debugLog("[DEBUG] UnwindStackWithFP: FP展开失败: %v\n", err)
			break
		}
	}

	debugLog("[DEBUG] UnwindStackWithFP: 总共展开了 %d 帧\n", len(frames))
	return frames, nil
}
