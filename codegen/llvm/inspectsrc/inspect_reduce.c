// inspect_reduce.c — util.inspect's line layout (KlainMainLang ADR-01067).
//
// Node renders an object/array/Map/Set by formatting each entry to a string,
// then deciding the layout in `reduceToSingleString` (lib/internal/util/inspect.js):
// the entries go on one line when their total width fits `breakLength` (80)
// and the value nests fewer than `compact` (3) levels below this one;
// otherwise one entry per line, indented two spaces past the enclosing
// indentation. An array of more than 6 short entries is first regrouped into
// aligned columns (`groupArrayElements`). The emitted inspectors build an
// entry list with __kml_inspect_begin/push and hand it to __kml_inspect_end,
// which applies exactly that algorithm. libc only.
//
// Strings are length-prefixed (i64 at ptr-8, TDD-00120); every string this
// file returns carries that header. Entry strings are read by strlen (an
// inspected string never embeds a NUL — it is rendered escaped).
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>

#define KML_INSPECT_BREAK_LENGTH 80
#define KML_INSPECT_COMPACT 3

typedef struct {
    char **items;
    long long n, cap;
    int extra; /* the last entry is `... n more items`: shown, never grouped */
} KmlInspectList;

/* Node's ctx.currentDepth: the depth of the most recently entered non-empty
   container. `currentDepth - recurseTimes < compact` is what lets only the
   innermost three levels share a line. */
static long long kml_inspect_cur_depth = 0;

static char *str_alloc(long long n) {
    char *base = (char *)malloc((size_t)n + 9);
    *(long long *)base = n;
    base[8 + n] = 0;
    return base + 8;
}

static char *str_alloc_lit(const char *lit) {
    long long n = (long long)strlen(lit);
    char *out = str_alloc(n);
    memcpy(out, lit, (size_t)n);
    return out;
}

/* Display width, Node's getStringWidth: a full-width code point counts 2,
   a zero-width one (combining marks, format characters, variation selectors,
   C0/C1 controls) 0, anything else 1. Ranges as in lib/internal/util/inspect.js
   (isFullWidthCodePoint / isZeroWidthCodePoint). */
static int cp_full_width(unsigned int c) {
    return c >= 0x1100 && (
        c <= 0x115F ||
        c == 0x2329 || c == 0x232A ||
        (c >= 0x2E80 && c <= 0x3247 && c != 0x303F) ||
        (c >= 0x3250 && c <= 0x4DBF) ||
        (c >= 0x4E00 && c <= 0xA4C6) ||
        (c >= 0xA960 && c <= 0xA97C) ||
        (c >= 0xAC00 && c <= 0xD7A3) ||
        (c >= 0xF900 && c <= 0xFAFF) ||
        (c >= 0xFE10 && c <= 0xFE19) ||
        (c >= 0xFE30 && c <= 0xFE6B) ||
        (c >= 0xFF01 && c <= 0xFF60) ||
        (c >= 0xFFE0 && c <= 0xFFE6) ||
        (c >= 0x1B000 && c <= 0x1B001) ||
        (c >= 0x1F200 && c <= 0x1F251) ||
        (c >= 0x1F300 && c <= 0x1F64F) ||
        (c >= 0x20000 && c <= 0x3FFFD));
}
static int cp_zero_width(unsigned int c) {
    return c <= 0x1F ||
        (c >= 0x7F && c <= 0x9F) ||
        (c >= 0x300 && c <= 0x36F) ||
        (c >= 0x200B && c <= 0x200F) ||
        (c >= 0x20D0 && c <= 0x20FF) ||
        (c >= 0xFE00 && c <= 0xFE0F) ||
        (c >= 0xFE20 && c <= 0xFE2F) ||
        (c >= 0xE0100 && c <= 0xE01EF);
}
static long long str_width(const char *s) {
    long long w = 0;
    const unsigned char *p = (const unsigned char *)s;
    while (*p) {
        unsigned int c = *p;
        int len = c < 0x80 ? 1 : c >= 0xF0 ? 4 : c >= 0xE0 ? 3 : c >= 0xC0 ? 2 : 1;
        if (len > 1) {
            c &= len == 2 ? 0x1F : len == 3 ? 0x0F : 0x07;
            for (int k = 1; k < len && p[k]; k++) c = (c << 6) | (p[k] & 0x3F);
        }
        w += cp_zero_width(c) ? 0 : cp_full_width(c) ? 2 : 1;
        p += len;
    }
    return w;
}

void *__kml_inspect_begin(long long depth, long long nonempty) {
    KmlInspectList *l = (KmlInspectList *)malloc(sizeof *l);
    l->n = 0;
    l->cap = 8;
    l->extra = 0;
    l->items = (char **)malloc(sizeof(char *) * (size_t)l->cap);
    if (nonempty) kml_inspect_cur_depth = depth;
    return l;
}

void __kml_inspect_push(void *lp, char *entry) {
    KmlInspectList *l = (KmlInspectList *)lp;
    if (l->n == l->cap) {
        l->cap *= 2;
        l->items = (char **)realloc(l->items, sizeof(char *) * (size_t)l->cap);
    }
    l->items[l->n++] = entry;
}

/* Node's maxArrayLength (100): the walker stops there and this appends the
   `... n more items` entry, which the layout shows but the column grouping
   leaves alone. */
void __kml_inspect_push_more(void *lp, long long remaining) {
    char tmp[64];
    snprintf(tmp, sizeof tmp, "... %lld more item%s", remaining, remaining > 1 ? "s" : "");
    __kml_inspect_push(lp, str_alloc_lit(tmp));
    ((KmlInspectList *)lp)->extra = 1;
}

typedef struct { char *p; size_t n, cap; } Sb;
static void sb_put(Sb *b, const char *s, size_t n) {
    if (b->n + n + 1 > b->cap) {
        size_t c = b->cap ? b->cap : 64;
        while (c < b->n + n + 1) c *= 2;
        b->p = (char *)realloc(b->p, c);
        b->cap = c;
    }
    memcpy(b->p + b->n, s, n);
    b->n += n;
}
static void sb_str(Sb *b, const char *s) { sb_put(b, s, strlen(s)); }
static void sb_pad(Sb *b, long long n) { while (n-- > 0) sb_put(b, " ", 1); }
static char *sb_finish(Sb *b) {
    char *out = str_alloc((long long)b->n);
    if (b->n) memcpy(out, b->p, b->n);
    free(b->p);
    return out;
}

/* groupArrayElements: more than 6 entries, all short → aligned columns.
   Returns a fresh list (the caller frees) or NULL when no grouping applies. */
static KmlInspectList *group_array(KmlInspectList *l, long long indent, long long numeric) {
    long long n = l->n - l->extra;
    long long *dataLen = (long long *)malloc(sizeof(long long) * (size_t)n);
    long long totalLength = 0, maxLength = 0;
    const long long separatorSpace = 2;
    for (long long i = 0; i < n; i++) {
        long long len = str_width(l->items[i]);
        dataLen[i] = len;
        totalLength += len + separatorSpace;
        if (maxLength < len) maxLength = len;
    }
    long long actualMax = maxLength + separatorSpace;
    KmlInspectList *out = NULL;
    if (actualMax * 3 + indent < KML_INSPECT_BREAK_LENGTH &&
        ((double)totalLength / (double)actualMax > 5 || maxLength <= 6)) {
        double approxCharHeights = 2.5;
        double averageBias = sqrt((double)actualMax - (double)totalLength / (double)n);
        double biasedMax = (double)actualMax - 3 - averageBias;
        if (biasedMax < 1) biasedMax = 1;
        long long columns = (long long)floor(sqrt(approxCharHeights * biasedMax * (double)n) / biasedMax + 0.5);
        long long c2 = (KML_INSPECT_BREAK_LENGTH - indent) / actualMax;
        if (c2 < columns) columns = c2;
        if (KML_INSPECT_COMPACT * 4 < columns) columns = KML_INSPECT_COMPACT * 4;
        if (15 < columns) columns = 15;
        if (columns > 1) {
            long long *maxLineLength = (long long *)malloc(sizeof(long long) * (size_t)columns);
            for (long long i = 0; i < columns; i++) {
                long long lineLength = 0;
                for (long long j = i; j < n; j += columns)
                    if (dataLen[j] > lineLength) lineLength = dataLen[j];
                maxLineLength[i] = lineLength + separatorSpace;
            }
            out = (KmlInspectList *)__kml_inspect_begin(0, 0);
            for (long long i = 0; i < n; i += columns) {
                long long max = i + columns < n ? i + columns : n;
                Sb b = {0, 0, 0};
                long long j = i;
                for (; j < max - 1; j++) {
                    /* `${output[j]}, ` padded to maxLineLength[j-i] (padStart for
                       numbers, padEnd otherwise). */
                    long long width = dataLen[j] + separatorSpace;
                    long long padding = maxLineLength[j - i] - width;
                    if (numeric) sb_pad(&b, padding);
                    sb_str(&b, l->items[j]);
                    sb_put(&b, ", ", 2);
                    if (!numeric) sb_pad(&b, padding);
                }
                if (numeric) {
                    long long padding = maxLineLength[j - i] - separatorSpace - dataLen[j];
                    sb_pad(&b, padding);
                }
                sb_str(&b, l->items[j]);
                __kml_inspect_push(out, sb_finish(&b));
            }
            if (l->extra) __kml_inspect_push(out, l->items[n]);
            free(maxLineLength);
        }
    }
    free(dataLen);
    return out;
}

static void list_free(KmlInspectList *l) {
    free(l->items);
    free(l);
}

/* __kml_inspect_end lays out the collected entries between open/close
   (`{`/`}`, `Point {`/`}`, `[`/`]`, `Map(2) {`/`}`) and frees the list.
   indent is the enclosing indentation (2 per nesting level), depth this
   container's nesting depth, is_array selects column grouping, numeric its
   right-alignment. */
char *__kml_inspect_end(void *lp, const char *open, const char *close,
                        long long indent, long long depth, long long is_array, long long numeric) {
    KmlInspectList *l = (KmlInspectList *)lp;
    Sb b = {0, 0, 0};
    if (l->n == 0) {
        sb_str(&b, open);
        sb_str(&b, close);
        list_free(l);
        return sb_finish(&b);
    }
    long long entries = l->n;
    KmlInspectList *output = l, *grouped = NULL;
    if (is_array && entries > 6) {
        grouped = group_array(l, indent, numeric);
        if (grouped) output = grouped;
    }
    if (kml_inspect_cur_depth - depth < KML_INSPECT_COMPACT && entries == output->n) {
        /* isBelowBreakLength: entries + separators + braces within 80 columns
           and no entry spanning lines. */
        long long start = output->n + indent + (long long)strlen(open) + 10;
        long long totalLength = output->n + start;
        int fits = totalLength + output->n <= KML_INSPECT_BREAK_LENGTH;
        int multiline = 0;
        for (long long i = 0; fits && i < output->n; i++) {
            totalLength += str_width(output->items[i]);
            if (totalLength > KML_INSPECT_BREAK_LENGTH) fits = 0;
            if (strchr(output->items[i], '\n')) multiline = 1;
        }
        if (fits && !multiline) {
            sb_str(&b, open);
            sb_put(&b, " ", 1);
            for (long long i = 0; i < output->n; i++) {
                if (i) sb_put(&b, ", ", 2);
                sb_str(&b, output->items[i]);
            }
            sb_put(&b, " ", 1);
            sb_str(&b, close);
            if (grouped) list_free(grouped);
            list_free(l);
            return sb_finish(&b);
        }
    }
    sb_str(&b, open);
    for (long long i = 0; i < output->n; i++) {
        if (i) sb_put(&b, ",", 1);
        sb_put(&b, "\n", 1);
        sb_pad(&b, indent + 2);
        sb_str(&b, output->items[i]);
    }
    sb_put(&b, "\n", 1);
    sb_pad(&b, indent);
    sb_str(&b, close);
    if (grouped) list_free(grouped);
    list_free(l);
    return sb_finish(&b);
}

/* __kml_inspect_quote renders a string the way util.inspect quotes it
   (strEscape): single quotes, or double quotes when the string contains a
   single quote and no double quote, or backticks when it contains both but no
   backtick; \n \t \r \b \f \\ and the chosen quote are escaped, other control
   characters as \xNN. A null pointer is the caller's concern (rendered as its
   keyword before reaching here). Length from the header (embedded NULs kept). */
char *__kml_inspect_quote(const char *s) {
    /* A null (absent / `string | null`) value: the caller selects its keyword
       afterwards, but the call itself runs unconditionally — answer `''`. */
    if (!s) return str_alloc_lit("''");
    long long n = *(const long long *)(s - 8);
    int hasS = 0, hasD = 0, hasB = 0;
    for (long long i = 0; i < n; i++) {
        if (s[i] == '\'') hasS = 1;
        else if (s[i] == '"') hasD = 1;
        else if (s[i] == '`') hasB = 1;
    }
    char q = '\'';
    if (hasS) {
        if (!hasD) q = '"';
        else if (!hasB) q = '`';
    }
    Sb b = {0, 0, 0};
    sb_put(&b, &q, 1);
    for (long long i = 0; i < n; i++) {
        unsigned char c = (unsigned char)s[i];
        char esc[4];
        if (c == (unsigned char)q) { esc[0] = '\\'; esc[1] = (char)c; sb_put(&b, esc, 2); }
        else if (c == '\\') sb_put(&b, "\\\\", 2);
        else if (c == '\n') sb_put(&b, "\\n", 2);
        else if (c == '\t') sb_put(&b, "\\t", 2);
        else if (c == '\r') sb_put(&b, "\\r", 2);
        else if (c == '\b') sb_put(&b, "\\b", 2);
        else if (c == '\f') sb_put(&b, "\\f", 2);
        else if (c < 0x20 || c == 0x7f) {
            static const char hex[] = "0123456789ABCDEF";
            esc[0] = '\\'; esc[1] = 'x'; esc[2] = hex[c >> 4]; esc[3] = hex[c & 15];
            sb_put(&b, esc, 4);
        } else sb_put(&b, (const char *)&c, 1);
    }
    sb_put(&b, &q, 1);
    return sb_finish(&b);
}
