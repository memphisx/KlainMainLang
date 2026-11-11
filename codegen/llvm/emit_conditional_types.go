// emit_conditional_types.go — conditional types, infer, generic type aliases,
// and the structural assignability check (TDD-00079 Stage 3).
//
// Conditional types evaluate at compile time because the check type is concrete
// after generic-alias substitution: `resolveConditionalType` resolves the check
// type, structurally matches it against the extends pattern (binding any `infer`
// variables), and resolves the taken branch. `assignable` is the general "does a
// satisfy b" predicate the extends test needs — the first structural subtype
// check in the compiler (only nominal `implements` and scalar-union checks
// existed before).
//
// V1 scope: the extends pattern is either a concrete type (an assignability
// test) or one of the common `infer` container patterns `Array<infer E>` /
// `Promise<infer V>` / a bare `infer R`; the true branch may be a bound infer
// variable, a concrete type, or such a variable wrapped one level in
// Array/Promise/Set. Deeper infer nesting and function-signature inference
// (`(...) => infer R`) are deferred.
package llvm

// resolveConditionalType evaluates `Check extends Extends ? True : False`.
const (
	maxCondTypeDepth = 16
	maxCondTypeWork  = 1000
	maxCondTypeTotal = 50000
)
