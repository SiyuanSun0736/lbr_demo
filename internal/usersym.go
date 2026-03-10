package lbr

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// UserSymbolResolver 用户态符号解析器
type UserSymbolResolver struct {
	pid        int
	execPath   string
	baseAddr   uint64
	hasSymbols bool
	maps       []MemoryMap // 缓存的内存映射
}

// NewUserSymbolResolver 创建用户态符号解析器
func NewUserSymbolResolver(pid int) (*UserSymbolResolver, error) {
	debugLog("[DEBUG] NewUserSymbolResolver: 为PID %d 创建解析器\n", pid)
	resolver := &UserSymbolResolver{
		pid: pid,
	}

	// 获取可执行文件路径
	execPath, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return nil, fmt.Errorf("failed to read exe link: %w", err)
	}
	resolver.execPath = execPath
	debugLog("[DEBUG] NewUserSymbolResolver: 使用可执行文件: %s\n", execPath)

	// 获取并缓存内存映射
	maps, err := GetProcessMaps(pid)
	if err != nil {
		return nil, fmt.Errorf("failed to get process maps: %w", err)
	}
	resolver.maps = maps

	// 获取基地址
	if err := resolver.loadBaseAddress(); err != nil {
		debugLog("[DEBUG] NewUserSymbolResolver: 获取基地址失败: %v\n", err)
		// 不返回错误，继续执行，某些情况下可能不需要基地址
	}
	debugLog("[DEBUG] NewUserSymbolResolver: 基地址: 0x%x\n", resolver.baseAddr)

	// 检查是否有调试符号
	resolver.hasSymbols = resolver.checkDebugSymbols()
	debugLog("[DEBUG] NewUserSymbolResolver: 调试符号检查结果: %v\n", resolver.hasSymbols)

	debugLog("[DEBUG] NewUserSymbolResolver: 解析器创建成功\n")
	return resolver, nil
}

// loadBaseAddress 获取进程的基地址
func (r *UserSymbolResolver) loadBaseAddress() error {
	// 查找主可执行文件的第一个可执行段
	for _, m := range r.maps {
		if strings.Contains(m.Perms, "x") && m.Pathname == r.execPath {
			r.baseAddr = m.StartAddr - m.Offset
			debugLog("[DEBUG] loadBaseAddress: 找到基地址 0x%x (StartAddr=0x%x, Offset=0x%x)\n",
				r.baseAddr, m.StartAddr, m.Offset)
			return nil
		}
	}

	return fmt.Errorf("executable not found in memory maps")
}

// findMemoryMap 查找地址所属的内存映射
func (r *UserSymbolResolver) findMemoryMap(addr uint64) *MemoryMap {
	for i := range r.maps {
		if addr >= r.maps[i].StartAddr && addr < r.maps[i].EndAddr {
			return &r.maps[i]
		}
	}
	return nil
}

// checkDebugSymbols 检查可执行文件是否包含调试符号
func (r *UserSymbolResolver) checkDebugSymbols() bool {
	debugLog("[DEBUG] checkDebugSymbols: 检查文件 %s 的调试符号\n", r.execPath)
	cmd := exec.Command("file", r.execPath)
	output, err := cmd.Output()
	if err != nil {
		debugLog("[DEBUG] checkDebugSymbols: file 命令执行失败: %v\n", err)
		return false
	}
	hasSymbols := strings.Contains(string(output), "not stripped")
	debugLog("[DEBUG] checkDebugSymbols: file 输出: %s\n", strings.TrimSpace(string(output)))
	debugLog("[DEBUG] checkDebugSymbols: 结果: %v\n", hasSymbols)
	return hasSymbols
}

// ResolveAddress 使用 addr2line 解析地址
func (r *UserSymbolResolver) ResolveAddress(addr uint64) (string, string, int, error) {
	debugLog("[DEBUG] ResolveAddress: 开始解析地址 0x%x\n", addr)

	// 查找地址所属的内存映射
	mmap := r.findMemoryMap(addr)
	if mmap == nil {
		debugLog("[DEBUG] ResolveAddress: 地址 0x%x 不在任何内存映射中\n", addr)
		return "", "", 0, fmt.Errorf("address not in any memory map")
	}

	// 确定目标文件和偏移量
	var targetFile string
	var fileOffset uint64
	var library string

	if mmap.Pathname == "" || mmap.Pathname == "[stack]" || mmap.Pathname == "[heap]" ||
		mmap.Pathname == "[vdso]" || mmap.Pathname == "[vsyscall]" {
		debugLog("[DEBUG] ResolveAddress: 地址在特殊区域: %s\n", mmap.Pathname)
		return "", "", 0, fmt.Errorf("address in special region: %s", mmap.Pathname)
	}

	if mmap.Pathname == r.execPath {
		// 主可执行文件
		if !r.hasSymbols {
			debugLog("[DEBUG] ResolveAddress: 主程序无调试符号\n")
			return "", "", 0, fmt.Errorf("no debug symbols available")
		}
		targetFile = r.execPath
		fileOffset = addr - r.baseAddr
		debugLog("[DEBUG] ResolveAddress: 主程序地址，基地址 0x%x, 文件偏移 0x%x\n",
			r.baseAddr, fileOffset)
	} else if strings.HasSuffix(mmap.Pathname, ".so") || strings.Contains(mmap.Pathname, ".so.") {
		// 共享库
		targetFile = mmap.Pathname
		// 对于共享库，文件偏移 = 运行时地址 - 映射起始地址 + 映射偏移
		fileOffset = addr - mmap.StartAddr + mmap.Offset
		library = getLibraryName(mmap.Pathname)
		debugLog("[DEBUG] ResolveAddress: 共享库 %s, 映射起始 0x%x, 映射偏移 0x%x, 文件偏移 0x%x\n",
			library, mmap.StartAddr, mmap.Offset, fileOffset)
	} else {
		debugLog("[DEBUG] ResolveAddress: 未知文件类型: %s\n", mmap.Pathname)
		return "", "", 0, fmt.Errorf("unknown file type: %s", mmap.Pathname)
	}

	// 检查目标文件是否存在
	if _, err := os.Stat(targetFile); err != nil {
		debugLog("[DEBUG] ResolveAddress: 文件不存在: %s\n", targetFile)
		return "", "", 0, fmt.Errorf("file not found: %s", targetFile)
	}

	// 使用 addr2line 解析地址
	debugLog("[DEBUG] ResolveAddress: 执行 addr2line，文件: %s, 偏移: 0x%x\n", targetFile, fileOffset)
	cmd := exec.Command("addr2line", "-e", targetFile, "-f", "-C", fmt.Sprintf("0x%x", fileOffset))
	output, err := cmd.Output()
	if err != nil {
		debugLog("[DEBUG] ResolveAddress: addr2line 失败: %v\n", err)
		return "", "", 0, fmt.Errorf("addr2line failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	debugLog("[DEBUG] ResolveAddress: addr2line 输出行数: %d\n", len(lines))
	if len(lines) < 2 {
		debugLog("[DEBUG] ResolveAddress: addr2line 输出格式异常\n")
		return "", "", 0, fmt.Errorf("unexpected addr2line output")
	}

	funcName := lines[0]
	location := lines[1]

	// 如果 addr2line 无法解析（返回 ??），尝试使用符号表
	if funcName == "??" || funcName == "" {
		debugLog("[DEBUG] ResolveAddress: addr2line 未解析，尝试符号表\n")
		symName := r.resolveFromSymbolTable(targetFile, fileOffset)
		if symName != "" {
			funcName = symName
			if library != "" {
				funcName = fmt.Sprintf("%s@%s", funcName, library)
			}
			debugLog("[DEBUG] ResolveAddress: 从符号表解析到: %s\n", funcName)
		} else {
			// 无法从符号表解析，返回地址
			funcName = fmt.Sprintf("0x%x", addr)
			if library != "" {
				funcName = fmt.Sprintf("%s@%s", funcName, library)
			}
			debugLog("[DEBUG] ResolveAddress: 无法解析，返回地址: %s\n", funcName)
		}
	} else {
		// 如果是共享库且函数名有效，添加库名前缀
		if library != "" {
			funcName = fmt.Sprintf("%s@%s", funcName, library)
		}
	}

	debugLog("[DEBUG] ResolveAddress: 函数名: %s, 位置: %s\n", funcName, location)

	// 解析位置信息 (file:line)
	parts := strings.Split(location, ":")
	file := ""
	line := 0
	if len(parts) >= 2 {
		file = parts[0]
		// 只有当不是 ?? 时才解析行号
		if parts[1] != "?" && parts[1] != "0" {
			if lineNum, err := strconv.Atoi(parts[1]); err == nil {
				line = lineNum
			}
		}
	}

	debugLog("[DEBUG] ResolveAddress: 解析成功 -> 函数: %s, 文件: %s, 行号: %d\n", funcName, file, line)
	return funcName, file, line, nil
}

// resolveFromSymbolTable 从符号表解析地址
func (r *UserSymbolResolver) resolveFromSymbolTable(file string, offset uint64) string {
	// 使用 nm 命令读取符号表
	cmd := exec.Command("nm", "-C", file)
	output, err := cmd.Output()
	if err != nil {
		debugLog("[DEBUG] resolveFromSymbolTable: nm 失败: %v\n", err)
		return ""
	}

	// 解析 nm 输出，查找最接近的符号
	var closestSymbol string
	var closestDist uint64 = ^uint64(0) // max uint64

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		// nm 输出格式: address type name
		symAddr, err := strconv.ParseUint(fields[0], 16, 64)
		if err != nil {
			continue
		}

		symType := fields[1]
		symName := strings.Join(fields[2:], " ")

		// 只关注代码段符号 (T, t, W, w)
		if symType != "T" && symType != "t" && symType != "W" && symType != "w" {
			continue
		}

		// 查找地址在符号之后且距离最近的符号
		if symAddr <= offset {
			dist := offset - symAddr
			if dist < closestDist {
				closestDist = dist
				closestSymbol = symName
				if dist == 0 {
					break // 精确匹配
				}
			}
		}
	}

	if closestSymbol != "" {
		if closestDist > 0 {
			return fmt.Sprintf("%s+0x%x", closestSymbol, closestDist)
		}
		return closestSymbol
	}

	return ""
}

// ResolveBatchAddresses 批量解析地址（更高效）
func (r *UserSymbolResolver) ResolveBatchAddresses(addrs []uint64) ([]AddrInfo, error) {
	debugLog("[DEBUG] ResolveBatchAddresses: 开始批量解析 %d 个地址\n", len(addrs))
	if !r.hasSymbols {
		debugLog("[DEBUG] ResolveBatchAddresses: 无调试符号\n")
		return nil, fmt.Errorf("no debug symbols available")
	}

	results := make([]AddrInfo, len(addrs))

	// 准备addr2line命令的输入（转换为文件偏移）
	addrStrings := make([]string, len(addrs))
	for i, addr := range addrs {
		fileOffset := addr
		if r.baseAddr > 0 {
			fileOffset = addr - r.baseAddr
		}
		addrStrings[i] = fmt.Sprintf("0x%x", fileOffset)
	}

	// 使用单个addr2line调用批量处理
	debugLog("[DEBUG] ResolveBatchAddresses: 执行批量 addr2line 命令\n")
	cmd := exec.Command("addr2line", "-e", r.execPath, "-f", "-C")
	cmd.Args = append(cmd.Args, addrStrings...)

	output, err := cmd.Output()
	if err != nil {
		debugLog("[DEBUG] ResolveBatchAddresses: addr2line 失败: %v\n", err)
		return nil, fmt.Errorf("addr2line failed: %w", err)
	}

	// 解析输出
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	debugLog("[DEBUG] ResolveBatchAddresses: addr2line 输出行数: %d\n", len(lines))
	for i := 0; i < len(addrs) && i*2+1 < len(lines); i++ {
		funcName := lines[i*2]
		location := lines[i*2+1]

		results[i].Addr = addrs[i]
		results[i].Function = funcName

		// 解析位置
		parts := strings.Split(location, ":")
		if len(parts) >= 2 {
			results[i].File = parts[0]
			if lineNum, err := strconv.Atoi(parts[1]); err == nil {
				results[i].Line = lineNum
			}
		}
	}

	debugLog("[DEBUG] ResolveBatchAddresses: 批量解析完成，成功解析 %d 个地址\n", len(results))
	return results, nil
}

// getLibraryName 从路径中提取库名称
func getLibraryName(path string) string {
	// 从路径中提取文件名
	parts := strings.Split(path, "/")
	if len(parts) == 0 {
		return path
	}
	filename := parts[len(parts)-1]

	// 移除版本号后缀（如 libc-2.31.so -> libc.so）
	if idx := strings.Index(filename, ".so"); idx != -1 {
		// 保留 .so 之前的部分
		base := filename[:idx]
		// 移除版本号（如 libc-2.31 -> libc）
		if dashIdx := strings.LastIndex(base, "-"); dashIdx != -1 {
			if _, err := strconv.ParseFloat(base[dashIdx+1:], 64); err == nil {
				base = base[:dashIdx]
			}
		}
		return base + ".so"
	}
	return filename
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

// GetFileOffset 根据虚拟地址返回其所在文件的路径及文件内偏移（用于 uprobe 挂载）。
// 不调用 addr2line，仅依赖 /proc/pid/maps 完成计算。
func (r *UserSymbolResolver) GetFileOffset(addr uint64) (filePath string, fileOffset uint64, err error) {
	mmap := r.findMemoryMap(addr)
	if mmap == nil {
		return "", 0, fmt.Errorf("address 0x%x not in any memory map", addr)
	}
	if mmap.Pathname == "" || mmap.Pathname == "[stack]" || mmap.Pathname == "[heap]" ||
		mmap.Pathname == "[vdso]" || mmap.Pathname == "[vsyscall]" {
		return "", 0, fmt.Errorf("address in special region: %s", mmap.Pathname)
	}
	if mmap.Pathname == r.execPath {
		// 主可执行文件（含 PIE）：file_offset = VA - segment_start + segment_file_offset
		// 与共享库算法一致，避免 baseAddr 计算差异导致偏移错误
		return r.execPath, addr - mmap.StartAddr + mmap.Offset, nil
	}
	// 共享库：file_offset = VA - segment_start + segment_file_offset
	return mmap.Pathname, addr - mmap.StartAddr + mmap.Offset, nil
}

// GetProcessMaps 获取进程内存映射（用于计算偏移）
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
		line := scanner.Text()
		var m MemoryMap
		if err := m.Parse(line); err == nil {
			maps = append(maps, m)
		}
	}

	debugLog("[DEBUG] GetProcessMaps: 读取到 %d 个内存映射\n", len(maps))
	return maps, scanner.Err()
}

// MemoryMap 表示进程的内存映射
type MemoryMap struct {
	StartAddr uint64
	EndAddr   uint64
	Perms     string
	Offset    uint64
	Device    string
	Inode     uint64
	Pathname  string
}

// Parse 解析 /proc/pid/maps 的一行
func (m *MemoryMap) Parse(line string) error {
	fields := strings.Fields(line)
	if len(fields) < 5 {
		return fmt.Errorf("invalid maps line")
	}

	// 解析地址范围
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
