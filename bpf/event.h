// SPDX-License-Identifier: GPL-2.0 OR Apache-2.0
/* Copyright 2025 Leon Hwang */

#ifndef __EVENT_H_
#define __EVENT_H_

#include "vmlinux.h"


struct event {
    __u16 type;
    __u16 length;
    __u32 kernel_ts;
    __u64 session_id;
    __u64 func_ip;
    __u32 cpu;
    __u32 pid;
    __u8 comm[16];
    __s64 func_stack_id;
};

struct {
    __uint(type, BPF_MAP_TYPE_RINGBUF);
    __uint(max_entries, 64 * 1024 * 1024); /* 64MiB */
} events SEC(".maps");

#endif // __EVENT_H_
