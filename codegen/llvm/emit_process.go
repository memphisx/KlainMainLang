// emit_process.go — process.argv, process.exit(code), process.env.KEY / process.env["KEY"],
// and the property reads still compiled directly.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// isProcessEnvExpr reports whether expr is exactly `process.env` (non-optional).
func (e *Emitter) isProcessEnvExpr(expr ast.Expression) bool {
	mem, ok := expr.(*ast.MemberExpression)
	if !ok || mem.Optional || mem.Property != "env" {
		return false
	}
	id, ok := mem.Object.(*ast.Identifier)
	return ok && id.Name == "process" && !e.isShadowedByLocal(id.Name)
}

// emitProcessNextTick implements process.nextTick(fn): enqueue fn onto the
// tick queue, which runs ahead of the promise jobs (drained after the current
// synchronous run, before timers).
// V1 accepts a zero-argument callback only (Node forwards extra args to fn).
func (e *Emitter) emitProcessNextTick(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: process.nextTick takes exactly 1 argument (a () => void callback)", pos.Line, pos.Col)
	}
	cbPtr, err := e.timerCallbackPtr(args[0], "process.nextTick", pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureMicrotasks()
	e.emitInstr(fmt.Sprintf("call void @__kml_nexttick_enqueue(ptr %s)", cbPtr))
	return Value{Ty: TypeVoid}, nil
}

// emitGetenvCall calls C getenv() on the given key pointer. The result is
// `string | undefined` (TDD-00187 Stage 3): a missing variable is the null
// pointer with the static type flagged Nullable|IsUndefined, so it prints and
// compares as `undefined` and strict mode gates bare-string use.
func (e *Emitter) emitGetenvCall(keyPtr string) Value {
	e.ensureGetenv()
	e.ensureStrHeaderRuntime()
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @getenv(ptr %s)", raw, keyPtr))
	// TDD-00120: getenv returns a foreign pointer into the environ block with no
	// length header — copy it into a length-prefixed string (null stays null).
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", result, raw))
	return Value{Ref: result, Ty: undefinedableElem(TypePtr)}
}

// memoryUsageType is NodeJS.MemoryUsage: byte counts, numbers.
func memoryUsageType() Type {
	return ObjectType([]Field{
		{Name: "rss", Ty: TypeF64},
		{Name: "heapTotal", Ty: TypeF64},
		{Name: "heapUsed", Ty: TypeF64},
		{Name: "external", Ty: TypeF64},
		{Name: "arrayBuffers", Ty: TypeF64},
	})
}

// UsesHeapStats reports whether process.memoryUsage()'s heapTotal/heapUsed
// shim (ProcMemSource) needs linking.
func (e *Emitter) UsesHeapStats() bool { return e.usedHeapStats }

// ProcMemSource is the heap-accounting shim behind process.memoryUsage()'s
// heapTotal/heapUsed. It reports this compiler's object heap: Boehm's collected
// heap under -mm=gc (KLAIN_GC), else the platform C allocator's own arena
// (Darwin's malloc_zone_statistics, glibc's mallinfo2). Every struct here is
// read against the platform's own headers so no allocator-internal layout is
// reproduced in IR; an unrecognised platform returns 0 (the prior behaviour).
func ProcMemSource() string {
	return `#include <stddef.h>
#include <stdint.h>

#if defined(KLAIN_GC)
#include <gc.h>
uint64_t __kml_heap_total_bytes(void) { return (uint64_t)GC_get_heap_size(); }
uint64_t __kml_heap_used_bytes(void) {
  return (uint64_t)(GC_get_heap_size() - GC_get_free_bytes());
}
#elif defined(__APPLE__)
#include <malloc/malloc.h>
uint64_t __kml_heap_total_bytes(void) {
  malloc_statistics_t s;
  malloc_zone_statistics(malloc_default_zone(), &s);
  return (uint64_t)s.size_allocated;
}
uint64_t __kml_heap_used_bytes(void) {
  malloc_statistics_t s;
  malloc_zone_statistics(malloc_default_zone(), &s);
  return (uint64_t)s.size_in_use;
}
#elif defined(__GLIBC__)
#include <malloc.h>
#if defined(__GLIBC_PREREQ) && __GLIBC_PREREQ(2, 33)
uint64_t __kml_heap_total_bytes(void) {
  struct mallinfo2 m = mallinfo2();
  return (uint64_t)(m.arena + m.hblkhd);
}
uint64_t __kml_heap_used_bytes(void) { return (uint64_t)mallinfo2().uordblks; }
#else
uint64_t __kml_heap_total_bytes(void) {
  struct mallinfo m = mallinfo();
  return (uint64_t)((unsigned)m.arena + (unsigned)m.hblkhd);
}
uint64_t __kml_heap_used_bytes(void) { return (uint64_t)(unsigned)mallinfo().uordblks; }
#endif
#elif defined(_WIN32)
#include <malloc.h>
// The UCRT has no mallinfo; _heapwalk sums the C runtime heap the same way the
// POSIX arena queries do — used = live blocks, total = used + free. The same
// native reinterpretation as the other platforms (not V8's number), and
// directionally correct: it tracks growth and leaks in this compiler's own heap.
static void __kml_win_heap(uint64_t *total, uint64_t *used) {
  uint64_t u = 0, f = 0;
  _HEAPINFO hi;
  hi._pentry = NULL;
  int rc;
  while ((rc = _heapwalk(&hi)) == _HEAPOK) {
    if (hi._useflag == _USEDENTRY) u += (uint64_t)hi._size;
    else f += (uint64_t)hi._size;
  }
  *used = u;
  *total = u + f;
}
uint64_t __kml_heap_total_bytes(void) { uint64_t t, u; __kml_win_heap(&t, &u); return t; }
uint64_t __kml_heap_used_bytes(void) { uint64_t t, u; __kml_win_heap(&t, &u); return u; }
#else
uint64_t __kml_heap_total_bytes(void) { return 0; }
uint64_t __kml_heap_used_bytes(void) { return 0; }
#endif
`
}
