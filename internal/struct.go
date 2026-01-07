package lbr

type Entry struct {
	From  uint64
	To    uint64
	Flags uint64
}

type Data struct {
	Entries [32]Entry
	NrBytes int64
}
