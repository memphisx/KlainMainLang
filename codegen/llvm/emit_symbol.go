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

// emitSymbolConstructor implements the bare (no `new`) Symbol()/Symbol(desc)
// call. A description other than undefined is ToString'd (`Symbol(42)` is
// described "42"); an absent or undefined one leaves the description
// undefined (a null pointer), which toString renders as `Symbol()`.
func (e *Emitter) emitSymbolConstructor(args []ast.Expression, pos ast.Pos) (Value, error) {
	// Arguments past the description are evaluated, then ignored, before
	// the description converts.
	extra := func() error {
		for _, a := range args[1:] {
			if _, err := e.emitExpr(a); err != nil {
				return err
			}
		}
		return nil
	}
	descPtr := "null"
	if len(args) >= 1 {
		if nl, ok := args[0].(*ast.NullLiteral); !ok || !nl.IsUndefined {
			val, err := e.emitExpr(args[0])
			if err != nil {
				return Value{}, err
			}
			if err := extra(); err != nil {
				return Value{}, err
			}
			switch {
			case val.Ty.IsDynamic():
				// undefined stays no description; anything else is ToString'd.
				undef := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", undef, e.convert(val, TypeAny).Ref, nbUndefined))
				str, err := e.emitDynamicToString(e.convert(val, TypeAny))
				if err != nil {
					return Value{}, err
				}
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" null, "+irPtr+" %s", r, undef, str.Ref))
				descPtr = r
			case val.Ty.IR == irPtr && !val.Ty.IsObject() && !val.Ty.IsArray() && !val.Ty.IsFunc() && !val.Ty.IsSymbol():
				descPtr = val.Ref // a string (null when `string | undefined` is undefined)
			default:
				str, err := e.emitValueToString(val)
				if err != nil {
					return Value{}, err
				}
				descPtr = str.Ref
			}
		} else if err := extra(); err != nil {
			return Value{}, err
		}
	}

	ty := SymbolType()
	dataReg := e.freshReg()
	e.emitObjMallocInto(dataReg, ty)
	e.heapStore("i64", fmt.Sprint(symbolTypeIDFlag), dataReg, 8)
	idx, fieldTy, _ := ty.FieldIndex("description")
	gepReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gepReg, ty.StructIR(), dataReg, idx))
	e.heapStore(StructFieldIR(fieldTy), descPtr, gepReg, fieldTy.Align())
	return Value{Ref: dataReg, Ty: ty}, nil
}

// symbolTypeIDFlag is the marker bit stored in a Symbol's hidden field 0
// (ADR-01059). Bit 47, for the same reason Errors use bit 48
// (errorTypeIDFlag): user-space heap pointers on every target are < 2^47, so
// a plain object whose field 0 is a pointer or a small tag can never carry
// it, and an Error's field 0 (bit 48 | kind) never has bit 47 set either —
// the two probes cannot mistake each other's objects.
const symbolTypeIDFlag = 1 << 47

// emitBoxedSymbolProbe reads field 0 of a boxed object (payloadReg is the
// ptrtoint'd object pointer) and answers whether it is a Symbol. Only valid
// under a kmlTagObject tag check.
func (e *Emitter) emitBoxedSymbolProbe(payloadReg string) (objPtr, isSym string) {
	objPtr = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, objPtr, payloadReg))
	f0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", f0, objPtr))
	flagged := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", flagged, f0, symbolTypeIDFlag))
	isSym = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isSym, flagged))
	return objPtr, isSym
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
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gepReg, ty.StructIR(), val.Ref, idx))
	descReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align %d", descReg, fieldTy.IR, gepReg, fieldTy.Align()))

	prefix := Value{Ref: e.internString("Symbol("), Ty: TypePtr}
	none, shown := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq "+irPtr+" %s, null", none, descReg))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", shown, none, e.internString(""), descReg))
	descVal := Value{Ref: shown, Ty: TypePtr}
	suffix := Value{Ref: e.internString(")"), Ty: TypePtr}
	acc, err := e.emitStringConcat(prefix, descVal)
	if err != nil {
		return Value{}, err
	}
	return e.emitStringConcat(acc, suffix)
}

// ensureSymbolRegistry declares the Symbol.for registry (ADR-00488,
// collectionssrc/collections.c): a lazily created string-keyed map from key
// to the shared Symbol.
func (e *Emitter) ensureSymbolRegistry() {
	if e.usedSymbolRegistry {
		return
	}
	e.usedSymbolRegistry = true
	e.ensureMapStrHelpers()
	if ty := SymbolType(); ty.StructSize() != 16 {
		panic("collections.c's Symbol.for builds a 16-byte Symbol")
	}
}

// emitSymbolStaticFor is Symbol.for(key) (ADR-00488). Symbols from the
// registry share pointer identity per key, so Symbol.for("a") ===
// Symbol.for("a").
func (e *Emitter) emitSymbolStaticFor(args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureSymbolRegistry()
	if len(args) == 0 {
		args = []ast.Expression{ast.NewNullLiteral(true, pos)} // Symbol.for(undefined)
	}
	kv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	for _, x := range args[1:] {
		if _, err := e.emitExpr(x); err != nil {
			return Value{}, err
		}
	}
	// The key is ToString(key): `Symbol.for(5)` is "5"'s, a symbol key a
	// TypeError.
	if !isStringTy(kv.Ty) || kv.Ty.IsObject() || kv.Ty.IsSymbol() || kv.Ty.Nullable() || kv.Ty.IsDynamic() {
		if kv.Ty.IR == "" || kv.Ty.IR == "void" {
			kv = Value{Ref: e.internString("undefined"), Ty: TypePtr}
		} else {
			if !kv.Ty.IsDynamic() {
				if kv, err = e.emitBoxValue(kv); err != nil {
					return Value{}, err
				}
			}
			e.emitAnySymbolGuard(kv.Ref, true)
			if kv, err = e.emitDynamicToString(kv); err != nil {
				return Value{}, err
			}
		}
	}
	kv = e.convert(kv, TypePtr)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_symbol_for("+irPtr+" %s)", r, kv.Ref))
	return Value{Ref: r, Ty: SymbolType()}, nil
}

// emitSymbolStaticKeyFor is Symbol.keyFor(sym): undefined for a symbol not
// in the registry.
func (e *Emitter) emitSymbolStaticKeyFor(args []ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureSymbolRegistry()
	if len(args) != 1 {
		return Value{}, internalAt(pos, "Symbol.keyFor arity")
	}
	sv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !sv.Ty.IsSymbol() {
		// V8's TypeError, naming the value: `undefined is not a symbol`.
		boxed, err := e.emitBoxValue(sv)
		if err != nil {
			return Value{}, err
		}
		desc, err := e.emitValueToString(boxed)
		if err != nil {
			return Value{}, err
		}
		msg, err := e.emitStringConcat(desc, Value{Ref: e.internString(" is not a symbol"), Ty: TypePtr})
		if err != nil {
			return Value{}, err
		}
		e.emitThrowTypeErrorValue(msg.Ref)
		e.emitLabel(e.freshLabel("keyfor.dead"))
		return Value{Ref: fmt.Sprint(nbUndefined), Ty: TypeAny}, nil
	}
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_symbol_keyfor("+irPtr+" %s)", r, sv.Ref))
	nt := TypePtr // string | undefined: a symbol not in the registry
	nt.setNullable(true)
	nt.setUndefined(true)
	return Value{Ref: r, Ty: nt}, nil
}

// wellKnownSymbols are the ECMAScript well-known symbols (`Symbol.iterator`,
// …). Each is one static Symbol; used as a property key, the two iteration
// protocols' map to the member names a class's `[Symbol.iterator]()` /
// `[Symbol.asyncIterator]()` compile to (`@@iterator`, `@@asyncIterator`).
var wellKnownSymbols = map[string]bool{
	"asyncIterator": true, "hasInstance": true, "isConcatSpreadable": true,
	"iterator": true, "match": true, "matchAll": true, "replace": true,
	"search": true, "species": true, "split": true, "toPrimitive": true,
	"toStringTag": true, "unscopables": true, "dispose": true, "asyncDispose": true,
}

// wellKnownSymbol returns the static Symbol for `Symbol.name`.
func (e *Emitter) wellKnownSymbol(name string) string {
	g := "@__kml_wksym_" + name
	if e.wkSymbols == nil {
		e.wkSymbols = map[string]bool{}
	}
	if !e.wkSymbols[name] {
		e.wkSymbols[name] = true
		ty := SymbolType()
		e.emitGlobal(fmt.Sprintf("%s = internal"+irGlobalAS+" global %s { i64 %d, "+irPtr+" %s }, align 8", g, ty.StructIR(), symbolTypeIDFlag, e.internString("Symbol."+name)))
	}
	return g
}
