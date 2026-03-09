/*
 * test_dtlb.c — dTLB 压力测试
 *
 * 通过以页大小为步长随机访问超大缓冲区，强制每次内存访问都触发
 * dTLB Miss（Load Miss + Store Miss），使硬件 TLB 的各个层级
 * 处于高压状态。
 *
 * 目标计数器：
 *   dTLB-loads / dTLB-load-misses
 *   dTLB-stores / dTLB-store-misses
 *
 * 原理：
 *   典型 Intel CPU 的 L1 dTLB 有 64 条目（4K 页），L2 STLB 有 1536 条目。
 *   使用 256 MB 缓冲区（=65536 页），随机以 PAGE_SIZE 步长访问，
 *   每次访问指向不同的物理页，完全超出 STLB 容量，接近 100% miss 率。
 *
 * 用法：
 *   ./test_dtlb [duration_sec]
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

#define PAGE_SIZE    4096UL
#define BUF_SIZE     (256UL * 1024 * 1024)   /* 256 MB，远超 STLB 覆盖范围 */
#define NUM_PAGES    (BUF_SIZE / PAGE_SIZE)   /* 65536 页 */
#define INNER        1000000                  /* 每轮访问次数              */

static volatile uint64_t g_sink    = 0;
static volatile int      g_running = 1;

static void handle_sig(int s __attribute__((unused))) { g_running = 0; }

/* xorshift64—轻量伪随机，避免 rand() 的 cache 开销 */
static inline uint64_t xorshift64(uint64_t *state)
{
    uint64_t x = *state;
    x ^= x << 13;
    x ^= x >> 7;
    x ^= x << 17;
    return (*state = x);
}

/*
 * dTLB Load 压力：随机以 PAGE_SIZE 步长读取
 * 每次读取 = 不同物理页 = dTLB Load Miss
 */
static void dtlb_load_stress(const uint8_t *buf)
{
    uint64_t sum = 0;
    uint64_t rng = 0xdeadbeef12345678ULL ^ g_sink;
    for (int i = 0; i < INNER; i++) {
        xorshift64(&rng);
        sum += buf[(rng % NUM_PAGES) * PAGE_SIZE];
    }
    g_sink ^= sum;
}

/*
 * dTLB Store 压力：随机以 PAGE_SIZE 步长写入
 * 每次写入 = 不同物理页 = dTLB Store Miss
 */
static void dtlb_store_stress(uint8_t *buf)
{
    uint64_t rng = 0xfeedcafe87654321ULL ^ g_sink;
    for (int i = 0; i < INNER; i++) {
        xorshift64(&rng);
        buf[(rng % NUM_PAGES) * PAGE_SIZE] = (uint8_t)(rng & 0xff);
    }
    g_sink ^= buf[0];
}

/*
 * dTLB Load+Store 混合压力：读后写同一随机页
 * 同时触发 Load Miss 和 Store Miss
 */
static void dtlb_mixed_stress(uint8_t *buf)
{
    uint64_t sum = 0;
    uint64_t rng = 0x123456789abcdef0ULL ^ g_sink;
    for (int i = 0; i < INNER; i++) {
        xorshift64(&rng);
        size_t page = rng % NUM_PAGES;
        sum += buf[page * PAGE_SIZE];                     /* load  */
        buf[page * PAGE_SIZE] = (uint8_t)(sum & 0xff);   /* store */
    }
    g_sink ^= sum;
}

int main(int argc, char **argv)
{
    int duration = 60;
    if (argc > 1) duration = atoi(argv[1]);
    if (duration <= 0) duration = 60;

    signal(SIGINT,  handle_sig);
    signal(SIGTERM, handle_sig);

    uint8_t *buf = malloc(BUF_SIZE);
    if (!buf) {
        perror("malloc");
        return 1;
    }
    /* 强制触发物理页分配，避免第一轮产生 page fault 噪声 */
    memset(buf, 0x55, BUF_SIZE);

    printf("=== dTLB 压力测试 ===\n");
    printf("PID       : %d\n", getpid());
    printf("工作集    : %lu MB (%lu 页，远超 STLB=%d 条目)\n",
           (unsigned long)(BUF_SIZE >> 20),
           (unsigned long)NUM_PAGES, 1536);
    printf("访问步长  : %lu 字节 (PAGE_SIZE)\n", PAGE_SIZE);
    printf("持续时间  : %d 秒\n", duration);
    printf("阶段      : load → store → mixed (循环)\n\n");
    fflush(stdout);

    time_t start = time(NULL);
    long   round = 0;
    int    phase = 0;
    const char *phase_names[] = { "load ", "store", "mixed" };

    while (g_running && (time(NULL) - start) < duration) {
        switch (phase % 3) {
        case 0: dtlb_load_stress(buf);  break;
        case 1: dtlb_store_stress(buf); break;
        case 2: dtlb_mixed_stress(buf); break;
        }
        printf("\r  round=%-6ld  phase=%s  elapsed=%-3lds",
               ++round, phase_names[phase % 3], (long)(time(NULL) - start));
        fflush(stdout);
        phase++;
    }

    printf("\n完成 %ld 轮。\n", round);
    free(buf);
    return 0;
}
