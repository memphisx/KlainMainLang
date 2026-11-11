/* osinfo_bags.c — the dynamic objects over osinfo.c's flat data:
 * `process.env` as a value (KEY -> string) and os.networkInterfaces()
 * (name -> [address records]). Both are bags: their keys exist only at run
 * time. Values are NaN-boxed: a string is its pointer, an object its
 * pointer tagged 5, an array tagged 6; null is 2, false 6, true 7,
 * undefined 10. */
#include <stdio.h>
#include <string.h>

typedef long long i64;

extern void *__kml_dynobj_new(void);
extern void __kml_dynobj_set(void *bag, const char *key, i64 v);
extern i64 __kml_dynobj_get(void *bag, const char *key);
extern void *__kml_dynarr_new(i64 cap);
extern void __kml_dynarr_push(void *arr, i64 v);
extern i64 __kml_nb_pay(i64 box);
extern i64 __kml_nb_pack(unsigned char tag, i64 pay);
extern void *__kml_str_from_cstr(const char *s);
extern void *__kml_str_alloc(i64 n);
extern void __kml_str_finalize(void *s);
extern char **__kml_env_entries(void);
extern void *__kml_os_netifs(i64 *count);

#define BOX_NULL 2
#define BOX_FALSE 6
#define BOX_TRUE 7
#define BOX_UNDEFINED 10
#define BOX_OBJ(p) ((i64)(p) | 5)
#define BOX_ARR(p) ((i64)(p) | 6)

/* A snapshot of the live environment block (Node's object is live,
 * ADR-01077). An entry without '=' or with an empty name is skipped. */
void *__kml_process_env_bag(void) {
    void *bag = __kml_dynobj_new();
    char **env = __kml_env_entries();
    if (!env) return bag;
    for (i64 i = 0; env[i]; i++) {
        const char *ent = env[i], *eq = strchr(ent, '=');
        if (!eq || eq == ent) continue;
        size_t klen = (size_t)(eq - ent);
        char *key = (char *)kml_alloc(KML_HEAP_RAW, klen + 1);
        memcpy(key, ent, klen);
        key[klen] = 0;
        __kml_dynobj_set(bag, key, (i64)__kml_str_from_cstr(eq + 1));
    }
    return bag;
}

/* One kml_ifaddr record of osinfo.c: name, address, netmask, family (4 or
 * 6), mac, internal, scopeid, prefix (-1 unknown). */
typedef struct {
    const char *name, *address, *netmask;
    i64 family;
    const char *mac;
    i64 internal, scopeid, prefix;
} kml_ifaddr;

void *__kml_os_netifs_bag(void) {
    i64 n = 0;
    void *bag = __kml_dynobj_new();
    kml_ifaddr *recs = (kml_ifaddr *)__kml_os_netifs(&n);
    if (!recs) return bag;
    for (i64 i = 0; i < n; i++) {
        kml_ifaddr *r = &recs[i];
        /* The per-name list, created on first sight (insertion order is
         * Node's). */
        if (__kml_dynobj_get(bag, r->name) == BOX_UNDEFINED)
            __kml_dynobj_set(bag, r->name, BOX_ARR(__kml_dynarr_new(2)));
        void *list = (void *)__kml_nb_pay(__kml_dynobj_get(bag, r->name));
        void *o = __kml_dynobj_new();
        __kml_dynobj_set(o, "address", (i64)__kml_str_from_cstr(r->address));
        __kml_dynobj_set(o, "netmask", (i64)__kml_str_from_cstr(r->netmask));
        __kml_dynobj_set(o, "family", (i64)__kml_str_from_cstr(r->family == 4 ? "IPv4" : "IPv6"));
        __kml_dynobj_set(o, "mac", (i64)__kml_str_from_cstr(r->mac));
        __kml_dynobj_set(o, "internal", r->internal ? BOX_TRUE : BOX_FALSE);
        i64 cidr = BOX_NULL;
        if (r->prefix >= 0) {
            char *buf = (char *)__kml_str_alloc(80);
            snprintf(buf, 80, "%s/%d", r->address, (int)r->prefix);
            __kml_str_finalize(buf);
            cidr = (i64)buf;
        }
        __kml_dynobj_set(o, "cidr", cidr);
        if (r->family != 4)
            __kml_dynobj_set(o, "scopeid", __kml_nb_pack(0, r->scopeid));
        __kml_dynarr_push(list, BOX_OBJ(o));
    }
    return bag;
}
