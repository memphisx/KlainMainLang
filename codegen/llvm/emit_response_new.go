package llvm

// emit_response_new.go — `new Response(body?, init?)` (Fetch's Response
// constructor). The body is normalised through a Blob (a string, a typed
// array, an ArrayBuffer or a Blob, as `new Blob([body])` takes it) into the
// buffered-body fields a fetched Response carries once read, so text(),
// json(), arrayBuffer() and .body read it as they read a fetched one. A
// ReadableStream body is kept as the stream `.body` returns and is read to
// its end when a body method runs; no body makes `.body` null. init's
// status, statusText and headers fill the constructed Response's own slots.

import (
	"fmt"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
	"KlainMainLang/sema"
)

// Response.__kml_bodyflags bits.
const (
	responseNullBody   = 1 // `.body` is null
	responseStreamBody = 2 // the body is __kml_stream's, not yet collected
	responseBodyUsed   = 4 // a body method has run (bodyUsed)
	responseStreamAny  = 8 // the stream's chunks are not typed Uint8Array
)

// isNullBodyArg reports an absent, `null` or `undefined` body.
func isNullBodyArg(x ast.Expression) bool {
	switch v := x.(type) {
	case nil:
		return true
	case *ast.NullLiteral:
		return true
	case *ast.Identifier:
		return v.Name == "undefined"
	}
	return false
}

func (e *Emitter) emitNewResponse(ex *ast.NewExpression) (Value, error) {
	pos := ex.GetPos()
	args, err := e.dropArgsPast(ex.Args, 2, pos)
	if err != nil {
		return Value{}, err
	}
	var bodyArg, initArg ast.Expression
	if len(args) > 0 {
		bodyArg = args[0]
	}
	if len(args) > 1 {
		initArg = args[1]
	}
	return e.emitResponseCore(bodyArg, initArg, responseShape{}, pos)
}

// responseShape is what a static factory fixes about the Response the core
// builds: Response.json's content type, Response.redirect's status and
// Location, Response.error's status 0 and type.
type responseShape struct {
	contentType string // the implied content type of a string body
	status      string // a fixed status (a double register or constant); "" reads init
	location    string // a Location header value register
	typeName    string // "" is "default"
}

// emitResponseCore builds a Response from a body and an init (either may
// be nil).
func (e *Emitter) emitResponseCore(bodyArg, initArg ast.Expression, shape responseShape, pos ast.Pos) (Value, error) {
	if isNullBodyArg(initArg) {
		initArg = nil
	}
	e.ensureStrHeaderRuntime()

	// init: { status, statusText, headers }.
	status := Value{Ref: "200.0", Ty: TypeF64}
	statusText := Value{Ref: e.internString(""), Ty: TypePtr}
	var headersExpr ast.Expression
	if lit, ok := initArg.(*ast.ObjectLiteral); ok {
		// An inline init: its properties, in source order.
		for _, prop := range lit.Properties {
			switch prop.Key {
			case "status":
				v, err := e.emitExpr(prop.Value)
				if err != nil {
					return Value{}, err
				}
				status = e.convert(v, TypeF64)
			case "statusText":
				v, err := e.emitExpr(prop.Value)
				if err != nil {
					return Value{}, err
				}
				statusText = e.convert(v, TypePtr)
			case "headers":
				headersExpr = prop.Value
			}
		}
	} else if initArg != nil {
		iv, err := e.emitExpr(initArg)
		if err != nil {
			return Value{}, err
		}
		if !iv.Ty.IsObject() {
			return e.emitResponseDynamicInit(bodyArg, iv, shape, pos)
		}
		tmp := "__kml_resp_init_" + e.freshReg()[1:]
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
		e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", iv.Ref, slot))
		e.define(tmp, Symbol{Ptr: slot, Ty: iv.Ty})
		member := func(name string) ast.Expression {
			if _, _, ok := iv.Ty.FieldIndex(name); !ok {
				return nil
			}
			return ast.NewMemberExpression(ast.NewIdentifier(tmp, pos), name, pos)
		}
		if m := member("status"); m != nil {
			v, err := e.emitExpr(m)
			if err != nil {
				return Value{}, err
			}
			status = e.convert(v, TypeF64)
		}
		if m := member("statusText"); m != nil {
			v, err := e.emitExpr(m)
			if err != nil {
				return Value{}, err
			}
			statusText = e.convert(v, TypePtr)
		}
		headersExpr = member("headers")
	}

	if shape.status != "" {
		status = Value{Ref: shape.status, Ty: TypeF64}
	}
	// A status outside 200–599 is a RangeError (a factory's is its own).
	if shape.status == "" {
		e.emitResponseStatusRange(status)
	}

	// Headers: init's, or a fresh one.
	var init *Value
	if headersExpr != nil {
		hv, err := e.emitExpr(headersExpr)
		if err != nil {
			return Value{}, err
		}
		init = &hv
	}
	headers, err := e.emitNewHeaders(init, pos)
	if err != nil {
		return Value{}, err
	}
	if shape.location != "" {
		loc := e.bindValue(Value{Ref: shape.location, Ty: TypePtr}, pos)
		if _, err := e.emitHeadersMethod(headers, "set", pos, ast.NewStringLiteral("location", pos), loc); err != nil {
			return Value{}, err
		}
	}
	// Response.redirect's and Response.error's headers are immutable.
	if shape.location != "" || shape.typeName == "error" {
		if err := e.emitHeadersSeal(headers, "immutable", pos); err != nil {
			return Value{}, err
		}
	}
	return e.emitResponseBodyAndFinish(bodyArg, status, statusText, headers, shape, pos)
}

// emitResponseDynamicInit builds a Response whose init is a value of no
// fixed layout: null or undefined is no init, a primitive is the
// platform's TypeError, an object's status, statusText and headers are
// read off it.
func (e *Emitter) emitResponseDynamicInit(bodyArg ast.Expression, iv Value, shape responseShape, pos ast.Pos) (Value, error) {
	box, err := e.emitBoxValue(iv)
	if err != nil {
		return Value{}, err
	}
	if err := e.emitThrowIfPrimitiveInit(box); err != nil {
		return Value{}, err
	}
	tmp := e.bindValue(box, pos)
	read := func(name string) (Value, string, error) {
		m := ast.NewMemberExpression(tmp, name, pos)
		m.Optional = true
		v, err := e.emitExpr(m)
		if err != nil {
			return Value{}, "", err
		}
		v = e.convert(v, TypeAny)
		absent := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", absent, v.Ref, nbUndefined))
		return v, absent, nil
	}
	sv, sAbsent, err := read("status")
	if err != nil {
		return Value{}, err
	}
	sNum := e.convert(sv, TypeF64)
	status := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, double 200.0, double %s", status, sAbsent, sNum.Ref))
	tv, tAbsent, err := read("statusText")
	if err != nil {
		return Value{}, err
	}
	tStr, err := e.emitValueToString(tv)
	if err != nil {
		return Value{}, err
	}
	statusText := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" %s, "+irPtr+" %s", statusText, tAbsent, e.internString(""), tStr.Ref))
	hv, hAbsent, err := read("headers")
	if err != nil {
		return Value{}, err
	}
	statusV := Value{Ref: status, Ty: TypeF64}
	if shape.status != "" {
		statusV = Value{Ref: shape.status, Ty: TypeF64}
	} else {
		e.emitResponseStatusRange(statusV)
	}
	// Headers: the init's when it has them, else a fresh one.
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
	withL, freshL, joinL := e.freshLabel("respinit.hdrs"), e.freshLabel("respinit.nohdrs"), e.freshLabel("respinit.hdrsjoin")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hAbsent, freshL, withL))
	e.emitLabel(withL)
	h1, err := e.emitNewHeaders(&hv, pos)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", h1.Ref, slot))
	e.emitTerminator("br label %" + joinL)
	e.emitLabel(freshL)
	h2, err := e.emitNewHeaders(nil, pos)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", h2.Ref, slot))
	e.emitTerminator("br label %" + joinL)
	e.emitLabel(joinL)
	hr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", hr, slot))
	headers := Value{Ref: hr, Ty: h2.Ty}
	if shape.location != "" {
		loc := e.bindValue(Value{Ref: shape.location, Ty: TypePtr}, pos)
		if _, err := e.emitHeadersMethod(headers, "set", pos, ast.NewStringLiteral("location", pos), loc); err != nil {
			return Value{}, err
		}
	}
	if shape.location != "" || shape.typeName == "error" {
		if err := e.emitHeadersSeal(headers, "immutable", pos); err != nil {
			return Value{}, err
		}
	}
	return e.emitResponseBodyAndFinish(bodyArg, statusV, Value{Ref: statusText, Ty: TypePtr}, headers, shape, pos)
}

// emitResponseStatusRange throws init["status"]'s RangeError for a status
// outside 200–599.
func (e *Emitter) emitResponseStatusRange(status Value) {
	lo, hi := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp olt double %s, 200.0", lo, status.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp ogt double %s, 599.0", hi, status.Ref))
	bad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", bad, lo, hi))
	badL, okL := e.freshLabel("resp.badstatus"), e.freshLabel("resp.status")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
	e.emitLabel(badL)
	e.ensureExceptionHelpers()
	rangeErr := e.buildErrorObj(errorKindIDs["RangeError"], e.internString(`init["status"] must be in the range of 200 to 599, inclusive.`), e.internString("RangeError"))
	e.excThrow(rangeErr)
	e.emitTerminator("unreachable")
	e.emitLabel(okL)
}

// emitResponseBodyAndFinish normalises the body and fills the Response.
func (e *Emitter) emitResponseBodyAndFinish(bodyArg ast.Expression, status, statusText, headers Value, shape responseShape, pos ast.Pos) (Value, error) {
	// The body's bytes and the content type it implies.
	bodyRef, lenRef := e.internString(""), "0"
	contentType := ""
	blobType := ""
	nullBody := isNullBodyArg(bodyArg)
	streamRef := "null"
	bodyFlags := responseNullBody
	if !nullBody {
		bodyFlags = 0
		bt := e.typeOfExpr(bodyArg)
		switch {
		case bt.IsReadableStream():
			// A stream body: `.body` is that stream; text()/json()/
			// arrayBuffer() read it to its end first (@__kml_resp_collect).
			sv, err := e.emitExpr(bodyArg)
			if err != nil {
				return Value{}, err
			}
			streamRef = sv.Ref
			bodyFlags = responseStreamBody
			if sv.Ty.StreamChunk == nil || !sv.Ty.StreamChunk.IsTypedArray() || sv.Ty.StreamChunk.ElemType == nil || sv.Ty.StreamChunk.ElemType.IR != "i8" {
				// Read as `any`, each chunk checked (a non-Uint8Array one
				// rejects the read, as Node's does).
				bodyFlags |= responseStreamAny
			}
			if err := e.ensureResponseStreamCollect(); err != nil {
				return Value{}, internalAt(pos, "%v", err)
			}
		case e.isGlobalClassInstance(bt, "URLSearchParams"):
			sv, err := e.emitExpr(ast.NewCallExpression(ast.NewMemberExpression(bodyArg, "toString", pos), nil, pos))
			if err != nil {
				return Value{}, err
			}
			bodyRef = sv.Ref
			l := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len("+irPtr+" %s)", l, sv.Ref))
			lenRef = l
			contentType = "application/x-www-form-urlencoded;charset=UTF-8"
		default:
			isBlob := e.isGlobalClassInstance(bt, "Blob")
			if e.isGlobalClassInstance(bt, "FormData") {
				// A FormData body is its multipart/form-data encoding: a
				// Blob whose type carries the boundary.
				bodyArg = ast.NewCallExpression(ast.NewMemberExpression(bodyArg, "kmlBlob", pos), nil, pos)
				isBlob = true
			}
			if isStringTy(bt) && !bt.IsTypedArray() && !bt.IsArrayBuffer() && !bt.IsClass() {
				contentType = "text/plain;charset=UTF-8"
				if shape.contentType != "" {
					contentType = shape.contentType
				}
			}
			// A Blob is read as it is (its type is the content type); any
			// other body through one.
			var bv Value
			var err error
			if isBlob {
				bv, err = e.emitExpr(bodyArg)
			} else {
				bv, err = e.emitNewBlobOf(bodyArg, pos)
			}
			if err != nil {
				return Value{}, err
			}
			data, size, tv, err := e.emitBlobBytes(bv, pos)
			if err != nil {
				return Value{}, err
			}
			if isBlob {
				blobType = tv
			}
			buf := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_str_alloc(i64 %s)", buf, size))
			e.emitInstr(fmt.Sprintf("call "+irPtr+" @memcpy("+irPtr+" %s, "+irPtr+" %s, i64 %s)", buf, data, size))
			end := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", end, buf, size))
			e.heapStore("i8", "0", end, 1)
			bodyRef, lenRef = buf, size
		}
		// A null-body status (101, 103, 204, 205, 304) with a body is a
		// TypeError.
		isNullStatus := ""
		for _, code := range []string{"101.0", "103.0", "204.0", "205.0", "304.0"} {
			c := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, %s", c, status.Ref, code))
			if isNullStatus == "" {
				isNullStatus = c
				continue
			}
			o := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", o, isNullStatus, c))
			isNullStatus = o
		}
		nbL, nbOkL := e.freshLabel("resp.nullbodystatus"), e.freshLabel("resp.bodyok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullStatus, nbL, nbOkL))
		e.emitLabel(nbL)
		msg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_str_alloc(i64 64)", msg))
		si := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", si, status.Ref))
		e.emitInstr(fmt.Sprintf("call i32 ("+irPtr+", "+irPtr+", ...) @sprintf("+irPtr+" %s, "+irPtr+" %s, i64 %s)", msg, e.internString("Response constructor: Invalid response status code %lld"), si))
		e.emitInstr(fmt.Sprintf("call void @__kml_str_finalize("+irPtr+" %s)", msg))
		e.emitThrowTypeErrorValue(msg)
		e.emitLabel(nbOkL)
	}

	// The implied Content-Type, unless init's headers set one: a Blob's
	// own type, when it has one.
	if contentType != "" || blobType != "" {
		ctRef := e.internString(contentType)
		if blobType != "" {
			l, empty := e.freshReg(), e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len("+irPtr+" %s)", l, blobType))
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", empty, l))
			ctRef = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, "+irPtr+" null, "+irPtr+" %s", ctRef, empty, blobType))
		}
		if err := e.emitSetHeaderIfAbsent(headers, "content-type", ctRef, pos); err != nil {
			return Value{}, err
		}
	}

	respTy := ResponseType()
	resp := e.freshReg()
	e.emitObjAllocInto(resp, respTy)
	store := func(name, ir, ref string) {
		idx, _, _ := respTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", gep, respTy.StructIR(), resp, idx))
		e.heapStore(ir, ref, gep, 8)
	}
	okLo, okHi, ok := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp oge double %s, 200.0", okLo, status.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp olt double %s, 300.0", okHi, status.Ref))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", ok, okLo, okHi))
	store("status", "double", status.Ref)
	store("ok", "i1", ok)
	store("body", irPtr, bodyRef)
	store("bodyLength", "i64", lenRef)
	store("__kml_headers", irPtr, headers.Ref)
	store("__kml_status_text", irPtr, statusText.Ref)
	typeName := shape.typeName
	if typeName == "" {
		typeName = "default"
	}
	store("__kml_type", irPtr, e.internString(typeName))
	store("__kml_stream", irPtr, streamRef)
	store("__kml_bodyflags", "i64", fmt.Sprint(bodyFlags))
	return Value{Ref: resp, Ty: respTy}, nil
}

// emitResponseStaticJson is Response.json(data, init?).
func (e *Emitter) emitResponseStaticJson(args []ast.Expression, pos ast.Pos) (Value, error) {
	arg := func(i int) ast.Expression {
		if i < len(args) {
			return args[i]
		}
		return nil
	}
	args, short, err := e.responseFactoryArity(args, "json", pos)
	if err != nil || short {
		return Value{Ref: "null", Ty: ResponseType()}, err
	}
	text, err := e.emitJSONStringify(args[:1], pos)
	if err != nil {
		return Value{}, err
	}
	return e.emitResponseCore(e.bindValue(text, pos), arg(1), responseShape{contentType: "application/json"}, pos)
}

// emitResponseStaticRedirect is Response.redirect(url, status?).
func (e *Emitter) emitResponseStaticRedirect(args []ast.Expression, pos ast.Pos) (Value, error) {
	args, short, err := e.responseFactoryArity(args, "redirect", pos)
	if err != nil || short {
		return Value{Ref: "null", Ty: ResponseType()}, err
	}
	// The Location is the parsed URL, serialized.
	urlClass, linked := e.globalClass("URL")
	if !linked {
		return Value{}, internalAt(pos, "Response.redirect without the URL class linked")
	}
	hrefExpr := ast.Expression(ast.NewMemberExpression(ast.NewNewExpression(urlClass.ClassName, []ast.Expression{args[0]}, pos), "href", pos))
	wrap := &ast.Program{Body: []ast.Statement{ast.NewExpressionStatement(hrefExpr, pos)}}
	if err := sema.Prepare(wrap); err != nil {
		return Value{}, err
	}
	href, err := e.emitExpr(wrap.Body[0].(*ast.ExpressionStatement).Expr)
	if err != nil {
		return Value{}, err
	}
	status := Value{Ref: "302.0", Ty: TypeF64}
	if len(args) > 1 {
		sv, err := e.emitExpr(args[1])
		if err != nil {
			return Value{}, err
		}
		status = e.convert(sv, TypeF64)
	}
	ok := ""
	for _, code := range []string{"301.0", "302.0", "303.0", "307.0", "308.0"} {
		c := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fcmp oeq double %s, %s", c, status.Ref, code))
		if ok == "" {
			ok = c
			continue
		}
		o := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", o, ok, c))
		ok = o
	}
	badL, goodL := e.freshLabel("resp.redirect.bad"), e.freshLabel("resp.redirect.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, goodL, badL))
	e.emitLabel(badL)
	e.ensureStrHeaderRuntime()
	msg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call "+irPtr+" @__kml_str_alloc(i64 48)", msg))
	si := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", si, status.Ref))
	e.emitInstr(fmt.Sprintf("call i32 ("+irPtr+", "+irPtr+", ...) @sprintf("+irPtr+" %s, "+irPtr+" %s, i64 %s)", msg, e.internString("Invalid status code %lld"), si))
	e.emitInstr(fmt.Sprintf("call void @__kml_str_finalize("+irPtr+" %s)", msg))
	e.ensureExceptionHelpers()
	rangeErr := e.buildErrorObj(errorKindIDs["RangeError"], msg, e.internString("RangeError"))
	e.excThrow(rangeErr)
	e.emitTerminator("unreachable")
	e.emitLabel(goodL)
	return e.emitResponseCore(nil, nil, responseShape{status: status.Ref, location: href.Ref}, pos)
}

// emitResponseStaticError is Response.error().
func (e *Emitter) emitResponseStaticError(args []ast.Expression, pos ast.Pos) (Value, error) {
	if _, err := e.dropArgsPast(args, 0, pos); err != nil {
		return Value{}, err
	}
	return e.emitResponseCore(nil, nil, responseShape{status: "0.0", typeName: "error"}, pos)
}

// ensureResponseStreamCollect defines @__kml_resp_collect(resp, closure, q):
// a body method's start on a Response whose body is a stream. It reads the
// stream to its end (a TypeScript snippet: the reader, the chunks, one
// Blob), then @__kml_resp_collect_done stores the bytes as the
// buffered body and runs the method's runner (closure), or rejects q with
// the read's error.
func (e *Emitter) ensureResponseStreamCollect() error {
	if e.respCollectDefined {
		return nil
	}
	e.respCollectDefined = true
	e.ensureExceptionHelpers()
	e.ensureMicrotasks()
	e.ensureUnhandledRejections()
	e.ensureNanBox()
	respTy := ResponseType()
	field := func(name string) int {
		idx, _, _ := respTy.FieldIndex(name)
		return idx
	}
	e.buildRespCollectDone(respTy, field("body"), field("bodyLength"), field("__kml_bodyflags"))

	restore := e.beginDetachedFunc()
	defer restore()
	e.pushScope()
	defer e.popScope()
	resp := Value{Ref: "%resp", Ty: respTy}
	sv := e.loadFieldValue(resp, field("__kml_stream"), TypePtr)
	fl := e.loadFieldValue(resp, field("__kml_bodyflags"), TypeI64)
	anyBit := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", anyBit, fl.Ref, responseStreamAny))
	isAny := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", isAny, anyBit))
	pslot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", pslot))
	anyL, u8L, joinL := e.freshLabel("rcol.any"), e.freshLabel("rcol.u8"), e.freshLabel("rcol.join")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isAny, anyL, u8L))
	e.emitLabel(anyL)
	anyP, err := e.emitStreamCollectPromise(sv.Ref, TypeAny, `const v: any = r.value;
    if (!(v instanceof Uint8Array)) throw new TypeError("Received non-Uint8Array chunk");
    const c = v as Uint8Array;`)
	if err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", anyP, pslot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(u8L)
	u8P, err := e.emitStreamCollectPromise(sv.Ref, TypedArrayType("uint8"), `const c = r.value;`)
	if err != nil {
		return err
	}
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", u8P, pslot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(joinL)
	pv := Value{Ref: e.freshReg()}
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", pv.Ref, pslot))
	env := e.freshReg()
	e.heapMallocInto(env, allocClosure, "32")
	for i, ref := range []string{"%resp", "%clo", "%q", pv.Ref} {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { "+irPtr+", "+irPtr+", "+irPtr+", "+irPtr+" }, "+irPtr+" %s, i32 0, i32 %d", g, env, i))
		e.heapStore(irPtr, ref, g, 8)
	}
	clo := e.buildBuiltinClosure("@__kml_resp_collect_done", env)
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_attach("+irPtr+" %s, "+irPtr+" %s)", pv.Ref, clo))
	e.emitTerminator("ret void")
	body := e.allocas.String() + e.body.String()
	e.writeFunc("internal void @__kml_resp_collect("+irPtr+" %resp, "+irPtr+" %clo, "+irPtr+" %q)", body)
	return nil
}

// buildRespCollectDone defines @__kml_resp_collect_done(env): env holds
// {resp, runner closure, q, the bytes promise p}. Fulfilled, p's v0 is the
// Uint8Array header; its bytes (NUL-terminated) become resp's buffered body,
// the stream flags are cleared and the runner runs. Rejected, q rejects
// with p's reason.
func (e *Emitter) buildRespCollectDone(respTy Type, bodyIdx, lenIdx, flagsIdx int) {
	restore := e.beginDetachedFunc()
	envTy := "{ " + irPtr + ", " + irPtr + ", " + irPtr + ", " + irPtr + " }"
	words := make([]string, 4)
	for i := range words {
		g, w := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %%env, i32 0, i32 %d", g, envTy, i))
		e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", w, g))
		words[i] = w
	}
	resp, clo, q, p := words[0], words[1], words[2], words[3]
	stP, st, ok := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 0", stP, promiseStructIR, p))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", st, stP))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", ok, st))
	fillL, rejL := e.freshLabel("fill"), e.freshLabel("rej")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", ok, fillL, rejL))
	e.emitLabel(fillL)
	hP, hBits, hdr, data, nP, n, n1, buf, end := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 2", hP, promiseStructIR, p))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", hBits, hP))
	e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to "+irPtr, hdr, hBits))
	e.emitInstr(fmt.Sprintf("%s = load "+irPtr+", "+irPtr+" %s, align 8", data, hdr))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 8", nP, hdr))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", n, nP))
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", n1, n))
	e.heapMallocInto(buf, allocBytes, n1)
	e.emitInstr(fmt.Sprintf("call "+irPtr+" @memcpy("+irPtr+" %s, "+irPtr+" %s, i64 %s)", buf, data, n))
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, "+irPtr+" %s, i64 %s", end, buf, n))
	e.heapStore("i8", "0", end, 1)
	bP, lP, fP, f, f2 := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", bP, respTy.StructIR(), resp, bodyIdx))
	e.heapStore(irPtr, buf, bP, 8)
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", lP, respTy.StructIR(), resp, lenIdx))
	e.heapStore("i64", n, lP, 8)
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, "+irPtr+" %s, i32 0, i32 %d", fP, respTy.StructIR(), resp, flagsIdx))
	e.emitInstr(fmt.Sprintf("%s = load i64, "+irPtr+" %s, align 8", f, fP))
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", f2, f, ^int64(responseStreamBody|responseStreamAny)))
	e.heapStore("i64", f2, fP, 8)
	e.emitInstr(fmt.Sprintf("call void @__kml_fbp_invoke("+irPtr+" %s)", clo))
	e.emitTerminator("ret void")
	e.emitLabel(rejL)
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_reject_from("+irPtr+" %s, "+irPtr+" %s)", q, p))
	e.emitTerminator("ret void")
	body := e.allocas.String() + e.body.String()
	restore()
	e.writeFunc("void @__kml_resp_collect_done("+irPtr+" %env)", body)
}

// emitResponseCollectFinalize defines @__kml_resp_collect as a no-op when a
// body method is called but no Response ever has a stream body.
func (e *Emitter) emitResponseCollectFinalize() {
	if e.respCollectCalled && !e.respCollectDefined {
		e.writeFunc("internal void @__kml_resp_collect("+irPtr+" %resp, "+irPtr+" %clo, "+irPtr+" %q)", "  ret void\n")
	}
}

// emitStreamCollectPromise reads the stream in streamRef (chunks of chunkTy)
// to its end and returns the Promise<Uint8Array> of its bytes; take binds
// the chunk `c` from the read result `r`.
func (e *Emitter) emitStreamCollectPromise(streamRef string, chunkTy Type, take string) (string, error) {
	e.pushScope()
	defer e.popScope()
	const src = "__kml_rcol_stream"
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca "+irPtr+", align 8", slot))
	e.emitInstr(fmt.Sprintf("store "+irPtr+" %s, "+irPtr+" %s, align 8", streamRef, slot))
	e.define(src, Symbol{Ptr: slot, Ty: ReadableStreamType(chunkTy)})
	chunkTS := "Uint8Array"
	if chunkTy.IsDynamic() {
		chunkTS = "any"
	}
	prog, err := parser.Parse(`(async (s: ReadableStream<` + chunkTS + `>): Promise<Uint8Array> => {
  const rd = s.getReader();
  const parts: Uint8Array[] = [];
  let n = 0;
  while (true) {
    const r = await rd.read();
    if (r.done) break;
    ` + take + `
    parts.push(c);
    n += c.length;
  }
  const out = new Uint8Array(n);
  let o = 0;
  for (const p of parts) {
    out.set(p, o);
    o += p.length;
  }
  return out;
})(` + src + `);`)
	if err == nil && len(prog.Body) == 1 {
		err = sema.Prepare(prog)
	}
	if err != nil {
		return "", fmt.Errorf("reading a Response's stream body: %v", err)
	}
	// Synthesized code has no checker declarations: `out.set` names its
	// intrinsic itself.
	ast.Inspect(prog, func(n ast.Node) bool {
		if ce, ok := n.(*ast.CallExpression); ok {
			if mem, ok := ce.Callee.(*ast.MemberExpression); ok && mem.Property == "set" {
				ce.Intrinsic = "TypedArray.prototype.set"
			}
		}
		return true
	})
	pv, err := e.emitExpr(prog.Body[0].(*ast.ExpressionStatement).Expr)
	if err != nil {
		return "", fmt.Errorf("reading a Response's stream body: %v", err)
	}
	return pv.Ref, nil
}

// responseFactoryArity fits Response.json's or Response.redirect's
// arguments to (one, two): extras are evaluated and dropped, none throws
// the platform's TypeError (short is then true).
func (e *Emitter) responseFactoryArity(args []ast.Expression, name string, pos ast.Pos) ([]ast.Expression, bool, error) {
	if len(e.passedArgs(args)) > 0 {
		out, err := e.dropArgsPast(args, 2, pos)
		return out, false, err
	}
	e.emitThrowTypeError("Response." + name + ": 1 argument required, but 0 found.")
	e.emitLabel(e.freshLabel("resp.arity.dead"))
	return nil, true, nil
}

// emitThrowIfPrimitiveInit throws WebIDL's dictionary-conversion TypeError
// when the boxed init box is a primitive (null and undefined already taken
// as no init by the caller): "undefined: Expected 7 to be one of: Null,
// Undefined, Object."
func (e *Emitter) emitThrowIfPrimitiveInit(box Value) error {
	tag, _ := e.emitUnboxTagPayload(box)
	isPrim := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ule i8 %s, %d", isPrim, tag, kmlTagBoolean))
	throwL, okL := e.freshLabel("respinit.prim"), e.freshLabel("respinit.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isPrim, throwL, okL))
	e.emitLabel(throwL)
	str, err := e.emitValueToString(box)
	if err != nil {
		return err
	}
	msg, err := e.emitStringConcat(Value{Ref: e.internString("undefined: Expected "), Ty: TypePtr}, str)
	if err != nil {
		return err
	}
	if msg, err = e.emitStringConcat(msg, Value{Ref: e.internString(" to be one of: Null, Undefined, Object."), Ty: TypePtr}); err != nil {
		return err
	}
	e.emitThrowTypeErrorValue(msg.Ref)
	e.emitLabel(okL)
	return nil
}
