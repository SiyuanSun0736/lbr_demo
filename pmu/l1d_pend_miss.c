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

static int fd_replacement = -1;
static int fd_fb_full = -1;
static int fd_pending = -1;
static FILE *log_file = NULL;

void cleanup(int sig __attribute__((unused))) {
    if (fd_replacement >= 0) close(fd_replacement);
    if (fd_fb_full >= 0) close(fd_fb_full);
    if (fd_pending >= 0) close(fd_pending);
    if (log_file) {
        fclose(log_file);
        printf("\nLog file closed.\n");
    }
    printf("\nCleaning up...\n");
    exit(0);
}

int main(int argc, char **argv) {
    struct perf_event_attr pe;
    uint64_t count_replacement = 0, count_fb_full = 0, count_pending = 0;
    pid_t target_pid = -1;
    int active_counters = 0;
    
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
    char link_name[] = "log/l1d_pend_miss_monitor.log";
    snprintf(timestamped_filename, sizeof(timestamped_filename), 
             "log/l1d_pend_miss_monitor_%04d%02d%02d_%02d%02d%02d.log",
             t->tm_year + 1900, t->tm_mon + 1, t->tm_mday,
             t->tm_hour, t->tm_min, t->tm_sec);
    
    // Open timestamped log file
    log_file = fopen(timestamped_filename, "w");
    if (!log_file) {
        fprintf(stderr, "Failed to open log file: %s\n", strerror(errno));
        return 1;
    }
    
    // Write header to log file
    fprintf(log_file, "Timestamp,l1d.replacement,l1d_pend_miss.fb_full,l1d_pend_miss.pending\n");
    fflush(log_file);
    
    // Remove old symlink and create new one
    unlink(link_name);
    char target[256];
    snprintf(target, sizeof(target), "l1d_pend_miss_monitor_%04d%02d%02d_%02d%02d%02d.log",
             t->tm_year + 1900, t->tm_mon + 1, t->tm_mday,
             t->tm_hour, t->tm_min, t->tm_sec);
    symlink(target, link_name);
    
    printf("L1D Pending Miss Events Monitoring\n");
    printf("Target PID: %d\n", target_pid == -1 ? getpid() : target_pid);
    printf("Log files:\n");
    printf("  - %s (timestamped)\n", timestamped_filename);
    printf("  - %s (symlink to latest)\n", link_name);
    printf("Opening performance counters...\n\n");
    
    // Try l1d.replacement
    memset(&pe, 0, sizeof(struct perf_event_attr));
    pe.type = PERF_TYPE_RAW;
    pe.config = 0x0151;  // l1d.replacement: event=0x51, umask=0x1
    pe.size = sizeof(struct perf_event_attr);
    pe.disabled = 1;
    pe.exclude_kernel = 0;
    pe.exclude_hv = 0;
    
    fd_replacement = try_open_perf_event(&pe, target_pid, "l1d.replacement");
    if (fd_replacement >= 0) active_counters++;
    
    // Try l1d_pend_miss.fb_full
    memset(&pe, 0, sizeof(struct perf_event_attr));
    pe.type = PERF_TYPE_RAW;
    pe.config = 0x0248;  // l1d_pend_miss.fb_full event code
    pe.size = sizeof(struct perf_event_attr);
    pe.disabled = 1;
    pe.exclude_kernel = 0;
    pe.exclude_hv = 0;
    
    fd_fb_full = try_open_perf_event(&pe, target_pid, "l1d_pend_miss.fb_full");
    if (fd_fb_full >= 0) active_counters++;
    
    // Try l1d_pend_miss.pending
    memset(&pe, 0, sizeof(struct perf_event_attr));
    pe.type = PERF_TYPE_RAW;
    pe.config = 0x0148;  // l1d_pend_miss.pending event code
    pe.size = sizeof(struct perf_event_attr);
    pe.disabled = 1;
    pe.exclude_kernel = 0;
    pe.exclude_hv = 0;
    
    fd_pending = try_open_perf_event(&pe, target_pid, "l1d_pend_miss.pending");
    if (fd_pending >= 0) active_counters++;
    
    if (active_counters == 0) {
        fprintf(stderr, "\nError: Could not open any performance counters.\n");
        fprintf(stderr, "Your hardware may not support these L1D events.\n");
        fprintf(stderr, "Try running 'perf list' to see available events.\n");
        fprintf(stderr, "\nNote: These are Intel-specific raw events. Event codes may vary by CPU model.\n");
        return 1;
    }
    
    printf("\nSuccessfully opened %d/%d counters\n", active_counters, 3);
    
    // Enable all opened counters
    if (fd_replacement >= 0) ioctl(fd_replacement, PERF_EVENT_IOC_ENABLE, 0);
    if (fd_fb_full >= 0) ioctl(fd_fb_full, PERF_EVENT_IOC_ENABLE, 0);
    if (fd_pending >= 0) ioctl(fd_pending, PERF_EVENT_IOC_ENABLE, 0);
    
    printf("Monitoring started. Press Ctrl+C to stop\n\n");
    
    // Read and display counters periodically
    while (1) {
        sleep(1);
        
        if (fd_replacement >= 0) {
            read(fd_replacement, &count_replacement, sizeof(uint64_t));
            ioctl(fd_replacement, PERF_EVENT_IOC_RESET, 0);
        }
        if (fd_fb_full >= 0) {
            read(fd_fb_full, &count_fb_full, sizeof(uint64_t));
            ioctl(fd_fb_full, PERF_EVENT_IOC_RESET, 0);
        }
        if (fd_pending >= 0) {
            read(fd_pending, &count_pending, sizeof(uint64_t));
            ioctl(fd_pending, PERF_EVENT_IOC_RESET, 0);
        }
        
        // Get current timestamp
        time_t now = time(NULL);
        char timestamp[64];
        strftime(timestamp, sizeof(timestamp), "%Y-%m-%d %H:%M:%S", localtime(&now));
        
        // Write to log file
        fprintf(log_file, "%s,%lu,%lu,%lu\n", timestamp, count_replacement, count_fb_full, count_pending);
        fflush(log_file);
        
        printf("\r");
        if (fd_replacement >= 0) printf("l1d.replacement: %-12lu | ", count_replacement);
        else printf("l1d.replacement: N/A          | ");
        
        if (fd_fb_full >= 0) printf("fb_full: %-12lu | ", count_fb_full);
        else printf("fb_full: N/A          | ");
        
        if (fd_pending >= 0) printf("pending: %-12lu", count_pending);
        else printf("pending: N/A         ");
        
        fflush(stdout);
    }
    
    return 0;
}
