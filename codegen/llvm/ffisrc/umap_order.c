/* umap_order.c — the iteration order of a std::unordered_map<std::string, …>
 * after inserting keys in a given order: the order Node gives objects it
 * builds from such a map (URLPattern's `groups`, from ada's
 * url_pattern_component_result). The container is ffi_umap_stl.cc's (the
 * platform's own C++ library, as Node's build uses) or, on Windows, the
 * MSVC port in ffi_umap_msvc.c. */
#include <stdlib.h>
#include <string.h>

typedef struct kml_umap kml_umap;
kml_umap *kml_umap_new(void);
int kml_umap_emplace(kml_umap *u, const char *key, size_t len, void *val);
void kml_umap_each(kml_umap *u, void (*cb)(void *ctx, const char *key, size_t len, void *val), void *ctx);
void kml_umap_free(kml_umap *u);
void kml_umap_reserve(kml_umap *u, size_t n);

typedef struct {
    char *out;
    size_t n;
} kml_umap_order_buf;

static void kml_umap_order_put(void *ctx, const char *key, size_t len, void *val) {
    (void)val;
    kml_umap_order_buf *b = (kml_umap_order_buf *)ctx;
    if (b->n > 0) b->out[b->n++] = '\n';
    memcpy(b->out + b->n, key, len);
    b->n += len;
}

/* keys: the names joined with '\n' (a name holds no newline), inserted in
 * that order after reserving their count; the result, the same names in the
 * container's iteration order, joined likewise, as a headered string. */
char *__kml_native_umap_order(const char *keys) {
    size_t total = *(const long long *)(keys - 8);
    char *base = (char *)malloc(8 + total + 1);
    kml_umap_order_buf b = {base + 8, 0};
    kml_umap *u = kml_umap_new();
    /* ada reserves the group count before inserting (as Node shows). */
    size_t count = total > 0 ? 1 : 0;
    for (size_t i = 0; i < total; i++)
        if (keys[i] == '\n') count++;
    kml_umap_reserve(u, count);
    size_t start = 0;
    for (size_t i = 0; i <= total; i++) {
        if (i == total || keys[i] == '\n') {
            kml_umap_emplace(u, keys + start, i - start, NULL);
            start = i + 1;
        }
    }
    if (total > 0) kml_umap_each(u, kml_umap_order_put, &b);
    kml_umap_free(u);
    *(long long *)base = (long long)b.n;
    base[8 + b.n] = 0;
    return base + 8;
}
