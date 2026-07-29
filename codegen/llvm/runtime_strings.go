package llvm

import ()

func (e *Emitter) ensureStringTrim() {
	if e.usedStringTrim {
		return
	}
	e.usedStringTrim = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.emitGlobal(`
define ptr @__kml_trim(ptr %s) {
entry:
  br label %skip_lead
skip_lead:
  %si = phi i64 [ 0, %entry ], [ %si_next, %skip_lead ]
  %sp = getelementptr i8, ptr %s, i64 %si
  %sc = load i8, ptr %sp, align 1
  %ws1 = icmp eq i8 %sc, 32
  %ws2 = icmp eq i8 %sc, 9
  %ws3 = icmp eq i8 %sc, 10
  %ws4 = icmp eq i8 %sc, 13
  %ws5 = icmp eq i8 %sc, 11
  %ws6 = icmp eq i8 %sc, 12
  %wa = or i1 %ws1, %ws2
  %wb = or i1 %wa, %ws3
  %wc = or i1 %wb, %ws4
  %wd = or i1 %wc, %ws5
  %is_ws = or i1 %wd, %ws6
  %si_next = add i64 %si, 1
  br i1 %is_ws, label %skip_lead, label %got_lead
got_lead:
  %start_p = getelementptr i8, ptr %s, i64 %si
  %rem_len = call i64 @strlen(ptr %start_p)
  %is_empty = icmp eq i64 %rem_len, 0
  br i1 %is_empty, label %ret_empty, label %skip_trail
ret_empty:
  %ebuf = call ptr @malloc(i64 1)
  store i8 0, ptr %ebuf, align 1
  ret ptr %ebuf
skip_trail:
  %end_init = sub i64 %rem_len, 1
  br label %trail_loop
trail_loop:
  %ei = phi i64 [ %end_init, %skip_trail ], [ %ei_next, %trail_loop ]
  %ep = getelementptr i8, ptr %start_p, i64 %ei
  %ec = load i8, ptr %ep, align 1
  %ews1 = icmp eq i8 %ec, 32
  %ews2 = icmp eq i8 %ec, 9
  %ews3 = icmp eq i8 %ec, 10
  %ews4 = icmp eq i8 %ec, 13
  %ews5 = icmp eq i8 %ec, 11
  %ews6 = icmp eq i8 %ec, 12
  %ewa = or i1 %ews1, %ews2
  %ewb = or i1 %ewa, %ews3
  %ewc = or i1 %ewb, %ews4
  %ewd = or i1 %ewc, %ews5
  %e_is_ws = or i1 %ewd, %ews6
  %ei_next = sub i64 %ei, 1
  br i1 %e_is_ws, label %trail_loop, label %got_trail
got_trail:
  %trimlen = add i64 %ei, 1
  %allocsz = add i64 %trimlen, 1
  %buf = call ptr @malloc(i64 %allocsz)
  call ptr @memcpy(ptr %buf, ptr %start_p, i64 %trimlen)
  %nullp = getelementptr i8, ptr %buf, i64 %trimlen
  store i8 0, ptr %nullp, align 1
  ret ptr %buf
}`)
}

// ensureStringTrimStart declares __kml_trim_start: strips only leading whitespace.
// Reaching the NUL terminator during the leading scan naturally stops the loop
// (a NUL byte never matches any whitespace check), so no separate empty-string
// case or strlen-based bounds check is needed before scanning.
func (e *Emitter) ensureStringTrimStart() {
	if e.usedStringTrimStart {
		return
	}
	e.usedStringTrimStart = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.emitGlobal(`
define ptr @__kml_trim_start(ptr %s) {
entry:
  br label %skip_lead
skip_lead:
  %si = phi i64 [ 0, %entry ], [ %si_next, %skip_lead ]
  %sp = getelementptr i8, ptr %s, i64 %si
  %sc = load i8, ptr %sp, align 1
  %ws1 = icmp eq i8 %sc, 32
  %ws2 = icmp eq i8 %sc, 9
  %ws3 = icmp eq i8 %sc, 10
  %ws4 = icmp eq i8 %sc, 13
  %ws5 = icmp eq i8 %sc, 11
  %ws6 = icmp eq i8 %sc, 12
  %wa = or i1 %ws1, %ws2
  %wb = or i1 %wa, %ws3
  %wc = or i1 %wb, %ws4
  %wd = or i1 %wc, %ws5
  %is_ws = or i1 %wd, %ws6
  %si_next = add i64 %si, 1
  br i1 %is_ws, label %skip_lead, label %got_lead
got_lead:
  %start_p = getelementptr i8, ptr %s, i64 %si
  %rem_len = call i64 @strlen(ptr %start_p)
  %allocsz = add i64 %rem_len, 1
  %buf = call ptr @malloc(i64 %allocsz)
  call ptr @memcpy(ptr %buf, ptr %start_p, i64 %allocsz)
  ret ptr %buf
}`)
}

// ensureStringTrimEnd declares __kml_trim_end: strips only trailing whitespace.
// Scans backward from the last byte; unlike .trim()'s trail_loop (which is only
// ever entered on a substring already known to start with a non-whitespace
// byte), this scans the ORIGINAL string, so an explicit bounds check is needed
// to avoid walking past index 0 when the whole string is whitespace (or empty).
func (e *Emitter) ensureStringTrimEnd() {
	if e.usedStringTrimEnd {
		return
	}
	e.usedStringTrimEnd = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.emitGlobal(`
define ptr @__kml_trim_end(ptr %s) {
entry:
  %slen = call i64 @strlen(ptr %s)
  %is_empty = icmp eq i64 %slen, 0
  br i1 %is_empty, label %ret_empty, label %init
init:
  %ei0 = sub i64 %slen, 1
  br label %trail_loop
trail_loop:
  %ei = phi i64 [ %ei0, %init ], [ %ei_next, %trail_body ]
  %ep = getelementptr i8, ptr %s, i64 %ei
  %ec = load i8, ptr %ep, align 1
  %ews1 = icmp eq i8 %ec, 32
  %ews2 = icmp eq i8 %ec, 9
  %ews3 = icmp eq i8 %ec, 10
  %ews4 = icmp eq i8 %ec, 13
  %ews5 = icmp eq i8 %ec, 11
  %ews6 = icmp eq i8 %ec, 12
  %ewa = or i1 %ews1, %ews2
  %ewb = or i1 %ewa, %ews3
  %ewc = or i1 %ewb, %ews4
  %ewd = or i1 %ewc, %ews5
  %e_is_ws = or i1 %ewd, %ews6
  br i1 %e_is_ws, label %check_bound, label %got_trail
check_bound:
  %at_zero = icmp eq i64 %ei, 0
  br i1 %at_zero, label %ret_empty, label %trail_body
trail_body:
  %ei_next = sub i64 %ei, 1
  br label %trail_loop
got_trail:
  %trimlen = add i64 %ei, 1
  %allocsz = add i64 %trimlen, 1
  %buf = call ptr @malloc(i64 %allocsz)
  call ptr @memcpy(ptr %buf, ptr %s, i64 %trimlen)
  %nullp = getelementptr i8, ptr %buf, i64 %trimlen
  store i8 0, ptr %nullp, align 1
  ret ptr %buf
ret_empty:
  %ebuf = call ptr @malloc(i64 1)
  store i8 0, ptr %ebuf, align 1
  ret ptr %ebuf
}`)
}

func (e *Emitter) ensureStringToUpper() {
	if e.usedStringToUpper {
		return
	}
	e.usedStringToUpper = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.emitGlobal(`
define ptr @__kml_toupper(ptr %s) {
entry:
  %len = call i64 @strlen(ptr %s)
  %alloc = add i64 %len, 1
  %buf = call ptr @malloc(i64 %alloc)
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
	e.emitGlobal(`
define ptr @__kml_tolower(ptr %s) {
entry:
  %len = call i64 @strlen(ptr %s)
  %alloc = add i64 %len, 1
  %buf = call ptr @malloc(i64 %alloc)
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
	e.ensureStrstr()
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.emitGlobal(`
define ptr @__kml_replace(ptr %s, ptr %search, ptr %rep) {
entry:
  %found = call ptr @strstr(ptr %s, ptr %search)
  %is_found = icmp ne ptr %found, null
  br i1 %is_found, label %do_replace, label %no_replace
no_replace:
  %slen0 = call i64 @strlen(ptr %s)
  %salloc0 = add i64 %slen0, 1
  %sbuf0 = call ptr @malloc(i64 %salloc0)
  call ptr @memcpy(ptr %sbuf0, ptr %s, i64 %salloc0)
  ret ptr %sbuf0
do_replace:
  %s_int = ptrtoint ptr %s to i64
  %f_int = ptrtoint ptr %found to i64
  %prefix_len = sub i64 %f_int, %s_int
  %search_len = call i64 @strlen(ptr %search)
  %rep_len = call i64 @strlen(ptr %rep)
  %suffix_ptr = getelementptr i8, ptr %found, i64 %search_len
  %suffix_len = call i64 @strlen(ptr %suffix_ptr)
  %total0 = add i64 %prefix_len, %rep_len
  %total1 = add i64 %total0, %suffix_len
  %total = add i64 %total1, 1
  %buf = call ptr @malloc(i64 %total)
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
	e.ensureStrstr()
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.emitGlobal(`
define ptr @__kml_replace_all(ptr %s, ptr %search, ptr %rep) {
entry:
  %search_len = call i64 @strlen(ptr %search)
  %is_empty_search = icmp eq i64 %search_len, 0
  br i1 %is_empty_search, label %copy_unchanged, label %count_setup
copy_unchanged:
  %slen_u = call i64 @strlen(ptr %s)
  %salloc_u = add i64 %slen_u, 1
  %sbuf_u = call ptr @malloc(i64 %salloc_u)
  call ptr @memcpy(ptr %sbuf_u, ptr %s, i64 %salloc_u)
  ret ptr %sbuf_u
count_setup:
  %rep_len = call i64 @strlen(ptr %rep)
  br label %cnt_loop
cnt_loop:
  %cur_c = phi ptr [ %s, %count_setup ], [ %nxt_c, %cnt_body ]
  %cnt = phi i64 [ 0, %count_setup ], [ %cnt1, %cnt_body ]
  %found_c = call ptr @strstr(ptr %cur_c, ptr %search)
  %has_c = icmp ne ptr %found_c, null
  br i1 %has_c, label %cnt_body, label %cnt_done
cnt_body:
  %cnt1 = add i64 %cnt, 1
  %nxt_c = getelementptr i8, ptr %found_c, i64 %search_len
  br label %cnt_loop
cnt_done:
  %slen = call i64 @strlen(ptr %s)
  %removed = mul i64 %cnt, %search_len
  %added = mul i64 %cnt, %rep_len
  %base = sub i64 %slen, %removed
  %total0 = add i64 %base, %added
  %total = add i64 %total0, 1
  %buf = call ptr @malloc(i64 %total)
  br label %fill_loop
fill_loop:
  %cur_f = phi ptr [ %s, %cnt_done ], [ %nxt_f, %fill_body ]
  %out_f = phi ptr [ %buf, %cnt_done ], [ %out_nxt, %fill_body ]
  %found_f = call ptr @strstr(ptr %cur_f, ptr %search)
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
  br label %fill_loop
fill_last:
  %last_len = call i64 @strlen(ptr %cur_f)
  call ptr @memcpy(ptr %out_f, ptr %cur_f, i64 %last_len)
  %out_last_end = getelementptr i8, ptr %out_f, i64 %last_len
  store i8 0, ptr %out_last_end, align 1
  ret ptr %buf
}`)
}

func (e *Emitter) ensureStringSplit() {
	if e.usedStringSplit {
		return
	}
	e.usedStringSplit = true
	e.ensureStrstr()
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.emitGlobal(`
define {ptr, i64} @__kml_split(ptr %s, ptr %sep) {
entry:
  %sep_len = call i64 @strlen(ptr %sep)
  %is_empty_sep = icmp eq i64 %sep_len, 0
  br i1 %is_empty_sep, label %char_split, label %cnt_loop
char_split:
  %slen_c = call i64 @strlen(ptr %s)
  %carr_bytes = mul i64 %slen_c, 8
  %carr = call ptr @malloc(i64 %carr_bytes)
  br label %char_loop
char_loop:
  %ci = phi i64 [ 0, %char_split ], [ %ci1, %char_body ]
  %cdone = icmp eq i64 %ci, %slen_c
  br i1 %cdone, label %char_done, label %char_body
char_body:
  %cbuf = call ptr @malloc(i64 2)
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
  %cnt = phi i64 [ 0, %entry ], [ %cnt1, %cnt_body ]
  %found_c = call ptr @strstr(ptr %cur_c, ptr %sep)
  %has_c = icmp ne ptr %found_c, null
  br i1 %has_c, label %cnt_body, label %cnt_done
cnt_body:
  %cnt1 = add i64 %cnt, 1
  %nxt_c = getelementptr i8, ptr %found_c, i64 %sep_len
  br label %cnt_loop
cnt_done:
  %num_parts = add i64 %cnt, 1
  %arr_bytes = mul i64 %num_parts, 8
  %arr = call ptr @malloc(i64 %arr_bytes)
  br label %fill_loop
fill_loop:
  %cur_f = phi ptr [ %s, %cnt_done ], [ %nxt_f, %fill_body ]
  %idx = phi i64 [ 0, %cnt_done ], [ %idx1, %fill_body ]
  %found_f = call ptr @strstr(ptr %cur_f, ptr %sep)
  %has_f = icmp ne ptr %found_f, null
  br i1 %has_f, label %fill_body, label %fill_last
fill_body:
  %cur_int = ptrtoint ptr %cur_f to i64
  %fnd_int = ptrtoint ptr %found_f to i64
  %part_len = sub i64 %fnd_int, %cur_int
  %part_alloc = add i64 %part_len, 1
  %part_buf = call ptr @malloc(i64 %part_alloc)
  call ptr @memcpy(ptr %part_buf, ptr %cur_f, i64 %part_len)
  %part_null = getelementptr i8, ptr %part_buf, i64 %part_len
  store i8 0, ptr %part_null, align 1
  %slot_f = getelementptr ptr, ptr %arr, i64 %idx
  store ptr %part_buf, ptr %slot_f, align 8
  %idx1 = add i64 %idx, 1
  %nxt_f = getelementptr i8, ptr %found_f, i64 %sep_len
  br label %fill_loop
fill_last:
  %last_len = call i64 @strlen(ptr %cur_f)
  %last_alloc = add i64 %last_len, 1
  %last_buf = call ptr @malloc(i64 %last_alloc)
  call ptr @memcpy(ptr %last_buf, ptr %cur_f, i64 %last_len)
  %last_null = getelementptr i8, ptr %last_buf, i64 %last_len
  store i8 0, ptr %last_null, align 1
  %last_slot = getelementptr ptr, ptr %arr, i64 %idx
  store ptr %last_buf, ptr %last_slot, align 8
  %r0 = insertvalue {ptr, i64} undef, ptr %arr, 0
  %r1 = insertvalue {ptr, i64} %r0, i64 %num_parts, 1
  ret {ptr, i64} %r1
}`)
}
