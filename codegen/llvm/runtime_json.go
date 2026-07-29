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
	fmtName := e.internString("%lld")
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_json_str_num(i64 %%n) {
entry:
  %%buf = call ptr @malloc(i64 32)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%buf, ptr %s, i64 %%n)
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
	e.emitGlobal(`
define ptr @__kml_json_str_str(ptr %s) {
entry:
  %len = call i64 @strlen(ptr %s)
  %max = mul i64 %len, 2
  %total = add i64 %max, 3
  %buf = call ptr @malloc(i64 %total)
  store i8 34, ptr %buf, align 1
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %i2, %plain ], [ %i2e, %esc ]
  %j = phi i64 [ 1, %entry ], [ %j2, %plain ], [ %j3, %esc ]
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
  ret ptr %buf
}`)
}

func (e *Emitter) ensureJSONParseStr() {
	if e.usedJSONParseStr {
		return
	}
	e.usedJSONParseStr = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.emitGlobal(`
define ptr @__kml_json_parse_str(ptr %s) {
entry:
  %len = call i64 @strlen(ptr %s)
  %ok = icmp sge i64 %len, 2
  br i1 %ok, label %do_copy, label %empty
empty:
  %eb = call ptr @malloc(i64 1)
  store i8 0, ptr %eb, align 1
  ret ptr %eb
do_copy:
  %inner = sub i64 %len, 2
  %size = add i64 %inner, 1
  %buf = call ptr @malloc(i64 %size)
  %src = getelementptr i8, ptr %s, i64 1
  call ptr @memcpy(ptr %buf, ptr %src, i64 %inner)
  %np = getelementptr i8, ptr %buf, i64 %inner
  store i8 0, ptr %np, align 1
  ret ptr %buf
}`)
}

// ensureJSONFindValue declares __kml_json_find_value: finds %pattern (a
// compile-time-known `"key":` string) in %json via strstr, then skips
// whitespace forward past it, returning a pointer to the start of the value —
// or null if the key isn't present. Does not allocate or copy; callers hand
// the returned pointer straight to atoll/strtod/strncmp/__kml_json_parse_field_str,
// each of which naturally stops at its own end (digit run, closing quote, etc.)
// without needing the value's extent bounded up front.
func (e *Emitter) ensureJSONFindValue() {
	if e.usedJSONFindValue {
		return
	}
	e.usedJSONFindValue = true
	e.ensureStrstr()
	e.ensureStrlen()
	e.emitGlobal(`
define ptr @__kml_json_find_value(ptr %json, ptr %pattern) {
entry:
  %found = call ptr @strstr(ptr %json, ptr %pattern)
  %is_found = icmp ne ptr %found, null
  br i1 %is_found, label %skip_ws, label %not_found
skip_ws:
  %plen = call i64 @strlen(ptr %pattern)
  %vstart = getelementptr i8, ptr %found, i64 %plen
  br label %ws_loop
ws_loop:
  %wi = phi i64 [ 0, %skip_ws ], [ %wi_next, %ws_body ]
  %wp = getelementptr i8, ptr %vstart, i64 %wi
  %wc = load i8, ptr %wp, align 1
  %isws1 = icmp eq i8 %wc, 32
  %isws2 = icmp eq i8 %wc, 9
  %isws3 = icmp eq i8 %wc, 10
  %isws4 = icmp eq i8 %wc, 13
  %wa = or i1 %isws1, %isws2
  %wb = or i1 %wa, %isws3
  %is_ws = or i1 %wb, %isws4
  br i1 %is_ws, label %ws_body, label %ws_done
ws_body:
  %wi_next = add i64 %wi, 1
  br label %ws_loop
ws_done:
  %result = getelementptr i8, ptr %vstart, i64 %wi
  ret ptr %result
not_found:
  ret ptr null
}`)
}

// ensureJSONParseFieldStr declares __kml_json_parse_field_str: unescapes a
// JSON string value starting at the opening '"', the reverse of
// __kml_json_str_str's escaping loop. Two passes (count then copy), like
// __kml_split/__kml_replace_all, since the unescaped length must be known
// before allocating. The escape-decode select chain's default case already
// correctly passes through \" and \\ unescaped (the raw escaped byte IS the
// decoded byte for those two), so only \n/\t/\r need explicit selects.
func (e *Emitter) ensureJSONParseFieldStr() {
	if e.usedJSONParseFieldStr {
		return
	}
	e.usedJSONParseFieldStr = true
	e.ensureMalloc()
	e.emitGlobal(`
define ptr @__kml_json_parse_field_str(ptr %v) {
entry:
  %s0 = getelementptr i8, ptr %v, i64 1
  br label %count_loop
count_loop:
  %ci = phi i64 [ 0, %entry ], [ %ci_next, %count_body ], [ %ci_next2, %count_esc ]
  %clen = phi i64 [ 0, %entry ], [ %clen_next, %count_body ], [ %clen_next2, %count_esc ]
  %cp = getelementptr i8, ptr %s0, i64 %ci
  %cc = load i8, ptr %cp, align 1
  %is_quote = icmp eq i8 %cc, 34
  br i1 %is_quote, label %count_done, label %count_check_esc
count_check_esc:
  %is_bs = icmp eq i8 %cc, 92
  br i1 %is_bs, label %count_esc, label %count_body
count_body:
  %ci_next = add i64 %ci, 1
  %clen_next = add i64 %clen, 1
  br label %count_loop
count_esc:
  %ci_next2 = add i64 %ci, 2
  %clen_next2 = add i64 %clen, 1
  br label %count_loop
count_done:
  %alloc = add i64 %clen, 1
  %buf = call ptr @malloc(i64 %alloc)
  br label %fill_loop
fill_loop:
  %fi = phi i64 [ 0, %count_done ], [ %fi_next, %fill_body ], [ %fi_next2, %fill_esc ]
  %fj = phi i64 [ 0, %count_done ], [ %fj_next, %fill_body ], [ %fj_next2, %fill_esc ]
  %fp = getelementptr i8, ptr %s0, i64 %fi
  %fc = load i8, ptr %fp, align 1
  %fis_quote = icmp eq i8 %fc, 34
  br i1 %fis_quote, label %fill_done, label %fill_check_esc
fill_check_esc:
  %fis_bs = icmp eq i8 %fc, 92
  br i1 %fis_bs, label %fill_esc, label %fill_body
fill_body:
  %fdst = getelementptr i8, ptr %buf, i64 %fj
  store i8 %fc, ptr %fdst, align 1
  %fi_next = add i64 %fi, 1
  %fj_next = add i64 %fj, 1
  br label %fill_loop
fill_esc:
  %fi_plus1 = add i64 %fi, 1
  %fnext_p = getelementptr i8, ptr %s0, i64 %fi_plus1
  %fescc = load i8, ptr %fnext_p, align 1
  %eis_n = icmp eq i8 %fescc, 110
  %eis_t = icmp eq i8 %fescc, 116
  %eis_r = icmp eq i8 %fescc, 114
  %edec1 = select i1 %eis_n, i8 10, i8 %fescc
  %edec2 = select i1 %eis_t, i8 9, i8 %edec1
  %edec3 = select i1 %eis_r, i8 13, i8 %edec2
  %fdst2 = getelementptr i8, ptr %buf, i64 %fj
  store i8 %edec3, ptr %fdst2, align 1
  %fi_next2 = add i64 %fi, 2
  %fj_next2 = add i64 %fj, 1
  br label %fill_loop
fill_done:
  %nullp = getelementptr i8, ptr %buf, i64 %fj
  store i8 0, ptr %nullp, align 1
  ret ptr %buf
}`)
}
