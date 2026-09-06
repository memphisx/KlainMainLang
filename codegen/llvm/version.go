package llvm

// Version constants surfaced through process.version / process.versions
// (TDD-00136), and the compiler's own version (TDD-00179).

// KlainVersion is this compiler's own version — printed by `klainmain
// --version` and baked into every compiled program as
// process.versions.klain. It is a variable, not a constant, because the
// release pipeline stamps it at link time from the release tag:
//
//	go build -ldflags "-X KlainMainLang/codegen/llvm.KlainVersion=1.2.3"
//
// `make build` stamps a `git describe` of the checkout the same way; a plain
// `go build` keeps the default below, which is deliberately not a real
// version so a stray unstamped binary is recognisable. Tests never assert a
// literal value (they read this variable).
var KlainVersion = "0.0.0-dev"

const (
	// nodeCompatVersion / nodeCompatV8 are the Node.js release this compiler's
	// API fidelity is measured against: the Node test/parallel corpus is
	// pinned to it (tools/conformance/fetch.sh NODE_TAG), so it is the honest
	// baseline for what our `process`/`fs`/`http`/… surface targets. Reported
	// verbatim by process.version and process.versions.node/v8 as the
	// compatibility *ceiling* — the version we aim to fully match as coverage
	// approaches 100%. A future --node-compat flag may switch this to a
	// conservative floor (the lowest version whose surface we fully cover);
	// finding that floor is deferred work (TDD-00136). v8 mirrors that Node
	// release's bundled V8 until this compiler ships its own JIT to version.
	nodeCompatVersion = "22.11.0"
	nodeCompatV8      = "12.4.254.21-node.21"
)
