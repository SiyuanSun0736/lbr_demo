#include "vmlinux.h"
#include <bpf/bpf_helpers.h>

#define MAX_LBR_ENTRIES 32

volatile const __u32 TARGET_PID = 0;
volatile const __u32 CPU_MASK = 0xFFFF;

struct lbr_data {
    __u64 pid_tgid;
    __s64 nr_bytes;
    char comm[16];
    struct perf_branch_entry entries[MAX_LBR_ENTRIES];
};

struct lbr_data lbr_buff[1] SEC(".data.lbrs");

// 用于存储最终结果的 HASH map
struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 1024);
    __type(key, __u64);
    __type(value, struct lbr_data);
} lbr_map SEC(".maps");

// perf_event 程序：周期性采样时捕获 LBR
SEC("perf_event")
int capture_lbr(struct bpf_perf_event_data *ctx)
{
    __u32 cpu;
    struct lbr_data *lbr;
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u32 pid = pid_tgid >> 32;
    __u32 tgid = pid_tgid >> 32;

    // 如果指定了目标 PID，则只捕获该 PID 的数据
    if (TARGET_PID != 0 && tgid != TARGET_PID) {
        return 0;
    }

    // 获取当前 CPU 并访问对应的 lbr_buff 元素
    cpu = bpf_get_smp_processor_id() & CPU_MASK;
    lbr = &lbr_buff[cpu];

    // 使用 bpf_get_branch_snapshot 获取 LBR 数据
    __s64 ret = bpf_get_branch_snapshot(lbr->entries, sizeof(lbr->entries), 0);
    
    if (ret <= 0) {
        return 0;
    }

    lbr->pid_tgid = pid_tgid;
    lbr->nr_bytes = ret;
    
    // 获取并存储进程名称
    bpf_get_current_comm(lbr->comm, sizeof(lbr->comm));

    // 使用 pid_tgid 作为 key，确保每个线程有独立的记录
    bpf_map_update_elem(&lbr_map, &pid_tgid, lbr, BPF_ANY);
    
    return 0;
}

char LICENSE[] SEC("license") = "GPL";