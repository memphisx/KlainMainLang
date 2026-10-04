// arrayguard.c — an array's integrity level: Object.freeze, Object.seal and
// Object.preventExtensions of an array record it in the frozen set, keyed by
// the array's shared {data, len} header, and every array mutation asks here
// first. The rejections are strict-mode TypeErrors with V8's messages.

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef long long i64;

extern void *__kml_frozen_set_get(void);
extern i64 __kml_map_num_get(void *map, i64 key);
extern void __kml_throw_nullderef(const char *msg) __attribute__((noreturn));

typedef struct { void *data; i64 len; } ArrHdr;

// The integrity levels the frozen set records (emitStaticIntegrity).
enum { LVL_NONE = 0, LVL_FROZEN = 1, LVL_SEALED = 2, LVL_NONEXT = 3 };

// The operations a mutation performs.
enum {
    OP_SET = 0,    // write element a
    OP_ADD = 1,    // add element a (past the end)
    OP_DELETE = 2, // remove element a (from the end)
    OP_LENGTH = 3, // set length to a
    OP_SHIFT = 4,  // shift: move every element down, remove the last
    OP_SPLICE = 5, // splice at a, b = inserted - removed, c = removed + inserted
    OP_REORDER = 6 // sort, reverse, fill, copyWithin: write in place
};

static void throw_type_error(const char *fmt, i64 arg) __attribute__((noreturn));
static void throw_type_error(const char *fmt, i64 arg) {
    char buf[160];
    int n = snprintf(buf, sizeof buf, fmt, arg);
    char *s = malloc(8 + (size_t)n + 1);
    *(i64 *)s = n;
    memcpy(s + 8, buf, (size_t)n + 1);
    __kml_throw_nullderef(s + 8);
}

static void read_only(i64 i) {
    throw_type_error("Cannot assign to read only property '%lld' of object '[object Array]'", i);
}
static void not_extensible(i64 i) { throw_type_error("Cannot add property %lld, object is not extensible", i); }
static void cannot_delete(i64 i) { throw_type_error("Cannot delete property '%lld' of [object Array]", i); }

extern void __kml_map_num_set(void *map, i64 key, i64 value);

// Set once any array has a level: until then a mutation skips the guard.
char __kml_array_integrity_any;

void __kml_array_set_level(void *hdr, i64 lvl) {
    if (!hdr) return;
    void *set = __kml_frozen_set_get();
    i64 cur = __kml_map_num_get(set, (i64)hdr);
    // A level only tightens: frozen (1) beats sealed (2) beats non-extensible (3).
    if (cur != LVL_NONE && cur < lvl) return;
    __kml_map_num_set(set, (i64)hdr, lvl);
    __kml_array_integrity_any = 1;
}

i64 __kml_array_level(void *hdr) {
    if (!hdr) return LVL_NONE;
    return __kml_map_num_get(__kml_frozen_set_get(), (i64)hdr);
}

void __kml_array_guard(void *hdr, i64 op, i64 a, i64 b) {
    i64 lvl = __kml_array_level(hdr);
    if (lvl == LVL_NONE) return;
    i64 len = ((ArrHdr *)hdr)->len;
    switch (op) {
    case OP_SET:
        if (a >= len) not_extensible(a);
        if (lvl == LVL_FROZEN) read_only(a);
        return;
    case OP_ADD:
        not_extensible(a);
    case OP_DELETE:
        if (lvl != LVL_NONEXT && a >= 0) cannot_delete(a);
        return;
    case OP_LENGTH:
        if (lvl == LVL_FROZEN && a != len)
            throw_type_error("Cannot assign to read only property 'length' of object '[object Array]'", 0);
        if (lvl == LVL_SEALED && a < len) cannot_delete(len - 1);
        return;
    case OP_SHIFT:
        if (len == 0) return;
        if (lvl == LVL_FROZEN && len > 1) read_only(0);
        if (lvl != LVL_NONEXT) cannot_delete(len - 1);
        return;
    case OP_SPLICE:
        if (b == 0 && lvl == LVL_NONEXT) return;
        if (lvl == LVL_FROZEN && (b != 0 || a < len)) read_only(a);
        if (b > 0) not_extensible(len);
        if (b < 0 && lvl == LVL_SEALED) cannot_delete(len - 1);
        return;
    case OP_REORDER:
        if (lvl == LVL_FROZEN && len > 0) read_only(a);
        return;
    }
}

// __kml_array_integrity answers Object.isFrozen (0), isSealed (1) and
// isExtensible (2) of an array: an empty non-extensible array is frozen.
int __kml_array_integrity(void *hdr, i64 which) {
    i64 lvl = __kml_array_level(hdr);
    i64 len = hdr ? ((ArrHdr *)hdr)->len : 0;
    switch (which) {
    case 0: return lvl == LVL_FROZEN || (lvl != LVL_NONE && len == 0);
    case 1: return lvl == LVL_FROZEN || lvl == LVL_SEALED || (lvl != LVL_NONE && len == 0);
    default: return lvl == LVL_NONE;
    }
}
