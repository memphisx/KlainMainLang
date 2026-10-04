/* module.c — a module with a top-level `await` runs as a coroutine
 * (TDD-00224; in C per TDD-00240): its trampoline and the helper a blocking
 * event-loop call in module code goes through. kml_layout.h is prepended by
 * the compiler. */
typedef long long i64;
typedef unsigned char u8;
typedef void (*kml_body)(void *);

#define F(p, off, T) (*(T *)((char *)(p) + (off)))

#pragma clang diagnostic ignored "-Wincompatible-library-redeclaration"
extern int swapcontext(void *, void *);

extern _Thread_local void *__kml_task_launching;
extern _Thread_local void *__kml_current_task;
extern _Thread_local int __kml_jmp_top;
extern void __kml_task_finish(void *);
extern void __kml_event_loop_run(void);
#ifdef KLAIN_GC
extern void __kml_task_gc_restore(void);
#endif

/* Read by the generated worker/entry loops and dynamic import. */
_Thread_local void *__kml_module_task;
_Thread_local u8 __kml_module_wants_loop;
/* The module promise of a worker module task, for the worker thread's
 * unsettled check (the entry program keeps its promise in a main() register). */
_Thread_local void *__kml_module_promise;

/* No catch-all (unlike __kml_task_trampoline): the task's jmpbuf stack is
 * empty at entry, so an uncaught throw takes the process-level uncaught path. */
void __kml_module_trampoline(void) {
    void *t = __kml_task_launching;
    __kml_module_task = t;
    F(t, KML_TASK_FN, kml_body)(F(t, KML_TASK_ARGS, void *));
    __kml_task_finish(t);
}

/* A call that runs the event loop inline (the blocking http.listen). The loop
 * may only run on the main stack — on a coroutine's stack it would switch
 * contexts out from under itself — so the module task asks main() to run it
 * and parks; main() resumes the task when that run of the loop returns.
 * Anywhere else it is the plain inline run. */
void __kml_module_run_loop(void) {
    void *ct = __kml_current_task;
    if (!ct || ct != __kml_module_task) {
        __kml_event_loop_run();
        return;
    }
    __kml_module_wants_loop = 1;
    F(ct, KML_TASK_STATE, i64) = 3;
    F(ct, KML_TASK_JMPTOP, i64) = (i64)(unsigned)__kml_jmp_top;
    swapcontext(F(ct, KML_TASK_CTX, void *), F(ct, KML_TASK_RESUMER, void *));
#ifdef KLAIN_GC
    __kml_task_gc_restore();
#endif
}
