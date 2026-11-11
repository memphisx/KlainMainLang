/* timers.c — Node's timers binding (src/timers.cc), TDD-00245.
 *
 * The timers themselves are lib/node/kml_timers.ts (Node's lib/timers.js
 * and lib/internal/timers.js): Timeout and Immediate objects, lists of
 * timers sharing a duration, a priority queue of the lists. This file is
 * what those call into: one timer armed for the earliest list, whose firing
 * runs processTimers(now); a check handle that runs processImmediate after
 * the poll while immediates are pending; an idle handle that keeps the poll
 * from blocking while a referenced immediate is pending; and the loop's
 * clock in milliseconds.
 *
 * They are libuv handles on the thread's loop (loop.c), on Windows too
 * (TDD-00252). */
#include <stdlib.h>
#include <string.h>
#include <stdint.h>

#ifdef _WIN32
/* the shim's clock_gettime takes { i64 sec, i64 nsec } */
typedef struct { long long tv_sec, tv_nsec; } kml_ts;
int clock_gettime(int clk, kml_ts *ts);
#define MONO_CLK 1
#else
#include <time.h>
typedef struct timespec kml_ts;
#ifdef __APPLE__
#define MONO_CLK CLOCK_UPTIME_RAW
#else
#define MONO_CLK CLOCK_MONOTONIC
#endif
#endif

typedef long long i64;
typedef void (*kml_inv2)(void *clo, double a, double b);
void __kml_drain_microtasks(void);
_Bool __kml_make_callback(kml_inv2 inv, void *clo, double a, double b);

i64 __kml_monotonic_ns(void) {
    kml_ts ts;
    clock_gettime(MONO_CLK, &ts);
    return (i64)ts.tv_sec * 1000000000LL + (i64)ts.tv_nsec;
}

/* processTimers and processImmediate, as (invoke, closure) pairs. */
static _Thread_local void *pt_inv, *pt_clo, *pi_inv, *pi_clo;
/* processTimers' answer (timersNext): the next expiry, positive when that
 * list is referenced, negative when not, 0 for none. */
static _Thread_local double next_expiry;
/* immediateInfo[kCount]: the pending immediates. */
static _Thread_local double imm_count;
/* The armed timer's deadline on the monotonic clock, 0 when none. */
static _Thread_local i64 due_ns;
static _Thread_local int timer_refed, imm_refed;

#include <uv.h>
uv_loop_t *__kml_loop_uv(void);
void *__kml_loop_raw_alloc(size_t n);

static _Thread_local uv_timer_t *timer;
static _Thread_local uv_check_t *check;
static _Thread_local uv_idle_t *idle;
/* The loop's clock when the binding was set up: Node's timer_base. */
static _Thread_local uint64_t base;

static double now_ms(void) {
    uv_loop_t *l = __kml_loop_uv();
    uv_update_time(l);
    return (double)(uv_now(l) - base);
}

static void on_timer(uv_timer_t *h);

static void arm(double ms) {
    uint64_t d = ms < 1 ? 1 : (uint64_t)ms;
    uv_timer_start(timer, on_timer, d, 0);
    due_ns = __kml_monotonic_ns() + (i64)d * 1000000;
}

static void set_timer_ref(int on) {
    timer_refed = on;
    if (on)
        uv_ref((uv_handle_t *)timer);
    else
        uv_unref((uv_handle_t *)timer);
}

/* Node's RunTimers: processTimers(now), then the timer re-armed for the
 * next expiry it answers, referenced or not as that list is; the microtasks
 * run last, as Node's callback scope closes, so a timer they schedule keeps
 * its own arming and reference. */
static void run_timers(void) {
    due_ns = 0;
    /* A throw a listener handled leaves the rest of the list due: Node
     * calls processTimers again. */
    do next_expiry = 0;
    while (__kml_make_callback((kml_inv2)pt_inv, pt_clo, now_ms(), 0));
    double r = next_expiry;
    if (r != 0) {
        double left = (r < 0 ? -r : r) - now_ms();
        arm(left > 0 ? left : 1);
        set_timer_ref(r > 0);
    } else {
        set_timer_ref(0);
    }
    __kml_drain_microtasks();
}

/* Node's CheckImmediate: processImmediate while any immediate is pending. */
static void run_immediates(void) {
    if (imm_count == 0)
        return;
    while (__kml_make_callback((kml_inv2)pi_inv, pi_clo, 0, 0) && imm_count != 0)
        ;
    __kml_drain_microtasks();
}

static void on_timer(uv_timer_t *h) {
    (void)h;
    run_timers();
}

static void on_check(uv_check_t *h) {
    (void)h;
    run_immediates();
}

static void on_idle(uv_idle_t *h) { (void)h; }

/* The natives lib/native.d.ts declares (`@link timers`). Numbers cross as
 * doubles; each callback as an (invoke, closure) pair. */
void __kml_native_timers_setup(void *ti, void *tc, void *ii, void *ic) {
    pt_inv = ti;
    pt_clo = tc;
    pi_inv = ii;
    pi_clo = ic;
    if (timer)
        return;
    uv_loop_t *l = __kml_loop_uv();
    uv_update_time(l);
    base = uv_now(l);
    timer = (uv_timer_t *)__kml_loop_raw_alloc(sizeof *timer);
    uv_timer_init(l, timer);
    uv_unref((uv_handle_t *)timer);
    check = (uv_check_t *)__kml_loop_raw_alloc(sizeof *check);
    uv_check_init(l, check);
    uv_check_start(check, on_check);
    uv_unref((uv_handle_t *)check);
    idle = (uv_idle_t *)__kml_loop_raw_alloc(sizeof *idle);
    uv_idle_init(l, idle);
}

double __kml_native_timers_now(void) { return now_ms(); }

void __kml_native_timers_schedule(double ms) { arm(ms); }

void __kml_native_timers_toggle_ref(double on) { set_timer_ref(on != 0); }

void __kml_native_timers_next(double next) { next_expiry = next; }

void __kml_native_immediate_toggle_ref(double on) {
    imm_refed = on != 0;
    if (imm_refed)
        uv_idle_start(idle, on_idle);
    else
        uv_idle_stop(idle);
}

void __kml_native_immediate_count(double n) { imm_count = n; }

/* process.getActiveResourcesInfo's handles: each referenced open handle
 * of the loop by the name of Node's wrap for it, as Node lists its
 * referenced handle wraps. The runtime's own timer,
 * phase and wakeup handles stand for no wrap. */
static char active_buf[4096];
static size_t active_len;
static void active_note(uv_handle_t *h, void *arg) {
    (void)arg;
    if (!uv_has_ref(h) || uv_is_closing(h)) return; /* (Node's: refed, open) */
    const char *n = 0;
    struct sockaddr_storage ss;
    int sl = sizeof ss;
    char pb[8];
    size_t pl = sizeof pb;
    switch (h->type) {
    case UV_TCP: n = uv_tcp_getpeername((uv_tcp_t *)h, (struct sockaddr *)&ss, &sl) == 0 ? "TCPSocketWrap" : "TCPServerWrap"; break;
    case UV_NAMED_PIPE: n = uv_pipe_getpeername((uv_pipe_t *)h, pb, &pl) != UV_ENOTCONN ? "PipeWrap" : "PipeServerWrap"; break;
    case UV_TTY: n = "TTYWrap"; break;
    case UV_UDP: n = "UDPWrap"; break;
    case UV_PROCESS: n = "ProcessWrap"; break;
    case UV_SIGNAL: n = "SignalWrap"; break;
    case UV_FS_EVENT: n = "FSEventWrap"; break;
    case UV_FS_POLL: n = "StatWatcher"; break;
    default: return;
    }
    size_t k = strlen(n);
    if (active_len + k + 2 >= sizeof active_buf) return;
    if (active_len) active_buf[active_len++] = ',';
    memcpy(active_buf + active_len, n, k);
    active_len += k;
}

char *__kml_native_active_handles(void) {
    active_len = 0;
    uv_walk(__kml_loop_uv(), active_note, 0);
    char *base = (char *)kml_alloc(KML_HEAP_RAW, active_len + 9);
    *(int64_t *)base = (int64_t)active_len;
    memcpy(base + 8, active_buf, active_len);
    base[8 + active_len] = 0;
    return base + 8;
}

/* The armed timer's deadline (0 when none): a dynamic-import island folds
 * it into its wait. */
i64 __kml_timer_next_deadline_ns(void) { return due_ns; }

