package llvm

import (
	"fmt"
	"KlainMainLang/ast"
)

// Object variable declarations, destructuring, and Object static methods (groupBy, keys).

// emitObjectLiteral allocates a heap struct for an object literal and returns
// a ptr Value. Field values are emitted recursively, so nested object literals
// work without any special handling at the call site.
//
// Properties (including spreads) are processed in source order, each storing
// straight into its field's slot in the final (already fully-merged) struct
// layout computed by inferObjectType — a later property or spread simply
// overwrites an earlier store at the same GEP index, which is exactly JS's
// last-write-wins object spread semantics, with no separate merge bookkeeping
// needed here.
func (e *Emitter) emitObjectLiteral(lit *ast.ObjectLiteral) (Value, error) {
	ty := e.inferObjectType(lit)
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, ty.StructSize()))
	structIR := ty.StructIR()

	storeField := func(name string, val Value) error {
		idx, fieldTy, ok := ty.FieldIndex(name)
		if !ok {
			return fmt.Errorf("%d:%d: object has no field '%s'", lit.GetPos().Line, lit.GetPos().Col, name)
		}
		val = e.coerce(val, fieldTy)
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, dataReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, val.Ref, gepReg, fieldTy.Align()))
		return nil
	}

	for _, prop := range lit.Properties {
		if spread, ok := prop.Value.(*ast.SpreadElement); ok && prop.Key == "" {
			srcVal, err := e.emitExpr(spread.Arg)
			if err != nil {
				return Value{}, err
			}
			if !srcVal.Ty.IsObject {
				return Value{}, fmt.Errorf("%d:%d: spread in object literal requires an object value", spread.GetPos().Line, spread.GetPos().Col)
			}
			srcStructIR := srcVal.Ty.StructIR()
			for _, f := range srcVal.Ty.Fields {
				srcIdx, _, _ := srcVal.Ty.FieldIndex(f.Name)
				srcGep := e.freshReg()
				loadReg := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", srcGep, srcStructIR, srcVal.Ref, srcIdx))
				e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", loadReg, f.Ty.IR, srcGep, f.Ty.Align()))
				if err := storeField(f.Name, Value{Ref: loadReg, Ty: f.Ty}); err != nil {
					return Value{}, err
				}
			}
			continue
		}
		val, err := e.emitExpr(prop.Value)
		if err != nil {
			return Value{}, err
		}
		if err := storeField(prop.Key, val); err != nil {
			return Value{}, err
		}
	}
	return Value{Ref: dataReg, Ty: ty}, nil
}

func (e *Emitter) emitObjectVarDecl(v *ast.VarDeclaration, ty Type) error {
	ptrName := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", ptrName))
	e.define(v.Name, Symbol{Ptr: ptrName, Ty: ty, IsConst: v.Kind == "const"})

	if v.Init == nil {
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", ptrName))
		return nil
	}

	switch init := v.Init.(type) {
	case *ast.ObjectLiteral:
		val, err := e.emitObjectLiteral(init)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
		return nil

	case *ast.NewErrorExpression:
		val, err := e.emitNewError(init)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
		return nil

	case *ast.CallExpression:
		// JSON.parse needs the target object type to parse fields correctly;
		// the generic emitExpr path would otherwise dispatch through
		// emitCall's JSON.parse case, which has no declaration context and
		// hardcodes TypePtr as the target (correct only for JSON.parse used
		// outside a typed declaration, e.g. as a bare expression).
		if mem, ok := init.Callee.(*ast.MemberExpression); ok {
			if id, ok2 := mem.Object.(*ast.Identifier); ok2 && id.Name == "JSON" && mem.Property == "parse" {
				val, err := e.emitJSONParse(init.Args, ty, init.GetPos())
				if err != nil {
					return err
				}
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
				return nil
			}
			// response.json() needs the same target-object-type context, for
			// the same reason.
			if mem.Property == "json" && e.inferExprType(mem.Object).IsResponse {
				val, err := e.emitResponseJSON(mem.Object, ty, init.GetPos())
				if err != nil {
					return err
				}
				e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
				return nil
			}
		}
		val, err := e.emitExpr(init)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
		return nil

	case *ast.AwaitExpression:
		// e.g. `const r: Response = await fetch(url)` — emitExpr already
		// dispatches AwaitExpression correctly (emitAwait unwraps the Promise
		// slot and frees it), returning the real object pointer directly; this
		// case just needed to exist so the switch doesn't fall through to the
		// default "must be an object literal or function call" error below.
		val, err := e.emitExpr(init)
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", val.Ref, ptrName))
		return nil

	default:
		return fmt.Errorf("%d:%d: object variable must be initialized with an object literal or function call", v.GetPos().Line, v.GetPos().Col)
	}
}


func (e *Emitter) emitObjectDestructuring(s *ast.ObjectDestructuring) error {
	objPtr, objTy, err := e.resolveObjectPtr(s.Init, s.GetPos())
	if err != nil {
		return err
	}
	structIR := objTy.StructIR()
	for _, prop := range s.Props {
		idx, fieldTy, ok := objTy.FieldIndex(prop.Key)
		if !ok {
			return fmt.Errorf("%d:%d: object has no field '%s'", s.GetPos().Line, s.GetPos().Col, prop.Key)
		}
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, objPtr, idx))
		valReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", valReg, fieldTy.IR, gepReg, fieldTy.Align()))
		localPtr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", localPtr, fieldTy.IR, fieldTy.Align()))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, valReg, localPtr, fieldTy.Align()))
		e.define(prop.Local, Symbol{Ptr: localPtr, Ty: fieldTy})
	}
	return nil
}

// resolveObjectPtr emits code to obtain the raw heap pointer for an object
// expression. Handles identifiers, function calls, and object literals.
func (e *Emitter) resolveObjectPtr(init ast.Expression, pos ast.Pos) (string, Type, error) {
	switch src := init.(type) {
	case *ast.Identifier:
		sym, found := e.lookup(src.Name)
		if !found || !sym.Ty.IsObject {
			return "", Type{}, fmt.Errorf("%d:%d: '%s' is not an object", pos.Line, pos.Col, src.Name)
		}
		objPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", objPtr, sym.Ptr))
		return objPtr, sym.Ty, nil

	case *ast.CallExpression:
		val, err := e.emitExpr(src)
		if err != nil {
			return "", Type{}, err
		}
		if !val.Ty.IsObject {
			return "", Type{}, fmt.Errorf("%d:%d: function call does not return an object", pos.Line, pos.Col)
		}
		return val.Ref, val.Ty, nil

	case *ast.ObjectLiteral:
		ty := e.inferObjectType(src)
		e.ensureMalloc()
		dataReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, ty.StructSize()))
		structIR := ty.StructIR()
		for _, prop := range src.Properties {
			idx, fieldTy, ok := ty.FieldIndex(prop.Key)
			if !ok {
				return "", Type{}, fmt.Errorf("%d:%d: object has no field '%s'", pos.Line, pos.Col, prop.Key)
			}
			val, err := e.emitExpr(prop.Value)
			if err != nil {
				return "", Type{}, err
			}
			val = e.coerce(val, fieldTy)
			gepReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, dataReg, idx))
			e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, val.Ref, gepReg, fieldTy.Align()))
		}
		return dataReg, ty, nil
	}
	return "", Type{}, fmt.Errorf("%d:%d: object destructuring requires an object variable, function call, or object literal", pos.Line, pos.Col)
}

// emitConditional emits a ternary expression cond ? consequent : alternate.
// Uses an alloca+store/load pattern so both branches can produce a single result.

func (e *Emitter) emitObjectGroupBy(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: Object.groupBy takes exactly 2 arguments", pos.Line, pos.Col)
	}
	ptrReg, lenReg, elemTy, err := e.resolveArrayForHOF(args[0], pos)
	if err != nil {
		return Value{}, err
	}
	cb, err := e.resolveCallbackWithHints(args[1], []Type{elemTy})
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(cb.retType()) {
		return Value{}, fmt.Errorf("%d:%d: Object.groupBy callback must return a string key", pos.Line, pos.Col)
	}
	e.ensureGroupMapHelpers()

	mapReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_gmap_create()", mapReg))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("grpby.cond")
	bodyL := e.freshLabel("grpby.body")
	doneL := e.freshLabel("grpby.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	loopDone := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", loopDone, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", loopDone, doneL, bodyL))

	e.emitLabel(bodyL)
	elemGep := e.freshReg()
	elemVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i64 %s", elemGep, elemTy.IR, ptrReg, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", elemVal, elemTy.IR, elemGep, elemTy.Align()))

	cbArgs := []Value{{Ref: elemVal, Ty: elemTy}}
	if cb.arity() >= 2 {
		cbArgs = append(cbArgs, Value{Ref: idxVal, Ty: TypeI64})
	}
	keyVal, err := e.emitCBCall(cb, cbArgs)
	if err != nil {
		return Value{}, err
	}

	bucketIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_gmap_find_or_add(ptr %s, ptr %s)", bucketIdx, mapReg, keyVal.Ref))

	// Convert element to i64 for uniform storage in the bucket.
	var elemAsI64 string
	switch elemTy.IR {
	case "i64":
		elemAsI64 = elemVal
	case "ptr":
		t := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", t, elemVal))
		elemAsI64 = t
	case "double":
		t := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = bitcast double %s to i64", t, elemVal))
		elemAsI64 = t
	case "i1":
		t := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", t, elemVal))
		elemAsI64 = t
	default:
		t := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sext %s %s to i64", t, elemTy.IR, elemVal))
		elemAsI64 = t
	}

	e.emitInstr(fmt.Sprintf("call void @__kml_gmap_append(ptr %s, i64 %s, i64 %s)", mapReg, bucketIdx, elemAsI64))

	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	gmapTy := Type{IR: "ptr", IsGroupMap: true, ElemType: &elemTy}
	return Value{Ref: mapReg, Ty: gmapTy}, nil
}

// emitObjectKeys implements Object.keys(obj | groupMap) → string[].
func (e *Emitter) emitObjectKeys(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Object.keys takes 1 argument", pos.Line, pos.Col)
	}
	val, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if val.Ty.IsGroupMap {
		e.ensureGroupMapHelpers()
		retReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_gmap_keys(ptr %s)", retReg, val.Ref))
		return Value{Ref: retReg, Ty: ArrayOf(TypePtr)}, nil
	}
	if !val.Ty.IsObject || len(val.Ty.Fields) == 0 {
		return Value{}, fmt.Errorf("%d:%d: Object.keys requires an object with known fields", pos.Line, pos.Col)
	}
	return e.emitObjectFieldNames(val.Ty.Fields, pos)
}

// emitObjectFieldNames allocates a string[] of compile-time field names.
func (e *Emitter) emitObjectFieldNames(fields []Field, pos ast.Pos) (Value, error) {
	n := int64(len(fields))
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*8))
	for i, f := range fields {
		keyPtr := e.internString(f.Name)
		slotReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", slotReg, dataReg, i))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", keyPtr, slotReg))
	}
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, dataReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %d, 1", r1, r0, n))
	return Value{Ref: r1, Ty: ArrayOf(TypePtr)}, nil
}

// emitObjectValues implements Object.values(obj) → string[].
// All field values are stringified (booleans → "true"/"false", numbers → decimal).
func (e *Emitter) emitObjectValues(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Object.values takes 1 argument", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !objVal.Ty.IsObject || len(objVal.Ty.Fields) == 0 {
		return Value{}, fmt.Errorf("%d:%d: Object.values requires an object with known fields", pos.Line, pos.Col)
	}
	n := int64(len(objVal.Ty.Fields))
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*8))
	for i, f := range objVal.Ty.Fields {
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, objVal.Ty.StructIR(), objVal.Ref, i))
		rawReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", rawReg, f.Ty.IR, gepReg, f.Ty.Align()))
		strVal, err := e.emitValueToString(Value{Ref: rawReg, Ty: f.Ty})
		if err != nil {
			return Value{}, fmt.Errorf("%d:%d: Object.values: field '%s': %w", pos.Line, pos.Col, f.Name, err)
		}
		slotReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", slotReg, dataReg, i))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", strVal.Ref, slotReg))
	}
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, dataReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %d, 1", r1, r0, n))
	return Value{Ref: r1, Ty: ArrayOf(TypePtr)}, nil
}

// emitObjectEntries implements Object.entries(obj) → {key: string, value: string}[].
// Each element of the returned array is a heap-allocated object with .key and .value fields.
// Iterate with `for (const e of Object.entries(obj))` then access `e.key` / `e.value`.
func (e *Emitter) emitObjectEntries(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: Object.entries takes 1 argument", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !objVal.Ty.IsObject || len(objVal.Ty.Fields) == 0 {
		return Value{}, fmt.Errorf("%d:%d: Object.entries requires an object with known fields", pos.Line, pos.Col)
	}
	entryTy := ObjectType([]Field{{Name: "key", Ty: TypePtr}, {Name: "value", Ty: TypePtr}})
	entrySize := int64(entryTy.StructSize())
	n := int64(len(objVal.Ty.Fields))
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, n*8))
	for i, f := range objVal.Ty.Fields {
		// Allocate one {key: string, value: string} entry struct.
		entryReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", entryReg, entrySize))
		// Store the key (compile-time field name).
		keyPtr := e.internString(f.Name)
		keySlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", keySlot, entryTy.StructIR(), entryReg))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", keyPtr, keySlot))
		// Read, stringify, and store the value.
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, objVal.Ty.StructIR(), objVal.Ref, i))
		rawReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", rawReg, f.Ty.IR, gepReg, f.Ty.Align()))
		strVal, err := e.emitValueToString(Value{Ref: rawReg, Ty: f.Ty})
		if err != nil {
			return Value{}, fmt.Errorf("%d:%d: Object.entries: field '%s': %w", pos.Line, pos.Col, f.Name, err)
		}
		valSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 1", valSlot, entryTy.StructIR(), entryReg))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", strVal.Ref, valSlot))
		// Store entry pointer in the outer array.
		slotReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %d", slotReg, dataReg, i))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", entryReg, slotReg))
	}
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, dataReg))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %d, 1", r1, r0, n))
	return Value{Ref: r1, Ty: ArrayOf(entryTy)}, nil
}

// emitGroupMapIndex handles groupResult["stringKey"] → sub-array.
func (e *Emitter) emitGroupMapIndex(sym Symbol, indexExpr ast.Expression, pos ast.Pos) (Value, error) {
	e.ensureGroupMapHelpers()
	mapPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", mapPtr, sym.Ptr))
	keyVal, err := e.emitExpr(indexExpr)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(keyVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: group map key must be a string", pos.Line, pos.Col)
	}
	retReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_gmap_get(ptr %s, ptr %s)", retReg, mapPtr, keyVal.Ref))
	elemTy := TypeI64
	if sym.Ty.ElemType != nil {
		elemTy = *sym.Ty.ElemType
	}
	return Value{Ref: retReg, Ty: ArrayOf(elemTy)}, nil
}

