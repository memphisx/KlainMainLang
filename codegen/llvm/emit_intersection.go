// emit_intersection.go — intersection types A & B & ... (TDD-00078).
//
// The whole feature reduces to one idea: an object-type intersection is just a
// single object type carrying the merged fields of every member. It needs no
// runtime representation of its own — unlike a union (a runtime-tagged value
// that is *one of* its members, see emit_dynamic.go), an intersection value
// satisfies *all* members at once, which for fixed-shape structs is one bigger
// struct fully known at compile time. So resolveType collapses A & B into a
// plain ObjectType (mergeIntersectionFields), and the only intersection-
// specific logic left is validating the members — which, because resolveType
// has no error return, happens at each use site via validateIntersectionMembers
// exactly as validateUnionMembers does for unions.
//
// V1 scope: every member must be an object type (named interface, inline {...},
// or a type alias of either). A non-object member, or a field declared with
// conflicting types across members, is a clean compile error. The -compat=js
// "conflicting field becomes `never`" path (TDD-00078) is a deferred second
// stage; today a conflict is rejected in both modes.
package llvm

import (
	"fmt"
	"strings"
)

// mergeIntersectionFields flattens the members of an intersection into one
// field list. Fields are kept in member order; a field name appearing in more
// than one member is deduplicated when the two declarations resolve to the
// same type, and is a conflict error when they don't. Every member must be an
// object type. The returned error (if any) is surfaced by
// validateIntersectionMembers at a use site with a real source position.
func mergeIntersectionFields(members []Type) ([]Field, error) {
	var merged []Field
	seen := map[string]int{} // field name -> index in merged
	for _, m := range members {
		if !m.IsObject {
			return nil, fmt.Errorf("an intersection member must be an object type (a non-object type like number/string/a function/an array intersects to `never`, which is not supported)")
		}
		for _, f := range m.Fields {
			if idx, ok := seen[f.Name]; ok {
				existing := merged[idx].Ty
				if typeMergeKey(existing, 0) == typeMergeKey(f.Ty, 0) {
					continue // identical redeclaration — dedupe
				}
				// Same field name with two different object types is itself an
				// intersection of those objects (`{ x: A } & { x: B }` ⇒
				// `x: A & B`), so merge them recursively rather than conflict.
				if existing.IsObject && f.Ty.IsObject {
					subFields, err := mergeIntersectionFields([]Type{existing, f.Ty})
					if err != nil {
						return nil, err
					}
					merged[idx].Ty = ObjectType(subFields)
					continue
				}
				return nil, fmt.Errorf("field %q has conflicting types across the intersection members", f.Name)
			}
			seen[f.Name] = len(merged)
			merged = append(merged, f)
		}
	}
	return merged, nil
}

// validateIntersectionMembers re-runs the merge to surface any member error
// (non-object member, or a conflicting field type) at a use site that has a
// source position — mirroring validateUnionMembers. It recurses into object
// fields and array elements, because an intersection can be nested arbitrarily
// deep (`interface W { p: A & number }`, `(A & B)[]`) — positions the use-site
// checkpoints don't reach directly, where a bad intersection would otherwise
// resolve to a malformed merged struct and emit invalid IR rather than a clean
// rejection. depth is bounded to stay safe against a cyclic Type.
func validateIntersectionMembers(ty Type, line, col int) error {
	return validateIntersectionDepth(ty, line, col, 0)
}

func validateIntersectionDepth(ty Type, line, col, depth int) error {
	if depth > 32 {
		return nil
	}
	if ty.IntersectionMembers != nil {
		if _, err := mergeIntersectionFields(ty.IntersectionMembers); err != nil {
			return fmt.Errorf("%d:%d: %s", line, col, err.Error())
		}
	}
	for _, f := range ty.Fields {
		if err := validateIntersectionDepth(f.Ty, line, col, depth+1); err != nil {
			return err
		}
	}
	if ty.ElemType != nil {
		if err := validateIntersectionDepth(*ty.ElemType, line, col, depth+1); err != nil {
			return err
		}
	}
	for _, p := range ty.FuncParams {
		if err := validateIntersectionDepth(p, line, col, depth+1); err != nil {
			return err
		}
	}
	if ty.FuncRetType != nil {
		if err := validateIntersectionDepth(*ty.FuncRetType, line, col, depth+1); err != nil {
			return err
		}
	}
	return nil
}

// validateCompositeType is the single guard every "a declared type reached this
// boundary" checkpoint (var decl, function/arrow/method param + return) runs on
// a freshly resolved type. It rejects an out-of-V1-scope union member
// (validateUnionMembers) or a bad intersection (validateIntersectionMembers) in
// one call, so a new checkpoint gets both checks — and any future composite
// kind added here — for free, and can't accidentally validate one but not the
// other.
func validateCompositeType(ty Type, line, col int) error {
	if err := validateUnionMembers(ty, line, col); err != nil {
		return err
	}
	return validateIntersectionMembers(ty, line, col)
}

// typeMergeKey is a bounded structural serialization of a type, used only to
// decide whether two same-named intersection fields are the "same type"
// (dedupe) or "different" (conflict). It distinguishes objects by field
// name+type (so `{a:number}` and `{b:number}` are different) and arrays by
// element type, and falls back to the LLVM IR string plus a nullability marker
// for scalars. Recursion is depth-capped so a self-referential interface can't
// spin forever; at the cap it degrades to the bare IR, which is conservative
// (more likely to report two deep types equal than to loop).
func typeMergeKey(t Type, depth int) string {
	if depth > 8 {
		return t.IR
	}
	switch {
	case t.IsArray && t.ElemType != nil:
		return "[]" + typeMergeKey(*t.ElemType, depth+1)
	case t.IsObject:
		var sb strings.Builder
		sb.WriteString("{")
		for _, f := range t.Fields {
			sb.WriteString(f.Name)
			sb.WriteByte(':')
			sb.WriteString(typeMergeKey(f.Ty, depth+1))
			sb.WriteByte(';')
		}
		sb.WriteString("}")
		return sb.String()
	default:
		k := t.IR
		if t.Nullable {
			k += "?"
		}
		return k
	}
}
