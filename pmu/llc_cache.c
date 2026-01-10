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

static int perf_event_open(struct perf_event_attr *hw_event, pid_t pid,
                           int cpu, int group_fd, unsigned long flags) {
    int ret = syscall(__NR_perf_event_open, hw_event, pid, cpu, group_fd, flags);
    return ret;
}

static int try_open_perf_event(struct perf_event_attr *pe, pid_t pid, const char *name) {
    int fd = perf_event_open(pe, pid, -1, -1, 0);
    if (fd == -1) {
        fprintf(stderr, "Failed to open %s (pid=%d, cpu=-1): %s (errno=%d)\n", 
                name, pid, strerror(errno), errno);
        
        // Try with CPU 0 instead
        fd = perf_event_open(pe, pid, 0, -1, 0);
        if (fd == -1) {
            fprintf(stderr, "Failed to open %s (pid=%d, cpu=0): %s (errno=%d)\n", 
                    name, pid, strerror(errno), errno);
            return -1;
        }
        printf("Successfully opened %s on CPU 0 (fd=%d)\n", name, fd);
        return fd;
    }
    printf("Successfully opened %s on all CPUs (fd=%d)\n", name, fd);
    return fd;
}

static int fd_llc_loads = -1;
static int fd_llc_load_misses = -1;
static int fd_llc_stores = -1;
static int fd_llc_store_misses = -1;
static int fd_cache_references = -1;
static int fd_cache_misses = -1;

void cleanup(int sig __attribute__((unused))) {
    if (fd_llc_loads >= 0) close(fd_llc_loads);
    if (fd_llc_load_misses >= 0) close(fd_llc_load_misses);
    if (fd_llc_stores >= 0) close(fd_llc_stores);
    if (fd_llc_store_misses >= 0) close(fd_llc_store_misses);
    if (fd_cache_references >= 0) close(fd_cache_references);
    if (fd_cache_misses >= 0) close(fd_cache_misses);
    printf("\nCleaning up...\n");
    exit(0);
}

int main(int argc, char **argv) {
    struct perf_event_attr pe;
    uint64_t count_llc_loads = 0, count_llc_load_misses = 0;
    uint64_t count_llc_stores = 0, count_llc_store_misses = 0;
    uint64_t count_cache_references = 0, count_cache_misses = 0;
    pid_t target_pid = -1;
    int active_counters = 0;
    
    if (argc > 1) {
        target_pid = atoi(argv[1]);
    }
    
    signal(SIGINT, cleanup);
    signal(SIGTERM, cleanup);
    
    printf("LLC (Last Level Cache) Monitoring\n");
    printf("Target PID: %d\n", target_pid == -1 ? getpid() : target_pid);
    printf("Opening performance counters...\n\n");
    
    // Try LLC-loads
    memset(&pe, 0, sizeof(struct perf_event_attr));
    pe.type = PERF_TYPE_HW_CACHE;
    pe.config = (PERF_COUNT_HW_CACHE_LL) | 
                (PERF_COUNT_HW_CACHE_OP_READ << 8) |
                (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    pe.size = sizeof(struct perf_event_attr);
    pe.disabled = 1;
    
    fd_llc_loads = try_open_perf_event(&pe, target_pid, "LLC-loads");
    if (fd_llc_loads >= 0) active_counters++;
    
    // Try LLC-load-misses
    memset(&pe, 0, sizeof(struct perf_event_attr));
    pe.type = PERF_TYPE_HW_CACHE;
    pe.config = (PERF_COUNT_HW_CACHE_LL) | 
                (PERF_COUNT_HW_CACHE_OP_READ << 8) |
                (PERF_COUNT_HW_CACHE_RESULT_MISS << 16);
    pe.size = sizeof(struct perf_event_attr);
    pe.disabled = 1;
    
    fd_llc_load_misses = try_open_perf_event(&pe, target_pid, "LLC-load-misses");
    if (fd_llc_load_misses >= 0) active_counters++;
    
    // Try LLC-stores
    memset(&pe, 0, sizeof(struct perf_event_attr));
    pe.type = PERF_TYPE_HW_CACHE;
    pe.config = (PERF_COUNT_HW_CACHE_LL) | 
                (PERF_COUNT_HW_CACHE_OP_WRITE << 8) |
                (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    pe.size = sizeof(struct perf_event_attr);
    pe.disabled = 1;
    
    fd_llc_stores = try_open_perf_event(&pe, target_pid, "LLC-stores");
    if (fd_llc_stores >= 0) active_counters++;
    
    // Try LLC-store-misses
    memset(&pe, 0, sizeof(struct perf_event_attr));
    pe.type = PERF_TYPE_HW_CACHE;
    pe.config = (PERF_COUNT_HW_CACHE_LL) | 
                (PERF_COUNT_HW_CACHE_OP_WRITE << 8) |
                (PERF_COUNT_HW_CACHE_RESULT_MISS << 16);
    pe.size = sizeof(struct perf_event_attr);
    pe.disabled = 1;
    
    fd_llc_store_misses = try_open_perf_event(&pe, target_pid, "LLC-store-misses");
    if (fd_llc_store_misses >= 0) active_counters++;
    
    // Try cache-references (generic hardware event)
    memset(&pe, 0, sizeof(struct perf_event_attr));
    pe.type = PERF_TYPE_HARDWARE;
    pe.config = PERF_COUNT_HW_CACHE_REFERENCES;
    pe.size = sizeof(struct perf_event_attr);
    pe.disabled = 1;
    
    fd_cache_references = try_open_perf_event(&pe, target_pid, "cache-references");
    if (fd_cache_references >= 0) active_counters++;
    
    // Try cache-misses (generic hardware event)
    memset(&pe, 0, sizeof(struct perf_event_attr));
    pe.type = PERF_TYPE_HARDWARE;
    pe.config = PERF_COUNT_HW_CACHE_MISSES;
    pe.size = sizeof(struct perf_event_attr);
    pe.disabled = 1;
    
    fd_cache_misses = try_open_perf_event(&pe, target_pid, "cache-misses");
    if (fd_cache_misses >= 0) active_counters++;
    
    if (active_counters == 0) {
        fprintf(stderr, "\nError: Could not open any performance counters.\n");
        fprintf(stderr, "Your hardware may not support LLC cache events.\n");
        fprintf(stderr, "Try running 'perf list cache' to see available cache events.\n");
        return 1;
    }
    
    printf("\nSuccessfully opened %d/%d counters\n", active_counters, 6);
    
    // Enable all opened counters
    if (fd_llc_loads >= 0) ioctl(fd_llc_loads, PERF_EVENT_IOC_ENABLE, 0);
    if (fd_llc_load_misses >= 0) ioctl(fd_llc_load_misses, PERF_EVENT_IOC_ENABLE, 0);
    if (fd_llc_stores >= 0) ioctl(fd_llc_stores, PERF_EVENT_IOC_ENABLE, 0);
    if (fd_llc_store_misses >= 0) ioctl(fd_llc_store_misses, PERF_EVENT_IOC_ENABLE, 0);
    if (fd_cache_references >= 0) ioctl(fd_cache_references, PERF_EVENT_IOC_ENABLE, 0);
    if (fd_cache_misses >= 0) ioctl(fd_cache_misses, PERF_EVENT_IOC_ENABLE, 0);
    
    printf("Monitoring started. Press Ctrl+C to stop\n\n");
    
    // Read and display counters periodically
    while (1) {
        sleep(1);
        
        if (fd_llc_loads >= 0) {
            read(fd_llc_loads, &count_llc_loads, sizeof(uint64_t));
        }
        if (fd_llc_load_misses >= 0) {
            read(fd_llc_load_misses, &count_llc_load_misses, sizeof(uint64_t));
        }
        if (fd_llc_stores >= 0) {
            read(fd_llc_stores, &count_llc_stores, sizeof(uint64_t));
        }
        if (fd_llc_store_misses >= 0) {
            read(fd_llc_store_misses, &count_llc_store_misses, sizeof(uint64_t));
        }
        if (fd_cache_references >= 0) {
            read(fd_cache_references, &count_cache_references, sizeof(uint64_t));
        }
        if (fd_cache_misses >= 0) {
            read(fd_cache_misses, &count_cache_misses, sizeof(uint64_t));
        }
        
        printf("\r");
        
        // First line: LLC specific events
        if (fd_llc_loads >= 0) printf("LLC-loads: %-12lu | ", count_llc_loads);
        else printf("LLC-loads: N/A          | ");
        
        if (fd_llc_load_misses >= 0) printf("LLC-load-misses: %-12lu | ", count_llc_load_misses);
        else printf("LLC-load-misses: N/A          | ");
        
        if (fd_llc_stores >= 0) printf("LLC-stores: %-12lu | ", count_llc_stores);
        else printf("LLC-stores: N/A          | ");
        
        if (fd_llc_store_misses >= 0) printf("LLC-store-misses: %-12lu\n", count_llc_store_misses);
        else printf("LLC-store-misses: N/A         \n");
        
        // Second line: Generic cache events
        printf("   ");
        if (fd_cache_references >= 0) printf("cache-refs: %-12lu | ", count_cache_references);
        else printf("cache-refs: N/A          | ");
        
        if (fd_cache_misses >= 0) {
            double miss_rate = 0.0;
            if (count_cache_references > 0) {
                miss_rate = (double)count_cache_misses / count_cache_references * 100.0;
            }
            printf("cache-misses: %-12lu (%.2f%%)", count_cache_misses, miss_rate);
        } else {
            printf("cache-misses: N/A                    ");
        }
        
        // Move cursor up one line for next update
        printf("\033[1A");
        fflush(stdout);
    }
    
    return 0;
}
