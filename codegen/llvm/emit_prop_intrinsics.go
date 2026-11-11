// emit_prop_intrinsics.go — a builtin property whose declaration names an
// intrinsic (`/** @intrinsic Owner.prototype.name */` on a property
// signature, TDD-00230 P3.5): its read is emitted, and typed, by that
// entry rather than by a branch on the property's name.
package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// propIntrinsic reads one builtin property of a receiver codegen holds as
// a representation held accepts.
type propIntrinsic struct {
	held func(Type) bool
	emit func(e *Emitter, ex *ast.MemberExpression) (Value, error)
	ty   func(e *Emitter, ex *ast.MemberExpression) Type
}

var propIntrinsics map[string]propIntrinsic

func init() {
	body := func(t Type) bool { return hasBodyMixin(t) }
	response := func(t Type) bool { return t.IsResponse() }
	typedArray := func(t Type) bool { return t.IsTypedArray() }
	arrayBuffer := func(t Type) bool { return t.IsArrayBuffer() }
	propIntrinsics = map[string]propIntrinsic{
		// Response.body / Request.body — must match emitResponseBodyStream
		// (TDD-00097 Stage 4).
		"Body.prototype.body": {
			held: body,
			emit: func(e *Emitter, ex *ast.MemberExpression) (Value, error) { return e.emitResponseBodyStream(ex) },
			ty:   func(*Emitter, *ast.MemberExpression) Type { return bodyStreamType() },
		},
		"Body.prototype.bodyUsed": {
			held: body,
			emit: func(e *Emitter, ex *ast.MemberExpression) (Value, error) {
				objVal, err := e.emitExpr(ex.Object)
				if err != nil {
					return Value{}, err
				}
				return Value{Ref: e.emitResponseBodyUsed(objVal), Ty: TypeBool}, nil
			},
			ty: func(*Emitter, *ast.MemberExpression) Type { return TypeBool },
		},
		"Response.prototype.type": {
			held: response,
			emit: (*Emitter).emitResponseType,
			ty:   func(*Emitter, *ast.MemberExpression) Type { return TypePtr },
		},
		"Response.prototype.statusText": {
			held: response,
			emit: (*Emitter).emitResponseStatusText,
			ty:   func(*Emitter, *ast.MemberExpression) Type { return TypePtr },
		},
		"Request.prototype.signal": {
			held: func(t Type) bool { return t.IsFetchRequest() },
			emit: func(e *Emitter, ex *ast.MemberExpression) (Value, error) {
				objVal, err := e.emitExpr(ex.Object)
				if err != nil {
					return Value{}, err
				}
				cls, ok := e.abortSignalClass()
				if !ok {
					return Value{}, internalAt(ex.GetPos(), "a Request's signal without the AbortSignal module (Request links it)")
				}
				idx, fty, _ := objVal.Ty.FieldIndex("__kml_signal")
				v := e.loadFieldValue(objVal, idx, fty)
				return Value{Ref: v.Ref, Ty: cls}, nil
			},
			ty: func(e *Emitter, _ *ast.MemberExpression) Type {
				cls, _ := e.abortSignalClass()
				return cls
			},
		},
		"Response.prototype.url": {
			held: response,
			emit: func(e *Emitter, ex *ast.MemberExpression) (Value, error) {
				return e.emitResponseFetched(ex, "__kml_fetch_url", TypePtr, e.internString(""))
			},
			ty: func(*Emitter, *ast.MemberExpression) Type { return TypePtr },
		},
		"Response.prototype.redirected": {
			held: response,
			emit: func(e *Emitter, ex *ast.MemberExpression) (Value, error) {
				return e.emitResponseFetched(ex, "__kml_fetch_redirected", TypeBool, "false")
			},
			ty: func(*Emitter, *ast.MemberExpression) Type { return TypeBool },
		},
	}
	// A Map's or Set's size: the count its header's first word holds.
	for _, owner := range []string{"Map", "Set"} {
		propIntrinsics[owner+".prototype.size"] = propIntrinsic{
			held: func(t Type) bool { return (t.IsMap() || t.IsSet()) && !t.Weak && !t.IsDynamicObject() },
			emit: func(e *Emitter, ex *ast.MemberExpression) (Value, error) {
				_, ptr, err := e.resolveMapOrSetForCall(ex.Object, ex.GetPos())
				if err != nil {
					return Value{}, err
				}
				r := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", r, ptr))
				return Value{Ref: r, Ty: TypeI64}, nil
			},
			ty: func(*Emitter, *ast.MemberExpression) Type { return TypeI64 },
		}
	}
	// A web stream's, reader's, writer's or controller's own state — must
	// match emitStreamProp's entry (streamProps).
	for _, key := range []string{
		"ReadableStream.prototype.locked", "ReadableStreamDefaultController.prototype.desiredSize",
		"ReadableStreamGenericReader.prototype.closed",
	} {
		propIntrinsics[key] = streamPropIntrinsic(false)
	}
	for _, key := range []string{
		"WritableStream.prototype.locked", "WritableStreamDefaultWriter.prototype.closed",
		"WritableStreamDefaultWriter.prototype.desiredSize", "WritableStreamDefaultWriter.prototype.ready",
	} {
		propIntrinsics[key] = streamPropIntrinsic(true)
	}
	// A Response's or Request's Headers (a fetched response's filled on
	// first read).
	for _, owner := range []string{"Response", "Request"} {
		propIntrinsics[owner+".prototype.headers"] = propIntrinsic{
			held: func(t Type) bool { return t.IsResponse() || t.IsFetchRequest() },
			emit: func(e *Emitter, ex *ast.MemberExpression) (Value, error) {
				objVal, err := e.emitExpr(ex.Object)
				if err != nil {
					return Value{}, err
				}
				if objVal.Ty.IsResponse() {
					return e.emitResponseHeaders(objVal, ex.GetPos())
				}
				return e.emitRequestHeaders(objVal, ex.GetPos())
			},
			ty: func(e *Emitter, ex *ast.MemberExpression) Type {
				if cls, ok := e.globalClass("Headers"); ok {
					return cls
				}
				return TypeAny
			},
		}
	}
	// A TransformStream's sides.
	for slot, name := range []string{"readable", "writable"} {
		name, slot := name, slot
		var ty func(e *Emitter, ex *ast.MemberExpression) Type
		propIntrinsics["TransformStream.prototype."+name] = propIntrinsic{
			held: func(t Type) bool { return t.IsTransformStream() },
			emit: func(e *Emitter, ex *ast.MemberExpression) (Value, error) {
				return e.emitTransformStreamSide(ex, slot, ty(e, ex))
			},
			ty: func(e *Emitter, ex *ast.MemberExpression) Type {
				return ty(e, ex)
			},
		}
		ty = func(e *Emitter, ex *ast.MemberExpression) Type {
			objTy := e.typeOfExpr(ex.Object)
			if name == "readable" {
				if objTy.StreamOut != nil {
					return ReadableStreamType(*objTy.StreamOut)
				}
				return ReadableStreamType(TypeAny)
			}
			if objTy.StreamChunk != nil {
				return WritableStreamType(*objTy.StreamChunk)
			}
			return WritableStreamType(TypeAny)
		}
	}
	// A typed array's view of its buffer — must match emitTypedArrayViewProp.
	for _, name := range []string{"buffer", "byteOffset"} {
		name := name
		propIntrinsics["TypedArray.prototype."+name] = propIntrinsic{
			held: typedArray,
			emit: func(e *Emitter, ex *ast.MemberExpression) (Value, error) {
				return e.emitTypedArrayViewProp(ex.Object, name, ex.GetPos())
			},
			ty: func(*Emitter, *ast.MemberExpression) Type {
				if name == "buffer" {
					return TypeAny
				}
				return TypeI64
			},
		}
	}
	// A resizable ArrayBuffer's or growable SharedArrayBuffer's limits
	// (ADR-00494) — must match emitBufferGrowableProps.
	for _, name := range []string{"growable", "resizable", "maxByteLength"} {
		name := name
		propIntrinsics["ArrayBuffer.prototype."+name] = propIntrinsic{
			held: arrayBuffer,
			emit: func(e *Emitter, ex *ast.MemberExpression) (Value, error) {
				objVal, err := e.emitExpr(ex.Object)
				if err != nil {
					return Value{}, err
				}
				return e.emitBufferGrowableProps(objVal, name)
			},
			ty: func(*Emitter, *ast.MemberExpression) Type {
				if name == "maxByteLength" {
					return TypeI64
				}
				return TypeBool
			},
		}
	}
}

// streamPropIntrinsic reads a stream property of a readable side, or of a
// writable one.
func streamPropIntrinsic(writable bool) propIntrinsic {
	side := isReadableSide
	if writable {
		side = isWritableSide
	}
	return propIntrinsic{
		held: side,
		emit: func(e *Emitter, ex *ast.MemberExpression) (Value, error) { return e.emitStreamProp(ex, writable) },
		ty: func(e *Emitter, ex *ast.MemberExpression) Type {
			if p, ok := streamPropFor(ex.Property, e.typeOfExpr(ex.Object)); ok {
				return p.ty()
			}
			return TypeAny
		},
	}
}

// propertyIntrinsic is the intrinsic that reads ex: its declaration's, or,
// for a read the checker never saw (synthesized, untyped), the one the
// owner of codegen's representation of the receiver has under its name.
func (e *Emitter) propertyIntrinsic(ex *ast.MemberExpression) (propIntrinsic, bool) {
	if ex.Optional {
		return propIntrinsic{}, false
	}
	ot := e.typeOfExpr(ex.Object)
	if isUnconstrainedDynamic(ot) {
		return propIntrinsic{}, false
	}
	key := ""
	if c := e.front(); c != nil {
		if ps, ok := c.MemberDecl(ex).(*ast.PropertySignature); ok {
			key = ps.Intrinsic
		}
	}
	if key == "" {
		for _, owner := range reprOwners(ot) {
			if _, ok := propIntrinsics[owner+".prototype."+ex.Property]; ok {
				key = owner + ".prototype." + ex.Property
				break
			}
		}
	}
	pi, ok := propIntrinsics[key]
	if !ok || !pi.held(ot) {
		return propIntrinsic{}, false
	}
	return pi, true
}

// emitResponseType is Response.type: a constructed Response's own, else
// "basic".
func (e *Emitter) emitResponseType(ex *ast.MemberExpression) (Value, error) {
	objVal, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}
	idx, fty, ok := objVal.Ty.FieldIndex("__kml_type")
	if !ok {
		return Value{}, internalAt(ex.GetPos(), "a Response without its type")
	}
	own := e.loadFieldValue(objVal, idx, fty)
	has := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", has, own.Ref))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", r, has, own.Ref, e.internString("basic")))
	return Value{Ref: r, Ty: TypePtr}, nil
}

// emitResponseFetched reads a fetched Response's property through fn on
// its pending fetch; a constructed one (no pending fetch) has dflt, as
// Node's has url "" and redirected false.
func (e *Emitter) emitResponseFetched(ex *ast.MemberExpression, fn string, ty Type, dflt string) (Value, error) {
	objVal, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}
	pendIdx, pendTy, ok := objVal.Ty.FieldIndex("__kml_pending")
	if !ok {
		return Value{}, internalAt(ex.GetPos(), "a Response without its pending fetch")
	}
	pend := e.loadFieldValue(objVal, pendIdx, pendTy)
	has := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", has, pend.Ref))
	fetchL, dfltL, doneL := e.freshLabel("resp.f.fetch"), e.freshLabel("resp.f.dflt"), e.freshLabel("resp.f.done")
	ir := ty.IR
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", slot, ir))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, fetchL, dfltL))
	e.emitLabel(fetchL)
	e.ensureStrHeaderRuntime()
	v := e.freshReg()
	ret := ir
	if ir == "i1" {
		ret = "zeroext i1"
	}
	e.emitInstr(fmt.Sprintf("%s = call %s @%s("+irPtr+" %s)", v, ret, fn, pend.Ref))
	e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", ir, v, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(dfltL)
	e.emitInstr(fmt.Sprintf("store %s %s, "+irPtr+" %s, align 8", ir, dflt, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, "+irPtr+" %s, align 8", r, ir, slot))
	return Value{Ref: r, Ty: ty}, nil
}

// emitResponseStatusText is Response.statusText: a constructed Response's
// own, else the reason phrase of a fetched one's status line.
func (e *Emitter) emitResponseStatusText(ex *ast.MemberExpression) (Value, error) {
	objVal, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, err
	}
	pendIdx, pendTy, ok1 := objVal.Ty.FieldIndex("__kml_pending")
	stIdx, stTy, ok2 := objVal.Ty.FieldIndex("__kml_status_text")
	if !ok1 || !ok2 {
		return Value{}, internalAt(ex.GetPos(), "a Response without its status text")
	}
	own := e.loadFieldValue(objVal, stIdx, stTy)
	hasOwn := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", hasOwn, own.Ref))
	ownL, fetchL, doneL := e.freshLabel("resp.st.own"), e.freshLabel("resp.st.fetch"), e.freshLabel("resp.st.done")
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasOwn, ownL, fetchL))
	e.emitLabel(ownL)
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", own.Ref, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(fetchL)
	pend := e.loadFieldValue(objVal, pendIdx, pendTy)
	e.ensureFetch()
	e.ensureStrHeaderRuntime()
	st := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_fetch_status_text("+irPtr+" %s)", st, pend.Ref))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", st, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", r, slot))
	return Value{Ref: r, Ty: TypePtr}, nil
}
