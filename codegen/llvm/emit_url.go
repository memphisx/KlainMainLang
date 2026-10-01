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
	curluPartUser     = 2
	curluPartPass     = 3
	curluPartHost     = 5
	curluPartPort     = 6
	curluPartPath     = 7
	curluPartQuery    = 8
	curluPartFragment = 9
)

// curluNonSupportScheme (CURLU_NON_SUPPORT_SCHEME) lets curl parse a scheme
// it has no protocol handler for: the WHATWG URL parser takes any scheme
// (`ws:`, `wss:`, `mailto:`, …).
const curluNonSupportScheme = 1 << 3

// curluAllowSpace (CURLU_ALLOW_SPACE, curl ≥ 7.78) accepts a space in a URL
// and percent-encodes it, as the WHATWG parser does (`/a b` → `/a%20b`).
const curluAllowSpace = 1 << 11

// curluParseFlags are the flags a URL string is parsed with.
const curluParseFlags = curluNonSupportScheme | curluAllowSpace

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
	return e.curlURLGetPartFlag(handle, part, 0)
}

// curluNoDefaultPort is CURLU_NO_DEFAULT_PORT (curl/urlapi.h, verified =2 on the
// build): omit a port equal to the scheme's default when reading the port or the
// full URL — the WHATWG normalization curl doesn't do by default (TDD-00203).
const curluNoDefaultPort = 1 << 1

// curlURLGetPartFlag is curlURLGetPart with an explicit curl_url_get flag.
func (e *Emitter) curlURLGetPartFlag(handle string, part, flag int) (ptrReg, present string) {
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", slot))
	code := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_get(ptr %s, i32 %d, ptr %s, i32 %d)", code, handle, part, slot, flag))
	present = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", present, code))
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", raw, slot))
	// TDD-00120: curl returns a foreign char* with no length header — copy it into
	// a length-prefixed string (null stays null when the part is absent).
	e.ensureStrHeaderRuntime()
	ptrReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_from_cstr(ptr %s)", ptrReg, raw))
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

	// The optional base argument is evaluated (if present) before the handle is
	// created so its side effects order before the relative resolution.
	var baseVal Value
	if ex.Base != nil {
		baseVal, err = e.emitExpr(ex.Base)
		if err != nil {
			return Value{}, err
		}
		baseVal = e.coerce(baseVal, TypePtr)
	}

	handle := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @curl_url()", handle))

	// setURLPartOrThrow sets CURLUPART_URL to `ref` (curl resolves a relative
	// value against whatever the handle already holds) and throws a catchable
	// "Invalid URL" Error on failure, matching Node — which throws for both an
	// invalid base and an invalid relative URL.
	setURLPartOrThrow := func(ref string) {
		// Read as the WHATWG parser does: spaces, non-ASCII bytes and the
		// component encode sets percent-encoded, a non-ASCII host punycoded,
		// before curl parses it.
		ref, _ = e.emitURLOpaqueCheck(ref)
		setCode := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_set(ptr %s, i32 %d, ptr %s, i32 %d)", setCode, handle, curluPartURL, ref, curluParseFlags))
		bad := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", bad, setCode))
		badL := e.freshLabel("url.bad")
		okL := e.freshLabel("url.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
		e.emitLabel(badL)
		e.emitInstr(fmt.Sprintf("call void @curl_url_cleanup(ptr %s)", handle))
		e.emitThrowCoded("TypeError", "ERR_INVALID_URL", e.internString("Invalid URL"))
		e.emitLabel(okL)
	}

	// With a base: seed the handle with the (absolute) base first, then apply the
	// possibly-relative URL, which curl resolves against the base. An absolute
	// URL value simply overwrites the base, matching the WHATWG algorithm.
	// An opaque URL (`mailto:a@b`) is split without the URL parser, which
	// reads hierarchical URLs only.
	urlTy := URLType()
	resSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resSlot))
	enc, isOpaque := e.emitURLOpaqueCheck(rawVal.Ref)
	opaqueL, parseL, doneL := e.freshLabel("url.opaque"), e.freshLabel("url.parse"), e.freshLabel("url.done")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isOpaque, opaqueL, parseL))
	e.emitLabel(opaqueL)
	e.emitInstr(fmt.Sprintf("call void @curl_url_cleanup(ptr %s)", handle))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.emitOpaqueURLObject(enc), resSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(parseL)
	if ex.Base != nil {
		setURLPartOrThrow(baseVal.Ref)
	}
	setURLPartOrThrow(rawVal.Ref)

	objReg := e.freshReg()
	e.emitObjMallocInto(objReg, urlTy)
	if err := e.deriveURLFieldsIntoObject(handle, objReg); err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", objReg, resSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(doneL)
	res := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", res, resSlot))
	return Value{Ref: res, Ty: urlTy}, nil
}

// emitURLStaticParse parses input (with an optional base) into a curl handle
// WITHOUT throwing — the shared core of the WHATWG statics URL.canParse/URL.parse
// (TDD-00203). Returns (handle, okReg) where okReg is an i1: true iff every
// curl_url_set succeeded. On failure the handle is left cleaned up.
func (e *Emitter) emitURLStaticParse(args []ast.Expression, pos ast.Pos) (handle, okReg, enc, isOpaque string, err error) {
	e.ensureCurlURL()
	e.ensureMalloc()
	e.ensureMapStrHelpers()
	e.ensureHTTPParseQuery()
	if len(args) < 1 {
		return "", "", "", "", fmt.Errorf("%d:%d: URL static takes at least 1 argument", pos.Line, pos.Col)
	}
	rawVal, err := e.emitExpr(args[0])
	if err != nil {
		return "", "", "", "", err
	}
	rawVal = e.coerce(rawVal, TypePtr)
	var baseVal Value
	if len(args) >= 2 {
		baseVal, err = e.emitExpr(args[1])
		if err != nil {
			return "", "", "", "", err
		}
		baseVal = e.coerce(baseVal, TypePtr)
	}
	enc, isOpaque = e.emitURLOpaqueCheck(rawVal.Ref)
	handle = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @curl_url()", handle))
	okAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", okAlloca))
	e.emitInstr(fmt.Sprintf("store i1 true, ptr %s, align 1", okAlloca))
	setPart := func(ref string) {
		ref, _ = e.emitURLOpaqueCheck(ref) // read as the WHATWG parser does
		code := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_set(ptr %s, i32 %d, ptr %s, i32 %d)", code, handle, curluPartURL, ref, curluParseFlags))
		bad := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", bad, code))
		badL := e.freshLabel("url.static.bad")
		contL := e.freshLabel("url.static.cont")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, contL))
		e.emitLabel(badL)
		e.emitInstr(fmt.Sprintf("store i1 false, ptr %s, align 1", okAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", contL))
		e.emitLabel(contL)
	}
	if len(args) >= 2 {
		setPart(baseVal.Ref)
	}
	setPart(rawVal.Ref)
	parsed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", parsed, okAlloca))
	// An opaque URL is valid though the URL parser rejects it.
	okReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", okReg, parsed, isOpaque))
	return handle, okReg, enc, isOpaque, nil
}

// emitURLStaticCall dispatches the WHATWG static methods URL.canParse(input[,
// base]) → boolean and URL.parse(input[, base]) → URL | null. Both are
// non-throwing (unlike `new URL(...)`), matching the spec (TDD-00203).
func (e *Emitter) emitURLStaticCall(property string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch property {
	case "canParse":
		handle, okReg, _, _, err := e.emitURLStaticParse(args, pos)
		if err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("call void @curl_url_cleanup(ptr %s)", handle))
		return Value{Ref: okReg, Ty: TypeBool}, nil
	case "parse":
		handle, okReg, enc, isOpaque, err := e.emitURLStaticParse(args, pos)
		if err != nil {
			return Value{}, err
		}
		urlTy := URLType()
		resAlloca := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resAlloca))
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", resAlloca))
		okL := e.freshLabel("url.parse.ok")
		failL := e.freshLabel("url.parse.fail")
		doneL := e.freshLabel("url.parse.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", okReg, okL, failL))
		e.emitLabel(failL)
		e.emitInstr(fmt.Sprintf("call void @curl_url_cleanup(ptr %s)", handle))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(okL)
		opaqueL, parsedL := e.freshLabel("url.parse.opaque"), e.freshLabel("url.parse.parsed")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isOpaque, opaqueL, parsedL))
		e.emitLabel(opaqueL)
		e.emitInstr(fmt.Sprintf("call void @curl_url_cleanup(ptr %s)", handle))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.emitOpaqueURLObject(enc), resAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(parsedL)
		objReg := e.freshReg()
		e.emitObjMallocInto(objReg, urlTy)
		if err := e.deriveURLFieldsIntoObject(handle, objReg); err != nil {
			return Value{}, err
		}
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", objReg, resAlloca))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		res := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", res, resAlloca))
		// A URL | null result: the object type, nullable.
		rt := urlTy
		rt.Nullable = true
		return Value{Ref: res, Ty: rt}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: URL has no static method '%s'", pos.Line, pos.Col, property)
}

// curluURLDecode / curluURLEncode are curl_url_get/set flags (curl/urlapi.h):
// decode percent-escapes on get, encode them on set.
const (
	curluURLDecode = 1 << 6 // CURLU_URLDECODE
	curluURLEncode = 1 << 7 // CURLU_URLENCODE
)

// emitFileURLToPath implements `url.fileURLToPath(url)` (TDD-00165 Stage 4,
// POSIX): converts a `file:` URL (a string or a WHATWG URL) to the decoded
// filesystem path. Throws a catchable Error on a non-`file:` scheme, matching
// Node's `ERR_INVALID_URL_SCHEME`.
func (e *Emitter) emitFileURLToPath(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: url.fileURLToPath(url[, { windows }]) requires a url string or URL argument", pos.Line, pos.Col)
	}
	e.ensureCurlURL()
	e.ensureExceptionHelpers()
	e.ensureStrHeaderRuntime()
	e.ensureStrcmp()

	objVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	// A WHATWG URL argument contributes its href string; anything else is coerced
	// to a string and parsed directly.
	var urlStr Value
	if objVal.Ty.IsObject && objVal.Ty.IsURL {
		idx, fieldTy, _ := objVal.Ty.FieldIndex("href")
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, objVal.Ty.StructIR(), objVal.Ref, idx))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", r, fieldTy.IR, gep, fieldTy.Align()))
		urlStr = Value{Ref: r, Ty: TypePtr}
	} else {
		urlStr = e.coerce(objVal, TypePtr)
	}
	return e.emitFlavorBranch(args[1:], "url.fileURLToPath", pos, func(flavor pathFlavor) (Value, error) {
		return e.emitFileURLToPathFlavor(urlStr, flavor)
	})
}

// emitFlavorBranch runs body for the path flavor `{ windows }` selects
// (ADR-01079): absent / `undefined` / a literal boolean picks it at compile
// time; a run-time boolean emits both halves under a branch and joins them.
func (e *Emitter) emitFlavorBranch(opt []ast.Expression, what string, pos ast.Pos, body func(pathFlavor) (Value, error)) (Value, error) {
	if len(opt) == 0 {
		return body(e.hostPathFlavor())
	}
	ol, ok := opt[0].(*ast.ObjectLiteral)
	if !ok {
		if nl, isNull := opt[0].(*ast.NullLiteral); isNull && nl.IsUndefined {
			return body(e.hostPathFlavor())
		}
		return Value{}, fmt.Errorf("%d:%d: %s options must be an object literal ({ windows })", pos.Line, pos.Col, what)
	}
	var winExpr ast.Expression
	for _, prop := range ol.Properties {
		if prop.Key != "windows" || prop.KeyExpr != nil {
			return Value{}, fmt.Errorf("%d:%d: %s: unknown option '%s' (only { windows } is supported)", pos.Line, pos.Col, what, prop.Key)
		}
		winExpr = prop.Value
	}
	if winExpr == nil {
		return body(e.hostPathFlavor())
	}
	switch v := winExpr.(type) {
	case *ast.BooleanLiteral:
		if v.Value {
			return body(pathWin32)
		}
		return body(pathPosix)
	case *ast.NullLiteral:
		if v.IsUndefined {
			return body(e.hostPathFlavor())
		}
	}
	// Run-time boolean: both halves, joined by a phi on the result pointer.
	wv, err := e.emitExpr(winExpr)
	if err != nil {
		return Value{}, err
	}
	cond := e.coerce(wv, TypeBool)
	winL := e.freshLabel("flavor.win")
	posL := e.freshLabel("flavor.posix")
	winEndL := e.freshLabel("flavor.winend")
	posEndL := e.freshLabel("flavor.posixend")
	joinL := e.freshLabel("flavor.join")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", cond.Ref, winL, posL))
	e.emitLabel(winL)
	wres, err := body(pathWin32)
	if err != nil {
		return Value{}, err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", winEndL))
	e.emitLabel(winEndL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(posL)
	pres, err := body(pathPosix)
	if err != nil {
		return Value{}, err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", posEndL))
	e.emitLabel(posEndL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", joinL))
	e.emitLabel(joinL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = phi ptr [ %s, %%%s ], [ %s, %%%s ]", r, wres.Ref, winEndL, pres.Ref, posEndL))
	return Value{Ref: r, Ty: wres.Ty}, nil
}

// emitFileURLToPathFlavor is emitFileURLToPath's body for one path flavor.
func (e *Emitter) emitFileURLToPathFlavor(urlStr Value, flavor pathFlavor) (Value, error) {
	// libcurl refuses `file://server/...`, so the host is split off before
	// parsing (ADR-00722): the win32 half rejoins it as a UNC prefix; the POSIX
	// half throws Node's ERR_INVALID_FILE_URL_HOST for anything but "" /
	// "localhost" (which the WHATWG parser drops for file: URLs).
	e.ensurePathWin32()
	hostSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", hostSlot))
	split := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_win32_split_file_host(ptr %s, ptr %s)", split, urlStr.Ref, hostSlot))
	urlStr = Value{Ref: split, Ty: TypePtr}
	uncHostRef := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", uncHostRef, hostSlot))
	if flavor == pathPosix {
		hasHost := e.emitStrNonEmpty(uncHostRef)
		isLocal := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @strcmp(ptr %s, ptr %s)", isLocal, uncHostRef, e.internString("localhost")))
		notLocal := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", notLocal, isLocal))
		badHost := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", badHost, hasHost, notLocal))
		bhL := e.freshLabel("f2p.badhost")
		okhL := e.freshLabel("f2p.okhost")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", badHost, bhL, okhL))
		e.emitLabel(bhL)
		// Node names the *host* platform in this message, whatever `windows` says.
		e.emitInternalThrow(e.internString("File URL host must be \"localhost\" or empty on " + e.nodePlatformName()))
		e.emitLabel(okhL)
	}

	if flavor == pathWin32 {
		// Windows (TDD-00178 / ADR-00722): Node's getPathFromURLWin32 — the
		// still-encoded pathname and the hostname go to the win32 sidecar, which
		// rejects an encoded `/` or ``, flips separators, percent-decodes, and
		// either prefixes `\host` or requires a drive letter. The pathname is
		// taken by the sidecar too, not libcurl: a Linux/macOS libcurl refuses a
		// drive letter in a file URL, and this half runs on every host (ADR-01079).
		e.ensurePathWin32()
		hostReg := uncHostRef
		rawReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_win32_file_url_pathname(ptr %s)", rawReg, urlStr.Ref))
		notFile := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", notFile, rawReg))
		nfL := e.freshLabel("f2p.notfile")
		fileL := e.freshLabel("f2p.file")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", notFile, nfL, fileL))
		e.emitLabel(nfL)
		e.emitInternalThrow(e.internString("The URL must be of scheme file"))
		e.emitLabel(fileL)
		errSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", errSlot))
		res := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_win32_from_file_url(ptr %s, ptr %s, ptr %s)", res, hostReg, rawReg, errSlot))
		code := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", code, errSlot))
		isEnc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 1", isEnc, code))
		encL := e.freshLabel("f2p.encsep")
		notEncL := e.freshLabel("f2p.notenc")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEnc, encL, notEncL))
		e.emitLabel(encL)
		e.emitInternalThrow(e.internString("File URL path must not include encoded \\ or / characters"))
		e.emitLabel(notEncL)
		isRel := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 2", isRel, code))
		relL := e.freshLabel("f2p.relative")
		doneL := e.freshLabel("f2p.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isRel, relL, doneL))
		e.emitLabel(relL)
		e.emitInternalThrow(e.internString("File URL path must be absolute"))
		e.emitLabel(doneL)
		return Value{Ref: res, Ty: TypePtr}, nil
	}

	// POSIX (getPathFromURLPosix): the still-encoded pathname (the same sidecar
	// parse as the win32 half — no libcurl, which refuses a drive letter in a
	// file URL on Linux/macOS), an encoded `/` rejected, then decodeURIComponent
	// (strict: a malformed escape is a URIError, as in Node).
	e.ensurePathWin32()
	e.ensureStrstr()
	e.ensureDecodeURIComponentStrict()
	encReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_win32_file_url_pathname(ptr %s)", encReg, urlStr.Ref))
	notFile := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", notFile, encReg))
	nfL := e.freshLabel("f2p.notfile")
	fileL := e.freshLabel("f2p.file")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", notFile, nfL, fileL))
	e.emitLabel(nfL)
	e.emitInternalThrow(e.internString("The URL must be of scheme file"))
	e.emitLabel(fileL)
	encUp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @strstr(ptr %s, ptr %s)", encUp, encReg, e.internString("%2F")))
	encLo := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @strstr(ptr %s, ptr %s)", encLo, encReg, e.internString("%2f")))
	hasUp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasUp, encUp))
	hasLo := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasLo, encLo))
	hasEnc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", hasEnc, hasUp, hasLo))
	encL := e.freshLabel("f2p.posixenc")
	decL := e.freshLabel("f2p.posixdec")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasEnc, encL, decL))
	e.emitLabel(encL)
	e.emitInternalThrow(e.internString("File URL path must not include encoded / characters"))
	e.emitLabel(decL)
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_decode_uri_component_strict(ptr %s)", r, encReg))
	return Value{Ref: r, Ty: TypePtr}, nil
}

// emitPathToFileURL implements `url.pathToFileURL(path)` (TDD-00165 Stage 4,
// POSIX): resolves the path to absolute, percent-encodes it, and returns a
// WHATWG URL object with the `file:` scheme (`file:///abs/path`).
func (e *Emitter) emitPathToFileURL(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: url.pathToFileURL(path[, { windows }]) requires a path string argument", pos.Line, pos.Col)
	}
	e.ensureCurlURL()
	e.ensureMalloc()
	e.ensureMapStrHelpers()
	e.ensureHTTPParseQuery()
	raw, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	raw = e.coerce(raw, TypePtr)
	return e.emitFlavorBranch(args[1:], "url.pathToFileURL", pos, func(flavor pathFlavor) (Value, error) {
		return e.emitPathToFileURLFlavor(raw, flavor, pos)
	})
}

// emitPathToFileURLFlavor is emitPathToFileURL's body for one path flavor.
func (e *Emitter) emitPathToFileURLFlavor(raw Value, flavor pathFlavor, pos ast.Pos) (Value, error) {
	// Resolve to an absolute, normalized path (path.resolve semantics). On
	// Windows (TDD-00178 / ADR-00722) the input is evaluated once and both the
	// raw string (a UNC path keeps its host) and the resolved path go to the
	// win32 sidecar, which hands back a `/`-separated, rooted pathname
	// (`/C:/foo/bar`) plus the URL host — Node's pathToFileURL on win32.
	hostRef := e.internString("")
	var abs Value
	if flavor == pathWin32 {
		e.ensurePathWin32()
		e.ensureProcessCwd()
		e.ensureExceptionHelpers()
		arr := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca [1 x ptr], align 8", arr))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", raw.Ref, arr))
		cwd := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_process_cwd()", cwd))
		resolved := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_win32_resolve(i64 1, ptr %s, ptr %s, i32 1)", resolved, arr, cwd))
		hostSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", hostSlot))
		pn := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_path_win32_to_file_url(ptr %s, ptr %s, ptr %s)", pn, raw.Ref, resolved, hostSlot))
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, pn))
		badL := e.freshLabel("p2f.badunc")
		okL := e.freshLabel("p2f.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, badL, okL))
		e.emitLabel(badL)
		e.emitInternalThrow(e.internString("Missing UNC resource path"))
		e.emitLabel(okL)
		h := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", h, hostSlot))
		hostRef = h
		abs = Value{Ref: pn, Ty: TypePtr}
	} else {
		var err error
		abs, err = e.emitPathResolveValues(pathPosix, []Value{raw}, pos)
		if err != nil {
			return Value{}, err
		}
		abs = e.coerce(abs, TypePtr)
	}

	handle := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @curl_url()", handle))
	// scheme=file, the host (empty → the `file://` authority; a UNC server on
	// Windows), then the path with percent-encoding (so a space/`#`/`?` in the
	// path stays part of the path).
	e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_set(ptr %s, i32 %d, ptr %s, i32 %d)", e.freshReg(), handle, curluPartScheme, e.internString("file"), curluNonSupportScheme))
	e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_set(ptr %s, i32 %d, ptr %s, i32 0)", e.freshReg(), handle, curluPartHost, hostRef))
	// The path is percent-encoded here, with Node's set, not by libcurl
	// (CURLU_URLENCODE leaves `[ ] { } ~` unescaped; Node escapes them).
	e.ensureEncodeFileURLPath()
	encPath := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_encode_file_url_path(ptr %s)", encPath, abs.Ref))
	e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_set(ptr %s, i32 %d, ptr %s, i32 0)", e.freshReg(), handle, curluPartPath, encPath))

	urlTy := URLType()
	objReg := e.freshReg()
	e.emitObjMallocInto(objReg, urlTy)
	if err := e.deriveURLFieldsIntoObject(handle, objReg); err != nil {
		return Value{}, err
	}
	// pathname and href are set from the path as encoded above, not read back
	// from libcurl: it lower-cases the hex digits of an already-escaped path
	// (Node's are upper-case) and serializes a `file:` URL without its host, so
	// a Windows UNC input would come back as `file:///share/p` where Node's is
	// `file://server/share/p`.
	field := func(name string) (gep string, ty Type) {
		idx, fieldTy, _ := urlTy.FieldIndex(name)
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, urlTy.StructIR(), objReg, idx))
		return g, fieldTy
	}
	hostGep, hostTy := field("host")
	hostVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", hostVal, hostTy.IR, hostGep, hostTy.Align()))
	a, err := e.emitStringConcat(Value{Ref: e.internString("file://"), Ty: TypePtr}, Value{Ref: hostVal, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	href, err := e.emitStringConcat(a, Value{Ref: encPath, Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	pathGep, pathTy := field("pathname")
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", pathTy.IR, encPath, pathGep, pathTy.Align()))
	hrefGep, hrefTy := field("href")
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", hrefTy.IR, href.Ref, hrefGep, hrefTy.Align()))
	return Value{Ref: objReg, Ty: urlTy}, nil
}

// curluPunycode / curluPuny2IDN are curl_url_get flags (curl/urlapi.h): return
// the host in punycode (ASCII), or convert a punycode host back to IDN (Unicode).
const (
	curluPunycode = 1 << 12 // CURLU_PUNYCODE
	curluPuny2IDN = 1 << 13 // CURLU_PUNY2IDN
)

// emitUrlDomainConvert implements `url.domainToASCII` / `url.domainToUnicode`
// (TDD-00165 Stage 4) via libcurl's IDN support: it wraps the bare domain in a
// throwaway `http://<domain>` URL and reads the host back with the given punycode
// flag. Returns "" on any failure — matching Node, which also yields "" for a
// domain it can't convert (and which is what a build lacking a libcurl IDN
// backend produces here). `flag` is curluPunycode (→ ASCII) or curluPuny2IDN
// (→ Unicode).
func (e *Emitter) emitUrlDomainConvert(args []ast.Expression, pos ast.Pos, flag int) (Value, error) {
	if len(args) < 1 {
		return Value{}, fmt.Errorf("%d:%d: url.domainTo* requires a domain string argument", pos.Line, pos.Col)
	}
	e.ensureCurlURL()
	e.ensureStrHeaderRuntime()

	domainVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	domainVal = e.coerce(domainVal, TypePtr)
	urlStr, err := e.emitStringConcat(Value{Ref: e.internString("http://"), Ty: TypePtr}, domainVal)
	if err != nil {
		return Value{}, err
	}
	// Map a non-ASCII host to punycode here, not through curl's IDN backend
	// (absent from the Mac's libcurl); the host read back is then ASCII.
	e.ensureCasemap()
	e.declareFn("__kml_url_idna", "declare ptr @__kml_url_idna(ptr noundef)")
	mapped := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_url_idna(ptr %s)", mapped, urlStr.Ref))
	urlStr = Value{Ref: mapped, Ty: TypePtr}
	toUnicode := flag == curluPuny2IDN
	flag = 0

	resPtr := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resPtr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(""), resPtr))

	handle := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @curl_url()", handle))
	setCode := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_set(ptr %s, i32 %d, ptr %s, i32 %d)", setCode, handle, curluPartURL, urlStr.Ref, curluNonSupportScheme))
	setOk := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", setOk, setCode))
	tryGetL := e.freshLabel("d2a.tryget")
	cleanupL := e.freshLabel("d2a.cleanup")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", setOk, tryGetL, cleanupL))

	e.emitLabel(tryGetL)
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", slot))
	getCode := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_get(ptr %s, i32 %d, ptr %s, i32 %d)", getCode, handle, curluPartHost, slot, flag))
	getOk := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i32 %s, 0", getOk, getCode))
	haveL := e.freshLabel("d2a.have")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", getOk, haveL, cleanupL))

	e.emitLabel(haveL)
	raw := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", raw, slot))
	// The WHATWG host parser lowercases the ASCII letters (Node:
	// `domainToASCII('Example.COM')` is `example.com`).
	e.ensureStringToLower()
	host := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_tolower(ptr %s)", host, raw))
	if toUnicode {
		e.declareFn("__kml_idna_to_unicode", "declare ptr @__kml_idna_to_unicode(ptr noundef)")
		u := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_idna_to_unicode(ptr %s)", u, host))
		host = u
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", host, resPtr))
	e.emitTerminator(fmt.Sprintf("br label %%%s", cleanupL))

	e.emitLabel(cleanupL)
	e.emitInstr(fmt.Sprintf("call void @curl_url_cleanup(ptr %s)", handle))
	result := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", result, resPtr))
	return Value{Ref: result, Ty: TypePtr}, nil
}

// emitUrlToHttpOptions implements `url.urlToHttpOptions(url)` (TDD-00165 Stage
// 4): remaps a WHATWG URL into the option-bag `http.request` accepts
// (protocol/hostname/hash/search/pathname/path/href/port/auth).
func (e *Emitter) emitUrlToHttpOptions(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 {
		return Value{}, fmt.Errorf("%d:%d: url.urlToHttpOptions(url) requires a URL argument", pos.Line, pos.Col)
	}
	e.ensureMalloc()
	objVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	if !objVal.Ty.IsObject || !objVal.Ty.IsURL {
		return Value{}, fmt.Errorf("%d:%d: url.urlToHttpOptions expects a WHATWG URL argument", pos.Line, pos.Col)
	}
	srcTy := objVal.Ty
	read := func(name string) Value {
		idx, fieldTy, _ := srcTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, srcTy.StructIR(), objVal.Ref, idx))
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", r, fieldTy.IR, gep, fieldTy.Align()))
		return Value{Ref: r, Ty: TypePtr}
	}
	protocol := read("protocol")
	hostname := read("hostname")
	hash := read("hash")
	search := read("search")
	pathname := read("pathname")
	href := read("href")
	port := read("port")
	username := read("username")
	password := read("password")

	path, err := e.emitStringConcat(pathname, search)
	if err != nil {
		return Value{}, err
	}
	auth, err := e.emitStrBranch(e.emitStrNonEmpty(username.Ref),
		func() (string, error) {
			return e.emitStrBranch(e.emitStrNonEmpty(password.Ref),
				func() (string, error) {
					colonPass, err := e.emitStringConcat(Value{Ref: e.internString(":"), Ty: TypePtr}, password)
					if err != nil {
						return "", err
					}
					v, err := e.emitStringConcat(username, colonPass)
					if err != nil {
						return "", err
					}
					return v.Ref, nil
				},
				func() (string, error) { return username.Ref, nil })
		},
		func() (string, error) { return e.internString(""), nil })
	if err != nil {
		return Value{}, err
	}

	optTy := HttpOptionsType()
	obj := e.freshReg()
	e.emitObjMallocInto(obj, optTy)
	optIR := optTy.StructIR()
	store := func(name, ref string) {
		idx, fieldTy, _ := optTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, optIR, obj, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, ref, gep, fieldTy.Align()))
	}
	store("protocol", protocol.Ref)
	store("hostname", hostname.Ref)
	store("hash", hash.Ref)
	store("search", search.Ref)
	store("pathname", pathname.Ref)
	store("path", path.Ref)
	store("href", href.Ref)
	store("port", port.Ref)
	store("auth", auth)
	return Value{Ref: obj, Ty: optTy}, nil
}

// emitStrNonEmpty returns an i1 that is true when the length-prefixed string at
// `ptr` has a non-NUL first byte (i.e. is non-empty) — used to distinguish an
// absent URL component (stored as "") from a present one.
func (e *Emitter) emitStrNonEmpty(ptr string) string {
	first := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", first, ptr))
	nonEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i8 %s, 0", nonEmpty, first))
	return nonEmpty
}

// deriveURLFieldsIntoObject reads every component out of an already-parsed
// CURLU `handle` and stores the derived URL fields (href/protocol/host/…/
// searchParams) into the URLType() object at `objReg`, then cleans up the
// handle. Shared by `new URL(...)` construction and the component setters
// (ADR-00572), which re-derive every field after mutating one part so the
// object never desyncs.
func (e *Emitter) deriveURLFieldsIntoObject(handle, objReg string) error {
	return e.deriveURLFieldsIntoObjectOpt(handle, objReg, true)
}

// deriveURLFieldsIntoObjectOpt is deriveURLFieldsIntoObject with control over the
// searchParams field: rebuildSearchParams=true (construction, component setters)
// re-parses the query into a fresh searchParams handle and links it back to this
// URL (TDD-00203 live link); false (the searchParams-mutation writeback) leaves
// the existing live handle in place so its identity survives — only the string
// fields (search/href/…) are refreshed from the mutated query.
func (e *Emitter) deriveURLFieldsIntoObjectOpt(handle, objReg string, rebuildSearchParams bool) error {
	// ws:/wss: default ports, which a libcurl without WebSocket support does
	// not know: cleared from the handle, as WHATWG does.
	e.ensureURLWSDefaultPort()
	e.emitInstr(fmt.Sprintf("call void @__kml_url_ws_default_port(ptr %s)", handle))
	// WHATWG host normalization curl doesn't do: lowercase the host and write it
	// back into the handle, so every derived field (host/hostname/href/origin)
	// reads the normalized form (TDD-00203). curl already lowercases the scheme.
	// ASCII lowercasing via __kml_tolower; guarded on the host being present.
	if hRaw, hPresent := e.curlURLGetPart(handle, curluPartHost); true {
		// Lowercase + set-back ONLY when the host is present: __kml_tolower(null)
		// would crash, and a file:// URL (no host) reaches here with a null hRaw.
		setL := e.freshLabel("url.host.lower")
		doneL := e.freshLabel("url.host.lowerdone")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hPresent, setL, doneL))
		e.emitLabel(setL)
		e.ensureStringToLower()
		lower := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_tolower(ptr %s)", lower, hRaw))
		e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_set(ptr %s, i32 %d, ptr %s, i32 0)", e.freshReg(), handle, curluPartHost, lower))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
	}

	schemeRaw, _ := e.curlURLGetPart(handle, curluPartScheme) // always present after a successful set
	protocol, err := e.emitStringConcat(Value{Ref: schemeRaw, Ty: TypePtr}, Value{Ref: e.internString(":"), Ty: TypePtr})
	if err != nil {
		return err
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
		return err
	}
	// A non-special URL's host stays percent-encoded (the parser decodes it).
	e.ensureStringC()
	e.declareFn("__kml_url_opaque_host_pct", "declare ptr @__kml_url_opaque_host_pct(ptr noundef, ptr noundef)")
	e.declareFn("__kml_url_opaque_href", "declare ptr @__kml_url_opaque_href(ptr noundef, ptr noundef)")
	pctHost := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_url_opaque_host_pct(ptr %s, ptr %s)", pctHost, protocol.Ref, hostname))
	hostname = pctHost

	// NO_DEFAULT_PORT: an explicit port equal to the scheme's default (http:80,
	// https:443, ws:80, wss:443, ftp:21) reads as absent — WHATWG strips it.
	portRaw, portPresent := e.curlURLGetPartFlag(handle, curluPartPort, curluNoDefaultPort)
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
		return err
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
		return err
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
		return err
	}

	queryRaw, queryPresent := e.curlURLGetPart(handle, curluPartQuery)
	search, err := e.emitStrBranch(queryPresent,
		func() (string, error) {
			// A present-but-empty query — a bare "?", or the result of deleting the
			// last searchParams pair — is no query at all under WHATWG: `search` is
			// "" (and href already drops it). Only a non-empty query gets the "?".
			return e.emitStrBranch(e.emitStrNonEmpty(queryRaw),
				func() (string, error) {
					v, err := e.emitStringConcat(Value{Ref: e.internString("?"), Ty: TypePtr}, Value{Ref: queryRaw, Ty: TypePtr})
					if err != nil {
						return "", err
					}
					return v.Ref, nil
				},
				func() (string, error) { return e.internString(""), nil })
		},
		func() (string, error) { return e.internString(""), nil },
	)
	if err != nil {
		return err
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
		return err
	}

	userRaw, userPresent := e.curlURLGetPart(handle, curluPartUser)
	username, err := e.emitStrBranch(userPresent,
		func() (string, error) {
			v, err := e.emitStringConcat(Value{Ref: userRaw, Ty: TypePtr}, Value{Ref: e.internString(""), Ty: TypePtr})
			if err != nil {
				return "", err
			}
			return v.Ref, nil
		},
		func() (string, error) { return e.internString(""), nil },
	)
	if err != nil {
		return err
	}

	passRaw, passPresent := e.curlURLGetPart(handle, curluPartPass)
	password, err := e.emitStrBranch(passPresent,
		func() (string, error) {
			v, err := e.emitStringConcat(Value{Ref: passRaw, Ty: TypePtr}, Value{Ref: e.internString(""), Ty: TypePtr})
			if err != nil {
				return "", err
			}
			return v.Ref, nil
		},
		func() (string, error) { return e.internString(""), nil },
	)
	if err != nil {
		return err
	}

	hrefRaw, _ := e.curlURLGetPartFlag(handle, curluPartURL, curluNoDefaultPort) // default port stripped, always present
	href, err := e.emitStringConcat(Value{Ref: hrefRaw, Ty: TypePtr}, Value{Ref: e.internString(""), Ty: TypePtr})
	if err != nil {
		return err
	}

	originPrefix, err := e.emitStringConcat(protocol, Value{Ref: e.internString("//"), Ty: TypePtr})
	if err != nil {
		return err
	}
	tuple, err := e.emitStringConcat(originPrefix, Value{Ref: host, Ty: TypePtr})
	if err != nil {
		return err
	}
	// Only http, https, ws, wss and ftp URLs have a tuple origin; any other
	// (a non-special scheme, file:) is opaque, "null".
	e.ensureStringC()
	e.declareFn("__kml_url_tuple_origin", "declare zeroext i1 @__kml_url_tuple_origin(ptr noundef)")
	hasTuple, originReg := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call zeroext i1 @__kml_url_tuple_origin(ptr %s)", hasTuple, protocol.Ref))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", originReg, hasTuple, tuple.Ref, e.internString("null")))
	origin := Value{Ref: originReg, Ty: TypePtr}

	// searchParams: the ordered pair-list (TDD-00203), parsed from the raw query
	// text preserving cross-key order and duplicate keys (percent-decoding both
	// name and value). Replaces the former Map<string,string> fill. Skipped on the
	// mutation-writeback path so the caller's live handle keeps its identity.
	var mapPtr string
	if rebuildSearchParams {
		mapPtr = e.buildURLSearchParamsFromQuery(queryRaw, queryPresent)
	}

	e.emitInstr(fmt.Sprintf("call void @curl_url_cleanup(ptr %s)", handle))

	urlTy := URLType()
	structIR := urlTy.StructIR()
	storeField := func(name, ref string) {
		idx, fieldTy, _ := urlTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, objReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", fieldTy.IR, ref, gep, fieldTy.Align()))
	}
	opHref := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_url_opaque_href(ptr %s, ptr %s)", opHref, protocol.Ref, href.Ref))
	storeField("href", opHref)
	storeField("protocol", protocol.Ref)
	storeField("host", host)
	storeField("hostname", hostname)
	storeField("port", port)
	storeField("pathname", pathname)
	storeField("search", search)
	storeField("hash", hash)
	storeField("origin", origin.Ref)
	storeField("username", username)
	storeField("password", password)
	if rebuildSearchParams {
		storeField("searchParams", mapPtr)
		// Link the fresh handle back to this URL so a later mutation on
		// `url.searchParams` writes the serialized query back into these fields.
		e.emitInstr(fmt.Sprintf("call void @__kml_usp_set_owner(ptr %s, ptr %s)", mapPtr, objReg))
	}

	return nil
}

// ensureURLUSPWriteback emits (once) @__kml_url_usp_writeback(ptr %usp): the
// searchParams→URL live-link writeback (TDD-00203). A standalone URLSearchParams
// has a null owner and the call is a cheap no-op; a URL-owned handle re-serializes
// its pairs, seeds a curl handle from the owner URL's current href, applies the
// new query, and re-derives the URL's string fields (search/href/query/path/…)
// WITHOUT rebuilding the searchParams handle — so the live handle the caller just
// mutated keeps its identity. Called after every mutating URLSearchParams op.
func (e *Emitter) ensureURLUSPWriteback() {
	if e.urlUSPWritebackEmit {
		return
	}
	e.urlUSPWritebackEmit = true
	e.ensureCurlURL()
	e.ensureURLSearchParams()
	e.emitStandaloneFunc("void @__kml_url_usp_writeback(ptr %usp)", func() string {
		owner := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_usp_owner(ptr %%usp)", owner))
		isNull := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, owner))
		doL := e.freshLabel("uspwb.do")
		retL := e.freshLabel("uspwb.ret")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, retL, doL))

		e.emitLabel(doL)
		// Serialize the mutated pairs and seed a fresh curl handle from the owner
		// URL's current href (valid by construction), then swap in the new query.
		q := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_usp_to_string(ptr %%usp)", q))
		urlTy := URLType()
		hrefIdx, hrefTy, _ := urlTy.FieldIndex("href")
		hrefGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", hrefGep, urlTy.StructIR(), owner, hrefIdx))
		curHref := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", curHref, hrefTy.IR, hrefGep, hrefTy.Align()))
		e.emitURLOpaqueSetBranch(owner, curHref, 6, q, false, retL)
		handle := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @curl_url()", handle))
		e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_set(ptr %s, i32 %d, ptr %s, i32 %d)", e.freshReg(), handle, curluPartURL, curHref, curluNonSupportScheme))
		e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_set(ptr %s, i32 %d, ptr %s, i32 0)", e.freshReg(), handle, curluPartQuery, q))
		// Re-derive string fields only; keep the live searchParams handle (cleans up
		// the curl handle itself). Error is impossible here (pure IR emission).
		_ = e.deriveURLFieldsIntoObjectOpt(handle, owner, false)
		e.emitTerminator(fmt.Sprintf("br label %%%s", retL))

		e.emitLabel(retL)
		return "ret void"
	})
}

// emitURLUSPWriteback calls the writeback helper for a just-mutated searchParams
// handle. A no-op at runtime for a standalone (non-URL-owned) handle.
func (e *Emitter) emitURLUSPWriteback(handle string) {
	e.ensureURLUSPWriteback()
	e.emitInstr(fmt.Sprintf("call void @__kml_url_usp_writeback(ptr %s)", handle))
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

// emitStripLeadingChar returns s advanced past a single leading byte equal to
// charCode, or s unchanged otherwise — the generalization of
// emitStripLeadingQuestionMark used by the URL component setters (ADR-00572) so
// `url.hash = "#x"` and `url.hash = "x"` both set the fragment to `x`.
func (e *Emitter) emitStripLeadingChar(s Value, charCode int) (Value, error) {
	first := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", first, s.Ref))
	is := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", is, first, charCode))
	stripped, err := e.emitStrBranch(is,
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

// emitStripTrailingColon returns s with a single trailing ':' removed (a fresh
// copy), or s unchanged — so `url.protocol = "https:"` and `url.protocol =
// "https"` both hand curl the bare scheme it expects (ADR-00572).
func (e *Emitter) emitStripTrailingColon(s Value) (Value, error) {
	e.ensureStrlen()
	sLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_str_len(ptr %s)", sLen, s.Ref))
	hasLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, 0", hasLen, sLen))
	lastIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, 1", lastIdx, sLen))
	// Guard the load: index 0 is always valid (empty string's NUL), and the
	// select below keeps the full length when the string is empty.
	safeLast := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", safeLast, hasLen, lastIdx))
	lastPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", lastPtr, s.Ref, safeLast))
	lastCh := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", lastCh, lastPtr))
	isColon := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, 58", isColon, lastCh)) // 58 == ':'
	trim := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", trim, isColon, hasLen))
	newLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", newLen, trim, lastIdx, sLen))
	return e.emitStringExtract(s.Ref, "0", newLen), nil
}

// emitURLComponentSet implements a URL component setter (`url.hash = …`,
// `url.protocol = …`, etc. — ADR-00572). It re-parses the URL's current href
// into a fresh CURLU handle, applies the one changed part, and re-derives every
// field back into the same URL object so the derived components never desync.
// `href` re-parses from scratch and throws on an invalid value (Node's own
// behavior); the other setters are lenient — an invalid curl_url_set leaves the
// handle (and thus the URL) unchanged, matching Node's silent-ignore posture.
func (e *Emitter) emitURLComponentSet(objVal Value, property string, rhsExpr ast.Expression, pos ast.Pos) (Value, error) {
	part, ok := map[string]int{
		"href": curluPartURL, "protocol": curluPartScheme, "hostname": curluPartHost,
		"port": curluPartPort, "pathname": curluPartPath, "search": curluPartQuery,
		"hash": curluPartFragment, "username": curluPartUser, "password": curluPartPass,
		"host": curluPartHost,
	}[property]
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: assigning to URL component '%s' is not supported (settable: href/protocol/host/hostname/port/pathname/search/hash/username/password)", pos.Line, pos.Col, property)
	}
	e.ensureCurlURL()
	e.ensureMalloc()
	e.ensureExceptionHelpers()
	e.ensureMapStrHelpers()
	e.ensureHTTPParseQuery()

	rhsVal, err := e.emitExpr(rhsExpr)
	if err != nil {
		return Value{}, err
	}
	rhsVal = e.coerce(rhsVal, TypePtr)

	// Normalize away the marker Node tolerates on each component.
	switch property {
	case "hash":
		if rhsVal, err = e.emitStripLeadingChar(rhsVal, '#'); err != nil {
			return Value{}, err
		}
	case "search":
		if rhsVal, err = e.emitStripLeadingChar(rhsVal, '?'); err != nil {
			return Value{}, err
		}
	case "protocol":
		if rhsVal, err = e.emitStripTrailingColon(rhsVal); err != nil {
			return Value{}, err
		}
	}

	setDoneL := e.freshLabel("url.set.done")
	if property == "href" {
		// A new opaque href is split without the URL parser.
		enc, isOpaque := e.emitURLOpaqueCheck(rhsVal.Ref)
		opL, hierL := e.freshLabel("url.href.opaque"), e.freshLabel("url.href.hier")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isOpaque, opL, hierL))
		e.emitLabel(opL)
		e.emitOpaqueURLFill(objVal.Ref, enc, true)
		e.emitTerminator(fmt.Sprintf("br label %%%s", setDoneL))
		e.emitLabel(hierL)
		rhsVal = Value{Ref: enc, Ty: TypePtr}
	} else {
		hrefIdx, hrefTy, _ := objVal.Ty.FieldIndex("href")
		hrefGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", hrefGep, objVal.Ty.StructIR(), objVal.Ref, hrefIdx))
		cur := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", cur, hrefTy.IR, hrefGep, hrefTy.Align()))
		opaquePart := map[string]int{"pathname": 5, "search": 6, "hash": 7}[property] // __kml_url_opaque_set's numbering
		e.emitURLOpaqueSetBranch(objVal.Ref, cur, opaquePart, rhsVal.Ref, true, setDoneL)
	}

	handle := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @curl_url()", handle))

	if property == "href" {
		// Full re-parse: set the whole URL and throw on an invalid value.
		setCode := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_set(ptr %s, i32 %d, ptr %s, i32 %d)", setCode, handle, curluPartURL, rhsVal.Ref, curluParseFlags))
		bad := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", bad, setCode))
		badL := e.freshLabel("url.set.bad")
		okL := e.freshLabel("url.set.ok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", bad, badL, okL))
		e.emitLabel(badL)
		e.emitInstr(fmt.Sprintf("call void @curl_url_cleanup(ptr %s)", handle))
		e.emitThrowCoded("TypeError", "ERR_INVALID_URL", e.internString("Invalid URL"))
		e.emitLabel(okL)
	} else {
		// Seed from the current href (valid by construction), then apply the one
		// component. A failed part-set is ignored (Node leniency): the handle
		// keeps the original URL, so re-derivation reproduces it unchanged.
		hrefIdx, hrefTy, _ := objVal.Ty.FieldIndex("href")
		hrefGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", hrefGep, objVal.Ty.StructIR(), objVal.Ref, hrefIdx))
		curHref := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", curHref, hrefTy.IR, hrefGep, hrefTy.Align()))
		e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_set(ptr %s, i32 %d, ptr %s, i32 %d)", e.freshReg(), handle, curluPartURL, curHref, curluNonSupportScheme))
		if property == "host" {
			// Node's `url.host` is the combined `hostname[:port]`. curl has no
			// combined HOST part (CURLUPART_HOST rejects an embedded port), so split
			// on the first ':' and set hostname and port separately. Without a colon
			// only hostname is set, leaving the existing port (Node's behavior).
			e.ensureStrlen()
			e.ensureStrchr()
			total := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", total, rhsVal.Ref))
			colonPtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @strchr(ptr %s, i32 58)", colonPtr, rhsVal.Ref))
			isNull := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, colonPtr))
			basePtr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", basePtr, rhsVal.Ref))
			colonInt := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", colonInt, colonPtr))
			colonIdxRaw := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", colonIdxRaw, colonInt, basePtr))
			hostLen := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", hostLen, isNull, total, colonIdxRaw))
			hostname := e.emitStringExtract(rhsVal.Ref, "0", hostLen)
			e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_set(ptr %s, i32 %d, ptr %s, i32 0)", e.freshReg(), handle, curluPartHost, hostname.Ref))
			// Set the port only when a colon was present.
			portL := e.freshLabel("url.host.port")
			afterL := e.freshLabel("url.host.after")
			e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, afterL, portL))
			e.emitLabel(portL)
			portStart := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", portStart, colonIdxRaw))
			portLen := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", portLen, total, portStart))
			portStr := e.emitStringExtract(rhsVal.Ref, portStart, portLen)
			e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_set(ptr %s, i32 %d, ptr %s, i32 0)", e.freshReg(), handle, curluPartPort, portStr.Ref))
			e.emitTerminator(fmt.Sprintf("br label %%%s", afterL))
			e.emitLabel(afterL)
		} else {
			e.emitInstr(fmt.Sprintf("%s = call i32 @curl_url_set(ptr %s, i32 %d, ptr %s, i32 %d)", e.freshReg(), handle, part, rhsVal.Ref, curluNonSupportScheme))
		}
	}

	if err := e.deriveURLFieldsIntoObject(handle, objVal.Ref); err != nil {
		return Value{}, err
	}
	e.emitTerminator(fmt.Sprintf("br label %%%s", setDoneL))
	e.emitLabel(setDoneL)
	return rhsVal, nil
}

// ensureURLWSDefaultPort emits @__kml_url_ws_default_port(handle): unset the
// port of a ws: URL on 80 or a wss: URL on 443.
func (e *Emitter) ensureURLWSDefaultPort() {
	if e.usedURLWSDefaultPort {
		return
	}
	e.usedURLWSDefaultPort = true
	e.ensureStrcmp()
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_url_ws_default_port(ptr %%h) {
entry:
  %%ss = alloca ptr, align 8
  %%ps = alloca ptr, align 8
  store ptr null, ptr %%ss, align 8
  store ptr null, ptr %%ps, align 8
  %%sc = call i32 @curl_url_get(ptr %%h, i32 %[1]d, ptr %%ss, i32 0)
  %%pc = call i32 @curl_url_get(ptr %%h, i32 %[2]d, ptr %%ps, i32 0)
  %%sok = icmp eq i32 %%sc, 0
  %%pok = icmp eq i32 %%pc, 0
  %%both = and i1 %%sok, %%pok
  br i1 %%both, label %%cmp, label %%done
cmp:
  %%s = load ptr, ptr %%ss, align 8
  %%p = load ptr, ptr %%ps, align 8
  %%isws = call i32 @strcmp(ptr %%s, ptr %[3]s)
  %%iswss = call i32 @strcmp(ptr %%s, ptr %[4]s)
  %%is80 = call i32 @strcmp(ptr %%p, ptr %[5]s)
  %%is443 = call i32 @strcmp(ptr %%p, ptr %[6]s)
  %%a = icmp eq i32 %%isws, 0
  %%b = icmp eq i32 %%is80, 0
  %%c = icmp eq i32 %%iswss, 0
  %%d = icmp eq i32 %%is443, 0
  %%ab = and i1 %%a, %%b
  %%cd = and i1 %%c, %%d
  %%strip = or i1 %%ab, %%cd
  br i1 %%strip, label %%unset, label %%done
unset:
  %%u = call i32 @curl_url_set(ptr %%h, i32 %[2]d, ptr null, i32 0)
  br label %%done
done:
  ret void
}`, curluPartScheme, curluPartPort, e.internString("ws"), e.internString("wss"), e.internString("80"), e.internString("443")))
}

// emitURLOpaqueCheck encodes a URL string as the WHATWG parser reads it and
// reports whether it is an opaque URL (`mailto:a@b`), which the URL parser
// cannot take.
func (e *Emitter) emitURLOpaqueCheck(raw string) (enc, isOpaque string) {
	e.ensureStringC()
	e.declareFn("__kml_url_whatwg_encode", "declare ptr @__kml_url_whatwg_encode(ptr noundef)")
	e.declareFn("__kml_url_is_opaque", "declare zeroext i1 @__kml_url_is_opaque(ptr noundef)")
	e.ensureCasemap()
	e.declareFn("__kml_url_idna", "declare ptr @__kml_url_idna(ptr noundef)")
	pct, isOpaque := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_url_whatwg_encode(ptr %s)", pct, raw))
	enc = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_url_idna(ptr %s)", enc, pct)) // a non-ASCII host → punycode
	e.emitInstr(fmt.Sprintf("%s = call zeroext i1 @__kml_url_is_opaque(ptr %s)", isOpaque, enc))
	return enc, isOpaque
}

// emitOpaqueURLObject builds the URL object of an opaque URL from its
// encoded string: no host, its path whole, origin "null".
func (e *Emitter) emitOpaqueURLObject(enc string) string {
	obj := e.freshReg()
	e.emitObjMallocInto(obj, URLType())
	e.emitOpaqueURLFill(obj, enc, true)
	return obj
}

// emitOpaqueURLFill stores an opaque URL's fields into obj; its searchParams
// are rebuilt, or (on a searchParams writeback) kept.
func (e *Emitter) emitOpaqueURLFill(obj, enc string, rebuildSearchParams bool) {
	e.declareFn("__kml_url_opaque_part", "declare ptr @__kml_url_opaque_part(ptr noundef, i32 noundef)")
	urlTy := URLType()
	structIR := urlTy.StructIR()
	store := func(name, v string) {
		idx, _, _ := urlTy.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, obj, idx))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", v, gep))
	}
	for _, f := range []struct {
		part int
		name string
	}{{0, "href"}, {1, "protocol"}, {2, "host"}, {3, "hostname"}, {4, "port"}, {5, "pathname"}, {6, "search"}, {7, "hash"}, {8, "origin"}, {10, "username"}, {11, "password"}} {
		v := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_url_opaque_part(ptr %s, i32 %d)", v, enc, f.part))
		store(f.name, v)
	}
	if !rebuildSearchParams {
		return
	}
	q, qPresent := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_url_opaque_part(ptr %s, i32 9)", q, enc))
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", qPresent, q))
	usp := e.buildURLSearchParamsFromQuery(q, qPresent)
	store("searchParams", usp)
	e.emitInstr(fmt.Sprintf("call void @__kml_usp_set_owner(ptr %s, ptr %s)", usp, obj))
}

// emitURLOpaqueSetBranch branches on whether obj's href is an opaque URL; on
// that side it applies setPart's new href (__kml_url_opaque_set) and jumps to
// doneL, leaving the emitter in the hierarchical side.
func (e *Emitter) emitURLOpaqueSetBranch(obj, curHref string, part int, value string, rebuildSearchParams bool, doneL string) {
	_, isOpaque := e.emitURLOpaqueCheck(curHref)
	opL, hierL := e.freshLabel("url.set.opaque"), e.freshLabel("url.set.hier")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isOpaque, opL, hierL))
	e.emitLabel(opL)
	e.declareFn("__kml_url_opaque_set", "declare ptr @__kml_url_opaque_set(ptr noundef, i32 noundef, ptr noundef)")
	nh := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_url_opaque_set(ptr %s, i32 %d, ptr %s)", nh, curHref, part, value))
	e.emitOpaqueURLFill(obj, nh, rebuildSearchParams)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
	e.emitLabel(hierL)
}
