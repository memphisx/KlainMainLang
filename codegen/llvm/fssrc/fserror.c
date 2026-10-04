/* fserror.c — the Error an fs syscall failure raises (TDD-00240; was
 * generated IR). Node's byte-exact message
 *   <CODE>: <libuv description>, <syscall>[ '<path>'[ -> '<dest>']]
 * (ADR-01000) on an error object carrying code, errno, syscall, path and
 * dest. kml_layout.h is prepended by the compiler. */
#include <errno.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef long long i64;

extern char *__kml_str_alloc(i64 n);
extern void __kml_str_finalize(char *s);
extern void __kml_throw(void *err);
extern void *__kml_errno_code(int e);
extern void *__kml_errno_desc(int e);
extern int __kml_uv_errno(int e);
/* The IR wrapper over __kml_fs_error_new_named: it supplies the error's
 * name string, one symbol per constructor name compared by address. */
extern void *__kml_fs_error_new(int e, const char *syscall, const char *path, const char *dest);

#define PUT(o, off, T, v) (*(T *)((char *)(o) + (off)) = (v))

char *__kml_fs_errmsg(const char *code, const char *desc, const char *sc, const char *path, const char *dest) {
    if (!path) path = "";
    if (!dest) dest = "";
    size_t lp = strlen(path), ld = strlen(dest);
    i64 size = (i64)(strlen(code) + strlen(desc) + strlen(sc) + lp + ld) + 32;
    char *buf = __kml_str_alloc(size);
    const char *fmt = lp ? (ld ? "%s: %s, %s '%s' -> '%s'" : "%s: %s, %s '%s'") : "%s: %s, %s";
    snprintf(buf, (size_t)size, fmt, code, desc, sc, path, dest);
    __kml_str_finalize(buf);
    return buf;
}

void *__kml_fs_error_new_named(int e, const char *syscall, const char *path, const char *dest, void *name) {
    static const struct { i64 n; char b[1]; } __attribute__((aligned(8))) empty = { 0, "" };
    const char *errmsg = strerror(e);
    const char *code_raw = __kml_errno_code(e);
    const char *code = code_raw ? code_raw : empty.b;
    const char *desc_raw = __kml_errno_desc(e);
    const char *desc = desc_raw ? desc_raw : errmsg;
    char *buf = __kml_fs_errmsg(code, desc, syscall, path, dest);
    void *o = calloc(1, KML_ERROR_SIZE);
    PUT(o, KML_ERROR_KIND, i64, 1LL << 48);
    PUT(o, KML_ERROR_MSG, void *, buf);
    PUT(o, KML_ERROR_NAME, void *, name);
    PUT(o, KML_ERROR_CODE, const void *, code_raw);
    PUT(o, KML_ERROR_ERRCODE, double, (double)e);
    PUT(o, KML_ERROR_ERRSTR, const void *, errmsg);
    PUT(o, KML_ERROR_SYSCALL, const void *, syscall);
    PUT(o, KML_ERROR_PATH, const void *, path);
    PUT(o, KML_ERROR_ERRNO, double, (double)__kml_uv_errno(e));
    PUT(o, KML_ERROR_DEST, const void *, dest);
    return o;
}

/* opdesc stays in the ABI (every call site passes its verb) but no longer
 * appears in the message. */
void __kml_fs_throw2(void *opdesc, const char *syscall, const char *path, const char *dest) {
    (void)opdesc;
    __kml_throw(__kml_fs_error_new(errno, syscall, path, dest));
}

void __kml_fs_throw(void *opdesc, const char *syscall, const char *path) {
    __kml_fs_throw2(opdesc, syscall, path, 0);
}
