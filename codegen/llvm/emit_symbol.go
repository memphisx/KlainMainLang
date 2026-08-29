// emit_symbol.go — Symbol(...) V1 (docs/tdd/TDD-00044.md): a guaranteed-
// unique opaque value. Storage-wise a plain 1-field SymbolType() heap
// object ({description: string}), built and read via the same generic
// object-alloc/GEP machinery emit_objects.go already uses for object
// literals — see IsSymbol's doc comment in types.go for why no id/counter
// field is needed (pointer identity alone gives === its correct semantics).
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emitSymbolConstructor implements the bare (no `new`) Symbol()/Symbol("desc")
// call. At most one argument, which must be a plain string — no implicit
// stringification of a non-string description, matching this compiler's
// general "clean compile error, not silent coercion" style.
func (e *Emitter) emitSymbolConstructor(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: Symbol() takes at most 1 argument (an optional description)", pos.Line, pos.Col)
	}
	descPtr := e.internString("")
	if len(args) == 1 {
		val, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if val.Ty.IR != "ptr" || val.Ty.IsObject || val.Ty.IsArray || val.Ty.IsFunc || val.Ty.IsSymbol {
			return Value{}, fmt.Errorf("%d:%d: Symbol()'s description must be a string", pos.Line, pos.Col)
		}
		descPtr = val.Ref
	}

	ty := SymbolType()
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, ty.StructSize()))
	idx, fieldTy, _ := ty.FieldIndex("description")
	gepReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, ty.StructIR(), dataReg, idx))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", StructFieldIR(fieldTy), descPtr, gepReg, fieldTy.Align()))
	return Value{Ref: dataReg, Ty: ty}, nil
}

// emitSymbolToString formats val (a Symbol-typed Value) as "Symbol(desc)" —
// shared by .toString(), console.log, and template-literal interpolation.
// Deliberately identical across all three call sites: real JS is stricter
// about template-literal interpolation (throws TypeError there), but this
// compiler treats all three the same way V1, documented in TDD-00044.
func (e *Emitter) emitSymbolToString(val Value) (Value, error) {
	ty := SymbolType()
	idx, fieldTy, _ := ty.FieldIndex("description")
	gepReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, ty.StructIR(), val.Ref, idx))
	descReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", descReg, fieldTy.IR, gepReg, fieldTy.Align()))

	prefix := Value{Ref: e.internString("Symbol("), Ty: TypePtr}
	descVal := Value{Ref: descReg, Ty: TypePtr}
	suffix := Value{Ref: e.internString(")"), Ty: TypePtr}
	acc, err := e.emitStringConcat(prefix, descVal)
	if err != nil {
		return Value{}, err
	}
	return e.emitStringConcat(acc, suffix)
}


// ensureSymbolRegistry emits the global Symbol.for registry (ADR-00488):
// a lazily-created string-keyed map from key to the shared Symbol pointer.
func (e *Emitter) ensureSymbolRegistry() {
	if e.usedSymbolRegistry {
		return
	}
	e.usedSymbolRegistry = true
	e.ensureMapStrHelpers()
	e.ensureMalloc()
	ty := SymbolType()
	idx, _, _ := ty.FieldIndex("description")
	e.emitGlobal("@__kml_sym_registry = internal global ptr null, align 8")
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_symbol_for(ptr %%key) {
entry:
  %%reg0 = load ptr, ptr @__kml_sym_registry, align 8
  %%isnull = icmp eq ptr %%reg0, null
  br i1 %%isnull, label %%mk, label %%have
mk:
  %%fresh = call ptr @__kml_map_str_create()
  store ptr %%fresh, ptr @__kml_sym_registry, align 8
  br label %%have
have:
  %%reg = load ptr, ptr @__kml_sym_registry, align 8
  %%raw = call i64 @__kml_map_str_get(ptr %%reg, ptr %%key)
  %%hit = icmp ne i64 %%raw, 0
  br i1 %%hit, label %%found, label %%create
found:
  %%p = inttoptr i64 %%raw to ptr
  ret ptr %%p
create:
  %%sym = call ptr @malloc(i64 %d)
  %%dgep = getelementptr %s, ptr %%sym, i32 0, i32 %d
  store ptr %%key, ptr %%dgep, align 8
  %%si = ptrtoint ptr %%sym to i64
  call void @__kml_map_str_set(ptr %%reg, ptr %%key, i64 %%si)
  ret ptr %%sym
}`, ty.StructSize(), ty.StructIR(), idx))
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_symbol_keyfor(ptr %%sym) {
entry:
  %%reg = load ptr, ptr @__kml_sym_registry, align 8
  %%isnull = icmp eq ptr %%reg, null
  br i1 %%isnull, label %%miss, label %%scan
scan:
  %%keys = call {ptr, i64} @__kml_map_str_keys(ptr %%reg)
  %%kp = extractvalue {ptr, i64} %%keys, 0
  %%kl = extractvalue {ptr, i64} %%keys, 1
  br label %%cond
cond:
  %%i = phi i64 [ 0, %%scan ], [ %%in, %%next ]
  %%more = icmp slt i64 %%i, %%kl
  br i1 %%more, label %%body, label %%miss
body:
  %%kg = getelementptr ptr, ptr %%kp, i64 %%i
  %%k = load ptr, ptr %%kg, align 8
  %%raw = call i64 @__kml_map_str_get(ptr %%reg, ptr %%k)
  %%p = inttoptr i64 %%raw to ptr
  %%same = icmp eq ptr %%p, %%sym
  br i1 %%same, label %%hit, label %%next
hit:
  ret ptr %%k
next:
  %%in = add i64 %%i, 1
  br label %%cond
miss:
  ret ptr null
}`))
}

// emitSymbolStatic dispatches Symbol.for(key) / Symbol.keyFor(sym)
// (ADR-00488). Symbols from the registry share pointer identity per key,
// so Symbol.for("a") === Symbol.for("a"); keyFor of an unregistered symbol
// is null.
func (e *Emitter) emitSymbolStatic(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureSymbolRegistry()
	switch method {
	case "for":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: Symbol.for takes exactly 1 string key", pos.Line, pos.Col)
		}
		kv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if kv.Ty.IR != "ptr" || kv.Ty.IsObject || kv.Ty.IsSymbol {
			return Value{}, fmt.Errorf("%d:%d: Symbol.for's key must be a string", pos.Line, pos.Col)
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_symbol_for(ptr %s)", r, kv.Ref))
		return Value{Ref: r, Ty: SymbolType()}, nil
	case "keyFor":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: Symbol.keyFor takes exactly 1 symbol", pos.Line, pos.Col)
		}
		sv, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		if !sv.Ty.IsSymbol {
			return Value{}, fmt.Errorf("%d:%d: Symbol.keyFor's argument must be a symbol", pos.Line, pos.Col)
		}
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_symbol_keyfor(ptr %s)", r, sv.Ref))
		nt := TypePtr
		nt.Nullable = true
		return Value{Ref: r, Ty: nt}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: Symbol.%s is not supported", pos.Line, pos.Col, method)
}
