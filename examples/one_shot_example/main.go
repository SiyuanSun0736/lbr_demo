package main

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// 模拟 BPF 上报的寄存器快照结构体（精简）
type uprobeRegs struct {
	PidTgid   uint64
	ProbeAddr uint64
	Rip       uint64
	Rsp       uint64
	Rbp       uint64
}

// MockLink 模拟 link.Link 的最小行为（Close）
type MockLink struct {
	addr   uint64
	closed bool
	mu     sync.Mutex
}

func (m *MockLink) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	m.closed = true
	log.Printf("MockLink closed for VA=0x%x", m.addr)
	return nil
}

var uprobeByAddr sync.Map      // VA -> *MockLink
var uprobeFileKeyToVA sync.Map // "file:0xoffset" -> VA

// mountUprobe 模拟挂载 uprobe：把 VA 和 file:offset 关系写入两个表
func mountUprobe(file string, offset uint64, va uint64) {
	l := &MockLink{addr: va}
	uprobeByAddr.Store(va, l)
	uprobeFileKeyToVA.Store(fmt.Sprintf("%s:0x%x", file, offset), va)
	log.Printf("mounted mock uprobe %s+0x%x -> VA=0x%x", file, offset, va)
}

// startReader 模拟 ring buffer 读取器：接收寄存器快照并做 one-shot 卸载
func startReader(ch <-chan uprobeRegs, wg *sync.WaitGroup) {
	defer wg.Done()
	for regs := range ch {
		pid := uint32(regs.PidTgid >> 32)
		log.Printf("received regs: probe=0x%x pid=%d", regs.ProbeAddr, pid)

		// 优先用 VA 直接匹配并卸载
		if v, ok := uprobeByAddr.LoadAndDelete(regs.ProbeAddr); ok {
			v.(*MockLink).Close()
			log.Printf("one-shot: closed by VA match 0x%x", regs.ProbeAddr)
			continue
		}

		// VA 未命中时，模拟通过临时 UserSymbolResolver 获取 file:offset 并反查
		// 这里直接演示用一个固定的 fileKey
		fk := "./demo:0x100"
		if va, ok := uprobeFileKeyToVA.Load(fk); ok {
			if v, ok2 := uprobeByAddr.LoadAndDelete(va.(uint64)); ok2 {
				v.(*MockLink).Close()
				log.Printf("one-shot: closed by fileKey match %s -> VA=0x%x", fk, va.(uint64))
			}
		} else {
			log.Printf("no matching uprobe for probe=0x%x (pid=%d)", regs.ProbeAddr, pid)
		}
	}
}

func main() {
	log.Println("one-shot example start")

	// 模拟挂载两个 mock uprobe
	mountUprobe("./demo", 0x100, 0x400100)
	mountUprobe("./demo", 0x200, 0x400200)

	ch := make(chan uprobeRegs, 4)
	var wg sync.WaitGroup
	wg.Add(1)
	go startReader(ch, &wg)

	// 模拟 BPF 向 ringbuf 推送一次触发（VA 命中）
	time.Sleep(100 * time.Millisecond)
	ch <- uprobeRegs{PidTgid: uint64(123)<<32 | 1, ProbeAddr: 0x400100, Rip: 0x401000, Rsp: 0x7fff0000, Rbp: 0x7fff1000}
	time.Sleep(100 * time.Millisecond)

	// 模拟另一次触发（VA 不命中），通过文件偏移反查卸载
	ch <- uprobeRegs{PidTgid: uint64(124)<<32 | 2, ProbeAddr: 0xdeadbeef}

	close(ch)
	wg.Wait()
	log.Println("one-shot example finished")
}
