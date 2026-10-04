/* asynchooks.c — the AsyncLocalStorage runtime (TDD-00168; in C per
 * TDD-00240). "The current async context" is the frame
 * lib/node/async_hooks.ts keeps, as an `any` word: per task (the task
 * struct's ASYNC_CTX field) while one runs, else the top-level root. A parked
 * task's struct persists and the scheduler restores __kml_current_task to it
 * before resuming, so context read after an await is the same task's head.
 * KML_NB_UNDEFINED is passed by AsyncHooksCFlags. kml_layout.h is prepended. */
#include <stdint.h>
typedef long long i64;

extern _Thread_local void *__kml_current_task;
extern _Thread_local void *__kml_root_async_ctx;

static void **ctx_slot(void) {
    char *t = (char *)__kml_current_task;
    return t ? (void **)(t + KML_TASK_ASYNC_CTX) : &__kml_root_async_ctx;
}

void *__kml_als_ctx_get(void) { return *ctx_slot(); }

void __kml_als_ctx_set(void *head) { *ctx_slot() = head; }

/* A timer callback's context binding: env is { origClosure, capturedCtx },
 * built at schedule time. A timer fires from the top-level loop (no running
 * task), so install/restore act on the root context. */
void __kml_als_timer_tramp(void *env) {
    void *oc = ((void **)env)[0];
    void *cc = ((void **)env)[1];
    void *saved = __kml_als_ctx_get();
    __kml_als_ctx_set(cc);
    void (*fn)(void *) = *(void (**)(void *))((char *)oc + KML_CLOSURE_FN);
    void *ep = *(void **)((char *)oc + KML_CLOSURE_ENV);
    fn(ep);
    __kml_als_ctx_set(saved);
}

/* lib/native.d.ts's asyncContextGet/Set: the frame as an `any` word,
 * undefined while there is none. */
i64 __kml_native_async_context_get(void) {
    void *h = __kml_als_ctx_get();
    return h ? (i64)(intptr_t)h : KML_NB_UNDEFINED;
}

void __kml_native_async_context_set(i64 frame) {
    __kml_als_ctx_set(frame == KML_NB_UNDEFINED ? (void *)0 : (void *)(intptr_t)frame);
}
