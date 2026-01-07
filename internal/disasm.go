package lbr

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Symbol struct {
	Addr uint64
	Name string
}

type Symbols struct {
	syms []Symbol
}

func LoadKallsyms() (*Symbols, error) {
	file, err := os.Open("/proc/kallsyms")
	if err != nil {
		return nil, fmt.Errorf("failed to open /proc/kallsyms: %w", err)
	}
	defer file.Close()

	s := &Symbols{
		syms: make([]Symbol, 0, 1024),
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		addr, err := strconv.ParseUint(fields[0], 16, 64)
		if err != nil {
			continue
		}

		s.syms = append(s.syms, Symbol{
			Addr: addr,
			Name: fields[2],
		})
	}

	return s, scanner.Err()
}

func (s *Symbols) Find(addr uint64) (string, uint64, bool) {
	var found Symbol
	for i := range s.syms {
		if s.syms[i].Addr <= addr {
			found = s.syms[i]
		} else {
			break
		}
	}

	if found.Name == "" {
		return "", 0, false
	}

	return found.Name, addr - found.Addr, true
}
