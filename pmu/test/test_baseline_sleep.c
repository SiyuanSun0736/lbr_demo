/*
 * test_baseline_sleep.c — 多阶段空载基准对比程序
 *
 * 交替运行三种"安静"状态，覆盖不同程度的系统噪声底线，
 * 为各专项 PMU 测试提供更有意义的对比参照：
 *
 *  阶段 A — pure-sleep   : 纯 sleep()，CPU 完全让出，几乎没有 PMU 事件；
 *                          代表进程阻塞/IO 等待场景的噪声底线。
 *
 *  阶段 B — pause-spin   : 用 PAUSE 指令自旋，模拟自旋锁写法；
 *                          会产生少量指令计数，但零 TLB/cache miss。
 *
 *  阶段 C — l1-touch     : 以 cacheline 步长顺序读写 4 KB 热缓冲区
 *                          （完全在 L1D 内），产生稳定的 L1D 命中，
 *                          但 L1D miss / LLC / TLB miss 接近零；
 *                          代表"轻量后台线程"的噪声底线。
 *
 * 设计原则：
 *   - 三阶段各占 1/3 总时长，循环直到超时
 *   - g_sink 防止编译器消除死代码
 *   - 阶段切换时打印时间戳，便于与 PMU 采样点对齐分析
 *
 * 用法：
 *   ./test_baseline_sleep [duration_sec]
 *   默认运行 60 秒。
 */

#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <string.h>
#include <time.h>
#include <signal.h>
#include <unistd.h>

/* L1-touch 缓冲区：4 KB，远小于 L1D（通常 32–48 KB） */
#define L1_BUF_BYTES   4096UL
#define CACHELINE      64UL
#define L1_ELEMS       (L1_BUF_BYTES / sizeof(uint64_t))   /* 512 */
#define TOUCH_ITERS    500000   /* 每轮 touch 次数：保持约 10 ms 执行量 */
#define PAUSE_ITERS    5000000  /* 每轮 pause 次数 */

static volatile uint64_t g_sink    = 0;
static volatile int      g_running = 1;

static void handle_sig(int s __attribute__((unused))) { g_running = 0; }

/* ------------------------------------------------------------------ */
/* 阶段 B — pause-spin                                                  */
/* 使用 PAUSE 指令（x86 hint）降低功耗并减少流水线争用，               */
/* 这是自旋锁等待的标准写法，产生最少的 PMU 副作用。                   */
/* ------------------------------------------------------------------ */
__attribute__((noinline))
static void phase_pause_spin(void)
{
    for (int i = 0; i < PAUSE_ITERS; i++) {
#if defined(__x86_64__) || defined(__i386__)
        __asm__ volatile("pause" ::: "memory");
#else
        /* ARM/其他架构：yield hint */
        __asm__ volatile("" ::: "memory");
#endif
    }
}

/* ------------------------------------------------------------------ */
/* 阶段 C — l1-touch                                                    */
/* cacheline 步长顺序读写，硬件预取器可轻松满足；                      */
/* 写入值依赖 g_sink 防止 DCE，4 路展开减少分支开销。                  */
/* ------------------------------------------------------------------ */
__attribute__((noinline, optimize("O2")))
static void phase_l1_touch(uint64_t *buf)
{
    const size_t mask = L1_ELEMS - 1;   /* L1_ELEMS 是 2 的幂 */
    uint64_t s = g_sink;

    for (int i = 0; i < TOUCH_ITERS; i += 4) {
        size_t b = (size_t)i & mask;
        s           ^= buf[b];
        buf[b]       = s + (uint64_t)i;
        s           ^= buf[(b + 1) & mask];
        buf[(b + 1) & mask] = s + (uint64_t)(i + 1);
        s           ^= buf[(b + 2) & mask];
        buf[(b + 2) & mask] = s + (uint64_t)(i + 2);
        s           ^= buf[(b + 3) & mask];
        buf[(b + 3) & mask] = s + (uint64_t)(i + 3);
    }
    g_sink = s;
}

/* ------------------------------------------------------------------ */
/* 工具：打印当前时间戳（ISO 8601 精度到秒）                            */
/* ------------------------------------------------------------------ */
static void print_ts(const char *tag)
{
    time_t now = time(NULL);
    struct tm tm_info;
    localtime_r(&now, &tm_info);
    char buf[32];
    strftime(buf, sizeof(buf), "%Y-%m-%d %H:%M:%S", &tm_info);
    printf("[%s] %s\n", buf, tag);
    fflush(stdout);
}

/* ------------------------------------------------------------------ */
/* 主函数                                                               */
/* ------------------------------------------------------------------ */
int main(int argc, char **argv)
{
    int duration = 60;
    if (argc > 1) duration = atoi(argv[1]);
    if (duration <= 0) duration = 60;

    signal(SIGINT,  handle_sig);
    signal(SIGTERM, handle_sig);

    printf("=== 多阶段空载基准测试 ===\n");
    printf("PID      : %d\n", getpid());
    printf("持续时间 : %d 秒\n", duration);
    printf("阶段      : A=pure-sleep  B=pause-spin  C=l1-touch (各 1/3 时长)\n\n");
    fflush(stdout);

    /* 分配 L1-touch 缓冲区并预热（避免首次 page-fault 引入噪声） */
    uint64_t *l1_buf = calloc(L1_ELEMS, sizeof(uint64_t));
    if (!l1_buf) {
        fprintf(stderr, "calloc 失败\n");
        return 1;
    }
    /* 预热：确保物理页已映射 */
    for (size_t i = 0; i < L1_ELEMS; i++)
        l1_buf[i] = (uint64_t)i;
    g_sink = l1_buf[0];

    /* 每个阶段时长 = duration / 3，至少 1 秒 */
    int phase_sec = duration / 3;
    if (phase_sec < 1) phase_sec = 1;

    time_t t_start = time(NULL);
    int round = 0;

    while (g_running && (time(NULL) - t_start) < duration) {
        round++;
        printf("--- Round %d ---\n", round);

        /* ── 阶段 A：pure-sleep ── */
        if (!g_running) break;
        print_ts("Phase A: pure-sleep start");
        time_t t_a = time(NULL);
        while (g_running && (time(NULL) - t_a) < phase_sec)
            sleep(1);
        print_ts("Phase A: pure-sleep end");

        /* ── 阶段 B：pause-spin ── */
        if (!g_running) break;
        print_ts("Phase B: pause-spin start");
        time_t t_b = time(NULL);
        while (g_running && (time(NULL) - t_b) < phase_sec)
            phase_pause_spin();
        print_ts("Phase B: pause-spin end");

        /* ── 阶段 C：l1-touch ── */
        if (!g_running) break;
        print_ts("Phase C: l1-touch start");
        time_t t_c = time(NULL);
        while (g_running && (time(NULL) - t_c) < phase_sec)
            phase_l1_touch(l1_buf);
        print_ts("Phase C: l1-touch end");
    }

    free(l1_buf);
    printf("\n完成。g_sink=0x%016llx\n", (unsigned long long)g_sink);
    return 0;
}
