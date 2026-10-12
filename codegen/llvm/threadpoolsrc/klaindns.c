// klaindns.c — the dns module's natives (TDD-00236), in the pool's
// translation unit: getaddrinfo/getnameinfo (libuv's side of Node's
// cares_wrap), run on pool workers. Each hands its result to the loop as
// JSON in the item's string. The resolver (c-ares) is caressrc/klaincares.c.

// ---- dns: getaddrinfo / getnameinfo (Node's cares_wrap, libuv side) ------------
// A result crosses to the loop as JSON in the item's string (the callback
// reads it through nativeLastString()); the status is libuv's: 0, or a
// UV_EAI_* / -errno number.
typedef struct { char *s; size_t n, cap; } kml_jbuf;

static void jb_put(kml_jbuf *b, const char *p, size_t n) {
    if (b->n + n + 1 > b->cap) {
        size_t cap = b->cap ? b->cap * 2 : 128;
        while (cap < b->n + n + 1) cap *= 2;
        b->s = (char *)realloc(b->s, cap);
        b->cap = cap;
    }
    memcpy(b->s + b->n, p, n);
    b->n += n;
    b->s[b->n] = 0;
}
static void jb_cstr(kml_jbuf *b, const char *p) { jb_put(b, p, strlen(p)); }
// A JSON string literal of the n bytes at p (Latin-1 bytes as their code
// points, as V8's OneByteString makes them).
static void jb_json(kml_jbuf *b, const char *p, size_t n) {
    jb_put(b, "\"", 1);
    for (size_t i = 0; i < n; i++) {
        unsigned char c = (unsigned char)p[i];
        char esc[8];
        if (c == '"' || c == '\\') { esc[0] = '\\'; esc[1] = (char)c; jb_put(b, esc, 2); }
        else if (c < 0x20 || c >= 0x7f) { snprintf(esc, sizeof esc, "\\u%04x", c); jb_put(b, esc, 6); }
        else jb_put(b, (const char *)&c, 1);
    }
    jb_put(b, "\"", 1);
}
static void jb_int(kml_jbuf *b, long long v) {
    char t[32];
    snprintf(t, sizeof t, "%lld", v);
    jb_cstr(b, t);
}

#ifdef _WIN32
#define KML_NI_NAMEREQD 0x04
#define KML_EAI_FAIL 11003
#define KML_EAI_FAMILY 10047
#define KML_EAI_MEMORY 8
#define KML_EAI_SERVICE 10109
#define KML_EAI_SOCKTYPE 10044
#define KML_EAI_BADFLAGS 10022
#define KML_AI_ADDRCONFIG 0x400
#define KML_AI_ALL 0x100
#define KML_AI_V4MAPPED 0x800
#else
#define KML_NI_NAMEREQD NI_NAMEREQD
#define KML_EAI_FAIL EAI_FAIL
#define KML_EAI_FAMILY EAI_FAMILY
#define KML_EAI_MEMORY EAI_MEMORY
#define KML_EAI_SERVICE EAI_SERVICE
#define KML_EAI_SOCKTYPE EAI_SOCKTYPE
#define KML_EAI_BADFLAGS EAI_BADFLAGS
#define KML_AI_ADDRCONFIG AI_ADDRCONFIG
#define KML_AI_ALL AI_ALL
#define KML_AI_V4MAPPED AI_V4MAPPED
#endif

// libuv's uv__getaddrinfo_translate_error.
static int64_t uv_eai(int rc) {
    if (rc == EAI_AGAIN) return -3001;
    if (rc == KML_EAI_BADFLAGS) return -3002;
    if (rc == KML_EAI_FAIL) return -3004;
    if (rc == KML_EAI_FAMILY) return -3005;
    if (rc == KML_EAI_MEMORY) return -3006;
#if defined(EAI_NODATA) && (!defined(EAI_NONAME) || EAI_NODATA != EAI_NONAME)
    if (rc == EAI_NODATA) return -3007;
#endif
    if (rc == EAI_NONAME) return -3008;
#ifdef EAI_OVERFLOW
    if (rc == EAI_OVERFLOW) return -3009;
#endif
    if (rc == KML_EAI_SERVICE) return -3010;
    if (rc == KML_EAI_SOCKTYPE) return -3011;
#ifdef EAI_ADDRFAMILY
    if (rc == EAI_ADDRFAMILY) return -3000;
#endif
#ifdef EAI_BADHINTS
    if (rc == EAI_BADHINTS) return -3013;
#endif
#ifdef EAI_PROTOCOL
    if (rc == EAI_PROTOCOL) return -3014;
#endif
#ifdef EAI_SYSTEM
    if (rc == EAI_SYSTEM) return -(int64_t)errno;
#endif
    return -3004;
}

// a[0] family (0, 4, 6), a[1] getaddrinfo flags, a[2] order (0 verbatim,
// 1 IPv4 first, 2 IPv6 first). The result: the addresses, a JSON array.
static void work_getaddrinfo(kml_pool_item *it) {
    struct addrinfo hints, *res = NULL;
    memset(&hints, 0, sizeof hints);
    hints.ai_family = it->a[0] == 4 ? AF_INET : it->a[0] == 6 ? AF_INET6 : 0;
    hints.ai_socktype = SOCK_STREAM;
    hints.ai_flags = (int)it->a[1];
    int rc = getaddrinfo(it->arg0, NULL, &hints, &res);
    if (rc != 0) {
        it->err = uv_eai(rc);
        return;
    }
    kml_jbuf b = {0};
    jb_cstr(&b, "[");
    int n = 0;
    for (int pass = 0; pass < 2; pass++) {
        int want4 = it->a[2] == 2 ? pass == 1 : (it->a[2] == 1 ? pass == 0 : 1);
        int want6 = it->a[2] == 1 ? pass == 1 : (it->a[2] == 2 ? pass == 0 : 1);
        if (it->a[2] == 0 && pass == 1) break;
        for (struct addrinfo *p = res; p; p = p->ai_next) {
            char ip[64];
            const void *addr;
            if (want4 && p->ai_family == AF_INET) addr = &((struct sockaddr_in *)p->ai_addr)->sin_addr;
            else if (want6 && p->ai_family == AF_INET6) addr = &((struct sockaddr_in6 *)p->ai_addr)->sin6_addr;
            else continue;
            if (!inet_ntop(p->ai_family, addr, ip, sizeof ip)) continue;
            if (n++) jb_cstr(&b, ",");
            jb_json(&b, ip, strlen(ip));
        }
    }
    jb_cstr(&b, "]");
    freeaddrinfo(res);
    if (n == 0) {
        free(b.s);
        it->err = -3007; // UV_EAI_NODATA
        return;
    }
    it->res_str = b.s;
}

void __kml_native_dns_getaddrinfo(const char *host, double family, double flags, double order, void *inv, void *clo) {
    kml_pool_item *it = native_item(work_getaddrinfo, inv, clo);
    it->arg0 = strdup(host ? host : "");
    it->a[0] = (int64_t)family;
    it->a[1] = (int64_t)flags;
    it->a[2] = (int64_t)order;
    native_submit(it);
}

// getnameinfo(address, port) with NI_NAMEREQD, as libuv's caller asks: the
// result is [hostname, service].
static void work_getnameinfo(kml_pool_item *it) {
    struct sockaddr_storage ss;
    memset(&ss, 0, sizeof ss);
    int len;
    struct sockaddr_in *s4 = (struct sockaddr_in *)&ss;
    struct sockaddr_in6 *s6 = (struct sockaddr_in6 *)&ss;
    if (inet_pton(AF_INET, it->arg0, &s4->sin_addr) == 1) {
        s4->sin_family = AF_INET;
        s4->sin_port = htons((unsigned short)it->a[0]);
        len = sizeof *s4;
    } else if (inet_pton(AF_INET6, it->arg0, &s6->sin6_addr) == 1) {
        s6->sin6_family = AF_INET6;
        s6->sin6_port = htons((unsigned short)it->a[0]);
        len = sizeof *s6;
    } else {
        it->err = -EINVAL;
        return;
    }
    char host[1025], serv[32];
    int rc = getnameinfo((struct sockaddr *)&ss, len, host, sizeof host, serv, sizeof serv, KML_NI_NAMEREQD);
    if (rc != 0) {
        it->err = uv_eai(rc);
        return;
    }
    kml_jbuf b = {0};
    jb_cstr(&b, "[");
    jb_json(&b, host, strlen(host));
    jb_cstr(&b, ",");
    jb_json(&b, serv, strlen(serv));
    jb_cstr(&b, "]");
    it->res_str = b.s;
}

void __kml_native_dns_getnameinfo(const char *address, double port, void *inv, void *clo) {
    kml_pool_item *it = native_item(work_getnameinfo, inv, clo);
    it->arg0 = strdup(address ? address : "");
    it->a[0] = (int64_t)port;
    native_submit(it);
}

// The host's getaddrinfo flags: 0 AI_ADDRCONFIG, 1 AI_ALL, 2 AI_V4MAPPED.
double __kml_native_dns_ai_flag(double which) {
    switch ((int)which) {
    case 0: return KML_AI_ADDRCONFIG;
    case 1: return KML_AI_ALL;
    }
    return KML_AI_V4MAPPED;
}

