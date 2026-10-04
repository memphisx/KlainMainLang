/* jmpstack.c — the stack of setjmp buffers try/catch unwinds through
 * (TDD-00240). A stack is a descriptor of chunks, each allocated when first
 * reached, so a buffer never moves while its frame is live and a deep
 * nesting of try frames (1000 nested AsyncLocalStorage.run calls) needs no
 * fixed bound. The thread's own stack is used while __kml_cur_jmp_stk is
 * null; a coroutine task or a generator swaps in its own descriptor and
 * top. kml_layout.h is prepended by the compiler. */
#include <stdio.h>
#include <stdlib.h>
#ifndef _WIN32
#include <sys/mman.h>
#endif

#define JMP_SLOT 512   /* a jmp_buf on every target, 16-aligned (Win64) */
#define JMP_CHUNK 64   /* slots per chunk */
#define JMP_CHUNKS 256 /* chunks per stack: 16384 nested frames */

typedef struct {
    char *chunk[JMP_CHUNKS];
} kml_jmpstack;

_Thread_local void *__kml_cur_jmp_stk;
_Thread_local int __kml_jmp_top;
static _Thread_local kml_jmpstack own;

/* A chunk lives outside the allocator: under -mm=gc, malloc is the
 * collector's, and a thread's own chunks are named only from thread-local
 * storage, which the collector does not scan, so it would free a chunk in
 * use. aligned_alloc stayed outside it but was handed to the shim's free,
 * which cannot release it. A mapping is page-aligned and is released. */
static void *chunk_alloc(void) {
#ifdef _WIN32
    return _aligned_malloc(JMP_CHUNK * JMP_SLOT, 16);
#else
    void *p = mmap(NULL, JMP_CHUNK * JMP_SLOT, PROT_READ | PROT_WRITE, MAP_PRIVATE | MAP_ANONYMOUS, -1, 0);
    return p == MAP_FAILED ? NULL : p;
#endif
}

static void chunk_free(void *c) {
#ifdef _WIN32
    _aligned_free(c);
#else
    munmap(c, JMP_CHUNK * JMP_SLOT);
#endif
}

static kml_jmpstack *cur(void) {
    return __kml_cur_jmp_stk ? (kml_jmpstack *)__kml_cur_jmp_stk : &own;
}

static char *slot(kml_jmpstack *s, int i) {
    int c = i / JMP_CHUNK;
    if (!s->chunk[c]) s->chunk[c] = (char *)chunk_alloc();
    return s->chunk[c] + (i % JMP_CHUNK) * JMP_SLOT;
}

void *__kml_jmp_stack_new(void) { return calloc(1, sizeof(kml_jmpstack)); }

void __kml_jmp_stack_free(void *p) {
    kml_jmpstack *s = (kml_jmpstack *)p;
    if (!s) return;
    for (int c = 0; c < JMP_CHUNKS && s->chunk[c]; c++) chunk_free(s->chunk[c]);
    free(s);
}

/* The next buffer, for the try frame setjmp'ing into it. */
void *__kml_push_jmpbuf(void) {
    int t = __kml_jmp_top;
    if (t >= JMP_CHUNK * JMP_CHUNKS) {
        fflush(stdout);
        fprintf(stderr, "RangeError: Maximum call stack size exceeded\n");
        exit(1);
    }
    __kml_jmp_top = t + 1;
    return slot(cur(), t);
}

void __kml_pop_jmpbuf(void) { __kml_jmp_top--; }

/* The innermost buffer, popped, for a throw to longjmp to. */
void *__kml_jmp_unwind_slot(void) {
    int t = --__kml_jmp_top;
    return slot(cur(), t);
}
