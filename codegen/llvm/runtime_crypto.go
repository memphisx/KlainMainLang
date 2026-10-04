package llvm

import _ "embed"

// crypto.* randomness (TDD-00240) lives in cryptorandsrc/cryptorand.c: a real
// CSPRNG, deliberately not the source Math.random()'s portable fallback uses
// (plain C89 rand(), not cryptographically secure) — arc4random_buf on the
// BSDs and macOS, getrandom() on Linux.

//go:embed cryptorandsrc/cryptorand.c
var cryptoRandSource string

// CryptoRandSource is the randomness runtime's C source.
func CryptoRandSource() string { return cryptoRandSource }

// UsesCryptoRand reports whether the program links cryptorand.c; the fill and
// UUID routines both pull in the bytes routine.
func (e *Emitter) UsesCryptoRand() bool { return e.usedCryptoRandomBytes }

// CryptoRandCFlags selects randomUUID, which needs the string runtime, and
// the fill of a value held in `any`, which needs the dynamic-value runtime.
func (e *Emitter) CryptoRandCFlags() []string {
	var flags []string
	if e.usedCryptoRandomUUID {
		flags = append(flags, "-DKML_CRYPTO_UUID")
	}
	if e.usedCryptoFillAny {
		flags = append(flags, "-DKML_CRYPTO_FILL_ANY")
	}
	return flags
}

// ensureCryptoRandomBytes declares __kml_crypto_random_bytes(ptr buf, i64 n):
// fills n bytes at buf with cryptographically-secure random data.
func (e *Emitter) ensureCryptoRandomBytes() {
	if e.usedCryptoRandomBytes {
		return
	}
	e.usedCryptoRandomBytes = true
	e.emitGlobal("declare void @__kml_crypto_random_bytes(ptr, i64)")
}

// ensureCryptoFillAny declares __kml_crypto_fill_any (cryptorandsrc):
// getRandomValues on a value known only at run time.
func (e *Emitter) ensureCryptoFillAny() {
	if e.usedCryptoFillAny {
		return
	}
	e.usedCryptoFillAny = true
	e.ensureCryptoRandomBytes()
	e.ensureDynJSONC()
	e.emitGlobal("declare i64 @__kml_crypto_fill_any(i64)")
}

// ensureCryptoRandomUUID declares __kml_crypto_random_uuid: 16 random bytes
// (via the same CSPRNG source as getRandomValues), with the version (4) and
// variant bits set per RFC 4122, formatted as the standard
// "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx" hex string.
func (e *Emitter) ensureCryptoRandomUUID() {
	if e.usedCryptoRandomUUID {
		return
	}
	e.usedCryptoRandomUUID = true
	e.ensureCryptoRandomBytes()
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_crypto_random_uuid()")
}
