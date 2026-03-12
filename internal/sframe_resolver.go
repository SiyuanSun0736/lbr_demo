package lbr

import (
	"debug/dwarf"
	"debug/elf"
	"fmt"
	"os"
)

// SFrameMagic SFrame格式的魔数
const SFrameMagic = 0xdee2

// SFrame ABI/架构标识符
const (
	SFRAME_ABI_AARCH64_ENDIAN_BIG    = 1 // AARCH64 大端序
	SFRAME_ABI_AARCH64_ENDIAN_LITTLE = 2 // AARCH64 小端序
	SFRAME_ABI_AMD64_ENDIAN_LITTLE   = 3 // AMD64 小端序
	SFRAME_ABI_S390X_ENDIAN_BIG      = 4 // s390x 大端序
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
	dwarfData   *dwarf.Data // 主程序 DWARF 调试信息（可选，用于 ResolveAddress fallback）
	sframeData  *SFrameData
	symbols     []ElfSymbol
	mappings    []MemoryMapping
	memFile     *os.File // /proc/pid/mem 文件句柄
}

// SFrameData SFrame数据结构
type SFrameData struct {
	Header      SFrameHeader
	Functions   []SFrameFunction
	FDEEntries  []SFrameFDE
	sectionAddr uint64 // .sframe节的虚拟地址
	sectionData []byte // .sframe节的原始数据（用于FRE解析）
	hasData     bool
}

// SFrameHeader SFrame头部信息
type SFrameHeader struct {
	Magic         uint16 // 真实的魔数是16位
	Version       uint8
	Flags         uint8
	ABI           uint8
	FixedFPOffset int8  // CFA fixed FP offset
	FixedRAOffset int8  // CFA fixed RA offset
	AuxHdrLen     uint8 // Auxiliary header length
	NumFDEs       uint32
	NumFREs       uint32 // 帧行条目数量
	FRELen        uint32 // FRE子节长度
	FDEOff        uint32 // FDE子节偏移
	FREOff        uint32 // FRE子节偏移
}

// SFrameFunction SFrame函数信息
// FDE Index 部分 (V2: 20字节; V3 Index: 16字节)
// V3还额外在FRE子节开头存有 sframe_func_desc_attr (5字节)，解析后写入此结构体
type SFrameFunction struct {
	// --- FDE Index 字段 ---
	StartAddr   int64  // 函数起始地址(PC-relative有符号偏移; V3为int64, V2为int32符号扩展)
	Size        uint32 // 函数大小(字节)
	StartFREOff uint32 // 函数数据在FRE子节中的字节偏移(V3包含5字节attr头)
	// --- 派生/属性字段 ---
	FREByteLen uint32 // 函数FRE数据总字节数(含V3 5字节attr; 由sframeComputeFREByteLens计算; V2为0)
	// --- FDE Attribute 字段 (V2: 来自FDE Index; V3: 来自FRE子节的sframe_func_desc_attr) ---
	NumFREs   uint32 // FRE数量 (V2: uint32; V3 sfda_func_num_fres: uint16)
	FuncInfo  uint8  // sfda_func_info: bits[3:0]=fre_type, bit[4]=pctype, bit[7]=signal(V3)
	FuncInfo2 uint8  // sfda_func_info2: bits[4:0]=fde_type (V3专用; V2恒为0)
	RepSize   uint8  // 重复块大小(用于PCMASK类型)
	Padding   uint16 // 填充(V2新增)
}

// SFrameFDE SFrame Frame Description Entry
// 描述函数内特定位置的栈帧布局信息
type SFrameFDE struct {
	StartOffset uint32 // 相对于函数起始的偏移
	FDEInfo     uint8  // FDE信息字节(保留,用于FDE级别信息)
	FREInfo     uint8  // FRE Info Word,包含CFA base寄存器、offset大小和数量等信息
	RepSize     uint32 // 重复次数/大小
	CFAOffset   int32  // CFA偏移量(从SP或BP计算)
	FPOffset    int32  // 帧指针保存位置偏移
	RAOffset    int32  // 返回地址保存位置偏移
}

// FRE Info Word 常量定义
const (
	// FRE Data Word Size (bits 5-6 of FRE info byte, V3)
	// V3规范将 SFRAME_FRE_OFFSET_<N>B 重命名为 SFRAME_FRE_DATAWORD_<N>B
	SFRAME_FRE_DATAWORD_1B = 0 // 1字节数据字
	SFRAME_FRE_DATAWORD_2B = 1 // 2字节数据字
	SFRAME_FRE_DATAWORD_4B = 2 // 4字节数据字
	// 兼容旧名称
	SFRAME_FRE_OFFSET_1B = SFRAME_FRE_DATAWORD_1B
	SFRAME_FRE_OFFSET_2B = SFRAME_FRE_DATAWORD_2B
	SFRAME_FRE_OFFSET_4B = SFRAME_FRE_DATAWORD_4B

	// FRE CFA Base Register ID (bit 0 of FRE info byte)
	// SFrame spec (SFRAME_BASE_REG_FP=0, SFRAME_BASE_REG_SP=1)
	SFRAME_FRE_CFA_BASE_REG_FP = 0 // FP-based CFA
	SFRAME_FRE_CFA_BASE_REG_SP = 1 // SP-based CFA

	// SFrame Flags (sfp_flags)
	SFRAME_F_FDE_SORTED           = 0x1 // FDE按PC排序
	SFRAME_F_FRAME_POINTER        = 0x2 // 所有函数保留帧指针
	SFRAME_F_FDE_FUNC_START_PCREL = 0x4 // FDE起始地址为PC-relative

	// SFrame FDE Type (sfda_func_info2 bits 0-4, V3专用)
	SFRAME_FDE_TYPE_DEFAULT = 0 // 默认类型: CFA=SP/FP+offset
	SFRAME_FDE_TYPE_FLEX    = 1 // 灵活类型: 用于DRAP、非标准CFA等复杂场景

	// SFrame FDE PC Type (sfda_func_info bit 4)
	SFRAME_FDE_PCTYPE_INC  = 0 // PC递增型: FRE_START_ADDR <= PC
	SFRAME_FDE_PCTYPE_MASK = 1 // PC掩码型: FRE_START_ADDR <= PC % REP_BLOCK_SIZE
)

// StackFrame 栈帧信息
type StackFrame struct {
	PC   uint64    // 程序计数器(指令地址)
	SP   uint64    // 栈指针
	BP   uint64    // 基址指针
	Info *AddrInfo // 符号信息
}

// UnwindContext 栈展开上下文
type UnwindContext struct {
	PC            uint64
	SP            uint64
	BP            uint64
	StackBase     uint64 // uprobe 触发时的 RSP（快照基地址）
	StackSnapshot []byte // BPF 在 uprobe 触发瞬间抓取的原始栈字节（从 StackBase 起）
}

// GetABIDescription 返回 ABI 值的描述
func GetABIDescription(abi uint8) string {
	switch abi {
	case SFRAME_ABI_AARCH64_ENDIAN_BIG:
		return "AARCH64 big-endian"
	case SFRAME_ABI_AARCH64_ENDIAN_LITTLE:
		return "AARCH64 little-endian"
	case SFRAME_ABI_AMD64_ENDIAN_LITTLE:
		return "AMD64 little-endian"
	case SFRAME_ABI_S390X_ENDIAN_BIG:
		return "s390x big-endian"
	default:
		return fmt.Sprintf("Unknown ABI (%d)", abi)
	}
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

	// 加载 DWARF 调试信息（可选）
	if dd, err := elfFile.DWARF(); err == nil {
		resolver.dwarfData = dd
		debugLog("[DEBUG] NewSFrameResolver: 成功加载DWARF数据\n")
	} else {
		debugLog("[DEBUG] NewSFrameResolver: 无DWARF数据: %v\n", err)
	}

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

	// 加载基址及共享库映射
	baseAddr, baseAddrEnd, baseOffset, err := loadProcessMappings(resolver.pid, resolver.execPath, resolver.loadLibraryMapping)
	if err != nil {
		debugLog("[DEBUG] NewSFrameResolver: 加载进程映射失败: %v\n", err)
	} else {
		resolver.baseAddr = baseAddr
		resolver.baseAddrEnd = baseAddrEnd
		resolver.baseOffset = baseOffset
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

// loadSFrameData 从ELF文件加载SFrame数据（委托给 parseSFrameDataFromELF）
func (r *SFrameResolver) loadSFrameData() (*SFrameData, error) {
	return parseSFrameDataFromELF(r.elfFile)
}

// loadSymbols 加载符号表（含 PLT 条目）
func (r *SFrameResolver) loadSymbols() error {
	r.symbols = append(loadELFSymbols(r.elfFile), loadPLTSymbols(r.elfFile)...)
	debugLog("[DEBUG] loadSymbols: 加载了 %d 个符号（含PLT）\n", len(r.symbols))
	return nil
}

// loadLibraryMapping 加载单个共享库映射（基础信息 + SFrame 数据）
func (r *SFrameResolver) loadLibraryMapping(path string, startAddr, endAddr, offset uint64) error {
	mapping, err := loadLibraryMappingBase(path, startAddr, endAddr, offset)
	if err != nil {
		return err
	}
	// 额外加载 SFrame 数据
	if sd, err := parseSFrameDataFromELF(mapping.ElfFile); err != nil {
		debugLog("[DEBUG] loadLibraryMapping: 加载共享库SFrame数据失败 %s: %v\n", path, err)
	} else {
		mapping.SFrameData = sd
		debugLog("[DEBUG] loadLibraryMapping: 成功加载共享库SFrame数据 %s, Version=%d, FDE数: %d\n",
			path, sd.Header.Version, len(sd.Functions))
	}
	r.mappings = append(r.mappings, mapping)
	debugLog("[DEBUG] loadLibraryMapping: 加载共享库 %s, 符号数: %d, SFrame: %v\n", path, len(mapping.Symbols), mapping.SFrameData != nil)
	return nil
}

// ResolveAddress 解析地址到符号信息
func (r *SFrameResolver) ResolveAddress(addr uint64) (*AddrInfo, error) {
	debugLog("[DEBUG] SFrameResolver.ResolveAddress: 解析地址 0x%x\n", addr)
	return resolveAddressInMappings(addr, r.baseAddr, r.baseAddrEnd, r.baseOffset,
		r.execPath, r.dwarfData, r.symbols, r.mappings)
}

// findSymbol 在主程序符号表中查找符号
func (r *SFrameResolver) findSymbol(addr uint64) string {
	return findSymbolInList(addr, r.symbols)
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

// findSFrameFunction 根据PC地址查找对应的SFrame函数信息
func (r *SFrameResolver) findSFrameFunction(pc uint64) (*SFrameFunction, uint64) {
	// 检查主程序范围
	if pc >= r.baseAddr && pc < r.baseAddrEnd {
		if r.sframeData != nil && r.sframeData.hasData {
			debugLog("[DEBUG] findSFrameFunction: 主程序地址 PC=0x%x, baseAddr=0x%x, baseOffset=0x%x, sectionAddr=0x%x, 函数数=%d, PCREL=%v\n",
				pc, r.baseAddr, r.baseOffset, r.sframeData.sectionAddr, len(r.sframeData.Functions),
				r.sframeData.Header.Flags&0x4 != 0)

			// 常量定义
			const SFRAME_F_FDE_FUNC_START_PCREL = 0x4

			// 在主程序SFrame函数列表中查找
			// 根据SFrame V2规范：
			// - 如果设置了SFRAME_F_FDE_FUNC_START_PCREL标志，StartAddr是相对于FDE字段本身的偏移
			// - 如果未设置，StartAddr是相对于.sframe节开始的偏移

			// 计算FDE数组的起始位置（在文件中）
			headerSize := 28 + int(r.sframeData.Header.AuxHdrLen)
			fdeArrayStart := headerSize + int(r.sframeData.Header.FDEOff)

			// 遍历所有FDE，选最精确的 PCINC（普通函数）类 FDE；
			// FuncInfo bit 4 判断 PC 类型 (V2来自FDE Index, V3来自sframe_func_desc_attr)
			// V3 FDE Index 为 16 字节，V2 为 20 字节
			fdeEntrySize := 20
			if r.sframeData.Header.Version >= 3 {
				fdeEntrySize = 16
			}
			var bestFn *SFrameFunction
			var bestFnStart uint64
			var num int
			for i := range r.sframeData.Functions {
				fn := &r.sframeData.Functions[i]

				// 跳过 PCMASK 类型的 FDE（PLT stub 专用）
				// V2/V3: 均通过 FuncInfo bit 4 判断
				if (fn.FuncInfo>>4)&0x01 == SFRAME_FDE_PCTYPE_MASK {
					debugLog("[DEBUG] findSFrameFunction: 跳过PCMASK FDE[%d] (FuncInfo=0x%x)\n", i, fn.FuncInfo)
					continue
				}

				// 当前FDE的文件偏移
				fdeOffset := fdeArrayStart + i*fdeEntrySize

				// 计算函数起始地址
				var fnStartVirtAddr uint64

				if r.sframeData.Header.Flags&SFRAME_F_FDE_FUNC_START_PCREL != 0 {
					// PC-relative: StartAddr是相对于FDE的sfde_func_start_address字段本身的偏移
					fdeFieldAddr := r.sframeData.sectionAddr + uint64(fdeOffset)
					fnStartVirtAddr = uint64(int64(fdeFieldAddr) + int64(fn.StartAddr))
				} else {
					// Absolute: StartAddr是相对于.sframe节开始的偏移
					fnStartVirtAddr = r.sframeData.sectionAddr + uint64(int64(fn.StartAddr))
				}

				fnEndVirtAddr := fnStartVirtAddr + uint64(fn.Size)

				// 转换为运行时地址并比较
				fnStartRuntimeAddr := r.baseAddr + (fnStartVirtAddr - r.baseOffset)
				fnEndRuntimeAddr := r.baseAddr + (fnEndVirtAddr - r.baseOffset)

				debugLog("[DEBUG] findSFrameFunction: PCINC FDE[%d] 范围[0x%x, 0x%x), size=%d (StartAddr_pcrel=0x%x, virtStart=0x%x)\n",
					i, fnStartRuntimeAddr, fnEndRuntimeAddr, fn.Size, uint64(fn.StartAddr), fnStartVirtAddr)

				if pc >= fnStartRuntimeAddr && pc < fnEndRuntimeAddr {
					debugLog("[DEBUG] findSFrameFunction: 候选主程序SFrame函数 @ 0x%x (virtual=0x%x), PC=0x%x, size=%d\n",
						fnStartRuntimeAddr, fnStartVirtAddr, pc, fn.Size)
					// 选起始地址最大的那个（最精确覆盖）
					if fnStartRuntimeAddr > bestFnStart {
						bestFnStart = fnStartRuntimeAddr
						bestFn = fn
						num = i
					}
				}
			}
			if bestFn != nil {
				debugLog("[DEBUG] findSFrameFunction: 找到主程序SFrame函数 #@%d @ 0x%x, PC=0x%x, size=%d\n",
					num, bestFnStart, pc, bestFn.Size)
				return bestFn, bestFnStart
			}
			debugLog("[DEBUG] findSFrameFunction: 主程序中未找到匹配的PCINC SFrame函数，回退FP (PC=0x%x)\n", pc)
		}
		return nil, 0
	}

	// 检查共享库
	for i := range r.mappings {
		if pc >= r.mappings[i].StartAddr && pc < r.mappings[i].EndAddr {
			if r.mappings[i].SFrameData != nil && r.mappings[i].SFrameData.hasData {
				debugLog("[DEBUG] findSFrameFunction: 共享库地址 PC=0x%x, lib=%s\n",
					pc, r.mappings[i].Path)

				// 常量定义
				const SFRAME_F_FDE_FUNC_START_PCREL = 0x4

				// 计算FDE数组的起始位置（在文件中）
				headerSize := 28 + int(r.mappings[i].SFrameData.Header.AuxHdrLen)
				fdeArrayStart := headerSize + int(r.mappings[i].SFrameData.Header.FDEOff)

				// 在共享库SFrame函数列表中查找，只选 PCINC 类型（跳过 PCMASK/PLT stub）
				// V3 FDE Index 为 16 字节，V2 为 20 字节
				libFDEEntrySize := 20
				if r.mappings[i].SFrameData.Header.Version >= 3 {
					libFDEEntrySize = 16
				}
				var bestLibFn *SFrameFunction
				var bestLibFnStart uint64
				var num int
				for j := range r.mappings[i].SFrameData.Functions {
					fn := &r.mappings[i].SFrameData.Functions[j]

					// 跳过 PCMASK 类型的 FDE (V2/V3 均通过 FuncInfo bit 4 判断)
					if (fn.FuncInfo>>4)&0x01 == SFRAME_FDE_PCTYPE_MASK {
						continue
					}

					// 当前FDE的文件偏移
					fdeOffset := fdeArrayStart + j*libFDEEntrySize

					// 计算函数起始地址
					var fnStartVirtAddr uint64

					if r.mappings[i].SFrameData.Header.Flags&SFRAME_F_FDE_FUNC_START_PCREL != 0 {
						// PC-relative: StartAddr是相对于FDE的sfde_func_start_address字段本身的偏移
						fdeFieldAddr := r.mappings[i].SFrameData.sectionAddr + uint64(fdeOffset)
						fnStartVirtAddr = uint64(int64(fdeFieldAddr) + int64(fn.StartAddr))
					} else {
						// Absolute: StartAddr是相对于.sframe节开始的偏移
						fnStartVirtAddr = r.mappings[i].SFrameData.sectionAddr + uint64(int64(fn.StartAddr))
					}

					fnEndVirtAddr := fnStartVirtAddr + uint64(fn.Size)

					// 转换为运行时地址并比较
					fnStartRuntimeAddr := r.mappings[i].StartAddr + (fnStartVirtAddr - r.mappings[i].Offset)
					fnEndRuntimeAddr := r.mappings[i].StartAddr + (fnEndVirtAddr - r.mappings[i].Offset)

					if pc >= fnStartRuntimeAddr && pc < fnEndRuntimeAddr {
						if fnStartRuntimeAddr > bestLibFnStart {
							bestLibFnStart = fnStartRuntimeAddr
							bestLibFn = fn
							num = j
						}
					}
				}
				if bestLibFn != nil {
					debugLog("[DEBUG] findSFrameFunction: 找到共享库SFrame函数 #@%d @ 0x%x, size=%d, lib=%s\n",
						num, bestLibFnStart, bestLibFn.Size, r.mappings[i].Path)
					return bestLibFn, bestLibFnStart
				}
			} else {
				debugLog("[DEBUG] findSFrameFunction: 共享库无SFrame数据 lib=%s\n", r.mappings[i].Path)
			}
			return nil, 0
		}
	}

	debugLog("[DEBUG] findSFrameFunction: PC=0x%x 不在任何映射范围内\n", pc)
	return nil, 0
}

// unwindFrameWithSFrame 使用SFrame信息展开栈帧
func (r *SFrameResolver) unwindFrameWithSFrame(ctx *UnwindContext) error {
	// 查找当前PC对应的SFrame函数
	sframeFunc, fnStartAddr := r.findSFrameFunction(ctx.PC)
	if sframeFunc == nil {
		return fmt.Errorf("no SFrame info for PC 0x%x", ctx.PC)
	}

	// 计算PC在函数内的偏移
	var pcOffset uint64
	var sframeData *SFrameData

	// fnStartAddr现在统一为运行时地址，直接计算偏移
	pcOffset = ctx.PC - fnStartAddr

	// 确定是主程序还是共享库，获取对应的SFrame数据
	if ctx.PC >= r.baseAddr && ctx.PC < r.baseAddrEnd {
		sframeData = r.sframeData
	} else {
		// 在共享库中查找
		for i := range r.mappings {
			if ctx.PC >= r.mappings[i].StartAddr && ctx.PC < r.mappings[i].EndAddr {
				sframeData = r.mappings[i].SFrameData
				break
			}
		}
	}

	// 查找对应的FDE
	var sectionData []byte
	if sframeData != nil {
		sectionData = sframeData.sectionData
	}
	fde := findFDEForFunction(sframeFunc, sframeData, pcOffset, sectionData)

	// V3: findFDEForFunction 返回 nil 表示到达最外层帧（DEFAULT + NumFREs=0）
	if fde == nil && sframeData != nil && sframeData.Header.Version >= 3 {
		return fmt.Errorf("outermost frame reached (V3 DEFAULT FDE with no FREs)")
	}

	// 没有 FDE，无法用 SFrame 展开，让调用者回退到 FP
	if fde == nil {
		return fmt.Errorf("no FDE found for PC 0x%x", ctx.PC)
	}

	// 从 FRE Info Word 的 bit 0 确定 CFA 基寄存器
	// SFrame spec: bit 0 = 0: FP-based (SFRAME_BASE_REG_FP=0), bit 0 = 1: SP-based (SFRAME_BASE_REG_SP=1)
	useFPBased := (fde.FREInfo & 0x01) == SFRAME_FRE_CFA_BASE_REG_FP

	debugLog("[DEBUG] unwindFrameWithSFrame: 函数size=%d, pcOffset=0x%x, useFPBased=%v (from FRE)\n",
		sframeFunc.Size, pcOffset, useFPBased)

	var cfa uint64 // Canonical Frame Address

	if !useFPBased {
		// SP-based CFA。SFrame 规范：CFA = SP + offset1，offset1 直接存为正数（如 sp+8、sp+16）。
		if fde != nil && fde.CFAOffset != 0 {
			cfa = uint64(int64(ctx.SP) + int64(fde.CFAOffset))
			debugLog("[DEBUG] unwindFrameWithSFrame: SP-based CFA (来自FDE), CFA=0x%x (SP=0x%x + %d)\n",
				cfa, ctx.SP, fde.CFAOffset)
		} else {
			// 无 FDE 信息，使用默认偏移：call 指令 push 了返回地址(8字节)，栈深度=1
			cfa = ctx.SP + 8
			debugLog("[DEBUG] unwindFrameWithSFrame: SP-based CFA (使用默认偏移), CFA=0x%x (SP=0x%x + 8)\n",
				cfa, ctx.SP)
		}
	} else {
		// FP-based CFA。SFrame 规范：CFA = FP + offset1，offset1 直接存为正数（如 fp+16）。
		if fde != nil && fde.CFAOffset != 0 {
			cfa = uint64(int64(ctx.BP) + int64(fde.CFAOffset))
			debugLog("[DEBUG] unwindFrameWithSFrame: FP-based CFA (来自FDE), CFA=0x%x (BP=0x%x + %d)\n",
				cfa, ctx.BP, fde.CFAOffset)
		} else {
			cfa = ctx.BP + 16
			debugLog("[DEBUG] unwindFrameWithSFrame: FP-based CFA (使用默认偏移), CFA=0x%x (BP=0x%x + 16)\n",
				cfa, ctx.BP)
		}
	}

	// 验证CFA的合理性
	if cfa < 0x1000 || cfa <= ctx.SP {
		return fmt.Errorf("invalid CFA: 0x%x (SP=0x%x)", cfa, ctx.SP)
	}

	// 读取返回地址
	// 对于SP-based: 返回地址在CFA-8的位置（即当前SP位置，因为CFA=SP+8）
	// 对于FP-based: 返回地址在CFA-8的位置（即BP+8）
	raOffset := int32(-8)
	if fde.RAOffset != 0 {
		raOffset = fde.RAOffset
	}

	retAddrLoc := uint64(int64(cfa) + int64(raOffset))

	// 对于SP-based unwinding，如果CFA=SP+8，则retAddrLoc=SP+8-8=SP
	// 这意味着返回地址就在当前栈顶
	debugLog("[DEBUG] unwindFrameWithSFrame: 读取返回地址，位置=0x%x (CFA=0x%x, RAOffset=%d)\n",
		retAddrLoc, cfa, raOffset)

	retAddr, err := r.readUint64WithCtx(retAddrLoc, ctx)
	if err != nil {
		return fmt.Errorf("failed to read return address at 0x%x: %w", retAddrLoc, err)
	}

	// 读取保存的 BP（如果有 FPOffset 信息）
	var newBP uint64
	if fde.FPOffset != 0 {
		// FPOffset 是相对于 CFA 的偏移: FP = CFA + FPOffset
		fpLoc := uint64(int64(cfa) + int64(fde.FPOffset))
		debugLog("[DEBUG] unwindFrameWithSFrame: 计算 FP 位置 = CFA + FPOffset, CFA=0x%x, FPOffset=%d, FP_location=0x%x\n",
			cfa, fde.FPOffset, fpLoc)
		newBP, err = r.readUint64WithCtx(fpLoc, ctx)
		if err != nil {
			debugLog("[DEBUG] unwindFrameWithSFrame: 读取BP失败 at 0x%x: %v\n", fpLoc, err)
			newBP = ctx.BP
		} else {
			debugLog("[DEBUG] unwindFrameWithSFrame: 读取保存的BP=0x%x (from CFA+%d=0x%x)\n",
				newBP, fde.FPOffset, fpLoc)
		}
	} else if useFPBased {
		// FP-based 帧未记录 FPOffset，按 x86-64 标准布局从 [BP+0] 读取 caller BP
		savedBP, bpErr := r.readUint64WithCtx(ctx.BP, ctx)
		if bpErr != nil {
			debugLog("[DEBUG] unwindFrameWithSFrame: FP-based 读取 [BP+0] 失败: %v，保持旧BP\n", bpErr)
			newBP = ctx.BP
		} else {
			debugLog("[DEBUG] unwindFrameWithSFrame: FP-based 从 [BP+0] 读取 caller BP=0x%x\n", savedBP)
			newBP = savedBP
		}
	} else {
		// SP-based 无 FPOffset，保持当前 BP
		newBP = ctx.BP
		debugLog("[DEBUG] unwindFrameWithSFrame: SP-based 无FPOffset，保持BP=0x%x\n", ctx.BP)
	}

	debugLog("[DEBUG] unwindFrameWithSFrame: retAddr=0x%x, newBP=0x%x, newSP=0x%x\n",
		retAddr, newBP, cfa)

	// 更新上下文
	ctx.PC = retAddr
	ctx.SP = cfa
	ctx.BP = newBP

	return nil
}

// UnwindStackFromContext 从指定的上下文开始执行栈回溯
// 允许从任意PC地址开始回溯，而不是只能从当前进程状态开始
func (r *SFrameResolver) UnwindStackFromContext(ctx *UnwindContext, maxFrames int) ([]StackFrame, error) {
	if maxFrames <= 0 {
		maxFrames = 32 // 默认最大32帧
	}

	// 验证上下文的有效性
	if ctx.PC == 0 || ctx.SP == 0 {
		return nil, fmt.Errorf("invalid context: PC=0x%x, SP=0x%x", ctx.PC, ctx.SP)
	}

	// 创建上下文的副本，避免修改原始上下文
	contextCopy := &UnwindContext{
		PC:            ctx.PC,
		SP:            ctx.SP,
		BP:            ctx.BP,
		StackBase:     ctx.StackBase,
		StackSnapshot: ctx.StackSnapshot, // 只读引用，无需深拷贝
	}

	return r.doUnwindStack(contextCopy, maxFrames)
}

// UnwindStack 从进程当前状态执行栈回溯
func (r *SFrameResolver) UnwindStack(maxFrames int) ([]StackFrame, error) {
	if maxFrames <= 0 {
		maxFrames = 32 // 默认最大32帧
	}

	// 获取初始寄存器状态
	ctx, err := r.GetRegisters()
	if err != nil {
		return nil, fmt.Errorf("failed to get registers: %w", err)
	}

	return r.doUnwindStack(ctx, maxFrames)
}

// doUnwindStack 执行实际的栈回溯逻辑
func (r *SFrameResolver) doUnwindStack(ctx *UnwindContext, maxFrames int) ([]StackFrame, error) {

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

		// 展开到下一帧 - 优先使用SFrame，如果失败则回退到FP
		err := r.unwindFrameWithSFrame(ctx)
		if err != nil {
			debugLog("[DEBUG] UnwindStack: SFrame展开失败，尝试FP: %v\n", err)
			// 回退到基于帧指针的展开
			if err := r.unwindFrameWithFP(ctx); err != nil {
				debugLog("[DEBUG] UnwindStack: FP展开也失败: %v\n", err)
				break
			}
			debugLog("[DEBUG] UnwindStack: 使用FP展开成功\n")
		} else {
			debugLog("[DEBUG] UnwindStack: 使用SFrame展开成功\n")
		}
	}

	debugLog("[DEBUG] UnwindStack: 总共展开了 %d 帧\n", len(frames))
	return frames, nil
}

// UnwindStackWithSFrameFromContext 从指定上下文开始，仅使用SFrame执行栈回溯（不回退到FP）
func (r *SFrameResolver) UnwindStackWithSFrameFromContext(ctx *UnwindContext, maxFrames int) ([]StackFrame, error) {
	if maxFrames <= 0 {
		maxFrames = 32
	}

	if r.sframeData == nil || !r.sframeData.hasData {
		return nil, fmt.Errorf("no SFrame data available")
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

	return r.doUnwindStackWithSFrame(contextCopy, maxFrames)
}

// UnwindStackWithSFrame 从进程当前状态开始，仅使用SFrame执行栈回溯（不回退到FP）
func (r *SFrameResolver) UnwindStackWithSFrame(maxFrames int) ([]StackFrame, error) {
	if maxFrames <= 0 {
		maxFrames = 32
	}

	if r.sframeData == nil || !r.sframeData.hasData {
		return nil, fmt.Errorf("no SFrame data available")
	}

	// 获取初始寄存器状态
	ctx, err := r.GetRegisters()
	if err != nil {
		return nil, fmt.Errorf("failed to get registers: %w", err)
	}

	return r.doUnwindStackWithSFrame(ctx, maxFrames)
}

// doUnwindStackWithSFrame 执行实际的SFrame栈回溯逻辑
func (r *SFrameResolver) doUnwindStackWithSFrame(ctx *UnwindContext, maxFrames int) ([]StackFrame, error) {

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
		debugLog("[DEBUG] UnwindStackWithSFrame: Frame %d: PC=0x%x, SP=0x%x, BP=0x%x\n",
			i, frame.PC, frame.SP, frame.BP)

		// 优先使用SFrame展开；若该帧所在库无SFrame数据（如libc等），
		// 则用FP过渡展开，直到进入有SFrame数据的帧
		if err := r.unwindFrameWithSFrame(ctx); err != nil {
			debugLog("[DEBUG] UnwindStackWithSFrame: SFrame展开失败，尝试FP过渡: %v\n", err)
			if fpErr := r.unwindFrameWithFP(ctx); fpErr != nil {
				debugLog("[DEBUG] UnwindStackWithSFrame: FP展开也失败: %v\n", fpErr)
				break
			}
			debugLog("[DEBUG] UnwindStackWithSFrame: FP过渡展开成功\n")
		}
	}

	debugLog("[DEBUG] UnwindStackWithSFrame: 总共展开了 %d 帧\n", len(frames))
	return frames, nil
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
