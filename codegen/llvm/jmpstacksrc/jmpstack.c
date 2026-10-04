/* jmpstack.c — the stack of setjmp buffers try/catch unwinds through
 * (TDD-00240). A stack is a descriptor of chunks, each allocated when first
 * reached, so a buffer never moves while its frame is live and a deep
 * nesting of try frames (1000 nested AsyncLocalStorage.run calls) needs no
 * fixed bound. The thread's own stack is used while __kml_cur_jmp_stk is
 * null; a coroutine task or a generator swaps in its own descriptor and
 * top. kml_layout.h is prepended by the compiler. */
#include <stdio.h>
#include <stdlib.h>

#define JMP_SLOT 512   /* a jmp_buf on every target, 16-aligned (Win64) */
#define JMP_CHUNK 64   /* slots per chunk */
#define JMP_CHUNKS 256 /* chunks per stack: 16384 nested frames */

typedef struct {
    char *chunk[JMP_CHUNKS];
} kml_jmpstack;

_Thread_local void *__kml_cur_jmp_stk;
_Thread_local int __kml_jmp_top;
static _Thread_local kml_jmpstack own;

static void *chunk_alloc(void) {
#ifdef _WIN32
    return _aligned_malloc(JMP_CHUNK * JMP_SLOT, 16);
#else
    /* malloc is 16-aligned on every 64-bit POSIX target, and under -mm=gc
     * it is the shim's: aligned_alloc would come from the system allocator,
     * and the shim's free cannot release it. */
    return malloc(JMP_CHUNK * JMP_SLOT);
#endif
}

static void chunk_free(void *c) {
#ifdef _WIN32
    _aligned_free(c);
#else
    free(c);
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
