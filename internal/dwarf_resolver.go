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
	pid       int
	execPath  string
	elfFile   *elf.File
	dwarfData *dwarf.Data
	symbols   []ElfSymbol
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
	resolver := &DwarfResolver{
		pid: pid,
	}

	// 获取可执行文件路径
	execPath, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return nil, fmt.Errorf("failed to read exe link: %w", err)
	}
	resolver.execPath = execPath

	// 打开ELF文件
	elfFile, err := elf.Open(execPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open ELF file: %w", err)
	}
	resolver.elfFile = elfFile

	// 加载DWARF调试信息
	dwarfData, err := elfFile.DWARF()
	if err != nil {
		// 没有DWARF信息，尝试使用符号表
		fmt.Printf("Warning: no DWARF debug info available, using symbol table only: %v\n", err)
	} else {
		resolver.dwarfData = dwarfData
	}

	// 加载符号表
	if err := resolver.loadSymbols(); err != nil {
		return nil, fmt.Errorf("failed to load symbols: %w", err)
	}

	return resolver, nil
}

// loadSymbols 加载ELF符号表
func (r *DwarfResolver) loadSymbols() error {
	// 尝试加载动态符号
	dynsyms, err := r.elfFile.DynamicSymbols()
	if err == nil {
		for _, sym := range dynsyms {
			if sym.Name != "" {
				r.symbols = append(r.symbols, ElfSymbol{
					Name:  sym.Name,
					Addr:  sym.Value,
					Size:  sym.Size,
					Type:  elf.SymType(sym.Info & 0xf),
					Value: sym.Value,
				})
			}
		}
	}

	// 加载普通符号表
	syms, err := r.elfFile.Symbols()
	if err == nil {
		for _, sym := range syms {
			if sym.Name != "" {
				r.symbols = append(r.symbols, ElfSymbol{
					Name:  sym.Name,
					Addr:  sym.Value,
					Size:  sym.Size,
					Type:  elf.SymType(sym.Info & 0xf),
					Value: sym.Value,
				})
			}
		}
	}

	// 按地址排序
	sort.Slice(r.symbols, func(i, j int) bool {
		return r.symbols[i].Addr < r.symbols[j].Addr
	})

	return nil
}

// ResolveAddress 解析地址到符号
func (r *DwarfResolver) ResolveAddress(addr uint64) (*AddrInfo, error) {
	info := &AddrInfo{
		Addr: addr,
	}

	// 首先尝试从符号表查找
	funcName := r.findSymbol(addr)
	if funcName != "" {
		info.Function = funcName
	}

	// 如果有DWARF信息，尝试获取更详细的信息
	if r.dwarfData != nil {
		if file, line, err := r.findLineInfo(addr); err == nil {
			info.File = file
			info.Line = line
		}

		// 如果还没有函数名，从DWARF查找
		if info.Function == "" {
			if fn := r.findDwarfFunction(addr); fn != "" {
				info.Function = fn
			}
		}
	}

	if info.Function == "" && info.File == "" {
		return nil, fmt.Errorf("no symbol found for address 0x%x", addr)
	}

	return info, nil
}

// findSymbol 从符号表查找符号
func (r *DwarfResolver) findSymbol(addr uint64) string {
	// 二分查找
	idx := sort.Search(len(r.symbols), func(i int) bool {
		return r.symbols[i].Addr > addr
	})

	if idx > 0 {
		sym := r.symbols[idx-1]
		// 检查地址是否在符号范围内
		if addr >= sym.Addr && (sym.Size == 0 || addr < sym.Addr+sym.Size) {
			return sym.Name
		}
	}

	return ""
}

// findLineInfo 从DWARF查找行号信息
func (r *DwarfResolver) findLineInfo(addr uint64) (string, int, error) {
	if r.dwarfData == nil {
		return "", 0, fmt.Errorf("no DWARF data available")
	}

	reader := r.dwarfData.Reader()
	for {
		entry, err := reader.Next()
		if err != nil || entry == nil {
			break
		}

		if entry.Tag == dwarf.TagCompileUnit {
			lr, err := r.dwarfData.LineReader(entry)
			if err != nil {
				continue
			}

			var lineEntry dwarf.LineEntry
			for {
				err := lr.Next(&lineEntry)
				if err != nil {
					break
				}

				if lineEntry.Address == addr {
					return lineEntry.File.Name, lineEntry.Line, nil
				}
			}
		}
	}

	return "", 0, fmt.Errorf("line info not found")
}

// findDwarfFunction 从DWARF查找函数名
func (r *DwarfResolver) findDwarfFunction(addr uint64) string {
	if r.dwarfData == nil {
		return ""
	}

	reader := r.dwarfData.Reader()
	for {
		entry, err := reader.Next()
		if err != nil || entry == nil {
			break
		}

		if entry.Tag == dwarf.TagSubprogram {
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

			if addr >= lowPC && (highPC == 0 || addr < highPC) {
				if name, ok := entry.Val(dwarf.AttrName).(string); ok {
					return name
				}
			}
		}
	}

	return ""
}

// Close 关闭资源
func (r *DwarfResolver) Close() error {
	if r.elfFile != nil {
		return r.elfFile.Close()
	}
	return nil
}
