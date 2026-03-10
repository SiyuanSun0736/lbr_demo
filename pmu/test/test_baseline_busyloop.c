/*
 * test_baseline_busyloop.c — 纯忙等基准程序
 *
 * CPU 全速空转（busy loop），不做任何内存访问，用于测量
 * PMU 计数器在"CPU 满负荷但无 cache/TLB 压力"时的基线值：
 *
 *   - instructions / cycles 接近最大（衡量 IPC 上限）
 *   - L1D/L2/LLC miss、TLB miss 接近零
 *   - mem_inst_retired 接近零
 *
 * 与其他基准对比：
 *   test_baseline_sleep   → CPU 让出，几乎零事件
 *   test_baseline_busyloop → CPU 满转，零 miss，纯指令压力
 *   test_workload2         → CPU 满转 + 各类 miss 压力
 *
 * 实现：
 *   仅执行 xorshift64 自旋（纯寄存器运算，无内存访问），
 *   编译器无法消除（依赖 g_sink 输出）。
 *
 * 用法：
 *   ./test_baseline_busyloop [duration_sec]
 *   默认运行 60 秒。
 */

#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <time.h>
#include <signal.h>
#include <unistd.h>

#define INNER_ITERS  10000000   /* 每次调用的迭代次数，约 10–20 ms */

static volatile uint64_t g_sink    = 0;
static volatile int      g_running = 1;

static void handle_sig(int s __attribute__((unused))) { g_running = 0; }

static inline double now_sec(void)
{
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (double)ts.tv_sec + (double)ts.tv_nsec * 1e-9;
}

/*
 * 纯寄存器运算忙等：xorshift64 自旋
 *
 * 选择 xorshift64 的原因：
 *   - 完全在寄存器内运算（无 load/store），cache/TLB 事件为零
 *   - 每次迭代包含 XOR+shift 共 6 条指令，IPC 接近流水线满载
 *   - 依赖 g_sink 传入初始值并写回，防止编译器将整个循环优化掉
 *   - 输出 8 路累加（s0–s7），利用超标量乱序窗口，避免单链依赖
 *     导致的流水线停顿（与真实自旋锁的单变量写法形成对比）
 */
__attribute__((noinline, optimize("O2")))
static void busyloop(void)
{
    /* 8 路独立累加，充分利用超标量 ILP */
    uint64_t s0 = g_sink ^ 0x0101010101010101ULL;
    uint64_t s1 = g_sink ^ 0x0202020202020202ULL;
    uint64_t s2 = g_sink ^ 0x0404040404040404ULL;
    uint64_t s3 = g_sink ^ 0x0808080808080808ULL;
    uint64_t s4 = g_sink ^ 0x1010101010101010ULL;
    uint64_t s5 = g_sink ^ 0x2020202020202020ULL;
    uint64_t s6 = g_sink ^ 0x4040404040404040ULL;
    uint64_t s7 = g_sink ^ 0x8080808080808080ULL;

    for (int i = 0; i < INNER_ITERS; i++) {
        /* xorshift64 — 6 条整数指令/路，纯寄存器操作 */
        s0 ^= s0 << 13; s0 ^= s0 >> 7; s0 ^= s0 << 17;
        s1 ^= s1 << 13; s1 ^= s1 >> 7; s1 ^= s1 << 17;
        s2 ^= s2 << 13; s2 ^= s2 >> 7; s2 ^= s2 << 17;
        s3 ^= s3 << 13; s3 ^= s3 >> 7; s3 ^= s3 << 17;
        s4 ^= s4 << 13; s4 ^= s4 >> 7; s4 ^= s4 << 17;
        s5 ^= s5 << 13; s5 ^= s5 >> 7; s5 ^= s5 << 17;
        s6 ^= s6 << 13; s6 ^= s6 >> 7; s6 ^= s6 << 17;
        s7 ^= s7 << 13; s7 ^= s7 >> 7; s7 ^= s7 << 17;
    }
    g_sink = s0 ^ s1 ^ s2 ^ s3 ^ s4 ^ s5 ^ s6 ^ s7;
}

int main(int argc, char **argv)
{
    int duration = 60;
    if (argc > 1) duration = atoi(argv[1]);
    if (duration <= 0) duration = 60;

    signal(SIGINT,  handle_sig);
    signal(SIGTERM, handle_sig);

    printf("=== 纯忙等基准测试 (busyloop) ===\n");
    printf("PID      : %d\n", getpid());
    printf("持续时间 : %d 秒\n", duration);
    printf("模式     : 纯寄存器 xorshift64 自旋，无内存访问\n\n");
    fflush(stdout);

    double t_end = now_sec() + (double)duration;
    long calls = 0;

    while (g_running && now_sec() < t_end) {
        busyloop();
        calls++;
    }

    printf("完成。calls=%ld  g_sink=0x%016llx\n",
           calls, (unsigned long long)g_sink);
    return 0;
}
