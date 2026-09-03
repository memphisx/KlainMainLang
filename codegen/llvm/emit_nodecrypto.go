// emit_nodecrypto.go — the Node `crypto` *module* surface (ADR-00434):
// `import { generateKeyPair } from 'crypto'` and friends, dispatched on the
// nodecrypto__kml_builtin marker. Distinct from (and reusing) the ambient
// WebCrypto global (`crypto.subtle`, getRandomValues, randomUUID): the
// module re-exports those, and adds the Node-only members —
// generateKeyPair/generateKeyPairSync (PEM-encoded, over the existing
// keygen ABI) and randomBytes.
package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// nodeCryptoKeyPairResultType is generateKeyPairSync's `{ publicKey,
// privateKey }` record (PEM strings).
func nodeCryptoKeyPairResultType() Type {
	return ObjectType([]Field{
		{Name: "publicKey", Ty: TypePtr},
		{Name: "privateKey", Ty: TypePtr},
	})
}

// ensurePemFromDer declares the bufcodecs PEM assembler once.
func (e *Emitter) ensurePemFromDer() {
	e.ensureBufferCodecs()
	if !e.usedPemFromDer {
		e.usedPemFromDer = true
		e.emitGlobal("declare ptr @__kml_pem_from_der(ptr, i64, ptr)")
	}
}

// emitNodeCryptoModuleCall dispatches `crypto.<member>(...)` through the
// module marker. WebCrypto members reuse the ambient emitters.
func (e *Emitter) emitNodeCryptoModuleCall(method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch method {
	case "getRandomValues":
		return e.emitCryptoGetRandomValues(args, pos)
	case "randomUUID":
		return e.emitCryptoRandomUUID(args, pos)
	case "randomBytes":
		return e.emitNodeCryptoRandomBytes(args, pos)
	case "generateKeyPair":
		return e.emitNodeCryptoGenerateKeyPair(args, pos, true)
	case "generateKeyPairSync":
		return e.emitNodeCryptoGenerateKeyPair(args, pos, false)
	case "createHash":
		return e.emitCryptoCreateHash(args, pos)
	case "createHmac":
		return e.emitCryptoCreateHmac(args, pos)
	}
	return Value{}, fmt.Errorf("%d:%d: crypto.%s is not supported", pos.Line, pos.Col, method)
}

// emitNodeCryptoRandomBytes implements crypto.randomBytes(n): n CSPRNG bytes
// as a Buffer (Uint8Array). The callback form is not provided (Node's is
// async-shaped only; use the return value).
func (e *Emitter) emitNodeCryptoRandomBytes(args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) < 1 || len(args) > 2 {
		return Value{}, fmt.Errorf("%d:%d: crypto.randomBytes takes (size[, callback])", pos.Line, pos.Col)
	}
	nv, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	n := e.coerce(nv, TypeI64)
	e.ensureMalloc()
	e.ensureCryptoRandomBytes()
	buf := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", buf, n.Ref))
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_random_bytes(ptr %s, i64 %s)", buf, n.Ref))
	r0 := e.freshReg()
	r1 := e.freshReg()
	bufTy := TypedArrayType("uint8")
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } undef, ptr %s, 0", r0, buf))
	e.emitInstr(fmt.Sprintf("%s = insertvalue { ptr, i64 } %s, i64 %s, 1", r1, r0, n.Ref))
	bufVal := Value{Ref: r1, Ty: bufTy}

	// Callback form: fire cb(null, buf) synchronously (generation never fails).
	// Async-shaped like the fs callbacks, not offloaded.
	if len(args) == 2 {
		cb, err := e.resolveCallbackWithHints(args[1], []Type{errorObjType, bufTy})
		if err != nil {
			return Value{}, err
		}
		if _, err := e.emitCBCall(cb, []Value{{Ref: "null", Ty: errorObjType}, bufVal}); err != nil {
			return Value{}, err
		}
		return Value{Ty: TypeVoid}, nil
	}
	return bufVal, nil
}

// nodeCryptoEncodingOK validates a publicKeyEncoding/privateKeyEncoding
// object literal: { type: <wantType>, format: 'pem' } only.
func nodeCryptoEncodingOK(expr ast.Expression, wantType, which string, pos ast.Pos) error {
	lit, ok := expr.(*ast.ObjectLiteral)
	if !ok {
		return fmt.Errorf("%d:%d: generateKeyPair's %s must be an object literal", pos.Line, pos.Col, which)
	}
	for _, prop := range lit.Properties {
		sl, isStr := prop.Value.(*ast.StringLiteral)
		switch prop.Key {
		case "type":
			if !isStr || sl.Value != wantType {
				return fmt.Errorf("%d:%d: generateKeyPair supports %s type '%s' only", pos.Line, pos.Col, which, wantType)
			}
		case "format":
			if !isStr || sl.Value != "pem" {
				return fmt.Errorf("%d:%d: generateKeyPair supports format 'pem' only", pos.Line, pos.Col)
			}
		default:
			return fmt.Errorf("%d:%d: generateKeyPair's %s supports { type, format } only (no cipher/passphrase — got '%s')", pos.Line, pos.Col, which, prop.Key)
		}
	}
	return nil
}

// emitNodeCryptoGenerateKeyPair implements generateKeyPair('rsa'|'ec',
// options, cb) and generateKeyPairSync (ADR-00434). V1: both encodings must
// be given as { type: 'spki'/'pkcs8', format: 'pem' } — the encoding-less
// KeyObject form is a clean rejection. Generation is synchronous; the async
// form only defers delivery shape (cb fired inline, like fs's callbacks).
func (e *Emitter) emitNodeCryptoGenerateKeyPair(args []ast.Expression, pos ast.Pos, async bool) (Value, error) {
	want := 2
	name := "generateKeyPairSync"
	if async {
		want = 3
		name = "generateKeyPair"
	}
	if len(args) != want {
		return Value{}, fmt.Errorf("%d:%d: crypto.%s takes (type, options%s)", pos.Line, pos.Col, name, map[bool]string{true: ", callback", false: ""}[async])
	}
	typeLit, ok := args[0].(*ast.StringLiteral)
	if !ok || (typeLit.Value != "rsa" && typeLit.Value != "ec") {
		return Value{}, fmt.Errorf("%d:%d: crypto.%s supports 'rsa' and 'ec' key types (literal)", pos.Line, pos.Col, name)
	}
	opts, ok := args[1].(*ast.ObjectLiteral)
	if !ok {
		return Value{}, fmt.Errorf("%d:%d: crypto.%s's options must be an object literal", pos.Line, pos.Col, name)
	}

	var bitsExpr ast.Expression
	curveID := 0
	sawPub, sawPriv := false, false
	for _, prop := range opts.Properties {
		switch prop.Key {
		case "modulusLength":
			bitsExpr = prop.Value
		case "namedCurve":
			sl, isStr := prop.Value.(*ast.StringLiteral)
			if !isStr {
				return Value{}, fmt.Errorf("%d:%d: namedCurve must be a string literal", pos.Line, pos.Col)
			}
			id, okc := subtleCurveID(sl.Value)
			if !okc {
				return Value{}, fmt.Errorf("%d:%d: unsupported namedCurve %q (P-256, P-384, P-521)", pos.Line, pos.Col, sl.Value)
			}
			curveID = id
		case "publicKeyEncoding":
			if err := nodeCryptoEncodingOK(prop.Value, "spki", "publicKeyEncoding", pos); err != nil {
				return Value{}, err
			}
			sawPub = true
		case "privateKeyEncoding":
			if err := nodeCryptoEncodingOK(prop.Value, "pkcs8", "privateKeyEncoding", pos); err != nil {
				return Value{}, err
			}
			sawPriv = true
		case "publicExponent":
			if !subtlePublicExponentOK(prop.Value) {
				return Value{}, fmt.Errorf("%d:%d: only publicExponent 65537 is supported", pos.Line, pos.Col)
			}
		default:
			return Value{}, fmt.Errorf("%d:%d: unsupported %s option '%s'", pos.Line, pos.Col, name, prop.Key)
		}
	}
	if !sawPub || !sawPriv {
		return Value{}, fmt.Errorf("%d:%d: crypto.%s requires publicKeyEncoding + privateKeyEncoding ({ type: 'spki'/'pkcs8', format: 'pem' }) — KeyObject results are not modeled", pos.Line, pos.Col, name)
	}

	p8P, p8LenP := e.emitOutSlots()
	spkiP, spkiLenP := e.emitOutSlots()
	rc := e.freshReg()
	if typeLit.Value == "ec" {
		if curveID == 0 {
			return Value{}, fmt.Errorf("%d:%d: 'ec' requires a namedCurve option", pos.Line, pos.Col)
		}
		e.ensureCryptoEcdsa()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_gen_ec(i64 %d, ptr %s, ptr %s, ptr %s, ptr %s)",
			rc, curveID, p8P, p8LenP, spkiP, spkiLenP))
	} else {
		if bitsExpr == nil {
			return Value{}, fmt.Errorf("%d:%d: 'rsa' requires a modulusLength option", pos.Line, pos.Col)
		}
		bitsRef, err := e.emitI64Operand(bitsExpr)
		if err != nil {
			return Value{}, err
		}
		e.ensureCryptoRsa()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_crypto_gen_rsa(i64 %s, ptr %s, ptr %s, ptr %s, ptr %s)",
			rc, bitsRef, p8P, p8LenP, spkiP, spkiLenP))
	}
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)", rc, e.internString("crypto."+name+" failed")))

	e.ensurePemFromDer()
	load := func(slotP, lenP string) (string, string) {
		d := e.freshReg()
		l := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", d, slotP))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", l, lenP))
		return d, l
	}
	spkiD, spkiL := load(spkiP, spkiLenP)
	p8D, p8L := load(p8P, p8LenP)
	pubPem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_pem_from_der(ptr %s, i64 %s, ptr %s)", pubPem, spkiD, spkiL, e.internString("PUBLIC KEY")))
	privPem := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_pem_from_der(ptr %s, i64 %s, ptr %s)", privPem, p8D, p8L, e.internString("PRIVATE KEY")))

	if !async {
		ty := nodeCryptoKeyPairResultType()
		e.ensureCalloc()
		obj := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @calloc(i64 1, i64 %d)", obj, ty.StructSize()))
		for i, ref := range []string{pubPem, privPem} {
			g := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", g, ty.StructIR(), obj, i))
			e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", ref, g))
		}
		return Value{Ref: obj, Ty: ty}, nil
	}

	// Async form: fire the callback inline (async-shaped, like fs). A
	// 2-param callback is the `mustSucceed((publicKey, privateKey) => …)`
	// shape (the wrapper's type mirrors the inner fn); 3 params get a null
	// leading error.
	// Contextual typing is positional, so the name list must match the
	// callback's own arity: 2 params = the mustSucceed inner shape.
	twoParam := false
	switch fn := unwrapTestWrapper(args[2]).(type) {
	case *ast.ArrowFunction:
		twoParam = len(fn.Params) == 2
	case *ast.FunctionExpression:
		twoParam = len(fn.Params) == 2
	}
	if twoParam {
		contextTypeArrowParams(args[2], "string", "string")
	} else {
		contextTypeArrowParams(args[2], "Error", "string", "string")
	}
	cb, err := e.resolveCallbackWithHints(args[2], []Type{errorObjType, TypePtr, TypePtr})
	if err != nil {
		return Value{}, err
	}
	vals := []Value{
		{Ref: "null", Ty: errorObjType},
		{Ref: pubPem, Ty: TypePtr},
		{Ref: privPem, Ty: TypePtr},
	}
	switch len(cb.paramTypes()) {
	case 3:
	case 2:
		vals = vals[1:]
	default:
		return Value{}, fmt.Errorf("%d:%d: crypto.generateKeyPair's callback takes (err, publicKey, privateKey) — or (publicKey, privateKey) via mustSucceed", pos.Line, pos.Col)
	}
	if _, err := e.emitCBCall(cb, vals); err != nil {
		return Value{}, err
	}
	return Value{Ty: TypeVoid}, nil
}
