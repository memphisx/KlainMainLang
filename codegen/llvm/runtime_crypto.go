package llvm

import (
	"fmt"
	"runtime"
	"strings"
)

// ensureCryptoRandomBytes declares __kml_crypto_random_bytes(ptr buf, i64 n):
// fills n bytes at buf with cryptographically-secure random data.
// Deliberately NOT the same source Math.random()'s portable fallback uses
// (plain C89 rand(), not cryptographically secure) — crypto.* needs a real
// CSPRNG: arc4random_buf (BSD/macOS, itself a CSPRNG, no seeding needed) or
// getrandom() (Linux, reads from the kernel's CSPRNG), matching the
// docs/status/README.md roadmap note this was scoped from.
func (e *Emitter) ensureCryptoRandomBytes() {
	if e.usedCryptoRandomBytes {
		return
	}
	e.usedCryptoRandomBytes = true
	switch runtime.GOOS {
	case "darwin", "freebsd", "openbsd", "netbsd", "dragonfly":
		e.emitGlobal("declare void @arc4random_buf(ptr noundef, i64 noundef)")
		e.emitGlobal(`
define void @__kml_crypto_random_bytes(ptr %buf, i64 %n) {
entry:
  call void @arc4random_buf(ptr %buf, i64 %n)
  ret void
}`)
	default:
		e.emitGlobal("declare i64 @getrandom(ptr noundef, i64 noundef, i32 noundef)")
		e.emitGlobal(`
define void @__kml_crypto_random_bytes(ptr %buf, i64 %n) {
entry:
  %r = call i64 @getrandom(ptr %buf, i64 %n, i32 0)
  ret void
}`)
	}
}

// ensureCryptoFillNumberArray declares __kml_crypto_fill_number_array(ptr
// arr, i64 len): fills an existing number[] array's elements with random
// byte values (0-255 each) — the crypto.getRandomValues(arr) implementation.
// A deliberate deviation from the real API (which fills a TypedArray in
// place, byte for byte): this predates ArrayBuffer/TypedArrays, and is kept
// as the legacy back-compat path now that getRandomValues fills real
// TypedArrays/ArrayBuffers directly (ADR-00317) — a plain number[] stands
// in as the "buffer," one random byte value per i64 element.
func (e *Emitter) ensureCryptoFillNumberArray() {
	if e.usedCryptoFillNumArray {
		return
	}
	e.usedCryptoFillNumArray = true
	e.ensureCryptoRandomBytes()
	e.ensureMalloc()
	e.ensureFree()
	e.emitGlobal(`
define void @__kml_crypto_fill_number_array(ptr %arr, i64 %len) {
entry:
  %tmpbuf = call ptr @malloc(i64 %len)
  call void @__kml_crypto_random_bytes(ptr %tmpbuf, i64 %len)
  br label %loopcheck

loopcheck:
  %i = phi i64 [ 0, %entry ], [ %i_next, %loopbody ]
  %cont = icmp slt i64 %i, %len
  br i1 %cont, label %loopbody, label %done

loopbody:
  %bp = getelementptr i8, ptr %tmpbuf, i64 %i
  %b8 = load i8, ptr %bp, align 1
  %b64 = zext i8 %b8 to i64
  %ap = getelementptr i64, ptr %arr, i64 %i
  store i64 %b64, ptr %ap, align 8
  %i_next = add i64 %i, 1
  br label %loopcheck

done:
  call void @free(ptr %tmpbuf)
  ret void
}`)
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
	e.ensureMalloc()
	e.ensureSprintf()

	var loads strings.Builder
	args := make([]string, 16)
	for i := 0; i < 16; i++ {
		loads.WriteString(fmt.Sprintf(`
  %%p%d = getelementptr i8, ptr %%bufp, i64 %d
  %%b%draw = load i8, ptr %%p%d, align 1`, i, i, i, i))
		args[i] = fmt.Sprintf("i32 %%b%dz", i)
	}
	// Version/variant bit-fixup happens on the raw bytes for indices 6 and 8
	// before they're zext'd for formatting.
	fixup := `
  %b6masked = and i8 %b6raw, 15
  %b6fixed = or i8 %b6masked, 64
  %b8masked = and i8 %b8raw, 63
  %b8fixed = or i8 %b8masked, 128`
	var zexts strings.Builder
	for i := 0; i < 16; i++ {
		src := fmt.Sprintf("%%b%draw", i)
		if i == 6 {
			src = "%b6fixed"
		} else if i == 8 {
			src = "%b8fixed"
		}
		zexts.WriteString(fmt.Sprintf("\n  %%b%dz = zext i8 %s to i32", i, src))
	}

	fmtPtr := e.internString("%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x")
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_crypto_random_uuid() {
entry:
  %%buf = alloca [16 x i8], align 1
  %%bufp = getelementptr [16 x i8], ptr %%buf, i32 0, i32 0
  call void @__kml_crypto_random_bytes(ptr %%bufp, i64 16)%s%s%s
  %%out = call ptr @malloc(i64 37)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%out, ptr %s, %s)
  ret ptr %%out
}`, loads.String(), fixup, zexts.String(), fmtPtr, strings.Join(args, ", ")))
}
