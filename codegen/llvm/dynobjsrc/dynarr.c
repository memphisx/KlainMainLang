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
    Arr *a = (Arr *)malloc(sizeof(Arr));
    a->len = 0;
    a->cap = cap0 < 4 ? 4 : cap0;
    a->data = (Elem *)malloc((size_t)a->cap * sizeof(Elem));
    return a;
}

i64 __kml_dynarr_len(void *a) { return ((Arr *)a)->len; }

/* Make index `need` addressable. */
void __kml_dynarr_grow(void *ap, i64 need) {
    Arr *a = (Arr *)ap;
    if (need < a->cap) return;
    i64 nc = a->cap * 2 > need + 1 ? a->cap * 2 : need + 1;
    a->data = (Elem *)realloc(a->data, (size_t)nc * sizeof(Elem));
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

/* arr[i] = v with JS extension semantics: writing past the end grows the
 * array and fills the gap with undefined holes. */
void __kml_dynarr_put(void *ap, i64 i, i64 v) {
    Arr *a = (Arr *)ap;
    if (i < 0) return;
    if (i >= a->len) {
        __kml_dynarr_grow(a, i);
        for (i64 j = a->len; j < i; j++) __kml_dynarr_store(a, j, 10);
        a->len = i + 1;
    }
    __kml_dynarr_store(a, i, v);
}

/* Parse key as a canonical non-negative array index; -1 when it isn't one
 * (then it's a plain property). */
i64 __kml_dynarr_index(const char *key) {
    char *end;
    i64 v = strtoll(key, &end, 10);
    return (end != key && *end == 0 && v >= 0) ? v : -1;
}

i64 __kml_dynarr_get_by_key(void *a, const char *key) {
    if (strcmp(key, "length") == 0) return nb_pack(0, ((Arr *)a)->len);
    i64 idx = __kml_dynarr_index(key);
    return idx < 0 ? 10 : __kml_dynarr_at(a, idx);
}

/* Returns false when the key isn't a writable index (a plain expando
 * property or a length write) — the caller turns that into a clean
 * TypeError. */
_Bool __kml_dynarr_set_by_key(void *a, const char *key, i64 v) {
    i64 idx = __kml_dynarr_index(key);
    if (idx < 0) return 0;
    __kml_dynarr_put(a, idx, v);
    return 1;
}

/* The array index keys "0".."len-1" as length-prefixed heap strings, as
 * Object.keys lists an array's. The IR wrappers build the {ptr,i64}. */
char **__kml_index_keys_c(i64 len) {
    char **arr = (char **)malloc((size_t)len * sizeof(char *));
    for (i64 i = 0; i < len; i++) {
        char *base = (char *)malloc(32);
        int n = snprintf(base + 8, 24, "%lld", i);
        *(i64 *)base = n;
        arr[i] = base + 8;
    }
    return arr;
}
