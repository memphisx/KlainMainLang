// klaindns.c — the dns module's natives (TDD-00236), in the pool's
// translation unit: getaddrinfo/getnameinfo (libuv's side of Node's
// cares_wrap) and a DNS client standing in for c-ares. Each runs on a pool
// worker and hands its result to the loop as JSON in the item's string.

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


// ---- the DNS client (c-ares' stand-in) ------------------------------------------
// A channel is a resolver's servers and retry policy, an id into a table.
// A query sends the question to each server in turn over UDP (TCP when the
// answer is truncated), for `tries` rounds with c-ares' timeout (doubling
// per round, capped by maxTimeout). The answer's records go to the loop as
// JSON: {"rcode":n,"an":[{…}, …]}. A failure is a c-ares status:
// 1 ENODATA … 12 ETIMEOUT (the TS side names them).

#ifndef _WIN32
#include <dlfcn.h>
#else
// kernel32, declared here: the pool's unit includes no Windows headers.
typedef void *kml_hmodule;
__declspec(dllimport) kml_hmodule __stdcall LoadLibraryA(const char *name);
__declspec(dllimport) void *__stdcall GetProcAddress(kml_hmodule module, const char *name);
__declspec(dllimport) int __stdcall FreeLibrary(kml_hmodule module);
#endif

enum {
    ARES_ENODATA = 1, ARES_EFORMERR = 2, ARES_ESERVFAIL = 3, ARES_ENOTFOUND = 4,
    ARES_ENOTIMP = 5, ARES_EREFUSED = 6, ARES_EBADQUERY = 7, ARES_EBADNAME = 8,
    ARES_EBADRESP = 10, ARES_ECONNREFUSED = 11, ARES_ETIMEOUT = 12, ARES_ENOMEM = 15,
    ARES_EBADSTR = 17, ARES_ECANCELLED = 24,
};

#define KML_DNS_MAXSERV 8
typedef struct {
    int family; // 4 or 6
    char addr[64];
    int port;
} kml_dns_server;

typedef struct {
    int used;
    int nserv;
    kml_dns_server serv[KML_DNS_MAXSERV];
    int timeout_ms; // -1: c-ares' default
    int tries;
    int max_timeout_ms; // 0: none
    int64_t generation; // bumped by cancel(): queries started before it end ECANCELLED
    char local4[64], local6[64]; // setLocalAddress's source addresses ("" for any)
} kml_dns_channel;

#define KML_DNS_MAXCHAN 256
static kml_dns_channel dns_chan[KML_DNS_MAXCHAN];
static pthread_mutex_t dns_lock = PTHREAD_MUTEX_INITIALIZER;

static void dns_add_server(kml_dns_server *s, int *n, const char *addr, int port) {
    if (*n >= KML_DNS_MAXSERV) return;
    char tmp[64];
    snprintf(tmp, sizeof tmp, "%s", addr);
    char *pct = strchr(tmp, '%'); // an IPv6 zone: c-ares reports the bare address
    if (pct) *pct = 0;
    unsigned char probe[16];
    int fam = inet_pton(AF_INET, tmp, probe) == 1 ? 4 : inet_pton(AF_INET6, tmp, probe) == 1 ? 6 : 0;
    if (!fam) return;
    for (int i = 0; i < *n; i++)
        if (strcmp(s[i].addr, tmp) == 0 && s[i].port == port) return;
    s[*n].family = fam;
    snprintf(s[*n].addr, sizeof s[*n].addr, "%s", tmp);
    s[*n].port = port;
    (*n)++;
}

// The system's servers, as c-ares reads them: the resolver configuration
// (libresolv's on macOS, /etc/resolv.conf elsewhere, the adapters' DNS
// servers on Windows); 127.0.0.1 when there are none.
static int dns_system_servers(kml_dns_server *s) {
    int n = 0;
#if defined(__APPLE__)
    // libSystem's DNS configuration (dnsinfo.h, 4-byte packed), as c-ares
    // reads it: the nameservers of every resolver that names no domain and
    // is not mDNS.
#pragma pack(push, 4)
    typedef struct {
        char *domain;
        int32_t n_nameserver;
        struct sockaddr **nameserver;
        uint16_t port;
        int32_t n_search;
        char **search;
        int32_t n_sortaddr;
        void **sortaddr;
        char *options;
    } kml_dns_resolver;
    typedef struct {
        int32_t n_resolver;
        kml_dns_resolver **resolver;
    } kml_dns_config;
#pragma pack(pop)
    kml_dns_config *(*dcopy)(void) = (kml_dns_config *(*)(void))dlsym(RTLD_DEFAULT, "dns_configuration_copy");
    void (*dfree)(kml_dns_config *) = (void (*)(kml_dns_config *))dlsym(RTLD_DEFAULT, "dns_configuration_free");
    if (dcopy) {
        kml_dns_config *cfg = dcopy();
        for (int32_t r = 0; cfg && cfg->resolver && r < cfg->n_resolver; r++) {
            kml_dns_resolver *rv = cfg->resolver[r];
            if (!rv || rv->domain) continue; // a domain: split DNS
            if (rv->options && strstr(rv->options, "mdns")) continue;
            for (int32_t k = 0; rv->nameserver && k < rv->n_nameserver; k++) {
                struct sockaddr *sa = rv->nameserver[k];
                char buf[64];
                int port = rv->port ? rv->port : 53;
                if (!sa) continue;
                if (sa->sa_family == AF_INET) {
                    struct sockaddr_in *s4 = (struct sockaddr_in *)sa;
                    inet_ntop(AF_INET, &s4->sin_addr, buf, sizeof buf);
                    if (s4->sin_port) port = ntohs(s4->sin_port);
                } else if (sa->sa_family == AF_INET6) {
                    struct sockaddr_in6 *s6 = (struct sockaddr_in6 *)sa;
                    inet_ntop(AF_INET6, &s6->sin6_addr, buf, sizeof buf);
                    if (s6->sin6_port) port = ntohs(s6->sin6_port);
                } else {
                    continue;
                }
                dns_add_server(s, &n, buf, port);
            }
        }
        if (cfg && dfree) dfree(cfg);
    }
    void *lib = n ? NULL : dlopen("/usr/lib/libresolv.9.dylib", RTLD_LAZY);
    if (lib) {
        int (*ninit)(void *) = (int (*)(void *))dlsym(lib, "res_9_ninit");
        int (*getservers)(void *, void *, int) = (int (*)(void *, void *, int))dlsym(lib, "res_9_getservers");
        void (*ndestroy)(void *) = (void (*)(void *))dlsym(lib, "res_9_ndestroy");
        if (ninit && getservers) {
            void *state = calloc(1, 65536); // an opaque res_state
            if (state && ninit(state) == 0) {
                unsigned char addrs[KML_DNS_MAXSERV][128]; // union res_sockaddr_union
                memset(addrs, 0, sizeof addrs);
                int cnt = getservers(state, addrs, KML_DNS_MAXSERV);
                for (int i = 0; i < cnt; i++) {
                    struct sockaddr *sa = (struct sockaddr *)addrs[i];
                    char buf[64];
                    int port = 53;
                    if (sa->sa_family == AF_INET) {
                        struct sockaddr_in *s4 = (struct sockaddr_in *)sa;
                        inet_ntop(AF_INET, &s4->sin_addr, buf, sizeof buf);
                        if (s4->sin_port) port = ntohs(s4->sin_port);
                    } else if (sa->sa_family == AF_INET6) {
                        struct sockaddr_in6 *s6 = (struct sockaddr_in6 *)sa;
                        inet_ntop(AF_INET6, &s6->sin6_addr, buf, sizeof buf);
                        if (s6->sin6_port) port = ntohs(s6->sin6_port);
                    } else {
                        continue;
                    }
                    dns_add_server(s, &n, buf, port);
                }
                if (ndestroy) ndestroy(state);
            }
            free(state);
        }
        dlclose(lib);
    }
#elif defined(_WIN32)
    typedef struct kml_sa_ptr { void *lpSockaddr; int iSockaddrLength; } kml_sa_ptr;
    typedef struct kml_dns_addr { unsigned long len, flags; struct kml_dns_addr *next; kml_sa_ptr addr; } kml_dns_addr;
    // IP_ADAPTER_ADDRESSES up to FirstDnsServerAddress and OperStatus.
    typedef struct kml_adapter {
        unsigned long long align;
        struct kml_adapter *next;
        char *name;
        void *uni, *any, *multi;
        kml_dns_addr *dns;
        void *suffix, *desc, *friendly;
        unsigned char phys[8];
        unsigned long physlen, flags, mtu, iftype;
        int oper;
    } kml_adapter;
    kml_hmodule h = LoadLibraryA("iphlpapi.dll");
    if (h) {
        typedef unsigned long (__stdcall *gaa_t)(unsigned long, unsigned long, void *, void *, unsigned long *);
        gaa_t gaa = (gaa_t)(void *)GetProcAddress(h, "GetAdaptersAddresses");
        unsigned long len = 16384;
        void *buf = malloc(len);
        // AF_UNSPEC; skip unicast/anycast/multicast addresses.
        if (gaa && buf && gaa(0, 0x7, NULL, buf, &len) == 0) {
            for (kml_adapter *a = (kml_adapter *)buf; a; a = a->next) {
                if (a->oper != 1) continue;
                for (kml_dns_addr *d = a->dns; d; d = d->next) {
                    char ip[64];
                    struct sockaddr *sa = (struct sockaddr *)d->addr.lpSockaddr;
                    if (!sa) continue;
                    if (sa->sa_family == AF_INET) inet_ntop(AF_INET, &((struct sockaddr_in *)sa)->sin_addr, ip, sizeof ip);
                    else if (sa->sa_family == AF_INET6) inet_ntop(AF_INET6, &((struct sockaddr_in6 *)sa)->sin6_addr, ip, sizeof ip);
                    else continue;
                    // Windows' site-local placeholders (fec0:0:0:ffff::1-3) are
                    // skipped, as c-ares skips them.
                    if (strncmp(ip, "fec0:0:0:ffff::", 15) == 0) continue;
                    dns_add_server(s, &n, ip, 53);
                }
            }
        }
        free(buf);
        FreeLibrary(h);
    }
#else
    FILE *f = fopen("/etc/resolv.conf", "r");
    if (f) {
        char line[512];
        while (fgets(line, sizeof line, f)) {
            char *p = line;
            while (*p == ' ' || *p == '\t') p++;
            if (strncmp(p, "nameserver", 10) != 0 || (p[10] != ' ' && p[10] != '\t')) continue;
            p += 10;
            while (*p == ' ' || *p == '\t') p++;
            p[strcspn(p, " \t\r\n#;")] = 0;
            dns_add_server(s, &n, p, 53);
        }
        fclose(f);
    }
#endif
    if (n == 0) dns_add_server(s, &n, "127.0.0.1", 53);
    return n;
}

// A new channel with the system's servers: its id, or -1 when the table is
// full.
double __kml_native_dns_channel_new(double timeout, double tries, double max_timeout) {
    pthread_mutex_lock(&dns_lock);
    int id = -1;
    for (int i = 0; i < KML_DNS_MAXCHAN; i++)
        if (!dns_chan[i].used) { id = i; break; }
    if (id >= 0) {
        kml_dns_channel *c = &dns_chan[id];
        memset(c, 0, sizeof *c);
        c->used = 1;
        c->timeout_ms = (int)timeout;
        c->tries = (int)tries > 0 ? (int)tries : 4;
        c->max_timeout_ms = (int)max_timeout;
        c->nserv = dns_system_servers(c->serv);
    }
    pthread_mutex_unlock(&dns_lock);
    return id;
}

// The channel's servers: [[address, port], …].
char *__kml_native_dns_channel_servers(double id) {
    kml_jbuf b = {0};
    jb_cstr(&b, "[");
    pthread_mutex_lock(&dns_lock);
    int i = (int)id;
    if (i >= 0 && i < KML_DNS_MAXCHAN && dns_chan[i].used) {
        for (int k = 0; k < dns_chan[i].nserv; k++) {
            if (k) jb_cstr(&b, ",");
            jb_cstr(&b, "[");
            jb_json(&b, dns_chan[i].serv[k].addr, strlen(dns_chan[i].serv[k].addr));
            jb_cstr(&b, ",");
            jb_int(&b, dns_chan[i].serv[k].port);
            jb_cstr(&b, "]");
        }
    }
    pthread_mutex_unlock(&dns_lock);
    jb_cstr(&b, "]");
    int64_t n = (int64_t)b.n;
    char *out = __kml_str_alloc(n + 1);
    memcpy(out, b.s, (size_t)n + 1);
    __kml_str_finalize(out);
    free(b.s);
    return out;
}

// Replaces the channel's servers with spec's lines ("address port"): 0, or
// ARES_EBADSTR for an address that is not an IP.
double __kml_native_dns_channel_set_servers(double id, const char *spec) {
    kml_dns_server s[KML_DNS_MAXSERV];
    int n = 0;
    const char *p = spec ? spec : "";
    while (*p) {
        char line[128];
        size_t len = strcspn(p, "\n");
        if (len >= sizeof line) return ARES_EBADSTR;
        memcpy(line, p, len);
        line[len] = 0;
        p += len;
        if (*p == '\n') p++;
        char addr[64];
        int port = 53;
        if (sscanf(line, "%63s %d", addr, &port) < 1) continue;
        int before = n;
        dns_add_server(s, &n, addr, port);
        if (n == before && n < KML_DNS_MAXSERV) {
            // A duplicate is dropped; anything else that was not added is bad.
            int dup = 0;
            for (int k = 0; k < n; k++) if (strcmp(s[k].addr, addr) == 0 && s[k].port == port) dup = 1;
            if (!dup) return ARES_EBADSTR;
        }
    }
    pthread_mutex_lock(&dns_lock);
    int i = (int)id;
    if (i >= 0 && i < KML_DNS_MAXCHAN && dns_chan[i].used) {
        memcpy(dns_chan[i].serv, s, sizeof s);
        dns_chan[i].nserv = n;
    }
    pthread_mutex_unlock(&dns_lock);
    return 0;
}

// setLocalAddress(first, second): the queries' source addresses, as
// cares_wrap sets them — the first an IPv4 or IPv6 address, the second (""
// for none) the other family. 0, or 1 an invalid address, 2 two IPv4
// addresses, 3 two IPv6 addresses.
double __kml_native_dns_channel_set_local(double id, const char *first, const char *second) {
    unsigned char probe[16];
    char v4[64] = "", v6[64] = "";
    int type0;
    if (inet_pton(AF_INET, first, probe) == 1) { snprintf(v4, sizeof v4, "%s", first); type0 = 4; }
    else if (inet_pton(AF_INET6, first, probe) == 1) { snprintf(v6, sizeof v6, "%s", first); type0 = 6; }
    else return 1;
    if (second && *second) {
        if (inet_pton(AF_INET, second, probe) == 1) {
            if (type0 == 4) return 2;
            snprintf(v4, sizeof v4, "%s", second);
        } else if (inet_pton(AF_INET6, second, probe) == 1) {
            if (type0 == 6) return 3;
            snprintf(v6, sizeof v6, "%s", second);
        } else {
            return 1;
        }
    }
    pthread_mutex_lock(&dns_lock);
    int i = (int)id;
    if (i >= 0 && i < KML_DNS_MAXCHAN && dns_chan[i].used) {
        memcpy(dns_chan[i].local4, v4, sizeof v4);
        memcpy(dns_chan[i].local6, v6, sizeof v6);
    }
    pthread_mutex_unlock(&dns_lock);
    return 0;
}

static int dns_sockaddr(const kml_dns_server *s, struct sockaddr_storage *ss);

// Binds fd to the channel's source address of s's family, when one is set.
static int dns_bind_local(int fd, const kml_dns_server *s, const char *local4, const char *local6) {
    const char *local = s->family == 4 ? local4 : local6;
    if (!local || !*local) return 0;
    kml_dns_server src;
    memset(&src, 0, sizeof src);
    src.family = s->family;
    snprintf(src.addr, sizeof src.addr, "%s", local);
    src.port = 0;
    struct sockaddr_storage ss;
    int len = dns_sockaddr(&src, &ss);
    return bind(fd, (struct sockaddr *)&ss, len);
}

// Ends the channel's in-flight queries with ECANCELLED.
void __kml_native_dns_channel_cancel(double id) {
    pthread_mutex_lock(&dns_lock);
    int i = (int)id;
    if (i >= 0 && i < KML_DNS_MAXCHAN && dns_chan[i].used) dns_chan[i].generation++;
    pthread_mutex_unlock(&dns_lock);
}

// A DNS question: header (id, RD), one QD, and an EDNS0 OPT record (c-ares'
// default, UDP payload 1232).
static int dns_build_query(unsigned char *q, size_t cap, const char *name, int type, unsigned short qid) {
    size_t n = 0;
    if (cap < 12) return -1;
    q[0] = (unsigned char)(qid >> 8); q[1] = (unsigned char)qid;
    q[2] = 0x01; q[3] = 0x00; // RD
    q[4] = 0; q[5] = 1;       // QDCOUNT
    q[6] = q[7] = q[8] = q[9] = 0;
    q[10] = 0; q[11] = 1;     // ARCOUNT (OPT)
    n = 12;
    const char *p = name;
    size_t total = strlen(name);
    if (total > 0 && name[total - 1] == '.') total--;
    size_t i = 0;
    while (i < total) {
        size_t j = i;
        while (j < total && name[j] != '.') j++;
        size_t len = j - i;
        if (len == 0 || len > 63 || n + 1 + len >= cap) return -1;
        q[n++] = (unsigned char)len;
        memcpy(q + n, p + i, len);
        n += len;
        i = j + 1;
    }
    if (n + 1 + 4 + 11 > cap || n - 12 > 255) return -1;
    q[n++] = 0;
    q[n++] = (unsigned char)(type >> 8); q[n++] = (unsigned char)type;
    q[n++] = 0; q[n++] = 1; // IN
    // OPT: root name, type 41, UDP size 1232, ext-rcode/version/flags 0, rdlen 0.
    q[n++] = 0; q[n++] = 0; q[n++] = 41; q[n++] = 0x04; q[n++] = 0xd0;
    q[n++] = 0; q[n++] = 0; q[n++] = 0; q[n++] = 0; q[n++] = 0; q[n++] = 0;
    return (int)n;
}

static unsigned short dns_rand16(void) {
    static unsigned int seed;
    if (!seed) seed = (unsigned int)time(NULL) ^ (unsigned int)(uintptr_t)&seed;
    seed = seed * 1103515245u + 12345u;
    return (unsigned short)(seed >> 16);
}

static int dns_sockaddr(const kml_dns_server *s, struct sockaddr_storage *ss) {
    memset(ss, 0, sizeof *ss);
    if (s->family == 4) {
        struct sockaddr_in *a = (struct sockaddr_in *)ss;
        a->sin_family = AF_INET;
        a->sin_port = htons((unsigned short)s->port);
        inet_pton(AF_INET, s->addr, &a->sin_addr);
        return (int)sizeof *a;
    }
    struct sockaddr_in6 *a = (struct sockaddr_in6 *)ss;
    a->sin6_family = AF_INET6;
    a->sin6_port = htons((unsigned short)s->port);
    inet_pton(AF_INET6, s->addr, &a->sin6_addr);
    return (int)sizeof *a;
}

// Waits up to ms for fd to be readable: 1 ready, 0 timeout, -1 error.
static int dns_wait(int fd, int ms) {
    struct pollfd pfd;
    pfd.fd = fd;
    pfd.events = POLLIN;
    pfd.revents = 0;
    int r = poll(&pfd, 1, ms);
    if (r > 0 && (pfd.revents & (POLLERR | POLLHUP)) && !(pfd.revents & POLLIN)) return -1;
    return r;
}

// One question to one server over UDP: the answer's length (into ans), 0 on
// a timeout, -ARES_ECONNREFUSED when the port refuses.
static int dns_udp(const kml_dns_server *s, const char *local4, const char *local6, const unsigned char *q, int qlen, unsigned char *ans, int cap, int ms) {
    struct sockaddr_storage ss;
    int slen = dns_sockaddr(s, &ss);
    int fd = (int)socket(s->family == 4 ? AF_INET : AF_INET6, SOCK_DGRAM, 0);
    if (fd < 0) return -ARES_ECONNREFUSED;
    if (dns_bind_local(fd, s, local4, local6) != 0 || connect(fd, (struct sockaddr *)&ss, slen) != 0 ||
        send(fd, (const char *)q, (size_t)qlen, 0) != qlen) {
        close(fd);
        return -ARES_ECONNREFUSED;
    }
    int result = 0;
    for (;;) {
        int w = dns_wait(fd, ms);
        if (w <= 0) { result = w < 0 ? -ARES_ECONNREFUSED : 0; break; }
        int64_t got = recv(fd, (char *)ans, (size_t)cap, 0);
        if (got < 0) { result = -ARES_ECONNREFUSED; break; }
        // Another query's late answer: keep waiting for ours.
        if (got >= 12 && ans[0] == q[0] && ans[1] == q[1] && (ans[2] & 0x80)) { result = (int)got; break; }
    }
    close(fd);
    return result;
}

// The same over TCP (a truncated UDP answer): the answer's length, 0 on a
// timeout, -ARES_ECONNREFUSED on a refused connection.
static int dns_tcp(const kml_dns_server *s, const char *local4, const char *local6, const unsigned char *q, int qlen, unsigned char *ans, int cap, int ms) {
    struct sockaddr_storage ss;
    int slen = dns_sockaddr(s, &ss);
    int fd = (int)socket(s->family == 4 ? AF_INET : AF_INET6, SOCK_STREAM, 0);
    if (fd < 0) return -ARES_ECONNREFUSED;
    if (dns_bind_local(fd, s, local4, local6) != 0 || connect(fd, (struct sockaddr *)&ss, slen) != 0) { close(fd); return -ARES_ECONNREFUSED; }
    unsigned char lenb[2] = {(unsigned char)(qlen >> 8), (unsigned char)qlen};
    if (send(fd, (const char *)lenb, 2, 0) != 2 || send(fd, (const char *)q, (size_t)qlen, 0) != qlen) { close(fd); return -ARES_ECONNREFUSED; }
    int have = 0, want = -1;
    unsigned char hdr[2];
    int hdrn = 0;
    while (want < 0 || have < want) {
        if (dns_wait(fd, ms) <= 0) { close(fd); return 0; }
        int64_t got;
        if (hdrn < 2) {
            got = recv(fd, (char *)hdr + hdrn, (size_t)(2 - hdrn), 0);
            if (got <= 0) { close(fd); return -ARES_ECONNREFUSED; }
            hdrn += (int)got;
            if (hdrn == 2) { want = (hdr[0] << 8) | hdr[1]; if (want > cap) want = cap; }
            continue;
        }
        got = recv(fd, (char *)ans + have, (size_t)(want - have), 0);
        if (got <= 0) { close(fd); return -ARES_ECONNREFUSED; }
        have += (int)got;
    }
    close(fd);
    return have;
}

// A (compressed) name at off, into out as dotted text: the offset past it
// in the record, or -1 when it is malformed.
static int dns_name(const unsigned char *m, int mlen, int off, char *out, int cap) {
    int n = 0, end = -1, hops = 0;
    out[0] = 0;
    while (off < mlen) {
        int len = m[off];
        if ((len & 0xc0) == 0xc0) {
            if (off + 1 >= mlen || ++hops > 64) return -1;
            if (end < 0) end = off + 2;
            off = ((len & 0x3f) << 8) | m[off + 1];
            continue;
        }
        if (len & 0xc0) return -1;
        off++;
        if (len == 0) break;
        if (off + len > mlen || n + len + 2 > cap) return -1;
        if (n) out[n++] = '.';
        for (int i = 0; i < len; i++) {
            unsigned char c = m[off + i];
            // c-ares escapes '.' and '\\' inside a label.
            if (c == '.' || c == '\\') {
                if (n + 3 > cap) return -1;
                out[n++] = '\\';
            }
            out[n++] = (char)c;
        }
        off += len;
    }
    out[n] = 0;
    return end >= 0 ? end : off;
}

static void jb_name(kml_jbuf *b, const char *key, const char *v) {
    jb_cstr(b, ",\"");
    jb_cstr(b, key);
    jb_cstr(b, "\":");
    jb_json(b, v, strlen(v));
}
static void jb_num(kml_jbuf *b, const char *key, long long v) {
    jb_cstr(b, ",\"");
    jb_cstr(b, key);
    jb_cstr(b, "\":");
    jb_int(b, v);
}

static int dns_u16(const unsigned char *p) { return (p[0] << 8) | p[1]; }
static unsigned long dns_u32(const unsigned char *p) { return ((unsigned long)p[0] << 24) | ((unsigned long)p[1] << 16) | ((unsigned long)p[2] << 8) | p[3]; }

// The answer section as JSON records: {"n":name,"t":type,"ttl":ttl, …}.
static int dns_parse(const unsigned char *m, int mlen, kml_jbuf *b) {
    if (mlen < 12) return ARES_EBADRESP;
    int qd = dns_u16(m + 4), an = dns_u16(m + 6);
    int off = 12;
    char name[1100], name2[1100];
    for (int i = 0; i < qd; i++) {
        off = dns_name(m, mlen, off, name, sizeof name);
        if (off < 0 || off + 4 > mlen) return ARES_EBADRESP;
        off += 4;
    }
    jb_cstr(b, "{\"rcode\":");
    jb_int(b, m[3] & 0x0f);
    jb_cstr(b, ",\"an\":[");
    for (int i = 0; i < an; i++) {
        off = dns_name(m, mlen, off, name, sizeof name);
        if (off < 0 || off + 10 > mlen) return ARES_EBADRESP;
        int type = dns_u16(m + off), rdlen = dns_u16(m + off + 8);
        long long ttl = (long long)dns_u32(m + off + 4);
        if (ttl > 0x7fffffffLL) ttl = 0;
        off += 10;
        if (off + rdlen > mlen) return ARES_EBADRESP;
        const unsigned char *rd = m + off;
        if (i) jb_cstr(b, ",");
        jb_cstr(b, "{\"t\":");
        jb_int(b, type);
        jb_name(b, "n", name);
        jb_num(b, "ttl", ttl);
        char ip[64];
        switch (type) {
        case 1: // A
            if (rdlen == 4 && inet_ntop(AF_INET, rd, ip, sizeof ip)) jb_name(b, "a", ip);
            break;
        case 28: // AAAA
            if (rdlen == 16 && inet_ntop(AF_INET6, rd, ip, sizeof ip)) jb_name(b, "a", ip);
            break;
        case 2: case 5: case 12: // NS, CNAME, PTR
            if (dns_name(m, mlen, off, name2, sizeof name2) < 0) return ARES_EBADRESP;
            jb_name(b, "h", name2);
            break;
        case 15: // MX
            if (rdlen < 3 || dns_name(m, mlen, off + 2, name2, sizeof name2) < 0) return ARES_EBADRESP;
            jb_num(b, "p", dns_u16(rd));
            jb_name(b, "h", name2);
            break;
        case 16: { // TXT: its character strings
            jb_cstr(b, ",\"c\":[");
            int k = 0, first = 1;
            while (k < rdlen) {
                int len = rd[k++];
                if (k + len > rdlen) return ARES_EBADRESP;
                if (!first) jb_cstr(b, ",");
                first = 0;
                jb_json(b, (const char *)rd + k, (size_t)len);
                k += len;
            }
            jb_cstr(b, "]");
            break;
        }
        case 33: // SRV
            if (rdlen < 7 || dns_name(m, mlen, off + 6, name2, sizeof name2) < 0) return ARES_EBADRESP;
            jb_num(b, "p", dns_u16(rd));
            jb_num(b, "w", dns_u16(rd + 2));
            jb_num(b, "port", dns_u16(rd + 4));
            jb_name(b, "h", name2);
            break;
        case 6: { // SOA
            int o = dns_name(m, mlen, off, name2, sizeof name2);
            if (o < 0) return ARES_EBADRESP;
            jb_name(b, "mname", name2);
            o = dns_name(m, mlen, o, name2, sizeof name2);
            if (o < 0 || o + 20 > off + rdlen) return ARES_EBADRESP;
            jb_name(b, "rname", name2);
            jb_num(b, "serial", (long long)dns_u32(m + o));
            jb_num(b, "refresh", (long long)dns_u32(m + o + 4));
            jb_num(b, "retry", (long long)dns_u32(m + o + 8));
            jb_num(b, "expire", (long long)dns_u32(m + o + 12));
            jb_num(b, "minimum", (long long)dns_u32(m + o + 16));
            break;
        }
        case 35: { // NAPTR
            if (rdlen < 7) return ARES_EBADRESP;
            jb_num(b, "order", dns_u16(rd));
            jb_num(b, "pref", dns_u16(rd + 2));
            int k = 4;
            const char *keys[3] = {"flags", "service", "regexp"};
            for (int s = 0; s < 3; s++) {
                if (k >= rdlen) return ARES_EBADRESP;
                int len = rd[k++];
                if (k + len > rdlen) return ARES_EBADRESP;
                jb_cstr(b, ",\"");
                jb_cstr(b, keys[s]);
                jb_cstr(b, "\":");
                jb_json(b, (const char *)rd + k, (size_t)len);
                k += len;
            }
            if (dns_name(m, mlen, off + k, name2, sizeof name2) < 0) return ARES_EBADRESP;
            jb_name(b, "replacement", name2);
            break;
        }
        case 257: { // CAA
            if (rdlen < 2) return ARES_EBADRESP;
            int tl = rd[1];
            if (2 + tl > rdlen) return ARES_EBADRESP;
            jb_num(b, "critical", rd[0]);
            jb_cstr(b, ",\"tag\":");
            jb_json(b, (const char *)rd + 2, (size_t)tl);
            jb_cstr(b, ",\"value\":");
            jb_json(b, (const char *)rd + 2 + tl, (size_t)(rdlen - 2 - tl));
            break;
        }
        case 52: { // TLSA
            if (rdlen < 3) return ARES_EBADRESP;
            jb_num(b, "usage", rd[0]);
            jb_num(b, "selector", rd[1]);
            jb_num(b, "match", rd[2]);
            jb_cstr(b, ",\"data\":\"");
            for (int k = 3; k < rdlen; k++) {
                char hx[3];
                snprintf(hx, sizeof hx, "%02x", rd[k]);
                jb_put(b, hx, 2);
            }
            jb_cstr(b, "\"");
            break;
        }
        }
        jb_cstr(b, "}");
        off += rdlen;
    }
    jb_cstr(b, "]}");
    return 0;
}

// a[0] the channel, a[1] the record type; arg0 the name.
static void work_query(kml_pool_item *it) {
    pthread_mutex_lock(&dns_lock);
    int ci = (int)it->a[0];
    kml_dns_channel c;
    int ok = ci >= 0 && ci < KML_DNS_MAXCHAN && dns_chan[ci].used;
    if (ok) c = dns_chan[ci];
    pthread_mutex_unlock(&dns_lock);
    if (!ok) { it->err = ARES_ECANCELLED; return; }
    int64_t gen = c.generation;
    unsigned char q[600];
    int qlen = dns_build_query(q, sizeof q, it->arg0, (int)it->a[1], dns_rand16());
    if (qlen < 0) { it->err = ARES_EBADNAME; return; }
    int timeout = c.timeout_ms < 0 ? 2000 : c.timeout_ms;
    if (timeout <= 0) timeout = 1;
    int status = ARES_ETIMEOUT;
    unsigned char *ans = (unsigned char *)malloc(65536);
    for (int round = 0; round < c.tries; round++) {
        int ms = timeout << (round < 16 ? round : 16);
        if (c.max_timeout_ms > 0 && ms > c.max_timeout_ms) ms = c.max_timeout_ms;
        for (int si = 0; si < c.nserv; si++) {
            pthread_mutex_lock(&dns_lock);
            int cancelled = dns_chan[ci].generation != gen;
            pthread_mutex_unlock(&dns_lock);
            if (cancelled) { free(ans); it->err = ARES_ECANCELLED; return; }
            int got = dns_udp(&c.serv[si], c.local4, c.local6, q, qlen, ans, 65536, ms);
            if (got > 0 && (ans[2] & 0x02)) // TC: the whole answer over TCP
                got = dns_tcp(&c.serv[si], c.local4, c.local6, q, qlen, ans, 65536, ms);
            if (got == -ARES_ECONNREFUSED) { status = ARES_ECONNREFUSED; continue; }
            if (got <= 0) { if (status != ARES_ECONNREFUSED) status = ARES_ETIMEOUT; continue; }
            int rcode = ans[3] & 0x0f;
            // SERVFAIL, NOTIMP and REFUSED move on to the next server.
            if (rcode == 2) { status = ARES_ESERVFAIL; continue; }
            if (rcode == 4) { status = ARES_ENOTIMP; continue; }
            if (rcode == 5) { status = ARES_EREFUSED; continue; }
            if (rcode == 1) { free(ans); it->err = ARES_EFORMERR; return; }
            if (rcode == 3) { free(ans); it->err = ARES_ENOTFOUND; return; }
            kml_jbuf b = {0};
            int pr = dns_parse(ans, got, &b);
            free(ans);
            if (pr) { free(b.s); it->err = pr; return; }
            it->res_str = b.s;
            return;
        }
    }
    free(ans);
    it->err = status;
}

void __kml_native_dns_query(double channel, double type, const char *name, void *inv, void *clo) {
    kml_pool_item *it = native_item(work_query, inv, clo);
    it->arg0 = strdup(name ? name : "");
    it->a[0] = (int64_t)channel;
    it->a[1] = (int64_t)type;
    native_submit(it);
}
