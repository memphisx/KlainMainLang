// The runtime's loop inside the GUI's main loop (TDD-00244 Stage 7, TDD-00252
// on Windows): the webview's main loop owns the thread, so it watches
// libuv's backend and a timer for the loop's next deadline, and runs one
// non-blocking pass (loop.c's __kml_loop_run_once) on either. A bound
// callback's return kicks a pass, so the work it started runs promptly.
extern "C" {
void __kml_loop_run_once(void);
int __kml_loop_backend_fd(void);
long long __kml_loop_next_ms(void);
}

#if defined(__APPLE__)
#include <CoreFoundation/CoreFoundation.h>

// A repeating timer whose next fire date is set after every pass: a
// one-shot CFRunLoopTimer is invalidated once it fires.
static CFRunLoopTimerRef kml_loop_timer;

static void kml_loop_rearm(void) {
  long long ms = __kml_loop_next_ms();
  CFAbsoluteTime now = CFAbsoluteTimeGetCurrent();
  CFRunLoopTimerSetNextFireDate(kml_loop_timer, ms < 0 ? now + 1.0e9 : now + ms / 1000.0);
}

static void kml_loop_pass(void) {
  __kml_loop_run_once();
  kml_loop_rearm();
}

static void kml_loop_on_timer(CFRunLoopTimerRef, void *) { kml_loop_pass(); }

static void kml_loop_on_fd(CFFileDescriptorRef f, CFOptionFlags, void *) {
  kml_loop_pass();
  // CFFileDescriptor callbacks are one-shot.
  CFFileDescriptorEnableCallBacks(f, kCFFileDescriptorReadCallBack);
}

extern "C" void __kml_wv_loop_embed(void) {
  if (kml_loop_timer) return;
  CFRunLoopRef rl = CFRunLoopGetMain();
  kml_loop_timer = CFRunLoopTimerCreate(nullptr, CFAbsoluteTimeGetCurrent(), 1.0e9, 0, 0,
                                        kml_loop_on_timer, nullptr);
  CFRunLoopAddTimer(rl, kml_loop_timer, kCFRunLoopCommonModes);
  CFFileDescriptorRef fd =
      CFFileDescriptorCreate(nullptr, __kml_loop_backend_fd(), false, kml_loop_on_fd, nullptr);
  CFFileDescriptorEnableCallBacks(fd, kCFFileDescriptorReadCallBack);
  CFRunLoopSourceRef src = CFFileDescriptorCreateRunLoopSource(nullptr, fd, 0);
  CFRunLoopAddSource(rl, src, kCFRunLoopCommonModes);
  CFRelease(src);
}

extern "C" void __kml_wv_loop_kick(void) {
  if (kml_loop_timer) CFRunLoopTimerSetNextFireDate(kml_loop_timer, CFAbsoluteTimeGetCurrent());
}

#elif defined(_WIN32)
#include <windows.h>
extern "C" void *__kml_loop_backend_port(void);

// libuv's backend on Windows is its completion port, which has no
// descriptor a message loop could watch. A thread waits on the port for the
// next completion or the loop's next deadline, puts back what it took, and
// posts the GUI thread a message that runs one pass; the end of that pass
// lets the thread wait again, with the deadline the pass left. (Electron
// embeds Node's loop the same way.) A kick is an empty completion, which
// ends the thread's wait and which libuv ignores.
static HWND kml_loop_hwnd;
static HANDLE kml_loop_port;
static HANDLE kml_loop_resume;           // auto-reset: a pass ran, wait again
static volatile long long kml_loop_wait; // its deadline, ms (-1: none)
static const UINT KML_WM_PASS = WM_APP + 0x4b4d;

static LRESULT CALLBACK kml_loop_wndproc(HWND h, UINT m, WPARAM w, LPARAM l) {
  if (m != KML_WM_PASS) return DefWindowProcW(h, m, w, l);
  __kml_loop_run_once();
  kml_loop_wait = __kml_loop_next_ms();
  SetEvent(kml_loop_resume);
  return 0;
}

static DWORD WINAPI kml_loop_watch(LPVOID) {
  for (;;) {
    WaitForSingleObject(kml_loop_resume, INFINITE);
    long long ms = kml_loop_wait;
    DWORD bytes = 0;
    ULONG_PTR key = 0;
    OVERLAPPED *ov = nullptr;
    BOOL got = GetQueuedCompletionStatus(kml_loop_port, &bytes, &key, &ov,
                                         ms < 0 ? INFINITE : (DWORD)ms);
    if (got || ov) PostQueuedCompletionStatus(kml_loop_port, bytes, key, ov);
    PostMessageW(kml_loop_hwnd, KML_WM_PASS, 0, 0);
  }
}

extern "C" void __kml_wv_loop_embed(void) {
  if (kml_loop_hwnd) return;
  WNDCLASSW wc = {};
  wc.lpfnWndProc = kml_loop_wndproc;
  wc.hInstance = GetModuleHandleW(nullptr);
  wc.lpszClassName = L"KmlLoopEmbed";
  RegisterClassW(&wc);
  kml_loop_hwnd = CreateWindowExW(0, wc.lpszClassName, L"", 0, 0, 0, 0, 0, HWND_MESSAGE, nullptr,
                                  wc.hInstance, nullptr);
  kml_loop_port = (HANDLE)__kml_loop_backend_port();
  kml_loop_resume = CreateEventW(nullptr, FALSE, FALSE, nullptr);
  // The first pass runs as soon as the GUI loop does.
  PostMessageW(kml_loop_hwnd, KML_WM_PASS, 0, 0);
  HANDLE t = CreateThread(nullptr, 64 * 1024, kml_loop_watch, nullptr, 0, nullptr);
  if (t) CloseHandle(t);
}

extern "C" void __kml_wv_loop_kick(void) {
  if (kml_loop_port) PostQueuedCompletionStatus(kml_loop_port, 0, 0, nullptr);
}

#elif defined(__linux__)
#include <glib-unix.h>
#include <glib.h>

static guint kml_loop_timer;
static bool kml_loop_embedded;

static gboolean kml_loop_on_timer(gpointer);

static void kml_loop_arm(long long ms) {
  if (kml_loop_timer) g_source_remove(kml_loop_timer);
  kml_loop_timer = ms < 0 ? 0 : g_timeout_add_full(G_PRIORITY_DEFAULT, (guint)ms, kml_loop_on_timer, nullptr, nullptr);
}

static void kml_loop_pass(void) {
  __kml_loop_run_once();
  kml_loop_arm(__kml_loop_next_ms());
}

static gboolean kml_loop_on_timer(gpointer) {
  kml_loop_timer = 0;  // removed by returning G_SOURCE_REMOVE, not by the re-arm
  kml_loop_pass();
  return G_SOURCE_REMOVE;
}

static gboolean kml_loop_on_fd(gint, GIOCondition, gpointer) {
  kml_loop_pass();
  return G_SOURCE_CONTINUE;
}

extern "C" void __kml_wv_loop_embed(void) {
  if (kml_loop_embedded) return;
  kml_loop_embedded = true;
  g_unix_fd_add(__kml_loop_backend_fd(), G_IO_IN, kml_loop_on_fd, nullptr);
  kml_loop_arm(0);
}

extern "C" void __kml_wv_loop_kick(void) {
  if (kml_loop_embedded) kml_loop_arm(0);
}
#endif
