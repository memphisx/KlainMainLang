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

i64 __kml_dynjson_from_node(void *n) {
    switch (__kml_json_kind(n)) {
    case 1: return __kml_json_bool(n) ? 7 : 6;
    case 2: {
        const char *lex = __kml_json_num_lexeme(n);
        if (strchr(lex, '.') || strchr(lex, 'e') || strchr(lex, 'E') || strlen(lex) > 18)
            return nb_double(strtod(lex, NULL));
        return nb_double((double)atoll(lex));
    }
    case 3: return (i64)__kml_json_string_dup(n);
    case 4: {
        i64 len = __kml_json_len(n);
        void *arr = __kml_dynarr_new(len);
        for (i64 i = 0; i < len; i++) __kml_dynarr_push(arr, __kml_dynjson_from_node(__kml_json_item(n, i)));
        return (i64)arr | 6;
    }
    case 5: {
        i64 len = __kml_json_len(n);
        void *bag = __kml_dynobj_new();
        for (i64 i = 0; i < len; i++)
            __kml_dynobj_set(bag, __kml_json_key(n, i), __kml_dynjson_from_node(__kml_json_val(n, i)));
        return (i64)bag | 5;
    }
    default: return 2;
    }
}
