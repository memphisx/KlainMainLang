package llvm

import ()

// ensureConsoleGroupDepth declares the hidden global backing
// console.group()/.groupEnd()'s nesting depth — a single process-wide
// counter (real Node's is per-console-instance, but this compiler has only
// ever had one implicit global console, so there's nothing to distinguish).
func (e *Emitter) ensureConsoleGroupDepth() {
	if e.usedConsoleGroupDepth {
		return
	}
	e.usedConsoleGroupDepth = true
	e.emitGlobal("@__kml_console_group_depth = internal thread_local global i64 0, align 8")
}

// ensureConsoleTimer declares the hidden global backing console.time()/
// .timeEnd() — a single global monotonic-time slot. V1 scope: only one
// timer can be "running" at a time, regardless of how many distinct labels
// are passed to time()/timeEnd() — real Node tracks each label
// independently. A later pass could switch this to the same Map<string,
// number> shape console.count() already uses below, if that scope ever
// actually gets felt as too narrow in practice.
func (e *Emitter) ensureConsoleTimer() {
	if e.usedConsoleTimer {
		return
	}
	e.usedConsoleTimer = true
	e.ensurePerformanceNow()
	e.emitGlobal("@__kml_console_time_start = internal thread_local global double 0.0, align 8")
}

// ensureConsoleCountMap declares the hidden global backing console.count()/
// .countReset() — a lazily-created Map<string, number>, reusing the exact
// same __kml_map_str_* runtime helpers a user-visible Map<string, number>
// already uses (ensureMapStrHelpers), just never exposed as a KML-level
// value. Unlike console.time's single-slot V1 narrowing above, this one
// matches real Node's per-label semantics exactly, since the machinery to
// do so was already sitting right there.
func (e *Emitter) ensureConsoleCountMap() {
	if e.usedConsoleCountMap {
		return
	}
	e.usedConsoleCountMap = true
	e.ensureMapStrHelpers()
	e.emitGlobal("@__kml_console_count_map = internal thread_local global ptr null, align 8")
}

// ensureMapFree declares __kml_map_free: frees a Map<K,V>/Set<T>'s own two
// backing buffers (the keys array and the values array, at fixed offsets 16
// and 24 in the 32-byte map header — the same layout ensureMapStrHelpers/
// ensureMapNumHelpers already create, shared identically by Set since a Set
// is just a Map with unit values under the hood) and then the header
// struct itself. Shallow: does NOT free the individual key/value entries
// themselves (e.g. each string key's own buffer) — only the map's own
// implementation-detail allocations, which the program has no other way to
// reach and free itself.
func (e *Emitter) ensureMapFree() {
	if e.usedMapFree {
		return
	}
	e.usedMapFree = true
	e.ensureFree()
	e.emitGlobal(`
define void @__kml_map_free(ptr %map) {
entry:
  %keys_p = getelementptr i8, ptr %map, i64 16
  %keys = load ptr, ptr %keys_p, align 8
  call void @free(ptr %keys)
  %vals_p = getelementptr i8, ptr %map, i64 24
  %vals = load ptr, ptr %vals_p, align 8
  call void @free(ptr %vals)
  call void @free(ptr %map)
  ret void
}`)
}

// ensureClosureFree declares __kml_closure_free: frees a closure's own two
// allocations — its {funcPtr, envPtr} header struct, and (if any variables
// were captured) the environment struct pointed to by the header's second
// word. Deliberately does NOT free the individual captured-variable cells
// the environment holds pointers to: those cells are heap-promoted
// (ADR-00001) specifically so multiple closures — and the enclosing scope
// itself — can share one mutable binding; freeing a cell here could free
// memory still live and in use elsewhere. Shallow free, same as
// ensureMapFree: only this closure's own two allocations, nothing it merely
// points to.
func (e *Emitter) ensureClosureFree() {
	if e.usedClosureFree {
		return
	}
	e.usedClosureFree = true
	e.ensureFree()
	e.emitGlobal(`
define void @__kml_closure_free(ptr %hdr) {
entry:
  %env_p = getelementptr { ptr, ptr }, ptr %hdr, i32 0, i32 1
  %env = load ptr, ptr %env_p, align 8
  %isnull = icmp eq ptr %env, null
  br i1 %isnull, label %skipenv, label %freeenv

freeenv:
  call void @free(ptr %env)
  br label %skipenv

skipenv:
  call void @free(ptr %hdr)
  ret void
}`)
}
