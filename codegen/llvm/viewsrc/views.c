/* views.c — a typed array's buffer and byteOffset (TDD-00243).
 *
 * A typed array is a {data, len} header; a view over an ArrayBuffer points
 * data into the buffer's bytes. The registry maps a view's data address to
 * the root allocation it lies in (a buffer's bytes, or an array's own
 * storage) and its byte offset there. The ArrayBuffer header ({i64 len, ptr
 * data}) of a root is made on first `.buffer` and kept, so `ta.buffer ===
 * ta.buffer`. An address not in the registry is an array with its own
 * storage: its own root at offset 0.
 *
 * Entries live in a chained hash table. Under -mm=gc (KML_GC) each entry is
 * allocated atomic, so the registry keeps neither views nor buffers alive,
 * and its buffer field is a disappearing link: a collected buffer clears it,
 * and the next `.buffer` makes a fresh header (no one held the old one). */
#include <stdlib.h>
#include <stdint.h>
#include <pthread.h>

typedef long long i64;

typedef struct kml_view {
    void *key;           /* the view's data address */
    void *root;          /* the root allocation's first byte */
    i64 root_len;        /* the root's byte length */
    i64 off;             /* key - root */
    void *buf;           /* the root's ArrayBuffer header (roots only), or NULL */
    int shared;          /* the root is a SharedArrayBuffer's bytes */
    struct kml_view *next;
} kml_view;

#ifdef KML_GC
extern void *GC_malloc_atomic(size_t n);
extern int GC_general_register_disappearing_link(void **link, const void *obj);
#define KML_VIEW_ALLOC(n) GC_malloc_atomic(n)
#else
#define KML_VIEW_ALLOC(n) malloc(n)
#endif

#define KML_VIEW_BUCKETS 4096
static kml_view *kml_views[KML_VIEW_BUCKETS];
static pthread_mutex_t kml_views_mu = PTHREAD_MUTEX_INITIALIZER;

static unsigned kml_view_hash(void *p) {
    uintptr_t x = (uintptr_t)p;
    x ^= x >> 17;
    x *= 0x9E3779B97F4A7C15ull;
    return (unsigned)(x >> 40) & (KML_VIEW_BUCKETS - 1);
}

static kml_view *kml_view_find(void *key) {
    for (kml_view *v = kml_views[kml_view_hash(key)]; v; v = v->next)
        if (v->key == key) return v;
    return NULL;
}

static kml_view *kml_view_put(void *key, void *root, i64 root_len, i64 off) {
    kml_view *v = kml_view_find(key);
    if (!v) {
        v = (kml_view *)KML_VIEW_ALLOC(sizeof(kml_view));
        unsigned h = kml_view_hash(key);
        v->key = key;
        v->buf = NULL;
        v->shared = 0;
        v->next = kml_views[h];
        kml_views[h] = v;
    }
    v->root = root;
    v->root_len = root_len;
    v->off = off;
    return v;
}

static void kml_view_set_buf(kml_view *v, void *buf) {
    v->buf = buf;
#ifdef KML_GC
    GC_general_register_disappearing_link(&v->buf, buf);
#endif
}

/* A view made over an ArrayBuffer (or, shared != 0, a SharedArrayBuffer):
 * data = buf.data + off. */
void __kml_view_register(void *data, void *buf, i64 off, i64 shared) {
    if (!data || !buf) return;
    i64 blen = *(i64 *)buf;
    void *bdata = *(void **)((char *)buf + 8);
    pthread_mutex_lock(&kml_views_mu);
    kml_view *r = kml_view_find(bdata);
    if (!r || r->root != bdata) r = kml_view_put(bdata, bdata, blen, 0);
    if (!r->buf) kml_view_set_buf(r, buf);
    if (shared) r->shared = 1;
    if (data != bdata) kml_view_put(data, bdata, blen, off);
    pthread_mutex_unlock(&kml_views_mu);
}

/* A subarray: child lies in parent (a view or an own-storage array of
 * parent_len bytes) and shares its root. */
void __kml_view_derive(void *parent, i64 parent_len, void *child) {
    if (!parent || child == parent) return;
    pthread_mutex_lock(&kml_views_mu);
    kml_view *p = kml_view_find(parent);
    if (p)
        kml_view_put(child, p->root, p->root_len, p->off + ((char *)child - (char *)parent));
    else
        kml_view_put(child, parent, parent_len, (char *)child - (char *)parent);
    pthread_mutex_unlock(&kml_views_mu);
}

/* ta.byteOffset. */
i64 __kml_view_offset(void *data) {
    if (!data) return 0;
    pthread_mutex_lock(&kml_views_mu);
    kml_view *v = kml_view_find(data);
    i64 off = v ? v->off : 0;
    pthread_mutex_unlock(&kml_views_mu);
    return off;
}

/* Whether the root data lies in is a SharedArrayBuffer's bytes. */
static int kml_view_shared(void *data) {
    kml_view *v = data ? kml_view_find(data) : NULL;
    void *root = v ? v->root : data;
    kml_view *r = root ? kml_view_find(root) : NULL;
    return r && r->root == root && r->shared;
}

/* ta.buffer: the root's ArrayBuffer header, made on first use. len is the
 * array's byte length, the root's when the array is its own root. */
void *__kml_view_buffer(void *data, i64 len) {
    pthread_mutex_lock(&kml_views_mu);
    void *root = data;
    i64 root_len = len;
    kml_view *v = data ? kml_view_find(data) : NULL;
    if (v) {
        root = v->root;
        root_len = v->root_len;
    }
    kml_view *r = root ? kml_view_find(root) : NULL;
    if (!r || r->root != root) r = kml_view_put(root, root, root_len, 0);
    if (!r->buf) {
        void **hdr = (void **)malloc(16);
        *(i64 *)hdr = root_len;
        hdr[1] = root;
        kml_view_set_buf(r, hdr);
    }
    void *buf = r->buf;
    pthread_mutex_unlock(&kml_views_mu);
    return buf;
}

#ifdef KML_BOXED_VIEWS
/* ta.buffer of a typed array held in `any`: the buffer boxed as a host
 * object, a two-word cell {header, handle} under the object tag. The header
 * word is the program's ArrayBuffer host type, exported by the compiler. */
extern const i64 __kml_view_ab_header, __kml_view_sab_header;
i64 __kml_view_buffer_any(void *data, i64 len) {
    void *buf = __kml_view_buffer(data, len);
    pthread_mutex_lock(&kml_views_mu);
    int shared = kml_view_shared(data);
    pthread_mutex_unlock(&kml_views_mu);
    i64 *cell = (i64 *)malloc(16);
    cell[0] = shared ? __kml_view_sab_header : __kml_view_ab_header;
    ((void **)cell)[1] = buf;
    return (i64)((uintptr_t)cell | 1);
}
#endif
