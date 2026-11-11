/* request.c — `new Request(url, init)`'s enumerated init members, checked
 * as the Fetch standard's (undici's) constructor checks them.
 *
 *   __kml_request_enum_error(kind, value) -> the TypeError message for a
 *     value that is not one of kind's enum strings (0 cache, 1 credentials,
 *     2 mode, 3 redirect, 4 referrerPolicy), or null when it is one
 *   __kml_request_referrer(value, &err) -> the referrer to keep ("" and
 *     "about:client" as given, an absolute URL as its href), or null with
 *     *err set to the message for one that is not a URL */
#include <stdlib.h>
#include <string.h>

typedef long long i64;
extern void *__kml_str_alloc(i64 len);
extern void *__kml_str_from_cstr(const char *s);

static const char *const request_enums[][10] = {
    {"default", "no-store", "reload", "no-cache", "force-cache", "only-if-cached", 0},
    {"omit", "same-origin", "include", 0},
    {"navigate", "same-origin", "no-cors", "cors", 0},
    {"follow", "manual", "error", 0},
    {"", "no-referrer", "no-referrer-when-downgrade", "same-origin", "origin", "strict-origin",
     "origin-when-cross-origin", "strict-origin-when-cross-origin", "unsafe-url", 0},
};

static void *join3(const char *a, const char *b, const char *c) {
    size_t na = strlen(a), nb = strlen(b), nc = strlen(c);
    char *o = (char *)__kml_str_alloc((i64)(na + nb + nc));
    memcpy(o, a, na);
    memcpy(o + na, b, nb);
    memcpy(o + na + nb, c, nc);
    o[na + nb + nc] = 0;
    return o;
}

void *__kml_request_enum_error(i64 kind, const char *value) {
    if (kind < 0 || kind > 4 || !value) return 0;
    const char *const *e = request_enums[kind];
    for (int i = 0; e[i]; i++)
        if (strcmp(e[i], value) == 0) {
            /* A Request cannot be made in navigate mode. */
            if (kind == 2 && i == 0) return join3("Request constructor: invalid request mode ", value, ".");
            return 0;
        }
    char list[256];
    size_t n = 0;
    for (int i = 0; e[i]; i++) {
        size_t l = strlen(e[i]);
        if (i) {
            memcpy(list + n, ", ", 2);
            n += 2;
        }
        memcpy(list + n, e[i], l);
        n += l;
    }
    list[n++] = '.';
    list[n] = 0;
    void *head = join3("undefined: ", value, " is not an accepted type. Expected one of ");
    return join3((const char *)head, list, "");
}

void *__kml_request_referrer(const char *value, void **err) {
    *err = 0;
    if (!value || !*value || strcmp(value, "about:client") == 0) return __kml_str_from_cstr(value ? value : "");
    const char *p = value;
    if (!((*p >= 'a' && *p <= 'z') || (*p >= 'A' && *p <= 'Z'))) goto bad;
    while (*p && ((*p >= 'a' && *p <= 'z') || (*p >= 'A' && *p <= 'Z') || (*p >= '0' && *p <= '9') || *p == '+' || *p == '-' || *p == '.')) p++;
    if (*p != ':') goto bad;
    /* A hierarchical URL with no path is its href with "/". */
    if (p[1] == '/' && p[2] == '/' && p[3] && !strchr(p + 3, '/') && !strchr(p + 3, '?') && !strchr(p + 3, '#'))
        return join3(value, "/", "");
    return __kml_str_from_cstr(value);
bad:
    *err = join3("Referrer \"", value, "\" is not a valid URL.");
    return 0;
}
