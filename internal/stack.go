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
	File     string
	Line     int
}

func (b *BranchEndpoint) String() string {
	if b.FuncName != "" {
		if b.File != "" && b.Line != 0 {
			// 简化文件路径显示（只显示文件名）
			fileName := b.File
			if idx := len(fileName) - 1; idx >= 0 {
				for i := idx; i >= 0; i-- {
					if fileName[i] == '/' {
						fileName = fileName[i+1:]
						break
					}
				}
			}
			return fmt.Sprintf("%s (%s:%d)", b.FuncName, fileName, b.Line)
		}
		if b.Offset != 0 {
			return fmt.Sprintf("%s+%#x", b.FuncName, b.Offset)
		}
		return b.FuncName
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
