package lbr

import (
	"bufio"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// SFrameResolver 基于SFrame格式的符号解析器
// SFrame (Simple Frame) 是一种轻量级的栈展开格式，比DWARF更紧凑
type SFrameResolver struct {
	pid         int
	execPath    string
	baseAddr    uint64
	baseAddrEnd uint64
	baseOffset  uint64
	elfFile     *elf.File
	sframeData  *SFrameData
	symbols     []ElfSymbol
	mappings    []MemoryMapping
	memFile     *os.File // /proc/pid/mem 文件句柄
}

// SFrameData SFrame数据结构
type SFrameData struct {
	Header     SFrameHeader
	Functions  []SFrameFunction
	FDEEntries []SFrameFDE
	hasData    bool
}

// SFrameHeader SFrame头部信息
type SFrameHeader struct {
	Magic       uint32
	Version     uint8
	Flags       uint8
	ABI         uint8
	FixedFPSize int8
	NumFDEs     uint32
	NumFuncs    uint32
}

// SFrameFunction SFrame函数信息
type SFrameFunction struct {
	StartAddr uint64
	Size      uint32
	FDEOffset uint32
	FuncInfo  uint8
}

// SFrameFDE SFrame Frame Description Entry
type SFrameFDE struct {
	StartOffset uint32
	FDEInfo     uint8
	RepSize     uint32
}

// StackFrame 栈帧信息
type StackFrame struct {
	PC   uint64    // 程序计数器(指令地址)
	SP   uint64    // 栈指针
	BP   uint64    // 基址指针
	Info *AddrInfo // 符号信息
}

// UnwindContext 栈展开上下文
type UnwindContext struct {
	PC uint64
	SP uint64
	BP uint64
}

// NewSFrameResolver 创建SFrame解析器
func NewSFrameResolver(pid int) (*SFrameResolver, error) {
	debugLog("[DEBUG] NewSFrameResolver: 为PID %d 创建SFrame解析器\n", pid)

	resolver := &SFrameResolver{
		pid: pid,
	}

	// 获取可执行文件路径
	execPath, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return nil, fmt.Errorf("failed to read exe link: %w", err)
	}
	resolver.execPath = execPath
	debugLog("[DEBUG] NewSFrameResolver: 可执行文件: %s\n", execPath)

	// 打开ELF文件
	elfFile, err := elf.Open(execPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open ELF file: %w", err)
	}
	resolver.elfFile = elfFile

	// 加载SFrame数据
	sframeData, err := resolver.loadSFrameData()
	if err != nil {
		debugLog("[DEBUG] NewSFrameResolver: 无法加载SFrame数据: %v\n", err)
		// SFrame可能不存在，尝试从符号表获取信息
	} else {
		resolver.sframeData = sframeData
		debugLog("[DEBUG] NewSFrameResolver: 成功加载SFrame数据\n")
	}

	// 加载符号表
	if err := resolver.loadSymbols(); err != nil {
		debugLog("[DEBUG] NewSFrameResolver: 加载符号表失败: %v\n", err)
	}

	// 加载基址
	if err := resolver.loadBaseAddress(); err != nil {
		debugLog("[DEBUG] NewSFrameResolver: 加载基址失败: %v\n", err)
	}

	// 加载共享库映射
	if err := resolver.loadLibraryMappings(); err != nil {
		debugLog("[DEBUG] NewSFrameResolver: 加载共享库映射失败: %v\n", err)
	}

	// 打开进程内存文件用于栈回溯
	memPath := fmt.Sprintf("/proc/%d/mem", pid)
	memFile, err := os.Open(memPath)
	if err != nil {
		debugLog("[DEBUG] NewSFrameResolver: 打开内存文件失败: %v\n", err)
		// 不致命，继续执行
	} else {
		resolver.memFile = memFile
	}

	debugLog("[DEBUG] NewSFrameResolver: SFrame解析器创建成功\n")
	return resolver, nil
}

// loadSFrameData 从ELF文件加载SFrame数据
func (r *SFrameResolver) loadSFrameData() (*SFrameData, error) {
	// 查找.sframe节
	section := r.elfFile.Section(".sframe")
	if section == nil {
		return nil, fmt.Errorf("no .sframe section found")
	}

	data, err := section.Data()
	if err != nil {
		return nil, fmt.Errorf("failed to read .sframe section: %w", err)
	}

	if len(data) < 16 {
		return nil, fmt.Errorf("invalid .sframe section size")
	}

	sframe := &SFrameData{hasData: true}

	// 解析头部（简化版本）
	sframe.Header.Magic = binary.LittleEndian.Uint32(data[0:4])
	sframe.Header.Version = data[4]
	sframe.Header.Flags = data[5]

	debugLog("[DEBUG] loadSFrameData: SFrame Magic=0x%x, Version=%d\n",
		sframe.Header.Magic, sframe.Header.Version)

	return sframe, nil
}

// loadSymbols 加载符号表
func (r *SFrameResolver) loadSymbols() error {
	var symbols []ElfSymbol

	// 动态符号表
	if dynsyms, err := r.elfFile.DynamicSymbols(); err == nil {
		for _, sym := range dynsyms {
			if sym.Name != "" && sym.Value != 0 {
				symbols = append(symbols, ElfSymbol{
					Name:  sym.Name,
					Addr:  sym.Value,
					Size:  sym.Size,
					Type:  elf.SymType(sym.Info & 0xf),
					Value: sym.Value,
				})
			}
		}
	}

	// 普通符号表
	if syms, err := r.elfFile.Symbols(); err == nil {
		for _, sym := range syms {
			if sym.Name != "" && sym.Value != 0 {
				symbols = append(symbols, ElfSymbol{
					Name:  sym.Name,
					Addr:  sym.Value,
					Size:  sym.Size,
					Type:  elf.SymType(sym.Info & 0xf),
					Value: sym.Value,
				})
			}
		}
	}

	r.symbols = symbols
	debugLog("[DEBUG] loadSymbols: 加载了 %d 个符号\n", len(symbols))
	return nil
}

// loadBaseAddress 加载进程基址
func (r *SFrameResolver) loadBaseAddress() error {
	maps, err := GetProcessMaps(r.pid)
	if err != nil {
		return err
	}

	for _, m := range maps {
		if strings.Contains(m.Perms, "x") && m.Pathname == r.execPath {
			r.baseAddr = m.StartAddr
			r.baseAddrEnd = m.EndAddr
			r.baseOffset = m.Offset
			debugLog("[DEBUG] loadBaseAddress: 基址=0x%x, 结束=0x%x, 偏移=0x%x\n",
				r.baseAddr, r.baseAddrEnd, r.baseOffset)
			return nil
		}
	}

	return fmt.Errorf("executable not found in memory maps")
}

// loadLibraryMappings 加载共享库映射
func (r *SFrameResolver) loadLibraryMappings() error {
	maps, err := GetProcessMaps(r.pid)
	if err != nil {
		return err
	}

	for _, m := range maps {
		// 只处理共享库
		if !strings.HasSuffix(m.Pathname, ".so") && !strings.Contains(m.Pathname, ".so.") {
			continue
		}
		if !strings.Contains(m.Perms, "x") {
			continue
		}

		// 加载共享库的符号信息
		if err := r.loadLibraryMapping(m.Pathname, m.StartAddr, m.EndAddr, m.Offset); err != nil {
			debugLog("[DEBUG] loadLibraryMappings: 加载共享库失败 %s: %v\n", m.Pathname, err)
		}
	}

	debugLog("[DEBUG] loadLibraryMappings: 加载了 %d 个共享库映射\n", len(r.mappings))
	return nil
}

// loadLibraryMapping 加载单个共享库映射
func (r *SFrameResolver) loadLibraryMapping(path string, startAddr, endAddr, offset uint64) error {
	// 检查文件是否存在
	if _, err := os.Stat(path); err != nil {
		return err
	}

	elfFile, err := elf.Open(path)
	if err != nil {
		return err
	}

	var symbols []ElfSymbol

	// 加载动态符号
	if dynsyms, err := elfFile.DynamicSymbols(); err == nil {
		for _, sym := range dynsyms {
			if sym.Name != "" && sym.Value != 0 {
				symbols = append(symbols, ElfSymbol{
					Name:  sym.Name,
					Addr:  sym.Value,
					Size:  sym.Size,
					Type:  elf.SymType(sym.Info & 0xf),
					Value: sym.Value,
				})
			}
		}
	}

	// 加载普通符号表作为后备
	if syms, err := elfFile.Symbols(); err == nil {
		for _, sym := range syms {
			if sym.Name != "" && sym.Value != 0 {
				symbols = append(symbols, ElfSymbol{
					Name:  sym.Name,
					Addr:  sym.Value,
					Size:  sym.Size,
					Type:  elf.SymType(sym.Info & 0xf),
					Value: sym.Value,
				})
			}
		}
	}

	mapping := MemoryMapping{
		StartAddr: startAddr,
		EndAddr:   endAddr,
		Offset:    offset,
		Path:      path,
		ElfFile:   elfFile,
		Symbols:   symbols,
	}

	r.mappings = append(r.mappings, mapping)
	debugLog("[DEBUG] loadLibraryMapping: 加载共享库 %s, 符号数: %d\n", path, len(symbols))
	return nil
}

// ResolveAddress 解析地址到符号信息
func (r *SFrameResolver) ResolveAddress(addr uint64) (*AddrInfo, error) {
	debugLog("[DEBUG] SFrameResolver.ResolveAddress: 解析地址 0x%x\n", addr)

	// 检查是否在主程序范围内
	if r.baseAddr > 0 && r.baseAddrEnd > 0 && addr >= r.baseAddr && addr < r.baseAddrEnd {
		fileOffset := addr - r.baseAddr + r.baseOffset
		debugLog("[DEBUG] SFrameResolver: 主程序地址，文件偏移 0x%x\n", fileOffset)

		info := &AddrInfo{
			Addr: addr,
		}

		// 从符号表查找
		funcName := r.findSymbol(fileOffset)
		if funcName != "" {
			info.Function = funcName
			debugLog("[DEBUG] SFrameResolver: 找到函数 %s\n", funcName)
			return info, nil
		}

		return nil, fmt.Errorf("symbol not found for address 0x%x", addr)
	}

	// 检查共享库
	for i := range r.mappings {
		if addr >= r.mappings[i].StartAddr && addr < r.mappings[i].EndAddr {
			fileOffset := addr - r.mappings[i].StartAddr + r.mappings[i].Offset
			debugLog("[DEBUG] SFrameResolver: 共享库地址, 路径: %s, 偏移: 0x%x\n",
				r.mappings[i].Path, fileOffset)

			info := &AddrInfo{
				Addr: addr,
			}

			// 提取库名
			libName := r.mappings[i].Path
			for j := len(libName) - 1; j >= 0; j-- {
				if libName[j] == '/' {
					info.Library = libName[j+1:]
					break
				}
			}
			if info.Library == "" {
				info.Library = libName
			}

			// 从共享库符号表查找
			funcName := r.findSymbolInList(fileOffset, r.mappings[i].Symbols)
			if funcName != "" {
				info.Function = funcName
				debugLog("[DEBUG] SFrameResolver: 找到共享库函数 %s@%s\n", funcName, info.Library)
			} else {
				// 找不到符号名，使用偏移表示
				info.Function = fmt.Sprintf("0x%x", fileOffset)
				debugLog("[DEBUG] SFrameResolver: 未找到符号，使用偏移 %s@%s\n", info.Function, info.Library)
			}
			return info, nil
		}
	}

	return nil, fmt.Errorf("address 0x%x not in any mapped region", addr)
}

// findSymbol 在主程序符号表中查找符号
func (r *SFrameResolver) findSymbol(addr uint64) string {
	return r.findSymbolInList(addr, r.symbols)
}

// findSymbolInList 在符号列表中查找符号
func (r *SFrameResolver) findSymbolInList(addr uint64, symbols []ElfSymbol) string {
	var bestMatch string
	var bestDist uint64 = ^uint64(0)

	for i := range symbols {
		sym := &symbols[i]

		// 检查符号类型（接受函数、无类型、对象等）
		// 不过滤类型，因为有些有效符号可能是其他类型
		_ = sym.Type // 保留类型字段以便后续可能的过滤

		// 检查地址是否在符号范围内
		if addr >= sym.Addr {
			dist := addr - sym.Addr
			if sym.Size > 0 && dist >= sym.Size {
				continue // 超出符号范围
			}
			if dist < bestDist {
				bestDist = dist
				bestMatch = sym.Name
			}
		}
	}

	if bestMatch != "" {
		if bestDist > 0 {
			return fmt.Sprintf("%s+0x%x", bestMatch, bestDist)
		}
		return bestMatch
	}

	return ""
}

// Close 关闭解析器并释放资源
func (r *SFrameResolver) Close() error {
	// 关闭内存文件
	if r.memFile != nil {
		_ = r.memFile.Close()
	}

	// 关闭共享库
	for i := range r.mappings {
		if r.mappings[i].ElfFile != nil {
			_ = r.mappings[i].ElfFile.Close()
		}
	}

	// 关闭主程序ELF文件
	if r.elfFile != nil {
		return r.elfFile.Close()
	}
	return nil
}

// GetSFrameInfo 获取SFrame信息（用于调试）
func (r *SFrameResolver) GetSFrameInfo() string {
	if r.sframeData == nil || !r.sframeData.hasData {
		return "No SFrame data available"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("SFrame Version: %d\n", r.sframeData.Header.Version))
	sb.WriteString(fmt.Sprintf("SFrame Flags: 0x%x\n", r.sframeData.Header.Flags))
	sb.WriteString(fmt.Sprintf("Number of symbols: %d\n", len(r.symbols)))
	sb.WriteString(fmt.Sprintf("Number of library mappings: %d\n", len(r.mappings)))

	return sb.String()
}

// ParseSFrameSection 解析SFrame节（用于后续扩展）
func ParseSFrameSection(reader io.Reader) (*SFrameData, error) {
	scanner := bufio.NewScanner(reader)
	sframe := &SFrameData{hasData: true}

	for scanner.Scan() {
		line := scanner.Text()
		// 解析SFrame数据（简化实现）
		debugLog("[DEBUG] ParseSFrameSection: %s\n", line)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return sframe, nil
}

// readMemory 从进程内存读取数据
func (r *SFrameResolver) readMemory(addr uint64, buf []byte) error {
	if r.memFile == nil {
		return fmt.Errorf("memory file not opened")
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

// GetRegisters 获取进程寄存器（从/proc/pid/syscall或其他方式）
// 简化实现：从栈顶推断寄存器值
func (r *SFrameResolver) GetRegisters() (*UnwindContext, error) {
	// 读取 /proc/pid/syscall 获取寄存器状态
	syscallPath := fmt.Sprintf("/proc/%d/syscall", r.pid)
	data, err := os.ReadFile(syscallPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read syscall: %w", err)
	}

	// syscall 文件格式: syscall_nr arg1 arg2 arg3 arg4 arg5 arg6 sp pc
	fields := strings.Fields(string(data))
	if len(fields) < 9 {
		return nil, fmt.Errorf("invalid syscall format")
	}

	ctx := &UnwindContext{}

	// 解析 SP 和 PC
	if sp, err := strconv.ParseUint(fields[7], 0, 64); err == nil {
		ctx.SP = sp
	}
	if pc, err := strconv.ParseUint(fields[8], 0, 64); err == nil {
		ctx.PC = pc
	}

	// BP 需要从栈中读取或使用其他方法获取
	// 简化实现：假设 BP 在栈顶附近
	if ctx.SP > 0 {
		if bp, err := r.readUint64(ctx.SP); err == nil {
			ctx.BP = bp
		}
	}

	debugLog("[DEBUG] GetRegisters: PC=0x%x, SP=0x%x, BP=0x%x\n", ctx.PC, ctx.SP, ctx.BP)
	return ctx, nil
}

// UnwindStack 执行栈回溯
func (r *SFrameResolver) UnwindStack(maxFrames int) ([]StackFrame, error) {
	if maxFrames <= 0 {
		maxFrames = 32 // 默认最大32帧
	}

	// 获取初始寄存器状态
	ctx, err := r.GetRegisters()
	if err != nil {
		return nil, fmt.Errorf("failed to get registers: %w", err)
	}

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
		debugLog("[DEBUG] UnwindStack: Frame %d: PC=0x%x, SP=0x%x, BP=0x%x\n",
			i, frame.PC, frame.SP, frame.BP)

		// 展开到下一帧
		if err := r.unwindFrame(ctx); err != nil {
			debugLog("[DEBUG] UnwindStack: 展开失败: %v\n", err)
			break
		}
	}

	debugLog("[DEBUG] UnwindStack: 总共展开了 %d 帧\n", len(frames))
	return frames, nil
}

// unwindFrame 展开一个栈帧
func (r *SFrameResolver) unwindFrame(ctx *UnwindContext) error {
	// 基于帧指针(BP)的栈展开
	// x86-64 标准栈帧布局：
	// [BP]     -> 上一个BP
	// [BP+8]   -> 返回地址(PC)
	// [BP+16]  -> 局部变量...

	if ctx.BP == 0 {
		return fmt.Errorf("null base pointer")
	}

	// 读取保存的BP
	newBP, err := r.readUint64(ctx.BP)
	if err != nil {
		return fmt.Errorf("failed to read saved BP: %w", err)
	}

	// 读取返回地址
	retAddr, err := r.readUint64(ctx.BP + 8)
	if err != nil {
		return fmt.Errorf("failed to read return address: %w", err)
	}

	// 验证新的值是否合理（在更新前检查）
	if retAddr == 0 || newBP == 0 {
		return fmt.Errorf("reached end of stack")
	}

	// 检查BP是否在合理范围内（应该递增，因为栈向下增长）
	// 保存旧BP用于比较
	oldBP := ctx.BP
	if newBP <= oldBP {
		return fmt.Errorf("invalid BP value: 0x%x <= 0x%x", newBP, oldBP)
	}

	// 更新上下文
	ctx.PC = retAddr
	ctx.BP = newBP
	ctx.SP = ctx.BP + 16 // 栈指针指向局部变量区

	return nil
}

// PrintStackTrace 打印栈回溯信息
func (r *SFrameResolver) PrintStackTrace(frames []StackFrame) {
	fmt.Println("\n=== Stack Trace ===")
	for i, frame := range frames {
		fmt.Printf("#%-2d 0x%016x", i, frame.PC)
		if frame.Info != nil {
			if frame.Info.Function != "" {
				fmt.Printf(" in %s", frame.Info.Function)
			}
			if frame.Info.File != "" {
				fmt.Printf(" at %s:%d", frame.Info.File, frame.Info.Line)
			}
			if frame.Info.Library != "" {
				fmt.Printf(" (%s)", frame.Info.Library)
			}
		}
		fmt.Printf(" [SP=0x%x, BP=0x%x]\n", frame.SP, frame.BP)
	}
	fmt.Println("===================")
}
