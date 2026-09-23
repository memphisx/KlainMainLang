// dynjson.c — JSON.stringify and Array-toString for D1 dynamic values
// (KlainMainLang TDD-00155 Stage 2). Self-contained walker over the dynamic
// object/array heap layouts; libc + __kml_dtoa only. Compiled alongside the
// program (the dtoa/json embedded-C pattern) only when a program stringifies
// a dynamic value.
//
// LAYOUT CONTRACTS (must stay in sync with the emitting runtime):
//   Dynamic object bag (runtime_dynobj.go):
//     header: [0]=i64 flags  [8]=ptr proto  [16]=ptr props  [24]=i64 count  [32]=i64 cap  [40]=i64 classtag
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
/* util.inspect layout + quoting (inspectsrc/inspect_reduce.c, ADR-01067):
   entries collected into a list, laid out on one line or one per line. */
#define KML_INSPECT_MAX_ARRAY 100 /* util.inspect's maxArrayLength default */
extern void *__kml_inspect_begin(long long depth, long long nonempty);
extern void __kml_inspect_push(void *l, char *entry);
extern void __kml_inspect_push_more(void *l, long long remaining);
extern char *__kml_inspect_end(void *l, const char *open, const char *close,
                               long long indent, long long depth, long long is_array, long long numeric);
extern char *__kml_inspect_quote(const char *s);

#define KML_DYN_MAX_DEPTH 512

/* ---- layout readers ---- */

static long long obj_count(char *o) { return *(long long *)(o + 24); }
static char *obj_props(char *o) { return *(char **)(o + 16); }
static char *obj_key(char *o, long long i) { return *(char **)(obj_props(o) + i * 32); }
static long long obj_tag(char *o, long long i) { return *(long long *)(obj_props(o) + i * 32 + 8); }
static long long obj_pay(char *o, long long i) { return *(long long *)(obj_props(o) + i * 32 + 16); }
static long long obj_attrs(char *o, long long i) { return *(long long *)(obj_props(o) + i * 32 + 24); }

/* es_is_index reports whether key `k` is a canonical ES array index — the
   decimal string of an integer in [0, 2^32-1), no leading zero — storing its
   value in *out. Drives own-property enumeration order (JSON.stringify here,
   mirroring __kml_dynobj_is_index in runtime_dynobj.go). */
static int es_is_index(const char *k, unsigned long long *out) {
    if (!k) return 0;
    size_t len = strlen(k);
    if (len == 0 || len > 10) return 0;
    if (k[0] == '0' && len > 1) return 0;
    unsigned long long v = 0;
    for (size_t i = 0; i < len; i++) {
        if (k[i] < '0' || k[i] > '9') return 0;
        v = v * 10 + (unsigned long long)(k[i] - '0');
    }
    if (v >= 4294967295ULL) return 0;
    *out = v;
    return 1;
}

/* es_order fills `out` (capacity n) with entry indices of object `o` in ES
   own-property enumeration order: array-index keys ascending numeric first,
   then every other key in insertion order. Returns the count written (== n).
   O(n^2) but n is a property count; needs no scratch/taken array (each pass
   selects the smallest index strictly greater than the last emitted). */
static long long es_order(char *o, long long n, long long *out) {
    long long w = 0, lastVal = -1;
    for (;;) {
        long long bestPos = -1;
        unsigned long long bestVal = 0;
        for (long long i = 0; i < n; i++) {
            unsigned long long v;
            if (es_is_index(obj_key(o, i), &v) && (long long)v > lastVal) {
                if (bestPos < 0 || v < bestVal) { bestPos = i; bestVal = v; }
            }
        }
        if (bestPos < 0) break;
        out[w++] = bestPos;
        lastVal = (long long)bestVal;
    }
    for (long long i = 0; i < n; i++) {
        unsigned long long v;
        if (!es_is_index(obj_key(o, i), &v)) out[w++] = i;
    }
    return w;
}

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

/* JSON string escaping per ECMAScript QuoteJSONString. strlen-bounded: not
   every runtime string carries a length header (sidecar-produced strings
   don't), so an embedded NUL still ends the string (BACKLOG). */
static void sb_json_bytes(Sb *b, const char *s, long long n) {
    sb_ch(b, '"');
    for (const unsigned char *p = (const unsigned char *)s, *e = p + n; p < e; p++) {
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
static void sb_json_string(Sb *b, const char *s) { sb_json_bytes(b, s, (long long)strlen(s)); }

/* A boxed STATIC array (tag 7) — the walkers are defined with the box layout
   further down (ADR-01059); stringify needs them first. */
struct KjBoxS;
static void kj_box_info(const void *box, long long *len, int *kind, int *typed);
static long long kj_elem_box(const struct KjBoxS *b, long long i);

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
    case 6: {
        /* A boxed Error (field-0 type-id flag, KlainMainLang TDD-00222) has no
           enumerable own properties, so JSON.stringify(new Error(...)) is "{}"
           — matching Node. Any other boxed object (a plain class instance) has
           no runtime shape to walk here. */
        if (pay && (*(long long *)pay & (1LL << 48))) {
            sb_cstr(b, "{}");
            return 1;
        }
        *err = 2;
        return 0;
    }
    case 7: {
        /* A boxed STATIC array (ADR-01059): a plain array serializes as a JSON
           array; a TypedArray, per JSON.stringify's own rules, as an object of
           its index keys (`{"0":8,"1":9}`). An element kind the box could not
           describe has no walkable shape → err 2 (the pre-existing rejection). */
        long long n;
        int kind, typed;
        kj_box_info((const void *)pay, &n, &kind, &typed);
        if (kind < 0) {
            *err = 2;
            return 0;
        }
        int pretty7 = indent && indent[0];
        char key[32];
        sb_ch(b, typed ? '{' : '[');
        for (long long i = 0; i < n; i++) {
            long long etag, epay;
            nb_decode(kj_elem_box((const struct KjBoxS *)pay, i), &etag, &epay);
            if (i) sb_ch(b, ',');
            if (pretty7) sb_indent(b, indent, depth + 1);
            if (typed) {
                snprintf(key, sizeof key, "%lld", i);
                sb_json_bytes(b, key, (long long)strlen(key));
                sb_cstr(b, pretty7 ? ": " : ":");
            }
            if (!stringify_val(b, etag, epay, indent, parents, depth + 1, err)) {
                if (*err) return 0;
                sb_cstr(b, "null"); /* an undefined element → null */
            }
        }
        if (pretty7 && n) sb_indent(b, indent, depth);
        sb_ch(b, typed ? '}' : ']');
        return 1;
    }
    case 10:
    case 11:
        break;
    default:
        *err = 2; /* funcRef/stream: no runtime shape to walk */
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
    /* Own-property keys serialize in ES enumeration order (array-index keys
       ascending first, then insertion order) — the same order Object.keys /
       for...in use (runtime_dynobj.go). order[] maps output position → entry. */
    long long *order = (n > 0) ? (long long *)malloc((size_t)n * sizeof(long long)) : NULL;
    if (order) n = es_order(o, n, order);
    int wrote = 0;
    for (long long oi = 0; oi < n; oi++) {
        long long i = order ? order[oi] : oi;
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
            free(order);
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
    free(order);
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
/* A boxed STATIC array (tag 7) nested in a dynamic value renders as itself
   through the box walkers defined below (ADR-01059). */
struct KjBoxS;
static void kj_join(Sb *b, const struct KjBoxS *bx, int depth);
static void kj_inspect(Sb *b, const struct KjBoxS *bx, int depth);

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
        kj_join(b, (const struct KjBoxS *)pay, depth);
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

/* ---- Array util.inspect form (console.log) — TDD-00212 Stage 2 ---- */
/* The console.log rendering of a dynamic (heterogeneous / empty / nested) array:
   `[ 1, 'hi', true ]`, a space inside the brackets, `, ` between elements,
   strings SINGLE-QUOTED, null/undefined printed literally (not the empty join
   form), nested arrays recursing into the same bracket form. `[]` when empty.
   Distinct from __kml_dynarr_join, which is the flat String() comma join. */

static void inspect_val(Sb *b, long long tag, long long pay, int depth);

/* key_is_ident: an inspect key prints bare when it is a valid JS identifier
   (`{ a: 1 }`), single-quoted otherwise (`{ 'a-b': 1 }`) — util.inspect's rule. */
static int key_is_ident(const char *k) {
    if (!k || !*k) return 0;
    if (!((k[0] >= 'a' && k[0] <= 'z') || (k[0] >= 'A' && k[0] <= 'Z') || k[0] == '_' || k[0] == '$'))
        return 0;
    for (const char *p = k + 1; *p; p++) {
        if (!((*p >= 'a' && *p <= 'z') || (*p >= 'A' && *p <= 'Z') ||
              (*p >= '0' && *p <= '9') || *p == '_' || *p == '$'))
            return 0;
    }
    return 1;
}

/* inspect_obj renders a dynamic object the way util.inspect (console.log)
   does: `{ a: 1, b: 'x' }`, `{}` when empty, keys in ES enumeration order,
   non-enumerable entries skipped, accessors shown as [Getter]/[Setter]
   (never invoked), and — Node's default depth — anything nested deeper than
   two object levels collapsed to [Object]. */
static void inspect_obj(Sb *b, char *o, int depth) {
    /* A Proxy header (flag 1<<33) forwards to its target, as JSON does. */
    while (*(long long *)o & (1LL << 33)) o = *(char **)(o + 8);
    long long n = obj_count(o);
    long long *order = (n > 0) ? (long long *)malloc((size_t)n * sizeof(long long)) : NULL;
    if (order) n = es_order(o, n, order);
    long long visible = 0;
    for (long long oi = 0; oi < n; oi++) {
        long long i = order ? order[oi] : oi;
        if (obj_attrs(o, i) & 2) visible++;
    }
    /* Node prints an empty object as `{}` even past the depth cap. */
    if (visible == 0) { free(order); sb_cstr(b, "{}"); return; }
    if (depth > 2) { free(order); sb_cstr(b, "[Object]"); return; }
    void *list = __kml_inspect_begin(depth, 1);
    for (long long oi = 0; oi < n; oi++) {
        long long i = order ? order[oi] : oi;
        long long attrs = obj_attrs(o, i);
        if (!(attrs & 2)) continue; /* non-enumerable: hidden from inspect */
        Sb eb;
        sb_init(&eb);
        const char *k = obj_key(o, i);
        if (key_is_ident(k)) {
            sb_cstr(&eb, k);
        } else {
            sb_ch(&eb, '\'');
            sb_cstr(&eb, k ? k : "");
            sb_ch(&eb, '\'');
        }
        sb_cstr(&eb, ": ");
        if (attrs & 8) {
            char *pair = (char *)obj_pay(o, i);
            void *getter = *(void **)pair;
            void *setter = *(void **)(pair + 8);
            sb_cstr(&eb, getter && setter ? "[Getter/Setter]" : (getter ? "[Getter]" : "[Setter]"));
        } else {
            inspect_val(&eb, obj_tag(o, i), obj_pay(o, i), depth + 1);
        }
        __kml_inspect_push(list, sb_finish(&eb));
    }
    free(order);
    char *out = __kml_inspect_end(list, "{", "}", 2 * depth, depth, 0, 0);
    sb_cstr(b, out);
    free(out - 8);
}

char *__kml_dynobj_inspect_at(char *o, long long depth) {
    Sb b;
    sb_init(&b);
    inspect_obj(&b, o, (int)depth);
    return sb_finish(&b);
}
char *__kml_dynobj_inspect(char *o) { return __kml_dynobj_inspect_at(o, 0); }

static void inspect_arr(Sb *b, char *a, int depth) {
    long long n = arr_len(a);
    if (n == 0) { sb_cstr(b, "[]"); return; }
    if (depth > 2) { sb_cstr(b, "[Array]"); return; }
    void *list = __kml_inspect_begin(depth, 1);
    long long numeric = 1;
    long long shown = n < KML_INSPECT_MAX_ARRAY ? n : KML_INSPECT_MAX_ARRAY;
    for (long long i = 0; i < shown; i++) {
        long long tag = arr_tag(a, i);
        if (tag != 0 && tag != 1) numeric = 0;
        Sb eb;
        sb_init(&eb);
        inspect_val(&eb, tag, arr_pay(a, i), depth + 1);
        __kml_inspect_push(list, sb_finish(&eb));
    }
    if (n > shown) __kml_inspect_push_more(list, n - shown);
    char *out = __kml_inspect_end(list, "[", "]", 2 * depth, depth, 1, numeric);
    sb_cstr(b, out);
    free(out - 8);
}

static void inspect_val(Sb *b, long long tag, long long pay, int depth) {
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
    case 2: {
        char *q = __kml_inspect_quote((const char *)pay);
        sb_cstr(b, q);
        free(q - 8);
        break;
    }
    case 3:
        sb_cstr(b, pay ? "true" : "false");
        break;
    case 4:
        sb_cstr(b, "null"); /* inspect shows null/undefined literally */
        break;
    case 5:
        sb_cstr(b, "undefined");
        break;
    case 10:
        inspect_obj(b, (char *)pay, depth);
        break;
    case 11:
        inspect_arr(b, (char *)pay, depth);
        break;
    case 12:
        sb_cstr(b, "[Function (anonymous)]");
        break;
    case 7:
        kj_inspect(b, (const struct KjBoxS *)pay, depth);
        break;
    default:
        sb_cstr(b, "[Object]");
        break;
    }
}

char *__kml_dynarr_inspect_at(char *a, long long depth) {
    Sb b;
    sb_init(&b);
    inspect_arr(&b, a, (int)depth);
    return sb_finish(&b);
}
char *__kml_dynarr_inspect(char *a) { return __kml_dynarr_inspect_at(a, 0); }

/* ---- A STATICALLY-TYPED array boxed into `any` (TDD-00212, ADR-01059) ----
   The box (anyArrayBoxTy in emit_dynamic.go, `{ ptr, i8, i8 }`) holds the LIVE
   array header ({data, len} — the cell the source array mutates through), the
   element-kind byte the walker strides/formats by, and a typed-array byte:
   0 = plain array, 1 = TypedArray, 2 = Uint8ClampedArray. Unlike the dynamic
   array above, the elements are raw (not boxed). A negative `kind` means the
   element kind was not representable at box time (nested array / object /
   Map / …), in which case the honest `[object Array]` / `[Array]` stand-in is
   rendered and an element read is refused by the caller. Keep `kind` in sync
   with arrayElemKind (emit_dynamic.go). */
enum {
    KJ_F64 = 0, KJ_F32 = 1,
    KJ_I64 = 2, KJ_U64 = 3, KJ_I32 = 4, KJ_U32 = 5,
    KJ_I16 = 6, KJ_U16 = 7, KJ_I8 = 8, KJ_U8 = 9,
    KJ_BOOL = 10, KJ_STRING = 11,
    KJ_ANY = 12,  /* each element is itself a NaN-boxed word (`any[]`) */
    KJ_ARRAY = 13 /* each element is an array header pointer (`T[][]`), its own
                     kind/typed described by the box's inner bytes */
};

typedef struct KjBoxS {
    char *hdr;          /* arrayHeaderTy: [0]=ptr data  [8]=i64 len */
    signed char kind;   /* KJ_* or -1 */
    signed char typed;  /* 0 plain, 1 TypedArray, 2 Uint8ClampedArray */
    signed char ikind;  /* KJ_ARRAY only: the nested arrays' element kind */
    signed char ityped; /* KJ_ARRAY only: the nested arrays' typed byte */
} KjBox;


static char *kj_data(const KjBox *b) { return *(char **)b->hdr; }
static long long kj_len(const KjBox *b) { return *(long long *)(b->hdr + 8); }
static void kj_box_info(const void *box, long long *len, int *kind, int *typed) {
    const KjBox *b = (const KjBox *)box;
    *len = kj_len(b);
    *kind = b->kind;
    *typed = b->typed;
}

/* kj_inner fills a box describing nested element i of a KJ_ARRAY box; false
   when that element is absent (a null header). */
static int kj_inner(const KjBox *b, long long i, KjBox *out) {
    char *h = ((char **)kj_data(b))[i];
    if (!h) return 0;
    out->hdr = h;
    out->kind = b->ikind;
    out->typed = b->ityped;
    out->ikind = -1;
    out->ityped = 0;
    return 1;
}

/* The `Int32Array(3) ` prefix util.inspect puts before a TypedArray. */
static const char *kj_typed_name(const KjBox *b) {
    if (b->typed == 2) return "Uint8ClampedArray";
    switch (b->kind) {
    case KJ_F64: return "Float64Array";
    case KJ_F32: return "Float32Array";
    case KJ_I64: return "BigInt64Array";
    case KJ_U64: return "BigUint64Array";
    case KJ_I32: return "Int32Array";
    case KJ_U32: return "Uint32Array";
    case KJ_I16: return "Int16Array";
    case KJ_U16: return "Uint16Array";
    case KJ_I8: return "Int8Array";
    case KJ_U8: return "Uint8Array";
    default: return "TypedArray";
    }
}

/* nb_double / nb_pack mirror __kml_nb_pack (runtime_nanbox.go — keep in sync):
   a number is its canonical-NaN double bits + 2^49; immediates undefined=10,
   null=2, false=6, true=7; a pointer carries its kind in the low 3 bits. */
static long long nb_double(double d) {
    unsigned long long bits;
    if (d != d) bits = 0x7FF8000000000000ULL;
    else memcpy(&bits, &d, 8);
    return (long long)(bits + (1ULL << 49));
}
static long long nb_pack(long long tag, long long pay) {
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

/* kj_elem_box reads element i of a boxed static array as a NaN-boxed word —
   a number widens to a double (a JS number IS a double), a string is its own
   pointer, a hole in a string array is undefined. The caller has already
   refused kind < 0. */
static long long kj_elem_box(const KjBox *b, long long i) {
    char *d = kj_data(b);
    switch (b->kind) {
    case KJ_F64: return nb_double(((double *)d)[i]);
    case KJ_F32: return nb_double((double)((float *)d)[i]);
    case KJ_I64: return nb_double((double)((long long *)d)[i]);
    case KJ_U64: return nb_double((double)((unsigned long long *)d)[i]);
    case KJ_I32: return nb_double((double)((int *)d)[i]);
    case KJ_U32: return nb_double((double)((unsigned int *)d)[i]);
    case KJ_I16: return nb_double((double)((short *)d)[i]);
    case KJ_U16: return nb_double((double)((unsigned short *)d)[i]);
    case KJ_I8: return nb_double((double)((signed char *)d)[i]);
    case KJ_U8: return nb_double((double)((unsigned char *)d)[i]);
    case KJ_BOOL: return ((signed char *)d)[i] ? 7 : 6;
    case KJ_STRING: { char *s = ((char **)d)[i]; return s ? (long long)s : 10; }
    case KJ_ANY: return ((long long *)d)[i];
    case KJ_ARRAY: {
        KjBox *nb = (KjBox *)malloc(sizeof(KjBox));
        if (!kj_inner(b, i, nb)) { free(nb); return 10; }
        return (long long)nb | 2; /* an array-box word (kind bits 2) */
    }
    default: return 10;
    }
}

/* __kml_anyarr_get_by_key: `x[k]` / `x.k` on an `any` holding a boxed static
   array — "length" answers the LIVE header length, a canonical index answers
   the element (undefined past the end, as JS), anything else is undefined. An
   in-range index into an element kind the box could not describe answers the
   otherwise-unused immediate 1: the emitter turns that into a TypeError
   rather than inventing an `undefined`. */
long long __kml_anyarr_get_by_key(void *box, const char *key) {
    KjBox *b = (KjBox *)box;
    if (strcmp(key, "length") == 0) return nb_double((double)kj_len(b));
    char *end;
    long long idx = strtoll(key, &end, 10);
    if (end == key || *end != 0 || idx < 0) return 10;
    if (idx >= kj_len(b)) return 10;
    if (b->kind < 0) return 1;
    return kj_elem_box(b, idx);
}

extern long long __kml_toprimitive(long long v, _Bool strHint);
extern double __kml_any_tonum(long long v);
extern long long __kml_dynobj_get(char *o, const char *key);

static double any_elem_tonum(long long word) {
    return __kml_any_tonum(__kml_toprimitive(word, 0));
}

/* __kml_any_arraylike_f64 materialises an array-like `any` as a fresh double
   buffer with ToNumber applied per element — the source of
   %TypedArray%.prototype.set(any) (ADR-01059). Per the spec's ToObject +
   LengthOfArrayLike walk: a boxed static array or dynamic array yields its
   elements; a string yields its characters (digits → their value, the rest
   NaN); a dynamic object is walked by its `length` and index keys; a number /
   boolean / function is an object with no `length` → zero elements. null and
   undefined cannot be converted to an object: *outLen = -1, NULL — the caller
   throws the TypeError. */
double *__kml_any_arraylike_f64(long long word, long long *outLen) {
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    long long n = 0;
    double *buf = NULL;
    switch (tag) {
    case 4:
    case 5:
        *outLen = -1;
        return NULL;
    case 7: {
        KjBox *b = (KjBox *)pay;
        n = kj_len(b);
        buf = (double *)malloc((size_t)(n > 0 ? n : 1) * sizeof(double));
        for (long long i = 0; i < n; i++)
            buf[i] = b->kind < 0 ? (0.0 / 0.0) : any_elem_tonum(kj_elem_box(b, i));
        break;
    }
    case 11: {
        char *a = (char *)pay;
        n = arr_len(a);
        buf = (double *)malloc((size_t)(n > 0 ? n : 1) * sizeof(double));
        for (long long i = 0; i < n; i++)
            buf[i] = any_elem_tonum(nb_pack(arr_tag(a, i), arr_pay(a, i)));
        break;
    }
    case 2: {
        const char *s = (const char *)pay;
        n = *(const long long *)(s - 8);
        buf = (double *)malloc((size_t)(n > 0 ? n : 1) * sizeof(double));
        for (long long i = 0; i < n; i++)
            buf[i] = (s[i] >= '0' && s[i] <= '9') ? (double)(s[i] - '0') : (0.0 / 0.0);
        break;
    }
    case 10: {
        char *o = (char *)pay;
        double lenD = any_elem_tonum(__kml_dynobj_get(o, "length"));
        if (!(lenD > 0)) lenD = 0; /* NaN / negative → 0 (ToLength) */
        if (lenD > 9007199254740991.0) lenD = 9007199254740991.0;
        n = (long long)lenD;
        buf = (double *)malloc((size_t)(n > 0 ? n : 1) * sizeof(double));
        char key[32];
        for (long long i = 0; i < n; i++) {
            snprintf(key, sizeof key, "%lld", i);
            buf[i] = any_elem_tonum(__kml_dynobj_get(o, key));
        }
        break;
    }
    default:
        buf = (double *)malloc(sizeof(double));
        break;
    }
    *outLen = n;
    return buf;
}

/* jsNumToStr writes d with JS Number.prototype.toString semantics — note this
   differs from JSON (NaN/Infinity print literally, not as null). */
static void jsNumToStr(Sb *b, double d) {
    char tmp[40];
    if (d != d) { sb_cstr(b, "NaN"); return; }
    if (d > 1.7976931348623157e308) { sb_cstr(b, "Infinity"); return; }
    if (d < -1.7976931348623157e308) { sb_cstr(b, "-Infinity"); return; }
    __kml_dtoa(tmp, d);
    sb_cstr(b, tmp);
}

/* kj_elem_render writes element i of a boxed static array: numbers/bools as
   JS formats them; a string bare (join) or single-quoted (inspect); an `any`
   element through the dynamic walkers, so a nested box renders as itself. */
static void kj_elem_render(Sb *b, const KjBox *bx, long long i, int inspect, int depth) {
    char *data = kj_data(bx);
    char tmp[40];
    switch (bx->kind) {
    case KJ_F64: jsNumToStr(b, ((double *)data)[i]); break;
    case KJ_F32: jsNumToStr(b, (double)((float *)data)[i]); break;
    case KJ_I64: snprintf(tmp, sizeof tmp, "%lld", ((long long *)data)[i]); sb_cstr(b, tmp); break;
    case KJ_U64: snprintf(tmp, sizeof tmp, "%llu", ((unsigned long long *)data)[i]); sb_cstr(b, tmp); break;
    case KJ_I32: snprintf(tmp, sizeof tmp, "%d", ((int *)data)[i]); sb_cstr(b, tmp); break;
    case KJ_U32: snprintf(tmp, sizeof tmp, "%u", ((unsigned int *)data)[i]); sb_cstr(b, tmp); break;
    case KJ_I16: snprintf(tmp, sizeof tmp, "%d", (int)((short *)data)[i]); sb_cstr(b, tmp); break;
    case KJ_U16: snprintf(tmp, sizeof tmp, "%u", (unsigned int)((unsigned short *)data)[i]); sb_cstr(b, tmp); break;
    case KJ_I8: snprintf(tmp, sizeof tmp, "%d", (int)((signed char *)data)[i]); sb_cstr(b, tmp); break;
    case KJ_U8: snprintf(tmp, sizeof tmp, "%u", (unsigned int)((unsigned char *)data)[i]); sb_cstr(b, tmp); break;
    case KJ_BOOL: sb_cstr(b, ((signed char *)data)[i] ? "true" : "false"); break;
    case KJ_STRING: {
        char *s = ((char **)data)[i];
        if (inspect) {
            if (s) { char *q = __kml_inspect_quote(s); sb_cstr(b, q); free(q - 8); }
            else sb_cstr(b, "''");
        } else if (s) sb_cstr(b, s); /* a null element joins as empty */
        break;
    }
    case KJ_ANY: {
        long long tag, pay;
        nb_decode(((long long *)data)[i], &tag, &pay);
        if (inspect) inspect_val(b, tag, pay, depth + 1);
        else join_val(b, tag, pay, depth + 1);
        break;
    }
    case KJ_ARRAY: {
        KjBox inner;
        if (!kj_inner(bx, i, &inner)) { if (inspect) sb_cstr(b, "undefined"); break; }
        if (inspect) kj_inspect(b, &inner, depth + 1);
        else kj_join(b, &inner, depth + 1);
        break;
    }
    default: break;
    }
}

/* __kml_array_join renders a boxed static array the way JS
   Array.prototype.toString does — elements joined with ",", a TypedArray the
   same (its toString is Array.prototype.toString). A negative kind renders
   the honest `[object Array]` stand-in. Returns a length-prefixed heap
   string. */
static void kj_join(Sb *b, const KjBox *bx, int depth) {
    if (bx->kind < 0 || depth >= KML_DYN_MAX_DEPTH) { sb_cstr(b, "[object Array]"); return; }
    long long len = kj_len(bx);
    for (long long i = 0; i < len; i++) {
        if (i > 0) sb_ch(b, ',');
        kj_elem_render(b, bx, i, 0, depth);
    }
}

char *__kml_array_join(void *box) {
    Sb b;
    sb_init(&b);
    kj_join(&b, (const KjBox *)box, 0);
    return sb_finish(&b);
}

/* __kml_array_inspect renders the same STATICALLY-TYPED any-boxed array
   (TDD-00212) the way Node's util.inspect / console.log does: `[ 1, 2, 3 ]`
   (a space inside the brackets, `, ` between elements), with string elements
   SINGLE-QUOTED (`[ 'x', 'y' ]`) — the top-level console.log form, distinct from
   the comma-join String() form above. An empty array is `[]`; a negative `kind`
   (element kind not representable at box time) yields the `[Array]` placeholder,
   matching util.inspect's depth behaviour. Numbers/bools format exactly as the
   join helper does. Returns a length-prefixed heap string. */
static void kj_inspect(Sb *b, const KjBox *bx, int depth) {
    if (bx->kind < 0 || depth >= KML_DYN_MAX_DEPTH) {
        /* util.inspect's depth placeholder; a TypedArray of an unrenderable
           kind (BigInt64Array) still names itself. */
        if (bx->typed) { sb_cstr(b, kj_typed_name(bx)); sb_cstr(b, " [Array]"); }
        else sb_cstr(b, "[Array]");
        return;
    }
    long long len = kj_len(bx);
    char open[64] = "[";
    if (bx->typed) {
        /* Node: `Int32Array(2) [ 8, 9 ]`, `Uint8Array(0) []`. */
        snprintf(open, sizeof open, "%s(%lld) [", kj_typed_name(bx), len);
    }
    if (len == 0) { sb_cstr(b, open); sb_cstr(b, "]"); return; }
    if (depth > 2) { sb_cstr(b, "[Array]"); return; }
    void *list = __kml_inspect_begin(depth, 1);
    long long shown = len < KML_INSPECT_MAX_ARRAY ? len : KML_INSPECT_MAX_ARRAY;
    for (long long i = 0; i < shown; i++) {
        Sb eb;
        sb_init(&eb);
        kj_elem_render(&eb, bx, i, 1, depth);
        __kml_inspect_push(list, sb_finish(&eb));
    }
    if (len > shown) __kml_inspect_push_more(list, len - shown);
    long long numeric = bx->kind != KJ_BOOL && bx->kind != KJ_STRING && bx->kind != KJ_ANY && bx->kind != KJ_ARRAY;
    char *out = __kml_inspect_end(list, open, "]", 2 * depth, depth, 1, numeric);
    sb_cstr(b, out);
    free(out - 8);
}

char *__kml_array_inspect_at(void *box, long long depth) {
    Sb b;
    sb_init(&b);
    kj_inspect(&b, (const KjBox *)box, (int)depth);
    return sb_finish(&b);
}
char *__kml_array_inspect(void *box) { return __kml_array_inspect_at(box, 0); }
