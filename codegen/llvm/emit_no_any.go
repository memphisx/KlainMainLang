package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emit_no_any.go — the --no-any flag (TDD-00209). When active (strict lane
// only), the any/unknown dynamic escape hatch is banned: every explicit
// any/unknown type annotation is a compile error, and the evolving-any widening
// (a null-initialized untyped binding/field later reassigned) is disabled so
// such a binding falls back to the ordinary strict cross-type rejection. The
// produced binary is then provably fully static — no NaN-boxing, no runtime
// type tags, no dynamic dispatch.
//
// Detection hooks into resolveType (the single entry point every user type
// annotation flows through), which returns a Type with no error channel — so the
// first violation is recorded here and surfaced by EmitProgram. Position info is
// not carried on a TypeAnnotation; the message names the offending type and the
// remedy instead (a precise source location is a documented follow-up).

// typeAnnotMentionsBareAny reports whether a type annotation is — or nests — a
// bare any/unknown (not a constrained union, which is a statically member-set-
// checked type, not the escape hatch). Recurses every child annotation.
func typeAnnotMentionsBareAny(ta *ast.TypeAnnotation) bool {
	if ta == nil {
		return false
	}
	// The `T[]` shorthand may arrive as a Name with trailing "[]" (e.g. "any[]",
	// "any[][]") rather than an ElemType child — strip them before comparing.
	base := ta.Name
	for len(base) >= 2 && base[len(base)-2:] == "[]" {
		base = base[:len(base)-2]
	}
	if len(ta.UnionMembers) == 0 && (base == "any" || base == "unknown") {
		return true
	}
	kids := []*ast.TypeAnnotation{
		ta.ElemType, ta.KeyType, ta.FuncRetType, ta.KeyofOperand,
		ta.IndexObject, ta.IndexKey, ta.MappedSource, ta.MappedValue,
		ta.CheckType, ta.ExtendsType, ta.TrueType, ta.FalseType,
	}
	for _, k := range kids {
		if typeAnnotMentionsBareAny(k) {
			return true
		}
	}
	for i := range ta.FuncParams {
		if typeAnnotMentionsBareAny(&ta.FuncParams[i]) {
			return true
		}
	}
	for _, list := range [][]*ast.TypeAnnotation{
		ta.TypeArgs, ta.UnionMembers, ta.TupleElems, ta.IntersectionMembers,
	} {
		for _, k := range list {
			if typeAnnotMentionsBareAny(k) {
				return true
			}
		}
	}
	for i := range ta.Fields {
		if typeAnnotMentionsBareAny(ta.Fields[i].Type) {
			return true
		}
	}
	return false
}

// checkNoAnyAnnotation records the first --no-any violation seen for a resolved
// type annotation. A no-op unless --no-any is active. Called from resolveType.
func (e *Emitter) checkNoAnyAnnotation(ta *ast.TypeAnnotation) {
	if !e.noAny || e.noAnyErr != nil || ta == nil {
		return
	}
	if typeAnnotMentionsBareAny(ta) {
		e.noAnyErr = fmt.Errorf("the 'any'/'unknown' type is banned under --no-any — annotate a concrete type (or drop --no-any, or use -compat=js): this compiler was asked to reject every dynamic escape hatch")
	}
}
