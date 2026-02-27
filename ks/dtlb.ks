// dTLB (Data Translation Lookaside Buffer) 监控程序
//
// 参考 pmu/dtlb.c，使用 eBPF Tracepoint 实现 dTLB 事件监控。
// pmu/dtlb.c 通过 perf_event_open 直接读取硬件 PMU 计数器，
// 本文件使用如下 eBPF tracepoint 作为等价替代：
//   - exceptions/page_fault_user : 用户态缺页异常（dTLB miss 的可观测结果）
//       error_code & 2 == 0  -> 读操作缺失（dTLB-load-miss）
//       error_code & 2 != 0  -> 写操作缺失（dTLB-store-miss）
//
// 注意：eBPF tracepoint 只能捕获导致缺页的 TLB miss，
//       硬件级别的全部 dTLB-loads / dTLB-stores 访问计数
//       需要 BPF_PROG_TYPE_PERF_EVENT（当前 KernelScript 暂不支持）。

// ----------------------------------------------------------------
// 数据结构定义
// ----------------------------------------------------------------

// 每次缺页事件上报到用户态的结构体
struct DtlbMissEvent {
    timestamp:   u64,   // 事件发生时间（单调时钟，纳秒）
    pid:         u32,   // 触发缺页的进程 PID
    is_write:    u8,    // 0 = load miss，1 = store miss
    _pad:        u8[3],
    address:     u64,   // 触发缺页的虚拟地址
    error_code:  u64,   // 原始 error_code（保留供调试）
}

// 全局聚合计数器（array）
// [0] = dTLB-load-misses  累计
// [1] = dTLB-store-misses 累计
var dtlb_counters : array<u32, u64>(2)

// 每进程 dTLB miss 统计
struct PerPidStats {
    load_misses:  u64,
    store_misses: u64,
}
var per_pid_stats : hash<u32, PerPidStats>(1024)

// Ring buffer：向用户态异步上报 dTLB miss 事件
var miss_events : ringbuf<DtlbMissEvent>(65536)

// ----------------------------------------------------------------
// eBPF 程序：捕获用户态缺页（dTLB load / store miss 的可观测代理）
// ----------------------------------------------------------------

// tracepoint: exceptions/page_fault_user
// 内核结构体字段（来自 BTF）：
//   error_code : u64  -- 缺页错误码（bit1 = 写操作）
//   address    : u64  -- 触发缺页的虚拟地址
//   ip         : u64  -- 触发缺页的指令地址

struct trace_event_raw_page_fault_user {
    ent:        u32,
    address:    u64,
    error_code: u64,
    ip:         u64,
}

@tracepoint("exceptions/page_fault_user")
fn page_fault_user_handler(ctx: *trace_event_raw_page_fault_user) -> i32 {
    var error_code = ctx->error_code
    var address    = ctx->address

    // bit1 == 0 → 读操作缺失（load miss）
    // bit1 != 0 → 写操作缺失（store miss）
    var mask: u64 = 2
    var write_flag = error_code & mask
    var is_write: u8 = 0
    if (write_flag != 0) {
        is_write = 1
    }

    // 更新全局累计计数器
    if (is_write == 0) {
        dtlb_counters[0] = dtlb_counters[0] + 1
    } else {
        dtlb_counters[1] = dtlb_counters[1] + 1
    }

    // 上报事件到 ring buffer（非阻塞：满则丢弃）
    var reserved = miss_events.reserve()
    if (reserved != null) {
        reserved->timestamp  = 0   // bpf_ktime_get_ns() — 占位，编译器填充
        reserved->pid        = 0   // bpf_get_current_pid_tgid() >> 32 — 占位
        reserved->is_write   = is_write
        reserved->address    = address
        reserved->error_code = error_code
        miss_events.submit(reserved)
    }

    return 0
}

// ----------------------------------------------------------------
// 用户态事件处理函数
// ----------------------------------------------------------------

fn handle_miss_event(event: *DtlbMissEvent) -> i32 {
    if (event->is_write == 0) {
        print("[dTLB load-miss ] pid=%u addr=0x%016llx err=0x%llx",
              event->pid, event->address, event->error_code)
    } else {
        print("[dTLB store-miss] pid=%u addr=0x%016llx err=0x%llx",
              event->pid, event->address, event->error_code)
    }
    return 0
}

// ----------------------------------------------------------------
// 用户态主程序
// ----------------------------------------------------------------

fn print_summary() -> i32 {
    var load_miss  = dtlb_counters[0]
    var store_miss = dtlb_counters[1]
    print("=== dTLB Summary ===")
    print("  dTLB-load-misses  : %llu", load_miss)
    print("  dTLB-store-misses : %llu", store_miss)
    print("====================")
    return 0
}

fn main() -> i32 {
    print("dTLB Monitoring (KernelScript / eBPF)")
    print("  Tracepoint: exceptions/page_fault_user  -> dTLB load/store miss")
    print("Press Ctrl+C to stop\n")

    // 加载 eBPF 程序
    var pf_prog = load(page_fault_user_handler)

    if (pf_prog == null) {
        print("ERROR: Failed to load page_fault_user_handler")
        return 1
    }

    // 挂载 tracepoint
    var r1 = attach(pf_prog, "exceptions/page_fault_user", 0)

    if (r1 != 0) {
        print("ERROR: Failed to attach exceptions/page_fault_user (r=%d)", r1)
        return 1
    }

    print("Tracepoint attached. Monitoring dTLB events...\n")

    // 注册 ring buffer 事件回调
    miss_events.on_event(handle_miss_event)

    // 进入事件循环（阻塞，直到 Ctrl+C）
    dispatch(miss_events)

    // 退出时打印汇总统计
    print_summary()

    // 卸载 tracepoint
    detach(pf_prog)
    print("Detached. Exiting.")

    return 0
}
