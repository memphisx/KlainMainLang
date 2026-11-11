// emit_intersection.go — the composite-type checkpoint and the structural
// type key. An intersection's type is the checker's merged object, mapped
// like any other object type; codegen keeps no intersection of its own.
package llvm

import "strings"

// validateCompositeType is the guard every "a declared type reached this
// boundary" checkpoint (var decl, function/arrow/method param + return)
// runs on a freshly resolved type: an out-of-scope union member.
func validateCompositeType(ty Type, line, col int) error {
	return validateUnionMembers(ty, line, col)
}

// typeMergeKey is a bounded structural serialization of a type, deciding
// whether two types are the "same" (a thenable member's key). It distinguishes objects by field
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
	case t.IsArray() && t.ElemType != nil:
		return "[]" + typeMergeKey(*t.ElemType, depth+1)
	case t.IsObject():
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
		if t.Nullable() {
			k += "?"
		}
		return k
	}
}
