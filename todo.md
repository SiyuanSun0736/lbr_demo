正在优化工具选择...我将创建一个简短计划，列出实现 Phase 2 捕获寄存器的关键步骤。

Created 5 todos

可以。简要说明与实现要点：

- 思路：为热点地址动态附加一个 uprobe（用户态探针），在探针执行时在 BPF 程序内读取寄存器上下文（`struct pt_regs` / `PT_REGS`），把需要的寄存器值通过 perf ring buffer 或 map 发回用户态。userspace 根据当前 Phase 写入目标 PID 和要监控的地址/偏移，并在 Phase2 开始时 attach，结束时 detach。
- 为什么用 uprobe：它能在用户态任意代码地址（可执行或共享库的文件偏移）上触发，并且 BPF 接口会传入寄存器上下文（x86-64 下用 `PT_REGS` 访问通用寄存器），比在内核侧依赖 LBR 更直接、准确。
- 关键技术点：
  1. 确定地址对应的文件和偏移：hotAddr - moduleBase = file_offset。可通过 `/proc/<pid>/maps` 读取 module 基址，或使用已有的符号解析工具计算偏移。
  2. BPF 程序（C）样板：定义 `BPF_PERF_OUTPUT(events)`（或 ringbuf），uprobe 处理函数以 `struct pt_regs *ctx` 参数读取 `PT_REGS_*`（如 `PT_REGS_PARM1(ctx)` 或直接读取 `ctx->rax/rcx/...` ），填充事件结构并 `events.perf_submit()`。
  3. 进程过滤：在 BPF 程序里用 `bpf_get_current_pid_tgid()` 检查 PID，只处理目标 PID，这样 uprobe 可全局 attach 但只为目标进程记录。
  4. 用户态（Go）部分：用 `cilium/ebpf` 加载 program、创建 perf reader（ringbuf），在 Phase2 到来时动态 attach uprobes 到目标文件+偏移，写入 `target_pid` 到 BPF map，读出事件并记录寄存器快照；Phase2 结束后 detach 并关闭 reader。
  5. 安全性与稳定性：避免在任意指令上 attach 可能会触发不可重入或原子性问题，优先 attach 到函数入口或已知安全位置；在高频触发场景考虑采样率或条件（例如只在命中热点 addr 时再启用短时间采样）。

- 最小 BPF C 示例（x86-64，伪码）：
  - 事件结构：
    struct regs_event { u32 pid; u64 ip; u64 rax, rbx, rcx, rdx, rsi, rdi, rbp, rsp, r8, r9, r10, r11, r12, r13, r14, r15; };
  - uprobe handler:
    int on_uprobe(struct pt_regs *ctx) {
      u64 pidtgid = bpf_get_current_pid_tgid();
      u32 pid = pidtgid >> 32;
      if (pid != *target_pid_from_map) return 0;
      struct regs_event ev = {};
      ev.pid = pid;
      ev.ip = PT_REGS_IP(ctx);
      ev.rax = PT_REGS_PARM1(ctx) /*或 ctx->ax*/; /*用合适宏/访问方式*/
      ... fill others ...
      events.perf_submit(ctx, &ev, sizeof(ev));
      return 0;
    }

- Go 端要点（伪码）：
  - 计算 file path 与 offset（hotAddr - base）
  - 使用 ebpf/link 的 Uprobe attach： link.Uprobe(binaryPath, offset, prog, nil)
  - 在 BPF map 写入 target PID
  - 启动 perf.Reader 或 ringbuf 读取 `regs_event`
  - 在 Phase2 结束时调用 link.Close() 卸载

下一步我可以：
- 为你在仓库里生成上述 BPF 程序模板和 Go-side 加载/attach 示例，并把对应 TODO 标记为 in-progress/complete。是否要我继续生成并在 repo 中添加实现代码？