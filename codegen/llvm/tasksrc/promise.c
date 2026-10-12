/* promise.c — the promise value's allocator and the scheduler-free scan for
 * Promise.any (TDD-00084 Part A; in C per TDD-00240): what a program with
 * promises but no coroutine tasks links. kml_layout.h is prepended by the
 * compiler. */
#include <stdlib.h>

typedef long long i64;

#define F(p, off, T) (*(T *)((char *)(p) + (off)))
#define P_STATE(p) F(p, KML_PROMISE_STATE, i64)

/* A pending promise body at p. v1 doubles as a rejection's caught-value tag
 * (TDD-00207), defaulted to an Error so a reject with just an error object in
 * v0 reads back as a caught Error. */
void __kml_promise_init_at(void *p) {
    P_STATE(p) = 0;
    F(p, KML_PROMISE_WAITER, void *) = 0;
    F(p, KML_PROMISE_REACTIONS, void *) = 0;
    F(p, KML_PROMISE_BOXED, void *) = 0;
    F(p, KML_PROMISE_FLAGS, i64) = 0;
    F(p, KML_PROMISE_V1, i64) = KML_TAG_ERROR;
}

/* A fresh pending promise: its body, after a header word naming no class
 * (an instance of a class extending Promise has its class's tag there,
 * TDD-00247). */
void *__kml_task_alloc_promise(void) {
    char *b = malloc(8 + KML_PROMISE_SIZE);
    *(i64 *)b = KML_PROMISE_HEADER;
    void *p = b + 8;
    __kml_promise_init_at(p);
    return p;
}

/* Promise.any over already-settled task promises: the first fulfilled member,
 * or -1. Never parks or drives. */
i64 __kml_promise_first_fulfilled(void **members, i64 count) {
    for (i64 i = 0; i < count; i++)
        if (P_STATE(members[i]) == 1) return i;
    return -1;
}

