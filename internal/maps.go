// Copyright 2025 Leon Hwang.
// SPDX-License-Identifier: Apache-2.0

package lbr

import (
	"fmt"
	"unsafe"

	"github.com/cilium/ebpf"
	"golang.org/x/sys/unix"
)

type LbrData struct {
	PidTgid uint64
	NrBytes int64
	Entries [32]struct {
		From  uint64
		To    uint64
		Flags uint64
	}
}

func PrepareBPFMaps(spec *ebpf.CollectionSpec) (map[string]*ebpf.Map, error) {
	numCPU, err := ebpf.PossibleCPU()
	if err != nil {
		return nil, fmt.Errorf("failed to get possible CPU: %w", err)
	}

	// Create .data.lbrs map with BPF_F_MMAPABLE flag
	lbrBuffMapSpec, ok := spec.Maps[".data.lbrs"]
	if !ok {
		return nil, fmt.Errorf(".data.lbrs map not found in spec")
	}
	lbrBuffMapSpec.Flags |= unix.BPF_F_MMAPABLE
	lbrBuffMapSpec.ValueSize = uint32(unsafe.Sizeof(LbrData{})) * uint32(numCPU)
	lbrBuffMapSpec.Contents[0].Value = make([]byte, lbrBuffMapSpec.ValueSize)
	lbrBuffMap, err := ebpf.NewMap(lbrBuffMapSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to create .data.lbrs map: %w", err)
	}

	// Create lbr_map
	lbrMap, err := ebpf.NewMap(spec.Maps["lbr_map"])
	if err != nil {
		lbrBuffMap.Close()
		return nil, fmt.Errorf("failed to create lbr_map: %w", err)
	}

	// Create comm_map
	commMap, err := ebpf.NewMap(spec.Maps["comm_map"])
	if err != nil {
		lbrBuffMap.Close()
		lbrMap.Close()
		return nil, fmt.Errorf("failed to create comm_map: %w", err)
	}

	return map[string]*ebpf.Map{
		".data.lbrs": lbrBuffMap,
		"lbr_map":    lbrMap,
		"comm_map":   commMap,
	}, nil
}

func CloseBPFMaps(maps map[string]*ebpf.Map) {
	for _, m := range maps {
		_ = m.Close()
	}
}
