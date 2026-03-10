我要先把实现步骤写入任务清单并开始第一个步骤（修改 BPF 程序以读取目标地址）。

Created 4 todos

下面是最直接且常用的做法与示例代码（简洁步骤 + 代码片段），可把你已有的 Phase1 结果（file_offset）用于“到达该偏移时中断并采集寄存器”。

思路概要
- 在 BPF 程序中增加一个可写的 map（例如 pid -> target_vaddr），uprobe 每次运行时查这个 map，只有 IP 与目标虚拟地址匹配时才写 ringbuf（即“中断采样”）。
- 在 userspace 把 file_offset 转为目标进程的虚拟地址（vaddr = mapping_base + file_offset），然后把 vaddr 写入 BPF map（按 pid 或全局 key）。
- 可选：直接在 userspace 用 libbpf/ebpf 的 uprobe attach API 把程序附到二进制的 file:offset，这样内核只在该偏移触发，不需在 BPF 里比较。

BPF 程序（修改 / 添加到 `bpf_uprobe.c`）
- 新增 map（pid -> target_vaddr）：
```c
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 1024);
    __type(key, __u32);
    __type(value, __u64);
} target_vaddr_map SEC(".maps");
```

- 在 `capture_uprobe` 中查表并比较 IP（只在命中时保存 regs）：
```c
SEC("uprobe")
int capture_uprobe(struct pt_regs *ctx)
{
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u32 pid = (__u32)(pid_tgid >> 32);
    __u64 ip = PT_REGS_IP(ctx);

    __u64 *target = bpf_map_lookup_elem(&target_vaddr_map, &pid);
    if (!target) {
        // 也可以支持全局 key=0
        __u32 zero = 0;
        target = bpf_map_lookup_elem(&target_vaddr_map, &zero);
        if (!target)
            return 0;
    }

    if (ip != *target) // 或用 range 判断：if (ip < *target || ip > *target + range) return 0;
        return 0;

    // 命中 -> reserve ringbuf 并保存 regs（你已有的实现）
    struct uprobe_regs *regs = bpf_ringbuf_reserve(&uprobe_regs_rb, sizeof(*regs), 0);
    if (!regs) return 0;

    regs->pid_tgid   = pid_tgid;
    regs->probe_addr = ip;
    regs->rip        = ip;
    regs->rsp        = PT_REGS_SP(ctx);
    regs->rbp        = PT_REGS_FP(ctx);

    bpf_ringbuf_submit(regs, 0);
    return 0;
}
```

Userspace：把 file_offset 转成进程虚拟地址并写入 BPF map
- 计算 vaddr（简单方法，读取 `/proc/<pid>/maps` 找出对应文件的 mapping base）：
```go
func fileOffsetToVaddr(pid int, filePath string, fileOffset uint64) (uint64, error) {
    data, err := os.ReadFile(fmt.Sprintf("/proc/%d/maps", pid))
    if err != nil { return 0, err }
    for _, line := range strings.Split(string(data), "\n") {
        if strings.Contains(line, filePath) {
            fields := strings.Fields(line)
            if len(fields) < 1 { continue }
            addrRange := strings.Split(fields[0], "-")
            base, err := strconv.ParseUint(addrRange[0], 16, 64)
            if err != nil { continue }
            return base + fileOffset, nil
        }
    }
    return 0, fmt.Errorf("mapping for %s not found in /proc/%d/maps", filePath, pid)
}
```

- 使用 `github.com/cilium/ebpf` 把 vaddr 写入 BPF map（示例）：
```go
var key uint32 = uint32(pid)   // 或使用 0 表示全局
var val uint64 = vaddr
if err := targetMap.Update(&key, &val, ebpf.UpdateAny); err != nil {
    return fmt.Errorf("update target_vaddr_map: %w", err)
}
```
（注意：API 视你所用 ebpf 版本而定，常见方法为 `Map.Update(key, value, flags)`。）

替代方案（更简洁，内核直接触发）
- 直接用 libbpf / cilium/ebpf 的 attach upobe 功能，在 userspace 把 BPF 程序 attach 到二进制的 file:offset。优点：内核只在偏移处触发，不用在 BPF 内部比较；缺点：如果要动态对大量偏移做监控或需要按 pid 过滤，管理开销更大。
- 示例思路：用 libbpf 的 attach_uprobe(file, offset, prog) 或 cilium/ebpf/link 中对应的 AttachUprobe API（查具体函数签名并传入文件路径和 offset）。

注意与要点
- file_offset 是相对于文件的偏移；真实运行时地址受 ASLR/PIE 影响，必须加上映射 base 才正确。
- 建议按 pid 存 map（key=pid），或用 key=0 表示全局目标；当目标进程退出时记得删除 map 条目。
- 比较时可用精确相等或范围（函数内偏移），按需求选择。
- 操作 BPF map 与附加 uprobe 通常需要 root / CAP_SYS_ADMIN 权限。
- 如果需要我可以：1) 直接修改 `bpf_uprobe.c`（按上面的代码）；2) 在 cmd 中添加一个用户态函数把 Phase1 CSV 的 file_offset 自动转换并写入 map；3) 或帮你演示用 libbpf attach_uprobe 的具体调用。你想从哪一步开始？