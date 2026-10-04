/* collections_engine.h — the Map/Set hash table shared by the string, number
 * and any-keyed flavors (in C per TDD-00240; concatenated ahead of
 * collections.c and collections_any.c by the Go side). A flavor supplies a
 * hash and an equality; the engine functions are always_inline so each
 * exported symbol gets a specialised body.
 *
 * Header layout (72 bytes), the one codegen and the inspect/clone paths read:
 *   +0 size  +8 cap  +16 keys  +24 vals  +32 idx  +40 idxcap  +48 used
 *   +56 flags (string-keyed: bit 0, a null-prototype dictionary)
 *   +64 iters (open iterators)
 * keys/vals are dense and insertion-ordered; every lookup goes through the
 * open-addressing index (linear probing; slot -1 empty, -2 tombstone, else an
 * entry index). The index rehashes at 3/4 load (occupied + tombstones). */
#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef long long i64;
typedef struct kml_iter {
    struct kml_iter *next;
    i64 pos;
    struct kml_map *map;
} kml_iter;
typedef struct kml_map {
    i64 size, cap;
    i64 *keys, *vals, *idx;
    i64 idxcap, used, flags;
    kml_iter *iters;
} kml_map;

_Static_assert(sizeof(kml_map) == 72, "map header is 72 bytes");
_Static_assert(offsetof(kml_map, keys) == 16 && offsetof(kml_map, idx) == 32 &&
                   offsetof(kml_map, iters) == 64,
               "map header offsets are read by generated code");
_Static_assert(sizeof(kml_iter) == 24, "iterator node is 24 bytes");

typedef i64 (*kml_hash_fn)(i64 key);
typedef int (*kml_eq_fn)(i64 a, i64 b);

/* The xor-fold multiply mix of a number key and the tail of every hash. */
static inline i64 kml_mix(i64 k) {
    unsigned long long h0 = (unsigned long long)k * 0x9E3779B97F4A7C15ULL;
    return (i64)(h0 ^ (h0 >> 33));
}

void __kml_map_remove_at(kml_map *m, i64 idx);

#define KML_INL static inline __attribute__((always_inline))

KML_INL void kml_map_init(kml_map *h) {
    h->iters = NULL;
    h->flags = 0;
    h->size = 0;
    h->cap = 8;
    h->keys = (i64 *)malloc(64);
    h->vals = (i64 *)malloc(64);
    h->idx = (i64 *)malloc(128);
    memset(h->idx, 255, 128);
    h->idxcap = 16;
    h->used = 0;
}

/* {idx-slot, entry-index}: entry-index is -1 on a miss, where idx-slot is the
 * slot an insert of this key must take (first tombstone on the probe path,
 * else the terminating empty slot). */
KML_INL void kml_probe(kml_map *m, i64 key, kml_hash_fn hf, kml_eq_fn eq, i64 *slotp, i64 *entp) {
    i64 mask = m->idxcap - 1;
    i64 slot = hf(key) & mask, ins = -1;
    for (;;) {
        i64 e = m->idx[slot];
        if (e == -1) {
            *slotp = ins == -1 ? slot : ins;
            *entp = -1;
            return;
        }
        if (e == -2) {
            if (ins == -1) ins = slot;
        } else if (eq(m->keys[e], key)) {
            *slotp = slot;
            *entp = e;
            return;
        }
        slot = (slot + 1) & mask;
    }
}

KML_INL i64 kml_find(kml_map *m, i64 key, kml_hash_fn hf, kml_eq_fn eq) {
    i64 slot, e;
    kml_probe(m, key, hf, eq, &slot, &e);
    return e;
}

/* Double the index, re-place every live entry, purge the tombstones. */
KML_INL void kml_rehash(kml_map *m, kml_hash_fn hf) {
    i64 ncap = m->idxcap * 2, mask = ncap - 1;
    i64 *nidx = (i64 *)malloc((size_t)ncap * 8);
    memset(nidx, 255, (size_t)ncap * 8);
    free(m->idx);
    m->idx = nidx;
    m->idxcap = ncap;
    m->used = m->size;
    for (i64 i = 0; i < m->size; i++) {
        i64 s = hf(m->keys[i]) & mask;
        while (nidx[s] != -1) s = (s + 1) & mask;
        nidx[s] = i;
    }
}

KML_INL void kml_set(kml_map *m, i64 key, i64 val, kml_hash_fn hf, kml_eq_fn eq) {
    i64 slot, e;
    kml_probe(m, key, hf, eq, &slot, &e);
    if (e >= 0) {
        m->vals[e] = val;
        return;
    }
    if (m->size >= m->cap) {
        i64 ncap = m->cap * 2;
        m->keys = (i64 *)realloc(m->keys, (size_t)ncap * 8);
        m->vals = (i64 *)realloc(m->vals, (size_t)ncap * 8);
        m->cap = ncap;
    }
    i64 at = m->size;
    m->keys[at] = key;
    m->vals[at] = val;
    m->size = at + 1;
    i64 old = m->idx[slot];
    m->idx[slot] = at;
    m->used += old == -1;
    if ((m->used + 1) * 4 > m->idxcap * 3) kml_rehash(m, hf);
}

KML_INL _Bool kml_delete(kml_map *m, i64 key, kml_hash_fn hf, kml_eq_fn eq) {
    i64 slot, e;
    kml_probe(m, key, hf, eq, &slot, &e);
    if (e < 0) return 0;
    m->idx[slot] = -2;
    __kml_map_remove_at(m, e);
    return 1;
}

/* Stamps the exported API of one key flavor: sfx names it, KT is the C key
 * type, TOKEY maps it to the table's i64 key word, MISS is get()'s miss
 * value. keys()/vals() return {ptr,i64}, which C cannot return portably;
 * the generated wrappers use __kml_map_copy_keys/vals. */
#define KML_MAP_FLAVOR(sfx, KT, HASH, EQ, TOKEY, MISS)                                         \
    void *__kml_map_##sfx##_create(void) {                                                     \
        kml_map *h = (kml_map *)malloc(sizeof(kml_map));                                       \
        kml_map_init(h);                                                                       \
        return h;                                                                              \
    }                                                                                          \
    void __kml_map_##sfx##_set(kml_map *m, KT key, i64 val) { kml_set(m, TOKEY(key), val, HASH, EQ); } \
    i64 __kml_map_##sfx##_get(kml_map *m, KT key) {                                            \
        i64 e = kml_find(m, TOKEY(key), HASH, EQ);                                             \
        return e >= 0 ? m->vals[e] : (MISS);                                                   \
    }                                                                                          \
    _Bool __kml_map_##sfx##_has(kml_map *m, KT key) { return kml_find(m, TOKEY(key), HASH, EQ) >= 0; } \
    _Bool __kml_map_##sfx##_delete(kml_map *m, KT key) { return kml_delete(m, TOKEY(key), HASH, EQ); }
