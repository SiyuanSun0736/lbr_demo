package lbr

import (
	"encoding/binary"
	"fmt"
	"io"
	"syscall"
)

// readMemory 从进程内存读取数据
func (r *SFrameResolver) readMemory(addr uint64, buf []byte) error {
	if r.memFile == nil {
		return fmt.Errorf("memory file not opened for pid %d", r.pid)
	}
	n, err := r.memFile.ReadAt(buf, int64(addr))
	if err != nil && err != io.EOF {
		return fmt.Errorf("failed to read memory at 0x%x: %w", addr, err)
	}
	if n != len(buf) {
		return fmt.Errorf("partial read at 0x%x: got %d bytes, expected %d", addr, n, len(buf))
	}
	return nil
}

// readUint64 从进程内存读取uint64
func (r *SFrameResolver) readUint64(addr uint64) (uint64, error) {
	buf := make([]byte, 8)
	if err := r.readMemory(addr, buf); err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(buf), nil
}

// readUint64WithCtx 优先从 BPF 栈快照读取，快照不覆盖时回退到 /proc/pid/mem。
// BPF 快照在 uprobe 触发瞬间同步采集，不存在 /proc/pid/mem 异步读取时的
// 栈内容过期（TOCTOU）问题。
func (r *SFrameResolver) readUint64WithCtx(addr uint64, ctx *UnwindContext) (uint64, error) {
	if ctx != nil && len(ctx.StackSnapshot) >= 8 && addr >= ctx.StackBase {
		off := addr - ctx.StackBase
		if off+8 <= uint64(len(ctx.StackSnapshot)) {
			bs := ctx.StackSnapshot[off : off+8]
			val := binary.LittleEndian.Uint64(bs)
			debugLog("[DEBUG] readUint64WithCtx: addr=0x%x off=0x%x bytes=% x val=0x%x\n",
				addr, off, bs, val)
			return val, nil
		}
	}
	return r.readUint64(addr)
}

// NewUnwindContextFromRegs 从完整的寄存器信息创建栈回溯上下文
func NewUnwindContextFromRegs(pc, sp, bp uint64) *UnwindContext {
	return &UnwindContext{
		PC: pc,
		SP: sp,
		BP: bp,
	}
}

// NewUnwindContextFromPC 从PC地址创建栈回溯上下文。
// 尝试通过 ptrace 读取进程当前寄存器获取 SP 和 BP；
// 若无法获取，则 SP 和 BP 为 0（部分功能可能受限）。
func (r *SFrameResolver) NewUnwindContextFromPC(pc uint64) (*UnwindContext, error) {
	ctx := &UnwindContext{PC: pc}
	if regs, err := r.GetRegisters(); err == nil {
		ctx.SP = regs.SP
		ctx.BP = regs.BP
		debugLog("[DEBUG] NewUnwindContextFromPC: 使用当前寄存器: SP=0x%x, BP=0x%x\n", ctx.SP, ctx.BP)
	} else {
		debugLog("[DEBUG] NewUnwindContextFromPC: 无法获取寄存器，SP/BP 为 0: %v\n", err)
	}
	return ctx, nil
}

// GetRegisters 通过 ptrace 获取进程寄存器，返回 UnwindContext
func (r *SFrameResolver) GetRegisters() (*UnwindContext, error) {
	if err := syscall.PtraceAttach(r.pid); err != nil {
		return nil, fmt.Errorf("failed to attach to process %d: %w", r.pid, err)
	}
	defer syscall.PtraceDetach(r.pid)

	var ws syscall.WaitStatus
	if _, err := syscall.Wait4(r.pid, &ws, 0, nil); err != nil {
		return nil, fmt.Errorf("failed to wait for process %d: %w", r.pid, err)
	}

	var regs syscall.PtraceRegs
	if err := syscall.PtraceGetRegs(r.pid, &regs); err != nil {
		return nil, fmt.Errorf("failed to get registers for pid %d: %w", r.pid, err)
	}

	ctx := &UnwindContext{
		PC: regs.Rip,
		SP: regs.Rsp,
		BP: regs.Rbp,
	}
	if ctx.SP < 0x1000 || ctx.PC < 0x1000 {
		return nil, fmt.Errorf("invalid register values: PC=0x%x, SP=0x%x, BP=0x%x",
			ctx.PC, ctx.SP, ctx.BP)
	}
	debugLog("[DEBUG] GetRegisters: PC=0x%x, SP=0x%x, BP=0x%x (via ptrace)\n",
		ctx.PC, ctx.SP, ctx.BP)
	return ctx, nil
}
