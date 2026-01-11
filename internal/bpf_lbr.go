//go:build linux

package lbr

import (
	"fmt"
	"unsafe"

	"github.com/cilium/ebpf"
	"github.com/cilium/ebpf/link"
	"golang.org/x/sys/unix"
)

type PerfEvent struct {
	fds []int
}

func openLbrPerfEvent(cpu int) (int, error) {
	attr := &unix.PerfEventAttr{
		Type:        unix.PERF_TYPE_HARDWARE,
		Size:        uint32(unsafe.Sizeof(unix.PerfEventAttr{})),
		Config:      unix.PERF_COUNT_HW_CPU_CYCLES,
		Sample:      4000,
		Bits:        unix.PerfBitFreq,
		Sample_type: unix.PERF_SAMPLE_BRANCH_STACK,
	}

	// Enable LBR - capture user-space branches
	attr.Branch_sample_type = unix.PERF_SAMPLE_BRANCH_USER |
		unix.PERF_SAMPLE_BRANCH_ANY

	fd, err := unix.PerfEventOpen(attr, -1, cpu, -1, unix.PERF_FLAG_FD_CLOEXEC)
	if err != nil {
		return -1, fmt.Errorf("perf_event_open failed: %w", err)
	}

	// Enable the perf event
	if err := unix.IoctlSetInt(fd, unix.PERF_EVENT_IOC_ENABLE, 0); err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("failed to enable perf event: %w", err)
	}

	return fd, nil
}

func OpenLbrPerfEvent(numCPU int) (*PerfEvent, error) {
	p := &PerfEvent{
		fds: make([]int, 0, numCPU),
	}

	for i := 0; i < numCPU; i++ {
		fd, err := openLbrPerfEvent(i)
		if err != nil {
			p.Close()
			return nil, fmt.Errorf("failed to open LBR perf event on CPU %d: %w", i, err)
		}
		p.fds = append(p.fds, fd)
	}

	return p, nil
}

func (p *PerfEvent) Close() {
	for _, fd := range p.fds {
		_ = unix.Close(fd)
	}
}

func (p *PerfEvent) FDs() []int {
	return p.fds
}

func AttachPerfEvent(prog *ebpf.Program, numCPU int, targetPID int) ([]link.Link, error) {
	var links []link.Link

	for cpu := 0; cpu < numCPU; cpu++ {
		// 创建 perf_event：采样 CPU 周期，并启用 LBR
		attr := &unix.PerfEventAttr{
			Type:        unix.PERF_TYPE_HARDWARE,
			Size:        uint32(unsafe.Sizeof(unix.PerfEventAttr{})),
			Config:      unix.PERF_COUNT_HW_CPU_CYCLES,
			Sample:      4000, // 采样频率：4000 Hz
			Bits:        unix.PerfBitFreq,
			Sample_type: unix.PERF_SAMPLE_BRANCH_STACK, // 启用 LBR 采样
		}

		// 配置 LBR：捕获所有分支（用户态+内核态）
		attr.Branch_sample_type = unix.PERF_SAMPLE_BRANCH_USER |
			//	unix.PERF_SAMPLE_BRANCH_KERNEL |
			unix.PERF_SAMPLE_BRANCH_ANY

		// 打开 perf event
		pid := -1 // 监控所有进程
		if targetPID > 0 {
			pid = targetPID // 只监控指定 PID
		}

		fd, err := unix.PerfEventOpen(attr, pid, cpu, -1, unix.PERF_FLAG_FD_CLOEXEC)
		if err != nil {
			CloseLinks(links)
			return nil, fmt.Errorf("failed to open perf event on CPU %d: %w", cpu, err)
		}

		// 附加 BPF 程序到 perf event
		l, err := link.AttachRawLink(link.RawLinkOptions{
			Target:  fd,
			Program: prog,
			Attach:  ebpf.AttachPerfEvent,
		})
		if err != nil {
			unix.Close(fd)
			CloseLinks(links)
			return nil, fmt.Errorf("failed to attach BPF program on CPU %d: %w", cpu, err)
		}

		links = append(links, l)

		// 启用 perf event
		if err := unix.IoctlSetInt(fd, unix.PERF_EVENT_IOC_ENABLE, 0); err != nil {
			CloseLinks(links)
			return nil, fmt.Errorf("failed to enable perf event on CPU %d: %w", cpu, err)
		}
	}

	return links, nil
}

func CloseLinks(links []link.Link) {
	for _, l := range links {
		_ = l.Close()
	}
}
