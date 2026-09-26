/* fnmeta.c — the object side of function values (TDD-00229 Stage A).
 *
 * A function value is a closure header {fnptr, env} (static ABI) or a boxed
 * dynamic record {fnptr, env, arity} (tag 12, dyn ABI). Its name, length and
 * kind live in a table the compiler emits once per program, keyed by the
 * code pointer (fnmeta.go). An unregistered code pointer is an anonymous
 * plain function — every runtime-internal callable (a promise resolver, a
 * stream callback) lands there, which is how Node renders those too. */

#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef struct {
    void *fn;
    const char *name;
    long long length;
    long long kind;
} KmlFnMeta;

extern const KmlFnMeta __kml_fnmeta_tab[];
extern const long long __kml_fnmeta_count;

enum {
    FN_PLAIN = 0,
    FN_ASYNC = 1,
    FN_GEN = 2,
    FN_ASYNCGEN = 3,
    FN_CLASS = 4,
    FN_BOUND = 0x100,
    FN_THROUGH_ENV = 0x200,
};

/* An extended record (a node:ffi bound function, TDD-00229) flags its arity
 * word and carries its own name and own-property bag:
 * { fnptr, env, arity | FN_EXT, char *name, void *props }. */
#define FN_EXT (1LL << 62)

/* Renders an own-property bag at `depth` ("" when empty) — defined by the
 * compiler at finalize (the real inspector when dynjson.c is linked). */
extern char *__kml_fn_props_inspect(void *props, long long depth);

/* Nested bind/adapter chains are short; the bound bounds a malformed cycle. */
#define FN_MAX_DEPTH 64

static const KmlFnMeta *fn_find(void *fn) {
    for (long long i = 0; i < __kml_fnmeta_count; i++)
        if (__kml_fnmeta_tab[i].fn == fn) return &__kml_fnmeta_tab[i];
    return NULL;
}

/* Resolved view of one function value. */
typedef struct {
    char name[512];
    long long length;
    int kind; /* FN_PLAIN..FN_CLASS */
} FnView;

static void fn_view_hdr(void **hdr, FnView *out, int depth);

static void fn_view_code(void *fn, void *env, FnView *out, int depth) {
    const KmlFnMeta *m = depth < FN_MAX_DEPTH && fn ? fn_find(fn) : NULL;
    if (!m) {
        out->name[0] = 0;
        out->length = 0;
        out->kind = FN_PLAIN;
        return;
    }
    if ((m->kind & FN_THROUGH_ENV) && env) {
        fn_view_hdr((void **)env, out, depth + 1);
        return;
    }
    if ((m->kind & FN_BOUND) && env) {
        FnView target;
        fn_view_hdr(*(void ***)env, &target, depth + 1);
        size_t n = strlen(target.name);
        if (n > sizeof out->name - 7) n = sizeof out->name - 7;
        memcpy(out->name, "bound ", 6);
        memcpy(out->name + 6, target.name, n);
        out->name[6 + n] = 0;
        /* m->length holds the bound-argument count here. */
        out->length = target.length > m->length ? target.length - m->length : 0;
        out->kind = target.kind == FN_CLASS ? FN_PLAIN : target.kind;
        return;
    }
    size_t n = strlen(m->name);
    if (n > sizeof out->name - 1) n = sizeof out->name - 1;
    memcpy(out->name, m->name, n);
    out->name[n] = 0;
    out->length = m->length;
    out->kind = (int)(m->kind & 0xff);
}

static void fn_view_hdr(void **hdr, FnView *out, int depth) {
    if (!hdr) {
        out->name[0] = 0;
        out->length = 0;
        out->kind = FN_PLAIN;
        return;
    }
    fn_view_code(hdr[0], hdr[1], out, depth);
}

static char *kml_str(const char *s, size_t n) {
    char *base = (char *)malloc(8 + n + 1);
    *(long long *)base = (long long)n;
    memcpy(base + 8, s, n);
    base[8 + n] = 0;
    return base + 8;
}

/* util.inspect's rendering of a function: `[Function: f]`,
 * `[AsyncFunction: f]`, `[GeneratorFunction: f]`,
 * `[AsyncGeneratorFunction: f]`, `[class C]`; an empty name reads
 * `(anonymous)` in place of `: name`. */
static char *fn_render(const FnView *v) {
    char buf[600];
    const char *tag = "Function";
    switch (v->kind) {
    case FN_ASYNC: tag = "AsyncFunction"; break;
    case FN_GEN: tag = "GeneratorFunction"; break;
    case FN_ASYNCGEN: tag = "AsyncGeneratorFunction"; break;
    }
    int n;
    if (v->kind == FN_CLASS)
        n = v->name[0] ? snprintf(buf, sizeof buf, "[class %s]", v->name)
                       : snprintf(buf, sizeof buf, "[class (anonymous)]");
    else
        n = v->name[0] ? snprintf(buf, sizeof buf, "[%s: %s]", tag, v->name)
                       : snprintf(buf, sizeof buf, "[%s (anonymous)]", tag);
    if (n < 0) n = 0;
    if ((size_t)n >= sizeof buf) n = (int)sizeof buf - 1;
    return kml_str(buf, (size_t)n);
}

char *__kml_fn_inspect_hdr(void **hdr) {
    FnView v;
    fn_view_hdr(hdr, &v, 0);
    return fn_render(&v);
}

char *__kml_fn_name_hdr(void **hdr) {
    FnView v;
    fn_view_hdr(hdr, &v, 0);
    return kml_str(v.name, strlen(v.name));
}

long long __kml_fn_length_hdr(void **hdr) {
    FnView v;
    fn_view_hdr(hdr, &v, 0);
    return v.length;
}

/* A tag-12 record {fnptr, env, arity}: its code pointer is either a natively
 * dynamic function (registered under its own name) or an adapter whose env is
 * the adapted closure header (registered FN_THROUGH_ENV); an extended record
 * names itself. */
static void fn_view_dyn(void **rec, FnView *v) {
    if (!rec) {
        fn_view_hdr(NULL, v, 0);
        return;
    }
    long long arity = (long long)rec[2];
    if (arity & FN_EXT) {
        const char *name = (const char *)rec[3];
        size_t n = name ? strlen(name) : 0;
        if (n > sizeof v->name - 1) n = sizeof v->name - 1;
        memcpy(v->name, name ? name : "", n);
        v->name[n] = 0;
        v->length = arity & ~FN_EXT;
        v->kind = FN_PLAIN;
        return;
    }
    fn_view_code(rec[0], rec[1], v, 0);
}

/* util.inspect of a boxed function at nesting `depth`: `[Function: f]`, plus
 * ` { own: props }` for an extended record — which past the depth limit
 * collapses to the name-less `[Function]`, as Node does for a function with
 * keys. */
char *__kml_fn_inspect_dyn(void **rec, long long depth) {
    FnView v;
    fn_view_dyn(rec, &v);
    if (rec && ((long long)rec[2] & FN_EXT) && rec[4]) {
        char *props = __kml_fn_props_inspect(rec[4], depth);
        if (props && props[0]) {
            free(props - 8);
            if (depth > 2) return kml_str("[Function]", 10);
            props = __kml_fn_props_inspect(rec[4], depth);
            char *head = fn_render(&v);
            size_t hn = strlen(head), pn = strlen(props);
            char *buf = (char *)malloc(hn + 1 + pn + 1);
            memcpy(buf, head, hn);
            buf[hn] = ' ';
            memcpy(buf + hn + 1, props, pn);
            char *out = kml_str(buf, hn + 1 + pn);
            free(buf);
            free(head - 8);
            free(props - 8);
            return out;
        }
    }
    return fn_render(&v);
}

char *__kml_fn_name_dyn(void **rec) {
    FnView v;
    fn_view_dyn(rec, &v);
    return kml_str(v.name, strlen(v.name));
}

long long __kml_fn_length_dyn(void **rec) {
    FnView v;
    fn_view_dyn(rec, &v);
    return v.length;
}

/* The identity of a dynamic function record for listener removal: an
 * adapter's (through its env, however deep) is the adapted closure header,
 * so boxing one closure twice (through a typed adapter too) gives records
 * that compare equal. */
void *__kml_fn_identity_dyn(void **rec) {
    if (!rec) return NULL;
    void *fn = rec[0];
    void **env = (void **)rec[1];
    void *id = rec;
    for (int depth = 0; depth < FN_MAX_DEPTH && fn && env; depth++) {
        const KmlFnMeta *m = fn_find(fn);
        if (!m || !(m->kind & FN_THROUGH_ENV)) break;
        id = env;
        fn = env[0];
        env = (void **)env[1];
    }
    return id;
}
