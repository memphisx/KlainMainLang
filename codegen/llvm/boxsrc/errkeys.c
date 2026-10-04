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
extern i64 __kml_dynobj_find(void *o, const char *key);
extern i64 __kml_dynobj_attrs_at(void *o, i64 i);
extern i64 __kml_dynobj_rawtag_at(void *o, i64 i);
extern i64 __kml_dynobj_rawpay_at(void *o, i64 i);
extern void __kml_dynobj_patch(void *o, i64 i, i64 tag, i64 pay, i64 attrs);
#define KML_ATTR_ENUM 2 /* dynobj.c's A_ENUM */

#define KSTR(id, s) static const struct { i64 len; char d[sizeof(s)]; } id = { sizeof(s) - 1, s }
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

typedef struct {
    const char *name;
    int off;
    int f64;   /* a double slot (present when non-zero), else a pointer slot */
    int gated; /* node:sqlite's: a system error's copies of them are internal */
} Fld;

/* Node's order: uvException's errno, code, syscall, path, dest; the host and
 * port form's address and port; node:sqlite's errcode and errstr. */
static const Fld flds[] = {
    {k_errno.d, KML_ERROR_ERRNO, 1, 0},
    {k_code.d, KML_ERROR_CODE, 0, 0},
    {k_syscall.d, KML_ERROR_SYSCALL, 0, 0},
    {k_address.d, KML_ERR_ADDRESS, 0, 0},
    {k_port.d, KML_ERR_PORT, 1, 0},
    {k_path.d, KML_ERROR_PATH, 0, 0},
    {k_dest.d, KML_ERROR_DEST, 0, 0},
    {k_errcode.d, KML_ERROR_ERRCODE, 1, 1},
    {k_errstr.d, KML_ERROR_ERRSTR, 0, 1},
};
#define NFLDS ((int)(sizeof flds / sizeof flds[0]))

static int present(const char *o, const Fld *f) {
    int p = f->f64 ? *(const double *)(o + f->off) != 0.0 : *(void *const *)(o + f->off) != 0;
    if (f->gated) p = p && *(const double *)(o + KML_ERROR_ERRNO) == 0.0;
    return p;
}

static void *extra_bag(const char *o) { return *(void *const *)(o + KML_ERR_EXTRA); }

/* The count of present fields plus the bag's keys. */
i64 __kml_error_nextra(void *op) {
    const char *o = (const char *)op;
    i64 n = 0;
    for (int i = 0; i < NFLDS; i++) n += present(o, &flds[i]);
    void *bag = extra_bag(o);
    if (bag)
        for (i64 i = 0; i < __kml_dynobj_count(bag); i++)
            n += (__kml_dynobj_attrs_at(bag, i) & KML_ATTR_ENUM) != 0;
    return n;
}

/* The i-th present field's name, else the bag's (i - present)-th key. */
void *__kml_error_extra_key(void *op, i64 i) {
    const char *o = (const char *)op;
    for (int k = 0; k < NFLDS; k++) {
        if (!present(o, &flds[k])) continue;
        if (i == 0) return (void *)flds[k].name;
        i--;
    }
    void *bag = extra_bag(o);
    if (!bag) return 0;
    /* The i-th enumerable entry: a non-enumerable one (defineProperty's
       default) is not listed. */
    for (i64 j = 0; j < __kml_dynobj_count(bag); j++) {
        if (!(__kml_dynobj_attrs_at(bag, j) & KML_ATTR_ENUM)) continue;
        if (i == 0) return __kml_dynobj_key_at(bag, j);
        i--;
    }
    return 0;
}

/* A present field's boxed value, else the bag's. An own `name` is listed by
 * __kml_obj_key at its place among the layout's. */
i64 __kml_error_extra_get(void *op, const char *key, int *found) {
    const char *o = (const char *)op;
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
    *found = 1;
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
