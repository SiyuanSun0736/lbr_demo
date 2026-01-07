package lbr

import (
	"fmt"
	"io"

	"github.com/fatih/color"
)

type BranchEndpoint struct {
	Addr     uint64
	FuncName string
	Offset   uint64
}

func (b *BranchEndpoint) String() string {
	if b.FuncName != "" {
		return fmt.Sprintf("%s+%#x", b.FuncName, b.Offset)
	}
	return fmt.Sprintf("%#x", b.Addr)
}

func (b *BranchEndpoint) Format(w io.Writer, nameLen int) {
	name := b.String()
	color.New(color.FgYellow).Fprint(w, name)
	if nameLen > len(name) {
		fmt.Fprintf(w, "%-*s", nameLen-len(name), "")
	}
}

type BranchEntry struct {
	From *BranchEndpoint
	To   *BranchEndpoint
}

func (e *BranchEntry) Format(w io.Writer, lNameLen, rNameLen int) {
	e.From.Format(w, lNameLen)
	fmt.Fprint(w, " -> ")
	e.To.Format(w, rNameLen)
}

type Stack struct {
	entries        []BranchEntry
	maxFromNameLen int
	maxToNameLen   int
}

func NewStack() *Stack {
	return &Stack{
		entries: make([]BranchEntry, 0, 32),
	}
}

func (s *Stack) AddEntry(entry BranchEntry) {
	s.entries = append(s.entries, entry)
	fromLen := len(entry.From.String())
	toLen := len(entry.To.String())
	if fromLen > s.maxFromNameLen {
		s.maxFromNameLen = fromLen
	}
	if toLen > s.maxToNameLen {
		s.maxToNameLen = toLen
	}
}

func (s *Stack) Output(w io.Writer) {
	fmt.Fprintln(w, "LBR Stack:")
	for i := len(s.entries) - 1; i >= 0; i-- {
		fmt.Fprintf(w, "[#%02d] ", 31-i)
		s.entries[i].Format(w, s.maxFromNameLen, s.maxToNameLen)
		fmt.Fprintln(w)
	}
}

func (s *Stack) Reset() {
	s.entries = s.entries[:0]
	s.maxFromNameLen = 0
	s.maxToNameLen = 0
}
