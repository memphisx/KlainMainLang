package llvm

import (
	"strings"
	"KlainMainLang/ast"
)

// Field is one field in an object type.
type Field struct {
	Name string
	Ty   Type
}

// Type represents an LLVM IR type.
type Type struct {
	IR       string // e.g. "i64", "i32", "float", "double", "i1", "ptr"
	Signed   bool
	Float    bool
	IsArray  bool
	ElemType *Type  // non-nil when IsArray
	IsObject bool
	Fields   []Field // non-nil when IsObject
	// Function/closure type: all closures are passed as ptr.
	IsFunc      bool
	FuncParams  []Type
	FuncRetType *Type // nil means void
	// IsGroupMap marks the result of Object.groupBy: a heap ptr to a dynamic
	// string-keyed map of typed sub-arrays. ElemType is the element type of
	// each bucket. Bracket-notation access returns ArrayOf(*ElemType).
	IsGroupMap bool
	// IsMap / IsSet mark Map<K,V> and Set<T> heap objects.
	// MapKey holds the key type; MapVal holds the value type (nil for Set).
	IsMap   bool
	IsSet   bool
	MapKey  *Type
	MapVal  *Type
	// IsNull marks the null/undefined literal sentinel type (ptr null at IR level).
	// IsUndefined distinguishes `undefined` from `null` for string rendering.
	// Nullable marks T | null / T | undefined type annotations.
	IsNull      bool
	IsUndefined bool
	Nullable    bool
	// IsPromise marks Promise<T> types (the coroutine handle, IR type ptr).
	// PromiseType is the T in Promise<T>; nil means Promise<void>.
	IsPromise   bool
	PromiseType *Type
	// IsDynamic marks any/unknown: a runtime-tagged { i8, i64 } box (tag +
	// payload) instead of one fixed concrete storage type. See emit_dynamic.go.
	IsDynamic bool
	// IsDate marks Date: a plain i64 milliseconds-since-epoch timestamp, same
	// storage as number, distinguished only so method dispatch (getFullYear,
	// toISOString, etc.) can recognize it. See emit_date.go.
	IsDate bool
	// IsResponse marks fetch()'s Response type: an ordinary heap object
	// (status, ok, body fields — all plain field reads via the existing
	// object machinery) with two extra dispatched methods, text()/json().
	// See emit_fetch.go.
	IsResponse bool
	// Inferred marks a parameter type that defaulted to TypeI64 because no
	// explicit annotation was given, as opposed to a real `number`/`int32`/
	// etc. annotation that happens to also resolve to i64. Call sites use
	// this to reject a non-numeric argument against an unannotated
	// parameter at compile time, instead of silently bit-reinterpreting it
	// as an i64 (see docs/adr/ADR-00042.md).
	Inferred bool
}

// ArrayOf returns an array type whose elements are of the given type.
func ArrayOf(elem Type) Type {
	return Type{IR: "ptr", IsArray: true, ElemType: &elem}
}

// ObjectType returns an object type with the given fields.
func ObjectType(fields []Field) Type {
	return Type{IR: "ptr", IsObject: true, Fields: fields}
}

// MapType returns a Map<key,val> type.
func MapType(key, val Type) Type {
	return Type{IR: "ptr", IsMap: true, MapKey: &key, MapVal: &val}
}

// SetType returns a Set<elem> type.
func SetType(elem Type) Type {
	return Type{IR: "ptr", IsSet: true, MapKey: &elem}
}

// PromiseOf returns a Promise<T> type (the coroutine handle ptr).
// Pass TypeVoid for Promise<void>.
func PromiseOf(inner Type) Type {
	if inner.IR == "void" {
		return Type{IR: "ptr", IsPromise: true}
	}
	innerCopy := inner
	return Type{IR: "ptr", IsPromise: true, PromiseType: &innerCopy}
}

// ResponseType returns fetch()'s Response object type: a plain heap object
// with status/ok/body fields (readable via the ordinary object field-access
// path — no special dispatch needed for those three), plus IsResponse set so
// emit_fetch.go's text()/json() method dispatch can recognize it.
func ResponseType() Type {
	ty := ObjectType([]Field{
		{Name: "status", Ty: TypeI64},
		{Name: "ok", Ty: TypeBool},
		{Name: "body", Ty: TypePtr},
	})
	ty.IsResponse = true
	return ty
}

// FuncType returns a closure/function type. All closures are represented as ptr
// at the LLVM level (a pointer to a {funcPtr, envPtr} header on the heap).
func FuncType(params []Type, ret Type) Type {
	retCopy := ret
	return Type{IR: "ptr", IsFunc: true, FuncParams: params, FuncRetType: &retCopy}
}

// StructIR returns the LLVM struct type string, e.g. "{ i64, i32 }".
func (t Type) StructIR() string {
	parts := make([]string, len(t.Fields))
	for i, f := range t.Fields {
		parts[i] = f.Ty.IR
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

// StructSize returns the byte size of the struct following natural alignment rules
// (same rules LLVM applies for the same field sequence).
// Assumes size == align for all primitive types (holds for i8..i64, float, double, ptr).
func (t Type) StructSize() int64 {
	offset := int64(0)
	maxAlign := int64(1)
	for _, f := range t.Fields {
		fa := int64(f.Ty.Align())
		if fa > maxAlign {
			maxAlign = fa
		}
		if offset%fa != 0 {
			offset = (offset/fa + 1) * fa
		}
		offset += fa
	}
	// round up to struct alignment
	if offset%maxAlign != 0 {
		offset = (offset/maxAlign + 1) * maxAlign
	}
	return offset
}

// FieldIndex returns the index, type, and ok of a named field.
func (t Type) FieldIndex(name string) (int, Type, bool) {
	for i, f := range t.Fields {
		if f.Name == name {
			return i, f.Ty, true
		}
	}
	return 0, Type{}, false
}

func (t Type) Align() int {
	switch t.IR {
	case "i8":
		return 1
	case "i16":
		return 2
	case "i32", "float":
		return 4
	case "i64", "double", "ptr":
		return 8
	case "i1":
		return 1
	}
	return 8
}

// IsInteger returns true for integer (non-float) types.
func (t Type) IsInteger() bool { return !t.Float && t.IR != "ptr" && t.IR != "void" }

// isSafeNumericArg reports whether v can be safely passed to an inferred
// (unannotated, defaulted-to-i64) parameter without silently corrupting
// data — see docs/adr/ADR-00042.md. IsInteger()/Float already exclude
// ptr-backed types (string/object/array/closure/Promise all use IR "ptr"),
// but a boxed any/unknown value's IR is a distinct aggregate ("{ i8, i64 }")
// that's neither ptr nor float, so IsDynamic needs its own explicit check —
// otherwise it would slip through as if it were already a plain number.
func isSafeNumericArg(t Type) bool {
	return (t.IsInteger() || t.Float) && !t.IsDynamic
}

// LLVMRetType returns the LLVM IR type string used in function definitions and
// call instructions. Arrays are returned as an aggregate {ptr, i64}.
func (t Type) LLVMRetType() string {
	if t.IsArray {
		return "{ptr, i64}"
	}
	return t.IR
}

// PrintfFmt returns the printf format specifier for this type, or "" for types
// that cannot be printed with a single printf call (e.g. arrays).
func (t Type) PrintfFmt() string {
	if t.IsArray {
		return ""
	}
	switch t.IR {
	case "i8", "i16", "i32":
		return "%d"
	case "i64":
		return "%lld"
	case "float", "double":
		return "%g"
	case "i1":
		return "%d"
	case "ptr":
		return "%s"
	}
	return "%d"
}

var (
	TypeVoid   = Type{IR: "void"}
	TypeBool   = Type{IR: "i1", Signed: false}
	TypeI8     = Type{IR: "i8", Signed: true}
	TypeI16    = Type{IR: "i16", Signed: true}
	TypeI32    = Type{IR: "i32", Signed: true}
	TypeI64    = Type{IR: "i64", Signed: true}
	TypeU8     = Type{IR: "i8", Signed: false}
	TypeU16    = Type{IR: "i16", Signed: false}
	TypeU32    = Type{IR: "i32", Signed: false}
	TypeU64    = Type{IR: "i64", Signed: false}
	TypeF32    = Type{IR: "float", Float: true}
	TypeF64    = Type{IR: "double", Float: true}
	TypePtr       = Type{IR: "ptr"}
	TypeNull      = Type{IR: "ptr", IsNull: true}
	TypeUndefined = Type{IR: "ptr", IsNull: true, IsUndefined: true}
	// TypeAny backs any/unknown: an anonymous/literal LLVM struct { tag, payload },
	// following the same "literal struct type used directly, no named-type
	// declaration needed" convention ObjectType()'s StructIR() already relies on.
	TypeAny = Type{IR: "{ i8, i64 }", IsDynamic: true}
	// TypeDate backs Date: a plain i64 milliseconds-since-epoch timestamp.
	TypeDate = Type{IR: "i64", Signed: true, IsDate: true}
)

// FuncSig holds the signature of a user-defined function.
type FuncSig struct {
	ParamTypes []Type
	ParamNames []string // for error messages only (e.g. an inferred-parameter type mismatch)
	RetType    Type
	HasRest    bool          // last param is a rest (variadic) parameter
	Defaults   []ast.Expression // per-param default expression; nil entry means no default
}

// ResolveTypeName maps a TypeScript or JSDoc type name to an LLVM Type.
// Handles array suffixes (e.g. "int32[]") and falls back to i64 for unknowns.
func ResolveTypeName(name string) Type {
	// Array suffix: T[]
	if len(name) > 2 && name[len(name)-2:] == "[]" {
		elem := ResolveTypeName(name[:len(name)-2])
		return ArrayOf(elem)
	}
	switch name {
	case "number":
		return TypeI64
	case "string":
		return TypePtr
	case "boolean":
		return TypeBool
	case "void":
		return TypeVoid
	case "null":
		return TypeNull
	case "undefined":
		return TypeUndefined
	case "any", "unknown":
		return TypeAny
	case "Date":
		return TypeDate
	case "Response":
		return ResponseType()
	case "int8":
		return TypeI8
	case "int16":
		return TypeI16
	case "int32":
		return TypeI32
	case "int64":
		return TypeI64
	case "uint8":
		return TypeU8
	case "uint16":
		return TypeU16
	case "uint32":
		return TypeU32
	case "uint64":
		return TypeU64
	case "float32":
		return TypeF32
	case "float64":
		return TypeF64
	}
	return TypeI64 // default
}
