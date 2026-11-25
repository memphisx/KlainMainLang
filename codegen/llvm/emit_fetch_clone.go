package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// Response.prototype.clone and Request.prototype.clone (Fetch §5.5/§6.4,
// "clone a body"): a body already read or locked throws a TypeError;
// otherwise the copy shares the status, URL and type, gets its own Headers,
// and a non-null body is teed — the original keeps one branch, the clone
// the other, so each is read independently.
func init() {
	for _, cls := range []string{"Response", "Request"} {
		cls := cls
		intrinsics[cls+".prototype.clone"] = intrinsic{
			emit: func(e *Emitter, ex *ast.CallExpression) (Value, error) {
				mem := ex.Callee.(*ast.MemberExpression)
				objVal, err := e.emitExpr(mem.Object)
				if err != nil {
					return Value{}, err
				}
				// The receiver known only as any here (a field of an
				// object of boxed values): its own clone, looked up at
				// run time.
				if objVal.Ty.IsDynamic() {
					return e.emitDynAnyMethodCall(objVal, "clone", ex.Args, ex.GetPos())
				}
				return e.emitBodyClone(objVal, cls, ex.GetPos())
			},
			ty: func(e *Emitter, ex *ast.CallExpression) Type {
				if cls == "Response" {
					return ResponseType()
				}
				return FetchRequestType()
			},
		}
	}
}

func (e *Emitter) emitBodyClone(objVal Value, cls string, pos ast.Pos) (Value, error) {
	if !hasBodyMixin(objVal.Ty) {
		return Value{}, internalAt(pos, "not a %s", cls)
	}
	ty := objVal.Ty
	// Unusable: read (disturbed) or locked.
	used := e.emitResponseBodyUsed(objVal)
	unusable := e.orStreamFlag(objVal, used, 32)
	badL, okL := e.freshLabel("clone.unusable"), e.freshLabel("clone.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", unusable, badL, okL))
	e.emitLabel(badL)
	if cls == "Response" {
		e.emitThrowTypeError("Response.clone: Body has already been consumed.")
	} else {
		e.emitThrowTypeError("unusable")
	}
	e.emitLabel(okL)

	// The copy: every field, then its own Headers.
	cp := e.freshReg()
	e.emitObjAllocInto(cp, ty)
	for i, f := range ty.Fields {
		if f.Name == "" {
			continue
		}
		v := e.loadFieldValue(objVal, i, f.Ty)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gep, ty.StructIR(), cp, i))
		e.heapStore(f.Ty.IR, v.Ref, gep, f.Ty.Align())
	}
	const src, dst = "__kml_clone_src", "__kml_clone_dst"
	e.pushScope()
	defer e.popScope()
	for _, b := range []struct{ name, ref string }{{src, objVal.Ref}, {dst, cp}} {
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", b.ref, slot))
		e.define(b.name, Symbol{Ptr: slot, Ty: ty})
	}
	hIdx, _, _ := ty.FieldIndex("__kml_headers")
	h := e.loadFieldValue(objVal, hIdx, TypePtr)
	hasH := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne "+irPtr+" %s, null", hasH, h.Ref))
	hL, hDone := e.freshLabel("clone.headers"), e.freshLabel("clone.headers.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasH, hL, hDone))
	e.emitLabel(hL)
	nh, err := e.emitExpr(ast.NewNewExpression(e.globalLinks["Headers"], []ast.Expression{
		ast.NewMemberExpression(ast.NewIdentifier(src, pos), "headers", pos)}, pos))
	if err != nil {
		return Value{}, err
	}
	hGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", hGep, ty.StructIR(), cp, hIdx))
	e.heapStore(irPtr, nh.Ref, hGep, 8)
	e.emitTerminator(fmt.Sprintf("br label %%%s", hDone))
	e.emitLabel(hDone)

	if cls != "Response" {
		// A Request's body is its bytes (never a stream): shared, each
		// side read on its own. A stream already handed out stays the
		// original's.
		sIdx, _, _ := ty.FieldIndex("__kml_stream")
		sGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", sGep, ty.StructIR(), cp, sIdx))
		e.heapStore(irPtr, "null", sGep, 8)
		return Value{Ref: cp, Ty: ty}, nil
	}

	// A non-null body: tee `.body`, each side a stream body over its branch.
	fIdx, _, _ := ty.FieldIndex("__kml_bodyflags")
	flags := e.loadFieldValue(objVal, fIdx, TypeI64)
	nb := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", nb, flags.Ref, responseNullBody))
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isNull, nb))
	teeL, endL := e.freshLabel("clone.tee"), e.freshLabel("clone.end")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, endL, teeL))
	e.emitLabel(teeL)
	// The body itself, not a `body` property read: a program writing a
	// property of that name reads it through the own-property shadow, as a
	// box.
	bs, err := e.emitResponseBodyStream(ast.NewMemberExpression(ast.NewIdentifier(src, pos), "body", pos))
	if err != nil {
		return Value{}, err
	}
	// A stream body of untyped chunks (responseStreamAny) tees boxed values.
	anyBit := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", anyBit, flags.Ref, responseStreamAny))
	isAny := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isAny, anyBit))
	anyL, u8L, joinL := e.freshLabel("clone.tee.any"), e.freshLabel("clone.tee.u8"), e.freshLabel("clone.tee.join")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isAny, anyL, u8L))
	var refs [2][2]string
	var ends [2]string
	for k, c := range []struct {
		label string
		chunk Type
	}{{anyL, TypeAny}, {u8L, TypedArrayType("uint8")}} {
		e.emitLabel(c.label)
		x, y, err := e.emitStreamTeeBranches(bs.Ref, c.chunk, pos)
		if err != nil {
			return Value{}, err
		}
		refs[k] = [2]string{x, y}
		ends[k] = e.freshLabel("clone.tee.end")
		e.emitTerminator(fmt.Sprintf("br label %%%s", ends[k]))
		e.emitLabel(ends[k])
		e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	}
	e.emitLabel(joinL)
	b1, b2 := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi "+irPtr+" [ %s, %%%s ], [ %s, %%%s ]", b1, refs[0][0], ends[0], refs[1][0], ends[1]))
	e.emitInstr(fmt.Sprintf("%s = phi "+irPtr+" [ %s, %%%s ], [ %s, %%%s ]", b2, refs[0][1], ends[0], refs[1][1], ends[1]))
	if err := e.ensureResponseStreamCollect(); err != nil {
		return Value{}, internalAt(pos, "%v", err)
	}
	e.respCollectCalled = true
	sIdx, _, _ := ty.FieldIndex("__kml_stream")
	for i, target := range []string{objVal.Ref, cp} {
		br := []string{b1, b2}[i]
		sGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", sGep, ty.StructIR(), target, sIdx))
		e.heapStore(irPtr, br, sGep, 8)
		fGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", fGep, ty.StructIR(), target, fIdx))
		f := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", f, fGep))
		f2 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i64 %s, %d", f2, f, responseStreamBody))
		e.heapStore("i64", f2, fGep, 8)
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", endL))
	e.emitLabel(endL)
	return Value{Ref: cp, Ty: ty}, nil
}
