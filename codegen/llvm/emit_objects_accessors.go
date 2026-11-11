package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// isSyntheticObjLitClass reports whether ty is a synthetic class an
// accessor-bearing object literal was lowered to (TDD-00153).
func isSyntheticObjLitClass(ty Type) bool {
	return ty.IsClass() && strings.HasPrefix(ty.ClassName, "__kml_objlit_")
}

// ownAccessorBox is v boxed when it is an accessor-bearing object literal,
// whose accessors are own enumerable properties its layout's fields do not
// list: key enumeration, serialization and inspection walk its boxed form.
// ok is false for any other value.
func (e *Emitter) ownAccessorBox(v Value) (Value, bool, error) {
	if !isSyntheticObjLitClass(v.Ty) || v.Ty.Nullable() || v.Ty.IsDynamic() {
		return Value{}, false, nil
	}
	box, err := e.emitBoxValue(v)
	return box, true, err
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
		fty := e.typeOfExpr(p.Value)
		if idx, ok := seen[p.Key]; ok {
			ownFields[idx].Ty = fty
			continue
		}
		seen[p.Key] = len(ownFields)
		ownFields = append(ownFields, Field{Name: p.Key, Ty: fty})
	}

	// Typing a field can ask for this literal's class again (a method
	// returning `this`): that nested ask registered it, and it is the one
	// class of the literal.
	if name, ok := e.objLitClasses[lit]; ok {
		return name
	}
	e.objLitClassCtr++
	className := fmt.Sprintf("__kml_objlit_%d", e.objLitClassCtr)
	ty := ClassType(className, nil, ownFields, false)
	e.interfaces[className] = ty

	info := ClassInfo{
		Ty:                      ty,
		OwnFields:               ownFields,
		FlatFields:              ownFields,
		Methods:                 make(map[string]*ast.FunctionDeclaration),
		MethodSigs:              make(map[string]FuncSig),
		MethodImplementor:       make(map[string]string),
		MethodDispatchSlot:      make(map[string]*MethodSlot),
		TagID:                   e.classTypeID(className),
		RootClass:               className,
		FieldOrigin:             make(map[string]string),
		StaticFieldTypes:        make(map[string]Type),
		OwnStaticFieldTypes:     make(map[string]Type),
		StaticFieldOwner:        make(map[string]string),
		StaticMethodSigs:        make(map[string]FuncSig),
		StaticMethodImplementor: make(map[string]string),
	}
	for _, f := range ownFields {
		info.FieldOrigin[f.Name] = className
	}
	// Registered before the accessor signatures: a getter returning the
	// literal's own binding (`get self() { return a; }`) asks for this class
	// while its signature is built.
	e.classes[className] = info
	e.objLitClasses[lit] = className

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
		_, dup := info.MethodSigs[key]
		fd := &ast.FunctionDeclaration{Name: p.Key, Params: fe.Params, ReturnType: fe.RetType, Body: fe.Body, Src: fe.Src}
		e.pushScope()
		e.define("this", Symbol{Ty: ty})
		sig := e.buildFunctionSig(fd)
		e.popScope()
		if p.AccessorKind == "set" {
			sig.RetType = TypeVoid
		} else if sig.RetType.IR == "void" {
			// A getter falling off its end, or one that only throws (the
			// checker reports a getter TypeScript requires to return):
			// undefined.
			sig.RetType = TypeAny
		}
		// A later accessor of the same kind and name replaces the earlier,
		// as a later property definition does.
		info.MethodSigs[key] = sig
		info.MethodImplementor[key] = className
		info.Methods[key] = fd
		if !dup {
			info.MethodOrder = append(info.MethodOrder, key)
		}
	}

	// Its property names in source order, each once.
	named := map[string]bool{}
	for _, p := range lit.Properties {
		if p.Key == "" || named[p.Key] {
			continue
		}
		if _, isAssign := p.Value.(*ast.AssignmentExpression); isAssign {
			continue
		}
		named[p.Key] = true
		info.OwnKeyOrder = append(info.OwnKeyOrder, p.Key)
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

	if err := e.emitObjLitBodies(lit, className); err != nil {
		return Value{}, err
	}
	if e.objLitInstances == nil {
		e.objLitInstances = map[string]bool{}
	}
	e.objLitInstances[className] = true

	// Construct: calloc, stamp the runtime tag, store each data field.
	dataReg := e.freshReg()
	e.emitObjAllocInto(dataReg, ty)
	structIR := ty.StructIR()
	tagGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", tagGep, structIR, dataReg))
	valTag := info.TagID
	if info.IsErrorSubclass { // error type-id flag, matching class construction (TDD-00222)
		valTag = errorTypeIDStored(valTag)
	}
	e.heapStore("i64", fmt.Sprint(valTag), tagGep, 8)

	for _, p := range lit.Properties {
		if p.AccessorKind != "" || p.Key == "" {
			continue
		}
		if _, isAssign := p.Value.(*ast.AssignmentExpression); isAssign {
			return Value{}, internalAt(pos, "shorthand-default '%s' is not valid in a value object literal", p.Key)
		}
		idx, fieldTy, ok := ty.FieldIndex(p.Key)
		if !ok {
			return Value{}, internalAt(pos, "object has no field '%s'", p.Key)
		}
		if !convertiblePure(e.typeOfExpr(p.Value), fieldTy) {
			return Value{}, internalAt(pos, "field '%s' is assigned a value of an incompatible type — this compiler is a typed subset", p.Key)
		}
		gepReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gepReg, structIR, dataReg, idx))
		if err := e.storeScalarOrNullableFieldExpr(gepReg, fieldTy, p.Value); err != nil {
			return Value{}, err
		}
	}

	return Value{Ref: dataReg, Ty: ty}, nil
}

// emitObjLitBodies validates an accessor-bearing literal's properties and
// emits its synthetic class's accessor bodies, once. The literal's emission
// calls it, and so does a dynamic adapter for one of the class's methods
// (shapeMethodRecord): the class can be registered by inference while the
// literal itself is emitted another way (into an `any`), and the adapter
// must not call a body nobody emitted.
func (e *Emitter) emitObjLitBodies(lit *ast.ObjectLiteral, className string) error {
	pos := lit.GetPos()
	info := e.classes[className]
	ty := info.Ty
	if e.objLitEmitted == nil {
		e.objLitEmitted = map[string]bool{}
	}
	// The bodies are emitted once each, and only where they emit: one a
	// prototype builder reaches before the literal's own scope does
	// (shapeMethodRecord) fails on the enclosing locals it reads, and the
	// literal's emission then emits it where they resolve.
	if !e.objLitEmitted[className] {
		e.objLitEmitted[className] = true
		defer delete(e.objLitEmitted, className)
		// The last accessor of each kind and name is the one defined.
		last := map[string]int{}
		for i, p := range lit.Properties {
			if p.AccessorKind != "" {
				last[accessorMethodName(p.AccessorKind, p.Key)] = i
			}
		}
		// Reject the property forms this path doesn't handle, rather than
		// silently dropping them.
		for i, p := range lit.Properties {
			if p.AccessorKind == "" {
				if p.Key == "" {
					return internalAt(pos, "an accessor literal with a spread (the dynamic literal's)")
				}
				if _, isAssign := p.Value.(*ast.AssignmentExpression); isAssign {
					return internalAt(pos, "shorthand-default '%s' is not valid in a value object literal", p.Key)
				}
				continue
			}
			fe := p.Value.(*ast.FunctionExpression)
			if p.AccessorKind == "get" && len(fe.Params) != 0 {
				return internalAt(pos, "a getter ('get %s') takes no parameters", p.Key)
			}
			if p.AccessorKind == "set" && len(fe.Params) != 1 {
				return internalAt(pos, "a setter ('set %s') takes exactly one parameter", p.Key)
			}
			key := accessorMethodName(p.AccessorKind, p.Key)
			sig := info.MethodSigs[key]
			if last[key] != i {
				continue // replaced by a later one
			}
			llvmName := llvmSafeSymbol(className + "_" + key)
			if e.objLitEmitted[llvmName] {
				continue
			}
			// Its own function, whatever function is open where this runs
			// (a prototype builder reaching it through shapeMethodRecord).
			restore := e.beginDetachedFunc()
			err := e.emitClassMember(llvmName, ty, fe.Params, sig, fe.Body, sig.RetType, pos, false, false)
			restore()
			if err != nil {
				return err
			}
			e.objLitEmitted[llvmName] = true
		}
	}

	return nil
}
