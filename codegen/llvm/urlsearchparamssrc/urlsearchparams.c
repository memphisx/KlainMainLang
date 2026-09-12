// urlsearchparams.c — the ordered name/value pair list backing URLSearchParams
// (TDD-00203). Replaces the former Map<string,string> backing, which could not
// represent the WHATWG model: cross-key insertion order and duplicate keys. The
// list is exactly the spec's "list of name-value tuples" — `?a=1&b=2&a=3`
// round-trips verbatim and `getAll("a")` is `["1","3"]`.
//
// String ABI (TDD-00120): a native string VALUE is a `char*` pointing at the
// bytes; its byte length is an i64 header at `ptr-8`, and a NUL follows the
// bytes. kml_slen reads that header (binary-safe past an embedded NUL); kml_new
// allocates a fresh string in the same layout so everything this file returns is
// a first-class native string the rest of the compiler can concat/compare/print.
// Per the default manual-memory model (docs/status/MEMORY-MANAGEMENT.md), the
// bounded per-call allocation here is intentionally not freed.

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

static int64_t kml_slen(const char *s) {
    if (!s) return 0;
    return *(const int64_t *)(s - 8);
}

// Allocate a native string [ i64 len ][ bytes ][ \0 ] and return the byte ptr.
static char *kml_new(const char *src, int64_t n) {
    char *base = (char *)malloc((size_t)n + 9);
    *(int64_t *)base = n;
    char *bytes = base + 8;
    if (n > 0 && src) memcpy(bytes, src, (size_t)n);
    bytes[n] = 0;
    return bytes;
}

typedef struct { char *key; char *val; } kml_usp_pair;
typedef struct { kml_usp_pair *items; int64_t len; int64_t cap; } kml_usp;

// A native `{ptr, i64}` string-array aggregate — the ABI getAll()/keys()/values()
// return and codegen consumes as `string[]` (matches emit_url.go's array shape).
typedef struct { void *ptr; int64_t len; } kml_arr;

kml_usp *__kml_usp_create(void) {
    return (kml_usp *)calloc(1, sizeof(kml_usp));
}

static void usp_push(kml_usp *u, char *k, char *v) {
    if (u->len == u->cap) {
        u->cap = u->cap ? u->cap * 2 : 8;
        u->items = (kml_usp_pair *)realloc(u->items, (size_t)u->cap * sizeof(kml_usp_pair));
    }
    u->items[u->len].key = k;
    u->items[u->len].val = v;
    u->len++;
}

static int key_eq(const char *a, const char *b) {
    int64_t la = kml_slen(a), lb = kml_slen(b);
    if (la != lb) return 0;
    return memcmp(a, b, (size_t)la) == 0;
}

void __kml_usp_append(kml_usp *u, char *k, char *v) { usp_push(u, k, v); }

// Node/WHATWG set: if the name exists, set the first match's value and remove all
// later matches; otherwise append.
void __kml_usp_set(kml_usp *u, char *k, char *v) {
    int64_t i, w = 0;
    int found = 0;
    for (i = 0; i < u->len; i++) {
        if (key_eq(u->items[i].key, k)) {
            if (!found) { u->items[w].key = k; u->items[w].val = v; w++; found = 1; }
            // drop later matches
        } else {
            u->items[w++] = u->items[i];
        }
    }
    u->len = w;
    if (!found) usp_push(u, k, v);
}

char *__kml_usp_get(kml_usp *u, char *k) {
    for (int64_t i = 0; i < u->len; i++)
        if (key_eq(u->items[i].key, k)) return u->items[i].val;
    return NULL; // undefined/null at the codegen boundary
}

int32_t __kml_usp_has(kml_usp *u, char *k) {
    for (int64_t i = 0; i < u->len; i++)
        if (key_eq(u->items[i].key, k)) return 1;
    return 0;
}

// WHATWG has(name, value): true only if a pair matches BOTH name and value.
int32_t __kml_usp_has2(kml_usp *u, char *k, char *v) {
    for (int64_t i = 0; i < u->len; i++)
        if (key_eq(u->items[i].key, k) && key_eq(u->items[i].val, v)) return 1;
    return 0;
}

void __kml_usp_delete(kml_usp *u, char *k) {
    int64_t w = 0;
    for (int64_t i = 0; i < u->len; i++)
        if (!key_eq(u->items[i].key, k)) u->items[w++] = u->items[i];
    u->len = w;
}

// WHATWG delete(name, value): remove only pairs matching BOTH name and value.
void __kml_usp_delete2(kml_usp *u, char *k, char *v) {
    int64_t w = 0;
    for (int64_t i = 0; i < u->len; i++)
        if (!(key_eq(u->items[i].key, k) && key_eq(u->items[i].val, v))) u->items[w++] = u->items[i];
    u->len = w;
}

int64_t __kml_usp_size(kml_usp *u) { return u->len; }

char *__kml_usp_key_at(kml_usp *u, int64_t i) {
    if (i < 0 || i >= u->len) return NULL;
    return u->items[i].key;
}
char *__kml_usp_val_at(kml_usp *u, int64_t i) {
    if (i < 0 || i >= u->len) return NULL;
    return u->items[i].val;
}

// getAll/keys/values/entries write their `{ptr,i64}` result through an
// out-parameter rather than returning the aggregate by value. A by-value return
// of a 16-byte struct is ABI-divergent across x86-64 targets — System V returns
// it in two registers (matching the IR `{ptr,i64}` return), but the Windows x64
// ABI returns it via a hidden sret pointer, so an IR caller expecting the
// register form crashed (0xc0000005) against clang's Windows lowering of the C
// definition. An explicit out-parameter has one ABI everywhere.
void __kml_usp_get_all(kml_arr *out, kml_usp *u, char *k) {
    int64_t got = 0, n = 0;
    for (int64_t i = 0; i < u->len; i++) if (key_eq(u->items[i].key, k)) n++;
    char **data = (char **)malloc((size_t)(n ? n : 1) * sizeof(char *));
    for (int64_t i = 0; i < u->len; i++)
        if (key_eq(u->items[i].key, k)) data[got++] = u->items[i].val;
    out->ptr = data;
    out->len = got;
}

void __kml_usp_keys(kml_arr *out, kml_usp *u) {
    char **data = (char **)malloc((size_t)(u->len ? u->len : 1) * sizeof(char *));
    for (int64_t i = 0; i < u->len; i++) data[i] = u->items[i].key;
    out->ptr = data; out->len = u->len;
}
void __kml_usp_values(kml_arr *out, kml_usp *u) {
    char **data = (char **)malloc((size_t)(u->len ? u->len : 1) * sizeof(char *));
    for (int64_t i = 0; i < u->len; i++) data[i] = u->items[i].val;
    out->ptr = data; out->len = u->len;
}
// entries(): an array of [key, value] tuples. A tuple value [string,string] is a
// POINTER to a heap {ptr,ptr} struct (tuples are objects), so the array holds one
// pointer per pair — each pointing at a kml_usp_pair, whose {key,val} layout is
// exactly the {ptr,ptr} tuple. Codegen types it ArrayOf(TupleType([string,string])),
// matching Map.entries()'s own shape.
void __kml_usp_entries(kml_arr *out, kml_usp *u) {
    void **ptrs = (void **)malloc((size_t)(u->len ? u->len : 1) * sizeof(void *));
    for (int64_t i = 0; i < u->len; i++) ptrs[i] = &u->items[i];
    out->ptr = ptrs;
    out->len = u->len;
}

// Stable sort by key. WHATWG sorts by UTF-16 code units; this compares UTF-8
// bytes over the shorter length then by length — identical to the spec for the
// ASCII keys this corpus uses, and a documented approximation for non-ASCII
// (TDD-00203). Insertion sort keeps it stable (equal keys retain relative order,
// which the spec requires for the values).
void __kml_usp_sort(kml_usp *u) {
    for (int64_t i = 1; i < u->len; i++) {
        kml_usp_pair cur = u->items[i];
        int64_t lc = kml_slen(cur.key);
        int64_t j = i - 1;
        while (j >= 0) {
            char *jk = u->items[j].key;
            int64_t lj = kml_slen(jk);
            int64_t m = lc < lj ? lc : lj;
            int c = memcmp(jk, cur.key, (size_t)m);
            if (c == 0) c = (lj < lc) ? -1 : (lj > lc ? 1 : 0);
            if (c > 0) { u->items[j + 1] = u->items[j]; j--; } else break;
        }
        u->items[j + 1] = cur;
    }
}

// application/x-www-form-urlencoded serialization (WHATWG URLSearchParams
// stringifier): space -> '+', keep alnum and *-._, percent-encode everything
// else as %XX (uppercase hex), on the raw UTF-8 bytes.
static const char HEX[] = "0123456789ABCDEF";
static int64_t enc_into(char *out, const char *s, int64_t n) {
    int64_t w = 0;
    for (int64_t i = 0; i < n; i++) {
        unsigned char c = (unsigned char)s[i];
        if ((c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') ||
            (c >= '0' && c <= '9') || c == '*' || c == '-' || c == '.' || c == '_') {
            if (out) out[w] = (char)c;
            w++;
        } else if (c == ' ') {
            if (out) out[w] = '+';
            w++;
        } else {
            if (out) { out[w] = '%'; out[w + 1] = HEX[c >> 4]; out[w + 2] = HEX[c & 0xF]; }
            w += 3;
        }
    }
    return w;
}

char *__kml_usp_to_string(kml_usp *u) {
    // First pass: total encoded length (keys + '=' + vals + '&' separators).
    int64_t total = 0;
    for (int64_t i = 0; i < u->len; i++) {
        total += enc_into(NULL, u->items[i].key, kml_slen(u->items[i].key));
        total += 1; // '='
        total += enc_into(NULL, u->items[i].val, kml_slen(u->items[i].val));
        if (i + 1 < u->len) total += 1; // '&'
    }
    char *base = (char *)malloc((size_t)total + 9);
    *(int64_t *)base = total;
    char *out = base + 8;
    int64_t w = 0;
    for (int64_t i = 0; i < u->len; i++) {
        w += enc_into(out + w, u->items[i].key, kml_slen(u->items[i].key));
        out[w++] = '=';
        w += enc_into(out + w, u->items[i].val, kml_slen(u->items[i].val));
        if (i + 1 < u->len) out[w++] = '&';
    }
    out[w] = 0;
    return out;
}

// console.log / util.inspect rendering: `URLSearchParams { 'a' => '1', 'b' => '2' }`
// (empty: `URLSearchParams {}`), matching Node. Single-quotes each name/value.
char *__kml_usp_inspect(kml_usp *u) {
    const char *pfx = "URLSearchParams {";
    if (u->len == 0) {
        return kml_new("URLSearchParams {}", 18);
    }
    // Length: prefix + per-pair " 'k' => 'v'" + ", " separators + " }".
    int64_t total = (int64_t)strlen(pfx);
    for (int64_t i = 0; i < u->len; i++) {
        total += 1 + 1 + kml_slen(u->items[i].key) + 1;   // space + ' + key + '
        total += 4;                                        // " => "
        total += 1 + kml_slen(u->items[i].val) + 1;        // ' + val + '
        if (i + 1 < u->len) total += 1;                    // ','
    }
    total += 2; // " }"
    char *base = (char *)malloc((size_t)total + 9);
    *(int64_t *)base = total;
    char *out = base + 8;
    int64_t w = 0;
    memcpy(out + w, pfx, strlen(pfx)); w += (int64_t)strlen(pfx);
    for (int64_t i = 0; i < u->len; i++) {
        if (i > 0) out[w++] = ',';
        out[w++] = ' '; out[w++] = '\'';
        int64_t lk = kml_slen(u->items[i].key);
        memcpy(out + w, u->items[i].key, (size_t)lk); w += lk;
        out[w++] = '\'';
        memcpy(out + w, " => ", 4); w += 4;
        out[w++] = '\'';
        int64_t lv = kml_slen(u->items[i].val);
        memcpy(out + w, u->items[i].val, (size_t)lv); w += lv;
        out[w++] = '\'';
    }
    out[w++] = ' '; out[w++] = '}';
    out[w] = 0;
    return out;
}

// Parse a query string (no leading '?') into the list, preserving order and
// duplicates, percent-decoding both name and value (and '+' -> space), the
// WHATWG application/x-www-form-urlencoded parser. Used by `new
// URLSearchParams(str)` and by URL's `.searchParams` construction.
static int hexval(char c) {
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
}
// Decode one component [s, s+n) into a fresh native string.
static char *decode_comp(const char *s, int64_t n) {
    char *buf = (char *)malloc((size_t)n + 1);
    int64_t w = 0;
    for (int64_t i = 0; i < n; i++) {
        char c = s[i];
        if (c == '+') {
            buf[w++] = ' ';
        } else if (c == '%' && i + 2 < n) {
            int hi = hexval(s[i + 1]), lo = hexval(s[i + 2]);
            if (hi >= 0 && lo >= 0) { buf[w++] = (char)((hi << 4) | lo); i += 2; }
            else buf[w++] = c;
        } else {
            buf[w++] = c;
        }
    }
    char *r = kml_new(buf, w);
    free(buf);
    return r;
}

void __kml_usp_parse(kml_usp *u, const char *q) {
    if (!q) return;
    // strlen, not the length header: callers pass a `?`-stripped substring
    // pointer (emitStripLeadingQuestionMark) that has no valid header at q-8. A
    // query string is NUL-terminated with no embedded raw NUL, so strlen is exact
    // (matching the former __kml_http_parse_query, which was also strlen-based).
    int64_t n = (int64_t)strlen(q);
    int64_t i = 0;
    while (i < n) {
        int64_t start = i;
        while (i < n && q[i] != '&') i++;
        int64_t seglen = i - start;
        if (seglen > 0) {
            const char *seg = q + start;
            int64_t eq = -1;
            for (int64_t j = 0; j < seglen; j++) if (seg[j] == '=') { eq = j; break; }
            char *key, *val;
            if (eq < 0) {
                key = decode_comp(seg, seglen);
                val = kml_new("", 0);
            } else {
                key = decode_comp(seg, eq);
                val = decode_comp(seg + eq + 1, seglen - eq - 1);
            }
            usp_push(u, key, val);
        }
        i++; // skip '&'
    }
}
