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
  ; A null string pointer (an absent/null ptr-typed field, e.g. an Error's
  ; unset code, ADR-00683) would fault at strlen(NULL); serialize it as an
  ; empty JSON string rather than crashing.
  %isnull = icmp eq ptr %s, null
  br i1 %isnull, label %nullstr, label %go
nullstr:
  %eb = call ptr @__kml_str_alloc(i64 2)
  store i8 34, ptr %eb, align 1
  %eb1 = getelementptr i8, ptr %eb, i64 1
  store i8 34, ptr %eb1, align 1
  ret ptr %eb
go:
  %len = call i64 @strlen(ptr %s)
  %max = mul i64 %len, 2
  %total = add i64 %max, 3
  %buf = call ptr @__kml_str_alloc(i64 %total)
  store i8 34, ptr %buf, align 1
  br label %loop
loop:
  %i = phi i64 [ 0, %go ], [ %i2, %plain ], [ %i2e, %esc ]
  %j = phi i64 [ 1, %go ], [ %j2, %plain ], [ %j3, %esc ]
  %at_end = icmp eq i64 %i, %len
  br i1 %at_end, label %close, label %body
body:
  %cp = getelementptr i8, ptr %s, i64 %i
  %c = load i8, ptr %cp, align 1
  %is_q  = icmp eq i8 %c, 34
  %is_bs = icmp eq i8 %c, 92
  %is_nl = icmp eq i8 %c, 10
  %is_cr = icmp eq i8 %c, 13
  %is_tb = icmp eq i8 %c, 9
  %ne1 = or i1 %is_q, %is_bs
  %ne2 = or i1 %ne1, %is_nl
  %ne3 = or i1 %ne2, %is_cr
  %ne4 = or i1 %ne3, %is_tb
  br i1 %ne4, label %esc, label %plain
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
  %ep2 = getelementptr i8, ptr %buf, i64 %j1e
  store i8 %ec4, ptr %ep2, align 1
  %j3  = add i64 %j1e, 1
  %i2e = add i64 %i, 1
  br label %loop
close:
  %cq = getelementptr i8, ptr %buf, i64 %j
  store i8 34, ptr %cq, align 1
  %jn = add i64 %j, 1
  %np = getelementptr i8, ptr %buf, i64 %jn
  store i8 0, ptr %np, align 1
  call void @__kml_str_finalize(ptr %buf)
  ret ptr %buf
}`)
}
