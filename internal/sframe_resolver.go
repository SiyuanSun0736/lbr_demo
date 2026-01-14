package lbr

import (
	"bufio"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
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

// SFrameFunction SFrame函数信息 (V2格式: 20字节)
type SFrameFunction struct {
	StartAddr   int32  // 函数起始地址(相对地址或绝对偏移)
	Size        uint32 // 函数大小
	StartFREOff uint32 // 第一个FRE的偏移
	NumFREs     uint32 // FRE数量
	FuncInfo    uint8  // FDE info word
	RepSize     uint8  // 重复块大小(用于PCMASK类型)
	Padding     uint16 // 填充(V2新增)
}

// SFrameFDE SFrame Frame Description Entry
// 描述函数内特定位置的栈帧布局信息
type SFrameFDE struct {
	StartOffset uint32 // 相对于函数起始的偏移
	FDEInfo     uint8  // FDE信息字节,包含CFA类型和偏移大小
	RepSize     uint32 // 重复次数/大小
	CFAOffset   int32  // CFA偏移量(从SP或BP计算)
	FPOffset    int32  // 帧指针保存位置偏移
	RAOffset    int32  // 返回地址保存位置偏移
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

	sframe := &SFrameData{
		hasData:     true,
		sectionAddr: section.Addr, // 保存.sframe节的虚拟地址
	}

	// 确保有足够的数据来读取完整头部(V2需要28字节)
	if len(data) < 28 {
		return nil, fmt.Errorf("invalid .sframe section size: need at least 28 bytes, got %d", len(data))
	}

	// 解析SFrame Preamble (4字节)
	sframe.Header.Magic = binary.LittleEndian.Uint16(data[0:2])
	sframe.Header.Version = data[2]
	sframe.Header.Flags = data[3]

	// 解析SFrame Header (从偏移4开始)
	sframe.Header.ABI = data[4]
	sframe.Header.FixedFPOffset = int8(data[5])
	sframe.Header.FixedRAOffset = int8(data[6])
	sframe.Header.AuxHdrLen = data[7]
	sframe.Header.NumFDEs = binary.LittleEndian.Uint32(data[8:12])
	sframe.Header.NumFREs = binary.LittleEndian.Uint32(data[12:16])
	sframe.Header.FRELen = binary.LittleEndian.Uint32(data[16:20])
	sframe.Header.FDEOff = binary.LittleEndian.Uint32(data[20:24])
	sframe.Header.FREOff = binary.LittleEndian.Uint32(data[24:28])

	debugLog("[DEBUG] loadSFrameData: SFrame Magic=0x%x, Version=%d, Flags=0x%x, ABI=%d\n",
		sframe.Header.Magic, sframe.Header.Version, sframe.Header.Flags, sframe.Header.ABI)
	debugLog("[DEBUG] loadSFrameData: FixedFPOffset=%d, FixedRAOffset=%d, AuxHdrLen=%d\n",
		sframe.Header.FixedFPOffset, sframe.Header.FixedRAOffset, sframe.Header.AuxHdrLen)
	debugLog("[DEBUG] loadSFrameData: NumFDEs=%d, NumFREs=%d, FRELen=%d, FDEOff=%d, FREOff=%d\n",
		sframe.Header.NumFDEs, sframe.Header.NumFREs, sframe.Header.FRELen,
		sframe.Header.FDEOff, sframe.Header.FREOff)

	// 校验魔数 (SFrame 魔数应该是 0xdee2)
	const SFrameMagic = 0xdee2
	if sframe.Header.Magic != SFrameMagic {
		return nil, fmt.Errorf("invalid SFrame magic number: got 0x%x, expected 0x%x",
			sframe.Header.Magic, SFrameMagic)
	}

	// 解析函数条目 (Function Descriptor Entries)
	// SFrame V2/V3格式: FDE子节从头部后开始(28字节基础头部 + 可能的辅助头部)
	headerSize := 28 + int(sframe.Header.AuxHdrLen)
	fdeStartOffset := headerSize + int(sframe.Header.FDEOff)
	functionEntrySize := 20 // SFrame V2: 4(StartAddr) + 4(Size) + 4(StartFREOff) + 4(NumFREs) + 1(FuncInfo) + 1(RepSize) + 2(Padding)

	if fdeStartOffset >= len(data) {
		return nil, fmt.Errorf("FDE offset %d exceeds data length %d", fdeStartOffset, len(data))
	}

	offset := fdeStartOffset

	for i := uint32(0); i < sframe.Header.NumFDEs && offset+functionEntrySize <= len(data); i++ {
		fn := SFrameFunction{
			StartAddr:   int32(binary.LittleEndian.Uint32(data[offset : offset+4])),
			Size:        binary.LittleEndian.Uint32(data[offset+4 : offset+8]),
			StartFREOff: binary.LittleEndian.Uint32(data[offset+8 : offset+12]),
			NumFREs:     binary.LittleEndian.Uint32(data[offset+12 : offset+16]),
			FuncInfo:    data[offset+16],
			RepSize:     data[offset+17],
			Padding:     binary.LittleEndian.Uint16(data[offset+18 : offset+20]),
		}
		sframe.Functions = append(sframe.Functions, fn)
		offset += functionEntrySize
	}

	debugLog("[DEBUG] loadSFrameData: 成功解析 %d 个SFrame FDE\n", len(sframe.Functions))

	// SFrame V2中，FRE(Frame Row Entries)是实际的栈展开信息
	// FDE(Function Descriptor Entries)只是函数描述符
	// 目前我们已经有了FDE信息，FRE解析可以在需要时进行
	// FRE数据从 headerSize + FREOff 开始
	debugLog("[DEBUG] loadSFrameData: FRE数据位于偏移 %d, 总长度 %d\n",
		headerSize+int(sframe.Header.FREOff), sframe.Header.FRELen)

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

	// 尝试加载共享库的SFrame数据
	var sframeData *SFrameData
	section := elfFile.Section(".sframe")
	if section != nil {
		data, err := section.Data()
		if err == nil && len(data) >= 28 {
			sframe := &SFrameData{
				hasData:     true,
				sectionAddr: section.Addr, // 保存.sframe节的虚拟地址
			}

			// 解析SFrame Preamble (4字节)
			sframe.Header.Magic = binary.LittleEndian.Uint16(data[0:2])
			sframe.Header.Version = data[2]
			sframe.Header.Flags = data[3]

			// 解析SFrame Header (从偏移4开始)
			sframe.Header.ABI = data[4]
			sframe.Header.FixedFPOffset = int8(data[5])
			sframe.Header.FixedRAOffset = int8(data[6])
			sframe.Header.AuxHdrLen = data[7]
			sframe.Header.NumFDEs = binary.LittleEndian.Uint32(data[8:12])
			sframe.Header.NumFREs = binary.LittleEndian.Uint32(data[12:16])
			sframe.Header.FRELen = binary.LittleEndian.Uint32(data[16:20])
			sframe.Header.FDEOff = binary.LittleEndian.Uint32(data[20:24])
			sframe.Header.FREOff = binary.LittleEndian.Uint32(data[24:28])

			const SFrameMagic = 0xdee2
			if sframe.Header.Magic == SFrameMagic {
				// 解析函数条目 (Function Descriptor Entries)
				headerSize := 28 + int(sframe.Header.AuxHdrLen)
				fdeStartOffset := headerSize + int(sframe.Header.FDEOff)
				functionEntrySize := 20 // SFrame V2: 4+4+4+4+1+1+2

				if fdeStartOffset < len(data) {
					offset := fdeStartOffset
					for i := uint32(0); i < sframe.Header.NumFDEs && offset+functionEntrySize <= len(data); i++ {
						fn := SFrameFunction{
							StartAddr:   int32(binary.LittleEndian.Uint32(data[offset : offset+4])),
							Size:        binary.LittleEndian.Uint32(data[offset+4 : offset+8]),
							StartFREOff: binary.LittleEndian.Uint32(data[offset+8 : offset+12]),
							NumFREs:     binary.LittleEndian.Uint32(data[offset+12 : offset+16]),
							FuncInfo:    data[offset+16],
							RepSize:     data[offset+17],
							Padding:     binary.LittleEndian.Uint16(data[offset+18 : offset+20]),
						}
						sframe.Functions = append(sframe.Functions, fn)
						offset += functionEntrySize
					}

					sframeData = sframe
					debugLog("[DEBUG] loadLibraryMapping: 成功加载共享库SFrame数据 %s, Version=%d, FDE数: %d\n",
						path, sframe.Header.Version, len(sframe.Functions))
				} else {
					debugLog("[DEBUG] loadLibraryMapping: FDE偏移超出范围 %s: %d >= %d\n", path, fdeStartOffset, len(data))
				}
			} else {
				debugLog("[DEBUG] loadLibraryMapping: SFrame魔数不匹配 %s: 0x%x\n", path, sframe.Header.Magic)
			}
		} else if err == nil {
			debugLog("[DEBUG] loadLibraryMapping: SFrame数据太小 %s: %d 字节\n", path, len(data))
		}
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

// parseFDEEntries 已废弃 - SFrame V2 使用 FRE (Frame Row Entries) 而不是旧式 FDE
// 保留此注释以说明架构变更

// parseSingleFDE 已废弃 - SFrame V2 使用 FRE (Frame Row Entries)
// 保留此注释以说明架构变更

// createDefaultFDE 为函数创建默认的FDE
// 注意：FuncInfo的低4位是FRE类型，第4位是FDE类型（PCINC/PCMASK）
// CFA base寄存器（FP或SP）的信息在FRE Info Word的bit 0中，不在这里
// 这里使用启发式方法：小函数假设SP-based，大函数假设FP-based
func createDefaultFDE(fn *SFrameFunction) *SFrameFDE {
	fde := &SFrameFDE{
		StartOffset: 0,
		FDEInfo:     0,
		RepSize:     fn.Size,
	}

	// 对于AMD64，RA总是在CFA-8，这是固定的
	fde.RAOffset = -8

	// 对于小函数，假设SP-based (CFA = SP + 8)
	if fn.Size <= 64 {
		// SP-based
		fde.CFAOffset = 8
	} else {
		// FP-based (CFA = BP + 16)
		fde.CFAOffset = 16
		fde.FPOffset = -16 // FP在CFA-16位置
	}

	return fde
}

// findFDEForFunction 为函数内的特定PC查找对应的FDE（用于栈展开）
// 注意：SFrame V2 使用 FRE (Frame Row Entries)，这里我们解析FRE数据
func findFDEForFunction(sframeFunc *SFrameFunction, sframeData *SFrameData, pcOffset uint64) *SFrameFDE {
	// pcOffset 是PC相对于函数起始地址的偏移

	debugLog("[DEBUG] findFDEForFunction: pcOffset=0x%x, FuncInfo=0x%x, StartFREOff=%d, FuncSize=%d, NumFREs=%d\n",
		pcOffset, sframeFunc.FuncInfo, sframeFunc.StartFREOff, sframeFunc.Size, sframeFunc.NumFREs)

	// 如果sframeData为空或没有FRE，使用默认FDE
	if sframeData == nil || sframeFunc.NumFREs == 0 {
		fde := createDefaultFDE(sframeFunc)
		debugLog("[DEBUG] findFDEForFunction: 无FRE数据，使用默认FDE, fpType=%d, CFAOffset=%d\n",
			sframeFunc.FuncInfo&0x0F, fde.CFAOffset)
		return fde
	}

	// 解析FRE数据
	// 根据SFrame规范，FuncInfo低4位包含FRE类型信息
	freType := sframeFunc.FuncInfo & 0x0F

	// FRE类型决定了start address字段的大小
	// 0 = ADDR1 (1字节), 1 = ADDR2 (2字节), 2 = ADDR4 (4字节)
	var addrSize int
	switch freType {
	case 0:
		addrSize = 1
	case 1:
		addrSize = 2
	case 2:
		addrSize = 4
	default:
		// 未知类型，使用默认FDE
		debugLog("[DEBUG] findFDEForFunction: 未知FRE类型 %d，使用默认FDE\n", freType)
		return createDefaultFDE(sframeFunc)
	}

	// 计算FRE数据的起始位置
	// FRE数据位于: 头部 + FREOff + StartFREOff
	headerSize := 28 + int(sframeData.Header.AuxHdrLen)
	freDataStart := headerSize + int(sframeData.Header.FREOff) + int(sframeFunc.StartFREOff)

	debugLog("[DEBUG] findFDEForFunction: 解析FRE, freType=%d, addrSize=%d, freDataStart=%d\n",
		freType, addrSize, freDataStart)

	// 由于完整解析FRE需要读取原始二进制数据且格式复杂
	// 这里使用改进的默认FDE
	// 注意：FuncInfo的bit 4是FDE类型（PCINC/PCMASK），不是FP类型
	// 真正的CFA base寄存器（FP或SP）在FRE Info Word的bit 0中
	fde := createDefaultFDE(sframeFunc)

	debugLog("[DEBUG] findFDEForFunction: 创建默认FDE, CFAOffset=%d, RAOffset=%d, FPOffset=%d\n",
		fde.CFAOffset, fde.RAOffset, fde.FPOffset)
	return fde
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
				debugLog("[DEBUG] findSymbolInList: 候选符号 %s @ 0x%x, size=%d, dist=0x%x\n",
					sym.Name, sym.Addr, sym.Size, dist)
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
		debugLog("[DEBUG] findSymbolInList: 目标地址=0x%x, 检查了%d个候选符号, 最佳匹配(范围内)=%s, 距离=0x%x\n",
			addr, candidatesChecked, bestInRange, bestInRangeDist)
		if bestInRangeDist > 0 {
			return fmt.Sprintf("%s+0x%x", bestInRange, bestInRangeDist)
		}
		return bestInRange
	}

	// 使用最近的符号（如果距离在合理范围内）
	if bestMatch != "" && bestDist != ^uint64(0) {
		debugLog("[DEBUG] findSymbolInList: 目标地址=0x%x, 检查了%d个候选符号, 最佳匹配(最近)=%s, 距离=0x%x\n",
			addr, candidatesChecked, bestMatch, bestDist)
		if bestDist > 0 {
			return fmt.Sprintf("%s+0x%x", bestMatch, bestDist)
		}
		return bestMatch
	}

	// 没有找到合适的符号
	debugLog("[DEBUG] findSymbolInList: 目标地址=0x%x, 检查了%d个候选符号, 未找到合适的符号\n",
		addr, candidatesChecked)
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
			debugLog("[DEBUG] findSFrameFunction: 主程序地址 PC=0x%x, baseAddr=0x%x, 函数数=%d\n",
				pc, r.baseAddr, len(r.sframeData.Functions))

			// 常量定义
			const SFRAME_F_FDE_FUNC_START_PCREL = 0x4

			// 在主程序SFrame函数列表中查找
			// 根据SFrame V2规范：
			// - 如果设置了SFRAME_F_FDE_FUNC_START_PCREL标志，StartAddr是相对于FDE字段本身的偏移
			// - 如果未设置，StartAddr是相对于.sframe节开始的偏移

			// 计算FDE数组的起始位置（在文件中）
			headerSize := 28 + int(r.sframeData.Header.AuxHdrLen)
			fdeArrayStart := headerSize + int(r.sframeData.Header.FDEOff)

			for i := range r.sframeData.Functions {
				fn := &r.sframeData.Functions[i]

				// 当前FDE的文件偏移（每个FDE 20字节）
				fdeOffset := fdeArrayStart + i*20

				// 计算函数起始地址
				var fnStartVirtAddr uint64

				if r.sframeData.Header.Flags&SFRAME_F_FDE_FUNC_START_PCREL != 0 {
					// PC-relative: StartAddr是相对于FDE的sfde_func_start_address字段本身的偏移
					// FDE字段位置 = .sframe节地址 + FDE偏移
					// 函数地址 = FDE字段位置 + StartAddr (作为有符号偏移)
					fdeFieldAddr := r.sframeData.sectionAddr + uint64(fdeOffset)
					fnStartVirtAddr = uint64(int64(fdeFieldAddr) + int64(fn.StartAddr))
					debugLog("[DEBUG] findSFrameFunction: FDE[%d] PCREL mode: fdeFieldAddr=0x%x, StartAddr=%d, fnStartVirtAddr=0x%x\n",
						i, fdeFieldAddr, fn.StartAddr, fnStartVirtAddr)
				} else {
					// Absolute: StartAddr是相对于.sframe节开始的偏移
					fnStartVirtAddr = r.sframeData.sectionAddr + uint64(int64(fn.StartAddr))
					debugLog("[DEBUG] findSFrameFunction: FDE[%d] ABS mode: sectionAddr=0x%x, StartAddr=%d, fnStartVirtAddr=0x%x\n",
						i, r.sframeData.sectionAddr, fn.StartAddr, fnStartVirtAddr)
				}

				fnEndVirtAddr := fnStartVirtAddr + uint64(fn.Size)

				// 转换为运行时地址并比较
				// 运行时地址 = 基址 + (虚拟地址 - 基址偏移)
				fnStartRuntimeAddr := r.baseAddr + (fnStartVirtAddr - r.baseOffset)
				fnEndRuntimeAddr := r.baseAddr + (fnEndVirtAddr - r.baseOffset)

				if pc >= fnStartRuntimeAddr && pc < fnEndRuntimeAddr {
					debugLog("[DEBUG] findSFrameFunction: 找到主程序SFrame函数 @ 0x%x (virtual=0x%x), PC=0x%x, size=%d\n",
						fnStartRuntimeAddr, fnStartVirtAddr, pc, fn.Size)
					return fn, fnStartRuntimeAddr
				}
			}
			debugLog("[DEBUG] findSFrameFunction: 主程序中未找到匹配的SFrame函数 (PC=0x%x)\n", pc)
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

				// 在共享库SFrame函数列表中查找
				for j := range r.mappings[i].SFrameData.Functions {
					fn := &r.mappings[i].SFrameData.Functions[j]

					// 当前FDE的文件偏移（每个FDE 20字节）
					fdeOffset := fdeArrayStart + j*20

					// 计算函数起始地址
					var fnStartVirtAddr uint64

					if r.mappings[i].SFrameData.Header.Flags&SFRAME_F_FDE_FUNC_START_PCREL != 0 {
						// PC-relative: StartAddr是相对于FDE的sfde_func_start_address字段本身的偏移
						fdeFieldAddr := r.mappings[i].SFrameData.sectionAddr + uint64(fdeOffset)
						fnStartVirtAddr = uint64(int64(fdeFieldAddr) + int64(fn.StartAddr))
						debugLog("[DEBUG] findSFrameFunction: Lib FDE[%d] PCREL mode: fdeFieldAddr=0x%x, StartAddr=%d, fnStartVirtAddr=0x%x\n",
							j, fdeFieldAddr, fn.StartAddr, fnStartVirtAddr)
					} else {
						// Absolute: StartAddr是相对于.sframe节开始的偏移
						fnStartVirtAddr = r.mappings[i].SFrameData.sectionAddr + uint64(int64(fn.StartAddr))
						debugLog("[DEBUG] findSFrameFunction: Lib FDE[%d] ABS mode: sectionAddr=0x%x, StartAddr=%d, fnStartVirtAddr=0x%x\n",
							j, r.mappings[i].SFrameData.sectionAddr, fn.StartAddr, fnStartVirtAddr)
					}

					fnEndVirtAddr := fnStartVirtAddr + uint64(fn.Size)

					// 转换为运行时地址并比较
					fnStartRuntimeAddr := r.mappings[i].StartAddr + (fnStartVirtAddr - r.mappings[i].Offset)
					fnEndRuntimeAddr := r.mappings[i].StartAddr + (fnEndVirtAddr - r.mappings[i].Offset)

					// 比较运行时地址
					if pc >= fnStartRuntimeAddr && pc < fnEndRuntimeAddr {
						debugLog("[DEBUG] findSFrameFunction: 找到共享库SFrame函数 @ 0x%x (virtual=0x%x), size=%d, lib=%s\n",
							fnStartRuntimeAddr, fnStartVirtAddr, fn.Size, r.mappings[i].Path)
						return fn, fnStartRuntimeAddr
					}
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
	fde := findFDEForFunction(sframeFunc, sframeData, pcOffset)

	// 根据SFrame header的Flags确定是否所有函数都保留帧指针
	// SFRAME_F_FRAME_POINTER = 0x2: 所有函数都保留FP
	const SFRAME_F_FRAME_POINTER = 0x2
	allFunctionsPreserveFP := (sframeData != nil && sframeData.Header.Flags&SFRAME_F_FRAME_POINTER != 0)

	// 决定是使用SP-based还是FP-based
	// 1. 如果header标记所有函数保留FP，使用FP-based
	// 2. 否则根据FDE的CFAOffset来判断：
	//    - 如果CFAOffset==8，通常是SP-based
	//    - 如果CFAOffset==16且有FPOffset，是FP-based
	useFPBased := allFunctionsPreserveFP

	if !useFPBased && fde != nil {
		// 根据FDE的偏移值判断
		if fde.CFAOffset == 16 && fde.FPOffset != 0 {
			useFPBased = true
		}
	}

	debugLog("[DEBUG] unwindFrameWithSFrame: 函数size=%d, pcOffset=0x%x, allFunctionsPreserveFP=%v, useFPBased=%v\n",
		sframeFunc.Size, pcOffset, allFunctionsPreserveFP, useFPBased)

	var cfa uint64 // Canonical Frame Address

	if !useFPBased {
		// SP-based CFA: CFA = SP + offset
		// 对于SP-based栈帧，返回地址通常在栈顶(SP+0)
		// CFA是调用后的栈指针位置，通常是SP + 8（因为call指令push了返回地址）
		if fde != nil && fde.CFAOffset > 0 {
			cfa = ctx.SP + uint64(fde.CFAOffset)
			debugLog("[DEBUG] unwindFrameWithSFrame: SP-based CFA (来自FDE), CFA=0x%x (SP=0x%x + %d)\n",
				cfa, ctx.SP, fde.CFAOffset)
		} else {
			// 使用默认偏移
			// 对于x86-64，call指令会push返回地址(8字节)
			// 所以调用者的栈指针位置是当前SP + 8
			defaultOffset := int32(8)
			cfa = ctx.SP + uint64(defaultOffset)
			debugLog("[DEBUG] unwindFrameWithSFrame: SP-based CFA (使用默认偏移), CFA=0x%x (SP=0x%x + %d)\n",
				cfa, ctx.SP, defaultOffset)
		}
	} else {
		// FP-based CFA: CFA = BP + offset
		// 在x86-64标准帧布局中，BP指向保存的旧BP，返回地址在BP+8，所以CFA应该是BP+16
		offset := int32(16) // 默认值
		if fde != nil && fde.CFAOffset > 0 {
			offset = fde.CFAOffset
		}
		cfa = ctx.BP + uint64(offset)
		debugLog("[DEBUG] unwindFrameWithSFrame: FP-based CFA, CFA=0x%x (BP=0x%x + %d)\n",
			cfa, ctx.BP, offset)
	}

	// 验证CFA的合理性
	if cfa < 0x1000 || cfa <= ctx.SP {
		return fmt.Errorf("invalid CFA: 0x%x (SP=0x%x)", cfa, ctx.SP)
	}

	// 读取返回地址
	// 对于SP-based: 返回地址在CFA-8的位置（即当前SP位置，因为CFA=SP+8）
	// 对于FP-based: 返回地址在CFA-8的位置（即BP+8）
	raOffset := int32(-8)
	if fde != nil && fde.RAOffset != 0 {
		raOffset = fde.RAOffset
	}

	retAddrLoc := uint64(int64(cfa) + int64(raOffset))

	// 对于SP-based unwinding，如果CFA=SP+8，则retAddrLoc=SP+8-8=SP
	// 这意味着返回地址就在当前栈顶
	debugLog("[DEBUG] unwindFrameWithSFrame: 读取返回地址，位置=0x%x (CFA=0x%x, RAOffset=%d)\n",
		retAddrLoc, cfa, raOffset)

	retAddr, err := r.readUint64(retAddrLoc)
	if err != nil {
		return fmt.Errorf("failed to read return address at 0x%x: %w", retAddrLoc, err)
	}

	// 验证返回地址的有效性
	// 检查是否看起来像有效的代码地址
	isValidCodeAddr := func(addr uint64) bool {
		if addr < 0x1000 || addr > 0x7fffffffffff {
			return false
		}
		// 检查是否在已知的代码区域（主程序或共享库）
		if addr >= r.baseAddr && addr < r.baseAddrEnd {
			return true
		}
		for i := range r.mappings {
			if addr >= r.mappings[i].StartAddr && addr < r.mappings[i].EndAddr {
				return true
			}
		}
		// 栈地址肯定不是代码地址
		if addr >= 0x7fff00000000 && addr <= 0x7fffffffffff {
			return false
		}
		return true
	}

	// 对于SP-based函数，如果从计算位置读取的不是有效代码地址，
	// 可能是因为函数实际使用了帧指针但我们的启发式判断错误
	// 尝试从BP+8读取返回地址作为后备
	if !isValidCodeAddr(retAddr) && !useFPBased && ctx.BP != 0 {
		debugLog("[DEBUG] unwindFrameWithSFrame: SP-based读取失败(retAddr=0x%x无效)，尝试从BP+8读取\n", retAddr)
		// 尝试从BP+8读取返回地址
		altRetAddrLoc := ctx.BP + 8
		altRetAddr, altErr := r.readUint64(altRetAddrLoc)
		if altErr == nil && isValidCodeAddr(altRetAddr) {
			// 从BP读取成功，说明函数实际使用了帧指针
			debugLog("[DEBUG] unwindFrameWithSFrame: 从BP+8成功读取返回地址 0x%x，切换到FP-based\n", altRetAddr)
			retAddr = altRetAddr
			retAddrLoc = altRetAddrLoc
			// 更新为FP-based展开
			useFPBased = true
			cfa = ctx.BP + 16
		} else {
			return fmt.Errorf("invalid return address: 0x%x (alt: 0x%x, err: %v)", retAddr, altRetAddr, altErr)
		}
	} else if !isValidCodeAddr(retAddr) {
		return fmt.Errorf("invalid return address: 0x%x (looks like stack address)", retAddr)
	}

	// 如果使用帧指针，读取保存的BP
	var newBP uint64
	if useFPBased {
		// 在x86-64标准帧布局中：
		// - 当前BP指向的位置保存着调用者的BP（即[BP+0]）
		// - 返回地址在[BP+8]
		// - CFA是BP+16
		// 所以保存的BP的位置是：CFA - 16 = BP + 16 - 16 = BP
		fpOffset := int32(-16) // 相对于CFA
		if fde != nil && fde.FPOffset != 0 {
			fpOffset = fde.FPOffset
		}

		fpLoc := uint64(int64(cfa) + int64(fpOffset))
		// 直接从BP位置读取也是等价的：fpLoc = ctx.BP
		newBP, err = r.readUint64(fpLoc)
		if err != nil {
			debugLog("[DEBUG] unwindFrameWithSFrame: 读取BP失败: %v\n", err)
			return fmt.Errorf("failed to read saved BP at 0x%x: %w", fpLoc, err)
		}
		// 允许newBP为0（可能已经到栈底）或者合理增长
		if newBP != 0 && newBP <= ctx.BP && ctx.BP != 0 {
			debugLog("[DEBUG] unwindFrameWithSFrame: BP未增长: newBP=0x%x, oldBP=0x%x\n", newBP, ctx.BP)
			return fmt.Errorf("invalid BP progression: newBP(0x%x) <= oldBP(0x%x)", newBP, ctx.BP)
		}
		debugLog("[DEBUG] unwindFrameWithSFrame: 读取保存的BP=0x%x (from 0x%x)\n", newBP, fpLoc)
	} else {
		// SP-based unwinding不更新BP，保持当前BP
		newBP = ctx.BP
	}

	debugLog("[DEBUG] unwindFrameWithSFrame: retAddr=0x%x, newBP=0x%x, newSP=0x%x\n",
		retAddr, newBP, cfa)

	// 更新上下文
	ctx.PC = retAddr
	ctx.SP = cfa
	ctx.BP = newBP

	return nil
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

		// 展开到下一帧 - 优先使用SFrame，如果失败则回退到FP
		err := r.unwindFrameWithSFrame(ctx)
		if err != nil {
			debugLog("[DEBUG] UnwindStack: SFrame展开失败，尝试FP: %v\n", err)
			// 回退到基于帧指针的展开
			if err := r.unwindFrame(ctx); err != nil {
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

	// 验证当前BP地址的合理性
	if ctx.BP < 0x1000 {
		return fmt.Errorf("invalid current BP address: 0x%x", ctx.BP)
	}

	// 读取保存的BP
	newBP, err := r.readUint64(ctx.BP)
	if err != nil {
		return fmt.Errorf("failed to read saved BP at 0x%x: %w", ctx.BP, err)
	}

	// 读取返回地址
	retAddr, err := r.readUint64(ctx.BP + 8)
	if err != nil {
		return fmt.Errorf("failed to read return address at 0x%x: %w", ctx.BP+8, err)
	}

	debugLog("[DEBUG] unwindFrame: 读取 newBP=0x%x, retAddr=0x%x (from BP=0x%x)\n", newBP, retAddr, ctx.BP)

	// 验证新的值是否合理
	if retAddr == 0 {
		debugLog("[DEBUG] unwindFrame: 返回地址为0，到达栈底\n")
		return fmt.Errorf("reached end of stack (null return address)")
	}

	if newBP == 0 {
		debugLog("[DEBUG] unwindFrame: 新BP为0，到达栈底\n")
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

	debugLog("[DEBUG] unwindFrame: 更新后 PC=0x%x, BP=0x%x, SP=0x%x\n", ctx.PC, ctx.BP, ctx.SP)
	return nil
}

// UnwindStackWithSFrame 仅使用SFrame执行栈回溯（不回退到FP）
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

		// 仅使用SFrame展开
		if err := r.unwindFrameWithSFrame(ctx); err != nil {
			debugLog("[DEBUG] UnwindStackWithSFrame: SFrame展开失败: %v\n", err)
			break
		}
	}

	debugLog("[DEBUG] UnwindStackWithSFrame: 总共展开了 %d 帧\n", len(frames))
	return frames, nil
}

// UnwindStackWithFP 仅使用帧指针执行栈回溯
func (r *SFrameResolver) UnwindStackWithFP(maxFrames int) ([]StackFrame, error) {
	if maxFrames <= 0 {
		maxFrames = 32
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
		debugLog("[DEBUG] UnwindStackWithFP: Frame %d: PC=0x%x, SP=0x%x, BP=0x%x\n",
			i, frame.PC, frame.SP, frame.BP)

		// 仅使用FP展开
		if err := r.unwindFrame(ctx); err != nil {
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
