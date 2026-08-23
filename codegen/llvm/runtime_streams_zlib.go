// runtime_streams_zlib.go — TDD-00097 Stage 6: CompressionStream /
// DecompressionStream. Each is an ordinary %kml.ts TransformStream context
// (so .readable/.writable/pipeThrough ride the Stage 3 machinery unchanged)
// whose transform/flush closures are these native functions over a zlib
// z_stream handle. `-lz` is linked only when one is actually constructed —
// the same used-only discipline libcurl has.
//
// %kml.zctx (32 B): 0 z_stream* · 1 readable rstream · 2 mode (0 deflate ·
// 1 inflate) · 3 finished flag.
//
// z_stream is treated as an opaque 112-byte calloc (size verified against
// zlib.h on this machine, and re-verified at runtime by passing
// sizeof(z_stream) to the Init_ entry points, which reject a mismatch);
// the four fields the streaming loop needs are poked at their 64-bit ABI
// offsets: next_in +0, avail_in +8 (i32), next_out +24, avail_out +32 (i32).
package llvm

import "fmt"

const zctxStructIR = "{ ptr, ptr, i64, i64 }"

// ensureZlibExterns declares the libz C entry points exactly once, shared by
// both the streaming CompressionStream runtime and the one-shot Node `zlib`
// module helper below. `-lz` is linked (via requireLink) by whichever caller
// actually needs it; declaring the externs is idempotent through this flag.
func (e *Emitter) ensureZlibExterns() {
	if e.usedZlibExterns {
		return
	}
	e.usedZlibExterns = true
	e.emitGlobal("declare ptr @zlibVersion()")
	e.emitGlobal("declare i32 @deflateInit2_(ptr noundef, i32 noundef, i32 noundef, i32 noundef, i32 noundef, i32 noundef, ptr noundef, i32 noundef)")
	e.emitGlobal("declare i32 @inflateInit2_(ptr noundef, i32 noundef, ptr noundef, i32 noundef)")
	e.emitGlobal("declare i32 @deflate(ptr noundef, i32 noundef)")
	e.emitGlobal("declare i32 @inflate(ptr noundef, i32 noundef)")
	e.emitGlobal("declare i32 @deflateEnd(ptr noundef)")
	e.emitGlobal("declare i32 @inflateEnd(ptr noundef)")
}

// ensureZlibOneshot installs @__kml_zlib_oneshot — the whole-buffer codec the
// Node `zlib` module's gzip/deflate/inflate/gunzip/unzip family (both the
// *Sync and callback forms) is built on. It reuses the same libz externs and
// z_stream ABI offsets as the streaming runtime above, but drives a single
// Z_FINISH pass over an in-memory input into a growable output buffer instead
// of the TransformStream pump.
//
//	mode:  0 = deflate/compress · 1 = inflate/decompress
//	wbits: 31 gzip · 15 zlib · -15 raw (compress) / 47 auto (gunzip/unzip)
//	level: deflate level (0-9, or -1 for zlib's default); ignored for inflate
//
// Returns { ptr, i64 } — the exact SSA aggregate a first-class TypedArray
// value uses, so the caller wraps it as a Buffer with no repacking (same shape
// fs.readFileSyncBytes returns). On any zlib error the length field is -1 and
// the pointer null, which the caller turns into a throw (sync) or an Error
// callback argument (callback form).
func (e *Emitter) ensureZlibOneshot() {
	if e.usedZlibOneshot {
		return
	}
	e.usedZlibOneshot = true
	e.requireLink("z")
	e.ensureMalloc()
	e.ensureCalloc()
	e.ensureRealloc()
	e.ensureFree()
	e.ensureZlibExterns()

	e.emitGlobal(`
define { ptr, i64 } @__kml_zlib_oneshot(ptr %data, i64 %len, i64 %mode, i64 %wbits, i64 %level) {
entry:
  %strm = call ptr @calloc(i64 1, i64 112)
  %ver = call ptr @zlibVersion()
  %wb32 = trunc i64 %wbits to i32
  %lvl32 = trunc i64 %level to i32
  %isInf = icmp ne i64 %mode, 0
  br i1 %isInf, label %doinf, label %dodef
dodef:
  %rc1 = call i32 @deflateInit2_(ptr %strm, i32 %lvl32, i32 8, i32 %wb32, i32 8, i32 0, ptr %ver, i32 112)
  br label %ckinit
doinf:
  %rc2 = call i32 @inflateInit2_(ptr %strm, i32 %wb32, ptr %ver, i32 112)
  br label %ckinit
ckinit:
  %rc0 = phi i32 [ %rc1, %dodef ], [ %rc2, %doinf ]
  %initbad = icmp ne i32 %rc0, 0
  br i1 %initbad, label %failinit, label %setup
failinit:
  call void @free(ptr %strm)
  %fr0 = insertvalue { ptr, i64 } undef, ptr null, 0
  %fr1 = insertvalue { ptr, i64 } %fr0, i64 -1, 1
  ret { ptr, i64 } %fr1
setup:
  %ni = getelementptr i8, ptr %strm, i64 0
  store ptr %data, ptr %ni, align 8
  %ai = getelementptr i8, ptr %strm, i64 8
  %len32 = trunc i64 %len to i32
  store i32 %len32, ptr %ai, align 4
  %half = lshr i64 %len, 1
  %capa = add i64 %len, %half
  %cap0 = add i64 %capa, 64
  %out0 = call ptr @malloc(i64 %cap0)
  %outp = alloca ptr, align 8
  %capp = alloca i64, align 8
  %totp = alloca i64, align 8
  store ptr %out0, ptr %outp, align 8
  store i64 %cap0, ptr %capp, align 8
  store i64 0, ptr %totp, align 8
  br label %loop
loop:
  %cap = load i64, ptr %capp, align 8
  %tot = load i64, ptr %totp, align 8
  %space = sub i64 %cap, %tot
  %noSpace = icmp eq i64 %space, 0
  br i1 %noSpace, label %grow, label %pump
grow:
  %capOld = load i64, ptr %capp, align 8
  %newcap = mul i64 %capOld, 2
  %outOld = load ptr, ptr %outp, align 8
  %outNew = call ptr @realloc(ptr %outOld, i64 %newcap)
  store ptr %outNew, ptr %outp, align 8
  store i64 %newcap, ptr %capp, align 8
  br label %loop
pump:
  %outc = load ptr, ptr %outp, align 8
  %totc = load i64, ptr %totp, align 8
  %nop = getelementptr i8, ptr %strm, i64 24
  %outAt = getelementptr i8, ptr %outc, i64 %totc
  store ptr %outAt, ptr %nop, align 8
  %big = icmp ugt i64 %space, 1073741824
  %chunk = select i1 %big, i64 1073741824, i64 %space
  %chunk32 = trunc i64 %chunk to i32
  %aop = getelementptr i8, ptr %strm, i64 32
  store i32 %chunk32, ptr %aop, align 4
  br i1 %isInf, label %pumpinf, label %pumpdef
pumpdef:
  %prc1 = call i32 @deflate(ptr %strm, i32 4)
  br label %pumped
pumpinf:
  %prc2 = call i32 @inflate(ptr %strm, i32 4)
  br label %pumped
pumped:
  %prc = phi i32 [ %prc1, %pumpdef ], [ %prc2, %pumpinf ]
  %aoLeft = load i32, ptr %aop, align 4
  %aoLeft64 = zext i32 %aoLeft to i64
  %produced = sub i64 %chunk, %aoLeft64
  %totNew = add i64 %totc, %produced
  store i64 %totNew, ptr %totp, align 8
  %isEnd = icmp eq i32 %prc, 1
  br i1 %isEnd, label %done, label %ckcont
ckcont:
  %isNeg = icmp slt i32 %prc, 0
  br i1 %isNeg, label %ckbuf, label %ckfull
ckbuf:
  %isBuf = icmp eq i32 %prc, -5
  br i1 %isBuf, label %ckfull, label %fail
ckfull:
  %filled = icmp eq i32 %aoLeft, 0
  br i1 %filled, label %loop, label %fail
fail:
  %outf = load ptr, ptr %outp, align 8
  call void @free(ptr %outf)
  br i1 %isInf, label %failinf, label %faildef
faildef:
  %fe1 = call i32 @deflateEnd(ptr %strm)
  br label %failend
failinf:
  %fe2 = call i32 @inflateEnd(ptr %strm)
  br label %failend
failend:
  call void @free(ptr %strm)
  %ff0 = insertvalue { ptr, i64 } undef, ptr null, 0
  %ff1 = insertvalue { ptr, i64 } %ff0, i64 -1, 1
  ret { ptr, i64 } %ff1
done:
  br i1 %isInf, label %doneinf, label %donedef
donedef:
  %de1 = call i32 @deflateEnd(ptr %strm)
  br label %doneend
doneinf:
  %de2 = call i32 @inflateEnd(ptr %strm)
  br label %doneend
doneend:
  call void @free(ptr %strm)
  %outr = load ptr, ptr %outp, align 8
  %totr = load i64, ptr %totp, align 8
  %r0 = insertvalue { ptr, i64 } undef, ptr %outr, 0
  %r1 = insertvalue { ptr, i64 } %r0, i64 %totr, 1
  ret { ptr, i64 } %r1
}`)
}

func (e *Emitter) ensureZlibStreamRuntime() {
	if e.usedZlibStreamRuntime {
		return
	}
	e.usedZlibStreamRuntime = true
	e.requireLink("z")
	e.ensureStreamRuntime()
	e.ensureStreamPipeRuntime() // the %kml.ts sink/pull machinery
	e.ensureMalloc()
	e.ensureCalloc()
	e.ensureExceptionHelpers()
	e.ensureZlibExterns()

	zc := zctxStructIR
	zlibErrMsg := e.internString("zlib stream error")
	errName := e.internString("Error")

	// __kml_zs_init(mode, windowBits) -> zctx (readable patched in by the
	// construction site). Returns null when zlib rejects the init.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_zs_init(i64 %%mode, i64 %%wbits) {
entry:
  %%strm = call ptr @calloc(i64 1, i64 112)
  %%ver = call ptr @zlibVersion()
  %%wb32 = trunc i64 %%wbits to i32
  %%isInf = icmp ne i64 %%mode, 0
  br i1 %%isInf, label %%doinf, label %%dodef
dodef:
  %%rc1 = call i32 @deflateInit2_(ptr %%strm, i32 6, i32 8, i32 %%wb32, i32 8, i32 0, ptr %%ver, i32 112)
  br label %%ck
doinf:
  %%rc2 = call i32 @inflateInit2_(ptr %%strm, i32 %%wb32, ptr %%ver, i32 112)
  br label %%ck
ck:
  %%rc = phi i32 [ %%rc1, %%dodef ], [ %%rc2, %%doinf ]
  %%bad = icmp ne i32 %%rc, 0
  br i1 %%bad, label %%fail, label %%ok
fail:
  ret ptr null
ok:
  %%ctx = call ptr @malloc(i64 32)
  %%f0 = getelementptr %s, ptr %%ctx, i32 0, i32 0
  store ptr %%strm, ptr %%f0, align 8
  %%f1 = getelementptr %s, ptr %%ctx, i32 0, i32 1
  store ptr null, ptr %%f1, align 8
  %%f2 = getelementptr %s, ptr %%ctx, i32 0, i32 2
  store i64 %%mode, ptr %%f2, align 8
  %%f3 = getelementptr %s, ptr %%ctx, i32 0, i32 3
  store i64 0, ptr %%f3, align 8
  ret ptr %%ctx
}`, zc, zc, zc, zc))

	// __kml_zs_pump(zctx, flushFlag): run deflate/inflate over the current
	// input until it is consumed, enqueuing every produced output block.
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_zs_pump(ptr %%ctx, i32 %%flush) {
entry:
  %%f0 = getelementptr %s, ptr %%ctx, i32 0, i32 0
  %%strm = load ptr, ptr %%f0, align 8
  %%f1 = getelementptr %s, ptr %%ctx, i32 0, i32 1
  %%rs = load ptr, ptr %%f1, align 8
  %%f2 = getelementptr %s, ptr %%ctx, i32 0, i32 2
  %%mode = load i64, ptr %%f2, align 8
  %%isInf = icmp ne i64 %%mode, 0
  br label %%loop
loop:
  %%out = call ptr @malloc(i64 65536)
  %%no_p = getelementptr i8, ptr %%strm, i64 24
  store ptr %%out, ptr %%no_p, align 8
  %%ao_p = getelementptr i8, ptr %%strm, i64 32
  store i32 65536, ptr %%ao_p, align 4
  br i1 %%isInf, label %%doinf, label %%dodef
dodef:
  %%rc1 = call i32 @deflate(ptr %%strm, i32 %%flush)
  br label %%after
doinf:
  %%rc2 = call i32 @inflate(ptr %%strm, i32 0)
  br label %%after
after:
  %%rc = phi i32 [ %%rc1, %%dodef ], [ %%rc2, %%doinf ]
  %%ao = load i32, ptr %%ao_p, align 4
  %%ao64 = zext i32 %%ao to i64
  %%produced = sub i64 65536, %%ao64
  %%hasOut = icmp sgt i64 %%produced, 0
  br i1 %%hasOut, label %%enq, label %%ckrc
enq:
  %%bits = ptrtoint ptr %%out to i64
  %%ign = call i64 @__kml_rs_enqueue(ptr %%rs, i64 %%bits, i64 %%produced)
  br label %%ckrc
ckrc:
  %%isEnd = icmp eq i32 %%rc, 1
  br i1 %%isEnd, label %%streamend, label %%ckerr
streamend:
  %%f3 = getelementptr %s, ptr %%ctx, i32 0, i32 3
  store i64 1, ptr %%f3, align 8
  ret void
ckerr:
  ; Z_BUF_ERROR (-5) just means "no progress possible right now" — stop
  ; without erroring; any other negative code errors the readable side.
  %%isNeg = icmp slt i32 %%rc, 0
  br i1 %%isNeg, label %%cknoprog, label %%cont
cknoprog:
  %%isBuf = icmp eq i32 %%rc, -5
  br i1 %%isBuf, label %%ret, label %%err
err:
  %%eo = call ptr @malloc(i64 24)
  %%eo_kind = getelementptr { i64, ptr, ptr }, ptr %%eo, i32 0, i32 0
  store i64 0, ptr %%eo_kind, align 8
  %%eo_msg = getelementptr { i64, ptr, ptr }, ptr %%eo, i32 0, i32 1
  store ptr %s, ptr %%eo_msg, align 8
  %%eo_name = getelementptr { i64, ptr, ptr }, ptr %%eo, i32 0, i32 2
  store ptr %s, ptr %%eo_name, align 8
  %%ebits = ptrtoint ptr %%eo to i64
  call void @__kml_rs_error(ptr %%rs, i64 %%ebits)
  ret void
cont:
  ; Keep looping while input remains or the output block filled up
  ; (avail_out == 0 means there may be more to produce).
  %%ai_p = getelementptr i8, ptr %%strm, i64 8
  %%ai = load i32, ptr %%ai_p, align 4
  %%moreIn = icmp ne i32 %%ai, 0
  %%outFull = icmp eq i32 %%ao, 0
  %%more = or i1 %%moreIn, %%outFull
  br i1 %%more, label %%loop, label %%ret
ret:
  ret void
}`, zc, zc, zc, zc, zlibErrMsg, errName))

	// The %kml.ts-compatible transform / flush closures.
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_zs_transform(ptr %%ctx, i64 %%v0, i64 %%v1) {
entry:
  %%f3 = getelementptr %s, ptr %%ctx, i32 0, i32 3
  %%fin = load i64, ptr %%f3, align 8
  %%done = icmp ne i64 %%fin, 0
  br i1 %%done, label %%ret, label %%go
go:
  %%f0 = getelementptr %s, ptr %%ctx, i32 0, i32 0
  %%strm = load ptr, ptr %%f0, align 8
  %%in = inttoptr i64 %%v0 to ptr
  %%ni_p = getelementptr i8, ptr %%strm, i64 0
  store ptr %%in, ptr %%ni_p, align 8
  %%ai_p = getelementptr i8, ptr %%strm, i64 8
  %%len32 = trunc i64 %%v1 to i32
  store i32 %%len32, ptr %%ai_p, align 4
  call void @__kml_zs_pump(ptr %%ctx, i32 0)
  ret ptr null
ret:
  ret ptr null
}

define ptr @__kml_zs_flush(ptr %%ctx) {
entry:
  %%f0 = getelementptr %s, ptr %%ctx, i32 0, i32 0
  %%strm = load ptr, ptr %%f0, align 8
  %%ni_p = getelementptr i8, ptr %%strm, i64 0
  store ptr null, ptr %%ni_p, align 8
  %%ai_p = getelementptr i8, ptr %%strm, i64 8
  store i32 0, ptr %%ai_p, align 4
  %%f2 = getelementptr %s, ptr %%ctx, i32 0, i32 2
  %%mode = load i64, ptr %%f2, align 8
  %%isInf = icmp ne i64 %%mode, 0
  br i1 %%isInf, label %%endinf, label %%finishdef
finishdef:
  ; Z_FINISH drains the deflate state; __kml_zs_pump loops until Z_STREAM_END.
  call void @__kml_zs_pump(ptr %%ctx, i32 4)
  %%ign1 = call i32 @deflateEnd(ptr %%strm)
  ret ptr null
endinf:
  %%ign2 = call i32 @inflateEnd(ptr %%strm)
  ret ptr null
}`, zc, zc, zc, zc))
}
