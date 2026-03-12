package lbr

import (
	"bufio"
	"debug/dwarf"
	"debug/elf"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// MemoryMapping 内存映射信息
type MemoryMapping struct {
	StartAddr  uint64
	EndAddr    uint64
	Offset     uint64 // 文件偏移
	Path       string
	ElfFile    *elf.File
	DwarfData  *dwarf.Data
	SFrameData *SFrameData // SFrame数据（用于栈展开）
	Symbols    []ElfSymbol
}

// ElfSymbol ELF符号信息
type ElfSymbol struct {
	Name  string
	Addr  uint64
	Size  uint64
	Type  elf.SymType
	Value uint64
}

// AddrInfo 地址解析信息
type AddrInfo struct {
	Addr     uint64
	Function string
	File     string
	Line     int
	Library  string // 外部库名称
}

// String 返回格式化的字符串
func (a *AddrInfo) String() string {
	if a.File != "" && a.Line != 0 {
		return fmt.Sprintf("%s (%s:%d)", a.Function, a.File, a.Line)
	}
	return a.Function
}

// loadELFSymbols 从 ELF 文件加载动态符号表和普通符号表，返回合并后的符号列表（未排序）。
// 调用方可按需自行排序（DwarfResolver 需要排序以支持二分查找）。
func loadELFSymbols(elfFile *elf.File) []ElfSymbol {
	var symbols []ElfSymbol
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
	return symbols
}

// loadPLTSymbols 从 ELF 文件加载 PLT 节，返回带 @plt 后缀标注的桩函数符号列表。
// PLT slot 0 是 _dl_runtime_resolve 头，从 slot 1 起依次对应动态符号表中的 FUNC 条目。
func loadPLTSymbols(elfFile *elf.File) []ElfSymbol {
	pltSec := elfFile.Section(".plt")
	if pltSec == nil {
		return nil
	}
	const pltEntrySize = 16
	pltAddr := pltSec.Addr
	dynsyms, err := elfFile.DynamicSymbols()
	if err != nil {
		return nil
	}
	var symbols []ElfSymbol
	slot := 1
	for _, sym := range dynsyms {
		if sym.Name != "" && elf.SymType(sym.Info&0xf) == elf.STT_FUNC {
			slotAddr := pltAddr + uint64(slot)*pltEntrySize
			symbols = append(symbols, ElfSymbol{
				Name:  sym.Name + "@plt",
				Addr:  slotAddr,
				Size:  pltEntrySize,
				Type:  elf.STT_FUNC,
				Value: slotAddr,
			})
			slot++
		}
	}
	return symbols
}

// loadLibraryMappingBase 打开指定路径的 ELF 文件，加载符号表和 DWARF，
// 返回包含基础信息的 MemoryMapping。供 DwarfResolver 和 SFrameResolver 共同使用。
func loadLibraryMappingBase(path string, startAddr, endAddr, offset uint64) (MemoryMapping, error) {
	if _, err := os.Stat(path); err != nil {
		return MemoryMapping{}, err
	}
	elfFile, err := elf.Open(path)
	if err != nil {
		return MemoryMapping{}, fmt.Errorf("failed to open ELF: %w", err)
	}
	mapping := MemoryMapping{
		StartAddr: startAddr,
		EndAddr:   endAddr,
		Offset:    offset,
		Path:      path,
		ElfFile:   elfFile,
		Symbols:   loadELFSymbols(elfFile),
	}
	if dd, err := elfFile.DWARF(); err == nil {
		mapping.DwarfData = dd
	}
	return mapping, nil
}

// loadProcessMappings 读取 /proc/pid/maps，找到主程序基址信息后，
// 对每个文件路径唯一的可执行共享库调用 onLib 回调。
// 返回基址三元组（baseAddr, baseAddrEnd, baseOffset）。
func loadProcessMappings(pid int, execPath string,
	onLib func(path string, startAddr, endAddr, offset uint64) error,
) (baseAddr, baseAddrEnd, baseOffset uint64, err error) {
	maps, err := GetProcessMaps(pid)
	if err != nil {
		return 0, 0, 0, err
	}
	loadedLibs := make(map[string]bool)
	for _, m := range maps {
		if !strings.Contains(m.Perms, "x") {
			continue
		}
		if m.Pathname == execPath {
			if baseAddr == 0 || m.StartAddr < baseAddr {
				baseAddr = m.StartAddr
				baseOffset = m.Offset
			}
			if m.EndAddr > baseAddrEnd {
				baseAddrEnd = m.EndAddr
			}
			continue
		}
		// 跳过匿名/特殊映射
		if m.Pathname == "" || strings.HasPrefix(m.Pathname, "[") {
			continue
		}
		// 每个路径只处理一次
		if loadedLibs[m.Pathname] {
			continue
		}
		loadedLibs[m.Pathname] = true
		if onLib != nil {
			if libErr := onLib(m.Pathname, m.StartAddr, m.EndAddr, m.Offset); libErr != nil {
				debugLog("[DEBUG] loadProcessMappings: 加载共享库失败 %s: %v\n", m.Pathname, libErr)
			}
		}
	}
	if baseAddr == 0 {
		return 0, 0, 0, fmt.Errorf("executable not found in memory maps")
	}
	debugLog("[DEBUG] loadProcessMappings: 基址=0x%x, 结束=0x%x, 偏移=0x%x\n", baseAddr, baseAddrEnd, baseOffset)
	return baseAddr, baseAddrEnd, baseOffset, nil
}

// GetProcessMaps 读取 /proc/pid/maps，返回所有内存映射条目。
func GetProcessMaps(pid int) ([]MemoryMap, error) {
	debugLog("[DEBUG] GetProcessMaps: 读取进程 %d 的内存映射\n", pid)
	file, err := os.Open(fmt.Sprintf("/proc/%d/maps", pid))
	if err != nil {
		debugLog("[DEBUG] GetProcessMaps: 打开 maps 文件失败: %v\n", err)
		return nil, err
	}
	defer file.Close()

	var maps []MemoryMap
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var m MemoryMap
		if err := m.Parse(scanner.Text()); err == nil {
			maps = append(maps, m)
		}
	}
	debugLog("[DEBUG] GetProcessMaps: 读取到 %d 个内存映射\n", len(maps))
	return maps, scanner.Err()
}

// MemoryMap 表示进程的内存映射（来自 /proc/pid/maps）。
type MemoryMap struct {
	StartAddr uint64
	EndAddr   uint64
	Perms     string
	Offset    uint64
	Device    string
	Inode     uint64
	Pathname  string
}

// Parse 解析 /proc/pid/maps 的一行。
func (m *MemoryMap) Parse(line string) error {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return fmt.Errorf("invalid maps line")
	}
	addrRange := strings.Split(fields[0], "-")
	if len(addrRange) != 2 {
		return fmt.Errorf("invalid address range")
	}
	var err error
	m.StartAddr, err = strconv.ParseUint(addrRange[0], 16, 64)
	if err != nil {
		return err
	}
	m.EndAddr, err = strconv.ParseUint(addrRange[1], 16, 64)
	if err != nil {
		return err
	}
	m.Perms = fields[1]
	m.Offset, _ = strconv.ParseUint(fields[2], 16, 64)
	m.Device = fields[3]
	m.Inode, _ = strconv.ParseUint(fields[4], 10, 64)
	if len(fields) > 5 {
		m.Pathname = fields[5]
	}
	return nil
}

// findSymbolInList 在符号列表中查找最匹配 addr 的符号名。
// 优先返回范围内（[sym.Addr, sym.Addr+sym.Size)）最近的符号；
// 其次返回 256 字节阈值内最近的未知大小符号（size=0 时放宽到 4KB）。
// 返回格式：精确匹配返回 "name"，偏移匹配返回 "name+0xoffset"，未找到返回 ""。
func findSymbolInList(addr uint64, symbols []ElfSymbol) string {
	const maxOutOfRangeDistance = 0x100 // 256字节

	var bestMatch string
	var bestDist uint64 = ^uint64(0)
	var bestInRange string
	var bestInRangeDist uint64 = ^uint64(0)

	for i := range symbols {
		sym := &symbols[i]
		if addr < sym.Addr {
			continue
		}
		dist := addr - sym.Addr

		if sym.Size > 0 && dist < sym.Size {
			if dist < bestInRangeDist {
				bestInRangeDist = dist
				bestInRange = sym.Name
			}
		}

		maxDist := uint64(0x1000)
		if sym.Size > 0 && dist >= sym.Size {
			maxDist = maxOutOfRangeDistance
		}
		if dist < maxDist && dist < bestDist {
			bestDist = dist
			bestMatch = sym.Name
		}
	}

	if bestInRange != "" {
		if bestInRangeDist > 0 {
			return fmt.Sprintf("%s+0x%x", bestInRange, bestInRangeDist)
		}
		return bestInRange
	}
	if bestMatch != "" && bestDist != ^uint64(0) {
		if bestDist > 0 {
			return fmt.Sprintf("%s+0x%x", bestMatch, bestDist)
		}
		return bestMatch
	}
	return ""
}

// resolveAddressInMappings 是 ResolveAddress 的共享核心，供 SFrameResolver 和
// DwarfResolver 共同使用：先匹配主程序范围，再遍历共享库映射。
func resolveAddressInMappings(addr, baseAddr, baseAddrEnd, baseOffset uint64,
	execPath string, mainDwarfData *dwarf.Data, mainSymbols []ElfSymbol,
	mappings []MemoryMapping) (*AddrInfo, error) {

	if baseAddr > 0 && baseAddrEnd > 0 && addr >= baseAddr && addr < baseAddrEnd {
		fileOffset := addr - baseAddr + baseOffset
		debugLog("[DEBUG] resolveAddressInMappings: 主程序地址，文件偏移 0x%x\n", fileOffset)
		return resolveInMapping(addr, fileOffset, mainDwarfData, mainSymbols, execPath, execPath)
	}

	for i := range mappings {
		if addr >= mappings[i].StartAddr && addr < mappings[i].EndAddr {
			fileOffset := addr - mappings[i].StartAddr + mappings[i].Offset
			debugLog("[DEBUG] resolveAddressInMappings: 共享库地址 %s，文件偏移 0x%x\n",
				mappings[i].Path, fileOffset)
			return resolveInMapping(addr, fileOffset, mappings[i].DwarfData, mappings[i].Symbols,
				mappings[i].Path, execPath)
		}
	}

	return nil, fmt.Errorf("address 0x%x not in any mapped region", addr)
}

// resolveInMapping 在单个映射内完成符号解析：先查符号表，再通过 DWARF 补充
// 文件/行号及函数名。共享库场景下若无任何符号信息则回退为十六进制偏移字符串。
func resolveInMapping(addr, fileOffset uint64, dwarfData *dwarf.Data,
	symbols []ElfSymbol, libPath, execPath string) (*AddrInfo, error) {

	info := &AddrInfo{Addr: addr}

	// 符号表查找
	debugLog("[DEBUG] resolveInMapping: 从符号表查找文件偏移 0x%x\n", fileOffset)
	if funcName := findSymbolInList(fileOffset, symbols); funcName != "" {
		debugLog("[DEBUG] resolveInMapping: 符号表找到函数名: %s\n", funcName)
		info.Function = funcName
	} else {
		debugLog("[DEBUG] resolveInMapping: 符号表未找到函数名\n")
	}

	// DWARF fallback：补充文件/行号，或在符号表未命中时提供函数名
	if dwarfData != nil {
		if file, line, err := dwarfFindLineInfo(fileOffset, dwarfData); err == nil {
			debugLog("[DEBUG] resolveInMapping: DWARF找到文件和行号: %s:%d\n", file, line)
			info.File = file
			info.Line = line
		}
		if info.Function == "" {
			if fn := dwarfFindFunctionName(fileOffset, dwarfData); fn != "" {
				debugLog("[DEBUG] resolveInMapping: DWARF找到函数名: %s\n", fn)
				info.Function = fn
			}
		}
	} else {
		debugLog("[DEBUG] resolveInMapping: 无DWARF调试信息\n")
	}

	// 提取库名（共享库场景）
	if libPath != "" && libPath != execPath {
		for i := len(libPath) - 1; i >= 0; i-- {
			if libPath[i] == '/' {
				info.Library = libPath[i+1:]
				break
			}
		}
		if info.Library == "" {
			info.Library = libPath
		}
		// 共享库无任何符号信息时回退为偏移字符串，避免返回 error
		if info.Function == "" && info.File == "" {
			info.Function = fmt.Sprintf("0x%x", fileOffset)
			debugLog("[DEBUG] resolveInMapping: 无符号信息，使用偏移 %s@%s\n", info.Function, info.Library)
		}
	} else if info.Function == "" && info.File == "" {
		debugLog("[DEBUG] resolveInMapping: 解析失败，未找到符号信息\n")
		return nil, fmt.Errorf("symbol not found for address 0x%x", addr)
	}

	debugLog("[DEBUG] resolveInMapping: 解析成功 -> 函数: %s, 文件: %s, 行号: %d, 库: %s\n",
		info.Function, info.File, info.Line, info.Library)
	return info, nil
}
