/* timers.c — setTimeout/setInterval and their drains (in C per TDD-00240).
 * One queue per isolate (thread): an array of 32-byte entries
 * {id, fireAtNs, intervalMs, closure}. intervalMs is 0 for a one-shot, the
 * cadence for setInterval, -1 for cancelled / done (never compacted). Bit
 * 62 of an id marks the entry unref'd: it still fires but does not keep the
 * loop alive. The queue globals are read by generated loop code. */
#include <stdlib.h>

#ifdef _WIN32
/* the shim's nanosleep/clock_gettime take { i64 sec, i64 nsec } */
typedef struct { long long tv_sec, tv_nsec; } kml_ts;
int clock_gettime(int clk, kml_ts *ts);
int nanosleep(const kml_ts *req, kml_ts *rem);
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
typedef struct { i64 id, fire, interval; void *closure; } kml_timer;
typedef struct { void (*fn)(void *); void *env; } kml_closure;

extern void __kml_signal_dispatch(void);

_Thread_local kml_timer *__kml_timer_data;
_Thread_local i64 __kml_timer_len;
_Thread_local i64 __kml_timer_cap;
_Thread_local i64 __kml_timer_next_id = 1;
/* Each entry's delay, by index, for Timeout.refresh(); -1 once cleared. */
static _Thread_local i64 *timer_delay;

#define ID_MASK 4611686018427387903LL
#define UNREF_BIT 4611686018427387904LL

i64 __kml_monotonic_ns(void) {
    kml_ts ts;
    clock_gettime(MONO_CLK, &ts);
    return (i64)ts.tv_sec * 1000000000LL + (i64)ts.tv_nsec;
}

i64 __kml_timer_schedule(void *closure, i64 delayms, i64 intervalms) {
    if (__kml_timer_len + 1 > __kml_timer_cap) {
        i64 nc = __kml_timer_cap * 2 > 8 ? __kml_timer_cap * 2 : 8;
        __kml_timer_data = (kml_timer *)realloc(__kml_timer_data, (size_t)nc * sizeof(kml_timer));
        __kml_timer_cap = nc;
        timer_delay = (i64 *)realloc(timer_delay, (size_t)nc * sizeof(i64));
    }
    timer_delay[__kml_timer_len] = delayms;
    kml_timer *t = &__kml_timer_data[__kml_timer_len++];
    t->id = __kml_timer_next_id++;
    t->fire = __kml_monotonic_ns() + delayms * 1000000;
    t->interval = intervalms;
    t->closure = closure;
    return t->id;
}

void __kml_timer_clear(i64 id) {
    for (i64 i = 0; i < __kml_timer_len; i++)
        if ((__kml_timer_data[i].id & ID_MASK) == id) {
            __kml_timer_data[i].interval = -1;
            timer_delay[i] = -1;
            return;
        }
}

/* Timeout.unref()/ref()/hasRef(). The i1 arrives as a bare int. */
void __kml_timer_set_ref(i64 id, int on) {
    for (i64 i = 0; i < __kml_timer_len; i++) {
        i64 eid = __kml_timer_data[i].id & ID_MASK;
        if (eid == id) {
            __kml_timer_data[i].id = (on & 1) ? eid : (eid | UNREF_BIT);
            return;
        }
    }
}

_Bool __kml_timer_has_ref(i64 id) {
    for (i64 i = 0; i < __kml_timer_len; i++)
        if ((__kml_timer_data[i].id & ID_MASK) == id)
            return (__kml_timer_data[i].id & UNREF_BIT) == 0;
    return 1;
}

/* Timeout.refresh(): restart the countdown from now with the original
 * delay. A one-shot that already fired is re-armed, as in Node; a cleared
 * timer stays cleared. */
void __kml_timer_refresh(i64 id) {
    for (i64 i = 0; i < __kml_timer_len; i++)
        if ((__kml_timer_data[i].id & ID_MASK) == id) {
            if (timer_delay[i] < 0) return;
            kml_timer *t = &__kml_timer_data[i];
            if (t->interval == -1) t->interval = 0;
            t->fire = __kml_monotonic_ns() + timer_delay[i] * 1000000;
            return;
        }
}

_Bool __kml_timer_any_ref(void) {
    for (i64 i = 0; i < __kml_timer_len; i++)
        if (__kml_timer_data[i].interval != -1 && (__kml_timer_data[i].id & UNREF_BIT) == 0)
            return 1;
    return 0;
}

/* The pending entry with the smallest fire time (-1 when none). */
static i64 find_best(i64 *bestfire) {
    i64 best = -1;
    for (i64 i = 0; i < __kml_timer_len; i++) {
        kml_timer *t = &__kml_timer_data[i];
        if (t->interval == -1) continue;
        if (best == -1 || t->fire < *bestfire) {
            best = i;
            *bestfire = t->fire;
        }
    }
    return best;
}

/* Run entry idx's callback, then — it may have scheduled or cleared timers
 * and moved the queue — reload and reschedule a repeating one or retire a
 * one-shot. */
static void fire(i64 idx) {
    kml_closure *c = (kml_closure *)__kml_timer_data[idx].closure;
    c->fn(c->env);
    kml_timer *t = &__kml_timer_data[idx];
    if (t->interval > 0)
        t->fire = __kml_monotonic_ns() + t->interval * 1000000;
    else
        t->interval = -1;
}

static int sleep_until(i64 fire_at, i64 now) {
    i64 wait = fire_at - now;
    kml_ts ts;
    ts.tv_sec = wait / 1000000000LL;
    ts.tv_nsec = wait % 1000000000LL;
    return nanosleep(&ts, NULL);
}

/* One step: fire the earliest pending timer after sleeping until it is due;
 * 0 when none is pending (TDD-00087). A signal interrupting the sleep leaves
 * dispatch to the caller. */
_Bool __kml_timer_fire_next(void) {
    i64 tf = 0;
    i64 best = find_best(&tf);
    if (best == -1) return 0;
    i64 now = __kml_monotonic_ns();
    if (tf > now) sleep_until(tf, now);
    if (tf <= __kml_monotonic_ns()) fire(best);
    return 1;
}

/* Run to empty after the program's top-level code. A pending signal's
 * watcher runs first each pass (a signal interrupting the sleep is seen on
 * looping back); only unref'd timers left ends the loop. */
void __kml_timer_drain(void) {
    for (;;) {
        __kml_signal_dispatch();
        i64 tf = 0;
        i64 best = find_best(&tf);
        if (best == -1 || !__kml_timer_any_ref()) return;
        i64 now = __kml_monotonic_ns();
        if (tf > now && sleep_until(tf, now) != 0) continue;
        if (tf <= __kml_monotonic_ns()) fire(best);
    }
}

/* Non-blocking: fire every timer already due, earliest first (the webview
 * page-tick pump); never sleeps. */
void __kml_timer_tick(void) {
    for (;;) {
        i64 tf = 0;
        i64 best = find_best(&tf);
        if (best == -1 || tf > __kml_monotonic_ns()) return;
        fire(best);
    }
}
