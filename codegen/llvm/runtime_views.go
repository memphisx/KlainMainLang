package llvm

import (
	_ "embed"
	"fmt"
)

// A typed array's buffer and byteOffset (TDD-00243) live in viewsrc/views.c:
// a registry from a view's data address to its root allocation and offset.

//go:embed viewsrc/views.c
var viewsSource string

// ViewsSource is the view registry's C source; under -mm=gc entries are
// unscanned and their buffer links disappearing (KML_GC).
func (e *Emitter) ViewsSource() string {
	if e.isGCMode() {
		return "#define KML_GC 1\n" + viewsSource
	}
	return viewsSource
}

// UsesViews reports whether the program links views.c.
func (e *Emitter) UsesViews() bool { return e.usedViews }

// ensureViews declares the view registry (views.c) once.
func (e *Emitter) ensureViews() {
	if e.usedViews {
		return
	}
	e.usedViews = true
	e.emitGlobal(`declare void @__kml_view_register(ptr, ptr, i64, i64)
declare void @__kml_view_derive(ptr, i64, ptr)
declare i64 @__kml_view_offset(ptr)
declare ptr @__kml_view_buffer(ptr, i64)`)
}

// ensureBoxedViews makes a boxed typed array's `buffer` and `byteOffset`
// readable through `any`: the ArrayBuffer host type is registered, and its
// header word is exported for views.c to box the buffer with.
func (e *Emitter) ensureBoxedViews() {
	if e.usedBoxedViews {
		return
	}
	e.usedBoxedViews = true
	e.ensureViews()
	e.ensureMalloc()
	e.ensureHostBoxHooks()
	id := e.hostID(ArrayBufferType())
	e.emitGlobal(fmt.Sprintf("@__kml_view_ab_header = constant i64 %d", id))
	sid := e.hostID(SharedArrayBufferType())
	e.emitGlobal(fmt.Sprintf("@__kml_view_sab_header = constant i64 %d", sid))
	e.emitGlobal("declare i64 @__kml_view_buffer_any(ptr, i64)")
}

// dynJSONViewFlags compiles dynjson's `buffer`/`byteOffset` reads of a boxed
// typed array, and views.c's boxing of the buffer, when one is boxed.
func (e *Emitter) dynJSONViewFlags() []string {
	if e.usedBoxedViews {
		return []string{"-DKML_VIEWS", "-DKML_BOXED_VIEWS"}
	}
	return nil
}
