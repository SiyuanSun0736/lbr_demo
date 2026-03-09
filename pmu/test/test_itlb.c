/*
 * test_itlb.c — iTLB 压力测试
 *
 * 通过在运行时动态分配 NUM_FUNCS 个可执行内存页，在每页起始处
 * 写入一段简单的函数代码，然后通过乱序函数指针表调用这些函数，
 * 使 CPU 的 iTLB 频繁缺失（每次调用都跳到不同的代码页）。
 *
 * 目标计数器：
 *   iTLB-loads / iTLB-load-misses
 *
 * 原理：
 *   典型 Intel CPU 的 L1 iTLB 有 128 条目（4K 页），L2 STLB 约 1536 条目。
 *   使用 NUM_FUNCS=512 个代码页（每页 4KB），随机调用顺序使每次 CALL
 *   都指向不同页，完全超出 STLB 容量。
 *
 *   每页写入的函数机器码（x86-64）：
 *     48 8D 87 NN NN NN NN    lea rax, [rdi + N]   (7 字节)
 *     C3                      ret                  (1 字节)
 *     CC CC ... CC             int3 (trap) 填充    (剩余字节)
 *
 * 安全性：
 *   先 mmap(PROT_WRITE)，写完代码后 mprotect(PROT_READ|PROT_EXEC)，
 *   遵循 W^X 原则，避免被内核或 SELinux 拒绝。
 *
 * 用法：
 *   ./test_itlb [duration_sec]
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

#define PAGE_SIZE   4096UL
#define NUM_FUNCS   512          /* 512个独立代码页，远超 iTLB/STLB 容量 */

typedef int (*func_t)(int);

static volatile uint64_t g_sink    = 0;
static volatile int      g_running = 1;

static void handle_sig(int s __attribute__((unused))) { g_running = 0; }

/*
 * 在指定内存页起始处写入 x86-64 函数：
 *   lea rax, [rdi + idx]   ; 返回值 = 参数 + idx
 *   ret
 * 剩余字节填充 int3 (0xCC)，触发 #BP 以便发现意外执行。
 *
 * 编码细节：
 *   REX.W    = 0x48
 *   Opcode   = 0x8D (LEA)
 *   ModRM    = mod=10 (disp32), reg=000(rax), r/m=111(rdi) → 0x87
 *   disp32   = idx 的 32 位小端表示（sign-extend 到 64 位 = idx，因为 idx ≥ 0）
 */
static void write_func_stub(uint8_t *page, int idx)
{
    /* lea rax, [rdi + idx] */
    page[0] = 0x48;                         /* REX.W             */
    page[1] = 0x8D;                         /* opcode: LEA r64,m */
    page[2] = 0x87;                         /* ModRM             */
    page[3] = (uint8_t)( idx        & 0xFF);
    page[4] = (uint8_t)((idx >>  8) & 0xFF);
    page[5] = (uint8_t)((idx >> 16) & 0xFF);
    page[6] = (uint8_t)((idx >> 24) & 0xFF);
    page[7] = 0xC3;                         /* ret               */
    /* fill remainder with int3 */
    memset(page + 8, 0xCC, PAGE_SIZE - 8);
}

/* Fisher-Yates shuffle — 打乱函数指针调用顺序 */
static void shuffle(func_t *arr, int n)
{
    uint64_t rng = 0x1234567890abcdefULL ^ (uint64_t)(uintptr_t)arr;
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

    size_t total = (size_t)NUM_FUNCS * PAGE_SIZE;

    /* 第一步：以 PROT_WRITE 分配（W^X：先写后执行） */
    void *mem = mmap(NULL, total,
                     PROT_READ | PROT_WRITE,
                     MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (mem == MAP_FAILED) {
        perror("mmap");
        return 1;
    }

    /* 第二步：向每页写入函数机器码 */
    func_t funcs[NUM_FUNCS];
    for (int i = 0; i < NUM_FUNCS; i++) {
        uint8_t *page = (uint8_t *)mem + (size_t)i * PAGE_SIZE;
        write_func_stub(page, i);
        funcs[i] = (func_t)(void *)page;
    }

    /* 第三步：改为 PROT_READ|PROT_EXEC（不允许 Write，符合 W^X） */
    if (mprotect(mem, total, PROT_READ | PROT_EXEC) != 0) {
        perror("mprotect");
        munmap(mem, total);
        return 1;
    }

    /* 打乱初始调用顺序 */
    func_t order[NUM_FUNCS];
    memcpy(order, funcs, sizeof(funcs));
    shuffle(order, NUM_FUNCS);

    printf("=== iTLB 压力测试 ===\n");
    printf("PID       : %d\n", getpid());
    printf("代码页数  : %d（每函数独占一个 %lu KB 代码页）\n",
           NUM_FUNCS, PAGE_SIZE / 1024);
    printf("总代码区  : %zu KB（远超 iTLB/STLB 覆盖范围）\n", total / 1024);
    printf("持续时间  : %d 秒\n\n", duration);
    fflush(stdout);

    time_t   start = time(NULL);
    long     round = 0;

    while (g_running && (time(NULL) - start) < duration) {
        uint64_t sum = 0;
        for (int i = 0; i < NUM_FUNCS; i++)
            sum += (uint64_t)order[i](i);
        g_sink ^= sum;
        /* 每轮重新乱序，防止 CPU 学习固定地址序列 */
        shuffle(order, NUM_FUNCS);
        printf("\r  round=%-6ld  elapsed=%-3lds", ++round, (long)(time(NULL) - start));
        fflush(stdout);
    }

    printf("\n完成 %ld 轮。\n", round);
    munmap(mem, total);
    return 0;
}
