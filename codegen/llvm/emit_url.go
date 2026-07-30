package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// CURLUPart values (curl/urlapi.h). Verified directly against the header
// rather than trusted from memory, same standard every other libcurl
// constant in this codebase already documents (see runtime_fetch.go).
const (
	curluPartURL      = 0
	curluPartScheme   = 1
	curluPartHost     = 5
	curluPartPort     = 6
	curluPartPath     = 7
	curluPartQuery    = 8
	curluPartFragment = 9
)

// emitStrBranch runs `condReg` (an i1) as a branch, evaluating exactly one
// of thenFn/elseFn to produce a ptr result — the same alloca+store-in-each-
// branch+load-after-merge pattern emitConsoleCountMapEnsure/emitConditional
// already use to merge a branch-computed value back into straight-line
// code, specialized here to a plain ptr since every URL part is a string.
func (e *Emitter) emitStrBranch(condReg string, thenFn, elseFn func() (string, error)) (string, error) {
	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resPtr))

	thenL := e.freshLabel("strb.then")
	elseL := e.freshLabel("strb.else")
	mergeL := e.freshLabel("strb.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", condReg, thenL, elseL))

	e.emitLabel(thenL)
	tv, err := thenFn()
	if err != nil {
		return "", err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", tv, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(elseL)
	ev, err := elseFn()
	if err != nil {
		return "", err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ev, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", result, resPtr))
	return result, nil
}

// curlURLGetPart extracts one CURLUPart from an already-parsed CURLU
// handle. present reports whether that part actually exists in the URL
// (curl_url_get fails with a non-zero code for an absent-but-not-malformed
// part, e.g. no port/query/fragment — this is normal, not an error to
// surface to KML code) rather than whether the call "succeeded" in a
// throw-worthy sense; only curl_url_set's own result (checked once, in
// emitNewURLExpression) can produce a KML-visible Error.
func (e *Emitter) curlURLGetPart(handle string, part int) (ptrReg, present string) {
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", slot))
	code := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_get(ptr %s, i32 %d, ptr %s, i32 0)", code, handle, part, slot))
	present = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", present, code))
	ptrReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", ptrReg, slot))
	return ptrReg, present
}

// emitNewURLExpression implements `new URL(url)`: parses url via libcurl's
// URL API (ensureCurlURL) into a URLType() heap object. Throws a KML Error
// ("Invalid URL") on a malformed URL, exactly like ensureFsThrow/
// ensureHTTPThrow already surface an OS/HTTP-level failure as a catchable
// Error rather than a hard crash.
func (e *Emitter) emitNewURLExpression(ex *ast.NewURLExpression) (Value, error) {
	e.ensureCurlURL()
	e.ensureMalloc()
	e.ensureExceptionHelpers()
	e.ensureMapStrHelpers()
	e.ensureHTTPParseQuery()

	rawVal, err := e.emitExpr(ex.URL)
	if err != nil {
		return Value{}, err
	}
	rawVal = e.coerce(rawVal, TypePtr)

	handle := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @curl_url()", handle))

	setCode := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_set(ptr %s, i32 %d, ptr %s, i32 0)", setCode, handle, curluPartURL, rawVal.Ref))
	badURL := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", badURL, setCode))

	badL := e.freshLabel("url.bad")
	okL := e.freshLabel("url.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", badURL, badL, okL))

	e.emitLabel(badL)
	e.emitInstr(fmt.Sprintf("call void @curl_url_cleanup(ptr %s)", handle))
	e.emitInternalThrow(e.internString("Invalid URL"))

	e.emitLabel(okL)

	schemeRaw, _ := e.curlURLGetPart(handle, curluPartScheme) // always present after a successful set
	protocol, err := e.emitStringConcat(Value{Ref: schemeRaw, Ty: TypePtr}, Value{Ref: e.internString(":"), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}

	hostnameRaw, hostnamePresent := e.curlURLGetPart(handle, curluPartHost)
	hostname, err := e.emitStrBranch(hostnamePresent,
		func() (string, error) {
			v, err := e.emitStringConcat(Value{Ref: hostnameRaw, Ty: TypePtr}, Value{Ref: e.internString(""), Ty: TypePtr})
			if err != nil {
				return "", err
			}
			return v.Ref, nil
		},
		func() (string, error) { return e.internString(""), nil },
	)
	if err != nil {
		return Value{}, err
	}

	portRaw, portPresent := e.curlURLGetPart(handle, curluPartPort)
	port, err := e.emitStrBranch(portPresent,
		func() (string, error) {
			v, err := e.emitStringConcat(Value{Ref: portRaw, Ty: TypePtr}, Value{Ref: e.internString(""), Ty: TypePtr})
			if err != nil {
				return "", err
			}
			return v.Ref, nil
		},
		func() (string, error) { return e.internString(""), nil },
	)
	if err != nil {
		return Value{}, err
	}

	host, err := e.emitStrBranch(portPresent,
		func() (string, error) {
			withColon, err := e.emitStringConcat(Value{Ref: hostname, Ty: TypePtr}, Value{Ref: e.internString(":"), Ty: TypePtr})
			if err != nil {
				return "", err
			}
			v, err := e.emitStringConcat(withColon, Value{Ref: port, Ty: TypePtr})
			if err != nil {
				return "", err
			}
			return v.Ref, nil
		},
		func() (string, error) { return hostname, nil },
	)
	if err != nil {
		return Value{}, err
	}

	pathRaw, pathPresent := e.curlURLGetPart(handle, curluPartPath)
	pathname, err := e.emitStrBranch(pathPresent,
		func() (string, error) {
			v, err := e.emitStringConcat(Value{Ref: pathRaw, Ty: TypePtr}, Value{Ref: e.internString(""), Ty: TypePtr})
			if err != nil {
				return "", err
			}
			return v.Ref, nil
		},
		func() (string, error) { return e.internString("/"), nil },
	)
	if err != nil {
		return Value{}, err
	}

	queryRaw, queryPresent := e.curlURLGetPart(handle, curluPartQuery)
	search, err := e.emitStrBranch(queryPresent,
		func() (string, error) {
			v, err := e.emitStringConcat(Value{Ref: e.internString("?"), Ty: TypePtr}, Value{Ref: queryRaw, Ty: TypePtr})
			if err != nil {
				return "", err
			}
			return v.Ref, nil
		},
		func() (string, error) { return e.internString(""), nil },
	)
	if err != nil {
		return Value{}, err
	}

	fragRaw, fragPresent := e.curlURLGetPart(handle, curluPartFragment)
	hash, err := e.emitStrBranch(fragPresent,
		func() (string, error) {
			v, err := e.emitStringConcat(Value{Ref: e.internString("#"), Ty: TypePtr}, Value{Ref: fragRaw, Ty: TypePtr})
			if err != nil {
				return "", err
			}
			return v.Ref, nil
		},
		func() (string, error) { return e.internString(""), nil },
	)
	if err != nil {
		return Value{}, err
	}

	hrefRaw, _ := e.curlURLGetPart(handle, curluPartURL) // always present after a successful set
	href, err := e.emitStringConcat(Value{Ref: hrefRaw, Ty: TypePtr}, Value{Ref: e.internString(""), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}

	originPrefix, err := e.emitStringConcat(protocol, Value{Ref: e.internString("//"), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	origin, err := e.emitStringConcat(originPrefix, Value{Ref: host, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}

	// searchParams: an ordinary Map<string,string>, populated from the raw
	// query text via the same __kml_http_parse_query helper req.query
	// already uses (percent-decodes both key and value).
	mapPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", mapPtr))
	qParseL := e.freshLabel("url.query.parse")
	qDoneL := e.freshLabel("url.query.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", queryPresent, qParseL, qDoneL))
	e.emitLabel(qParseL)
	e.emitInstr(fmt.Sprintf("call void @__kml_http_parse_query(ptr %s, ptr %s)", queryRaw, mapPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", qDoneL))
	e.emitLabel(qDoneL)

	e.emitInstr(fmt.Sprintf("call void @curl_url_cleanup(ptr %s)", handle))

	urlTy := URLType()
	structIR := urlTy.StructIR()
	objReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", objReg, urlTy.StructSize()))
	storeField := func(name, ref string) {
		idx, fieldTy, _ := urlTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, objReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, ref, gep, fieldTy.Align()))
	}
	storeField("href", href.Ref)
	storeField("protocol", protocol.Ref)
	storeField("host", host)
	storeField("hostname", hostname)
	storeField("port", port)
	storeField("pathname", pathname)
	storeField("search", search)
	storeField("hash", hash)
	storeField("origin", origin.Ref)
	storeField("searchParams", mapPtr)

	return Value{Ref: objReg, Ty: urlTy}, nil
}

// emitStripLeadingQuestionMark returns s unchanged, or a 1-byte-advanced
// view of it, depending on whether its first byte is '?' — reading byte 0
// is always safe even for an empty string (the null terminator itself just
// compares unequal to '?'). Used so `new URLSearchParams(str)` tolerates
// being handed a URL's own `.search` value (which includes the leading
// '?') as well as a bare query string, matching real URLSearchParams.
func (e *Emitter) emitStripLeadingQuestionMark(s Value) (Value, error) {
	first := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", first, s.Ref))
	isQ := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, 63", isQ, first)) // 63 == '?'
	stripped, err := e.emitStrBranch(isQ,
		func() (string, error) {
			g := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 1", g, s.Ref))
			return g, nil
		},
		func() (string, error) { return s.Ref, nil },
	)
	if err != nil {
		return Value{}, err
	}
	return Value{Ref: stripped, Ty: TypePtr}, nil
}

// emitNewURLSearchParamsExpression implements `new URLSearchParams()`
// (empty) and `new URLSearchParams(init)` (parses init as a query string,
// tolerating a leading '?'). See URLSearchParamsType's doc comment for the
// single-value-per-key scope narrowing.
func (e *Emitter) emitNewURLSearchParamsExpression(ex *ast.NewURLSearchParamsExpression) (Value, error) {
	e.ensureMapStrHelpers()
	mapPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", mapPtr))

	if ex.Init != nil {
		val, err := e.emitExpr(ex.Init)
		if err != nil {
			return Value{}, err
		}
		val = e.coerce(val, TypePtr)
		stripped, err := e.emitStripLeadingQuestionMark(val)
		if err != nil {
			return Value{}, err
		}
		e.ensureHTTPParseQuery()
		e.emitInstr(fmt.Sprintf("call void @__kml_http_parse_query(ptr %s, ptr %s)", stripped.Ref, mapPtr))
	}

	return Value{Ref: mapPtr, Ty: URLSearchParamsType()}, nil
}

// emitURLSearchParamsToString implements urlSearchParams.toString():
// serializes back to "k1=v1&k2=v2" (percent-encoding each key/value via the
// same helper encodeURIComponent uses), in whatever order
// __kml_map_str_keys/vals iterate — insertion order, matching every other
// Map<string,string> iteration in this compiler.
func (e *Emitter) emitURLSearchParamsToString(objExpr ast.Expression, pos ast.Pos) (Value, error) {
	_, mapPtr, err := e.resolveMapOrSetForCall(objExpr, pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureMapStrHelpers()
	e.ensureEncodeURIComponent()

	keysAgg := e.freshReg()
	valsAgg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_str_keys(ptr %s)", keysAgg, mapPtr))
	e.emitInstr(fmt.Sprintf("%s = call {ptr, i64} @__kml_map_str_vals(ptr %s)", valsAgg, mapPtr))
	keysPtr := e.freshReg()
	lenReg := e.freshReg()
	valsPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", keysPtr, keysAgg))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", lenReg, keysAgg))
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", valsPtr, valsAgg))

	accAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", accAlloca))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(""), accAlloca))
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("usp.tostr.cond")
	bodyL := e.freshLabel("usp.tostr.body")
	firstL := e.freshLabel("usp.tostr.first")
	restL := e.freshLabel("usp.tostr.rest")
	incL := e.freshLabel("usp.tostr.inc")
	doneL := e.freshLabel("usp.tostr.done")

	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
	e.emitLabel(condL)
	idxVal := e.freshReg()
	done := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", done, idxVal, lenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", done, doneL, bodyL))

	e.emitLabel(bodyL)
	keySlot := e.freshReg()
	keyRaw := e.freshReg()
	valSlot := e.freshReg()
	valRaw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", keySlot, keysPtr, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", keyRaw, keySlot))
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", valSlot, valsPtr, idxVal))
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", valRaw, valSlot))

	keyEnc := e.freshReg()
	valEnc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_encode_uri_component(ptr %s)", keyEnc, keyRaw))
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_encode_uri_component(ptr %s)", valEnc, valRaw))
	withEq, err := e.emitStringConcat(Value{Ref: keyEnc, Ty: TypePtr}, Value{Ref: e.internString("="), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	pair, err := e.emitStringConcat(withEq, Value{Ref: valEnc, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}

	isFirst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isFirst, idxVal))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isFirst, firstL, restL))

	e.emitLabel(firstL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", pair.Ref, accAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(restL)
	accCur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", accCur, accAlloca))
	withAmp, err := e.emitStringConcat(Value{Ref: accCur, Ty: TypePtr}, Value{Ref: e.internString("&"), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	newAcc, err := e.emitStringConcat(withAmp, pair)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", newAcc.Ref, accAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", incL))

	e.emitLabel(incL)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", result, accAlloca))
	return Value{Ref: result, Ty: TypePtr}, nil
}

// emitURLSearchParamsGetAll implements urlSearchParams.getAll(name): since
// this compiler's URLSearchParams keeps only one value per key (see
// URLSearchParamsType's doc comment), this always returns a 0- or 1-element
// array — present iff .has(name).
func (e *Emitter) emitURLSearchParamsGetAll(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: getAll takes exactly 1 argument", pos.Line, pos.Col)
	}
	_, mapPtr, err := e.resolveMapOrSetForCall(mem.Object, pos)
	if err != nil {
		return Value{}, err
	}
	nameVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	nameVal = e.coerce(nameVal, TypePtr)

	e.ensureMapStrHelpers()
	hasReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i1 @__kml_map_str_has(ptr %s, ptr %s)", hasReg, mapPtr, nameVal.Ref))

	e.ensureMalloc()
	oneBytes := TypePtr.Align()
	outPtr, err := e.emitStrBranch(hasReg,
		func() (string, error) {
			p := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", p, oneBytes))
			valReg := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", valReg, mapPtr, nameVal.Ref))
			valPtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", valPtr, valReg))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align %d", valPtr, p, oneBytes))
			return p, nil
		},
		func() (string, error) { return "null", nil },
	)
	if err != nil {
		return Value{}, err
	}
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 1, i64 0", lenReg, hasReg))

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, outPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, lenReg))
	return Value{Ref: r1, Ty: ArrayOf(TypePtr)}, nil
}
