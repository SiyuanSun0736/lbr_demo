#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <sys/ioctl.h>
#include <sys/mman.h>
#include <linux/perf_event.h>
#include <asm/unistd.h>
#include <signal.h>
#include <stdint.h>
#include <errno.h>
#include <time.h>
#include <sys/stat.h>

// Performance counter file descriptors
typedef struct {
    int fd;
    const char *name;
    uint64_t count;
    int enabled;
} perf_counter_t;

// Define all counters
static perf_counter_t counters[] = {
    // dTLB counters
    {-1, "dTLB-loads", 0, 0},
    {-1, "dTLB-load-misses", 0, 0},
    {-1, "dTLB-stores", 0, 0},
    {-1, "dTLB-store-misses", 0, 0},
    
    // iTLB counters
    {-1, "iTLB-loads", 0, 0},
    {-1, "iTLB-load-misses", 0, 0},
    
    // L1 data cache counters
    {-1, "L1-dcache-loads", 0, 0},
    {-1, "L1-dcache-load-misses", 0, 0},
    {-1, "L1-dcache-stores", 0, 0},
    
    // L1 instruction cache counters
    {-1, "L1-icache-loads", 0, 0},
    {-1, "L1-icache-load-misses", 0, 0},
    
    // L1D pending miss counters (raw events)
    {-1, "l1d.replacement", 0, 0},
    {-1, "l1d_pend_miss.fb_full", 0, 0},
    {-1, "l1d_pend_miss.pending", 0, 0},
    
    // LLC counters
    {-1, "LLC-loads", 0, 0},
    {-1, "LLC-load-misses", 0, 0},
    {-1, "LLC-stores", 0, 0},
    {-1, "LLC-store-misses", 0, 0},
    {-1, "cache-references", 0, 0},
    {-1, "cache-misses", 0, 0},
    
    // Memory instruction counters (raw events)
    {-1, "mem-loads", 0, 0},
    {-1, "mem-stores", 0, 0},
    {-1, "mem_inst_retired.all_loads", 0, 0},
    {-1, "mem_inst_retired.all_stores", 0, 0},
    {-1, "mem_inst_retired.any", 0, 0},
};

#define NUM_COUNTERS (sizeof(counters) / sizeof(counters[0]))

static FILE *log_file = NULL;

static int perf_event_open(struct perf_event_attr *hw_event, pid_t pid,
                           int cpu, int group_fd, unsigned long flags) {
    int ret = syscall(__NR_perf_event_open, hw_event, pid, cpu, group_fd, flags);
    return ret;
}

static int try_open_perf_event(struct perf_event_attr *pe, pid_t pid, const char *name __attribute__((unused))) {
    int fd = perf_event_open(pe, pid, -1, -1, 0);
    if (fd == -1) {
        fd = perf_event_open(pe, pid, 0, -1, 0);
        if (fd == -1) {
            return -1;
        }
    }
    return fd;
}

void cleanup(int sig __attribute__((unused))) {
    for (size_t i = 0; i < NUM_COUNTERS; i++) {
        if (counters[i].fd >= 0) {
            close(counters[i].fd);
        }
    }
    if (log_file) {
        fclose(log_file);
        printf("\nLog file closed.\n");
    }
    printf("\nCleaning up...\n");
    exit(0);
}

static int init_counter(int idx, struct perf_event_attr *pe, pid_t pid) {
    counters[idx].fd = try_open_perf_event(pe, pid, counters[idx].name);
    if (counters[idx].fd >= 0) {
        counters[idx].enabled = 1;
        ioctl(counters[idx].fd, PERF_EVENT_IOC_ENABLE, 0);
        return 1;
    }
    return 0;
}

int main(int argc, char **argv) {
    struct perf_event_attr pe;
    pid_t target_pid = -1;
    int active_counters = 0;
    int idx = 0;
    
    if (argc > 1) {
        target_pid = atoi(argv[1]);
    }
    
    signal(SIGINT, cleanup);
    signal(SIGTERM, cleanup);
    
    // Create log directory if it doesn't exist
    mkdir("log", 0755);
    
    // Generate timestamped filename
    time_t now = time(NULL);
    struct tm *t = localtime(&now);
    char timestamped_filename[256];
    char link_name[] = "log/pmu_monitor_all.log";
    snprintf(timestamped_filename, sizeof(timestamped_filename), 
             "log/pmu_monitor_all_%04d%02d%02d_%02d%02d%02d.log",
             t->tm_year + 1900, t->tm_mon + 1, t->tm_mday,
             t->tm_hour, t->tm_min, t->tm_sec);
    
    // Open timestamped log file
    log_file = fopen(timestamped_filename, "w");
    if (!log_file) {
        fprintf(stderr, "Failed to open log file: %s\n", strerror(errno));
        return 1;
    }
    
    // Write header to log file
    fprintf(log_file, "Timestamp");
    for (size_t i = 0; i < NUM_COUNTERS; i++) {
        fprintf(log_file, ",%s", counters[i].name);
    }
    fprintf(log_file, "\n");
    fflush(log_file);
    
    // Remove old symlink and create new one
    unlink(link_name);
    char target[256];
    snprintf(target, sizeof(target), "pmu_monitor_all_%04d%02d%02d_%02d%02d%02d.log",
             t->tm_year + 1900, t->tm_mon + 1, t->tm_mday,
             t->tm_hour, t->tm_min, t->tm_sec);
    symlink(target, link_name);
    
    setbuf(stdout, NULL);
    setbuf(stderr, NULL);
    
    printf("=== Comprehensive PMU Monitoring ===\n");
    printf("Target PID: %d\n", target_pid == -1 ? getpid() : target_pid);
    printf("Log files:\n");
    printf("  - %s (timestamped)\n", timestamped_filename);
    printf("  - %s (symlink to latest)\n", link_name);
    printf("Opening performance counters...\n\n");
    
    // Initialize dTLB counters
    idx = 0;
    memset(&pe, 0, sizeof(struct perf_event_attr));
    pe.type = PERF_TYPE_HW_CACHE;
    pe.config = (PERF_COUNT_HW_CACHE_DTLB) | (PERF_COUNT_HW_CACHE_OP_READ << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    pe.size = sizeof(struct perf_event_attr);
    pe.disabled = 1;
    active_counters += init_counter(idx++, &pe, target_pid);
    
    pe.config = (PERF_COUNT_HW_CACHE_DTLB) | (PERF_COUNT_HW_CACHE_OP_READ << 8) | (PERF_COUNT_HW_CACHE_RESULT_MISS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);
    
    pe.config = (PERF_COUNT_HW_CACHE_DTLB) | (PERF_COUNT_HW_CACHE_OP_WRITE << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);
    
    pe.config = (PERF_COUNT_HW_CACHE_DTLB) | (PERF_COUNT_HW_CACHE_OP_WRITE << 8) | (PERF_COUNT_HW_CACHE_RESULT_MISS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);
    
    // Initialize iTLB counters
    pe.config = (PERF_COUNT_HW_CACHE_ITLB) | (PERF_COUNT_HW_CACHE_OP_READ << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);
    
    pe.config = (PERF_COUNT_HW_CACHE_ITLB) | (PERF_COUNT_HW_CACHE_OP_READ << 8) | (PERF_COUNT_HW_CACHE_RESULT_MISS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);
    
    // Initialize L1 data cache counters
    pe.config = (PERF_COUNT_HW_CACHE_L1D) | (PERF_COUNT_HW_CACHE_OP_READ << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);
    
    pe.config = (PERF_COUNT_HW_CACHE_L1D) | (PERF_COUNT_HW_CACHE_OP_READ << 8) | (PERF_COUNT_HW_CACHE_RESULT_MISS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);
    
    pe.config = (PERF_COUNT_HW_CACHE_L1D) | (PERF_COUNT_HW_CACHE_OP_WRITE << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);
    
    // Initialize L1 instruction cache counters
    pe.config = (PERF_COUNT_HW_CACHE_L1I) | (PERF_COUNT_HW_CACHE_OP_READ << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);
    
    pe.config = (PERF_COUNT_HW_CACHE_L1I) | (PERF_COUNT_HW_CACHE_OP_READ << 8) | (PERF_COUNT_HW_CACHE_RESULT_MISS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);
    
    // Initialize L1D pending miss counters (raw events)
    pe.type = PERF_TYPE_RAW;
    pe.config = 0x0151;  // l1d.replacement
    pe.exclude_kernel = 0;
    pe.exclude_hv = 0;
    active_counters += init_counter(idx++, &pe, target_pid);
    
    pe.config = 0x0248;  // l1d_pend_miss.fb_full
    active_counters += init_counter(idx++, &pe, target_pid);
    
    pe.config = 0x0148;  // l1d_pend_miss.pending
    active_counters += init_counter(idx++, &pe, target_pid);
    
    // Initialize LLC counters
    pe.type = PERF_TYPE_HW_CACHE;
    pe.exclude_kernel = 0;
    pe.exclude_hv = 0;
    pe.config = (PERF_COUNT_HW_CACHE_LL) | (PERF_COUNT_HW_CACHE_OP_READ << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);
    
    pe.config = (PERF_COUNT_HW_CACHE_LL) | (PERF_COUNT_HW_CACHE_OP_READ << 8) | (PERF_COUNT_HW_CACHE_RESULT_MISS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);
    
    pe.config = (PERF_COUNT_HW_CACHE_LL) | (PERF_COUNT_HW_CACHE_OP_WRITE << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);
    
    pe.config = (PERF_COUNT_HW_CACHE_LL) | (PERF_COUNT_HW_CACHE_OP_WRITE << 8) | (PERF_COUNT_HW_CACHE_RESULT_MISS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);
    
    // Generic cache events
    pe.type = PERF_TYPE_HARDWARE;
    pe.config = PERF_COUNT_HW_CACHE_REFERENCES;
    active_counters += init_counter(idx++, &pe, target_pid);
    
    pe.config = PERF_COUNT_HW_CACHE_MISSES;
    active_counters += init_counter(idx++, &pe, target_pid);
    
    // Memory instruction counters
    pe.type = PERF_TYPE_HW_CACHE;
    pe.config = (PERF_COUNT_HW_CACHE_NODE) | (PERF_COUNT_HW_CACHE_OP_READ << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);
    
    pe.config = (PERF_COUNT_HW_CACHE_NODE) | (PERF_COUNT_HW_CACHE_OP_WRITE << 8) | (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    active_counters += init_counter(idx++, &pe, target_pid);
    
    // Raw memory instruction events
    pe.type = PERF_TYPE_RAW;
    pe.config = 0x81D0;  // mem_inst_retired.all_loads
    active_counters += init_counter(idx++, &pe, target_pid);
    
    pe.config = 0x82D0;  // mem_inst_retired.all_stores
    active_counters += init_counter(idx++, &pe, target_pid);
    
    pe.config = 0x83D0;  // mem_inst_retired.any
    active_counters += init_counter(idx++, &pe, target_pid);
    
    if (active_counters == 0) {
        fprintf(stderr, "\nError: Could not open any performance counters.\n");
        fprintf(stderr, "Your hardware may not support these events.\n");
        return 1;
    }
    
    printf("Successfully opened %d/%zu counters\n\n", active_counters, NUM_COUNTERS);
    printf("Active counters:\n");
    for (size_t i = 0; i < NUM_COUNTERS; i++) {
        if (counters[i].enabled) {
            printf("  ✓ %s\n", counters[i].name);
        }
    }
    printf("\nMonitoring started. Press Ctrl+C to stop\n\n");
    
    // Read and display counters periodically
    while (1) {
        sleep(1);
        
        // Read all counters
        for (size_t i = 0; i < NUM_COUNTERS; i++) {
            if (counters[i].enabled) {
                read(counters[i].fd, &counters[i].count, sizeof(uint64_t));
                ioctl(counters[i].fd, PERF_EVENT_IOC_RESET, 0);
            }
        }
        
        // Get current timestamp
        time_t now = time(NULL);
        char timestamp[64];
        strftime(timestamp, sizeof(timestamp), "%Y-%m-%d %H:%M:%S", localtime(&now));
        
        // Write to log file
        fprintf(log_file, "%s", timestamp);
        for (size_t i = 0; i < NUM_COUNTERS; i++) {
            if (counters[i].enabled) {
                fprintf(log_file, ",%lu", counters[i].count);
            } else {
                fprintf(log_file, ",N/A");
            }
        }
        fprintf(log_file, "\n");
        fflush(log_file);
        
        // Display summary on screen
        printf("\r=== PMU Summary (%s) ===\n", timestamp);
        
        // TLB section
        printf("TLB:\n");
        for (size_t i = 0; i < 6; i++) {
            if (counters[i].enabled) {
                printf("  %-30s: %12lu\n", counters[i].name, counters[i].count);
            }
        }
        
        // Cache section
        printf("\nCache:\n");
        for (size_t i = 6; i < 20; i++) {
            if (counters[i].enabled) {
                printf("  %-30s: %12lu\n", counters[i].name, counters[i].count);
            }
        }
        
        // Memory instructions section
        printf("\nMemory Instructions:\n");
        for (size_t i = 20; i < NUM_COUNTERS; i++) {
            if (counters[i].enabled) {
                printf("  %-30s: %12lu\n", counters[i].name, counters[i].count);
            }
        }
        
        printf("\n");
        fflush(stdout);
        
        // Move cursor up for next update
        int lines_to_clear = 8 + active_counters;
        for (int i = 0; i < lines_to_clear; i++) {
            printf("\033[1A\033[2K");
        }
    }
    
    return 0;
}
