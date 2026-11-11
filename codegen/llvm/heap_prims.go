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

// allocKind is an allocation's static type id: what a collector needs to
// know about a block at its allocation (TDD-00230 P3.3a `alloc(typeid,
// size)`). Every allocation names one.
type allocKind int

const (
	allocObject    allocKind = iota // a static-layout object, class instance or host struct: holds references
	allocDynObject                  // a dynamic property bag
	allocArray                      // an array header
	allocElems                      // an array's element buffer that can hold references
	allocString                     // a headered string: no references
	allocClosure                    // a closure or its captured environment
	allocCell                       // a boxed variable cell (a captured or escaping local)
	allocHost                       // a host object's state (promise, timer, stream, regex, iterator…)
	allocBytes                      // raw bytes or numbers: no references (typed-array data, scratch buffers)
)

// atomic reports whether a block of kind k never holds a reference, so a
// tracing collector need not scan it.
func (k allocKind) atomic() bool { return k == allocString || k == allocBytes }

// elemAllocKind is the kind of an element buffer of elements of type t:
// plain numbers, booleans and bytes hold no reference.
func elemAllocKind(t Type) allocKind {
	if t.Kind != kindNone || t.Host != hostNone {
		return allocElems
	}
	if t.IR == "i1" || t.IR == "i8" || t.IR == "i16" || t.IR == "i32" || t.IR == "i64" || t.IR == "float" || t.IR == "double" {
		return allocBytes
	}
	return allocElems
}

// arrayDataKind is elemAllocKind of the array type t's element type.
func arrayDataKind(t Type) allocKind {
	if t.ElemType == nil {
		return allocElems
	}
	return elemAllocKind(*t.ElemType)
}

// heapMallocInto emits dst = an uninitialized heap block of kind k and size
// bytes.
func (e *Emitter) heapMallocInto(dst string, k allocKind, size string) {
	e.heapPrim(fmt.Sprintf("%s = call "+irPtr+" @malloc(i64 %s)", dst, size))
	if k == allocCell {
		// A captured binding's cell escapes here: a closure that a later
		// try body calls may write it, and a catch reached through setjmp's
		// second return must reload it rather than reuse a store from before
		// the try (the optimizer sees no call between the two otherwise).
		e.emitInstr(fmt.Sprintf("call void asm sideeffect \"\", \"r,~{memory}\"("+irPtr+" %s)", dst))
	}
}

// heapCallocInto emits dst = a zeroed heap block of kind k and n × size
// bytes.
func (e *Emitter) heapCallocInto(dst string, k allocKind, n, size string) {
	e.heapPrim(fmt.Sprintf("%s = call "+irPtr+" @calloc(i64 %s, i64 %s)", dst, n, size))
}

// heapReallocInto emits dst = the heap block p grown or shrunk to size
// bytes.
func (e *Emitter) heapReallocInto(dst string, k allocKind, p, size string) {
	e.heapPrim(fmt.Sprintf("%s = call "+irPtr+" @realloc("+irPtr+" %s, i64 %s)", dst, p, size))
}

// heapFree emits the explicit free of heap block p (-mm=manual/auto). A
// tracing or moving collector would make it a no-op.
func (e *Emitter) heapFree(k allocKind, p string) {
	e.heapPrim(fmt.Sprintf("call void @free("+irPtr+" %s)", p))
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
	if ty == irPtr || ty == "i64" || strings.HasPrefix(ty, "{") || strings.HasPrefix(ty, "[") {
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
		e.heapPrim(fmt.Sprintf("store %s %s, "+irPtr+" %s", ty, val, addr))
		return
	}
	e.heapPrim(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", ty, val, addr, align))
}

// heapStoreTV emits a store of tv, an IR type and its value as one operand
// (`i64 7`), into the heap slot addr.
func (e *Emitter) heapStoreTV(tv, addr string, align int) {
	e.emitBarrier(storeBarrier(strings.Fields(tv)[0]), addr)
	e.heapPrim(fmt.Sprintf("store %s, "+irPtr+" %s, align %d", tv, addr, align))
}

// heapStoreAtomic emits a sequentially consistent atomic store into the heap
// slot addr.
func (e *Emitter) heapStoreAtomic(ty, val, addr string, align int) {
	e.emitBarrier(storeBarrier(ty), addr)
	e.heapPrim(fmt.Sprintf("store atomic %s %s, "+irPtr+" %s seq_cst, align %d", ty, val, addr, align))
}

// heapAllocCall emits dst = a heap block from a named allocator call, the
// text after `call ptr @` (`kml_gc_alloc_pinned(i64 16)`).
func (e *Emitter) heapAllocCall(dst string, k allocKind, call string) {
	e.heapPrim(fmt.Sprintf("%s = call "+irPtr+" @%s", dst, call))
}

// heapMallocAt is heapMallocInto, emitted in the entry block when entry is
// set (a cell every path through the function sees).
func (e *Emitter) heapMallocAt(entry bool, dst string, k allocKind, size string) {
	if !entry {
		e.heapMallocInto(dst, k, size)
		return
	}
	e.emitAlloca(fmt.Sprintf("%s = call "+irPtr+" @malloc(i64 %s)", dst, size))
}

// heapStoreAt is heapStore, emitted in the entry block when entry is set.
func (e *Emitter) heapStoreAt(entry bool, ty, val, addr string, align int) {
	if !entry {
		e.heapStore(ty, val, addr, align)
		return
	}
	e.emitAlloca(fmt.Sprintf("store %s %s, "+irPtr+" %s, align %d", ty, val, addr, align))
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
