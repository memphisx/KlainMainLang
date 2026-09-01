package llvm

import (
	"fmt"
	"strings"
)

const base64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

// ensureBase64Alphabet declares the shared @__kml_base64_alphabet constant
// both __kml_btoa and __kml_base64_encode_bytes index into — factored out
// (rather than each function declaring it inline, as __kml_btoa alone used
// to) so a program using both in the same compile (e.g. WebSocket's
// Sec-WebSocket-Accept, TDD-00039 Stage 1, which needs the length-aware
// encoder, alongside ordinary user-code btoa() calls) gets exactly one
// definition, not a duplicate-symbol link error.
func (e *Emitter) ensureBase64Alphabet() {
	if e.usedBase64Alphabet {
		return
	}
	e.usedBase64Alphabet = true
	e.emitGlobal(fmt.Sprintf(`@__kml_base64_alphabet = private unnamed_addr constant [64 x i8] c"%s"`, base64Alphabet))
}

// ensureBase64Encode declares __kml_btoa: standard base64 encoding (RFC
// 4045), '='-padded. Operates byte-for-byte on the input string — real
// btoa works over a "binary string" (one code unit per byte, 0-255); since
// this compiler's strings are already just byte sequences, encoding a
// plain UTF-8 text string this way matches the common case (ASCII/UTF-8
// text) directly, with no separate byte-buffer type needed.
func (e *Emitter) ensureBase64Encode() {
	if e.usedBase64Encode {
		return
	}
	e.usedBase64Encode = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureBase64Alphabet()
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`
define ptr @__kml_btoa(ptr %str) {
entry:
  %len = call i64 @strlen(ptr %str)
  %len_plus2 = add i64 %len, 2
  %ngroups = udiv i64 %len_plus2, 3
  %outlen = mul i64 %ngroups, 4
  %out = call ptr @__kml_str_alloc(i64 %outlen)
  br label %loopcheck

loopcheck:
  %i = phi i64 [ 0, %entry ], [ %i_next, %loopbody ]
  %oi = phi i64 [ 0, %entry ], [ %oi_next, %loopbody ]
  %cont = icmp slt i64 %i, %len
  br i1 %cont, label %loopbody, label %done

loopbody:
  %i1 = add i64 %i, 1
  %i2 = add i64 %i, 2
  %has1 = icmp slt i64 %i1, %len
  %has2 = icmp slt i64 %i2, %len
  %i1c = select i1 %has1, i64 %i1, i64 %len
  %i2c = select i1 %has2, i64 %i2, i64 %len

  %p0 = getelementptr i8, ptr %str, i64 %i
  %p1 = getelementptr i8, ptr %str, i64 %i1c
  %p2 = getelementptr i8, ptr %str, i64 %i2c
  %b0_8 = load i8, ptr %p0, align 1
  %b1_8 = load i8, ptr %p1, align 1
  %b2_8 = load i8, ptr %p2, align 1
  %b0 = zext i8 %b0_8 to i32
  %b1 = zext i8 %b1_8 to i32
  %b2 = zext i8 %b2_8 to i32

  %b0sh = shl i32 %b0, 16
  %b1sh = shl i32 %b1, 8
  %n0 = or i32 %b0sh, %b1sh
  %n = or i32 %n0, %b2

  %idx0 = lshr i32 %n, 18
  %idx0m = and i32 %idx0, 63
  %idx1 = lshr i32 %n, 12
  %idx1m = and i32 %idx1, 63
  %idx2 = lshr i32 %n, 6
  %idx2m = and i32 %idx2, 63
  %idx3m = and i32 %n, 63

  %idx0_64 = zext i32 %idx0m to i64
  %idx1_64 = zext i32 %idx1m to i64
  %idx2_64 = zext i32 %idx2m to i64
  %idx3_64 = zext i32 %idx3m to i64

  %c0p = getelementptr [64 x i8], ptr @__kml_base64_alphabet, i64 0, i64 %idx0_64
  %c1p = getelementptr [64 x i8], ptr @__kml_base64_alphabet, i64 0, i64 %idx1_64
  %c2p = getelementptr [64 x i8], ptr @__kml_base64_alphabet, i64 0, i64 %idx2_64
  %c3p = getelementptr [64 x i8], ptr @__kml_base64_alphabet, i64 0, i64 %idx3_64
  %c0 = load i8, ptr %c0p, align 1
  %c1 = load i8, ptr %c1p, align 1
  %c2raw = load i8, ptr %c2p, align 1
  %c3raw = load i8, ptr %c3p, align 1

  %c2 = select i1 %has1, i8 %c2raw, i8 61
  %c3 = select i1 %has2, i8 %c3raw, i8 61

  %oi1 = add i64 %oi, 1
  %oi2 = add i64 %oi, 2
  %oi3 = add i64 %oi, 3
  %op0 = getelementptr i8, ptr %out, i64 %oi
  %op1 = getelementptr i8, ptr %out, i64 %oi1
  %op2 = getelementptr i8, ptr %out, i64 %oi2
  %op3 = getelementptr i8, ptr %out, i64 %oi3
  store i8 %c0, ptr %op0, align 1
  store i8 %c1, ptr %op1, align 1
  store i8 %c2, ptr %op2, align 1
  store i8 %c3, ptr %op3, align 1

  %i_next = add i64 %i, 3
  %oi_next = add i64 %oi, 4
  br label %loopcheck

done:
  %termp = getelementptr i8, ptr %out, i64 %oi
  store i8 0, ptr %termp, align 1
  ret ptr %out
}`)
}

// ensureBase64EncodeBytes declares __kml_base64_encode_bytes(ptr data, i64
// len): identical algorithm to __kml_btoa above, except len is a parameter
// instead of computed via strlen — the binary-safe counterpart needed
// whenever the input isn't a NUL-terminated C string, e.g. a SHA-1 digest
// (TDD-00039 Stage 1's Sec-WebSocket-Accept, RFC 6455 §1.3), which can
// legitimately contain an embedded 0x00 byte. Calling strlen-based __kml_btoa
// on such a buffer would silently truncate (or, worse, read past the end of
// the allocation looking for a zero byte that isn't there) — found and
// worked around just for a test's own known-answer input in TDD-00039
// Stage 0's ADR-00125, flagged there as needing exactly this real fix
// before any actual handshake code could ship. Same mirroring
// output-length formula (`ceil(len/3)*4`, `+2`/`udiv 3` idiom) and the same
// shared @__kml_base64_alphabet table — only the length source differs.
func (e *Emitter) ensureBase64EncodeBytes() {
	if e.usedBase64EncodeBytes {
		return
	}
	e.usedBase64EncodeBytes = true
	e.ensureMalloc()
	e.ensureBase64Alphabet()
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`
define ptr @__kml_base64_encode_bytes(ptr %str, i64 %len) {
entry:
  %len_plus2 = add i64 %len, 2
  %ngroups = udiv i64 %len_plus2, 3
  %outlen = mul i64 %ngroups, 4
  %out = call ptr @__kml_str_alloc(i64 %outlen)
  br label %loopcheck

loopcheck:
  %i = phi i64 [ 0, %entry ], [ %i_next, %b64mergeb2 ]
  %oi = phi i64 [ 0, %entry ], [ %oi_next, %b64mergeb2 ]
  %cont = icmp slt i64 %i, %len
  br i1 %cont, label %loopbody, label %done

loopbody:
  %i1 = add i64 %i, 1
  %i2 = add i64 %i, 2
  %has1 = icmp slt i64 %i1, %len
  %has2 = icmp slt i64 %i2, %len

  %p0 = getelementptr i8, ptr %str, i64 %i
  %b0_8 = load i8, ptr %p0, align 1
  %b0 = zext i8 %b0_8 to i32

  br i1 %has1, label %b64loadb1, label %b64skipb1
b64loadb1:
  %p1 = getelementptr i8, ptr %str, i64 %i1
  %b1_8v = load i8, ptr %p1, align 1
  br label %b64mergeb1
b64skipb1:
  br label %b64mergeb1
b64mergeb1:
  %b1_8 = phi i8 [ %b1_8v, %b64loadb1 ], [ 0, %b64skipb1 ]
  %b1 = zext i8 %b1_8 to i32

  br i1 %has2, label %b64loadb2, label %b64skipb2
b64loadb2:
  %p2 = getelementptr i8, ptr %str, i64 %i2
  %b2_8v = load i8, ptr %p2, align 1
  br label %b64mergeb2
b64skipb2:
  br label %b64mergeb2
b64mergeb2:
  %b2_8 = phi i8 [ %b2_8v, %b64loadb2 ], [ 0, %b64skipb2 ]
  %b2 = zext i8 %b2_8 to i32

  %b0sh = shl i32 %b0, 16
  %b1sh = shl i32 %b1, 8
  %n0 = or i32 %b0sh, %b1sh
  %n = or i32 %n0, %b2

  %idx0 = lshr i32 %n, 18
  %idx0m = and i32 %idx0, 63
  %idx1 = lshr i32 %n, 12
  %idx1m = and i32 %idx1, 63
  %idx2 = lshr i32 %n, 6
  %idx2m = and i32 %idx2, 63
  %idx3m = and i32 %n, 63

  %idx0_64 = zext i32 %idx0m to i64
  %idx1_64 = zext i32 %idx1m to i64
  %idx2_64 = zext i32 %idx2m to i64
  %idx3_64 = zext i32 %idx3m to i64

  %c0p = getelementptr [64 x i8], ptr @__kml_base64_alphabet, i64 0, i64 %idx0_64
  %c1p = getelementptr [64 x i8], ptr @__kml_base64_alphabet, i64 0, i64 %idx1_64
  %c2p = getelementptr [64 x i8], ptr @__kml_base64_alphabet, i64 0, i64 %idx2_64
  %c3p = getelementptr [64 x i8], ptr @__kml_base64_alphabet, i64 0, i64 %idx3_64
  %c0 = load i8, ptr %c0p, align 1
  %c1 = load i8, ptr %c1p, align 1
  %c2raw = load i8, ptr %c2p, align 1
  %c3raw = load i8, ptr %c3p, align 1

  %c2 = select i1 %has1, i8 %c2raw, i8 61
  %c3 = select i1 %has2, i8 %c3raw, i8 61

  %oi1 = add i64 %oi, 1
  %oi2 = add i64 %oi, 2
  %oi3 = add i64 %oi, 3
  %op0 = getelementptr i8, ptr %out, i64 %oi
  %op1 = getelementptr i8, ptr %out, i64 %oi1
  %op2 = getelementptr i8, ptr %out, i64 %oi2
  %op3 = getelementptr i8, ptr %out, i64 %oi3
  store i8 %c0, ptr %op0, align 1
  store i8 %c1, ptr %op1, align 1
  store i8 %c2, ptr %op2, align 1
  store i8 %c3, ptr %op3, align 1

  %i_next = add i64 %i, 3
  %oi_next = add i64 %oi, 4
  br label %loopcheck

done:
  %termp = getelementptr i8, ptr %out, i64 %oi
  store i8 0, ptr %termp, align 1
  ret ptr %out
}`)
}

// ensureBase64Decode declares __kml_atob: the inverse of __kml_btoa.
// Implements WHATWG forgiving-base64 (ADR-00458/ADR-00550): all ASCII
// whitespace (tab/LF/FF/CR/space) is stripped first, up to two trailing '='
// are removed, a remaining length ≡ 1 (mod 4) throws `InvalidCharacterError`,
// and a non-multiple-of-4 length is re-padded to the next multiple so the
// trailing 2- or 3-character group decodes instead of being dropped. A
// character outside the base64 alphabet still throws `InvalidCharacterError`,
// as does an interior or excess '=' — after the up-to-two trailing '=' are
// stripped, the remaining data region is validated against the alphabet only
// ('=' is not part of it), so `"a=b="` and `"ab==="` are rejected the way real
// atob rejects them (ADR-00563).
func (e *Emitter) ensureBase64Decode() {
	if e.usedBase64Decode {
		return
	}
	e.usedBase64Decode = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureExceptionHelpers()

	table := make([]byte, 256)
	for i, c := range []byte(base64Alphabet) {
		table[c] = byte(i)
	}
	entries := make([]string, 256)
	for i, v := range table {
		entries[i] = fmt.Sprintf("i8 %d", v)
	}
	e.ensureStrHeaderRuntime()
	e.emitGlobal(fmt.Sprintf("@__kml_base64_decode_table = private unnamed_addr constant [256 x i8] [%s]", strings.Join(entries, ", ")))
	valid := make([]byte, 256)
	for _, c := range []byte(base64Alphabet) {
		valid[c] = 1
	}
	// '=' is deliberately NOT valid: trailing padding is stripped before the
	// validation loop runs (which now scans only the data region [0, %sw)), so
	// any '=' that survives into the data is an interior/excess pad — a failure.
	ventries := make([]string, 256)
	for i, v := range valid {
		ventries[i] = fmt.Sprintf("i8 %d", v)
	}
	e.emitGlobal(fmt.Sprintf("@__kml_base64_valid_table = private unnamed_addr constant [256 x i8] [%s]", strings.Join(ventries, ", ")))
	e.emitGlobal(`@.kml_atob_errmsg = private unnamed_addr constant [54 x i8] c"The string to be decoded contains invalid characters.\00"`)
	e.emitGlobal(`@.kml_atob_errname = private unnamed_addr constant [22 x i8] c"InvalidCharacterError\00"`)
	e.emitGlobal(`
define ptr @__kml_atob(ptr %str) {
entry:
  %rawlen = call i64 @strlen(ptr %str)
  %cap = add i64 %rawlen, 5
  %norm = call ptr @malloc(i64 %cap)
  br label %nloop
nloop:
  %ni = phi i64 [0, %entry], [%nin, %ncont]
  %nw = phi i64 [0, %entry], [%nw2, %ncont]
  %ndone = icmp sge i64 %ni, %rawlen
  br i1 %ndone, label %nfin, label %nchk
nchk:
  %np = getelementptr i8, ptr %str, i64 %ni
  %nc = load i8, ptr %np, align 1
  %isws1 = icmp eq i8 %nc, 9
  %isws2 = icmp eq i8 %nc, 10
  %isws3 = icmp eq i8 %nc, 12
  %isws4 = icmp eq i8 %nc, 13
  %isws5 = icmp eq i8 %nc, 32
  %w_a = or i1 %isws1, %isws2
  %w_b = or i1 %w_a, %isws3
  %w_c = or i1 %w_b, %isws4
  %isws = or i1 %w_c, %isws5
  br i1 %isws, label %nskip, label %nwrite
nwrite:
  %wp = getelementptr i8, ptr %norm, i64 %nw
  store i8 %nc, ptr %wp, align 1
  %nw_w = add i64 %nw, 1
  br label %ncont
nskip:
  br label %ncont
ncont:
  %nw2 = phi i64 [%nw_w, %nwrite], [%nw, %nskip]
  %nin = add i64 %ni, 1
  br label %nloop
nfin:
  br label %sloop
sloop:
  %sw = phi i64 [%nw, %nfin], [%sw2, %sbody]
  %sc = phi i64 [0, %nfin], [%sc2, %sbody]
  %can = icmp slt i64 %sc, 2
  %haschars = icmp sgt i64 %sw, 0
  %can2 = and i1 %can, %haschars
  br i1 %can2, label %scheck, label %sdone
scheck:
  %swm = sub i64 %sw, 1
  %scp = getelementptr i8, ptr %norm, i64 %swm
  %scc = load i8, ptr %scp, align 1
  %iseq = icmp eq i8 %scc, 61
  br i1 %iseq, label %sbody, label %sdone
sbody:
  %sw2 = sub i64 %sw, 1
  %sc2 = add i64 %sc, 1
  br label %sloop
sdone:
  %rem = urem i64 %sw, 4
  %isbad = icmp eq i64 %rem, 1
  br i1 %isbad, label %vthrow, label %padcalc
padcalc:
  %sub = sub i64 4, %rem
  %pad = urem i64 %sub, 4
  br label %ploop
ploop:
  %pi = phi i64 [0, %padcalc], [%pin, %pbody]
  %pdone = icmp sge i64 %pi, %pad
  br i1 %pdone, label %pfin, label %pbody
pbody:
  %pidx = add i64 %sw, %pi
  %pp = getelementptr i8, ptr %norm, i64 %pidx
  store i8 61, ptr %pp, align 1
  %pin = add i64 %pi, 1
  br label %ploop
pfin:
  %len = add i64 %sw, %pad
  %tp = getelementptr i8, ptr %norm, i64 %len
  store i8 0, ptr %tp, align 1
  br label %vloop
vloop:
  %vi = phi i64 [0, %pfin], [%vin, %vcont]
  %vdone = icmp sge i64 %vi, %sw
  br i1 %vdone, label %decode, label %vchk
vchk:
  %vp = getelementptr i8, ptr %norm, i64 %vi
  %vc = load i8, ptr %vp, align 1
  %vc64 = zext i8 %vc to i64
  %vtp = getelementptr [256 x i8], ptr @__kml_base64_valid_table, i64 0, i64 %vc64
  %vv = load i8, ptr %vtp, align 1
  %vok = icmp ne i8 %vv, 0
  br i1 %vok, label %vcont, label %vthrow
vcont:
  %vin = add i64 %vi, 1
  br label %vloop
vthrow:
  %emsg = call ptr @__kml_str_from_cstr(ptr @.kml_atob_errmsg)
  %ename = call ptr @__kml_str_from_cstr(ptr @.kml_atob_errname)
  %errobj = call ptr @malloc(i64 24)
  %kindp = getelementptr { i64, ptr, ptr }, ptr %errobj, i32 0, i32 0
  store i64 7, ptr %kindp, align 8
  %msgp = getelementptr { i64, ptr, ptr }, ptr %errobj, i32 0, i32 1
  store ptr %emsg, ptr %msgp, align 8
  %namep = getelementptr { i64, ptr, ptr }, ptr %errobj, i32 0, i32 2
  store ptr %ename, ptr %namep, align 8
  call void @__kml_throw(ptr %errobj)
  unreachable
decode:
  %ngroups = udiv i64 %len, 4
  %outlen_est = mul i64 %ngroups, 3
  %out = call ptr @__kml_str_alloc(i64 %outlen_est)
  br label %loopcheck

loopcheck:
  %i = phi i64 [ 0, %decode ], [ %i_next, %loopbody ]
  %oi = phi i64 [ 0, %decode ], [ %oi_next, %loopbody ]
  %i4 = add i64 %i, 4
  %cont = icmp sle i64 %i4, %len
  br i1 %cont, label %loopbody, label %done

loopbody:
  %i1 = add i64 %i, 1
  %i2 = add i64 %i, 2
  %i3 = add i64 %i, 3
  %p0 = getelementptr i8, ptr %norm, i64 %i
  %p1 = getelementptr i8, ptr %norm, i64 %i1
  %p2 = getelementptr i8, ptr %norm, i64 %i2
  %p3 = getelementptr i8, ptr %norm, i64 %i3
  %ch0 = load i8, ptr %p0, align 1
  %ch1 = load i8, ptr %p1, align 1
  %ch2 = load i8, ptr %p2, align 1
  %ch3 = load i8, ptr %p3, align 1

  %ch2eq = icmp eq i8 %ch2, 61
  %ch3eq = icmp eq i8 %ch3, 61

  %ch0_64 = zext i8 %ch0 to i64
  %ch1_64 = zext i8 %ch1 to i64
  %ch2_64 = zext i8 %ch2 to i64
  %ch3_64 = zext i8 %ch3 to i64

  %t0p = getelementptr [256 x i8], ptr @__kml_base64_decode_table, i64 0, i64 %ch0_64
  %t1p = getelementptr [256 x i8], ptr @__kml_base64_decode_table, i64 0, i64 %ch1_64
  %t2p = getelementptr [256 x i8], ptr @__kml_base64_decode_table, i64 0, i64 %ch2_64
  %t3p = getelementptr [256 x i8], ptr @__kml_base64_decode_table, i64 0, i64 %ch3_64
  %v0_8 = load i8, ptr %t0p, align 1
  %v1_8 = load i8, ptr %t1p, align 1
  %v2_8 = load i8, ptr %t2p, align 1
  %v3_8 = load i8, ptr %t3p, align 1

  %v0 = zext i8 %v0_8 to i32
  %v1 = zext i8 %v1_8 to i32
  %v2 = zext i8 %v2_8 to i32
  %v3 = zext i8 %v3_8 to i32

  %v0sh = shl i32 %v0, 18
  %v1sh = shl i32 %v1, 12
  %v2sh = shl i32 %v2, 6
  %n0 = or i32 %v0sh, %v1sh
  %n1 = or i32 %n0, %v2sh
  %n = or i32 %n1, %v3

  %b0_32 = lshr i32 %n, 16
  %b0_8 = trunc i32 %b0_32 to i8
  %b1_32 = lshr i32 %n, 8
  %b1_8 = trunc i32 %b1_32 to i8
  %b2_8 = trunc i32 %n to i8

  %oi1 = add i64 %oi, 1
  %oi2 = add i64 %oi, 2
  %op0 = getelementptr i8, ptr %out, i64 %oi
  %op1 = getelementptr i8, ptr %out, i64 %oi1
  %op2 = getelementptr i8, ptr %out, i64 %oi2
  store i8 %b0_8, ptr %op0, align 1
  store i8 %b1_8, ptr %op1, align 1
  store i8 %b2_8, ptr %op2, align 1

  %prodA = select i1 %ch3eq, i64 2, i64 3
  %prod = select i1 %ch2eq, i64 1, i64 %prodA

  %i_next = add i64 %i, 4
  %oi_next = add i64 %oi, %prod
  br label %loopcheck

done:
  %termp = getelementptr i8, ptr %out, i64 %oi
  store i8 0, ptr %termp, align 1
  %hdrp = getelementptr i8, ptr %out, i64 -8
  store i64 %oi, ptr %hdrp, align 8
  ret ptr %out
}`)
}

// ensureUtf8LabelCheck declares __kml_is_utf8_label(ptr) -> i1: the WHATWG
// "get an encoding" normalization (trim leading/trailing ASCII whitespace,
// ASCII-lowercase) restricted to the UTF-8 label set — the only encoding this
// compiler decodes (ADR-00567). Returns true iff the normalized label is one of
// the six UTF-8 aliases; TextDecoder throws a RangeError otherwise.
func (e *Emitter) ensureUtf8LabelCheck() {
	if e.usedUtf8LabelCheck {
		return
	}
	e.usedUtf8LabelCheck = true
	e.ensureStrlen()
	e.emitGlobal(`declare i32 @strcmp(ptr, ptr)`)
	e.emitGlobal(`@.utf8lbl0 = private unnamed_addr constant [6 x i8] c"utf-8\00"`)
	e.emitGlobal(`@.utf8lbl1 = private unnamed_addr constant [5 x i8] c"utf8\00"`)
	e.emitGlobal(`@.utf8lbl2 = private unnamed_addr constant [18 x i8] c"unicode-1-1-utf-8\00"`)
	e.emitGlobal(`@.utf8lbl3 = private unnamed_addr constant [14 x i8] c"unicode11utf8\00"`)
	e.emitGlobal(`@.utf8lbl4 = private unnamed_addr constant [14 x i8] c"unicode20utf8\00"`)
	e.emitGlobal(`@.utf8lbl5 = private unnamed_addr constant [16 x i8] c"x-unicode20utf8\00"`)
	e.emitGlobal(`
define i1 @__kml_is_utf8_label(ptr %s) {
entry:
  %buf = alloca [40 x i8], align 1
  %si = alloca i64, align 8
  %ei = alloca i64, align 8
  %ci = alloca i64, align 8
  %len = call i64 @strlen(ptr %s)
  store i64 0, ptr %si, align 8
  br label %lt.cond
lt.cond:
  %s0 = load i64, ptr %si, align 8
  %s0lt = icmp slt i64 %s0, %len
  br i1 %s0lt, label %lt.chk, label %lt.done
lt.chk:
  %sp = getelementptr i8, ptr %s, i64 %s0
  %sc = load i8, ptr %sp, align 1
  %w1 = icmp eq i8 %sc, 9
  %w2 = icmp eq i8 %sc, 10
  %w3 = icmp eq i8 %sc, 12
  %w4 = icmp eq i8 %sc, 13
  %w5 = icmp eq i8 %sc, 32
  %wa = or i1 %w1, %w2
  %wb = or i1 %wa, %w3
  %wc = or i1 %wb, %w4
  %isws = or i1 %wc, %w5
  br i1 %isws, label %lt.inc, label %lt.done
lt.inc:
  %s0n = add i64 %s0, 1
  store i64 %s0n, ptr %si, align 8
  br label %lt.cond
lt.done:
  store i64 %len, ptr %ei, align 8
  br label %rt.cond
rt.cond:
  %e0 = load i64, ptr %ei, align 8
  %st = load i64, ptr %si, align 8
  %egt = icmp sgt i64 %e0, %st
  br i1 %egt, label %rt.chk, label %rt.done
rt.chk:
  %em1 = sub i64 %e0, 1
  %ep = getelementptr i8, ptr %s, i64 %em1
  %ec = load i8, ptr %ep, align 1
  %rw1 = icmp eq i8 %ec, 9
  %rw2 = icmp eq i8 %ec, 10
  %rw3 = icmp eq i8 %ec, 12
  %rw4 = icmp eq i8 %ec, 13
  %rw5 = icmp eq i8 %ec, 32
  %rwa = or i1 %rw1, %rw2
  %rwb = or i1 %rwa, %rw3
  %rwc = or i1 %rwb, %rw4
  %risws = or i1 %rwc, %rw5
  br i1 %risws, label %rt.dec, label %rt.done
rt.dec:
  store i64 %em1, ptr %ei, align 8
  br label %rt.cond
rt.done:
  %start = load i64, ptr %si, align 8
  %end = load i64, ptr %ei, align 8
  %nlen = sub i64 %end, %start
  %toolong = icmp sgt i64 %nlen, 39
  br i1 %toolong, label %retfalse, label %cp.pre
cp.pre:
  store i64 0, ptr %ci, align 8
  br label %cp.cond
cp.cond:
  %c0 = load i64, ptr %ci, align 8
  %clt = icmp slt i64 %c0, %nlen
  br i1 %clt, label %cp.body, label %cp.fin
cp.body:
  %srcidx = add i64 %start, %c0
  %srcp = getelementptr i8, ptr %s, i64 %srcidx
  %ch = load i8, ptr %srcp, align 1
  %up1 = icmp sge i8 %ch, 65
  %up2 = icmp sle i8 %ch, 90
  %up = and i1 %up1, %up2
  %chl = add i8 %ch, 32
  %chf = select i1 %up, i8 %chl, i8 %ch
  %dstp = getelementptr [40 x i8], ptr %buf, i64 0, i64 %c0
  store i8 %chf, ptr %dstp, align 1
  %c0n = add i64 %c0, 1
  store i64 %c0n, ptr %ci, align 8
  br label %cp.cond
cp.fin:
  %nulp = getelementptr [40 x i8], ptr %buf, i64 0, i64 %nlen
  store i8 0, ptr %nulp, align 1
  %bp = getelementptr [40 x i8], ptr %buf, i64 0, i64 0
  %r0 = call i32 @strcmp(ptr %bp, ptr @.utf8lbl0)
  %m0 = icmp eq i32 %r0, 0
  %r1 = call i32 @strcmp(ptr %bp, ptr @.utf8lbl1)
  %m1 = icmp eq i32 %r1, 0
  %r2 = call i32 @strcmp(ptr %bp, ptr @.utf8lbl2)
  %m2 = icmp eq i32 %r2, 0
  %r3 = call i32 @strcmp(ptr %bp, ptr @.utf8lbl3)
  %m3 = icmp eq i32 %r3, 0
  %r4 = call i32 @strcmp(ptr %bp, ptr @.utf8lbl4)
  %m4 = icmp eq i32 %r4, 0
  %r5 = call i32 @strcmp(ptr %bp, ptr @.utf8lbl5)
  %m5 = icmp eq i32 %r5, 0
  %oa = or i1 %m0, %m1
  %ob = or i1 %oa, %m2
  %oc = or i1 %ob, %m3
  %od = or i1 %oc, %m4
  %oe = or i1 %od, %m5
  ret i1 %oe
retfalse:
  ret i1 false
}`)
}

func (e *Emitter) ensureHexDigits() {
	if e.usedHexDigits {
		return
	}
	e.usedHexDigits = true
	e.emitGlobal(`@__kml_hex_digits = private unnamed_addr constant [16 x i8] c"0123456789ABCDEF"`)
}

// ensureHexDecodeTable declares a 256-entry reverse hex-digit lookup table:
// '0'-'9'/'a'-'f'/'A'-'F' map to 0-15, everything else maps to the sentinel
// -1 (255 as an unsigned byte) — used to validate a "%XX" escape's two
// digits before treating it as a real decode rather than literal text.
func (e *Emitter) ensureHexDecodeTable() {
	if e.usedHexDecodeTable {
		return
	}
	e.usedHexDecodeTable = true
	table := make([]int, 256)
	for i := range table {
		table[i] = -1
	}
	for i := 0; i < 10; i++ {
		table['0'+i] = i
	}
	for i := 0; i < 6; i++ {
		table['a'+i] = 10 + i
		table['A'+i] = 10 + i
	}
	entries := make([]string, 256)
	for i, v := range table {
		entries[i] = fmt.Sprintf("i8 %d", v)
	}
	e.emitGlobal(fmt.Sprintf("@__kml_hex_decode_table = private unnamed_addr constant [256 x i8] [%s]", strings.Join(entries, ", ")))
}

// percentEncodeUnreserved is the character set encodeURIComponent leaves
// unescaped (real ES spec's exact unreserved set). percentEncodeReserved is
// the additional set encodeURI also leaves alone (real ES spec's reserved
// set — characters with special meaning in different parts of a URI, which
// encodeURIComponent escapes but encodeURI does not, since encodeURI is
// meant to be applied to an already-structured full URI).
const (
	percentEncodeUnreserved = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_.!~*'()"
	percentEncodeReserved   = ";/?:@&=+$,#"
)

// ensurePercentEncode is the shared implementation behind
// encodeURIComponent and encodeURI — identical shape, differing only in
// which characters are left unescaped.
func (e *Emitter) ensurePercentEncode(used *bool, fnName, safeChars string) {
	if *used {
		return
	}
	*used = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureHexDigits()
	safeTable := make([]int, 256)
	for _, c := range []byte(safeChars) {
		safeTable[c] = 1
	}
	entries := make([]string, 256)
	for i, v := range safeTable {
		entries[i] = fmt.Sprintf("i8 %d", v)
	}
	tableName := fmt.Sprintf("@__kml_uri_safe_table_%s", fnName)
	e.ensureStrHeaderRuntime()
	e.emitGlobal(fmt.Sprintf("%s = private unnamed_addr constant [256 x i8] [%s]", tableName, strings.Join(entries, ", ")))
	e.emitGlobal(fmt.Sprintf(`
define ptr @%s(ptr %%str) {
entry:
  %%len = call i64 @strlen(ptr %%str)
  %%len3 = mul i64 %%len, 3
  %%out = call ptr @__kml_str_alloc(i64 %%len3)
  br label %%loopcheck

loopcheck:
  %%i = phi i64 [ 0, %%entry ], [ %%i_next_safe, %%safewrite ], [ %%i_next_hex, %%hexwrite ]
  %%oi = phi i64 [ 0, %%entry ], [ %%oi_next_safe, %%safewrite ], [ %%oi_next_hex, %%hexwrite ]
  %%cont = icmp slt i64 %%i, %%len
  br i1 %%cont, label %%loopbody, label %%done

loopbody:
  %%p = getelementptr i8, ptr %%str, i64 %%i
  %%ch_8 = load i8, ptr %%p, align 1
  %%ch_64 = zext i8 %%ch_8 to i64
  %%tp = getelementptr [256 x i8], ptr %s, i64 0, i64 %%ch_64
  %%issafe_8 = load i8, ptr %%tp, align 1
  %%issafe = icmp ne i8 %%issafe_8, 0
  br i1 %%issafe, label %%safewrite, label %%hexwrite

safewrite:
  %%op = getelementptr i8, ptr %%out, i64 %%oi
  store i8 %%ch_8, ptr %%op, align 1
  %%i_next_safe = add i64 %%i, 1
  %%oi_next_safe = add i64 %%oi, 1
  br label %%loopcheck

hexwrite:
  %%ch_32 = zext i8 %%ch_8 to i32
  %%hi = lshr i32 %%ch_32, 4
  %%lo = and i32 %%ch_32, 15
  %%hi_64 = zext i32 %%hi to i64
  %%lo_64 = zext i32 %%lo to i64
  %%hip = getelementptr [16 x i8], ptr @__kml_hex_digits, i64 0, i64 %%hi_64
  %%lop = getelementptr [16 x i8], ptr @__kml_hex_digits, i64 0, i64 %%lo_64
  %%hic = load i8, ptr %%hip, align 1
  %%loc = load i8, ptr %%lop, align 1
  %%op0 = getelementptr i8, ptr %%out, i64 %%oi
  %%oi1 = add i64 %%oi, 1
  %%op1 = getelementptr i8, ptr %%out, i64 %%oi1
  %%oi2 = add i64 %%oi, 2
  %%op2 = getelementptr i8, ptr %%out, i64 %%oi2
  store i8 37, ptr %%op0, align 1
  store i8 %%hic, ptr %%op1, align 1
  store i8 %%loc, ptr %%op2, align 1
  %%i_next_hex = add i64 %%i, 1
  %%oi_next_hex = add i64 %%oi, 3
  br label %%loopcheck

done:
  %%termp = getelementptr i8, ptr %%out, i64 %%oi
  store i8 0, ptr %%termp, align 1
  %%hdrp = getelementptr i8, ptr %%out, i64 -8
  store i64 %%oi, ptr %%hdrp, align 8
  ret ptr %%out
}`, fnName, tableName))
}

func (e *Emitter) ensureEncodeURIComponent() {
	e.ensurePercentEncode(&e.usedEncodeURIComponent, "__kml_encode_uri_component", percentEncodeUnreserved)
}

func (e *Emitter) ensureEncodeURI() {
	e.ensurePercentEncode(&e.usedEncodeURI, "__kml_encode_uri", percentEncodeUnreserved+percentEncodeReserved)
}

// ensurePercentDecode is the shared implementation behind
// decodeURIComponent and decodeURI. Permissive: a malformed or truncated
// "%" escape (not followed by two valid hex digits) passes through as a
// literal '%' rather than throwing, a documented V1 simplification (real
// decodeURIComponent/decodeURI throw a URIError for malformed input).
//
// checkReserved is decodeURI's one real behavioral difference from
// decodeURIComponent: decodeURI must NOT decode a "%XX" escape whose
// decoded byte is one of the reserved URI characters (;/?:@&=+$,#) — those
// are left as the literal 3-character "%XX" text, so a URI's own structural
// characters (e.g. an escaped "/" inside a path segment) can't be
// silently unescaped into something that changes the URI's meaning.
func (e *Emitter) ensurePercentDecode(used *bool, fnName string, checkReserved bool) {
	e.ensurePercentDecodeVariant(used, fnName, checkReserved, false)
}

// ensurePercentDecodeVariant is the shared body for both the lenient
// (HTTP/query-string internal) and strict (the global decodeURI/
// decodeURIComponent) percent decoders. When strict, a malformed escape (a
// `%` not followed by two hex digits, including a truncated one at the string
// end) throws a real `URIError` instead of passing the `%` through as literal
// text — matching real JS's `decodeURIComponent("%")` throwing (ADR-00556).
func (e *Emitter) ensurePercentDecodeVariant(used *bool, fnName string, checkReserved, strict bool) {
	if *used {
		return
	}
	*used = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureHexDecodeTable()

	malformedTarget := "plain"
	pcterrBlock := ""
	if strict {
		e.ensureExceptionHelpers()
		e.ensureStrHeaderRuntime()
		malformedTarget = "pcterr"
		msgGlobal := fmt.Sprintf("@.%s_uri_errmsg", fnName)
		nameGlobal := fmt.Sprintf("@.%s_uri_errname", fnName)
		e.emitGlobal(fmt.Sprintf(`%s = private unnamed_addr constant [14 x i8] c"URI malformed\00"`, msgGlobal))
		e.emitGlobal(fmt.Sprintf(`%s = private unnamed_addr constant [9 x i8] c"URIError\00"`, nameGlobal))
		pcterrBlock = fmt.Sprintf(`
pcterr:
  %%uemsg = call ptr @__kml_str_from_cstr(ptr %s)
  %%uename = call ptr @__kml_str_from_cstr(ptr %s)
  %%uerrobj = call ptr @malloc(i64 24)
  %%ukindp = getelementptr { i64, ptr, ptr }, ptr %%uerrobj, i32 0, i32 0
  store i64 5, ptr %%ukindp, align 8
  %%umsgp = getelementptr { i64, ptr, ptr }, ptr %%uerrobj, i32 0, i32 1
  store ptr %%uemsg, ptr %%umsgp, align 8
  %%unamep = getelementptr { i64, ptr, ptr }, ptr %%uerrobj, i32 0, i32 2
  store ptr %%uename, ptr %%unamep, align 8
  call void @__kml_throw(ptr %%uerrobj)
  unreachable
`, msgGlobal, nameGlobal)
	}

	reservedBlock := ""
	pctdoneLabel := "pctvalid"
	if checkReserved {
		reservedTable := make([]int, 256)
		for _, c := range []byte(percentEncodeReserved) {
			reservedTable[c] = 1
		}
		entries := make([]string, 256)
		for i, v := range reservedTable {
			entries[i] = fmt.Sprintf("i8 %d", v)
		}
		tableName := fmt.Sprintf("@__kml_uri_reserved_table_%s", fnName)
		e.ensureStrHeaderRuntime()
		e.emitGlobal(fmt.Sprintf("%s = private unnamed_addr constant [256 x i8] [%s]", tableName, strings.Join(entries, ", ")))
		pctdoneLabel = "pctdone"
		reservedBlock = fmt.Sprintf(`
  %%isreserved_idx = zext i8 %%byte8 to i64
  %%rtp = getelementptr [256 x i8], ptr %s, i64 0, i64 %%isreserved_idx
  %%isreserved_8 = load i8, ptr %%rtp, align 1
  %%isreserved = icmp ne i8 %%isreserved_8, 0
  br i1 %%isreserved, label %%keepliteral, label %%decodewrite

keepliteral:
  %%opp_lit0 = getelementptr i8, ptr %%out, i64 %%oi
  store i8 37, ptr %%opp_lit0, align 1
  %%oi_lit1 = add i64 %%oi, 1
  %%opp_lit1 = getelementptr i8, ptr %%out, i64 %%oi_lit1
  store i8 %%h1_8, ptr %%opp_lit1, align 1
  %%oi_lit2 = add i64 %%oi, 2
  %%opp_lit2 = getelementptr i8, ptr %%out, i64 %%oi_lit2
  store i8 %%h2_8, ptr %%opp_lit2, align 1
  br label %%pctdone

decodewrite:
  %%opp = getelementptr i8, ptr %%out, i64 %%oi
  store i8 %%byte8, ptr %%opp, align 1
  br label %%pctdone

pctdone:
  %%oi_delta = phi i64 [ 3, %%keepliteral ], [ 1, %%decodewrite ]
  %%i_next_pct = add i64 %%i, 3
  %%oi_next_pct = add i64 %%oi, %%oi_delta
  br label %%loopcheck
`, tableName)
	} else {
		reservedBlock = `
  %opp = getelementptr i8, ptr %out, i64 %oi
  store i8 %byte8, ptr %opp, align 1
  %i_next_pct = add i64 %i, 3
  %oi_next_pct = add i64 %oi, 1
  br label %loopcheck
`
	}

	e.emitGlobal(fmt.Sprintf(`
define ptr @%s(ptr %%str) {
entry:
  %%len = call i64 @strlen(ptr %%str)
  %%out = call ptr @__kml_str_alloc(i64 %%len)
  br label %%loopcheck

loopcheck:
  %%i = phi i64 [ 0, %%entry ], [ %%i_next_plain, %%plain ], [ %%i_next_pct, %%%s ]
  %%oi = phi i64 [ 0, %%entry ], [ %%oi_next_plain, %%plain ], [ %%oi_next_pct, %%%s ]
  %%cont = icmp slt i64 %%i, %%len
  br i1 %%cont, label %%loopbody, label %%done

loopbody:
  %%p = getelementptr i8, ptr %%str, i64 %%i
  %%ch = load i8, ptr %%p, align 1
  %%ispct = icmp eq i8 %%ch, 37
  br i1 %%ispct, label %%trypct, label %%plain

trypct:
  %%i1 = add i64 %%i, 1
  %%i2 = add i64 %%i, 2
  %%has1 = icmp slt i64 %%i1, %%len
  %%has2 = icmp slt i64 %%i2, %%len
  %%i1c = select i1 %%has1, i64 %%i1, i64 %%len
  %%i2c = select i1 %%has2, i64 %%i2, i64 %%len
  %%p1 = getelementptr i8, ptr %%str, i64 %%i1c
  %%p2 = getelementptr i8, ptr %%str, i64 %%i2c
  %%h1_8 = load i8, ptr %%p1, align 1
  %%h2_8 = load i8, ptr %%p2, align 1
  %%h1_64 = zext i8 %%h1_8 to i64
  %%h2_64 = zext i8 %%h2_8 to i64
  %%t1p = getelementptr [256 x i8], ptr @__kml_hex_decode_table, i64 0, i64 %%h1_64
  %%t2p = getelementptr [256 x i8], ptr @__kml_hex_decode_table, i64 0, i64 %%h2_64
  %%v1 = load i8, ptr %%t1p, align 1
  %%v2 = load i8, ptr %%t2p, align 1
  %%v1ok = icmp ne i8 %%v1, -1
  %%v2ok = icmp ne i8 %%v2, -1
  %%bothok0 = and i1 %%v1ok, %%v2ok
  %%bothok1 = and i1 %%bothok0, %%has1
  %%bothok = and i1 %%bothok1, %%has2
  br i1 %%bothok, label %%pctvalid, label %%%s

pctvalid:
  %%v1_32 = zext i8 %%v1 to i32
  %%v2_32 = zext i8 %%v2 to i32
  %%v1sh = shl i32 %%v1_32, 4
  %%byte32 = or i32 %%v1sh, %%v2_32
  %%byte8 = trunc i32 %%byte32 to i8
%s
plain:
  %%opp2 = getelementptr i8, ptr %%out, i64 %%oi
  store i8 %%ch, ptr %%opp2, align 1
  %%i_next_plain = add i64 %%i, 1
  %%oi_next_plain = add i64 %%oi, 1
  br label %%loopcheck
%s
done:
  %%termp = getelementptr i8, ptr %%out, i64 %%oi
  store i8 0, ptr %%termp, align 1
  %%hdrp = getelementptr i8, ptr %%out, i64 -8
  store i64 %%oi, ptr %%hdrp, align 8
  ret ptr %%out
}`, fnName, pctdoneLabel, pctdoneLabel, malformedTarget, reservedBlock, pcterrBlock))
}

func (e *Emitter) ensureDecodeURIComponent() {
	e.ensurePercentDecode(&e.usedDecodeURIComponent, "__kml_decode_uri_component", false)
}

func (e *Emitter) ensureDecodeURI() {
	e.ensurePercentDecode(&e.usedDecodeURI, "__kml_decode_uri", true)
}

// The strict variants back the global decodeURIComponent()/decodeURI()
// functions, which throw a URIError on a malformed escape (ADR-00556). The
// lenient variants above stay in use for HTTP query-string/URLSearchParams
// decoding, where a bad escape is tolerated rather than fatal.
func (e *Emitter) ensureDecodeURIComponentStrict() {
	e.ensurePercentDecodeVariant(&e.usedDecodeURIComponentStrict, "__kml_decode_uri_component_strict", false, true)
}

func (e *Emitter) ensureDecodeURIStrict() {
	e.ensurePercentDecodeVariant(&e.usedDecodeURIStrict, "__kml_decode_uri_strict", true, true)
}
