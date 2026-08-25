package llvm

import "fmt"

// runtime_crypto_subtle.go — declarations and helpers for the __kml_crypto_*
// subtle-crypto ABI (TDD-00104). The ABI is implemented by the backend C file
// selected with -crypto (cryptosrc/crypto_openssl.c or
// crypto_commoncrypto.c, see crypto.go); every ensure* here also marks
// usesCrypto so main.go compiles+links the backend.
//
// Shared error contract: 0 = ok, -1 = OperationError, -2 = DataError,
// -3 = NotSupportedError. __kml_crypto_check maps a negative code to a
// catchable error carrying the matching DOMException-style name.

// ensureCryptoCheck emits __kml_crypto_check(i64 code, ptr msg): a no-op for
// code >= 0, otherwise builds an error object (the fetch error-path shape:
// { i64 kind, ptr msg, ptr name }) and throws it.
func (e *Emitter) ensureCryptoCheck() {
	if e.usedCryptoCheck {
		return
	}
	e.usedCryptoCheck = true
	e.ensureMalloc()
	e.ensureExceptionHelpers()
	opErr := e.internString("OperationError")
	dataErr := e.internString("DataError")
	notSup := e.internString("NotSupportedError")
	invAcc := e.internString("InvalidAccessError")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_crypto_check(i64 %%code, ptr %%msg) {
entry:
  %%bad = icmp slt i64 %%code, 0
  br i1 %%bad, label %%throw, label %%ok

throw:
  %%isdata = icmp eq i64 %%code, -2
  %%n1 = select i1 %%isdata, ptr %s, ptr %s
  %%isnotsup = icmp eq i64 %%code, -3
  %%n2 = select i1 %%isnotsup, ptr %s, ptr %%n1
  %%isinv = icmp eq i64 %%code, -4
  %%name = select i1 %%isinv, ptr %s, ptr %%n2
  %%errobj = call ptr @malloc(i64 24)
  %%kindp = getelementptr { i64, ptr, ptr }, ptr %%errobj, i32 0, i32 0
  store i64 0, ptr %%kindp, align 8
  %%msgp = getelementptr { i64, ptr, ptr }, ptr %%errobj, i32 0, i32 1
  store ptr %%msg, ptr %%msgp, align 8
  %%namep = getelementptr { i64, ptr, ptr }, ptr %%errobj, i32 0, i32 2
  store ptr %%name, ptr %%namep, align 8
  call void @__kml_throw(ptr %%errobj)
  unreachable

ok:
  ret void
}`, dataErr, opErr, notSup, invAcc))
}

// ensureCryptoDigest declares the backend's __kml_crypto_digest.
func (e *Emitter) ensureCryptoDigest() {
	if e.usedCryptoDigest {
		return
	}
	e.usedCryptoDigest = true
	e.usesCrypto = true
	e.ensureCryptoCheck()
	e.emitGlobal("declare i64 @__kml_crypto_digest(i64 noundef, ptr noundef, i64 noundef, ptr noundef, ptr noundef)")
}

// ensureCryptoHmac declares the backend's __kml_crypto_hmac_sign.
func (e *Emitter) ensureCryptoHmac() {
	if e.usedCryptoHmac {
		return
	}
	e.usedCryptoHmac = true
	e.usesCrypto = true
	e.ensureCryptoCheck()
	e.emitGlobal("declare i64 @__kml_crypto_hmac_sign(i64 noundef, ptr noundef, i64 noundef, ptr noundef, i64 noundef, ptr noundef, ptr noundef)")
}

// ensureCryptoMemeq declares the backend's constant-time __kml_crypto_memeq.
func (e *Emitter) ensureCryptoMemeq() {
	if e.usedCryptoMemeq {
		return
	}
	e.usedCryptoMemeq = true
	e.usesCrypto = true
	e.emitGlobal("declare i64 @__kml_crypto_memeq(ptr noundef, ptr noundef, i64 noundef)")
}

// ensureCryptoAesGcm declares the backend's __kml_crypto_aes_gcm.
func (e *Emitter) ensureCryptoAesGcm() {
	if e.usedCryptoAesGcm {
		return
	}
	e.usedCryptoAesGcm = true
	e.usesCrypto = true
	e.ensureCryptoCheck()
	e.emitGlobal("declare i64 @__kml_crypto_aes_gcm(i64 noundef, ptr noundef, i64 noundef, ptr noundef, i64 noundef, ptr noundef, i64 noundef, i64 noundef, ptr noundef, i64 noundef, ptr noundef, ptr noundef)")
}

// ensureCryptoAesCbc declares the backend's __kml_crypto_aes_cbc.
func (e *Emitter) ensureCryptoAesCbc() {
	if e.usedCryptoAesCbc {
		return
	}
	e.usedCryptoAesCbc = true
	e.usesCrypto = true
	e.ensureCryptoCheck()
	e.emitGlobal("declare i64 @__kml_crypto_aes_cbc(i64 noundef, ptr noundef, i64 noundef, ptr noundef, ptr noundef, i64 noundef, ptr noundef, ptr noundef)")
}

// ensureCryptoB64url declares the backend's base64url codec pair (the JWK
// `k`/component codec).
func (e *Emitter) ensureCryptoB64url() {
	if e.usedCryptoB64url {
		return
	}
	e.usedCryptoB64url = true
	e.usesCrypto = true
	e.ensureCryptoCheck()
	e.emitGlobal("declare i64 @__kml_crypto_b64url_encode(ptr noundef, i64 noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i64 @__kml_crypto_b64url_decode(ptr noundef, i64 noundef, ptr noundef, ptr noundef)")
}

// ensureCryptoRsa declares the RSA keygen/OAEP/PSS ABI functions.
func (e *Emitter) ensureCryptoRsa() {
	if e.usedCryptoRsa {
		return
	}
	e.usedCryptoRsa = true
	e.usesCrypto = true
	e.ensureCryptoCheck()
	e.emitGlobal("declare i64 @__kml_crypto_gen_rsa(i64 noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i64 @__kml_crypto_rsa_oaep(i64 noundef, i64 noundef, ptr noundef, i64 noundef, i64 noundef, ptr noundef, i64 noundef, ptr noundef, i64 noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i64 @__kml_crypto_rsa_pss_sign(i64 noundef, i64 noundef, ptr noundef, i64 noundef, ptr noundef, i64 noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i64 @__kml_crypto_rsa_pss_verify(i64 noundef, i64 noundef, ptr noundef, i64 noundef, ptr noundef, i64 noundef, ptr noundef, i64 noundef)")
}

// ensureCryptoEcdsa declares the EC keygen/ECDSA ABI functions.
func (e *Emitter) ensureCryptoEcdsa() {
	if e.usedCryptoEcdsa {
		return
	}
	e.usedCryptoEcdsa = true
	e.usesCrypto = true
	e.ensureCryptoCheck()
	e.emitGlobal("declare i64 @__kml_crypto_gen_ec(i64 noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i64 @__kml_crypto_ecdsa_sign(i64 noundef, i64 noundef, ptr noundef, i64 noundef, ptr noundef, i64 noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i64 @__kml_crypto_ecdsa_verify(i64 noundef, i64 noundef, ptr noundef, i64 noundef, ptr noundef, i64 noundef, ptr noundef, i64 noundef)")
}

// ensureCryptoEcRaw declares the EC raw-point ↔ SPKI converters.
func (e *Emitter) ensureCryptoEcRaw() {
	if e.usedCryptoEcRaw {
		return
	}
	e.usedCryptoEcRaw = true
	e.usesCrypto = true
	e.ensureCryptoCheck()
	e.emitGlobal("declare i64 @__kml_crypto_ec_raw_to_spki(i64 noundef, ptr noundef, i64 noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i64 @__kml_crypto_ec_spki_to_raw(i64 noundef, ptr noundef, i64 noundef, ptr noundef, ptr noundef)")
}

// ensureCryptoJwkRsa declares the RSA JWK component bridge.
func (e *Emitter) ensureCryptoJwkRsa() {
	if e.usedCryptoJwkRsa {
		return
	}
	e.usedCryptoJwkRsa = true
	e.usesCrypto = true
	e.ensureCryptoCheck()
	e.emitGlobal("declare i64 @__kml_crypto_jwk_export_rsa(i64 noundef, ptr noundef, i64 noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i64 @__kml_crypto_jwk_import_rsa(ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef)")
}

// ensureCryptoJwkEc declares the EC JWK component bridge.
func (e *Emitter) ensureCryptoJwkEc() {
	if e.usedCryptoJwkEc {
		return
	}
	e.usedCryptoJwkEc = true
	e.usesCrypto = true
	e.ensureCryptoCheck()
	e.emitGlobal("declare i64 @__kml_crypto_jwk_export_ec(i64 noundef, i64 noundef, ptr noundef, i64 noundef, ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i64 @__kml_crypto_jwk_import_ec(i64 noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef)")
}

// ensureCryptoDerive declares the PBKDF2/HKDF ABI functions.
func (e *Emitter) ensureCryptoDerive() {
	if e.usedCryptoDerive {
		return
	}
	e.usedCryptoDerive = true
	e.usesCrypto = true
	e.ensureCryptoCheck()
	e.emitGlobal("declare i64 @__kml_crypto_pbkdf2(i64 noundef, ptr noundef, i64 noundef, ptr noundef, i64 noundef, i64 noundef, ptr noundef, i64 noundef)")
	e.emitGlobal("declare i64 @__kml_crypto_hkdf(i64 noundef, ptr noundef, i64 noundef, ptr noundef, i64 noundef, ptr noundef, i64 noundef, ptr noundef, i64 noundef)")
}

// ensureCryptoJwkMapSet emits __kml_jwk_map_set(map, key, val): a
// null-skipping Map<string,string> setter, so JWK export can hand every
// component slot to it without per-field branches (absent components are
// NULL from the shim).
func (e *Emitter) ensureCryptoJwkMapSet() {
	if e.usedCryptoJwkMapSet {
		return
	}
	e.usedCryptoJwkMapSet = true
	e.ensureMapStrHelpers()
	e.emitGlobal(`
define void @__kml_jwk_map_set(ptr %m, ptr %k, ptr %v) {
entry:
  %isnull = icmp eq ptr %v, null
  br i1 %isnull, label %skip, label %set

set:
  %bits = ptrtoint ptr %v to i64
  call void @__kml_map_str_set(ptr %m, ptr %k, i64 %bits)
  ret void

skip:
  ret void
}`)
}

// CryptoKey hidden-header layout (TDD-00104): 7 words —
// { i64 algId, i64 param, i64 usages, i64 extractable, i64 kind,
//
//	ptr keyData, i64 keyLen }. kind: 0 secret / 1 public / 2 private.
//
// param is the hash id for HMAC/RSA-OAEP/RSA-PSS keys and the curve id for
// ECDSA keys (Web Crypto binds hash to RSA keys but curve to EC keys —
// ECDSA's hash arrives per sign/verify call instead).
const cryptoKeyStructIR = "{ i64, i64, i64, i64, i64, ptr, i64 }"
const cryptoKeyStructSize = 56

// Web Crypto usage bits (usagesBitmask field).
const (
	cryptoUsageEncrypt    = 1
	cryptoUsageDecrypt    = 2
	cryptoUsageSign       = 4
	cryptoUsageVerify     = 8
	cryptoUsageDeriveKey  = 16
	cryptoUsageDeriveBits = 32
	cryptoUsageWrapKey    = 64
	cryptoUsageUnwrapKey  = 128
)

// emitNewCryptoKey mallocs and fills a CryptoKey header. algId/param are
// compile-time constants; kind/usages/extractable/dataReg/lenReg are i64/
// i64/i64/ptr/i64 runtime registers or literal constant strings.
func (e *Emitter) emitNewCryptoKey(algID, param int, kindRef, usagesRef, extractableRef, dataReg, lenReg string) string {
	e.ensureMalloc()
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", hdr, cryptoKeyStructSize))
	store := func(idx int, ty, ref string) {
		p := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", p, cryptoKeyStructIR, hdr, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", ty, ref, p))
	}
	store(0, "i64", fmt.Sprintf("%d", algID))
	store(1, "i64", fmt.Sprintf("%d", param))
	store(2, "i64", usagesRef)
	store(3, "i64", extractableRef)
	store(4, "i64", kindRef)
	store(5, "ptr", dataReg)
	store(6, "i64", lenReg)
	return hdr
}

// emitCryptoKeyField loads field idx of a CryptoKey header ("ptr" for the
// keyData slot, "i64" for everything else).
func (e *Emitter) emitCryptoKeyField(keyReg string, idx int, ty string) string {
	p := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", p, cryptoKeyStructIR, keyReg, idx))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, ty, p))
	return r
}

// emitCryptoUsageCheck throws InvalidAccessError unless the key's usages
// bitmask contains usageBit.
func (e *Emitter) emitCryptoUsageCheck(keyReg string, usageBit int, opName string) {
	e.ensureCryptoCheck()
	usages := e.emitCryptoKeyField(keyReg, 2, "i64")
	masked := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i64 %s, %d", masked, usages, usageBit))
	ok := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i64 %s, 0", ok, masked))
	code := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 -4", code, ok))
	e.emitInstr(fmt.Sprintf("call void @__kml_crypto_check(i64 %s, ptr %s)",
		code, e.internString("key does not support "+opName)))
}
