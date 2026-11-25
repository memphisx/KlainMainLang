// kml_pool.h — the thread pool's work/completion item (klainpool.c), for
// the natives compiled as units of their own: they make an item with
// native_item and hand it to the pool with native_submit.
#include <pthread.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <errno.h>

struct kml_loop_port;
extern char *__kml_str_alloc(int64_t n);
extern void __kml_str_finalize(char *s);
// One struct, reused as work item (loop -> pool) and completion item (pool ->
// loop). arg0/arg1 are strdup'd copies the item owns and frees.
typedef struct kml_pool_item {
    struct kml_pool_item *next;
    char *arg0;
    char *arg1;
    struct kml_loop_port *port;
    // Its work, run on a worker, fills err/res; the loop then calls
    // inv(clo, err, res). a[]/d[] and buf/buflen are its arguments. live
    // links it into native_live while it is pending. The closure is held
    // through the handle table (kml_ref), never by address.
    void (*work)(struct kml_pool_item *);
    void *inv;
    kml_ref clo;
    int64_t a[4];
    double d[4];            // an fs operation's numbers (klainfs.c)
    void *buf;
    int64_t buflen;
    kml_ref pin[2];         // managed buffers a worker reads or writes, pinned while pending
    int64_t err;            // a positive errno, or 0
    double res;
    char *res_str;          // a string result, read by nativeLastString()
    struct kml_pool_item *live_prev, *live_next;
} kml_pool_item;

#ifndef KML_POOL_SELF
kml_pool_item *__kml_pool_item(void (*work)(kml_pool_item *), void *inv, void *clo);
void __kml_pool_submit(kml_pool_item *it);
#define native_item __kml_pool_item
#define native_submit __kml_pool_submit
#endif
