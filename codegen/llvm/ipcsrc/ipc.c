// ipc.c — child_process.fork IPC channel framing (TDD-00141).
//
// Wire format: one JSON string per line (Node's own `json` serialization
// mode), so newlines/quotes/control bytes inside a message never break
// framing. Self-contained: hand-rolled JSON string quote/unquote for the
// string-payload V1 — deliberately NOT dependent on the json parse-tree
// runtime, which is a separate optionally-linked source.
//
// A channel accumulates raw bytes from the (nonblocking) socket; take()
// yields one decoded message at a time. All returned strings are plain
// malloc'd NUL-terminated C strings; the IR caller copies them into
// length-prefixed kml strings.

#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <unistd.h>

typedef struct {
    char *data;
    long len, cap;
} KmlIpcChan;

void *__kml_ipc_chan_new(void) {
    return calloc(1, sizeof(KmlIpcChan));
}

void __kml_ipc_feed(void *chanv, const char *src, long n) {
    KmlIpcChan *ch = (KmlIpcChan *)chanv;
    if (ch->len + n + 1 > ch->cap) {
        long need = ch->len + n + 1;
        long cap = ch->cap < 64 ? 64 : ch->cap;
        while (cap < need) cap *= 2;
        ch->data = realloc(ch->data, cap);
        ch->cap = cap;
    }
    memcpy(ch->data + ch->len, src, n);
    ch->len += n;
    ch->data[ch->len] = 0;
}

// Decode a JSON string token (must start with '"'); returns malloc'd payload
// or NULL on malformed input. Handles \" \\ \/ \b \f \n \r \t and \uXXXX
// (BMP only; a surrogate pair decodes to UTF-8).
static char *ipc_unquote(const char *s, long n) {
    if (n < 2 || s[0] != '"') return NULL;
    char *out = malloc(n); // decoded is never longer than input
    long o = 0;
    long i = 1;
    while (i < n) {
        char c = s[i];
        if (c == '"') { out[o] = 0; return out; }
        if (c != '\\') { out[o++] = c; i++; continue; }
        if (i + 1 >= n) break;
        char e = s[++i]; i++;
        switch (e) {
        case '"': out[o++] = '"'; break;
        case '\\': out[o++] = '\\'; break;
        case '/': out[o++] = '/'; break;
        case 'b': out[o++] = '\b'; break;
        case 'f': out[o++] = '\f'; break;
        case 'n': out[o++] = '\n'; break;
        case 'r': out[o++] = '\r'; break;
        case 't': out[o++] = '\t'; break;
        case 'u': {
            if (i + 4 > n) { free(out); return NULL; }
            unsigned int cp = 0;
            for (int k = 0; k < 4; k++) {
                char h = s[i + k];
                cp <<= 4;
                if (h >= '0' && h <= '9') cp |= h - '0';
                else if (h >= 'a' && h <= 'f') cp |= h - 'a' + 10;
                else if (h >= 'A' && h <= 'F') cp |= h - 'A' + 10;
                else { free(out); return NULL; }
            }
            i += 4;
            if (cp >= 0xD800 && cp <= 0xDBFF && i + 6 <= n && s[i] == '\\' && s[i + 1] == 'u') {
                unsigned int lo = 0;
                int ok = 1;
                for (int k = 0; k < 4; k++) {
                    char h = s[i + 2 + k];
                    lo <<= 4;
                    if (h >= '0' && h <= '9') lo |= h - '0';
                    else if (h >= 'a' && h <= 'f') lo |= h - 'a' + 10;
                    else if (h >= 'A' && h <= 'F') lo |= h - 'A' + 10;
                    else { ok = 0; break; }
                }
                if (ok && lo >= 0xDC00 && lo <= 0xDFFF) {
                    cp = 0x10000 + ((cp - 0xD800) << 10) + (lo - 0xDC00);
                    i += 6;
                }
            }
            if (cp < 0x80) out[o++] = (char)cp;
            else if (cp < 0x800) {
                out[o++] = (char)(0xC0 | (cp >> 6));
                out[o++] = (char)(0x80 | (cp & 0x3F));
            } else if (cp < 0x10000) {
                out[o++] = (char)(0xE0 | (cp >> 12));
                out[o++] = (char)(0x80 | ((cp >> 6) & 0x3F));
                out[o++] = (char)(0x80 | (cp & 0x3F));
            } else {
                out[o++] = (char)(0xF0 | (cp >> 18));
                out[o++] = (char)(0x80 | ((cp >> 12) & 0x3F));
                out[o++] = (char)(0x80 | ((cp >> 6) & 0x3F));
                out[o++] = (char)(0x80 | (cp & 0x3F));
            }
            break;
        }
        default: free(out); return NULL;
        }
    }
    free(out);
    return NULL;
}

// Next complete message from the channel, or NULL. Caller frees.
char *__kml_ipc_take(void *chanv) {
    KmlIpcChan *ch = (KmlIpcChan *)chanv;
    if (!ch->data) return NULL;
    char *nl = memchr(ch->data, '\n', ch->len);
    if (!nl) return NULL;
    long linelen = nl - ch->data;
    char *msg = ipc_unquote(ch->data, linelen);
    if (!msg) {
        // Malformed line: deliver its raw bytes rather than dropping it.
        msg = malloc(linelen + 1);
        memcpy(msg, ch->data, linelen);
        msg[linelen] = 0;
    }
    long rest = ch->len - linelen - 1;
    memmove(ch->data, nl + 1, rest);
    ch->len = rest;
    ch->data[ch->len] = 0;
    return msg;
}

// JSON-quote s and write "<quoted>\n" to fd. Returns 1 on a full write.
long __kml_ipc_send(long fd, const char *s) {
    long n = (long)strlen(s);
    char *buf = malloc(n * 6 + 4);
    long o = 0;
    buf[o++] = '"';
    for (long i = 0; i < n; i++) {
        unsigned char c = (unsigned char)s[i];
        switch (c) {
        case '"': buf[o++] = '\\'; buf[o++] = '"'; break;
        case '\\': buf[o++] = '\\'; buf[o++] = '\\'; break;
        case '\n': buf[o++] = '\\'; buf[o++] = 'n'; break;
        case '\r': buf[o++] = '\\'; buf[o++] = 'r'; break;
        case '\t': buf[o++] = '\\'; buf[o++] = 't'; break;
        default:
            if (c < 0x20) o += sprintf(buf + o, "\\u%04x", c);
            else buf[o++] = (char)c;
        }
    }
    buf[o++] = '"';
    buf[o++] = '\n';
    long w = 0;
    while (w < o) {
        long r = (long)write((int)fd, buf + w, o - w);
        if (r <= 0) break;
        w += r;
    }
    free(buf);
    return w == o ? 1 : 0;
}
