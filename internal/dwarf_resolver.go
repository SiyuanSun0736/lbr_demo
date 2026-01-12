package lbr

import (
	"bufio"
	"debug/dwarf"
	"debug/elf"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
)

var debugMode bool

// SetDebugMode 设置调试模式
func SetDebugMode(enabled bool) {
	debugMode = enabled
}

// debugLog 调试日志输出（仅在debug模式下）
func debugLog(format string, v ...interface{}) {
	if debugMode {
		log.Printf(format, v...)
	}
}

// MemoryMapping 内存映射信息
type MemoryMapping struct {
	StartAddr uint64
	EndAddr   uint64
	Offset    uint64 // 文件偏移
	Path      string
	ElfFile   *elf.File
	DwarfData *dwarf.Data
	Symbols   []ElfSymbol
}

// DwarfResolver 基于DWARF的符号解析器
type DwarfResolver struct {
	pid         int
	execPath    string
	elfFile     *elf.File
	dwarfData   *dwarf.Data
	symbols     []ElfSymbol
	baseAddr    uint64          // 进程加载基址
	baseAddrEnd uint64          // 进程加载结束地址
	baseOffset  uint64          // 主程序文件偏移
	mappings    []MemoryMapping // 所有内存映射（包括共享库）
}

// ElfSymbol ELF符号信息
type ElfSymbol struct {
	Name  string
	Addr  uint64
	Size  uint64
	Type  elf.SymType
	Value uint64
}

// NewDwarfResolver 创建DWARF解析器
func NewDwarfResolver(pid int) (*DwarfResolver, error) {
	debugLog("[DEBUG] NewDwarfResolver: 为PID %d 创建解析器\n", pid)
	resolver := &DwarfResolver{
		pid: pid,
	}

	// 获取可执行文件路径
	execPath, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return nil, fmt.Errorf("failed to read exe link: %w", err)
	}
	resolver.execPath = execPath
	debugLog("[DEBUG] NewDwarfResolver: 使用可执行文件: %s\n", execPath)

	// 打开ELF文件
	debugLog("[DEBUG] NewDwarfResolver: 打开ELF文件...\n")
	elfFile, err := elf.Open(execPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open ELF file: %w", err)
	}
	resolver.elfFile = elfFile
	debugLog("[DEBUG] NewDwarfResolver: ELF文件打开成功\n")

	// 加载DWARF调试信息
	debugLog("[DEBUG] NewDwarfResolver: 尝试加载DWARF调试信息...\n")
	dwarfData, err := elfFile.DWARF()
	if err != nil {
		// 没有DWARF信息，尝试使用符号表
		if debugMode {
			fmt.Printf("[WARNING] NewDwarfResolver: 无DWARF调试信息，仅使用符号表: %v\n", err)
		}
	} else {
		resolver.dwarfData = dwarfData
		debugLog("[DEBUG] NewDwarfResolver: DWARF调试信息加载成功\n")
	}

	// 加载符号表
	if err := resolver.loadSymbols(); err != nil {
		return nil, fmt.Errorf("failed to load symbols: %w", err)
	}

	// 加载进程基址
	if err := resolver.loadBaseAddress(); err != nil {
		if debugMode {
			fmt.Printf("[WARNING] NewDwarfResolver: 无法加载基址: %v\n", err)
		}
		// 基址为0，假设地址已经是文件偏移
	}

	debugLog("[DEBUG] NewDwarfResolver: 解析器创建成功\n")
	return resolver, nil
}

// loadSymbols 加载ELF符号表
func (r *DwarfResolver) loadSymbols() error {
	debugLog("[DEBUG] loadSymbols: 开始加载符号表\n")
	// 尝试加载动态符号
	dynsyms, err := r.elfFile.DynamicSymbols()
	if err == nil {
		dynSymCount := 0
		for _, sym := range dynsyms {
			// 过滤掉地址为0的符号(外部符号)
			if sym.Name != "" && sym.Value != 0 {
				r.symbols = append(r.symbols, ElfSymbol{
					Name:  sym.Name,
					Addr:  sym.Value,
					Size:  sym.Size,
					Type:  elf.SymType(sym.Info & 0xf),
					Value: sym.Value,
				})
				dynSymCount++
			}
		}
		debugLog("[DEBUG] loadSymbols: 加载了 %d 个动态符号\n", dynSymCount)
	} else {
		debugLog("[DEBUG] loadSymbols: 无法加载动态符号: %v\n", err)
	}

	// 加载普通符号表
	syms, err := r.elfFile.Symbols()
	if err == nil {
		normalSymCount := 0
		for _, sym := range syms {
			// 过滤掉地址为0的符号(外部符号)
			if sym.Name != "" && sym.Value != 0 {
				r.symbols = append(r.symbols, ElfSymbol{
					Name:  sym.Name,
					Addr:  sym.Value,
					Size:  sym.Size,
					Type:  elf.SymType(sym.Info & 0xf),
					Value: sym.Value,
				})
				normalSymCount++
			}
		}
		debugLog("[DEBUG] loadSymbols: 加载了 %d 个普通符号\n", normalSymCount)
	} else {
		debugLog("[DEBUG] loadSymbols: 无法加载普通符号: %v\n", err)
	}

	// 按地址排序
	sort.Slice(r.symbols, func(i, j int) bool {
		return r.symbols[i].Addr < r.symbols[j].Addr
	})

	debugLog("[DEBUG] loadSymbols: 共加载 %d 个符号\n", len(r.symbols))
	if len(r.symbols) > 0 {
		debugLog("[DEBUG] loadSymbols: 符号地址范围: 0x%x - 0x%x\n",
			r.symbols[0].Addr, r.symbols[len(r.symbols)-1].Addr)
	}

	return nil
}

// loadBaseAddress 从 /proc/pid/maps 加载所有可执行映射（包括共享库）
func (r *DwarfResolver) loadBaseAddress() error {
	debugLog("[DEBUG] loadBaseAddress: 读取 /proc/%d/maps\n", r.pid)

	mapsPath := fmt.Sprintf("/proc/%d/maps", r.pid)
	file, err := os.Open(mapsPath)
	if err != nil {
		return fmt.Errorf("failed to open maps: %w", err)
	}
	defer file.Close()

	// 用于跟踪已加载的库，避免重复
	loadedLibs := make(map[string]bool)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		// 格式: address perms offset dev inode pathname
		// 例如: 555555554000-555555556000 r-xp 00000000 08:01 1234 /path/to/exe
		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		pathname := fields[5]
		perms := fields[1]
		offsetStr := fields[2]

		// 只处理具有执行权限的映射
		if !strings.Contains(perms, "x") {
			continue
		}

		// 解析地址范围
		addrRange := strings.Split(fields[0], "-")
		if len(addrRange) != 2 {
			continue
		}

		var startAddr, endAddr uint64
		var fileOffset uint64
		_, err1 := fmt.Sscanf(addrRange[0], "%x", &startAddr)
		_, err2 := fmt.Sscanf(addrRange[1], "%x", &endAddr)
		_, err3 := fmt.Sscanf(offsetStr, "%x", &fileOffset)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}

		// 主程序：设置基址
		if pathname == r.execPath {
			if r.baseAddr == 0 || startAddr < r.baseAddr {
				r.baseAddr = startAddr
				r.baseOffset = fileOffset // 保存文件偏移
			}
			if endAddr > r.baseAddrEnd {
				r.baseAddrEnd = endAddr
			}
			debugLog("[DEBUG] loadBaseAddress: 主程序映射 0x%x - 0x%x (权限: %s, 偏移: 0x%x)\n", startAddr, endAddr, perms, fileOffset)
			continue
		}

		// 跳过特殊映射
		if strings.HasPrefix(pathname, "[") || pathname == "" {
			continue
		}

		// 共享库：只加载每个库的第一个可执行段
		if loadedLibs[pathname] {
			continue
		}

		// 标记为已加载
		loadedLibs[pathname] = true

		// 尝试加载共享库的符号
		debugLog("[DEBUG] loadBaseAddress: 发现共享库 %s @ 0x%x - 0x%x (偏移: 0x%x)\n", pathname, startAddr, endAddr, fileOffset)
		if err := r.loadLibraryMapping(pathname, startAddr, endAddr, fileOffset); err != nil {
			debugLog("[DEBUG] loadBaseAddress: 加载共享库失败: %v\n", err)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("failed to scan maps: %w", err)
	}

	if r.baseAddr == 0 {
		return fmt.Errorf("executable mapping not found in maps")
	}

	debugLog("[DEBUG] loadBaseAddress: 共加载 %d 个内存映射\n", len(r.mappings))
	return nil
}

// loadLibraryMapping 加载共享库的符号信息
func (r *DwarfResolver) loadLibraryMapping(path string, startAddr, endAddr, offset uint64) error {
	// 打开ELF文件
	elfFile, err := elf.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open ELF: %w", err)
	}

	mapping := MemoryMapping{
		StartAddr: startAddr,
		EndAddr:   endAddr,
		Offset:    offset,
		Path:      path,
		ElfFile:   elfFile,
	}

	// 尝试加载DWARF（可能没有）
	dwarfData, err := elfFile.DWARF()
	if err == nil {
		mapping.DwarfData = dwarfData
	}

	// 加载符号表
	symbols := []ElfSymbol{}

	// 动态符号
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

	// 普通符号
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

	// 排序
	sort.Slice(symbols, func(i, j int) bool {
		return symbols[i].Addr < symbols[j].Addr
	})

	mapping.Symbols = symbols
	r.mappings = append(r.mappings, mapping)

	debugLog("[DEBUG] loadLibraryMapping: %s 加载 %d 个符号\n", path, len(symbols))
	return nil
}

// ResolveAddress 解析地址到符号
func (r *DwarfResolver) ResolveAddress(addr uint64) (*AddrInfo, error) {
	debugLog("[DEBUG] ResolveAddress: 开始解析地址 0x%x (主程序基址: 0x%x - 0x%x)\n", addr, r.baseAddr, r.baseAddrEnd)

	// 检查是否在主程序范围内
	if r.baseAddr > 0 && r.baseAddrEnd > 0 && addr >= r.baseAddr && addr < r.baseAddrEnd {
		// 主程序地址
		fileOffset := addr - r.baseAddr + r.baseOffset
		debugLog("[DEBUG] ResolveAddress: 主程序地址，文件偏移 0x%x (虚拟偏移: 0x%x + 文件偏移: 0x%x)\n",
			fileOffset, addr-r.baseAddr, r.baseOffset)
		return r.resolveInMapping(addr, fileOffset, r.elfFile, r.dwarfData, r.symbols, r.execPath)
	}

	// 检查共享库映射
	for i := range r.mappings {
		mapping := &r.mappings[i]
		if addr >= mapping.StartAddr && addr < mapping.EndAddr {
			fileOffset := addr - mapping.StartAddr + mapping.Offset
			debugLog("[DEBUG] ResolveAddress: 共享库地址 %s，文件偏移 0x%x (虚拟偏移: 0x%x + 文件偏移: 0x%x)\n",
				mapping.Path, fileOffset, addr-mapping.StartAddr, mapping.Offset)
			return r.resolveInMapping(addr, fileOffset, mapping.ElfFile, mapping.DwarfData, mapping.Symbols, mapping.Path)
		}
	}

	debugLog("[DEBUG] ResolveAddress: 地址 0x%x 不在任何已知映射中\n", addr)
	return nil, fmt.Errorf("address 0x%x not found in any mapping", addr)
}

// resolveInMapping 在指定的映射中解析地址
func (r *DwarfResolver) resolveInMapping(addr, fileOffset uint64, elfFile *elf.File, dwarfData *dwarf.Data, symbols []ElfSymbol, libPath string) (*AddrInfo, error) {

	info := &AddrInfo{
		Addr: addr,
	}

	// 首先尝试从符号表查找
	debugLog("[DEBUG] resolveInMapping: 从符号表查找文件偏移 0x%x\n", fileOffset)
	funcName := r.findSymbolInList(fileOffset, symbols)
	if funcName != "" {
		debugLog("[DEBUG] resolveInMapping: 符号表找到函数名: %s\n", funcName)
		info.Function = funcName
	} else {
		debugLog("[DEBUG] resolveInMapping: 符号表未找到函数名\n")
	}

	// 如果有DWARF信息，尝试获取更详细的信息
	if dwarfData != nil {
		debugLog("[DEBUG] resolveInMapping: 从DWARF查找行号信息\n")
		if file, line, err := r.findLineInfoInDwarf(fileOffset, dwarfData); err == nil {
			debugLog("[DEBUG] resolveInMapping: DWARF找到文件和行号: %s:%d\n", file, line)
			info.File = file
			info.Line = line
		} else {
			debugLog("[DEBUG] resolveInMapping: DWARF未找到行号信息: %v\n", err)
		}

		// 如果还没有函数名，从DWARF查找
		if info.Function == "" {
			debugLog("[DEBUG] resolveInMapping: 从DWARF查找函数名\n")
			if fn := r.findDwarfFunctionInData(fileOffset, dwarfData); fn != "" {
				debugLog("[DEBUG] resolveInMapping: DWARF找到函数名: %s\n", fn)
				info.Function = fn
			} else {
				debugLog("[DEBUG] resolveInMapping: DWARF未找到函数名\n")
			}
		}
	} else {
		debugLog("[DEBUG] resolveInMapping: 无DWARF调试信息\n")
	}

	if info.Function == "" && info.File == "" {
		debugLog("[DEBUG] resolveInMapping: 解析失败，未找到符号信息\n")
		return nil, fmt.Errorf("no symbol found for address 0x%x", addr)
	}

	// 如果是外部库，提取库名称
	if libPath != "" && libPath != r.execPath {
		// 只保留库文件名
		for i := len(libPath) - 1; i >= 0; i-- {
			if libPath[i] == '/' {
				info.Library = libPath[i+1:]
				break
			}
		}
		if info.Library == "" {
			info.Library = libPath
		}
	}

	debugLog("[DEBUG] resolveInMapping: 解析成功 -> 函数: %s, 文件: %s, 行号: %d, 库: %s\n",
		info.Function, info.File, info.Line, info.Library)
	return info, nil
}

// findSymbol 从符号表查找符号（主程序）
func (r *DwarfResolver) findSymbol(addr uint64) string {
	return r.findSymbolInList(addr, r.symbols)
}

// findSymbolInList 从指定符号列表查找符号
func (r *DwarfResolver) findSymbolInList(addr uint64, symbols []ElfSymbol) string {
	debugLog("[DEBUG] findSymbolInList: 在%d个符号中查找地址 0x%x\n", len(symbols), addr)

	// 打印前几个符号用于调试
	if debugMode && len(symbols) > 0 {
		n := 5
		if len(symbols) < 5 {
			n = len(symbols)
		}
		debugLog("[DEBUG] findSymbolInList: 前%d个符号:\n", n)
		for i := 0; i < n; i++ {
			debugLog("[DEBUG]   [%d] %s @ 0x%x (大小: %d)\n", i, symbols[i].Name, symbols[i].Addr, symbols[i].Size)
		}
	}

	// 二分查找
	idx := sort.Search(len(symbols), func(i int) bool {
		return symbols[i].Addr > addr
	})

	if idx > 0 {
		sym := symbols[idx-1]
		debugLog("[DEBUG] findSymbolInList: 找到候选符号 %s (地址: 0x%x, 大小: %d)\n",
			sym.Name, sym.Addr, sym.Size)
		// 检查地址是否在符号范围内
		if addr >= sym.Addr && (sym.Size == 0 || addr < sym.Addr+sym.Size) {
			debugLog("[DEBUG] findSymbolInList: 地址匹配成功\n")
			return sym.Name
		}
		debugLog("[DEBUG] findSymbolInList: 地址不在符号范围内 (addr=0x%x, sym.Addr=0x%x, sym.Addr+Size=0x%x)\n",
			addr, sym.Addr, sym.Addr+sym.Size)
	}

	return ""
}

// findLineInfo 从DWARF查找行号信息（主程序）
func (r *DwarfResolver) findLineInfo(addr uint64) (string, int, error) {
	return r.findLineInfoInDwarf(addr, r.dwarfData)
}

// findLineInfoInDwarf 从指定DWARF数据查找行号信息
func (r *DwarfResolver) findLineInfoInDwarf(addr uint64, dwarfData *dwarf.Data) (string, int, error) {
	if dwarfData == nil {
		return "", 0, fmt.Errorf("no DWARF data available")
	}

	reader := dwarfData.Reader()
	var bestMatch *dwarf.LineEntry
	var bestDistance uint64 = ^uint64(0)

	for {
		entry, err := reader.Next()
		if err != nil || entry == nil {
			break
		}

		if entry.Tag == dwarf.TagCompileUnit {
			lr, err := dwarfData.LineReader(entry)
			if err != nil {
				continue
			}

			var prevEntry dwarf.LineEntry
			var hasPrev bool
			for {
				var lineEntry dwarf.LineEntry
				err := lr.Next(&lineEntry)
				if err != nil {
					break
				}

				// 精确匹配
				if lineEntry.Address == addr {
					return lineEntry.File.Name, lineEntry.Line, nil
				}

				// 范围匹配：如果地址在前一个条目和当前条目之间
				if hasPrev && addr >= prevEntry.Address && addr < lineEntry.Address {
					distance := addr - prevEntry.Address
					if distance < bestDistance {
						bestDistance = distance
						bestMatch = &prevEntry
					}
				}

				prevEntry = lineEntry
				hasPrev = true
			}

			// 检查最后一个条目
			if hasPrev && addr >= prevEntry.Address {
				distance := addr - prevEntry.Address
				if distance < bestDistance && distance < 100 { // 限制在合理范围内
					bestDistance = distance
					bestMatch = &prevEntry
				}
			}
		}
	}

	if bestMatch != nil {
		debugLog("[DEBUG] findLineInfo: 找到最佳匹配 (距离: %d 字节)\n", bestDistance)
		return bestMatch.File.Name, bestMatch.Line, nil
	}

	return "", 0, fmt.Errorf("line info not found")
}

// findDwarfFunction 从DWARF查找函数名（主程序）
func (r *DwarfResolver) findDwarfFunction(addr uint64) string {
	return r.findDwarfFunctionInData(addr, r.dwarfData)
}

// findDwarfFunctionInData 从指定DWARF数据查找函数名
func (r *DwarfResolver) findDwarfFunctionInData(addr uint64, dwarfData *dwarf.Data) string {
	if dwarfData == nil {
		return ""
	}

	reader := dwarfData.Reader()
	funcCount := 0
	for {
		entry, err := reader.Next()
		if err != nil || entry == nil {
			break
		}

		if entry.Tag == dwarf.TagSubprogram {
			funcCount++
			// 获取函数的低PC和高PC
			lowPC, ok := entry.Val(dwarf.AttrLowpc).(uint64)
			if !ok {
				continue
			}

			highPC := uint64(0)
			if hpc, ok := entry.Val(dwarf.AttrHighpc).(uint64); ok {
				highPC = hpc
			} else if hpc, ok := entry.Val(dwarf.AttrHighpc).(int64); ok {
				// 相对偏移
				highPC = lowPC + uint64(hpc)
			}

			name, _ := entry.Val(dwarf.AttrName).(string)
			if debugMode && funcCount <= 5 {
				debugLog("[DEBUG] findDwarfFunction: 函数 #%d: %s [0x%x - 0x%x]\n", funcCount, name, lowPC, highPC)
			}

			if addr >= lowPC && (highPC == 0 || addr < highPC) {
				if name != "" {
					debugLog("[DEBUG] findDwarfFunction: 匹配成功！地址 0x%x 在函数 %s [0x%x - 0x%x] 范围内\n",
						addr, name, lowPC, highPC)
					return name
				}
			}
		}
	}

	debugLog("[DEBUG] findDwarfFunction: 共检查了 %d 个函数，未找到匹配\n", funcCount)
	return ""
}

// Close 关闭资源
func (r *DwarfResolver) Close() error {
	// 关闭所有共享库映射
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
