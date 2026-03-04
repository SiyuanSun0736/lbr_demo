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

var (
	targetPID      = flag.Int("pid", 0, "Target PID to monitor (0 = all processes)")
	useAddr2line   = flag.Bool("addr2line", true, "Use addr2line for user symbol resolution")
	useDwarf       = flag.Bool("dwarf", true, "Use DWARF for user symbol resolution (requires debug symbols)")
	useSFrame      = flag.Bool("sframe", true, "Use SFrame for user symbol resolution (lightweight stack unwinding)")
	unwindRealtime = flag.Bool("unwind-realtime", false, "Perform stack unwinding in real-time for each sample (high overhead)")
	unwindOnExit   = flag.Bool("unwind-on-exit", true, "Perform stack unwinding on top 5 addresses when exiting")
	resolveSymbols = flag.Bool("resolve", true, "Resolve user space addresses to symbols")
	logDir         = flag.String("logdir", "log", "Directory to save log files")
	debugMode      = flag.Bool("debug", false, "Enable debug logging")
)

var logFile *os.File

// AddrContext 存储地址及其采样时的寄存器上下文
type AddrContext struct {
	Addr  uint64
	Rip   uint64 // 采样时的指令指针
	Rsp   uint64 // 采样时的栈指针
	Rbp   uint64 // 采样时的帧指针
	Count uint64
}

// 全局地址访问统计
var addrStats = make(map[uint64]*AddrContext)

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
	ctx, stop := signal.NotifyContext(context.Background(),
		syscall.SIGINT, syscall.SIGTERM)
	defer stop()

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

	// 如果需要在退出时分析，提前创建 SFrame 解析器
	var sframeResolver *lbr.SFrameResolver
	if *unwindOnExit && *useSFrame && *targetPID != 0 {
		if sr, err := lbr.NewSFrameResolver(*targetPID); err == nil {
			sframeResolver = sr
			defer sframeResolver.Close()
			log.Println("SFrame 解析器已创建，用于退出时分析")
		} else {
			log.Printf("Warning: 无法创建SFrame解析器: %v", err)
		}
	}

	log.Println("LBR demo is running. Press Ctrl-C to exit.")
	log.Printf("Attached to %d CPUs, checking for LBR data every second...", numCPU)

	// Process events
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// 在退出前进行统计分析
			if *unwindOnExit {
				log.Println("\n收到退出信号，正在分析统计数据...")
				analyzeTopAddresses(sframeResolver)
			}
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

	// 用户态符号解析器（缓存）
	var userResolver *lbr.UserSymbolResolver
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
			// 优先级: SFrame > DWARF > addr2line
			if *useSFrame && sframeResolver == nil {
				if sr, err := lbr.NewSFrameResolver(int(pid)); err == nil {
					sframeResolver = sr
					defer sframeResolver.Close()
					if *unwindRealtime {
						log.Printf("已启用 SFrame 符号解析和实时栈展开 for PID %d", pid)
					} else {
						log.Printf("已启用 SFrame 符号解析 for PID %d", pid)
					}
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
			if *useAddr2line && userResolver == nil && dwarfResolver == nil && sframeResolver == nil {
				if ur, err := lbr.NewUserSymbolResolver(int(pid)); err == nil {
					userResolver = ur
					log.Printf("已启用 addr2line 符号解析 for PID %d", pid)
				} else {
					log.Printf("addr2line 解析器创建失败: %v", err)
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
				} else if userResolver != nil {
					if fn, file, line, err := userResolver.ResolveAddress(entry.From); err == nil {
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
				} else if userResolver != nil {
					if fn, file, line, err := userResolver.ResolveAddress(entry.To); err == nil {
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
					// 更新为最新的寄存器上下文（如果有的话）
					if data.Rip != 0 {
						stat.Rip = data.Rip
						stat.Rsp = data.Rsp
						stat.Rbp = data.Rbp
					}
				} else {
					addrStats[entry.From] = &AddrContext{
						Addr:  entry.From,
						Rip:   data.Rip,
						Rsp:   data.Rsp,
						Rbp:   data.Rbp,
						Count: 1,
					}
				}
			}

			// 统计To地址
			if entry.To != 0 {
				if stat, exists := addrStats[entry.To]; exists {
					stat.Count++
					if data.Rip != 0 {
						stat.Rip = data.Rip
						stat.Rsp = data.Rsp
						stat.Rbp = data.Rbp
					}
				} else {
					addrStats[entry.To] = &AddrContext{
						Addr:  entry.To,
						Rip:   data.Rip,
						Rsp:   data.Rsp,
						Rbp:   data.Rbp,
						Count: 1,
					}
				}
			}
		}

		// 如果启用了实时栈展开，执行栈回溯
		if *unwindRealtime && sframeResolver != nil {
			unwindHeader := "\n[Stack Unwinding - Real-time Call Stack]\n"
			fmt.Print(unwindHeader)
			if logFile != nil {
				logFile.WriteString(unwindHeader)
			}

			// 使用eBPF采样时记录的寄存器状态进行栈展开
			// 这比使用当前寄存器状态更准确，因为它对应于LBR数据采集时的瞬间
			var frames []lbr.StackFrame
			var err error

			if data.Rip != 0 && data.Rsp != 0 {
				// 使用eBPF记录的寄存器状态
				ctx := lbr.NewUnwindContextFromRegs(data.Rip, data.Rsp, data.Rbp)
				frames, err = sframeResolver.UnwindStackFromContext(ctx, 32)

				regInfo := fmt.Sprintf("使用采样时寄存器状态: RIP=0x%x, RSP=0x%x, RBP=0x%x\n",
					data.Rip, data.Rsp, data.Rbp)
				fmt.Print(regInfo)
				if logFile != nil {
					logFile.WriteString(regInfo)
				}
			} else {
				// 回退到使用当前进程寄存器（旧的行为）
				frames, err = sframeResolver.UnwindStack(32)

				warnMsg := "警告: eBPF未记录寄存器状态，使用当前进程寄存器（可能不准确）\n"
				fmt.Print(warnMsg)
				if logFile != nil {
					logFile.WriteString(warnMsg)
				}
			}

			if err != nil {
				errMsg := fmt.Sprintf("栈展开失败: %v\n", err)
				fmt.Print(errMsg)
				if logFile != nil {
					logFile.WriteString(errMsg)
				}
			} else {
				// 输出栈帧信息
				for i, frame := range frames {
					var frameStr string
					if frame.Info != nil {
						if frame.Info.Library != "" {
							frameStr = fmt.Sprintf("#%02d  0x%016x in \033[36m%s\033[0m (%s)\n",
								i, frame.PC, frame.Info.Function, frame.Info.Library)
						} else if frame.Info.File != "" && frame.Info.Line > 0 {
							frameStr = fmt.Sprintf("#%02d  0x%016x in \033[36m%s\033[0m at %s:%d\n",
								i, frame.PC, frame.Info.Function, frame.Info.File, frame.Info.Line)
						} else {
							frameStr = fmt.Sprintf("#%02d  0x%016x in \033[36m%s\033[0m\n",
								i, frame.PC, frame.Info.Function)
						}
					} else {
						frameStr = fmt.Sprintf("#%02d  0x%016x\n", i, frame.PC)
					}
					fmt.Print(frameStr)
					if logFile != nil {
						logFile.WriteString(frameStr)
					}
				}
				summary := fmt.Sprintf("\n展开了 %d 个栈帧\n", len(frames))
				fmt.Print(summary)
				if logFile != nil {
					logFile.WriteString(summary)
				}
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

// analyzeTopAddresses 分析访问最多的地址并进行栈展开
func analyzeTopAddresses(sframeResolver *lbr.SFrameResolver) {
	if len(addrStats) == 0 {
		log.Println("没有统计数据可分析")
		return
	}

	// 将map转换为切片以便排序
	type addrCount struct {
		addr uint64
		ctx  *AddrContext
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

			// 使用LBR采样时保存的寄存器状态进行栈展开（如果有的话）
			if stat.Rip != 0 && stat.Rsp != 0 {
				unwindHeader := "    [栈展开 - 使用采样时寄存器状态]:\n"
				log.Print(unwindHeader)
				if logFile != nil {
					logFile.WriteString(unwindHeader)
				}

				regInfo := fmt.Sprintf("    采样时寄存器: RIP=0x%x, RSP=0x%x, RBP=0x%x\n", stat.Rip, stat.Rsp, stat.Rbp)
				log.Print(regInfo)
				if logFile != nil {
					logFile.WriteString(regInfo)
				}

				ctx := lbr.NewUnwindContextFromRegs(stat.Rip, stat.Rsp, stat.Rbp)
				frames, err := sframeResolver.UnwindStackFromContext(ctx, 16)
				if err != nil {
					errMsg := fmt.Sprintf("    栈展开失败: %v\n", err)
					log.Print(errMsg)
					if logFile != nil {
						logFile.WriteString(errMsg)
					}
				} else {
					for j, frame := range frames {
						var frameStr string
						if frame.Info != nil {
							if frame.Info.Library != "" {
								frameStr = fmt.Sprintf("      #%02d  0x%016x in %s (%s)\n",
									j, frame.PC, frame.Info.Function, frame.Info.Library)
							} else if frame.Info.File != "" && frame.Info.Line > 0 {
								frameStr = fmt.Sprintf("      #%02d  0x%016x in %s at %s:%d\n",
									j, frame.PC, frame.Info.Function, frame.Info.File, frame.Info.Line)
							} else {
								frameStr = fmt.Sprintf("      #%02d  0x%016x in %s\n",
									j, frame.PC, frame.Info.Function)
							}
						} else {
							frameStr = fmt.Sprintf("      #%02d  0x%016x\n", j, frame.PC)
						}
						log.Print(frameStr)
						if logFile != nil {
							logFile.WriteString(frameStr)
						}
					}
					summary := fmt.Sprintf("    展开了 %d 个栈帧\n", len(frames))
					log.Print(summary)
					if logFile != nil {
						logFile.WriteString(summary)
					}
				}
			} else {
				noRegs := "    无采样时寄存器上下文，无法进行栈展开\n"
				log.Print(noRegs)
				if logFile != nil {
					logFile.WriteString(noRegs)
				}
			}
		}
	}

	totalStats := fmt.Sprintf("\n========================================\n总计统计了 %d 个不同的地址\n========================================\n", len(addrStats))
	log.Print(totalStats)
	if logFile != nil {
		logFile.WriteString(totalStats)
	}
}
