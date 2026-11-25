/* errkeys.c — an Error's own enumerable fields beyond its layout (TDD-00240
 * port): the system fields set on it (errno, code, syscall, address, port,
 * path, dest, then node:sqlite's errcode and errstr, each only when
 * present), then the keys of its `extra` bag. The object walkers (inspect's
 * keys, JSON, Object.keys) read them through these three hooks.
 * kml_layout.h is prepended by the compiler. */

typedef long long i64;
typedef unsigned long long u64;

extern i64 __kml_dynobj_count(void *bag);
extern void *__kml_dynobj_key_at(void *bag, i64 i);
extern i64 __kml_dynobj_get(void *bag, const void *key);
extern int strcmp(const char *a, const char *b);
extern void *__kml_dynobj_new(void);
extern void __kml_dynobj_set(void *o, const char *key, i64 v);
extern void __kml_dynobj_set_attrs(void *o, const char *key, i64 v, i64 attrs);
extern i64 __kml_dynobj_find(void *o, const char *key);
extern _Bool __kml_dynobj_delete(void *o, const char *key);
extern i64 __kml_dynobj_attrs_at(void *o, i64 i);
extern i64 __kml_dynobj_rawtag_at(void *o, i64 i);
extern i64 __kml_dynobj_rawpay_at(void *o, i64 i);
extern void __kml_dynobj_patch(void *o, i64 i, i64 tag, i64 pay, i64 attrs);
extern int __kml_error_has_message(void *op);
#define KML_ATTR_ENUM 2 /* dynobj.c's A_ENUM */

#define KSTR(id, s) static const struct { i64 len; char d[sizeof(s)]; } id = { KML_STR_ASCII_HDR(sizeof(s) - 1), s }
KSTR(k_name, "name");
KSTR(k_errno, "errno");
KSTR(k_code, "code");
KSTR(k_syscall, "syscall");
KSTR(k_address, "address");
KSTR(k_port, "port");
KSTR(k_path, "path");
KSTR(k_dest, "dest");
KSTR(k_errcode, "errcode");
KSTR(k_errstr, "errstr");
KSTR(k_cause, "cause");
/* A system field's place among the extra bag's keys: a hidden marker key
 * set when the program assigns the field (__kml_error_note_field), so the
 * walkers list it in insertion order with the bag's own keys. */
KSTR(m_errno, "\x7f" "errno");
KSTR(m_code, "\x7f" "code");
KSTR(m_syscall, "\x7f" "syscall");
KSTR(m_address, "\x7f" "address");
KSTR(m_port, "\x7f" "port");
KSTR(m_path, "\x7f" "path");
KSTR(m_dest, "\x7f" "dest");
KSTR(m_errcode, "\x7f" "errcode");
KSTR(m_errstr, "\x7f" "errstr");

typedef struct {
    const char *name;
    const char *marker;
    int off;
    int f64;   /* a double slot (present when non-zero), else a pointer slot */
    int gated; /* node:sqlite's: a system error's copies of them are internal */
} Fld;

/* Node's order: uvException's errno, code, syscall, path, dest; the host and
 * port form's address and port; node:sqlite's errcode and errstr. */
static const Fld flds[] = {
    {k_errno.d, m_errno.d, KML_ERROR_ERRNO, 1, 0},
    {k_code.d, m_code.d, KML_ERROR_CODE, 0, 0},
    {k_syscall.d, m_syscall.d, KML_ERROR_SYSCALL, 0, 0},
    {k_address.d, m_address.d, KML_ERR_ADDRESS, 0, 0},
    {k_port.d, m_port.d, KML_ERR_PORT, 1, 0},
    {k_path.d, m_path.d, KML_ERROR_PATH, 0, 0},
    {k_dest.d, m_dest.d, KML_ERROR_DEST, 0, 0},
    {k_errcode.d, m_errcode.d, KML_ERROR_ERRCODE, 1, 1},
    {k_errstr.d, m_errstr.d, KML_ERROR_ERRSTR, 0, 1},
};
#define NFLDS ((int)(sizeof flds / sizeof flds[0]))

static int present(const char *o, const Fld *f) {
    int p = f->f64 ? *(const double *)(o + f->off) != 0.0 : *(void *const *)(o + f->off) != 0;
    if (f->gated) p = p && *(const double *)(o + KML_ERROR_ERRNO) == 0.0;
    return p;
}

static void *extra_bag(const char *o) { return *(void *const *)(o + KML_ERR_EXTRA); }

static int is_marker(const char *k) { return k[0] == '\x7f'; }

/* The field a marker key stands for, or -1. */
static int marker_field(const char *k) {
    if (!is_marker(k)) return -1;
    for (int f = 0; f < NFLDS; f++)
        if (!strcmp(k + 1, flds[f].name)) return f;
    return -1;
}

/* Whether the bag holds field f's marker. */
static int marked(void *bag, int f) { return bag && __kml_dynobj_find(bag, flds[f].marker) >= 0; }

/* An Error's own extra keys in Node's order: the present system fields the
 * runtime set (uvException's order), then, in insertion order, the bag's
 * keys and the fields the program assigned. enumOnly skips the bag's
 * non-enumerable entries. Each key goes to out (when non-null); the count
 * is returned. */
static i64 ordered_keys(const char *o, int enumOnly, const char **out) {
    void *bag = extra_bag(o);
    i64 n = 0;
    for (int k = 0; k < NFLDS; k++)
        if (present(o, &flds[k]) && !marked(bag, k)) {
            if (out) out[n] = flds[k].name;
            n++;
        }
    if (!bag) return n;
    for (i64 j = 0; j < __kml_dynobj_count(bag); j++) {
        const char *key = (const char *)__kml_dynobj_key_at(bag, j);
        int f = marker_field(key);
        if (f >= 0) {
            if (!present(o, &flds[f])) continue;
            key = flds[f].name;
        } else if (is_marker(key) || (enumOnly && !(__kml_dynobj_attrs_at(bag, j) & KML_ATTR_ENUM))) {
            continue;
        }
        if (out) out[n] = key;
        n++;
    }
    return n;
}

/* The count of an Error's enumerable extra keys. */
i64 __kml_error_nextra(void *op) { return ordered_keys((const char *)op, 1, 0); }

/* The i-th of an Error's enumerable extra keys. */
void *__kml_error_extra_key(void *op, i64 i) {
    const char *o = (const char *)op;
    i64 n = ordered_keys(o, 1, 0);
    if (i < 0 || i >= n) return 0;
    const char **keys = (const char **)kml_alloc(KML_HEAP_WORDS, (__SIZE_TYPE__)n * 8);
    ordered_keys(o, 1, keys);
    return (void *)keys[i];
}

/* `delete err.key` of a system field: the slot cleared (absent), its place
 * in the order forgotten. 1 when key is one. */
int __kml_error_delete_field(void *op, const char *key) {
    for (int f = 0; f < NFLDS; f++) {
        if (strcmp(key, flds[f].name)) continue;
        char *o = (char *)op;
        if (flds[f].f64) *(double *)(o + flds[f].off) = 0.0;
        else *(void **)(o + flds[f].off) = 0;
        void *bag = extra_bag(o);
        if (bag) __kml_dynobj_delete(bag, flds[f].marker);
        return 1;
    }
    return 0;
}

/* The program assigns system field key on an Error (before the store): a
 * field it does not have yet takes its place in the order now; one it has
 * keeps its place. */
void __kml_error_note_field(void *op, const char *key) {
    for (int f = 0; f < NFLDS; f++) {
        if (strcmp(key, flds[f].name)) continue;
        if (present((const char *)op, &flds[f])) return;
        void **bagp = (void **)((char *)op + KML_ERR_EXTRA);
        if (!*bagp) *bagp = __kml_dynobj_new();
        __kml_dynobj_delete(*bagp, flds[f].marker); /* a deleted field's earlier place */
        __kml_dynobj_set(*bagp, flds[f].marker, 10);
        return;
    }
}

/* A present field's boxed value, else the bag's. An own `name` is listed by
 * __kml_obj_key at its place among the layout's. */
extern void *__kml_error_stack(void *err);

i64 __kml_error_extra_get(void *op, const char *key, int *found) {
    const char *o = (const char *)op;
    /* `stack`, an own non-enumerable property formatted on first read
     * (framesrc/frames.c). */
    if (!strcmp(key, "stack")) {
        *found = 1;
        return (i64)(u64)__kml_error_stack(op);
    }
    if (!strcmp(key, k_name.d) && *(const i64 *)(o + KML_ERR_NAME_OWN) != 0) {
        *found = 1;
        return (i64)(u64) * (void *const *)(o + KML_ERR_NAME);
    }
    for (int k = 0; k < NFLDS; k++) {
        const Fld *f = &flds[k];
        if (strcmp(key, f->name) || !present(o, f)) continue;
        *found = 1;
        if (!f->f64) return (i64)(u64) * (void *const *)(o + f->off);
        double d = *(const double *)(o + f->off);
        i64 bits;
        if (d != d) return 0x7FF8000000000000LL + (1LL << 49);
        __builtin_memcpy(&bits, &d, 8);
        return bits + (1LL << 49);
    }
    void *bag = extra_bag(o);
    if (!bag) {
        *found = 0;
        return 10;
    }
    *found = __kml_dynobj_find(bag, key) >= 0;
    return __kml_dynobj_get(bag, key);
}

/* An assigned `cause` (`e.cause = v`) is an own enumerable property, where
 * the constructor's option is not: its value, already in the slot, is copied
 * into the extra bag, whose keys the walkers list. Each assignment refreshes
 * the copy, so the bag and the slot agree. */
void __kml_error_own_cause(void *op) {
    char *o = (char *)op;
    void **bagp = (void **)(o + KML_ERR_EXTRA);
    if (!*bagp) *bagp = __kml_dynobj_new();
    __kml_dynobj_set(*bagp, k_cause.d, *(i64 *)(o + KML_ERR_CAUSE));
}

/* Sets key on an Error as a non-enumerable own data property (an
 * AggregateError subclass's `errors`), in its extra bag. */
void __kml_error_set_hidden(void *op, const char *key, i64 v) {
    char *o = (char *)op;
    void **bagp = (void **)(o + KML_ERR_EXTRA);
    if (!*bagp) *bagp = __kml_dynobj_new();
    __kml_dynobj_set_attrs(*bagp, key, v, 1 | 4); /* writable, configurable */
}

/* Whether an Error's own key is enumerable now: an entry of its extra bag
 * with the enumerable attribute. */
i64 __kml_error_key_enumerable(void *op, const char *key) {
    void *bag = extra_bag((const char *)op);
    if (!bag) return 0;
    i64 i = __kml_dynobj_find(bag, key);
    return i >= 0 && (__kml_dynobj_attrs_at(bag, i) & KML_ATTR_ENUM) != 0;
}

/* Object.defineProperty on an Error's own key, after its value is stored:
 * enumerable as the descriptor says, else as it was (false for a property
 * the error did not have), as ValidateAndApplyPropertyDescriptor does. */
void __kml_error_define_enum(void *op, const char *key, i64 was, void *desc) {
    void *bag = extra_bag((const char *)op);
    if (!bag) return;
    i64 i = __kml_dynobj_find(bag, key);
    if (i < 0) return;
    i64 en = was;
    if (desc && __kml_dynobj_find(desc, "enumerable") >= 0) {
        i64 v = __kml_dynobj_get(desc, "enumerable");
        en = !(v == 6 || v == 10 || v == 2 || v == (1LL << 49)); /* false, undefined, null, +0 */
    }
    i64 attrs = __kml_dynobj_attrs_at(bag, i);
    attrs = en ? (attrs | KML_ATTR_ENUM) : (attrs & ~(i64)KML_ATTR_ENUM);
    __kml_dynobj_patch(bag, i, __kml_dynobj_rawtag_at(bag, i), __kml_dynobj_rawpay_at(bag, i), attrs);
}

/* The cause the constructor's option set, which is not enumerable:
 * util.inspect shows it after the own properties, as `[cause]`. 0 when
 * there is none (the slot holds undefined, as it does for an explicit
 * `{ cause: undefined }`) or an assignment made it an own enumerable
 * property. */
int __kml_error_option_cause(void *op, i64 *out) {
    const char *o = (const char *)op;
    i64 c = *(const i64 *)(o + KML_ERR_CAUSE);
    void *bag = extra_bag(o);
    i64 i = bag ? __kml_dynobj_find(bag, k_cause.d) : -1;
    if (i >= 0 && (__kml_dynobj_attrs_at(bag, i) & KML_ATTR_ENUM)) return 0; /* assigned */
    if ((c == 0 || c == KML_NB_UNDEFINED) && i < 0) return 0;
    *out = c ? c : KML_NB_UNDEFINED;
    return 1;
}

/* The constructor's option installed a cause: one that is undefined is
 * still an own property (`"cause" in e`), which the slot cannot tell from
 * none, so it is recorded hidden in the extra bag (not enumerable). */
void __kml_error_option_cause_own(void *op) {
    char *o = (char *)op;
    if (*(i64 *)(o + KML_ERR_CAUSE) != KML_NB_UNDEFINED) return;
    __kml_error_set_hidden(op, k_cause.d, KML_NB_UNDEFINED);
}

/* An AggregateError's errors array and its length, which util.inspect shows
 * as `[errors]`; 0 for any other error. */
int __kml_error_aggregate(void *op, void ***errs, i64 *n) {
    const char *o = (const char *)op;
    if ((*(const i64 *)(o + KML_ERROR_KIND) & ~KML_HDR_STATE_MASK) != KML_ERRKIND_AGG) return 0;
    *errs = *(void **const *)(o + KML_AGG_ERRORS);
    *n = *(const i64 *)(o + KML_AGG_ERRORS + 8);
    return 1;
}

/* Object.getOwnPropertyDescriptor of an Error's own key: `message` (when
 * the constructor was given one), `cause` (when its option was given, even
 * as undefined), `stack` and an own `name` are writable, configurable and
 * not enumerable; a system field or an extra-bag entry is enumerable as it
 * is listed. Attributes Object.defineProperty changed (propattr.c, linked
 * only when the program defines a property) apply over them. The boxed
 * descriptor, or undefined for a key the error does not own. */
/* propattr.c's attributes when the program defines a property; this weak
 * default (no attributes) stands in otherwise. */
__attribute__((weak)) i64 __kml_pattr_attrs(void *obj, const char *name) {
    (void)obj;
    (void)name;
    return 0;
}
KSTR(k_message, "message");
KSTR(k_stack, "stack");
KSTR(d_value, "value");
KSTR(d_writable, "writable");
KSTR(d_enumerable, "enumerable");
KSTR(d_configurable, "configurable");
KSTR(k_errors, "errors");

/* Object.getOwnPropertyNames of an Error, as Node lists them: stack, its
 * message when it has one, the system fields set on it, the cause its
 * constructor's option installed, an AggregateError's errors, then every
 * key of its extra bag (non-enumerable ones too), in the order they were
 * added. out is {keys, count}, as __kml_shape_names_c fills it. */
int __kml_error_own_names(void *op, void **out) {
    const char *o = (const char *)op;
    void *bag = extra_bag(o);
    i64 nb = bag ? __kml_dynobj_count(bag) : 0;
    const char **keys = (const char **)kml_alloc(KML_HEAP_WORDS, (__SIZE_TYPE__)(4 + NFLDS + nb) * 8);
    i64 n = 0;
    keys[n++] = k_stack.d;
    if (__kml_error_has_message(op)) keys[n++] = k_message.d;
    for (int k = 0; k < NFLDS; k++)
        if (present(o, &flds[k]) && !marked(bag, k)) keys[n++] = flds[k].name;
    i64 c = *(const i64 *)(o + KML_ERR_CAUSE);
    if (c != 0 && c != KML_NB_UNDEFINED && !(bag && __kml_dynobj_find(bag, k_cause.d) >= 0)) keys[n++] = k_cause.d;
    if ((*(const i64 *)(o + KML_ERROR_KIND) & ~KML_HDR_STATE_MASK) == KML_ERRKIND_AGG) keys[n++] = k_errors.d;
    for (i64 j = 0; j < nb; j++) {
        const char *k = (const char *)__kml_dynobj_key_at(bag, j);
        int f = marker_field(k);
        if (f >= 0) {
            if (present(o, &flds[f])) keys[n++] = flds[f].name;
        } else if (!is_marker(k) && strcmp(k, k_stack.d) && strcmp(k, k_message.d)) {
            keys[n++] = k;
        }
    }
    out[0] = (void *)keys;
    out[1] = (void *)(__SIZE_TYPE__)n;
    return 1;
}

i64 __kml_error_own_desc(void *op, const char *key) {
    const char *o = (const char *)op;
    i64 v;
    int en = 0, found = 0;
    if (!strcmp(key, k_message.d)) {
        void *m = *(void *const *)(o + KML_ERR_MESSAGE);
        if (!__kml_error_has_message(op)) return KML_NB_UNDEFINED;
        v = (i64)(u64)m;
        found = 1;
    } else if (!strcmp(key, k_cause.d) && *(const i64 *)(o + KML_ERR_CAUSE) != 0 &&
               *(const i64 *)(o + KML_ERR_CAUSE) != KML_NB_UNDEFINED &&
               __kml_error_key_enumerable(op, key) == 0) {
        v = *(const i64 *)(o + KML_ERR_CAUSE);
        found = 1;
    } else {
        v = __kml_error_extra_get(op, key, &found);
        if (found != 1) return KML_NB_UNDEFINED;
        en = strcmp(key, k_stack.d) && strcmp(key, k_name.d);
        void *bag = extra_bag(o);
        if (bag && __kml_dynobj_find(bag, key) >= 0) en = (int)__kml_error_key_enumerable(op, key);
    }
    i64 at = __kml_pattr_attrs(op, key);
    void *d = __kml_dynobj_new();
    __kml_dynobj_set(d, d_value.d, v);
    __kml_dynobj_set(d, d_writable.d, (at & 1) ? KML_NB_FALSE : KML_NB_TRUE);
    __kml_dynobj_set(d, d_enumerable.d, en && !(at & 2) ? KML_NB_TRUE : KML_NB_FALSE);
    __kml_dynobj_set(d, d_configurable.d, (at & 4) ? KML_NB_FALSE : KML_NB_TRUE);
    return (i64)d | 5; /* a dynamic object's box */
}
