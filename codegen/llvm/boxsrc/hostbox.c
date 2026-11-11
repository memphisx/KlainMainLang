/* hostbox.c — the dispatchers over a boxed host handle (a Map, a Date, a
 * RegExp, …: TDD-00230 P3.3; in C per TDD-00240). A host box is a cell
 * { i64 header, ptr handle }; each host layout the program boxes registers
 * a row under its header word (unit kind 2):
 *   { i64 hdr, ptr tab, ptr tostr, ptr classTag, ptr className, ptr iter,
 *     ptr tojson, ptr toprim }
 * where tab is { effective depth, max depth, inspect routine per depth }.
 * The routines the rows name are generated with the program. */

typedef long long i64;
typedef unsigned long long u64;

typedef struct {
    i64 hdr;
    void **tab;
    void *(*tostr)(void *cell);
    const char *classtag;
    const char *classname;
    i64 (*iter)(void *cell);
    void *(*tojson)(void *cell);
    i64 (*toprim)(void *cell, i64 v, int hint);
} HostRow;

extern const void *__kml_unit_find(i64 kind, i64 id);
/* util.inspect's `depth` option, and the indentation it shifts back. */
extern i64 __kml_inspect_opt_depth;
extern i64 __kml_inspect_indent_shift;

#define KML_UNIT_HOST 2
#define KSTR(id, s) static const struct { i64 len; char d[sizeof(s)]; } id = { KML_STR_ASCII_HDR(sizeof(s) - 1), s }
KSTR(k_object, "[Object]");
KSTR(k_objobj, "[object Object]");

#define KML_UNIT_CLASS_PARENT 7
#define KML_ID_MASK 0xFFFFFFFFLL
#define KML_HDR_MAGIC (0x4B4DLL << 32)
#define KML_HDR_MAGIC_MASK (0x7FFFLL << 32)
#define KML_HOST_FLAG (1LL << 49)
#define KML_HOST_INLINE (1LL << 50)

/* A host value's row. An instance of a class extending Map or Set
 * (TDD-00247) carries its class tag: its row is the host layout its class
 * chain reaches, whose body follows the tag word as a host's does. */
static const HostRow *host_row(const void *cell) {
    i64 h = *(const i64 *)cell & ~KML_HDR_STATE_MASK;
    const HostRow *row = (const HostRow *)__kml_unit_find(KML_UNIT_HOST, h);
    if (row || (h & KML_HDR_MAGIC_MASK) != KML_HDR_MAGIC || (h & KML_HOST_FLAG)) return row;
    i64 id = h & KML_ID_MASK;
    for (int depth = 0; depth < 4096; depth++) {
        const i64 *p = (const i64 *)__kml_unit_find(KML_UNIT_CLASS_PARENT, id);
        if (!p) return 0;
        id = p[1];
        row = (const HostRow *)__kml_unit_find(KML_UNIT_HOST, KML_HDR_MAGIC | KML_HOST_FLAG | KML_HOST_INLINE | id);
        if (row) return row;
    }
    return 0;
}

/* The cell of a boxed object word (kind 1, below the number range), or null. */
static void *box_cell(i64 w) {
    u64 v = (u64)w;
    if ((v & 7) != 1 || v < 65536 || v >= (1ULL << 49)) return 0;
    return (void *)(v & ~7ULL);
}

/* A host value as console.log renders it. The routines cap nesting at the
 * compile-time depth; the `depth` option moves the cap at run time, so the
 * depth shifts by the difference (clamped to the routines generated). */
void *__kml_host_inspect(void *cell, i64 depth0) {
    const HostRow *row = host_row(cell);
    if (!row) return (void *)k_object.d;
    i64 eff = (i64)row->tab[0], maxd = (i64)row->tab[1];
    i64 d = depth0 + (eff - __kml_inspect_opt_depth);
    if (d < 0) d = 0;
    if (d > maxd) d = maxd;
    i64 old = __kml_inspect_indent_shift;
    __kml_inspect_indent_shift = d - depth0;
    void *r = ((void *(*)(void *))row->tab[2 + d])(cell);
    __kml_inspect_indent_shift = old;
    return r;
}

/* Object.prototype.toString's `[object Map]`, whatever the class's own
 * toString. */
void *__kml_host_class_tag(void *cell) {
    const HostRow *row = host_row(cell);
    return (void *)(row && row->classtag ? row->classtag : k_objobj.d);
}

/* String(box): a class with a toString of its own (a RegExp's `/a/g`)
 * renders through the typed conversion; any other is `[object Map]`. */
void *__kml_host_tag(void *cell) {
    const HostRow *row = host_row(cell);
    if (row && row->tostr) return row->tostr(cell);
    return __kml_host_class_tag(cell);
}

/* `box instanceof <host class>`: the box's row names that class. */
_Bool __kml_host_is(i64 v, const char *name) {
    void *cell = box_cell(v);
    if (!cell) return 0;
    const HostRow *row = host_row(cell);
    if (!row || !row->classname) return 0;
    const char *a = row->classname;
    while (*a && *a == *name) a++, name++;
    return *a == *name;
}

/* A host box's class name (its constructor's, `Map`), or null for any other
 * value. */
void *__kml_host_class_name(i64 v) {
    void *cell = box_cell(v);
    if (!cell) return 0;
    const HostRow *row = host_row(cell);
    return row ? (void *)row->classname : 0;
}

/* A typed array out of bounds of its resizable buffer (views.c, linked
 * with typed arrays over buffers). */
extern i64 __kml_views_oob __attribute__((weak));
extern i64 __kml_view_oob(void *h) __attribute__((weak));
extern void *__kml_str_from_cstr(const char *c);
extern void __kml_throw_nullderef(const char *msg) __attribute__((noreturn));

/* An iterable host box's entries as a boxed array, else the value itself.
 * A typed array out of bounds of its resizable buffer throws, as its
 * values() does. */
i64 __kml_host_iter(i64 v) {
    void *cell = box_cell(v);
    if (!cell) return v;
    if (&__kml_views_oob && __kml_views_oob && __kml_view_oob((char *)cell + 8))
        __kml_throw_nullderef(__kml_str_from_cstr("Cannot perform %TypedArray%.prototype.values on a detached ArrayBuffer"));
    const HostRow *row = host_row(cell);
    return row && row->iter ? row->iter(cell) : v;
}

/* The string a host box's toJSON returns (a Date's toISOString), or null. */
void *__kml_host_tojson(void *cell) {
    const HostRow *row = host_row(cell);
    return row && row->tojson ? row->tojson(cell) : 0;
}

/* A host box's primitive (a Date's time value for the number hint, its
 * toString otherwise), or the box itself. */
i64 __kml_host_toprim(i64 v, int hint) {
    void *cell = (void *)((u64)v & ~7ULL);
    if (!cell) return v;
    const HostRow *row = host_row(cell);
    return row && row->toprim ? row->toprim(cell, v, hint & 0xff) : v;
}

/* ---- class references (emit_classref_rows.go) ----
 * Each constructor reference the program can reach registers a row under
 * its payload (unit kind KML_UNIT_CTOR): what util.inspect shows for it, its
 * static members' count, keys and values, its instanceof test, its kind
 * (1 class, 2 builtin), its dynamic construct, and its static get and set. */
typedef struct {
    i64 payload;
    void *shown;
    i64 (*nstatic)(void *p);
    void *(*skey)(void *p, i64 i);
    i64 (*sget)(void *p, i64 i);
    _Bool (*inst)(i64 v);
    i64 kind;
    void *construct;
    i64 (*get)(const char *key);
    _Bool (*set)(const char *key, i64 v); /* whether key is a static it writes */
} CtorRow;

extern unsigned char __kml_nb_tag(long long w);
extern long long __kml_nb_pay(long long w);
extern int strcmp(const char *a, const char *b);

static const CtorRow *ctor_row(void *p) { return (const CtorRow *)__kml_unit_find(KML_UNIT_CTOR, (i64)p); }

/* The row of a boxed constructor reference, or null for any other value. */
static const CtorRow *ctor_row_of(i64 c) {
    if (__kml_nb_tag(c) != KML_TAG_FUNCREF) return 0;
    return (const CtorRow *)__kml_unit_find(KML_UNIT_CTOR, __kml_nb_pay(c));
}

void *__kml_classref_shown(void *p) {
    const CtorRow *r = ctor_row(p);
    return r ? r->shown : 0;
}
/* A property a class value gains that is none of its statics (`K.extra =
 * 7`, `Object.defineProperty(K, k, …)`, a symbol key): kept per reference
 * in its own bag, with its attributes, after its static fields. */
typedef struct ClassExtra {
    void *ref;
    void *bag;
    struct ClassExtra *next;
} ClassExtra;
static ClassExtra *class_extras;

extern void *__kml_dynobj_new(void);
extern i64 __kml_dynobj_find(void *o, const char *key);
extern i64 __kml_dynobj_get_recv(void *o, const char *key, i64 recv);
extern i64 __kml_dynobj_setv(void *o, const char *key, i64 v);
extern char **__kml_dynobj_keys_enum_c(void *o, i64 *n);
extern i64 __kml_dynobj_get(void *o, const char *key);
extern _Bool __kml_dynobj_delete(void *o, const char *key);

/* The reference's own bag, made on first need when make is set. */
void *__kml_classref_extra(void *p, int make) {
    for (ClassExtra *x = class_extras; x; x = x->next)
        if (x->ref == p) return x->bag;
    if (!make) return 0;
    ClassExtra *x = kml_alloc(KML_HEAP_WORDS, sizeof *x);
    x->ref = p;
    x->bag = __kml_dynobj_new();
    x->next = class_extras;
    class_extras = x;
    return x->bag;
}
/* The reference whose extra bag bag is (a constructor given as another
 * function's prototype: Object.setPrototypeOf(T, Buffer)), boxed; 0 for
 * any other bag. */
i64 __kml_classref_of_extra(void *bag) {
    for (ClassExtra *x = class_extras; x; x = x->next)
        if (x->bag == bag) return (i64)x->ref | KML_NB_FUNCREF_KIND;
    return 0;
}
/* The bag's enumerable string keys (a symbol's key starts with \x01). */
static char **extra_keys(void *p, i64 *n) {
    *n = 0;
    void *bag = __kml_classref_extra(p, 0);
    if (!bag) return 0;
    i64 m = 0;
    char **ks = __kml_dynobj_keys_enum_c(bag, &m);
    i64 j = 0;
    for (i64 i = 0; i < m; i++)
        if (ks[i][0] != '\x01') ks[j++] = ks[i];
    *n = j;
    return ks;
}
static i64 class_static_count(const CtorRow *r, void *p) { return r && r->nstatic ? r->nstatic(p) : 0; }
static i64 ref_box(void *p) { return (i64)p | KML_NB_FUNCREF_KIND; }

i64 __kml_classref_nstatic(void *p) {
    const CtorRow *r = ctor_row(p);
    i64 n = 0;
    extra_keys(p, &n);
    return class_static_count(r, p) + n;
}
void *__kml_classref_skey(void *p, i64 i) {
    const CtorRow *r = ctor_row(p);
    i64 n = class_static_count(r, p);
    if (i < n) return r->skey ? r->skey(p, i) : 0;
    i64 m = 0;
    char **ks = extra_keys(p, &m);
    return i - n < m ? __kml_str_from_cstr(ks[i - n]) : 0;
}
i64 __kml_classref_sget(void *p, i64 i) {
    const CtorRow *r = ctor_row(p);
    i64 n = class_static_count(r, p);
    if (i < n) return r->sget ? r->sget(p, i) : KML_NB_UNDEFINED;
    i64 m = 0;
    char **ks = extra_keys(p, &m);
    return i - n < m ? __kml_dynobj_get_recv(__kml_classref_extra(p, 0), ks[i - n], ref_box(p)) : KML_NB_UNDEFINED;
}
extern void __kml_dynobj_copy_own(void *dst, void *src);
/* A snapshot of the reference's statics, with its own bag's properties. */
void __kml_classref_bag_merge(void *p, void *snap) {
    if (snap) __kml_dynobj_copy_own(snap, __kml_classref_extra(p, 0));
}
/* Whether the reference's own bag has key. */
_Bool __kml_classref_extra_has(void *p, const char *key) {
    void *bag = __kml_classref_extra(p, 0);
    return bag && __kml_dynobj_find(bag, key) >= 0;
}
/* Deletes key from the reference's own bag: whether it was there. */
_Bool __kml_classref_extra_delete(void *p, const char *key) {
    void *bag = __kml_classref_extra(p, 0);
    return bag && __kml_dynobj_delete(bag, key);
}
/* Whether key names one of the reference's statics: a present field, or a
 * member its row reads. */
static _Bool class_static_key(const CtorRow *r, void *p, const char *key) {
    i64 n = class_static_count(r, p);
    for (i64 i = 0; i < n; i++) {
        const char *k = r->skey ? (const char *)r->skey(p, i) : 0;
        if (k && strcmp(k, key) == 0) return 1;
    }
    return r && r->get && r->get(key) != KML_NB_UNDEFINED;
}
/* A static member through a reference; a builtin constructor (an Error
 * kind) has only its length, 1. */
extern i64 __kml_dynobj_setv_recv(void *o, const char *key, i64 v, i64 recv);
i64 __kml_classref_get(void *p, const char *key) {
    const CtorRow *r = ctor_row(p);
    if (__kml_classref_extra_has(p, key)) return __kml_dynobj_get_recv(__kml_classref_extra(p, 0), key, ref_box(p));
    if (r && r->get) return r->get(key);
    if (strcmp(key, "length") == 0) {
        double one = 1.0;
        i64 bits;
        __builtin_memcpy(&bits, &one, sizeof bits);
        return bits + KML_NB_DOUBLE_OFFSET;
    }
    return KML_NB_UNDEFINED;
}
void __kml_classref_set(void *p, const char *key, i64 v) {
    const CtorRow *r = ctor_row(p);
    if (!__kml_classref_extra_has(p, key)) {
        /* (A setter-only static reads as undefined: the row says it wrote.) */
        if (r && r->set && r->set(key, v)) return;
        /* A static method is a writable data property: the new value
         * shadows it in the reference's own bag. */
        _Bool method = r && r->get && strcmp(key, "length") != 0 && strcmp(key, "name") != 0 &&
                       strcmp(key, "prototype") != 0 && __kml_nb_tag(r->get(key)) == 12;
        if (!method && (class_static_key(r, p, key) || (!r && strcmp(key, "length") == 0))) return;
    } else {
        /* An accessor defined on the class runs with the class as this;
         * 4 is a data property, stored below. */
        if (__kml_dynobj_setv_recv(__kml_classref_extra(p, 0), key, v, ref_box(p)) != 4) return;
    }
    __kml_dynobj_setv(__kml_classref_extra(p, 1), key, v);
}
/* `v instanceof c` with c a reference held in `any`. */
_Bool __kml_dyn_instanceof(i64 v, i64 c) {
    const CtorRow *r = ctor_row_of(c);
    return r && r->inst ? r->inst(v) : 0;
}
/* A reference's kind (1 when it has no row); 0 for any other value. */
double __kml_ctor_kind(i64 c) {
    if (__kml_nb_tag(c) != KML_TAG_FUNCREF) return 0.0;
    const CtorRow *r = ctor_row_of(c);
    return (double)(r ? r->kind : 1);
}

/* `obj.constructor` for an instance carrying its type id (unit kind
 * KML_UNIT_CTOR_OF: { i64 id, ptr payload }), boxed as a reference, or
 * undefined. */
typedef struct {
    i64 id;
    void *payload;
} CtorOfRow;
const void *__kml_classrec_of(const void *inst);

i64 __kml_constructor_of(void *o) {
    i64 id = *(i64 *)o & KML_HDR_ID_MASK;
    const CtorOfRow *r = (const CtorOfRow *)__kml_unit_find(KML_UNIT_CTOR_OF, id);
    if (!r || !r->payload) return KML_NB_UNDEFINED;
    // An instance of a local class: the evaluation that built it, whose
    // record holds the bindings a `new` through this value needs.
    const void *rec = __kml_classrec_of(o);
    return (i64)(rec ? rec : r->payload) | KML_NB_FUNCREF_KIND;
}
