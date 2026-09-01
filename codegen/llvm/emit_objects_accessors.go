package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// isSyntheticObjLitClass reports whether ty is a synthetic class an
// accessor-bearing object literal was lowered to (TDD-00153).
func isSyntheticObjLitClass(ty Type) bool {
	return ty.IsClass && strings.HasPrefix(ty.ClassName, "__kml_objlit_")
}

// ensureObjLitClass registers (once, idempotently) the synthetic anonymous
// class an accessor-bearing object literal is lowered to (TDD-00153), returning
// its name. It builds only class *metadata* (fields, accessor signatures) — no
// IR — so it is safe to call from the pure-inference path (inferObjectType),
// which must agree with the emit site on the literal's type. Method *bodies* are
// emitted lazily, exactly once, by emitObjectLiteralWithAccessors.
//
// Data properties become the class's fields; each `get x`/`set x` is registered
// under the same accessorMethodName-keyed dispatch a class accessor uses, so the
// existing member-read / assign / `this`-binding machinery applies unchanged.
func (e *Emitter) ensureObjLitClass(lit *ast.ObjectLiteral) string {
	if e.objLitClasses == nil {
		e.objLitClasses = map[*ast.ObjectLiteral]string{}
	}
	if name, ok := e.objLitClasses[lit]; ok {
		return name
	}

	var ownFields []Field
	seen := map[string]int{}
	for _, p := range lit.Properties {
		if p.AccessorKind != "" || p.Key == "" {
			continue
		}
		if _, isAssign := p.Value.(*ast.AssignmentExpression); isAssign {
			continue // shorthand-default is not a data field (rejected at emit)
		}
		fty := e.inferExprType(p.Value)
		if idx, ok := seen[p.Key]; ok {
			ownFields[idx].Ty = fty
			continue
		}
		seen[p.Key] = len(ownFields)
		ownFields = append(ownFields, Field{Name: p.Key, Ty: fty})
	}

	e.objLitClassCtr++
	className := fmt.Sprintf("__kml_objlit_%d", e.objLitClassCtr)
	ty := ClassType(className, nil, ownFields, false, false, false)
	e.interfaces[className] = ty

	info := ClassInfo{
		Ty:                        ty,
		OwnFields:                 ownFields,
		FlatFields:                ownFields,
		Methods:                   make(map[string]*ast.FunctionDeclaration),
		MethodSigs:                make(map[string]FuncSig),
		MethodImplementor:         make(map[string]string),
		MethodDispatchSlot:        make(map[string]*MethodSlot),
		TagID:                     e.nextClassTagID,
		RootClass:                 className,
		FieldOrigin:               make(map[string]string),
		OwnFieldVisibility:        make(map[string]string),
		OwnMethodVisibility:       make(map[string]string),
		StaticFieldTypes:          make(map[string]Type),
		OwnStaticFieldTypes:       make(map[string]Type),
		StaticFieldOwner:          make(map[string]string),
		OwnStaticFieldVisibility:  make(map[string]string),
		StaticMethodSigs:          make(map[string]FuncSig),
		StaticMethodImplementor:   make(map[string]string),
		OwnStaticMethodVisibility: make(map[string]string),
	}
	e.nextClassTagID++
	for _, f := range ownFields {
		info.FieldOrigin[f.Name] = className
	}

	// Accessor signatures, with `this` bound to the (field-complete) class type
	// so an unannotated getter's return type infers from `this._x`.
	for _, p := range lit.Properties {
		if p.AccessorKind == "" {
			continue
		}
		fe, ok := p.Value.(*ast.FunctionExpression)
		if !ok {
			continue
		}
		key := accessorMethodName(p.AccessorKind, p.Key)
		if _, dup := info.MethodSigs[key]; dup {
			continue // duplicate reported at emit
		}
		fd := &ast.FunctionDeclaration{Name: p.Key, Params: fe.Params, ReturnType: fe.RetType, Body: fe.Body}
		e.pushScope()
		e.define("this", Symbol{Ty: ty})
		sig := e.buildFunctionSig(fd)
		e.popScope()
		if p.AccessorKind == "set" {
			sig.RetType = TypeVoid
		}
		info.MethodSigs[key] = sig
		info.MethodImplementor[key] = className
		info.Methods[key] = fd
		info.MethodOrder = append(info.MethodOrder, key)
	}

	e.classes[className] = info
	e.objLitClasses[lit] = className
	return className
}

// emitObjectLiteralWithAccessors lowers an accessor-bearing object literal
// (TDD-00153) to an instance of its synthetic class: it emits the accessor
// method bodies (once) and constructs the instance in the literal's own scope
// (so a data initializer referencing an enclosing local works).
func (e *Emitter) emitObjectLiteralWithAccessors(lit *ast.ObjectLiteral) (Value, error) {
	pos := lit.GetPos()
	className := e.ensureObjLitClass(lit)
	info := e.classes[className]
	ty := info.Ty

	// Validate accessor shapes and emit their bodies once.
	if e.objLitEmitted == nil {
		e.objLitEmitted = map[string]bool{}
	}
	if !e.objLitEmitted[className] {
		e.objLitEmitted[className] = true
		// Reject the property forms this path doesn't handle, rather than
		// silently dropping them.
		for _, p := range lit.Properties {
			if p.AccessorKind == "" {
				if p.Key == "" {
					return Value{}, fmt.Errorf("%d:%d: an object literal that mixes a spread with a getter/setter is not yet supported", pos.Line, pos.Col)
				}
				if _, isAssign := p.Value.(*ast.AssignmentExpression); isAssign {
					return Value{}, fmt.Errorf("%d:%d: shorthand-default '%s' is not valid in a value object literal", pos.Line, pos.Col, p.Key)
				}
				continue
			}
			fe := p.Value.(*ast.FunctionExpression)
			if p.AccessorKind == "get" && len(fe.Params) != 0 {
				return Value{}, fmt.Errorf("%d:%d: a getter ('get %s') takes no parameters", pos.Line, pos.Col, p.Key)
			}
			if p.AccessorKind == "set" && len(fe.Params) != 1 {
				return Value{}, fmt.Errorf("%d:%d: a setter ('set %s') takes exactly one parameter", pos.Line, pos.Col, p.Key)
			}
			key := accessorMethodName(p.AccessorKind, p.Key)
			sig := info.MethodSigs[key]
			if p.AccessorKind == "get" && sig.RetType.IR == "void" {
				return Value{}, fmt.Errorf("%d:%d: a getter ('get %s') must return a value", pos.Line, pos.Col, p.Key)
			}
			llvmName := llvmSafeSymbol(className + "_" + key)
			if err := e.emitClassMember(llvmName, ty, fe.Params, sig, fe.Body, sig.RetType, pos, false, false); err != nil {
				return Value{}, err
			}
		}
		// A get/set pair must agree on the property type.
		for _, p := range lit.Properties {
			if p.AccessorKind == "" {
				continue
			}
			g, gok := info.MethodSigs[accessorMethodName("get", p.Key)]
			s, sok := info.MethodSigs[accessorMethodName("set", p.Key)]
			if gok && sok && !coerciblePure(s.ParamTypes[0], g.RetType) {
				return Value{}, fmt.Errorf("%d:%d: getter and setter for '%s' disagree on its type", pos.Line, pos.Col, p.Key)
			}
		}
	}

	// Construct: calloc, stamp the runtime tag, store each data field.
	e.ensureCalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", dataReg, ty.StructSize()))
	structIR := ty.StructIR()
	tagGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 0", tagGep, structIR, dataReg))
	e.emitInstr(fmt.Sprintf("store i64 %d, ptr %s, align 8", info.TagID, tagGep))

	for _, p := range lit.Properties {
		if p.AccessorKind != "" || p.Key == "" {
			continue
		}
		if _, isAssign := p.Value.(*ast.AssignmentExpression); isAssign {
			return Value{}, fmt.Errorf("%d:%d: shorthand-default '%s' is not valid in a value object literal", pos.Line, pos.Col, p.Key)
		}
		idx, fieldTy, ok := ty.FieldIndex(p.Key)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: object has no field '%s'", pos.Line, pos.Col, p.Key)
		}
		if !coerciblePure(e.inferExprType(p.Value), fieldTy) {
			return Value{}, fmt.Errorf("%d:%d: field '%s' is assigned a value of an incompatible type — this compiler is a typed subset", pos.Line, pos.Col, p.Key)
		}
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gepReg, structIR, dataReg, idx))
		if err := e.storeScalarOrNullableFieldExpr(gepReg, fieldTy, p.Value); err != nil {
			return Value{}, err
		}
	}

	return Value{Ref: dataReg, Ty: ty}, nil
}
