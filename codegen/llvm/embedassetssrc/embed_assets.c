/*
 * embed_assets.c — TDD-00142 Stage 7: a minimal static HTTP/1.0 server over an
 * embedded, in-binary read-only asset blob (a packed SPA/SSG `dist/`).
 *
 * The blob is produced at compile time (a `.incbin`'d packed image; see the Go
 * side) and linked into the binary. __kml_embed_serve() binds 127.0.0.1:0,
 * reads the OS-assigned ephemeral port, spawns a detached accept thread, and
 * returns the port synchronously — so the caller can navigate a webview to it
 * with no port race. Serves binary bytes verbatim (fonts/images), with a
 * dir->index.html and miss->index.html/404.html fallback covering both Quasar
 * SPA (single index.html) and SSG (per-route index.html + 404.html) output.
 *
 * libc + pthread only. Close-after-response; no keep-alive/ranges (fine for a
 * local single-app server).
 */
#include <stdint.h>
#include <string.h>
#include <stdlib.h>
#include <stdio.h>
#include <unistd.h>
#include <pthread.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>

/* Packed blob layout (little-endian, offsets from blob base):
 *   header:  char magic[4] = "KMLA"; uint32 version; uint32 count; uint32 rsvd;
 *   entries: count * { uint32 pathOff, pathLen, dataOff, dataLen, ctype, rsvd }
 *   then the string table and file data (referenced by the offsets above).
 * Entries are sorted by path (byte order) for binary search. */
typedef struct {
  uint32_t path_off, path_len, data_off, data_len, ctype, rsvd;
} kml_embed_entry;

static const char *kml_ctype_str(uint32_t c) {
  switch (c) {
    case 1: return "text/html; charset=utf-8";
    case 2: return "text/javascript; charset=utf-8";
    case 3: return "text/css; charset=utf-8";
    case 4: return "application/json";
    case 5: return "image/svg+xml";
    case 6: return "font/woff";
    case 7: return "font/woff2";
    case 8: return "image/png";
    case 9: return "image/jpeg";
    case 10: return "image/gif";
    case 11: return "application/wasm";
    case 12: return "text/plain; charset=utf-8";
    case 13: return "image/x-icon";
    case 14: return "application/json"; /* .map */
    case 15: return "image/webp";
    default: return "application/octet-stream";
  }
}

/* __kml_embed_lookup: binary-search the blob's manifest for an exact path
 * (path and path_len need not be NUL-terminated). Returns 1 and fills the out
 * pointers (data, len, ctype) on hit, else 0. */
int __kml_embed_lookup(const void *blob, const char *path, long path_len,
                       const unsigned char **data, long *len, uint32_t *ctype) {
  const unsigned char *base = (const unsigned char *)blob;
  uint32_t count;
  memcpy(&count, base + 8, 4);
  const kml_embed_entry *ents = (const kml_embed_entry *)(base + 16);
  long lo = 0, hi = (long)count - 1;
  while (lo <= hi) {
    long mid = (lo + hi) / 2;
    const kml_embed_entry *e = &ents[mid];
    const char *ep = (const char *)(base + e->path_off);
    long n = e->path_len < (uint32_t)path_len ? (long)e->path_len : path_len;
    int cmp = memcmp(ep, path, (size_t)n);
    if (cmp == 0) cmp = (int)((long)e->path_len - path_len);
    if (cmp == 0) {
      *data = base + e->data_off;
      *len = (long)e->data_len;
      *ctype = e->ctype;
      return 1;
    }
    if (cmp < 0) lo = mid + 1; else hi = mid - 1;
  }
  return 0;
}

/* __kml_embed_get: exact-match lookup for the klain:assets `get(path)` method.
 * Returns the data pointer (into the static blob, no copy) and sets *out_len, or
 * NULL if the path isn't embedded. */
const unsigned char *__kml_embed_get(const void *blob, const char *path,
                                     long path_len, long *out_len) {
  const unsigned char *data = 0;
  long len = 0;
  uint32_t ctype = 0;
  if (__kml_embed_lookup(blob, path, path_len, &data, &len, &ctype)) {
    *out_len = len;
    return data;
  }
  *out_len = 0;
  return 0;
}

/* Resolve a request path to an asset, applying the SPA/SSG fallback rule.
 * `rel` points at the request path (without query), rlen its length. */
static int kml_embed_resolve(const void *blob, const char *rel, long rlen,
                             const unsigned char **data, long *len, uint32_t *ctype) {
  char buf[2048];
  /* "/" -> "/index.html" */
  if (rlen == 1 && rel[0] == '/') {
    return __kml_embed_lookup(blob, "/index.html", 11, data, len, ctype);
  }
  /* exact */
  if (__kml_embed_lookup(blob, rel, rlen, data, len, ctype)) return 1;
  /* trailing slash -> +index.html; else try /index.html appended */
  if (rlen > 0 && rlen < (long)sizeof(buf) - 12) {
    memcpy(buf, rel, (size_t)rlen);
    long m = rlen;
    if (buf[m - 1] != '/') buf[m++] = '/';
    memcpy(buf + m, "index.html", 10);
    m += 10;
    if (__kml_embed_lookup(blob, buf, m, data, len, ctype)) return 1;
  }
  /* SSG miss -> 404.html if present, else SPA fallback to /index.html */
  if (__kml_embed_lookup(blob, "/404.html", 9, data, len, ctype)) return 1;
  return __kml_embed_lookup(blob, "/index.html", 11, data, len, ctype);
}

static void kml_embed_handle(int fd, const void *blob) {
  char req[4096];
  long n = (long)recv(fd, req, sizeof(req) - 1, 0);
  if (n <= 0) { close(fd); return; }
  req[n] = 0;
  /* Parse "GET /path HTTP/1.x" (also accept HEAD). */
  int head = 0;
  char *p = req;
  if (!strncmp(p, "GET ", 4)) p += 4;
  else if (!strncmp(p, "HEAD ", 5)) { p += 5; head = 1; }
  else { close(fd); return; }
  char *sp = strchr(p, ' ');
  if (!sp) { close(fd); return; }
  *sp = 0;
  /* strip query string */
  char *q = strchr(p, '?');
  if (q) *q = 0;
  long rlen = (long)strlen(p);

  const unsigned char *data = 0; long dlen = 0; uint32_t ctype = 0;
  char hdr[512];
  if (kml_embed_resolve(blob, p, rlen, &data, &dlen, &ctype)) {
    int hn = snprintf(hdr, sizeof(hdr),
      "HTTP/1.0 200 OK\r\nContent-Type: %s\r\nContent-Length: %ld\r\n"
      "Connection: close\r\n\r\n", kml_ctype_str(ctype), dlen);
    send(fd, hdr, (size_t)hn, 0);
    if (!head && dlen > 0) {
      long off = 0;
      while (off < dlen) {
        long w = (long)send(fd, data + off, (size_t)(dlen - off), 0);
        if (w <= 0) break;
        off += w;
      }
    }
  } else {
    const char *body = "not found";
    int hn = snprintf(hdr, sizeof(hdr),
      "HTTP/1.0 404 Not Found\r\nContent-Type: text/plain\r\n"
      "Content-Length: 9\r\nConnection: close\r\n\r\n");
    send(fd, hdr, (size_t)hn, 0);
    if (!head) send(fd, body, 9, 0);
  }
  close(fd);
}

typedef struct { int lfd; const void *blob; } kml_embed_ctx;

static void *kml_embed_loop(void *arg) {
  kml_embed_ctx *c = (kml_embed_ctx *)arg;
  for (;;) {
    int fd = accept(c->lfd, 0, 0);
    if (fd < 0) continue;
    kml_embed_handle(fd, c->blob);
  }
  return 0;
}

/* __kml_embed_serve: bind 127.0.0.1:0, listen, spawn a detached accept thread,
 * and return the OS-assigned port (or -1 on failure). Synchronous — the port is
 * live before this returns, so the caller can navigate to it immediately. */
int __kml_embed_serve(const void *blob) {
  int lfd = socket(AF_INET, SOCK_STREAM, 0);
  if (lfd < 0) return -1;
  int one = 1;
  setsockopt(lfd, SOL_SOCKET, SO_REUSEADDR, &one, sizeof(one));
  struct sockaddr_in a;
  memset(&a, 0, sizeof(a));
  a.sin_family = AF_INET;
  a.sin_addr.s_addr = htonl(INADDR_LOOPBACK);
  a.sin_port = 0;
  if (bind(lfd, (struct sockaddr *)&a, sizeof(a)) < 0) { close(lfd); return -1; }
  if (listen(lfd, 16) < 0) { close(lfd); return -1; }
  struct sockaddr_in got;
  socklen_t gl = sizeof(got);
  if (getsockname(lfd, (struct sockaddr *)&got, &gl) < 0) { close(lfd); return -1; }
  int port = (int)ntohs(got.sin_port);

  kml_embed_ctx *c = (kml_embed_ctx *)malloc(sizeof(kml_embed_ctx));
  c->lfd = lfd;
  c->blob = blob;
  pthread_t t;
  pthread_attr_t at;
  pthread_attr_init(&at);
  pthread_attr_setdetachstate(&at, PTHREAD_CREATE_DETACHED);
  if (pthread_create(&t, &at, kml_embed_loop, c) != 0) { close(lfd); free(c); return -1; }
  pthread_attr_destroy(&at);
  return port;
}
