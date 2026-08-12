package resolver

import "fmt"

// TDD-00050: real browsers/Node genuinely let a program do `const Math =
// {}` — JS has no reserved words for its own built-in globals. This
// compiler's default is deliberately stricter than that: a binding that
// collides with an ambient global name is a compile error, closing the
// same class of silent-miscompile bug TDD-00049 already fixed for
// import-gated built-ins (fs/path/etc.), but for the names that stay
// ambient by design (Math/JSON/console/process/… — Category A/C in
// TDD-00049's own split). `-globals=permissive` opts back into real JS/
// browser semantics for the names where that's actually cheap to do
// correctly — see the tier split below.
//
// reservedTier1 — plain *ast.Identifier references, resolved at any point
// after parsing (codegen/llvm's dispatch, `e.lookup()`-first-then-fallback
// exactly like `NaN`/`Infinity` already do — see emit_exprs.go). Real
// shadowing is cheap here: `-globals=permissive` lifts these.
var reservedTier1 = map[string]bool{
	"Math": true, "JSON": true, "console": true, "process": true,
	"crypto": true, "performance": true, "Object": true, "Array": true,
	"String": true, "Number": true, "Infinity": true, "NaN": true,
	"parseInt": true, "parseFloat": true, "isNaN": true, "isFinite": true,
	"fetch": true, "btoa": true, "atob": true, "encodeURIComponent": true,
	"decodeURIComponent": true, "encodeURI": true, "decodeURI": true,
	"setTimeout": true, "setInterval": true, "setImmediate": true,
	"clearTimeout": true, "clearInterval": true, "clearImmediate": true,
	"structuredClone": true, "Symbol": true,
}

// reservedTier2 — parser-level `new`-form built-ins (parser/parser_literals.go's
// parseNew): the parser commits to the built-in AST node type (e.g.
// *ast.NewMapExpression) from the bare token text alone, before any
// resolution phase exists at all — `new Map()` can never mean anything but
// the built-in, regardless of a same-named user class. Real shadowing here
// would need the parser to stop making that decision at parse time and
// defer it to the resolver instead — decided directly (not this TDD's
// scope) to leave these reserved under both -globals modes rather than
// take that on. Kept in sync by hand with parseNew's own switch and its
// typedArrayElemKinds map — same maintenance model TDD-00049's
// virtualModuleMembers already established.
var reservedTier2 = map[string]bool{
	"Map": true, "Set": true, "Date": true, "EventEmitter": true,
	"Error": true, "TypeError": true, "RangeError": true, "SyntaxError": true,
	"EvalError": true, "URIError": true, "ReferenceError": true,
	"RegExp": true, "URL": true, "EventSource": true, "WebSocket": true,
	"URLSearchParams": true, "Headers": true, "Request": true,
	"XMLHttpRequest": true, "ArrayBuffer": true, "TextEncoder": true,
	"TextDecoder": true,
	"Int8Array":   true, "Uint8Array": true, "Int16Array": true,
	"Uint16Array": true, "Int32Array": true, "Uint32Array": true,
	"Float32Array": true, "Float64Array": true,
}

// checkReservedBinding returns a non-nil error if name may not be used as a
// binding under allowGlobalShadowing — Tier 2 always rejected, Tier 1
// rejected only when allowGlobalShadowing is false (`-globals=strict`, the
// default). pos is the best available position for the binding (an exact
// per-name position isn't always available — e.g. a destructured pattern
// checks against its enclosing statement's own position — see rename.go's
// call sites).
func checkReservedBinding(name string, line, col int, allowGlobalShadowing bool) error {
	if reservedTier2[name] {
		return fmt.Errorf("%d:%d: '%s' is a reserved built-in name (constructed via `new %s(...)`) — this can never be shadowed, even under -globals=permissive, since the parser resolves `new %s(...)` to the built-in before any scope information exists",
			line, col, name, name, name)
	}
	if reservedTier1[name] && !allowGlobalShadowing {
		return fmt.Errorf("%d:%d: '%s' is a reserved built-in name — pass -globals=permissive to allow shadowing it (matches real JS/browser behavior)",
			line, col, name)
	}
	return nil
}
