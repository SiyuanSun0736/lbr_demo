package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"
	"unsafe"

	lbr "github.com/SiyuanSun0736/lbr_demo/internal"
	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"github.com/cilium/ebpf/ringbuf"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target amd64 -type lbr_data lbr ../bpf/bpf_lbr.c -- -I../bpf -mllvm -bpf-stack-size=2048
//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target amd64 uprobe ../bpf/bpf_uprobe.c -- -I../bpf

var (
	targetPID      = flag.Int("pid", 0, "Target PID to monitor (0 = all processes)")
	useAddr2line   = flag.Bool("addr2line", true, "Use addr2line for user symbol resolution")
	useDwarf       = flag.Bool("dwarf", true, "Use DWARF for user symbol resolution (requires debug symbols)")
	useSFrame      = flag.Bool("sframe", true, "Use SFrame for user symbol resolution (lightweight stack unwinding)")
	resolveSymbols = flag.Bool("resolve", true, "Resolve user space addresses to symbols")
	logDir         = flag.String("logdir", "log", "Directory to save log files")
	debugMode      = flag.Bool("debug", false, "Enable debug logging")
	twoPassMode    = flag.Bool("two-pass", false, "启用两阶段模式：先采集热点地址（按 Ctrl-C 结束），再对热点地址进行详细采样和栈展开")
	topNAddresses  = flag.Int("top-n", 5, "两阶段模式中关注的热点地址数量")
)

var logFile *os.File

// uprobeStackBytes 与 bpf_uprobe.c 中 UPROBE_STACK_BYTES 保持一致
const uprobeStackBytes = 1024

// uprobeRegs 与 bpf_uprobe.c 中的 struct uprobe_regs 内存布局保持一致
type uprobeRegs struct {
	PidTgid   uint64
	ProbeAddr uint64
	Rip       uint64
	Rsp       uint64
	Rbp       uint64
	StackLen  uint32
	StackData [uprobeStackBytes]byte
}

// AddrContext 存储地址及其访问次数
type AddrContext struct {
	Addr  uint64
	Count uint64
	Pid   uint32 // 首次观察到该地址时的 PID，用于符号解析
}

// 全局地址访问统计
var addrStats = make(map[uint64]*AddrContext)

// addrCount 用于排序地址统计
type addrCount struct {
	addr uint64
	ctx  *AddrContext
}

// 两阶段模式状态
var (
	currentPhase         = 1
	topAddrsSet          = make(map[uint64]bool)
	phase1UserResolver   *lbr.UserSymbolResolver // 进程存活期间创建，供 Phase1 统计导出使用
	phase1SFrameResolver *lbr.SFrameResolver     // 持久化 SFrame 解析器，供 Phase1 统计导出使用（含函数内偏移）
)

// uprobe Phase 2 资源
var (
	uprobeObjs        *uprobeObjects // 已加载的 uprobe BPF 对象
	uprobeLinks       []link.Link    // 动态挂载的全量 uprobe link 列表，程序退出时统一关闭
	uprobeByAddr      sync.Map       // VA(uint64) -> link.Link，用于 one-shot 触发后立即卸载
	uprobeFileKeyToVA sync.Map       // "filePath:0xOffset" -> VA(uint64)，ASLR 跨进程反查时使用
)

// phase2CaptureResult 保存单次 uprobe 触发的寄存器快照及展开后的调用栈
type phase2CaptureResult struct {
	regs   uprobeRegs
	frames []lbr.StackFrame
}

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
	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(sigCh)

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

	// 如果启用两阶段模式，提前加载 uprobe BPF 对象，等 Phase 2 时按需挂载
	if *twoPassMode {
		uprobeObjs = &uprobeObjects{}
		if err := loadUprobeObjects(uprobeObjs, nil); err != nil {
			log.Printf("Warning: 无法加载 uprobe BPF 对象，Phase 2 将不使用 uprobe: %v", err)
			uprobeObjs = nil
		} else {
			defer uprobeObjs.Close()
			defer func() {
				for _, l := range uprobeLinks {
					l.Close()
				}
			}()
			if *targetPID != 0 {
				if err := uprobeObjs.UPROBE_TARGET_PID.Set(uint32(*targetPID)); err != nil {
					log.Printf("Warning: 设置 UPROBE_TARGET_PID 失败: %v", err)
				}
			}
			log.Println("uprobe BPF 程序已就绪，Phase 2 将自动挂载到热点地址")
		}
	}

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

	var sframeResolver *lbr.SFrameResolver

	if *twoPassMode {
		log.Println("LBR demo 运行中（两阶段模式）。")
		log.Println("  Phase 1：正在采集热点地址，按 Ctrl-C 结束采集并进入 Phase 2。")
		log.Println("  Phase 2：对热点地址挂载 uprobe，完成后自动退出（再次 Ctrl-C 可强制退出）。")
	} else {
		log.Println("LBR demo is running. Press Ctrl-C to exit.")
	}
	log.Printf("Attached to %d CPUs, checking for LBR data every second...", numCPU)

	// Process events
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// Phase 2 完成后 cancelCtx() 被调用，直接退出
			return nil
		case sig := <-sigCh:
			if *twoPassMode && currentPhase == 1 {
				// 两阶段模式第一阶段：Ctrl-C 触发切换到 Phase 2
				log.Printf("\n[两阶段模式] 收到信号 %v，结束 Phase 1，开始 Phase 2 分析...", sig)
				switchToPhase2(sframeResolver, ctx, cancelCtx)
			} else {
				// 非两阶段模式，或 Phase 2 中强制退出
				log.Println("\n收到退出信号，正在分析热点地址统计...")
				analyzeTopAddresses(sframeResolver)
				return nil
			}
		case <-ticker.C:
			if !*twoPassMode || currentPhase == 1 {
				processLbrData(lbrs, commMap, syms, *targetPID, sframeResolver)
			}
		}
	}
}

func processLbrData(lbrMap *ebpf.Map, commMap *ebpf.Map, syms *lbr.Symbols, targetPID int, extResolver *lbr.SFrameResolver) {
	var (
		key  uint64
		data lbrLbrData
	)

	// 获取当前进程的 PID，用于过滤自己
	currentPID := uint32(os.Getpid())

	// 用户态符号解析器（缓存）
	var dwarfResolver *lbr.DwarfResolver
	var sframeResolver *lbr.SFrameResolver

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

		// 如果需要解析用户态符号，且这是目标进程，创建解析器
		if *resolveSymbols && int(pid) == targetPID && targetPID != 0 {
			// phase1UserResolver 无条件创建：仅依赖 /proc/pid/maps，
			// 用于 GetFileOffset 计算文件偏移，与 SFrame/DWARF 无关。
			if phase1UserResolver == nil {
				if ur, err := lbr.NewUserSymbolResolver(int(pid)); err == nil {
					phase1UserResolver = ur
					log.Printf("已创建 UserSymbolResolver for PID %d（用于 file_offset 计算）", pid)
				} else {
					log.Printf("UserSymbolResolver 创建失败: %v", err)
				}
			}
			// 符号名解析优先级: SFrame > DWARF > addr2line
			if *useSFrame && sframeResolver == nil {
				// 复用全局实例：避免每次轮询创建/关闭，同时保证 writePhase1StatsToFile 可用
				if phase1SFrameResolver != nil {
					sframeResolver = phase1SFrameResolver
				} else if sr, err := lbr.NewSFrameResolver(int(pid)); err == nil {
					phase1SFrameResolver = sr
					sframeResolver = sr
					log.Printf("已启用 SFrame 符号解析 for PID %d", pid)
				} else {
					log.Printf("SFrame 解析器创建失败: %v，回退到 DWARF 或 addr2line", err)
				}
			}
			if *useDwarf && dwarfResolver == nil && sframeResolver == nil {
				if dr, err := lbr.NewDwarfResolver(int(pid)); err == nil {
					dwarfResolver = dr
					defer dwarfResolver.Close()
					log.Printf("已启用 DWARF 符号解析 for PID %d", pid)
				} else {
					log.Printf("DWARF 解析器创建失败: %v，回退到 addr2line", err)
				}
			}
		}

		// 获取进程名称
		var comm [16]byte
		commName := "<unknown>"
		if err := commMap.Lookup(&key, &comm); err == nil {
			// 找到第一个空字节作为字符串结尾
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

			// 先尝试内核符号
			fromName, fromOffset, _ := syms.Find(entry.From)
			toName, toOffset, _ := syms.Find(entry.To)

			var fromFile, toFile string
			var fromLine, toLine int

			var fromLibName, toLibName string

			// 如果找不到内核符号，尝试解析用户态地址
			if fromName == "" && entry.From != 0 {
				if sframeResolver != nil {
					if info, err := sframeResolver.ResolveAddress(entry.From); err == nil {
						fromName = info.Function
						fromFile = info.File
						fromLine = info.Line
						fromLibName = info.Library
						fromOffset = 0
					}
				} else if dwarfResolver != nil {
					if info, err := dwarfResolver.ResolveAddress(entry.From); err == nil {
						fromName = info.Function
						fromFile = info.File
						fromLine = info.Line
						fromLibName = info.Library
						fromOffset = 0
					}
				} else if phase1UserResolver != nil {
					if fn, file, line, err := phase1UserResolver.ResolveAddress(entry.From); err == nil {
						fromName = fn
						fromFile = file
						fromLine = line
						fromOffset = 0
					}
				}
				// 如果仍然没有解析到，标记为用户态地址
				if fromName == "" {
					fromName = "[user]"
					fromOffset = entry.From
				}
			}
			if toName == "" && entry.To != 0 {
				if sframeResolver != nil {
					if info, err := sframeResolver.ResolveAddress(entry.To); err == nil {
						toName = info.Function
						toFile = info.File
						toLine = info.Line
						toLibName = info.Library
						toOffset = 0
					}
				} else if dwarfResolver != nil {
					if info, err := dwarfResolver.ResolveAddress(entry.To); err == nil {
						toName = info.Function
						toFile = info.File
						toLine = info.Line
						toLibName = info.Library
						toOffset = 0
					}
				} else if phase1UserResolver != nil {
					if fn, file, line, err := phase1UserResolver.ResolveAddress(entry.To); err == nil {
						toName = fn
						toFile = file
						toLine = line
						toOffset = 0
					}
				}
				// 如果仍然没有解析到，标记为用户态地址
				if toName == "" {
					toName = "[user]"
					toOffset = entry.To
				}
			}

			stack.AddEntry(lbr.BranchEntry{
				From: &lbr.BranchEndpoint{
					Addr:     entry.From,
					FuncName: fromName,
					Offset:   fromOffset,
					File:     fromFile,
					Line:     fromLine,
					LibName:  fromLibName,
				},
				To: &lbr.BranchEndpoint{
					Addr:     entry.To,
					FuncName: toName,
					Offset:   toOffset,
					File:     toFile,
					Line:     toLine,
					LibName:  toLibName,
				},
			})
		}

		// 输出到控制台和日志文件
		output := fmt.Sprintf("\n=== PID: %d, TID: %d, COMM: %s, Entries: %d ===\n", pid, tid, commName, numEntries)
		fmt.Print(output)
		if logFile != nil {
			logFile.WriteString(output)
		}

		// 输出LBR分支历史
		lbrHeader := "\n[LBR Branch History]\n"
		fmt.Print(lbrHeader)
		if logFile != nil {
			logFile.WriteString(lbrHeader)
		}
		// 输出到控制台
		stack.Output(os.Stdout)
		// 也输出到日志文件
		if logFile != nil {
			stack.Output(logFile)
		}

		// 收集地址访问统计
		for i := 0; i < numEntries && i < 32; i++ {
			entry := &data.Entries[i]

			// 统计From地址
			if entry.From != 0 {
				if stat, exists := addrStats[entry.From]; exists {
					stat.Count++
				} else {
					addrStats[entry.From] = &AddrContext{Addr: entry.From, Count: 1, Pid: pid}
				}
			}

			// 统计To地址
			if entry.To != 0 {
				if stat, exists := addrStats[entry.To]; exists {
					stat.Count++
				} else {
					addrStats[entry.To] = &AddrContext{Addr: entry.To, Count: 1, Pid: pid}
				}
			}
		}

		// Phase 2：检测热点地址并执行详细分析
		if *twoPassMode && currentPhase == 2 {
			var hotAddr uint64
			for i := 0; i < numEntries && i < 32; i++ {
				entry := &data.Entries[i]
				if topAddrsSet[entry.From] {
					hotAddr = entry.From
					break
				}
				if topAddrsSet[entry.To] {
					hotAddr = entry.To
					break
				}
			}
			if hotAddr != 0 {
				activeResolver := extResolver
				if activeResolver == nil {
					activeResolver = sframeResolver
				}
				performPhase2Analysis(hotAddr, pid, activeResolver)
			}
		}

		// Delete processed entry
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

// switchToPhase2 从第一阶段切换到第二阶段，识别热点地址并将统计写入文件
func switchToPhase2(sframeResolver *lbr.SFrameResolver, runCtx context.Context, stop context.CancelFunc) {
	if len(addrStats) == 0 {
		log.Println("[两阶段模式] Phase 1 结束：无统计数据，继续使用第一阶段模式")
		return
	}

	stats := make([]addrCount, 0, len(addrStats))
	for addr, ctx := range addrStats {
		stats = append(stats, addrCount{addr: addr, ctx: ctx})
	}

	// 按访问次数排序（降序）
	for i := 0; i < len(stats); i++ {
		for j := i + 1; j < len(stats); j++ {
			if stats[j].ctx.Count > stats[i].ctx.Count {
				stats[i], stats[j] = stats[j], stats[i]
			}
		}
	}

	topN := *topNAddresses
	if len(stats) < topN {
		topN = len(stats)
	}

	log.Printf("\n========================================")
	log.Printf("[两阶段模式] Phase 1 结束，识别到 %d 个热点地址:", topN)
	log.Printf("========================================")
	for i := 0; i < topN; i++ {
		topAddrsSet[stats[i].addr] = true
		symInfo := ""
		if sframeResolver != nil {
			if info, err := sframeResolver.ResolveAddress(stats[i].addr); err == nil && info.Function != "" {
				symInfo = fmt.Sprintf(" (%s)", info.Function)
			}
		}
		log.Printf("  #%d: 0x%016x  访问次数: %d%s", i+1, stats[i].addr, stats[i].ctx.Count, symInfo)
	}
	log.Printf("========================================\n")

	// 将所有地址统计写入文件
	writePhase1StatsToFile(stats, phase1UserResolver)

	// 对 top-N 热点地址动态挂载 uprobe，捕获寄存器快照
	attachPhase2Uprobes(stats[:topN], *targetPID)
	startUprobeReader(runCtx, stop)

	// 重置统计，开始第二阶段采集
	addrStats = make(map[uint64]*AddrContext)
	currentPhase = 2
	log.Println("[两阶段模式] Phase 2 开始：对上述热点地址进行详细采样和栈展开")
}

// attachPhase2Uprobes 根据 Phase 1 识别的热点地址，在目标二进制的 file_offset 处动态挂载 uprobe。
// 挂载成功后，执行流经过这些地址时将触发 BPF handler 并向 ring buffer 写入寄存器快照。
func attachPhase2Uprobes(topAddrs []addrCount, pid int) {
	if uprobeObjs == nil {
		log.Println("[Phase 2] uprobe BPF 对象未就绪，跳过 uprobe 挂载")
		return
	}
	if phase1UserResolver == nil {
		log.Println("[Phase 2] UserResolver 未就绪，无法计算 file_offset，跳过 uprobe 挂载")
		return
	}

	type entry struct {
		filePath   string
		fileOffset uint64
		va         uint64
	}

	seen := make(map[string]bool)
	var entries []entry
	for _, ac := range topAddrs {
		fp, fo, err := phase1UserResolver.GetFileOffset(ac.addr)
		if err != nil {
			log.Printf("[Phase 2] GetFileOffset(0x%x) 失败: %v", ac.addr, err)
			continue
		}
		key := fmt.Sprintf("%s:0x%x", fp, fo)
		if seen[key] {
			continue
		}
		seen[key] = true
		entries = append(entries, entry{fp, fo, ac.addr})
	}

	for _, e := range entries {
		exe, err := link.OpenExecutable(e.filePath)
		if err != nil {
			log.Printf("[Phase 2] OpenExecutable(%s) 失败: %v", e.filePath, err)
			continue
		}
		opts := &link.UprobeOptions{Address: e.fileOffset}
		mountedWithPID := false
		if pid > 0 {
			opts.PID = pid
			mountedWithPID = true
		}

		log.Printf("[Phase 2] 尝试挂载 uprobe: file=%s offset=0x%x pid=%d", e.filePath, e.fileOffset, pid)
		// 第一个参数 symbol 为空字符串，Address 字段直接指定文件偏移，绕过 ELF symbol 查找
		l, err := exe.Uprobe("", uprobeObjs.CaptureUprobe, opts)
		if err != nil && pid > 0 {
			// 目标进程可能已退出（ESRCH），回退到全局挂载（不绑定 PID）
			log.Printf("[Phase 2] uprobe PID 绑定挂载失败 (VA=0x%x)，回退到全局挂载: %v", e.va, err)
			optsGlobal := &link.UprobeOptions{Address: e.fileOffset}
			l, err = exe.Uprobe("", uprobeObjs.CaptureUprobe, optsGlobal)
			mountedWithPID = false
		}
		if err != nil {
			log.Printf("[Phase 2] uprobe 挂载失败 (file=%s offset=0x%x VA=0x%x): %v",
				e.filePath, e.fileOffset, e.va, err)
			continue
		}
		uprobeLinks = append(uprobeLinks, l)
		uprobeByAddr.Store(e.va, l) // 记录 VA->link，供 one-shot 卸载
		fileKey := fmt.Sprintf("%s:0x%x", e.filePath, e.fileOffset)
		uprobeFileKeyToVA.Store(fileKey, e.va) // 辅助反查：文件偏移 -> 原始 VA（应对跨进程 ASLR）
		if *debugMode {
			log.Printf("[Phase 2] uprobe 已挂载: %s + 0x%x  (VA=0x%x)", e.filePath, e.fileOffset, e.va)
			// 额外打印以便在 stdout 明确看到挂载结果和是否绑定 PID
			log.Printf("[Phase 2] uprobe mounted: %s+0x%x VA=0x%016x pid_bound=%t link=%v\n", e.filePath, e.fileOffset, e.va, mountedWithPID, l)
		} else {
			log.Printf("[Phase 2] uprobe 已挂载: %s + 0x%x", e.filePath, e.fileOffset)
		}
	}

	if len(uprobeLinks) > 0 {
		log.Printf("[Phase 2] 共挂载 %d 个 uprobe，等待寄存器快照...", len(uprobeLinks))
	}
}

// startUprobeReader 启动后台 goroutine，从 uprobe ring buffer 读取寄存器快照。
// 每个 uprobe 只捕获第一次触发（one-shot）：收到快照后立即关闭对应 link（卸载 uprobe）。
// 所有 N 个地址均已捕获一次后，执行栈展开、输出结果并调用 stop 退出程序。
func startUprobeReader(ctx context.Context, stop context.CancelFunc) {
	if uprobeObjs == nil {
		return
	}
	rb, err := ringbuf.NewReader(uprobeObjs.UprobeRegsRb)
	if err != nil {
		log.Printf("[Phase 2] 创建 uprobe ring buffer reader 失败: %v", err)
		return
	}

	// 统计还未触发的 uprobe 数量
	total := 0
	uprobeByAddr.Range(func(k, v interface{}) bool { total++; return true })
	if total == 0 {
		rb.Close()
		return
	}

	go func() {
		defer rb.Close()
		regsSize := int(unsafe.Sizeof(uprobeRegs{}))
		remain := total
		var results []phase2CaptureResult
		for remain > 0 {
			select {
			case <-ctx.Done():
				return
			default:
			}
			record, err := rb.Read()
			if err != nil {
				if errors.Is(err, ringbuf.ErrClosed) {
					return
				}
				log.Printf("[Phase 2] ring buffer read error: %v", err)
				continue
			}
			if len(record.RawSample) < regsSize {
				continue
			}
			var regs uprobeRegs
			if err := binary.Read(bytes.NewReader(record.RawSample), binary.LittleEndian, &regs); err != nil {
				continue
			}
			pid := uint32(regs.PidTgid >> 32)
			tid := uint32(regs.PidTgid)
			msg := fmt.Sprintf(
				"[Phase 2 uprobe] PID=%-6d TID=%-6d probe_addr=0x%016x  RIP=0x%016x  RSP=0x%016x  RBP=0x%016x\n",
				pid, tid, regs.ProbeAddr, regs.Rip, regs.Rsp, regs.Rbp,
			)
			fmt.Print(msg)
			if logFile != nil {
				logFile.WriteString(msg)
			}
			// one-shot：先尝试直接 VA 匹配，失败则通过文件偏移反查原始 link
			var closedLink bool
			if val, loaded := uprobeByAddr.LoadAndDelete(regs.ProbeAddr); loaded {
				val.(link.Link).Close()
				remain--
				closedLink = true
				log.Printf("[Phase 2] uprobe 已触发并卸载 (VA=0x%x)，剩余 %d 个", regs.ProbeAddr, remain)
			} else {
				// VA 不匹配（ASLR 导致跨进程地址不同）：通过文件偏移反查
				if tempRes, terr := lbr.NewUserSymbolResolver(int(pid)); terr == nil {
					if fp, fo, ferr := tempRes.GetFileOffset(regs.ProbeAddr); ferr == nil {
						fk := fmt.Sprintf("%s:0x%x", fp, fo)
						if origVA, ok := uprobeFileKeyToVA.LoadAndDelete(fk); ok {
							if val, loaded := uprobeByAddr.LoadAndDelete(origVA.(uint64)); loaded {
								val.(link.Link).Close()
								remain--
								closedLink = true
								log.Printf("[Phase 2] uprobe 已触发并卸载(via file offset, origVA=0x%x)，剩余 %d 个", origVA.(uint64), remain)
							}
						} else {
							// 该偏移已被之前的事件处理过，跳过重复触发
							log.Printf("[Phase 2] 忽略重复触发 (probe_addr=0x%x, 文件偏移已卸载)", regs.ProbeAddr)
						}
					} else {
						log.Printf("[Phase 2] 无法获取文件偏移 (probe_addr=0x%x, pid=%d): %v", regs.ProbeAddr, pid, ferr)
					}
				} else {
					log.Printf("[Phase 2] 无法创建临时 UserResolver (pid=%d): %v", pid, terr)
				}
			}
			if !closedLink {
				// 未能关闭对应 uprobe（重复触发或无匹配），跳过此次事件
				continue
			}

			// 栈展开：优先为触发进程创建专用 SFrame 解析器，避免 ASLR 映射错误
			var unwindResolver *lbr.SFrameResolver
			if phase1SFrameResolver != nil && (*targetPID == 0 || pid == uint32(*targetPID)) {
				unwindResolver = phase1SFrameResolver
			} else {
				if sr, srerr := lbr.NewSFrameResolver(int(pid)); srerr == nil {
					unwindResolver = sr
					defer sr.Close()
				} else if phase1SFrameResolver != nil {
					unwindResolver = phase1SFrameResolver
					log.Printf("[Phase 2] 无法为 PID %d 创建 SFrame 解析器，回退到 Phase1 解析器: %v", pid, srerr)
				}
			}

			var frames []lbr.StackFrame
			if unwindResolver != nil {
				unwindCtx := &lbr.UnwindContext{PC: regs.Rip, SP: regs.Rsp, BP: regs.Rbp}
				// 将 BPF 快照附加到展开上下文：快照在 uprobe 触发瞬间同步采集，
				// 内容保证有效，优先于 /proc/pid/mem 异步读取（后者可能已过期）。
				if regs.StackLen > 0 {
					snapshot := make([]byte, regs.StackLen)
					copy(snapshot, regs.StackData[:regs.StackLen])
					unwindCtx.StackBase = regs.Rsp
					unwindCtx.StackSnapshot = snapshot
				}
				if fs, uerr := unwindResolver.UnwindStackFromContext(unwindCtx, 32); uerr == nil {
					frames = fs
					header := fmt.Sprintf("[Phase 2 栈展开] probe_addr=0x%016x:\n", regs.ProbeAddr)
					fmt.Print(header)
					if logFile != nil {
						logFile.WriteString(header)
					}
					for i, frame := range frames {
						var frameLine string
						if frame.Info != nil && frame.Info.Function != "" {
							if frame.Info.File != "" && frame.Info.Line > 0 {
								frameLine = fmt.Sprintf("  #%-2d 0x%016x  %s  (%s:%d)\n", i, frame.PC, frame.Info.Function, frame.Info.File, frame.Info.Line)
							} else if frame.Info.Library != "" {
								frameLine = fmt.Sprintf("  #%-2d 0x%016x  %s  (%s)\n", i, frame.PC, frame.Info.Function, frame.Info.Library)
							} else {
								frameLine = fmt.Sprintf("  #%-2d 0x%016x  %s\n", i, frame.PC, frame.Info.Function)
							}
						} else {
							frameLine = fmt.Sprintf("  #%-2d 0x%016x\n", i, frame.PC)
						}
						fmt.Print(frameLine)
						if logFile != nil {
							logFile.WriteString(frameLine)
						}
					}
				} else {
					log.Printf("[Phase 2] 栈展开失败 (addr=0x%x): %v", regs.ProbeAddr, uerr)
				}
			}
			results = append(results, phase2CaptureResult{regs: regs, frames: frames})
		}
		log.Println("[Phase 2] 所有热点地址均已捕获一次寄存器快照，uprobe 全部卸载")
		writePhase2UnwindToFile(results)
		stop()
	}()
}

// writePhase1StatsToFile 将 Phase 1 采集的地址统计信息写入 CSV 文件。
// resolver 在进程存活期间已创建，此时进程可能已退出，直接复用已有实例。
func writePhase1StatsToFile(stats []addrCount, resolver *lbr.UserSymbolResolver) {
	timestamp := time.Now().Format("20060102_150405")
	fileName := fmt.Sprintf("phase1_addr_stats_%s.csv", timestamp)
	filePath := filepath.Join(*logDir, fileName)

	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Printf("无法创建 Phase 1 统计文件: %v", err)
		return
	}
	defer f.Close()

	fmt.Fprintf(f, "rank,addr,count,pid,file_path,file_offset,symbol\n")
	limit := 50
	if len(stats) < limit {
		limit = len(stats)
	}
	for i, s := range stats[:limit] {
		var (
			filePathStr   string
			fileOffsetStr string
			symbol        string
		)
		if resolver != nil {
			if fp, fo, ferr := resolver.GetFileOffset(s.addr); ferr == nil {
				filePathStr = fp
				fileOffsetStr = fmt.Sprintf("0x%x", fo)
				// 优先使用 SFrame 解析器（与 LBR 实时显示逻辑一致，输出 funcName+offset）
				if phase1SFrameResolver != nil {
					if info, sferr := phase1SFrameResolver.ResolveAddress(s.addr); sferr == nil {
						symbol = info.Function
					}
				}
				// SFrame 未解析时回退到 addr2line
				if symbol == "" {
					if fn, _, _, aerr := resolver.ResolveAddress(s.addr); aerr == nil {
						symbol = fn
					}
				}
			} else {
				log.Printf("GetFileOffset 0x%x (PID %d): %v", s.addr, s.ctx.Pid, ferr)
			}
		}
		fmt.Fprintf(f, "%d,0x%016x,%d,%d,%s,%s,%s\n",
			i+1, s.addr, s.ctx.Count, s.ctx.Pid, filePathStr, fileOffsetStr, symbol)
	}

	log.Printf("统计已写入文件: %s（共 %d 条地址记录）", filePath, len(stats))
}

// writePhase2UnwindToFile 将 Phase 2 uprobe 捕获的寄存器快照及栈展开结果写入文本文件。
// 每个探测点独立成块，完整展示调用栈层次，格式与 stdout 输出保持一致。
func writePhase2UnwindToFile(results []phase2CaptureResult) {
	if len(results) == 0 {
		log.Println("[Phase 2] 无展开结果可写入")
		return
	}
	timestamp := time.Now().Format("20060102_150405")
	fileName := fmt.Sprintf("phase2_unwind_%s.txt", timestamp)
	filePath := filepath.Join(*logDir, fileName)

	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		log.Printf("[Phase 2] 无法创建展开文件: %v", err)
		return
	}
	defer f.Close()

	for idx, r := range results {
		pid := uint32(r.regs.PidTgid >> 32)
		tid := uint32(r.regs.PidTgid)
		fmt.Fprintf(f, "\n=== [%d] probe_addr=0x%016x  PID=%-6d TID=%-6d  RIP=0x%016x  RSP=0x%016x  RBP=0x%016x ===\n",
			idx+1, r.regs.ProbeAddr, pid, tid, r.regs.Rip, r.regs.Rsp, r.regs.Rbp)
		if len(r.frames) == 0 {
			fmt.Fprintf(f, "  (栈展开失败或无帧)\n")
			continue
		}
		for i, frame := range r.frames {
			if frame.Info != nil && frame.Info.Function != "" {
				if frame.Info.File != "" && frame.Info.Line > 0 {
					fmt.Fprintf(f, "  #%-2d 0x%016x  %s  (%s:%d)\n", i, frame.PC, frame.Info.Function, frame.Info.File, frame.Info.Line)
				} else if frame.Info.Library != "" {
					fmt.Fprintf(f, "  #%-2d 0x%016x  %s  (%s)\n", i, frame.PC, frame.Info.Function, frame.Info.Library)
				} else {
					fmt.Fprintf(f, "  #%-2d 0x%016x  %s\n", i, frame.PC, frame.Info.Function)
				}
			} else {
				fmt.Fprintf(f, "  #%-2d 0x%016x\n", i, frame.PC)
			}
		}
	}
	log.Printf("[Phase 2] 栈展开结果已写入: %s（共 %d 个地址快照）", filePath, len(results))
}

// performPhase2Analysis 对命中热点地址的样本执行详细符号解析
func performPhase2Analysis(hotAddr uint64, pid uint32, resolver *lbr.SFrameResolver) {
	header := fmt.Sprintf("\n[Phase 2 热点命中] 地址: 0x%016x  PID: %d\n", hotAddr, pid)
	fmt.Print(header)
	if logFile != nil {
		logFile.WriteString(header)
	}

	if resolver == nil {
		return
	}

	if info, err := resolver.ResolveAddress(hotAddr); err == nil {
		var symInfo string
		if info.Library != "" {
			symInfo = fmt.Sprintf("  函数: %s  库: %s\n", info.Function, info.Library)
		} else if info.File != "" && info.Line > 0 {
			symInfo = fmt.Sprintf("  函数: %s  位置: %s:%d\n", info.Function, info.File, info.Line)
		} else {
			symInfo = fmt.Sprintf("  函数: %s\n", info.Function)
		}
		fmt.Print(symInfo)
		if logFile != nil {
			logFile.WriteString(symInfo)
		}
	}
}

// analyzeTopAddresses 在非两阶段模式下，于退出时打印访问最多的地址摘要
func analyzeTopAddresses(sframeResolver *lbr.SFrameResolver) {
	if len(addrStats) == 0 {
		log.Println("没有统计数据可分析")
		return
	}

	stats := make([]addrCount, 0, len(addrStats))
	for addr, ctx := range addrStats {
		stats = append(stats, addrCount{addr: addr, ctx: ctx})
	}

	// 按访问次数排序（降序）
	for i := 0; i < len(stats); i++ {
		for j := i + 1; j < len(stats); j++ {
			if stats[j].ctx.Count > stats[i].ctx.Count {
				stats[i], stats[j] = stats[j], stats[i]
			}
		}
	}

	// 取前5个
	topN := 5
	if len(stats) < topN {
		topN = len(stats)
	}

	log.Printf("\n========================================")
	log.Printf("访问频率最高的 %d 个地址:", topN)
	log.Printf("========================================\n")

	// 输出到日志文件
	if logFile != nil {
		header := fmt.Sprintf("\n========================================\n访问频率最高的 %d 个地址:\n========================================\n", topN)
		logFile.WriteString(header)
	}

	for i := 0; i < topN; i++ {
		stat := stats[i].ctx

		output := fmt.Sprintf("\n#%d 地址: 0x%016x (访问次数: %d)\n", i+1, stat.Addr, stat.Count)
		log.Print(output)
		if logFile != nil {
			logFile.WriteString(output)
		}

		// 解析符号
		if sframeResolver != nil {
			if info, err := sframeResolver.ResolveAddress(stat.Addr); err == nil {
				var symInfo string
				if info.Library != "" {
					symInfo = fmt.Sprintf("    函数: %s (%s)\n", info.Function, info.Library)
				} else if info.File != "" && info.Line > 0 {
					symInfo = fmt.Sprintf("    函数: %s at %s:%d\n", info.Function, info.File, info.Line)
				} else {
					symInfo = fmt.Sprintf("    函数: %s\n", info.Function)
				}
				log.Print(symInfo)
				if logFile != nil {
					logFile.WriteString(symInfo)
				}
			}
		}
	}

	totalStats := fmt.Sprintf("\n========================================\n总计统计了 %d 个不同的地址\n========================================\n", len(addrStats))
	log.Print(totalStats)
	if logFile != nil {
		logFile.WriteString(totalStats)
	}

	// 将全量地址统计写入 CSV 文件
	writePhase1StatsToFile(stats, phase1UserResolver)
}
