package llvm

import (
	"fmt"
	"strings"
)

// bigEndianLoad emits IR that loads nbytes consecutive i8s starting at
// ptrReg+baseOffset and combines them into a single big-endian integer of
// LLVM type ty ("i32" or "i64"), returning the register name holding the
// result. uniq must be unique among sibling calls within the same function
// text (register names are function-local, but this file defines several
// functions that each call this helper more than once). Shared by SHA-1's
// per-word block loading (4 bytes -> i32) and the frame codec's
// extended-length/mask-key fields (2/8 bytes -> i64, 4 bytes -> i32) — one
// implementation of "N big-endian bytes -> an integer", not four.
//
// Shift amounts are derived from the FIELD width (nbytes*8), not from ty's
// own bit width — those two only coincide when nbytes*8 == ty's width (true
// for every call site except the frame codec's 2-byte extended-length field,
// itself carried in an i64). Using ty's width there is a real, confirmed bug
// (found via this file's own known-answer test decoding a 2-byte length
// field as all-zero): it produces shifts of 56/48 instead of the correct
// 8/0, extracting the wrong bits entirely.
func bigEndianLoad(b *strings.Builder, uniq, ptrReg string, baseOffset, nbytes int, ty string) string {
	bits := nbytes * 8
	zregs := make([]string, nbytes)
	for i := 0; i < nbytes; i++ {
		pReg := fmt.Sprintf("%%%s_p%d", uniq, i)
		byReg := fmt.Sprintf("%%%s_b%d", uniq, i)
		zReg := fmt.Sprintf("%%%s_z%d", uniq, i)
		fmt.Fprintf(b, "  %s = getelementptr i8, ptr %s, i64 %d\n", pReg, ptrReg, baseOffset+i)
		fmt.Fprintf(b, "  %s = load i8, ptr %s, align 1\n", byReg, pReg)
		fmt.Fprintf(b, "  %s = zext i8 %s to %s\n", zReg, byReg, ty)
		zregs[i] = zReg
	}
	terms := make([]string, nbytes)
	for i := 0; i < nbytes-1; i++ {
		shift := bits - 8*(i+1)
		sReg := fmt.Sprintf("%%%s_s%d", uniq, i)
		fmt.Fprintf(b, "  %s = shl %s %s, %d\n", sReg, ty, zregs[i], shift)
		terms[i] = sReg
	}
	terms[nbytes-1] = zregs[nbytes-1]
	acc := terms[0]
	for i := 1; i < nbytes; i++ {
		oReg := fmt.Sprintf("%%%s_o%d", uniq, i)
		fmt.Fprintf(b, "  %s = or %s %s, %s\n", oReg, ty, acc, terms[i])
		acc = oReg
	}
	return acc
}

// bigEndianStore is bigEndianLoad's inverse: splits valReg (of LLVM type ty)
// into nbytes big-endian bytes and stores them at ptrReg+baseOffset. Shared
// by SHA-1's bit-length/digest-output writes and the frame codec's
// extended-length-field writes. Shift amounts derive from the field width
// (nbytes*8), not ty's own bit width — see bigEndianLoad's doc comment for
// why (the same bug, same fix, in the inverse direction).
func bigEndianStore(b *strings.Builder, uniq, ptrReg string, baseOffset, nbytes int, valReg, ty string) {
	bits := nbytes * 8
	for i := 0; i < nbytes; i++ {
		shift := bits - 8*(i+1)
		srcReg := valReg
		if shift != 0 {
			shReg := fmt.Sprintf("%%%s_sh%d", uniq, i)
			fmt.Fprintf(b, "  %s = lshr %s %s, %d\n", shReg, ty, valReg, shift)
			srcReg = shReg
		}
		tReg := fmt.Sprintf("%%%s_t%d", uniq, i)
		fmt.Fprintf(b, "  %s = trunc %s %s to i8\n", tReg, ty, srcReg)
		pReg := fmt.Sprintf("%%%s_p%d", uniq, i)
		fmt.Fprintf(b, "  %s = getelementptr i8, ptr %s, i64 %d\n", pReg, ptrReg, baseOffset+i)
		fmt.Fprintf(b, "  store i8 %s, ptr %s, align 1\n", tReg, pReg)
	}
}

// ensureWSFshl32 declares LLVM's funnel-shift-left intrinsic, used as a
// direct rotate-left(i32, i32) primitive — SHA-1's own rotate operations
// (its message-schedule extension and every compression round) — the same
// "declare a standard IR intrinsic directly" precedent ensureCtlz32
// (runtime_core.go) already established for Math.clz32.
func (e *Emitter) ensureWSFshl32() {
	if e.usedWSFshl32 {
		return
	}
	e.usedWSFshl32 = true
	e.emitGlobal("declare i32 @llvm.fshl.i32(i32, i32, i32)")
}

// ensureWSSHA1 declares __kml_ws_sha1(ptr data, i64 len, ptr out): a
// self-contained SHA-1 (FIPS 180-4) implementation writing the 20-byte
// digest into caller-owned out. TDD-00039 Stage 0 — needed for the
// WebSocket handshake's Sec-WebSocket-Accept (RFC 6455 §1.3:
// base64(SHA1(key + GUID))), and, per this file's own doc comment
// investigation, the first hash of any kind in this codebase
// (crypto.subtle.digest remains entirely unimplemented).
//
// Written as hand-rolled LLVM IR text, matching runtime_encoding.go's
// base64/hex helpers rather than introducing a new embedded-C mechanism
// (the only embedded-C precedent, gcsrc/gcshim.c, is specific to the GC
// allocator shim). The 16-word-per-block message-schedule extension and the
// 80-round compression are both fully unrolled at Go-generation time (each
// round/word index is compile-time constant), so the only genuine runtime
// loop is the outer per-64-byte-block iteration (a phi-based loop over
// nblocks, the same style ensureBase64Encode already uses) — this avoids
// needing any alloca-based mutable array for the message schedule or the
// running a/b/c/d/e registers, both threaded through purely as SSA register
// names computed by this Go function.
func (e *Emitter) ensureWSSHA1() {
	if e.usedWSSHA1 {
		return
	}
	e.usedWSSHA1 = true
	e.ensureMalloc()
	e.ensureFree()
	e.ensureMemset()
	e.ensureMemcpy()
	e.ensureWSFshl32()

	var b strings.Builder
	b.WriteString("define void @__kml_ws_sha1(ptr %data, i64 %len, ptr %out) {\n")
	b.WriteString("entry:\n")
	// Padding (FIPS 180-4 §5.1.1): append 0x80, zero-pad, then the original
	// bit-length as a big-endian 64-bit trailer, total a multiple of 64
	// bytes. paddedlen = ceil((len+9)/64)*64, computed via the standard
	// (len+1+8+63)/64*64 integer-division idiom.
	b.WriteString("  %t1 = add i64 %len, 1\n")
	b.WriteString("  %t2 = add i64 %t1, 8\n")
	b.WriteString("  %t3 = add i64 %t2, 63\n")
	b.WriteString("  %nblocks = udiv i64 %t3, 64\n")
	b.WriteString("  %paddedlen = mul i64 %nblocks, 64\n")
	b.WriteString("  %buf = call ptr @malloc(i64 %paddedlen)\n")
	b.WriteString("  call ptr @memset(ptr %buf, i32 0, i64 %paddedlen)\n")
	b.WriteString("  call ptr @memcpy(ptr %buf, ptr %data, i64 %len)\n")
	b.WriteString("  %tagpos = getelementptr i8, ptr %buf, i64 %len\n")
	b.WriteString("  store i8 128, ptr %tagpos, align 1\n")
	b.WriteString("  %bitlen = mul i64 %len, 8\n")
	b.WriteString("  %lenfieldoff = sub i64 %paddedlen, 8\n")
	b.WriteString("  %lenfieldptr = getelementptr i8, ptr %buf, i64 %lenfieldoff\n")
	bigEndianStore(&b, "lenfield", "%lenfieldptr", 0, 8, "%bitlen", "i64")

	b.WriteString("  br label %blockloop\n")
	b.WriteString("blockloop:\n")
	b.WriteString("  %i = phi i64 [ 0, %entry ], [ %inext, %blockend ]\n")
	b.WriteString("  %h0 = phi i32 [ 1732584193, %entry ], [ %h0n, %blockend ]\n")
	b.WriteString("  %h1 = phi i32 [ 4023233417, %entry ], [ %h1n, %blockend ]\n")
	b.WriteString("  %h2 = phi i32 [ 2562383102, %entry ], [ %h2n, %blockend ]\n")
	b.WriteString("  %h3 = phi i32 [ 271733878, %entry ], [ %h3n, %blockend ]\n")
	b.WriteString("  %h4 = phi i32 [ 3285377520, %entry ], [ %h4n, %blockend ]\n")
	b.WriteString("  %blockdone = icmp eq i64 %i, %nblocks\n")
	b.WriteString("  br i1 %blockdone, label %finish, label %body\n")
	b.WriteString("body:\n")
	b.WriteString("  %blockoff = mul i64 %i, 64\n")
	b.WriteString("  %blockptr = getelementptr i8, ptr %buf, i64 %blockoff\n")

	w := func(i int) string { return fmt.Sprintf("%%w%d", i) }

	// Message schedule words 0-15: loaded directly from the block, 4 bytes
	// each, big-endian (FIPS 180-4 §5.2.1/§6.1.2 step 1).
	for j := 0; j < 16; j++ {
		val := bigEndianLoad(&b, fmt.Sprintf("w%d", j), "%blockptr", j*4, 4, "i32")
		fmt.Fprintf(&b, "  %s = add i32 %s, 0\n", w(j), val) // name the result %wJ for uniform reference below
	}
	// Words 16-79: w[i] = rotl1(w[i-3] xor w[i-8] xor w[i-14] xor w[i-16]).
	for i := 16; i < 80; i++ {
		x1 := fmt.Sprintf("%%w%d_x1", i)
		x2 := fmt.Sprintf("%%w%d_x2", i)
		x3 := fmt.Sprintf("%%w%d_x3", i)
		fmt.Fprintf(&b, "  %s = xor i32 %s, %s\n", x1, w(i-3), w(i-8))
		fmt.Fprintf(&b, "  %s = xor i32 %s, %s\n", x2, x1, w(i-14))
		fmt.Fprintf(&b, "  %s = xor i32 %s, %s\n", x3, x2, w(i-16))
		fmt.Fprintf(&b, "  %s = call i32 @llvm.fshl.i32(i32 %s, i32 %s, i32 1)\n", w(i), x3, x3)
	}

	// 80-round compression (FIPS 180-4 §6.1.2 step 3). aReg/bReg/.../eReg
	// track the current (a,b,c,d,e) tuple as SSA register-name strings —
	// most of the tuple's next-round entries are just aliases to an
	// already-computed register (b_{i+1}=a_i, d_{i+1}=c_i, e_{i+1}=d_i), so
	// only a_{i+1} (temp) and c_{i+1} (rotl30(b_i)) ever need a fresh
	// instruction.
	aReg := make([]string, 81)
	bReg := make([]string, 81)
	cReg := make([]string, 81)
	dReg := make([]string, 81)
	eReg := make([]string, 81)
	aReg[0], bReg[0], cReg[0], dReg[0], eReg[0] = "%h0", "%h1", "%h2", "%h3", "%h4"
	for i := 0; i < 80; i++ {
		rot5 := fmt.Sprintf("%%rnd%d_rot5", i)
		fmt.Fprintf(&b, "  %s = call i32 @llvm.fshl.i32(i32 %s, i32 %s, i32 5)\n", rot5, aReg[i], aReg[i])

		fName := fmt.Sprintf("%%rnd%d_f", i)
		var k int64
		switch {
		case i < 20:
			notb := fmt.Sprintf("%%rnd%d_notb", i)
			bandc := fmt.Sprintf("%%rnd%d_bandc", i)
			notbandd := fmt.Sprintf("%%rnd%d_notbandd", i)
			fmt.Fprintf(&b, "  %s = xor i32 %s, -1\n", notb, bReg[i])
			fmt.Fprintf(&b, "  %s = and i32 %s, %s\n", bandc, bReg[i], cReg[i])
			fmt.Fprintf(&b, "  %s = and i32 %s, %s\n", notbandd, notb, dReg[i])
			fmt.Fprintf(&b, "  %s = or i32 %s, %s\n", fName, bandc, notbandd)
			k = 1518500249
		case i < 40, i >= 60:
			x1 := fmt.Sprintf("%%rnd%d_x1", i)
			fmt.Fprintf(&b, "  %s = xor i32 %s, %s\n", x1, bReg[i], cReg[i])
			fmt.Fprintf(&b, "  %s = xor i32 %s, %s\n", fName, x1, dReg[i])
			if i < 40 {
				k = 1859775393
			} else {
				k = 3395469782
			}
		default: // 40 <= i < 60
			bandc := fmt.Sprintf("%%rnd%d_bandc", i)
			bandd := fmt.Sprintf("%%rnd%d_bandd", i)
			candd := fmt.Sprintf("%%rnd%d_candd", i)
			or1 := fmt.Sprintf("%%rnd%d_or1", i)
			fmt.Fprintf(&b, "  %s = and i32 %s, %s\n", bandc, bReg[i], cReg[i])
			fmt.Fprintf(&b, "  %s = and i32 %s, %s\n", bandd, bReg[i], dReg[i])
			fmt.Fprintf(&b, "  %s = and i32 %s, %s\n", candd, cReg[i], dReg[i])
			fmt.Fprintf(&b, "  %s = or i32 %s, %s\n", or1, bandc, bandd)
			fmt.Fprintf(&b, "  %s = or i32 %s, %s\n", fName, or1, candd)
			k = 2400959708
		}

		t1 := fmt.Sprintf("%%rnd%d_t1", i)
		t2 := fmt.Sprintf("%%rnd%d_t2", i)
		t3 := fmt.Sprintf("%%rnd%d_t3", i)
		temp := fmt.Sprintf("%%rnd%d_temp", i)
		fmt.Fprintf(&b, "  %s = add i32 %s, %s\n", t1, rot5, fName)
		fmt.Fprintf(&b, "  %s = add i32 %s, %s\n", t2, t1, eReg[i])
		fmt.Fprintf(&b, "  %s = add i32 %s, %d\n", t3, t2, k)
		fmt.Fprintf(&b, "  %s = add i32 %s, %s\n", temp, t3, w(i))

		rot30 := fmt.Sprintf("%%rnd%d_rot30", i)
		fmt.Fprintf(&b, "  %s = call i32 @llvm.fshl.i32(i32 %s, i32 %s, i32 30)\n", rot30, bReg[i], bReg[i])

		aReg[i+1] = temp
		bReg[i+1] = aReg[i]
		cReg[i+1] = rot30
		dReg[i+1] = cReg[i]
		eReg[i+1] = dReg[i]
	}

	fmt.Fprintf(&b, "  %%h0n = add i32 %%h0, %s\n", aReg[80])
	fmt.Fprintf(&b, "  %%h1n = add i32 %%h1, %s\n", bReg[80])
	fmt.Fprintf(&b, "  %%h2n = add i32 %%h2, %s\n", cReg[80])
	fmt.Fprintf(&b, "  %%h3n = add i32 %%h3, %s\n", dReg[80])
	fmt.Fprintf(&b, "  %%h4n = add i32 %%h4, %s\n", eReg[80])
	b.WriteString("  br label %blockend\n")
	b.WriteString("blockend:\n")
	b.WriteString("  %inext = add i64 %i, 1\n")
	b.WriteString("  br label %blockloop\n")
	b.WriteString("finish:\n")
	bigEndianStore(&b, "out0", "%out", 0, 4, "%h0", "i32")
	bigEndianStore(&b, "out1", "%out", 4, 4, "%h1", "i32")
	bigEndianStore(&b, "out2", "%out", 8, 4, "%h2", "i32")
	bigEndianStore(&b, "out3", "%out", 12, 4, "%h3", "i32")
	bigEndianStore(&b, "out4", "%out", 16, 4, "%h4", "i32")
	b.WriteString("  call void @free(ptr %buf)\n")
	b.WriteString("  ret void\n")
	b.WriteString("}")

	e.emitGlobal(b.String())
}

// ensureWSMaskApply declares __kml_ws_mask_apply(ptr data, i64 len, i32
// maskkey): applies RFC 6455 §5.3's cyclic 4-byte XOR mask to data in place.
// XOR is its own inverse, so this single helper both masks (client-side
// frame encode) and unmasks (frame decode) — TDD-00039's Design explicitly
// calls for "one loop... not two copies", the whole reason this is its own
// function rather than inlined into encode/decode separately.
func (e *Emitter) ensureWSMaskApply() {
	if e.usedWSMaskApply {
		return
	}
	e.usedWSMaskApply = true
	e.emitGlobal(`
define void @__kml_ws_mask_apply(ptr %data, i64 %len, i32 %maskkey) {
entry:
  %mk0v = lshr i32 %maskkey, 24
  %mk0 = trunc i32 %mk0v to i8
  %mk1v = lshr i32 %maskkey, 16
  %mk1 = trunc i32 %mk1v to i8
  %mk2v = lshr i32 %maskkey, 8
  %mk2 = trunc i32 %mk2v to i8
  %mk3 = trunc i32 %maskkey to i8
  br label %loopcheck
loopcheck:
  %i = phi i64 [ 0, %entry ], [ %inext, %loopbody ]
  %cont = icmp slt i64 %i, %len
  br i1 %cont, label %loopbody, label %done
loopbody:
  %idxmod = and i64 %i, 3
  %ismod0 = icmp eq i64 %idxmod, 0
  %ismod1 = icmp eq i64 %idxmod, 1
  %ismod2 = icmp eq i64 %idxmod, 2
  %m01 = select i1 %ismod0, i8 %mk0, i8 %mk1
  %isle1 = or i1 %ismod0, %ismod1
  %m012 = select i1 %isle1, i8 %m01, i8 %mk2
  %isle2 = or i1 %isle1, %ismod2
  %mbyte = select i1 %isle2, i8 %m012, i8 %mk3
  %p = getelementptr i8, ptr %data, i64 %i
  %v = load i8, ptr %p, align 1
  %xv = xor i8 %v, %mbyte
  store i8 %xv, ptr %p, align 1
  %inext = add i64 %i, 1
  br label %loopcheck
done:
  ret void
}`)
}

// ensureWSFrameEncode declares __kml_ws_frame_encode(i32 opcode, i1 masked,
// ptr payload, i64 len, i32 maskkey) -> {ptr, i64}: builds one complete RFC
// 6455 frame (FIN always 1 — see TDD-00039's documented no-fragmentation
// scope cut) in a freshly malloc'd buffer, returned as {buf, totalLen}.
// maskkey is only meaningful when masked is true (server-side callers, per
// the TDD, always pass masked=false; a future client-side caller generates
// maskkey via the existing rand()-backed Math.random() machinery).
//
// The length-class branch (small/mid/large, RFC 6455 §5.2) runs twice: once
// before the malloc to size the header, once after (to actually write the
// extended-length bytes, which can't happen until %buf exists). The second
// branch deliberately re-derives its condition from %extbytes (the phi
// defined at the merge point itself) rather than reusing %small/%mid from
// the first branch — reusing them is a real SSA-dominance bug: there is a
// graph-structural path (entry -> lenSmall -> lenmerge -> a hypothetical
// second use of %mid) that never passes through %mid's defining block
// (checkMid) at all, even though that path can never actually be taken at
// runtime (found the hard way, as a segfault from a garbage value along
// exactly that structurally-valid-but-dynamically-impossible path, in this
// file's own known-answer test). %extbytes has no such problem: it's a phi
// defined in lenmerge itself, so it trivially dominates every block
// downstream of lenmerge.
func (e *Emitter) ensureWSFrameEncode() {
	if e.usedWSFrameEncode {
		return
	}
	e.usedWSFrameEncode = true
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureWSMaskApply()

	var b strings.Builder
	b.WriteString("define { ptr, i64 } @__kml_ws_frame_encode(i32 %opcode, i1 %masked, ptr %payload, i64 %len, i32 %maskkey) {\n")
	b.WriteString("entry:\n")
	b.WriteString("  %small = icmp ule i64 %len, 125\n")
	b.WriteString("  br i1 %small, label %lenSmall, label %checkMid\n")
	b.WriteString("checkMid:\n")
	b.WriteString("  %mid = icmp ule i64 %len, 65535\n")
	b.WriteString("  br i1 %mid, label %lenMid, label %lenLarge\n")
	b.WriteString("lenSmall:\n")
	b.WriteString("  %lenbyte_s = trunc i64 %len to i8\n")
	b.WriteString("  br label %lenmerge\n")
	b.WriteString("lenMid:\n")
	b.WriteString("  br label %lenmerge\n")
	b.WriteString("lenLarge:\n")
	b.WriteString("  br label %lenmerge\n")
	b.WriteString("lenmerge:\n")
	b.WriteString("  %extbytes = phi i64 [ 0, %lenSmall ], [ 2, %lenMid ], [ 8, %lenLarge ]\n")
	b.WriteString("  %lenbyte = phi i8 [ %lenbyte_s, %lenSmall ], [ 126, %lenMid ], [ 127, %lenLarge ]\n")
	b.WriteString("  %maskbytes = select i1 %masked, i64 4, i64 0\n")
	b.WriteString("  %hdrlen0 = add i64 2, %extbytes\n")
	b.WriteString("  %hdrlen = add i64 %hdrlen0, %maskbytes\n")
	b.WriteString("  %totallen = add i64 %hdrlen, %len\n")
	b.WriteString("  %buf = call ptr @malloc(i64 %totallen)\n")
	b.WriteString("  %opcodeb = trunc i32 %opcode to i8\n")
	b.WriteString("  %byte0 = or i8 %opcodeb, -128\n")
	b.WriteString("  %b0p = getelementptr i8, ptr %buf, i64 0\n")
	b.WriteString("  store i8 %byte0, ptr %b0p, align 1\n")
	b.WriteString("  %maskflagbyte = select i1 %masked, i8 -128, i8 0\n")
	b.WriteString("  %byte1 = or i8 %lenbyte, %maskflagbyte\n")
	b.WriteString("  %b1p = getelementptr i8, ptr %buf, i64 1\n")
	b.WriteString("  store i8 %byte1, ptr %b1p, align 1\n")
	b.WriteString("  %hdrIsSmall = icmp eq i64 %extbytes, 0\n")
	b.WriteString("  br i1 %hdrIsSmall, label %afterext, label %checkMid2\n")
	b.WriteString("checkMid2:\n")
	b.WriteString("  %hdrIsMid = icmp eq i64 %extbytes, 2\n")
	b.WriteString("  br i1 %hdrIsMid, label %writeMid, label %writeLarge\n")
	b.WriteString("writeMid:\n")
	bigEndianStore(&b, "extmid", "%buf", 2, 2, "%len", "i64")
	b.WriteString("  br label %afterext\n")
	b.WriteString("writeLarge:\n")
	bigEndianStore(&b, "extlarge", "%buf", 2, 8, "%len", "i64")
	b.WriteString("  br label %afterext\n")
	b.WriteString("afterext:\n")
	b.WriteString("  br i1 %masked, label %writeMask, label %noMask\n")
	b.WriteString("writeMask:\n")
	b.WriteString("  %maskoff = add i64 2, %extbytes\n")
	b.WriteString("  %maskptr = getelementptr i8, ptr %buf, i64 %maskoff\n")
	bigEndianStore(&b, "maskw", "%maskptr", 0, 4, "%maskkey", "i32")
	b.WriteString("  br label %copy\n")
	b.WriteString("noMask:\n")
	b.WriteString("  br label %copy\n")
	b.WriteString("copy:\n")
	b.WriteString("  %payloadptr = getelementptr i8, ptr %buf, i64 %hdrlen\n")
	b.WriteString("  call ptr @memcpy(ptr %payloadptr, ptr %payload, i64 %len)\n")
	b.WriteString("  br i1 %masked, label %domask, label %done\n")
	b.WriteString("domask:\n")
	b.WriteString("  call void @__kml_ws_mask_apply(ptr %payloadptr, i64 %len, i32 %maskkey)\n")
	b.WriteString("  br label %done\n")
	b.WriteString("done:\n")
	b.WriteString("  %result0 = insertvalue { ptr, i64 } undef, ptr %buf, 0\n")
	b.WriteString("  %result1 = insertvalue { ptr, i64 } %result0, i64 %totallen, 1\n")
	b.WriteString("  ret { ptr, i64 } %result1\n")
	b.WriteString("}")

	e.emitGlobal(b.String())
}

// ensureWSFrameDecode declares __kml_ws_frame_decode(ptr buf, i64 avail) ->
// { i32 status, i32 opcode, ptr payload, i64 payloadLen, i64 consumed }:
// parses one frame from the head of an accumulator buffer (the same
// "reload the pointer/length fresh every call, never cache across a
// suspend point" convention __kml_eventsource_process_available already
// established, TDD-00038/ADR-00123 — buf/avail are always passed fresh by
// the caller, this function itself holds nothing across calls).
//
// status: 0 = incomplete (not enough bytes buffered yet for a full frame —
// every other field is zero/null; the caller's scan just tries again once
// more bytes arrive, exactly EventSource's own "not \n\n-terminated yet,
// leave for next iteration" handling), 1 = ok, 2 = protocol error (FIN=0 —
// TDD-00039's documented no-fragmentation cut — or a nonzero RSV bit) but
// still fully parsed (opcode/payload/consumed all valid), so a caller can
// still advance past it and close the connection cleanly rather than
// spinning on an unparseable frame forever. payload is a freshly malloc'd
// copy (already unmasked if the frame was masked) the caller owns.
func (e *Emitter) ensureWSFrameDecode() {
	if e.usedWSFrameDecode {
		return
	}
	e.usedWSFrameDecode = true
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureWSMaskApply()

	retTy := "{ i32, i32, ptr, i64, i64 }"
	var b strings.Builder
	fmt.Fprintf(&b, "define %s @__kml_ws_frame_decode(ptr %%buf, i64 %%avail) {\n", retTy)
	b.WriteString("entry:\n")
	b.WriteString("  %havehdr = icmp uge i64 %avail, 2\n")
	b.WriteString("  br i1 %havehdr, label %haveHeader, label %incomplete\n")
	b.WriteString("haveHeader:\n")
	b.WriteString("  %b0p = getelementptr i8, ptr %buf, i64 0\n")
	b.WriteString("  %b0 = load i8, ptr %b0p, align 1\n")
	b.WriteString("  %b1p = getelementptr i8, ptr %buf, i64 1\n")
	b.WriteString("  %b1 = load i8, ptr %b1p, align 1\n")
	b.WriteString("  %finbit = and i8 %b0, -128\n")
	b.WriteString("  %finzero = icmp eq i8 %finbit, 0\n")
	b.WriteString("  %rsvbits = and i8 %b0, 112\n")
	b.WriteString("  %rsvbad = icmp ne i8 %rsvbits, 0\n")
	b.WriteString("  %badframe = or i1 %finzero, %rsvbad\n")
	b.WriteString("  %opcodebyte = and i8 %b0, 15\n")
	b.WriteString("  %opcode32 = zext i8 %opcodebyte to i32\n")
	b.WriteString("  %maskbit = and i8 %b1, -128\n")
	b.WriteString("  %masked = icmp ne i8 %maskbit, 0\n")
	b.WriteString("  %lenfieldbyte = and i8 %b1, 127\n")
	b.WriteString("  %lenfield64 = zext i8 %lenfieldbyte to i64\n")
	b.WriteString("  %issmall = icmp ult i64 %lenfield64, 126\n")
	b.WriteString("  br i1 %issmall, label %small, label %checkmid\n")
	b.WriteString("checkmid:\n")
	b.WriteString("  %ismid = icmp eq i64 %lenfield64, 126\n")
	b.WriteString("  br i1 %ismid, label %midcheck, label %largecheck\n")
	b.WriteString("small:\n")
	b.WriteString("  br label %lenmerge\n")
	b.WriteString("midcheck:\n")
	b.WriteString("  %midneed = icmp uge i64 %avail, 4\n")
	b.WriteString("  br i1 %midneed, label %midread, label %incomplete\n")
	b.WriteString("midread:\n")
	midlen := bigEndianLoad(&b, "midlen", "%buf", 2, 2, "i64")
	fmt.Fprintf(&b, "  %%midlen = add i64 %s, 0\n", midlen)
	b.WriteString("  br label %lenmerge\n")
	b.WriteString("largecheck:\n")
	b.WriteString("  %largeneed = icmp uge i64 %avail, 10\n")
	b.WriteString("  br i1 %largeneed, label %largeread, label %incomplete\n")
	b.WriteString("largeread:\n")
	largelen := bigEndianLoad(&b, "largelen", "%buf", 2, 8, "i64")
	fmt.Fprintf(&b, "  %%largelen = add i64 %s, 0\n", largelen)
	b.WriteString("  br label %lenmerge\n")
	b.WriteString("lenmerge:\n")
	b.WriteString("  %plen = phi i64 [ %lenfield64, %small ], [ %midlen, %midread ], [ %largelen, %largeread ]\n")
	b.WriteString("  %hdrbase = phi i64 [ 2, %small ], [ 4, %midread ], [ 10, %largeread ]\n")
	b.WriteString("  br i1 %masked, label %maskcheck, label %nomaskhdr\n")
	b.WriteString("maskcheck:\n")
	b.WriteString("  %maskneed = add i64 %hdrbase, 4\n")
	b.WriteString("  %havemaskbytes = icmp uge i64 %avail, %maskneed\n")
	b.WriteString("  br i1 %havemaskbytes, label %readmask, label %incomplete\n")
	b.WriteString("readmask:\n")
	b.WriteString("  %maskkeyptr = getelementptr i8, ptr %buf, i64 %hdrbase\n")
	maskkey := bigEndianLoad(&b, "maskkey", "%maskkeyptr", 0, 4, "i32")
	fmt.Fprintf(&b, "  %%maskkey_r = add i32 %s, 0\n", maskkey)
	b.WriteString("  %payloadstart_m = add i64 %hdrbase, 4\n")
	b.WriteString("  br label %afterhdr\n")
	b.WriteString("nomaskhdr:\n")
	b.WriteString("  br label %afterhdr\n")
	b.WriteString("afterhdr:\n")
	b.WriteString("  %payloadstart = phi i64 [ %payloadstart_m, %readmask ], [ %hdrbase, %nomaskhdr ]\n")
	// %maskkey is defined only on the %readmask path; phi it here (0 on the
	// unmasked path, never consumed) so it dominates its use in %dounmask.
	b.WriteString("  %maskkey = phi i32 [ %maskkey_r, %readmask ], [ 0, %nomaskhdr ]\n")
	b.WriteString("  %needtotal = add i64 %payloadstart, %plen\n")
	b.WriteString("  %havefull = icmp uge i64 %avail, %needtotal\n")
	b.WriteString("  br i1 %havefull, label %havefullframe, label %incomplete\n")
	b.WriteString("havefullframe:\n")
	b.WriteString("  %payloadsrc = getelementptr i8, ptr %buf, i64 %payloadstart\n")
	b.WriteString("  %outbuf = call ptr @malloc(i64 %plen)\n")
	b.WriteString("  call ptr @memcpy(ptr %outbuf, ptr %payloadsrc, i64 %plen)\n")
	b.WriteString("  br i1 %masked, label %dounmask, label %afterunmask\n")
	b.WriteString("dounmask:\n")
	b.WriteString("  call void @__kml_ws_mask_apply(ptr %outbuf, i64 %plen, i32 %maskkey)\n")
	b.WriteString("  br label %afterunmask\n")
	b.WriteString("afterunmask:\n")
	b.WriteString("  %statusval = select i1 %badframe, i32 2, i32 1\n")
	fmt.Fprintf(&b, "  %%r0 = insertvalue %s undef, i32 %%statusval, 0\n", retTy)
	fmt.Fprintf(&b, "  %%r1 = insertvalue %s %%r0, i32 %%opcode32, 1\n", retTy)
	fmt.Fprintf(&b, "  %%r2 = insertvalue %s %%r1, ptr %%outbuf, 2\n", retTy)
	fmt.Fprintf(&b, "  %%r3 = insertvalue %s %%r2, i64 %%plen, 3\n", retTy)
	fmt.Fprintf(&b, "  %%r4 = insertvalue %s %%r3, i64 %%needtotal, 4\n", retTy)
	fmt.Fprintf(&b, "  ret %s %%r4\n", retTy)
	b.WriteString("incomplete:\n")
	fmt.Fprintf(&b, "  %%ri0 = insertvalue %s undef, i32 0, 0\n", retTy)
	fmt.Fprintf(&b, "  %%ri1 = insertvalue %s %%ri0, i32 0, 1\n", retTy)
	fmt.Fprintf(&b, "  %%ri2 = insertvalue %s %%ri1, ptr null, 2\n", retTy)
	fmt.Fprintf(&b, "  %%ri3 = insertvalue %s %%ri2, i64 0, 3\n", retTy)
	fmt.Fprintf(&b, "  %%ri4 = insertvalue %s %%ri3, i64 0, 4\n", retTy)
	fmt.Fprintf(&b, "  ret %s %%ri4\n", retTy)
	b.WriteString("}")

	e.emitGlobal(b.String())
}
