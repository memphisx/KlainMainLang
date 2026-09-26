// ---- HTTP/1 parser (Node's HTTPParser over llhttp) -----------------------------
// An incremental request/response parser with llhttp's strict-mode rules and
// error codes. execute() runs until the next event and reports it, with the
// bytes it consumed; the caller reads the message's fields through the
// getters and executes again from where it stopped:
//   0  the input is consumed; more is needed
//   1  headers complete (method/url or status, version, headers, flags)
//   2  a body chunk: [bodyOffset, bodyOffset + bodyLength) of the input
//   3  message complete (the header list now holds the trailers)
//   4  upgrade: the rest of the input is another protocol's
//  -1  a parse error: errorCode()/errorReason(), at consumed()
// Header strings are Latin-1, as Node decodes them.

typedef struct {
    char *s;
    int64_t n, cap;
} kml_hbuf;

static void hbuf_put(kml_hbuf *b, const char *p, int64_t n) {
    if (b->n + n + 1 > b->cap) {
        int64_t nc = b->cap ? b->cap * 2 : 64;
        while (nc < b->n + n + 1) nc *= 2;
        b->s = (char *)realloc(b->s, (size_t)nc);
        b->cap = nc;
    }
    memcpy(b->s + b->n, p, (size_t)n);
    b->n += n;
    b->s[b->n] = 0;
}

static void hbuf_ch(kml_hbuf *b, char c) { hbuf_put(b, &c, 1); }

enum {
    HP_START, HP_METHOD, HP_URL_START, HP_URL, HP_REQ_PROTO, HP_VER_MAJOR, HP_VER_DOT, HP_VER_MINOR,
    HP_LINE_CR, HP_LINE_LF,
    HP_RES_PROTO, HP_RES_VER_MAJOR, HP_RES_VER_DOT, HP_RES_VER_MINOR, HP_RES_SP, HP_STATUS,
    HP_STATUS_SP, HP_STATUS_MSG, HP_STATUS_LF,
    HP_HDR_START, HP_HDR_FIELD, HP_HDR_VALUE_WS, HP_HDR_VALUE, HP_HDR_VALUE_LF, HP_HDR_NEXT,
    HP_HDRS_LF, HP_HDRS_DONE,
    HP_BODY_IDENTITY, HP_BODY_EOF,
    HP_CHUNK_SIZE_START, HP_CHUNK_SIZE, HP_CHUNK_EXT, HP_CHUNK_SIZE_LF, HP_CHUNK_DATA,
    HP_CHUNK_DATA_CR, HP_CHUNK_DATA_LF,
    HP_MSG_DONE, HP_UPGRADED, HP_DEAD,
};

typedef struct kml_hp {
    int type; // 0 request, 1 response
    int state;
    int64_t max_header, header_nread;
    kml_hbuf method, url, status_msg, field, value;
    char **hdrs;       // flat [name, value, …]
    int64_t *hdr_len;
    int nh, hcap;
    int status, major, minor;
    int chunked, has_cl, has_te, te_chunked, conn_close, conn_keepalive, conn_upgrade, has_upgrade;
    int upgrade, keepalive, skip_body, in_trailer, cl_overflow;
    uint64_t cl, remaining;
    int64_t consumed, body_off, body_len;
    const char *err_code, *err_reason;
} kml_hp;

static kml_hp **hp_tab = NULL;
static int hp_cap = 0, hp_n = 0;

static kml_hp *hp_get(double id) {
    int i = (int)id;
    return (i >= 0 && i < hp_n) ? hp_tab[i] : NULL;
}

static void hp_clear_headers(kml_hp *p) {
    for (int i = 0; i < p->nh; i++) free(p->hdrs[i]);
    p->nh = 0;
}

static void hp_reset_message(kml_hp *p) {
    p->method.n = p->url.n = p->status_msg.n = p->field.n = p->value.n = 0;
    hp_clear_headers(p);
    p->status = p->major = p->minor = 0;
    p->chunked = p->has_cl = p->has_te = p->te_chunked = p->conn_close = p->conn_keepalive = 0;
    p->conn_upgrade = p->has_upgrade = p->upgrade = p->keepalive = p->skip_body = p->in_trailer = 0;
    p->cl_overflow = 0;
    p->cl = p->remaining = 0;
    p->header_nread = 0;
    p->state = HP_START;
}

double __kml_native_http_parser_new(double type, double maxHeader) {
    if (hp_n == hp_cap) {
        int nc = hp_cap ? hp_cap * 2 : 16;
        kml_hp **nt = (kml_hp **)calloc((size_t)nc, sizeof *nt);
        if (hp_tab) memcpy(nt, hp_tab, (size_t)hp_cap * sizeof *nt);
        hp_tab = nt;
        hp_cap = nc;
    }
    kml_hp *p = (kml_hp *)calloc(1, sizeof *p);
    p->type = (int)type;
    p->max_header = maxHeader > 0 ? (int64_t)maxHeader : 16384;
    hp_reset_message(p);
    hp_tab[hp_n] = p;
    return hp_n++;
}

// Reinitialize for another message stream (a parser reused from a pool).
void __kml_native_http_parser_init(double id, double type, double maxHeader) {
    kml_hp *p = hp_get(id);
    if (!p) return;
    p->type = (int)type;
    p->max_header = maxHeader > 0 ? (int64_t)maxHeader : 16384;
    p->err_code = p->err_reason = NULL;
    hp_reset_message(p);
}

static const char *hp_methods[] = {
    "ACL", "BIND", "CHECKOUT", "CONNECT", "COPY", "DELETE", "GET", "HEAD", "LINK", "LOCK", "M-SEARCH",
    "MERGE", "MKACTIVITY", "MKCALENDAR", "MKCOL", "MOVE", "NOTIFY", "OPTIONS", "PATCH", "POST",
    "PROPFIND", "PROPPATCH", "PURGE", "PUT", "QUERY", "REBIND", "REPORT", "SEARCH", "SOURCE",
    "SUBSCRIBE", "TRACE", "UNBIND", "UNLINK", "UNLOCK", "UNSUBSCRIBE",
};

// Whether some method starts with the n bytes of m.
static int hp_method_prefix(const char *m, int64_t n, int *exact) {
    int any = 0;
    *exact = 0;
    for (size_t i = 0; i < sizeof hp_methods / sizeof hp_methods[0]; i++) {
        if ((int64_t)strlen(hp_methods[i]) >= n && memcmp(hp_methods[i], m, (size_t)n) == 0) {
            any = 1;
            if ((int64_t)strlen(hp_methods[i]) == n) *exact = 1;
        }
    }
    return any;
}

static int hp_tchar(unsigned char c) {
    if (c >= '0' && c <= '9') return 1;
    if ((c | 0x20) >= 'a' && (c | 0x20) <= 'z') return 1;
    return c && strchr("!#$%&'*+-.^_`|~", c) != NULL;
}

static int hp_ieq(const char *a, int64_t n, const char *b) {
    if ((int64_t)strlen(b) != n) return 0;
    for (int64_t i = 0; i < n; i++)
        if ((a[i] | 0x20) != (b[i] | 0x20)) return 0;
    return 1;
}

// Each comma-separated token of a Connection or Transfer-Encoding value.
static void hp_tokens(const char *v, int64_t n, void (*fn)(kml_hp *, const char *, int64_t, int), kml_hp *p) {
    int64_t i = 0;
    while (i <= n) {
        int64_t s = i;
        while (i < n && v[i] != ',') i++;
        int64_t e = i;
        while (s < e && (v[s] == ' ' || v[s] == '\t')) s++;
        while (e > s && (v[e - 1] == ' ' || v[e - 1] == '\t')) e--;
        fn(p, v + s, e - s, i >= n);
        i++;
    }
}

static void hp_conn_token(kml_hp *p, const char *t, int64_t n, int last) {
    (void)last;
    if (hp_ieq(t, n, "close")) p->conn_close = 1;
    else if (hp_ieq(t, n, "keep-alive")) p->conn_keepalive = 1;
    else if (hp_ieq(t, n, "upgrade")) p->conn_upgrade = 1;
}

static void hp_te_token(kml_hp *p, const char *t, int64_t n, int last) {
    if (last) p->te_chunked = hp_ieq(t, n, "chunked");
    else if (hp_ieq(t, n, "chunked")) p->te_chunked = 0;
}

#define HP_FAIL(code, reason) do { p->err_code = (code); p->err_reason = (reason); p->state = HP_DEAD; p->consumed = i; return -1; } while (0)

// A completed header line: store it and note the fields that frame the body.
static int hp_header_done(kml_hp *p, int64_t i) {
    int64_t vn = p->value.n;
    while (vn > 0 && (p->value.s[vn - 1] == ' ' || p->value.s[vn - 1] == '\t')) vn--;
    p->value.n = vn;
    if (p->value.s) p->value.s[vn] = 0;
    const char *f = p->field.s ? p->field.s : "";
    int64_t fn = p->field.n;
    const char *v = p->value.s ? p->value.s : "";
    if (!p->in_trailer) {
        if (hp_ieq(f, fn, "content-length")) {
            if (p->has_cl) HP_FAIL("HPE_UNEXPECTED_CONTENT_LENGTH", "Duplicate Content-Length");
            if (p->has_te) HP_FAIL("HPE_UNEXPECTED_CONTENT_LENGTH", "Content-Length can't be present with Transfer-Encoding");
            if (vn == 0) HP_FAIL("HPE_INVALID_CONTENT_LENGTH", "Invalid character in Content-Length");
            uint64_t cl = 0;
            for (int64_t k = 0; k < vn; k++) {
                if (v[k] < '0' || v[k] > '9') HP_FAIL("HPE_INVALID_CONTENT_LENGTH", "Invalid character in Content-Length");
                if (cl > (UINT64_MAX - 9) / 10) HP_FAIL("HPE_INVALID_CONTENT_LENGTH", "Content-Length overflow");
                cl = cl * 10 + (uint64_t)(v[k] - '0');
            }
            p->has_cl = 1;
            p->cl = cl;
        } else if (hp_ieq(f, fn, "transfer-encoding")) {
            if (p->has_cl) HP_FAIL("HPE_INVALID_TRANSFER_ENCODING", "Transfer-Encoding can't be present with Content-Length");
            p->has_te = 1;
            hp_tokens(v, vn, hp_te_token, p);
        } else if (hp_ieq(f, fn, "connection")) {
            hp_tokens(v, vn, hp_conn_token, p);
        } else if (hp_ieq(f, fn, "upgrade")) {
            p->has_upgrade = 1;
        }
    }
    if (p->nh + 2 > p->hcap) {
        int nc = p->hcap ? p->hcap * 2 : 32;
        p->hdrs = (char **)realloc(p->hdrs, (size_t)nc * sizeof *p->hdrs);
        p->hdr_len = (int64_t *)realloc(p->hdr_len, (size_t)nc * sizeof *p->hdr_len);
        p->hcap = nc;
    }
    p->hdrs[p->nh] = (char *)malloc((size_t)fn + 1);
    memcpy(p->hdrs[p->nh], f, (size_t)fn);
    p->hdrs[p->nh][fn] = 0;
    p->hdr_len[p->nh++] = fn;
    p->hdrs[p->nh] = (char *)malloc((size_t)vn + 1);
    memcpy(p->hdrs[p->nh], v, (size_t)vn);
    p->hdrs[p->nh][vn] = 0;
    p->hdr_len[p->nh++] = vn;
    p->field.n = p->value.n = 0;
    return 0;
}

// Whether the message is kept alive after it, llhttp_should_keep_alive's rule.
static int hp_keepalive(kml_hp *p) {
    int ka;
    if (p->major > 0 && p->minor > 0) ka = !p->conn_close;
    else ka = p->conn_keepalive;
    if (!ka) return 0;
    // A response read until EOF cannot be followed by another.
    if (p->type == 1 && !p->skip_body && !p->chunked && !p->has_cl &&
        !(p->status / 100 == 1 || p->status == 204 || p->status == 304))
        return 0;
    return 1;
}

// Headers are complete: decide how the body is framed.
static int hp_after_headers(kml_hp *p, int64_t i) {
    p->chunked = p->has_te && p->te_chunked;
    if (p->type == 0 && p->has_te && !p->te_chunked)
        HP_FAIL("HPE_INVALID_TRANSFER_ENCODING", "Request has invalid `Transfer-Encoding`");
    if (p->type == 0) {
        int is_connect = p->method.n == 7 && memcmp(p->method.s, "CONNECT", 7) == 0;
        p->upgrade = is_connect || (p->has_upgrade && p->conn_upgrade);
    } else {
        p->upgrade = p->status == 101;
    }
    p->keepalive = hp_keepalive(p);
    p->state = HP_HDRS_DONE;
    return 0;
}

// Where the body starts, once the caller has seen the headers (and may have
// asked to skip the body of a response to HEAD).
static void hp_begin_body(kml_hp *p) {
    int has_body = p->chunked || (p->has_cl && p->cl > 0);
    int is_connect = p->type == 0 && p->method.n == 7 && memcmp(p->method.s, "CONNECT", 7) == 0;
    if (p->upgrade && (is_connect || p->skip_body || !has_body || (p->type == 1 && p->status == 101))) {
        p->state = HP_MSG_DONE;
        return;
    }
    p->upgrade = 0;
    if (p->skip_body || (p->type == 1 && (p->status / 100 == 1 || p->status == 204 || p->status == 304))) {
        p->state = HP_MSG_DONE;
    } else if (p->chunked) {
        p->state = HP_CHUNK_SIZE_START;
    } else if (p->has_cl) {
        p->remaining = p->cl;
        p->state = p->cl > 0 ? HP_BODY_IDENTITY : HP_MSG_DONE;
    } else if (p->type == 0) {
        p->state = HP_MSG_DONE;
    } else {
        p->state = HP_BODY_EOF;
    }
}

static int hp_header_bytes(kml_hp *p, int64_t n, int64_t i) {
    p->header_nread += n;
    if (p->header_nread > p->max_header) HP_FAIL("HPE_HEADER_OVERFLOW", "Header overflow");
    return 0;
}

double __kml_native_http_parser_execute(double id, const unsigned char *d, int64_t dlen, double offset, double length) {
    kml_hp *p = hp_get(id);
    if (!p) return -1;
    int64_t start = (int64_t)offset;
    int64_t end = start + (int64_t)length;
    if (start < 0) start = 0;
    if (end > dlen) end = dlen;
    int64_t i = start;
    p->consumed = 0;
    p->body_off = p->body_len = 0;
    if (p->state == HP_DEAD) { p->consumed = 0; return -1; }
    if (p->state == HP_UPGRADED) { p->consumed = 0; return 4; }
    // Events that need no input.
    if (p->state == HP_HDRS_DONE) hp_begin_body(p);
    if (p->state == HP_MSG_DONE) {
        int up = p->upgrade;
        hp_reset_message(p);
        if (up) p->state = HP_UPGRADED;
        p->consumed = 0;
        return 3;
    }
    while (i < end) {
        unsigned char c = d[i];
        switch (p->state) {
        case HP_START:
            if (c == '\r' || c == '\n') { i++; break; }
            p->state = p->type == 0 ? HP_METHOD : HP_RES_PROTO;
            break;
        case HP_METHOD: {
            if (c == ' ') {
                int exact;
                hp_method_prefix(p->method.s ? p->method.s : "", p->method.n, &exact);
                if (!exact) HP_FAIL("HPE_INVALID_METHOD", "Invalid method encountered");
                p->state = HP_URL_START;
                i++;
                break;
            }
            hbuf_ch(&p->method, (char)c);
            int exact;
            if (!hp_method_prefix(p->method.s, p->method.n, &exact)) HP_FAIL("HPE_INVALID_METHOD", "Invalid method encountered");
            if (hp_header_bytes(p, 1, i)) return -1;
            i++;
            break;
        }
        case HP_URL_START:
            if (c == ' ') { i++; break; }
            p->state = HP_URL;
            break;
        case HP_URL:
            if (c == ' ') { p->state = HP_REQ_PROTO; p->field.n = 0; i++; break; }
            if (c < 0x21 || c == 0x7f || c >= 0x80) HP_FAIL("HPE_INVALID_URL", "Invalid char in url path");
            hbuf_ch(&p->url, (char)c);
            if (hp_header_bytes(p, 1, i)) return -1;
            i++;
            break;
        case HP_REQ_PROTO:
        case HP_RES_PROTO: {
            // "HTTP/" (field holds what matched so far)
            const char *want = "HTTP/";
            if (c != (unsigned char)want[p->field.n]) {
                if (p->type == 1 && p->field.n == 0) HP_FAIL("HPE_INVALID_CONSTANT", "Expected HTTP/");
                HP_FAIL("HPE_INVALID_CONSTANT", "Expected HTTP/, RTSP/ or ICE/");
            }
            hbuf_ch(&p->field, (char)c);
            i++;
            if (p->field.n == 5) {
                p->field.n = 0;
                p->state = p->state == HP_REQ_PROTO ? HP_VER_MAJOR : HP_RES_VER_MAJOR;
            }
            break;
        }
        case HP_VER_MAJOR:
        case HP_RES_VER_MAJOR:
            if (c < '0' || c > '9') HP_FAIL("HPE_INVALID_VERSION", "Invalid major version");
            p->major = c - '0';
            p->state = p->state == HP_VER_MAJOR ? HP_VER_DOT : HP_RES_VER_DOT;
            i++;
            break;
        case HP_VER_DOT:
        case HP_RES_VER_DOT:
            if (c != '.') HP_FAIL("HPE_INVALID_VERSION", "Expected dot");
            p->state = p->state == HP_VER_DOT ? HP_VER_MINOR : HP_RES_VER_MINOR;
            i++;
            break;
        case HP_VER_MINOR:
        case HP_RES_VER_MINOR: {
            if (c < '0' || c > '9') HP_FAIL("HPE_INVALID_VERSION", "Invalid minor version");
            p->minor = c - '0';
            int ok = (p->major == 1 && (p->minor == 0 || p->minor == 1)) || (p->major == 0 && p->minor == 9) ||
                     (p->major == 2 && p->minor == 0);
            if (!ok) HP_FAIL("HPE_INVALID_VERSION", "Invalid HTTP version");
            p->state = p->state == HP_VER_MINOR ? HP_LINE_CR : HP_RES_SP;
            i++;
            break;
        }
        case HP_LINE_CR:
            if (c != '\r') HP_FAIL("HPE_INVALID_VERSION", "Expected CRLF after version");
            p->state = HP_LINE_LF;
            i++;
            break;
        case HP_LINE_LF:
            if (c != '\n') HP_FAIL("HPE_INVALID_VERSION", "Expected CRLF after version");
            p->state = HP_HDR_START;
            i++;
            break;
        case HP_RES_SP:
            if (c != ' ') HP_FAIL("HPE_INVALID_VERSION", "Expected space after version");
            p->state = HP_STATUS;
            i++;
            break;
        case HP_STATUS:
            if (c >= '0' && c <= '9' && p->field.n < 3) {
                p->status = p->status * 10 + (c - '0');
                hbuf_ch(&p->field, (char)c);
                i++;
                break;
            }
            if (p->field.n != 3) HP_FAIL("HPE_INVALID_STATUS", "Invalid status code");
            p->field.n = 0;
            if (c == ' ') { p->state = HP_STATUS_MSG; i++; break; }
            if (c == '\r') { p->state = HP_STATUS_LF; i++; break; }
            HP_FAIL("HPE_INVALID_STATUS", "Invalid response status");
        case HP_STATUS_MSG:
            if (c == '\r') { p->state = HP_STATUS_LF; i++; break; }
            if (c == '\n') HP_FAIL("HPE_CR_EXPECTED", "Missing expected CR after response line");
            hbuf_ch(&p->status_msg, (char)c);
            if (hp_header_bytes(p, 1, i)) return -1;
            i++;
            break;
        case HP_STATUS_LF:
            if (c != '\n') HP_FAIL("HPE_STRICT", "Expected LF after CR");
            p->state = HP_HDR_START;
            i++;
            break;
        case HP_HDR_START:
            if (c == '\r') { p->state = HP_HDRS_LF; i++; break; }
            if (c == '\n') HP_FAIL("HPE_CR_EXPECTED", "Missing expected CR after header value");
            if (!hp_tchar(c)) HP_FAIL("HPE_INVALID_HEADER_TOKEN", "Invalid header token");
            p->state = HP_HDR_FIELD;
            break;
        case HP_HDR_FIELD:
            if (c == ':') {
                if (p->field.n == 0) HP_FAIL("HPE_INVALID_HEADER_TOKEN", "Invalid header token");
                p->state = HP_HDR_VALUE_WS;
                i++;
                if (hp_header_bytes(p, 1, i)) return -1;
                break;
            }
            if (!hp_tchar(c)) HP_FAIL("HPE_INVALID_HEADER_TOKEN", "Invalid header token");
            hbuf_ch(&p->field, (char)c);
            if (hp_header_bytes(p, 1, i)) return -1;
            i++;
            break;
        case HP_HDR_VALUE_WS:
            if (c == ' ' || c == '\t') { i++; if (hp_header_bytes(p, 1, i)) return -1; break; }
            p->state = HP_HDR_VALUE;
            break;
        case HP_HDR_VALUE: {
            // A run of value bytes at once.
            int64_t j = i;
            while (j < end) {
                unsigned char v = d[j];
                if (v == '\r' || v == '\n') break;
                if ((v < 0x20 && v != '\t') || v == 0x7f) {
                    i = j;
                    HP_FAIL("HPE_INVALID_HEADER_TOKEN", "Invalid header value char");
                }
                j++;
            }
            // Latin-1 → UTF-8, as Node's latin1 decoding.
            for (int64_t k = i; k < j; k++) {
                unsigned char v = d[k];
                if (v < 0x80) hbuf_ch(&p->value, (char)v);
                else { hbuf_ch(&p->value, (char)(0xC0 | (v >> 6))); hbuf_ch(&p->value, (char)(0x80 | (v & 0x3F))); }
            }
            if (hp_header_bytes(p, j - i, j)) return -1;
            i = j;
            if (i < end) {
                if (d[i] == '\n') HP_FAIL("HPE_CR_EXPECTED", "Missing expected CR after header value");
                p->state = HP_HDR_VALUE_LF;
                i++;
            }
            break;
        }
        case HP_HDR_VALUE_LF:
            if (c != '\n') HP_FAIL("HPE_LF_EXPECTED", "Missing expected LF after header value");
            i++;
            p->state = HP_HDR_NEXT;
            break;
        case HP_HDR_NEXT:
            if (c == ' ' || c == '\t') HP_FAIL("HPE_INVALID_HEADER_TOKEN", "Unexpected whitespace after header value");
            if (hp_header_done(p, i)) return -1;
            p->state = HP_HDR_START;
            break;
        case HP_HDRS_LF:
            if (c != '\n') HP_FAIL("HPE_STRICT", "Expected LF after headers");
            i++;
            if (p->in_trailer) {
                // The trailers end the message.
                p->in_trailer = 0;
                p->consumed = i - start;
                int up = p->upgrade;
                // Keep the trailer list for the caller: reset only the framing.
                p->state = HP_START;
                p->method.n = p->url.n = p->status_msg.n = 0;
                p->chunked = p->has_cl = p->has_te = p->te_chunked = 0;
                p->conn_close = p->conn_keepalive = p->conn_upgrade = p->has_upgrade = 0;
                p->header_nread = 0;
                if (up) p->state = HP_UPGRADED;
                return 3;
            }
            if (hp_after_headers(p, i)) return -1;
            p->consumed = i - start;
            return 1;
        case HP_HDRS_DONE:
            hp_begin_body(p);
            if (p->state == HP_MSG_DONE) {
                p->consumed = i - start;
                int up = p->upgrade;
                hp_reset_message(p);
                if (up) p->state = HP_UPGRADED;
                return 3;
            }
            break;
        case HP_MSG_DONE: {
            p->consumed = i - start;
            int up = p->upgrade;
            hp_reset_message(p);
            if (up) p->state = HP_UPGRADED;
            return 3;
        }
        case HP_UPGRADED:
            p->consumed = i - start;
            return 4;
        case HP_BODY_IDENTITY: {
            int64_t n = end - i;
            if ((uint64_t)n > p->remaining) n = (int64_t)p->remaining;
            p->remaining -= (uint64_t)n;
            p->body_off = i;
            p->body_len = n;
            i += n;
            if (p->remaining == 0) p->state = HP_MSG_DONE;
            p->consumed = i - start;
            return 2;
        }
        case HP_BODY_EOF:
            p->body_off = i;
            p->body_len = end - i;
            i = end;
            p->consumed = i - start;
            return 2;
        case HP_CHUNK_SIZE_START:
        case HP_CHUNK_SIZE: {
            int h = -1;
            if (c >= '0' && c <= '9') h = c - '0';
            else if ((c | 0x20) >= 'a' && (c | 0x20) <= 'f') h = (c | 0x20) - 'a' + 10;
            if (h >= 0) {
                if (p->remaining > (UINT64_MAX >> 4)) HP_FAIL("HPE_INVALID_CHUNK_SIZE", "Chunk size overflow");
                p->remaining = p->remaining * 16 + (uint64_t)h;
                p->state = HP_CHUNK_SIZE;
                i++;
                break;
            }
            if (p->state == HP_CHUNK_SIZE_START) HP_FAIL("HPE_INVALID_CHUNK_SIZE", "Invalid character in chunk size");
            if (c == ';' || c == ' ' || c == '\t') { p->state = HP_CHUNK_EXT; i++; break; }
            if (c == '\r') { p->state = HP_CHUNK_SIZE_LF; i++; break; }
            HP_FAIL("HPE_INVALID_CHUNK_SIZE", "Invalid character in chunk size");
        }
        case HP_CHUNK_EXT:
            if (c == '\r') { p->state = HP_CHUNK_SIZE_LF; i++; break; }
            if ((c < 0x20 && c != '\t') || c == 0x7f) HP_FAIL("HPE_STRICT", "Invalid character in chunk extensions");
            i++;
            break;
        case HP_CHUNK_SIZE_LF:
            if (c != '\n') HP_FAIL("HPE_STRICT", "Expected LF after chunk size");
            i++;
            if (p->remaining == 0) {
                // The last chunk: trailers follow.
                hp_clear_headers(p);
                p->in_trailer = 1;
                p->header_nread = 0;
                p->state = HP_HDR_START;
            } else {
                p->state = HP_CHUNK_DATA;
            }
            break;
        case HP_CHUNK_DATA: {
            int64_t n = end - i;
            if ((uint64_t)n > p->remaining) n = (int64_t)p->remaining;
            p->remaining -= (uint64_t)n;
            p->body_off = i;
            p->body_len = n;
            i += n;
            if (p->remaining == 0) p->state = HP_CHUNK_DATA_CR;
            p->consumed = i - start;
            return 2;
        }
        case HP_CHUNK_DATA_CR:
            if (c != '\r') HP_FAIL("HPE_STRICT", "Expected LF after chunk data");
            p->state = HP_CHUNK_DATA_LF;
            i++;
            break;
        case HP_CHUNK_DATA_LF:
            if (c != '\n') HP_FAIL("HPE_STRICT", "Expected LF after chunk data");
            p->state = HP_CHUNK_SIZE_START;
            i++;
            break;
        case HP_DEAD:
            p->consumed = i - start;
            return -1;
        }
    }
    p->consumed = i - start;
    // A message whose last byte was just consumed.
    if (p->state == HP_MSG_DONE) {
        int up = p->upgrade;
        hp_reset_message(p);
        if (up) p->state = HP_UPGRADED;
        return 3;
    }
    return 0;
}

// The end of the input: a body read to EOF completes (3); a message cut
// short is an error (-1, HPE_INVALID_EOF_STATE); otherwise 0.
double __kml_native_http_parser_finish(double id) {
    kml_hp *p = hp_get(id);
    if (!p) return 0;
    p->consumed = 0;
    if (p->state == HP_BODY_EOF) {
        hp_reset_message(p);
        return 3;
    }
    if (p->state == HP_START || p->state == HP_UPGRADED || p->state == HP_DEAD) return 0;
    p->err_code = "HPE_INVALID_EOF_STATE";
    p->err_reason = "";
    p->state = HP_DEAD;
    return -1;
}

// 0 major, 1 minor, 2 status, 3 upgrade, 4 keep-alive, 5 header list length,
// 6 consumed, 7 body offset, 8 body length.
double __kml_native_http_parser_info(double id, double which) {
    kml_hp *p = hp_get(id);
    if (!p) return 0;
    switch ((int)which) {
    case 0: return p->major;
    case 1: return p->minor;
    case 2: return p->status;
    case 3: return p->upgrade;
    case 4: return p->keepalive;
    case 5: return p->nh;
    case 6: return (double)p->consumed;
    case 7: return (double)p->body_off;
    case 8: return (double)p->body_len;
    }
    return 0;
}

// After headers complete: 1 skips the body (a response to HEAD); 2 cancels
// an upgrade the program does not take (the message goes on as HTTP).
void __kml_native_http_parser_set(double id, double flag) {
    kml_hp *p = hp_get(id);
    if (!p) return;
    if ((int)flag == 1) p->skip_body = 1;
    if ((int)flag == 2) {
        p->upgrade = 0;
        if (p->state == HP_UPGRADED) p->state = HP_START;
    }
}

static char *hp_str(const char *s, int64_t n) {
    char *out = __kml_str_alloc(n + 1);
    if (n) memcpy(out, s, (size_t)n);
    out[n] = 0;
    __kml_str_finalize(out);
    return out;
}

// 0 method, 1 url, 2 status message, 3 error code, 4 error reason.
char *__kml_native_http_parser_string(double id, double which) {
    kml_hp *p = hp_get(id);
    if (!p) return hp_str("", 0);
    const char *s = "";
    int64_t n = -1;
    switch ((int)which) {
    case 0: s = p->method.s; n = p->method.n; break;
    case 1: s = p->url.s; n = p->url.n; break;
    case 2: s = p->status_msg.s; n = p->status_msg.n; break;
    case 3: s = p->err_code; break;
    case 4: s = p->err_reason; break;
    }
    if (!s) s = "";
    if (n < 0) n = (int64_t)strlen(s);
    return hp_str(s, n);
}

char *__kml_native_http_parser_header(double id, double index) {
    kml_hp *p = hp_get(id);
    int i = (int)index;
    if (!p || i < 0 || i >= p->nh) return hp_str("", 0);
    return hp_str(p->hdrs[i], p->hdr_len[i]);
}
