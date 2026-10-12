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
#define KSTR(id, s) static const struct { i64 len; char d[sizeof(s)]; } id = { sizeof(s) - 1, s }
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
    i64 h = *(const i64 *)cell;
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

/* An iterable host box's entries as a boxed array, else the value itself. */
i64 __kml_host_iter(i64 v) {
    void *cell = box_cell(v);
    if (!cell) return v;
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
