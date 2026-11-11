/* dynentries.c — Object.values / Object.entries over a dynamic object
 * (TDD-00240): a dynamic array of its enumerable own values, or of
 * [key, value] pairs, in Object.keys order. */

typedef long long i64;
typedef unsigned long long u64;

extern char **__kml_dynobj_keys_enum_c(void *o, i64 *n);
extern i64 __kml_dynobj_get(void *o, const char *key);
extern void *__kml_dynarr_new(i64 cap);
extern void __kml_dynarr_push(void *a, i64 v);
extern char *__kml_str_from_cstr(const char *s);

void *__kml_dynobj_entries(void *o, _Bool withKeys) {
    i64 n = 0;
    char **keys = __kml_dynobj_keys_enum_c(o, &n);
    void *out = __kml_dynarr_new(n);
    for (i64 i = 0; i < n; i++) {
        i64 v = __kml_dynobj_get(o, keys[i]);
        if (!withKeys) {
            __kml_dynarr_push(out, v);
            continue;
        }
        void *pair = __kml_dynarr_new(2);
        __kml_dynarr_push(pair, (i64)(u64)__kml_str_from_cstr(keys[i]));
        __kml_dynarr_push(pair, v);
        __kml_dynarr_push(out, (i64)((u64)pair | 6)); /* an array box */
    }
    return out;
}
