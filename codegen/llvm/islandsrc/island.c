/* island.c — the loop exports of an isolated dynamic-import island with a
 * top-level await (TDD-00225). The importer finds them by these fixed names
 * in the island's own library (dlsym on its handle) and watches the island's
 * loop through them (dynimportwatch.c). Exported despite the island's hidden
 * runtime. */
typedef long long i64;

extern _Thread_local void *__kml_module_promise;
extern _Bool __kml_loop_pass(void);
extern i64 __kml_loop_backend_handle(void);
/* timers.c's: 0 while no timer is pending. */
extern i64 __kml_timer_next_deadline_ns(void);

#define KML_EXPORT __attribute__((visibility("default")))

/* One non-blocking pass of the island's loop, then its module promise's
 * state (0 pending, 1 fulfilled, 2 rejected), with 0x100 set when the pass
 * found nothing left that can wake the island. */
KML_EXPORT i64 __kml_island_poll(void) {
    i64 idle = __kml_loop_pass() ? 256 : 0;
    return *(i64 *)__kml_module_promise | idle;
}

/* The module promise's state word (0 pending, 1 fulfilled, 2 rejected). */
KML_EXPORT i64 __kml_island_state(void) { return *(i64 *)__kml_module_promise; }

/* The module promise's rejection value. */
KML_EXPORT void *__kml_island_error(void) { return (void *)(__SIZE_TYPE__)((i64 *)__kml_module_promise)[2]; }

/* The island's earliest pending timer on the shared monotonic clock; 0 for
 * none. */
KML_EXPORT i64 __kml_island_next_deadline_ns(void) { return __kml_timer_next_deadline_ns(); }

/* The island loop's backend descriptor (Windows: its completion port). */
KML_EXPORT i64 __kml_island_backend(void) { return __kml_loop_backend_handle(); }
