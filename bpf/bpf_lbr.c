#include "vmlinux.h"
#include <bpf/bpf_helpers.h>

#define MAX_LBR_ENTRIES 32

volatile const __u32 PID = -1;
volatile const __u32 CPU_MASK = 0xFFFF;
volatile const __u64 FUNC_IP = 0;

struct lbr_data {
    __u64 pid_tgid;
    __s64 nr_bytes;
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

// // 用于临时存储的 PERCPU_ARRAY map，避免栈溢出
// struct {
//     __uint(type, BPF_MAP_TYPE_PERCPU_ARRAY);
//     __uint(max_entries, 1);
//     __type(key, __u32);
//     __type(value, struct lbr_data);
// } temp_lbr_data SEC(".maps");


SEC("kprobe/__x64_sys_execve")
int trace___x64_sys_execve(void *ctx)
{
    __u32 cpu;
    struct lbr_data *lbr;
    __u64 pid_tgid = bpf_get_current_pid_tgid();
    __u32 pid = pid_tgid >> 32;

    bpf_printk("__x64_sys_execve: pid=%u", pid);

    // 获取当前 CPU 并访问对应的 lbr_buff 元素
    cpu = bpf_get_smp_processor_id() & CPU_MASK;
    lbr = &lbr_buff[cpu];


    // // 从 percpu array 获取临时存储空间
    // __u32 zero = 0;
    // struct lbr_data *data = bpf_map_lookup_elem(&temp_lbr_data, &zero);
    // if (!data) {
    //     bpf_printk("failed to get temp storage");
    //     return 0;
    // }

    // 使用 bpf_get_branch_snapshot 获取 LBR 数据
    __s64 ret = bpf_get_branch_snapshot(lbr->entries, sizeof(lbr->entries), 0);
    
    bpf_printk("bpf_get_branch_snapshot returned: %lld", ret);
    
    if (ret < 0) {
        bpf_printk("bpf_get_branch_snapshot failed: %lld", ret);
        return 0;
    }

    lbr->pid_tgid = pid_tgid;
    lbr->nr_bytes = ret;
    bpf_map_update_elem(&lbr_map, &pid_tgid, lbr, 0);
    bpf_printk("LBR data written: pid=%u, nr_bytes=%lld", pid, lbr->nr_bytes);
    
    return 0;
}

char LICENSE[] SEC("license") = "GPL";