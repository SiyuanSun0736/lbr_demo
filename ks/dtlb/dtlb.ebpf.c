#include "vmlinux.h"
#include <bpf/bpf_helpers.h>

/* eBPF Dynptr API integration for enhanced pointer safety */
/* Using system-provided bpf_dynptr_* helper functions from bpf_helpers.h */

/* Enhanced dynptr safety macros */
#define DYNPTR_SAFE_ACCESS(dynptr, offset, size, type) \
    ({ \
        type *__ptr = (type*)bpf_dynptr_data(dynptr, offset, sizeof(type)); \
        __ptr ? *__ptr : (type){0}; \
    })

#define DYNPTR_SAFE_WRITE(dynptr, offset, value, type) \
    ({ \
        type __tmp = (value); \
        bpf_dynptr_write(dynptr, offset, &__tmp, sizeof(type), 0); \
    })

#define DYNPTR_SAFE_READ(dst, dynptr, offset, type) \
    bpf_dynptr_read(dst, sizeof(type), dynptr, offset, 0)

/* Fallback macros for regular pointer operations */
#define SAFE_DEREF(ptr) \
    ({ \
        typeof(*ptr) __val = {0}; \
        if (ptr) { \
            __builtin_memcpy(&__val, ptr, sizeof(__val)); \
        } \
        __val; \
    })

#define SAFE_PTR_ACCESS(ptr, field) \
    ({ \
        typeof((ptr)->field) __val = {0}; \
        if (ptr) { \
            __val = (ptr)->field; \
        } \
        __val; \
    })

/* Global variables */
/* Ring buffer for miss_events */
struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 65536);
} miss_events SEC(".maps");


struct DtlbMissEvent {
    __u64 timestamp;
    __u32 pid;
    __u8 is_write;
    __u8 _pad[3];
    __u64 address;
    __u64 error_code;
};

struct {
    __uint(type, BPF_MAP_TYPE_ARRAY);
    __uint(max_entries, 2);
    __type(key, __u32);
    __type(value, __u64);
} dtlb_counters SEC(".maps");

struct PerPidStats {
    __u64 load_misses;
    __u64 store_misses;
};

struct {
    __uint(type, BPF_MAP_TYPE_HASH);
    __uint(max_entries, 1024);
    __type(key, __u32);
    __type(value, struct PerPidStats);
} per_pid_stats SEC(".maps");

struct trace_event_raw_page_fault_user {
    __u32 ent;
    __u64 address;
    __u64 error_code;
    __u64 ip;
};

SEC("tracepoint/exceptions/page_fault_user")
__s32 page_fault_user_handler(struct trace_event_raw_page_fault_user* ctx) {
    __u8 __binop_10;
    struct DtlbMissEvent* __ringbuf_reserve_9;
    __u64 __binop_8;
    __u64* __map_lookup_7;
    __u64 __binop_6;
    __u64* __map_lookup_5;
    __u8 __binop_4;
    __u8 __binop_3;
    __u64 __arrow_access_1;
    __u64 __arrow_access_0;
    __arrow_access_0 = ({ typeof((ctx)->error_code) __field_val = {0}; if (ctx && (void*)ctx >= (void*)0x1000) { __field_val = (ctx)->error_code; } __field_val; });
    __u64 error_code = __arrow_access_0;
    __arrow_access_1 = ({ typeof((ctx)->address) __field_val = {0}; if (ctx && (void*)ctx >= (void*)0x1000) { __field_val = (ctx)->address; } __field_val; });
    __u64 address = __arrow_access_1;
    __u64 mask = 2;
    __u64 write_flag = error_code;
    __u64* __unop_2 = (&mask);
    __u8 is_write = 0;
    __binop_3 = (write_flag != 0);
    if (__binop_3) {
        is_write = 1;
    }
    __binop_4 = (is_write == 0);
    if (__binop_4) {
        __u32 key_1 = 0;
        __map_lookup_5 = bpf_map_lookup_elem(&dtlb_counters, &key_1);
        __binop_6 = (({ __u64 __val = {0}; if (__map_lookup_5) { __val = *(__map_lookup_5); } __val; }) + 1);
        __u32 key_2 = 0;
        bpf_map_update_elem(&dtlb_counters, &key_2, &__binop_6, BPF_ANY);
    } else {
        __u32 key_3 = 1;
        __map_lookup_7 = bpf_map_lookup_elem(&dtlb_counters, &key_3);
        __binop_8 = (({ __u64 __val = {0}; if (__map_lookup_7) { __val = *(__map_lookup_7); } __val; }) + 1);
        __u32 key_4 = 1;
        bpf_map_update_elem(&dtlb_counters, &key_4, &__binop_8, BPF_ANY);
    }
    struct DtlbMissEvent* reserved;
    struct bpf_dynptr __ringbuf_reserve_9_dynptr;
    if (bpf_ringbuf_reserve_dynptr(&miss_events, sizeof(struct DtlbMissEvent), 0, &__ringbuf_reserve_9_dynptr) == 0) {
        __ringbuf_reserve_9 = bpf_dynptr_data(&__ringbuf_reserve_9_dynptr, 0, sizeof(struct DtlbMissEvent));
    } else {
        __ringbuf_reserve_9 = NULL;
    }
    reserved = __ringbuf_reserve_9;
    __binop_10 = (reserved != NULL);
    if (__binop_10) {
        DYNPTR_SAFE_WRITE(&__ringbuf_reserve_9_dynptr, __builtin_offsetof(struct DtlbMissEvent, timestamp), 0, __u32);
        DYNPTR_SAFE_WRITE(&__ringbuf_reserve_9_dynptr, __builtin_offsetof(struct DtlbMissEvent, pid), 0, __u32);
        DYNPTR_SAFE_WRITE(&__ringbuf_reserve_9_dynptr, __builtin_offsetof(struct DtlbMissEvent, is_write), is_write, __u8);
        DYNPTR_SAFE_WRITE(&__ringbuf_reserve_9_dynptr, __builtin_offsetof(struct DtlbMissEvent, address), address, __u64);
        DYNPTR_SAFE_WRITE(&__ringbuf_reserve_9_dynptr, __builtin_offsetof(struct DtlbMissEvent, error_code), error_code, __u64);
        if (reserved) bpf_ringbuf_submit_dynptr(&__ringbuf_reserve_9_dynptr, 0);
    }
    return 0;
}

char _license[] SEC("license") = "GPL";