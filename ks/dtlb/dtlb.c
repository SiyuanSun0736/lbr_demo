#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>
#include <unistd.h>
#include <signal.h>
#include <bpf/bpf.h>
#include <bpf/libbpf.h>
#include <stdbool.h>
#include <stdint.h>
#include <inttypes.h>
#include <getopt.h>
#include <fcntl.h>
#include <net/if.h>
#include <setjmp.h>
#include <linux/bpf.h>
#include <sys/resource.h>
#include <pthread.h>

/* TCX attachment constants - defined inline to ensure availability */
#ifndef BPF_TCX_INGRESS
#define BPF_TCX_INGRESS  44
#endif
#ifndef BPF_TCX_EGRESS
#define BPF_TCX_EGRESS   45
#endif

/* Generated from KernelScript IR */
#include "dtlb.skel.h"




struct DtlbMissEvent {
    uint64_t timestamp;
    uint32_t pid;
    uint8_t is_write;
    uint8_t _pad[3];
    uint64_t address;
    uint64_t error_code;
};

struct PerPidStats {
    uint64_t load_misses;
    uint64_t store_misses;
};

struct trace_event_raw_page_fault_user {
    uint32_t ent;
    uint64_t address;
    uint64_t error_code;
    uint64_t ip;
};










/* eBPF skeleton instance */
struct dtlb_ebpf *obj = NULL;


int dtlb_counters_fd = -1;


// Map operations for dtlb_counters
int dtlb_counters_lookup(uint32_t *key, uint64_t *value) {
    return bpf_map_lookup_elem(dtlb_counters_fd, key, value);
}

int dtlb_counters_update(uint32_t *key, uint64_t *value) {
    return bpf_map_update_elem(dtlb_counters_fd, key, value, BPF_ANY);
}

int dtlb_counters_delete(uint32_t *key) {
    return bpf_map_delete_elem(dtlb_counters_fd, key);
}

int dtlb_counters_get_next_key(uint32_t *key, uint32_t *next_key) {
    return bpf_map_get_next_key(dtlb_counters_fd, key, next_key);
}

// Ring buffer event handler for miss_events
static int miss_events_event_handler(void *ctx, void *data, size_t data_sz) {
    struct DtlbMissEvent *event = (struct DtlbMissEvent *)data;
    return handle_miss_event(event);
}
// Combined ring buffer for all ring buffers
static struct ring_buffer *combined_rb = NULL;



// Dispatch function for ring buffer event processing
int dispatch_ring_buffers() {
    int err;
    
    printf("Starting ring buffer event processing...\n");
    
    if (!combined_rb) {
        fprintf(stderr, "Combined ring buffer not initialized\n");
        return -1;
    }
    
    // Poll all ring buffers with a single call
    while (1) {
        err = ring_buffer__poll(combined_rb, 1000);  // 1 second timeout
        if (err < 0 && err != -EINTR) {
            fprintf(stderr, "Error polling combined ring buffer: %d\n", err);
            return err;
        }
    }
    
    return 0;
}





/* BPF Helper Functions (generated only when used) */


// Global attachment storage for tracking active program attachments
struct attachment_entry {
    int prog_fd;
    char target[128];
    uint32_t flags;
    struct bpf_link *link;    // For kprobe/tracepoint programs (NULL for XDP)
    int ifindex;              // For XDP programs (0 for kprobe/tracepoint)
    enum bpf_prog_type type;
    struct attachment_entry *next;
};

static struct attachment_entry *attached_programs = NULL;
static pthread_mutex_t attachment_mutex = PTHREAD_MUTEX_INITIALIZER;

// Helper function to find attachment entry
static struct attachment_entry *find_attachment(int prog_fd) {
    pthread_mutex_lock(&attachment_mutex);
    struct attachment_entry *current = attached_programs;
    while (current) {
        if (current->prog_fd == prog_fd) {
            pthread_mutex_unlock(&attachment_mutex);
            return current;
        }
        current = current->next;
    }
    pthread_mutex_unlock(&attachment_mutex);
    return NULL;
}

// Helper function to remove attachment entry
static void remove_attachment(int prog_fd) {
    pthread_mutex_lock(&attachment_mutex);
    struct attachment_entry **current = &attached_programs;
    while (*current) {
        if ((*current)->prog_fd == prog_fd) {
            struct attachment_entry *to_remove = *current;
            *current = (*current)->next;
            free(to_remove);
            break;
        }
        current = &(*current)->next;
    }
    pthread_mutex_unlock(&attachment_mutex);
}

// Helper function to add attachment entry
static int add_attachment(int prog_fd, const char *target, uint32_t flags, 
                         struct bpf_link *link, int ifindex, enum bpf_prog_type type) {
    struct attachment_entry *entry = malloc(sizeof(struct attachment_entry));
    if (!entry) {
        fprintf(stderr, "Failed to allocate memory for attachment entry\n");
        return -1;
    }
    
    entry->prog_fd = prog_fd;
    strncpy(entry->target, target, sizeof(entry->target) - 1);
    entry->target[sizeof(entry->target) - 1] = '\0';
    entry->flags = flags;
    entry->link = link;
    entry->ifindex = ifindex;
    entry->type = type;
    
    pthread_mutex_lock(&attachment_mutex);
    entry->next = attached_programs;
    attached_programs = entry;
    pthread_mutex_unlock(&attachment_mutex);
    
    return 0;
}


int get_bpf_program_handle(const char *program_name) {
    if (!obj) {
        fprintf(stderr, "eBPF skeleton not loaded - this should not happen with implicit loading\n");
        return -1;
    }
    
    struct bpf_program *prog = bpf_object__find_program_by_name(obj->obj, program_name);
    if (!prog) {
        fprintf(stderr, "Failed to find program '%s' in BPF object\n", program_name);
        return -1;
    }
    
    int prog_fd = bpf_program__fd(prog);
    if (prog_fd < 0) {
        fprintf(stderr, "Failed to get file descriptor for program '%s'\n", program_name);
        return -1;
    }
    

    return prog_fd;
}

int attach_bpf_program_by_fd(int prog_fd, const char *target, int flags) {
    if (prog_fd < 0) {
        fprintf(stderr, "Invalid program file descriptor: %d\n", prog_fd);
        return -1;
    }
    
    // Check if program is already attached
    if (find_attachment(prog_fd)) {
        fprintf(stderr, "Program with fd %d is already attached. Use detach() first.\n", prog_fd);
        return -1;
    }
    
    // Get program type from file descriptor  
    struct bpf_prog_info info = {};
    uint32_t info_len = sizeof(info);
    int ret = bpf_obj_get_info_by_fd(prog_fd, &info, &info_len);
    if (ret) {
        fprintf(stderr, "Failed to get program info: %s\n", strerror(errno));
        return -1;
    }
    
    switch (info.type) {
        case BPF_PROG_TYPE_XDP: {
            int ifindex = if_nametoindex(target);
            if (ifindex == 0) {
                fprintf(stderr, "Failed to get interface index for '%s'\n", target);
                return -1;
            }
            
            // Use modern libbpf API for XDP attachment
            ret = bpf_xdp_attach(ifindex, prog_fd, flags, NULL);
            if (ret) {
                fprintf(stderr, "Failed to attach XDP program to interface '%s': %s\n", target, strerror(errno));
                return -1;
            }
            
            // Store XDP attachment (no bpf_link for XDP)
            if (add_attachment(prog_fd, target, flags, NULL, ifindex, BPF_PROG_TYPE_XDP) != 0) {
                // If storage fails, detach and return error
                bpf_xdp_detach(ifindex, flags, NULL);
                return -1;
            }
            
            printf("XDP attached to interface: %s\n", target);
            return 0;
        }
        case BPF_PROG_TYPE_KPROBE: {
            // For probe programs, target should be the kernel function name (e.g., "sys_read")
            // Use libbpf high-level API for probe attachment
            
            // Get the bpf_program struct from the object and file descriptor
            struct bpf_program *prog = NULL;
            struct bpf_object *obj_iter;

            // Find the program object corresponding to this fd
            // We need to get the program from the skeleton object
            if (!obj) {
                fprintf(stderr, "eBPF skeleton not loaded for probe attachment\n");
                return -1;
            }

            bpf_object__for_each_program(prog, obj->obj) {
                if (bpf_program__fd(prog) == prog_fd) {
                    break;
                }
            }

            if (!prog) {
                fprintf(stderr, "Failed to find bpf_program for fd %d\n", prog_fd);
                return -1;
            }

            // BPF_PROG_TYPE_KPROBE programs always use kprobe attachment
            // (these are generated from @probe("target+offset"))
            struct bpf_link *link = bpf_program__attach_kprobe(prog, false, target);
            if (!link) {
                fprintf(stderr, "Failed to attach kprobe to function '%s': %s\n", target, strerror(errno));
                return -1;
            }
            printf("Kprobe attached to function: %s\n", target);
            
            // Store probe attachment for later cleanup
            if (add_attachment(prog_fd, target, flags, link, 0, BPF_PROG_TYPE_KPROBE) != 0) {
                // If storage fails, destroy link and return error
                bpf_link__destroy(link);
                return -1;
            }
            
            return 0;
        }
        case BPF_PROG_TYPE_TRACING: {
            // For fentry/fexit programs (BPF_PROG_TYPE_TRACING)
            // These are loaded with SEC("fentry/target") or SEC("fexit/target")
            
            // Get the bpf_program struct from the object and file descriptor
            struct bpf_program *prog = NULL;

            // Find the program object corresponding to this fd
            if (!obj) {
                fprintf(stderr, "eBPF skeleton not loaded for tracing program attachment\n");
                return -1;
            }

            bpf_object__for_each_program(prog, obj->obj) {
                if (bpf_program__fd(prog) == prog_fd) {
                    break;
                }
            }

            if (!prog) {
                fprintf(stderr, "Failed to find bpf_program for fd %d\n", prog_fd);
                return -1;
            }

            // For fentry/fexit programs, use bpf_program__attach_trace
            struct bpf_link *link = bpf_program__attach_trace(prog);
            if (!link) {
                fprintf(stderr, "Failed to attach fentry/fexit program to function '%s': %s\n", target, strerror(errno));
                return -1;
            }
            
            printf("Fentry/fexit program attached to function: %s\n", target);
            
            // Store tracing attachment for later cleanup
            if (add_attachment(prog_fd, target, flags, link, 0, BPF_PROG_TYPE_TRACING) != 0) {
                // If storage fails, destroy link and return error
                bpf_link__destroy(link);
                return -1;
            }
            
            return 0;
        }
        case BPF_PROG_TYPE_TRACEPOINT: {
            // For regular tracepoint programs, target should be in "category:event" format (e.g., "sched:sched_switch")
            // Split into category and event name for attachment
            
            // Make a copy of target since we need to modify it
            char target_copy[256];
            strncpy(target_copy, target, sizeof(target_copy) - 1);
            target_copy[sizeof(target_copy) - 1] = '\0';
            
            char *category = target_copy;
            char *event_name = NULL;
            char *colon_pos = strchr(target_copy, ':');
            if (colon_pos) {
                // Null-terminate category and get event name
                *colon_pos = '\0';
                event_name = colon_pos + 1;
            } else {
                fprintf(stderr, "Invalid tracepoint target format: '%s'. Expected 'category:event'\n", target);
                return -1;
            }
            
            // Get the bpf_program struct from the object and file descriptor
            struct bpf_program *prog = NULL;

            // Find the program object corresponding to this fd
            // We need to get the program from the skeleton object
            if (!obj) {
                fprintf(stderr, "eBPF skeleton not loaded for tracepoint attachment\n");
                return -1;
            }

            bpf_object__for_each_program(prog, obj->obj) {
                if (bpf_program__fd(prog) == prog_fd) {
                    break;
                }
            }

            if (!prog) {
                fprintf(stderr, "Failed to find bpf_program for fd %d\n", prog_fd);
                return -1;
            }

            // Use libbpf's high-level tracepoint attachment API with category and event name
            struct bpf_link *link = bpf_program__attach_tracepoint(prog, category, event_name);
            if (!link) {
                fprintf(stderr, "Failed to attach tracepoint to '%s:%s': %s\n", category, event_name, strerror(errno));
                return -1;
            }
            
            // Store tracepoint attachment for later cleanup
            if (add_attachment(prog_fd, target, flags, link, 0, BPF_PROG_TYPE_TRACEPOINT) != 0) {
                // If storage fails, destroy link and return error
                bpf_link__destroy(link);
                return -1;
            }
            
            printf("Tracepoint attached to: %s:%s\n", category, event_name);
            
            return 0;
        }
        case BPF_PROG_TYPE_SCHED_CLS: {
            // For TC (Traffic Control) programs, target should be the interface name (e.g., "eth0")
            
            int ifindex = if_nametoindex(target);
            if (ifindex == 0) {
                fprintf(stderr, "Failed to get interface index for '%s'\n", target);
                return -1;
            }
            
            // Get the bpf_program struct from the object and file descriptor
            struct bpf_program *prog = NULL;

            // Find the program object corresponding to this fd
            if (!obj) {
                fprintf(stderr, "eBPF skeleton not loaded for TC attachment\n");
                return -1;
            }

            bpf_object__for_each_program(prog, obj->obj) {
                if (bpf_program__fd(prog) == prog_fd) {
                    break;
                }
            }

            if (!prog) {
                fprintf(stderr, "Failed to find bpf_program for fd %d\n", prog_fd);
                return -1;
            }

            // Set up TCX options using LIBBPF_OPTS macro
            LIBBPF_OPTS(bpf_tcx_opts, tcx_opts);

            // Use libbpf's TC attachment API
            struct bpf_link *link = bpf_program__attach_tcx(prog, ifindex, &tcx_opts);
            if (!link) {
                fprintf(stderr, "Failed to attach TC program to interface '%s': %s\n", target, strerror(errno));
                return -1;
            }
            
            // Store TC attachment for later cleanup (flags no longer needed for direction)
            if (add_attachment(prog_fd, target, 0, link, ifindex, BPF_PROG_TYPE_SCHED_CLS) != 0) {
                // If storage fails, destroy link and return error
                bpf_link__destroy(link);
                return -1;
            }
            
            printf("TC program attached to interface: %s\n", target);
            
            return 0;
        }
        default:
            fprintf(stderr, "Unsupported program type for attachment: %d\n", info.type);
            return -1;
    }
}

void detach_bpf_program_by_fd(int prog_fd) {
    if (prog_fd < 0) {
        fprintf(stderr, "Invalid program file descriptor: %d\n", prog_fd);
        return;
    }
    
    // Find the attachment entry
    struct attachment_entry *entry = find_attachment(prog_fd);
    if (!entry) {
        fprintf(stderr, "No active attachment found for program fd %d\n", prog_fd);
        return;
    }
    
    // Detach based on program type
    switch (entry->type) {
        case BPF_PROG_TYPE_XDP: {
            int ret = bpf_xdp_detach(entry->ifindex, entry->flags, NULL);
            if (ret) {
                fprintf(stderr, "Failed to detach XDP program from interface: %s\n", strerror(errno));
            } else {
                printf("XDP detached from interface index: %d\n", entry->ifindex);
            }
            break;
        }
        case BPF_PROG_TYPE_KPROBE: {
            if (entry->link) {
                bpf_link__destroy(entry->link);
                printf("Kprobe detached from: %s\n", entry->target);
            } else {
                fprintf(stderr, "Invalid kprobe link for program fd %d\n", prog_fd);
            }
            break;
        }
        case BPF_PROG_TYPE_TRACING: {
            if (entry->link) {
                bpf_link__destroy(entry->link);
                printf("Fentry/fexit program detached from: %s\n", entry->target);
            } else {
                fprintf(stderr, "Invalid tracing program link for program fd %d\n", prog_fd);
            }
            break;
        }
        case BPF_PROG_TYPE_TRACEPOINT: {
            if (entry->link) {
                bpf_link__destroy(entry->link);
                printf("Tracepoint detached from: %s\n", entry->target);
            } else {
                fprintf(stderr, "Invalid tracepoint link for program fd %d\n", prog_fd);
            }
            break;
        }
        case BPF_PROG_TYPE_SCHED_CLS: {
            if (entry->link) {
                bpf_link__destroy(entry->link);
                printf("TC program detached from interface: %s\n", entry->target);
            } else {
                fprintf(stderr, "Invalid TC program link for program fd %d\n", prog_fd);
            }
            break;
        }
        default:
            fprintf(stderr, "Unsupported program type for detachment: %d\n", entry->type);
            break;
    }
    
    // Remove from tracking
    remove_attachment(prog_fd);
}



int32_t handle_miss_event(struct DtlbMissEvent* event) {
    uint32_t __func_call_9;
    uint32_t __func_call_5;
    uint8_t var___arrow_access_0 = event->is_write;
    bool __binop_1 = (__arrow_access_0 == 0);
    if (__binop_1) {
        uint32_t var___arrow_access_2 = event->pid;
        uint64_t var___arrow_access_3 = event->address;
        uint64_t var___arrow_access_4 = event->error_code;
        __func_call_5 = printf("[dTLB load-miss ] pid=%u addr=0x%016llx err=0x%llx\n", __arrow_access_2, __arrow_access_3, __arrow_access_4);
    } else {
        uint32_t var___arrow_access_6 = event->pid;
        uint64_t var___arrow_access_7 = event->address;
        uint64_t var___arrow_access_8 = event->error_code;
        __func_call_9 = printf("[dTLB store-miss] pid=%u addr=0x%016llx err=0x%llx\n", __arrow_access_6, __arrow_access_7, __arrow_access_8);
    }
    return 0;
}

int32_t print_summary(void) {
    uint32_t __func_call_12;
    uint32_t __func_call_15;
    uint32_t __func_call_14;
    uint32_t __func_call_13;
    uint32_t key_1 = 0;
    __map_lookup_10 = bpf_map_lookup_elem(dtlb_counters_fd, &key_1);
    uint64_t var_load_miss = ({ uint64_t __val = {0}; if (__map_lookup_10) { __val = *(__map_lookup_10); } __val; });
    uint32_t key_2 = 1;
    __map_lookup_11 = bpf_map_lookup_elem(dtlb_counters_fd, &key_2);
    uint64_t var_store_miss = ({ uint64_t __val = {0}; if (__map_lookup_11) { __val = *(__map_lookup_11); } __val; });
    __func_call_12 = printf("=== dTLB Summary ===\n");
    __func_call_13 = printf("  dTLB-load-misses  : %llu\n", __map_lookup_10);
    __func_call_14 = printf("  dTLB-store-misses : %llu\n", __map_lookup_11);
    __func_call_15 = printf("====================\n");
    return 0;
}

int main(void) {
    uint32_t __func_call_17;
    int32_t __func_call_25;
    uint32_t __func_call_27;
    uint32_t __func_call_22;
    struct ring_buffer* var_miss_events;
    uint32_t __func_call_18;
    int32_t __func_call_26;
    uint32_t __func_call_23;
    uint32_t __func_call_16;
    uint32_t __func_call_20;
    // No arguments to parse
    // Implicit eBPF skeleton loading - makes global variables immediately accessible
    if (!obj) {
        obj = dtlb_ebpf__open_and_load();
        if (!obj) {
            fprintf(stderr, "Failed to open and load eBPF skeleton\n");
            return 1;
        }
    }

    // Load map dtlb_counters from eBPF object
    struct bpf_map *dtlb_counters_map = bpf_object__find_map_by_name(obj->obj, "dtlb_counters");
    if (!dtlb_counters_map) {
        fprintf(stderr, "Failed to find dtlb_counters map in eBPF object\n");
        return 1;
    }
        // Non-pinned map, just get file descriptor
        dtlb_counters_fd = bpf_map__fd(dtlb_counters_map);
    if (dtlb_counters_fd < 0) {
        fprintf(stderr, "Failed to get fd for dtlb_counters map\n");
        return 1;
    }
    // Get ring buffer map FD for miss_events
    int miss_events_map_fd = bpf_object__find_map_fd_by_name(obj->obj, "miss_events");
    if (miss_events_map_fd < 0) {
        fprintf(stderr, "Failed to find miss_events ring buffer map\n");
        return 1;
    }
    // Create combined ring buffer starting with first ring buffer
    int err;
    combined_rb = ring_buffer__new(miss_events_map_fd, miss_events_event_handler, NULL, NULL);
    if (!combined_rb) {
        fprintf(stderr, "Failed to create combined ring buffer\n");
        return 1;
    }

    // Note: Skeleton loaded implicitly above, load() now gets program handles
    
    __func_call_16 = printf("dTLB Monitoring (KernelScript / eBPF)\n");
    __func_call_17 = printf("  Tracepoint: exceptions/page_fault_user  -> dTLB load/store miss\n");
    __func_call_18 = printf("Press Ctrl+C to stop
\n");
    int32_t var_pf_prog;
    var_pf_prog = get_bpf_program_handle("page_fault_user_handler");
    if (var_pf_prog < 0) {
        fprintf(stderr, "Failed to get BPF program handle\n");
        return 1;
    }
    bool __binop_19 = (var_pf_prog == NULL);
    if (__binop_19) {
        __func_call_20 = printf("ERROR: Failed to load page_fault_user_handler\n");
        return 1;
    }
    uint32_t var_r1;
    var_r1 = attach_bpf_program_by_fd(var_pf_prog, "exceptions:page_fault_user", 0);
    bool __binop_21 = (var_r1 != 0);
    if (__binop_21) {
        __func_call_22 = printf("ERROR: Failed to attach exceptions/page_fault_user (r=%d)\n", var_r1);
        return 1;
    }
    __func_call_23 = printf("Tracepoint attached. Monitoring dTLB events...
\n");
    /* Ring buffer miss_events registered with handler handle_miss_event */
    __func_call_25 = dispatch_ring_buffers();
    __func_call_26 = print_summary();
    detach_bpf_program_by_fd(var_pf_prog);
    __func_call_27 = printf("Detached. Exiting.\n");
    return 0;
}
