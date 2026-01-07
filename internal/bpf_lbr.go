//go:build linux

package lbr

import (
	"fmt"
	"unsafe"

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

	// Enable LBR - use KERNEL instead of USER for kprobe context
	attr.Branch_sample_type = unix.PERF_SAMPLE_BRANCH_KERNEL |
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
