/* klain:webview's entry points (lib/node/klain_webview.ts through
 * lib/native.d.ts) over the webview_* C API (webview.h, or a backend shim).
 * A window crosses as its webview_t's address in a double, exact below 2^53. */
#include <stdint.h>
#include <stdlib.h>
#include <string.h>

typedef void *webview_t;
extern webview_t webview_create(int debug, void *window);
extern void webview_destroy(webview_t w);
extern void webview_run(webview_t w);
extern void webview_terminate(webview_t w);
extern void webview_dispatch(webview_t w, void (*fn)(webview_t w, void *arg), void *arg);
extern void webview_set_title(webview_t w, const char *title);
extern void webview_set_size(webview_t w, int width, int height, int hints);
extern void webview_navigate(webview_t w, const char *url);
extern void webview_set_html(webview_t w, const char *html);
extern void webview_init(webview_t w, const char *js);
extern void webview_eval(webview_t w, const char *js);
extern void webview_bind(webview_t w, const char *name,
                         void (*fn)(const char *id, const char *req, void *arg), void *arg);
extern void webview_unbind(webview_t w, const char *name);
extern void webview_return(webview_t w, const char *id, int status, const char *result);

extern char *__kml_str_alloc(int64_t n);

#ifdef _WIN32
/* The deferring navigation shims (webview_win32.go). */
extern void __kml_wv_navigate(webview_t w, const char *url);
extern void __kml_wv_set_html(webview_t w, const char *html);
#define KWV_NAVIGATE __kml_wv_navigate
#define KWV_SET_HTML __kml_wv_set_html
#else
#define KWV_NAVIGATE webview_navigate
#define KWV_SET_HTML webview_set_html
#endif
/* The runtime's loop inside the GUI's main loop (webviewsrc/loop_embed.cc). */
extern void __kml_wv_loop_embed(void);
extern void __kml_wv_loop_kick(void);

#define KWV(h) ((webview_t)(uintptr_t)(h))

/* A C string as a runtime string ([i64 len][bytes][NUL], value at +8). */
static char *kwv_str(const char *s) {
  int64_t n = (int64_t)strlen(s);
  char *out = __kml_str_alloc(n);
  memcpy(out, s, (size_t)n);
  out[n] = 0;
  return out;
}

double __kml_native_wv_create(double debug) {
  return (double)(uintptr_t)webview_create(debug != 0, 0);
}

void __kml_native_wv_set_title(double h, const char *s) { webview_set_title(KWV(h), s); }
void __kml_native_wv_navigate(double h, const char *s) { KWV_NAVIGATE(KWV(h), s); }
void __kml_native_wv_set_html(double h, const char *s) { KWV_SET_HTML(KWV(h), s); }
void __kml_native_wv_init(double h, const char *s) { webview_init(KWV(h), s); }
void __kml_native_wv_unbind(double h, const char *s) { webview_unbind(KWV(h), s); }

void __kml_native_wv_set_size(double h, double width, double height) {
  webview_set_size(KWV(h), (int)width, (int)height, 0);
}

/* eval goes through webview_dispatch, so it runs on the GUI thread whichever
   thread calls it (a Worker pushing a UI update). */
static void kwv_eval(webview_t w, void *js) {
  webview_eval(w, (const char *)js);
  free(js); /* scratch */
}

void __kml_native_wv_eval(double h, const char *js) {
  size_t n = strlen(js) + 1;
  char *copy = (char *)malloc(n); /* scratch */
  memcpy(copy, js, n);
  webview_dispatch(KWV(h), kwv_eval, copy);
}

void __kml_native_wv_run(double h) {
  webview_t w = KWV(h);
  __kml_wv_loop_embed();
  webview_run(w);
}

void __kml_native_wv_terminate(double h) { webview_terminate(KWV(h)); }
void __kml_native_wv_destroy(double h) { webview_destroy(KWV(h)); }

/* A binding calls inv(clo, id, req) with the page's call id and its JSON
   array of arguments; the callback answers through wvReturn. */
typedef void (*kwv_inv)(void *clo, const char *id, const char *req);

static void kwv_bound(const char *id, const char *req, void *env) {
  void **e = (void **)env;
  ((kwv_inv)e[0])(e[1], kwv_str(id), kwv_str(req));
  /* Whatever the callback started (a fetch, a timer, a settled promise)
     gets a loop pass now, not at the next deadline the host had armed. */
  __kml_wv_loop_kick();
}

void __kml_native_wv_bind(double h, const char *name, void *inv, void *clo) {
  void **env = (void **)kml_alloc(KML_HEAP_WORDS, 2 * sizeof(void *)); /* holds the JS closure, never freed */
  env[0] = inv;
  env[1] = clo;
  webview_bind(KWV(h), name, kwv_bound, env);
}

void __kml_native_wv_return(double h, const char *id, double status, const char *result) {
  webview_return(KWV(h), id, (int)status, result);
}
