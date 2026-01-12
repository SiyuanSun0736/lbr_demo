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
}

// NewUserSymbolResolver 创建用户态符号解析器
func NewUserSymbolResolver(pid int) (*UserSymbolResolver, error) {
	resolver := &UserSymbolResolver{
		pid: pid,
	}

	// 获取可执行文件路径
	execPath, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if err != nil {
		return nil, fmt.Errorf("failed to read exe link: %w", err)
	}
	resolver.execPath = execPath

	// 检查是否有调试符号
	resolver.hasSymbols = resolver.checkDebugSymbols()

	return resolver, nil
}

// checkDebugSymbols 检查可执行文件是否包含调试符号
func (r *UserSymbolResolver) checkDebugSymbols() bool {
	cmd := exec.Command("file", r.execPath)
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(output), "not stripped")
}

// ResolveAddress 使用 addr2line 解析地址
func (r *UserSymbolResolver) ResolveAddress(addr uint64) (string, string, int, error) {
	if !r.hasSymbols {
		return "", "", 0, fmt.Errorf("no debug symbols available")
	}

	// 使用 addr2line 解析地址
	cmd := exec.Command("addr2line", "-e", r.execPath, "-f", "-C", fmt.Sprintf("0x%x", addr))
	output, err := cmd.Output()
	if err != nil {
		return "", "", 0, fmt.Errorf("addr2line failed: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 2 {
		return "", "", 0, fmt.Errorf("unexpected addr2line output")
	}

	funcName := lines[0]
	location := lines[1]

	// 解析位置信息 (file:line)
	parts := strings.Split(location, ":")
	file := ""
	line := 0
	if len(parts) >= 2 {
		file = parts[0]
		if lineNum, err := strconv.Atoi(parts[1]); err == nil {
			line = lineNum
		}
	}

	return funcName, file, line, nil
}

// ResolveBatchAddresses 批量解析地址（更高效）
func (r *UserSymbolResolver) ResolveBatchAddresses(addrs []uint64) ([]AddrInfo, error) {
	if !r.hasSymbols {
		return nil, fmt.Errorf("no debug symbols available")
	}

	results := make([]AddrInfo, len(addrs))

	// 准备addr2line命令的输入
	addrStrings := make([]string, len(addrs))
	for i, addr := range addrs {
		addrStrings[i] = fmt.Sprintf("0x%x", addr)
	}

	// 使用单个addr2line调用批量处理
	cmd := exec.Command("addr2line", "-e", r.execPath, "-f", "-C")
	cmd.Args = append(cmd.Args, addrStrings...)

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("addr2line failed: %w", err)
	}

	// 解析输出
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
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

	return results, nil
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

// GetProcessMaps 获取进程内存映射（用于计算偏移）
func GetProcessMaps(pid int) ([]MemoryMap, error) {
	file, err := os.Open(fmt.Sprintf("/proc/%d/maps", pid))
	if err != nil {
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
