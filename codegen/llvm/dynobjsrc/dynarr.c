/* dynarr.c — the D1 dynamic array (TDD-00155 Stage 2; in C per TDD-00240),
 * box tag 11. Layout (dynjson.c reads it directly):
 *   header (24 bytes): i64 len, i64 cap, ptr data (realloc-grown, doubling)
 *   element (16 bytes): { i64 tag, i64 payload } — a decoded any box.
 * The element universe untyped JSON.parse needs: every element is a
 * self-describing box (tag 7 boxes a statically typed array instead). Plain
 * malloc/realloc for the -mm=gc shim. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef long long i64;

typedef struct {
    i64 tag, pay;
} Elem;

typedef struct {
    i64 len, cap;
    Elem *data;
} Arr;


void *__kml_dynarr_new(i64 cap0) {
    Arr *a = (Arr *)kml_alloc(KML_HEAP_WORDS, sizeof(Arr));
    a->len = 0;
    a->cap = cap0 < 4 ? 4 : cap0;
    a->data = (Elem *)kml_alloc(KML_HEAP_WORDS, (size_t)a->cap * sizeof(Elem));
    return a;
}

i64 __kml_dynarr_len(void *a) { return ((Arr *)a)->len; }

/* Make index `need` addressable. */
void __kml_dynarr_grow(void *ap, i64 need) {
    Arr *a = (Arr *)ap;
    if (need < a->cap) return;
    i64 nc = a->cap * 2 > need + 1 ? a->cap * 2 : need + 1;
    /* Past V8's largest fast-elements store (2^27 slots), or when the
     * allocation fails, the program ends as Node's does, before the
     * buffer is touched (holes.c ends a dense walk the same way). */
    if (need >= (1LL << 27)) nc = -1;
    else if (nc > (1LL << 27)) nc = 1LL << 27;
    Elem *d = nc < 0 ? NULL : (Elem *)kml_realloc(KML_HEAP_WORDS, a->data, (size_t)nc * sizeof(Elem));
    if (!d) {
        fprintf(stderr, "FATAL ERROR: invalid array length Allocation failed - JavaScript heap out of memory\n");
        abort();
    }
    a->data = d;
    a->cap = nc;
}

void __kml_dynarr_store(void *ap, i64 i, i64 v) {
    Elem *e = &((Arr *)ap)->data[i];
    e->tag = kml_nb_tag(v);
    KML_STORE_PTR(&e->pay, kml_nb_pay(v));
}

void __kml_dynarr_push(void *ap, i64 v) {
    Arr *a = (Arr *)ap;
    __kml_dynarr_grow(a, a->len);
    __kml_dynarr_store(a, a->len, v);
    a->len++;
}

i64 __kml_dynarr_at(void *ap, i64 i) {
    Arr *a = (Arr *)ap;
    if (i < 0 || i >= a->len) return 10;
    return kml_nb_pack(a->data[i].tag, a->data[i].pay);
}

/* A hole — an element never set, or deleted — is element tag 13, which
 * nb_pack reads back as undefined; keys, `in` and inspect skip it. */
#define DA_HOLE 13

static void da_holes(Arr *a, i64 from, i64 to) {
    for (i64 j = from; j < to; j++) {
        a->data[j].tag = DA_HOLE;
        a->data[j].pay = 0;
    }
}

int __kml_dynarr_is_hole(void *ap, i64 i) {
    Arr *a = (Arr *)ap;
    return i >= 0 && i < a->len && a->data[i].tag == DA_HOLE;
}

/* delete arr[i]: the element becomes a hole. */
void __kml_dynarr_delete_index(void *ap, i64 i) {
    if (i >= 0 && i < ((Arr *)ap)->len) da_holes((Arr *)ap, i, i + 1);
}

/* arr[i] = v with JS extension semantics: writing past the end grows the
 * array, leaving the gap holes. */
void __kml_dynarr_put(void *ap, i64 i, i64 v) {
    Arr *a = (Arr *)ap;
    if (i < 0) return;
    if (i >= a->len) {
        __kml_dynarr_grow(a, i);
        da_holes(a, a->len, i);
        a->len = i + 1;
    }
    __kml_dynarr_store(a, i, v);
}

/* Parse key as a canonical non-negative array index; -1 when it isn't one
 * (then it's a plain property). */
i64 __kml_dynarr_index(const char *key) {
    /* An array index is a canonical decimal below 2^32-1; any other key
     * ("01", "+1", "4294967295") is an ordinary property. */
    if (key[0] < '0' || key[0] > '9' || (key[0] == '0' && key[1] != 0)) return -1;
    i64 v = 0;
    for (const char *p = key; *p; p++) {
        if (*p < '0' || *p > '9') return -1;
        v = v * 10 + (*p - '0');
        if (v >= 4294967295LL) return -1;
    }
    return v;
}

/* Array.prototype's property named key, as an array's property read finds
 * it (an accessor run with recv, the array, as this): generated when the
 * program reads a method as a value or builds Array.prototype's object;
 * this weak default (undefined) stands in otherwise. */
__attribute__((weak)) i64 __kml_array_proto_get(const char *key, i64 recv) { (void)key; (void)recv; return 10; }
/* Array.prototype's [Symbol.iterator] once the program replaced it
 * (dynobj.c); this weak default stands in without the dynamic runtime. */
__attribute__((weak)) int __kml_array_iter_override(const char *key, i64 *out) { (void)key; (void)out; return 0; }

/* An array's own non-index properties (`a.tag = 1`, a symbol key) live in
 * the shape runtime's added-property table, keyed by the array. */
extern int __kml_shape_expando_get(void *obj, const char *key, i64 *out);
extern const char *__kml_shape_expando_key(void *obj, i64 i);
extern void __kml_shape_expando_set(void *obj, const char *key, i64 value);
extern int __kml_shape_expando_get_recv(void *obj, const char *key, i64 recv, i64 *out);
extern int __kml_shape_expando_put(void *obj, const char *key, i64 v, i64 recv);
extern int __kml_shape_expando_attrs(void *obj, const char *key);

i64 __kml_dynarr_get_by_key(void *a, const char *key) {
    if (strcmp(key, "length") == 0) return kml_nb_pack(0, ((Arr *)a)->len);
    i64 idx = __kml_dynarr_index(key);
    if (idx < 0) {
        i64 v;
        if (__kml_shape_expando_get_recv(a, key, (i64)a | 6, &v)) return v;
        if (__kml_array_iter_override(key, &v)) return v;
        return __kml_array_proto_get(key, (i64)a | 6);
    }
    if (__kml_dynarr_is_hole(a, idx)) {
        /* a hole, or an accessor element */
        i64 v = 10;
        __kml_shape_expando_get_recv(a, key, (i64)a | 6, &v);
        return v;
    }
    return __kml_dynarr_at(a, idx);
}

extern double __kml_any_tonum(i64 v);

/* A length write's new length: v as a number, -1 when it is no valid array
 * length (an integer in [0, 2^32)). */
static i64 array_length_of(i64 v) {
    double d = __kml_any_tonum(v);
    if (!(d >= 0 && d < 4294967296.0) || d != (double)(i64)d) return -1;
    return (i64)d;
}

/* 1 when written; 2 for a length write of no valid array length (the caller
 * throws the RangeError). A length write truncates or extends; a non-index
 * key is the array's own added property. */
int __kml_dynarr_set_by_key(void *ap, const char *key, i64 v) {
    i64 idx = __kml_dynarr_index(key);
    if (idx < 0) {
        if (strcmp(key, "length") == 0) {
            Arr *a = (Arr *)ap;
            i64 n = array_length_of(v);
            if (n < 0) return 2;
            if (n > a->len) {
                __kml_dynarr_grow(a, n - 1);
                da_holes(a, a->len, n);
            }
            a->len = n;
            return 1;
        }
        if (__kml_shape_expando_put(ap, key, v, (i64)ap | 6) >= 0) return 1;
        __kml_shape_expando_set(ap, key, v);
        return 1;
    }
    if (__kml_dynarr_is_hole(ap, idx) && __kml_shape_expando_put(ap, key, v, (i64)ap | 6) >= 0) return 1;
    __kml_dynarr_put(ap, idx, v);
    return 1;
}

/* The index keys "0".."len-1" as length-prefixed heap strings: a string's
 * own keys. */
char **__kml_index_keys_c(i64 len) {
    char **arr = (char **)kml_alloc(KML_HEAP_WORDS, (size_t)(len > 0 ? len : 1) * sizeof(char *));
    for (i64 i = 0; i < len; i++) {
        char *base = (char *)kml_alloc(KML_HEAP_RAW, 32);
        int m = snprintf(base + 8, 24, "%lld", i);
        *(i64 *)base = m;
        KML_STORE_PTR(&arr[i], base + 8);
    }
    return arr;
}

/* A dynamic array's own string keys, as Object.keys lists them: the index
 * keys "0".."len-1" as length-prefixed heap strings, then the properties
 * added to it. *n receives the count. */
char **__kml_dynarr_keys_c(void *ap, i64 *n) {
    i64 len = ((Arr *)ap)->len, extra = 0;
    while (__kml_shape_expando_key(ap, extra)) extra++;
    char **arr = (char **)kml_alloc(KML_HEAP_WORDS, (size_t)(len + extra + 1) * sizeof(char *));
    i64 k = 0;
    for (i64 i = 0; i < len; i++) {
        char *base = (char *)kml_alloc(KML_HEAP_RAW, 32);
        int m = snprintf(base + 8, 24, "%lld", i);
        *(i64 *)base = m;
        if (((Arr *)ap)->data[i].tag == DA_HOLE) {
            /* a hole is left out, an enumerable accessor element kept */
            int at = __kml_shape_expando_attrs(ap, base + 8);
            if (at < 0 || !(at & 2)) continue;
        }
        KML_STORE_PTR(&arr[k++], base + 8);
    }
    for (i64 i = 0; i < extra; i++) {
        const char *key = __kml_shape_expando_key(ap, i);
        if (strncmp(key, "\x01@@sym:", 7) != 0 && __kml_dynarr_index(key) < 0 && (__kml_shape_expando_attrs(ap, key) & 2)) KML_STORE_PTR(&arr[k++], (char *)key);
    }
    *n = k;
    return arr;
}

/* delete arr[key]: an index becomes a hole, any other key an added property
 * removed. */
extern int __kml_shape_expando_delete(void *obj, const char *key);
void __kml_dynarr_delete_key(void *ap, const char *key) {
    i64 idx = __kml_dynarr_index(key);
    if (idx < 0) __kml_shape_expando_delete(ap, key);
    else __kml_dynarr_delete_index(ap, idx);
}

/* RegExp exec's `indices` (the d flag): a boxed array of each group's
 * [start, end] (undefined for an unmatched group) whose `groups` is a
 * null-prototype dictionary of the named groups' pairs, or undefined when
 * the pattern names none. count, esize and table are PCRE2's name table
 * (see __kml_regex_groups); offsets are bytes, in UTF-16 code units when
 * utf16 is set, as exec's index. */
extern void *__kml_dynobj_new(void);
extern _Bool __kml_dynobj_set_proto(void *o, void *proto);
extern void __kml_dynobj_set(void *o, const char *key, i64 v);
extern i64 __kml_regex_byte_to_utf16(const unsigned char *str, i64 bytelen);
#define KML_RX_UNSET (~(size_t)0)
static i64 kml_rx_off(int utf16, const char *s, size_t off) {
    return utf16 ? __kml_regex_byte_to_utf16((const unsigned char *)s, (i64)off) : (i64)off;
}
static i64 kml_rx_pair(int utf16, const char *s, const size_t *ovec, int rc, unsigned int n) {
    if ((int)n >= rc || ovec[2 * n] == KML_RX_UNSET) return 10; /* undefined */
    void *p = __kml_dynarr_new(2);
    __kml_dynarr_push(p, kml_nb_double((double)kml_rx_off(utf16, s, ovec[2 * n])));
    __kml_dynarr_push(p, kml_nb_double((double)kml_rx_off(utf16, s, ovec[2 * n + 1])));
    return (i64)p | 6;
}
i64 __kml_regex_indices(int utf16, unsigned int count, unsigned int esize, const unsigned char *table, const char *subject, const size_t *ovec, int rc, i64 ngroups) {
    void *arr = __kml_dynarr_new(ngroups);
    for (i64 i = 0; i < ngroups; i++) __kml_dynarr_push(arr, kml_rx_pair(utf16, subject, ovec, rc, (unsigned int)i));
    i64 groups = 10;
    if (count > 0 && table != NULL) {
        void *g = __kml_dynobj_new();
        __kml_dynobj_set_proto(g, NULL);
        unsigned int maxn = 0;
        for (unsigned int i = 0; i < count; i++) {
            const unsigned char *e = table + (size_t)i * esize;
            unsigned int n = ((unsigned int)e[0] << 8) | e[1];
            if (n > maxn) maxn = n;
        }
        for (unsigned int want = 1; want <= maxn; want++) { /* the pattern's order */
            for (unsigned int i = 0; i < count; i++) {
                const unsigned char *e = table + (size_t)i * esize;
                if ((((unsigned int)e[0] << 8) | e[1]) == want)
                    __kml_dynobj_set(g, (const char *)e + 2, kml_rx_pair(utf16, subject, ovec, rc, want));
            }
        }
        groups = (i64)g | 5;
    }
    __kml_dynarr_set_by_key(arr, "groups", groups);
    return (i64)arr | 6;
}
