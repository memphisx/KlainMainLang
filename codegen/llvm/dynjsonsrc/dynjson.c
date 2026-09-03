// dynjson.c — JSON.stringify and Array-toString for D1 dynamic values
// (KlainMainLang TDD-00155 Stage 2). Self-contained walker over the dynamic
// object/array heap layouts; libc + __kml_dtoa only. Compiled alongside the
// program (the dtoa/json embedded-C pattern) only when a program stringifies
// a dynamic value.
//
// LAYOUT CONTRACTS (must stay in sync with the emitting runtime):
//   Dynamic object bag (runtime_dynobj.go):
//     header: [0]=i64 flags  [8]=ptr proto  [16]=ptr props  [24]=i64 count  [32]=i64 cap
//     entry (32B): [0]=char* key  [8]=i64 tag  [16]=i64 payload  [24]=i64 attrs
//   Dynamic array (runtime_dynarr.go):
//     header: [0]=i64 len  [8]=i64 cap  [16]=ptr data
//     element (16B): [0]=i64 tag  [8]=i64 payload
//   Box tags: 0=int 1=float 2=string 3=bool 4=null 5=undefined 6=object
//   7=array 8=funcRef 9=stream 10=dynobj 11=dynarr.
//   Strings are length-prefixed (i64 length at ptr-8, TDD-00120); every
//   string this file returns carries that header.

#include <stdlib.h>
#include <string.h>
#include <stdio.h>

extern void __kml_dtoa(char *buf, double v);

#define KML_DYN_MAX_DEPTH 512

/* ---- layout readers ---- */

static long long obj_count(char *o) { return *(long long *)(o + 24); }
static char *obj_props(char *o) { return *(char **)(o + 16); }
static char *obj_key(char *o, long long i) { return *(char **)(obj_props(o) + i * 32); }
static long long obj_tag(char *o, long long i) { return *(long long *)(obj_props(o) + i * 32 + 8); }
static long long obj_pay(char *o, long long i) { return *(long long *)(obj_props(o) + i * 32 + 16); }
static long long obj_attrs(char *o, long long i) { return *(long long *)(obj_props(o) + i * 32 + 24); }

/* nb_decode mirrors __kml_nb_tag/__kml_nb_pay (runtime_nanbox.go — keep in
   sync): numbers are double_bits + 2^49 (tag 1); small immediates; else a
   low-3-bit-kind-tagged pointer. */
static void nb_decode(long long word, long long *tag, long long *pay) {
    unsigned long long v = (unsigned long long)word;
    if (v >= (1ULL << 49)) {
        *tag = 1;
        *pay = (long long)(v - (1ULL << 49));
        return;
    }
    if (v < 65536) {
        switch (v) {
        case 2: *tag = 4; *pay = 0; return;
        case 6: *tag = 3; *pay = 0; return;
        case 7: *tag = 3; *pay = 1; return;
        default: *tag = 5; *pay = 0; return;
        }
    }
    static const long long kindTag[8] = {2, 6, 7, 8, 9, 10, 11, 12};
    long long kind = (long long)(v & 7);
    *tag = kindTag[kind];
    *pay = kind == 0 ? (long long)v : (long long)(v & ~7ULL);
}

static long long arr_len(char *a) { return *(long long *)a; }
static char *arr_data(char *a) { return *(char **)(a + 16); }
static long long arr_tag(char *a, long long i) { return *(long long *)(arr_data(a) + i * 16); }
static long long arr_pay(char *a, long long i) { return *(long long *)(arr_data(a) + i * 16 + 8); }

/* ---- growable output buffer ---- */

typedef struct {
    char *d;
    long long len, cap;
} Sb;

static void sb_init(Sb *b) {
    b->cap = 64;
    b->len = 0;
    b->d = (char *)malloc((size_t)b->cap);
}

static void sb_need(Sb *b, long long extra) {
    if (b->len + extra + 1 <= b->cap) return;
    while (b->len + extra + 1 > b->cap) b->cap *= 2;
    b->d = (char *)realloc(b->d, (size_t)b->cap);
}

static void sb_ch(Sb *b, char c) {
    sb_need(b, 1);
    b->d[b->len++] = c;
}

static void sb_raw(Sb *b, const char *s, long long n) {
    sb_need(b, n);
    memcpy(b->d + b->len, s, (size_t)n);
    b->len += n;
}

static void sb_cstr(Sb *b, const char *s) { sb_raw(b, s, (long long)strlen(s)); }

/* sb_finish converts the buffer into a length-prefixed heap string
   ([i64 len][bytes][NUL], value pointer at base+8 — TDD-00120). */
static char *sb_finish(Sb *b) {
    char *base = (char *)malloc((size_t)(8 + b->len + 1));
    *(long long *)base = b->len;
    memcpy(base + 8, b->d, (size_t)b->len);
    base[8 + b->len] = 0;
    free(b->d);
    return base + 8;
}

/* JSON string escaping per ECMAScript QuoteJSONString. */
static void sb_json_string(Sb *b, const char *s) {
    sb_ch(b, '"');
    for (const unsigned char *p = (const unsigned char *)s; *p; p++) {
        unsigned char c = *p;
        switch (c) {
        case '"': sb_cstr(b, "\\\""); break;
        case '\\': sb_cstr(b, "\\\\"); break;
        case '\b': sb_cstr(b, "\\b"); break;
        case '\f': sb_cstr(b, "\\f"); break;
        case '\n': sb_cstr(b, "\\n"); break;
        case '\r': sb_cstr(b, "\\r"); break;
        case '\t': sb_cstr(b, "\\t"); break;
        default:
            if (c < 0x20) {
                char tmp[8];
                snprintf(tmp, sizeof tmp, "\\u%04x", c);
                sb_cstr(b, tmp);
            } else {
                sb_ch(b, (char)c);
            }
        }
    }
    sb_ch(b, '"');
}

static void sb_number(Sb *b, long long tag, long long pay) {
    char tmp[40];
    if (tag == 0) {
        snprintf(tmp, sizeof tmp, "%lld", pay);
        sb_cstr(b, tmp);
        return;
    }
    double d;
    memcpy(&d, &pay, 8);
    if (d != d || d > 1.7976931348623157e308 || d < -1.7976931348623157e308) {
        sb_cstr(b, "null"); /* JSON: NaN/Infinity serialize as null */
        return;
    }
    __kml_dtoa(tmp, d);
    sb_cstr(b, tmp);
}

/* sb_indent writes a newline followed by `depth` copies of the indent unit —
   the pretty-print gap JSON.stringify(x, null, space) inserts before each
   nested element. Only called when an indent unit is active. */
static void sb_indent(Sb *b, const char *indent, int depth) {
    sb_ch(b, '\n');
    for (int i = 0; i < depth; i++) sb_cstr(b, indent);
}

/* err: 0 ok, 1 circular, 2 statically-typed value in a dynamic position.
   Returns 1 if a value was written, 0 if it must be skipped (undefined /
   funcRef in an object position). `indent` is the pretty-print unit (one
   level) or NULL/"" for compact output byte-identical to the pre-pretty path. */
static int stringify_val(Sb *b, long long tag, long long pay,
                         const char *indent, void **parents, int depth, int *err) {
    switch (tag) {
    case 0:
    case 1:
        sb_number(b, tag, pay);
        return 1;
    case 2:
        sb_json_string(b, (const char *)pay);
        return 1;
    case 3:
        sb_cstr(b, pay ? "true" : "false");
        return 1;
    case 4:
        sb_cstr(b, "null");
        return 1;
    case 5:
    case 8:
    case 9:
    case 12:
        return 0; /* undefined / function-ish: skipped (object) or null (array) */
    case 10:
    case 11:
        break;
    default:
        *err = 2; /* tag 6/7: no runtime shape to walk */
        return 0;
    }
    if (depth >= KML_DYN_MAX_DEPTH) {
        *err = 1;
        return 0;
    }
    for (int i = 0; i < depth; i++) {
        if (parents[i] == (void *)pay) {
            *err = 1; /* Converting circular structure to JSON */
            return 0;
        }
    }
    parents[depth] = (void *)pay;
    int pretty = indent && indent[0];
    if (tag == 11) {
        char *a = (char *)pay;
        sb_ch(b, '[');
        long long n = arr_len(a);
        for (long long i = 0; i < n; i++) {
            if (i) sb_ch(b, ',');
            if (pretty) sb_indent(b, indent, depth + 1);
            if (!stringify_val(b, arr_tag(a, i), arr_pay(a, i), indent, parents, depth + 1, err)) {
                if (*err) return 0;
                sb_cstr(b, "null"); /* array holes/undefined → null */
            }
        }
        if (pretty && n) sb_indent(b, indent, depth);
        sb_ch(b, ']');
        return 1;
    }
    char *o = (char *)pay;
    /* Stage 7: a Proxy header (flag 1<<33) forwards to its target. */
    while (*(long long *)o & (1LL << 33)) o = *(char **)(o + 8);
    sb_ch(b, '{');
    long long n = obj_count(o);
    int wrote = 0;
    for (long long i = 0; i < n; i++) {
        /* Stage 5: skip non-ENUMERABLE entries; an ACCESSOR entry's value
           comes from its getter (JSON.stringify invokes getters), called
           through the dynamic-function record with the receiver boxed
           (bag | dynobj kind bits) — decoded below via the NaN-box rules. */
        long long attrs = obj_attrs(o, i);
        if (!(attrs & 2)) continue;
        long long etag = obj_tag(o, i), epay = obj_pay(o, i);
        if (attrs & 8) {
            char *pair = (char *)epay;
            void *getter = *(void **)pair;
            if (!getter) continue; /* setter-only: undefined, skipped */
            long long (*fn)(void *, long long, long long, void *) =
                *(long long (**)(void *, long long, long long, void *))getter;
            void *genv = *(void **)((char *)getter + 8);
            long long word = fn(genv, (long long)o | 5, 0, 0);
            nb_decode(word, &etag, &epay);
        }
        Sb probe; /* value may be skippable — write to a probe first */
        sb_init(&probe);
        int ok = stringify_val(&probe, etag, epay, indent, parents, depth + 1, err);
        if (*err) {
            free(probe.d);
            return 0;
        }
        if (ok) {
            if (wrote) sb_ch(b, ',');
            if (pretty) sb_indent(b, indent, depth + 1);
            sb_json_string(b, obj_key(o, i));
            sb_cstr(b, pretty ? ": " : ":");
            sb_raw(b, probe.d, probe.len);
            wrote = 1;
        }
        free(probe.d);
    }
    if (pretty && wrote) sb_indent(b, indent, depth);
    sb_ch(b, '}');
    return 1;
}

/* __kml_dynjson_stringify serializes one boxed dynamic value (tag passed
   widened to long long — the arm64 ABI wants the caller to extend sub-32-bit
   arguments, which the IR call site does not guarantee for i8). `indent` is the
   pretty-print unit (JSON.stringify's `space`) or NULL/"" for compact output.
   NULL result with err==0 means the JS result is undefined (top-level
   undefined/function).
   err: 1 = circular structure, 2 = statically-typed value in the tree. */
char *__kml_dynjson_stringify(long long tag, long long pay,
                              const char *indent, int *err) {
    void *parents[KML_DYN_MAX_DEPTH];
    *err = 0;
    Sb b;
    sb_init(&b);
    int ok = stringify_val(&b, tag, pay, indent, parents, 0, err);
    if (*err || !ok) {
        free(b.d);
        return NULL;
    }
    return sb_finish(&b);
}

/* ---- Array toString (String(arr) / `${arr}` / console.log) ---- */

static void join_val(Sb *b, long long tag, long long pay, int depth);

static void join_arr(Sb *b, char *a, int depth) {
    if (depth >= KML_DYN_MAX_DEPTH) return;
    long long n = arr_len(a);
    for (long long i = 0; i < n; i++) {
        if (i) sb_ch(b, ',');
        join_val(b, arr_tag(a, i), arr_pay(a, i), depth + 1);
    }
}

static void join_val(Sb *b, long long tag, long long pay, int depth) {
    char tmp[40];
    switch (tag) {
    case 0:
        snprintf(tmp, sizeof tmp, "%lld", pay);
        sb_cstr(b, tmp);
        break;
    case 1: {
        double d;
        memcpy(&d, &pay, 8);
        __kml_dtoa(tmp, d);
        sb_cstr(b, tmp);
        break;
    }
    case 2:
        sb_cstr(b, (const char *)pay);
        break;
    case 3:
        sb_cstr(b, pay ? "true" : "false");
        break;
    case 4:
    case 5:
        break; /* null/undefined join as empty, per Array.prototype.join */
    case 11:
        join_arr(b, (char *)pay, depth);
        break;
    case 7:
        sb_cstr(b, "[object Array]");
        break;
    default:
        sb_cstr(b, "[object Object]");
        break;
    }
}

/* __kml_dynarr_join renders a dynamic array the way JS Array toString does:
   elements joined with ",", null/undefined empty, nested arrays flattened
   through their own join. Returns a length-prefixed heap string. */
char *__kml_dynarr_join(char *a) {
    Sb b;
    sb_init(&b);
    join_arr(&b, a, 0);
    return sb_finish(&b);
}
