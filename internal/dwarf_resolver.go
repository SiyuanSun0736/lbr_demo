package lbr

import (
	"debug/dwarf"
	"debug/elf"
	"fmt"
	"os"
	"sort"
)

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
	r.symbols = loadELFSymbols(r.elfFile)
	sort.Slice(r.symbols, func(i, j int) bool {
		return r.symbols[i].Addr < r.symbols[j].Addr
	})
	debugLog("[DEBUG] loadSymbols: 共加载 %d 个符号\n", len(r.symbols))
	return nil
}

// loadBaseAddress 从 /proc/pid/maps 加载主程序基址及所有共享库映射
func (r *DwarfResolver) loadBaseAddress() error {
	baseAddr, baseAddrEnd, baseOffset, err := loadProcessMappings(r.pid, r.execPath, r.loadLibraryMapping)
	if err != nil {
		return err
	}
	r.baseAddr = baseAddr
	r.baseAddrEnd = baseAddrEnd
	r.baseOffset = baseOffset
	debugLog("[DEBUG] loadBaseAddress: 共加载 %d 个内存映射\n", len(r.mappings))
	return nil
}

// loadLibraryMapping 加载共享库的符号信息
func (r *DwarfResolver) loadLibraryMapping(path string, startAddr, endAddr, offset uint64) error {
	mapping, err := loadLibraryMappingBase(path, startAddr, endAddr, offset)
	if err != nil {
		return err
	}
	sort.Slice(mapping.Symbols, func(i, j int) bool {
		return mapping.Symbols[i].Addr < mapping.Symbols[j].Addr
	})
	r.mappings = append(r.mappings, mapping)
	debugLog("[DEBUG] loadLibraryMapping: %s 加载 %d 个符号\n", path, len(mapping.Symbols))
	return nil
}

// ResolveAddress 解析地址到符号
func (r *DwarfResolver) ResolveAddress(addr uint64) (*AddrInfo, error) {
	debugLog("[DEBUG] DwarfResolver.ResolveAddress: 解析地址 0x%x\n", addr)
	return resolveAddressInMappings(addr, r.baseAddr, r.baseAddrEnd, r.baseOffset,
		r.execPath, r.dwarfData, r.symbols, r.mappings)
}

// findSymbol 从符号表查找符号（主程序）
func (r *DwarfResolver) findSymbol(addr uint64) string {
	return findSymbolInList(addr, r.symbols)
}

// findLineInfo 从DWARF查找行号信息（主程序）
func (r *DwarfResolver) findLineInfo(addr uint64) (string, int, error) {
	return dwarfFindLineInfo(addr, r.dwarfData)
}

// dwarfFindLineInfo 从指定DWARF数据查找行号信息（包级函数，可被多个解析器复用）
func dwarfFindLineInfo(addr uint64, dwarfData *dwarf.Data) (string, int, error) {
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
	return dwarfFindFunctionName(addr, r.dwarfData)
}

// dwarfFindFunctionName 从指定DWARF数据查找函数名（包级函数，可被多个解析器复用）
func dwarfFindFunctionName(addr uint64, dwarfData *dwarf.Data) string {
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
