// SPDX-License-Identifier: GPL-2.0 OR Apache-2.0
/* Copyright 2025 Leon Hwang */

#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include "event.h"

volatile const __u64 FUNC_IP = 0;

static __always_inline int
emit_lbr_event(void *ctx, __u64 session_id)
{
    struct event *evt;
    __u64 pid_tgid;
    __u32 cpu, pid;

    pid_tgid = bpf_get_current_pid_tgid();
    pid = pid_tgid >> 32;
    cpu = bpf_get_smp_processor_id();

    evt = bpf_ringbuf_reserve(&events, sizeof(*evt), 0);
    if (!evt)
        return 0;

    evt->type = 0;  // Event type
    evt->length = sizeof(*evt);
    evt->kernel_ts = (__u32) bpf_ktime_get_ns();
    evt->session_id = session_id;
    evt->func_ip = FUNC_IP;
    evt->cpu = cpu;
    evt->pid = pid;
    bpf_get_current_comm(evt->comm, sizeof(evt->comm));
    evt->func_stack_id = -1;

    bpf_ringbuf_submit(evt, 0);

    return 0;
}
