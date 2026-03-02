package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	lbr "github.com/SiyuanSun0736/lbr_demo/internal"
	"github.com/cilium/ebpf"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target amd64 -type lbr_data lbr ../bpf/bpf_lbr.c -- -I../bpf -mllvm -bpf-stack-size=2048

type resolverCache struct {
	sframe *lbr.SFrameResolver
	dwarf  *lbr.DwarfResolver
	user   *lbr.UserSymbolResolver // addr2line 回退
}

var (
	targetPID      = flag.Int("pid", 0, "Target PID to monitor (0 = all processes)")
	useAddr2line   = flag.Bool("addr2line", true, "Use addr2line for user symbol resolution")
	useDwarf       = flag.Bool("dwarf", false, "Use DWARF for user symbol resolution (requires debug symbols)")
	useSFrame      = flag.Bool("sframe", false, "Use SFrame for user symbol resolution (lightweight stack unwinding)")
	resolveSymbols = flag.Bool("resolve", true, "Resolve user space addresses to symbols")
	logDir         = flag.String("logdir", "log", "Directory to save log files")
	debugMode      = flag.Bool("debug", false, "Enable debug logging")
)

var logFile *os.File

var pidResolvers = make(map[uint32]*resolverCache)

func main() {
	flag.Parse()

	// 设置调试模式
	lbr.SetDebugMode(*debugMode)

	// 设置日志文件
	if err := setupLogFile(); err != nil {
		log.Fatal(err)
	}
	defer closeLogFile()

	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// resolveAddrInfo 统一解析地址，返回完整符号信息
// 优先级: kallsyms(内核) > SFrame > DWARF > addr2line > 原始地址
func resolveAddrInfo(
	addr uint64,
	syms *lbr.Symbols,
	cache *resolverCache,
) (name string, offset uint64, file string, line int, libName string) {
	if addr == 0 {
		return
	}

	// 1. 内核地址: 使用 kallsyms
	if addr > 0xffff800000000000 {
		if syms != nil {
			n, off, ok := syms.Find(addr)
			if ok {
				return n, off, "", 0, ""
			}
		}
		return "[kernel]", addr, "", 0, ""
	}

	if cache == nil {
		return "[user]", addr, "", 0, ""
	}

	// 2. 用户态地址: SFrame
	if cache.sframe != nil {
		if info, err := cache.sframe.ResolveAddress(addr); err == nil && info.Function != "" {
			return info.Function, 0, info.File, info.Line, info.Library
		}
	}

	// 3. 用户态地址: DWARF
	if cache.dwarf != nil {
		if info, err := cache.dwarf.ResolveAddress(addr); err == nil && info.Function != "" {
			return info.Function, 0, info.File, info.Line, info.Library
		}
	}

	// 4. 用户态地址: addr2line
	if cache.user != nil {
		if fn, f, l, err := cache.user.ResolveAddress(addr); err == nil && fn != "" {
			return fn, 0, f, l, ""
		}
	}

	// 5. 回退: 原始地址
	return "[user]", addr, "", 0, ""
}

// getOrCreateCache 获取或创建 PID 对应的解析器缓存
// 优先级: SFrame > DWARF > addr2line，每级在上一级不可用时作为后备
func getOrCreateCache(pid uint32) *resolverCache {
	if cache, ok := pidResolvers[pid]; ok {
		return cache
	}

	cache := &resolverCache{}

	// 优先尝试 SFrame（轻量级，无需调试符号）
	if *useSFrame {
		sr, err := lbr.NewSFrameResolver(int(pid))
		if err == nil {
			cache.sframe = sr
			log.Printf("[resolver] PID %d: 已启用 SFrame", pid)
		} else {
			log.Printf("[resolver] PID %d: SFrame 失败 (%v)，尝试下一级", pid, err)
		}
	}

	// SFrame 不可用时尝试 DWARF
	if *useDwarf && cache.sframe == nil {
		dr, err := lbr.NewDwarfResolver(int(pid))
		if err == nil {
			cache.dwarf = dr
			log.Printf("[resolver] PID %d: 已启用 DWARF", pid)
		} else {
			log.Printf("[resolver] PID %d: DWARF 失败 (%v)，尝试下一级", pid, err)
		}
	}

	// SFrame 和 DWARF 均不可用时回退到 addr2line
	if *useAddr2line && cache.sframe == nil && cache.dwarf == nil {
		ur, err := lbr.NewUserSymbolResolver(int(pid))
		if err == nil {
			cache.user = ur
			log.Printf("[resolver] PID %d: 已启用 addr2line", pid)
		} else {
			log.Printf("[resolver] PID %d: addr2line 失败 (%v)", pid, err)
		}
	}

	pidResolvers[pid] = cache
	return cache
}

// closeAllResolvers 关闭所有解析器并清空缓存
func closeAllResolvers() {
	for pid, cache := range pidResolvers {
		if cache.sframe != nil {
			cache.sframe.Close()
		}
		if cache.dwarf != nil {
			cache.dwarf.Close()
		}
		// UserSymbolResolver 无需显式关闭（无底层文件句柄）
		delete(pidResolvers, pid)
	}
}

func setupLogFile() error {
	// 创建 log 目录（如果不存在）
	if err := os.MkdirAll(*logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %w", err)
	}

	// 生成日志文件名（带时间戳）
	timestamp := time.Now().Format("20060102_150405")
	logFileName := fmt.Sprintf("lbr_output_%s.log", timestamp)
	logPath := filepath.Join(*logDir, logFileName)

	// 打开日志文件
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %w", err)
	}

	logFile = f

	// 设置日志输出到文件和控制台
	multiWriter := io.MultiWriter(os.Stdout, logFile)
	log.SetOutput(multiWriter)

	log.Printf("日志文件: %s", logPath)

	return nil
}

func closeLogFile() {
	if logFile != nil {
		logFile.Close()
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	defer closeAllResolvers()

	var r syscall.Rlimit
	err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &r)
	if err != nil {
		log.Printf("Failed to get rlimit: %v", err)
	} else {
		log.Printf("Current nofile rlimit: curr=%d max=%d", r.Cur, r.Max)
	}

	if *targetPID != 0 {
		log.Printf("Monitoring PID: %d", *targetPID)
	} else {
		log.Printf("Monitoring all processes")
	}

	// Load BPF program
	spec, err := loadLbr()
	if err != nil {
		return fmt.Errorf("failed to load BPF spec: %w", err)
	}

	// 设置 TARGET_PID
	if err := spec.RewriteConstants(map[string]interface{}{
		"TARGET_PID": uint32(*targetPID),
	}); err != nil {
		return fmt.Errorf("failed to rewrite constants: %w", err)
	}

	numCPU, err := ebpf.PossibleCPU()
	if err != nil {
		return fmt.Errorf("failed to get CPU count: %w", err)
	}

	// Prepare BPF maps
	maps, err := lbr.PrepareBPFMaps(spec)
	if err != nil {
		return fmt.Errorf("failed to prepare BPF maps: %w", err)
	}
	defer lbr.CloseBPFMaps(maps)

	lbrs := maps["lbr_map"]
	commMap := maps["comm_map"]
	objs := &lbrObjects{}
	if err := spec.LoadAndAssign(objs, &ebpf.CollectionOptions{
		Maps: ebpf.MapOptions{
			PinPath: "",
		},
		MapReplacements: maps,
	}); err != nil {
		return fmt.Errorf("failed to load BPF objects: %w", err)
	}
	defer objs.Close()

	// Attach perf_event BPF program
	links, err := lbr.AttachPerfEvent(objs.CaptureLbr, numCPU, *targetPID)
	if err != nil {
		return fmt.Errorf("failed to attach perf_event: %w", err)
	}
	defer lbr.CloseLinks(links)

	// Load symbols
	syms, err := lbr.LoadKallsyms()
	if err != nil {
		log.Printf("Warning: failed to load kallsyms: %v", err)
	}

	log.Println("LBR demo is running. Press Ctrl-C to exit.")
	log.Printf("Attached to %d CPUs, checking for LBR data every second...", numCPU)

	// Process events
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			processLbrData(lbrs, commMap, syms, *targetPID)
		}
	}
}

func processLbrData(lbrMap *ebpf.Map, commMap *ebpf.Map, syms *lbr.Symbols, targetPID int) {
	var (
		key  uint64
		data lbrLbrData
	)

	// 获取当前进程的 PID，用于过滤自己
	currentPID := uint32(os.Getpid())

	iter := lbrMap.Iterate()
	totalEntries := 0
	validEntries := 0
	emptyEntries := 0
	allZeroEntries := 0

	for iter.Next(&key, &data) {
		totalEntries++

		if data.NrBytes <= 0 {
			emptyEntries++
			continue
		}

		numEntries := int(data.NrBytes) / (8 * 3) // 3 * sizeof(u64)

		// 检查是否所有地址都是 0（无效数据）
		allZero := true
		for i := 0; i < numEntries && i < 32; i++ {
			if data.Entries[i].From != 0 || data.Entries[i].To != 0 {
				allZero = false
				break
			}
		}

		if allZero {
			allZeroEntries++
			_ = lbrMap.Delete(key)
			continue
		}

		validEntries++
		pid := uint32(key >> 32)
		tid := uint32(key & 0xFFFFFFFF)

		// 跳过自己的进程，避免监测自己
		if pid == currentPID {
			_ = lbrMap.Delete(key)
			continue
		}

		// 按需获取或创建该 PID 的符号解析器缓存（SFrame > DWARF > addr2line）
		var cache *resolverCache
		if *resolveSymbols && (targetPID == 0 || int(pid) == targetPID) {
			cache = getOrCreateCache(pid)
		}

		// 获取进程名称
		var comm [16]byte
		commName := "<unknown>"
		if err := commMap.Lookup(&key, &comm); err == nil {
			for i, b := range comm {
				if b == 0 {
					commName = string(comm[:i])
					break
				}
			}
			if commName == "<unknown>" {
				commName = string(comm[:])
			}
		}

		stack := lbr.NewStack()
		for i := 0; i < numEntries && i < 32; i++ {
			entry := &data.Entries[i]

			// 统一解析 From / To 地址（内核符号 → SFrame → DWARF → addr2line → 原始地址）
			fromName, fromOff, fromFile, fromLine, fromLib := resolveAddrInfo(entry.From, syms, cache)
			toName, toOff, toFile, toLine, toLib := resolveAddrInfo(entry.To, syms, cache)

			stack.AddEntry(lbr.BranchEntry{
				From: &lbr.BranchEndpoint{
					Addr:     entry.From,
					FuncName: fromName,
					Offset:   fromOff,
					File:     fromFile,
					Line:     fromLine,
					LibName:  fromLib,
				},
				To: &lbr.BranchEndpoint{
					Addr:     entry.To,
					FuncName: toName,
					Offset:   toOff,
					File:     toFile,
					Line:     toLine,
					LibName:  toLib,
				},
			})
		}

		// 输出到控制台和日志文件
		output := fmt.Sprintf("\n=== PID: %d, TID: %d, COMM: %s, Entries: %d ===\n", pid, tid, commName, numEntries)
		fmt.Print(output)
		if logFile != nil {
			logFile.WriteString(output)
		}
		stack.Output(os.Stdout)
		if logFile != nil {
			stack.Output(logFile)
		}

		// 删除已处理的条目
		_ = lbrMap.Delete(key)
	}

	// 输出统计信息
	if totalEntries == 0 {
		log.Printf("No LBR data - map中无任何条目")
	} else if validEntries == 0 {
		log.Printf("No valid LBR data - 总条目=%d, 空数据=%d, 全零数据=%d",
			totalEntries, emptyEntries, allZeroEntries)
	} else {
		log.Printf("LBR数据统计: 总条目=%d, 有效=%d, 空数据=%d, 全零数据=%d",
			totalEntries, validEntries, emptyEntries, allZeroEntries)
	}
}
