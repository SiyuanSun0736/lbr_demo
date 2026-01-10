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

static int fd_loads = -1;
static int fd_load_misses = -1;
static int fd_stores = -1;
static int fd_store_misses = -1;

void cleanup(int sig __attribute__((unused))) {
    if (fd_loads >= 0) close(fd_loads);
    if (fd_load_misses >= 0) close(fd_load_misses);
    if (fd_stores >= 0) close(fd_stores);
    if (fd_store_misses >= 0) close(fd_store_misses);
    printf("\nCleaning up...\n");
    exit(0);
}

int main(int argc, char **argv) {
    struct perf_event_attr pe_loads, pe_load_misses, pe_stores, pe_store_misses;
    uint64_t count_loads = 0, count_load_misses = 0, count_stores = 0, count_store_misses = 0;
    pid_t target_pid = -1;
    int active_counters = 0;
    
    if (argc > 1) {
        target_pid = atoi(argv[1]);
    }
    
    signal(SIGINT, cleanup);
    signal(SIGTERM, cleanup);
    
    // Ensure output is not buffered
    setbuf(stdout, NULL);
    setbuf(stderr, NULL);
    
    printf("dTLB (Data Translation Lookaside Buffer) Monitoring\n");
    printf("Target PID: %d\n", target_pid == -1 ? getpid() : target_pid);
    printf("Opening performance counters...\n\n");
    fflush(stdout);
    
    // Try dTLB-loads
    memset(&pe_loads, 0, sizeof(struct perf_event_attr));
    pe_loads.type = PERF_TYPE_HW_CACHE;
    pe_loads.config = (PERF_COUNT_HW_CACHE_DTLB) | 
                      (PERF_COUNT_HW_CACHE_OP_READ << 8) |
                      (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    pe_loads.size = sizeof(struct perf_event_attr);
    pe_loads.disabled = 1;
    
    fd_loads = try_open_perf_event(&pe_loads, target_pid, "dTLB-loads");
    if (fd_loads >= 0) active_counters++;
    
    // Try dTLB-load-misses
    memset(&pe_load_misses, 0, sizeof(struct perf_event_attr));
    pe_load_misses.type = PERF_TYPE_HW_CACHE;
    pe_load_misses.config = (PERF_COUNT_HW_CACHE_DTLB) | 
                            (PERF_COUNT_HW_CACHE_OP_READ << 8) |
                            (PERF_COUNT_HW_CACHE_RESULT_MISS << 16);
    pe_load_misses.size = sizeof(struct perf_event_attr);
    pe_load_misses.disabled = 1;
    
    fd_load_misses = try_open_perf_event(&pe_load_misses, target_pid, "dTLB-load-misses");
    if (fd_load_misses >= 0) active_counters++;
    
    // Try dTLB-stores
    memset(&pe_stores, 0, sizeof(struct perf_event_attr));
    pe_stores.type = PERF_TYPE_HW_CACHE;
    pe_stores.config = (PERF_COUNT_HW_CACHE_DTLB) | 
                       (PERF_COUNT_HW_CACHE_OP_WRITE << 8) |
                       (PERF_COUNT_HW_CACHE_RESULT_ACCESS << 16);
    pe_stores.size = sizeof(struct perf_event_attr);
    pe_stores.disabled = 1;
    
    fd_stores = try_open_perf_event(&pe_stores, target_pid, "dTLB-stores");
    if (fd_stores >= 0) active_counters++;
    
    // Try dTLB-store-misses
    memset(&pe_store_misses, 0, sizeof(struct perf_event_attr));
    pe_store_misses.type = PERF_TYPE_HW_CACHE;
    pe_store_misses.config = (PERF_COUNT_HW_CACHE_DTLB) | 
                             (PERF_COUNT_HW_CACHE_OP_WRITE << 8) |
                             (PERF_COUNT_HW_CACHE_RESULT_MISS << 16);
    pe_store_misses.size = sizeof(struct perf_event_attr);
    pe_store_misses.disabled = 1;
    
    fd_store_misses = try_open_perf_event(&pe_store_misses, target_pid, "dTLB-store-misses");
    if (fd_store_misses >= 0) active_counters++;
    
    if (active_counters == 0) {
        fprintf(stderr, "\nError: Could not open any performance counters.\n");
        fprintf(stderr, "Your hardware may not support dTLB events.\n");
        fprintf(stderr, "Try running 'perf list cache' to see available cache events.\n");
        return 1;
    }
    
    printf("\nSuccessfully opened %d/%d counters\n", active_counters, 4);
    
    // Enable all opened counters
    if (fd_loads >= 0) ioctl(fd_loads, PERF_EVENT_IOC_ENABLE, 0);
    if (fd_load_misses >= 0) ioctl(fd_load_misses, PERF_EVENT_IOC_ENABLE, 0);
    if (fd_stores >= 0) ioctl(fd_stores, PERF_EVENT_IOC_ENABLE, 0);
    if (fd_store_misses >= 0) ioctl(fd_store_misses, PERF_EVENT_IOC_ENABLE, 0);
    
    printf("Monitoring started. Press Ctrl+C to stop\n\n");
    
    // Read and display counters periodically
    while (1) {
        sleep(1);
        
        if (fd_loads >= 0) {
            read(fd_loads, &count_loads, sizeof(uint64_t));
        }
        if (fd_load_misses >= 0) {
            read(fd_load_misses, &count_load_misses, sizeof(uint64_t));
        }
        if (fd_stores >= 0) {
            read(fd_stores, &count_stores, sizeof(uint64_t));
        }
        if (fd_store_misses >= 0) {
            read(fd_store_misses, &count_store_misses, sizeof(uint64_t));
        }
        
        // Clear previous 2 lines and move cursor up
        printf("\033[2K\r");  // Clear current line
        printf("\033[1A\033[2K");  // Move up and clear line
        
        // Print first line
        if (fd_loads >= 0) printf("dTLB-loads: %-12lu | ", count_loads);
        else printf("dTLB-loads: N/A          | ");
        
        if (fd_load_misses >= 0) printf("dTLB-load-misses: %-12lu\n", count_load_misses);
        else printf("dTLB-load-misses: N/A          \n");
        
        // Print second line
        if (fd_stores >= 0) printf("dTLB-stores: %-12lu | ", count_stores);
        else printf("dTLB-stores: N/A          | ");
        
        if (fd_store_misses >= 0) printf("dTLB-store-misses: %-12lu", count_store_misses);
        else printf("dTLB-store-misses: N/A         ");
        
        fflush(stdout);
    }
    
    return 0;
}
