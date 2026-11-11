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
#include <string.h>
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
#define KML_VIEW_ALLOC(n) kml_gc_alloc_atomic(n)
#else
#define KML_VIEW_ALLOC(n) kml_alloc(KML_HEAP_RAW, n)
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
    kml_gc_weak_link(&v->buf, buf);
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

/* Whether a typed array's bytes are a SharedArrayBuffer's (Atomics.wait). */
i64 __kml_view_is_shared(void *data) {
    pthread_mutex_lock(&kml_views_mu);
    int sh = kml_view_shared(data);
    if (!sh && data) {
        /* A view registered without knowing its buffer was shared (one
           reached through ArrayBufferLike or any): the buffer's own
           header word says. */
        kml_view *v = kml_view_find(data);
        void *root = v ? v->root : data;
        kml_view *r = root ? kml_view_find(root) : NULL;
        if (r && r->buf && ((i64 *)r->buf)[-1] == KML_SAB_HDR) sh = 1;
    }
    pthread_mutex_unlock(&kml_views_mu);
    return sh;
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
        /* The ArrayBuffer's host header word precedes its header. */
        i64 *base = (i64 *)kml_alloc(KML_HEAP_WORDS, 32);
        base[0] = r->shared ? KML_SAB_HDR : KML_AB_HDR;
        void **hdr = (void **)(base + 1);
        *(i64 *)hdr = root_len;
        hdr[1] = root;
        base[3] = -1; /* fixed-length: no maximum */
        kml_view_set_buf(r, hdr);
    }
    void *buf = r->buf;
    pthread_mutex_unlock(&kml_views_mu);
    return buf;
}

/* ta.buffer of a typed array held in `any`: the buffer boxed as a host
 * object under the object tag, at its header word. */
i64 __kml_view_buffer_any(void *data, i64 len) {
    void *buf = __kml_view_buffer(data, len);
    return (i64)((uintptr_t)((i64 *)buf - 1) | 1);
}

/* Views over a resizable (growable) buffer: each typed array's header
 * length is kept as the buffer's byte length makes it. A length-tracking
 * view (fixed < 0) spans the buffer from off to its end; a fixed-length one
 * spans fixed elements; either is out of bounds, its length 0, while the
 * buffer is too short for it. lenp is the header's length word; under
 * -mm=gc the entry links the header weakly, so a dead view drops out. */
typedef struct kml_track {
    void *buf;
    void *hdr; /* the header's allocation (lenp - 16), weakly linked */
    void *key; /* hdr, unlinked: the by-header index's key */
    i64 off, bpe, fixed;
    int oob;   /* out of bounds as the buffer's length last left it */
    struct kml_track *next, *hnext;
} kml_track;
static kml_track *kml_tracks[KML_VIEW_BUCKETS], *kml_tracks_h[KML_VIEW_BUCKETS];

/* How many tracked views are out of bounds: while 0, no typed array is,
 * and its methods skip asking (__kml_view_oob). */
i64 __kml_views_oob;

/* A typed array whose header length word is lenp views buf at byte offset
 * off with bpe-byte elements, fixed of them (-1: tracking its length). */
void __kml_view_track(i64 *lenp, void *buf, i64 off, i64 bpe, i64 fixed) {
    if (!lenp || !buf || ((i64 *)buf)[2] < 0) return; /* fixed-length buffer */
    kml_track *t = (kml_track *)KML_VIEW_ALLOC(sizeof(kml_track));
    t->buf = buf;
    t->hdr = (char *)lenp - 16;
#ifdef KML_GC
    kml_gc_weak_link(&t->hdr, t->hdr);
    kml_gc_weak_link(&t->buf, buf); /* (a dead buffer's address may be reused) */
#endif
    t->key = (char *)lenp - 16;
    t->off = off;
    t->bpe = bpe;
    t->fixed = fixed;
    t->oob = 0;
    unsigned h = kml_view_hash(buf), hh = kml_view_hash(t->key);
    pthread_mutex_lock(&kml_views_mu);
    t->next = kml_tracks[h];
    kml_tracks[h] = t;
    t->hnext = kml_tracks_h[hh];
    kml_tracks_h[hh] = t;
    pthread_mutex_unlock(&kml_views_mu);
}

/* buf was resized (grown) from old bytes: bytes it grew into read zero
 * (whatever a shrink left there), and every view tracked over it takes the
 * length its new byte length gives. */
void __kml_view_resized(void *buf, i64 old) {
    i64 n = *(i64 *)buf;
    if (n > old) memset(*(char **)((char *)buf + 8) + old, 0, (size_t)(n - old));
    pthread_mutex_lock(&kml_views_mu);
    for (kml_track *t = kml_tracks[kml_view_hash(buf)]; t; t = t->next) {
        if (t->buf != buf || !t->hdr) continue;
        i64 *lenp = (i64 *)((char *)t->hdr + 16);
        int oob = t->fixed >= 0 ? t->off + t->fixed * t->bpe > n : t->off > n;
        *lenp = oob ? 0 : t->fixed >= 0 ? t->fixed : (n - t->off) / t->bpe;
        __kml_views_oob += oob - t->oob;
        t->oob = oob;
    }
    pthread_mutex_unlock(&kml_views_mu);
}

/* Whether the typed array whose header is h (its data field) is out of
 * bounds of its resizable buffer. */
i64 __kml_view_oob(void *h) {
    void *key = (char *)h - 8;
    int oob = 0;
    pthread_mutex_lock(&kml_views_mu);
    for (kml_track *t = kml_tracks_h[kml_view_hash(key)]; t; t = t->hnext)
        if (t->key == key && t->hdr) {
            oob = t->oob;
            break;
        }
    pthread_mutex_unlock(&kml_views_mu);
    return oob;
}

/* ta.byteOffset: 0 while out of bounds. */
i64 __kml_view_offset_of(void *h, void *data) {
    return __kml_views_oob && __kml_view_oob(h) ? 0 : __kml_view_offset(data);
}
