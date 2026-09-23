package llvm

import (
	"fmt"
)

func (e *Emitter) ensureJSONStringifyNum() {
	if e.usedJSONStringifyNum {
		return
	}
	e.usedJSONStringifyNum = true
	e.ensureMalloc()
	e.ensureSprintf()
	e.ensureStrHeaderRuntime()
	fmtName := e.internString("%lld")
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_json_str_num(i64 %%n) {
entry:
  %%buf = call ptr @__kml_str_alloc(i64 32)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%buf, ptr %s, i64 %%n)
  call void @__kml_str_finalize(ptr %%buf)
  ret ptr %%buf
}`, fmtName))
}

// ensureJSONConcat2 declares __kml_json_concat2: concatenate two header
// strings into a fresh one, conditionally freeing each input. The stringify
// accumulator loops use it so intermediate accumulators and element
// fragments are reclaimed as they go, in every memory mode — previously
// each append leaked its inputs, quadratically (found by the benchmark
// campaign's json_churn: ~180MB of transient garbage per round).
func (e *Emitter) ensureJSONConcat2() {
	if e.usedJSONConcat2 {
		return
	}
	e.usedJSONConcat2 = true
	e.ensureMemcpy()
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`
define ptr @__kml_json_concat2(ptr %a, ptr %b, i1 %fa, i1 %fb) {
entry:
  %la = call i64 @__kml_str_len(ptr %a)
  %lb = call i64 @__kml_str_len(ptr %b)
  %n = add i64 %la, %lb
  %dst = call ptr @__kml_str_alloc(i64 %n)
  call ptr @memcpy(ptr %dst, ptr %a, i64 %la)
  %mid = getelementptr i8, ptr %dst, i64 %la
  %lb1 = add i64 %lb, 1
  call ptr @memcpy(ptr %mid, ptr %b, i64 %lb1)
  br i1 %fa, label %freea, label %chkb
freea:
  call void @__kml_str_free(ptr %a)
  br label %chkb
chkb:
  br i1 %fb, label %freeb, label %done
freeb:
  call void @__kml_str_free(ptr %b)
  br label %done
done:
  ret ptr %dst
}`)
}

func (e *Emitter) ensureJSONStringifyStr() {
	if e.usedJSONStringifyStr {
		return
	}
	e.usedJSONStringifyStr = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`
define ptr @__kml_json_str_str(ptr %s) {
entry:
  ; A null string pointer is the value null (a string-or-null field holding
  ; null, an Error's unset code): serialize the JSON literal null, as
  ; JSON.stringify does for a null-valued property. (ADR-00683 stopped the
  ; strlen(NULL) fault here with "" — a placeholder, not Node's output.)
  %isnull = icmp eq ptr %s, null
  br i1 %isnull, label %nullstr, label %go
nullstr:
  %eb = call ptr @__kml_str_alloc(i64 4)
  store i8 110, ptr %eb, align 1
  %eb1 = getelementptr i8, ptr %eb, i64 1
  store i8 117, ptr %eb1, align 1
  %eb2 = getelementptr i8, ptr %eb, i64 2
  store i8 108, ptr %eb2, align 1
  %eb3 = getelementptr i8, ptr %eb, i64 3
  store i8 108, ptr %eb3, align 1
  ret ptr %eb
go:
  ; strlen-bounded: not every runtime string carries a length header (the
  ; URL/crypto sidecars, EventSource and Blob.text() hand back bare C strings),
  ; so an embedded NUL still ends the string here — BACKLOG §0. Worst case is
  ; every byte as \u00XX: 6x, plus the quotes.
  %len = call i64 @strlen(ptr %s)
  %max = mul i64 %len, 6
  %total = add i64 %max, 3
  %buf = call ptr @__kml_str_alloc(i64 %total)
  store i8 34, ptr %buf, align 1
  br label %loop
loop:
  %i = phi i64 [ 0, %go ], [ %i2, %plain ], [ %i2e, %esc ], [ %i2u, %uesc ]
  %j = phi i64 [ 1, %go ], [ %j2, %plain ], [ %j3, %esc ], [ %j6, %uesc ]
  %at_end = icmp eq i64 %i, %len
  br i1 %at_end, label %close, label %body
body:
  %cp = getelementptr i8, ptr %s, i64 %i
  %c = load i8, ptr %cp, align 1
  %is_q  = icmp eq i8 %c, 34
  %is_bs = icmp eq i8 %c, 92
  %is_bb = icmp eq i8 %c, 8
  %is_ff = icmp eq i8 %c, 12
  %is_nl = icmp eq i8 %c, 10
  %is_cr = icmp eq i8 %c, 13
  %is_tb = icmp eq i8 %c, 9
  %ne1 = or i1 %is_q, %is_bs
  %ne2 = or i1 %ne1, %is_nl
  %ne3 = or i1 %ne2, %is_cr
  %ne4 = or i1 %ne3, %is_tb
  %ne5 = or i1 %ne4, %is_bb
  %ne6 = or i1 %ne5, %is_ff
  br i1 %ne6, label %esc, label %ctl
ctl:
  ; any other control character (incl. NUL) is \u00XX — QuoteJSONString
  %is_ctl = icmp ult i8 %c, 32
  br i1 %is_ctl, label %uesc, label %plain
plain:
  %dp = getelementptr i8, ptr %buf, i64 %j
  store i8 %c, ptr %dp, align 1
  %j2 = add i64 %j, 1
  %i2 = add i64 %i, 1
  br label %loop
esc:
  %ep1 = getelementptr i8, ptr %buf, i64 %j
  store i8 92, ptr %ep1, align 1
  %j1e = add i64 %j, 1
  %ec1 = select i1 %is_q,  i8 34,  i8 92
  %ec2 = select i1 %is_nl, i8 110, i8 %ec1
  %ec3 = select i1 %is_cr, i8 114, i8 %ec2
  %ec4 = select i1 %is_tb, i8 116, i8 %ec3
  %ec5 = select i1 %is_bb, i8 98, i8 %ec4
  %ec6 = select i1 %is_ff, i8 102, i8 %ec5
  %ep2 = getelementptr i8, ptr %buf, i64 %j1e
  store i8 %ec6, ptr %ep2, align 1
  %j3  = add i64 %j1e, 1
  %i2e = add i64 %i, 1
  br label %loop
uesc:
  %u0 = getelementptr i8, ptr %buf, i64 %j
  store i8 92, ptr %u0, align 1
  %ju1 = add i64 %j, 1
  %u1 = getelementptr i8, ptr %buf, i64 %ju1
  store i8 117, ptr %u1, align 1
  %ju2 = add i64 %j, 2
  %u2 = getelementptr i8, ptr %buf, i64 %ju2
  store i8 48, ptr %u2, align 1
  %ju3 = add i64 %j, 3
  %u3 = getelementptr i8, ptr %buf, i64 %ju3
  store i8 48, ptr %u3, align 1
  %hi = lshr i8 %c, 4
  %hi_ge10 = icmp uge i8 %hi, 10
  %hi_a = add i8 %hi, 87
  %hi_d = add i8 %hi, 48
  %hi_ch = select i1 %hi_ge10, i8 %hi_a, i8 %hi_d
  %ju4 = add i64 %j, 4
  %u4 = getelementptr i8, ptr %buf, i64 %ju4
  store i8 %hi_ch, ptr %u4, align 1
  %lo = and i8 %c, 15
  %lo_ge10 = icmp uge i8 %lo, 10
  %lo_a = add i8 %lo, 87
  %lo_d = add i8 %lo, 48
  %lo_ch = select i1 %lo_ge10, i8 %lo_a, i8 %lo_d
  %ju5 = add i64 %j, 5
  %u5 = getelementptr i8, ptr %buf, i64 %ju5
  store i8 %lo_ch, ptr %u5, align 1
  %j6 = add i64 %j, 6
  %i2u = add i64 %i, 1
  br label %loop
close:
  %cq = getelementptr i8, ptr %buf, i64 %j
  store i8 34, ptr %cq, align 1
  %jn = add i64 %j, 1
  %np = getelementptr i8, ptr %buf, i64 %jn
  store i8 0, ptr %np, align 1
  %hp = getelementptr i8, ptr %buf, i64 -8
  store i64 %jn, ptr %hp, align 8
  ret ptr %buf
}`)
}
