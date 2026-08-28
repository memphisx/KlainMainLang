package llvm

// Version constants surfaced through process.version / process.versions
// (TDD-00136).

const (
	// klainVersion is this compiler's own version — surfaced as
	// process.versions.klain. This constant is the single source of truth (no
	// build-time injection yet). Each release bumps the minor (0.51 → 0.52), so
	// this is set to the next release's version since the change ships in it. A
	// better versioning scheme is planned once the project reaches 1.0.
	klainVersion = "0.52.0"

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
