/* path.c — the posix path normalizer behind path.join/path.resolve (in C
 * per TDD-00240; the win32 flavour is path_win32.go's own sidecar). Splits
 * on '/', drops "." segments, resolves ".." against the segments kept so
 * far (a leading ".." is kept for a relative result, dropped for an
 * absolute one: no going above root), then rejoins with '/'; an empty
 * result is "." (relative) or "/" (absolute). */
#include <stdlib.h>
#include <string.h>

typedef long long i64;

/* Allocates an n-byte string with its length header; room for the NUL. */
extern char *__kml_str_alloc(i64 n);

typedef struct {
    const char *p;
    i64 len;
} seg;

typedef struct {
    seg *v;
    i64 len, cap;
} segs;

static void push(segs *s, const char *p, i64 len) {
    if (s->len + 1 > s->cap) {
        i64 nc = s->cap * 2 > 8 ? s->cap * 2 : 8;
        s->v = (seg *)realloc(s->v, (size_t)nc * sizeof(seg));
        s->cap = nc;
    }
    s->v[s->len].p = p;
    s->v[s->len].len = len;
    s->len++;
}

static void one_segment(segs *s, const char *raw, i64 start, i64 end, _Bool is_absolute) {
    i64 n = end - start;
    const char *p = raw + start;
    if (n == 0 || (n == 1 && p[0] == '.')) return;
    if (n == 2 && p[0] == '.' && p[1] == '.') {
        if (s->len > 0) {
            seg *t = &s->v[s->len - 1];
            if (!(t->len == 2 && t->p[0] == '.' && t->p[1] == '.')) {
                s->len--;
                return;
            }
        }
        if (!is_absolute) push(s, p, n);
        return;
    }
    push(s, p, n);
}

static char *str_of(const char *p, i64 n) {
    char *b = __kml_str_alloc(n);
    memcpy(b, p, (size_t)n);
    b[n] = 0;
    return b;
}

char *__kml_path_normalize(const char *raw, _Bool is_absolute) {
    i64 len = (i64)strlen(raw), start = 0;
    segs s = {0, 0, 0};
    for (i64 i = 0; i < len; i++) {
        if (raw[i] == '/') {
            one_segment(&s, raw, start, i, is_absolute);
            start = i + 1;
        }
    }
    one_segment(&s, raw, start, len, is_absolute);
    char *out;
    if (s.len == 0) {
        out = str_of(is_absolute ? "/" : ".", 1);
    } else {
        i64 total = (is_absolute ? 1 : 0) + s.len - 1;
        for (i64 i = 0; i < s.len; i++) total += s.v[i].len;
        out = __kml_str_alloc(total);
        char *w = out;
        if (is_absolute) *w++ = '/';
        for (i64 i = 0; i < s.len; i++) {
            if (i) *w++ = '/';
            memcpy(w, s.v[i].p, (size_t)s.v[i].len);
            w += s.v[i].len;
        }
        *w = 0;
    }
    free(s.v);
    return out;
}
