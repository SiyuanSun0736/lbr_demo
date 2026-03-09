/*
 * test_llc.c — LLC（最后级缓存）压力测试
 *
 * 分两个阶段针对 LLC 的命中与缺失施压：
 *
 *   阶段 hit  : 工作集 8 MB（超出 L2，但在典型 LLC 内），随机访问
 *               → LLC 命中（L1/L2 Miss + LLC Hit）
 *               主要产生：LLC-loads / LLC-stores / cache-references
 *
 *   阶段 miss : 工作集 256 MB（远超 LLC），随机访问
 *               → LLC 缺失，访问 DRAM
 *               主要产生：LLC-load-misses / LLC-store-misses / cache-misses
 *
 * 目标计数器：
 *   LLC-loads / LLC-load-misses / LLC-stores / LLC-store-misses
 *   cache-references / cache-misses
 *
 * 用法：
 *   ./test_llc [duration_sec]
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

#define LLC_HIT_SIZE    (8UL   * 1024 * 1024)  /* 8 MB — 在 LLC 内    */
#define LLC_MISS_SIZE   (256UL * 1024 * 1024)  /* 256 MB — 超出 LLC   */
#define INNER           800000

static volatile uint64_t g_sink    = 0;
static volatile int      g_running = 1;

static void handle_sig(int s __attribute__((unused))) { g_running = 0; }

static inline uint64_t xorshift64(uint64_t *s)
{
    *s ^= *s << 13; *s ^= *s >> 7; *s ^= *s << 17;
    return *s;
}

/* 随机读 — 在 [0, n_elem) 范围内均匀随机读取 */
__attribute__((noinline))
static void random_load(const uint64_t *buf, size_t n_elem)
{
    uint64_t sum = 0;
    uint64_t rng = 0xdeadbeef12345678ULL ^ g_sink;
    for (int i = 0; i < INNER; i++) {
        xorshift64(&rng);
        sum += buf[rng % n_elem];
    }
    g_sink ^= sum;
}

/* 随机写 — 在 [0, n_elem) 范围内均匀随机写入 */
__attribute__((noinline))
static void random_store(uint64_t *buf, size_t n_elem)
{
    uint64_t rng = 0xfeedcafe87654321ULL ^ g_sink;
    for (int i = 0; i < INNER; i++) {
        xorshift64(&rng);
        buf[rng % n_elem] ^= rng;
    }
    g_sink ^= buf[0];
}

/* 随机读改写 — load + store 同一位置；同时拉高 LLC loads 和 stores */
__attribute__((noinline))
static void random_rmw(uint64_t *buf, size_t n_elem)
{
    uint64_t rng = 0x9e3779b97f4a7c15ULL ^ g_sink;
    for (int i = 0; i < INNER; i++) {
        xorshift64(&rng);
        size_t idx = rng % n_elem;
        buf[idx] ^= rng + buf[idx];   /* read-modify-write */
    }
    g_sink ^= buf[0];
}

int main(int argc, char **argv)
{
    int duration = 60;
    if (argc > 1) duration = atoi(argv[1]);
    if (duration <= 0) duration = 60;

    signal(SIGINT,  handle_sig);
    signal(SIGTERM, handle_sig);

    size_t hit_n  = LLC_HIT_SIZE  / sizeof(uint64_t);
    size_t miss_n = LLC_MISS_SIZE / sizeof(uint64_t);

    uint64_t *hit_buf  = malloc(LLC_HIT_SIZE);
    uint64_t *miss_buf = malloc(LLC_MISS_SIZE);
    if (!hit_buf || !miss_buf) {
        perror("malloc");
        free(hit_buf);
        free(miss_buf);
        return 1;
    }

    /* 强制物理页分配 */
    memset(hit_buf,  0x11, LLC_HIT_SIZE);
    memset(miss_buf, 0x22, LLC_MISS_SIZE);

    printf("=== LLC 压力测试 ===\n");
    printf("PID           : %d\n", getpid());
    printf("LLC命中工作集 : %lu MB（在典型 L3 缓存内）\n",
           (unsigned long)(LLC_HIT_SIZE  >> 20));
    printf("LLC缺失工作集 : %lu MB（远超 LLC，访问 DRAM）\n",
           (unsigned long)(LLC_MISS_SIZE >> 20));
    printf("持续时间      : %d 秒\n", duration);
    printf("阶段          : hit_r → hit_rmw → miss_r → miss_w (循环)\n\n");
    fflush(stdout);

    time_t start = time(NULL);
    long   round = 0;
    int    phase = 0;
    const char *names[] = { "hit_r  ", "hit_rmw", "miss_r ", "miss_w " };

    while (g_running && (time(NULL) - start) < duration) {
        switch (phase % 4) {
        case 0: random_load(hit_buf,  hit_n);  break;   /* LLC hit:  loads  */
        case 1: random_rmw(hit_buf,   hit_n);  break;   /* LLC hit:  r+w    */
        case 2: random_load(miss_buf, miss_n); break;   /* LLC miss: loads  */
        case 3: random_store(miss_buf, miss_n); break;  /* LLC miss: stores */
        }
        printf("\r  round=%-6ld  phase=%s  elapsed=%-3lds",
               ++round, names[phase % 4], (long)(time(NULL) - start));
        fflush(stdout);
        phase++;
    }

    printf("\n完成 %ld 轮。\n", round);
    free(hit_buf);
    free(miss_buf);
    return 0;
}
