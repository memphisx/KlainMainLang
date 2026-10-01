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
	if len(ex.Args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: new Response takes (body?, init?)", pos.Line, pos.Col)
	}
	var bodyArg, initArg ast.Expression
	if len(ex.Args) > 0 {
		bodyArg = ex.Args[0]
	}
	if len(ex.Args) > 1 {
		initArg = ex.Args[1]
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
	e.ensureMalloc()
	e.ensureMapStrHelpers()
	e.ensureStrHeaderRuntime()
	e.ensureMemcpy()

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
				status = e.coerce(v, TypeF64)
			case "statusText":
				v, err := e.emitExpr(prop.Value)
				if err != nil {
					return Value{}, err
				}
				statusText = e.coerce(v, TypePtr)
			case "headers":
				headersExpr = prop.Value
			}
		}
	} else if initArg != nil {
		iv, err := e.emitExpr(initArg)
		if err != nil {
			return Value{}, err
		}
		if !iv.Ty.IsObject {
			return Value{}, fmt.Errorf("%d:%d: new Response's init must be an object ({ status, statusText, headers })", pos.Line, pos.Col)
		}
		tmp := "__kml_resp_init_" + e.freshReg()[1:]
		slot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", iv.Ref, slot))
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
			status = e.coerce(v, TypeF64)
		}
		if m := member("statusText"); m != nil {
			v, err := e.emitExpr(m)
			if err != nil {
				return Value{}, err
			}
			statusText = e.coerce(v, TypePtr)
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
	var headers Value
	if headersExpr != nil {
		hv, err := e.emitExpr(ast.NewNewHeadersExpression(headersExpr, pos))
		if err != nil {
			return Value{}, err
		}
		headers = hv
	} else {
		m := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", m))
		headers = Value{Ref: m, Ty: HeadersType()}
	}
	if shape.location != "" {
		lv := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", lv, shape.location))
		e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", headers.Ref, e.internString("location"), lv))
	}
	return e.emitResponseBodyAndFinish(bodyArg, status, statusText, headers, shape, pos)
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
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", rangeErr))
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
		bt := e.inferExprType(bodyArg)
		switch {
		case bt.IsReadableStream:
			// A stream body: `.body` is that stream; text()/json()/
			// arrayBuffer() read it to its end first (@__kml_resp_collect).
			sv, err := e.emitExpr(bodyArg)
			if err != nil {
				return Value{}, err
			}
			if sv.Ty.StreamChunk == nil || !sv.Ty.StreamChunk.IsTypedArray || sv.Ty.StreamChunk.ElemType == nil || sv.Ty.StreamChunk.ElemType.IR != "i8" {
				return Value{}, fmt.Errorf("%d:%d: a Response's stream body must be a ReadableStream<Uint8Array> (a stream of other chunks is not supported)", pos.Line, pos.Col)
			}
			streamRef = sv.Ref
			bodyFlags = responseStreamBody
			if err := e.ensureResponseStreamCollect(); err != nil {
				return Value{}, fmt.Errorf("%d:%d: %v", pos.Line, pos.Col, err)
			}
		case bt.IsURLSearchParams:
			sv, err := e.emitExpr(ast.NewCallExpression(ast.NewMemberExpression(bodyArg, "toString", pos), nil, pos))
			if err != nil {
				return Value{}, err
			}
			bodyRef = sv.Ref
			l := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", l, sv.Ref))
			lenRef = l
			contentType = "application/x-www-form-urlencoded;charset=UTF-8"
		default:
			if isStringTy(bt) && !bt.IsBlob && !bt.IsTypedArray && !bt.IsArrayBuffer {
				contentType = "text/plain;charset=UTF-8"
				if shape.contentType != "" {
					contentType = shape.contentType
				}
			}
			// A Blob is read as it is (its type is the content type); any
			// other body through one.
			blobExpr := bodyArg
			if !bt.IsBlob {
				blobExpr = ast.NewNewBlobExpression(ast.NewArrayLiteral([]ast.Expression{bodyArg}, pos), nil, pos)
			}
			bv, err := e.emitExpr(blobExpr)
			if err != nil {
				return Value{}, err
			}
			size, data := e.emitBlobSizeData(bv.Ref)
			if bt.IsBlob {
				tslot := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 2", tslot, blobStructIR, bv.Ref))
				tv := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", tv, tslot))
				blobType = tv
			}
			buf := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_alloc(i64 %s)", buf, size))
			e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", buf, data, size))
			end := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", end, buf, size))
			e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", end))
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
		e.ensureSprintf()
		msg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_alloc(i64 64)", msg))
		si := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", si, status.Ref))
		e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)", msg, e.internString("Response constructor: Invalid response status code %lld"), si))
		e.emitInstr(fmt.Sprintf("call void @__kml_str_finalize(ptr %s)", msg))
		e.emitThrowTypeErrorValue(msg)
		e.emitLabel(nbOkL)
	}

	// The implied Content-Type, unless init's headers set one.
	if contentType != "" || blobType != "" {
		key := e.internString("content-type")
		has := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", has, headers.Ref, key))
		setL, doneL := e.freshLabel("resp.ct.set"), e.freshLabel("resp.ct.done")
		ctRef := e.internString(contentType)
		if blobType != "" {
			// A Blob's own type, when it has one.
			l := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", l, blobType))
			empty := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", empty, l))
			skip := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", skip, has, empty))
			has = skip
			ctRef = blobType
		}
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", has, doneL, setL))
		e.emitLabel(setL)
		cv := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", cv, ctRef))
		e.emitInstr(fmt.Sprintf("call void @__kml_map_str_set(ptr %s, ptr %s, i64 %s)", headers.Ref, key, cv))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
	}

	respTy := ResponseType()
	resp := e.freshReg()
	e.emitObjAllocInto(resp, respTy)
	store := func(name, ir, ref string) {
		idx, _, _ := respTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, respTy.StructIR(), resp, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ir, ref, gep))
	}
	okLo, okHi, ok := e.freshReg(), e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = fcmp oge double %s, 200.0", okLo, status.Ref))
	e.emitInstr(fmt.Sprintf("%s = fcmp olt double %s, 300.0", okHi, status.Ref))
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", ok, okLo, okHi))
	store("status", "double", status.Ref)
	store("ok", "i1", ok)
	store("body", "ptr", bodyRef)
	store("bodyLength", "i64", lenRef)
	store("__kml_headers", "ptr", headers.Ref)
	store("__kml_status_text", "ptr", statusText.Ref)
	typeName := shape.typeName
	if typeName == "" {
		typeName = "default"
	}
	store("__kml_type", "ptr", e.internString(typeName))
	store("__kml_stream", "ptr", streamRef)
	store("__kml_bodyflags", "i64", fmt.Sprint(bodyFlags))
	return Value{Ref: resp, Ty: respTy}, nil
}

// emitResponseStatic is Response.json(data, init?), Response.redirect(url,
// status?) and Response.error().
func (e *Emitter) emitResponseStatic(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	arg := func(i int) ast.Expression {
		if i < len(args) {
			return args[i]
		}
		return nil
	}
	switch method {
	case "json":
		if len(args) == 0 || len(args) > 2 {
			return Value{}, fmt.Errorf("%d:%d: Response.json takes (data, init?)", pos.Line, pos.Col)
		}
		body := ast.NewCallExpression(ast.NewMemberExpression(ast.NewIdentifier("JSON", pos), "stringify", pos), []ast.Expression{args[0]}, pos)
		return e.emitResponseCore(body, arg(1), responseShape{contentType: "application/json"}, pos)
	case "redirect":
		if len(args) == 0 || len(args) > 2 {
			return Value{}, fmt.Errorf("%d:%d: Response.redirect takes (url, status?)", pos.Line, pos.Col)
		}
		// The Location is the parsed URL, serialized.
		hrefExpr := ast.Expression(ast.NewMemberExpression(ast.NewNewExpression("URL", []ast.Expression{args[0]}, pos), "href", pos))
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
			status = e.coerce(sv, TypeF64)
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
		e.ensureSprintf()
		e.ensureStrHeaderRuntime()
		msg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_alloc(i64 48)", msg))
		si := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = fptosi double %s to i64", si, status.Ref))
		e.emitInstr(fmt.Sprintf("call i32 (ptr, ptr, ...) @sprintf(ptr %s, ptr %s, i64 %s)", msg, e.internString("Invalid status code %lld"), si))
		e.emitInstr(fmt.Sprintf("call void @__kml_str_finalize(ptr %s)", msg))
		e.ensureExceptionHelpers()
		rangeErr := e.buildErrorObj(errorKindIDs["RangeError"], msg, e.internString("RangeError"))
		e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", rangeErr))
		e.emitTerminator("unreachable")
		e.emitLabel(goodL)
		return e.emitResponseCore(nil, nil, responseShape{status: status.Ref, location: href.Ref}, pos)
	case "error":
		if len(args) != 0 {
			return Value{}, fmt.Errorf("%d:%d: Response.error takes no arguments", pos.Line, pos.Col)
		}
		return e.emitResponseCore(nil, nil, responseShape{status: "0.0", typeName: "error"}, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: Response has no static '%s'", pos.Line, pos.Col, method)
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
	e.ensureFetchBodyProm()
	e.ensurePromiseAdopt()
	e.ensurePromiseSettle()
	e.ensureMalloc()
	e.ensureMemcpy()
	respTy := ResponseType()
	field := func(name string) int {
		idx, _, _ := respTy.FieldIndex(name)
		return idx
	}
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_resp_collect_done(ptr %%env) {
entry:
  %%resp_p = getelementptr { ptr, ptr, ptr, ptr }, ptr %%env, i32 0, i32 0
  %%resp = load ptr, ptr %%resp_p, align 8
  %%clo_p = getelementptr { ptr, ptr, ptr, ptr }, ptr %%env, i32 0, i32 1
  %%clo = load ptr, ptr %%clo_p, align 8
  %%q_p = getelementptr { ptr, ptr, ptr, ptr }, ptr %%env, i32 0, i32 2
  %%q = load ptr, ptr %%q_p, align 8
  %%p_p = getelementptr { ptr, ptr, ptr, ptr }, ptr %%env, i32 0, i32 3
  %%p = load ptr, ptr %%p_p, align 8
  %%st_p = getelementptr %[1]s, ptr %%p, i32 0, i32 0
  %%st = load i64, ptr %%st_p, align 8
  %%ok = icmp eq i64 %%st, 1
  br i1 %%ok, label %%fill, label %%rej
fill:
  ; The Blob: { i64 size, ptr data, ptr type }.
  %%ab_p = getelementptr %[1]s, ptr %%p, i32 0, i32 2
  %%ab_bits = load i64, ptr %%ab_p, align 8
  %%ab = inttoptr i64 %%ab_bits to ptr
  %%len = load i64, ptr %%ab, align 8
  %%data_p = getelementptr { i64, ptr }, ptr %%ab, i32 0, i32 1
  %%data = load ptr, ptr %%data_p, align 8
  %%n1 = add i64 %%len, 1
  %%buf = call ptr @malloc(i64 %%n1)
  %%ign = call ptr @memcpy(ptr %%buf, ptr %%data, i64 %%len)
  %%end = getelementptr i8, ptr %%buf, i64 %%len
  store i8 0, ptr %%end, align 1
  %%b_p = getelementptr %[2]s, ptr %%resp, i32 0, i32 %[3]d
  store ptr %%buf, ptr %%b_p, align 8
  %%l_p = getelementptr %[2]s, ptr %%resp, i32 0, i32 %[4]d
  store i64 %%len, ptr %%l_p, align 8
  %%f_p = getelementptr %[2]s, ptr %%resp, i32 0, i32 %[5]d
  %%f = load i64, ptr %%f_p, align 8
  %%f2 = and i64 %%f, %[6]d
  store i64 %%f2, ptr %%f_p, align 8
  call void @__kml_fbp_invoke(ptr %%clo)
  ret void
rej:
  %%v0_p = getelementptr %[1]s, ptr %%p, i32 0, i32 2
  %%v0 = load i64, ptr %%v0_p, align 8
  %%v1_p = getelementptr %[1]s, ptr %%p, i32 0, i32 3
  %%v1 = load i64, ptr %%v1_p, align 8
  %%q0_p = getelementptr %[1]s, ptr %%q, i32 0, i32 2
  store i64 %%v0, ptr %%q0_p, align 8
  %%q1_p = getelementptr %[1]s, ptr %%q, i32 0, i32 3
  store i64 %%v1, ptr %%q1_p, align 8
  call void @__kml_promise_settle(ptr %%q, i64 2)
  ret void
}`, promiseStructIR, respTy.StructIR(), field("body"), field("bodyLength"), field("__kml_bodyflags"), ^int64(responseStreamBody)))

	restore := e.beginDetachedFunc()
	defer restore()
	e.pushScope()
	defer e.popScope()
	resp := Value{Ref: "%resp", Ty: respTy}
	sv := e.loadFieldValue(resp, field("__kml_stream"), TypePtr)
	const src = "__kml_rcol_stream"
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", sv.Ref, slot))
	e.define(src, Symbol{Ptr: slot, Ty: ReadableStreamType(TypedArrayType("uint8"))})
	prog, err := parser.Parse(`(async (s: ReadableStream<Uint8Array>): Promise<Blob> => {
  const rd = s.getReader();
  const parts: Uint8Array[] = [];
  let n = 0;
  while (true) {
    const r = await rd.read();
    if (r.done) break;
    parts.push(r.value);
    n += r.value.length;
  }
  const out = new Uint8Array(n);
  let o = 0;
  for (const p of parts) {
    out.set(p, o);
    o += p.length;
  }
  return new Blob([out]);
})(` + src + `);`)
	if err == nil && len(prog.Body) == 1 {
		err = sema.Prepare(prog)
	}
	if err != nil {
		return fmt.Errorf("reading a Response's stream body: %v", err)
	}
	pv, err := e.emitExpr(prog.Body[0].(*ast.ExpressionStatement).Expr)
	if err != nil {
		return fmt.Errorf("reading a Response's stream body: %v", err)
	}
	env := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 32)", env))
	for i, ref := range []string{"%resp", "%clo", "%q", pv.Ref} {
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr, ptr, ptr }, ptr %s, i32 0, i32 %d", g, env, i))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ref, g))
	}
	clo := e.buildBuiltinClosure("@__kml_resp_collect_done", env)
	e.emitInstr(fmt.Sprintf("call void @__kml_promise_attach(ptr %s, ptr %s)", pv.Ref, clo))
	e.emitTerminator("ret void")
	body := e.allocas.String() + e.body.String()
	e.functions.WriteString(fmt.Sprintf("\ndefine internal void @__kml_resp_collect(ptr %%resp, ptr %%clo, ptr %%q) {\nentry:\n%s}\n", body))
	return nil
}

// emitResponseCollectFinalize defines @__kml_resp_collect as a no-op when a
// body method is called but no Response ever has a stream body.
func (e *Emitter) emitResponseCollectFinalize() {
	if e.respCollectCalled && !e.respCollectDefined {
		e.emitGlobal("define internal void @__kml_resp_collect(ptr %resp, ptr %clo, ptr %q) {\nentry:\n  ret void\n}")
	}
}
