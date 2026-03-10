/*
 * test_mem_inst.c — 内存指令吞吐量压力测试
 *
 * 针对 mem_inst_retired.* 计数器，通过在 L1D 内（无 miss 延迟）
 * 执行密集的 load / store / 读改写指令序列，最大化每秒退休的
 * 内存指令总数。
 *
 * 目标计数器：
 *   mem-loads / mem-stores
 *   mem_inst_retired.all_loads  (raw 0x81D0)
 *   mem_inst_retired.all_stores (raw 0x82D0)
 *   mem_inst_retired.any        (raw 0x83D0)
 *
 * 原理：
 *   工作集保持在 L1D（8 KB）内，消除缓存缺失延迟，使
 *   流水线充满 load/store micro-ops，令计数器以接近饱和的
 *   速率递增。手动 4× 循环展开减少分支开销，四路交替访问
 *   避免写后读依赖导致的流水线停顿。
 *
 * 阶段：
 *   load  — 纯 load 密集（4 路展开，顺序步进）
 *   store — 纯 store 密集（4 路展开）
 *   mixed — load + store 混合（读改写，2 路展开）
 *
 * 用法：
 *   ./test_mem_inst [duration_sec]
 *   默认运行 60 秒。
 */

#define _GNU_SOURCE
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <stdint.h>
#include <time.h>
#include <signal.h>
#include <unistd.h>

#define BUF_ELEMS   1024UL   /* 8 KB — 完全在 L1D 32KB 内 */
#define INNER       2000000  /* 每轮内层迭代次数           */

static volatile uint64_t g_sink    = 0;
static volatile int      g_running = 1;

static void handle_sig(int s __attribute__((unused))) { g_running = 0; }

/*
 * Load 密集：4 路展开顺序读取。
 * 步进小（4 word = 32 B），让硬件预取器轻松满足，确保每次
 * 访问都是 L1D 命中，load 延迟 ~4 cycle，吞吐量最大化。
 */
__attribute__((noinline, optimize("O2")))
static void load_heavy(const uint64_t *buf)
{
    uint64_t s0 = 0, s1 = 0, s2 = 0, s3 = 0;
    const size_t mask = BUF_ELEMS - 1;   /* BUF_ELEMS 是 2 的幂 */
    for (int i = 0; i < INNER; i += 4) {
        size_t base = (size_t)i & mask;
        s0 += buf[base];
        s1 += buf[(base + 1) & mask];
        s2 += buf[(base + 2) & mask];
        s3 += buf[(base + 3) & mask];
    }
    g_sink ^= s0 ^ s1 ^ s2 ^ s3;
}

/*
 * Store 密集：4 路展开顺序写入。
 * 写入的值依赖 g_sink（运行时常量），阻止编译器将 store 完全
 * 消除；同时各路写入无数据依赖，允许 CPU 乱序发射。
 */
__attribute__((noinline, optimize("O2")))
static void store_heavy(uint64_t *buf)
{
    uint64_t v    = g_sink;
    const size_t mask = BUF_ELEMS - 1;
    for (int i = 0; i < INNER; i += 4) {
        size_t base = (size_t)i & mask;
        buf[base]               = v ^ (uint64_t) i;
        buf[(base + 1) & mask]  = v ^ (uint64_t)(i + 1);
        buf[(base + 2) & mask]  = v ^ (uint64_t)(i + 2);
        buf[(base + 3) & mask]  = v ^ (uint64_t)(i + 3);
    }
    g_sink ^= buf[0] ^ buf[BUF_ELEMS - 1];
}

/*
 * 混合（读改写）：load 后立即 store 到相邻位置，2 路展开。
 * 每次迭代产生 2 个 load + 2 个 store micro-ops。
 */
__attribute__((noinline, optimize("O2")))
static void mixed_rmw(uint64_t *buf)
{
    uint64_t v = g_sink;
    const size_t mask = BUF_ELEMS - 1;
    for (int i = 0; i < INNER; i += 2) {
        size_t a = (size_t) i      & mask;
        size_t b = (size_t)(i + 1) & mask;
        v ^= buf[a];         /* load  a */
        buf[b] = v;          /* store b */
        v ^= buf[b];         /* load  b */
        buf[a] = v + 1;      /* store a */
    }
    g_sink ^= v;
}

/*
 * 跨步 Load+Store：以 cache line（8 word）为步长交替读写。
 * 模拟稀疏更新场景，保持 L1D 命中率的同时产生更多独立的
 * 地址流（欺骗 mem_inst_retired 计数器统计多种地址模式）。
 */
__attribute__((noinline, optimize("O2")))
static void strided_ldst(uint64_t *buf)
{
    uint64_t v = g_sink;
    for (size_t i = 0; i < BUF_ELEMS; i += 8) {
        /* 读前 4 个 word */
        v ^= buf[i] ^ buf[i+1] ^ buf[i+2] ^ buf[i+3];
        /* 写后 4 个 word */
        buf[i+4] = v;
        buf[i+5] = v + 1;
        buf[i+6] = v + 2;
        buf[i+7] = v + 3;
    }
    g_sink ^= v;
}

int main(int argc, char **argv)
{
    int duration = 60;
    if (argc > 1) duration = atoi(argv[1]);
    if (duration <= 0) duration = 60;

    signal(SIGINT,  handle_sig);
    signal(SIGTERM, handle_sig);

    uint64_t *buf = malloc(BUF_ELEMS * sizeof(uint64_t));
    if (!buf) { perror("malloc"); return 1; }
    for (size_t i = 0; i < BUF_ELEMS; i++)
        buf[i] = (uint64_t)i * 0x9e3779b97f4a7c15ULL;

    printf("=== 内存指令吞吐量测试 ===\n");
    printf("PID        : %d\n", getpid());
    printf("工作集     : %lu KB（全部在 L1D 内，消除缓存延迟）\n",
           (unsigned long)(BUF_ELEMS * sizeof(uint64_t) >> 10));
    printf("持续时间   : %d 秒\n", duration);
    printf("阶段       : load → store → mixed → strided (循环)\n\n");
    fflush(stdout);

    time_t start = time(NULL);
    long   round = 0;
    int    phase = 0;
    const char *names[] = { "load  ", "store ", "mixed ", "stride" };

    while (g_running && (time(NULL) - start) < duration) {
        switch (phase % 4) {
        case 0: load_heavy(buf);          break;
        case 1: store_heavy(buf);         break;
        case 2: mixed_rmw(buf);           break;
        case 3:
            /* strided_ldst 较短，多执行几次以凑成一轮 */
            for (int r = 0; r < 10000; r++)
                strided_ldst(buf);
            break;
        }
        printf("\r  round=%-6ld  phase=%s  elapsed=%-3lds",
               ++round, names[phase % 4], (long)(time(NULL) - start));
        fflush(stdout);
        phase++;
    }

    printf("\n完成 %ld 轮。\n", round);
    free(buf);
    return 0;
}
