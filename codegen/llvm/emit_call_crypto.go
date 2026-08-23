package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emit_call_crypto.go — the crypto.subtle.* call surface (TDD-00104). The
// heavy lifting is delegated to the __kml_crypto_* ABI (see
// runtime_crypto_subtle.go and cryptosrc/); this file parses the
// compile-time algorithm literals, extracts BufferSource operands, and
// wraps results in settled task promises so `await`, `.then`, `.catch` and
// `.finally` all behave.

// isCryptoSubtle reports whether expr is the (unshadowed) global
// `crypto.subtle` member expression — the receiver shape of every
// crypto.subtle.<method>(...) call.
func (e *Emitter) isCryptoSubtle(expr ast.Expression) bool {
	mem, ok := expr.(*ast.MemberExpression)
	if !ok || mem.Property != "subtle" {
		return false
	}
	id, ok := mem.Object.(*ast.Identifier)
	return ok && id.Name == "crypto" && !e.isShadowedByLocal("crypto")
}

// emitCryptoSubtleCall routes crypto.subtle.<method>(...) to its emitter.
func (e *Emitter) emitCryptoSubtleCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch method {
	case "digest":
		return e.emitSubtleDigest(args, pos)
	case "encrypt":
		return e.emitSubtleEncryptDecrypt(args, pos, true)
	case "decrypt":
		return e.emitSubtleEncryptDecrypt(args, pos, false)
	case "sign":
		return e.emitSubtleSign(args, pos)
	case "verify":
		return e.emitSubtleVerify(args, pos)
	case "generateKey":
		return e.emitSubtleGenerateKey(args, pos)
	case "importKey":
		return e.emitSubtleImportKey(args, pos)
	case "exportKey":
		return e.emitSubtleExportKey(args, pos)
	case "deriveBits":
		return e.emitSubtleDeriveBits(args, pos)
	case "deriveKey":
		return e.emitSubtleDeriveKey(args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: unknown crypto.subtle method '%s'", pos.Line, pos.Col, method)
}

// subtleAlgoName extracts the compile-time algorithm name from a subtle
// algorithm argument: either a string literal ("SHA-256") or an object
// literal with a literal name: property ({ name: "SHA-256", ... }).
func subtleAlgoName(expr ast.Expression) (string, bool) {
	switch a := expr.(type) {
	case *ast.StringLiteral:
		return a.Value, true
	case *ast.ObjectLiteral:
		for _, p := range a.Properties {
			if p.KeyExpr == nil && p.Key == "name" {
				if s, ok := p.Value.(*ast.StringLiteral); ok {
					return s.Value, true
				}
			}
		}
	}
	return "", false
}

// subtleHashID maps a Web Crypto hash algorithm name to the ABI's hash id.
func subtleHashID(name string) (int, bool) {
	switch name {
	case "SHA-1":
		return 1, true
	case "SHA-256":
		return 2, true
	case "SHA-384":
		return 3, true
	case "SHA-512":
		return 4, true
	}
	return 0, false
}

// emitCryptoBufferSource evaluates a BufferSource argument (ArrayBuffer or
// any TypedArray; a plain Uint8Array-shaped u8 array also qualifies since
// TypedArrays are storage-identical to arrays here) down to a raw
// (dataPtr, byteLen) register pair — the TextDecoder.decode extraction
// pattern, plus element-size scaling for non-byte TypedArrays.
func (e *Emitter) emitCryptoBufferSource(arg ast.Expression, pos ast.Pos, what string) (dataReg, byteLenReg string, err error) {
	argTy := e.inferExprType(arg)
	switch {
	case argTy.IsArrayBuffer:
		bufVal, err := e.emitExpr(arg)
		if err != nil {
			return "", "", err
		}
		byteLenReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", byteLenReg, bufVal.Ref))
		dataSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataSlot, bufVal.Ref))
		dataReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataReg, dataSlot))
		return dataReg, byteLenReg, nil
	case argTy.IsArray && argTy.ElemType != nil &&
		(argTy.IsTypedArray || (argTy.ElemType.IR == "i8" && !argTy.ElemType.Signed)):
		dataReg, lenReg, elemTy, err := e.resolveArrayForHOF(arg, pos)
		if err != nil {
			return "", "", err
		}
		if elemTy.Align() == 1 {
			return dataReg, lenReg, nil
		}
		byteLenReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %d", byteLenReg, lenReg, elemTy.Align()))
		return dataReg, byteLenReg, nil
	}
	return "", "", fmt.Errorf("%d:%d: %s expects a TypedArray or ArrayBuffer", pos.Line, pos.Col, what)
}

// wrapSettledTaskPromise wraps an already-computed value in a fulfilled
// task-shaped promise — the Promise.resolve machinery, so both `await` and
// `.then`/`.catch`/`.finally` behave on every crypto.subtle result.
func (e *Emitter) wrapSettledTaskPromise(val Value) Value {
	e.ensurePromiseRuntime()
	q := e.emitAllocSettledPromise()
	e.storePromiseValue(q, val)
	e.emitSetPromiseState(q, 1)
	qt := PromiseOf(val.Ty)
	qt.PromiseTask = true
	return Value{Ref: q, Ty: qt}
}

// emitSubtleDigest implements crypto.subtle.digest(algorithm, data):
// Promise<ArrayBuffer> for SHA-1/SHA-256/SHA-384/SHA-512.
func (e *Emitter) emitSubtleDigest(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: crypto.subtle.digest takes exactly 2 arguments (algorithm, data)", pos.Line, pos.Col)
	}
	name, ok := subtleAlgoName(args[0])
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: crypto.subtle.digest algorithm must be a string literal or an object literal with a literal name (e.g. \"SHA-256\")", pos.Line, pos.Col)
	}
	hashID, ok := subtleHashID(name)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: unsupported digest algorithm %q — must be one of SHA-1, SHA-256, SHA-384, SHA-512", pos.Line, pos.Col, name)
	}
	dataReg, byteLenReg, err := e.emitCryptoBufferSource(args[1], pos, "crypto.subtle.digest")
	if err != nil {
		return Value{}, err
	}

	e.ensureCryptoDigest()
	e.ensureMalloc()
	outReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 64)", outReg))
	outLenP := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", outLenP))
	rc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_digest(i64 %d, ptr %s, i64 %s, ptr %s, ptr %s)",
		rc, hashID, dataReg, byteLenReg, outReg, outLenP))
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)",
		rc, e.internString("crypto.subtle.digest failed")))
	digestLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", digestLen, outLenP))

	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", digestLen, hdr))
	dataSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataSlot, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", outReg, dataSlot))

	return e.wrapSettledTaskPromise(Value{Ref: hdr, Ty: ArrayBufferType()}), nil
}

// emitCryptoKeyProp implements a CryptoKey's .type ("secret"/"public"/
// "private", from the header's kind field) and .extractable property reads.
func (e *Emitter) emitCryptoKeyProp(objVal Value, prop string) (Value, error) {
	if prop == "extractable" {
		ext := e.emitCryptoKeyField(objVal.Ref, 3, "i64")
		b := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", b, ext))
		return Value{Ref: b, Ty: TypeBool}, nil
	}
	kind := e.emitCryptoKeyField(objVal.Ref, 4, "i64")
	isPub := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", isPub, kind))
	isPriv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 2", isPriv, kind))
	s1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", s1, isPub, e.internString("public"), e.internString("secret")))
	s2 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", s2, isPriv, e.internString("private"), s1))
	return Value{Ref: s2, Ty: TypePtr}, nil
}

// ── algorithm/parameter parsing (compile-time literals, TDD-00104) ──────────

// Alg ids (the CryptoKey header's algId field / shared with the C ABI).
const (
	cryptoAlgAesGcm = 1
	cryptoAlgAesCbc = 2
	cryptoAlgHmac   = 3
	cryptoAlgRsaOep = 4
	cryptoAlgRsaPss = 5
	cryptoAlgEcdsa  = 6
	cryptoAlgPbkdf2 = 7
	cryptoAlgHkdf   = 8
)

// subtleAlg is the compile-time view of a subtle algorithm argument.
type subtleAlg struct {
	name   string
	algID  int
	hashID int                // 0 when the algorithm carries no hash
	obj    *ast.ObjectLiteral // non-nil when given as an object literal
}

// subtleAlgField returns the object-literal property expression for key, or
// nil (also nil for the plain-string algorithm form).
func (a *subtleAlg) field(key string) ast.Expression {
	if a.obj == nil {
		return nil
	}
	for _, p := range a.obj.Properties {
		if p.KeyExpr == nil && p.Key == key {
			return p.Value
		}
	}
	return nil
}

func subtleAlgID(name string) (int, bool) {
	switch name {
	case "AES-GCM":
		return cryptoAlgAesGcm, true
	case "AES-CBC":
		return cryptoAlgAesCbc, true
	case "HMAC":
		return cryptoAlgHmac, true
	case "RSA-OAEP":
		return cryptoAlgRsaOep, true
	case "RSA-PSS":
		return cryptoAlgRsaPss, true
	case "ECDSA":
		return cryptoAlgEcdsa, true
	case "PBKDF2":
		return cryptoAlgPbkdf2, true
	case "HKDF":
		return cryptoAlgHkdf, true
	}
	return 0, false
}

// parseSubtleAlgorithm parses a subtle algorithm argument: a string literal
// or an object literal with a literal name: (and, when present, a literal
// hash: — itself a string literal or { name: "..." } literal).
func parseSubtleAlgorithm(expr ast.Expression, pos ast.Pos, what string) (*subtleAlg, error) {
	name, ok := subtleAlgoName(expr)
	if !ok {
		return nil, fmt.Errorf("%d:%d: %s algorithm must be a string literal or an object literal with a literal name", pos.Line, pos.Col, what)
	}
	algID, ok := subtleAlgID(name)
	if !ok {
		return nil, fmt.Errorf("%d:%d: unsupported algorithm %q", pos.Line, pos.Col, name)
	}
	a := &subtleAlg{name: name, algID: algID}
	if o, isObj := expr.(*ast.ObjectLiteral); isObj {
		a.obj = o
	}
	if hf := a.field("hash"); hf != nil {
		hname, ok := subtleAlgoName(hf)
		if !ok {
			return nil, fmt.Errorf("%d:%d: %s hash must be a string literal or an object literal with a literal name", pos.Line, pos.Col, what)
		}
		hid, ok := subtleHashID(hname)
		if !ok {
			return nil, fmt.Errorf("%d:%d: unsupported hash %q — must be one of SHA-1, SHA-256, SHA-384, SHA-512", pos.Line, pos.Col, hname)
		}
		a.hashID = hid
	}
	return a, nil
}

// subtleCurveID maps a namedCurve literal to the ABI's curve id.
func subtleCurveID(name string) (int, bool) {
	switch name {
	case "P-256":
		return 1, true
	case "P-384":
		return 2, true
	case "P-521":
		return 3, true
	}
	return 0, false
}

// parseSubtleCurve extracts a required literal namedCurve: member.
func (a *subtleAlg) parseCurve(pos ast.Pos, what string) (int, error) {
	cf := a.field("namedCurve")
	if cf == nil {
		return 0, fmt.Errorf("%d:%d: %s %s requires a literal namedCurve: member (P-256, P-384, or P-521)", pos.Line, pos.Col, what, a.name)
	}
	s, ok := cf.(*ast.StringLiteral)
	if !ok {
		return 0, fmt.Errorf("%d:%d: %s namedCurve must be a string literal", pos.Line, pos.Col, what)
	}
	id, ok := subtleCurveID(s.Value)
	if !ok {
		return 0, fmt.Errorf("%d:%d: unsupported namedCurve %q — must be one of P-256, P-384, P-521", pos.Line, pos.Col, s.Value)
	}
	return id, nil
}

// subtlePublicExponentOK accepts an absent publicExponent or the literal
// `new Uint8Array([1, 0, 1])` (65537) — the only supported exponent.
func subtlePublicExponentOK(expr ast.Expression) bool {
	if expr == nil {
		return true
	}
	ta, ok := expr.(*ast.NewTypedArrayExpression)
	if !ok || ta.ElemKind != "uint8" {
		return false
	}
	arr, ok := ta.Arg.(*ast.ArrayLiteral)
	if !ok || len(arr.Elements) != 3 {
		return false
	}
	want := []string{"1", "0", "1"}
	for i, el := range arr.Elements {
		n, ok := el.(*ast.NumberLiteral)
		if !ok || n.Value != want[i] {
			return false
		}
	}
	return true
}

// parseUsagesBitmask folds a keyUsages array literal of string literals into
// the header bitmask at compile time.
func parseUsagesBitmask(expr ast.Expression, pos ast.Pos) (int, error) {
	arr, ok := expr.(*ast.ArrayLiteral)
	if !ok {
		return 0, fmt.Errorf("%d:%d: keyUsages must be an array literal of string literals (e.g. [\"sign\", \"verify\"])", pos.Line, pos.Col)
	}
	mask := 0
	for _, el := range arr.Elements {
		s, ok := el.(*ast.StringLiteral)
		if !ok {
			return 0, fmt.Errorf("%d:%d: keyUsages must be an array literal of string literals", pos.Line, pos.Col)
		}
		switch s.Value {
		case "encrypt":
			mask |= cryptoUsageEncrypt
		case "decrypt":
			mask |= cryptoUsageDecrypt
		case "sign":
			mask |= cryptoUsageSign
		case "verify":
			mask |= cryptoUsageVerify
		case "deriveKey":
			mask |= cryptoUsageDeriveKey
		case "deriveBits":
			mask |= cryptoUsageDeriveBits
		case "wrapKey":
			mask |= cryptoUsageWrapKey
		case "unwrapKey":
			mask |= cryptoUsageUnwrapKey
		default:
			return 0, fmt.Errorf("%d:%d: unknown key usage %q", pos.Line, pos.Col, s.Value)
		}
	}
	return mask, nil
}

// emitI64Operand evaluates an expression down to an i64 register/constant.
func (e *Emitter) emitI64Operand(expr ast.Expression) (string, error) {
	v, err := e.emitExpr(expr)
	if err != nil {
		return "", err
	}
	v = e.coerce(v, TypeI64)
	return v.Ref, nil
}

// emitOutSlots allocas a (ptr, i64) out-parameter pair for the malloc'ing
// ABI calls and returns the two slot registers.
func (e *Emitter) emitOutSlots() (outP, outLenP string) {
	outP = e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", outP))
	outLenP = e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", outLenP))
	return outP, outLenP
}

// emitFreshArrayBuffer builds an ArrayBuffer header over (dataReg, lenReg).
func (e *Emitter) emitFreshArrayBuffer(dataReg, lenReg string) Value {
	e.ensureMalloc()
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenReg, hdr))
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", slot, hdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataReg, slot))
	return Value{Ref: hdr, Ty: ArrayBufferType()}
}

// emitCryptoKeyOperand evaluates a CryptoKey-typed argument.
func (e *Emitter) emitCryptoKeyOperand(expr ast.Expression, pos ast.Pos, what string) (Value, error) {
	if !e.inferExprType(expr).IsCryptoKey {
		return Value{}, fmt.Errorf("%d:%d: %s expects a CryptoKey", pos.Line, pos.Col, what)
	}
	return e.emitExpr(expr)
}

// ── importKey / exportKey / generateKey ─────────────────────────────────────

// emitSubtleImportKey implements crypto.subtle.importKey(format, keyData,
// algorithm, extractable, keyUsages): Promise<CryptoKey>. Phase 2 scope:
// symmetric algorithms (AES-GCM/AES-CBC/HMAC), formats "raw" and "jwk"
// (oct — the JWK's literal/runtime `k` member, base64url-decoded in the
// backend shim).
func (e *Emitter) emitSubtleImportKey(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 5 {
		return Value{}, fmt.Errorf("%d:%d: crypto.subtle.importKey takes exactly 5 arguments (format, keyData, algorithm, extractable, keyUsages)", pos.Line, pos.Col)
	}
	format, ok := args[0].(*ast.StringLiteral)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: importKey format must be a string literal", pos.Line, pos.Col)
	}
	alg, err := parseSubtleAlgorithm(args[2], pos, "importKey")
	if err != nil {
		return Value{}, err
	}
	switch alg.algID {
	case cryptoAlgAesGcm, cryptoAlgAesCbc, cryptoAlgHmac:
		// symmetric
	case cryptoAlgPbkdf2, cryptoAlgHkdf:
		// key-derivation base material — "raw" only per spec
		if format.Value != "raw" {
			return Value{}, fmt.Errorf("%d:%d: importKey %s only supports the \"raw\" format", pos.Line, pos.Col, alg.name)
		}
	case cryptoAlgRsaOep, cryptoAlgRsaPss, cryptoAlgEcdsa:
		return e.emitSubtleImportKeyAsym(alg, format.Value, args, pos)
	default:
		return Value{}, fmt.Errorf("%d:%d: crypto.subtle.importKey for %s is not implemented yet", pos.Line, pos.Col, alg.name)
	}
	if alg.algID == cryptoAlgHmac && alg.hashID == 0 {
		return Value{}, fmt.Errorf("%d:%d: importKey HMAC algorithm requires a literal hash (e.g. { name: \"HMAC\", hash: \"SHA-256\" })", pos.Line, pos.Col)
	}
	mask, err := parseUsagesBitmask(args[4], pos)
	if err != nil {
		return Value{}, err
	}

	var dataReg, lenReg string
	switch format.Value {
	case "raw":
		srcReg, srcLenReg, err := e.emitCryptoBufferSource(args[1], pos, "crypto.subtle.importKey")
		if err != nil {
			return Value{}, err
		}
		// Copy — the CryptoKey owns its bytes; later mutations of the
		// source view must not alias the key material.
		e.ensureMalloc()
		e.ensureMemcpy()
		dataReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", dataReg, srcLenReg))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", dataReg, srcReg, srcLenReg))
		lenReg = srcLenReg
	case "jwk":
		kExpr := ast.Expression(nil)
		if obj, isObj := args[1].(*ast.ObjectLiteral); isObj {
			for _, p := range obj.Properties {
				if p.KeyExpr == nil && p.Key == "k" {
					kExpr = p.Value
				}
			}
			if kExpr == nil {
				return Value{}, fmt.Errorf("%d:%d: importKey \"jwk\" for a symmetric key requires a k: member", pos.Line, pos.Col)
			}
		}
		var kReg string
		if kExpr != nil {
			kVal, err := e.emitExpr(kExpr)
			if err != nil {
				return Value{}, err
			}
			kReg = e.coerce(kVal, TypePtr).Ref
		} else if e.inferExprType(args[1]).IsMap {
			mapVal, err := e.emitExpr(args[1])
			if err != nil {
				return Value{}, err
			}
			e.ensureMapStrHelpers()
			raw := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", raw, mapVal.Ref, e.internString("k")))
			kReg = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", kReg, raw))
		} else {
			return Value{}, fmt.Errorf("%d:%d: importKey \"jwk\" keyData must be an object literal with a k: member or a Map<string,string> (an exportKey(\"jwk\") result)", pos.Line, pos.Col)
		}
		e.ensureCryptoB64url()
		e.ensureStrlen()
		kLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", kLen, kReg))
		outP, outLenP := e.emitOutSlots()
		rc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_b64url_decode(ptr %s, i64 %s, ptr %s, ptr %s)", rc, kReg, kLen, outP, outLenP))
		e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString("importKey: invalid JWK k member")))
		dataReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataReg, outP))
		lenReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, outLenP))
	default:
		return Value{}, fmt.Errorf("%d:%d: importKey format %q is not supported for symmetric keys — use \"raw\" or \"jwk\"", pos.Line, pos.Col, format.Value)
	}

	extVal, err := e.emitExpr(args[3])
	if err != nil {
		return Value{}, err
	}
	extRef := e.coerce(extVal, TypeI64).Ref
	key := e.emitNewCryptoKey(alg.algID, alg.hashID, "0", fmt.Sprintf("%d", mask), extRef, dataReg, lenReg)
	return e.wrapSettledTaskPromise(Value{Ref: key, Ty: CryptoKeyType()}), nil
}

// subtleJwkFieldGetter builds a per-field accessor over a JWK argument:
// an object literal (fields evaluated as string expressions, absent →
// null) or a Map<string,string> (runtime lookups, missing → null).
func (e *Emitter) subtleJwkFieldGetter(arg ast.Expression, pos ast.Pos) (func(name string) (string, error), error) {
	if obj, isObj := arg.(*ast.ObjectLiteral); isObj {
		return func(name string) (string, error) {
			for _, p := range obj.Properties {
				if p.KeyExpr == nil && p.Key == name {
					v, err := e.emitExpr(p.Value)
					if err != nil {
						return "", err
					}
					return e.coerce(v, TypePtr).Ref, nil
				}
			}
			return "null", nil
		}, nil
	}
	if e.inferExprType(arg).IsMap {
		mapVal, err := e.emitExpr(arg)
		if err != nil {
			return nil, err
		}
		e.ensureMapStrHelpers()
		return func(name string) (string, error) {
			raw := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_map_str_get(ptr %s, ptr %s)", raw, mapVal.Ref, e.internString(name)))
			p := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = inttoptr i64 %s to ptr", p, raw))
			return p, nil
		}, nil
	}
	return nil, fmt.Errorf("%d:%d: importKey \"jwk\" keyData must be an object literal or a Map<string,string> (an exportKey(\"jwk\") result)", pos.Line, pos.Col)
}

// emitSubtleImportKeyAsym implements importKey for RSA-OAEP/RSA-PSS
// (formats pkcs8/spki/jwk) and ECDSA (pkcs8/spki/raw/jwk).
func (e *Emitter) emitSubtleImportKeyAsym(alg *subtleAlg, format string, args []ast.Expression, pos ast.Pos) (Value, error) {
	mask, err := parseUsagesBitmask(args[4], pos)
	if err != nil {
		return Value{}, err
	}
	var param int
	if alg.algID == cryptoAlgEcdsa {
		param, err = alg.parseCurve(pos, "importKey")
		if err != nil {
			return Value{}, err
		}
	} else {
		if alg.hashID == 0 {
			return Value{}, fmt.Errorf("%d:%d: importKey %s requires a literal hash member", pos.Line, pos.Col, alg.name)
		}
		param = alg.hashID
	}

	var dataReg, lenReg, kindRef string
	copyBuf := func(kind string) error {
		srcReg, srcLenReg, err := e.emitCryptoBufferSource(args[1], pos, "crypto.subtle.importKey")
		if err != nil {
			return err
		}
		e.ensureMalloc()
		e.ensureMemcpy()
		dataReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", dataReg, srcLenReg))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", dataReg, srcReg, srcLenReg))
		lenReg = srcLenReg
		kindRef = kind
		return nil
	}
	switch format {
	case "pkcs8":
		if err := copyBuf("2"); err != nil {
			return Value{}, err
		}
	case "spki":
		if err := copyBuf("1"); err != nil {
			return Value{}, err
		}
	case "raw":
		if alg.algID != cryptoAlgEcdsa {
			return Value{}, fmt.Errorf("%d:%d: importKey \"raw\" is only supported for ECDSA public keys — use pkcs8/spki/jwk for RSA", pos.Line, pos.Col)
		}
		srcReg, srcLenReg, err := e.emitCryptoBufferSource(args[1], pos, "crypto.subtle.importKey")
		if err != nil {
			return Value{}, err
		}
		e.ensureCryptoEcRaw()
		outP, outLenP := e.emitOutSlots()
		rc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_ec_raw_to_spki(i64 %d, ptr %s, i64 %s, ptr %s, ptr %s)",
			rc, param, srcReg, srcLenReg, outP, outLenP))
		e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString("importKey: invalid raw EC public key")))
		dataReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataReg, outP))
		lenReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, outLenP))
		kindRef = "1"
	case "jwk":
		get, err := e.subtleJwkFieldGetter(args[1], pos)
		if err != nil {
			return Value{}, err
		}
		derP, derLenP := e.emitOutSlots()
		kindP := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", kindP))
		rc := e.freshReg()
		if alg.algID == cryptoAlgEcdsa {
			var regs [3]string
			for i, f := range []string{"x", "y", "d"} {
				if regs[i], err = get(f); err != nil {
					return Value{}, err
				}
			}
			e.ensureCryptoJwkEc()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_jwk_import_ec(i64 %d, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s)",
				rc, param, regs[0], regs[1], regs[2], derP, derLenP, kindP))
		} else {
			var regs [8]string
			for i, f := range []string{"n", "e", "d", "p", "q", "dp", "dq", "qi"} {
				if regs[i], err = get(f); err != nil {
					return Value{}, err
				}
			}
			e.ensureCryptoJwkRsa()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_jwk_import_rsa(ptr %s, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s)",
				rc, regs[0], regs[1], regs[2], regs[3], regs[4], regs[5], regs[6], regs[7], derP, derLenP, kindP))
		}
		e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString("importKey: invalid JWK")))
		dataReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", dataReg, derP))
		lenReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenReg, derLenP))
		kindRef = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", kindRef, kindP))
	default:
		return Value{}, fmt.Errorf("%d:%d: importKey format %q is not supported — use raw, pkcs8, spki, or jwk", pos.Line, pos.Col, format)
	}

	extVal, err := e.emitExpr(args[3])
	if err != nil {
		return Value{}, err
	}
	extRef := e.coerce(extVal, TypeI64).Ref
	key := e.emitNewCryptoKey(alg.algID, param, kindRef, fmt.Sprintf("%d", mask), extRef, dataReg, lenReg)
	return e.wrapSettledTaskPromise(Value{Ref: key, Ty: CryptoKeyType()}), nil
}

// emitSubtleExportKey implements crypto.subtle.exportKey(format, key):
// "raw" → Promise<ArrayBuffer>; "jwk" → Promise<Map<string,string>>
// ({ kty: "oct", k: base64url } for the Phase 2 symmetric keys).
func (e *Emitter) emitSubtleExportKey(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: crypto.subtle.exportKey takes exactly 2 arguments (format, key)", pos.Line, pos.Col)
	}
	format, ok := args[0].(*ast.StringLiteral)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: exportKey format must be a string literal", pos.Line, pos.Col)
	}
	keyVal, err := e.emitCryptoKeyOperand(args[1], pos, "crypto.subtle.exportKey")
	if err != nil {
		return Value{}, err
	}
	e.ensureCryptoCheck()
	ext := e.emitCryptoKeyField(keyVal.Ref, 3, "i64")
	extOK := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", extOK, ext))
	code := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 -4", code, extOK))
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", code, e.internString("exportKey: key is not extractable")))

	dataReg := e.emitCryptoKeyField(keyVal.Ref, 5, "ptr")
	lenReg := e.emitCryptoKeyField(keyVal.Ref, 6, "i64")
	algID := e.emitCryptoKeyField(keyVal.Ref, 0, "i64")
	kind := e.emitCryptoKeyField(keyVal.Ref, 4, "i64")
	param := e.emitCryptoKeyField(keyVal.Ref, 1, "i64")

	// requireKind throws InvalidAccessError unless the key's kind == want.
	requireKind := func(want int, msg string) {
		ok := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", ok, kind, want))
		code := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 -4", code, ok))
		e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", code, e.internString(msg)))
	}
	copyToBuffer := func() Value {
		e.ensureMalloc()
		e.ensureMemcpy()
		cp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", cp, lenReg))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", cp, dataReg, lenReg))
		return e.emitFreshArrayBuffer(cp, lenReg)
	}

	switch format.Value {
	case "pkcs8":
		requireKind(2, "exportKey: pkcs8 requires a private key")
		return e.wrapSettledTaskPromise(copyToBuffer()), nil
	case "spki":
		requireKind(1, "exportKey: spki requires a public key")
		return e.wrapSettledTaskPromise(copyToBuffer()), nil
	case "raw":
		// EC public keys convert SPKI → uncompressed point; every other key
		// exports its stored bytes (symmetric raw material).
		e.ensureCryptoEcRaw()
		outP, outLenP := e.emitOutSlots()
		isEC := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isEC, algID, cryptoAlgEcdsa))
		ecL := e.freshLabel("exraw.ec")
		plainL := e.freshLabel("exraw.plain")
		doneL := e.freshLabel("exraw.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEC, ecL, plainL))
		e.emitLabel(ecL)
		requireKind(1, "exportKey: raw requires an EC public key")
		rc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_ec_spki_to_raw(i64 %s, ptr %s, i64 %s, ptr %s, ptr %s)",
			rc, param, dataReg, lenReg, outP, outLenP))
		e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString("exportKey failed")))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(plainL)
		e.ensureMalloc()
		e.ensureMemcpy()
		cp := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", cp, lenReg))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", cp, dataReg, lenReg))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", cp, outP))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenReg, outLenP))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		outReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", outReg, outP))
		outLenReg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", outLenReg, outLenP))
		return e.wrapSettledTaskPromise(e.emitFreshArrayBuffer(outReg, outLenReg)), nil
	case "jwk":
		e.ensureCryptoB64url()
		e.ensureCryptoJwkRsa()
		e.ensureCryptoJwkEc()
		e.ensureCryptoJwkMapSet()
		e.ensureMapStrHelpers()
		mSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", mSlot))
		isPriv := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 2", isPriv, kind))
		isPriv64 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", isPriv64, isPriv))
		newMapWithKty := func(kty string) string {
			m := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_map_str_create()", m))
			e.emitInstr(fmt.Sprintf("call void @__kml_jwk_map_set(ptr %s, ptr %s, ptr %s)",
				m, e.internString("kty"), e.internString(kty)))
			return m
		}

		rsaL := e.freshLabel("exjwk.rsa")
		ecChkL := e.freshLabel("exjwk.ecchk")
		ecL := e.freshLabel("exjwk.ec")
		octL := e.freshLabel("exjwk.oct")
		doneL := e.freshLabel("exjwk.done")
		isRsa1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isRsa1, algID, cryptoAlgRsaOep))
		isRsa2 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isRsa2, algID, cryptoAlgRsaPss))
		isRsa := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", isRsa, isRsa1, isRsa2))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isRsa, rsaL, ecChkL))

		e.emitLabel(rsaL)
		{
			slots := make([]string, 8)
			for i := range slots {
				slots[i] = e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slots[i]))
			}
			rc := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_jwk_export_rsa(i64 %s, ptr %s, i64 %s, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s)",
				rc, isPriv64, dataReg, lenReg, slots[0], slots[1], slots[2], slots[3], slots[4], slots[5], slots[6], slots[7]))
			e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString("exportKey failed")))
			m := newMapWithKty("RSA")
			for i, f := range []string{"n", "e", "d", "p", "q", "dp", "dq", "qi"} {
				v := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", v, slots[i]))
				e.emitInstr(fmt.Sprintf("call void @__kml_jwk_map_set(ptr %s, ptr %s, ptr %s)", m, e.internString(f), v))
			}
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", m, mSlot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		}

		e.emitLabel(ecChkL)
		isEC := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isEC, algID, cryptoAlgEcdsa))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEC, ecL, octL))

		e.emitLabel(ecL)
		{
			slots := make([]string, 3)
			for i := range slots {
				slots[i] = e.freshReg()
				e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slots[i]))
			}
			rc := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_jwk_export_ec(i64 %s, i64 %s, ptr %s, i64 %s, ptr %s, ptr %s, ptr %s)",
				rc, param, isPriv64, dataReg, lenReg, slots[0], slots[1], slots[2]))
			e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString("exportKey failed")))
			m := newMapWithKty("EC")
			isP384 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 2", isP384, param))
			isP521 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 3", isP521, param))
			c1 := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", c1, isP384, e.internString("P-384"), e.internString("P-256")))
			crv := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", crv, isP521, e.internString("P-521"), c1))
			e.emitInstr(fmt.Sprintf("call void @__kml_jwk_map_set(ptr %s, ptr %s, ptr %s)", m, e.internString("crv"), crv))
			for i, f := range []string{"x", "y", "d"} {
				v := e.freshReg()
				e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", v, slots[i]))
				e.emitInstr(fmt.Sprintf("call void @__kml_jwk_map_set(ptr %s, ptr %s, ptr %s)", m, e.internString(f), v))
			}
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", m, mSlot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		}

		e.emitLabel(octL)
		{
			outP, outLenP := e.emitOutSlots()
			rc := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_b64url_encode(ptr %s, i64 %s, ptr %s, ptr %s)", rc, dataReg, lenReg, outP, outLenP))
			e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString("exportKey failed")))
			kStr := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", kStr, outP))
			m := newMapWithKty("oct")
			e.emitInstr(fmt.Sprintf("call void @__kml_jwk_map_set(ptr %s, ptr %s, ptr %s)", m, e.internString("k"), kStr))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", m, mSlot))
			e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		}

		e.emitLabel(doneL)
		m := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", m, mSlot))
		return e.wrapSettledTaskPromise(Value{Ref: m, Ty: MapType(TypePtr, TypePtr)}), nil
	}
	return Value{}, fmt.Errorf("%d:%d: exportKey format %q is not supported — use raw, pkcs8, spki, or jwk", pos.Line, pos.Col, format.Value)
}

// emitSubtleGenerateKey implements crypto.subtle.generateKey(algorithm,
// extractable, keyUsages): Promise<CryptoKey> for the Phase 2 symmetric
// algorithms (AES-GCM/AES-CBC via length: bits; HMAC via hash: + optional
// length: bits, defaulting to the hash's block size per spec).
func (e *Emitter) emitSubtleGenerateKey(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 3 {
		return Value{}, fmt.Errorf("%d:%d: crypto.subtle.generateKey takes exactly 3 arguments (algorithm, extractable, keyUsages)", pos.Line, pos.Col)
	}
	alg, err := parseSubtleAlgorithm(args[0], pos, "generateKey")
	if err != nil {
		return Value{}, err
	}
	mask, err := parseUsagesBitmask(args[2], pos)
	if err != nil {
		return Value{}, err
	}

	var byteLenRef string
	switch alg.algID {
	case cryptoAlgAesGcm, cryptoAlgAesCbc:
		lf := alg.field("length")
		if lf == nil {
			return Value{}, fmt.Errorf("%d:%d: generateKey %s requires a length: member (128, 192, or 256)", pos.Line, pos.Col, alg.name)
		}
		bitsRef, err := e.emitI64Operand(lf)
		if err != nil {
			return Value{}, err
		}
		byteLenRef = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sdiv i64 %s, 8", byteLenRef, bitsRef))
	case cryptoAlgHmac:
		if alg.hashID == 0 {
			return Value{}, fmt.Errorf("%d:%d: generateKey HMAC requires a literal hash (e.g. { name: \"HMAC\", hash: \"SHA-256\" })", pos.Line, pos.Col)
		}
		if lf := alg.field("length"); lf != nil {
			bitsRef, err := e.emitI64Operand(lf)
			if err != nil {
				return Value{}, err
			}
			byteLenRef = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = sdiv i64 %s, 8", byteLenRef, bitsRef))
		} else {
			// Spec default: the hash's block size. SHA-1/SHA-256: 512
			// bits; SHA-384/SHA-512: 1024 bits.
			block := 64
			if alg.hashID >= 3 {
				block = 128
			}
			byteLenRef = fmt.Sprintf("%d", block)
		}
	case cryptoAlgRsaOep, cryptoAlgRsaPss, cryptoAlgEcdsa:
		return e.emitSubtleGenerateKeyPair(alg, args, mask, pos)
	default:
		return Value{}, fmt.Errorf("%d:%d: crypto.subtle.generateKey for %s is not implemented yet", pos.Line, pos.Col, alg.name)
	}

	e.ensureCryptoRandomBytes()
	e.ensureMalloc()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", dataReg, byteLenRef))
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_random_bytes(ptr %s, i64 %s)", dataReg, byteLenRef))

	extVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	extRef := e.coerce(extVal, TypeI64).Ref
	key := e.emitNewCryptoKey(alg.algID, alg.hashID, "0", fmt.Sprintf("%d", mask), extRef, dataReg, byteLenRef)
	return e.wrapSettledTaskPromise(Value{Ref: key, Ty: CryptoKeyType()}), nil
}

// emitSubtleGenerateKeyPair implements generateKey for RSA-OAEP/RSA-PSS
// (modulusLength, publicExponent 65537, hash) and ECDSA (namedCurve):
// Promise<CryptoKeyPair>. Spec usage split: the public key keeps
// encrypt/verify/wrapKey, the private key decrypt/sign/unwrapKey.
func (e *Emitter) emitSubtleGenerateKeyPair(alg *subtleAlg, args []ast.Expression, mask int, pos ast.Pos) (Value, error) {
	var param int // hash id (RSA) or curve id (EC)
	var bitsRef string
	if alg.algID == cryptoAlgEcdsa {
		curveID, err := alg.parseCurve(pos, "generateKey")
		if err != nil {
			return Value{}, err
		}
		param = curveID
		e.ensureCryptoEcdsa()
	} else {
		if alg.hashID == 0 {
			return Value{}, fmt.Errorf("%d:%d: generateKey %s requires a literal hash (e.g. { name: %q, hash: \"SHA-256\", modulusLength: 2048 })", pos.Line, pos.Col, alg.name, alg.name)
		}
		param = alg.hashID
		ml := alg.field("modulusLength")
		if ml == nil {
			return Value{}, fmt.Errorf("%d:%d: generateKey %s requires a modulusLength: member", pos.Line, pos.Col, alg.name)
		}
		if !subtlePublicExponentOK(alg.field("publicExponent")) {
			return Value{}, fmt.Errorf("%d:%d: generateKey %s: only publicExponent 65537 is supported — pass the literal new Uint8Array([1, 0, 1]) or omit the member", pos.Line, pos.Col, alg.name)
		}
		var err error
		bitsRef, err = e.emitI64Operand(ml)
		if err != nil {
			return Value{}, err
		}
		e.ensureCryptoRsa()
	}

	p8P, p8LenP := e.emitOutSlots()
	spkiP, spkiLenP := e.emitOutSlots()
	rc := e.freshReg()
	if alg.algID == cryptoAlgEcdsa {
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_gen_ec(i64 %d, ptr %s, ptr %s, ptr %s, ptr %s)",
			rc, param, p8P, p8LenP, spkiP, spkiLenP))
	} else {
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_gen_rsa(i64 %s, ptr %s, ptr %s, ptr %s, ptr %s)",
			rc, bitsRef, p8P, p8LenP, spkiP, spkiLenP))
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString("crypto.subtle.generateKey failed")))

	extVal, err := e.emitExpr(args[1])
	if err != nil {
		return Value{}, err
	}
	extRef := e.coerce(extVal, TypeI64).Ref

	load := func(slotP, ty string) string {
		r := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, ty, slotP))
		return r
	}
	pubMask := mask & (cryptoUsageEncrypt | cryptoUsageVerify | cryptoUsageWrapKey)
	privMask := mask & (cryptoUsageDecrypt | cryptoUsageSign | cryptoUsageUnwrapKey)
	pub := e.emitNewCryptoKey(alg.algID, param, "1", fmt.Sprintf("%d", pubMask), extRef, load(spkiP, "ptr"), load(spkiLenP, "i64"))
	priv := e.emitNewCryptoKey(alg.algID, param, "2", fmt.Sprintf("%d", privMask), extRef, load(p8P, "ptr"), load(p8LenP, "i64"))

	e.ensureMalloc()
	pair := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", pair))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", pub, pair))
	privSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 1", privSlot, pair))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", priv, privSlot))
	return e.wrapSettledTaskPromise(Value{Ref: pair, Ty: CryptoKeyPairType()}), nil
}

// emitCryptoKeyPairProp implements pair.publicKey / pair.privateKey.
func (e *Emitter) emitCryptoKeyPairProp(objVal Value, prop string) (Value, error) {
	idx := 0
	if prop == "privateKey" {
		idx = 1
	}
	slot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr { ptr, ptr }, ptr %s, i32 0, i32 %d", slot, objVal.Ref, idx))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", r, slot))
	return Value{Ref: r, Ty: CryptoKeyType()}, nil
}

// ── deriveBits / deriveKey (PBKDF2, HKDF) ───────────────────────────────────

// emitSubtleDeriveBytes runs the PBKDF2 ({salt, iterations, literal hash})
// or HKDF ({salt, info, literal hash}) derivation into a fresh malloc'd
// buffer of byteLenRef bytes and returns its register.
func (e *Emitter) emitSubtleDeriveBytes(alg *subtleAlg, keyReg, byteLenRef string, pos ast.Pos, what string) (string, error) {
	if alg.hashID == 0 {
		return "", fmt.Errorf("%d:%d: %s %s requires a literal hash member", pos.Line, pos.Col, what, alg.name)
	}
	saltExpr := alg.field("salt")
	if saltExpr == nil {
		return "", fmt.Errorf("%d:%d: %s %s requires a salt: member", pos.Line, pos.Col, what, alg.name)
	}
	saltReg, saltLenReg, err := e.emitCryptoBufferSource(saltExpr, pos, what)
	if err != nil {
		return "", err
	}
	kData := e.emitCryptoKeyField(keyReg, 5, "ptr")
	kLen := e.emitCryptoKeyField(keyReg, 6, "i64")
	e.ensureCryptoDerive()
	e.ensureMalloc()
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", out, byteLenRef))
	rc := e.freshReg()
	if alg.algID == cryptoAlgPbkdf2 {
		itExpr := alg.field("iterations")
		if itExpr == nil {
			return "", fmt.Errorf("%d:%d: %s PBKDF2 requires an iterations: member", pos.Line, pos.Col, what)
		}
		itRef, err := e.emitI64Operand(itExpr)
		if err != nil {
			return "", err
		}
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_pbkdf2(i64 %d, ptr %s, i64 %s, ptr %s, i64 %s, i64 %s, ptr %s, i64 %s)",
			rc, alg.hashID, kData, kLen, saltReg, saltLenReg, itRef, out, byteLenRef))
	} else {
		infoReg, infoLenReg := "null", "0"
		if infoExpr := alg.field("info"); infoExpr != nil {
			infoReg, infoLenReg, err = e.emitCryptoBufferSource(infoExpr, pos, what)
			if err != nil {
				return "", err
			}
		}
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_hkdf(i64 %d, ptr %s, i64 %s, ptr %s, i64 %s, ptr %s, i64 %s, ptr %s, i64 %s)",
			rc, alg.hashID, kData, kLen, saltReg, saltLenReg, infoReg, infoLenReg, out, byteLenRef))
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString(what+" failed")))
	return out, nil
}

// emitSubtleDeriveBits implements crypto.subtle.deriveBits(algorithm,
// baseKey, length): Promise<ArrayBuffer> for PBKDF2/HKDF.
func (e *Emitter) emitSubtleDeriveBits(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 3 {
		return Value{}, fmt.Errorf("%d:%d: crypto.subtle.deriveBits takes exactly 3 arguments (algorithm, baseKey, length)", pos.Line, pos.Col)
	}
	alg, err := parseSubtleAlgorithm(args[0], pos, "deriveBits")
	if err != nil {
		return Value{}, err
	}
	if alg.algID != cryptoAlgPbkdf2 && alg.algID != cryptoAlgHkdf {
		return Value{}, fmt.Errorf("%d:%d: crypto.subtle.deriveBits for %s is not implemented yet", pos.Line, pos.Col, alg.name)
	}
	keyVal, err := e.emitCryptoKeyOperand(args[1], pos, "crypto.subtle.deriveBits")
	if err != nil {
		return Value{}, err
	}
	e.emitCryptoUsageCheck(keyVal.Ref, cryptoUsageDeriveBits, "deriveBits")
	bitsRef, err := e.emitI64Operand(args[2])
	if err != nil {
		return Value{}, err
	}
	byteLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sdiv i64 %s, 8", byteLen, bitsRef))
	out, err := e.emitSubtleDeriveBytes(alg, keyVal.Ref, byteLen, pos, "crypto.subtle.deriveBits")
	if err != nil {
		return Value{}, err
	}
	return e.wrapSettledTaskPromise(e.emitFreshArrayBuffer(out, byteLen)), nil
}

// emitSubtleDeriveKey implements crypto.subtle.deriveKey(algorithm,
// baseKey, derivedKeyType, extractable, keyUsages): Promise<CryptoKey> —
// PBKDF2/HKDF deriving an AES-GCM/AES-CBC/HMAC key.
func (e *Emitter) emitSubtleDeriveKey(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 5 {
		return Value{}, fmt.Errorf("%d:%d: crypto.subtle.deriveKey takes exactly 5 arguments (algorithm, baseKey, derivedKeyType, extractable, keyUsages)", pos.Line, pos.Col)
	}
	alg, err := parseSubtleAlgorithm(args[0], pos, "deriveKey")
	if err != nil {
		return Value{}, err
	}
	if alg.algID != cryptoAlgPbkdf2 && alg.algID != cryptoAlgHkdf {
		return Value{}, fmt.Errorf("%d:%d: crypto.subtle.deriveKey for %s is not implemented yet", pos.Line, pos.Col, alg.name)
	}
	derived, err := parseSubtleAlgorithm(args[2], pos, "deriveKey derivedKeyType")
	if err != nil {
		return Value{}, err
	}
	mask, err := parseUsagesBitmask(args[4], pos)
	if err != nil {
		return Value{}, err
	}

	var byteLenRef string
	switch derived.algID {
	case cryptoAlgAesGcm, cryptoAlgAesCbc:
		lf := derived.field("length")
		if lf == nil {
			return Value{}, fmt.Errorf("%d:%d: deriveKey %s derivedKeyType requires a length: member", pos.Line, pos.Col, derived.name)
		}
		bitsRef, err := e.emitI64Operand(lf)
		if err != nil {
			return Value{}, err
		}
		byteLenRef = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sdiv i64 %s, 8", byteLenRef, bitsRef))
	case cryptoAlgHmac:
		if derived.hashID == 0 {
			return Value{}, fmt.Errorf("%d:%d: deriveKey HMAC derivedKeyType requires a literal hash member", pos.Line, pos.Col)
		}
		if lf := derived.field("length"); lf != nil {
			bitsRef, err := e.emitI64Operand(lf)
			if err != nil {
				return Value{}, err
			}
			byteLenRef = e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = sdiv i64 %s, 8", byteLenRef, bitsRef))
		} else {
			block := 64
			if derived.hashID >= 3 {
				block = 128
			}
			byteLenRef = fmt.Sprintf("%d", block)
		}
	default:
		return Value{}, fmt.Errorf("%d:%d: deriveKey can only derive AES-GCM, AES-CBC, or HMAC keys (got %s)", pos.Line, pos.Col, derived.name)
	}

	keyVal, err := e.emitCryptoKeyOperand(args[1], pos, "crypto.subtle.deriveKey")
	if err != nil {
		return Value{}, err
	}
	e.emitCryptoUsageCheck(keyVal.Ref, cryptoUsageDeriveKey, "deriveKey")
	out, err := e.emitSubtleDeriveBytes(alg, keyVal.Ref, byteLenRef, pos, "crypto.subtle.deriveKey")
	if err != nil {
		return Value{}, err
	}
	extVal, err := e.emitExpr(args[3])
	if err != nil {
		return Value{}, err
	}
	extRef := e.coerce(extVal, TypeI64).Ref
	key := e.emitNewCryptoKey(derived.algID, derived.hashID, "0", fmt.Sprintf("%d", mask), extRef, out, byteLenRef)
	return e.wrapSettledTaskPromise(Value{Ref: key, Ty: CryptoKeyType()}), nil
}

// ── sign / verify (HMAC) ────────────────────────────────────────────────────

// emitSubtleHmacCompute shares sign's and verify's MAC computation: returns
// (macReg, macLenReg) over data with the key's hash and material.
func (e *Emitter) emitSubtleHmacCompute(keyReg string, dataExpr ast.Expression, pos ast.Pos, what string) (string, string, error) {
	dataReg, dataLenReg, err := e.emitCryptoBufferSource(dataExpr, pos, what)
	if err != nil {
		return "", "", err
	}
	e.ensureCryptoHmac()
	e.ensureMalloc()
	hashID := e.emitCryptoKeyField(keyReg, 1, "i64")
	kData := e.emitCryptoKeyField(keyReg, 5, "ptr")
	kLen := e.emitCryptoKeyField(keyReg, 6, "i64")
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 64)", out))
	outLenP := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", outLenP))
	rc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_hmac_sign(i64 %s, ptr %s, i64 %s, ptr %s, i64 %s, ptr %s, ptr %s)",
		rc, hashID, kData, kLen, dataReg, dataLenReg, out, outLenP))
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString(what+" failed")))
	macLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", macLen, outLenP))
	return out, macLen, nil
}

// emitSubtleSign implements crypto.subtle.sign(algorithm, key, data):
// Promise<ArrayBuffer>. Phase 2 scope: HMAC.
func (e *Emitter) emitSubtleSign(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 3 {
		return Value{}, fmt.Errorf("%d:%d: crypto.subtle.sign takes exactly 3 arguments (algorithm, key, data)", pos.Line, pos.Col)
	}
	alg, err := parseSubtleAlgorithm(args[0], pos, "sign")
	if err != nil {
		return Value{}, err
	}
	keyVal, err := e.emitCryptoKeyOperand(args[1], pos, "crypto.subtle.sign")
	if err != nil {
		return Value{}, err
	}
	e.emitCryptoUsageCheck(keyVal.Ref, cryptoUsageSign, "sign")
	switch alg.algID {
	case cryptoAlgHmac:
		mac, macLen, err := e.emitSubtleHmacCompute(keyVal.Ref, args[2], pos, "crypto.subtle.sign")
		if err != nil {
			return Value{}, err
		}
		return e.wrapSettledTaskPromise(e.emitFreshArrayBuffer(mac, macLen)), nil
	case cryptoAlgRsaPss, cryptoAlgEcdsa:
		sig, sigLen, err := e.emitSubtleAsymSign(alg, keyVal.Ref, args[2], pos)
		if err != nil {
			return Value{}, err
		}
		return e.wrapSettledTaskPromise(e.emitFreshArrayBuffer(sig, sigLen)), nil
	}
	return Value{}, fmt.Errorf("%d:%d: crypto.subtle.sign for %s is not implemented yet", pos.Line, pos.Col, alg.name)
}

// emitSubtleAsymSign shares RSA-PSS's (saltLength: runtime i64, hash from
// the key) and ECDSA's (hash: per-call literal, curve from the key) sign
// paths, returning (sigReg, sigLenReg).
func (e *Emitter) emitSubtleAsymSign(alg *subtleAlg, keyReg string, dataExpr ast.Expression, pos ast.Pos) (string, string, error) {
	dataReg, dataLenReg, err := e.emitCryptoBufferSource(dataExpr, pos, "crypto.subtle.sign")
	if err != nil {
		return "", "", err
	}
	kData := e.emitCryptoKeyField(keyReg, 5, "ptr")
	kLen := e.emitCryptoKeyField(keyReg, 6, "i64")
	sigP, sigLenP := e.emitOutSlots()
	rc := e.freshReg()
	if alg.algID == cryptoAlgRsaPss {
		slExpr := alg.field("saltLength")
		if slExpr == nil {
			return "", "", fmt.Errorf("%d:%d: sign RSA-PSS requires a saltLength: member", pos.Line, pos.Col)
		}
		saltRef, err := e.emitI64Operand(slExpr)
		if err != nil {
			return "", "", err
		}
		e.ensureCryptoRsa()
		hashID := e.emitCryptoKeyField(keyReg, 1, "i64")
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_rsa_pss_sign(i64 %s, i64 %s, ptr %s, i64 %s, ptr %s, i64 %s, ptr %s, ptr %s)",
			rc, hashID, saltRef, kData, kLen, dataReg, dataLenReg, sigP, sigLenP))
	} else {
		if alg.hashID == 0 {
			return "", "", fmt.Errorf("%d:%d: sign ECDSA requires a literal hash (e.g. { name: \"ECDSA\", hash: \"SHA-256\" })", pos.Line, pos.Col)
		}
		e.ensureCryptoEcdsa()
		curveID := e.emitCryptoKeyField(keyReg, 1, "i64")
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_ecdsa_sign(i64 %s, i64 %d, ptr %s, i64 %s, ptr %s, i64 %s, ptr %s, ptr %s)",
			rc, curveID, alg.hashID, kData, kLen, dataReg, dataLenReg, sigP, sigLenP))
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString("crypto.subtle.sign failed")))
	sig := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", sig, sigP))
	sigLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", sigLen, sigLenP))
	return sig, sigLen, nil
}

// emitSubtleVerify implements crypto.subtle.verify(algorithm, key,
// signature, data): Promise<boolean>. Phase 2 scope: HMAC (recompute +
// constant-time compare).
func (e *Emitter) emitSubtleVerify(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 4 {
		return Value{}, fmt.Errorf("%d:%d: crypto.subtle.verify takes exactly 4 arguments (algorithm, key, signature, data)", pos.Line, pos.Col)
	}
	alg, err := parseSubtleAlgorithm(args[0], pos, "verify")
	if err != nil {
		return Value{}, err
	}
	keyVal, err := e.emitCryptoKeyOperand(args[1], pos, "crypto.subtle.verify")
	if err != nil {
		return Value{}, err
	}
	e.emitCryptoUsageCheck(keyVal.Ref, cryptoUsageVerify, "verify")
	sigReg, sigLenReg, err := e.emitCryptoBufferSource(args[2], pos, "crypto.subtle.verify")
	if err != nil {
		return Value{}, err
	}
	switch alg.algID {
	case cryptoAlgHmac:
		mac, macLen, err := e.emitSubtleHmacCompute(keyVal.Ref, args[3], pos, "crypto.subtle.verify")
		if err != nil {
			return Value{}, err
		}
		e.ensureCryptoMemeq()
		lenEq := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", lenEq, sigLenReg, macLen))
		eqRaw := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_memeq(ptr %s, ptr %s, i64 %s)", eqRaw, mac, sigReg, macLen))
		bytesEq := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", bytesEq, eqRaw))
		both := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", both, lenEq, bytesEq))
		return e.wrapSettledTaskPromise(Value{Ref: both, Ty: TypeBool}), nil
	case cryptoAlgRsaPss, cryptoAlgEcdsa:
		dataReg, dataLenReg, err := e.emitCryptoBufferSource(args[3], pos, "crypto.subtle.verify")
		if err != nil {
			return Value{}, err
		}
		kData := e.emitCryptoKeyField(keyVal.Ref, 5, "ptr")
		kLen := e.emitCryptoKeyField(keyVal.Ref, 6, "i64")
		rc := e.freshReg()
		if alg.algID == cryptoAlgRsaPss {
			slExpr := alg.field("saltLength")
			if slExpr == nil {
				return Value{}, fmt.Errorf("%d:%d: verify RSA-PSS requires a saltLength: member", pos.Line, pos.Col)
			}
			saltRef, err := e.emitI64Operand(slExpr)
			if err != nil {
				return Value{}, err
			}
			e.ensureCryptoRsa()
			hashID := e.emitCryptoKeyField(keyVal.Ref, 1, "i64")
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_rsa_pss_verify(i64 %s, i64 %s, ptr %s, i64 %s, ptr %s, i64 %s, ptr %s, i64 %s)",
				rc, hashID, saltRef, kData, kLen, dataReg, dataLenReg, sigReg, sigLenReg))
		} else {
			if alg.hashID == 0 {
				return Value{}, fmt.Errorf("%d:%d: verify ECDSA requires a literal hash (e.g. { name: \"ECDSA\", hash: \"SHA-256\" })", pos.Line, pos.Col)
			}
			e.ensureCryptoEcdsa()
			curveID := e.emitCryptoKeyField(keyVal.Ref, 1, "i64")
			e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_ecdsa_verify(i64 %s, i64 %d, ptr %s, i64 %s, ptr %s, i64 %s, ptr %s, i64 %s)",
				rc, curveID, alg.hashID, kData, kLen, dataReg, dataLenReg, sigReg, sigLenReg))
		}
		// Negative → throw (parse failures etc.); 0/1 → the boolean result.
		e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString("crypto.subtle.verify failed")))
		ok := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 1", ok, rc))
		return e.wrapSettledTaskPromise(Value{Ref: ok, Ty: TypeBool}), nil
	}
	return Value{}, fmt.Errorf("%d:%d: crypto.subtle.verify for %s is not implemented yet", pos.Line, pos.Col, alg.name)
}

// ── encrypt / decrypt (AES-GCM, AES-CBC) ────────────────────────────────────

// emitSubtleEncryptDecrypt implements crypto.subtle.encrypt/decrypt(
// algorithm, key, data): Promise<ArrayBuffer>. Phase 2 scope: AES-GCM
// (iv, optional additionalData, optional literal tagLength, default 128)
// and AES-CBC (iv).
func (e *Emitter) emitSubtleEncryptDecrypt(args []ast.Expression, pos ast.Pos, encrypt bool) (Value, error) {
	what := "crypto.subtle.decrypt"
	encFlag := 0
	usageBit := cryptoUsageDecrypt
	if encrypt {
		what = "crypto.subtle.encrypt"
		encFlag = 1
		usageBit = cryptoUsageEncrypt
	}
	if len(args) != 3 {
		return Value{}, fmt.Errorf("%d:%d: %s takes exactly 3 arguments (algorithm, key, data)", pos.Line, pos.Col, what)
	}
	alg, err := parseSubtleAlgorithm(args[0], pos, what)
	if err != nil {
		return Value{}, err
	}
	if alg.algID == cryptoAlgRsaOep {
		return e.emitSubtleRsaOaep(alg, args, pos, encrypt, what, usageBit)
	}
	if alg.algID != cryptoAlgAesGcm && alg.algID != cryptoAlgAesCbc {
		return Value{}, fmt.Errorf("%d:%d: %s for %s is not implemented yet", pos.Line, pos.Col, what, alg.name)
	}
	ivExpr := alg.field("iv")
	if ivExpr == nil {
		return Value{}, fmt.Errorf("%d:%d: %s %s requires an iv: member", pos.Line, pos.Col, what, alg.name)
	}

	keyVal, err := e.emitCryptoKeyOperand(args[1], pos, what)
	if err != nil {
		return Value{}, err
	}
	e.emitCryptoUsageCheck(keyVal.Ref, usageBit, map[bool]string{true: "encrypt", false: "decrypt"}[encrypt])
	ivReg, ivLenReg, err := e.emitCryptoBufferSource(ivExpr, pos, what)
	if err != nil {
		return Value{}, err
	}
	dataReg, dataLenReg, err := e.emitCryptoBufferSource(args[2], pos, what)
	if err != nil {
		return Value{}, err
	}
	kData := e.emitCryptoKeyField(keyVal.Ref, 5, "ptr")
	kLen := e.emitCryptoKeyField(keyVal.Ref, 6, "i64")
	outP, outLenP := e.emitOutSlots()

	rc := e.freshReg()
	if alg.algID == cryptoAlgAesGcm {
		aadReg, aadLenReg := "null", "0"
		if aadExpr := alg.field("additionalData"); aadExpr != nil {
			aadReg, aadLenReg, err = e.emitCryptoBufferSource(aadExpr, pos, what)
			if err != nil {
				return Value{}, err
			}
		}
		tagBits := "128"
		if tlExpr := alg.field("tagLength"); tlExpr != nil {
			tagBits, err = e.emitI64Operand(tlExpr)
			if err != nil {
				return Value{}, err
			}
		}
		e.ensureCryptoAesGcm()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_aes_gcm(i64 %d, ptr %s, i64 %s, ptr %s, i64 %s, ptr %s, i64 %s, i64 %s, ptr %s, i64 %s, ptr %s, ptr %s)",
			rc, encFlag, kData, kLen, ivReg, ivLenReg, aadReg, aadLenReg, tagBits, dataReg, dataLenReg, outP, outLenP))
	} else {
		e.ensureCryptoAesCbc()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_aes_cbc(i64 %d, ptr %s, i64 %s, ptr %s, ptr %s, i64 %s, ptr %s, ptr %s)",
			rc, encFlag, kData, kLen, ivReg, dataReg, dataLenReg, outP, outLenP))
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString(what+" failed")))
	outReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", outReg, outP))
	outLenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", outLenReg, outLenP))
	return e.wrapSettledTaskPromise(e.emitFreshArrayBuffer(outReg, outLenReg)), nil
}

// emitSubtleRsaOaep implements RSA-OAEP encrypt (public key) / decrypt
// (private key), with an optional label: BufferSource (openssl backend
// only — the commoncrypto backend's SecKey has no OAEP-label parameter and
// reports NotSupportedError at runtime for a non-empty label).
func (e *Emitter) emitSubtleRsaOaep(alg *subtleAlg, args []ast.Expression, pos ast.Pos, encrypt bool, what string, usageBit int) (Value, error) {
	keyVal, err := e.emitCryptoKeyOperand(args[1], pos, what)
	if err != nil {
		return Value{}, err
	}
	e.emitCryptoUsageCheck(keyVal.Ref, usageBit, map[bool]string{true: "encrypt", false: "decrypt"}[encrypt])
	labelReg, labelLenReg := "null", "0"
	if lExpr := alg.field("label"); lExpr != nil {
		labelReg, labelLenReg, err = e.emitCryptoBufferSource(lExpr, pos, what)
		if err != nil {
			return Value{}, err
		}
	}
	dataReg, dataLenReg, err := e.emitCryptoBufferSource(args[2], pos, what)
	if err != nil {
		return Value{}, err
	}
	e.ensureCryptoRsa()
	hashID := e.emitCryptoKeyField(keyVal.Ref, 1, "i64")
	kind := e.emitCryptoKeyField(keyVal.Ref, 4, "i64")
	isPriv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 2", isPriv, kind))
	isPriv64 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i1 %s to i64", isPriv64, isPriv))
	kData := e.emitCryptoKeyField(keyVal.Ref, 5, "ptr")
	kLen := e.emitCryptoKeyField(keyVal.Ref, 6, "i64")
	outP, outLenP := e.emitOutSlots()
	encFlag := 0
	if encrypt {
		encFlag = 1
	}
	rc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_rsa_oaep(i64 %d, i64 %s, ptr %s, i64 %s, i64 %s, ptr %s, i64 %s, ptr %s, i64 %s, ptr %s, ptr %s)",
		rc, encFlag, hashID, kData, kLen, isPriv64, labelReg, labelLenReg, dataReg, dataLenReg, outP, outLenP))
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString(what+" failed")))
	outReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", outReg, outP))
	outLenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", outLenReg, outLenP))
	return e.wrapSettledTaskPromise(e.emitFreshArrayBuffer(outReg, outLenReg)), nil
}
