// emit_crypto_hash.go — crypto.createHash / crypto.createHmac (TDD-00159,
// ADR-00636/ADR-00637): faithful Node `Hash` and `Hmac` objects over the
// backend's streaming digest/HMAC contexts (OpenSSL EVP / CommonCrypto).
//
//	const h = crypto.createHash('sha1')
//	h.update(key + GUID)
//	const accept = h.digest('base64')             // Sec-WebSocket-Accept, by hand
//
//	crypto.createHmac('sha256', secret).update(msg).digest('hex')
//
// The handle is the opaque backend context pointer: createHash/createHmac call
// `__kml_crypto_hash_new`/`__kml_crypto_hmac_new`, `.update` streams bytes in
// via `_update` (chainable, returning the handle), and `.digest(encoding?)`
// runs `_final` (which frees the context) then encodes. This is a true
// streaming digest — no input buffering. Encodings: hex / base64 / base64url /
// latin1, or a Buffer when omitted.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// HashType / HmacType are the createHash / createHmac handles — opaque ptrs to
// the backend's streaming context.
func HashType() Type { return Type{IR: "ptr", IsHash: true} }
func HmacType() Type { return Type{IR: "ptr", IsHmac: true} }

// nodeHashAlgoID maps a Node crypto hash-algorithm name to the backend id
// (shared by createHash and createHmac). Node uses OpenSSL's lowercase names,
// distinct from WebCrypto's "SHA-256" spelling (subtleHashID).
func nodeHashAlgoID(name string) (int, bool) {
	switch name {
	case "sha1":
		return 1, true
	case "sha256":
		return 2, true
	case "sha384":
		return 3, true
	case "sha512":
		return 4, true
	case "md5":
		return 5, true
	}
	return 0, false
}

// emitCryptoCreateHash implements crypto.createHash(algorithm).
func (e *Emitter) emitCryptoCreateHash(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: crypto.createHash takes exactly one argument (algorithm)", pos.Line, pos.Col)
	}
	hashID, err := e.hashAlgoLiteral(args[0], "createHash", pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureCryptoHashStream()
	ctx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_crypto_hash_new(i64 %d)", ctx, hashID))
	e.emitCryptoNullCheck(ctx, "crypto.createHash: unsupported algorithm")
	return Value{Ref: ctx, Ty: HashType()}, nil
}

// emitCryptoCreateHmac implements crypto.createHmac(algorithm, key).
func (e *Emitter) emitCryptoCreateHmac(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 2 {
		return Value{}, fmt.Errorf("%d:%d: crypto.createHmac takes two arguments (algorithm, key)", pos.Line, pos.Col)
	}
	hashID, err := e.hashAlgoLiteral(args[0], "createHmac", pos)
	if err != nil {
		return Value{}, err
	}
	keyReg, keyLenReg, err := e.hashUpdateSource(args[1], pos)
	if err != nil {
		return Value{}, err
	}
	e.ensureCryptoHmacStream()
	ctx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_crypto_hmac_new(i64 %d, ptr %s, i64 %s)", ctx, hashID, keyReg, keyLenReg))
	e.emitCryptoNullCheck(ctx, "crypto.createHmac: unsupported algorithm")
	return Value{Ref: ctx, Ty: HmacType()}, nil
}

// hashAlgoLiteral resolves a string-literal algorithm argument to a backend id.
func (e *Emitter) hashAlgoLiteral(arg ast.Expression, what string, pos ast.Pos) (int, error) {
	lit, ok := arg.(*ast.StringLiteral)
	if !ok {
		return 0, fmt.Errorf("%d:%d: crypto.%s's algorithm must be a string literal (e.g. \"sha256\")", pos.Line, pos.Col, what)
	}
	id, ok := nodeHashAlgoID(lit.Value)
	if !ok {
		return 0, fmt.Errorf("%d:%d: unsupported hash algorithm %q — must be one of md5, sha1, sha256, sha384, sha512", pos.Line, pos.Col, lit.Value)
	}
	return id, nil
}

// emitCryptoNullCheck throws (via the crypto error path) if reg is null.
func (e *Emitter) emitCryptoNullCheck(reg, msg string) {
	e.ensureCryptoCheck()
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, reg))
	rc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 -1, i64 0", rc, isNull))
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString(msg)))
}

// emitHashMethod dispatches `.update`/`.digest` on a Hash or Hmac (both use
// the same streaming update/final shape, differing only in the backend fn).
func (e *Emitter) emitHashMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos, isHmac bool) (Value, error) {
	objVal, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	updateFn, finalFn := "__kml_crypto_hash_update", "__kml_crypto_hash_final"
	label := "Hash"
	if isHmac {
		updateFn, finalFn, label = "__kml_crypto_hmac_update", "__kml_crypto_hmac_final", "Hmac"
	}
	switch method {
	case "update":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: %s.update takes one argument (data)", pos.Line, pos.Col, label)
		}
		dataReg, byteLenReg, err := e.hashUpdateSource(args[0], pos)
		if err != nil {
			return Value{}, err
		}
		rc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @%s(ptr %s, ptr %s, i64 %s)", rc, updateFn, objVal.Ref, dataReg, byteLenReg))
		e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString(label+".update failed")))
		return objVal, nil // chainable
	case "digest":
		return e.emitHashDigest(objVal.Ref, finalFn, label, args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: a %s supports .update(data) and .digest(encoding?) (got '%s')", pos.Line, pos.Col, label, method)
}

// hashUpdateSource resolves an update()/key argument to (dataPtr, byteLen): a
// string uses its binary-safe length header; a Buffer/TypedArray/ArrayBuffer
// goes through the shared BufferSource extractor.
func (e *Emitter) hashUpdateSource(arg ast.Expression, pos ast.Pos) (dataReg, byteLenReg string, err error) {
	if isPlainStringType(e.inferExprType(arg)) {
		v, err := e.emitExpr(arg)
		if err != nil {
			return "", "", err
		}
		return v.Ref, e.emitStrLenHeader(v.Ref), nil
	}
	return e.emitCryptoBufferSource(arg, pos, "crypto hash/hmac input")
}

// emitHashDigest finalizes the context and encodes: 'hex'/'base64'/'base64url'/
// 'latin1' → string, omitted → Buffer.
func (e *Emitter) emitHashDigest(ctxPtr, finalFn, label string, args []ast.Expression, pos ast.Pos) (Value, error) {
	enc := ""
	if len(args) == 1 {
		lit, ok := args[0].(*ast.StringLiteral)
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: %s.digest's encoding must be a string literal", pos.Line, pos.Col, label)
		}
		enc = lit.Value
	} else if len(args) > 1 {
		return Value{}, fmt.Errorf("%d:%d: %s.digest takes at most one argument (encoding)", pos.Line, pos.Col, label)
	}
	switch enc {
	case "", "hex", "base64", "base64url", "latin1", "binary":
	default:
		return Value{}, fmt.Errorf("%d:%d: %s.digest encoding %q is not supported — use 'hex', 'base64', 'base64url', 'latin1', or omit it for a Buffer", pos.Line, pos.Col, label, enc)
	}

	e.ensureMalloc()
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 64)", out))
	outLenP := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", outLenP))
	rc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @%s(ptr %s, ptr %s, ptr %s)", rc, finalFn, ctxPtr, out, outLenP))
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString(label+".digest failed")))
	outLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", outLen, outLenP))

	switch enc {
	case "hex":
		e.ensureBufferCodecs()
		s := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_buf_hex_enc(ptr %s, i64 %s)", s, out, outLen))
		return Value{Ref: s, Ty: TypePtr}, nil
	case "base64":
		e.ensureBufferCodecs()
		s := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_buf_b64_enc(ptr %s, i64 %s, i32 0)", s, out, outLen))
		return Value{Ref: s, Ty: TypePtr}, nil
	case "base64url":
		e.ensureBufferCodecs()
		s := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_buf_b64_enc(ptr %s, i64 %s, i32 1)", s, out, outLen))
		return Value{Ref: s, Ty: TypePtr}, nil
	case "latin1", "binary":
		e.ensureBufferCodecs()
		s := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_buf_latin1_str(ptr %s, i64 %s)", s, out, outLen))
		return Value{Ref: s, Ty: TypePtr}, nil
	default:
		// No encoding → a Buffer (Uint8Array) over the raw digest bytes.
		r0 := e.freshReg()
		r1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", r0, out))
		e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %s, 1", r1, r0, outLen))
		return Value{Ref: r1, Ty: TypedArrayType("uint8")}, nil
	}
}
