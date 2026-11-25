/* The handle table (TDD-00230 phase 4): native code that keeps a managed
 * object across an asynchronous boundary — a libuv handle's data, a curl
 * easy handle's private pointer, an nghttp2 stream's user data, a
 * thread-pool record — holds a kml_ref, an index into this table, instead
 * of the object's address.
 *
 * The slots are the object's only native reference: the table is reached
 * from a static (a root in every mode: under -mm=gc malloc is the
 * collector's, and a static's referent is scanned), so a referenced object
 * stays alive until its ref is freed, and a collector that moves objects
 * rewrites the slot (kml_ref_set) instead of every native holder.
 *
 * A ref is the slot's index plus one, so 0 is "no ref". Freed slots chain
 * through a free list; any thread may create, read or free a ref. */

#include <stdatomic.h>
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

static void **ref_slots;   /* the referents; a free slot holds its next free index + 1 */
static unsigned char *ref_live;
static long long ref_cap;
static long long ref_len;  /* slots ever handed out */
static long long ref_free; /* first free slot + 1, 0 when none */
static atomic_flag ref_lock = ATOMIC_FLAG_INIT;

static void lock(void) {
    while (atomic_flag_test_and_set_explicit(&ref_lock, memory_order_acquire)) {
    }
}
static void unlock(void) { atomic_flag_clear_explicit(&ref_lock, memory_order_release); }

static int grow(void) {
    long long nc = ref_cap ? ref_cap * 2 : 64;
    void **s = (void **)kml_realloc(KML_HEAP_WORDS, ref_slots, (size_t)nc * sizeof(void *));
    if (!s) return 0;
    ref_slots = s;
    unsigned char *l = (unsigned char *)realloc(ref_live, (size_t)nc); /* scratch */
    if (!l) return 0;
    ref_live = l;
    ref_cap = nc;
    return 1;
}

kml_ref kml_ref_new(void *obj) {
    lock();
    long long i;
    if (ref_free) {
        i = ref_free - 1;
        ref_free = (long long)(intptr_t)ref_slots[i];
    } else {
        if (ref_len == ref_cap && !grow()) {
            unlock();
            abort();
        }
        i = ref_len++;
    }
    ref_slots[i] = obj;
    ref_live[i] = 1;
    unlock();
    return i + 1;
}

void *kml_ref_get(kml_ref r) {
    if (r <= 0) return NULL;
    lock();
    void *p = r <= ref_len && ref_live[r - 1] ? ref_slots[r - 1] : NULL;
    unlock();
    return p;
}

void kml_ref_set(kml_ref r, void *obj) {
    if (r <= 0) return;
    lock();
    if (r <= ref_len && ref_live[r - 1]) ref_slots[r - 1] = obj;
    unlock();
}

void kml_ref_free(kml_ref r) {
    if (r <= 0) return;
    lock();
    if (r <= ref_len && ref_live[r - 1]) {
        ref_live[r - 1] = 0;
        ref_slots[r - 1] = (void *)(intptr_t)ref_free;
        ref_free = r;
    }
    unlock();
}

/* How many refs are live: a leak check for tests. */
long long kml_ref_live(void) {
    lock();
    long long n = 0;
    for (long long i = 0; i < ref_len; i++) n += ref_live[i];
    unlock();
    return n;
}
