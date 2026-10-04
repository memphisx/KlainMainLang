/* native.c — the errno and fs-error primitives the TypeScript builtin
 * modules call (lib/native.d.ts; in C per TDD-00240). KML_NATIVE_ERRNO
 * builds Node's system-error names/descriptions; KML_NATIVE_FS_ERROR the
 * fs Error object. */
#include <stdlib.h>
#include <string.h>

typedef long long i64;

extern void *__kml_str_alloc(i64 n);
extern void __kml_str_finalize(void *s);

#ifdef KML_NATIVE_ERRNO
extern const char *__kml_errno_code(int e);
extern const char *__kml_errno_desc(int e);
extern int __kml_uv_errno(int e);

/* A C string as a headered string value. */
void *__kml_native_headered(const char *s) {
    i64 n1 = (i64)strlen(s) + 1;
    char *out = (char *)__kml_str_alloc(n1);
    memcpy(out, s, (size_t)n1);
    __kml_str_finalize(out);
    return out;
}

/* The code name ("ECONNREFUSED"), "UNKNOWN" when the table lacks it. */
void *__kml_native_errno_name(double e) {
    const char *c = __kml_errno_code((int)e);
    return __kml_native_headered(c ? c : "UNKNOWN");
}

/* libuv's description, strerror when the table lacks it. */
void *__kml_native_errno_desc(double e) {
    int n = (int)e;
    const char *d = __kml_errno_desc(n);
    return __kml_native_headered(d ? d : strerror(n));
}

/* err.errno: libuv's negative number. */
double __kml_native_uv_errno(double e) { return (double)__kml_uv_errno((int)e); }
#endif

#ifdef KML_NATIVE_FS_ERROR
extern void *__kml_fs_error_new(int errno_val, const char *syscall, const char *path, const char *dest);

/* The i1 flags arrive as bare ints. */
void *__kml_native_fs_error(double errno_val, const char *syscall, int has_path, const char *path,
                            int has_dest, const char *dest) {
    return __kml_fs_error_new((int)errno_val, syscall, (has_path & 1) ? path : NULL,
                              (has_dest & 1) ? dest : NULL);
}
#endif
