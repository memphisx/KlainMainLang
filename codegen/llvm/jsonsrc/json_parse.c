// json_parse.c — validating recursive-descent JSON parser producing a tagged
// value tree (KlainMainLang TDD-00077 Track P, P1). Self-contained: libc only,
// no external library. Compiled alongside the program (the bigint/gcshim
// pattern) only when a program uses JSON.parse.
//
// The emitter calls __kml_json_parse to validate a JSON.parse argument: a NULL
// return means malformed input (with *err_pos set to the byte offset), which
// the emitted IR turns into a catchable SyntaxError; a non-NULL return is the
// parsed tree, which the caller releases with __kml_json_free. Tree *accessors*
// for typed/dynamic projection (P3/P4) build on this same node shape and ABI —
// P1 builds and validates the tree; it does not yet read it for projection.

#include <stdint.h>
#include <stdlib.h>
#include <stdio.h>
#include <string.h>

#define KJSON_NULL 0
#define KJSON_BOOL 1
#define KJSON_NUMBER 2
#define KJSON_STRING 3
#define KJSON_ARRAY 4
#define KJSON_OBJECT 5


typedef struct KmlJsonNode {
    int kind;
    int bool_val;              // KJSON_BOOL
    double num_val;            // KJSON_NUMBER (parsed)
    char *num_lexeme;          // KJSON_NUMBER raw token (for exact integer projection, P3)
    char *str_val;             // KJSON_STRING (decoded, NUL-terminated)
    int64_t str_len;           // KJSON_STRING decoded byte length (a \u0000 included)
    int64_t len;                  // KJSON_ARRAY item count / KJSON_OBJECT pair count
    struct KmlJsonNode **items; // KJSON_ARRAY
    char **keys;               // KJSON_OBJECT keys (decoded)
    struct KmlJsonNode **vals; // KJSON_OBJECT values
} KmlJsonNode;

typedef struct {
    const char *p;
    const char *end;
    int ok; // set to 0 on the first error; cursor then marks the offending byte
    int why;        // the first error's V8 message (W_*), at `at`
    const char *at;
} Parser;

/* V8's JSON.parse messages (json-parser.cc), by the first error found. */
enum {
    W_TOKEN = 0,     /* Unexpected token 'c' (end of input when at the end) */
    W_NUMBER,        /* Unexpected number */
    W_PROPNAME_OR_RBRACE,
    W_COMMA_RBRACK,
    W_COMMA_RBRACE,
    W_COLON,
    W_DQ_PROPNAME,
    W_UNTERM_STRING,
    W_BAD_CONTROL,
    W_BAD_UNICODE,
    W_BAD_ESCAPE,
    W_NO_NUMBER_MINUS,
    W_EXPONENT,
    W_UNTERM_FRACTION,
    W_TRAILING
};

/* fail records the first error: its message and position. */
static void fail(Parser *ps, int why, const char *at) {
    if (ps->ok) {
        ps->ok = 0;
        ps->why = why;
        ps->at = at;
    }
}

void __kml_json_free(KmlJsonNode *n);

static KmlJsonNode *node_new(int kind) {
    KmlJsonNode *n = (KmlJsonNode *)calloc(1, sizeof(KmlJsonNode)); /* scratch */
    if (n) {
        n->kind = kind;
    }
    return n;
}

static void skip_ws(Parser *ps) {
    while (ps->p < ps->end) {
        char c = *ps->p;
        if (c == ' ' || c == '\t' || c == '\n' || c == '\r') {
            ps->p++;
        } else {
            break;
        }
    }
}

// append_utf8 encodes a Unicode code point as UTF-8 into buf at *outlen.
static void append_utf8(char *buf, int64_t *outlen, unsigned int cp) {
    if (cp < 0x80) {
        buf[(*outlen)++] = (char)cp;
    } else if (cp < 0x800) {
        buf[(*outlen)++] = (char)(0xC0 | (cp >> 6));
        buf[(*outlen)++] = (char)(0x80 | (cp & 0x3F));
    } else if (cp < 0x10000) {
        buf[(*outlen)++] = (char)(0xE0 | (cp >> 12));
        buf[(*outlen)++] = (char)(0x80 | ((cp >> 6) & 0x3F));
        buf[(*outlen)++] = (char)(0x80 | (cp & 0x3F));
    } else {
        buf[(*outlen)++] = (char)(0xF0 | (cp >> 18));
        buf[(*outlen)++] = (char)(0x80 | ((cp >> 12) & 0x3F));
        buf[(*outlen)++] = (char)(0x80 | ((cp >> 6) & 0x3F));
        buf[(*outlen)++] = (char)(0x80 | (cp & 0x3F));
    }
}

static int hex_digit(char c) {
    if (c >= '0' && c <= '9') return c - '0';
    if (c >= 'a' && c <= 'f') return c - 'a' + 10;
    if (c >= 'A' && c <= 'F') return c - 'A' + 10;
    return -1;
}

// read_hex4 parses exactly four hex digits at ps->p (advancing it), returning
// the value or -1 on a non-hex digit / short read (a bad Unicode escape there).
static int read_hex4(Parser *ps) {
    int v = 0;
    for (int i = 0; i < 4; i++) {
        if (ps->p >= ps->end) { fail(ps, W_TOKEN, ps->p); return -1; }
        int d = hex_digit(*ps->p);
        if (d < 0) { fail(ps, W_BAD_UNICODE, ps->p); return -1; }
        v = (v << 4) | d;
        ps->p++;
    }
    return v;
}

// parse_string_raw parses a JSON string starting at the opening quote and
// returns a freshly malloc'd, decoded, NUL-terminated string (advancing past
// the closing quote). On error it sets ps->ok = 0 and returns NULL.
static char *parse_string_raw(Parser *ps, int64_t *len_out) {
    if (ps->p >= ps->end || *ps->p != '"') {
        fail(ps, W_TOKEN, ps->p);
        return NULL;
    }
    ps->p++; // opening quote
    // The decoded form is never longer than its raw bytes up to the closing
    // quote (each escape decodes shorter than it is written); allocate that
    // once, plus 3 slack for a 4-byte UTF-8 write, plus NUL. Sized by the
    // rest of the input instead, every string of a large document held a
    // buffer of the whole remainder: untouched pages on Linux, but committed
    // memory on Windows, gigabytes for a few-hundred-KB file.
    const char *q = ps->p;
    while (q < ps->end && *q != '"') q += (*q == '\\') ? 2 : 1;
    if (q > ps->end) q = ps->end;
    int64_t cap = (int64_t)(q - ps->p) + 4;
    char *out = (char *)malloc(cap); /* scratch */
    if (!out) {
        ps->ok = 0;
        return NULL;
    }
    int64_t outlen = 0;
    while (ps->p < ps->end) {
        char c = *ps->p;
        if (c == '"') {
            ps->p++; // closing quote
            out[outlen] = '\0';
            if (len_out) *len_out = outlen;
            return out;
        }
        if ((unsigned char)c < 0x20) { // control char must be escaped in JSON
            fail(ps, W_BAD_CONTROL, ps->p);
            break;
        }
        if (c == '\\') {
            ps->p++;
            if (ps->p >= ps->end) { fail(ps, W_TOKEN, ps->p); break; }
            char e = *ps->p;
            switch (e) {
            case '"': out[outlen++] = '"'; ps->p++; break;
            case '\\': out[outlen++] = '\\'; ps->p++; break;
            case '/': out[outlen++] = '/'; ps->p++; break;
            case 'b': out[outlen++] = '\b'; ps->p++; break;
            case 'f': out[outlen++] = '\f'; ps->p++; break;
            case 'n': out[outlen++] = '\n'; ps->p++; break;
            case 'r': out[outlen++] = '\r'; ps->p++; break;
            case 't': out[outlen++] = '\t'; ps->p++; break;
            case 'u': {
                ps->p++; // past 'u'
                int cp = read_hex4(ps);
                if (cp < 0) goto fail;
                // Combine a high+low surrogate pair into an astral code point.
                if (cp >= 0xD800 && cp <= 0xDBFF && ps->p + 1 < ps->end &&
                    ps->p[0] == '\\' && ps->p[1] == 'u') {
                    ps->p += 2;
                    int lo = read_hex4(ps);
                    if (lo < 0) goto fail;
                    if (lo >= 0xDC00 && lo <= 0xDFFF) {
                        cp = 0x10000 + ((cp - 0xD800) << 10) + (lo - 0xDC00);
                    } else {
                        append_utf8(out, &outlen, (unsigned int)cp);
                        cp = lo;
                    }
                }
                append_utf8(out, &outlen, (unsigned int)cp);
                break;
            }
            default:
                fail(ps, W_BAD_ESCAPE, ps->p);
                goto fail;
            }
        } else {
            out[outlen++] = c;
            ps->p++;
        }
    }
    if (ps->p >= ps->end) fail(ps, W_UNTERM_STRING, ps->p);
fail:
    free(out); /* scratch */
    fail(ps, W_TOKEN, ps->p);
    return NULL;
}


static KmlJsonNode *parse_string(Parser *ps) {
    int64_t len = 0;
    char *s = parse_string_raw(ps, &len);
    if (!s) return NULL;
    KmlJsonNode *n = node_new(KJSON_STRING);
    if (!n) { free(s); ps->ok = 0; return NULL; } /* scratch */
    n->str_val = s;
    n->str_len = len;
    return n;
}

static KmlJsonNode *parse_number(Parser *ps) {
    const char *start = ps->p;
    if (ps->p < ps->end && *ps->p == '-') ps->p++;
    // Integer part: a lone 0, or [1-9] digits — no leading zeros (strict JSON).
    if (ps->p >= ps->end || *ps->p < '0' || *ps->p > '9') { fail(ps, W_NO_NUMBER_MINUS, ps->p); return NULL; }
    if (*ps->p == '0') {
        ps->p++;
        if (ps->p < ps->end && *ps->p >= '0' && *ps->p <= '9') { fail(ps, W_NUMBER, ps->p); return NULL; }
    } else {
        while (ps->p < ps->end && *ps->p >= '0' && *ps->p <= '9') ps->p++;
    }
    // Fraction.
    if (ps->p < ps->end && *ps->p == '.') {
        ps->p++;
        if (ps->p >= ps->end || *ps->p < '0' || *ps->p > '9') { fail(ps, W_UNTERM_FRACTION, ps->p); return NULL; }
        while (ps->p < ps->end && *ps->p >= '0' && *ps->p <= '9') ps->p++;
    }
    // Exponent.
    if (ps->p < ps->end && (*ps->p == 'e' || *ps->p == 'E')) {
        ps->p++;
        if (ps->p < ps->end && (*ps->p == '+' || *ps->p == '-')) ps->p++;
        if (ps->p >= ps->end || *ps->p < '0' || *ps->p > '9') { fail(ps, W_EXPONENT, ps->p); return NULL; }
        while (ps->p < ps->end && *ps->p >= '0' && *ps->p <= '9') ps->p++;
    }
    int64_t n = (int64_t)(ps->p - start);
    char *lex = (char *)malloc(n + 1); /* scratch */
    if (!lex) { ps->ok = 0; return NULL; }
    memcpy(lex, start, n);
    lex[n] = '\0';
    KmlJsonNode *node = node_new(KJSON_NUMBER);
    if (!node) { free(lex); ps->ok = 0; return NULL; } /* scratch */
    node->num_lexeme = lex;
    node->num_val = strtod(lex, NULL);
    return node;
}

// match_literal checks for lit at the cursor and advances past it on success;
// a mismatch is an unexpected token at its first differing character.
static int match_literal(Parser *ps, const char *lit) {
    int64_t n = (int64_t)strlen(lit);
    for (int64_t i = 0; i < n; i++) {
        if (ps->p + i >= ps->end || ps->p[i] != lit[i]) {
            fail(ps, W_TOKEN, ps->p + i);
            return 0;
        }
    }
    ps->p += n;
    return 1;
}

/* push_item appends v to the array node; push_pair adds key: v to the
   object node. 0 when out of memory. */
static int push_item(KmlJsonNode *node, KmlJsonNode *v, int64_t *cap) {
    if (node->len == *cap) {
        int64_t nc = *cap ? *cap * 2 : 8;
        KmlJsonNode **grown = (KmlJsonNode **)realloc(node->items, sizeof(KmlJsonNode *) * nc); /* scratch */
        if (!grown) return 0;
        node->items = grown;
        *cap = nc;
    }
    node->items[node->len++] = v;
    return 1;
}

static int push_pair(KmlJsonNode *node, char *key, KmlJsonNode *v, int64_t *cap) {
    if (node->len == *cap) {
        int64_t nc = *cap ? *cap * 2 : 8;
        char **gk = (char **)realloc(node->keys, sizeof(char *) * nc); /* scratch */
        if (gk) node->keys = gk;
        KmlJsonNode **gv = (KmlJsonNode **)realloc(node->vals, sizeof(KmlJsonNode *) * nc); /* scratch */
        if (gv) node->vals = gv;
        if (!gk || !gv) return 0;
        *cap = nc;
    }
    node->keys[node->len] = key;
    node->vals[node->len] = v;
    node->len++;
    return 1;
}

/* One open array or object of parse_value's explicit stack: nesting is
   bounded by memory alone, as in V8, not by the C stack. */
typedef struct {
    KmlJsonNode *node;
    int64_t cap;
    char *key; /* an object's key awaiting its value */
} Frame;

/* parse_key reads an object's key and its colon (first: no key yet, so
   `}` would also have done). NULL on an error. */
static char *parse_key(Parser *ps, int first) {
    skip_ws(ps);
    if (ps->p >= ps->end || *ps->p != '"') {
        fail(ps, first ? W_PROPNAME_OR_RBRACE : W_DQ_PROPNAME, ps->p);
        return NULL;
    }
    char *key = parse_string_raw(ps, NULL);
    if (!key) return NULL;
    skip_ws(ps);
    if (ps->p >= ps->end || *ps->p != ':') {
        free(key); /* scratch */
        fail(ps, W_COLON, ps->p);
        return NULL;
    }
    ps->p++;
    return key;
}

static KmlJsonNode *parse_value(Parser *ps) {
    Frame *st = NULL;
    int64_t top = 0, stcap = 0;
    KmlJsonNode *v = NULL;
    for (;;) {
        /* a value */
        skip_ws(ps);
        v = NULL;
        if (ps->p >= ps->end) {
            fail(ps, W_TOKEN, ps->p);
            goto bad;
        }
        char c = *ps->p;
        if (c == '{' || c == '[') {
            ps->p++;
            KmlJsonNode *n = node_new(c == '{' ? KJSON_OBJECT : KJSON_ARRAY);
            if (!n) { fail(ps, W_TOKEN, ps->p); goto bad; }
            skip_ws(ps);
            if (ps->p < ps->end && *ps->p == (c == '{' ? '}' : ']')) {
                ps->p++;
                v = n;
            } else {
                if (top == stcap) {
                    int64_t nc = stcap ? stcap * 2 : 16;
                    Frame *g = (Frame *)realloc(st, sizeof(Frame) * nc); /* scratch */
                    if (!g) { __kml_json_free(n); fail(ps, W_TOKEN, ps->p); goto bad; }
                    st = g;
                    stcap = nc;
                }
                st[top].node = n;
                st[top].cap = 0;
                st[top].key = NULL;
                top++;
                if (c == '{' && !(st[top - 1].key = parse_key(ps, 1))) goto bad;
                continue;
            }
        } else {
            switch (c) {
            case '"': v = parse_string(ps); break;
            case 't':
                if (match_literal(ps, "true")) { v = node_new(KJSON_BOOL); if (v) v->bool_val = 1; }
                break;
            case 'f':
                if (match_literal(ps, "false")) v = node_new(KJSON_BOOL);
                break;
            case 'n':
                if (match_literal(ps, "null")) v = node_new(KJSON_NULL);
                break;
            default:
                if (c == '-' || (c >= '0' && c <= '9')) v = parse_number(ps);
                else fail(ps, W_TOKEN, ps->p);
                break;
            }
            if (!ps->ok || !v) { fail(ps, W_TOKEN, ps->p); goto bad; }
        }
        /* v is complete: attach it to the open container, closing those it
           ends */
        for (;;) {
            if (!top) {
                free(st); /* scratch */
                return v;
            }
            Frame *f = &st[top - 1];
            int ok = f->node->kind == KJSON_ARRAY ? push_item(f->node, v, &f->cap)
                                                  : push_pair(f->node, f->key, v, &f->cap);
            if (!ok) { fail(ps, W_TOKEN, ps->p); goto bad; }
            f->key = NULL;
            v = NULL;
            skip_ws(ps);
            int arr = f->node->kind == KJSON_ARRAY;
            if (ps->p < ps->end && *ps->p == ',') {
                ps->p++;
                if (!arr && !(f->key = parse_key(ps, 0))) goto bad;
                break;
            }
            if (ps->p < ps->end && *ps->p == (arr ? ']' : '}')) {
                ps->p++;
                v = f->node;
                top--;
                continue;
            }
            fail(ps, arr ? W_COMMA_RBRACK : W_COMMA_RBRACE, ps->p);
            goto bad;
        }
    }
bad:
    __kml_json_free(v);
    while (top > 0) {
        top--;
        free(st[top].key); /* scratch */
        __kml_json_free(st[top].node);
    }
    free(st); /* scratch */
    return NULL;
}

// __kml_json_parse validates and parses JSON text into a tree. Returns the root
// node, or NULL on malformed input with *err_pos set to the offending byte
// offset (used to build the SyntaxError message). Trailing non-whitespace after
// the root value is an error.
static _Thread_local int kjson_last_why;

KmlJsonNode *__kml_json_parse(const char *text, int64_t *err_pos) {
    Parser ps;
    ps.p = text;
    /* The text is a headered string (TDD-00120): all of its bytes, so an
       embedded NUL byte is an error rather than the end of the input. */
    ps.end = text + KML_STR_LEN(text);
    ps.ok = 1;
    ps.why = W_TOKEN;
    ps.at = NULL;
    KmlJsonNode *root = parse_value(&ps);
    if (ps.ok) {
        skip_ws(&ps);
        if (ps.p != ps.end) fail(&ps, W_TRAILING, ps.p); // trailing junk
    }
    if (!ps.ok) {
        const char *at = ps.at ? ps.at : ps.p;
        kjson_last_why = ps.why;
        if (err_pos) *err_pos = (int64_t)(at - text);
        __kml_json_free(root);
        return NULL;
    }
    return root;
}

// ---- Tree accessors (P3 typed projection) ----
// The emitter walks a parsed tree against a compile-time target type, reading
// each node through these. Values that must outlive the tree (strings) are
// copied out; the tree is freed once projection finishes.

int __kml_json_kind(const KmlJsonNode *n) { return n ? n->kind : KJSON_NULL; }

int __kml_json_bool(const KmlJsonNode *n) { return n ? n->bool_val : 0; }

// __kml_json_num_lexeme returns the raw numeric token (transient — the caller
// hands it straight to atoll/strtod before the tree is freed), or "0".
const char *__kml_json_num_lexeme(const KmlJsonNode *n) {
    return (n && n->kind == KJSON_NUMBER && n->num_lexeme) ? n->num_lexeme : "0";
}

int64_t __kml_json_len(const KmlJsonNode *n) { return n ? n->len : 0; }

KmlJsonNode *__kml_json_item(const KmlJsonNode *n, int64_t i) {
    if (!n || n->kind != KJSON_ARRAY || i < 0 || i >= n->len) return NULL;
    return n->items[i];
}

// __kml_json_string_dup returns an owned copy of a string node's decoded value
// (the projected string must outlive the tree). A non-string node yields "".
char *__kml_json_string_dup(const KmlJsonNode *n) {
    const char *s = (n && n->kind == KJSON_STRING && n->str_val) ? n->str_val : "";
    int64_t len = (n && n->kind == KJSON_STRING && n->str_val) ? n->str_len : 0;
    /* TDD-00120: length-prefixed heap string — [i64 len][bytes][\0], value ptr
       at base+8, so KML string ops can read the true length via ptr-8. */
    char *base = (char *)kml_alloc(KML_HEAP_RAW, 8 + len + 1);
    if (!base) return base;
    *(int64_t *)base = len;
    char *out = base + 8;
    memcpy(out, s, len + 1);
    return out;
}

// __kml_json_key / __kml_json_val expose an object node's i-th pair for the
// dynamic-tree conversion (TDD-00155 Stage 2): the key is transient (the
// caller's bag-set copies it before the tree is freed).
const char *__kml_json_key(const KmlJsonNode *n, int64_t i) {
    if (!n || n->kind != KJSON_OBJECT || i < 0 || i >= n->len) return "";
    return n->keys[i];
}

KmlJsonNode *__kml_json_val(const KmlJsonNode *n, int64_t i) {
    if (!n || n->kind != KJSON_OBJECT || i < 0 || i >= n->len) return NULL;
    return n->vals[i];
}

// __kml_keyorder_json gives an object projected from n its keys' order:
// the input's (keyordersrc/keyorder.c's block: [count, bit, ...]), stored
// in *slot when its string keys list otherwise than the layout's sequence
// seq. names[b] is the layout's field name for presence bit b (NULL for
// none); ints marks integer-like keys, which list first whatever the input.
void __kml_keyorder_json(unsigned char **slot, const KmlJsonNode *n, const char *const *names,
                         const unsigned char *seq, int64_t ints) {
    if (!n || n->kind != KJSON_OBJECT) return;
    unsigned char got[65];
    unsigned long long seen = 0, im = (unsigned long long)ints;
    got[0] = 0;
    for (int64_t i = 0; i < n->len; i++)
        for (int b = 0; b < 64; b++)
            if (names[b] && !(seen >> b & 1) && !(im >> b & 1) && strcmp(names[b], n->keys[i]) == 0) {
                got[1 + got[0]++] = (unsigned char)b;
                seen |= 1ULL << b;
                break;
            }
    /* The layout's own order for the same keys needs no block. */
    int k = 1, same = 1;
    for (int i = 1; i <= seq[0] && same; i++) {
        int b = seq[i];
        if (!(seen >> b & 1) || (im >> b & 1)) continue;
        same = got[k++] == b;
    }
    if (same) return;
    unsigned char *o = (unsigned char *)kml_alloc(KML_HEAP_RAW, 65);
    memcpy(o, got, (size_t)got[0] + 1);
    *slot = o;
}

// __kml_json_get returns an object's value for key, or NULL if the key is absent
// or n isn't an object. Scans backward so a duplicate key is last-wins (JS).
KmlJsonNode *__kml_json_get(const KmlJsonNode *n, const char *key) {
    if (!n || n->kind != KJSON_OBJECT) return NULL;
    for (int64_t i = n->len - 1; i >= 0; i--) {
        if (strcmp(n->keys[i], key) == 0) return n->vals[i];
    }
    return NULL;
}

// __kml_json_free releases a tree (NULL-safe, recursive).
/* Iterative: a node's children are moved to a worklist, so a deep tree
   frees without deep C recursion. */
void __kml_json_free(KmlJsonNode *n) {
    if (!n) return;
    KmlJsonNode **work = NULL;
    int64_t top = 0, cap = 0;
    for (;;) {
        switch (n->kind) {
        case KJSON_STRING:
            free(n->str_val); /* scratch */
            break;
        case KJSON_NUMBER:
            free(n->num_lexeme); /* scratch */
            break;
        case KJSON_ARRAY:
        case KJSON_OBJECT: {
            KmlJsonNode **kids = n->kind == KJSON_ARRAY ? n->items : n->vals;
            for (int64_t i = 0; i < n->len; i++) {
                if (n->kind == KJSON_OBJECT) free(n->keys[i]); /* scratch */
                if (!kids || !kids[i]) continue;
                if (top == cap) {
                    int64_t nc = cap ? cap * 2 : 16;
                    KmlJsonNode **g = (KmlJsonNode **)realloc(work, sizeof(KmlJsonNode *) * nc); /* scratch */
                    if (!g) continue; /* leaked on out-of-memory */
                    work = g;
                    cap = nc;
                }
                work[top++] = kids[i];
            }
            free(n->items); /* scratch */
            free(n->keys); /* scratch */
            free(n->vals); /* scratch */
            break;
        }
        default:
            break;
        }
        free(n); /* scratch */
        if (!top) break;
        n = work[--top];
    }
    free(work); /* scratch */
}

extern void *__kml_str_alloc(int64_t n);
extern void __kml_str_finalize(char *s);

/* The UTF-16 length of the UTF-8 bytes [s, s+n): a 4-byte sequence is two
   code units, every other sequence one. */
static int64_t u16_len(const char *s, int64_t n) {
    int64_t u = 0;
    for (int64_t i = 0; i < n; i++) {
        unsigned char c = (unsigned char)s[i];
        if ((c & 0xC0) == 0x80) continue;
        u += c >= 0xF0 ? 2 : 1;
    }
    return u;
}

/* The byte offset of UTF-16 index u in text (len bytes); *split is set
   when u falls between the two halves of a 4-byte character, which then
   lies before the offset. */
static int64_t u16_to_byte(const char *text, int64_t len, int64_t u, int *split) {
    int64_t i = 0, k = 0;
    *split = 0;
    while (i < len && k < u) {
        unsigned char c = (unsigned char)text[i];
        int w = c >= 0xF0 ? 4 : c >= 0xE0 ? 3 : c >= 0xC0 ? 2 : 1;
        k += w == 4 ? 2 : 1;
        i += w;
    }
    if (k > u) *split = 1;
    return i < len ? i : len;
}

/* The SyntaxError message of the last failed __kml_json_parse of text, its
   error at byte pos, in V8's words: a specific message with the position
   and its line and column, or `Unexpected token 'c', "<text>" is not valid
   JSON` with at most ten characters of context either side, or
   `Unexpected end of JSON input`. Positions count UTF-16 code units, as in
   JavaScript. A headered string. */
void *__kml_json_syntax_message(const char *text, int64_t pos) {
    int64_t len = KML_STR_LEN(text);
    int why = kjson_last_why;
    static const char *const specific[] = {
        [W_NUMBER] = "Unexpected number",
        [W_PROPNAME_OR_RBRACE] = "Expected property name or '}'",
        [W_COMMA_RBRACK] = "Expected ',' or ']' after array element",
        [W_COMMA_RBRACE] = "Expected ',' or '}' after property value",
        [W_COLON] = "Expected ':' after property name",
        [W_DQ_PROPNAME] = "Expected double-quoted property name",
        [W_UNTERM_STRING] = "Unterminated string",
        [W_BAD_CONTROL] = "Bad control character in string literal",
        [W_BAD_UNICODE] = "Bad Unicode escape",
        [W_BAD_ESCAPE] = "Bad escaped character",
        [W_NO_NUMBER_MINUS] = "No number after minus sign",
        [W_EXPONENT] = "Exponent part is missing a number",
        [W_UNTERM_FRACTION] = "Unterminated fractional number",
        [W_TRAILING] = "Unexpected non-whitespace character after JSON",
    };
    char head[160];
    const char *ctx = NULL, *pre = "", *post = "", *lead = "", *trail = "";
    int64_t ctxlen = 0;
    int64_t upos = u16_len(text, pos);
    if (why != W_TOKEN) {
        int64_t line = 1, col = 1;
        for (int64_t i = 0; i < pos; i++) {
            if (text[i] == '\n') { line++; col = 1; }
            else if (((unsigned char)text[i] & 0xC0) != 0x80) col += (unsigned char)text[i] >= 0xF0 ? 2 : 1;
        }
        snprintf(head, sizeof head, "%s %s at position %lld (line %lld column %lld)", specific[why],
                 why == W_TRAILING ? "" : "in JSON", (long long)upos, (long long)line, (long long)col);
        if (why == W_TRAILING) {
            snprintf(head, sizeof head, "%s at position %lld (line %lld column %lld)", specific[why],
                     (long long)upos, (long long)line, (long long)col);
        }
    } else if (pos >= len) {
        snprintf(head, sizeof head, "Unexpected end of JSON input");
    } else if (len == 9 && !memcmp(text, "undefined", 9)) {
        snprintf(head, sizeof head, "\"undefined\" is not valid JSON");
    } else if (len == 3 && !memcmp(text, "NaN", 3)) {
        snprintf(head, sizeof head, "\"NaN\" is not valid JSON");
    } else if (len == 8 && !memcmp(text, "Infinity", 8)) {
        snprintf(head, sizeof head, "\"Infinity\" is not valid JSON");
    } else if (len == 15 && !memcmp(text, "[object Object]", 15)) {
        snprintf(head, sizeof head, "\"[object Object]\" is not valid JSON");
    } else {
        unsigned char c0 = (unsigned char)text[pos];
        int w = c0 >= 0xF0 ? 4 : c0 >= 0xE0 ? 3 : c0 >= 0xC0 ? 2 : 1;
        if (pos + w > len) w = 1;
        snprintf(head, sizeof head, "Unexpected token '%.*s', ", w, text + pos);
        int64_t ulen = u16_len(text, len);
        int64_t from = 0, to = ulen;
        if (ulen >= 21) {
            if (upos < 10) { to = upos + 10; post = "..."; }
            else if (upos < ulen - 10) { from = upos - 10; to = upos + 10; pre = "..."; post = "..."; }
            else { from = upos - 10; pre = "..."; }
        }
        /* A context edge between a surrogate pair's halves keeps the lone
           half, which prints as U+FFFD. */
        int s0, s1;
        int64_t b0 = u16_to_byte(text, len, from, &s0), b1 = u16_to_byte(text, len, to, &s1);
        ctx = text + b0;
        ctxlen = b1 - b0;
        if (s0) lead = "\xEF\xBF\xBD";
        if (s1) {
            ctxlen -= 4;
            trail = "\xEF\xBF\xBD";
        }
    }
    int64_t hl = (int64_t)strlen(head);
    int64_t total = hl + (ctx ? (int64_t)strlen(pre) + 1 + 3 + ctxlen + 3 + 1 + (int64_t)strlen(post) + 18 : 0);
    char *m = (char *)__kml_str_alloc(total);
    memcpy(m, head, (size_t)hl);
    int64_t n = hl;
    if (ctx) {
        n += sprintf(m + n, "%s\"%s", pre, lead);
        memcpy(m + n, ctx, (size_t)ctxlen);
        n += ctxlen;
        n += sprintf(m + n, "%s\"%s is not valid JSON", trail, post);
    }
    m[n] = 0;
    __kml_str_finalize(m);
    return m;
}
