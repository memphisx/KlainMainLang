/* wrapper.c — a primitive's wrapper object (`new String("ab")`,
 * emit_wrappers.go). The box is a host cell { header, primitive, bag }:
 * the boxed primitive the constructor was given, and the dynamic bag of the
 * properties assigned to it (null until one is). A property read finds the
 * bag's own property first and the primitive's (its prototype's) otherwise. */
#include <stdio.h>
#include <string.h>

typedef long long i64;
typedef unsigned long long u64;

extern void *__kml_host_class_name(i64 v);
extern void *__kml_dynobj_new(void);
extern i64 __kml_dynobj_find(void *o, const char *key);
extern void __kml_dynobj_set(void *o, const char *key, i64 v);
extern char **__kml_dynobj_keys_c(void *o, i64 *n);
extern char **__kml_dynobj_keys_enum_c(void *o, i64 *n);
extern i64 __kml_dynobj_count(void *o);
extern void __kml_throw_nullderef(const char *msg) __attribute__((noreturn));
extern _Bool __kml_dynobj_delete(void *o, const char *key);

enum { W_NONE, W_NUMBER, W_STRING, W_BOOLEAN };

/* Which wrapper v boxes, or W_NONE. */
static int wkind(i64 v) {
    const char *n = (const char *)__kml_host_class_name(v);
    if (!n) return W_NONE;
    if (!strcmp(n, "Number")) return W_NUMBER;
    if (!strcmp(n, "String")) return W_STRING;
    if (!strcmp(n, "Boolean")) return W_BOOLEAN;
    return W_NONE;
}

static i64 *wcell(i64 v) { return (i64 *)((u64)v & ~7ULL); }

/* A String wrapper's primitive: its length-prefixed string, or null. */
static const char *wstr(i64 v) {
    i64 p = wcell(v)[1];
    return (u64)p >= 65536 && (p & 7) == 0 ? (const char *)p : 0;
}
static i64 wlen(const char *s) { return s ? KML_STR_LEN(s) : 0; }

/* A canonical array index ("0", "17"), or -1. */
static i64 index_of(const char *k) {
    if (!k[0] || (k[0] == '0' && k[1])) return -1;
    i64 n = 0;
    for (const char *p = k; *p; p++) {
        if (*p < '0' || *p > '9' || n > (1LL << 40)) return -1;
        n = n * 10 + (*p - '0');
    }
    return n;
}

/* The primitive a wrapper holds, or v itself for any other value. */
i64 __kml_host_unwrap(i64 v) { return wkind(v) ? wcell(v)[1] : v; }

/* What a read of key on v reads from: the wrapper's bag when it has key as
 * its own property, else the primitive; v itself when not a wrapper. */
i64 __kml_wrapper_recv(i64 v, const char *key) {
    if (!wkind(v)) return v;
    void *bag = (void *)wcell(v)[2];
    if (bag && __kml_dynobj_find(bag, key) >= 0) return (i64)((u64)bag | 5);
    return wcell(v)[1];
}

/* Assigns key on a wrapper (into its bag) and returns 1, or returns 0 for
 * any other value. A String's index and length are read-only: the strict-
 * mode TypeError, or (sloppy) no effect. */
int __kml_wrapper_set(i64 v, const char *key, i64 val, int sloppy) {
    int k = wkind(v);
    if (!k) return 0;
    if (k == W_STRING) {
        i64 i = index_of(key);
        if (!strcmp(key, "length") || (i >= 0 && i < wlen(wstr(v)))) {
            if (sloppy) return 1;
            char buf[256];
            int n = snprintf(buf, sizeof buf, "Cannot assign to read only property '%s' of object '[object String]'", key);
            if (n > (int)sizeof buf - 1) n = (int)sizeof buf - 1;
            char *s = kml_alloc(KML_HEAP_RAW, 8 + (size_t)n + 1);
            *(i64 *)s = n;
            memcpy(s + 8, buf, (size_t)n);
            s[8 + n] = 0;
            __kml_throw_nullderef(s + 8);
        }
    }
    if (!wcell(v)[2]) wcell(v)[2] = (i64)__kml_dynobj_new();
    __kml_dynobj_set((void *)wcell(v)[2], key, val);
    return 1;
}

/* The hooks program-independent code calls through (emitWrapperHookCall):
 * its weak null definitions give way to these. */
i64 (*__kml_wrapper_recv_hook)(i64, const char *) = __kml_wrapper_recv;
i64 (*__kml_host_unwrap_hook)(i64) = __kml_host_unwrap;
int __kml_wrapper_has(i64 v, const char *key);
int (*__kml_wrapper_has_hook)(i64, const char *) = __kml_wrapper_has;
int __kml_wrapper_delete(i64 v, const char *key);
int (*__kml_wrapper_delete_hook)(i64, const char *) = __kml_wrapper_delete;
int (*__kml_wrapper_set_hook)(i64, const char *, i64, int) = __kml_wrapper_set;

/* Whether v is a wrapper. */
int __kml_wrapper_is(i64 v) { return wkind(v) != W_NONE; }

/* A wrapper's own property bag, or null. */
void *__kml_wrapper_bag(i64 v) { return wkind(v) ? (void *)wcell(v)[2] : 0; }

/* A wrapper's own string keys: a String's indices, then `length` when all
 * (getOwnPropertyNames) are asked for, then its bag's. */
char **__kml_wrapper_keys_c(i64 v, int all, i64 *out) {
    i64 n = wkind(v) == W_STRING ? wlen(wstr(v)) : 0, bn = 0;
    void *bag = (void *)wcell(v)[2];
    char **bk = bag ? (all ? __kml_dynobj_keys_c(bag, &bn) : __kml_dynobj_keys_enum_c(bag, &bn)) : 0;
    int len = all && wkind(v) == W_STRING;
    char **arr = (char **)kml_alloc(KML_HEAP_WORDS, (size_t)(n + len + bn + 1) * sizeof(char *));
    i64 w = 0;
    for (i64 i = 0; i < n; i++) {
        char *base = (char *)kml_alloc(KML_HEAP_RAW, 32);
        int d = snprintf(base + 8, 24, "%lld", i);
        *(i64 *)base = d;
        arr[w++] = base + 8;
    }
    if (len) {
        static const struct { i64 len; char d[7]; } k_len = { KML_STR_ASCII_HDR(6), "length" };
        arr[w++] = (char *)k_len.d;
    }
    for (i64 i = 0; i < bn; i++) arr[w++] = bk[i];
    *out = w;
    return arr;
}

/* `key in v` for a wrapper's own property: 1 for a String's index or
 * length or a bag key, else -1 (the prototype's members answer as the
 * other host values' do). */
/* hasOwnProperty on primitive string s: its indices below the length and
 * `length`, as its String object's own properties. */
int __kml_str_has_own(const char *s, const char *key) {
    i64 i = index_of(key);
    return !strcmp(key, "length") || (i >= 0 && i < wlen(s));
}

int __kml_wrapper_has(i64 v, const char *key) {
    int k = wkind(v);
    if (!k) return -1;
    if (k == W_STRING) {
        i64 i = index_of(key);
        if (!strcmp(key, "length") || (i >= 0 && i < wlen(wstr(v)))) return 1;
    }
    void *bag = (void *)wcell(v)[2];
    return bag && __kml_dynobj_find(bag, key) >= 0 ? 1 : -1;
}

/* Deletes key from a wrapper: 1 when it is gone, 0 when it is a String's
 * index or length (not configurable), -1 when v is no wrapper. */
int __kml_wrapper_delete(i64 v, const char *key) {
    int k = wkind(v);
    if (!k) return -1;
    if (k == W_STRING) {
        i64 i = index_of(key);
        if (!strcmp(key, "length") || (i >= 0 && i < wlen(wstr(v)))) return 0;
    }
    void *bag = (void *)wcell(v)[2];
    return !bag || __kml_dynobj_delete(bag, key) ? 1 : 0;
}
