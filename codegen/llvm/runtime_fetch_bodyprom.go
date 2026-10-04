// runtime_fetch_bodyprom.go — TDD-00186 Part B (lazy variant): a Response body
// accessor (text()/json()/arrayBuffer()) returns a pending Promise settled off
// the fetch reactor's completion, so the download overlaps intervening work and
// a JSON parse error becomes a promise rejection — rather than driving the body
// to done at the accessor call site.
//
// A body-promise request is a { ptr pending, ptr closure } entry: `pending` is
// the Response's in-flight fetch handle, `closure` a { fn, env } whose per-kind
// settle runner (emit_fetch.go) builds the value from the now-complete body and
// settles/rejects the promise. Requests park in a thread-local registry the
// curl-drain fires by `pending` on CURLMSG_DONE. The promise + Response are kept
// reachable by the awaiting task's own stack (GC-scanned), so the registry —
// like req-body's — is plain system memory.
package llvm

import _ "embed"

// The registry and its settle/fire routines live in promisesrc/fetchbodyprom.c
// (TDD-00240).
//
//go:embed promisesrc/fetchbodyprom.c
var fetchBodyPromSource string

// FetchBodyPromSource is fetchbodyprom.c, behind kml_layout.h.
func FetchBodyPromSource() string { return layoutHeader() + fetchBodyPromSource }

// UsesFetchBodyProm reports whether the program links fetchbodyprom.c.
func (e *Emitter) UsesFetchBodyProm() bool { return e.usedFetchBodyProm }

func (e *Emitter) ensureFetchBodyProm() {
	if e.usedFetchBodyProm {
		return
	}
	e.usedFetchBodyProm = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureFree()
	e.ensurePromiseRuntime() // the settle runners settle the body promise
	e.ensurePromiseSettle()

	e.emitGlobal(`declare void @__kml_fbp_invoke(ptr)
declare void @__kml_fetch_bodyprom_register(ptr, ptr)
declare void @__kml_fetch_bodyprom_on_done(ptr)`)
}
