# One-Shot 机制介绍

## 概览
One-shot 机制用于在 Phase 2 中对 Phase 1 识别出的热点地址进行“单次触发采样”（one-shot）。对每个热点地址挂载一个 uprobe，首次触发时捕获寄存器快照与栈快照，随后立即卸载该 uprobe，从而对每个热点只采集一次高质量上下文信息，便于精确栈展开与离线分析。

## 设计目标
- 最小化对被测进程的持续侵入：每个探测点只触发一次并马上卸载。
- 保证快照一致性：在 uprobe 回调内同步采集寄存器和部分栈数据，避免 /proc/pid/mem 异步读导致的不一致。
- 支持跨进程 ASLR：通过 file_offset 与 VA 的映射，能够在不同 PID 或全局挂载之间做反查与回退。
- 自动化流程：Phase 1 自动识别热点，Phase 2 自动挂载、采样、卸载并写出结果后退出。

## 工作流程
1. Phase 1：运行 LBR 采样，统计地址访问频次，生成 `addrStats`。
2. 识别 Top-N 热点地址并通过 `UserSymbolResolver.GetFileOffset` 获得对应二进制文件路径与 file_offset。
3. 对每个唯一的 file:offset 调用 `link.OpenExecutable(...).Uprobe("", handler, &link.UprobeOptions{Address: fileOffset, PID: <opt>})` 挂载 uprobe。
   - 优先尝试绑定目标 PID（若目标进程仍存在）。
   - 若绑定失败，则回退到全局挂载（不绑定 PID）。
4. 启动 ring buffer 读取器，等待来自 uprobe 的寄存器快照消息。
5. 当读取到快照后：
   - 解析出 probe VA、RIP/RSP/RBP、以及栈快照字节。
   - 尝试使用 VA 直接匹配挂载表关闭对应 link（one-shot 卸载）。
   - 若 VA 无法直接匹配，则使用临时 `UserSymbolResolver` 通过该触发 PID 的 file_offset 反查并匹配原始挂载，再卸载。
6. 为触发的 PID 创建或复用 `SFrameResolver`（优先），利用同步快照执行栈展开并记录 Frame 信息。
7. 所有 Top-N 地址各触发一次并卸载后，汇总结果写入 Phase 2 输出文件并调用停止函数结束程序。

## 实现要点
- 挂载记录：
  - 维护 `uprobeByAddr`（VA -> link）用于快速 one-shot 卸载。
  - 维护 `uprobeFileKeyToVA`（filePath:0xOffset -> VA）用于跨进程 ASLR 反查。
- one-shot 卸载逻辑：
  - 首选 `LoadAndDelete(regs.ProbeAddr)` 直接关闭 link；
  - 若失败，创建临时 `UserSymbolResolver(pid)` 调用 `GetFileOffset(probeAddr)` 得到 file+offset，查找 `uprobeFileKeyToVA`，再找到 VA 对应的 link 并关闭。
- 快照内容：
  - `PidTgid`, `ProbeAddr`, `RIP`, `RSP`, `RBP`, `StackLen`, `StackData`（limited bytes）。
  - 使用 `binary.Read` 从 ringbuf 读取到结构体并解析。
- 栈展开优先级：
  - 优先使用基于进程的 `SFrameResolver`（若存在或能创建），回退到 Phase1 的解析器或 DWARF/addr2line。

## 示例（可运行的演示程序）
仓库中提供了一个简化且可运行的示例，位于 `examples/one_shot_example/main.go`。该示例通过模拟挂载与 ringbuf 消息，展示 one-shot 卸载与反查逻辑，便于本地理解与调试（无需实际加载 BPF 程序即可运行）。

下面是示例的精简版（实际示例请查看 `examples/one_shot_example/main.go`）：

```go
package main

import (
    "fmt"
    "log"
    "sync"
    "time"
)

// 模拟 BPF 上报的寄存器快照结构体（精简）
type uprobeRegs struct {
    PidTgid  uint64
    ProbeAddr uint64
    Rip      uint64
    Rsp      uint64
    Rbp      uint64
}

// MockLink 模拟 link.Link
type MockLink struct{ addr uint64; closed bool; mu sync.Mutex }
func (m *MockLink) Close() error { m.mu.Lock(); defer m.mu.Unlock(); if m.closed { return nil }; m.closed=true; log.Printf("MockLink closed for VA=0x%x", m.addr); return nil }

var uprobeByAddr sync.Map // VA -> *MockLink
var uprobeFileKeyToVA sync.Map // "file:0xoffset" -> VA

func mountUprobe(file string, offset uint64, va uint64) {
    l := &MockLink{addr: va}
    uprobeByAddr.Store(va, l)
    uprobeFileKeyToVA.Store(fmt.Sprintf("%s:0x%x", file, offset), va)
    log.Printf("mounted mock uprobe %s+0x%x -> VA=0x%x", file, offset, va)
}

func startReader(ch <-chan uprobeRegs, wg *sync.WaitGroup) {
    defer wg.Done()
    for regs := range ch {
        log.Printf("received regs: probe=0x%x pid=%d", regs.ProbeAddr, regs.PidTgid>>32)
        if v, ok := uprobeByAddr.LoadAndDelete(regs.ProbeAddr); ok {
            v.(*MockLink).Close()
            log.Printf("one-shot: closed by VA match 0x%x", regs.ProbeAddr)
            continue
        }
        // 反查演示：假设我们通过 pid 创建临时 resolver 获得 file:offset
        // 这里用固定值演示
        fk := "./demo:0x100"
        if va, ok := uprobeFileKeyToVA.Load(fk); ok {
            if v, ok2 := uprobeByAddr.LoadAndDelete(va.(uint64)); ok2 {
                v.(*MockLink).Close()
                log.Printf("one-shot: closed by fileKey match %s -> VA=0x%x", fk, va.(uint64))
            }
        }
    }
}

func main() {
    // 挂载两个 mock uprobe
    mountUprobe("./demo", 0x100, 0x400100)
    mountUprobe("./demo", 0x200, 0x400200)

    ch := make(chan uprobeRegs, 4)
    var wg sync.WaitGroup
    wg.Add(1)
    go startReader(ch, &wg)

    // 模拟 BPF 向 ringbuf 推送一次触发
    time.Sleep(200 * time.Millisecond)
    ch <- uprobeRegs{PidTgid: uint64(123)<<32 | 1, ProbeAddr: 0x400100, Rip: 0x401000, Rsp: 0x7fff0000, Rbp: 0x7fff1000}
    time.Sleep(200 * time.Millisecond)

    // 模拟另一次触发，通过文件偏移反查
    ch <- uprobeRegs{PidTgid: uint64(124)<<32 | 2, ProbeAddr: 0xdeadbeef}

    close(ch)
    wg.Wait()
    fmt.Println("example finished")
}
```

## 输出与持久化
- Phase1 写入：`phase1_addr_stats_*.csv`（rank, addr, count, pid, file_path, file_offset, symbol）
- Phase2 写入：`phase2_unwind_*.txt`（每个探测点的寄存器快照与展开后的调用栈）

## 注意事项与限制
- 目标进程已退出或短生命周期可能导致 PID 绑定的 uprobe 无法触发，需回退到全局挂载。
- 部分地址可能对应内联/优化导致的难以精确展开，SFrame 能提高命中率但依赖可用的解析信息。
- ring buffer 的 payload 大小受限（栈快照字节数需在 BPF 侧配置合适上限）。
- 对于高并发或大量热点地址，挂载/卸载与等待触发会延长总体运行时间，慎用过大的 Top-N。

---

示例程序已写入：`examples/one_shot_example/main.go`，可直接运行验证 one-shot 行为。
