#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>

// uprobe 触发时同步抓取的栈字节数（从 RSP 起向高地址）。
// BPF handler 在目标线程上下文中同步执行，此时栈内容完全有效，
// 避免用户态异步消费时栈帧已被覆写的 TOCTOU 问题。
#define UPROBE_STACK_BYTES 1024

// uprobe 捕获的寄存器快照 + 实时栈快照
struct uprobe_regs {
    __u64 pid_tgid;
    __u64 probe_addr;                  // 触发 uprobe 的虚拟地址（RIP）
    __u64 rip;                         // 指令指针
    __u64 rsp;                         // 栈指针（SFrame CFA 基准）
    __u64 rbp;                         // 帧指针（FP 展开基准）
    __u32 stack_len;                   // 实际捕获的栈字节数（0 = 捕获失败）
    __u8  stack_data[UPROBE_STACK_BYTES]; // 从 RSP 起抓取的原始栈内容
};

// 目标 PID（0 表示不过滤）
volatile const __u32 UPROBE_TARGET_PID = 0;

// ring buffer：向用户态传送寄存器数据
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 4096 * 1024); // 4MB
} uprobe_regs_rb SEC(".maps");

// uprobe 程序：在指定地址被触发时捕获完整寄存器上下文
SEC("uprobe")
int capture_uprobe(struct pt_regs *ctx)
{
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u32 pid = (__u32)(pid_tgid >> 32);

    if (UPROBE_TARGET_PID != 0 && pid != UPROBE_TARGET_PID) {
        return 0;
    }

    struct uprobe_regs *regs = bpf_ringbuf_reserve(&uprobe_regs_rb,
                                                    sizeof(struct uprobe_regs), 0);
    if (!regs) {
        return 0;
    }

    regs->pid_tgid   = pid_tgid;
    regs->probe_addr = PT_REGS_IP(ctx);
    regs->rip        = PT_REGS_IP(ctx);
    regs->rsp        = PT_REGS_SP(ctx);
    regs->rbp        = PT_REGS_FP(ctx);

    // 同步抓取栈快照：uprobe handler 在目标线程上下文中执行，
    // 此时 RSP 指向的栈内容保证有效，不存在用户态异步消费时的过期问题。
    regs->stack_len = 0;
    if (bpf_probe_read_user(regs->stack_data, UPROBE_STACK_BYTES,
                            (void *)PT_REGS_SP(ctx)) == 0) {
        regs->stack_len = UPROBE_STACK_BYTES;
    }

    bpf_ringbuf_submit(regs, 0);
    return 0;
}

char LICENSE[] SEC("license") = "GPL";
