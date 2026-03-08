/*
 * test_pmu_workload.c — 用于 pmu_monitor_all 的压力测试工作负载
 *
 * 本程序制造多种内存访问模式，以在各 PMU 计数器上产生可观测的事件：
 *   - 顺序读写      → L1D/LLC 命中，低 TLB miss
 *   - 随机大跨度读   → LLC miss、dTLB miss
 *   - 指针追逐链    → 串行化 cache miss（典型内存延迟场景）
 *   - 指令密集循环  → iTLB/icache 压力
 *
 * 用法：
 *   ./test/test_pmu_workload [duration_sec]
 *   默认运行 60 秒。
 */

#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <time.h>
#include <stdint.h>
#include <signal.h>

/* ------------------------------------------------------------------ */
/* 工作集大小                                                           */
/* ------------------------------------------------------------------ */
#define L1_BYTES      (32UL  * 1024)            /* 32 KB  – 留在 L1   */
#define LLC_BYTES     (16UL  * 1024 * 1024)     /* 16 MB  – 超出 L2   */
#define TLB_BYTES     (256UL * 1024 * 1024)     /* 256 MB – 超出 dTLB */

#define INNER_ITERS   2000000   /* 每轮内层迭代次数 */

/* ------------------------------------------------------------------ */
/* 全局：使编译器无法消除死代码                                          */
/* ------------------------------------------------------------------ */
static volatile uint64_t g_sink = 0;

static volatile int g_running = 1;

static void handle_sigint(int sig __attribute__((unused))) {
    g_running = 0;
}

/* ------------------------------------------------------------------ */
/* 工作负载 1：顺序读 — L1/L2 热路径                                    */
/* ------------------------------------------------------------------ */
static void workload_seq_read(const uint8_t *buf, size_t size)
{
    uint64_t sum = 0;
    for (size_t i = 0; i < size; i++)
        sum += buf[i];
    g_sink ^= sum;
}

/* ------------------------------------------------------------------ */
/* 工作负载 2：顺序写 — dTLB store 事件                                 */
/* ------------------------------------------------------------------ */
static void workload_seq_write(uint8_t *buf, size_t size)
{
    for (size_t i = 0; i < size; i++)
        buf[i] = (uint8_t)(i * 0x9e + g_sink);
    g_sink ^= buf[size - 1];
}

/* ------------------------------------------------------------------ */
/* 工作负载 3：随机大跨度读 — LLC miss + dTLB miss                      */
/* ------------------------------------------------------------------ */
static void workload_random_read(const uint8_t *buf, size_t size)
{
    uint64_t sum  = 0;
    uint64_t state = 0xdeadbeefcafe1234ULL;
    for (int i = 0; i < INNER_ITERS; i++) {
        /* xorshift64 — 轻量随机，避免 rand() 开销 */
        state ^= state << 13;
        state ^= state >> 7;
        state ^= state << 17;
        sum += buf[state % size];
    }
    g_sink ^= sum;
}

/* ------------------------------------------------------------------ */
/* 工作负载 4：指针追逐 — 串行化 cache miss                             */
/* ------------------------------------------------------------------ */
#define CHASE_ELEMS (LLC_BYTES / sizeof(uintptr_t))

static void build_chase_list(uintptr_t *arr, size_t n)
{
    /* 构造随机排列的链表：arr[i] 存储下一个节点的索引 */
    size_t *perm = malloc(n * sizeof(size_t));
    if (!perm) return;

    for (size_t i = 0; i < n; i++) perm[i] = i;

    /* Fisher-Yates shuffle（使用简单 LCG，确保可重现） */
    uint64_t rng = 0x123456789abcdef0ULL;
    for (size_t i = n - 1; i > 0; i--) {
        rng = rng * 6364136223846793005ULL + 1442695040888963407ULL;
        size_t j = rng % (i + 1);
        size_t tmp = perm[i]; perm[i] = perm[j]; perm[j] = tmp;
    }

    for (size_t i = 0; i < n; i++)
        arr[perm[i]] = perm[(i + 1) % n];

    free(perm);
}

static void workload_pointer_chase(const uintptr_t *arr, size_t n)
{
    size_t idx = 0;
    for (int i = 0; i < INNER_ITERS; i++)
        idx = arr[idx % n];
    g_sink ^= (uint64_t)idx;
}

/* ------------------------------------------------------------------ */
/* main                                                                 */
/* ------------------------------------------------------------------ */
int main(int argc, char **argv)
{
    int duration = 60;
    if (argc > 1)
        duration = atoi(argv[1]);
    if (duration <= 0) duration = 60;

    signal(SIGINT, handle_sigint);
    signal(SIGTERM, handle_sigint);

    printf("=== PMU 压力测试工作负载 ===\n");
    printf("PID       : %d\n", getpid());
    printf("持续时间  : %d 秒\n", duration);
    printf("TLB 工作集: %lu MB\n", (unsigned long)(TLB_BYTES >> 20));
    printf("LLC 工作集: %lu MB\n", (unsigned long)(LLC_BYTES >> 20));
    fflush(stdout);

    /* 分配缓冲区 */
    uint8_t *l1_buf   = malloc(L1_BYTES);
    uint8_t *tlb_buf  = malloc(TLB_BYTES);
    uintptr_t *chase  = malloc(CHASE_ELEMS * sizeof(uintptr_t));

    if (!l1_buf || !tlb_buf || !chase) {
        fprintf(stderr, "内存分配失败\n");
        return 1;
    }

    /* 初始化缓冲区（强制物理页分配） */
    memset(l1_buf,  0xAA, L1_BYTES);
    memset(tlb_buf, 0x55, TLB_BYTES);
    build_chase_list(chase, CHASE_ELEMS);

    printf("内存初始化完成，开始工作负载...\n\n");
    fflush(stdout);

    time_t start = time(NULL);
    int phase = 0;
    long round = 0;

    while (g_running && (time(NULL) - start) < duration) {
        switch (phase % 4) {
        case 0:
            /* 顺序读 L1 – 热缓存路径 */
            for (int r = 0; r < 200; r++)
                workload_seq_read(l1_buf, L1_BYTES);
            break;
        case 1:
            /* 随机读大数组 – 高 cache/TLB miss */
            workload_random_read(tlb_buf, TLB_BYTES);
            break;
        case 2:
            /* 顺序写 – dTLB store 事件 */
            workload_seq_write(tlb_buf, LLC_BYTES);
            break;
        case 3:
            /* 指针追逐 – 串行化 miss 延迟 */
            workload_pointer_chase(chase, CHASE_ELEMS);
            break;
        }

        phase++;
        round++;

        /* 每 4 轮打印一次状态 */
        if (round % 4 == 0) {
            printf("\r  round=%-6ld  elapsed=%lds  phase上一轮=%d  sink=0x%016lx",
                   round, (long)(time(NULL) - start),
                   (phase - 1) % 4, (unsigned long)g_sink);
            fflush(stdout);
        }
    }

    printf("\n\n工作负载结束，共完成 %ld 轮。\n", round);

    free(l1_buf);
    free(tlb_buf);
    free(chase);
    return 0;
}
