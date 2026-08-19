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

import (
	"KlainMainLang/ast"
	"strings"
)

// substituteAnnotation returns a copy of ta with every bare reference to a type
// parameter in subs replaced by that parameter's concrete argument annotation,
// recursing through every nested annotation. Used to instantiate a generic type
// alias body with concrete arguments before resolving it.
func substituteAnnotation(ta *ast.TypeAnnotation, subs map[string]*ast.TypeAnnotation) *ast.TypeAnnotation {
	if ta == nil {
		return nil
	}
	// A bare parameter reference (optionally with one or more `[]` suffixes),
	// with none of the composite flags/children set.
	if ta.Name != "" && len(ta.TypeArgs) == 0 && ta.ElemType == nil && ta.Fields == nil &&
		!ta.IsStringLiteral && !ta.IsKeyof && !ta.IsIndexedAccess && !ta.IsMapped &&
		!ta.IsConditional && !ta.IsInfer {
		base := ta.Name
		arrays := 0
		for strings.HasSuffix(base, "[]") {
			base = base[:len(base)-2]
			arrays++
		}
		if sub, ok := subs[base]; ok {
			result := sub
			for i := 0; i < arrays; i++ {
				result = &ast.TypeAnnotation{Source: ta.Source, ElemType: result}
			}
			return result
		}
	}
	cp := *ta
	cp.ElemType = substituteAnnotation(ta.ElemType, subs)
	cp.KeyType = substituteAnnotation(ta.KeyType, subs)
	cp.FuncRetType = substituteAnnotation(ta.FuncRetType, subs)
	cp.KeyofOperand = substituteAnnotation(ta.KeyofOperand, subs)
	cp.IndexObject = substituteAnnotation(ta.IndexObject, subs)
	cp.IndexKey = substituteAnnotation(ta.IndexKey, subs)
	cp.MappedSource = substituteAnnotation(ta.MappedSource, subs)
	cp.MappedValue = substituteAnnotation(ta.MappedValue, subs)
	cp.CheckType = substituteAnnotation(ta.CheckType, subs)
	cp.ExtendsType = substituteAnnotation(ta.ExtendsType, subs)
	cp.TrueType = substituteAnnotation(ta.TrueType, subs)
	cp.FalseType = substituteAnnotation(ta.FalseType, subs)
	cp.TypeArgs = substituteAnnotationSlice(ta.TypeArgs, subs)
	cp.UnionMembers = substituteAnnotationSlice(ta.UnionMembers, subs)
	cp.TupleElems = substituteAnnotationSlice(ta.TupleElems, subs)
	cp.IntersectionMembers = substituteAnnotationSlice(ta.IntersectionMembers, subs)
	if ta.Fields != nil {
		cp.Fields = make([]ast.AnnotField, len(ta.Fields))
		for i, f := range ta.Fields {
			cp.Fields[i] = ast.AnnotField{Name: f.Name, Type: substituteAnnotation(f.Type, subs)}
		}
	}
	if ta.FuncParams != nil {
		cp.FuncParams = make([]ast.TypeAnnotation, len(ta.FuncParams))
		for i := range ta.FuncParams {
			cp.FuncParams[i] = *substituteAnnotation(&ta.FuncParams[i], subs)
		}
	}
	return &cp
}

func substituteAnnotationSlice(in []*ast.TypeAnnotation, subs map[string]*ast.TypeAnnotation) []*ast.TypeAnnotation {
	if in == nil {
		return nil
	}
	out := make([]*ast.TypeAnnotation, len(in))
	for i, a := range in {
		out[i] = substituteAnnotation(a, subs)
	}
	return out
}

// resolveConditionalType evaluates `Check extends Extends ? True : False`.
func (e *Emitter) resolveConditionalType(ta *ast.TypeAnnotation) Type {
	checkTy := e.resolveType(ta.CheckType)
	bindings := map[string]Type{}
	if e.matchExtends(checkTy, ta.ExtendsType, bindings) {
		return e.resolveWithInferBindings(ta.TrueType, bindings)
	}
	return e.resolveType(ta.FalseType)
}

// matchExtends reports whether the concrete type `check` satisfies the extends
// pattern, binding any `infer` variables it captures. A bare `infer R` matches
// anything (capturing it); the container patterns Array<infer E>/Promise<infer V>
// match structurally; anything else is a plain assignability test.
func (e *Emitter) matchExtends(check Type, pattern *ast.TypeAnnotation, bindings map[string]Type) bool {
	if pattern == nil {
		return false
	}
	if pattern.IsInfer {
		bindings[pattern.InferName] = check
		return true
	}
	if pattern.ElemType != nil {
		switch pattern.Name {
		case "Array":
			if check.IsArray && check.ElemType != nil {
				return e.matchExtends(*check.ElemType, pattern.ElemType, bindings)
			}
			return false
		case "Promise":
			if check.IsPromise && check.PromiseType != nil {
				return e.matchExtends(*check.PromiseType, pattern.ElemType, bindings)
			}
			return false
		}
	}
	return e.assignable(check, e.resolveType(pattern))
}

// resolveWithInferBindings resolves a conditional's true branch, substituting a
// captured `infer` variable (by name) with its bound type; a variable wrapped
// one level in Array/Promise/Set is handled too.
func (e *Emitter) resolveWithInferBindings(ta *ast.TypeAnnotation, bindings map[string]Type) Type {
	if ta == nil {
		return TypeVoid
	}
	if t, ok := bindings[ta.Name]; ok && ta.ElemType == nil && len(ta.TypeArgs) == 0 {
		return t
	}
	if ta.ElemType != nil {
		switch ta.Name {
		case "Array":
			return ArrayOf(e.resolveWithInferBindings(ta.ElemType, bindings))
		case "Promise":
			return PromiseOf(e.resolveWithInferBindings(ta.ElemType, bindings))
		case "Set":
			return SetType(e.resolveWithInferBindings(ta.ElemType, bindings))
		}
	}
	return e.resolveType(ta)
}

// assignable reports whether a value of type a can be used where b is required —
// the structural test behind a conditional `extends`. Width subtyping for
// objects (a has all of b's fields, assignably), element-wise for arrays,
// IR-equality (with numeric width leniency) for scalars; `any`/`unknown`/a union
// on the right accepts anything, and null/undefined follow nullability.
func (e *Emitter) assignable(a, b Type) bool {
	if b.IsDynamic {
		return true
	}
	if a.IsNull || a.IsUndefined {
		return b.IsNull || b.IsUndefined || b.Nullable
	}
	if b.IsObject {
		if !a.IsObject {
			return false
		}
		for _, bf := range b.Fields {
			found := false
			for _, af := range a.Fields {
				if af.Name == bf.Name && e.assignable(af.Ty, bf.Ty) {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	if b.IsArray {
		return a.IsArray && a.ElemType != nil && b.ElemType != nil && e.assignable(*a.ElemType, *b.ElemType)
	}
	if a.IR == b.IR {
		return true
	}
	return isNumericAssignable(a) && isNumericAssignable(b)
}

func isNumericAssignable(t Type) bool {
	if t.IsObject || t.IsArray || t.IsDynamic || t.IsNull || t.IsUndefined {
		return false
	}
	switch t.IR {
	case "i8", "i16", "i32", "i64", "float", "double":
		return true
	}
	return t.Float
}
