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

/* nb_* mirror __kml_nb_pack/tag/pay (runtime_nanbox.go — keep in sync). */
static i64 nb_double(double d) {
    unsigned long long bits;
    if (d != d) bits = 0x7FF8000000000000ULL;
    else memcpy(&bits, &d, 8);
    return (i64)(bits + (1ULL << 49));
}
static i64 nb_pack(i64 tag, i64 pay) {
    switch (tag) {
    case 0: return nb_double((double)pay);
    case 1: { double d; memcpy(&d, &pay, 8); return nb_double(d); }
    case 3: return pay ? 7 : 6;
    case 4: return 2;
    case 5: return 10;
    case 2: return pay;
    case 6: return pay | 1;
    case 7: return pay | 2;
    case 8: return pay | 3;
    case 9: return pay | 4;
    case 10: return pay | 5;
    case 11: return pay | 6;
    case 12: return pay | 7;
    default: return 10;
    }
}
static i64 nb_tag(i64 word) {
    unsigned long long v = (unsigned long long)word;
    static const i64 kindTag[8] = {2, 6, 7, 8, 9, 10, 11, 12};
    if (v >= (1ULL << 49)) return 1;
    if (v < 65536) return v == 2 ? 4 : (v == 6 || v == 7) ? 3 : 5;
    return kindTag[v & 7];
}
static i64 nb_pay(i64 word) {
    unsigned long long v = (unsigned long long)word;
    if (v >= (1ULL << 49)) return (i64)(v - (1ULL << 49));
    if (v < 65536) return v == 7;
    return (v & 7) == 0 ? (i64)v : (i64)(v & ~7ULL);
}

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
    a->data = (Elem *)kml_realloc(KML_HEAP_WORDS, a->data, (size_t)nc * sizeof(Elem));
    a->cap = nc;
}

void __kml_dynarr_store(void *ap, i64 i, i64 v) {
    Elem *e = &((Arr *)ap)->data[i];
    e->tag = nb_tag(v);
    e->pay = nb_pay(v);
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
    return nb_pack(a->data[i].tag, a->data[i].pay);
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

/* Array.prototype's method named key, as an array's property read finds it:
 * generated when the program reads one as a value; this weak default
 * (undefined) stands in otherwise. */
__attribute__((weak)) i64 __kml_array_proto_get(const char *key) { (void)key; return 10; }
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
    if (strcmp(key, "length") == 0) return nb_pack(0, ((Arr *)a)->len);
    i64 idx = __kml_dynarr_index(key);
    if (idx < 0) {
        i64 v;
        if (__kml_shape_expando_get_recv(a, key, (i64)a | 6, &v)) return v;
        if (__kml_array_iter_override(key, &v)) return v;
        return __kml_array_proto_get(key);
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
        arr[i] = base + 8;
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
        arr[k++] = base + 8;
    }
    for (i64 i = 0; i < extra; i++) {
        const char *key = __kml_shape_expando_key(ap, i);
        if (strncmp(key, "\x01@@sym:", 7) != 0 && __kml_dynarr_index(key) < 0 && (__kml_shape_expando_attrs(ap, key) & 2)) arr[k++] = (char *)key;
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
