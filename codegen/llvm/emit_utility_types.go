// emit_utility_types.go — built-in TypeScript utility types (TDD-00079).
//
// Because this compiler monomorphizes, a utility type has concrete inputs by the
// time resolveType runs, so it is *evaluated* here to a concrete Type (an
// ObjectType, or an existing scalar/array/map type) and then flows through the
// unchanged object machinery — no runtime representation of its own. This is the
// same seam generic-interface instantiation already uses (instantiateGenericInterface).
//
// Stage 1a (this file, initially): the single-type-argument utilities
// Partial/Required/Readonly/NonNullable. In this compiler's structural,
// zero-fill object model, Partial/Required/Readonly currently have no observable
// effect — object-literal fields are already omittable and calloc-zeroed, and
// there is no mutation checking — so they resolve to their argument's own shape.
// That is the point of doing it at all: without this, `Partial<User>` silently
// resolved to `User[]` via resolveType's generic ElemType fallback (a genuinely
// wrong result). NonNullable strips the nullable flag. Pick/Omit/Record (which
// need string-literal keys) and the general mapped/conditional forms are later
// stages of TDD-00079.
package llvm

import "KlainMainLang/ast"

// utilityTypeNames is the set resolveUtilityType handles, used only so
// resolveType can cheaply skip the switch for the common non-utility name.
var utilityTypeNames = map[string]bool{
	"Partial":     true,
	"Required":    true,
	"Readonly":    true,
	"NonNullable": true,
	"Pick":        true,
	"Omit":        true,
	"Record":      true,
}

// resolveUtilityType evaluates a built-in single-argument utility type to a
// concrete Type, returning (type, true) when name is a handled utility with a
// usable argument, else (zero, false) so resolveType falls through to its
// existing branches. It is consulted only after the user-defined generic
// registry (emitter.go), so a user type of the same name still wins.
func (e *Emitter) resolveUtilityType(name string, args []*ast.TypeAnnotation) (Type, bool) {
	switch name {
	case "Partial", "Required", "Readonly":
		// Structural no-ops over the argument's shape in the current object
		// model (see file comment). Resolving to the argument's own type is
		// correct where it matters — the shape — and honest about the rest.
		if len(args) != 1 || args[0] == nil {
			return Type{}, false
		}
		return e.resolveType(args[0]), true
	case "NonNullable":
		if len(args) != 1 || args[0] == nil {
			return Type{}, false
		}
		ty := e.resolveType(args[0])
		ty.Nullable = false
		return ty, true
	case "Pick", "Omit":
		// Pick<T,K>/Omit<T,K>: filter T's field set by the string-literal-union
		// key K. A non-object T or a non-literal K falls through (return false)
		// to resolveType's remaining branches rather than producing a wrong
		// shape here.
		if len(args) != 2 {
			return Type{}, false
		}
		base := e.resolveType(args[0])
		if !base.IsObject {
			return Type{}, false
		}
		keys, ok := collectStringLiteralKeys(args[1])
		if !ok {
			return Type{}, false
		}
		inKeys := make(map[string]bool, len(keys))
		for _, k := range keys {
			inKeys[k] = true
		}
		var fields []Field
		for _, f := range base.Fields {
			if inKeys[f.Name] == (name == "Pick") { // Pick keeps ∈K, Omit keeps ∉K
				fields = append(fields, f)
			}
		}
		return ObjectType(fields), true
	case "Record":
		// Record<K,V>: a string-literal-union K gives a fixed-shape object with
		// those keys typed V; a general key type (`string`) gives a Map<string,V>.
		if len(args) != 2 {
			return Type{}, false
		}
		valTy := e.resolveType(args[1])
		if keys, ok := collectStringLiteralKeys(args[0]); ok {
			fields := make([]Field, len(keys))
			for i, k := range keys {
				fields[i] = Field{Name: k, Ty: valTy}
			}
			return ObjectType(fields), true
		}
		// The open-key form is the index-signature dict (IsDynamicObject —
		// ADR-00485): bracket read/write, Object.keys/values/entries,
		// for…of, and JSON.stringify all ride the map-backed machinery,
		// exactly like `{ [k: string]: V }`. Previously a plain MapType,
		// whose value has none of the object-style surface.
		keyTy := TypePtr
		return Type{IR: "ptr", IsMap: true, IsDynamicObject: true, MapKey: &keyTy, MapVal: &valTy}, true
	}
	return Type{}, false
}

// collectStringLiteralKeys returns the set of key strings from a string-literal
// type (`"a"`) or a union of them (`"a" | "b"`), used by Pick/Omit/Record. The
// bool is false when the annotation isn't made entirely of string literals, so
// the caller can fall through rather than guess.
func collectStringLiteralKeys(ta *ast.TypeAnnotation) ([]string, bool) {
	if ta == nil {
		return nil, false
	}
	if ta.UnionMembers != nil {
		keys := make([]string, 0, len(ta.UnionMembers))
		for _, m := range ta.UnionMembers {
			if !m.IsStringLiteral {
				return nil, false
			}
			keys = append(keys, m.LiteralValue)
		}
		return keys, true
	}
	if ta.IsStringLiteral {
		return []string{ta.LiteralValue}, true
	}
	return nil, false
}
