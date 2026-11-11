/* objextra.c — the reads of a static object that carries properties added
 * through `any` (ADR-01212): whether it has any, and its console.log,
 * JSON.stringify and Object.keys forms through the shape runtime. A program
 * where no property can be added links hooks.c's weak defaults instead (no
 * added property; the static path is the whole answer); the emitter names
 * __kml_obj_extra_on when one can be added, which links this file. */
typedef long long i64;

extern i64 __kml_shape_expando_count(void *o);
extern char *__kml_obj_inspect_at(void *o, i64 depth);
extern char *__kml_dynjson_stringify_at(i64 tag, i64 pay, const char *indent, i64 base, int *err);
extern void __kml_shape_keys_array_c(void *o, void **out);

const char __kml_obj_extra_on = 1;

int __kml_obj_has_extra(void *o) { return __kml_shape_expando_count(o) > 0; }
char *__kml_obj_inspect_dyn(void *o, i64 depth) { return __kml_obj_inspect_at(o, depth); }
char *__kml_obj_json_dyn(void *o, const char *indent, i64 base) {
    int err = 0;
    return __kml_dynjson_stringify_at(KML_TAG_OBJECT, (i64)o, indent, base, &err);
}
void __kml_obj_keys_dyn(void *o, void **out) { __kml_shape_keys_array_c(o, out); }
