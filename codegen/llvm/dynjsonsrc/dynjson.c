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

extern void __kml_dtoa(char *buf, double v);
/* A runtime string's length: its header word. */
static long long kj_str_len(const char *s) { return *(const long long *)(s - 8); }
extern char *__kml_fn_inspect_dyn(void **rec, long long depth); /* fnmeta.c (TDD-00229) */
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
extern long long __kml_inspect_opt_compact, __kml_inspect_opt_sorted, __kml_inspect_opt_depth, __kml_inspect_opt_break, __kml_inspect_opt_maxarr;
/* A host box's toJSON result as a string (a Date's ISO form), or 0. */
extern char *__kml_host_tojson(void *cell);
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

/* kj_tojson is `v.toJSON(key)` when v (a dynamic or static object) has a
   toJSON function — own, inherited or a class method — else v itself: a
   tag-12 record called as fn(env, this, argc, argv). */
static long long kj_tojson(long long tag, long long pay, long long w, const char *key) {
    long long f = tag == 10 ? __kml_dynobj_get((char *)pay, "toJSON") : __kml_obj_get((void *)pay, "toJSON");
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
            free(probe.d);
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
        free(probe.d);
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
        sb_ch(b, typed ? '{' : '[');
        for (long long i = 0; i < n; i++) {
            long long etag, epay;
            nb_decode(kj_elem_box((const struct KjBoxS *)pay, i), &etag, &epay);
            kj_key(depth + 1, NULL, i);
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
        kj_key(depth + 1, obj_key(o, i), 0);
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
        free(b.d);
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

/* inspect_obj renders a dynamic object the way util.inspect (console.log)
   does: `{ a: 1, b: 'x' }`, `{}` when empty, keys in ES enumeration order,
   non-enumerable entries skipped, accessors shown as [Getter]/[Setter]
   (never invoked), and — Node's default depth — anything nested deeper than
   two object levels collapsed to [Object]. */
extern _Bool __kml_key_is_symbol(const char *k);

/* dyn_tag: a dynamic object's own or inherited Symbol.toStringTag, when a
   string (`Math`'s "Math"), else NULL. */
static const char *dyn_tag(char *o) {
    long long t, p;
    nb_decode(__kml_dynobj_get(o, "@@toStringTag"), &t, &p);
    return t == 2 ? (const char *)p : NULL;
}

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
    if (stag) snprintf(open, sizeof open, "%s [%s] {", nullproto ? "[Object: null prototype]" : "Object", stag);
    else snprintf(open, sizeof open, "%s", nullproto ? "[Object: null prototype] {" : "{");
    if (visible == 0) { free(order); sb_cstr(b, open); sb_ch(b, '}'); return; }
    if (depth > __kml_inspect_opt_depth) { free(order); sb_cstr(b, nullproto ? "[Object: null prototype]" : "[Object]"); return; }
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
        int symKey = k && (strncmp(k, "\x01@@sym:", 7) == 0 || __kml_key_is_symbol(k));
        if (symKey != pass) continue;
        Sb eb;
        sb_init(&eb);
        if (symKey && k[0] == '@') {
            /* A well-known symbol's member key ("@@iterator"). */
            sb_cstr(&eb, "Symbol(Symbol.");
            sb_cstr(&eb, k + 2);
            sb_ch(&eb, ')');
        } else if (symKey) {
            void *sym = NULL;
            sscanf(k + 7, "%p", &sym);
            const char *desc = sym ? *(const char **)((char *)sym + 8) : NULL;
            sb_cstr(&eb, "Symbol(");
            sb_cstr(&eb, desc ? desc : "");
            sb_ch(&eb, ')');
        } else if (k && strcmp(k, "__proto__") == 0) {
            /* An own `__proto__` key (computed / JSON-parsed) is quoted in
               brackets so it can't read as the prototype setter. */
            sb_cstr(&eb, "['__proto__']");
        } else if (key_is_ident(k)) {
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
    /* The prefix is part of the opening brace, so it counts toward the
       80-column single-line budget exactly as Node's braces[0] does. */
    char *out = __kml_inspect_end(list, open, "}", 2 * depth, depth, 0, 0);
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
    if (depth > __kml_inspect_opt_depth) { sb_cstr(b, "[Array]"); return; }
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

/* inspect_static renders a static object through its layout row as
   util.inspect does: `Name { a: 1 }` for a class instance, `{ a: 1 }` for a
   plain object, `[Name]`/`[Object]` past the depth limit. */
static void inspect_static(Sb *b, void *o, long long n, int depth) {
    /* A class's own `[inspect.custom]` renders it. */
    char *custom = __kml_obj_inspect_custom(o, depth);
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
    for (long long i = 0; i < n; i++) {
        const char *k = __kml_obj_key(o, i);
        if (!k || !__kml_obj_has(o, k)) continue;
        Sb eb;
        sb_init(&eb);
        if (key_is_ident(k)) {
            sb_cstr(&eb, k);
        } else {
            char *q = __kml_inspect_quote(k);
            sb_cstr(&eb, q);
            free(q - 8);
        }
        sb_cstr(&eb, ": ");
        long long etag, epay;
        nb_decode(__kml_obj_get(o, k), &etag, &epay);
        inspect_val(&eb, etag, epay, depth + 1);
        __kml_inspect_push(list, sb_finish(&eb));
    }
    char *out = __kml_inspect_end(list, open, "}", 2 * depth, depth, 0, 0);
    sb_cstr(b, out);
    free(out - 8);
}

/* An Error as util.inspect shows it, without the stack's frames: the
   stack's first line — `Name: message`, with Node's `Ctor [Name]` when the
   class is not what the name says (`class G extends Error {}` shows
   `G [Error]: g`) — then its own enumerable properties, one per line. */
static int error_header_key(const char *k) {
    return strcmp(k, "name") == 0 || strcmp(k, "message") == 0;
}

static void inspect_error(Sb *b, void *o, int depth) {
    const char *head = __kml_error_inspect(o);
    const char *ctor = __kml_obj_name(o);
    size_t nl = strcspn(head, ":");
    int improve = 0;
    if (ctor && *ctor && nl >= 5 && strncmp(head + nl - 5, "Error", 5) == 0 &&
        (strlen(ctor) != nl || strncmp(ctor, head, nl) != 0)) {
        improve = 1;
    }
    if (improve) {
        sb_cstr(b, ctor);
        sb_cstr(b, " [");
        for (size_t i = 0; i < nl; i++) sb_ch(b, head[i]);
        sb_ch(b, ']');
        sb_cstr(b, head + nl);
    } else {
        sb_cstr(b, head);
    }
    long long n = __kml_obj_nkeys(o);
    long long shown = 0;
    for (long long i = 0; i < n; i++) {
        const char *k = __kml_obj_key(o, i);
            if (k && __kml_obj_has(o, k)) shown++;
    }
    if (shown == 0) return;
    if (depth > __kml_inspect_opt_depth) {
        sb_cstr(b, " { ... }");
        return;
    }
    sb_cstr(b, " {");
    long long done = 0;
    for (long long i = 0; i < n; i++) {
        const char *k = __kml_obj_key(o, i);
        if (!k || error_header_key(k) || !__kml_obj_has(o, k)) continue;
        if (done++ > 0) sb_ch(b, ',');
        sb_ch(b, '\n');
        for (int s = 0; s < 2 * (depth + 1); s++) sb_ch(b, ' ');
        if (key_is_ident(k)) {
            sb_cstr(b, k);
        } else {
            char *q = __kml_inspect_quote(k);
            sb_cstr(b, q);
            free(q - 8);
        }
        sb_cstr(b, ": ");
        long long etag, epay;
        nb_decode(__kml_obj_get(o, k), &etag, &epay);
        inspect_val(b, etag, epay, depth + 1);
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
            free(q - 8);
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
    free(open - 8);
    sb_cstr(b, out);
    free(out - 8);
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
                free(out - 8);
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
            free(fs - 8);
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
   maxArrayLength }) of a box: the options hold for this one rendering
   (compact 0 is `false`). */
char *__kml_inspect_opts(long long word, double depth, double compact, double sorted, double breakLength, double maxArrayLength) {
    long long od = __kml_inspect_opt_depth, oc = __kml_inspect_opt_compact, os = __kml_inspect_opt_sorted;
    long long ob = __kml_inspect_opt_break, om = __kml_inspect_opt_maxarr;
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
    if (!b->typed) return 0;
    const char *n = kj_typed_name(b);
    for (int i = 0; i < 11; i++)
        if (strcmp(kj_ctor_names[i], n) == 0) return i + 1;
    return 0;
}

static const char *kj_typed_name_of(const void *box) {
    const KjBox *b = (const KjBox *)box;
    return b->typed ? kj_typed_name(b) : "Array";
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
    case KJ_BOXED: return b->boxer(d, i);
    case KJ_ARRAY: {
        KjBox *nb = (KjBox *)malloc(sizeof(KjBox));
        if (!kj_inner(b, i, nb)) { free(nb); return 10; }
        return (long long)nb | 2; /* an array-box word (kind bits 2) */
    }
    default: return 10;
    }
}

/* __kml_anyarr_typed_is: whether a boxed static array is the TypedArray
   named name (`x instanceof Uint8Array` on an `any`). */
int __kml_anyarr_typed_is(void *box, const char *name) {
    const KjBox *b = (const KjBox *)box;
    if (!b || !b->typed) return 0;
    /* A Buffer (typed 3) is a Uint8Array subclass. */
    if (strcmp(name, "Buffer") == 0) return b->typed == 3;
    return strcmp(kj_typed_name(b), name) == 0;
}

/* __kml_anyarr_words: a boxed static array read as an `any[]` — its own
   header when its elements already are NaN-boxed words, else a fresh header
   whose elements are each boxed as kj_elem_box reads them (an element kind
   the box cannot describe reads as undefined). */
char *__kml_anyarr_words(void *box) {
    KjBox *b = (KjBox *)box;
    if (b->kind == KJ_ANY) return b->hdr;
    long long n = kj_len(b);
    long long *d = (long long *)malloc((size_t)(n > 0 ? n : 1) * 8);
    for (long long i = 0; i < n; i++) d[i] = b->kind < 0 ? 10 : kj_elem_box(b, i);
    char *h = (char *)malloc(16);
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
        w->d = (long long *)realloc(w->d, (size_t)w->cap * 8);
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
    if (!w.d) w.d = (long long *)malloc(8);
    char *h = (char *)malloc(16);
    *(long long **)h = w.d;
    *(long long *)(h + 8) = w.n;
    return h;
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
        KjBox *rb = (KjBox *)calloc(1, sizeof *rb);
        rb->hdr = (char *)raw;
        rb->kind = KJ_STRING;
        return nb_pack(7, (long long)rb);
    }
    char *end;
    long long idx = strtoll(key, &end, 10);
    if (end == key || *end != 0 || idx < 0) return 10;
    if (idx >= kj_len(b)) return 10;
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
    KjTmpl *t = (KjTmpl *)malloc(sizeof *t);
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
   index answers that one-character string (undefined past the end, as JS;
   strings are byte-indexed, as `s[i]` is), anything else undefined. */
long long __kml_str_get_by_key(const char *s, const char *key) {
    char *end;
    long long idx = strtoll(key, &end, 10);
    if (end == key || *end != 0 || idx < 0 || (key[0] == '0' && key[1] != 0) || key[0] == '+' || key[0] == ' ') return 10;
    if (idx >= (long long)strlen(s)) return 10;
    char *base = (char *)malloc(8 + 2);
    *(long long *)base = 1;
    base[8] = s[idx];
    base[9] = 0;
    return nb_pack(2, (long long)(base + 8));
}

extern long long __kml_toprimitive(long long v, signed char hint);
extern double __kml_any_tonum(long long v);

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
        data = (long long *)malloc((size_t)(n > 0 ? n : 1) * 8);
        for (long long i = 0; i < n; i++) data[i] = kj_elem_box(b, i);
    } else if (tag == 11) {
        char *a = (char *)pay;
        n = arr_len(a);
        data = (long long *)malloc((size_t)(n > 0 ? n : 1) * 8);
        for (long long i = 0; i < n; i++) data[i] = nb_pack(arr_tag(a, i), arr_pay(a, i));
    } else {
        return NULL;
    }
    char *hdr = (char *)malloc(16);
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

/* kj_store writes the NaN-boxed word w as element i of a box's data d,
   converted to the box's element kind. */
static void kj_store(const KjBox *b, char *d, long long i, long long w) {
    switch (b->kind) {
    case KJ_BOXED: b->unboxer(d, i, w); break;
    case KJ_F64: ((double *)d)[i] = any_elem_tonum(w); break;
    case KJ_F32: ((float *)d)[i] = (float)any_elem_tonum(w); break;
    case KJ_I64: ((long long *)d)[i] = (long long)any_elem_tonum(w); break;
    case KJ_U64: ((unsigned long long *)d)[i] = (unsigned long long)any_elem_tonum(w); break;
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
            *(char **)(a + 16) = (char *)realloc(arr_data(a), (size_t)(n > 0 ? n : 1) * 16);
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
    char *d = (char *)calloc((size_t)(n > 0 ? n : 1), (size_t)width);
    for (long long i = 0; i < n; i++) kj_store(b, d, i, src[i]);
    *(char **)b->hdr = d;
    *(long long *)(b->hdr + 8) = n;
}

/* __kml_anyarr_set_by_key: `x[k] = w` on an `any` holding a boxed static
   array. A canonical index stores the element (converted to the array's
   element kind); past the end, a plain array grows to hold it (the gap
   reads as its kind's zero value) and a TypedArray ignores the write, as
   JS does. False for any other key, or an element kind the box cannot
   describe. */
int __kml_anyarr_set_by_key(void *box, const char *key, long long w) {
    KjBox *b = (KjBox *)box;
    if (b->kind < 0) return 0;
    unsigned long long idx;
    if (!es_is_index(key, &idx)) return 0;
    if (__kml_array_integrity_any && !b->typed) __kml_array_guard(b->hdr, 0, (long long)idx, 0);
    long long n = kj_len(b);
    if ((long long)idx >= n) {
        if (b->typed) return 1;
        int width = b->kind == KJ_BOXED ? b->esize : kj_width[b->kind];
        long long m = (long long)idx + 1;
        char *d = (char *)calloc((size_t)m, (size_t)width);
        if (n > 0) memcpy(d, kj_data(b), (size_t)(n * width));
        if (b->kind == KJ_ANY)
            for (long long i = n; i < m; i++) ((long long *)d)[i] = 10;
        *(char **)b->hdr = d;
        *(long long *)(b->hdr + 8) = m;
    }
    kj_store(b, kj_data(b), (long long)idx, w);
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
    long long len = kj_len(bx);
    char open[64] = "[";
    if (bx->typed) {
        /* Node: `Int32Array(2) [ 8, 9 ]`, `Uint8Array(0) []`. */
        snprintf(open, sizeof open, "%s(%lld) [", kj_typed_name(bx), len);
    }
    if (len == 0) { sb_cstr(b, open); sb_cstr(b, "]"); return; }
    if (depth > __kml_inspect_opt_depth) {
        /* Node names the class past the depth: `[Array]`, `[Uint8Array]`. */
        if (bx->typed) { sb_cstr(b, "["); sb_cstr(b, kj_typed_name(bx)); sb_cstr(b, "]"); }
        else sb_cstr(b, "[Array]");
        return;
    }
    void *list = __kml_inspect_begin(depth, 1);
    long long shown = len < KML_INSPECT_MAX_ARRAY ? len : KML_INSPECT_MAX_ARRAY;
    for (long long i = 0; i < shown; i++) {
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
        k = typed == 3 ? "Buffer" : typed ? kj_typed_name((const KjBox *)pay) : "Array";
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
    char *base = (char *)malloc((size_t)n + 9);
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
        if (tag == 2 && pay && *(long long *)((char *)pay - 8) > 28) {
            /* A long string: its first 25 characters and "...", inspected. */
            cut = (char *)malloc(8 + 25 + 3 + 1);
            *(long long *)cut = 28;
            memcpy(cut + 8, (char *)pay, 25);
            memcpy(cut + 8 + 25, "...", 4);
            shownWord = (long long)(cut + 8);
        }
        char *shown = __kml_inspect_opts(shownWord, 2, 3, 0, 80, 100);
        free(cut);
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
            free(pk - 8);
        } else {
            snprintf(buf, sizeof buf, " Received type %s (%s)", type, shown);
        }
        free(shown - 8);
    }
    long long n = (long long)strlen(buf);
    char *base = (char *)malloc((size_t)n + 9);
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
    case 8: case 12: k = "Function"; break;
    case 7: case 11: {
        char *pk = __kml_proto_key(word);
        k = strcmp(pk, "Buffer") == 0 ? "Uint8Array" : pk;
        snprintf(buf, sizeof buf, "[object %s]", k);
        free(pk - 8);
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
        break;
    }
    case 13: k = "Error"; break;
    }
    if (k) snprintf(buf, sizeof buf, "[object %s]", k);
    long long n = (long long)strlen(buf);
    char *base = (char *)malloc((size_t)n + 9);
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
    char *base = (char *)malloc((size_t)n + 9);
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
