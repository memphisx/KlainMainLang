/* collections_any.c — the any-keyed Map/Set (TDD-00211; in C per TDD-00240):
 * keys and values are NaN-boxed words, hashed by any_hash and compared by
 * SameValueZero. The table is collections_engine.h, prepended with
 * kml_layout.h by the compiler; the iterator list and shared operations are
 * in collections.c. */

/* Defined by generated code / other runtimes. __kml_any_eq returns an i1 the
 * IR does not zero-extend, so only its low bit is read. */
extern unsigned char __kml_any_eq(i64 a, i64 b);
extern char *__kml_boxed_bigint_str(void *cell);
extern void *__kml_fn_identity_dyn(void *fn);
extern i64 __kml_map_str_hash(const char *s);

/* KML_BIGINT_MAGIC, KML_HDR_MAGIC_HOST and KML_HDR_MAGIC_HOST_MASK are
 * defined by the compiler ahead of this file. */

/* SameValueZero over two boxes. It differs from strict equality in exactly
 * one case, NaN equals NaN; every encoded number is canonicalized before
 * boxing, so two NaN keys are bit-identical and the leading check covers it
 * (and short-circuits identical immediates and pointers). */
static int any_svz(i64 a, i64 b) { return a == b || (__kml_any_eq(a, b) & 1); }

/* A hash consistent with any_svz: strings hash by content, arrays by their
 * inner data pointer (the identity any_eq compares), a function box by its
 * identity, a bigint by its digits, a host box by its handle, the numeric
 * zero normalized (-0 -> +0 bits); every other box by its raw bits. */
static i64 any_hash(i64 key) {
    const unsigned long long NUM0 = 562949953421312ULL; /* every encoded number is >= 2^49 */
    unsigned long long u = (unsigned long long)key;
    if (u >= NUM0) {
        unsigned long long bits = u - NUM0;
        double d;
        memcpy(&d, &bits, 8);
        return kml_mix(d == 0.0 ? (i64)NUM0 : key);
    }
    if (u >= 65536) { /* pointer-kind boxes; immediates are below */
        i64 kind = key & 7;
        void *p = (void *)(uintptr_t)(key & -8);
        if (kind == 0) return kml_mix(__kml_map_str_hash((const char *)(uintptr_t)key));
        if (kind == 2) return kml_mix((i64)(uintptr_t) * (void **)p);
        if (kind == 7) return kml_mix((i64)(uintptr_t)__kml_fn_identity_dyn(p));
        if (kind == 1) {
            i64 oh = *(i64 *)p;
            if (oh == KML_BIGINT_MAGIC) return kml_mix(__kml_map_str_hash(__kml_boxed_bigint_str(p)));
            if ((oh & KML_HDR_MAGIC_HOST_MASK) == KML_HDR_MAGIC_HOST)
                return kml_mix((i64)(uintptr_t) * (void **)((char *)p + 8));
        }
    }
    return kml_mix(key);
}

static i64 any_hash_fn(i64 k) { return any_hash(k); }

#define ANY_KEY(k) (k)

/* get() returns the undefined box (10) on a miss. */
KML_MAP_FLAVOR(any, i64, any_hash_fn, any_svz, ANY_KEY, 10)
