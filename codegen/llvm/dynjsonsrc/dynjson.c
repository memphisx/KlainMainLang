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
#include <math.h>
#include <stdio.h>

extern char **__kml_dynobj_keys_enum_c(void *o, long long *n);
extern void __kml_dtoa(char *buf, double v);
/* A runtime string's length: its header word. */
static long long kj_str_len(const char *s) { return KML_STR_LEN(s); }
extern char *__kml_fn_inspect_dyn(void **rec, long long depth); /* fnmeta.c (TDD-00229) */
extern const char *__kml_fn_tag_dyn(void **rec);
/* A boxed promise's state and value/reason (emitPromiseInspectHook); -1 for
   another object. */
extern long long __kml_promise_inspect_parts(void *o, long long *out);
/* A boxed Error's `Name: message` (emitErrorInspectHook). */
extern char *__kml_error_inspect(void *o);
extern char *__kml_obj_inspect_custom(void *o, long long depth);
extern char *__kml_obj_tostring_tag(void *o);
/* A boxed bigint is a { magic, bigint* } cell (emit_bigint_box.go); its
   digits come from an IR hook that exists whether or not the program links
   the bigint runtime (TDD-00229). */
#define KML_BOXED_BIGINT_MAGIC 0x7FF40000B1616B16LL
extern char *__kml_boxed_bigint_str(void *cell);
static int is_boxed_bigint(long long pay) {
    return pay && *(long long *)pay == KML_BOXED_BIGINT_MAGIC;
}
/* util.inspect layout + quoting (inspectsrc/inspect_reduce.c, ADR-01067):
   entries collected into a list, laid out on one line or one per line. */
#define KML_INSPECT_MAX_ARRAY __kml_inspect_opt_maxarr /* util.inspect's maxArrayLength (100) */
/* A static object's layout-table row (KlainMainLang TDD-00233), through
   hooks code generation defines at finalize: -1 keys when the program has
   no layout table or the object no row. A host box renders through its own
   routine. */
extern long long __kml_obj_nkeys(void *o);
extern const char *__kml_obj_key(void *o, long long i);
extern long long __kml_obj_get(void *o, const char *key);
extern int __kml_obj_has(void *o, const char *key);
extern const char *__kml_obj_name(void *o);
extern char *__kml_host_inspect(void *cell, long long depth);
extern long long __kml_inspect_opt_compact, __kml_inspect_opt_sorted, __kml_inspect_opt_depth, __kml_inspect_opt_break, __kml_inspect_opt_maxarr, __kml_inspect_opt_custom;
/* A host box's toJSON result as a string (a Date's ISO form), or 0. */
extern char *__kml_host_tojson(void *cell);
/* hostbox.c's wrapper-object unwrap when the program boxes a host value;
 * this weak default (no wrapper exists) stands in otherwise. */
__attribute__((weak)) long long __kml_host_unwrap(long long v) { return v; }
/* A host box's Object.prototype.toString tag (`[object Map]`). */
extern char *__kml_host_class_tag(void *cell);
extern void *__kml_dynobj_get_proto(void *bag);
#define KML_HOST_HDR_MASK ((0x7FFFLL << 32) | (1LL << 49))
#define KML_HOST_HDR ((0x4B4DLL << 32) | (1LL << 49))
static int is_host_box(long long pay) {
    return pay && (*(long long *)pay & KML_HOST_HDR_MASK) == KML_HOST_HDR;
}
extern void *__kml_inspect_begin(long long depth, long long nonempty);
extern void __kml_inspect_push(void *l, char *entry);
extern void __kml_inspect_push_holes(void *l, long long n);
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
/* A hole's element tag (dynarr.c's DA_HOLE); every reader but inspect sees
   undefined. */
static long long arr_raw_tag(char *a, long long i) { return *(long long *)(arr_data(a) + i * 16); }
static long long arr_tag(char *a, long long i) {
    long long t = arr_raw_tag(a, i);
    return t == 13 ? 5 : t;
}
static long long arr_pay(char *a, long long i) { return *(long long *)(arr_data(a) + i * 16 + 8); }

/* ---- growable output buffer ---- */

typedef struct {
    char *d;
    long long len, cap;
} Sb;

static void sb_init(Sb *b) {
    b->cap = 64;
    b->len = 0;
    b->d = (char *)malloc((size_t)b->cap); /* scratch */
}

static void sb_need(Sb *b, long long extra) {
    if (b->len + extra + 1 <= b->cap) return;
    while (b->len + extra + 1 > b->cap) b->cap *= 2;
    b->d = (char *)realloc(b->d, (size_t)b->cap); /* scratch */
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
    char *base = (char *)kml_alloc(KML_HEAP_RAW, (size_t)(8 + b->len + 1));
    *(long long *)base = b->len;
    memcpy(base + 8, b->d, (size_t)b->len);
    base[8 + b->len] = 0;
    free(b->d); /* scratch */
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
            } else if (c == 0xED && e - p >= 3 && p[1] >= 0xA0) {
                /* A lone surrogate (WTF-8): well-formed JSON.stringify's \udXXX. */
                char tmp[8];
                snprintf(tmp, sizeof tmp, "\\u%04x", 0xD000 | (p[1] & 0x3F) << 6 | (p[2] & 0x3F));
                sb_cstr(b, tmp);
                p += 2;
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
struct KjBoxS;
static int kj_is_args(const struct KjBoxS *bx);
static long long kj_elem_box(const struct KjBoxS *b, long long i);
static const unsigned char *kj_bytes(const struct KjBoxS *bx);

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
/* kj_json_base is the nesting depth of the statically typed value a dynamic
   one is serialized inside (`JSON.stringify({ a: anyValue }, null, 2)`): it
   indents only, the cycle check counting from the dynamic value itself. */
static _Thread_local int kj_json_base;

static void sb_indent(Sb *b, const char *indent, int depth) {
    sb_ch(b, '\n');
    for (int i = 0; i < depth + kj_json_base; i++) sb_cstr(b, indent);
}

/* The walk's path, for a cycle's message: kj_pkey[d] / kj_pidx[d] is the key
   (or array index, kj_pkey NULL) the value at depth d was reached under, and
   kj_ptag[d] the kind of parents[d]. */
static _Thread_local const char *kj_pkey[KML_DYN_MAX_DEPTH + 1];
static _Thread_local long long kj_pidx[KML_DYN_MAX_DEPTH + 1];
static _Thread_local int kj_ptag[KML_DYN_MAX_DEPTH + 1];
static _Thread_local char *kj_circ_msg;
/* kj_tojson_done: the depth whose value is a toJSON result (-1 for none). */
static _Thread_local int kj_tojson_done = -1;
static long long nb_pack(long long tag, long long pay);

extern long long __kml_dynobj_get(char *o, const char *key);

/* A key a Proxy's trap may receive: a JS string, length-prefixed. */
static const struct { long long len; char s[7]; } kj_tojson_key = {KML_STR_ASCII_HDR(6), "toJSON"};

/* kj_tojson is `v.toJSON(key)` when v (a dynamic or static object) has a
   toJSON function — own, inherited or a class method — else v itself: a
   tag-12 record called as fn(env, this, argc, argv). */
static long long kj_tojson(long long tag, long long pay, long long w, const char *key) {
    long long f = tag == 10 ? __kml_dynobj_get((char *)pay, kj_tojson_key.s) : __kml_obj_get((void *)pay, kj_tojson_key.s);
    long long ft, fp;
    nb_decode(f, &ft, &fp);
    if (ft != 12 || !fp) return w;
    Sb ks; /* the key as a runtime string (length-headered) */
    sb_init(&ks);
    sb_cstr(&ks, key);
    long long argv[1] = {nb_pack(2, (long long)sb_finish(&ks))};
    void **rec = (void **)fp;
    long long (*fn)(void *, long long, long long, long long *) =
        (long long (*)(void *, long long, long long, long long *))rec[0];
    return fn(rec[1], w, 1, argv);
}

static void kj_key(int d, const char *k, long long i) {
    if (d > KML_DYN_MAX_DEPTH) return;
    kj_pkey[d] = k;
    kj_pidx[d] = i;
}

static const char *kj_typed_name_of(const void *box);
static void kj_ctor_name(Sb *m, int d, void **parents) {
    const char *name = "Object";
    switch (kj_ptag[d]) {
    case 11: name = "Array"; break;
    case 7: name = kj_typed_name_of(parents[d]); break;
    case 6: {
        const char *n = __kml_obj_name(parents[d]);
        if (n && n[0]) name = n;
        break;
    }
    }
    sb_ch(m, '\'');
    sb_cstr(m, name);
    sb_ch(m, '\'');
}

static void kj_key_text(Sb *m, int d) {
    char tmp[32];
    if (kj_pkey[d] == NULL) {
        snprintf(tmp, sizeof tmp, "index %lld", kj_pidx[d]);
        sb_cstr(m, tmp);
        return;
    }
    sb_cstr(m, "property '");
    sb_cstr(m, kj_pkey[d]);
    sb_ch(m, '\'');
}

static void kj_line(Sb *m, int d, void **parents) {
    sb_cstr(m, "\n    |     ");
    kj_key_text(m, d);
    sb_cstr(m, " -> object with constructor ");
    kj_ctor_name(m, d, parents);
}

/* V8's JsonStringifier::ConstructCircularStructureErrorMessage: the cycle
   from its start, the first two links after it, `...` for the elided middle,
   the last, and the key that closes it. */
static void kj_circular(void **parents, int start, int depth) {
    Sb m;
    sb_init(&m);
    sb_cstr(&m, "Converting circular structure to JSON\n    --> starting at object with constructor ");
    kj_ctor_name(&m, start, parents);
    int i = start + 1;
    int prefix_end = depth < i + 2 ? depth : i + 2;
    for (; i < prefix_end; i++) kj_line(&m, i, parents);
    if (depth > i + 1) sb_cstr(&m, "\n    |     ...");
    if (i < depth - 1) i = depth - 1;
    for (; i < depth; i++) kj_line(&m, i, parents);
    sb_cstr(&m, "\n    --- ");
    kj_key_text(&m, depth);
    sb_cstr(&m, " closes the circle");
    kj_circ_msg = sb_finish(&m);
}

/* __kml_dynjson_circ_msg: the message of the last walk's cycle. */
char *__kml_dynjson_circ_msg(void) { return kj_circ_msg; }

/* kj_enter puts object p (of kind tag) on the walk's path at depth: 0 with
   err 1 on a cycle (or a path past the depth limit). */
static int kj_enter(void *p, int tag, void **parents, int depth, int *err) {
    if (depth >= KML_DYN_MAX_DEPTH) {
        *err = 1;
        kj_circ_msg = NULL;
        return 0;
    }
    for (int i = 0; i < depth; i++) {
        if (parents[i] == p) {
            *err = 1;
            kj_circular(parents, i, depth);
            return 0;
        }
    }
    parents[depth] = p;
    kj_ptag[depth] = tag;
    return 1;
}

/* err: 0 ok, 1 circular, 2 statically-typed value in a dynamic position.
   Returns 1 if a value was written, 0 if it must be skipped (undefined /
   funcRef in an object position). `indent` is the pretty-print unit (one
   level) or NULL/"" for compact output byte-identical to the pre-pretty path. */
static int stringify_val(Sb *b, long long tag, long long pay,
                         const char *indent, void **parents, int depth, int *err);

/* stringify_static serializes a static object through its layout row: each
   key in declaration order, its value read by name. */
static int stringify_static(Sb *b, void *o, long long n, const char *indent,
                            void **parents, int depth, int *err) {
    if (!kj_enter(o, 6, parents, depth, err)) return 0;
    int pretty = indent && indent[0];
    int wrote = 0;
    sb_ch(b, '{');
    for (long long i = 0; i < n; i++) {
        const char *k = __kml_obj_key(o, i);
        if (!k || !__kml_obj_has(o, k)) continue;
        long long etag, epay;
        nb_decode(__kml_obj_get(o, k), &etag, &epay);
        kj_key(depth + 1, k, 0);
        Sb probe;
        sb_init(&probe);
        int ok = stringify_val(&probe, etag, epay, indent, parents, depth + 1, err);
        if (*err) {
            free(probe.d); /* scratch */
            return 0;
        }
        if (ok) {
            if (wrote) sb_ch(b, ',');
            if (pretty) sb_indent(b, indent, depth + 1);
            sb_json_string(b, k);
            sb_cstr(b, pretty ? ": " : ":");
            sb_raw(b, probe.d, probe.len);
            wrote = 1;
        }
        free(probe.d); /* scratch */
    }
    if (pretty && wrote) sb_indent(b, indent, depth);
    sb_ch(b, '}');
    return 1;
}

static int stringify_val(Sb *b, long long tag, long long pay,
                         const char *indent, void **parents, int depth, int *err) {
    /* An object's own or inherited toJSON(key) replaces it (a host box's —
       a Date's — is its tojson row, below). */
    if (kj_tojson_done != depth && (tag == 10 || (tag == 6 && pay && !is_host_box(pay) &&
                                                 !(*(long long *)pay & (1LL << 48))))) {
        char idx[32];
        const char *key = depth <= KML_DYN_MAX_DEPTH ? kj_pkey[depth] : "";
        if (!key) {
            snprintf(idx, sizeof idx, "%lld", kj_pidx[depth]);
            key = idx;
        }
        long long w = nb_pack(tag, pay);
        long long r = kj_tojson(tag, pay, w, depth == 0 ? "" : key);
        if (r != w) {
            long long rt, rp;
            nb_decode(r, &rt, &rp);
            /* toJSON applies once per property: its result is serialized
               as it is (its own properties still get theirs). */
            int saved = kj_tojson_done;
            kj_tojson_done = depth;
            int ok = stringify_val(b, rt, rp, indent, parents, depth, err);
            kj_tojson_done = saved;
            return ok;
        }
    }
    if (tag == 6 && is_host_box(pay)) {
        /* A wrapper object (`new Number(3)`) serializes its primitive. */
        long long w = nb_pack(tag, pay), u = __kml_host_unwrap(w);
        if (u != w) {
            long long ut, up;
            nb_decode(u, &ut, &up);
            return stringify_val(b, ut, up, indent, parents, depth, err);
        }
    }
    switch (tag) {
    case 0:
    case 1:
        sb_number(b, tag, pay);
        return 1;
    case 2:
        /* A string value: its length header bounds it, so an embedded NUL is
           escaped rather than ending it. */
        sb_json_bytes(b, (const char *)pay, kj_str_len((const char *)pay));
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
        /* JSON has no bigint: Node throws "Do not know how to serialize a
           BigInt" (err 3). */
        if (is_boxed_bigint(pay)) {
            *err = 3;
            return 0;
        }
        /* A boxed Error (field-0 type-id flag, KlainMainLang TDD-00222): its
           own enumerable properties only — an Error subclass's fields and the
           system fields set on it (__kml_obj_nkeys counts both), so
           JSON.stringify(new Error(...)) is "{}", as in Node. */
        if (pay && (*(long long *)pay & (1LL << 48))) {
            long long en = __kml_obj_nkeys((void *)pay);
            if (en <= 0) {
                sb_cstr(b, "{}");
                return 1;
            }
            return stringify_static(b, (void *)pay, en, indent, parents, depth, err);
        }
        if (is_host_box(pay)) {
            char *js = __kml_host_tojson((void *)pay);
            if (js) {
                sb_json_string(b, js);
                return 1;
            }
        }
        /* A static object with a layout-table row: its own enumerable keys
           (KlainMainLang TDD-00233); a host box has none. */
        long long sn = pay && !is_host_box(pay) ? __kml_obj_nkeys((void *)pay) : (is_host_box(pay) ? 0 : -1);
        if (sn >= 0)
            return stringify_static(b, (void *)pay, sn, indent, parents, depth, err);
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
        if (typed == 3) {
            /* A Buffer's toJSON: {"type":"Buffer","data":[…bytes]}. */
            const unsigned char *d = kj_bytes((const struct KjBoxS *)pay);
            sb_ch(b, '{');
            if (pretty7) sb_indent(b, indent, depth + 1);
            sb_cstr(b, pretty7 ? "\"type\": \"Buffer\"," : "\"type\":\"Buffer\",");
            if (pretty7) sb_indent(b, indent, depth + 1);
            sb_cstr(b, pretty7 ? "\"data\": [" : "\"data\":[");
            for (long long i = 0; i < n; i++) {
                if (i) sb_ch(b, ',');
                if (pretty7) sb_indent(b, indent, depth + 2);
                snprintf(key, sizeof key, "%d", d[i]);
                sb_cstr(b, key);
            }
            if (pretty7 && n) sb_indent(b, indent, depth + 1);
            sb_ch(b, ']');
            if (pretty7) sb_indent(b, indent, depth);
            sb_ch(b, '}');
            return 1;
        }
        if (!kj_enter((void *)pay, 7, parents, depth, err)) return 0;
        /* An Arguments object is an object of its index keys too, an
           undefined value's key left out. */
        int obj7 = typed || kj_is_args((const struct KjBoxS *)pay);
        long long put = 0;
        sb_ch(b, obj7 ? '{' : '[');
        for (long long i = 0; i < n; i++) {
            long long etag, epay;
            nb_decode(kj_elem_box((const struct KjBoxS *)pay, i), &etag, &epay);
            if (obj7 && !typed && etag == 5) continue;
            kj_key(depth + 1, NULL, i);
            if (put++) sb_ch(b, ',');
            if (pretty7) sb_indent(b, indent, depth + 1);
            if (obj7) {
                snprintf(key, sizeof key, "%lld", i);
                sb_json_bytes(b, key, (long long)strlen(key));
                sb_cstr(b, pretty7 ? ": " : ":");
            }
            if (!stringify_val(b, etag, epay, indent, parents, depth + 1, err)) {
                if (*err) return 0;
                sb_cstr(b, "null"); /* an undefined element → null */
            }
        }
        if (pretty7 && put) sb_indent(b, indent, depth);
        sb_ch(b, obj7 ? '}' : ']');
        return 1;
    }
    case 10:
    case 11:
        break;
    default:
        *err = 2; /* funcRef/stream: no runtime shape to walk */
        return 0;
    }
    if (!kj_enter((void *)pay, (int)tag, parents, depth, err)) return 0;
    int pretty = indent && indent[0];
    if (tag == 11) {
        char *a = (char *)pay;
        sb_ch(b, '[');
        long long n = arr_len(a);
        for (long long i = 0; i < n; i++) {
            kj_key(depth + 1, NULL, i);
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
    if (*(long long *)o & (1LL << 33)) {
        /* A Proxy: its enumerable own keys ([[OwnPropertyKeys]] and
           [[GetOwnProperty]], its traps), each value through [[Get]]. */
        long long kn;
        char **keys = __kml_dynobj_keys_enum_c(o, &kn);
        sb_ch(b, '{');
        int wrote = 0;
        for (long long i = 0; i < kn; i++) {
            long long etag, epay;
            nb_decode(__kml_dynobj_get(o, keys[i]), &etag, &epay);
            kj_key(depth + 1, keys[i], 0);
            Sb probe;
            sb_init(&probe);
            int ok = stringify_val(&probe, etag, epay, indent, parents, depth + 1, err);
            if (*err) {
                free(probe.d); /* scratch */
                return 0;
            }
            if (ok) {
                if (wrote) sb_ch(b, ',');
                if (pretty) sb_indent(b, indent, depth + 1);
                sb_json_string(b, keys[i]);
                sb_cstr(b, pretty ? ": " : ":");
                sb_raw(b, probe.d, probe.len);
                wrote = 1;
            }
            free(probe.d); /* scratch */
        }
        if (pretty && wrote) sb_indent(b, indent, depth);
        sb_ch(b, '}');
        return 1;
    }
    if (*(long long *)o & (1LL << 35)) {
        /* Array.prototype: an empty Array. */
        sb_cstr(b, "[]");
        return 1;
    }
    sb_ch(b, '{');
    long long n = obj_count(o);
    /* Own-property keys serialize in ES enumeration order (array-index keys
       ascending first, then insertion order) — the same order Object.keys /
       for...in use (runtime_dynobj.go). order[] maps output position → entry. */
    long long *order = (n > 0) ? (long long *)malloc((size_t)n * sizeof(long long)) : NULL; /* scratch */
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
        kj_key(depth + 1, obj_key(o, i), 0);
        Sb probe; /* value may be skippable — write to a probe first */
        sb_init(&probe);
        int ok = stringify_val(&probe, etag, epay, indent, parents, depth + 1, err);
        if (*err) {
            free(probe.d); /* scratch */
            free(order); /* scratch */
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
        free(probe.d); /* scratch */
    }
    free(order); /* scratch */
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
char *__kml_dynjson_stringify_at(long long tag, long long pay,
                                 const char *indent, long long base, int *err) {
    void *parents[KML_DYN_MAX_DEPTH];
    *err = 0;
    Sb b;
    sb_init(&b);
    int saved = kj_json_base;
    kj_json_base = (int)base;
    kj_key(0, "", 0);
    int ok = stringify_val(&b, tag, pay, indent, parents, 0, err);
    kj_json_base = saved;
    if (*err || !ok) {
        free(b.d); /* scratch */
        return NULL;
    }
    return sb_finish(&b);
}

char *__kml_dynjson_stringify(long long tag, long long pay,
                              const char *indent, int *err) {
    return __kml_dynjson_stringify_at(tag, pay, indent, 0, err);
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

extern _Bool __kml_key_is_symbol(const char *k);

/* Whether a property key is a symbol's: a Symbol's "\x01@@sym:%p" key or a
   well-known symbol's member key ("@@iterator"). */
static int key_is_sym(const char *k) {
    return k && (strncmp(k, "\x01@@sym:", 7) == 0 || __kml_key_is_symbol(k));
}

/* A property key as util.inspect shows it: a symbol's as `Symbol(desc)`
   (`Symbol(Symbol.iterator)` for a well-known one), an identifier bare,
   anything else single-quoted (an own `__proto__` in brackets). */
static void append_key(Sb *eb, const char *k) {
    if (k && strncmp(k, "\x01@@symf:", 8) == 0) {
        /* A field keyed by a declared symbol: its description is up to
           the \x02 before the const's binding (TDD-00246). */
        sb_cstr(eb, "Symbol(");
        for (const char *p = k + 8; *p && *p != 2; p++) sb_ch(eb, *p);
        sb_ch(eb, ')');
    } else if (key_is_sym(k) && k[0] == '@') {
        sb_cstr(eb, "Symbol(Symbol.");
        sb_cstr(eb, k + 2);
        sb_ch(eb, ')');
    } else if (key_is_sym(k)) {
        void *sym = NULL;
        sscanf(k + 7, "%p", &sym);
        const char *desc = sym ? *(const char **)((char *)sym + 8) : NULL;
        sb_cstr(eb, "Symbol(");
        sb_cstr(eb, desc ? desc : "");
        sb_ch(eb, ')');
    } else if (k && strcmp(k, "__proto__") == 0) {
        sb_cstr(eb, "['__proto__']");
    } else if (key_is_ident(k)) {
        sb_cstr(eb, k);
    } else {
        sb_ch(eb, '\'');
        sb_cstr(eb, k ? k : "");
        sb_ch(eb, '\'');
    }
}

/* inspect_obj renders a dynamic object the way util.inspect (console.log)
   does: `{ a: 1, b: 'x' }`, `{}` when empty, keys in ES enumeration order,
   non-enumerable entries skipped, accessors shown as [Getter]/[Setter]
   (never invoked), and — Node's default depth — anything nested deeper than
   two object levels collapsed to [Object]. */

/* dyn_tag: a dynamic object's own or inherited Symbol.toStringTag, when a
   string (`Math`'s "Math"), else NULL. */
static const char *dyn_tag(char *o) {
    long long t, p;
    nb_decode(__kml_dynobj_get(o, "@@toStringTag"), &t, &p);
    return t == 2 ? (const char *)p : NULL;
}

/* fnmeta.c's, when linked; without it no function is named. */
__attribute__((weak)) char *__kml_fn_name_dyn(void **rec) { (void)rec; return NULL; }

/* ctor_name: util.inspect's constructor name for a dynamic object — the
   name of the `constructor` the nearest prototype on its chain owns, NULL
   when none does or it is Object (no prefix). Node takes only a
   constructor the object is an instance of, so the object's own
   `constructor` property never names it. */
static const char *ctor_name(char *o) {
    o = (char *)__kml_dynobj_get_proto(o);
    for (int hops = 0; o && hops < 64; hops++) {
        for (long long i = 0; i < obj_count(o); i++) {
            if (strcmp(obj_key(o, i), "constructor") != 0) continue;
            long long t, p;
            nb_decode(__kml_dynobj_get(o, "constructor"), &t, &p);
            const char *name = NULL;
            if (t == 12) name = __kml_fn_name_dyn((void **)p);
            else if (t == 8) name = (const char *)p;
            if (!name || !name[0] || strcmp(name, "Object") == 0) return NULL;
            return name;
        }
        o = (char *)__kml_dynobj_get_proto(o);
    }
    return NULL;
}

static void inspect_obj(Sb *b, char *o, int depth) {
    /* A Proxy header (flag 1<<33) forwards to its target, as JSON does. */
    while (*(long long *)o & (1LL << 33)) o = *(char **)(o + 8);
    long long n = obj_count(o);
    long long *order = (n > 0) ? (long long *)malloc((size_t)n * sizeof(long long)) : NULL; /* scratch */
    if (order) n = es_order(o, n, order);
    long long visible = 0;
    for (long long oi = 0; oi < n; oi++) {
        long long i = order ? order[oi] : oi;
        if (obj_attrs(o, i) & 2) visible++;
    }
    /* A null-prototype object (flag 1<<34) is prefixed the way util.inspect
       does; past the depth cap it collapses to the bare tag. Node prints an
       empty object as `{}` even past the depth cap. */
    int nullproto = (*(long long *)o & (1LL << 34)) != 0;
    /* A Symbol.toStringTag names the object: `Object [Math] {}`. */
    /* Not when the tag is an own enumerable property, which the entries
       already show (util.inspect's rule). */
    const char *stag = dyn_tag(o);
    for (long long i = 0; stag && i < obj_count(o); i++)
        if (strcmp(obj_key(o, i), "@@toStringTag") == 0 && (obj_attrs(o, i) & 2)) stag = NULL;
    char open[128];
    if (*(long long *)o & (1LL << 35)) {
        /* Array.prototype: an Array whose constructor is not its own. */
        if (visible == 0) { free(order); sb_cstr(b, "Object(0) []"); return; } /* scratch */
    }
    if (*(long long *)o & (1LL << 36)) snprintf(open, sizeof open, "[Module: null prototype] {");
    else if (stag) snprintf(open, sizeof open, "%s [%s] {", nullproto ? "[Object: null prototype]" : "Object", stag);
    else if (!nullproto && ctor_name(o)) snprintf(open, sizeof open, "%.100s {", ctor_name(o));
    else snprintf(open, sizeof open, "%s", nullproto ? "[Object: null prototype] {" : "{");
    if (visible == 0) { free(order); sb_cstr(b, open); sb_ch(b, '}'); return; } /* scratch */
    if (depth > __kml_inspect_opt_depth) { free(order); sb_cstr(b, nullproto ? "[Object: null prototype]" : "[Object]"); return; } /* scratch */
    void *list = __kml_inspect_begin(depth, 1);
    /* String keys first, then symbol keys (the bag's "\x01@@sym:%p" form,
       the pointer a Symbol { i64 flag, ptr description }), as
       Reflect.ownKeys orders them. */
    for (int pass = 0; pass < 2; pass++)
    for (long long oi = 0; oi < n; oi++) {
        long long i = order ? order[oi] : oi;
        long long attrs = obj_attrs(o, i);
        if (!(attrs & 2)) continue; /* non-enumerable: hidden from inspect */
        const char *k = obj_key(o, i);
        if (key_is_sym(k) != pass) continue;
        Sb eb;
        sb_init(&eb);
        append_key(&eb, k);
        sb_cstr(&eb, ": ");
        if (attrs & 16) {
            /* A live binding shows its value, as the data property it is. */
            void *getter = *(void **)obj_pay(o, i);
            long long (*fn)(void *, long long, long long, void *) =
                *(long long (**)(void *, long long, long long, void *))getter;
            long long etag, epay;
            nb_decode(fn(*(void **)((char *)getter + 8), (long long)o | 5, 0, 0), &etag, &epay);
            inspect_val(&eb, etag, epay, depth + 1);
        } else if (attrs & 8) {
            char *pair = (char *)obj_pay(o, i);
            void *getter = *(void **)pair;
            void *setter = *(void **)(pair + 8);
            sb_cstr(&eb, getter && setter ? "[Getter/Setter]" : (getter ? "[Getter]" : "[Setter]"));
        } else {
            inspect_val(&eb, obj_tag(o, i), obj_pay(o, i), depth + 1);
        }
        __kml_inspect_push(list, sb_finish(&eb));
    }
    free(order); /* scratch */
    /* The prefix is part of the opening brace, so it counts toward the
       80-column single-line budget exactly as Node's braces[0] does. */
    char *out = __kml_inspect_end(list, open, "}", 2 * depth, depth, 0, 0);
    sb_cstr(b, out);
    kml_free(KML_HEAP_RAW, out - 8);
}

char *__kml_dynobj_inspect_at(char *o, long long depth) {
    Sb b;
    sb_init(&b);
    inspect_obj(&b, o, (int)depth);
    return sb_finish(&b);
}
char *__kml_dynobj_inspect(char *o) { return __kml_dynobj_inspect_at(o, 0); }

extern const char *__kml_shape_expando_key(void *obj, long long i);
extern int __kml_shape_expando_attrs(void *obj, const char *key);
extern int __kml_shape_expando_get(void *obj, const char *key, long long *out);

extern long long __kml_shape_expando_raw(void *obj, const char *key);

/* Whether added property k of an array is listed after its elements: an
   enumerable one that is no element (an accessor element shows in place). */
static int arr_extra_shown(void *owner, const char *k) {
    unsigned long long ix;
    return !es_is_index(k, &ix) && (__kml_shape_expando_attrs(owner, k) & 2);
}

/* util.inspect's rendering of an accessor property k of owner, NULL for a
   data property. */
static const char *acc_label(void *owner, const char *k) {
    int a = __kml_shape_expando_attrs(owner, k);
    if (a < 0 || !(a & 8)) return NULL;
    void **pair = (void **)__kml_shape_expando_raw(owner, k);
    return pair[0] && pair[1] ? "[Getter/Setter]" : pair[0] ? "[Getter]" : "[Setter]";
}

static const char *acc_label_at(void *owner, long long i) {
    char key[24];
    snprintf(key, sizeof key, "%lld", i);
    return acc_label(owner, key);
}

/* The number of properties listed after an array's elements (keyed by
   owner: a dynamic array, or a static array's header). */
static long long arr_extras(void *owner) {
    long long n = 0;
    for (long long i = 0;; i++) {
        const char *k = __kml_shape_expando_key(owner, i);
        if (!k) break;
        n += arr_extra_shown(owner, k);
    }
    return n;
}

/* Pushes an array's added properties after its elements, as util.inspect
   lists them: `key: value`, string keys first, then symbols. */
static void push_arr_extras(void *list, void *owner, int depth) {
    for (int pass = 0; pass < 2; pass++)
        for (long long i = 0;; i++) {
            const char *k = __kml_shape_expando_key(owner, i);
            if (!k) break;
            if (key_is_sym(k) != pass || !arr_extra_shown(owner, k)) continue;
            Sb eb;
            sb_init(&eb);
            if (pass || key_is_ident(k)) {
                append_key(&eb, k);
            } else {
                char *q = __kml_inspect_quote(k);
                sb_cstr(&eb, q);
                kml_free(KML_HEAP_RAW, q - 8);
            }
            sb_cstr(&eb, ": ");
            long long w = 10, etag, epay;
            const char *acc = acc_label(owner, k);
            if (acc) {
                sb_cstr(&eb, acc);
            } else {
                __kml_shape_expando_get(owner, k, &w);
                nb_decode(w, &etag, &epay);
                inspect_val(&eb, etag, epay, depth + 1);
            }
            __kml_inspect_push(list, sb_finish(&eb));
        }
}

/* A typed array's added properties, from its rendering (inspect_reduce.c's
   weak default stands in when this file is not linked). */
long long __kml_inspect_push_arr_extras(void *list, void *hdr, long long depth) {
    long long n = arr_extras(hdr);
    if (n) push_arr_extras(list, hdr, (int)depth);
    return n;
}

static void inspect_arr(Sb *b, char *a, int depth) {
    long long n = arr_len(a), extras = arr_extras(a);
    if (n == 0 && extras == 0) { sb_cstr(b, "[]"); return; }
    if (depth > __kml_inspect_opt_depth) { sb_cstr(b, "[Array]"); return; }
    void *list = __kml_inspect_begin(depth, 1);
    long long numeric = 1;
    long long shown = n < KML_INSPECT_MAX_ARRAY ? n : KML_INSPECT_MAX_ARRAY;
    for (long long i = 0; i < shown; i++) {
        long long tag = arr_raw_tag(a, i);
        if (tag == 13) {
            /* a run of holes is one `<n empty items>` entry */
            long long run = 1;
            while (i + run < shown && arr_raw_tag(a, i + run) == 13) run++;
            __kml_inspect_push_holes(list, run);
            i += run - 1;
            numeric = 0;
            continue;
        }
        if (tag != 0 && tag != 1) numeric = 0;
        Sb eb;
        sb_init(&eb);
        inspect_val(&eb, tag, arr_pay(a, i), depth + 1);
        __kml_inspect_push(list, sb_finish(&eb));
    }
    if (n > shown) __kml_inspect_push_more(list, n - shown);
    /* an added property's entry is grouped with the elements, left-aligned */
    if (extras) {
        push_arr_extras(list, a, depth);
        numeric = 0;
    }
    char *out = __kml_inspect_end(list, "[", "]", 2 * depth, depth, 1, numeric);
    sb_cstr(b, out);
    kml_free(KML_HEAP_RAW, out - 8);
}

/* inspect_static renders a static object through its layout row as
   util.inspect does: `Name { a: 1 }` for a class instance, `{ a: 1 }` for a
   plain object, `[Name]`/`[Object]` past the depth limit. */
extern int __kml_obj_accessor(void *o, const char *key);

static void inspect_static(Sb *b, void *o, long long n, int depth) {
    /* A class's own `[inspect.custom]` renders it, unless customInspect is
       off for this rendering. */
    char *custom = __kml_inspect_opt_custom ? __kml_obj_inspect_custom(o, depth) : NULL;
    if (custom) {
        sb_cstr(b, custom);
        return;
    }
    const char *name = __kml_obj_name(o);
    /* `Name [Tag]` when a Symbol.toStringTag getter answers a tag the class
       name does not already contain (util.inspect's getPrefix). */
    const char *tag = name ? __kml_obj_tostring_tag(o) : NULL;
    char head[256];
    if (tag && *tag && !strstr(name, tag)) snprintf(head, sizeof head, "%s [%s]", name, tag);
    else snprintf(head, sizeof head, "%s", name ? name : "");
    char open[260];
    snprintf(open, sizeof open, "%s%s{", head, name ? " " : "");
    long long shown = 0;
    for (long long i = 0; i < n; i++) {
        const char *k = __kml_obj_key(o, i);
        if (k && __kml_obj_has(o, k)) shown++;
    }
    if (shown == 0) {
        sb_cstr(b, open);
        sb_ch(b, '}');
        return;
    }
    if (depth > __kml_inspect_opt_depth) {
        sb_ch(b, '[');
        sb_cstr(b, name ? head : "Object");
        sb_ch(b, ']');
        return;
    }
    void *list = __kml_inspect_begin(depth, 1);
    /* String keys first, then symbol keys, as Reflect.ownKeys orders them. */
    for (int pass = 0; pass < 2; pass++)
    for (long long i = 0; i < n; i++) {
        const char *k = __kml_obj_key(o, i);
        if (!k || key_is_sym(k) != pass || !__kml_obj_has(o, k)) continue;
        Sb eb;
        sb_init(&eb);
        if (pass || key_is_ident(k)) {
            append_key(&eb, k);
        } else {
            char *q = __kml_inspect_quote(k);
            sb_cstr(&eb, q);
            kml_free(KML_HEAP_RAW, q - 8);
        }
        sb_cstr(&eb, ": ");
        int acc = __kml_obj_accessor(o, k);
        if (acc) {
            /* An own accessor shows as one, its getter not run. */
            sb_cstr(&eb, acc == 3 ? "[Getter/Setter]" : (acc == 1 ? "[Getter]" : "[Setter]"));
        } else {
            long long etag, epay;
            nb_decode(__kml_obj_get(o, k), &etag, &epay);
            inspect_val(&eb, etag, epay, depth + 1);
        }
        __kml_inspect_push(list, sb_finish(&eb));
    }
    char *out = __kml_inspect_end(list, open, "}", 2 * depth, depth, 0, 0);
    sb_cstr(b, out);
    kml_free(KML_HEAP_RAW, out - 8);
}

/* An Error as util.inspect shows it, without the stack's frames: the
   stack's first line — `Name: message`, with Node's `Ctor [Name]` when the
   class is not what the name says (`class G extends Error {}` shows
   `G [Error]: g`) — then its own enumerable properties, one per line. */
static int error_header_key(const char *k) {
    return strcmp(k, "name") == 0 || strcmp(k, "message") == 0;
}

/* errkeys.c's, linked with this file (UsesErrKeysC). */
extern int __kml_error_option_cause(void *o, long long *out);
extern int __kml_error_aggregate(void *o, void ***errs, long long *n);

/* frames.c's: an error's captured frames, unless its stack was set. */
extern const char *__kml_error_frames_shown(void *o);
extern const char *__kml_error_stack_assigned(void *o);

static void inspect_error(Sb *b, void *o, int depth) {
    const char *head = __kml_error_inspect(o);
    const char *ctor = __kml_obj_name(o);
    size_t nl = strcspn(head, ":");
    int improve = 0;
    if (ctor && *ctor && nl >= 5 && strncmp(head + nl - 5, "Error", 5) == 0 &&
        (strlen(ctor) != nl || strncmp(ctor, head, nl) != 0)) {
        improve = 1;
    }
    /* An error with own properties past the depth limit is its bracketed
       constructor name (util.inspect's formatRaw); one with none prints in
       full at any depth. */
    if (depth > __kml_inspect_opt_depth) {
        long long nk = __kml_obj_nkeys(o), own = 0, c0 = 0;
        void **e0 = 0;
        long long ne0 = 0;
        for (long long i = 0; i < nk && !own; i++) {
            const char *k = __kml_obj_key(o, i);
            if (k && __kml_obj_has(o, k)) own = 1;
        }
        if (own || __kml_error_option_cause(o, &c0) || __kml_error_aggregate(o, &e0, &ne0)) {
            sb_ch(b, '[');
            if (ctor && *ctor) sb_cstr(b, ctor);
            else for (size_t i = 0; i < nl; i++) sb_ch(b, head[i]);
            sb_ch(b, ']');
            return;
        }
    }
    const char *assigned = __kml_error_stack_assigned(o);
    if (assigned) {
        /* A stack that is a string prints as it is, bracketed when it has
         * no frames (Node's formatError). */
        int framed = strstr(assigned, "\n    at ") != 0;
        if (!framed) sb_ch(b, '[');
        sb_cstr(b, assigned);
        if (!framed) sb_ch(b, ']');
    } else if (improve) {
        sb_cstr(b, ctor);
        sb_cstr(b, " [");
        for (size_t i = 0; i < nl; i++) sb_ch(b, head[i]);
        sb_ch(b, ']');
        sb_cstr(b, head + nl);
    } else {
        sb_cstr(b, head);
    }
    /* Then its frames, as Node prints an error's stack, each line indented
     * as deep as the error is nested. */
    const char *fr = __kml_error_frames_shown(o);
    if (fr) {
        for (const char *c = fr; *c; c++) {
            sb_ch(b, *c);
            if (*c == '\n')
                for (int s = 0; s < 2 * depth; s++) sb_ch(b, ' ');
        }
    }
    long long n = __kml_obj_nkeys(o);
    long long shown = 0;
    for (long long i = 0; i < n; i++) {
        const char *k = __kml_obj_key(o, i);
            if (k && __kml_obj_has(o, k)) shown++;
    }
    long long cause = 0;
    int has_cause = __kml_error_option_cause(o, &cause);
    void **errs = 0;
    long long nerrs = 0;
    int has_errors = __kml_error_aggregate(o, &errs, &nerrs);
    if (shown == 0 && !has_cause && !has_errors) return;
    sb_cstr(b, " {");
    long long done = 0;
    for (long long i = 0; i < n; i++) {
        const char *k = __kml_obj_key(o, i);
        if (!k || error_header_key(k) || !__kml_obj_has(o, k)) continue;
        if (done++ > 0) sb_ch(b, ',');
        sb_ch(b, '\n');
        for (int s = 0; s < 2 * (depth + 1); s++) sb_ch(b, ' ');
        if (key_is_ident(k) || key_is_sym(k)) {
            append_key(b, k);
        } else {
            char *q = __kml_inspect_quote(k);
            sb_cstr(b, q);
            kml_free(KML_HEAP_RAW, q - 8);
        }
        sb_cstr(b, ": ");
        long long etag, epay;
        nb_decode(__kml_obj_get(o, k), &etag, &epay);
        inspect_val(b, etag, epay, depth + 1);
    }
    if (has_cause) {
        if (done++ > 0) sb_ch(b, ',');
        sb_ch(b, '\n');
        for (int s = 0; s < 2 * (depth + 1); s++) sb_ch(b, ' ');
        sb_cstr(b, "[cause]: ");
        long long ctag, cpay;
        nb_decode(cause, &ctag, &cpay);
        inspect_val(b, ctag, cpay, depth + 1);
    }
    if (has_errors) {
        if (done++ > 0) sb_ch(b, ',');
        sb_ch(b, '\n');
        for (int s = 0; s < 2 * (depth + 1); s++) sb_ch(b, ' ');
        sb_cstr(b, "[errors]: ");
        if (nerrs == 0) {
            sb_cstr(b, "[]");
        } else {
            void *list = __kml_inspect_begin(depth + 1, 1);
            for (long long i = 0; i < nerrs; i++) {
                Sb eb;
                sb_init(&eb);
                long long etag, epay; /* errors is any[]: boxed elements */
                nb_decode(((long long *)errs)[i], &etag, &epay);
                inspect_val(&eb, etag, epay, depth + 2);
                __kml_inspect_push(list, sb_finish(&eb));
            }
            char *out = __kml_inspect_end(list, "[", "]", 2 * (depth + 1), depth + 1, 1, 0);
            sb_cstr(b, out);
            kml_free(KML_HEAP_RAW, out - 8);
        }
    }
    sb_ch(b, '\n');
    for (int s = 0; s < 2 * depth; s++) sb_ch(b, ' ');
    sb_ch(b, '}');
}

/* A class boxed as a value: `[class B extends A]`, then its own public
   static fields as an object's (`[class C] { s: 5 }`); 0 for a built-in
   constructor, which shares the tag. Code generation defines the
   accessors. */
extern const char *__kml_classref_shown(void *pay);
extern long long __kml_classref_nstatic(void *pay);
extern const char *__kml_classref_skey(void *pay, long long i);
extern long long __kml_classref_sget(void *pay, long long i);
static void inspect_val(Sb *b, long long tag, long long pay, int depth);

static int inspect_classref(Sb *b, void *pay, int depth) {
    const char *cls = __kml_classref_shown(pay);
    if (!cls) return 0;
    long long n = __kml_classref_nstatic(pay);
    if (n == 0 || depth > __kml_inspect_opt_depth) {
        sb_cstr(b, cls);
        return 1;
    }
    void *list = __kml_inspect_begin(depth, 1);
    for (long long i = 0; i < n; i++) {
        const char *k = __kml_classref_skey(pay, i);
        Sb eb;
        sb_init(&eb);
        if (key_is_ident(k)) {
            sb_cstr(&eb, k);
        } else {
            char *q = __kml_inspect_quote(k);
            sb_cstr(&eb, q);
            kml_free(KML_HEAP_RAW, q - 8);
        }
        sb_cstr(&eb, ": ");
        long long etag, epay;
        nb_decode(__kml_classref_sget(pay, i), &etag, &epay);
        inspect_val(&eb, etag, epay, depth + 1);
        __kml_inspect_push(list, sb_finish(&eb));
    }
    Sb ob;
    sb_init(&ob);
    sb_cstr(&ob, cls);
    sb_cstr(&ob, " {");
    char *open = sb_finish(&ob);
    char *out = __kml_inspect_end(list, open, "}", 2 * depth, depth, 0, 0);
    kml_free(KML_HEAP_RAW, open - 8);
    sb_cstr(b, out);
    kml_free(KML_HEAP_RAW, out - 8);
    return 1;
}

/* __kml_classref_inspect_at is console.log's rendering of a constructor
   reference at depth. */
/* __kml_any_inspect_at is util.inspect's rendering of any value at a depth
   known only at run time. */
char *__kml_any_inspect_at(long long v, long long depth) {
    long long tag, pay;
    nb_decode(v, &tag, &pay);
    Sb b;
    sb_init(&b);
    inspect_val(&b, tag, pay, (int)depth);
    return sb_finish(&b);
}

char *__kml_classref_inspect_at(void *pay, long long depth) {
    Sb b;
    sb_init(&b);
    inspect_val(&b, 8, (long long)pay, (int)depth);
    return sb_finish(&b);
}

/* __kml_obj_inspect_at is console.log's rendering of a static object handed
   over from code generation (an `any` holding one). */
char *__kml_obj_inspect_at(void *o, long long depth) {
    Sb b;
    sb_init(&b);
    inspect_val(&b, 6, (long long)o, (int)depth);
    return sb_finish(&b);
}

static void inspect_val(Sb *b, long long tag, long long pay, int depth) {
    char tmp[40];
    long long pst, pv = 0;
    switch (tag) {
    case 0:
        snprintf(tmp, sizeof tmp, "%lld", pay);
        sb_cstr(b, tmp);
        break;
    case 1: {
        double d;
        memcpy(&d, &pay, 8);
        if (d == 0 && signbit(d)) {
            sb_cstr(b, "-0"); /* util.inspect shows the sign */
            break;
        }
        __kml_dtoa(tmp, d);
        sb_cstr(b, tmp);
        break;
    }
    case 2: {
        char *q = __kml_inspect_quote((const char *)pay);
        sb_cstr(b, q);
        kml_free(KML_HEAP_RAW, q - 8);
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
    case 6:
        if (is_boxed_bigint(pay)) {
            sb_cstr(b, __kml_boxed_bigint_str((void *)pay));
            sb_ch(b, 'n');
        } else if (is_host_box(pay)) {
            char *hs = __kml_host_inspect((void *)pay, depth);
            sb_cstr(b, hs);
        } else if (pay && (*(long long *)pay & (1LL << 48))) {
            /* A boxed Error (field-0 type-id flag, TDD-00222). */
            inspect_error(b, (void *)pay, depth);
        } else if (pay && (pst = __kml_promise_inspect_parts((void *)pay, &pv)) >= 0) {
            /* Node's `Promise { value }`, `Promise { <pending> }`,
               `Promise { <rejected> reason }`. */
            if (pst == 0) {
                sb_cstr(b, "Promise { <pending> }");
            } else if (depth > __kml_inspect_opt_depth) {
                sb_cstr(b, "[Promise]");
            } else {
                Sb eb;
                sb_init(&eb);
                if (pst == 2) sb_cstr(&eb, "<rejected> ");
                long long vt, vp;
                nb_decode(pv, &vt, &vp);
                inspect_val(&eb, vt, vp, depth + 1);
                void *list = __kml_inspect_begin(depth, 1);
                __kml_inspect_push(list, sb_finish(&eb));
                char *out = __kml_inspect_end(list, "Promise {", "}", 2 * depth, depth, 0, 0);
                sb_cstr(b, out);
                kml_free(KML_HEAP_RAW, out - 8);
            }
        } else {
            long long sn = pay ? __kml_obj_nkeys((void *)pay) : -1;
            if (sn >= 0) inspect_static(b, (void *)pay, sn, depth);
            else sb_cstr(b, "[Object]");
        }
        break;
    case 8: /* a constructor reference: payload is its name */
        if (!inspect_classref(b, (void *)pay, depth)) {
            sb_cstr(b, "[Function: ");
            sb_cstr(b, (const char *)pay);
            sb_ch(b, ']');
        }
        break;
    case 12:
        {
            char *fs = __kml_fn_inspect_dyn((void **)pay, depth);
            sb_cstr(b, fs);
            kml_free(KML_HEAP_RAW, fs - 8);
        }
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

/* An inspect option's number as an integer, Infinity as a huge one. */
static long long opt_int(double v) {
    if (v != v) return 0;
    if (v > 1e15) return 1LL << 62;
    if (v < -1e15) return -(1LL << 62);
    return (long long)v;
}

/* util.inspect(value, { depth, compact, sorted, breakLength,
   maxArrayLength, customInspect }) of a box: the options hold for this one
   rendering (compact 0 is `false`). */
char *__kml_inspect_opts(long long word, double depth, double compact, double sorted, double breakLength, double maxArrayLength, double customInspect) {
    long long od = __kml_inspect_opt_depth, oc = __kml_inspect_opt_compact, os = __kml_inspect_opt_sorted;
    long long ob = __kml_inspect_opt_break, om = __kml_inspect_opt_maxarr, ocu = __kml_inspect_opt_custom;
    __kml_inspect_opt_custom = customInspect != 0;
    __kml_inspect_opt_depth = opt_int(depth);
    __kml_inspect_opt_compact = opt_int(compact);
    __kml_inspect_opt_sorted = sorted != 0;
    __kml_inspect_opt_break = opt_int(breakLength);
    __kml_inspect_opt_maxarr = opt_int(maxArrayLength);
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    Sb b;
    sb_init(&b);
    inspect_val(&b, tag, pay, 0);
    __kml_inspect_opt_depth = od;
    __kml_inspect_opt_compact = oc;
    __kml_inspect_opt_sorted = os;
    __kml_inspect_opt_break = ob;
    __kml_inspect_opt_maxarr = om;
    __kml_inspect_opt_custom = ocu;
    return sb_finish(&b);
}

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
    KJ_ARRAY = 13, /* each element is an array header pointer (`T[][]`), its own
                     kind/typed described by the box's inner bytes */
    KJ_BOXED = 14 /* any other element (an object, a class instance, a Map, a
                     `T | null` number …): the box's own routines, generated for
                     its element type, box element i and store a box into it */
};

typedef struct KjBoxS {
    char *hdr;          /* arrayHeaderTy: [0]=ptr data  [8]=i64 len */
    signed char kind;   /* KJ_* or -1 */
    signed char typed;  /* 0 plain, 1 TypedArray, 2 Uint8ClampedArray, 3 Buffer */
    signed char ikind;  /* KJ_ARRAY only: the nested arrays' element kind */
    signed char ityped; /* KJ_ARRAY only: the nested arrays' typed byte */
    int esize;          /* KJ_BOXED only: the element's storage size */
    long long (*boxer)(char *data, long long i);          /* KJ_BOXED only */
    void (*unboxer)(char *data, long long i, long long w); /* KJ_BOXED only */
} KjBox;


static char *kj_data(const KjBox *b) { return *(char **)b->hdr; }
static long long kj_len(const KjBox *b) { return *(long long *)(b->hdr + 8); }

/* Whether a boxed array is an Arguments object (its header word,
   KML_ARGS_HDR_WORD): its elements and length an array's, otherwise an
   ordinary object (`[Arguments] { '0': 1 }`, `[object Arguments]`). */
static int kj_is_args(const KjBox *bx) {
    return bx && bx->hdr && (((const long long *)bx->hdr)[-1] & ~KML_HDR_STATE_MASK) == KML_ARGS_HDR_WORD;
}

static const unsigned char *kj_bytes(const struct KjBoxS *bx) { return (const unsigned char *)kj_data((const KjBox *)bx); }
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
    out->esize = 0;
    out->boxer = NULL;
    out->unboxer = NULL;
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

/* __kml_anyarr_ctor: which constructor an array boxed into `any` was made
   by, for its `constructor`: 0 Array, else 1 + the index of its TypedArray
   kind in kj_ctor_names. */
static const char *const kj_ctor_names[] = {"Float64Array", "Float32Array", "BigInt64Array",
    "BigUint64Array", "Int32Array", "Uint32Array", "Int16Array", "Uint16Array", "Int8Array",
    "Uint8Array", "Uint8ClampedArray"};
long long __kml_anyarr_ctor(void *box) {
    const KjBox *b = (const KjBox *)box;
    if (kj_is_args(b)) return -1; /* an Arguments object: Object */
    if (!b->typed) return 0;
    const char *n = kj_typed_name(b);
    for (int i = 0; i < 11; i++)
        if (strcmp(kj_ctor_names[i], n) == 0) return i + 1;
    return 0;
}

/* __kml_any_typed_index: the index in kj_ctor_names of the TypedArray a
   boxed value v is (a static array box, low bits 2), else -1 (a Buffer
   included: its methods are Buffer's). */
long long __kml_any_typed_index(long long v) {
    unsigned long long w = (unsigned long long)v;
    if (w < 65536 || w >= (1ULL << 49) || (w & 7) != 2 || !(w & ~7ULL)) return -1;
    const KjBox *b = (const KjBox *)(w & ~7ULL);
    if (!b->typed || b->typed == 3 || kj_is_args(b)) return -1;
    long long k = __kml_anyarr_ctor((void *)b);
    return k > 0 ? k - 1 : -1;
}

static const char *kj_typed_name_of(const void *box) {
    const KjBox *b = (const KjBox *)box;
    return b->typed ? kj_typed_name(b) : "Array";
}

/* __kml_anyarr_class_name: the class a boxed static array is an instance
   of (Array, or its TypedArray kind), for a TypeError's receiver. */
const char *__kml_anyarr_class_name(void *box) {
    const KjBox *b = (const KjBox *)box;
    if (kj_is_args(b)) return "Arguments";
    return kj_typed_name_of(box);
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
/* A BigInt64Array's (BigUint64Array's) element as a boxed bigint and back
   (boxsrc/bigintbox.c defines these when the program links the bigint
   runtime, as any program holding such an array does). */
__attribute__((weak)) long long __kml_typed_big_box(long long raw, int uns) {
    return nb_double(uns ? (double)(unsigned long long)raw : (double)raw);
}
static double any_elem_tonum(long long word);
__attribute__((weak)) long long __kml_typed_big_unbox(long long w, int uns) {
    (void)uns;
    return (long long)any_elem_tonum(w);
}

static long long kj_elem_box(const KjBox *b, long long i) {
    char *d = kj_data(b);
    switch (b->kind) {
    case KJ_F64: return nb_double(((double *)d)[i]);
    case KJ_F32: return nb_double((double)((float *)d)[i]);
    case KJ_I64: return b->typed ? __kml_typed_big_box(((long long *)d)[i], 0) : nb_double((double)((long long *)d)[i]);
    case KJ_U64: return b->typed ? __kml_typed_big_box(((long long *)d)[i], 1) : nb_double((double)((unsigned long long *)d)[i]);
    case KJ_I32: return nb_double((double)((int *)d)[i]);
    case KJ_U32: return nb_double((double)((unsigned int *)d)[i]);
    case KJ_I16: return nb_double((double)((short *)d)[i]);
    case KJ_U16: return nb_double((double)((unsigned short *)d)[i]);
    case KJ_I8: return nb_double((double)((signed char *)d)[i]);
    case KJ_U8: return nb_double((double)((unsigned char *)d)[i]);
    case KJ_BOOL: return ((signed char *)d)[i] ? 7 : 6;
    case KJ_STRING: { char *s = ((char **)d)[i]; return s ? (long long)s : 10; }
    case KJ_ANY: return ((long long *)d)[i];
    case KJ_BOXED: return b->boxer(d, i);
    case KJ_ARRAY: {
        KjBox *nb = (KjBox *)kml_alloc(KML_HEAP_WORDS, sizeof(KjBox));
        if (!kj_inner(b, i, nb)) { kml_free(KML_HEAP_WORDS, nb); return 10; }
        return (long long)nb | 2; /* an array-box word (kind bits 2) */
    }
    default: return 10;
    }
}

/* __kml_anyarr_typed_is: whether a boxed static array is the TypedArray
   named name (`x instanceof Uint8Array` on an `any`). */
/* Whether a boxed static array is a TypedArray (or Buffer), not an Array. */
int __kml_anyarr_is_typed(void *box) { return ((KjBox *)box)->typed != 0; }

int __kml_anyarr_typed_is(void *box, const char *name) {
    const KjBox *b = (const KjBox *)box;
    if (!b || !b->typed) return 0;
    /* A Buffer (typed 3) is a Uint8Array subclass. */
    if (strcmp(name, "Buffer") == 0) return b->typed == 3;
    return strcmp(kj_typed_name(b), name) == 0;
}

/* kj_arr_hdr_new: a fresh array header {data, len, holes} (packed: holes
   null), after the header word the compiler writes before every array
   header (KML_ARRAY_HDR_WORD). */
static char *kj_arr_hdr_new(void) {
    long long *cell = (long long *)kml_alloc(KML_HEAP_WORDS, 32);
    cell[0] = KML_ARRAY_HDR_WORD;
    cell[3] = 0;
    return (char *)(cell + 1);
}

/* __kml_anyarr_words: a boxed static array read as an `any[]` — its own
   header when its elements already are NaN-boxed words, else a fresh header
   whose elements are each boxed as kj_elem_box reads them (an element kind
   the box cannot describe reads as undefined). */
char *__kml_anyarr_words(void *box) {
    KjBox *b = (KjBox *)box;
    if (b->kind == KJ_ANY) return b->hdr;
    long long n = kj_len(b);
    long long *d = (long long *)kml_alloc(KML_HEAP_WORDS, (size_t)(n > 0 ? n : 1) * 8);
    for (long long i = 0; i < n; i++) d[i] = b->kind < 0 ? 10 : kj_elem_box(b, i);
    char *h = kj_arr_hdr_new();
    *(long long **)h = d;
    *(long long *)(h + 8) = n;
    return h;
}

/* __kml_anyarr_flat: Array.prototype.flat over boxed words — an element
   that is an Array (a boxed static array that is no TypedArray, or a dynamic
   array) is spliced in, depth levels deep. The result is a fresh header. */
typedef struct { long long *d; long long n, cap; } kj_words;

static void kj_words_push(kj_words *w, long long v) {
    if (w->n == w->cap) {
        w->cap = w->cap ? w->cap * 2 : 8;
        w->d = (long long *)kml_realloc(KML_HEAP_WORDS, w->d, (size_t)w->cap * 8);
    }
    w->d[w->n++] = v;
}

static void kj_flat_into(kj_words *w, long long word, long long depth) {
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    if (depth > 0 && tag == 7 && !((KjBox *)pay)->typed && ((KjBox *)pay)->kind >= 0) {
        KjBox *b = (KjBox *)pay;
        long long n = kj_len(b);
        for (long long i = 0; i < n; i++) kj_flat_into(w, kj_elem_box(b, i), depth - 1);
        return;
    }
    if (depth > 0 && tag == 11) {
        char *a = (char *)pay;
        long long n = *(long long *)a;
        for (long long i = 0; i < n; i++) {
            long long t = *(long long *)(arr_data(a) + i * 16);
            long long p = *(long long *)(arr_data(a) + i * 16 + 8);
            kj_flat_into(w, nb_pack(t, p), depth - 1);
        }
        return;
    }
    kj_words_push(w, word);
}

char *__kml_anyarr_flat(long long *data, long long len, long long depth) {
    kj_words w = {0};
    for (long long i = 0; i < len; i++) kj_flat_into(&w, data[i], depth);
    if (!w.d) w.d = (long long *)kml_alloc(KML_HEAP_WORDS, 8);
    char *h = kj_arr_hdr_new();
    *(long long **)h = w.d;
    *(long long *)(h + 8) = w.n;
    return h;
}

/* Whether element i of an array header is a hole: holes.c's answer when the
   program links it, none otherwise. */
__attribute__((weak)) int __kml_holes_is(void *hdr, long long i) { (void)hdr; (void)i; return 0; }
__attribute__((weak)) int __kml_holes_raw(void *hdr, long long i) { (void)hdr; (void)i; return 0; }
__attribute__((weak)) void __kml_holes_mark(void *hdr, long long from, long long to) { (void)hdr; (void)from; (void)to; }
__attribute__((weak)) void __kml_holes_clear(void *hdr, long long from, long long to) { (void)hdr; (void)from; (void)to; }
__attribute__((weak)) void __kml_holes_setlen(void *hdr, long long old, long long n) { (void)hdr; (void)old; (void)n; }

/* The index keys of a boxed static array, a hole's left out, as Object.keys
   and for...in list them; *n is their count. */
char **__kml_anyarr_keys_c(void *box, long long *n) {
    KjBox *b = (KjBox *)box;
    long long len = kj_len(b), k = 0, extra = 0;
    while (__kml_shape_expando_key(b->hdr, extra)) extra++;
    char **arr = (char **)kml_alloc(KML_HEAP_WORDS, (size_t)(len + extra + 1) * sizeof(char *));
    for (long long i = 0; i < len; i++) {
        char *base = (char *)kml_alloc(KML_HEAP_RAW, 32);
        int m = snprintf(base + 8, 24, "%lld", i);
        *(long long *)base = m;
        if (!b->typed && __kml_holes_raw(b->hdr, i)) {
            /* a hole is left out, an enumerable accessor element kept */
            int at = __kml_shape_expando_attrs(b->hdr, base + 8);
            if (at < 0 || !(at & 2)) continue;
        }
        arr[k++] = base + 8;
    }
    /* then the properties added to it, a symbol's and an element's excepted */
    for (long long i = 0; i < extra; i++) {
        const char *key = __kml_shape_expando_key(b->hdr, i);
        unsigned long long ix;
        if (strncmp(key, "\x01@@sym:", 7) != 0 && !es_is_index(key, &ix) && (__kml_shape_expando_attrs(b->hdr, key) & 2)) arr[k++] = (char *)key;
    }
    *n = k;
    return arr;
}

/* __kml_anyarr_len: the live length of a boxed static array (or TypedArray). */
long long __kml_anyarr_len(void *box) {
    return kj_len((KjBox *)box);
}

/* __kml_anyarr_get_by_key: `x[k]` / `x.k` on an `any` holding a boxed static
   array — "length" answers the LIVE header length, a canonical index answers
   the element (undefined past the end, as JS), anything else is undefined. An
   in-range index into an element kind the box could not describe answers the
   otherwise-unused immediate 1: the emitter turns that into a TypeError
   rather than inventing an `undefined`. */
/* A TypedArray element's byte size. */
static int kj_typed_esize(const KjBox *b) {
    switch (b->kind) {
    case KJ_F64: case KJ_I64: case KJ_U64: return 8;
    case KJ_F32: case KJ_I32: case KJ_U32: return 4;
    case KJ_I16: case KJ_U16: return 2;
    default: return 1;
    }
}

void *__kml_template_raw(void *cooked);
#ifdef KML_VIEWS
long long __kml_view_offset(void *data);
long long __kml_view_buffer_any(void *data, long long len);
#endif

/* Array.prototype's method named key, as an array's property read finds it:
   generated when the program reads one as a value; this weak default
   (undefined) stands in otherwise. */
__attribute__((weak)) long long __kml_array_proto_get(const char *key) { (void)key; return 10; }
__attribute__((weak)) int __kml_array_iter_override(const char *key, long long *out) { (void)key; (void)out; return 0; }
__attribute__((weak)) long long __kml_typed_proto_get(const char *key) { (void)key; return 10; }

/* An array's own non-index properties live in the shape runtime's
   added-property table, keyed by the array's header. */
extern void __kml_shape_expando_set(void *obj, const char *key, long long value);
extern int __kml_shape_expando_delete(void *obj, const char *key);
extern int __kml_shape_expando_get_recv(void *obj, const char *key, long long recv, long long *out);
extern int __kml_shape_expando_put(void *obj, const char *key, long long v, long long recv);

long long __kml_anyarr_get_by_key(void *box, const char *key) {
    KjBox *b = (KjBox *)box;
    if (strcmp(key, "length") == 0) return nb_double((double)kj_len(b));
    if (b->typed && strcmp(key, "byteLength") == 0) return nb_double((double)(kj_len(b) * kj_typed_esize(b)));
    if (b->typed && strcmp(key, "BYTES_PER_ELEMENT") == 0) return nb_double((double)kj_typed_esize(b));
#ifdef KML_VIEWS
    if (b->typed && strcmp(key, "byteOffset") == 0) return nb_double((double)__kml_view_offset(kj_data(b)));
    if (b->typed && strcmp(key, "buffer") == 0) return __kml_view_buffer_any(kj_data(b), kj_len(b) * kj_typed_esize(b));
#endif
    if (!b->typed && b->kind == KJ_STRING && strcmp(key, "raw") == 0) {
        void *raw = __kml_template_raw(b->hdr);
        if (!raw) return 10;
        KjBox *rb = (KjBox *)kml_calloc(KML_HEAP_WORDS, 1, sizeof *rb);
        rb->hdr = (char *)raw;
        rb->kind = KJ_STRING;
        return nb_pack(7, (long long)rb);
    }
    unsigned long long uidx;
    if (!es_is_index(key, &uidx)) {
        /* an own property added to the array (keyed by its header) */
        long long v;
        if (__kml_shape_expando_get_recv(b->hdr, key, (long long)b | 2, &v)) return v;
        if (!b->typed && __kml_array_iter_override(key, &v)) return v;
        return b->typed ? (b->typed == 3 ? 10 : __kml_typed_proto_get(key)) : __kml_array_proto_get(key);
    }
    long long idx = (long long)uidx;
    if (idx >= kj_len(b)) return 10;
    if (!b->typed && __kml_holes_is(b->hdr, idx)) {
        /* a hole, or an accessor element */
        long long v = 10;
        __kml_shape_expando_get_recv(b->hdr, key, (long long)b | 2, &v);
        return v;
    }
    if (b->kind < 0) return 1;
    return kj_elem_box(b, idx);
}

/* Template objects (tagged templates): each cooked strings array's raw
   strings array, registered once per call site (both are held by the site's
   cache global, so the table never owns them). A spinlock covers worker
   isolates registering their own sites. */
typedef struct KjTmpl { void *cooked, *raw; struct KjTmpl *next; } KjTmpl;
static KjTmpl *kj_tmpls;
static int kj_tmpl_lock;

void __kml_template_register(void *cooked, void *raw) {
    KjTmpl *t = (KjTmpl *)malloc(sizeof *t); /* scratch */
    t->cooked = cooked;
    t->raw = raw;
    while (__atomic_exchange_n(&kj_tmpl_lock, 1, __ATOMIC_ACQUIRE)) {}
    t->next = kj_tmpls;
    kj_tmpls = t;
    __atomic_store_n(&kj_tmpl_lock, 0, __ATOMIC_RELEASE);
}

/* A template object's raw strings array (its header), or NULL for any other
   array. */
void *__kml_template_raw(void *cooked) {
    void *raw = 0;
    while (__atomic_exchange_n(&kj_tmpl_lock, 1, __ATOMIC_ACQUIRE)) {}
    for (KjTmpl *t = kj_tmpls; t; t = t->next)
        if (t->cooked == cooked) { raw = t->raw; break; }
    __atomic_store_n(&kj_tmpl_lock, 0, __ATOMIC_RELEASE);
    return raw;
}

/* __kml_str_get_by_key: `x[k]` on an `any` holding a string — a canonical
   index answers that UTF-16 unit as a string (undefined past the end, as
   `s[i]`), anything else String.prototype's member or undefined. */
/* String.prototype's method named key, as a string's property read finds
   it: generated when the program reads one as a value; this weak default
   (undefined) stands in otherwise. */
__attribute__((weak)) long long __kml_string_proto_get(const char *key) { (void)key; return 10; }
extern char *__kml_str_index(const char *s, long long i);

long long __kml_str_get_by_key(const char *s, const char *key) {
    char *end;
    long long idx = strtoll(key, &end, 10);
    if (end == key || *end != 0) return __kml_string_proto_get(key);
    if (idx < 0 || (key[0] == '0' && key[1] != 0) || key[0] == '+' || key[0] == ' ') return 10;
    char *c = __kml_str_index(s, idx);
    return c ? nb_pack(2, (long long)c) : 10;
}

extern long long __kml_toprimitive(long long v, signed char hint);
extern double __kml_any_tonum(long long v);

/* __kml_boxed_bigint_to_double: a bigint cell's nearest double
   (boxsrc/bigintbox.c; without the bigint runtime no cell exists). */
__attribute__((weak)) double __kml_boxed_bigint_to_double(long long pay) {
    (void)pay;
    return 0.0 / 0.0;
}

/* __kml_any_number_conv: Number(w) — ToNumeric of ToPrimitive(w, number),
   a bigint taken to its nearest double. */
double __kml_any_number_conv(long long w) {
    long long p = __kml_toprimitive(w, 0), tag, pay;
    nb_decode(p, &tag, &pay);
    if (tag == 6 && is_boxed_bigint(pay)) return __kml_boxed_bigint_to_double(pay);
    return __kml_any_tonum(p);
}

/* __kml_boxed_bigint_neg: -x of a bigint cell, boxed (bigintbox.c). */
__attribute__((weak)) long long __kml_boxed_bigint_neg(long long pay) {
    (void)pay;
    return nb_double(0.0 / 0.0);
}

/* __kml_any_neg: unary minus of w — ToNumeric, a bigint negated as one. */
long long __kml_any_neg(long long w) {
    long long p = __kml_toprimitive(w, 0), tag, pay;
    nb_decode(p, &tag, &pay);
    if (tag == 6 && is_boxed_bigint(pay)) return __kml_boxed_bigint_neg(pay);
    return nb_double(-__kml_any_tonum(p));
}

static double any_elem_tonum(long long word) {
    return __kml_any_tonum(__kml_toprimitive(word, 0));
}

/* ---- Array methods on an `any` (TDD-00155 Stage 6) ----
   `x.slice()` / `x.push(v)` on an `any` holding an array run the static
   `any[]` method over a view of it: __kml_anyarr_view is an `any[]` header
   ({data of box words, len}) — the array's own live header when it is an
   `any[]`, else a fresh one holding its elements boxed; NULL for any other
   value (and a static array whose element kind the box could not describe).
   After a method that mutates, __kml_anyarr_sync writes the view back into
   the array. */
/* __kml_typed_copy_bytes: the bytes of a TypedArray held in `any`, copied
   into out (as many as fit); the count copied, 0 for any other value
   (lib/native.d.ts typedBytes). */
double __kml_typed_copy_bytes(long long word, unsigned char *out, long long out_len) {
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    if (tag != 7) return 0;
    KjBox *b = (KjBox *)pay;
    if (!b->typed || b->kind < 0) return 0;
    long long n = kj_len(b) * kj_typed_esize(b);
    if (n > out_len) n = out_len;
    if (n > 0) memcpy(out, kj_data(b), (size_t)n);
    return (double)n;
}

/* __kml_typed_set_bytes: in's bytes copied into a TypedArray held in
   `any` (as many as fit); the count copied, 0 for any other value
   (lib/native.d.ts typedBytesBack). */
double __kml_typed_set_bytes(unsigned char *in, long long in_len, long long word) {
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    if (tag != 7) return 0;
    KjBox *b = (KjBox *)pay;
    if (!b->typed || b->kind < 0) return 0;
    long long n = kj_len(b) * kj_typed_esize(b);
    if (n > in_len) n = in_len;
    if (n > 0) memcpy(kj_data(b), in, (size_t)n);
    return (double)n;
}

/* __kml_typed_int_view: an integer TypedArray (or Buffer) held in `any`:
   its bytes in *data and their count; -1 for any other value (a float
   TypedArray, a DataView, an ArrayBuffer, a plain array). */
long long __kml_typed_int_view(long long word, void **data) {
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    if (tag != 7) return -1;
    KjBox *b = (KjBox *)pay;
    if (!b->typed || b->kind < 0 || b->kind == KJ_F64 || b->kind == KJ_F32) return -1;
    *data = kj_data(b);
    return kj_len(b) * kj_typed_esize(b);
}

extern char __kml_array_integrity_any;
extern long long __kml_array_level(void *hdr);
extern void __kml_array_set_level(void *hdr, long long lvl);
extern void __kml_array_guard(void *hdr, long long op, long long a, long long b);

char *__kml_anyarr_view(long long word) {
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    long long n;
    long long *data;
    if (tag == 7) {
        KjBox *b = (KjBox *)pay;
        if (b->kind == KJ_ANY && !b->typed) return b->hdr;
        if (b->kind < 0) return NULL;
        n = kj_len(b);
        data = (long long *)kml_alloc(KML_HEAP_WORDS, (size_t)(n > 0 ? n : 1) * 8);
        for (long long i = 0; i < n; i++) data[i] = kj_elem_box(b, i);
    } else if (tag == 11) {
        char *a = (char *)pay;
        n = arr_len(a);
        data = (long long *)kml_alloc(KML_HEAP_WORDS, (size_t)(n > 0 ? n : 1) * 8);
        for (long long i = 0; i < n; i++) data[i] = nb_pack(arr_tag(a, i), arr_pay(a, i));
    } else {
        return NULL;
    }
    char *hdr = kj_arr_hdr_new();
    *(long long **)hdr = data;
    *(long long *)(hdr + 8) = n;
    // A frozen or sealed array's view carries its level, so a mutation
    // through it is rejected as one of the array would be (arrayguard.c).
    if (tag == 7 && __kml_array_integrity_any) {
        long long lvl = __kml_array_level(((KjBox *)pay)->hdr);
        if (lvl) __kml_array_set_level(hdr, lvl);
    }
    return hdr;
}

/* A box stored into a string[] slot: the string itself, a hole for
   undefined, else its ToString. */
static char *anyarr_str(long long w) {
    long long tag, pay;
    nb_decode(w, &tag, &pay);
    if (tag == 2) return (char *)pay;
    if (tag == 5) return NULL;
    long long p = __kml_toprimitive(w, 1);
    nb_decode(p, &tag, &pay);
    if (tag == 2) return (char *)pay;
    Sb b;
    sb_init(&b);
    inspect_val(&b, tag, pay, 0);
    return sb_finish(&b);
}

static const int kj_width[] = {8, 4, 8, 8, 4, 4, 2, 2, 1, 1, 1, 8, 8, 8, 0};
/* The managed-heap kind of a box's element data: numbers and booleans hold
   no pointers; strings, boxes, arrays and objects may. */
static int kj_heap_kind(int kind) { return kind <= KJ_BOOL ? KML_HEAP_RAW : KML_HEAP_WORDS; }

/* kj_store writes the NaN-boxed word w as element i of a box's data d,
   converted to the box's element kind. */
void __kml_throw_nullderef(const char *msg) __attribute__((noreturn));

/* __kml_tonum_nobigint: ToNumber's TypeError for a bigint w (a number-kind
   TypedArray's element store); any other value returns. */
void __kml_tonum_nobigint(long long w) {
    long long tag, pay;
    nb_decode(w, &tag, &pay);
    if (tag == 6 && is_boxed_bigint(pay)) {
        Sb m;
        sb_init(&m);
        sb_cstr(&m, "Cannot convert a BigInt value to a number");
        __kml_throw_nullderef(sb_finish(&m));
    }
}

static void kj_store(const KjBox *b, char *d, long long i, long long w) {
    if (b->typed && b->kind != KJ_I64 && b->kind != KJ_U64 && b->kind >= KJ_F64 && b->kind <= KJ_U8)
        __kml_tonum_nobigint(w);
    switch (b->kind) {
    case KJ_BOXED: b->unboxer(d, i, w); break;
    case KJ_F64: ((double *)d)[i] = any_elem_tonum(w); break;
    case KJ_F32: ((float *)d)[i] = (float)any_elem_tonum(w); break;
    case KJ_I64: ((long long *)d)[i] = b->typed ? __kml_typed_big_unbox(w, 0) : (long long)any_elem_tonum(w); break;
    case KJ_U64: ((unsigned long long *)d)[i] = b->typed ? (unsigned long long)__kml_typed_big_unbox(w, 1) : (unsigned long long)any_elem_tonum(w); break;
    case KJ_I32: ((int *)d)[i] = (int)(long long)any_elem_tonum(w); break;
    case KJ_U32: ((unsigned int *)d)[i] = (unsigned int)(long long)any_elem_tonum(w); break;
    case KJ_I16: ((short *)d)[i] = (short)(long long)any_elem_tonum(w); break;
    case KJ_U16: ((unsigned short *)d)[i] = (unsigned short)(long long)any_elem_tonum(w); break;
    case KJ_I8: ((signed char *)d)[i] = (signed char)(long long)any_elem_tonum(w); break;
    case KJ_U8: ((unsigned char *)d)[i] = (unsigned char)(long long)any_elem_tonum(w); break;
    case KJ_BOOL: ((signed char *)d)[i] = w == 7; break;
    case KJ_STRING: ((char **)d)[i] = anyarr_str(w); break;
    case KJ_ANY: ((long long *)d)[i] = w; break;
    case KJ_ARRAY: {
        long long t, p;
        nb_decode(w, &t, &p);
        ((char **)d)[i] = t == 7 ? ((KjBox *)p)->hdr : NULL;
        break;
    }
    }
}

void __kml_anyarr_sync(long long word, char *hdr) {
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    long long n = *(long long *)(hdr + 8);
    long long *src = *(long long **)hdr;
    if (tag == 11) {
        char *a = (char *)pay;
        if (*(long long *)(a + 8) < n) {
            *(char **)(a + 16) = (char *)kml_realloc(KML_HEAP_WORDS, arr_data(a), (size_t)(n > 0 ? n : 1) * 16);
            *(long long *)(a + 8) = n;
        }
        for (long long i = 0; i < n; i++) {
            long long t, p;
            nb_decode(src[i], &t, &p);
            *(long long *)(arr_data(a) + i * 16) = t;
            *(long long *)(arr_data(a) + i * 16 + 8) = p;
        }
        *(long long *)a = n;
        return;
    }
    if (tag != 7) return;
    KjBox *b = (KjBox *)pay;
    if (hdr == b->hdr || b->kind < 0) return;
    int width = b->kind == KJ_BOXED ? b->esize : kj_width[b->kind];
    char *d = (char *)kml_calloc(kj_heap_kind(b->kind), (size_t)(n > 0 ? n : 1), (size_t)width);
    for (long long i = 0; i < n; i++) kj_store(b, d, i, src[i]);
    *(char **)b->hdr = d;
    *(long long *)(b->hdr + 8) = n;
}

/* __kml_anyarr_set_by_key: `x[k] = w` on an `any` holding a boxed static
   array. A canonical index stores the element (converted to the array's
   element kind); past the end, a plain array grows to hold it (the gap
   is holes) and a TypedArray ignores the write, as
   JS does. False for any other key, or an element kind the box cannot
   describe. */
/* A length write's new length: v as a number, -1 when it is no valid array
   length (an integer in [0, 2^32)). */
static long long array_length_of(long long v) {
    double d = __kml_any_tonum(v);
    if (!(d >= 0 && d < 4294967296.0) || d != (double)(long long)d) return -1;
    return (long long)d;
}

/* Resizes a plain array to m elements: truncated, or extended by holes. */
static void kj_set_len(KjBox *b, long long m) {
    long long n = kj_len(b);
    if (m > n) {
        int width = b->kind == KJ_BOXED ? b->esize : kj_width[b->kind];
        char *d = (char *)kml_calloc(kj_heap_kind(b->kind), (size_t)m, (size_t)width);
        if (n > 0) kml_copy(kj_heap_kind(b->kind), d, kj_data(b), (size_t)(n * width));
        if (b->kind == KJ_ANY)
            for (long long i = n; i < m; i++) ((long long *)d)[i] = 10;
        *(char **)b->hdr = d;
    }
    *(long long *)(b->hdr + 8) = m;
    __kml_holes_setlen(b->hdr, n, m);
}

/* Returns 1 when written; 0 when not (an element kind the box cannot
   describe); 2 for a length write of no valid array length (a RangeError);
   3 for a TypedArray's length, which has only a getter. */
int __kml_anyarr_set_by_key(void *box, const char *key, long long w) {
    KjBox *b = (KjBox *)box;
    unsigned long long idx;
    if (!es_is_index(key, &idx)) {
        if (strcmp(key, "length") == 0) {
            if (b->typed) return 3;
            if (b->kind < 0) return 0;
            long long m = array_length_of(w);
            if (m < 0) return 2;
            if (__kml_array_integrity_any) __kml_array_guard(b->hdr, 0, m, 0);
            kj_set_len(b, m);
            return 1;
        }
        /* any other key is the array's own added property (a setter runs,
           a read-only one keeps its value); a frozen array refuses a write,
           a sealed or non-extensible one a new key (4) */
        if (__kml_array_integrity_any && !b->typed) {
            long long lvl = __kml_array_level(b->hdr);
            int had = __kml_shape_expando_attrs(b->hdr, key) >= 0;
            if (lvl == 1 || (lvl != 0 && !had)) return 4;
        }
        if (__kml_shape_expando_put(b->hdr, key, w, (long long)b | 2) >= 0) return 1;
        __kml_shape_expando_set(b->hdr, key, w);
        return 1;
    }
    if (b->kind < 0) return 0;
    if (__kml_array_integrity_any && !b->typed) __kml_array_guard(b->hdr, 0, (long long)idx, 0);
    long long n = kj_len(b);
    if ((long long)idx >= n) {
        if (b->typed) return 1;
        int width = b->kind == KJ_BOXED ? b->esize : kj_width[b->kind];
        long long m = (long long)idx + 1;
        char *d = (char *)kml_calloc(kj_heap_kind(b->kind), (size_t)m, (size_t)width);
        if (n > 0) kml_copy(kj_heap_kind(b->kind), d, kj_data(b), (size_t)(n * width));
        if (b->kind == KJ_ANY)
            for (long long i = n; i < m; i++) ((long long *)d)[i] = 10;
        *(char **)b->hdr = d;
        *(long long *)(b->hdr + 8) = m;
        if ((long long)idx > n) __kml_holes_mark(b->hdr, n, (long long)idx);
    }
    if (!b->typed && (long long)idx < kj_len(b) && __kml_holes_raw(b->hdr, (long long)idx) &&
        __kml_shape_expando_put(b->hdr, key, w, (long long)b | 2) >= 0)
        return 1; /* an accessor element */
    if (!b->typed) __kml_holes_clear(b->hdr, (long long)idx, (long long)idx + 1);
    kj_store(b, kj_data(b), (long long)idx, w);
    return 1;
}

/* delete x[k] on a boxed static array: an index becomes a hole, any other
   key an added property removed; NULL. A TypedArray's element cannot be
   deleted: its class name, for the caller's TypeError. */
const char *__kml_anyarr_delete_key(void *box, const char *key) {
    KjBox *b = (KjBox *)box;
    unsigned long long idx;
    if (!es_is_index(key, &idx)) {
        __kml_shape_expando_delete(b->hdr, key);
        return NULL;
    }
    if ((long long)idx >= kj_len(b)) return NULL;
    if (b->typed) return b->typed == 3 ? "Buffer" : kj_typed_name(b);
    __kml_holes_mark(b->hdr, (long long)idx, (long long)idx + 1);
    return NULL;
}

/* Object.defineProperty / getOwnPropertyDescriptor on an array (a boxed
   static one, tag 7, or a dynamic one, tag 11): its named properties live
   in the added-property table with their attributes, an accessor's word a
   {getter, setter} pair. */
extern void __kml_shape_expando_define(void *obj, const char *key, long long v, int a);
extern void *__kml_dynobj_new(void);
extern void __kml_dynobj_set(void *o, const char *key, long long v);
extern long long __kml_dynarr_get_by_key(void *a, const char *key);
extern int __kml_dynarr_set_by_key(void *a, const char *key, long long v);
extern unsigned char __kml_any_tobool(long long v);
extern long long __kml_dynarr_len(void *a);
extern int __kml_dynarr_is_hole(void *a, long long i);
extern void __kml_dynarr_delete_index(void *a, long long i);
extern int __kml_shape_has_field(void *obj, const char *key);

static int arr_owner(long long word, void **owner, KjBox **box) {
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    *box = NULL;
    if (tag == 7) { *box = (KjBox *)pay; *owner = (*box)->hdr; return 1; }
    if (tag == 11) { *owner = (void *)pay; return 1; }
    return 0;
}

/* The owner of a static object's (tag 6) added properties: the object;
   NULL for any other value. */
static void *obj_owner(long long word) {
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    return tag == 6 ? (void *)pay : NULL;
}

static void *fn_rec(long long w) {
    long long tag, pay;
    nb_decode(w, &tag, &pay);
    return tag == 12 ? (void *)pay : NULL;
}

/* 0: not an array; 1: defined; 2: a non-configurable property refused the
   change (TypeError "Cannot redefine property"); 3: an accessor on an
   element, which an array's element storage cannot hold; 4: a length of
   no valid array length (RangeError "Invalid array length"). */
static int elem_acc_any; /* set once an accessor element exists */

int __kml_anyarr_defprop(long long word, const char *key, void *desc) {
    void *owner;
    KjBox *b;
    int isArr = arr_owner(word, &owner, &b);
    if (!isArr) {
        /* A static object's property beside its layout (a field keeps its
           own path) */
        owner = obj_owner(word);
        if (!owner || __kml_shape_has_field(owner, key)) return 0;
    }
    long long v = __kml_dynobj_get(desc, "value"), wr = __kml_dynobj_get(desc, "writable");
    long long en = __kml_dynobj_get(desc, "enumerable"), co = __kml_dynobj_get(desc, "configurable");
    long long g = __kml_dynobj_get(desc, "get"), s = __kml_dynobj_get(desc, "set");
    int acc = g != 10 || s != 10;
    unsigned long long idx;
    int isIdx = isArr && es_is_index(key, &idx);
    int a = __kml_shape_expando_attrs(owner, key);
    if (isArr && strcmp(key, "length") == 0 || isIdx && (b && b->typed || !acc && a < 0)) {
        if (acc) return 3;
        if (v != 10 && strcmp(key, "length") == 0) {
            double d = any_elem_tonum(v);
            if (!(d >= 0 && d <= 4294967295.0 && d == (double)(unsigned long long)d)) return 4;
        }
        if (v != 10 || strcmp(key, "length") != 0) {
            if (b) __kml_anyarr_set_by_key(b, key, v);
            else __kml_dynarr_set_by_key(owner, key, v);
        }
        return 1;
    }
    if (isIdx && a < 0) {
        /* An element made an accessor: a hole, its accessor in the table
           under its index (an element that was there becomes the data
           property the accessor replaces). */
        long long len = b ? kj_len(b) : __kml_dynarr_len(owner);
        int present = (long long)idx < len &&
                      !(b ? __kml_holes_is(owner, (long long)idx) : __kml_dynarr_is_hole(owner, (long long)idx));
        if (present) {
            long long cur = b ? __kml_anyarr_get_by_key(b, key) : __kml_dynarr_get_by_key(owner, key);
            __kml_shape_expando_define(owner, key, cur, 7);
            a = 7;
        } else if ((long long)idx >= len) {
            if (b) __kml_anyarr_set_by_key(b, key, 10);
            else __kml_dynarr_set_by_key(owner, key, 10);
        }
        if (b) __kml_holes_mark(owner, (long long)idx, (long long)idx + 1);
        else __kml_dynarr_delete_index(owner, (long long)idx);
        elem_acc_any = 1;
    }
    if (a < 0) {
        int na = (en != 10 && __kml_any_tobool(en) ? 2 : 0) | (co != 10 && __kml_any_tobool(co) ? 4 : 0);
        if (acc) {
            void **pair = (void **)kml_alloc(KML_HEAP_WORDS, 3 * sizeof(void *));
            pair[0] = fn_rec(g);
            pair[1] = fn_rec(s);
            pair[2] = (void *)word;
            __kml_shape_expando_define(owner, key, (long long)pair, na | 8);
        } else {
            __kml_shape_expando_define(owner, key, v, na | (wr != 10 && __kml_any_tobool(wr) ? 1 : 0));
        }
        return 1;
    }
    int wasAcc = (a & 8) != 0;
    int dataDesc = v != 10 || wr != 10;
    if (!(a & 4)) {
        if (co != 10 && __kml_any_tobool(co)) return 2;
        if (en != 10 && (__kml_any_tobool(en) != 0) != ((a & 2) != 0)) return 2;
        if ((wasAcc && dataDesc) || (!wasAcc && acc)) return 2;
        if (!wasAcc && !(a & 1)) {
            if (wr != 10 && __kml_any_tobool(wr)) return 2;
            if (v != 10 && v != __kml_shape_expando_raw(owner, key)) return 2;
        }
    }
    int na = a & ~(acc || dataDesc ? 9 : 0);
    if (en != 10) na = (na & ~2) | (__kml_any_tobool(en) ? 2 : 0);
    if (co != 10) na = (na & ~4) | (__kml_any_tobool(co) ? 4 : 0);
    if (acc) {
        void **old = wasAcc ? (void **)__kml_shape_expando_raw(owner, key) : NULL;
        void **pair = (void **)kml_alloc(KML_HEAP_WORDS, 3 * sizeof(void *));
        pair[0] = g != 10 ? fn_rec(g) : old ? old[0] : NULL;
        pair[1] = s != 10 ? fn_rec(s) : old ? old[1] : NULL;
        pair[2] = (void *)word;
        __kml_shape_expando_define(owner, key, (long long)pair, na | 8);
    } else if (dataDesc) {
        long long nv = v != 10 ? v : wasAcc ? 10 : __kml_shape_expando_raw(owner, key);
        int w = wr != 10 ? (__kml_any_tobool(wr) != 0) : (wasAcc ? 0 : (a & 1));
        __kml_shape_expando_define(owner, key, nv, na | w);
    } else {
        __kml_shape_expando_define(owner, key, __kml_shape_expando_raw(owner, key), na | (a & 9));
    }
    if (isIdx && !(__kml_shape_expando_attrs(owner, key) & 8)) {
        /* an accessor element made data again: back in the element */
        long long nv = __kml_shape_expando_raw(owner, key);
        __kml_shape_expando_delete(owner, key);
        if (b) __kml_anyarr_set_by_key(b, key, nv);
        else __kml_dynarr_set_by_key(owner, key, nv);
    }
    return 1;
}

/* getOwnPropertyNames of an array: its index keys, "length", then every
   added string key; NULL when word is no array. */
char **__kml_anyarr_names_c(long long word, long long *n) {
    void *owner;
    KjBox *b;
    if (!arr_owner(word, &owner, &b)) return NULL;
    long long len = b ? kj_len(b) : __kml_dynarr_len(owner), extra = 0, k = 0;
    while (__kml_shape_expando_key(owner, extra)) extra++;
    char **arr = (char **)kml_alloc(KML_HEAP_WORDS, (size_t)(len + extra + 2) * sizeof(char *));
    for (long long i = 0; i < len; i++) {
        char *base = (char *)kml_alloc(KML_HEAP_RAW, 32);
        *(long long *)base = snprintf(base + 8, 24, "%lld", i);
        if ((b ? !b->typed && __kml_holes_is(owner, i) : __kml_dynarr_is_hole(owner, i)) && __kml_shape_expando_attrs(owner, base + 8) < 0) continue;
        arr[k++] = base + 8;
    }
    if (!b || !b->typed) {
        static struct { long long n; char s[8]; } lenkey = {KML_STR_ASCII_HDR(6), "length"};
        arr[k++] = lenkey.s;
    }
    for (long long i = 0; i < extra; i++) {
        const char *key = __kml_shape_expando_key(owner, i);
        unsigned long long ix;
        if (strncmp(key, "\x01@@sym:", 7) != 0 && !es_is_index(key, &ix)) arr[k++] = (char *)key;
    }
    *n = k;
    return arr;
}

static long long data_desc(long long v, int w, int e, int c) {
    void *d = __kml_dynobj_new();
    __kml_dynobj_set(d, "value", v);
    __kml_dynobj_set(d, "writable", w ? 7 : 6);
    __kml_dynobj_set(d, "enumerable", e ? 7 : 6);
    __kml_dynobj_set(d, "configurable", c ? 7 : 6);
    return nb_pack(10, (long long)d);
}

/* An accessor element of a boxed static array: its getter's result stored
   in its slot, as the element's storage holds a value; 0 for any other
   element (holes.c asks before every hole verdict). */
int __kml_elem_refresh(void *hdr, long long i) {
    if (!elem_acc_any) return 0;
    char key[24];
    snprintf(key, sizeof key, "%lld", i);
    int a = __kml_shape_expando_attrs(hdr, key);
    if (a < 0 || !(a & 8)) return 0;
    void **pair = (void **)__kml_shape_expando_raw(hdr, key);
    long long tag, pay;
    nb_decode((long long)pair[2], &tag, &pay);
    if (tag != 7) return 0;
    KjBox *b = (KjBox *)pay;
    long long v = 10;
    __kml_shape_expando_get_recv(hdr, key, 0, &v);
    if (b->kind < 0 || i >= kj_len(b)) return 0;
    kj_store(b, kj_data(b), i, v);
    return 1;
}

/* 0: not an array; else 1 and *out the descriptor (undefined for none). */
int __kml_anyarr_gopd(long long word, const char *key, long long *out) {
    void *owner;
    KjBox *b;
    if (!arr_owner(word, &owner, &b)) return 0;
    unsigned long long idx;
    long long len = b ? kj_len(b) : __kml_dynarr_len(owner);
    *out = 10;
    if (strcmp(key, "length") == 0) {
        *out = data_desc(nb_double((double)len), !b || !b->typed, 0, 0);
    } else if (es_is_index(key, &idx) && __kml_shape_expando_attrs(owner, key) < 0) {
        if ((long long)idx < len && (b ? b->typed || !__kml_holes_is(owner, (long long)idx) : !__kml_dynarr_is_hole(owner, (long long)idx)))
            *out = data_desc(b ? __kml_anyarr_get_by_key(b, key) : __kml_dynarr_get_by_key(owner, key), 1, 1, !b || !b->typed);
    } else {
        int a = __kml_shape_expando_attrs(owner, key);
        if (a < 0) return 1;
        long long raw = __kml_shape_expando_raw(owner, key);
        if (a & 8) {
            void **pair = (void **)raw;
            void *d = __kml_dynobj_new();
            __kml_dynobj_set(d, "get", pair[0] ? (long long)pair[0] | 7 : 10);
            __kml_dynobj_set(d, "set", pair[1] ? (long long)pair[1] | 7 : 10);
            __kml_dynobj_set(d, "enumerable", a & 2 ? 7 : 6);
            __kml_dynobj_set(d, "configurable", a & 4 ? 7 : 6);
            *out = nb_pack(10, (long long)d);
        } else {
            *out = data_desc(raw, a & 1, a & 2, a & 4);
        }
    }
    return 1;
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
        buf = (double *)kml_alloc(KML_HEAP_RAW, (size_t)(n > 0 ? n : 1) * sizeof(double));
        for (long long i = 0; i < n; i++) {
            if (b->kind < 0) { buf[i] = 0.0 / 0.0; continue; }
            long long w = kj_elem_box(b, i);
            __kml_tonum_nobigint(w);
            buf[i] = any_elem_tonum(w);
        }
        break;
    }
    case 11: {
        char *a = (char *)pay;
        n = arr_len(a);
        buf = (double *)kml_alloc(KML_HEAP_RAW, (size_t)(n > 0 ? n : 1) * sizeof(double));
        for (long long i = 0; i < n; i++) {
            long long w = nb_pack(arr_tag(a, i), arr_pay(a, i));
            __kml_tonum_nobigint(w);
            buf[i] = any_elem_tonum(w);
        }
        break;
    }
    case 2: {
        const char *s = (const char *)pay;
        n = KML_STR_LEN(s);
        buf = (double *)kml_alloc(KML_HEAP_RAW, (size_t)(n > 0 ? n : 1) * sizeof(double));
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
        buf = (double *)kml_alloc(KML_HEAP_RAW, (size_t)(n > 0 ? n : 1) * sizeof(double));
        char key[32];
        for (long long i = 0; i < n; i++) {
            snprintf(key, sizeof key, "%lld", i);
            buf[i] = any_elem_tonum(__kml_dynobj_get(o, key));
        }
        break;
    }
    default:
        buf = (double *)kml_alloc(KML_HEAP_RAW, sizeof(double));
        break;
    }
    *outLen = n;
    return buf;
}

/* __kml_any_arraylike_words is the __kml_any_arraylike_f64 walk with each
   element left the box it is: a BigInt64Array converts each with ToBigInt.
   A string's characters are one-character strings. */
long long *__kml_any_arraylike_words(long long word, long long *outLen) {
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    long long n = 0;
    long long *buf = NULL;
    switch (tag) {
    case 4:
    case 5:
        *outLen = -1;
        return NULL;
    case 7: {
        KjBox *b = (KjBox *)pay;
        n = kj_len(b);
        buf = (long long *)kml_alloc(KML_HEAP_RAW, (size_t)(n > 0 ? n : 1) * sizeof(long long));
        for (long long i = 0; i < n; i++)
            buf[i] = b->kind < 0 ? nb_pack(5, 0) : kj_elem_box(b, i);
        break;
    }
    case 11: {
        char *a = (char *)pay;
        n = arr_len(a);
        buf = (long long *)kml_alloc(KML_HEAP_RAW, (size_t)(n > 0 ? n : 1) * sizeof(long long));
        for (long long i = 0; i < n; i++)
            buf[i] = nb_pack(arr_tag(a, i), arr_pay(a, i));
        break;
    }
    case 2: {
        const char *str = (const char *)pay;
        n = KML_STR_LEN(str);
        buf = (long long *)kml_alloc(KML_HEAP_RAW, (size_t)(n > 0 ? n : 1) * sizeof(long long));
        for (long long i = 0; i < n; i++) {
            char *one = (char *)kml_alloc(KML_HEAP_RAW, 10);
            *(long long *)one = 1;
            one[8] = str[i];
            one[9] = 0;
            buf[i] = nb_pack(2, (long long)(one + 8));
        }
        break;
    }
    case 10: {
        char *o = (char *)pay;
        double lenD = any_elem_tonum(__kml_dynobj_get(o, "length"));
        if (!(lenD > 0)) lenD = 0;
        if (lenD > 9007199254740991.0) lenD = 9007199254740991.0;
        n = (long long)lenD;
        buf = (long long *)kml_alloc(KML_HEAP_RAW, (size_t)(n > 0 ? n : 1) * sizeof(long long));
        char key[32];
        for (long long i = 0; i < n; i++) {
            snprintf(key, sizeof key, "%lld", i);
            buf[i] = __kml_dynobj_get(o, key);
        }
        break;
    }
    default:
        buf = (long long *)kml_alloc(KML_HEAP_RAW, sizeof(long long));
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
    /* A BigInt64Array's element inspects with its `n` suffix. */
    case KJ_I64: snprintf(tmp, sizeof tmp, bx->typed && inspect ? "%lldn" : "%lld", ((long long *)data)[i]); sb_cstr(b, tmp); break;
    case KJ_U64: snprintf(tmp, sizeof tmp, bx->typed && inspect ? "%llun" : "%llu", ((unsigned long long *)data)[i]); sb_cstr(b, tmp); break;
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
            if (s) { char *q = __kml_inspect_quote(s); sb_cstr(b, q); kml_free(KML_HEAP_RAW, q - 8); }
            else sb_cstr(b, "''");
        } else if (s) sb_cstr(b, s); /* a null element joins as empty */
        break;
    }
    case KJ_ANY:
    case KJ_BOXED: {
        long long tag, pay;
        nb_decode(bx->kind == KJ_ANY ? ((long long *)data)[i] : bx->boxer(data, i), &tag, &pay);
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
    if (bx->typed == 3) {
        /* String(buf) is buf.toString(): its bytes as UTF-8. */
        sb_raw(b, kj_data(bx), kj_len(bx));
        return;
    }
    if (kj_is_args(bx)) { sb_cstr(b, "[object Arguments]"); return; }
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
/* kj_buffer_inspect renders a Buffer as util.inspect does: `<Buffer 68 69>`,
   at most INSPECT_MAX_BYTES (50) bytes, then ` ... N more bytes`. */
static void kj_buffer_inspect(Sb *b, const unsigned char *d, long long len) {
    char tmp[48];
    long long shown = len < 50 ? len : 50;
    sb_cstr(b, "<Buffer ");
    for (long long i = 0; i < shown; i++) {
        snprintf(tmp, sizeof tmp, i ? " %02x" : "%02x", d[i]);
        sb_cstr(b, tmp);
    }
    if (len > shown) {
        snprintf(tmp, sizeof tmp, " ... %lld more byte%s", len - shown, len - shown == 1 ? "" : "s");
        sb_cstr(b, tmp);
    }
    sb_ch(b, '>');
}

/* __kml_buffer_inspect is kj_buffer_inspect as a length-prefixed heap
   string, for a statically typed Buffer. */
char *__kml_buffer_inspect(const unsigned char *d, long long len) {
    Sb b;
    sb_init(&b);
    kj_buffer_inspect(&b, d, len);
    return sb_finish(&b);
}

static void kj_inspect(Sb *b, const KjBox *bx, int depth) {
    if (kj_is_args(bx)) {
        if (depth > __kml_inspect_opt_depth) { sb_cstr(b, "[Arguments]"); return; }
        long long len = kj_len(bx);
        void *list = __kml_inspect_begin(depth, len > 0);
        for (long long i = 0; i < len; i++) {
            Sb eb;
            sb_init(&eb);
            char key[32];
            snprintf(key, sizeof key, "'%lld': ", i);
            sb_cstr(&eb, key);
            kj_elem_render(&eb, bx, i, 1, depth);
            __kml_inspect_push(list, sb_finish(&eb));
        }
        char *out = __kml_inspect_end(list, "[Arguments] {", "}", 2 * depth, depth, 0, 0);
        sb_cstr(b, out);
        kml_free(KML_HEAP_RAW, out - 8);
        return;
    }
    if (bx->typed == 3) {
        kj_buffer_inspect(b, (const unsigned char *)kj_data(bx), kj_len(bx));
        return;
    }
    if (bx->kind < 0 || depth >= KML_DYN_MAX_DEPTH) {
        /* util.inspect's depth placeholder; a TypedArray of an unrenderable
           kind (BigInt64Array) still names itself. */
        if (bx->typed) { sb_cstr(b, kj_typed_name(bx)); sb_cstr(b, " [Array]"); }
        else sb_cstr(b, "[Array]");
        return;
    }
    long long len = kj_len(bx), extras = arr_extras(bx->hdr);
    char open[64] = "[";
    if (bx->typed) {
        /* Node: `Int32Array(2) [ 8, 9 ]`, `Uint8Array(0) []`. */
        snprintf(open, sizeof open, "%s(%lld) [", kj_typed_name(bx), len);
    }
    if (len == 0 && extras == 0) { sb_cstr(b, open); sb_cstr(b, "]"); return; }
    if (depth > __kml_inspect_opt_depth) {
        /* Node names the class past the depth: `[Array]`, `[Uint8Array]`. */
        if (bx->typed) { sb_cstr(b, "["); sb_cstr(b, kj_typed_name(bx)); sb_cstr(b, "]"); }
        else sb_cstr(b, "[Array]");
        return;
    }
    void *list = __kml_inspect_begin(depth, 1);
    long long shown = len < KML_INSPECT_MAX_ARRAY ? len : KML_INSPECT_MAX_ARRAY;
    for (long long i = 0; i < shown; i++) {
        if (!bx->typed && __kml_holes_raw(bx->hdr, i)) {
            /* an accessor element shows as one, its getter not run */
            char key[24];
            snprintf(key, sizeof key, "%lld", i);
            const char *acc = acc_label(bx->hdr, key);
            if (acc) {
                Sb eb;
                sb_init(&eb);
                sb_cstr(&eb, acc);
                __kml_inspect_push(list, sb_finish(&eb));
                continue;
            }
        }
        if (!bx->typed && __kml_holes_raw(bx->hdr, i)) {
            /* a run of holes is one `<n empty items>` entry */
            long long run = 1;
            while (i + run < shown && __kml_holes_raw(bx->hdr, i + run) && !acc_label_at(bx->hdr, i + run)) run++;
            __kml_inspect_push_holes(list, run);
            i += run - 1;
            continue;
        }
        Sb eb;
        sb_init(&eb);
        kj_elem_render(&eb, bx, i, 1, depth);
        __kml_inspect_push(list, sb_finish(&eb));
    }
    if (len > shown) __kml_inspect_push_more(list, len - shown);
    long long numeric = bx->kind != KJ_BOOL && bx->kind != KJ_STRING && bx->kind != KJ_ANY && bx->kind != KJ_ARRAY && bx->kind != KJ_BOXED;
    if (bx->kind == KJ_ANY) {
        /* An any[] groups right-aligned when every shown element is a
           number or a bigint (util.inspect's groupArrayElements). */
        numeric = 1;
        for (long long i = 0; i < shown && numeric; i++) {
            long long t, p;
            nb_decode(kj_elem_box(bx, i), &t, &p);
            if (t != 0 && t != 1 && !(t == 6 && is_boxed_bigint(p))) numeric = 0;
        }
    }
    if (extras) {
        push_arr_extras(list, bx->hdr, depth);
        numeric = 0;
    }
    char *out = __kml_inspect_end(list, open, "]", 2 * depth, depth, 1, numeric);
    sb_cstr(b, out);
    kml_free(KML_HEAP_RAW, out - 8);
}

char *__kml_array_inspect_at(void *box, long long depth) {
    Sb b;
    sb_init(&b);
    kj_inspect(&b, (const KjBox *)box, (int)depth);
    return sb_finish(&b);
}
char *__kml_array_inspect(void *box) { return __kml_array_inspect_at(box, 0); }

/* A key naming an object's prototype, so two objects have one prototype
   exactly when their keys are equal: `Object`, `Array`, `null`, a class
   (`C:Point`), a host class (`[object Map]`), a typed array kind, `Buffer`,
   an error's kind or Error subclass (`Error:<id>`), `Function`, `Promise`,
   `BigInt`, or a dynamic object's own prototype (`P:<address>`). A
   primitive's key is empty. */
char *__kml_proto_key(long long word) {
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    char buf[64];
    const char *k = "";
    switch (tag) {
    case 7: {
        long long n;
        int kind, typed;
        kj_box_info((const void *)pay, &n, &kind, &typed);
        k = kj_is_args((const KjBox *)pay) ? "Object" : typed == 3 ? "Buffer" : typed ? kj_typed_name((const KjBox *)pay) : "Array";
        break;
    }
    case 11:
        k = "Array";
        break;
    case 10: {
        char *o = (char *)pay;
        void *proto = __kml_dynobj_get_proto(o);
        if (*(long long *)o & (1LL << 34)) k = "null";
        else if (!proto) k = "Object";
        else { snprintf(buf, sizeof buf, "P:%p", proto); k = buf; }
        break;
    }
    case 6:
        if (is_boxed_bigint(pay)) k = "BigInt";
        else if (is_host_box(pay)) k = __kml_host_class_tag((void *)pay);
        else if (pay && (*(long long *)pay & (1LL << 48))) {
            /* An error: its kind, or its Error subclass's type id. */
            snprintf(buf, sizeof buf, "Error:%lld", *(long long *)pay & 0xFFFFFFFFLL);
            k = buf;
        } else {
            long long pv = 0;
            if (pay && __kml_promise_inspect_parts((void *)pay, &pv) >= 0) k = "Promise";
            else {
                const char *name = pay ? __kml_obj_name((void *)pay) : 0;
                if (name && *name && strcmp(name, "Object") != 0) { snprintf(buf, sizeof buf, "C:%s", name); k = buf; }
                else k = "Object";
            }
        }
        break;
    case 8:
    case 12:
        k = "Function";
        break;
    }
    long long n = (long long)strlen(k);
    char *base = (char *)kml_alloc(KML_HEAP_RAW, (size_t)n + 9);
    *(long long *)base = n;
    memcpy(base + 8, k, (size_t)n + 1);
    return base + 8;
}

/* The " Received …" end of Node's ERR_INVALID_ARG_TYPE message for a box
   (lib/internal/errors.js determineSpecificType). */
char *__kml_received(long long word) {
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    char buf[512];
    if (tag == 4 || tag == 5) {
        snprintf(buf, sizeof buf, " Received %s", tag == 4 ? "null" : "undefined");
    } else {
        long long shownWord = word;
        char *cut = NULL;
        if (tag == 2 && pay && KML_STR_LEN(pay) > 28) {
            /* A long string: its first 25 characters and "...", inspected. */
            cut = (char *)kml_alloc(KML_HEAP_RAW, 8 + 25 + 3 + 1);
            *(long long *)cut = 28;
            memcpy(cut + 8, (char *)pay, 25);
            memcpy(cut + 8 + 25, "...", 4);
            shownWord = (long long)(cut + 8);
        }
        char *shown = __kml_inspect_opts(shownWord, 2, 3, 0, 80, 100, 1);
        kml_free(KML_HEAP_RAW, cut);
        const char *type = "object";
        switch (tag) {
        case 0: case 1: type = "number"; break;
        case 2: type = "string"; break;
        case 3: type = "boolean"; break;
        case 8: case 12: type = "function"; break;
        case 6: if (is_boxed_bigint(pay)) type = "bigint"; break;
        }
        if (strcmp(type, "function") == 0 && strncmp(shown, "[Function: ", 11) == 0) {
            snprintf(buf, sizeof buf, " Received function %.*s", (int)(strlen(shown) - 12), shown + 11);
        } else if (strcmp(type, "object") == 0) {
            char *pk = __kml_proto_key(word);
            const char *name = pk;
            if (strncmp(pk, "C:", 2) == 0) name = pk + 2;
            else if (strncmp(pk, "Error:", 6) == 0 || strncmp(pk, "P:", 2) == 0 || *pk == 0) name = "Object";
            else if (strcmp(pk, "null") == 0) name = NULL;
            if (name) snprintf(buf, sizeof buf, " Received an instance of %s", name);
            else snprintf(buf, sizeof buf, " Received %s", shown);
            kml_free(KML_HEAP_RAW, pk - 8);
        } else {
            snprintf(buf, sizeof buf, " Received type %s (%s)", type, shown);
        }
        kml_free(KML_HEAP_RAW, shown - 8);
    }
    long long n = (long long)strlen(buf);
    char *base = (char *)kml_alloc(KML_HEAP_RAW, (size_t)n + 9);
    *(long long *)base = n;
    memcpy(base + 8, buf, (size_t)n + 1);
    return base + 8;
}

/* Object.prototype.toString.call(value): `[object Tag]`. */
char *__kml_object_tostring(long long word) {
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    char buf[96];
    const char *k = "Object";
    switch (tag) {
    case 0: case 1: k = "Number"; break;
    case 2: k = "String"; break;
    case 3: k = "Boolean"; break;
    case 4: k = "Null"; break;
    case 5: k = "Undefined"; break;
    case 8: k = "Function"; break;
    case 12: k = __kml_fn_tag_dyn((void **)pay); break;
    case 9: k = "ReadableStream"; break;
    case 7: case 11: {
        if (tag == 7 && kj_is_args((const KjBox *)pay)) { k = "Arguments"; break; }
        char *pk = __kml_proto_key(word);
        k = strcmp(pk, "Buffer") == 0 ? "Uint8Array" : pk;
        snprintf(buf, sizeof buf, "[object %s]", k);
        kml_free(KML_HEAP_RAW, pk - 8);
        k = NULL;
        break;
    }
    case 6:
        if (is_host_box(pay)) {
            char *t = __kml_host_class_tag((void *)pay);
            snprintf(buf, sizeof buf, "%s", t);
            k = NULL;
        } else if (is_boxed_bigint(pay)) k = "BigInt";
        else if (pay && (*(long long *)pay & (1LL << 48))) k = "Error";
        else {
            long long pv = 0;
            char *t = pay ? __kml_obj_tostring_tag((void *)pay) : NULL;
            if (t) {
                snprintf(buf, sizeof buf, "[object %s]", t);
                k = NULL;
            } else if (pay && __kml_promise_inspect_parts((void *)pay, &pv) >= 0) k = "Promise";
        }
        break;
    case 10: {
        const char *t = dyn_tag((char *)pay);
        if (t) k = t;
        else if (*(long long *)pay & (1LL << 35)) k = "Array";
        else if (*(long long *)pay & (1LL << 36)) k = "Module";
        break;
    }
    case 13: k = "Error"; break;
    }
    if (k) snprintf(buf, sizeof buf, "[object %s]", k);
    long long n = (long long)strlen(buf);
    char *base = (char *)kml_alloc(KML_HEAP_RAW, (size_t)n + 9);
    *(long long *)base = n;
    memcpy(base + 8, buf, (size_t)n + 1);
    return base + 8;
}

/* The name of the Error subclass a boxed error was made by, or "" for a
   builtin error kind (whose constructor is its name). */
char *__kml_error_ctor_name(long long word) {
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    const char *k = "";
    if (tag == 6 && pay && (*(long long *)pay & (1LL << 48)) && (*(long long *)pay & 0xFFFFFFFFLL) >= 1000) {
        const char *name = __kml_obj_name((void *)pay);
        if (name) k = name;
    }
    long long n = (long long)strlen(k);
    char *base = (char *)kml_alloc(KML_HEAP_RAW, (size_t)n + 9);
    *(long long *)base = n;
    memcpy(base + 8, k, (size_t)n + 1);
    return base + 8;
}

/* __kml_any_view_bytes: the bytes of a typed array or Buffer held in an
   `any` (an ArrayBufferView), their count returned and their address stored
   in *data; -1 for any other value. */
long long __kml_any_view_bytes(long long word, char **data) {
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    if (tag != 7) return -1;
    KjBox *b = (KjBox *)pay;
    if (!b || !b->typed) return -1;
    int size;
    switch (b->kind) {
    case KJ_F64: case KJ_I64: case KJ_U64: size = 8; break;
    case KJ_F32: case KJ_I32: case KJ_U32: size = 4; break;
    case KJ_I16: case KJ_U16: size = 2; break;
    case KJ_I8: case KJ_U8: size = 1; break;
    default: return -1;
    }
    *data = kj_data(b);
    return kj_len(b) * size;
}

/* The entry console.log shows inside a promise's braces: `<pending>`, its
 * value, or `<rejected> reason` (an instance of a class extending Promise
 * renders `P [Promise] { entry, fields }`, TDD-00247). wrapper is the
 * promise's box payload. */
char *__kml_promise_inspect_entry(void *wrapper, long long depth) {
    long long pv = 0;
    long long pst = __kml_promise_inspect_parts(wrapper, &pv);
    Sb eb;
    sb_init(&eb);
    if (pst == 0) {
        sb_cstr(&eb, "<pending>");
    } else {
        if (pst == 2) sb_cstr(&eb, "<rejected> ");
        long long vt, vp;
        nb_decode(pv, &vt, &vp);
        inspect_val(&eb, vt, vp, depth + 1);
    }
    return sb_finish(&eb);
}

void __kml_throw_nullderef(const char *msg) __attribute__((noreturn));

/* A callback that is no function (`[1].map(5)`): V8's TypeError, the value
   described by its type (typeof, given as `type`) and a primitive by its
   value, as `number 5 is not a function`. A function returns. */
void __kml_require_callable(long long word, const char *type) {
    if (strcmp(type, "function") == 0) return;
    long long tag, pay;
    nb_decode(word, &tag, &pay);
    Sb b;
    sb_init(&b);
    if (strcmp(type, "undefined") == 0) sb_cstr(&b, "undefined");
    else if (tag == 4) sb_cstr(&b, "object null");
    else if (strcmp(type, "number") == 0) { sb_cstr(&b, "number "); sb_number(&b, tag, pay); }
    else if (strcmp(type, "string") == 0) {
        sb_cstr(&b, "string \"");
        sb_raw(&b, (const char *)pay, (size_t)KML_STR_LEN(pay));
        sb_ch(&b, '"');
    } else if (strcmp(type, "boolean") == 0) sb_cstr(&b, pay ? "boolean true" : "boolean false");
    else sb_cstr(&b, type);
    sb_cstr(&b, " is not a function");
    __kml_throw_nullderef(sb_finish(&b));
}

/* A rejected write or delete on a dynamic object o, in V8's words: code 1 a
   read-only property, 2 an addition to a non-extensible object, 3 a
   getter-only accessor, 4 a non-configurable delete. The object reads as
   `#<Ctor>`, or `[object Object]` with a null prototype, `[object Module]`
   for a module namespace. */
static void set_throw(const char *desc, const char *key, long long code) __attribute__((noreturn));

void __kml_dyn_set_throw(char *o, const char *key, long long code) {
    long long flags = *(long long *)o;
    char desc[128];
    const char *cn = ctor_name(o);
    if (flags & (1LL << 36)) snprintf(desc, sizeof desc, "[object Module]");
    else if (flags & (1LL << 34)) snprintf(desc, sizeof desc, "[object Object]");
    else snprintf(desc, sizeof desc, "#<%.100s>", cn ? cn : "Object");
    set_throw(desc, key, code);
}

/* __kml_dyn_set_throw for a static object (a frozen literal or class
   instance): `#<Ctor>` by its class's name. */
void __kml_static_set_throw(void *o, const char *key, long long code) {
    char desc[128];
    const char *cn = __kml_obj_name(o);
    snprintf(desc, sizeof desc, "#<%.100s>", cn && *cn ? cn : "Object");
    set_throw(desc, key, code);
}

/* The strict-mode TypeError of a property write on a primitive of the given
 * typeof, whose String() is str: a string's length and indices are read-only
 * properties, any other key would be a new one. */
void __kml_prim_set_throw(const char *type, const char *str, const char *key) {
    Sb b;
    sb_init(&b);
    int ro = 0;
    if (strcmp(type, "string") == 0) {
        if (strcmp(key, "length") == 0) ro = 1;
        else if (*key >= '0' && *key <= '9' && (key[0] != '0' || !key[1])) {
            char *end;
            long long i = strtoll(key, &end, 10);
            ro = !*end && i < kml_str_u16len(str);
        }
    }
    if (ro) {
        sb_cstr(&b, "Cannot assign to read only property '"); sb_cstr(&b, key);
        sb_cstr(&b, "' of "); sb_cstr(&b, type); sb_cstr(&b, " '"); sb_cstr(&b, str); sb_ch(&b, '\'');
    } else {
        sb_cstr(&b, "Cannot create property '"); sb_cstr(&b, key);
        sb_cstr(&b, "' on "); sb_cstr(&b, type); sb_cstr(&b, " '"); sb_cstr(&b, str); sb_ch(&b, '\'');
    }
    __kml_throw_nullderef(sb_finish(&b));
}

static void set_throw(const char *desc, const char *key, long long code) {
    Sb b;
    sb_init(&b);
    switch (code) {
    case 1:
        sb_cstr(&b, "Cannot assign to read only property '"); sb_cstr(&b, key);
        sb_cstr(&b, "' of object '"); sb_cstr(&b, desc); sb_ch(&b, '\'');
        break;
    case 2:
        sb_cstr(&b, "Cannot add property "); sb_cstr(&b, key); sb_cstr(&b, ", object is not extensible");
        break;
    case 3:
        sb_cstr(&b, "Cannot set property "); sb_cstr(&b, key); sb_cstr(&b, " of "); sb_cstr(&b, desc);
        sb_cstr(&b, " which has only a getter");
        break;
    default:
        sb_cstr(&b, "Cannot delete property '"); sb_cstr(&b, key); sb_cstr(&b, "' of "); sb_cstr(&b, desc);
    }
    __kml_throw_nullderef(sb_finish(&b));
}
