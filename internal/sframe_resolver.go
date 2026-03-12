package lbr

import (
	"debug/elf"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
)

// TODO: 1.PC_MASK部分解析有问题
// TODO: 2.PTL的解析有问题
// TODO: 3.Flex类型的FDE解析未实现
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

// parseFREInfo 解析 FRE Info Word
// 位布局（SFrame V3 规范 §2.4.1，From MSB to LSB）：
//
//	bit  0   : fre_cfa_base_reg_id (0=FP, 1=SP)
//	bits 1-4 : fre_dataword_count  SFRAME_FRE_INFO_NUM_OFFSETS_SHIFT=1, MASK=0x1E
//	bits 5-6 : fre_dataword_size   SFRAME_FRE_INFO_DATAWORD_SIZE_SHIFT=5, MASK=0x60
//	bit  7   : fre_mangled_ra_p
//
// 注意：V2 旧布局为 bits 1-2=size、bits 3-6=count，V3 将两者互换。
func parseFREInfo(freInfo uint8) (cfaBaseReg uint8, offsetSize uint8, offsetCount uint8, mangledRA bool) {
	// bit 0: CFA base register (0=FP, 1=SP)
	cfaBaseReg = freInfo & 0x01
	// bits 1-4: fre_dataword_count（最多15个数据字）
	offsetCount = (freInfo >> 1) & 0x0F
	// bits 5-6: fre_dataword_size (0=1B, 1=2B, 2=4B)
	offsetSize = (freInfo >> 5) & 0x03
	// bit 7: mangled RA flag
	mangledRA = (freInfo>>7)&0x01 != 0
	return
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

// parseSFrameDataFromELF 从 ELF 文件中解析 .sframe 节并返回 SFrameData。
// loadSFrameData 和 loadLibraryMapping 均通过此函数复用解析逻辑。
func parseSFrameDataFromELF(elfFile *elf.File) (*SFrameData, error) {
	section := elfFile.Section(".sframe")
	if section == nil {
		return nil, fmt.Errorf("no .sframe section found")
	}

	data, err := section.Data()
	if err != nil {
		return nil, fmt.Errorf("failed to read .sframe section: %w", err)
	}

	if len(data) < 28 {
		return nil, fmt.Errorf("invalid .sframe section size: need at least 28 bytes, got %d", len(data))
	}

	sframe := &SFrameData{
		hasData:     true,
		sectionAddr: section.Addr,
		sectionData: data,
	}

	// 解析 SFrame Preamble (4字节)
	sframe.Header.Magic = binary.LittleEndian.Uint16(data[0:2])
	sframe.Header.Version = data[2]
	sframe.Header.Flags = data[3]

	// 解析 SFrame Header (从偏移4开始)
	sframe.Header.ABI = data[4]
	sframe.Header.FixedFPOffset = int8(data[5])
	sframe.Header.FixedRAOffset = int8(data[6])
	sframe.Header.AuxHdrLen = data[7]
	sframe.Header.NumFDEs = binary.LittleEndian.Uint32(data[8:12])
	sframe.Header.NumFREs = binary.LittleEndian.Uint32(data[12:16])
	sframe.Header.FRELen = binary.LittleEndian.Uint32(data[16:20])
	sframe.Header.FDEOff = binary.LittleEndian.Uint32(data[20:24])
	sframe.Header.FREOff = binary.LittleEndian.Uint32(data[24:28])

	if sframe.Header.Magic != SFrameMagic {
		return nil, fmt.Errorf("invalid SFrame magic number: got 0x%x, expected 0x%x",
			sframe.Header.Magic, SFrameMagic)
	}
	debugLog("sframe.Header.Magic : 0x%x", sframe.Header.Magic)
	debugLog("sframe.Header.Version : %d", sframe.Header.Version)
	debugLog("sframe.Header.Flags : 0x%x", sframe.Header.Flags)
	debugLog("sframe.Header.ABI : %d (%s)", sframe.Header.ABI, GetABIDescription(sframe.Header.ABI))
	debugLog("sframe.Header.FixedFPOffset : %d", sframe.Header.FixedFPOffset)
	debugLog("sframe.Header.FixedRAOffset : %d", sframe.Header.FixedRAOffset)
	debugLog("sframe.Header.AuxHdrLen : %d", sframe.Header.AuxHdrLen)
	debugLog("sframe.Header.NumFDEs : %d", sframe.Header.NumFDEs)
	debugLog("sframe.Header.NumFREs : %d", sframe.Header.NumFREs)
	debugLog("sframe.Header.FRELen : %d", sframe.Header.FRELen)
	debugLog("sframe.Header.FDEOff : %d", sframe.Header.FDEOff)
	debugLog("sframe.Header.FREOff : %d", sframe.Header.FREOff)

	// 解析函数条目 (Function Descriptor Entries)
	// V2格式: 20字节/FDE (int32 StartAddr + uint32 Size + uint32 StartFREOff + uint32 NumFREs + uint8 FuncInfo + uint8 RepSize + uint16 Padding)
	// V3格式: 16字节/FDE (int64 StartAddr + uint32 Size + uint32 StartFREOff; 无NumFREs/FuncInfo字段)
	headerSize := 28 + int(sframe.Header.AuxHdrLen)
	fdeStartOffset := headerSize + int(sframe.Header.FDEOff)
	functionEntrySize := 20
	if sframe.Header.Version >= 3 {
		functionEntrySize = 16
	}

	if fdeStartOffset >= len(data) {
		return nil, fmt.Errorf("FDE offset %d exceeds data length %d", fdeStartOffset, len(data))
	}

	offset := fdeStartOffset
	for i := uint32(0); i < sframe.Header.NumFDEs && offset+functionEntrySize <= len(data); i++ {
		var fn SFrameFunction
		if sframe.Header.Version >= 3 {
			fn = SFrameFunction{
				StartAddr:   int64(binary.LittleEndian.Uint64(data[offset : offset+8])),
				Size:        binary.LittleEndian.Uint32(data[offset+8 : offset+12]),
				StartFREOff: binary.LittleEndian.Uint32(data[offset+12 : offset+16]),
				// NumFREs/FuncInfo/RepSize 在 V3 FDE 中不存在; FREByteLen 稍后由 sframeComputeFREByteLens 计算
			}
			// 打印解析后的 VA（PCREL: fdeFieldAddr + StartAddr；非PCREL: sectionAddr + StartAddr）
			{
				const SFRAME_F_FDE_FUNC_START_PCREL_INNER = 0x4
				var displayVA uint64
				if sframe.Header.Flags&SFRAME_F_FDE_FUNC_START_PCREL_INNER != 0 {
					fdeFieldAddr := sframe.sectionAddr + uint64(fdeStartOffset+int(i)*functionEntrySize)
					displayVA = uint64(int64(fdeFieldAddr) + fn.StartAddr)
				} else {
					displayVA = sframe.sectionAddr + uint64(fn.StartAddr)
				}
				debugLog("StartAddr=0x%x(pcrel_raw=0x%x), Size=%d, StartFREOff=%d\n", displayVA, uint64(fn.StartAddr), fn.Size, fn.StartFREOff)
			}
		} else {
			fn = SFrameFunction{
				StartAddr:   int64(int32(binary.LittleEndian.Uint32(data[offset : offset+4]))),
				Size:        binary.LittleEndian.Uint32(data[offset+4 : offset+8]),
				StartFREOff: binary.LittleEndian.Uint32(data[offset+8 : offset+12]),
				NumFREs:     binary.LittleEndian.Uint32(data[offset+12 : offset+16]),
				FuncInfo:    data[offset+16],
				RepSize:     data[offset+17],
				Padding:     binary.LittleEndian.Uint16(data[offset+18 : offset+20]),
			}
		}
		sframe.Functions = append(sframe.Functions, fn)
		offset += functionEntrySize
	}

	// V3: FDE Index 无 NumFREs/FuncInfo 字段，通过相邻 StartFREOff 差值计算总字节范围
	if sframe.Header.Version >= 3 {
		sframeComputeFREByteLens(sframe.Functions, sframe.Header.FRELen)

		// V3: 解析 sframe_func_desc_attr (5字节) —— 位于每个函数FRE数据的开头
		// 布局: uint16 sfda_func_num_fres | uint8 sfda_func_info | uint8 sfda_func_info2 | uint8 sfda_func_rep_size
		freSubStart := headerSize + int(sframe.Header.FREOff)
		for i := range sframe.Functions {
			fn := &sframe.Functions[i]
			attrOff := freSubStart + int(fn.StartFREOff)
			if attrOff+5 > len(data) {
				debugLog("[DEBUG] loadSFrameData: V3 FDE[%d] 属性超出范围: attrOff=%d, dataLen=%d\n", i, attrOff, len(data))
				continue
			}
			fn.NumFREs = uint32(binary.LittleEndian.Uint16(data[attrOff : attrOff+2]))
			fn.FuncInfo = data[attrOff+2]
			fn.FuncInfo2 = data[attrOff+3]
			fn.RepSize = data[attrOff+4]
			debugLog("[DEBUG] parseSFrameDataFromELF: V3 FDE[%d] 属性: NumFREs=%d, FuncInfo=0x%x(fre_type=%d,pctype=%d,signal=%v), FuncInfo2=0x%x(fde_type=%d), RepSize=%d\n",
				i, fn.NumFREs,
				fn.FuncInfo, fn.FuncInfo&0x0F, (fn.FuncInfo>>4)&0x01, (fn.FuncInfo>>7) != 0,
				fn.FuncInfo2, fn.FuncInfo2&0x1F,
				fn.RepSize)
		}
	}

	// 对比验证：用逐条解析法（calcFRESize）重新计算 FREByteLen，与差值法结果比较
	if sframe.Header.Version >= 3 {
		parsedLens := sframeComputeFREByteLensByParsing(sframe.Functions, sframe, data)
		mismatchCount := 0
		for i, fn := range sframe.Functions {
			parsed := parsedLens[i]
			diff := fn.FREByteLen
			if parsed == diff {
				//debugLog("[DEBUG] parseSFrameDataFromELF: FDE[%d] FREByteLen 一致: diff=%d parsed=%d ✓\n", i, diff, parsed)
			} else {
				//debugLog("[DEBUG] parseSFrameDataFromELF: FDE[%d] FREByteLen 不一致: diff=%d parsed=%d ✗ (StartFREOff=%d NumFREs=%d FuncInfo=0x%x)\n",
				//	i, diff, parsed, fn.StartFREOff, fn.NumFREs, fn.FuncInfo)
				mismatchCount++
			}
		}
		if mismatchCount == 0 {
			//debugLog("[DEBUG] parseSFrameDataFromELF: FREByteLen 差值法 vs 解析法 全部一致 (%d 个FDE)\n", len(sframe.Functions))
		} else {
			//debugLog("[DEBUG] parseSFrameDataFromELF: FREByteLen 差值法 vs 解析法 发现 %d 处不一致（共 %d 个FDE）\n",
			//	mismatchCount, len(sframe.Functions))
		}
	}

	debugLog("[DEBUG] parseSFrameDataFromELF: 成功解析 %d 个SFrame FDE, Version=%d\n", len(sframe.Functions), sframe.Header.Version)
	debugLog("[DEBUG] parseSFrameDataFromELF: FRE数据位于偏移 %d, 总长度 %d\n",
		headerSize+int(sframe.Header.FREOff), sframe.Header.FRELen)

	return sframe, nil
}

// loadSFrameData 从ELF文件加载SFrame数据
func (r *SFrameResolver) loadSFrameData() (*SFrameData, error) {
	sframe, err := parseSFrameDataFromELF(r.elfFile)
	if err != nil {
		return nil, err
	}
	debugLog("[DEBUG] loadSFrameData: SFrame Magic=0x%x, Version=%d, ABI=%d (%s), NumFDEs=%d\n",
		sframe.Header.Magic, sframe.Header.Version,
		sframe.Header.ABI, GetABIDescription(sframe.Header.ABI),
		len(sframe.Functions))
	return sframe, nil
}

// loadSymbols 加载符号表（含 PLT 条目）
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

	// PLT 节：每个条目 16 字节，对应一个外部函数调用桩。
	// 动态符号表中的函数符号按字母序存储，与 PLT 槽位顺序一致（slot 0 是 PLT 头，从 slot 1 起对应导入函数）。
	// 特别地，addr2line / nm 等工具也用 @plt 后缀标注这些地址，此处照此惯例。
	if pltSec := r.elfFile.Section(".plt"); pltSec != nil {
		pltAddr := pltSec.Addr
		const pltEntrySize = 16
		// 收集所有 FUNC 类型的动态符号，按地址排序后依次分配 PLT 槽位
		type dynFunc struct {
			name string
		}
		var dynFuncs []dynFunc
		if dynsyms, err := r.elfFile.DynamicSymbols(); err == nil {
			for _, sym := range dynsyms {
				if sym.Name != "" && elf.SymType(sym.Info&0xf) == elf.STT_FUNC {
					dynFuncs = append(dynFuncs, dynFunc{name: sym.Name})
				}
			}
		}
		// PLT slot 0 是 stub（_dl_runtime_resolve），从 slot 1 开始对应导入函数
		for i, fn := range dynFuncs {
			slotAddr := pltAddr + uint64(i+1)*pltEntrySize
			symbols = append(symbols, ElfSymbol{
				Name:  fn.name + "@plt",
				Addr:  slotAddr,
				Size:  pltEntrySize,
				Type:  elf.STT_FUNC,
				Value: slotAddr,
			})
		}
		debugLog("[DEBUG] loadSymbols: 加载了 %d 个PLT符号\n", len(dynFuncs))
	}

	r.symbols = symbols
	debugLog("[DEBUG] loadSymbols: 加载了 %d 个符号（含PLT）\n", len(symbols))
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

	// 尝试加载共享库的SFrame数据
	var sframeData *SFrameData
	if sd, err := parseSFrameDataFromELF(elfFile); err != nil {
		debugLog("[DEBUG] loadLibraryMapping: 加载共享库SFrame数据失败 %s: %v\n", path, err)
	} else {
		sframeData = sd
		debugLog("[DEBUG] loadLibraryMapping: 成功加载共享库SFrame数据 %s, Version=%d, FDE数: %d\n",
			path, sd.Header.Version, len(sd.Functions))
	}

	mapping := MemoryMapping{
		StartAddr:  startAddr,
		EndAddr:    endAddr,
		Offset:     offset,
		Path:       path,
		ElfFile:    elfFile,
		SFrameData: sframeData,
		Symbols:    symbols,
	}

	r.mappings = append(r.mappings, mapping)
	debugLog("[DEBUG] loadLibraryMapping: 加载共享库 %s, 符号数: %d, SFrame: %v\n", path, len(symbols), sframeData != nil)
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
			// 计算相对于库加载基址的偏移（符号表中的地址是相对地址）
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

// sframeComputeFREByteLens 为 SFrame V3 函数列表计算每个函数的 FRE 字节范围。
// V3 FDE 无 NumFREs 字段，通过比较所有函数的 StartFREOff 确定各函数 FRE 数据的字节边界。
func sframeComputeFREByteLens(functions []SFrameFunction, totalFRELen uint32) {
	for i := range functions {
		nextOff := totalFRELen
		for j := range functions {
			if functions[j].StartFREOff > functions[i].StartFREOff && functions[j].StartFREOff < nextOff {
				nextOff = functions[j].StartFREOff
			}
		}
		functions[i].FREByteLen = nextOff - functions[i].StartFREOff
	}
}

// calcFRESize 计算单条 FRE 的字节大小（不解析偏移值语义，仅计算字节数）。
// 布局：[start_addr: addrSize] [fre_info: 1B] [offsets: offsetCount * offsetBytes]
// addrSize 由 freType 决定（0→1B, 1→2B, 2→4B）；
// offsetBytes 和 offsetCount 由 fre_info 字节的 bits[5:6] 和 bits[1:4] 决定。
// 返回 (字节数, 是否成功)。
func calcFRESize(data []byte, offset int, freType uint8) (int, bool) {
	var addrSize int
	switch freType {
	case 0:
		addrSize = 1
	case 1:
		addrSize = 2
	case 2:
		addrSize = 4
	default:
		return 0, false
	}
	if offset+addrSize+1 > len(data) {
		return 0, false
	}
	freInfo := data[offset+addrSize]
	offsetCount := int((freInfo >> 1) & 0x0F)
	offsetSizeCode := (freInfo >> 5) & 0x03
	var offsetBytes int
	switch offsetSizeCode {
	case SFRAME_FRE_DATAWORD_1B:
		offsetBytes = 1
	case SFRAME_FRE_DATAWORD_2B:
		offsetBytes = 2
	case SFRAME_FRE_DATAWORD_4B:
		offsetBytes = 4
	default:
		return 0, false
	}
	total := addrSize + 1 + offsetCount*offsetBytes
	if offset+total > len(data) {
		return 0, false
	}
	return total, true
}

// sframeComputeFREByteLensByParsing 通过逐条解析 FRE 的字节大小来计算每个 V3 函数的
// FRE 字节范围，作为对比验证手段（与 sframeComputeFREByteLens 差值法结果互相校验）。
//
// 对于 V3：每个函数的 FRE 数据以 5 字节 sframe_func_desc_attr 开头，其中含 NumFREs。
// FREByteLen_parsed = 5(attr) + Σ calcFRESize(每条FRE)
//
// 返回值：与 functions 等长的切片，存储逐条解析得到的 FREByteLen；
// 若某函数解析失败，对应位置为 0。
func sframeComputeFREByteLensByParsing(functions []SFrameFunction, sframe *SFrameData, sectionData []byte) []uint32 {
	result := make([]uint32, len(functions))
	if sframe == nil || sframe.Header.Version < 3 {
		return result
	}
	headerSize := 28 + int(sframe.Header.AuxHdrLen)
	freSubStart := headerSize + int(sframe.Header.FREOff)

	for i := range functions {
		fn := &functions[i]
		attrOff := freSubStart + int(fn.StartFREOff)
		if attrOff+5 > len(sectionData) {
			debugLog("[DEBUG] sframeComputeFREByteLensByParsing: FDE[%d] attr 超出范围\n", i)
			continue
		}
		numFREs := int(uint32(sectionData[attrOff]) | uint32(sectionData[attrOff+1])<<8)
		funcInfo := sectionData[attrOff+2]
		freType := funcInfo & 0x0F

		offset := attrOff + 5 // 跳过 sframe_func_desc_attr
		totalFREBytes := 0
		ok := true
		for j := 0; j < numFREs; j++ {
			sz, valid := calcFRESize(sectionData, offset, freType)
			if !valid {
				debugLog("[DEBUG] sframeComputeFREByteLensByParsing: FDE[%d] FRE[%d] 解析失败, offset=%d\n", i, j, offset)
				ok = false
				break
			}
			totalFREBytes += sz
			offset += sz
		}
		if ok {
			result[i] = uint32(5 + totalFREBytes)
		}
	}
	return result
}

// parseFRE 从二进制数据中解析单个 FRE
func parseFRE(data []byte, offset int, freType uint8, abi uint8) (*SFrameFDE, int, error) {
	// freType 决定 start address 字段的大小
	var addrSize int
	var startAddr uint32

	switch freType {
	case 0: // SFRAME_FRE_TYPE_ADDR1
		addrSize = 1
		if offset+1 > len(data) {
			return nil, 0, fmt.Errorf("insufficient data for FRE addr1")
		}
		startAddr = uint32(data[offset])
	case 1: // SFRAME_FRE_TYPE_ADDR2
		addrSize = 2
		if offset+2 > len(data) {
			return nil, 0, fmt.Errorf("insufficient data for FRE addr2")
		}
		startAddr = uint32(binary.LittleEndian.Uint16(data[offset : offset+2]))
	case 2: // SFRAME_FRE_TYPE_ADDR4
		addrSize = 4
		if offset+4 > len(data) {
			return nil, 0, fmt.Errorf("insufficient data for FRE addr4")
		}
		startAddr = binary.LittleEndian.Uint32(data[offset : offset+4])
	default:
		return nil, 0, fmt.Errorf("invalid FRE type: %d", freType)
	}

	offset += addrSize

	// 读取 FRE Info Word (1 字节)
	if offset+1 > len(data) {
		return nil, 0, fmt.Errorf("insufficient data for FRE info")
	}
	freInfo := data[offset]
	offset++

	// 解析 FRE Info Word
	_, offsetSize, offsetCount, _ := parseFREInfo(freInfo)

	// 确定偏移值的字节大小
	var offsetBytes int
	switch offsetSize {
	case SFRAME_FRE_OFFSET_1B:
		offsetBytes = 1
	case SFRAME_FRE_OFFSET_2B:
		offsetBytes = 2
	case SFRAME_FRE_OFFSET_4B:
		offsetBytes = 4
	default:
		return nil, 0, fmt.Errorf("invalid offset size: %d", offsetSize)
	}

	// 读取偏移值
	totalOffsetBytes := int(offsetCount) * offsetBytes
	if offset+totalOffsetBytes > len(data) {
		return nil, 0, fmt.Errorf("insufficient data for FRE offsets")
	}

	// 读取各个偏移值
	offsets := make([]int32, offsetCount)
	for i := 0; i < int(offsetCount); i++ {
		var val int32
		switch offsetBytes {
		case 1:
			val = int32(int8(data[offset]))
			offset++
		case 2:
			val = int32(int16(binary.LittleEndian.Uint16(data[offset : offset+2])))
			offset += 2
		case 4:
			val = int32(binary.LittleEndian.Uint32(data[offset : offset+4]))
			offset += 4
		}
		offsets[i] = val
		debugLog("offsets[%d]: %d\n", i, val)
	}

	// 根据 ABI 解释偏移值
	fde := &SFrameFDE{
		StartOffset: startAddr,
		FREInfo:     freInfo, // 存储 FRE Info Word
	}

	// 第一个偏移始终是 CFA offset
	if offsetCount >= 1 {
		fde.CFAOffset = offsets[0]
	}

	// 根据 ABI 解释剩余偏移
	switch abi {
	case SFRAME_ABI_AMD64_ENDIAN_LITTLE:
		// AMD64: offset1 = CFA, offset2 = FP (如果存在)
		// RA 总是在 CFA-8 (固定)，但 offsetCount==0 表示 RA undefined（最外层帧），不应设置
		// 规范 §1.3 Errata 2: offsetCount==0 => RA is undefined, outermost frame reached
		if offsetCount >= 1 {
			fde.RAOffset = -8
		}
		if offsetCount >= 2 {
			fde.FPOffset = offsets[1]
		}
	case SFRAME_ABI_AARCH64_ENDIAN_LITTLE, SFRAME_ABI_AARCH64_ENDIAN_BIG:
		// AArch64: offset1 = CFA, offset2 = RA, offset3 = FP
		if offsetCount >= 2 {
			fde.RAOffset = offsets[1]
		}
		if offsetCount >= 3 {
			fde.FPOffset = offsets[2]
		}
	case SFRAME_ABI_S390X_ENDIAN_BIG:
		// s390x: offset1 = CFA, offset2 = RA, offset3 = FP
		// 注意：s390x 有特殊的编码方式（寄存器号等）
		if offsetCount >= 2 {
			fde.RAOffset = offsets[1]
		}
		if offsetCount >= 3 {
			fde.FPOffset = offsets[2]
		}
	default:
		// 未知 ABI，使用默认解释
		if offsetCount >= 2 {
			fde.RAOffset = offsets[1]
		}
		if offsetCount >= 3 {
			fde.FPOffset = offsets[2]
		}
	}

	return fde, offset, nil
}

// findFDEForFunction 为函数内的特定PC查找对应的FDE（用于栈展开）
// V2/V3 的 FuncInfo 均在 SFrameFunction 中（V2来自FDE Index, V3来自解析后的sframe_func_desc_attr）
func findFDEForFunction(sframeFunc *SFrameFunction, sframeData *SFrameData, pcOffset uint64, sectionData []byte) *SFrameFDE {
	// pcOffset 是PC相对于函数起始地址的偏移

	debugLog("[DEBUG] findFDEForFunction: pcOffset=0x%x, FuncInfo=0x%x, FuncInfo2=0x%x, StartFREOff=%d, FuncSize=%d, NumFREs=%d, FREByteLen=%d\n",
		pcOffset, sframeFunc.FuncInfo, sframeFunc.FuncInfo2, sframeFunc.StartFREOff, sframeFunc.Size, sframeFunc.NumFREs, sframeFunc.FREByteLen)

	// 如果sframeData为空或没有FRE，无法用SFrame展开，回退到FP
	if sframeData == nil || sectionData == nil || (sframeFunc.NumFREs == 0 && sframeFunc.FREByteLen == 0) {
		debugLog("[DEBUG] findFDEForFunction: 无FRE数据，返回nil\n")
		return nil
	}

	// V3: 检查 FLEX FDE 类型 (sfda_func_info2 bits 0-4)
	// FLEX 类型的 FRE 数据解释与 DEFAULT 完全不同，目前回退到默认 FDE
	if sframeData.Header.Version >= 3 {
		fdeTypeV3 := sframeFunc.FuncInfo2 & 0x1F
		if fdeTypeV3 == SFRAME_FDE_TYPE_FLEX {
			debugLog("[DEBUG] findFDEForFunction: FLEX FDE (sfda_func_info2=0x%x)，无法展开，回退到FP\n", sframeFunc.FuncInfo2)
			return nil
		}
		// V3 outermost frame: DEFAULT 类型 + NumFREs=0 表示最外层帧
		if fdeTypeV3 == SFRAME_FDE_TYPE_DEFAULT && sframeFunc.NumFREs == 0 {
			debugLog("[DEBUG] findFDEForFunction: V3 outermost frame (DEFAULT + NumFREs=0)\n")
			return nil // 返回nil通知调用者到达最外层
		}
	}

	// FRE 类型: bits 0-3 of FuncInfo (V2来自FDE Index; V3来自sframe_func_desc_attr)
	freType := sframeFunc.FuncInfo & 0x0F

	// 计算FRE数据的起始位置。
	// V3: sframe_func_desc_attr (5字节) 位于每函数 FRE 数据开头，需跳过。
	// V2: FRE 数据直接从 StartFREOff 起始，无 attr 头。
	headerSize := 28 + int(sframeData.Header.AuxHdrLen)
	freAttrStart := headerSize + int(sframeData.Header.FREOff) + int(sframeFunc.StartFREOff)
	freDataStart := freAttrStart
	var num uint32
	if sframeData.Header.Version >= 3 {
		freDataStart = freAttrStart + 5 // 跳过 sframe_func_desc_attr
	}

	debugLog("[DEBUG] findFDEForFunction: freType=%d, freAttrStart=%d, freDataStart=%d\n", freType, freAttrStart, freDataStart)

	if freDataStart >= len(sectionData) {
		debugLog("[DEBUG] findFDEForFunction: FRE数据超出范围，返回nil\n")
		return nil
	}

	// 遍历所有 FRE，查找匹配的
	offset := freDataStart
	// 字节结束边界: FREByteLen 覆盖从 freAttrStart 起的全部数据（含V3 attr）
	var freByteEnd int
	if sframeFunc.FREByteLen > 0 {
		freByteEnd = freAttrStart + int(sframeFunc.FREByteLen)
	} else {
		freByteEnd = len(sectionData) // V2 模式：不按字节限制，由 NumFREs 计数控制
	}
	var bestMatch *SFrameFDE

	for i := uint32(0); ; i++ {
		if sframeFunc.NumFREs > 0 && i >= sframeFunc.NumFREs {
			break
		}
		if offset >= freByteEnd {
			break
		}
		fre, newOffset, err := parseFRE(sectionData, offset, freType, sframeData.Header.ABI)
		if err != nil {
			debugLog("[DEBUG] findFDEForFunction: 解析FRE #%d 失败: %v\n", i, err)
			break
		}

		debugLog("[DEBUG] findFDEForFunction: FRE #%d: StartOffset=0x%x, CFAOffset=%d, RAOffset=%d, FPOffset=%d\n",
			i, fre.StartOffset, fre.CFAOffset, fre.RAOffset, fre.FPOffset)

		// 检查是否匹配
		// FuncInfo bit 4 决定 PC 类型 (0=PCINC, 1=PCMASK)，V2/V3 均如此
		var isMatch bool
		pcType := (sframeFunc.FuncInfo >> 4) & 0x01
		if pcType == SFRAME_FDE_PCTYPE_INC {
			// SFRAME_FDE_PCTYPE_INC: startOffset <= pcOffset 的最后一个 FRE
			isMatch = uint64(fre.StartOffset) <= pcOffset
		} else {
			// SFRAME_FDE_PCTYPE_MASK: PC % REP_BLOCK_SIZE >= FRE_START_ADDR
			if sframeFunc.RepSize > 0 {
				pcInBlock := pcOffset % uint64(sframeFunc.RepSize)
				isMatch = uint64(fre.StartOffset) <= pcInBlock
			}
		}
		if isMatch {
			num = i
			bestMatch = fre
		}

		offset = newOffset
	}

	if bestMatch != nil {
		// 解析 CFA base register 从 FRE Info Word
		cfaBaseReg := bestMatch.FREInfo & 0x01
		cfaBaseStr := "SP"
		if cfaBaseReg == SFRAME_FRE_CFA_BASE_REG_FP {
			debugLog("[DEBUG] findFDEForFunction: CFA_BASE=FP")
			cfaBaseStr = "FP"
		}
		debugLog("[DEBUG] findFDEForFunction: 找到匹配的FRE#%d, CFA_BASE=%s, CFAOffset=%d, RAOffset=%d, FPOffset=%d\n",
			num, cfaBaseStr, bestMatch.CFAOffset, bestMatch.RAOffset, bestMatch.FPOffset)
		return bestMatch
	}

	// 没有找到匹配的FRE，无法用SFrame展开，回退到FP
	debugLog("[DEBUG] findFDEForFunction: 未找到匹配的FRE，返回nil\n")
	return nil
}

// findSymbol 在主程序符号表中查找符号
func (r *SFrameResolver) findSymbol(addr uint64) string {
	return r.findSymbolInList(addr, r.symbols)
}

// findSymbolInList 在符号列表中查找符号
func (r *SFrameResolver) findSymbolInList(addr uint64, symbols []ElfSymbol) string {
	var bestMatch string
	var bestDist uint64 = ^uint64(0)
	var bestInRange string
	var bestInRangeDist uint64 = ^uint64(0)
	var candidatesChecked int

	// 对于超出符号范围的情况，限制更严格的距离阈值
	// 因为ELF符号表中的size信息不总是准确的，但距离太远的匹配很可能是错误的
	const maxOutOfRangeDistance = 0x100 // 256字节

	for i := range symbols {
		sym := &symbols[i]

		// 检查符号类型（接受函数、无类型、对象等）
		// 不过滤类型，因为有些有效符号可能是其他类型
		_ = sym.Type // 保留类型字段以便后续可能的过滤

		// 检查地址是否在符号范围内
		if addr >= sym.Addr {
			candidatesChecked++
			dist := addr - sym.Addr

			// 调试：记录接近的符号
			if dist < 0x1000 {
				//debugLog("[DEBUG] findSymbolInList: 候选符号 %s @ 0x%x, size=%d, dist=0x%x\n",
				//	sym.Name, sym.Addr, sym.Size, dist)
			}

			// 优先选择在符号范围内的
			if sym.Size > 0 && dist < sym.Size {
				if dist < bestInRangeDist {
					bestInRangeDist = dist
					bestInRange = sym.Name
				}
			}

			// 记录最近的符号作为后备（但对超出范围的符号使用更严格的距离限制）
			// 如果符号有明确的size且地址超出范围，则限制在256字节内
			// 如果符号没有size信息（size=0），则允许更大的距离（4KB）
			maxDist := uint64(0x1000) // 默认4KB
			if sym.Size > 0 && dist >= sym.Size {
				// 地址超出符号范围，使用更严格的限制
				maxDist = maxOutOfRangeDistance
			}

			if dist < maxDist && dist < bestDist {
				bestDist = dist
				bestMatch = sym.Name
			}
		}
	}

	// 优先使用范围内的匹配
	if bestInRange != "" {
		//debugLog("[DEBUG] findSymbolInList: 目标地址=0x%x, 检查了%d个候选符号, 最佳匹配(范围内)=%s, 距离=0x%x\n",
		//	addr, candidatesChecked, bestInRange, bestInRangeDist)
		if bestInRangeDist > 0 {
			return fmt.Sprintf("%s+0x%x", bestInRange, bestInRangeDist)
		}
		return bestInRange
	}

	// 使用最近的符号（如果距离在合理范围内）
	if bestMatch != "" && bestDist != ^uint64(0) {
		//debugLog("[DEBUG] findSymbolInList: 目标地址=0x%x, 检查了%d个候选符号, 最佳匹配(最近)=%s, 距离=0x%x\n",
		//	addr, candidatesChecked, bestMatch, bestDist)
		if bestDist > 0 {
			return fmt.Sprintf("%s+0x%x", bestMatch, bestDist)
		}
		return bestMatch
	}

	// 没有找到合适的符号
	//debugLog("[DEBUG] findSymbolInList: 目标地址=0x%x, 检查了%d个候选符号, 未找到合适的符号\n",
	//	addr, candidatesChecked)
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

// readUint64WithCtx 优先从 BPF 栈快照读取，快照不覆盖时回退到 /proc/pid/mem。
// BPF 快照在 uprobe 触发瞬间同步采集，不存在 /proc/pid/mem 异步读取时的
// 栈内容过期（TOCTOU）问题。
func (r *SFrameResolver) readUint64WithCtx(addr uint64, ctx *UnwindContext) (uint64, error) {
	if ctx != nil && len(ctx.StackSnapshot) >= 8 && addr >= ctx.StackBase {
		off := addr - ctx.StackBase
		if off+8 <= uint64(len(ctx.StackSnapshot)) {
			bs := ctx.StackSnapshot[off : off+8]
			val := binary.LittleEndian.Uint64(bs)
			debugLog("[DEBUG] readUint64WithCtx: addr=0x%x off=0x%x bytes=% x val=0x%x\n", addr, off, bs, val)
			return val, nil
		}
	}
	return r.readUint64(addr)
}

// NewUnwindContextFromPC 从PC地址创建栈回溯上下文
// 这个方法会尝试通过读取进程寄存器来获取SP和BP
// 如果无法获取，则只设置PC，SP和BP为0（部分功能可能受限）
func (r *SFrameResolver) NewUnwindContextFromPC(pc uint64) (*UnwindContext, error) {
	ctx := &UnwindContext{
		PC: pc,
	}

	// 尝试获取当前的SP和BP作为参考
	// 注意：这只是一个近似值，可能不准确
	if regs, err := r.GetRegisters(); err == nil {
		ctx.SP = regs.SP
		ctx.BP = regs.BP
		debugLog("[DEBUG] NewUnwindContextFromPC: 使用当前寄存器作为参考: SP=0x%x, BP=0x%x\n", ctx.SP, ctx.BP)
	} else {
		debugLog("[DEBUG] NewUnwindContextFromPC: 无法获取寄存器，SP和BP将为0\n")
	}

	return ctx, nil
}

// NewUnwindContextFromRegs 从完整的寄存器信息创建栈回溯上下文
func NewUnwindContextFromRegs(pc, sp, bp uint64) *UnwindContext {
	return &UnwindContext{
		PC: pc,
		SP: sp,
		BP: bp,
	}
}

// GetRegisters 获取进程寄存器（使用ptrace）
func (r *SFrameResolver) GetRegisters() (*UnwindContext, error) {
	// 使用 ptrace 附加到进程
	err := syscall.PtraceAttach(r.pid)
	if err != nil {
		return nil, fmt.Errorf("failed to attach to process: %w", err)
	}
	defer syscall.PtraceDetach(r.pid)

	// 等待进程停止
	var ws syscall.WaitStatus
	_, err = syscall.Wait4(r.pid, &ws, 0, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for process: %w", err)
	}

	// 获取寄存器
	var regs syscall.PtraceRegs
	err = syscall.PtraceGetRegs(r.pid, &regs)
	if err != nil {
		return nil, fmt.Errorf("failed to get registers: %w", err)
	}

	ctx := &UnwindContext{
		PC: regs.Rip,
		SP: regs.Rsp,
		BP: regs.Rbp,
	}

	// 验证地址合理性
	if ctx.SP < 0x1000 || ctx.PC < 0x1000 {
		return nil, fmt.Errorf("invalid register values: SP=0x%x, PC=0x%x, BP=0x%x", ctx.SP, ctx.PC, ctx.BP)
	}

	debugLog("[DEBUG] GetRegisters: PC=0x%x, SP=0x%x, BP=0x%x (via ptrace)\n", ctx.PC, ctx.SP, ctx.BP)
	return ctx, nil
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
