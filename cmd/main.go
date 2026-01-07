package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	lbr "github.com/SiyuanSun0736/lbr_demo/internal"
	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
)

//go:generate go run github.com/cilium/ebpf/cmd/bpf2go -target amd64 -type lbr_data lbr ../bpf/bpf_lbr.c -- -I../bpf -mllvm -bpf-stack-size=2048

func main() {

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

	// Load BPF program
	spec, err := loadLbr()
	if err != nil {
		return fmt.Errorf("failed to load BPF spec: %w", err)
	}

	// 设置 CPU_MASK
	numCPU, err := ebpf.PossibleCPU()
	if err != nil {
		return fmt.Errorf("failed to get CPU count: %w", err)
	}

	// 计算 CPU mask：对于 16 个 CPU，mask 为 0xF
	cpuMask := uint32(1<<numCPU - 1)
	if err := spec.RewriteConstants(map[string]interface{}{
		"CPU_MASK": cpuMask,
	}); err != nil {
		return fmt.Errorf("failed to rewrite CPU_MASK: %w", err)
	}

	// Open LBR perf events to enable LBR
	perfEvent, err := lbr.OpenLbrPerfEvent(numCPU)
	if err != nil {
		return fmt.Errorf("failed to open LBR perf event: %w", err)
	}
	defer perfEvent.Close()

	// Prepare BPF maps
	maps, err := lbr.PrepareBPFMaps(spec)
	if err != nil {
		return fmt.Errorf("failed to prepare BPF maps: %w", err)
	}
	defer lbr.CloseBPFMaps(maps)

	lbrs := maps["lbr_map"]
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

	// Attach kprobe
	kp, err := link.Kprobe("__x64_sys_execve", objs.TraceX64SysExecve, nil)
	if err != nil {
		return fmt.Errorf("failed to attach kprobe: %w", err)
	}
	defer kp.Close()

	// Load symbols
	syms, err := lbr.LoadKallsyms()
	if err != nil {
		log.Printf("Warning: failed to load kallsyms: %v", err)
	}

	log.Println("LBR demo is running. Press Ctrl-C to exit.")

	// Process events
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			processLbrData(lbrs, syms)
		}
	}
}

func processLbrData(lbrMap *ebpf.Map, syms *lbr.Symbols) {
	var (
		key  uint64
		data lbrLbrData
	)

	iter := lbrMap.Iterate()
	foundAny := false
	for iter.Next(&key, &data) {
		foundAny = true
		log.Printf("DEBUG: Found entry for PID/TID: %d, NrBytes: %d", key, data.NrBytes)

		if data.NrBytes <= 0 {
			log.Printf("DEBUG: Skipping entry with NrBytes <= 0")
			continue
		}

		stack := lbr.NewStack()
		numEntries := int(data.NrBytes) / (8 * 3) // 3 * sizeof(u64)
		log.Printf("DEBUG: Processing %d LBR entries", numEntries)

		for i := 0; i < numEntries && i < 32; i++ {
			entry := &data.Entries[i]

			fromName, fromOffset, _ := syms.Find(entry.From)
			toName, toOffset, _ := syms.Find(entry.To)

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

		fmt.Printf("\n=== PID/TID: %d ===\n", key)
		stack.Output(os.Stdout)

		// Delete processed entry
		_ = lbrMap.Delete(key)
	}

	if !foundAny {
		log.Printf("DEBUG: No entries found in lbr_map")
	}
}
