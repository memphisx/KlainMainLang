/* dynjsonnode.c — the KmlJsonNode -> dynamic-value converter behind untyped
 * JSON.parse (TDD-00155 Stage 2; in C per TDD-00240): scalars box as their
 * tags (a number becomes an int unless its lexeme carries a fraction or
 * exponent or is too long for exact i64), an array becomes a tag-11 dynamic
 * array, an object a tag-10 bag. Strings/keys are copied, so the tree can
 * be freed afterward. */
#include <stdlib.h>
#include <string.h>

typedef long long i64;

extern int __kml_json_kind(void *n);
extern int __kml_json_bool(void *n);
extern char *__kml_json_num_lexeme(void *n);
extern i64 __kml_json_len(void *n);
extern void *__kml_json_item(void *n, i64 i);
extern char *__kml_json_string_dup(void *n);
extern char *__kml_json_key(void *n, i64 i);
extern void *__kml_json_val(void *n, i64 i);
extern void *__kml_dynarr_new(i64 cap);
extern void __kml_dynarr_push(void *a, i64 v);
extern void *__kml_dynobj_new(void);
extern void __kml_dynobj_set(void *o, const char *key, i64 v);

/* __kml_nb_pack(tag, pay) for the tags this produces: 0 int, 1 float. */
static i64 nb_double(double d) {
    unsigned long long bits;
    if (d != d) bits = 0x7FF8000000000000ULL;
    else memcpy(&bits, &d, 8);
    return (i64)(bits + (1ULL << 49));
}

static i64 from_scalar(void *n) {
    switch (__kml_json_kind(n)) {
    case 1: return __kml_json_bool(n) ? 7 : 6;
    case 2: {
        const char *lex = __kml_json_num_lexeme(n);
        if (strchr(lex, '.') || strchr(lex, 'e') || strchr(lex, 'E') || strlen(lex) > 18)
            return nb_double(strtod(lex, NULL));
        return nb_double((double)atoll(lex));
    }
    case 3: return (i64)__kml_json_string_dup(n);
    default: return 2;
    }
}

/* An array or object being filled: its node, the value built for it, and
   the next child. */
typedef struct {
    void *n, *dst;
    i64 i, len;
    int obj;
} JFrame;

static i64 open_frame(void *n, JFrame *f) {
    f->n = n;
    f->i = 0;
    f->len = __kml_json_len(n);
    f->obj = __kml_json_kind(n) == 5;
    f->dst = f->obj ? __kml_dynobj_new() : __kml_dynarr_new(f->len);
    return (i64)f->dst | (f->obj ? 5 : 6);
}

/* Iterative, over an explicit stack, so a deeply nested document converts
   without deep C recursion. */
i64 __kml_dynjson_from_node(void *n) {
    int k = __kml_json_kind(n);
    if (k != 4 && k != 5) return from_scalar(n);
    i64 cap = 16, top = 1;
    JFrame *st = (JFrame *)malloc(sizeof(JFrame) * (size_t)cap); /* scratch */
    i64 root = open_frame(n, &st[0]);
    while (top) {
        JFrame *f = &st[top - 1];
        if (f->i == f->len) { top--; continue; }
        i64 idx = f->i++;
        void *c = f->obj ? __kml_json_val(f->n, idx) : __kml_json_item(f->n, idx);
        int ck = __kml_json_kind(c);
        i64 v;
        if (ck == 4 || ck == 5) {
            if (top == cap) {
                cap *= 2;
                st = (JFrame *)realloc(st, sizeof(JFrame) * (size_t)cap); /* scratch */
                f = &st[top - 1];
            }
            v = open_frame(c, &st[top]);
            top++;
        } else {
            v = from_scalar(c);
        }
        if (f->obj) __kml_dynobj_set(f->dst, __kml_json_key(f->n, idx), v);
        else __kml_dynarr_push(f->dst, v);
    }
    free(st); /* scratch */
    return root;
}

extern void __kml_shape_expando_set(void *obj, const char *key, i64 value);

/* __kml_json_expando_rest gives obj, a JSON object projected onto a static
 * shape, the parsed keys that shape does not declare (known[0..nknown)) as
 * own expando properties, so JSON.parse into a declared type keeps every
 * key, as JavaScript's untyped parse does. */
void __kml_json_expando_rest(void *obj, void *n, const char *const *known, i64 nknown) {
    if (__kml_json_kind(n) != 5) return;
    i64 len = __kml_json_len(n);
    for (i64 i = 0; i < len; i++) {
        const char *k = __kml_json_key(n, i);
        int seen = 0;
        for (i64 j = 0; j < nknown && !seen; j++) seen = strcmp(k, known[j]) == 0;
        if (!seen) __kml_shape_expando_set(obj, k, __kml_dynjson_from_node(__kml_json_val(n, i)));
    }
}
