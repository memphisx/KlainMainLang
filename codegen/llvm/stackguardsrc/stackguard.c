/* stackguard.c — a stack overflow throws Node's RangeError (TDD-00249).
 * The emitted code makes no tail calls, so unbounded recursion grows the
 * stack until it faults on the guard region below it: the main thread's,
 * or the guard page under a fiber's or goroutine's stack
 * (__kml_fiber_stack_alloc).
 * The fault is taken on an alternate signal stack. When its address is next
 * to the faulting stack pointer, the handler throws through the normal
 * throw path, which longjmps to the innermost try frame (or reports it
 * uncaught). Any other fault keeps the default action. */
#if !defined(_WIN32)
#define _GNU_SOURCE
#include <pthread.h>
#include <signal.h>
#if defined(__APPLE__)
#include <sys/ucontext.h>
#else
#include <ucontext.h>
#endif
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/mman.h>
#include <unistd.h>

#if defined(__has_feature)
#if __has_feature(address_sanitizer) || __has_feature(thread_sanitizer) || __has_feature(memory_sanitizer)
#define KML_SG_SANITIZER 1
#endif
#endif
#if defined(__SANITIZE_ADDRESS__) || defined(__SANITIZE_THREAD__)
#define KML_SG_SANITIZER 1
#endif

/* The handler's own stack: building the RangeError (an allocation and a
 * few stores) and starting the longjmp run here. It is a mapping, not
 * malloc'd: under -mm=gc only the kernel would name it. */
#define SG_ALT_BYTES (256 * 1024)
/* An overflow faults at a store into the frame being built: at most a
 * page below the stack pointer (a push) or within the frame above it. */
#define SG_BELOW (64 * 1024)
#define SG_ABOVE (1024 * 1024)

/* Node's default --stack-size: the main thread overflows past this much
 * stack below main, as Node's does, rather than at the native 8 MB. */
#define SG_MAIN_STACK (984 * 1024)
/* Linux will not grow a stack to within its guard gap (256 pages by
 * default) of the mapping below it, so the guard mapping goes that much
 * lower there. */
#define SG_LINUX_GAP (256 * 4096)
/* The main thread's software limit: each user function's prologue checks
 * its frame against the band [__kml_sg_band_lo, +SG_HEADROOM) just above the
 * guard page and throws there, from generated code. The guard page is only
 * the backstop: a native call (libc, libcurl) made by the deepest frame
 * still has SG_HEADROOM below it, so an overflow is not taken inside it,
 * where throwing would longjmp out of a held lock (malloc's). */
#define SG_HEADROOM (256 * 1024)
uintptr_t __kml_sg_band_lo;

static void (*sg_throw)(const char *);
static const char *sg_msg;
void __kml_frames_fault_page(uintptr_t lo, uintptr_t hi); /* frames.c */
/* Set once the handler is installed: another thread then needs its own
 * alternate stack (__kml_stack_guard_thread). */
static int sg_on;

/* The interrupted stack pointer, 0 on a host this does not know. */
static uintptr_t sg_sp(void *uc) {
    ucontext_t *u = (ucontext_t *)uc;
#if defined(__APPLE__) && defined(__aarch64__)
    return (uintptr_t)u->uc_mcontext->__ss.__sp;
#elif defined(__APPLE__) && defined(__x86_64__)
    return (uintptr_t)u->uc_mcontext->__ss.__rsp;
#elif defined(__linux__) && defined(__aarch64__)
    return (uintptr_t)u->uc_mcontext.sp;
#elif defined(__linux__) && defined(__x86_64__)
    return (uintptr_t)u->uc_mcontext.gregs[REG_RSP];
#else
    (void)u;
    return 0;
#endif
}

static void sg_handler(int sig, siginfo_t *si, void *uc) {
    uintptr_t a = (uintptr_t)si->si_addr, sp = sg_sp(uc);
    if (sp && a + SG_BELOW >= sp && a < sp + SG_ABOVE) {
        /* The overflowing call's frame record may sit in the faulting page:
         * the error's stack trace starts above it. */
        uintptr_t g = (uintptr_t)getpagesize();
        __kml_frames_fault_page(a & ~(g - 1), (a & ~(g - 1)) + g);
        if (sg_throw) sg_throw(sg_msg);
        static const char m[] = "RangeError: Maximum call stack size exceeded\n";
        fflush(stdout);
        write(2, m, sizeof m - 1);
        _exit(1);
    }
    /* Not an overflow: fault again with the default action. */
    signal(sig, SIG_DFL);
}

/* A prologue's frame fell in the band: the RangeError, thrown from the
 * program's own code. */
void __kml_stack_overflow(void) {
    if (sg_throw) sg_throw(sg_msg);
    static const char m[] = "RangeError: Maximum call stack size exceeded\n";
    fflush(stdout);
    write(2, m, sizeof m - 1);
    _exit(1);
}

/* A PROT_NONE page SG_MAIN_STACK below the caller's frame (main). On
 * macOS the main stack is mapped in full, so the page is protected in
 * place; on Linux it grows on demand, so an unused page is mapped there,
 * and left alone if anything already is. */
static void sg_limit_main(void) {
    uintptr_t g = (uintptr_t)getpagesize();
    uintptr_t sp = (uintptr_t)__builtin_frame_address(0);
#if defined(__APPLE__)
    /* Only the main thread's own stack: below a fiber's stack pointer lies
     * whatever happens to be mapped there, the executable's code included. */
    uintptr_t top = (uintptr_t)pthread_get_stackaddr_np(pthread_self());
    if (!pthread_main_np() || sp > top || sp < top - pthread_get_stacksize_np(pthread_self())) return;
    uintptr_t lim = (sp - SG_MAIN_STACK - SG_HEADROOM) & ~(g - 1);
    if (mprotect((void *)lim, g, PROT_NONE) == 0) __kml_sg_band_lo = lim + g;
#elif defined(MAP_FIXED_NOREPLACE)
    uintptr_t lim = (sp - SG_MAIN_STACK - SG_HEADROOM - SG_LINUX_GAP) & ~(g - 1);
    void *p = mmap((void *)lim, g, PROT_NONE, MAP_PRIVATE | MAP_ANONYMOUS | MAP_FIXED_NOREPLACE, -1, 0);
    if (p != MAP_FAILED && p != (void *)lim) munmap(p, g);
    if (p == (void *)lim) __kml_sg_band_lo = (sp - SG_MAIN_STACK - SG_HEADROOM) & ~(g - 1);
#else
    (void)g;
    (void)sp;
#endif
}

void __kml_stack_guard_init(void (*throw_fn)(const char *), const char *msg) {
#ifndef KML_SG_SANITIZER
    sg_throw = throw_fn;
    sg_msg = msg;
    sg_limit_main();
    stack_t ss;
    memset(&ss, 0, sizeof ss);
    ss.ss_sp = mmap(NULL, SG_ALT_BYTES, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    ss.ss_size = SG_ALT_BYTES;
    if (ss.ss_sp == MAP_FAILED || sigaltstack(&ss, NULL) != 0) return;
    struct sigaction sa;
    memset(&sa, 0, sizeof sa);
    sa.sa_sigaction = sg_handler;
    sa.sa_flags = SA_SIGINFO | SA_ONSTACK | SA_NODEFER;
    sigemptyset(&sa.sa_mask);
    sigaction(SIGSEGV, &sa, NULL);
    sigaction(SIGBUS, &sa, NULL);
    sg_on = 1;
#else
    (void)throw_fn;
    (void)msg;
#endif
}

/* An alternate stack is per thread: a thread that runs program code (a
 * Worker, a goroutine's machine) maps its own, unmapped as it ends. */
static pthread_key_t sg_alt_key;
static pthread_once_t sg_alt_once = PTHREAD_ONCE_INIT;

static void sg_alt_free(void *p) {
    stack_t ss;
    memset(&ss, 0, sizeof ss);
    ss.ss_flags = SS_DISABLE;
    sigaltstack(&ss, NULL);
    munmap(p, SG_ALT_BYTES);
}

static void sg_alt_key_make(void) { pthread_key_create(&sg_alt_key, sg_alt_free); }

void __kml_stack_guard_thread(void) {
    if (!sg_on) return;
    pthread_once(&sg_alt_once, sg_alt_key_make);
    stack_t ss;
    memset(&ss, 0, sizeof ss);
    ss.ss_sp = mmap(NULL, SG_ALT_BYTES, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    ss.ss_size = SG_ALT_BYTES;
    if (ss.ss_sp == MAP_FAILED) return;
    if (sigaltstack(&ss, NULL) != 0) {
        munmap(ss.ss_sp, SG_ALT_BYTES);
        return;
    }
    pthread_setspecific(sg_alt_key, ss.ss_sp);
}

/* A task fiber's stack, with a PROT_NONE guard page under it so an overflow
 * faults instead of writing over the heap. The page sits below the range
 * returned: macOS makecontext faults on a stack range holding one. */
/* Spent stacks of the common size are kept for the next fiber: a
 * short-lived generator or task would otherwise pay an mmap, mprotect and
 * munmap each. */
#define FS_POOL 64
static char *fs_pool[FS_POOL];
static size_t fs_pool_n, fs_pool_len;
static pthread_mutex_t fs_pool_mu = PTHREAD_MUTEX_INITIALIZER;

void *__kml_fiber_stack_alloc(size_t n) {
    pthread_mutex_lock(&fs_pool_mu);
    if (fs_pool_len == 0) fs_pool_len = n;
    char *s = fs_pool_n > 0 && fs_pool_len == n ? fs_pool[--fs_pool_n] : NULL;
    pthread_mutex_unlock(&fs_pool_mu);
    if (s) return s;
    size_t g = (size_t)getpagesize();
    char *p = mmap(NULL, n + g, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    if (p == MAP_FAILED) return NULL;
    mprotect(p, g, PROT_NONE);
    return p + g;
}

void __kml_fiber_stack_free(void *s, size_t n) {
    if (!s) return;
    pthread_mutex_lock(&fs_pool_mu);
    if (fs_pool_len == n && fs_pool_n < FS_POOL) {
        fs_pool[fs_pool_n++] = s;
        s = NULL;
    }
    pthread_mutex_unlock(&fs_pool_mu);
    size_t g = (size_t)getpagesize();
    if (s) munmap((char *)s - g, n + g);
}
#else
/* Windows (TDD-00249 Stage 3): the thread's guard page raises
 * EXCEPTION_STACK_OVERFLOW, which a vectored handler turns into the
 * RangeError. Windows has no alternate signal stack: the handler runs on what
 * is left below the guard page, which SetThreadStackGuarantee keeps at
 * SG_GUARANTEE. The throw longjmps to the innermost try frame (setjmp with a
 * null frame: no unwinding through the handler's frames), and the catch
 * re-arms the spent guard page (exceptions.c, __kml_get_thrown_tag). */
#include <windows.h>
#include <stdint.h>
#include <stdio.h>

#define SG_GUARANTEE (64 * 1024)

static void (*sg_throw)(const char *);
static const char *sg_msg;

/* Not instrumented under -fsanitize: the handler sees every exception of
 * every thread first, including the access faults with which ASan commits its
 * shadow memory on Windows. An instrumented read here checks shadow that may
 * not be committed yet, faults, and re-enters the handler without end. */
__attribute__((no_sanitize("address", "undefined")))
static LONG CALLBACK sg_handler(EXCEPTION_POINTERS *ep) {
    if (ep->ExceptionRecord->ExceptionCode != EXCEPTION_STACK_OVERFLOW) return EXCEPTION_CONTINUE_SEARCH;
    if (sg_throw) sg_throw(sg_msg);
    static const char m[] = "RangeError: Maximum call stack size exceeded\n";
    fflush(stdout);
    DWORD w;
    WriteFile(GetStdHandle(STD_ERROR_HANDLE), m, sizeof m - 1, &w, NULL);
    ExitProcess(1);
    return EXCEPTION_CONTINUE_SEARCH;
}

/* Windows reserves its own guarantee below the guard page: no band. */
uintptr_t __kml_sg_band_lo;
void __kml_stack_overflow(void) {
    if (sg_throw) sg_throw(sg_msg);
    ExitProcess(1);
}

void __kml_stack_guard_init(void (*throw_fn)(const char *), const char *msg) {
    sg_throw = throw_fn;
    sg_msg = msg;
    ULONG g = SG_GUARANTEE;
    SetThreadStackGuarantee(&g);
    AddVectoredExceptionHandler(1, sg_handler);
}

/* Another thread that runs program code (a Worker): the handler is the
 * process's, but the stack it runs on below the guard page is per thread. */
void __kml_stack_guard_thread(void) {
    ULONG g = SG_GUARANTEE;
    SetThreadStackGuarantee(&g);
}
#endif
