/*
 * test_l1d_pend_miss.c — L1D 悬挂缺失（pending miss）压力测试
 *
 * 目标计数器：
 *   l1d.replacement       — L1D cache line 被替换次数
 *   l1d_pend_miss.fb_full — Fill Buffer 满导致的请求阻塞
 *   l1d_pend_miss.pending — 每个周期内处于 pending 状态的 L1D 缺失数（累计）
 *
 * 原理：
 *   l1d_pend_miss.pending 统计每个时钟周期中正在等待的 L1D
 *   Fill Buffer（LFB）条目数之和。要使该值大，需要同时产生
 *   尽量多的未决 L1D miss，即最大化"内存级并行度"（MLP）。
 *
 *   方法：维护 N_STREAMS 条独立的随机访问流，每条流访问不同的
 *   超大数组（远超 LLC）。将所有流的 访问交替交织（interleave），
 *   让 CPU 的乱序执行引擎同时发出多个 DRAM 请求，使 LFB 尽量满载。
 *
 *   N_STREAMS 选取 16（≈ Intel Meteor Lake L1D LFB 深度上限）。
 *   每流数组大小 32 MB，总计 512 MB >> LLC，保证几乎所有访问
 *   都穿透到 DRAM，产生高延迟的悬挂 miss。
 *
 * 用法：
 *   ./test_l1d_pend_miss [duration_sec]
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

#define N_STREAMS    16                            /* 并行独立访问流数          */
#define STREAM_BYTES (32UL * 1024 * 1024)          /* 每流 32 MB，合计超过 LLC  */
#define INNER        200000                        /* 每轮迭代次数              */

static volatile uint64_t g_sink    = 0;
static volatile int      g_running = 1;

static void handle_sig(int s __attribute__((unused))) { g_running = 0; }

/*
 * 多流交织随机访问：
 *
 * 关键点：N_STREAMS 个流的地址计算相互独立（不存在数据依赖），
 * 使 CPU 的乱序窗口可以同时将多条 load 指令送入执行单元。
 * 待 DRAM 回填之前，这些 load 都会占据 LFB 条目，令
 * l1d_pend_miss.pending 在等待期间持续累积。
 */
__attribute__((noinline))
static void multi_stream_random(uint64_t *streams[N_STREAMS], size_t n_elem)
{
    /* N 个独立的 xorshift64 状态 */
    uint64_t seeds[N_STREAMS];
    uint64_t sums[N_STREAMS];

    for (int s = 0; s < N_STREAMS; s++) {
        seeds[s] = 0x1234567890abcdefULL
                   ^ ((uint64_t)s * 0x9e3779b97f4a7c15ULL)
                   ^ g_sink;
        sums[s] = 0;
    }

    for (int i = 0; i < INNER; i++) {
        /*
         * 展开：所有流的下一个索引先全部计算，再统一发出 load。
         * 这样编译器（和 CPU）更容易并发地调度多个 load。
         */
        uint64_t idx[N_STREAMS];
        for (int s = 0; s < N_STREAMS; s++) {
            seeds[s] ^= seeds[s] << 13;
            seeds[s] ^= seeds[s] >> 7;
            seeds[s] ^= seeds[s] << 17;
            idx[s] = seeds[s] % n_elem;
        }
        /* 交织 load — 鼓励硬件同时发出 N_STREAMS 个缺失请求 */
        for (int s = 0; s < N_STREAMS; s++)
            sums[s] += streams[s][idx[s]];
    }

    uint64_t total = 0;
    for (int s = 0; s < N_STREAMS; s++)
        total ^= sums[s];
    g_sink ^= total;
}

/*
 * 顺序预取阶段：顺序扫描全部数组，目的是驱逐残余缓存内容，
 * 确保下一轮 multi_stream_random 的首次访问仍能产生 DRAM miss。
 */
__attribute__((noinline))
static void flush_caches(uint64_t *streams[N_STREAMS], size_t n_elem)
{
    uint64_t sum = 0;
    for (int s = 0; s < N_STREAMS; s++)
        for (size_t i = 0; i < n_elem; i += 64)   /* stride=64 words = 512B */
            sum += streams[s][i];
    g_sink ^= sum;
}

int main(int argc, char **argv)
{
    int duration = 60;
    if (argc > 1) duration = atoi(argv[1]);
    if (duration <= 0) duration = 60;

    signal(SIGINT,  handle_sig);
    signal(SIGTERM, handle_sig);

    uint64_t *streams[N_STREAMS];
    size_t    n_elem = STREAM_BYTES / sizeof(uint64_t);

    for (int s = 0; s < N_STREAMS; s++) {
        streams[s] = malloc(STREAM_BYTES);
        if (!streams[s]) {
            perror("malloc");
            /* 释放已分配 */
            for (int k = 0; k < s; k++) free(streams[k]);
            return 1;
        }
        /* 实际写入以强制物理页分配，避免第一轮触发批量 page fault */
        for (size_t i = 0; i < n_elem; i++)
            streams[s][i] = (uint64_t)i ^ (uint64_t)(s * 0x5a5a5a5a5a5a5a5aULL);
    }

    printf("=== L1D 悬挂缺失压力测试 ===\n");
    printf("PID         : %d\n", getpid());
    printf("并行访问流  : %d\n", N_STREAMS);
    printf("每流大小    : %lu MB（远超 LLC，访问命中 DRAM）\n",
           (unsigned long)(STREAM_BYTES >> 20));
    printf("总内存使用  : %lu MB\n",
           (unsigned long)(N_STREAMS * STREAM_BYTES >> 20));
    printf("持续时间    : %d 秒\n\n", duration);
    fflush(stdout);

    time_t start = time(NULL);
    long   round = 0;
    long   flush_every = 4;   /* 每 4 轮随机访问后刷一次缓存 */

    while (g_running && (time(NULL) - start) < duration) {
        multi_stream_random(streams, n_elem);
        round++;
        if (round % flush_every == 0)
            flush_caches(streams, n_elem);
        printf("\r  round=%-6ld  elapsed=%-3lds", round, (long)(time(NULL) - start));
        fflush(stdout);
    }

    printf("\n完成 %ld 轮（其中随机 %ld 轮，flush %ld 次）。\n",
           round, round, round / flush_every);

    for (int s = 0; s < N_STREAMS; s++)
        free(streams[s]);
    return 0;
}
