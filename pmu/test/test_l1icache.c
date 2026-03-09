/*
 * test_l1icache.c — L1I 缓存压力测试
 *
 * 通过在运行时动态分配大量可执行代码（总量 >> L1I 容量 32KB），
 * 并以随机顺序调用分布在这些代码页上的函数，迫使 CPU 的指令
 * 缓存（L1 I-cache）频繁缺失并从 L2/LLC 重新加载代码行。
 *
 * 目标计数器：
 *   L1-icache-load-misses
 *
 * 原理：
 *   典型 Intel CPU 的 L1 I-cache 为 32KB，共 512 条 64 字节 cache line。
 *   本程序在连续 mmap 内存中写入 NUM_FUNCS=1024 个函数桩，每个函数
 *   桩大小 STUB_BYTES=128 字节（= 2 条 cache line），总代码量 128KB。
 *
 *   关键设计：
 *     - 函数密集排列（128 字节对齐，不按页对齐）→ iTLB 压力低
 *     - 总代码量 128KB >> L1I 32KB → L1I 极高 miss 率
 *     - 随机调用顺序 → 阻止硬件预取器预判下一条代码行
 *
 *   函数代码布局（x86-64，128 字节/函数）：
 *     [0..6]   48 8D 87 NN NN NN NN  lea rax, [rdi + N]
 *     [7]      C3                    ret
 *     [8..127] 0F 1F 40 00 ...       long NOP（不影响语义，但填满 cache line）
 *
 * 安全性：
 *   先 mmap(PROT_WRITE)，写完代码后 mprotect(PROT_READ|PROT_EXEC)，
 *   遵循 W^X 原则。
 *
 * 用法：
 *   ./test_l1icache [duration_sec]
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
#include <sys/mman.h>

#define NUM_FUNCS    1024    /* 函数总数                              */
#define STUB_BYTES   128     /* 每个函数占用的字节数（= 2 cache line）*/
/* 总代码量 = 1024 × 128 = 128 KB >> L1I 32 KB */

typedef int (*func_t)(int);

static volatile uint64_t g_sink    = 0;
static volatile int      g_running = 1;

static void handle_sig(int s __attribute__((unused))) { g_running = 0; }

/*
 * 写入函数桩：
 *   lea rax, [rdi + N]   (7 字节)
 *   ret                  (1 字节)
 *   long NOP × 15        (each 8 字节 = "0F 1F 84 00 00 00 00 00"，共 120 字节)
 *
 * long NOP 不影响性能，但填占 cache line，确保每个函数确实消耗
 * 2 条 64 字节 cache line。
 */
static void write_stub(uint8_t *dst, int idx)
{
    /* lea rax, [rdi + idx] — REX.W + LEA + ModRM + disp32 */
    dst[0] = 0x48;
    dst[1] = 0x8D;
    dst[2] = 0x87;
    dst[3] = (uint8_t)( idx        & 0xFF);
    dst[4] = (uint8_t)((idx >>  8) & 0xFF);
    dst[5] = (uint8_t)((idx >> 16) & 0xFF);
    dst[6] = (uint8_t)((idx >> 24) & 0xFF);
    dst[7] = 0xC3;  /* ret */

    /* 填充 15 个 8 字节 long NOP：0F 1F 84 00 00 00 00 00 */
    for (int j = 0; j < 15; j++) {
        uint8_t *p = dst + 8 + j * 8;
        p[0] = 0x0F; p[1] = 0x1F; p[2] = 0x84; p[3] = 0x00;
        p[4] = 0x00; p[5] = 0x00; p[6] = 0x00; p[7] = 0x00;
    }
}

/* Fisher-Yates shuffle */
static void shuffle(func_t *arr, int n)
{
    uint64_t rng = 0xdeadbeef87654321ULL ^ (uint64_t)(uintptr_t)arr;
    for (int i = n - 1; i > 0; i--) {
        rng ^= rng << 13; rng ^= rng >> 7; rng ^= rng << 17;
        int j = (int)(rng % (uint64_t)(i + 1));
        func_t tmp = arr[i]; arr[i] = arr[j]; arr[j] = tmp;
    }
}

int main(int argc, char **argv)
{
    int duration = 60;
    if (argc > 1) duration = atoi(argv[1]);
    if (duration <= 0) duration = 60;

    signal(SIGINT,  handle_sig);
    signal(SIGTERM, handle_sig);

    size_t total = (size_t)NUM_FUNCS * STUB_BYTES;

    /* 必须按 STUB_BYTES 对齐分配；mmap 返回页对齐地址，满足要求 */
    void *mem = mmap(NULL, total,
                     PROT_READ | PROT_WRITE,
                     MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (mem == MAP_FAILED) {
        perror("mmap");
        return 1;
    }

    func_t funcs[NUM_FUNCS];
    for (int i = 0; i < NUM_FUNCS; i++) {
        uint8_t *dst = (uint8_t *)mem + (size_t)i * STUB_BYTES;
        write_stub(dst, i);
        funcs[i] = (func_t)(void *)dst;
    }

    /* W^X：写完后切换为只读+可执行 */
    if (mprotect(mem, total, PROT_READ | PROT_EXEC) != 0) {
        perror("mprotect");
        munmap(mem, total);
        return 1;
    }

    func_t order[NUM_FUNCS];
    memcpy(order, funcs, sizeof(funcs));
    shuffle(order, NUM_FUNCS);

    printf("=== L1I 缓存压力测试 ===\n");
    printf("PID        : %d\n", getpid());
    printf("函数数量   : %d\n", NUM_FUNCS);
    printf("每函数大小 : %d 字节（= %d cache lines × 64 B）\n",
           STUB_BYTES, STUB_BYTES / 64);
    printf("总代码量   : %zu KB（L1I 32KB 的 %zu 倍）\n",
           total / 1024, total / 1024 / 32);
    printf("持续时间   : %d 秒\n\n", duration);
    fflush(stdout);

    time_t   start = time(NULL);
    long     round = 0;

    while (g_running && (time(NULL) - start) < duration) {
        uint64_t sum = 0;
        for (int i = 0; i < NUM_FUNCS; i++)
            sum += (uint64_t)order[i](i);
        g_sink ^= sum;
        shuffle(order, NUM_FUNCS);   /* 每轮重新随机排列 */
        printf("\r  round=%-6ld  elapsed=%-3lds", ++round, (long)(time(NULL) - start));
        fflush(stdout);
    }

    printf("\n完成 %ld 轮。\n", round);
    munmap(mem, total);
    return 0;
}
