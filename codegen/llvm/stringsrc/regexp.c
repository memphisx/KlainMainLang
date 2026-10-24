/* regexp.c — RegExp helpers around PCRE2: flag parsing and validation, the
 * byte <-> UTF-16 offset converters, the ECMAScript source rewrite (in C per
 * TDD-00240). The pcre2_* calls themselves stay in generated code. */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef long long i64;
typedef unsigned char u8;

extern void *__kml_str_alloc(i64 n);
extern void __kml_str_finalize(char *s);

/* Bytes (1-4) of the UTF-8 code point at str[off], from the lead byte alone; a
 * continuation or malformed lead counts as 1. */
i64 __kml_regex_utf8_width(const u8 *str, i64 off) {
    u8 b = str[off];
    if (b < 0x80) return 1;
    if ((b & 0xE0) == 0xC0) return 2;
    if ((b & 0xF0) == 0xE0) return 3;
    if ((b & 0xF8) == 0xF0) return 4;
    return 1;
}

/* UTF-16 code units in str[0:bytelen]; bytelen is non-negative. */
i64 __kml_regex_byte_to_utf16(const u8 *str, i64 bytelen) {
    i64 i = 0, u = 0;
    while (i < bytelen) {
        i64 w = __kml_regex_utf8_width(str, i);
        u += w == 4 ? 2 : 1;
        i += w;
    }
    return u;
}

/* Byte offset of the target-th UTF-16 unit. Past the NUL it extends one byte
 * per missing unit instead of clamping: the global empty-match advance relies
 * on a strlen+1 target mapping beyond the end so PCRE2 rejects it and the loop
 * ends. */
i64 __kml_regex_utf16_to_byte(const u8 *str, i64 target) {
    i64 i = 0, u = 0;
    while (u < target) {
        if (str[i] == 0) return i + (target - u);
        i64 w = __kml_regex_utf8_width(str, i);
        u += w == 4 ? 2 : 1;
        i += w;
    }
    return i;
}

/* -regex=ecmascript: rewrite an unescaped top-level `.` (outside a class, and
 * only without the s flag) to the class of everything but the four ES line
 * terminators. Everything else is copied verbatim. A malloc'd result; the
 * longest expansion is 19 bytes per input byte. */
void *__kml_regex_es_normalize(const char *pat, unsigned char dotall) {
    static const char repl[20] = "[^\\n\\r\\u2028\\u2029]";
    i64 len = (i64)strlen(pat), s = 0, d = 0;
    char *out = (char *)malloc((size_t)len * 19 + 1); /* scratch */
    int incls = 0;
    while (s < len) {
        char c = pat[s];
        if (c == '\\') {
            out[d++] = '\\';
            if (s + 1 < len) {
                out[d++] = pat[s + 1];
                s += 2;
            } else {
                s += 1;
            }
        } else if (c == '.' && !incls && !(dotall & 1)) {
            memcpy(out + d, repl, 19);
            d += 19;
            s += 1;
        } else {
            if (c == '[' && !incls) incls = 1;
            else if (c == ']' && incls) incls = 0;
            out[d++] = c;
            s += 1;
        }
    }
    out[d] = 0;
    return out;
}

/* ECMAScript's two class forms PCRE reads otherwise, rewritten for every ES
 * dialect: `[]` matches nothing, `(?!)`, and `[^]` anything, `[\s\S]` (PCRE
 * reads a `]` right after `[` or `[^` as a literal member). The pattern
 * itself when neither occurs, else a fresh malloc'd copy. */
static char *v_translate(const char *pat, const char **err);

static void *es_classes(const char *pat);
static void *empty_iter_guard(void *in);
static void *iter_mark(void *in);

/* The ES dialects' rewrite of a pattern before PCRE2 compiles it: a `v`-mode
 * pattern's class sets (v_translate), then `[]` and `[^]`. NULL on a class
 * set syntax error, with *err set to Node's SyntaxError message. */
/* u and v modes reject a lone `]` or `}` and a `{` that starts no
 * quantifier; the reason, or NULL. */
static const char *uv_brackets(const char *pat) {
    i64 len = (i64)strlen(pat);
    int incls = 0;
    for (i64 i = 0; i < len; i++) {
        char c = pat[i];
        if (c == '\\') {
            /* `\p{…}`, `\P{…}`, `\u{…}`: braces of the escape, no quantifier. */
            if ((pat[i + 1] == 'p' || pat[i + 1] == 'P' || pat[i + 1] == 'u') && pat[i + 2] == '{') {
                i += 3;
                while (pat[i] && pat[i] != '}') i++;
                continue;
            }
            i++;
            continue;
        }
        if (incls) {
            if (c == '[') incls++;
            else if (c == ']') incls--;
            continue;
        }
        if (c == '[') { incls = 1; if (pat[i + 1] == '^') i++; continue; }
        if (c == ']' || c == '}') return "Lone quantifier brackets";
        if (c == '{') {
            i64 j = i + 1;
            int digits = 0;
            while (pat[j] >= '0' && pat[j] <= '9') { j++; digits++; }
            if (digits && pat[j] == ',') { j++; while (pat[j] >= '0' && pat[j] <= '9') j++; }
            if (!digits || pat[j] != '}') return "Incomplete quantifier";
            i = j;
        }
    }
    return NULL;
}

static void *uv_error(const char *pat, const char *flags, const char *why, char **err) {
    i64 n = (i64)strlen(pat) + (i64)strlen(flags) + (i64)strlen(why) + 40;
    char *m = (char *)__kml_str_alloc(n);
    snprintf(m, (size_t)n + 1, "Invalid regular expression: /%s/%s: %s", pat, flags, why);
    __kml_str_finalize(m);
    *err = m;
    return NULL;
}

/* A quantified assertion group: V8 rejects a quantifier after a lookbehind,
   and after a lookahead under u or v ("Invalid quantifier"). */
static const char *quantified_lookaround(const char *pat, int uv) {
    char kinds[256]; /* per open group: 0 plain, 1 lookahead, 2 lookbehind */
    int depth = 0, incls = 0;
    for (const char *c = pat; *c; c++) {
        if (*c == '\\') { if (c[1]) c++; continue; }
        if (incls) { if (*c == ']') incls = 0; continue; }
        if (*c == '[') { incls = 1; continue; }
        if (*c == '(') {
            char k = 0;
            if (c[1] == '?' && (c[2] == '=' || c[2] == '!')) k = 1;
            else if (c[1] == '?' && c[2] == '<' && (c[3] == '=' || c[3] == '!')) k = 2;
            if (depth < (int)sizeof kinds) kinds[depth] = k;
            depth++;
            continue;
        }
        if (*c == ')' && depth > 0) {
            depth--;
            char k = depth < (int)sizeof kinds ? kinds[depth] : 0;
            char n = c[1];
            int quant = n == '*' || n == '+' || n == '?' || (n == '{' && c[2] >= '0' && c[2] <= '9');
            if (quant && (k == 2 || (k == 1 && uv))) return "Invalid quantifier";
        }
    }
    return NULL;
}

void *__kml_regex_es_classes(const char *pat, const char *flags, char **err) {
    *err = NULL;
    {
        const char *why = quantified_lookaround(pat, flags && (strchr(flags, 'u') || strchr(flags, 'v')));
        if (why) return uv_error(pat, flags ? flags : "", why, err);
    }
    if (flags && (strchr(flags, 'u') || strchr(flags, 'v'))) {
        const char *why = uv_brackets(pat);
        if (why) return uv_error(pat, flags, why, err);
    }
    if (flags && strchr(flags, 'v')) {
        const char *why = NULL;
        char *t = v_translate(pat, &why);
        if (!t) return uv_error(pat, flags, why, err);
        return iter_mark(empty_iter_guard(es_classes(t)));
    }
    return iter_mark(empty_iter_guard(es_classes(pat)));
}

/* ---- Repeated non-capturing groups ----
 * ES clears a quantified atom's captures at each iteration, so after
 * `/(?:(a)|b)*\/` matches "ab", group 1 is undefined; PCRE2 keeps "a" from
 * the first iteration. A repeated non-capturing group k with a capture
 * inside becomes `(?:(?C"sk")(?:X)(?C"ek"))`: each iteration notes where it
 * starts, and a completed one records that start; after the match,
 * __kml_regex_fix_optional unsets an inner capture that starts before the
 * last iteration. */
static void *iter_mark(void *in) {
    const char *pat = (const char *)in;
    if (!pat) return NULL;
    i64 len = (i64)strlen(pat);
    i64 open[256], at[256], close[256];
    int nc[256], firstCap[256], top = 0, nw = 0, incls = 0, next = 1;
    for (i64 i = 0; i < len; i++) {
        char c = pat[i];
        if (c == '\\') { i++; continue; }
        if (incls) { if (c == ']') incls = 0; continue; }
        if (c == '[') {
            incls = 1;
            if (pat[i + 1] == '^') i++;
            if (pat[i + 1] == ']') i++;
            continue;
        }
        if (c == '(') {
            int num = pat[i + 1] != '?' || (pat[i + 2] == '<' && pat[i + 3] != '=' && pat[i + 3] != '!');
            if (top < 256) { open[top] = i; nc[top] = pat[i + 1] == '?' && pat[i + 2] == ':'; firstCap[top] = next; }
            if (num) next++;
            top++;
            continue;
        }
        if (c == ')' && top > 0) {
            top--;
            if (top >= 256 || !nc[top] || firstCap[top] == next) continue;
            const char *q = pat + i + 1;
            int rep = q[0] == '*' || q[0] == '+' ||
                      (q[0] == '{' && strncmp(q, "{0,1}", 5) != 0 && strncmp(q, "{1}", 3) != 0 && strncmp(q, "{0}", 3) != 0 && strncmp(q, "{1,1}", 5) != 0);
            if (rep && nw < 256) { at[nw] = open[top] + 3; close[nw] = i; nw++; }
        }
    }
    if (!nw) return in;
    char *out = (char *)malloc((size_t)(len + nw * 28 + 1)); /* scratch */
    i64 d = 0;
    for (i64 i = 0; i < len; i++) {
        for (int k = 0; k < nw; k++) {
            if (at[k] == i) d += sprintf(out + d, "(?C\"s%d\")(?:", k);
            if (close[k] == i) d += sprintf(out + d, ")(?C\"e%d\")", k);
        }
        out[d++] = pat[i];
    }
    out[d] = 0;
    return out;
}

/* ---- Empty iterations ----
 * ECMAScript rejects an iteration of a quantified atom that matches empty
 * once its minimum is met: `/(a|)*\/` on "aa" captures "a", where PCRE2
 * runs one more, empty, iteration and captures "". A capture group under a
 * quantifier with no minimum (`*`, `?`, `{0,…}`) whose body may match
 * empty becomes `(?:(X)(?C255))`: the callout (__kml_regex_mctx's) fails
 * the iteration when the group, the last one closed, is empty. Under `+`
 * or `{1,…}`, group g becomes `(?Cg)(?:(X)(?C128+g))`: the first callout
 * notes where the repetition starts, and only an empty iteration there,
 * the first, is allowed. */
static int may_match_empty(const char *b, i64 n) {
    if (n == 0) return 1;
    for (i64 i = 0; i < n; i++) {
        char c = b[i];
        if (c == '\\') {
            if (i + 1 < n && ((b[i + 1] >= '1' && b[i + 1] <= '9') || b[i + 1] == 'k' || b[i + 1] == 'b' || b[i + 1] == 'B')) return 1;
            i++;
            continue;
        }
        if (c == '|' || c == '*' || c == '?' || c == '^' || c == '$' || (c == '{' && i + 1 < n && b[i + 1] == '0')) return 1;
    }
    return 0;
}

static void *empty_iter_guard(void *in) {
    const char *pat = (const char *)in;
    if (!pat) return NULL;
    i64 len = (i64)strlen(pat);
    i64 open[256], wrapOpen[256], wrapClose[256];
    int cap[256], wrapGroup[256], top = 0, nw = 0, incls = 0, next = 1;
    for (i64 i = 0; i < len; i++) {
        char c = pat[i];
        if (c == '\\') { i++; continue; }
        if (incls) { if (c == ']') incls = 0; continue; }
        if (c == '[') {
            incls = 1;
            if (pat[i + 1] == '^') i++;
            if (pat[i + 1] == ']') i++;
            continue;
        }
        if (c == '(') {
            int num = pat[i + 1] != '?' || (pat[i + 2] == '<' && pat[i + 3] != '=' && pat[i + 3] != '!');
            if (num) num = next++;
            if (top < 256) { open[top] = i; cap[top] = num; }
            top++;
            continue;
        }
        if (c == ')' && top > 0) {
            top--;
            if (top >= 256 || !cap[top]) continue;
            const char *q = pat + i + 1;
            int nomin = q[0] == '*' || q[0] == '?' || (q[0] == '{' && q[1] == '0' && (q[2] == ',' || q[2] == '}'));
            int one = q[0] == '+' || (q[0] == '{' && q[1] == '1' && q[2] == ',' && q[3] != '1');
            i64 bs = open[top] + 1;
            if (pat[bs] == '?') { /* (?<name> */
                const char *e = strchr(pat + bs, '>');
                bs = e ? (i64)(e - pat) + 1 : i;
            }
            if ((nomin || (one && cap[top] <= 120)) && nw < 256 && may_match_empty(pat + bs, i - bs)) {
                wrapOpen[nw] = open[top];
                wrapClose[nw] = i;
                wrapGroup[nw] = nomin ? 0 : cap[top];
                nw++;
            }
        }
    }
    if (!nw) return in;
    char *out = (char *)malloc((size_t)(len + nw * 20 + 1)); /* scratch */
    i64 d = 0;
    for (i64 i = 0; i < len; i++) {
        for (int k = 0; k < nw; k++)
            if (wrapOpen[k] == i) d += sprintf(out + d, wrapGroup[k] ? "(?C%d)(?:" : "(?:", wrapGroup[k]);
        out[d++] = pat[i];
        for (int k = 0; k < nw; k++)
            if (wrapClose[k] == i) d += sprintf(out + d, "(?C%d))", wrapGroup[k] ? 128 + wrapGroup[k] : 255);
    }
    out[d] = 0;
    return out;
}

typedef struct {
    unsigned version, callout_number, capture_top, capture_last;
    size_t *offset_vector;
    const unsigned char *mark, *subject;
    size_t subject_length, start_match, current_position;
    size_t pattern_position, next_item_length;
    size_t callout_string_offset, callout_string_length;
    const unsigned char *callout_string;
} KmlCalloutBlock; /* pcre2_callout_block's leading fields */

static _Thread_local size_t iter_start[121];

/* iter_mark's groups: the start of the iteration under way, and of the
   last completed one, with the match attempt it was in. */
static _Thread_local size_t rep_pending[256], rep_last[256], rep_attempt[256];
static _Thread_local unsigned char rep_seen[256];

static int empty_iter_callout(KmlCalloutBlock *cb, void *data) {
    (void)data;
    unsigned n = cb->callout_number, g = cb->capture_last;
    if (cb->version >= 1 && cb->callout_string && cb->callout_string_length >= 2) {
        const char *t = (const char *)cb->callout_string;
        int k = atoi(t + 1);
        if (k < 0 || k >= 256) return 0;
        if (t[0] == 's') rep_pending[k] = cb->current_position;
        else if (t[0] == 'e') {
            rep_last[k] = rep_pending[k];
            rep_attempt[k] = cb->start_match;
            rep_seen[k] = 1;
        }
        return 0;
    }
    if (n >= 1 && n <= 120) { iter_start[n] = cb->current_position; return 0; }
    int empty = g && cb->offset_vector[2 * g] == cb->offset_vector[2 * g + 1];
    if (n == 255) return empty;
    if (n > 128 && n <= 248) return empty && cb->offset_vector[2 * g] != iter_start[n - 128];
    return 0;
}

extern void *pcre2_match_context_create_8(void *gctx) __attribute__((weak));
extern int pcre2_set_callout_8(void *mctx, int (*fn)(KmlCalloutBlock *, void *), void *data) __attribute__((weak));

/* The match context every ES-mode match runs with: its callout rejects an
   empty iteration (empty_iter_guard). One per thread. */
void *__kml_regex_mctx(void) {
    static _Thread_local void *m;
    if (!m) {
        m = pcre2_match_context_create_8(NULL);
        pcre2_set_callout_8(m, empty_iter_callout, NULL);
    }
    return m;
}

static void *es_classes(const char *pat) {
    i64 len = (i64)strlen(pat), s = 0, d = 0;
    char *out = NULL;
    int incls = 0;
    while (s < len) {
        char c = pat[s];
        if (c == '\\') {
            if (out) {
                out[d++] = c;
                if (s + 1 < len) out[d++] = pat[s + 1];
            }
            s += s + 1 < len ? 2 : 1;
            continue;
        }
        if (!incls && c == '[' && (pat[s + 1] == ']' || (pat[s + 1] == '^' && pat[s + 2] == ']'))) {
            if (!out) {
                out = (char *)malloc((size_t)len * 3 + 1); /* scratch */
                memcpy(out, pat, (size_t)s);
                d = s;
            }
            if (pat[s + 1] == ']') {
                memcpy(out + d, "(?!)", 4);
                d += 4;
                s += 2;
            } else {
                memcpy(out + d, "[\\s\\S]", 6);
                d += 6;
                s += 3;
            }
            continue;
        }
        if (c == '[' && !incls) {
            incls = 1;
            if (out) out[d++] = c;
            s++;
            /* A leading `^` and then a leading `]` are class members here. */
            if (s < len && pat[s] == '^') {
                if (out) out[d++] = '^';
                s++;
            }
            continue;
        }
        if (c == ']' && incls) incls = 0;
        if (out) out[d++] = c;
        s++;
    }
    if (!out) return (void *)pat;
    out[d] = 0;
    return out;
}

/* The flags (already validated, duplicate-free) in canonical d,g,i,m,s,u,v,y
 * order, as a fresh headered string. */
void *__kml_regex_flags_canon(const char *f) {
    static const char order[8] = {'d', 'g', 'i', 'm', 's', 'u', 'v', 'y'};
    i64 len = (i64)strlen(f), d = 0;
    char *out = (char *)__kml_str_alloc(len);
    for (int k = 0; k < 8; k++)
        for (i64 s = 0; s < len; s++)
            if (f[s] == order[k]) out[d++] = order[k];
    out[d] = 0;
    __kml_str_finalize(out);
    return out;
}

/* `.source` of an empty pattern is "(?:)". */
void *__kml_regex_source_norm(char *s) {
    if (strlen(s) != 0) return s;
    char *out = (char *)__kml_str_alloc(4);
    memcpy(out, "(?:)", 5);
    return out;
}

/* 0 for a valid flags string, 1 for an unknown letter or a repeated one. */
int __kml_regex_validate_flags(const char *flags) {
    int seen = 0;
    for (i64 i = 0; flags[i]; i++) {
        const char *p = strchr("dgimsuvy", flags[i]);
        if (!p) return 1;
        int bit = 1 << (p - "dgimsuvy");
        if (seen & bit) return 1;
        seen |= bit;
    }
    return 0;
}

/* Combined PCRE2 compile options (i=8, m=1024, s=32) plus the g/i/m/s/y
 * booleans, in one pass; unknown letters are ignored. */
void __kml_regex_parse_flags(const char *flags, int *opt, u8 *g, u8 *i, u8 *m, u8 *s, u8 *y) {
    *opt = 0;
    *g = *y = *i = *m = *s = 0;
    for (; *flags; flags++) {
        switch (*flags) {
        case 'g': *g = 1; break;
        case 'i': *i = 1; *opt |= 8; break;
        case 'm': *m = 1; *opt |= 1024; break;
        case 's': *s = 1; *opt |= 32; break;
        case 'y': *y = 1; break;
        }
    }
}

/* ---- The `v` flag's class sets (ES2024 UnicodeSets mode) ----
 * PCRE2 before 10.45 has no class set operations, so each class of a
 * `v`-mode pattern is rewritten into constructs it does read:
 *   [A B C]   union: a PCRE class for its single characters, ranges and
 *             escapes, alternated with its nested classes and its strings
 *             (\q{…}, longest first)
 *   [A&&B]    intersection: (?:(?=B)A)
 *   [A--B]    difference: (?:(?!B)A)
 *   [^…]      complement: (?:(?!…)[\s\S]) (a plain class keeps [^…])
 * Everything outside a class is copied unchanged. */

typedef struct {
    char *b;
    i64 n, cap;
} VBuf;

static void vb_put(VBuf *v, const char *s, i64 n) {
    if (v->n + n + 1 > v->cap) {
        v->cap = (v->n + n + 1) * 2 + 64;
        v->b = (char *)realloc(v->b, (size_t)v->cap); /* scratch */
    }
    memcpy(v->b + v->n, s, (size_t)n);
    v->n += n;
    v->b[v->n] = 0;
}
static void vb_cstr(VBuf *v, const char *s) { vb_put(v, s, (i64)strlen(s)); }

typedef struct {
    const char *p;
    i64 i, len;
    const char *err;
} VSrc;

static int v_utf8_len(unsigned char c) { return c < 0x80 ? 1 : c < 0xE0 ? 2 : c < 0xF0 ? 3 : 4; }

/* A ClassSetSyntaxCharacter, which v mode requires escaped. */
static int v_syntax(char c) { return c && strchr("()[]{}/-\\|", c) != NULL; }

/* A doubled ClassSetReservedDoublePunctuator (`&&` is the intersection). */
static int v_reserved_double(const VSrc *s) {
    char c = s->p[s->i];
    return c && s->i + 1 < s->len && s->p[s->i + 1] == c && strchr("!#$%*+,.:;<=>?@^`~", c);
}

/* One character of a class (a literal or an escape) appended to plain as a
 * PCRE class member; *single is set when it names exactly one character
 * (a range bound). 0 on a syntax error. */
static int v_char(VSrc *s, VBuf *plain, int *single) {
    char c = s->p[s->i];
    *single = 1;
    if (c == '\\') {
        if (s->i + 1 >= s->len) { s->err = "\\ at end of pattern"; return 0; }
        char e = s->p[s->i + 1];
        i64 start = s->i;
        s->i += 2;
        if (strchr("dDwWsS", e)) *single = 0;
        if ((e == 'p' || e == 'P') && s->p[s->i] == '{') {
            while (s->i < s->len && s->p[s->i] != '}') s->i++;
            if (s->i >= s->len) { s->err = "Invalid property name in character class"; return 0; }
            s->i++;
            *single = 0;
        } else if (e == 'u' && s->p[s->i] == '{') {
            while (s->i < s->len && s->p[s->i] != '}') s->i++;
            if (s->i < s->len) s->i++;
        } else if (e == 'u' || e == 'x') {
            i64 k = e == 'u' ? 4 : 2;
            while (k-- > 0 && s->i < s->len) s->i++;
        } else if (e == 'c' && s->i < s->len) {
            s->i++;
        } else if (e == 'b') {
            vb_cstr(plain, "\\x08");
            return 1;
        }
        vb_put(plain, s->p + start, s->i - start);
        return 1;
    }
    if (v_syntax(c)) { s->err = "Invalid character in character class"; return 0; }
    if (v_reserved_double(s)) { s->err = "Invalid set operation in character class"; return 0; }
    int n = v_utf8_len((unsigned char)c);
    if (c == '^' || c == ']') vb_cstr(plain, "\\");
    vb_put(plain, s->p + s->i, n);
    s->i += n;
    return 1;
}

static int v_class(VSrc *s, VBuf *out);

/* \q{…} at s->i: its strings as an alternation, longest first (a class's
 * strings match the longest that fits). */
static int v_strings(VSrc *s, VBuf *out) {
    s->i += 3;
    VBuf alt[64];
    int n = 0;
    memset(alt, 0, sizeof alt);
    while (1) {
        if (s->i >= s->len) { s->err = "Invalid escape"; break; }
        char c = s->p[s->i];
        if (c == '}' || c == '|') {
            if (n < 64) { vb_put(&alt[n], "", 0); n++; }
            s->i++;
            if (c == '}') break;
            continue;
        }
        VBuf *a = &alt[n < 64 ? n : 63];
        if (c == '\\' && s->i + 1 < s->len) { vb_put(a, s->p + s->i, 2); s->i += 2; continue; }
        if (strchr(".*+?()[]{}^$/", c)) vb_cstr(a, "\\");
        int k = v_utf8_len((unsigned char)c);
        vb_put(a, s->p + s->i, k);
        s->i += k;
    }
    if (s->err) {
        for (int k = 0; k < 64; k++) free(alt[k].b); /* scratch */
        return 0;
    }
    /* longest first, stable */
    for (int x = 1; x < n; x++)
        for (int y = x; y > 0 && alt[y].n > alt[y - 1].n; y--) {
            VBuf t = alt[y];
            alt[y] = alt[y - 1];
            alt[y - 1] = t;
        }
    vb_cstr(out, "(?:");
    for (int k = 0; k < n; k++) {
        if (k) vb_cstr(out, "|");
        vb_put(out, alt[k].b ? alt[k].b : "", alt[k].n);
    }
    vb_cstr(out, ")");
    for (int k = 0; k < 64; k++) free(alt[k].b); /* scratch */
    return 1;
}

typedef struct {
    char kind; /* 'p' a PCRE class member, 'a' a snippet of its own */
    VBuf s;
} VItem;

/* One item of a class: a nested class or \q{…} (kind 'a'), or a character
 * or range (kind 'p'). */
static int v_item(VSrc *s, VItem *it) {
    memset(it, 0, sizeof *it);
    if (s->p[s->i] == '[') { it->kind = 'a'; return v_class(s, &it->s); }
    if (s->p[s->i] == '\\' && s->p[s->i + 1] == 'q' && s->p[s->i + 2] == '{') {
        it->kind = 'a';
        return v_strings(s, &it->s);
    }
    it->kind = 'p';
    int single;
    if (!v_char(s, &it->s, &single)) return 0;
    if (s->p[s->i] == '-' && s->p[s->i + 1] != '-' && s->p[s->i + 1] != ']') {
        s->i++;
        int single2;
        VBuf hi = {0};
        if (!single || !v_char(s, &hi, &single2) || !single2) {
            if (!s->err) s->err = "Invalid character class";
            free(hi.b); /* scratch */
            return 0;
        }
        vb_cstr(&it->s, "-");
        vb_put(&it->s, hi.b, hi.n);
        free(hi.b); /* scratch */
    }
    return 1;
}

static void v_snippet(VBuf *out, const VItem *it) {
    if (it->kind == 'p') {
        vb_cstr(out, "[");
        vb_put(out, it->s.b, it->s.n);
        vb_cstr(out, "]");
    } else {
        vb_put(out, it->s.b, it->s.n);
    }
}

/* A class at s->p[s->i] == '[', through its `]`, appended to out. */
static int v_class(VSrc *s, VBuf *out) {
    s->i++;
    int neg = 0;
    if (s->p[s->i] == '^') { neg = 1; s->i++; }
    VItem items[256];
    int n = 0, ok = 1;
    char op = 0; /* '&' intersection, '-' difference, 0 union */
    while (1) {
        if (s->i >= s->len) { s->err = "Unterminated character class"; ok = 0; break; }
        char c = s->p[s->i];
        if (c == ']') { s->i++; break; }
        int isand = c == '&' && s->p[s->i + 1] == '&';
        int isdiff = c == '-' && s->p[s->i + 1] == '-';
        if (isand || isdiff) {
            char want = isand ? '&' : '-';
            /* an operator needs exactly one operand before it, and
               intersections and differences do not mix with a union or
               with each other */
            if (n == 0 || (!op && n != 1) || (op && op != want)) { s->err = "Invalid set operation in character class"; ok = 0; break; }
            /* the operand before the first operator is no range either */
            if (!op && items[0].kind == 'p' && items[0].s.b[0] != '\\' && strchr(items[0].s.b, '-')) {
                s->err = "Invalid set operation in character class";
                ok = 0;
                break;
            }
            op = want;
            s->i += 2;
            if (s->i < s->len && s->p[s->i] == want) {
                /* `&&&`: the third is a character a class cannot hold */
                s->err = "Invalid character in character class";
                ok = 0;
                break;
            }
            if (s->i >= s->len || s->p[s->i] == ']') {
                s->err = "Invalid set operation in character class";
                ok = 0;
                break;
            }
            if (n >= 256) { s->err = "Character class too large"; ok = 0; break; }
            if (!v_item(s, &items[n])) { free(items[n].s.b); ok = 0; break; } /* scratch */
            /* an operand of an operator is no range */
            if (items[n].kind == 'p' && strchr(items[n].s.b, '-') && items[n].s.b[0] != '\\') {
                n++;
                s->err = "Invalid set operation in character class";
                ok = 0;
                break;
            }
            n++;
            continue;
        }
        if (op) { s->err = "Invalid set operation in character class"; ok = 0; break; }
        if (n >= 256) { s->err = "Character class too large"; ok = 0; break; }
        if (!v_item(s, &items[n])) { free(items[n].s.b); ok = 0; break; } /* scratch */
        n++;
    }
    if (ok) {
        VBuf body = {0};
        int plainOnly = 0;
        vb_put(&body, "", 0);
        if (op) {
            vb_cstr(&body, "(?:");
            for (int k = 1; k < n; k++) {
                vb_cstr(&body, op == '&' ? "(?=" : "(?!");
                v_snippet(&body, &items[k]);
                vb_cstr(&body, ")");
            }
            v_snippet(&body, &items[0]);
            vb_cstr(&body, ")");
        } else {
            VBuf plain = {0}, alts = {0};
            for (int k = 0; k < n; k++) {
                if (items[k].kind == 'p') {
                    vb_put(&plain, items[k].s.b, items[k].s.n);
                } else {
                    if (alts.n) vb_cstr(&alts, "|");
                    vb_put(&alts, items[k].s.b, items[k].s.n);
                }
            }
            if (!alts.n && !plain.n) {
                vb_cstr(&body, neg ? "[\\s\\S]" : "(?!)");
                neg = 0;
            } else if (!alts.n) {
                vb_cstr(&body, neg ? "[^" : "[");
                vb_put(&body, plain.b, plain.n);
                vb_cstr(&body, "]");
                plainOnly = 1;
            } else {
                vb_cstr(&body, "(?:");
                vb_put(&body, alts.b, alts.n);
                if (plain.n) {
                    vb_cstr(&body, "|[");
                    vb_put(&body, plain.b, plain.n);
                    vb_cstr(&body, "]");
                }
                vb_cstr(&body, ")");
            }
            free(plain.b); /* scratch */
            free(alts.b); /* scratch */
        }
        if (neg && !plainOnly) {
            vb_cstr(out, "(?:(?!");
            vb_put(out, body.b, body.n);
            vb_cstr(out, ")[\\s\\S])");
        } else {
            vb_put(out, body.b, body.n);
        }
        free(body.b); /* scratch */
    }
    for (int k = 0; k < n; k++) free(items[k].s.b); /* scratch */
    return ok;
}

/* The `v`-mode pattern pat with its classes rewritten (malloc'd), or NULL
 * with *err set to the reason on a class set syntax error. */
static char *v_translate(const char *pat, const char **err) {
    VSrc s = {pat, 0, (i64)strlen(pat), NULL};
    VBuf out = {0};
    vb_put(&out, "", 0);
    while (s.i < s.len) {
        char c = pat[s.i];
        if (c == '\\' && s.i + 1 < s.len) {
            vb_put(&out, pat + s.i, 2);
            s.i += 2;
            continue;
        }
        if (c == '[') {
            if (!v_class(&s, &out)) {
                *err = s.err ? s.err : "Invalid character class";
                free(out.b); /* scratch */
                return NULL;
            }
            continue;
        }
        vb_put(&out, pat + s.i, 1);
        s.i++;
    }
    return out.b;
}

/* ---- Empty optional groups ----
 * ECMAScript rejects an iteration of a quantified atom that matches empty
 * once its minimum is met, so in `(X)?` an X that could only match empty
 * leaves the group undefined; PCRE2 records the empty match. Each pattern's
 * capture groups directly under a `?` ({0,1}) are noted when it compiles,
 * keyed by the compiled pattern; after a match, such a group that captured
 * an empty string, and every group inside it, becomes unset. */

typedef struct OptGroups {
    void *code;
    int n;          /* triples */
    int *pairs;     /* first group, last group inside it, 1 when optional */
    struct OptGroups *next;
} OptGroups;

static OptGroups *opt_table[64];
static volatile int opt_lock;

static void opt_acquire(void) { while (__sync_lock_test_and_set(&opt_lock, 1)) {} }
static void opt_release(void) { __sync_lock_release(&opt_lock); }

static unsigned opt_slot(void *code) { return (unsigned)(((unsigned long long)code >> 4) & 63); }

/* Notes the optional capture groups of pat (the pattern as compiled). */
void __kml_regex_note_optional(void *code, const char *pat) {
    if (!code) return;
    /* a freed pattern's address may come back for this one */
    opt_acquire();
    for (OptGroups **pp = &opt_table[opt_slot(code)]; *pp; pp = &(*pp)->next)
        if ((*pp)->code == code) {
            *pp = (*pp)->next;
            break;
        }
    opt_release();
    i64 len = (i64)strlen(pat);
    int stack[256], top = 0, next = 1;
    int pairs[768], np = 0, markOf[256];
    int incls = 0;
    for (i64 i = 0; i < len; i++) {
        char c = pat[i];
        if (c == '\\') { i++; continue; }
        if (incls) { if (c == ']') incls = 0; continue; }
        if (c == '[') {
            incls = 1;
            if (pat[i + 1] == '^') i++;
            if (pat[i + 1] == ']') i++;
            continue;
        }
        if (c == '(' && pat[i + 1] == '?' && pat[i + 2] == 'C' && pat[i + 3] == '"' && pat[i + 4] == 's') {
            /* iter_mark's start callout: its group opened just before */
            int k = atoi(pat + i + 5);
            if (top > 0 && np < 765) {
                pairs[np++] = next; /* first capture inside, fixed at ')' */
                pairs[np++] = -1;
                pairs[np++] = 2 + k;
                if (top <= 256) markOf[top - 1] = np - 3;
            }
        }
        if (c == '(') {
            int num = 0;
            if (pat[i + 1] != '?' && pat[i + 1] != '*') num = next++;
            else if (pat[i + 2] == '<' && pat[i + 3] != '=' && pat[i + 3] != '!') num = next++;
            else if (pat[i + 2] == 'P' && pat[i + 3] == '<') num = next++;
            else if (pat[i + 2] == '\'') num = next++;
            if (top < 256) { markOf[top] = -1; stack[top++] = num; }
            continue;
        }
        if (c == ')' && top > 0) {
            int num = stack[--top];
            if (markOf[top] >= 0) pairs[markOf[top] + 1] = next - 1;
            if (!num) continue;
            const char *q = pat + i + 1;
            /* through empty_iter_guard's wrapper `(?Cn))` */
            if (q[0] == '(' && q[1] == '?' && q[2] == 'C') {
                const char *e = strchr(q, ')');
                if (e && e[1] == ')') q = e + 2;
            }
            int opt = q[0] == '?' || strncmp(q, "{0,1}", 5) == 0 || strncmp(q, "{0}", 3) == 0;
            int quant = opt || q[0] == '*' || q[0] == '+' || q[0] == '{';
            if (quant && np < 765) {
                pairs[np++] = num;
                pairs[np++] = next - 1;
                pairs[np++] = opt;
            }
        }
    }
    if (!np) return;
    OptGroups *g = (OptGroups *)malloc(sizeof *g); /* scratch */
    g->code = code;
    g->n = np / 3;
    g->pairs = (int *)malloc(sizeof(int) * (size_t)np); /* scratch */
    memcpy(g->pairs, pairs, sizeof(int) * (size_t)np);
    opt_acquire();
    unsigned k = opt_slot(code);
    g->next = opt_table[k];
    opt_table[k] = g;
    opt_release();
}

/* Weak: a program may link this file without matching anything. */
extern size_t *pcre2_get_ovector_pointer_8(void *md) __attribute__((weak));
extern unsigned pcre2_get_ovector_count_8(void *md) __attribute__((weak));

static void fix_groups(OptGroups *g, void *md);

/* After a match (rc > 0) of code into md: an optional group that captured
 * an empty string, and the groups inside it, become unset; inside a
 * repeated group, a capture from an earlier iteration (starting before the
 * group's last one) becomes unset, as ES clears them each iteration. */
void __kml_regex_fix_optional(void *code, void *md, int rc) {
    if (!opt_table[opt_slot(code)]) return;
    opt_acquire();
    OptGroups *g = opt_table[opt_slot(code)];
    while (g && g->code != code) g = g->next;
    opt_release();
    if (!g) return;
    if (rc > 0) fix_groups(g, md);
    /* the marks are this match's alone */
    for (int k = 0; k < g->n; k++)
        if (g->pairs[3 * k + 2] >= 2) rep_seen[g->pairs[3 * k + 2] - 2] = 0;
}

static void fix_groups(OptGroups *g, void *md) {
    size_t *ov = pcre2_get_ovector_pointer_8(md);
    unsigned cnt = pcre2_get_ovector_count_8(md);
    for (int k = 0; k < g->n; k++) {
        unsigned first = (unsigned)g->pairs[3 * k], last = (unsigned)g->pairs[3 * k + 1];
        if (g->pairs[3 * k + 2] >= 2) {
            /* iter_mark's group r: captures before its last iteration */
            int r = g->pairs[3 * k + 2] - 2;
            if (g->pairs[3 * k + 1] < 0 || !rep_seen[r] || rep_attempt[r] != ov[0]) continue;
            for (unsigned j = first; j <= last && j < cnt; j++)
                if (ov[2 * j] != (size_t)-1 && ov[2 * j] < rep_last[r]) ov[2 * j] = ov[2 * j + 1] = (size_t)-1;
            continue;
        }
        if (first >= cnt || ov[2 * first] == (size_t)-1) continue;
        if (g->pairs[3 * k + 2] && ov[2 * first] == ov[2 * first + 1]) {
            for (unsigned j = first; j <= last && j < cnt; j++) ov[2 * j] = ov[2 * j + 1] = (size_t)-1;
            continue;
        }
        for (unsigned j = first + 1; j <= last && j < cnt; j++)
            if (ov[2 * j] != (size_t)-1 && ov[2 * j] < ov[2 * first]) ov[2 * j] = ov[2 * j + 1] = (size_t)-1;
    }
}

/* An unterminated `(?` that is no group syntax V8 knows: `(?` at the end,
   or followed by a character other than `:`, `=`, `!` or `<`. */
static int bad_group_opener(const char *pat) {
    int incls = 0;
    for (const char *c = pat; *c; c++) {
        if (*c == '\\') { if (c[1]) c++; continue; }
        if (incls) { if (*c == ']') incls = 0; continue; }
        if (*c == '[') { incls = 1; continue; }
        if (c[0] == '(' && c[1] == '?' && !strchr(":=!<", c[2] ? c[2] : 'x')) return 1;
        if (c[0] == '(' && c[1] == '?' && c[2] == 0) return 1;
    }
    return 0;
}

/* A PCRE2 compile error (code, its message pcre) in V8's words, as the
   SyntaxError message `Invalid regular expression: /pat/flags: why`. A code
   without a V8 counterpart keeps PCRE2's message. */
void *__kml_regex_syntax_message(int code, const char *pcre, const char *pat, const char *flags) {
    const char *why = pcre;
    switch (code) {
    case 101: why = "\\ at end of pattern"; break;
    case 104: why = "numbers out of order in {} quantifier"; break;
    case 106: why = "Unterminated character class"; break;
    case 108: why = "Range out of order in character class"; break;
    case 109: why = "Nothing to repeat"; break;
    case 111: why = "Invalid group"; break;
    case 114: why = bad_group_opener(pat) ? "Invalid group" : "Unterminated group"; break;
    case 115: why = "Invalid named capture referenced"; break;
    case 122: why = "Unmatched ')'"; break;
    case 142: case 144: case 162: why = "Invalid capture group name"; break;
    case 143: why = "Duplicate capture group name"; break;
    case 146: case 147: why = "Invalid property name"; break;
    }
    char *err = NULL;
    uv_error(pat, flags, why, &err);
    return err;
}

/* A replacement template expanded for one match, as the spec's
   GetSubstitution: `$$`, `$&` the match, `` $` `` the text before it, `$'`
   the text after it, `$n`/`$nn` capture n (two digits when that group
   exists, else one), an unmatched group empty; anything else literal.
   groups[0..ngroups) is the match and its captures (null: unmatched);
   the match spans subject's bytes [mstart, mend). A headered string. */
void *__kml_regex_expand(const char *tpl, char **groups, i64 ngroups, const char *subject, i64 mstart, i64 mend) {
    i64 caps = ngroups - 1, slen = (i64)strlen(subject), tlen = (i64)strlen(tpl);
    char *out = NULL;
    for (int pass = 0; pass < 2; pass++) {
        i64 n = 0;
        char *o = pass ? out : NULL;
#define EMIT(p, l) do { if (o) memcpy(o + n, (p), (size_t)(l)); n += (l); } while (0)
        for (i64 i = 0; i < tlen; i++) {
            char c = tpl[i];
            if (c != '$' || i + 1 >= tlen) { EMIT(&tpl[i], 1); continue; }
            char d = tpl[i + 1];
            if (d == '$') { EMIT("$", 1); i++; continue; }
            if (d == '&') { const char *g = groups[0] ? groups[0] : ""; EMIT(g, strlen(g)); i++; continue; }
            if (d == '`') { EMIT(subject, mstart); i++; continue; }
            if (d == '\'') { if (mend < slen) EMIT(subject + mend, slen - mend); i++; continue; }
            if (d >= '0' && d <= '9') {
                i64 k = d - '0', used = 1;
                if (i + 2 < tlen && tpl[i + 2] >= '0' && tpl[i + 2] <= '9') {
                    i64 kk = k * 10 + (tpl[i + 2] - '0');
                    if (kk >= 1 && kk <= caps) { k = kk; used = 2; }
                }
                if (k >= 1 && k <= caps) {
                    const char *g = groups[k] ? groups[k] : "";
                    EMIT(g, strlen(g));
                    i += used;
                    continue;
                }
            }
            EMIT("$", 1);
        }
#undef EMIT
        if (!pass) {
            out = (char *)__kml_str_alloc(n);
        } else {
            out[n] = 0;
            __kml_str_finalize(out);
            return out;
        }
    }
    return NULL;
}
