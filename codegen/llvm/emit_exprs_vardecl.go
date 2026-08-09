package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// emitVarDecl handles variable declarations (scalar, array, and object).
func (e *Emitter) emitVarDecl(v *ast.VarDeclaration) error {
	if init, ok := v.Init.(*ast.NewMapExpression); ok {
		return e.emitMapVarDecl(v, init)
	}
	if init, ok := v.Init.(*ast.NewSetExpression); ok {
		return e.emitSetVarDecl(v, init)
	}
	if init, ok := v.Init.(*ast.NewEventEmitterExpression); ok {
		return e.emitEventEmitterVarDecl(v, init)
	}

	ty := e.resolveType(v.TypeAnnot)

	// Infer type from init when no annotation.
	if !ty.IsArray && !ty.IsObject && v.TypeAnnot == nil {
		switch init := v.Init.(type) {
		case *ast.NullLiteral:
			if init.IsUndefined {
				ty = TypeUndefined
			} else {
				ty = TypeNull
			}
		case *ast.StringLiteral:
			ty = TypePtr
		case *ast.TemplateLiteral:
			ty = TypePtr
		case *ast.Identifier:
			if sym, ok := e.lookup(init.Name); ok {
				ty = sym.Ty
			} else {
				switch init.Name {
				case "NaN", "Infinity":
					ty = TypeF64
				}
			}
		case *ast.IndexExpression:
			ty = e.inferExprType(init)
		case *ast.BinaryExpression:
			ty = e.inferExprType(init)
		case *ast.ArrayLiteral:
			ty = e.inferArrayType(init)
		case *ast.ObjectLiteral:
			ty = e.inferObjectType(init)
		case *ast.NewErrorExpression:
			ty = errorObjType
		case *ast.NewDateExpression:
			ty = TypeDate
		case *ast.NewURLExpression:
			ty = URLType()
		case *ast.NewURLSearchParamsExpression:
			ty = URLSearchParamsType()
		case *ast.NewArrayBufferExpression:
			ty = ArrayBufferType()
		case *ast.NewTypedArrayExpression:
			ty = TypedArrayType(init.ElemKind)
		case *ast.NewTextEncoderExpression:
			ty = TextEncoderType()
		case *ast.NewTextDecoderExpression:
			ty = TextDecoderType()
		case *ast.NewRegExpExpression:
			ty = RegExpType()
		case *ast.NewEventSourceExpression:
			ty = EventSourceType()
		case *ast.NewWebSocketExpression:
			ty = WebSocketClientType()
		case *ast.NewHeadersExpression:
			ty = HeadersType()
		case *ast.NewRequestExpression:
			ty = FetchRequestType()
		case *ast.NewXMLHttpRequestExpression:
			ty = XMLHttpRequestType()
		case *ast.AwaitExpression:
			ty = e.inferExprType(init)
		case *ast.ArrowFunction:
			ty = e.inferExprType(init)
		case *ast.MemberExpression:
			ty = e.inferExprType(init)
		case *ast.CallExpression:
			if callee, ok := init.Callee.(*ast.Identifier); ok {
				switch callee.Name {
				case "fetch":
					ty = PromiseOf(ResponseType())
				case "btoa", "atob", "encodeURIComponent", "decodeURIComponent", "encodeURI", "decodeURI":
					ty = TypePtr
				case "structuredClone":
					if len(init.Args) == 1 {
						ty = e.inferExprType(init.Args[0])
					}
				case "Symbol":
					ty = SymbolType()
				default:
					// A plain `ptr`-shaped return (a string — see isStringTy)
					// was missing from this condition entirely alongside
					// array/object/func/date/map/set: since ty otherwise stays
					// this switch's TypeI64 default, a string-returning
					// function assigned to an unannotated const/let allocated
					// an i64-sized slot for what emitExpr(v.Init) then
					// actually produces as a ptr value — a real, pre-existing,
					// unrelated bug (confirmed with a plain, non-generic
					// function too) found while wiring TDD-00010 V1's
					// generic-function call support, whose most natural usage
					// (`const y = identity("hi")`) hit this exact gap.
					if sig, found := e.funcs[callee.Name]; found && (sig.RetType.IsArray || sig.RetType.IsObject || sig.RetType.IsFunc || sig.RetType.IsDate || sig.RetType.IsMap || sig.RetType.IsSet || sig.RetType.IsDynamic || isStringTy(sig.RetType)) {
						ty = sig.RetType
					} else if sym, found := e.lookup(callee.Name); found && sym.Ty.IsFunc && sym.Ty.FuncRetType != nil {
						// Calling a closure-typed variable (e.g. a const-bound
						// arrow function) rather than a named declaration —
						// same fallback as inferExprType's CallExpression case.
						retTy := *sym.Ty.FuncRetType
						if retTy.IsArray || retTy.IsObject || retTy.IsFunc || retTy.IsDate || retTy.IsMap || retTy.IsSet || isStringTy(retTy) {
							ty = retTy
						}
					} else if genDecl, found := e.genericFuncs[callee.Name]; found {
						// Generic function (TDD-00010 V1): infer purely, same
						// reasoning as the NewExpression branch below —
						// emitVarDecl's pre-inference switch must not trigger
						// real emission as a side effect.
						if retTy, ok := e.genericCallReturnType(genDecl, init.Args); ok {
							ty = retTy
						}
					}
				}
			}
			// Built-in methods that return arrays with the same element type.
			if mem, ok := init.Callee.(*ast.MemberExpression); ok {
				switch mem.Property {
				case "splice":
					if arrId, ok := mem.Object.(*ast.Identifier); ok {
						if s, found := e.lookup(arrId.Name); found && s.Ty.IsArray {
							ty = s.Ty
						}
					}
				case "pop", "shift":
					if arrId, ok := mem.Object.(*ast.Identifier); ok {
						if s, found := e.lookup(arrId.Name); found && s.Ty.IsArray && s.Ty.ElemType != nil && s.Ty.ElemType.IsObject {
							ty = *s.Ty.ElemType
						}
					}
				default:
					inferred := e.inferExprType(init)
					if inferred.IR != TypeI64.IR || inferred.IsArray || inferred.IsObject {
						ty = inferred
					}
				}
			}
		case *ast.NewArrayExpression:
			if init.ElemType != nil {
				ty = ArrayOf(e.resolveType(init.ElemType))
			}
		case *ast.NewExpression:
			if info, ok := e.classes[init.ClassName]; ok {
				ty = info.Ty
			} else if genDecl, ok := e.genericClasses[init.ClassName]; ok && len(init.TypeArgs) == len(genDecl.TypeParams) {
				// Pure lookup only (see genericClassInstanceType's doc
				// comment) — the real, memoized instantiation still happens
				// exactly once, from emitExpr(v.Init) below via
				// emitNewExpression.
				subs := e.buildTypeArgSubs(genDecl.TypeParams, init.TypeArgs)
				if instTy, err := e.genericClassInstanceType(genDecl, subs); err == nil {
					ty = instTy
				}
			}
		}
	}
	if _, ok := v.Init.(*ast.NewArrayExpression); ok && !ty.IsArray {
		return fmt.Errorf("%d:%d: new Array() requires a type annotation or a type parameter, e.g. new Array<number>(n)", v.GetPos().Line, v.GetPos().Col)
	}

	if containsDynamicElement(ty) {
		return fmt.Errorf("%d:%d: any/unknown is not yet supported as an array element or object field type", v.GetPos().Line, v.GetPos().Col)
	}
	if err := validateUnionMembers(ty, v.GetPos().Line, v.GetPos().Col); err != nil {
		return err
	}
	if ty.UnionMembers != nil && !ty.Nullable && v.Init == nil {
		return fmt.Errorf("%d:%d: a union type without null/undefined as a member requires an initializer", v.GetPos().Line, v.GetPos().Col)
	}
	if ty.IsArray {
		return e.emitArrayVarDecl(v, ty)
	}
	if ty.IsObject || ty.IsDynamicObject {
		return e.emitObjectVarDecl(v, ty)
	}

	// If init is a float literal and no explicit type, use f64.
	if v.TypeAnnot == nil {
		if nl, ok := v.Init.(*ast.NumberLiteral); ok && strings.ContainsRune(nl.Value, '.') {
			ty = TypeF64
		}
	}

	ptrName := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align %d", ptrName, ty.IR, ty.Align()))
	e.define(v.Name, Symbol{Ptr: ptrName, Ty: ty, IsConst: v.Kind == "const"})

	if v.Init != nil {
		// JSON.parse needs the target type to choose number vs string deserialization.
		if ce, ok := v.Init.(*ast.CallExpression); ok {
			if mem, ok2 := ce.Callee.(*ast.MemberExpression); ok2 {
				if id, ok3 := mem.Object.(*ast.Identifier); ok3 && id.Name == "JSON" && mem.Property == "parse" {
					val, err := e.emitJSONParse(ce.Args, ty, ce.GetPos())
					if err != nil {
						return err
					}
					e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, val.Ref, ptrName, ty.Align()))
					return nil
				}
			}
			// response.json() needs the same target-type context as JSON.parse
			// itself, for exactly the same reason (emitResponseJSON delegates
			// to emitJSONParseValue once it has the buffered body string).
			if mem, ok2 := ce.Callee.(*ast.MemberExpression); ok2 {
				if mem.Property == "json" && e.inferExprType(mem.Object).IsResponse {
					val, err := e.emitResponseJSON(mem.Object, ty, ce.GetPos())
					if err != nil {
						return err
					}
					e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, val.Ref, ptrName, ty.Align()))
					return nil
				}
			}
		}
		val, err := e.emitExprWithObjectHint(v.Init, ty)
		if err != nil {
			return err
		}
		if ty.IsDynamic {
			if ty.UnionMembers != nil && !unionAllowsAssignmentFrom(ty, val.Ty) {
				return fmt.Errorf("%d:%d: value's type is not a member of the declared union type", v.GetPos().Line, v.GetPos().Col)
			}
			val, err = e.emitBoxValue(val)
			if err != nil {
				return err
			}
		} else {
			val = e.coerce(val, ty)
		}
		// Re-resolve the variable's current storage location rather than
		// trusting ptrName (captured above, before the initializer ran):
		// if evaluating the initializer itself created a closure that
		// captures this same variable — e.g. the self-cancelling-timer
		// idiom `const id = setInterval(() => { ...; clearInterval(id) },
		// ms)` — ADR-00001's capture-time promotion (boxing) already moved
		// v.Name from ptrName to a new shared heap cell via
		// updateSymbolInPlace. Storing into the now-stale ptrName in that
		// case would silently write the real value nowhere anyone (least
		// of all the closure itself) still reads from.
		finalPtr := ptrName
		if sym, ok := e.lookup(v.Name); ok {
			finalPtr = sym.Ptr
		}
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, val.Ref, finalPtr, ty.Align()))
	} else if ty.IsDynamic {
		// No initializer: any/unknown default to undefined (matching JS `let x: any;`
		// -> x === undefined), rather than leaving the tag byte as uninitialized
		// garbage, which would drive real runtime branching in print/typeof/equality.
		undef, err := e.emitBoxValue(Value{Ty: TypeUndefined})
		if err != nil {
			return err
		}
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ty.IR, undef.Ref, ptrName, ty.Align()))
	}
	return nil
}
