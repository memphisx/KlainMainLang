/* atomics.c — Atomics.wait/notify (TDD-00099; in C per TDD-00240): a portable
 * futex substitute (macOS has none). Waiting is an address-keyed list of
 * waiter nodes guarded by one PROCESS-WIDE (not thread-local: cross-thread
 * wakeup is the point) mutex + condvar. notify marks matching nodes and
 * broadcasts; a spurious wakeup never yields "ok" since only a matching
 * notify sets a node's flag. Plain Atomics ops lower inline in the emitter. */
#include <errno.h>
#include <pthread.h>
#include <time.h>

typedef long long i64;

typedef struct kml_waiter {
    i64 addr;
    i64 notified;
    struct kml_waiter *next;
} kml_waiter;

static pthread_mutex_t mtx = PTHREAD_MUTEX_INITIALIZER;
static pthread_cond_t cond = PTHREAD_COND_INITIALIZER;
static kml_waiter *waiters;

/* Remove node from the list; caller holds the mutex. */
static void unlink_waiter(kml_waiter *node) {
    for (kml_waiter **p = &waiters; *p; p = &(*p)->next) {
        if (*p == node) {
            *p = node->next;
            return;
        }
    }
}

/* Block until notified on addr. NaN (also "absent") or +Infinity waits with
 * no deadline; a negative timeout clamps to 0 (spec DoWait). Returns 0 "ok",
 * 1 "not-equal", 2 "timed-out". The value check is under the mutex, closing the
 * check-then-sleep race against a concurrent store+notify. */
static i64 wait_on(void *addr, int wide, i64 expected, double tmoms) {
    pthread_mutex_lock(&mtx);
    i64 cur = wide ? __atomic_load_n((i64 *)addr, __ATOMIC_SEQ_CST)
                   : (i64)__atomic_load_n((int *)addr, __ATOMIC_SEQ_CST);
    if (cur != expected) {
        pthread_mutex_unlock(&mtx);
        return 1;
    }
    kml_waiter node = {(i64)(size_t)addr, 0, waiters};
    waiters = &node;
    struct timespec ts = {0, 0};
    int hastmo = tmoms == tmoms && tmoms != __builtin_inf();
    if (hastmo) {
        /* absolute CLOCK_REALTIME deadline (what cond_timedwait takes) */
        clock_gettime(CLOCK_REALTIME, &ts);
        double ms = tmoms < 0.0 ? 0.0 : tmoms;
        double nsf = ms * 1.0e6;
        i64 ns = nsf >= 9223372036854775807.0 ? 9223372036854775807LL : (i64)nsf;
        i64 sec = (i64)ts.tv_sec + ns / 1000000000;
        i64 nsec = (i64)ts.tv_nsec + ns % 1000000000;
        if (nsec >= 1000000000) {
            sec += 1;
            nsec -= 1000000000;
        }
        ts.tv_sec = (time_t)sec;
        ts.tv_nsec = (long)nsec;
    }
    while (!node.notified) {
        if (!hastmo) {
            pthread_cond_wait(&cond, &mtx);
        } else if (pthread_cond_timedwait(&cond, &mtx, &ts) == ETIMEDOUT) {
            /* a notify may have landed the instant the timeout fired: the
             * flag, read under the mutex, is the truth */
            if (!node.notified) {
                unlink_waiter(&node);
                pthread_mutex_unlock(&mtx);
                return 2;
            }
        }
    }
    unlink_waiter(&node);
    pthread_mutex_unlock(&mtx);
    return 0;
}

i64 __kml_atomics_wait(void *addr, int expected, double tmoms) {
    return wait_on(addr, 0, expected, tmoms);
}

/* A BigInt64Array element's wait: the same, over 64 bits. */
i64 __kml_atomics_wait64(void *addr, i64 expected, double tmoms) {
    return wait_on(addr, 1, expected, tmoms);
}

/* Mark up to count waiters on addr notified, broadcast, return how many. */
i64 __kml_atomics_notify(void *addr, i64 count) {
    pthread_mutex_lock(&mtx);
    i64 marked = 0;
    for (kml_waiter *c = waiters; c; c = c->next) {
        if (c->addr == (i64)(size_t)addr && !c->notified && marked < count) {
            c->notified = 1;
            marked++;
        }
    }
    if (marked > 0) pthread_cond_broadcast(&cond);
    pthread_mutex_unlock(&mtx);
    return marked;
}
