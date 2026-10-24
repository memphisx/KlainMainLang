package llvm

import (
	"fmt"
	"strings"
)

// heap_prims.go — the one allocation path and the one heap store path
// (TDD-00230 P3.3a). Every allocation codegen emits and every store into a
// heap object goes through these, so a collector's allocation fast path and
// a write barrier each land here rather than across the emit sites.
// emitInstr rejects an allocation or a heap store emitted any other way.

// heapMallocInto emits dst = an uninitialized heap block of size bytes.
func (e *Emitter) heapMallocInto(dst, size string) {
	e.heapPrim(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", dst, size))
}

// heapCallocInto emits dst = a zeroed heap block of n × size bytes.
func (e *Emitter) heapCallocInto(dst, n, size string) {
	e.heapPrim(fmt.Sprintf("%s = call ptr @calloc(i64 %s, i64 %s)", dst, n, size))
}

// heapReallocInto emits dst = the heap block p grown or shrunk to size
// bytes.
func (e *Emitter) heapReallocInto(dst, p, size string) {
	e.heapPrim(fmt.Sprintf("%s = call ptr @realloc(ptr %s, i64 %s)", dst, p, size))
}

// heapFree emits the explicit free of heap block p (-mm=manual/auto). A
// tracing or moving collector would make it a no-op.
func (e *Emitter) heapFree(p string) {
	e.heapPrim(fmt.Sprintf("call void @free(ptr %s)", p))
}

// heapBarrier is a store's static write-barrier kind.
type heapBarrier int

const (
	barrierNone    heapBarrier = iota // the stored value holds no heap pointer
	barrierPointer                    // a pointer or a boxed word: a collector's barrier goes here
	barrierRC                         // a reference-count cluster's member slot (-mm=auto, TDD-00175)
)

// storeBarrier is the barrier kind of a store of IR type ty: only a ptr or
// an i64 (a boxed word may carry a pointer) can hold a heap reference.
func storeBarrier(ty string) heapBarrier {
	if ty == "ptr" || ty == "i64" || strings.HasPrefix(ty, "{") || strings.HasPrefix(ty, "[") {
		return barrierPointer
	}
	return barrierNone
}

// heapStore emits a store of val (of IR type ty) into the heap slot addr.
// align 0 leaves the alignment to the type's. Its barrier is the type's
// (storeBarrier); no collector needs one yet, so none is emitted.
func (e *Emitter) heapStore(ty, val, addr string, align int) {
	e.emitBarrier(storeBarrier(ty), addr)
	if align == 0 {
		e.heapPrim(fmt.Sprintf("store %s %s, ptr %s", ty, val, addr))
		return
	}
	e.heapPrim(fmt.Sprintf("store %s %s, ptr %s, align %d", ty, val, addr, align))
}

// heapStoreTV emits a store of tv, an IR type and its value as one operand
// (`i64 7`), into the heap slot addr.
func (e *Emitter) heapStoreTV(tv, addr string, align int) {
	e.emitBarrier(storeBarrier(strings.Fields(tv)[0]), addr)
	e.heapPrim(fmt.Sprintf("store %s, ptr %s, align %d", tv, addr, align))
}

// heapStoreAtomic emits a sequentially consistent atomic store into the heap
// slot addr.
func (e *Emitter) heapStoreAtomic(ty, val, addr string, align int) {
	e.emitBarrier(storeBarrier(ty), addr)
	e.heapPrim(fmt.Sprintf("store atomic %s %s, ptr %s seq_cst, align %d", ty, val, addr, align))
}

// heapAllocCall emits dst = a heap block from a named allocator call, the
// text after `call ptr @` (`kml_gc_alloc_pinned(i64 16)`).
func (e *Emitter) heapAllocCall(dst, call string) {
	e.heapPrim(fmt.Sprintf("%s = call ptr @%s", dst, call))
}

// heapMallocAt is heapMallocInto, emitted in the entry block when entry is
// set (a cell every path through the function sees).
func (e *Emitter) heapMallocAt(entry bool, dst, size string) {
	if !entry {
		e.heapMallocInto(dst, size)
		return
	}
	e.emitAlloca(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", dst, size))
}

// heapStoreAt is heapStore, emitted in the entry block when entry is set.
func (e *Emitter) heapStoreAt(entry bool, ty, val, addr string, align int) {
	if !entry {
		e.heapStore(ty, val, addr, align)
		return
	}
	e.emitAlloca(fmt.Sprintf("store %s %s, ptr %s, align %d", ty, val, addr, align))
}

// emitBarrier emits the write barrier of kind k before a store into addr.
// Under every memory model today it is empty: malloc, Boehm (non-moving,
// non-generational) and -mm=auto's scope frees need none. A generational
// collector's card mark, or a cluster's count adjustment, goes here.
func (e *Emitter) emitBarrier(k heapBarrier, addr string) {}

// heapPrim emits one primitive's instruction.
func (e *Emitter) heapPrim(line string) {
	e.inHeapPrim = true
	e.emitInstr(line)
	e.inHeapPrim = false
}
