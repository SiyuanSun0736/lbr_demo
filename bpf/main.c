#include "vmlinux.h"
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_tracing.h>
#include "bpf_map_helpers.h"
#include "bpfsnoop_stack.h"

volatile const __u32 PID = -1;
volatile const __u32 CPU_MASK = 0xFFFF;
volatile const __u64 FUNC_IP = 0;



static __always_inline __u64
gen_session_id(__u64 fp)
{
    __u32 rnd = bpf_get_prandom_u32();

    return ((__u64) rnd) << 32 | (fp & 0xFFFFFFFF);
}

static __always_inline __u64
get_tracee_caller_fp(void *ctx, __u32 args_nr, bool retval)
{
    u64 fp, fp_caller;

    fp = get_tramp_fp(ctx, args_nr, retval); /* read tramp fp */
    (void) bpf_probe_read_kernel(&fp_caller, sizeof(fp_caller), (void *) fp); /* fp of tracee caller */
    return fp_caller;
}


static __always_inline int
emit_bpfsnoop_event(void *ctx)
{
    __u64 fp, session_id = 0;
    fp = get_tracee_caller_fp(ctx, cfg->fn_args.args_nr, cfg->both_entry_exit || cfg->fn_args.with_retval); /* fp of tracee caller */

    return 0;
}