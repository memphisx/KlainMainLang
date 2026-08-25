package llvm

import ()

// ensureWsSpan declares @__kml_ws_span: the byte length of the JS WhiteSpace/
// LineTerminator character starting at p (0 when p doesn't start one). Covers
// the full spec set in UTF-8: ASCII \t \n \v \f \r space, U+00A0 (C2 A0),
// U+1680 (E1 9A 80), U+2000-200A / U+2028 / U+2029 / U+202F (E2 80 xx and
// E2 81 9F for U+205F), U+3000 (E3 80 80), and U+FEFF (EF BB BF). Reads
// beyond p[0] only after confirming the earlier byte is non-NUL, so a
// truncated sequence at the end of a string never reads past the terminator.
func (e *Emitter) ensureWsSpan() {
	if e.usedWsSpan {
		return
	}
	e.usedWsSpan = true
	e.emitGlobal(`
define i64 @__kml_ws_span(ptr %p) {
entry:
  %c0 = load i8, ptr %p, align 1
  %a1 = icmp eq i8 %c0, 32
  %a2 = icmp uge i8 %c0, 9
  %a3 = icmp ule i8 %c0, 13
  %a23 = and i1 %a2, %a3
  %ascii = or i1 %a1, %a23
  br i1 %ascii, label %ret1, label %chk2
chk2:
  %is_c2 = icmp eq i8 %c0, 194
  %is_e1 = icmp eq i8 %c0, 225
  %is_e2 = icmp eq i8 %c0, 226
  %is_e3 = icmp eq i8 %c0, 227
  %is_ef = icmp eq i8 %c0, 239
  %m1 = or i1 %is_c2, %is_e1
  %m2 = or i1 %m1, %is_e2
  %m3 = or i1 %m2, %is_e3
  %multi = or i1 %m3, %is_ef
  br i1 %multi, label %load1, label %ret0
load1:
  %p1 = getelementptr i8, ptr %p, i64 1
  %c1 = load i8, ptr %p1, align 1
  %c1_nul = icmp eq i8 %c1, 0
  br i1 %c1_nul, label %ret0, label %dis2
dis2:
  br i1 %is_c2, label %nbsp, label %load2
nbsp:
  %is_a0 = icmp eq i8 %c1, 160
  br i1 %is_a0, label %ret2, label %ret0
load2:
  %p2 = getelementptr i8, ptr %p, i64 2
  %c2 = load i8, ptr %p2, align 1
  %c2_nul = icmp eq i8 %c2, 0
  br i1 %c2_nul, label %ret0, label %dis3
dis3:
  br i1 %is_e1, label %ogham, label %dis_e2
ogham:
  %e1_9a = icmp eq i8 %c1, 154
  %e1_80 = icmp eq i8 %c2, 128
  %e1_ok = and i1 %e1_9a, %e1_80
  br i1 %e1_ok, label %ret3, label %ret0
dis_e2:
  br i1 %is_e2, label %e2fam, label %dis_e3
e2fam:
  %e2_80 = icmp eq i8 %c1, 128
  br i1 %e2_80, label %e2_80fam, label %e2_81chk
e2_80fam:
  %sp_ge = icmp uge i8 %c2, 128
  %sp_le = icmp ule i8 %c2, 138
  %sp_rng = and i1 %sp_ge, %sp_le
  %ls = icmp eq i8 %c2, 168
  %ps = icmp eq i8 %c2, 169
  %nnbsp = icmp eq i8 %c2, 175
  %e2a = or i1 %sp_rng, %ls
  %e2b = or i1 %e2a, %ps
  %e2ok = or i1 %e2b, %nnbsp
  br i1 %e2ok, label %ret3, label %ret0
e2_81chk:
  %e2_81 = icmp eq i8 %c1, 129
  %mmsp = icmp eq i8 %c2, 159
  %e281ok = and i1 %e2_81, %mmsp
  br i1 %e281ok, label %ret3, label %ret0
dis_e3:
  br i1 %is_e3, label %ideo, label %bom
ideo:
  %e3_80 = icmp eq i8 %c1, 128
  %e3_80b = icmp eq i8 %c2, 128
  %e3ok = and i1 %e3_80, %e3_80b
  br i1 %e3ok, label %ret3, label %ret0
bom:
  %ef_bb = icmp eq i8 %c1, 187
  %ef_bf = icmp eq i8 %c2, 191
  %efok = and i1 %ef_bb, %ef_bf
  br i1 %efok, label %ret3, label %ret0
ret1:
  ret i64 1
ret2:
  ret i64 2
ret3:
  ret i64 3
ret0:
  ret i64 0
}`)
}

func (e *Emitter) ensureStringTrim() {
	if e.usedStringTrim {
		return
	}
	e.usedStringTrim = true
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureWsSpan()
	e.ensureStrHeaderRuntime() // TDD-00120: length-prefixed result
	// Forward scan: skip the leading whitespace run, then walk to the end
	// tracking the exclusive end of the last non-whitespace byte — this is
	// what lets multi-byte whitespace work without a backwards UTF-8 decoder.
	e.emitGlobal(`
define ptr @__kml_trim(ptr %s) {
entry:
  br label %lead
lead:
  %i = phi i64 [ 0, %entry ], [ %i_adv, %lead_ws ]
  %lp = getelementptr i8, ptr %s, i64 %i
  %ln = call i64 @__kml_ws_span(ptr %lp)
  %lws = icmp ne i64 %ln, 0
  br i1 %lws, label %lead_ws, label %scan_init
lead_ws:
  %i_adv = add i64 %i, %ln
  br label %lead
scan_init:
  br label %scan
scan:
  %j = phi i64 [ %i, %scan_init ], [ %j_next, %scan_step ]
  %end = phi i64 [ %i, %scan_init ], [ %end_next, %scan_step ]
  %jp = getelementptr i8, ptr %s, i64 %j
  %jc = load i8, ptr %jp, align 1
  %at_nul = icmp eq i8 %jc, 0
  br i1 %at_nul, label %done, label %step
step:
  %n = call i64 @__kml_ws_span(ptr %jp)
  %is_ws = icmp ne i64 %n, 0
  %adv = select i1 %is_ws, i64 %n, i64 1
  br label %scan_step
scan_step:
  %j_next = add i64 %j, %adv
  %end_next = select i1 %is_ws, i64 %end, i64 %j_next
  br label %scan
done:
  %tl = sub i64 %end, %i
  %buf = call ptr @__kml_str_alloc(i64 %tl)
  %sp = getelementptr i8, ptr %s, i64 %i
  call ptr @memcpy(ptr %buf, ptr %sp, i64 %tl)
  %np = getelementptr i8, ptr %buf, i64 %tl
  store i8 0, ptr %np, align 1
  ret ptr %buf
}`)
}

// ensureStringTrimStart declares __kml_trim_start: strips only leading
// whitespace (full JS whitespace set via @__kml_ws_span).
func (e *Emitter) ensureStringTrimStart() {
	if e.usedStringTrimStart {
		return
	}
	e.usedStringTrimStart = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureWsSpan()
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`
define ptr @__kml_trim_start(ptr %s) {
entry:
  br label %lead
lead:
  %i = phi i64 [ 0, %entry ], [ %i_adv, %lead_ws ]
  %lp = getelementptr i8, ptr %s, i64 %i
  %ln = call i64 @__kml_ws_span(ptr %lp)
  %lws = icmp ne i64 %ln, 0
  br i1 %lws, label %lead_ws, label %done
lead_ws:
  %i_adv = add i64 %i, %ln
  br label %lead
done:
  %sp = getelementptr i8, ptr %s, i64 %i
  %rl = call i64 @strlen(ptr %sp)
  %asz = add i64 %rl, 1
  %buf = call ptr @__kml_str_alloc(i64 %rl)
  call ptr @memcpy(ptr %buf, ptr %sp, i64 %asz)
  ret ptr %buf
}`)
}

// ensureStringTrimEnd declares __kml_trim_end: strips only trailing
// whitespace — the same forward end-tracking scan __kml_trim uses, from
// index 0.
func (e *Emitter) ensureStringTrimEnd() {
	if e.usedStringTrimEnd {
		return
	}
	e.usedStringTrimEnd = true
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureWsSpan()
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`
define ptr @__kml_trim_end(ptr %s) {
entry:
  br label %scan
scan:
  %j = phi i64 [ 0, %entry ], [ %j_next, %scan_step ]
  %end = phi i64 [ 0, %entry ], [ %end_next, %scan_step ]
  %jp = getelementptr i8, ptr %s, i64 %j
  %jc = load i8, ptr %jp, align 1
  %at_nul = icmp eq i8 %jc, 0
  br i1 %at_nul, label %done, label %step
step:
  %n = call i64 @__kml_ws_span(ptr %jp)
  %is_ws = icmp ne i64 %n, 0
  %adv = select i1 %is_ws, i64 %n, i64 1
  br label %scan_step
scan_step:
  %j_next = add i64 %j, %adv
  %end_next = select i1 %is_ws, i64 %end, i64 %j_next
  br label %scan
done:
  %buf = call ptr @__kml_str_alloc(i64 %end)
  call ptr @memcpy(ptr %buf, ptr %s, i64 %end)
  %np = getelementptr i8, ptr %buf, i64 %end
  store i8 0, ptr %np, align 1
  ret ptr %buf
}`)
}

func (e *Emitter) ensureStringToUpper() {
	if e.usedStringToUpper {
		return
	}
	e.usedStringToUpper = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`
define ptr @__kml_toupper(ptr %s) {
entry:
  %len = call i64 @strlen(ptr %s)
  %buf = call ptr @__kml_str_alloc(i64 %len)
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %i_next, %body ]
  %done = icmp eq i64 %i, %len
  br i1 %done, label %exit, label %body
body:
  %srcp = getelementptr i8, ptr %s, i64 %i
  %c = load i8, ptr %srcp, align 1
  %ge_a = icmp uge i8 %c, 97
  %le_z = icmp ule i8 %c, 122
  %is_lower = and i1 %ge_a, %le_z
  %upper_c = add i8 %c, -32
  %out_c = select i1 %is_lower, i8 %upper_c, i8 %c
  %dstp = getelementptr i8, ptr %buf, i64 %i
  store i8 %out_c, ptr %dstp, align 1
  %i_next = add i64 %i, 1
  br label %loop
exit:
  %nullp = getelementptr i8, ptr %buf, i64 %len
  store i8 0, ptr %nullp, align 1
  ret ptr %buf
}`)
}

func (e *Emitter) ensureStringToLower() {
	if e.usedStringToLower {
		return
	}
	e.usedStringToLower = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`
define ptr @__kml_tolower(ptr %s) {
entry:
  %len = call i64 @strlen(ptr %s)
  %buf = call ptr @__kml_str_alloc(i64 %len)
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %i_next, %body ]
  %done = icmp eq i64 %i, %len
  br i1 %done, label %exit, label %body
body:
  %srcp = getelementptr i8, ptr %s, i64 %i
  %c = load i8, ptr %srcp, align 1
  %ge_A = icmp uge i8 %c, 65
  %le_Z = icmp ule i8 %c, 90
  %is_upper = and i1 %ge_A, %le_Z
  %lower_c = add i8 %c, 32
  %out_c = select i1 %is_upper, i8 %lower_c, i8 %c
  %dstp = getelementptr i8, ptr %buf, i64 %i
  store i8 %out_c, ptr %dstp, align 1
  %i_next = add i64 %i, 1
  br label %loop
exit:
  %nullp = getelementptr i8, ptr %buf, i64 %len
  store i8 0, ptr %nullp, align 1
  ret ptr %buf
}`)
}

func (e *Emitter) ensureStringReplace() {
	if e.usedStringReplace {
		return
	}
	e.usedStringReplace = true
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureStrHeaderRuntime() // memmem + __kml_str_alloc (binary-safe, TDD-00120 Stage 3)
	// Lengths are explicit parameters, not read from the header: user .replace()
	// passes __kml_str_len(...), but internal callers (HTTP header/query parsing,
	// SSE) pass strlen() of a raw, headerless request buffer. Reading ptr-8 on
	// those would be garbage — this keeps the search memmem-bounded either way.
	e.emitGlobal(`
define ptr @__kml_replace(ptr %s, i64 %slen, ptr %search, i64 %search_len, ptr %rep, i64 %rep_len) {
entry:
  %found = call ptr @memmem(ptr %s, i64 %slen, ptr %search, i64 %search_len)
  %is_found = icmp ne ptr %found, null
  br i1 %is_found, label %do_replace, label %no_replace
no_replace:
  %sbuf0 = call ptr @__kml_str_alloc(i64 %slen)
  call ptr @memcpy(ptr %sbuf0, ptr %s, i64 %slen)
  %n0 = getelementptr i8, ptr %sbuf0, i64 %slen
  store i8 0, ptr %n0, align 1
  ret ptr %sbuf0
do_replace:
  %s_int = ptrtoint ptr %s to i64
  %f_int = ptrtoint ptr %found to i64
  %prefix_len = sub i64 %f_int, %s_int
  %suffix_ptr = getelementptr i8, ptr %found, i64 %search_len
  %pfx_plus_search = add i64 %prefix_len, %search_len
  %suffix_len = sub i64 %slen, %pfx_plus_search
  %total0 = add i64 %prefix_len, %rep_len
  %total1 = add i64 %total0, %suffix_len
  %buf = call ptr @__kml_str_alloc(i64 %total1)
  call ptr @memcpy(ptr %buf, ptr %s, i64 %prefix_len)
  %rep_dst = getelementptr i8, ptr %buf, i64 %prefix_len
  call ptr @memcpy(ptr %rep_dst, ptr %rep, i64 %rep_len)
  %suf_dst = getelementptr i8, ptr %buf, i64 %total0
  call ptr @memcpy(ptr %suf_dst, ptr %suffix_ptr, i64 %suffix_len)
  %null_slot = getelementptr i8, ptr %buf, i64 %total1
  store i8 0, ptr %null_slot, align 1
  ret ptr %buf
}`)
}

// ensureStringReplaceAll declares __kml_replace_all: replaces every non-overlapping
// occurrence of %search in %s with %rep, in a single left-to-right pass over the
// ORIGINAL string (never re-scanning already-written replacement text, so a %rep
// that itself contains %search is handled correctly, unlike a naive "call
// __kml_replace in a loop until no match remains" approach). An empty %search is
// treated as "no matches" (returns a copy of %s unchanged) to avoid a zero-length
// match making no forward progress.
func (e *Emitter) ensureStringReplaceAll() {
	if e.usedStringReplaceAll {
		return
	}
	e.usedStringReplaceAll = true
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureStrHeaderRuntime() // memmem + __kml_str_len (binary-safe, TDD-00120 Stage 3)
	e.emitGlobal(`
define ptr @__kml_replace_all(ptr %s, i64 %slen, ptr %search, i64 %search_len, ptr %rep, i64 %rep_len) {
entry:
  %is_empty_search = icmp eq i64 %search_len, 0
  br i1 %is_empty_search, label %copy_unchanged, label %count_setup
copy_unchanged:
  %sbuf_u = call ptr @__kml_str_alloc(i64 %slen)
  call ptr @memcpy(ptr %sbuf_u, ptr %s, i64 %slen)
  %nu = getelementptr i8, ptr %sbuf_u, i64 %slen
  store i8 0, ptr %nu, align 1
  ret ptr %sbuf_u
count_setup:
  br label %cnt_loop
cnt_loop:
  %cur_c = phi ptr [ %s, %count_setup ], [ %nxt_c, %cnt_body ]
  %rem_c = phi i64 [ %slen, %count_setup ], [ %remnxt_c, %cnt_body ]
  %cnt = phi i64 [ 0, %count_setup ], [ %cnt1, %cnt_body ]
  %found_c = call ptr @memmem(ptr %cur_c, i64 %rem_c, ptr %search, i64 %search_len)
  %has_c = icmp ne ptr %found_c, null
  br i1 %has_c, label %cnt_body, label %cnt_done
cnt_body:
  %cnt1 = add i64 %cnt, 1
  %nxt_c = getelementptr i8, ptr %found_c, i64 %search_len
  %fc_int = ptrtoint ptr %found_c to i64
  %cc_int = ptrtoint ptr %cur_c to i64
  %skip_c = sub i64 %fc_int, %cc_int
  %consumed_c = add i64 %skip_c, %search_len
  %remnxt_c = sub i64 %rem_c, %consumed_c
  br label %cnt_loop
cnt_done:
  %removed = mul i64 %cnt, %search_len
  %added = mul i64 %cnt, %rep_len
  %base = sub i64 %slen, %removed
  %total0 = add i64 %base, %added
  %buf = call ptr @__kml_str_alloc(i64 %total0)
  br label %fill_loop
fill_loop:
  %cur_f = phi ptr [ %s, %cnt_done ], [ %nxt_f, %fill_body ]
  %rem_f = phi i64 [ %slen, %cnt_done ], [ %remnxt_f, %fill_body ]
  %out_f = phi ptr [ %buf, %cnt_done ], [ %out_nxt, %fill_body ]
  %found_f = call ptr @memmem(ptr %cur_f, i64 %rem_f, ptr %search, i64 %search_len)
  %has_f = icmp ne ptr %found_f, null
  br i1 %has_f, label %fill_body, label %fill_last
fill_body:
  %cur_int = ptrtoint ptr %cur_f to i64
  %fnd_int = ptrtoint ptr %found_f to i64
  %part_len = sub i64 %fnd_int, %cur_int
  call ptr @memcpy(ptr %out_f, ptr %cur_f, i64 %part_len)
  %out_after_part = getelementptr i8, ptr %out_f, i64 %part_len
  call ptr @memcpy(ptr %out_after_part, ptr %rep, i64 %rep_len)
  %out_nxt = getelementptr i8, ptr %out_after_part, i64 %rep_len
  %nxt_f = getelementptr i8, ptr %found_f, i64 %search_len
  %consumed_f = add i64 %part_len, %search_len
  %remnxt_f = sub i64 %rem_f, %consumed_f
  br label %fill_loop
fill_last:
  call ptr @memcpy(ptr %out_f, ptr %cur_f, i64 %rem_f)
  %out_last_end = getelementptr i8, ptr %out_f, i64 %rem_f
  store i8 0, ptr %out_last_end, align 1
  ret ptr %buf
}`)
}

func (e *Emitter) ensureStringSplit() {
	if e.usedStringSplit {
		return
	}
	e.usedStringSplit = true
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureStrHeaderRuntime() // memmem + __kml_str_len (binary-safe, TDD-00120 Stage 3)
	e.emitGlobal(`
define {ptr, i64} @__kml_split(ptr %s, i64 %slen_c, ptr %sep, i64 %sep_len) {
entry:
  %is_empty_sep = icmp eq i64 %sep_len, 0
  br i1 %is_empty_sep, label %char_split, label %cnt_loop
char_split:
  %carr_bytes = mul i64 %slen_c, 8
  %carr = call ptr @malloc(i64 %carr_bytes)
  br label %char_loop
char_loop:
  %ci = phi i64 [ 0, %char_split ], [ %ci1, %char_body ]
  %cdone = icmp eq i64 %ci, %slen_c
  br i1 %cdone, label %char_done, label %char_body
char_body:
  %cbuf = call ptr @__kml_str_alloc(i64 1)
  %csrc = getelementptr i8, ptr %s, i64 %ci
  %cval = load i8, ptr %csrc, align 1
  store i8 %cval, ptr %cbuf, align 1
  %cnull = getelementptr i8, ptr %cbuf, i64 1
  store i8 0, ptr %cnull, align 1
  %cslot = getelementptr ptr, ptr %carr, i64 %ci
  store ptr %cbuf, ptr %cslot, align 8
  %ci1 = add i64 %ci, 1
  br label %char_loop
char_done:
  %rc0 = insertvalue {ptr, i64} undef, ptr %carr, 0
  %rc1 = insertvalue {ptr, i64} %rc0, i64 %slen_c, 1
  ret {ptr, i64} %rc1
cnt_loop:
  %cur_c = phi ptr [ %s, %entry ], [ %nxt_c, %cnt_body ]
  %rem_c = phi i64 [ %slen_c, %entry ], [ %remnxt_c, %cnt_body ]
  %cnt = phi i64 [ 0, %entry ], [ %cnt1, %cnt_body ]
  %found_c = call ptr @memmem(ptr %cur_c, i64 %rem_c, ptr %sep, i64 %sep_len)
  %has_c = icmp ne ptr %found_c, null
  br i1 %has_c, label %cnt_body, label %cnt_done
cnt_body:
  %cnt1 = add i64 %cnt, 1
  %nxt_c = getelementptr i8, ptr %found_c, i64 %sep_len
  %fc_int = ptrtoint ptr %found_c to i64
  %cc_int = ptrtoint ptr %cur_c to i64
  %skip_c = sub i64 %fc_int, %cc_int
  %consumed_c = add i64 %skip_c, %sep_len
  %remnxt_c = sub i64 %rem_c, %consumed_c
  br label %cnt_loop
cnt_done:
  %num_parts = add i64 %cnt, 1
  %arr_bytes = mul i64 %num_parts, 8
  %arr = call ptr @malloc(i64 %arr_bytes)
  br label %fill_loop
fill_loop:
  %cur_f = phi ptr [ %s, %cnt_done ], [ %nxt_f, %fill_body ]
  %rem_f = phi i64 [ %slen_c, %cnt_done ], [ %remnxt_f, %fill_body ]
  %idx = phi i64 [ 0, %cnt_done ], [ %idx1, %fill_body ]
  %found_f = call ptr @memmem(ptr %cur_f, i64 %rem_f, ptr %sep, i64 %sep_len)
  %has_f = icmp ne ptr %found_f, null
  br i1 %has_f, label %fill_body, label %fill_last
fill_body:
  %cur_int = ptrtoint ptr %cur_f to i64
  %fnd_int = ptrtoint ptr %found_f to i64
  %part_len = sub i64 %fnd_int, %cur_int
  %part_buf = call ptr @__kml_str_alloc(i64 %part_len)
  call ptr @memcpy(ptr %part_buf, ptr %cur_f, i64 %part_len)
  %part_null = getelementptr i8, ptr %part_buf, i64 %part_len
  store i8 0, ptr %part_null, align 1
  %slot_f = getelementptr ptr, ptr %arr, i64 %idx
  store ptr %part_buf, ptr %slot_f, align 8
  %idx1 = add i64 %idx, 1
  %nxt_f = getelementptr i8, ptr %found_f, i64 %sep_len
  %consumed_f = add i64 %part_len, %sep_len
  %remnxt_f = sub i64 %rem_f, %consumed_f
  br label %fill_loop
fill_last:
  %last_buf = call ptr @__kml_str_alloc(i64 %rem_f)
  call ptr @memcpy(ptr %last_buf, ptr %cur_f, i64 %rem_f)
  %last_null = getelementptr i8, ptr %last_buf, i64 %rem_f
  store i8 0, ptr %last_null, align 1
  %last_slot = getelementptr ptr, ptr %arr, i64 %idx
  store ptr %last_buf, ptr %last_slot, align 8
  %r0 = insertvalue {ptr, i64} undef, ptr %arr, 0
  %r1 = insertvalue {ptr, i64} %r0, i64 %num_parts, 1
  ret {ptr, i64} %r1
}`)
}
