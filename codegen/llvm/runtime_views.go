package llvm

import (
	_ "embed"
)

// A typed array's buffer and byteOffset (TDD-00243) live in viewsrc/views.c:
// a registry from a view's data address to its root allocation and offset.

//go:embed viewsrc/views.c
var viewsSource string

// ViewsSource is the view registry's C source; under -mm=gc entries are
// unscanned and their buffer links disappearing (KML_GC).
func (e *Emitter) ViewsSource() string {
	return layoutHeader() + viewsSource
}

// ensureBoxedViews makes a boxed typed array's `buffer` and `byteOffset`
// readable through `any`: the ArrayBuffer host types are registered, and
// views.c boxes a buffer at its header word.
func (e *Emitter) ensureBoxedViews() {
	if e.usedBoxedViews {
		return
	}
	e.usedBoxedViews = true
	e.ensureHostBoxHooks()
	// views.c boxes a buffer by its header word; register both types so
	// their inspection and instanceof rows exist.
	e.hostID(ArrayBufferType())
	e.hostID(SharedArrayBufferType())
}
