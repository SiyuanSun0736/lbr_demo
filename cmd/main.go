package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	lbr "github.com/SiyuanSun0736/lbr_demo/internal"
	"github.com/cilium/ebpf"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target amd64 -type lbr_data lbr ../bpf/bpf_lbr.c -- -I../bpf -mllvm -bpf-stack-size=2048

var (
	targetPID = flag.Int("pid", 0, "Target PID to monitor (0 = all processes)")
)

func main() {
	flag.Parse()

	if err := run(); err != nil {
		log.Fatal(err)
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
			processLbrData(lbrs, commMap, syms)
		}
	}
}

func processLbrData(lbrMap *ebpf.Map, commMap *ebpf.Map, syms *lbr.Symbols) {
	var (
		key  uint64
		data lbrLbrData
	)

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

			// 如果找不到内核符号，标记为用户态地址
			if fromName == "" && entry.From != 0 {
				fromName = fmt.Sprintf("[user]")
				fromOffset = entry.From
			}
			if toName == "" && entry.To != 0 {
				toName = fmt.Sprintf("[user]")
				toOffset = entry.To
			}

			stack.AddEntry(lbr.BranchEntry{
				From: &lbr.BranchEndpoint{
					Addr:     entry.From,
					FuncName: fromName,
					Offset:   fromOffset,
				},
				To: &lbr.BranchEndpoint{
					Addr:     entry.To,
					FuncName: toName,
					Offset:   toOffset,
				},
			})
		}

		fmt.Printf("\n=== PID: %d, TID: %d, COMM: %s, Entries: %d ===\n", pid, tid, commName, numEntries)
		stack.Output(os.Stdout)

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
