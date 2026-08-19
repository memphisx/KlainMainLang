// emit_mapped_types.go — keyof, indexed access, and general mapped types
// (TDD-00079 Stage 2).
//
// All three evaluate at resolveType to concrete types, the same as the utility
// types: `keyof T` and `T[K]` read a concrete object T's field set/field types,
// and `{ [K in Source]: V }` rebuilds a concrete ObjectType by mapping V over the
// source keys. No runtime representation of their own.
//
// V1 scope: the mapped value body V is either `T[K]` (homomorphic — the field's
// own type), a bare `K` (→ string), a concrete type not referencing K (uniform),
// or `T[K]` wrapped one level in Array/Promise/Set. Key remapping (`as`), the
// modifier-removal forms (`-?`/`-readonly`), and deeper `T[K]` nesting are
// deferred. The `?`/`readonly` modifiers are accepted but near-no-ops in the
// current object model (fields already omittable, no mutation checking).
package llvm

import "KlainMainLang/ast"

// resolveKeyof resolves `keyof T` as a standalone value type: the union of T's
// string keys, which this compiler treats as `string`. Its structural use as a
// mapped source is handled directly by resolveMappedType (which reads the key
// set), not through this.
func (e *Emitter) resolveKeyof(ta *ast.TypeAnnotation) Type {
	return TypePtr
}

// resolveIndexedAccess resolves a standalone `T["name"]` to the named field's
// type. A non-literal key (a bare mapped variable used outside expansion) or an
// unknown key falls back to the array element type when T is array-like, else
// i64 — the same lenient shape resolveType uses for other unknowns.
func (e *Emitter) resolveIndexedAccess(ta *ast.TypeAnnotation) Type {
	obj := e.resolveType(ta.IndexObject)
	if ta.IndexKey != nil && ta.IndexKey.IsStringLiteral {
		for _, f := range obj.Fields {
			if f.Name == ta.IndexKey.LiteralValue {
				return f.Ty
			}
		}
	}
	if obj.IsArray && obj.ElemType != nil {
		return *obj.ElemType
	}
	return TypeI64
}

// resolveMappedType evaluates `{ [K in Source]: V }` to a concrete ObjectType.
func (e *Emitter) resolveMappedType(ta *ast.TypeAnnotation) Type {
	src := ta.MappedSource
	type keyEntry struct {
		name     string
		fieldTy  Type
		hasField bool
	}
	var entries []keyEntry
	switch {
	case src != nil && src.IsKeyof:
		// Homomorphic over T's fields — the common `keyof T` source.
		obj := e.resolveType(src.KeyofOperand)
		for _, f := range obj.Fields {
			entries = append(entries, keyEntry{f.Name, f.Ty, true})
		}
	default:
		// A string-literal-union source (`{ [K in "a" | "b"]: V }`) — no source
		// object, so T[K] has no field to draw from.
		keys, ok := collectStringLiteralKeys(src)
		if !ok {
			return ObjectType(nil) // unsupported source in V1
		}
		for _, k := range keys {
			entries = append(entries, keyEntry{k, Type{}, false})
		}
	}
	fields := make([]Field, 0, len(entries))
	for _, ke := range entries {
		fields = append(fields, Field{
			Name: ke.name,
			Ty:   e.resolveMappedValue(ta.MappedValue, ta.MappedKeyVar, ke.fieldTy, ke.hasField),
		})
	}
	return ObjectType(fields)
}

// resolveMappedValue resolves the mapped value body for one key (see file scope).
func (e *Emitter) resolveMappedValue(v *ast.TypeAnnotation, keyVar string, fieldTy Type, hasField bool) Type {
	if v == nil {
		return TypeI64
	}
	// T[K]: the field's own type (homomorphic).
	if v.IsIndexedAccess && v.IndexKey != nil && v.IndexKey.Name == keyVar {
		if hasField {
			return fieldTy
		}
		return TypePtr
	}
	// Bare K as a value type → string.
	if v.Name == keyVar && !v.IsStringLiteral && !v.IsIndexedAccess && !v.IsMapped {
		return TypePtr
	}
	// T[K] wrapped one level in a known single-argument container.
	if v.ElemType != nil {
		switch v.Name {
		case "Array":
			return ArrayOf(e.resolveMappedValue(v.ElemType, keyVar, fieldTy, hasField))
		case "Promise":
			return PromiseOf(e.resolveMappedValue(v.ElemType, keyVar, fieldTy, hasField))
		case "Set":
			return SetType(e.resolveMappedValue(v.ElemType, keyVar, fieldTy, hasField))
		}
	}
	// No T[K] reference we handle — an ordinary concrete (uniform) value type.
	return e.resolveType(v)
}
