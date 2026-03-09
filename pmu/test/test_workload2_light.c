/*
 * test_workload2_light.c — 低频率综合 PMU 压力测试
 *
 * 与 test_workload2.c 结构完全相同（8 阶段、函数指针表驱动），
 * 但设计为"低频率"模式：
 *
 *   ① 每次函数调用的内层迭代数缩减为 test_workload2 的 1/20
 *   ② 每次调用结束后 usleep(IDLE_US)（默认 2000 µs = 2 ms），
 *      拉低 PMU 事件的到达频率，让采样间隔内的计数器增量更小
 *
 * 典型用途：
 *   - 与 test_workload2 对比，观察 PMU 计数器在不同负载强度下的线性关系
 *   - 测试 PMU 采样器对低事件率的分辨能力
 *   - 作为"中等负载"基准（介于 baseline_sleep 和 test_workload2 之间）
 *
 * 用法：
 *   ./test_workload2_light [duration_sec [idle_us]]
 *   duration_sec : 总运行时长，默认 64 秒
 *   idle_us      : 每次调用后的休眠微秒数，默认 2000（2 ms）
 *
 * 阶段与目标计数器和 test_workload2 相同：
 *  P1 L1D-HOT    → L1-dcache-loads/stores, mem_inst_retired.*
 *  P2 L2-THRASH  → L1-dcache-load-misses, l1d.replacement
 *  P3 LLC-HIT    → LLC-loads/stores, cache-references
 *  P4 LLC-MISS   → LLC-load-misses, cache-misses, dTLB-load-misses
 *  P5 DTLB-STORE → dTLB-stores, dTLB-store-misses
 *  P6 PEND-MISS  → l1d_pend_miss.pending, l1d_pend_miss.fb_full
 *  P7 PTR-CHASE  → LLC-load-misses（串行），内存访问延迟
 *  P8 ITLB-MISS  → iTLB-loads, iTLB-load-misses
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

/* ── 工作集大小（与 test_workload2 相同）────────────────────────────── */
#define L1D_BYTES       (16UL  * 1024)
#define L2_BYTES        (256UL * 1024)
#define LLC_HIT_BYTES   (8UL   * 1024 * 1024)
#define DRAM_BYTES      (256UL * 1024 * 1024)
#define PAGE_BYTES       4096UL
#define NUM_CODE_PAGES   512

/* ── 内层迭代次数：test_workload2 的 1/20 ──────────────────────────── */
#define ITER_HOT         200000   /* P1: 4M → 200K  */
#define ITER_L2           25000   /* P2: 500K → 25K */
#define ITER_LLC          10000   /* P3: 200K → 10K */
#define ITER_DRAM          5000   /* P4: 100K → 5K  */
#define ITER_DTLB_ST      25000   /* P5: 500K → 25K */
#define ITER_PEND         10000   /* P6: 200K → 10K */
#define ITER_CHASE        10000   /* P7: 200K → 10K */
#define ITER_ITLB        200000   /* P8: 4M → 200K  */

/* ── 默认每次调用后的空闲时间（微秒）── */
#define DEFAULT_IDLE_US  2000     /* 2 ms */

/* ── 全局 ── */
static volatile uint64_t g_sink    = 0;
static volatile int      g_running = 1;
static int               g_idle_us = DEFAULT_IDLE_US;

static void handle_sig(int s __attribute__((unused))) { g_running = 0; }

static inline uint64_t xorshift64(uint64_t *s)
{
    *s ^= *s << 13; *s ^= *s >> 7; *s ^= *s << 17; return *s;
}

typedef int (*jit_func_t)(int);

typedef struct {
    uint64_t   *l1d_buf;
    uint64_t   *l2_buf;
    uint64_t   *llc_buf;
    uint8_t    *dram_buf;
    uintptr_t  *chase;
    size_t      chase_n;
    jit_func_t *funcs;
    int        *call_order;
    int         n_funcs;
} ctx_t;

static inline double now_sec(void)
{
    struct timespec ts;
    clock_gettime(CLOCK_MONOTONIC, &ts);
    return (double)ts.tv_sec + (double)ts.tv_nsec * 1e-9;
}

static void print_ts(const char *tag)
{
    struct timespec ts;
    clock_gettime(CLOCK_REALTIME, &ts);
    struct tm tm_info;
    localtime_r(&ts.tv_sec, &tm_info);
    char buf[32];
    strftime(buf, sizeof(buf), "%Y-%m-%d %H:%M:%S", &tm_info);
    printf("[%s.%03ld] %s\n", buf, ts.tv_nsec / 1000000L, tag);
    fflush(stdout);
}

/* ════════════ 各阶段实现（逻辑与 test_workload2 相同，迭代次数已缩减）══ */

__attribute__((noinline, optimize("O2")))
static void phase_l1d_hot(ctx_t *c)
{
    const size_t n = L1D_BYTES / sizeof(uint64_t);
    uint64_t *b = c->l1d_buf;
    uint64_t s = g_sink;
    for (int i = 0; i < ITER_HOT; i += 4) {
        size_t k = (size_t)i & (n - 1);
        s ^= b[k];             b[k]             = s + (uint64_t)i;
        s ^= b[(k+1)&(n-1)];   b[(k+1)&(n-1)]   = s + (uint64_t)(i+1);
        s ^= b[(k+2)&(n-1)];   b[(k+2)&(n-1)]   = s + (uint64_t)(i+2);
        s ^= b[(k+3)&(n-1)];   b[(k+3)&(n-1)]   = s + (uint64_t)(i+3);
    }
    g_sink = s;
}

__attribute__((noinline))
static void phase_l2_thrash(ctx_t *c)
{
    const size_t n = L2_BYTES / sizeof(uint64_t);
    uint64_t *b = c->l2_buf;
    uint64_t rng = 0xabcdef1234567890ULL ^ g_sink;
    uint64_t s = 0;
    for (int i = 0; i < ITER_L2; i++) {
        xorshift64(&rng);
        size_t k = rng & (n - 1);
        s += b[k]; b[k] ^= s;
    }
    g_sink ^= s;
}

__attribute__((noinline))
static void phase_llc_hit(ctx_t *c)
{
    const size_t n = LLC_HIT_BYTES / sizeof(uint64_t);
    uint64_t *b = c->llc_buf;
    uint64_t rng = 0xdeadbeefcafe1234ULL ^ g_sink;
    uint64_t s = 0;
    for (int i = 0; i < ITER_LLC; i++) {
        xorshift64(&rng);
        size_t k = rng & (n - 1);
        s += b[k]; b[k] ^= rng;
    }
    g_sink ^= s;
}

__attribute__((noinline))
static void phase_llc_miss(ctx_t *c)
{
    const size_t n = DRAM_BYTES / sizeof(uint64_t);
    uint64_t *b = (uint64_t *)(void *)c->dram_buf;
    uint64_t rng = 0x8765432112345678ULL ^ g_sink;
    uint64_t s = 0;
    for (int i = 0; i < ITER_DRAM; i++) {
        xorshift64(&rng);
        size_t k = rng & (n - 1);
        s += b[k]; b[k] ^= rng;
    }
    g_sink ^= s;
}

__attribute__((noinline))
static void phase_dtlb_store(ctx_t *c)
{
    const size_t n_pages = DRAM_BYTES / PAGE_BYTES;
    uint8_t *b = c->dram_buf;
    uint64_t rng = 0xfeedcafe87654321ULL ^ g_sink;
    for (int i = 0; i < ITER_DTLB_ST; i++) {
        xorshift64(&rng);
        b[(rng % n_pages) * PAGE_BYTES] = (uint8_t)(rng & 0xff);
    }
    g_sink ^= b[0];
}

__attribute__((noinline, optimize("O2")))
static void phase_pend_miss(ctx_t *c)
{
    const size_t n = LLC_HIT_BYTES / sizeof(uint64_t);
    const uint64_t *b = c->llc_buf;
    uint64_t r0 = 0xa1b2c3d4e5f60708ULL ^ g_sink;
    uint64_t r1 = 0x1234567890abcdefULL;
    uint64_t r2 = 0xfedcba9876543210ULL;
    uint64_t r3 = 0x0807060504030201ULL;
    uint64_t s0 = 0, s1 = 0, s2 = 0, s3 = 0;
    for (int i = 0; i < ITER_PEND; i++) {
        xorshift64(&r0); s0 += b[r0 & (n-1)];
        xorshift64(&r1); s1 += b[r1 & (n-1)];
        xorshift64(&r2); s2 += b[r2 & (n-1)];
        xorshift64(&r3); s3 += b[r3 & (n-1)];
    }
    g_sink ^= s0 ^ s1 ^ s2 ^ s3;
}

__attribute__((noinline))
static void phase_ptr_chase(ctx_t *c)
{
    const uintptr_t *arr = c->chase;
    const size_t n = c->chase_n;
    size_t idx = (size_t)(g_sink % n);
    for (int i = 0; i < ITER_CHASE; i++)
        idx = arr[idx];
    g_sink ^= (uint64_t)idx;
}

__attribute__((noinline))
static void phase_itlb_miss(ctx_t *c)
{
    if (c->n_funcs <= 0) return;
    int n = c->n_funcs;
    int *order = c->call_order;
    int sum = 0;
    for (int i = 0; i < ITER_ITLB; i++)
        sum += c->funcs[order[i % n]]((int)((uint64_t)g_sink & 0xff));
    g_sink ^= (uint64_t)(unsigned int)sum;
}

/* ── JIT 代码页辅助函数 ── */
static void write_stub(uint8_t *page, int idx)
{
    page[0] = 0x48; page[1] = 0x8D; page[2] = 0x87;
    page[3] = (uint8_t)( idx        & 0xFF);
    page[4] = (uint8_t)((idx >>  8) & 0xFF);
    page[5] = (uint8_t)((idx >> 16) & 0xFF);
    page[6] = (uint8_t)((idx >> 24) & 0xFF);
    page[7] = 0xC3;
    memset(page + 8, 0xCC, PAGE_BYTES - 8);
}

static void shuffle_ints(int *arr, int n)
{
    uint64_t rng = 0x1234567890abcdefULL ^ (uint64_t)(uintptr_t)arr;
    for (int i = n - 1; i > 0; i--) {
        xorshift64(&rng);
        int j = (int)(rng % (uint64_t)(unsigned int)(i + 1));
        int t = arr[i]; arr[i] = arr[j]; arr[j] = t;
    }
}

static void build_chase(uintptr_t *arr, size_t n)
{
    size_t *perm = malloc(n * sizeof(size_t));
    if (!perm) return;
    for (size_t i = 0; i < n; i++) perm[i] = i;
    uint64_t rng = 0x123456789abcdef0ULL;
    for (size_t i = n - 1; i > 0; i--) {
        rng = rng * 6364136223846793005ULL + 1442695040888963407ULL;
        size_t j = rng % (i + 1);
        size_t t = perm[i]; perm[i] = perm[j]; perm[j] = t;
    }
    for (size_t i = 0; i < n; i++)
        arr[perm[i]] = perm[(i + 1) % n];
    free(perm);
}

/* ── 阶段定义表 ── */
typedef struct {
    const char *name;
    const char *counters;
    void      (*fn)(ctx_t *);
} phase_def_t;

static const phase_def_t PHASES[] = {
    { "P1:L1D-HOT",    "L1-dcache-loads/stores, mem_inst_retired.*",        phase_l1d_hot    },
    { "P2:L2-THRASH",  "L1-dcache-load-misses, l1d.replacement",            phase_l2_thrash  },
    { "P3:LLC-HIT",    "LLC-loads/stores, cache-references",                 phase_llc_hit    },
    { "P4:LLC-MISS",   "LLC-load-misses, cache-misses, dTLB-load-misses",    phase_llc_miss   },
    { "P5:DTLB-STORE", "dTLB-stores, dTLB-store-misses",                    phase_dtlb_store },
    { "P6:PEND-MISS",  "l1d_pend_miss.pending, l1d_pend_miss.fb_full",       phase_pend_miss  },
    { "P7:PTR-CHASE",  "LLC-load-misses (serial), mem latency",              phase_ptr_chase  },
    { "P8:ITLB-MISS",  "iTLB-loads, iTLB-load-misses, L1-icache-load-miss",  phase_itlb_miss  },
};
#define NUM_PHASES  ((int)(sizeof(PHASES) / sizeof(PHASES[0])))

/* ████ main ████ */
int main(int argc, char **argv)
{
    int duration = NUM_PHASES * 8;
    if (argc > 1) duration = atoi(argv[1]);
    if (duration <= 0) duration = NUM_PHASES * 8;

    g_idle_us = DEFAULT_IDLE_US;
    if (argc > 2) g_idle_us = atoi(argv[2]);
    if (g_idle_us < 0) g_idle_us = 0;

    int phase_sec = duration / NUM_PHASES;
    if (phase_sec < 1) phase_sec = 1;

    signal(SIGINT,  handle_sig);
    signal(SIGTERM, handle_sig);

    printf("=== 低频率综合 PMU 压力测试 ===\n");
    printf("PID           : %d\n", getpid());
    printf("总时长        : %d 秒\n", duration);
    printf("每阶段时长    : %d 秒\n", phase_sec);
    printf("每次调用后空闲: %d µs  (duty≈%.1f%%)\n",
           g_idle_us,
           g_idle_us > 0 ? 100.0 * 2.0 / (2.0 + g_idle_us * 1e-3) : 100.0);
    printf("内存需求      : ~%lu MB\n\n",
           (unsigned long)((L1D_BYTES + L2_BYTES + LLC_HIT_BYTES + DRAM_BYTES
                            + LLC_HIT_BYTES) >> 20));
    fflush(stdout);

    /* ── 分配工作集 ── */
    ctx_t ctx;
    memset(&ctx, 0, sizeof(ctx));

    ctx.l1d_buf  = calloc(L1D_BYTES     / sizeof(uint64_t), sizeof(uint64_t));
    ctx.l2_buf   = calloc(L2_BYTES      / sizeof(uint64_t), sizeof(uint64_t));
    ctx.llc_buf  = calloc(LLC_HIT_BYTES / sizeof(uint64_t), sizeof(uint64_t));
    ctx.dram_buf = malloc(DRAM_BYTES);
    ctx.chase_n  = LLC_HIT_BYTES / sizeof(uintptr_t);
    ctx.chase    = malloc(ctx.chase_n * sizeof(uintptr_t));

    if (!ctx.l1d_buf || !ctx.l2_buf || !ctx.llc_buf || !ctx.dram_buf || !ctx.chase) {
        fprintf(stderr, "内存分配失败\n");
        return 1;
    }

    memset(ctx.dram_buf, 0x55, DRAM_BYTES);
    for (size_t i = 0; i < LLC_HIT_BYTES / sizeof(uint64_t); i++)
        ctx.llc_buf[i] = (uint64_t)i;
    g_sink = ctx.llc_buf[0];
    build_chase(ctx.chase, ctx.chase_n);
    printf("内存初始化完成。\n");

    /* ── 初始化 JIT 代码页 ── */
    ctx.funcs      = calloc(NUM_CODE_PAGES, sizeof(jit_func_t));
    ctx.call_order = malloc(NUM_CODE_PAGES * sizeof(int));
    ctx.n_funcs    = 0;

    if (ctx.funcs && ctx.call_order) {
        for (int i = 0; i < NUM_CODE_PAGES; i++) {
            uint8_t *page = mmap(NULL, PAGE_BYTES,
                                 PROT_READ | PROT_WRITE,
                                 MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
            if (page == MAP_FAILED) break;
            write_stub(page, i);
            if (mprotect(page, PAGE_BYTES, PROT_READ | PROT_EXEC) != 0) {
                munmap(page, PAGE_BYTES); break;
            }
            ctx.funcs[ctx.n_funcs] = (jit_func_t)(void *)page;
            ctx.call_order[ctx.n_funcs] = ctx.n_funcs;
            ctx.n_funcs++;
        }
        if (ctx.n_funcs > 0) {
            shuffle_ints(ctx.call_order, ctx.n_funcs);
            printf("iTLB 代码页: %d 页 (%lu KB)\n",
                   ctx.n_funcs, (unsigned long)(ctx.n_funcs * PAGE_BYTES >> 10));
        } else {
            printf("警告: iTLB 代码页 mmap 失败（P8 将跳过）。\n");
        }
    }
    printf("\n");
    fflush(stdout);

    /* ── 主循环 ── */
    double t_total_end = now_sec() + (double)duration;
    int round = 0;

    while (g_running && now_sec() < t_total_end) {
        round++;
        printf("── Round %d ────────────────────────────────────────────────\n", round);

        for (int p = 0; p < NUM_PHASES && g_running; p++) {
            char tag[160];

            snprintf(tag, sizeof(tag), "%-16s %-52s BEGIN",
                     PHASES[p].name, PHASES[p].counters);
            print_ts(tag);

            double ph_end = now_sec() + (double)phase_sec;
            long calls = 0;
            while (g_running && now_sec() < ph_end) {
                PHASES[p].fn(&ctx);
                calls++;
                /* 每次调用后空闲，降低 PMU 事件到达频率 */
                if (g_idle_us > 0)
                    usleep((useconds_t)g_idle_us);
            }

            snprintf(tag, sizeof(tag), "%-16s END   calls=%-6ld  sink=0x%016llx",
                     PHASES[p].name, calls, (unsigned long long)g_sink);
            print_ts(tag);
        }
    }

    /* ── 清理 ── */
    for (int i = 0; i < ctx.n_funcs; i++)
        if (ctx.funcs[i])
            munmap((void *)(uintptr_t)(void *)ctx.funcs[i], PAGE_BYTES);
    free(ctx.funcs);
    free(ctx.call_order);
    free(ctx.l1d_buf);
    free(ctx.l2_buf);
    free(ctx.llc_buf);
    free(ctx.dram_buf);
    free(ctx.chase);

    printf("\n完成。共 %d 轮，g_sink=0x%016llx\n",
           round, (unsigned long long)g_sink);
    return 0;
}
