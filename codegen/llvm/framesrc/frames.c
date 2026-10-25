/* frames.c — the shadow call stack an Error's `stack` reads (TDD-00188).
 * Each user function's frame record lives in its own stack frame, linked
 * through __kml_frame_top: { previous, descriptor, position }. The
 * descriptor is a constant { name, file URL }; an empty name is a frame
 * Node prints as its location alone (the module, an anonymous function), a
 * null descriptor a fiber's base, which prints nothing. The position is the
 * call or construction in progress, line << 32 | column. A return restores
 * the previous top, a throw the top its try saved (jmpstack.c), and a fiber
 * switch its own (KML_SWAPCONTEXT, kml_heap.h). kml_layout.h is prepended
 * by the compiler. */
#include <stdio.h>
#include <string.h>
#include <stdint.h>
typedef int64_t i64;

/* A frame's descriptor; one with no file is a method call's marker, below
 * its callee's frame: the receiver's type (name) and the property (prop)
 * the callee is called as. */
typedef struct {
    const char *name;
    const char *file;
    const char *prop;
} kml_fdesc;

typedef struct kml_frame {
    struct kml_frame *prev;
    const kml_fdesc *desc;
    i64 pos;
} kml_frame;

_Thread_local void *__kml_frame_top;
_Thread_local void *__kml_frame_base;

/* A switched-in side's top and base back, and its base cut from the frames
 * of whoever resumed it (KML_SWAPCONTEXT). Out of line, so the thread-locals
 * are those of the thread it now runs on. */
__attribute__((noinline)) void __kml_frame_resume(void *top, void *base) {
    __kml_frame_top = top;
    __kml_frame_base = base;
    if (base) *(void **)base = 0;
}

/* The thread-locals' addresses on the calling thread, for code a klain:sync
 * goroutine runs: each access asks anew, as it may have moved threads since
 * the last (a safepoint, a channel operation). */
__attribute__((noinline)) void **__kml_frame_top_slot(void) { return &__kml_frame_top; }
__attribute__((noinline)) void **__kml_frame_base_slot(void) { return &__kml_frame_base; }

/* Error.stackTraceLimit's default. */
#define KML_STACK_LIMIT 10

/* Error.stackTraceLimit: the frames a stack keeps, per thread as V8 keeps
 * it per isolate. */
_Thread_local double __kml_stack_limit = KML_STACK_LIMIT;
double __kml_stack_limit_get(void) { return __kml_stack_limit; }
void __kml_stack_limit_set(double v) { __kml_stack_limit = v; }

static int stack_limit(void) {
    double v = __kml_stack_limit;
    if (!(v > 0)) return 0; /* NaN, zero, negative */
    return v > 1000000 ? 1000000 : (int)v;
}

static i64 str_len(const char *s) { return KML_STR_LEN(s); }

/* A heap string of len bytes, its length word ahead of it. */
static char *heap_str(const char *bytes, size_t len) {
    char *base = (char *)kml_alloc(KML_HEAP_RAW, len + 9);
    *(i64 *)base = (i64)len;
    memcpy(base + 8, bytes, len);
    base[8 + len] = 0;
    return base + 8;
}

/* A fiber switched out: its top kept in its base frame (KML_SWAPCONTEXT's
 * first step, for the IR switches). */
void __kml_frame_park(void *base, void *top) {
    if (base) ((void **)base)[2] = top;
}

extern _Thread_local void *__kml_current_task;
/* The task awaiting a promise: task.c's, set when it registers a task. */
void *(*__kml_task_awaiting_hook)(void *p);

/* The frame of a switched-out task's own function: the one just above its
 * base frame. */
static kml_frame *task_own_frame(void *task) {
    void **base = *(void ***)((char *)task + KML_TASK_FRAME_BASE);
    if (!base) return 0;
    kml_frame *own = 0;
    for (kml_frame *f = (kml_frame *)base[2]; f && (void *)f != (void *)base; f = f->prev)
        if (f->desc && f->desc->file) own = f;
    return own;
}

static int put_frame(char *buf, size_t cap, const char *async, kml_frame *f) {
    int line = (int)(f->pos >> 32), col = (int)(f->pos & 0xffffffff);
    kml_frame *m = f->prev;
    if (m && m->desc && !m->desc->file) {
        /* Called as a method: V8's `Type.name [as prop]`. */
        const char *n = f->desc->name, *t = m->desc->name, *p = m->desc->prop;
        size_t tl = strlen(t);
        if (strncmp(n, t, tl) == 0 && n[tl] == '.') n += tl + 1; /* named for it already */
        if (!n[0] || strcmp(n, p) == 0)
            return snprintf(buf, cap, "\n    at %s%s.%s (%s:%d:%d)", async, t, p, f->desc->file, line, col);
        return snprintf(buf, cap, "\n    at %s%s.%s [as %s] (%s:%d:%d)", async, t, n, p, f->desc->file, line, col);
    }
    if (f->desc->name[0])
        return snprintf(buf, cap, "\n    at %s%s (%s:%d:%d)", async, f->desc->name, f->desc->file, line, col);
    return snprintf(buf, cap, "\n    at %s%s:%d:%d", async, f->desc->file, line, col);
}

/* The frame lines of the current stack, each "\n    at name (file:line:col)"
 * or "\n    at file:line:col", at most KML_STACK_LIMIT; null for none. */
/* The guard page a stack overflow faulted on (stackguard.c): the record of
 * the call that overflowed may lie in it, unreadable, so a capture starts
 * above it. */
static _Thread_local uintptr_t fault_lo, fault_hi;

void __kml_frames_fault_page(uintptr_t lo, uintptr_t hi) {
    fault_lo = lo;
    fault_hi = hi;
}

static int frame_unreadable(const kml_frame *f) {
    uintptr_t a = (uintptr_t)f;
    return fault_hi && a + sizeof *f > fault_lo && a < fault_hi;
}

static char *capture_from(kml_frame *start) {
    char buf[16384];
    size_t n = 0;
    int count = 0, limit = stack_limit();
    kml_frame *last = 0;
    if (start && frame_unreadable(start)) return 0; /* its chain is unreachable */
    for (kml_frame *f = start; f && count < limit && !frame_unreadable(f); f = f->prev) {
        last = f;
        if (!f->desc || !f->desc->file) continue;
        int w = put_frame(buf + n, sizeof buf - n, "", f);
        if (w < 0 || (size_t)w >= sizeof buf - n) break;
        n += (size_t)w;
        count++;
    }
    /* A task resumed after an await: its caller's frames are gone, and as
     * V8 does the stack goes on with the async functions awaiting it, each
     * at its await. */
    void *t = __kml_current_task;
    if (t && last && (void *)last == __kml_frame_base && !last->prev) {
        for (int hops = 0; t && count < limit && hops < 64; hops++) {
            void *p = *(void **)((char *)t + KML_TASK_PROMISE);
            void *w = p && __kml_task_awaiting_hook ? __kml_task_awaiting_hook(p) : 0;
            kml_frame *f = w ? task_own_frame(w) : 0;
            if (!f) break;
            int k = put_frame(buf + n, sizeof buf - n, "async ", f);
            if (k < 0 || (size_t)k >= sizeof buf - n) break;
            n += (size_t)k;
            count++;
            t = w;
        }
    }
    return n ? heap_str(buf, n) : 0;
}

char *__kml_frames_capture(void) { return capture_from((kml_frame *)__kml_frame_top); }

/* Whether frame f is of the function named name: its own name, its
 * constructor's (`new name`), or a method's (`Type.name`). */
static int frame_is(kml_frame *f, const char *name, size_t nl) {
    const char *n = f->desc->name;
    if (strlen(n) == nl && memcmp(n, name, nl) == 0) return 1;
    if (strncmp(n, "new ", 4) == 0 && strlen(n + 4) == nl && memcmp(n + 4, name, nl) == 0) return 1;
    const char *dot = strrchr(n, '.');
    return dot && strlen(dot + 1) == nl && memcmp(dot + 1, name, nl) == 0;
}

/* Error.captureStackTrace's frames: the current ones, or with a
 * constructorOpt named name, those below its topmost frame (none when it
 * is not on the stack). An empty string for none. */
char *__kml_native_capture_frames(const char *name) {
    size_t nl = name ? (size_t)str_len(name) : 0;
    kml_frame *start = (kml_frame *)__kml_frame_top;
    if (nl) {
        kml_frame *f = start;
        while (f && !(f->desc && f->desc->file && frame_is(f, name, nl))) f = f->prev;
        start = f ? f->prev : 0;
    }
    char *r = start ? capture_from(start) : 0;
    return r ? r : heap_str("", 0);
}

/* An error's `stack`: as assigned, or else its header — the name, then the
 * message when there is one — read now, as V8 formats it on first read,
 * over the frames captured when it was made. */
void *__kml_error_stack(void *err) {
    char *o = (char *)err;
    void *set = *(void **)(o + KML_ERR_STACK);
    if (set) return set;
    const char *name = *(const char **)(o + KML_ERR_NAME);
    const char *msg = *(const char **)(o + KML_ERR_MESSAGE);
    const char *frames = *(const char **)(o + KML_ERR_FRAMES);
    size_t nl = name ? (size_t)str_len(name) : 5, ml = msg ? (size_t)str_len(msg) : 0;
    size_t fl = frames ? (size_t)str_len(frames) : 0;
    size_t len = nl + (ml ? 2 + ml : 0) + fl;
    char *base = (char *)kml_alloc(KML_HEAP_RAW, len + 9);
    *(i64 *)base = (i64)len;
    char *p = base + 8;
    memcpy(p, name ? name : "Error", nl);
    p += nl;
    if (ml) {
        memcpy(p, ": ", 2);
        memcpy(p + 2, msg, ml);
        p += 2 + ml;
    }
    if (fl) memcpy(p, frames, fl);
    base[8 + len] = 0;
    *(void **)(o + KML_ERR_STACK) = base + 8;
    return base + 8;
}

/* A new error's frames, captured where it is made. */
void __kml_error_capture(void *err) {
    *(void **)((char *)err + KML_ERR_FRAMES) = __kml_frames_capture();
}

/* A new error's fields (an Error's own, not a subclass's): its kind word,
 * message and name, every other field unset (null, 0, a cause of
 * undefined, its name the prototype's, no stack yet), and the frames where
 * it is made. */
void __kml_error_init(void *err, i64 kind, void *msg, void *name) {
    char *o = (char *)err;
    memset(o + 8, 0, KML_ERR_SIZE - 8);
    *(i64 *)o = kind;
    *(void **)(o + KML_ERR_MESSAGE) = msg;
    *(void **)(o + KML_ERR_NAME) = name;
    *(i64 *)(o + KML_ERR_CAUSE) = 10; /* undefined */
    __kml_error_capture(err);
}

/* A fiber resumed after a switch: its base frame cut from its creator's,
 * which may be gone (KML_SWAPCONTEXT's last step, for the IR switches). */
void __kml_frame_cut(void *base) {
    if (base) ((kml_frame *)base)->prev = 0;
}

/* A context switch that keeps each side's shadow call stack, for the
 * runtime written in IR. */
extern int swapcontext(void *, void *);
void __kml_swapcontext_kept(void *from, void *to) { KML_SWAPCONTEXT(from, to); }

/* console.trace's frames, after its message: one line each on stderr. */
void __kml_trace_frames(void) {
    char *f = __kml_frames_capture();
    if (!f) return;
    fwrite(f + 1, 1, (size_t)str_len(f) - 1, stderr);
    fputc('\n', stderr);
}

/* The frames an error's inspection prints: those captured, unless its stack
 * was assigned (then the header alone, as before). */
const char *__kml_error_frames_shown(void *o) {
    if (*(void **)((char *)o + KML_ERR_STACK)) return 0;
    return *(const char **)((char *)o + KML_ERR_FRAMES);
}

/* An error's stack once it is a string (assigned, or formatted by a read),
 * else null. */
const char *__kml_error_stack_assigned(void *o) {
    return *(const char **)((char *)o + KML_ERR_STACK);
}
