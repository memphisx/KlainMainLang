/* fetchslot.c — bridge a raw fetch handle to a pending task promise
 * (TDD-00223; in C per TDD-00240). The bridge (__kml_fetch_drive_run, which
 * builds the program's Response) runs as a coroutine, so its wait for the
 * response headers parks on the fetch while the event loop keeps running.
 * kml_layout.h is prepended by the compiler. */
#include <stdlib.h>

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

extern void *__kml_task_alloc_promise(void);
extern void __kml_promise_mark_handled(void *);
extern void *__kml_spawn_task(void *fn, void *args, void *promiseSlot);
/* Emitted with the program (it reads its Response layout). */
extern void __kml_fetch_drive_run(void *env);

typedef struct {
    void *slot, *prom;
} bridge_env;

void *__kml_fetch_slot_to_promise(void *slot) {
    /* One bridge promise per fetch: p.then(f) and "await p" are reactions on
     * the same promise, so they run in registration order and see the same
     * Response object, as in JS where p is one promise. */
    void *pending = *(void **)slot;
    void *have = F(pending, KML_FETCHPENDING_BRIDGE, void *);
    if (have) return have;
    void *prom = __kml_task_alloc_promise();
    F(pending, KML_FETCHPENDING_BRIDGE, void *) = prom;
    bridge_env *env = (bridge_env *)malloc(sizeof(bridge_env));
    env->slot = slot;
    env->prom = prom;
    /* The coroutine's own task promise is bookkeeping only: the bridge
     * settles prom itself (the Response, or a transport error). */
    void *taskprom = __kml_task_alloc_promise();
    __kml_promise_mark_handled(taskprom);
    __kml_spawn_task((void *)__kml_fetch_drive_run, env, taskprom);
    return prom;
}
