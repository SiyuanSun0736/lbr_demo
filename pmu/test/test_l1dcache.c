/*
 * test_l1dcache.c — L1D 缓存压力测试
 *
 * 分三个阶段针对不同的 L1D 访问模式施压：
 *
 *   阶段 hot  : 工作集 16 KB（≤ L1D），顺序读写 → 极高 L1D 命中率
 *               产生大量 L1-dcache-loads / L1-dcache-stores
 *
 *   阶段 miss : 工作集 512 KB（超出 L1D 32KB，在 L2 内），随机访问
 *               → L1D Miss（L2 Hit），拉高 L1-dcache-load-misses
 *
 *   阶段 evict: 顺序扫描 512KB，每 64 字节访问一次（按 cache line）
 *               → 持续把 L1D 内容替换出去（L1-dcache-load-misses++）
 *
 * 目标计数器：
 *   L1-dcache-loads / L1-dcache-load-misses / L1-dcache-stores
 *
 * 用法：
 *   ./test_l1dcache [duration_sec]
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

#define L1D_SIZE     (16UL  * 1024)         /* 16 KB — L1D 热工作集     */
#define L2_SIZE      (512UL * 1024)         /* 512 KB — L1D Miss 工作集 */
#define INNER_HOT    5000000
#define INNER_MISS   1000000

static volatile uint64_t g_sink    = 0;
static volatile int      g_running = 1;

static void handle_sig(int s __attribute__((unused))) { g_running = 0; }

static inline uint64_t xorshift64(uint64_t *s)
{
    *s ^= *s << 13; *s ^= *s >> 7; *s ^= *s << 17;
    return *s;
}

/* ── 阶段 hot：顺序读 + 写，工作集全部在 L1D 内 ── */
static void l1d_hot_read(const uint64_t *buf, size_t n)
{
    uint64_t sum = 0;
    for (int r = 0; r < 100; r++)
        for (size_t i = 0; i < n; i++)
            sum += buf[i];
    g_sink ^= sum;
}

static void l1d_hot_write(uint64_t *buf, size_t n)
{
    uint64_t v = g_sink;
    for (int r = 0; r < 100; r++)
        for (size_t i = 0; i < n; i++)
            buf[i] = v ^ (uint64_t)i;
    g_sink ^= buf[n - 1];
}

/* ── 阶段 miss：随机访问 512KB 工作集 → L1D Miss ── */
static void l1d_miss_load(const uint64_t *buf, size_t n)
{
    uint64_t sum = 0;
    uint64_t rng = 0xabcdef1234567890ULL ^ g_sink;
    for (int i = 0; i < INNER_MISS; i++) {
        xorshift64(&rng);
        sum += buf[rng % n];
    }
    g_sink ^= sum;
}

static void l1d_miss_store(uint64_t *buf, size_t n)
{
    uint64_t rng = 0xfeedface98765432ULL ^ g_sink;
    for (int i = 0; i < INNER_MISS; i++) {
        xorshift64(&rng);
        buf[rng % n] ^= rng;
    }
    g_sink ^= buf[0];
}

/* ── 阶段 evict：按 cache line 步长顺序扫描，持续驱逐 L1D 内容 ── */
static void l1d_evict_scan(const uint8_t *buf, size_t size)
{
    uint64_t sum = 0;
    /* 步长 64 字节（一个 cache line），每次加载一行的第一字 */
    for (size_t off = 0; off < size; off += 64)
        sum += *(const uint64_t *)(buf + off);
    g_sink ^= sum;
}

int main(int argc, char **argv)
{
    int duration = 60;
    if (argc > 1) duration = atoi(argv[1]);
    if (duration <= 0) duration = 60;

    signal(SIGINT,  handle_sig);
    signal(SIGTERM, handle_sig);

    size_t l1_n = L1D_SIZE / sizeof(uint64_t);
    size_t l2_n = L2_SIZE  / sizeof(uint64_t);

    uint64_t *hot_buf = malloc(L1D_SIZE);
    uint64_t *big_buf = malloc(L2_SIZE);
    if (!hot_buf || !big_buf) { perror("malloc"); return 1; }

    memset(hot_buf, 0xAA, L1D_SIZE);
    memset(big_buf, 0x55, L2_SIZE);

    printf("=== L1D 缓存压力测试 ===\n");
    printf("PID             : %d\n", getpid());
    printf("Hot 工作集      : %lu KB (≤ L1D 32 KB)\n",
           (unsigned long)(L1D_SIZE >> 10));
    printf("Miss 工作集     : %lu KB (> L1D，在 L2 内)\n",
           (unsigned long)(L2_SIZE  >> 10));
    printf("持续时间        : %d 秒\n", duration);
    printf("阶段            : hot_r → hot_w → miss_r → miss_w → evict (循环)\n\n");
    fflush(stdout);

    time_t start = time(NULL);
    long   round = 0;
    int    phase = 0;
    const char *names[] = { "hot_r ", "hot_w ", "miss_r", "miss_w", "evict " };

    while (g_running && (time(NULL) - start) < duration) {
        switch (phase % 5) {
        case 0: l1d_hot_read(hot_buf, l1_n);             break;
        case 1: l1d_hot_write(hot_buf, l1_n);            break;
        case 2: l1d_miss_load(big_buf, l2_n);            break;
        case 3: l1d_miss_store(big_buf, l2_n);           break;
        case 4: l1d_evict_scan((uint8_t *)big_buf, L2_SIZE); break;
        }
        printf("\r  round=%-6ld  phase=%s  elapsed=%-3lds",
               ++round, names[phase % 5], (long)(time(NULL) - start));
        fflush(stdout);
        phase++;
    }

    printf("\n完成 %ld 轮。\n", round);
    free(hot_buf);
    free(big_buf);
    return 0;
}
