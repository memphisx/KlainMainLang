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

#include <stdlib.h>
#include <string.h>

#define KJSON_NULL 0
#define KJSON_BOOL 1
#define KJSON_NUMBER 2
#define KJSON_STRING 3
#define KJSON_ARRAY 4
#define KJSON_OBJECT 5

// A hard nesting cap: this parser runs on untrusted runtime input, so deeply
// nested adversarial JSON could otherwise overflow the C stack. Exceeding it is
// reported as a parse error (see TDD-00077's depth-guard note).
#define KJSON_MAX_DEPTH 512

typedef struct KmlJsonNode {
    int kind;
    int bool_val;              // KJSON_BOOL
    double num_val;            // KJSON_NUMBER (parsed)
    char *num_lexeme;          // KJSON_NUMBER raw token (for exact integer projection, P3)
    char *str_val;             // KJSON_STRING (decoded, NUL-terminated)
    long len;                  // KJSON_ARRAY item count / KJSON_OBJECT pair count
    struct KmlJsonNode **items; // KJSON_ARRAY
    char **keys;               // KJSON_OBJECT keys (decoded)
    struct KmlJsonNode **vals; // KJSON_OBJECT values
} KmlJsonNode;

typedef struct {
    const char *p;
    const char *end;
    int depth;
    int ok; // set to 0 on the first error; cursor then marks the offending byte
} Parser;

void __kml_json_free(KmlJsonNode *n);

static KmlJsonNode *node_new(int kind) {
    KmlJsonNode *n = (KmlJsonNode *)calloc(1, sizeof(KmlJsonNode));
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
static void append_utf8(char *buf, long *outlen, unsigned int cp) {
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
// the value or -1 on a non-hex digit / short read.
static int read_hex4(Parser *ps) {
    int v = 0;
    for (int i = 0; i < 4; i++) {
        if (ps->p >= ps->end) return -1;
        int d = hex_digit(*ps->p);
        if (d < 0) return -1;
        v = (v << 4) | d;
        ps->p++;
    }
    return v;
}

// parse_string_raw parses a JSON string starting at the opening quote and
// returns a freshly malloc'd, decoded, NUL-terminated string (advancing past
// the closing quote). On error it sets ps->ok = 0 and returns NULL.
static char *parse_string_raw(Parser *ps) {
    if (ps->p >= ps->end || *ps->p != '"') {
        ps->ok = 0;
        return NULL;
    }
    ps->p++; // opening quote
    // The decoded form is never longer than the remaining raw bytes; over-
    // allocate once (plus 3 slack for a 4-byte UTF-8 write, plus NUL).
    long cap = (long)(ps->end - ps->p) + 4;
    char *out = (char *)malloc(cap);
    if (!out) {
        ps->ok = 0;
        return NULL;
    }
    long outlen = 0;
    while (ps->p < ps->end) {
        char c = *ps->p;
        if (c == '"') {
            ps->p++; // closing quote
            out[outlen] = '\0';
            return out;
        }
        if ((unsigned char)c < 0x20) { // control char must be escaped in JSON
            break;
        }
        if (c == '\\') {
            ps->p++;
            if (ps->p >= ps->end) break;
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
                goto fail;
            }
        } else {
            out[outlen++] = c;
            ps->p++;
        }
    }
fail:
    free(out);
    ps->ok = 0;
    return NULL;
}

static KmlJsonNode *parse_value(Parser *ps); // fwd

static KmlJsonNode *parse_string(Parser *ps) {
    char *s = parse_string_raw(ps);
    if (!s) return NULL;
    KmlJsonNode *n = node_new(KJSON_STRING);
    if (!n) { free(s); ps->ok = 0; return NULL; }
    n->str_val = s;
    return n;
}

static KmlJsonNode *parse_number(Parser *ps) {
    const char *start = ps->p;
    if (ps->p < ps->end && *ps->p == '-') ps->p++;
    // Integer part: a lone 0, or [1-9] digits — no leading zeros (strict JSON).
    if (ps->p >= ps->end || *ps->p < '0' || *ps->p > '9') { ps->ok = 0; return NULL; }
    if (*ps->p == '0') {
        ps->p++;
    } else {
        while (ps->p < ps->end && *ps->p >= '0' && *ps->p <= '9') ps->p++;
    }
    // Fraction.
    if (ps->p < ps->end && *ps->p == '.') {
        ps->p++;
        if (ps->p >= ps->end || *ps->p < '0' || *ps->p > '9') { ps->ok = 0; return NULL; }
        while (ps->p < ps->end && *ps->p >= '0' && *ps->p <= '9') ps->p++;
    }
    // Exponent.
    if (ps->p < ps->end && (*ps->p == 'e' || *ps->p == 'E')) {
        ps->p++;
        if (ps->p < ps->end && (*ps->p == '+' || *ps->p == '-')) ps->p++;
        if (ps->p >= ps->end || *ps->p < '0' || *ps->p > '9') { ps->ok = 0; return NULL; }
        while (ps->p < ps->end && *ps->p >= '0' && *ps->p <= '9') ps->p++;
    }
    long n = (long)(ps->p - start);
    char *lex = (char *)malloc(n + 1);
    if (!lex) { ps->ok = 0; return NULL; }
    memcpy(lex, start, n);
    lex[n] = '\0';
    KmlJsonNode *node = node_new(KJSON_NUMBER);
    if (!node) { free(lex); ps->ok = 0; return NULL; }
    node->num_lexeme = lex;
    node->num_val = strtod(lex, NULL);
    return node;
}

// match_literal checks for lit at the cursor and advances past it on success.
static int match_literal(Parser *ps, const char *lit) {
    long n = (long)strlen(lit);
    if (ps->end - ps->p < n) return 0;
    if (memcmp(ps->p, lit, n) != 0) return 0;
    ps->p += n;
    return 1;
}

static KmlJsonNode *parse_array(Parser *ps) {
    ps->p++; // '['
    KmlJsonNode *node = node_new(KJSON_ARRAY);
    if (!node) { ps->ok = 0; return NULL; }
    skip_ws(ps);
    if (ps->p < ps->end && *ps->p == ']') { ps->p++; return node; }
    long cap = 8;
    node->items = (KmlJsonNode **)malloc(sizeof(KmlJsonNode *) * cap);
    if (!node->items) { ps->ok = 0; __kml_json_free(node); return NULL; }
    for (;;) {
        KmlJsonNode *v = parse_value(ps);
        if (!ps->ok) { __kml_json_free(v); __kml_json_free(node); return NULL; }
        if (node->len == cap) {
            cap *= 2;
            KmlJsonNode **grown = (KmlJsonNode **)realloc(node->items, sizeof(KmlJsonNode *) * cap);
            if (!grown) { ps->ok = 0; __kml_json_free(v); __kml_json_free(node); return NULL; }
            node->items = grown;
        }
        node->items[node->len++] = v;
        skip_ws(ps);
        if (ps->p < ps->end && *ps->p == ',') { ps->p++; skip_ws(ps); continue; }
        if (ps->p < ps->end && *ps->p == ']') { ps->p++; return node; }
        ps->ok = 0;
        __kml_json_free(node);
        return NULL;
    }
}

static KmlJsonNode *parse_object(Parser *ps) {
    ps->p++; // '{'
    KmlJsonNode *node = node_new(KJSON_OBJECT);
    if (!node) { ps->ok = 0; return NULL; }
    skip_ws(ps);
    if (ps->p < ps->end && *ps->p == '}') { ps->p++; return node; }
    long cap = 8;
    node->keys = (char **)malloc(sizeof(char *) * cap);
    node->vals = (KmlJsonNode **)malloc(sizeof(KmlJsonNode *) * cap);
    if (!node->keys || !node->vals) { ps->ok = 0; __kml_json_free(node); return NULL; }
    for (;;) {
        skip_ws(ps);
        char *key = parse_string_raw(ps);
        if (!key) { __kml_json_free(node); return NULL; }
        skip_ws(ps);
        if (ps->p >= ps->end || *ps->p != ':') { free(key); ps->ok = 0; __kml_json_free(node); return NULL; }
        ps->p++; // ':'
        KmlJsonNode *v = parse_value(ps);
        if (!ps->ok) { free(key); __kml_json_free(v); __kml_json_free(node); return NULL; }
        if (node->len == cap) {
            cap *= 2;
            char **gk = (char **)realloc(node->keys, sizeof(char *) * cap);
            KmlJsonNode **gv = (KmlJsonNode **)realloc(node->vals, sizeof(KmlJsonNode *) * cap);
            if (!gk || !gv) { ps->ok = 0; free(key); __kml_json_free(v); __kml_json_free(node);
                              if (gk) node->keys = gk; if (gv) node->vals = gv; return NULL; }
            node->keys = gk;
            node->vals = gv;
        }
        node->keys[node->len] = key;
        node->vals[node->len] = v;
        node->len++;
        skip_ws(ps);
        if (ps->p < ps->end && *ps->p == ',') { ps->p++; continue; }
        if (ps->p < ps->end && *ps->p == '}') { ps->p++; return node; }
        ps->ok = 0;
        __kml_json_free(node);
        return NULL;
    }
}

static KmlJsonNode *parse_value(Parser *ps) {
    if (++ps->depth > KJSON_MAX_DEPTH) { ps->ok = 0; ps->depth--; return NULL; }
    skip_ws(ps);
    KmlJsonNode *result = NULL;
    if (ps->p >= ps->end) {
        ps->ok = 0;
    } else {
        char c = *ps->p;
        switch (c) {
        case '{': result = parse_object(ps); break;
        case '[': result = parse_array(ps); break;
        case '"': result = parse_string(ps); break;
        case 't':
            if (match_literal(ps, "true")) { result = node_new(KJSON_BOOL); if (result) result->bool_val = 1; }
            else ps->ok = 0;
            break;
        case 'f':
            if (match_literal(ps, "false")) { result = node_new(KJSON_BOOL); if (result) result->bool_val = 0; }
            else ps->ok = 0;
            break;
        case 'n':
            if (match_literal(ps, "null")) result = node_new(KJSON_NULL);
            else ps->ok = 0;
            break;
        default:
            if (c == '-' || (c >= '0' && c <= '9')) result = parse_number(ps);
            else ps->ok = 0;
            break;
        }
    }
    ps->depth--;
    return result;
}

// __kml_json_parse validates and parses JSON text into a tree. Returns the root
// node, or NULL on malformed input with *err_pos set to the offending byte
// offset (used to build the SyntaxError message). Trailing non-whitespace after
// the root value is an error.
KmlJsonNode *__kml_json_parse(const char *text, long *err_pos) {
    Parser ps;
    ps.p = text;
    ps.end = text + strlen(text);
    ps.depth = 0;
    ps.ok = 1;
    KmlJsonNode *root = parse_value(&ps);
    if (ps.ok) {
        skip_ws(&ps);
        if (ps.p != ps.end) ps.ok = 0; // trailing junk
    }
    if (!ps.ok) {
        if (err_pos) *err_pos = (long)(ps.p - text);
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

long __kml_json_len(const KmlJsonNode *n) { return n ? n->len : 0; }

KmlJsonNode *__kml_json_item(const KmlJsonNode *n, long i) {
    if (!n || n->kind != KJSON_ARRAY || i < 0 || i >= n->len) return NULL;
    return n->items[i];
}

// __kml_json_string_dup returns an owned copy of a string node's decoded value
// (the projected string must outlive the tree). A non-string node yields "".
char *__kml_json_string_dup(const KmlJsonNode *n) {
    const char *s = (n && n->kind == KJSON_STRING && n->str_val) ? n->str_val : "";
    long len = (long)strlen(s);
    char *out = (char *)malloc(len + 1);
    if (out) memcpy(out, s, len + 1);
    return out;
}

// __kml_json_get returns an object's value for key, or NULL if the key is absent
// or n isn't an object. Scans backward so a duplicate key is last-wins (JS).
KmlJsonNode *__kml_json_get(const KmlJsonNode *n, const char *key) {
    if (!n || n->kind != KJSON_OBJECT) return NULL;
    for (long i = n->len - 1; i >= 0; i--) {
        if (strcmp(n->keys[i], key) == 0) return n->vals[i];
    }
    return NULL;
}

// __kml_json_free releases a tree (NULL-safe, recursive).
void __kml_json_free(KmlJsonNode *n) {
    if (!n) return;
    switch (n->kind) {
    case KJSON_STRING:
        free(n->str_val);
        break;
    case KJSON_NUMBER:
        free(n->num_lexeme);
        break;
    case KJSON_ARRAY:
        for (long i = 0; i < n->len; i++) __kml_json_free(n->items[i]);
        free(n->items);
        break;
    case KJSON_OBJECT:
        for (long i = 0; i < n->len; i++) {
            free(n->keys[i]);
            __kml_json_free(n->vals[i]);
        }
        free(n->keys);
        free(n->vals);
        break;
    default:
        break;
    }
    free(n);
}
