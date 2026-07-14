// runtime.go — ensures C runtime function declarations and KML runtime helper
// functions are emitted exactly once into the LLVM IR global section.
package llvm

import (
	"fmt"
	"runtime"
	"strings"
)

func (e *Emitter) ensurePrintf() {
	if !e.usedPrintf {
		e.emitGlobal("declare i32 @printf(ptr noundef, ...)")
		e.usedPrintf = true
	}
}

func (e *Emitter) ensureDprintf() {
	if !e.usedDprintf {
		e.emitGlobal("declare i32 @dprintf(i32 noundef, ptr noundef, ...)")
		e.usedDprintf = true
	}
}

func (e *Emitter) ensureMalloc() {
	if !e.usedMalloc {
		e.emitGlobal("declare ptr @malloc(i64 noundef)")
		e.usedMalloc = true
	}
}

func (e *Emitter) ensureExit() {
	if !e.usedExit {
		e.emitGlobal("declare void @exit(i32) noreturn")
		e.usedExit = true
	}
}

func (e *Emitter) ensureGetenv() {
	if !e.usedGetenv {
		e.emitGlobal("declare ptr @getenv(ptr noundef)")
		e.usedGetenv = true
	}
}

func (e *Emitter) ensureCalloc() {
	if !e.usedCalloc {
		e.emitGlobal("declare ptr @calloc(i64 noundef, i64 noundef)")
		e.usedCalloc = true
	}
}

func (e *Emitter) ensureRealloc() {
	if !e.usedRealloc {
		e.emitGlobal("declare ptr @realloc(ptr noundef, i64 noundef)")
		e.usedRealloc = true
	}
}

func (e *Emitter) ensureMemmove() {
	if !e.usedMemmove {
		e.emitGlobal("declare ptr @memmove(ptr noundef, ptr noundef, i64 noundef)")
		e.usedMemmove = true
	}
}

func (e *Emitter) ensureStrlen() {
	if !e.usedStrlen {
		e.emitGlobal("declare i64 @strlen(ptr noundef)")
		e.usedStrlen = true
	}
}

func (e *Emitter) ensureMemcpy() {
	if !e.usedMemcpy {
		e.emitGlobal("declare ptr @memcpy(ptr noundef, ptr noundef, i64 noundef)")
		e.usedMemcpy = true
	}
}

func (e *Emitter) ensureMemset() {
	if !e.usedMemset {
		e.emitGlobal("declare ptr @memset(ptr noundef, i32 noundef, i64 noundef)")
		e.usedMemset = true
	}
}

func (e *Emitter) ensureStrcmp() {
	if !e.usedStrcmp {
		e.emitGlobal("declare i32 @strcmp(ptr noundef, ptr noundef)")
		e.usedStrcmp = true
	}
}

func (e *Emitter) ensureSprintf() {
	if !e.usedSprintf {
		e.emitGlobal("declare i32 @sprintf(ptr noundef, ptr noundef, ...)")
		e.usedSprintf = true
	}
}

func (e *Emitter) ensureStrstr() {
	if !e.usedStrstr {
		e.emitGlobal("declare ptr @strstr(ptr noundef, ptr noundef)")
		e.usedStrstr = true
	}
}

func (e *Emitter) ensureStrncmp() {
	if !e.usedStrncmp {
		e.emitGlobal("declare i32 @strncmp(ptr noundef, ptr noundef, i64 noundef)")
		e.usedStrncmp = true
	}
}

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

func (e *Emitter) ensureAtoll() {
	if !e.usedAtoll {
		e.emitGlobal("declare i64 @atoll(ptr noundef)")
		e.usedAtoll = true
	}
}

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

// ensureAnyEq declares __kml_any_eq: compares two boxed any/unknown values
// { i8 tag, i64 payload } for equality (backs === / !==). Equal-tag pairs
// compare directly per tag's meaning (string payloads are ptrtoint'd string
// pointers, so string/string compares via strcmp, not pointer identity;
// object/object compares by pointer, matching JS reference equality); an
// int/float tag mismatch (either order) is still a real numeric comparison,
// converting the int side to double first; any other tag mismatch is false.
// Tags: 0=int, 1=float, 2=string, 3=boolean, 4=null, 5=undefined, 6=object.
func (e *Emitter) ensureAnyEq() {
	if e.usedAnyEq {
		return
	}
	e.usedAnyEq = true
	e.ensureStrcmp()
	e.emitGlobal(`
define i1 @__kml_any_eq({ i8, i64 } %a, { i8, i64 } %b) {
entry:
  %tagA = extractvalue { i8, i64 } %a, 0
  %payA = extractvalue { i8, i64 } %a, 1
  %tagB = extractvalue { i8, i64 } %b, 0
  %payB = extractvalue { i8, i64 } %b, 1
  %same_tag = icmp eq i8 %tagA, %tagB
  br i1 %same_tag, label %same, label %cross_check
cross_check:
  %a_is_int = icmp eq i8 %tagA, 0
  %a_is_float = icmp eq i8 %tagA, 1
  %b_is_int = icmp eq i8 %tagB, 0
  %b_is_float = icmp eq i8 %tagB, 1
  %int_float = and i1 %a_is_int, %b_is_float
  %float_int = and i1 %a_is_float, %b_is_int
  %is_cross_numeric = or i1 %int_float, %float_int
  br i1 %is_cross_numeric, label %cross_numeric, label %not_equal
cross_numeric:
  %a_from_int = sitofp i64 %payA to double
  %a_from_float = bitcast i64 %payA to double
  %a_double = select i1 %a_is_int, double %a_from_int, double %a_from_float
  %b_from_int = sitofp i64 %payB to double
  %b_from_float = bitcast i64 %payB to double
  %b_double = select i1 %b_is_int, double %b_from_int, double %b_from_float
  %cross_eq = fcmp oeq double %a_double, %b_double
  ret i1 %cross_eq
same:
  %is_int = icmp eq i8 %tagA, 0
  br i1 %is_int, label %cmp_int, label %check_float
cmp_int:
  %int_eq = icmp eq i64 %payA, %payB
  ret i1 %int_eq
check_float:
  %is_float = icmp eq i8 %tagA, 1
  br i1 %is_float, label %cmp_float, label %check_string
cmp_float:
  %fa = bitcast i64 %payA to double
  %fb = bitcast i64 %payB to double
  %float_eq = fcmp oeq double %fa, %fb
  ret i1 %float_eq
check_string:
  %is_string = icmp eq i8 %tagA, 2
  br i1 %is_string, label %cmp_string, label %check_bool
cmp_string:
  %sa = inttoptr i64 %payA to ptr
  %sb = inttoptr i64 %payB to ptr
  %scmp = call i32 @strcmp(ptr %sa, ptr %sb)
  %string_eq = icmp eq i32 %scmp, 0
  ret i1 %string_eq
check_bool:
  %is_bool = icmp eq i8 %tagA, 3
  br i1 %is_bool, label %cmp_bool, label %check_null_undef
cmp_bool:
  %bool_eq = icmp eq i64 %payA, %payB
  ret i1 %bool_eq
check_null_undef:
  %is_null = icmp eq i8 %tagA, 4
  %is_undef = icmp eq i8 %tagA, 5
  %is_null_or_undef = or i1 %is_null, %is_undef
  br i1 %is_null_or_undef, label %ret_true, label %check_object
check_object:
  %is_object = icmp eq i8 %tagA, 6
  br i1 %is_object, label %cmp_object, label %not_equal
cmp_object:
  %oa = inttoptr i64 %payA to ptr
  %ob = inttoptr i64 %payB to ptr
  %obj_eq = icmp eq ptr %oa, %ob
  ret i1 %obj_eq
ret_true:
  ret i1 true
not_equal:
  ret i1 false
}`)
}

// ensureDateNow declares __kml_date_now: the current time in milliseconds
// since the Unix epoch, via clock_gettime(CLOCK_REALTIME, ...). Uses
// clock_gettime/struct timespec rather than gettimeofday/struct timeval
// specifically because struct timespec's two fields (time_t tv_sec, long
// tv_nsec) are BOTH 64-bit on every LP64 target this compiler supports
// (macOS ARM64, Linux x86-64/aarch64) — struct timeval's tv_usec is a
// 32-bit suseconds_t on macOS/BSD but 64-bit on Linux, so hardcoding a
// {i64,i64} GEP layout for it would silently misread on macOS.
// CLOCK_REALTIME is defined as 0 on both platforms, so it's safe to inline.
func (e *Emitter) ensureClockGettime() {
	if e.usedClockGettime {
		return
	}
	e.usedClockGettime = true
	e.emitGlobal("declare i32 @clock_gettime(i32 noundef, ptr noundef)")
}

// monotonicClockID returns the CLOCK_MONOTONIC numeric value for whatever
// OS is running this compiler right now (and will therefore also run clang
// moments later — this project doesn't cross-compile). Verified directly
// against the system header rather than trusted from memory: Darwin's is 6
// (confirmed in <_time.h>); glibc's is the well-known, decades-stable
// kernel UAPI value 1. The same class of platform check as errnoAccessor.
func monotonicClockID() string {
	if runtime.GOOS == "darwin" {
		return "6"
	}
	return "1"
}

func (e *Emitter) ensureDateNow() {
	if e.usedDateNow {
		return
	}
	e.usedDateNow = true
	e.ensureClockGettime()
	e.emitGlobal(`
define i64 @__kml_date_now() {
entry:
  %ts = alloca { i64, i64 }, align 8
  %r = call i32 @clock_gettime(i32 0, ptr %ts)
  %sec_p = getelementptr { i64, i64 }, ptr %ts, i32 0, i32 0
  %nsec_p = getelementptr { i64, i64 }, ptr %ts, i32 0, i32 1
  %sec = load i64, ptr %sec_p, align 8
  %nsec = load i64, ptr %nsec_p, align 8
  %sec_ms = mul i64 %sec, 1000
  %nsec_ms = sdiv i64 %nsec, 1000000
  %total = add i64 %sec_ms, %nsec_ms
  ret i64 %total
}`)
}

// ensurePerformanceNow declares __kml_performance_now: a CLOCK_MONOTONIC
// timestamp in milliseconds, as a double with sub-millisecond precision
// (real performance.now() is spec'd relative to a "time origin," typically
// process/page start — this compiler has no such fixed origin concept, so
// it returns the raw monotonic clock reading instead; fine for the common
// use of subtracting two calls to measure elapsed time, a documented
// simplification for anything expecting an absolute origin-relative value).
func (e *Emitter) ensurePerformanceNow() {
	if e.usedPerformanceNow {
		return
	}
	e.usedPerformanceNow = true
	e.ensureClockGettime()
	e.emitGlobal(fmt.Sprintf(`
define double @__kml_performance_now() {
entry:
  %%ts = alloca { i64, i64 }, align 8
  %%r = call i32 @clock_gettime(i32 %s, ptr %%ts)
  %%sec_p = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 0
  %%nsec_p = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 1
  %%sec = load i64, ptr %%sec_p, align 8
  %%nsec = load i64, ptr %%nsec_p, align 8
  %%sec_f = sitofp i64 %%sec to double
  %%nsec_f = sitofp i64 %%nsec to double
  %%sec_ms = fmul double %%sec_f, 1000.0
  %%nsec_ms = fdiv double %%nsec_f, 1000000.0
  %%total = fadd double %%sec_ms, %%nsec_ms
  ret double %%total
}`, monotonicClockID()))
}

// ensureDateDecompose declares __kml_date_decompose: converts a milliseconds-
// since-epoch i64 into its UTC calendar fields (year, month[0-11], day,
// weekday[0=Sun..6=Sat], hour, minute, second, millisecond) via gmtime(),
// returned as an { i64 x 8 } aggregate in that order. Deliberately UTC (not
// local time) so output is deterministic across machines/CI regardless of
// timezone — see docs/adr for the Date ADR. struct tm's first 7 fields
// (tm_sec, tm_min, tm_hour, tm_mday, tm_mon, tm_year, tm_wday) are `int`
// (i32) in that exact order on both glibc and Darwin/BSD, the standard
// POSIX layout — reading only those (not the platform-varying tail fields
// like tm_gmtoff) keeps this portable across this compiler's targets.
func (e *Emitter) ensureDateDecompose() {
	if e.usedDateDecompose {
		return
	}
	e.usedDateDecompose = true
	e.emitGlobal("declare ptr @gmtime(ptr noundef)")
	e.emitGlobal(`
define { i64, i64, i64, i64, i64, i64, i64, i64 } @__kml_date_decompose(i64 %ms) {
entry:
  %secs = sdiv i64 %ms, 1000
  %millis_raw = srem i64 %ms, 1000
  %millis_neg = icmp slt i64 %millis_raw, 0
  %millis_adj = add i64 %millis_raw, 1000
  %millis = select i1 %millis_neg, i64 %millis_adj, i64 %millis_raw
  %secs_adj = select i1 %millis_neg, i64 -1, i64 0
  %secs_final = add i64 %secs, %secs_adj
  %tbuf = alloca i64, align 8
  store i64 %secs_final, ptr %tbuf, align 8
  %tmptr = call ptr @gmtime(ptr %tbuf)
  %sec_p = getelementptr { i32, i32, i32, i32, i32, i32, i32 }, ptr %tmptr, i32 0, i32 0
  %min_p = getelementptr { i32, i32, i32, i32, i32, i32, i32 }, ptr %tmptr, i32 0, i32 1
  %hour_p = getelementptr { i32, i32, i32, i32, i32, i32, i32 }, ptr %tmptr, i32 0, i32 2
  %mday_p = getelementptr { i32, i32, i32, i32, i32, i32, i32 }, ptr %tmptr, i32 0, i32 3
  %mon_p = getelementptr { i32, i32, i32, i32, i32, i32, i32 }, ptr %tmptr, i32 0, i32 4
  %year_p = getelementptr { i32, i32, i32, i32, i32, i32, i32 }, ptr %tmptr, i32 0, i32 5
  %wday_p = getelementptr { i32, i32, i32, i32, i32, i32, i32 }, ptr %tmptr, i32 0, i32 6
  %sec_i32 = load i32, ptr %sec_p, align 4
  %min_i32 = load i32, ptr %min_p, align 4
  %hour_i32 = load i32, ptr %hour_p, align 4
  %mday_i32 = load i32, ptr %mday_p, align 4
  %mon_i32 = load i32, ptr %mon_p, align 4
  %year_i32 = load i32, ptr %year_p, align 4
  %wday_i32 = load i32, ptr %wday_p, align 4
  %sec64 = sext i32 %sec_i32 to i64
  %min64 = sext i32 %min_i32 to i64
  %hour64 = sext i32 %hour_i32 to i64
  %mday64 = sext i32 %mday_i32 to i64
  %mon64 = sext i32 %mon_i32 to i64
  %year64_raw = sext i32 %year_i32 to i64
  %year64 = add i64 %year64_raw, 1900
  %wday64 = sext i32 %wday_i32 to i64
  %r0 = insertvalue { i64, i64, i64, i64, i64, i64, i64, i64 } undef, i64 %year64, 0
  %r1 = insertvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %r0, i64 %mon64, 1
  %r2 = insertvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %r1, i64 %mday64, 2
  %r3 = insertvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %r2, i64 %wday64, 3
  %r4 = insertvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %r3, i64 %hour64, 4
  %r5 = insertvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %r4, i64 %min64, 5
  %r6 = insertvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %r5, i64 %sec64, 6
  %r7 = insertvalue { i64, i64, i64, i64, i64, i64, i64, i64 } %r6, i64 %millis, 7
  ret { i64, i64, i64, i64, i64, i64, i64, i64 } %r7
}`)
}

// ensureDateNameTables declares two global arrays of string pointers,
// indexed by the weekday[0-6]/month[0-11] fields __kml_date_decompose
// returns — a runtime lookup, since the index is only known at run time
// (not a Go-side compile-time switch), used by Date's toDateString.
func (e *Emitter) ensureDateNameTables() {
	if e.usedDateNameTables {
		return
	}
	e.usedDateNameTables = true
	wdayInit := make([]string, len(weekdayAbbrevs))
	for i, name := range weekdayAbbrevs {
		wdayInit[i] = "ptr " + e.internString(name)
	}
	monthInit := make([]string, len(monthAbbrevs))
	for i, name := range monthAbbrevs {
		monthInit[i] = "ptr " + e.internString(name)
	}
	e.emitGlobal(fmt.Sprintf("@__kml_weekday_names = private unnamed_addr constant [7 x ptr] [%s]", strings.Join(wdayInit, ", ")))
	e.emitGlobal(fmt.Sprintf("@__kml_month_names = private unnamed_addr constant [12 x ptr] [%s]", strings.Join(monthInit, ", ")))
}

func (e *Emitter) ensureSscanf() {
	if e.usedSscanf {
		return
	}
	e.usedSscanf = true
	e.emitGlobal("declare i32 @sscanf(ptr noundef, ptr noundef, ...)")
}

// ensureDaysFromCivil declares __kml_days_from_civil: days since the Unix
// epoch (1970-01-01) for a given proleptic-Gregorian (year, month[1-12],
// day[1-31]), via Howard Hinnant's days_from_civil algorithm
// (http://howardhinnant.github.io/date_algorithms.html). Chosen over calling
// libc's timegm() specifically to avoid needing a caller-allocated
// struct-tm-sized buffer whose exact byte layout/size varies by platform
// (glibc appends tm_gmtoff/tm_zone; so does Darwin, but not necessarily at
// the same offsets) — this is pure integer arithmetic, so it's portable by
// construction and works for any year, including pre-1970 (negative
// timestamps).
func (e *Emitter) ensureDaysFromCivil() {
	if e.usedDaysFromCivil {
		return
	}
	e.usedDaysFromCivil = true
	e.emitGlobal(`
define i64 @__kml_days_from_civil(i64 %y0, i64 %m, i64 %d) {
entry:
  %mle2 = icmp sle i64 %m, 2
  %madj = select i1 %mle2, i64 1, i64 0
  %y = sub i64 %y0, %madj
  %yneg = icmp slt i64 %y, 0
  %yminus399 = sub i64 %y, 399
  %era_base = select i1 %yneg, i64 %yminus399, i64 %y
  %era = sdiv i64 %era_base, 400
  %era400 = mul i64 %era, 400
  %yoe = sub i64 %y, %era400
  %mgt2 = icmp sgt i64 %m, 2
  %madj2 = select i1 %mgt2, i64 -3, i64 9
  %mplus = add i64 %m, %madj2
  %mul153 = mul i64 153, %mplus
  %plus2 = add i64 %mul153, 2
  %div5 = sdiv i64 %plus2, 5
  %dm1 = sub i64 %d, 1
  %doy = add i64 %div5, %dm1
  %yoe365 = mul i64 %yoe, 365
  %yoediv4 = sdiv i64 %yoe, 4
  %yoediv100 = sdiv i64 %yoe, 100
  %t1 = add i64 %yoe365, %yoediv4
  %t2 = sub i64 %t1, %yoediv100
  %doe = add i64 %t2, %doy
  %era146097 = mul i64 %era, 146097
  %sum = add i64 %era146097, %doe
  %result = sub i64 %sum, 719468
  ret i64 %result
}`)
}

// ensureDateParse declares __kml_date_parse: parses an ISO 8601 UTC date
// string into milliseconds since epoch, trying (in order) the full
// "YYYY-MM-DDTHH:mm:ss.sssZ" shape (exactly what toISOString produces), the
// same shape without milliseconds, and a bare "YYYY-MM-DD" date (UTC
// midnight, matching real JS's date-only parsing rule). Anything else
// returns -1 — real JS's Date.parse returns NaN for unparseable input, but
// this compiler's Date is a plain i64 with no NaN representation, so -1 is
// used as the documented sentinel instead.
// ensureDateCompose declares __kml_date_compose: the inverse of
// __kml_date_decompose — takes calendar fields (year, month[1-12, note:
// 1-indexed here, unlike the 0-indexed month __kml_date_decompose returns],
// day, hour, min, sec, millis) and returns milliseconds since epoch. Shared
// by both Date.parse (ADR-00015) and the Date setters (setFullYear, etc.,
// ADR-00016) so the calendar-to-timestamp math exists in exactly one place.
func (e *Emitter) ensureDateCompose() {
	if e.usedDateCompose {
		return
	}
	e.usedDateCompose = true
	e.ensureDaysFromCivil()
	e.emitGlobal(`
define i64 @__kml_date_compose(i64 %year, i64 %month, i64 %day, i64 %hour, i64 %min, i64 %sec, i64 %msec) {
entry:
  %days = call i64 @__kml_days_from_civil(i64 %year, i64 %month, i64 %day)
  %daysecs = mul i64 %days, 86400
  %hoursecs = mul i64 %hour, 3600
  %minsecs = mul i64 %min, 60
  %t1 = add i64 %daysecs, %hoursecs
  %t2 = add i64 %t1, %minsecs
  %totalsecs = add i64 %t2, %sec
  %totalms1 = mul i64 %totalsecs, 1000
  %totalms = add i64 %totalms1, %msec
  ret i64 %totalms
}`)
}

// ensureDateParse declares __kml_date_parse. Tries, in order (most specific
// first): full ISO with milliseconds and a "+HH:MM"/"-HH:MM" offset; the
// same without milliseconds; the plain "...Z" (UTC) forms with and without
// milliseconds (ADR-00015); and a bare "YYYY-MM-DD" date. The offset
// patterns MUST be tried before the "Z" patterns: sscanf's return value only
// counts successfully assigned %-conversions, not whether trailing literal
// characters (like "Z") matched, so an offset string like
// "...20.000+02:00" fed to the "Z" pattern would still report all 7 numeric
// fields as matched even though the literal "Z" never matched the "+" — a
// real bug found while implementing this (confirmed: the pre-offset-support
// parser silently returned the wrong value for such input, treating the
// local time as if it were already UTC). Trying the higher-specificity
// (higher expected field count) offset patterns first, and requiring an
// exact field-count match, avoids that ambiguity entirely — a genuine "Z"
// string can never satisfy an offset pattern's field count (matching stops
// at the literal '+'/'-', which isn't present), so it correctly falls
// through.
//
// The offset sign is baked into which of the four offset patterns matched
// (a literal '+' or '-' in the format string) rather than relying on
// sscanf's %d parsing a signed hour value — a "-00:30" offset (zero hour,
// negative sign) would otherwise silently lose its sign, since -0 and 0 are
// the same integer. Structural per-pattern sign tracking sidesteps that.
func (e *Emitter) ensureDateParse() {
	if e.usedDateParse {
		return
	}
	e.usedDateParse = true
	e.ensureSscanf()
	e.ensureDateCompose()
	fmtPlusMillis := e.internString("%d-%d-%dT%d:%d:%d.%d+%d:%d")
	fmtMinusMillis := e.internString("%d-%d-%dT%d:%d:%d.%d-%d:%d")
	fmtPlusNoMillis := e.internString("%d-%d-%dT%d:%d:%d+%d:%d")
	fmtMinusNoMillis := e.internString("%d-%d-%dT%d:%d:%d-%d:%d")
	fmtFull := e.internString("%d-%d-%dT%d:%d:%d.%dZ")
	fmtNoMillis := e.internString("%d-%d-%dT%d:%d:%dZ")
	fmtDateOnly := e.internString("%d-%d-%d")
	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_date_parse(ptr %%str) {
entry:
  %%year_a = alloca i32, align 4
  %%month_a = alloca i32, align 4
  %%day_a = alloca i32, align 4
  %%hour_a = alloca i32, align 4
  %%min_a = alloca i32, align 4
  %%sec_a = alloca i32, align 4
  %%msec_a = alloca i32, align 4
  %%offh_a = alloca i32, align 4
  %%offm_a = alloca i32, align 4
  %%offset_ms_a = alloca i64, align 8
  store i32 0, ptr %%hour_a, align 4
  store i32 0, ptr %%min_a, align 4
  store i32 0, ptr %%sec_a, align 4
  store i32 0, ptr %%msec_a, align 4
  store i64 0, ptr %%offset_ms_a, align 8

  %%noff1 = call i32 (ptr, ptr, ...) @sscanf(ptr %%str, ptr %s, ptr %%year_a, ptr %%month_a, ptr %%day_a, ptr %%hour_a, ptr %%min_a, ptr %%sec_a, ptr %%msec_a, ptr %%offh_a, ptr %%offm_a)
  %%offok1 = icmp eq i32 %%noff1, 9
  br i1 %%offok1, label %%off_plus_ms, label %%try_off_minus_ms

off_plus_ms:
  %%offh_ld1 = load i32, ptr %%offh_a, align 4
  %%offm_ld1 = load i32, ptr %%offm_a, align 4
  %%offh64_1 = sext i32 %%offh_ld1 to i64
  %%offm64_1 = sext i32 %%offm_ld1 to i64
  %%offmin_1 = mul i64 %%offh64_1, 60
  %%offmintot_1 = add i64 %%offmin_1, %%offm64_1
  %%offsec_1 = mul i64 %%offmintot_1, 60
  %%offms_1 = mul i64 %%offsec_1, 1000
  store i64 %%offms_1, ptr %%offset_ms_a, align 8
  br label %%compute

try_off_minus_ms:
  %%noff2 = call i32 (ptr, ptr, ...) @sscanf(ptr %%str, ptr %s, ptr %%year_a, ptr %%month_a, ptr %%day_a, ptr %%hour_a, ptr %%min_a, ptr %%sec_a, ptr %%msec_a, ptr %%offh_a, ptr %%offm_a)
  %%offok2 = icmp eq i32 %%noff2, 9
  br i1 %%offok2, label %%off_minus_ms, label %%try_off_plus_s

off_minus_ms:
  %%offh_ld2 = load i32, ptr %%offh_a, align 4
  %%offm_ld2 = load i32, ptr %%offm_a, align 4
  %%offh64_2 = sext i32 %%offh_ld2 to i64
  %%offm64_2 = sext i32 %%offm_ld2 to i64
  %%offmin_2 = mul i64 %%offh64_2, 60
  %%offmintot_2 = add i64 %%offmin_2, %%offm64_2
  %%offsec_2 = mul i64 %%offmintot_2, 60
  %%offms_2 = mul i64 %%offsec_2, 1000
  %%negoffms_2 = sub i64 0, %%offms_2
  store i64 %%negoffms_2, ptr %%offset_ms_a, align 8
  br label %%compute

try_off_plus_s:
  store i32 0, ptr %%msec_a, align 4
  %%noff3 = call i32 (ptr, ptr, ...) @sscanf(ptr %%str, ptr %s, ptr %%year_a, ptr %%month_a, ptr %%day_a, ptr %%hour_a, ptr %%min_a, ptr %%sec_a, ptr %%offh_a, ptr %%offm_a)
  %%offok3 = icmp eq i32 %%noff3, 8
  br i1 %%offok3, label %%off_plus_s, label %%try_off_minus_s

off_plus_s:
  %%offh_ld3 = load i32, ptr %%offh_a, align 4
  %%offm_ld3 = load i32, ptr %%offm_a, align 4
  %%offh64_3 = sext i32 %%offh_ld3 to i64
  %%offm64_3 = sext i32 %%offm_ld3 to i64
  %%offmin_3 = mul i64 %%offh64_3, 60
  %%offmintot_3 = add i64 %%offmin_3, %%offm64_3
  %%offsec_3 = mul i64 %%offmintot_3, 60
  %%offms_3 = mul i64 %%offsec_3, 1000
  store i64 %%offms_3, ptr %%offset_ms_a, align 8
  br label %%compute

try_off_minus_s:
  store i32 0, ptr %%msec_a, align 4
  %%noff4 = call i32 (ptr, ptr, ...) @sscanf(ptr %%str, ptr %s, ptr %%year_a, ptr %%month_a, ptr %%day_a, ptr %%hour_a, ptr %%min_a, ptr %%sec_a, ptr %%offh_a, ptr %%offm_a)
  %%offok4 = icmp eq i32 %%noff4, 8
  br i1 %%offok4, label %%off_minus_s, label %%try_z_ms

off_minus_s:
  %%offh_ld4 = load i32, ptr %%offh_a, align 4
  %%offm_ld4 = load i32, ptr %%offm_a, align 4
  %%offh64_4 = sext i32 %%offh_ld4 to i64
  %%offm64_4 = sext i32 %%offm_ld4 to i64
  %%offmin_4 = mul i64 %%offh64_4, 60
  %%offmintot_4 = add i64 %%offmin_4, %%offm64_4
  %%offsec_4 = mul i64 %%offmintot_4, 60
  %%offms_4 = mul i64 %%offsec_4, 1000
  %%negoffms_4 = sub i64 0, %%offms_4
  store i64 %%negoffms_4, ptr %%offset_ms_a, align 8
  br label %%compute

try_z_ms:
  store i32 0, ptr %%hour_a, align 4
  store i32 0, ptr %%min_a, align 4
  store i32 0, ptr %%sec_a, align 4
  store i32 0, ptr %%msec_a, align 4
  %%n1 = call i32 (ptr, ptr, ...) @sscanf(ptr %%str, ptr %s, ptr %%year_a, ptr %%month_a, ptr %%day_a, ptr %%hour_a, ptr %%min_a, ptr %%sec_a, ptr %%msec_a)
  %%ok1 = icmp eq i32 %%n1, 7
  br i1 %%ok1, label %%compute, label %%try_z_s

try_z_s:
  store i32 0, ptr %%hour_a, align 4
  store i32 0, ptr %%min_a, align 4
  store i32 0, ptr %%sec_a, align 4
  store i32 0, ptr %%msec_a, align 4
  %%n2 = call i32 (ptr, ptr, ...) @sscanf(ptr %%str, ptr %s, ptr %%year_a, ptr %%month_a, ptr %%day_a, ptr %%hour_a, ptr %%min_a, ptr %%sec_a)
  %%ok2 = icmp eq i32 %%n2, 6
  br i1 %%ok2, label %%compute, label %%try_date

try_date:
  store i32 0, ptr %%hour_a, align 4
  store i32 0, ptr %%min_a, align 4
  store i32 0, ptr %%sec_a, align 4
  store i32 0, ptr %%msec_a, align 4
  %%n3 = call i32 (ptr, ptr, ...) @sscanf(ptr %%str, ptr %s, ptr %%year_a, ptr %%month_a, ptr %%day_a)
  %%ok3 = icmp eq i32 %%n3, 3
  br i1 %%ok3, label %%compute, label %%invalid

invalid:
  ret i64 -1

compute:
  %%year32 = load i32, ptr %%year_a, align 4
  %%month32 = load i32, ptr %%month_a, align 4
  %%day32 = load i32, ptr %%day_a, align 4
  %%hour32 = load i32, ptr %%hour_a, align 4
  %%min32 = load i32, ptr %%min_a, align 4
  %%sec32 = load i32, ptr %%sec_a, align 4
  %%msec32 = load i32, ptr %%msec_a, align 4
  %%year = sext i32 %%year32 to i64
  %%month = sext i32 %%month32 to i64
  %%day = sext i32 %%day32 to i64
  %%hour = sext i32 %%hour32 to i64
  %%min = sext i32 %%min32 to i64
  %%sec = sext i32 %%sec32 to i64
  %%msec = sext i32 %%msec32 to i64
  %%localms = call i64 @__kml_date_compose(i64 %%year, i64 %%month, i64 %%day, i64 %%hour, i64 %%min, i64 %%sec, i64 %%msec)
  %%offset_ms = load i64, ptr %%offset_ms_a, align 8
  %%totalms = sub i64 %%localms, %%offset_ms
  ret i64 %%totalms
}`, fmtPlusMillis, fmtMinusMillis, fmtPlusNoMillis, fmtMinusNoMillis, fmtFull, fmtNoMillis, fmtDateOnly))
}

func (e *Emitter) ensureMathFuncs() {
	if e.usedMathFuncs {
		return
	}
	e.usedMathFuncs = true
	// On Linux these symbols live in libm, linked separately from libc — omitted
	// on macOS too since libSystem folds libm in and -lm is still accepted there
	// as a standard no-op flag, so this doesn't need a runtime.GOOS branch.
	e.requireLink("m")
	e.emitGlobal("declare double @floor(double noundef)")
	e.emitGlobal("declare double @ceil(double noundef)")
	e.emitGlobal("declare double @round(double noundef)")
	e.emitGlobal("declare double @trunc(double noundef)")
	e.emitGlobal("declare double @fabs(double noundef)")
	e.emitGlobal("declare double @sqrt(double noundef)")
	e.emitGlobal("declare double @pow(double noundef, double noundef)")
	e.emitGlobal("declare double @log(double noundef)")
	e.emitGlobal("declare double @log2(double noundef)")
	e.emitGlobal("declare double @log10(double noundef)")
	e.emitGlobal("declare double @sin(double noundef)")
	e.emitGlobal("declare double @cos(double noundef)")
	e.emitGlobal("declare double @tan(double noundef)")
	e.emitGlobal("declare double @hypot(double noundef, double noundef)")
	e.emitGlobal("declare double @asin(double noundef)")
	e.emitGlobal("declare double @acos(double noundef)")
	e.emitGlobal("declare double @atan(double noundef)")
	e.emitGlobal("declare double @atan2(double noundef, double noundef)")
	e.emitGlobal("declare double @sinh(double noundef)")
	e.emitGlobal("declare double @cosh(double noundef)")
	e.emitGlobal("declare double @tanh(double noundef)")
	e.emitGlobal("declare double @cbrt(double noundef)")
	e.emitGlobal("declare double @expm1(double noundef)")
	e.emitGlobal("declare double @log1p(double noundef)")
}

func (e *Emitter) ensureArc4Random() {
	if !e.usedArc4Random {
		e.emitGlobal("declare i32 @arc4random()")
		e.usedArc4Random = true
	}
}

// ensureRandRandom emits a self-contained @__klain_math_random helper in LLVM IR
// that uses C89 rand()/srand()/time() — available on every libc — as the portable
// fallback for Math.random() on non-BSD platforms.
func (e *Emitter) ensureRandRandom() {
	if e.usedArc4Random { // reuse flag slot; only one path is ever taken
		return
	}
	e.usedArc4Random = true // mark as emitted so we don't emit it twice

	// C89 declarations needed by the helper.
	e.emitGlobal("declare i32  @rand()")
	e.emitGlobal("declare void @srand(i32 noundef)")
	e.emitGlobal("declare i64  @time(ptr)")

	// One-time seeded flag (thread-unsafe but fine for single-threaded scripts).
	e.emitGlobal("@__klain_rand_seeded = private global i1 false, align 1")

	// The helper function itself — defined fully in IR, no external symbols beyond the above.
	e.emitGlobal(`define private double @__klain_math_random() {
entry:
  %seeded = load i1, ptr @__klain_rand_seeded, align 1
  br i1 %seeded, label %gen, label %do_seed
do_seed:
  %t = call i64 @time(ptr null)
  %t32 = trunc i64 %t to i32
  call void @srand(i32 %t32)
  store i1 true, ptr @__klain_rand_seeded, align 1
  br label %gen
gen:
  %r = call i32 @rand()
  %rf = sitofp i32 %r to double
  %result = fdiv double %rf, 2147483647.0
  ret double %result
}`)
}

func (e *Emitter) ensureStrtoll() {
	if !e.usedStrtoll {
		e.emitGlobal("declare i64 @strtoll(ptr noundef, ptr noundef, i32 noundef)")
		e.usedStrtoll = true
	}
}

func (e *Emitter) ensureStrtod() {
	if !e.usedStrtod {
		e.emitGlobal("declare double @strtod(ptr noundef, ptr noundef)")
		e.usedStrtod = true
	}
}

func (e *Emitter) ensureGroupMapHelpers() {
	if e.usedGroupMapHelpers {
		return
	}
	e.usedGroupMapHelpers = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureStrcmp()
	e.ensureMemcpy()
	// Group-map header layout (48 bytes):
	//   +0  i64 count  — number of distinct keys
	//   +8  i64 cap    — capacity of key/bucket arrays
	//   +16 ptr keys   — char** (key strings)
	//   +24 ptr bptrs  — ptr* (bucket data arrays, each is i64*)
	//   +32 ptr lens   — i64* (element count per bucket)
	//   +40 ptr caps   — i64* (capacity per bucket)
	e.emitGlobal(`
define ptr @__kml_gmap_create() {
entry:
  %h = call ptr @malloc(i64 48)
  store i64 0, ptr %h, align 8
  %cap_p = getelementptr i8, ptr %h, i64 8
  store i64 8, ptr %cap_p, align 8
  %keys = call ptr @malloc(i64 64)
  %keys_p = getelementptr i8, ptr %h, i64 16
  store ptr %keys, ptr %keys_p, align 8
  %bptrs = call ptr @malloc(i64 64)
  %bptrs_p = getelementptr i8, ptr %h, i64 24
  store ptr %bptrs, ptr %bptrs_p, align 8
  %lens = call ptr @malloc(i64 64)
  %lens_p = getelementptr i8, ptr %h, i64 32
  store ptr %lens, ptr %lens_p, align 8
  %caps = call ptr @malloc(i64 64)
  %caps_p = getelementptr i8, ptr %h, i64 40
  store ptr %caps, ptr %caps_p, align 8
  ret ptr %h
}

define i64 @__kml_gmap_find_or_add(ptr %map, ptr %key) {
entry:
  %count = load i64, ptr %map, align 8
  %cap_p = getelementptr i8, ptr %map, i64 8
  %cap = load i64, ptr %cap_p, align 8
  %keys_p = getelementptr i8, ptr %map, i64 16
  %keys = load ptr, ptr %keys_p, align 8
  br label %scan
scan:
  %i = phi i64 [ 0, %entry ], [ %i_next, %scan_cont ]
  %scan_done = icmp sge i64 %i, %count
  br i1 %scan_done, label %add_key, label %scan_chk
scan_chk:
  %kslot = getelementptr ptr, ptr %keys, i64 %i
  %kptr = load ptr, ptr %kslot, align 8
  %cmp = call i32 @strcmp(ptr %kptr, ptr %key)
  %eq = icmp eq i32 %cmp, 0
  br i1 %eq, label %found, label %scan_cont
found:
  ret i64 %i
scan_cont:
  %i_next = add i64 %i, 1
  br label %scan
add_key:
  %need_grow = icmp sge i64 %count, %cap
  br i1 %need_grow, label %grow, label %do_add
grow:
  %new_cap = mul i64 %cap, 2
  %new_bytes = mul i64 %new_cap, 8
  %old_keys = load ptr, ptr %keys_p, align 8
  %nkeys = call ptr @realloc(ptr %old_keys, i64 %new_bytes)
  store ptr %nkeys, ptr %keys_p, align 8
  %bptrs_p1 = getelementptr i8, ptr %map, i64 24
  %old_bptrs = load ptr, ptr %bptrs_p1, align 8
  %nbptrs = call ptr @realloc(ptr %old_bptrs, i64 %new_bytes)
  store ptr %nbptrs, ptr %bptrs_p1, align 8
  %lens_p1 = getelementptr i8, ptr %map, i64 32
  %old_lens = load ptr, ptr %lens_p1, align 8
  %nlens = call ptr @realloc(ptr %old_lens, i64 %new_bytes)
  store ptr %nlens, ptr %lens_p1, align 8
  %caps_p1 = getelementptr i8, ptr %map, i64 40
  %old_caps = load ptr, ptr %caps_p1, align 8
  %ncaps = call ptr @realloc(ptr %old_caps, i64 %new_bytes)
  store ptr %ncaps, ptr %caps_p1, align 8
  store i64 %new_cap, ptr %cap_p, align 8
  br label %do_add
do_add:
  %keys2 = load ptr, ptr %keys_p, align 8
  %bptrs_p2 = getelementptr i8, ptr %map, i64 24
  %bptrs2 = load ptr, ptr %bptrs_p2, align 8
  %lens_p2 = getelementptr i8, ptr %map, i64 32
  %lens2 = load ptr, ptr %lens_p2, align 8
  %caps_p2 = getelementptr i8, ptr %map, i64 40
  %caps2 = load ptr, ptr %caps_p2, align 8
  %kslot2 = getelementptr ptr, ptr %keys2, i64 %count
  store ptr %key, ptr %kslot2, align 8
  %bdata = call ptr @malloc(i64 64)
  %bslot = getelementptr ptr, ptr %bptrs2, i64 %count
  store ptr %bdata, ptr %bslot, align 8
  %lslot = getelementptr i64, ptr %lens2, i64 %count
  store i64 0, ptr %lslot, align 8
  %cslot = getelementptr i64, ptr %caps2, i64 %count
  store i64 8, ptr %cslot, align 8
  %count1 = add i64 %count, 1
  store i64 %count1, ptr %map, align 8
  ret i64 %count
}

define void @__kml_gmap_append(ptr %map, i64 %idx, i64 %val) {
entry:
  %bptrs_p = getelementptr i8, ptr %map, i64 24
  %bptrs = load ptr, ptr %bptrs_p, align 8
  %lens_p = getelementptr i8, ptr %map, i64 32
  %lens = load ptr, ptr %lens_p, align 8
  %caps_p = getelementptr i8, ptr %map, i64 40
  %caps = load ptr, ptr %caps_p, align 8
  %lslot = getelementptr i64, ptr %lens, i64 %idx
  %len = load i64, ptr %lslot, align 8
  %cslot = getelementptr i64, ptr %caps, i64 %idx
  %cap = load i64, ptr %cslot, align 8
  %bslot = getelementptr ptr, ptr %bptrs, i64 %idx
  %bdata = load ptr, ptr %bslot, align 8
  %need_grow = icmp sge i64 %len, %cap
  br i1 %need_grow, label %grow, label %do_append
grow:
  %new_cap = mul i64 %cap, 2
  %new_bytes = mul i64 %new_cap, 8
  %new_bdata = call ptr @realloc(ptr %bdata, i64 %new_bytes)
  store ptr %new_bdata, ptr %bslot, align 8
  store i64 %new_cap, ptr %cslot, align 8
  br label %do_append
do_append:
  %bdata2 = load ptr, ptr %bslot, align 8
  %vslot = getelementptr i64, ptr %bdata2, i64 %len
  store i64 %val, ptr %vslot, align 8
  %len1 = add i64 %len, 1
  store i64 %len1, ptr %lslot, align 8
  ret void
}

define {ptr, i64} @__kml_gmap_get(ptr %map, ptr %key) {
entry:
  %count = load i64, ptr %map, align 8
  %keys_p = getelementptr i8, ptr %map, i64 16
  %keys = load ptr, ptr %keys_p, align 8
  br label %scan
scan:
  %i = phi i64 [ 0, %entry ], [ %i_next, %cont ]
  %done = icmp sge i64 %i, %count
  br i1 %done, label %not_found, label %chk
chk:
  %kslot = getelementptr ptr, ptr %keys, i64 %i
  %kptr = load ptr, ptr %kslot, align 8
  %cmp = call i32 @strcmp(ptr %kptr, ptr %key)
  %eq = icmp eq i32 %cmp, 0
  br i1 %eq, label %found, label %cont
found:
  %bptrs_p = getelementptr i8, ptr %map, i64 24
  %bptrs = load ptr, ptr %bptrs_p, align 8
  %bslot = getelementptr ptr, ptr %bptrs, i64 %i
  %bdata = load ptr, ptr %bslot, align 8
  %lens_p = getelementptr i8, ptr %map, i64 32
  %lens = load ptr, ptr %lens_p, align 8
  %lslot = getelementptr i64, ptr %lens, i64 %i
  %blen = load i64, ptr %lslot, align 8
  %r0 = insertvalue {ptr, i64} undef, ptr %bdata, 0
  %r1 = insertvalue {ptr, i64} %r0, i64 %blen, 1
  ret {ptr, i64} %r1
cont:
  %i_next = add i64 %i, 1
  br label %scan
not_found:
  %e0 = insertvalue {ptr, i64} undef, ptr null, 0
  %e1 = insertvalue {ptr, i64} %e0, i64 0, 1
  ret {ptr, i64} %e1
}

define {ptr, i64} @__kml_gmap_keys(ptr %map) {
entry:
  %count = load i64, ptr %map, align 8
  %keys_p = getelementptr i8, ptr %map, i64 16
  %keys = load ptr, ptr %keys_p, align 8
  %bytes = mul i64 %count, 8
  %arr = call ptr @malloc(i64 %bytes)
  call ptr @memcpy(ptr %arr, ptr %keys, i64 %bytes)
  %r0 = insertvalue {ptr, i64} undef, ptr %arr, 0
  %r1 = insertvalue {ptr, i64} %r0, i64 %count, 1
  ret {ptr, i64} %r1
}`)
}

// --- Sort helpers ---

func (e *Emitter) ensureQsort() {
	if !e.usedQsort {
		e.emitGlobal("declare void @qsort(ptr, i64, i64, ptr)")
		e.usedQsort = true
	}
}

func (e *Emitter) ensureSortClosGlobal() {
	if !e.usedSortClosGlobal {
		e.emitGlobal("@__kml_sort_clos = global ptr null")
		e.usedSortClosGlobal = true
	}
}

func (e *Emitter) ensureSortCmpI64() {
	if e.usedSortCmpI64 {
		return
	}
	e.usedSortCmpI64 = true
	e.emitGlobal(`define i32 @__kml_cmp_i64(ptr %pa, ptr %pb) {
  %a = load i64, ptr %pa, align 8
  %b = load i64, ptr %pb, align 8
  %lt = icmp slt i64 %a, %b
  %gt = icmp sgt i64 %a, %b
  %r0 = select i1 %lt, i32 -1, i32 0
  %r1 = select i1 %gt, i32 1, i32 %r0
  ret i32 %r1
}`)
}

func (e *Emitter) ensureSortCmpF64() {
	if e.usedSortCmpF64 {
		return
	}
	e.usedSortCmpF64 = true
	e.emitGlobal(`define i32 @__kml_cmp_f64(ptr %pa, ptr %pb) {
  %a = load double, ptr %pa, align 8
  %b = load double, ptr %pb, align 8
  %lt = fcmp olt double %a, %b
  %gt = fcmp ogt double %a, %b
  %r0 = select i1 %lt, i32 -1, i32 0
  %r1 = select i1 %gt, i32 1, i32 %r0
  ret i32 %r1
}`)
}

func (e *Emitter) ensureSortCmpStr() {
	if e.usedSortCmpStr {
		return
	}
	e.usedSortCmpStr = true
	e.ensureStrcmp()
	e.emitGlobal(`define i32 @__kml_cmp_str(ptr %pa, ptr %pb) {
  %a = load ptr, ptr %pa, align 8
  %b = load ptr, ptr %pb, align 8
  %r = call i32 @strcmp(ptr %a, ptr %b)
  ret i32 %r
}`)
}

// ensureSortTrampoline emits the trampoline and global closure ptr for custom sort.
// The trampoline loads the KML closure from the global, loads both elements, and
// calls the closure with (envptr, a, b), truncating the i64 result to i32.
func (e *Emitter) ensureSortTrampolineI64() {
	if e.usedSortTrampolineI64 {
		return
	}
	e.usedSortTrampolineI64 = true
	e.ensureSortClosGlobal()
	e.emitGlobal(`define i32 @__kml_sort_tramp_i64(ptr %pa, ptr %pb) {
  %clos = load ptr, ptr @__kml_sort_clos, align 8
  %a = load i64, ptr %pa, align 8
  %b = load i64, ptr %pb, align 8
  %fp_slot = getelementptr {ptr, ptr}, ptr %clos, i32 0, i32 0
  %fp = load ptr, ptr %fp_slot, align 8
  %ep_slot = getelementptr {ptr, ptr}, ptr %clos, i32 0, i32 1
  %ep = load ptr, ptr %ep_slot, align 8
  %r = call i64 (ptr, i64, i64) %fp(ptr %ep, i64 %a, i64 %b)
  %ri = trunc i64 %r to i32
  ret i32 %ri
}`)
}

func (e *Emitter) ensureSortTrampolineF64() {
	if e.usedSortTrampolineF64 {
		return
	}
	e.usedSortTrampolineF64 = true
	e.ensureSortClosGlobal()
	e.emitGlobal(`define i32 @__kml_sort_tramp_f64(ptr %pa, ptr %pb) {
  %clos = load ptr, ptr @__kml_sort_clos, align 8
  %a = load double, ptr %pa, align 8
  %b = load double, ptr %pb, align 8
  %fp_slot = getelementptr {ptr, ptr}, ptr %clos, i32 0, i32 0
  %fp = load ptr, ptr %fp_slot, align 8
  %ep_slot = getelementptr {ptr, ptr}, ptr %clos, i32 0, i32 1
  %ep = load ptr, ptr %ep_slot, align 8
  %r = call i64 (ptr, double, double) %fp(ptr %ep, double %a, double %b)
  %ri = trunc i64 %r to i32
  ret i32 %ri
}`)
}

func (e *Emitter) ensureSortTrampolineStr() {
	if e.usedSortTrampolineStr {
		return
	}
	e.usedSortTrampolineStr = true
	e.ensureSortClosGlobal()
	e.emitGlobal(`define i32 @__kml_sort_tramp_str(ptr %pa, ptr %pb) {
  %clos = load ptr, ptr @__kml_sort_clos, align 8
  %a = load ptr, ptr %pa, align 8
  %b = load ptr, ptr %pb, align 8
  %fp_slot = getelementptr {ptr, ptr}, ptr %clos, i32 0, i32 0
  %fp = load ptr, ptr %fp_slot, align 8
  %ep_slot = getelementptr {ptr, ptr}, ptr %clos, i32 0, i32 1
  %ep = load ptr, ptr %ep_slot, align 8
  %r = call i64 (ptr, ptr, ptr) %fp(ptr %ep, ptr %a, ptr %b)
  %ri = trunc i64 %r to i32
  ret i32 %ri
}`)
}

// --- Map / Set helpers ---
//
// Map header layout (32 bytes):
//   +0   i64  size  — current entry count
//   +8   i64  cap   — capacity (starts at 8)
//   +16  ptr  keys  — key array  (ptr[] for string keys, i64[] for number keys)
//   +24  ptr  vals  — value array (i64[])
//
// Set reuses the exact same layout; elements are stored as keys. vals is
// allocated but ignored. set.values() returns the keys array.

func (e *Emitter) ensureMapStrHelpers() {
	if e.usedMapStrHelpers {
		return
	}
	e.usedMapStrHelpers = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureStrcmp()
	e.ensureMemcpy()
	e.emitGlobal(`
define ptr @__kml_map_str_create() {
entry:
  %h = call ptr @malloc(i64 32)
  store i64 0, ptr %h, align 8
  %cap_p = getelementptr i8, ptr %h, i64 8
  store i64 8, ptr %cap_p, align 8
  %keys = call ptr @malloc(i64 64)
  %keys_p = getelementptr i8, ptr %h, i64 16
  store ptr %keys, ptr %keys_p, align 8
  %vals = call ptr @malloc(i64 64)
  %vals_p = getelementptr i8, ptr %h, i64 24
  store ptr %vals, ptr %vals_p, align 8
  ret ptr %h
}

define i64 @__kml_map_str_find(ptr %map, ptr %key) {
entry:
  %size = load i64, ptr %map, align 8
  %keys_p = getelementptr i8, ptr %map, i64 16
  %keys = load ptr, ptr %keys_p, align 8
  br label %scan
scan:
  %i = phi i64 [ 0, %entry ], [ %i_next, %cont ]
  %done = icmp sge i64 %i, %size
  br i1 %done, label %miss, label %chk
chk:
  %kslot = getelementptr ptr, ptr %keys, i64 %i
  %kptr = load ptr, ptr %kslot, align 8
  %cmp = call i32 @strcmp(ptr %kptr, ptr %key)
  %eq = icmp eq i32 %cmp, 0
  br i1 %eq, label %hit, label %cont
hit:
  ret i64 %i
cont:
  %i_next = add i64 %i, 1
  br label %scan
miss:
  ret i64 -1
}

define void @__kml_map_str_set(ptr %map, ptr %key, i64 %val) {
entry:
  %idx = call i64 @__kml_map_str_find(ptr %map, ptr %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %do_update, label %grow_chk
do_update:
  %vp0 = getelementptr i8, ptr %map, i64 24
  %va0 = load ptr, ptr %vp0, align 8
  %vs0 = getelementptr i64, ptr %va0, i64 %idx
  store i64 %val, ptr %vs0, align 8
  ret void
grow_chk:
  %size = load i64, ptr %map, align 8
  %cap_p = getelementptr i8, ptr %map, i64 8
  %cap = load i64, ptr %cap_p, align 8
  %need = icmp sge i64 %size, %cap
  br i1 %need, label %do_grow, label %do_ins
do_grow:
  %ncap = mul i64 %cap, 2
  %nb = mul i64 %ncap, 8
  %kp1 = getelementptr i8, ptr %map, i64 16
  %ok = load ptr, ptr %kp1, align 8
  %nk = call ptr @realloc(ptr %ok, i64 %nb)
  store ptr %nk, ptr %kp1, align 8
  %vp1 = getelementptr i8, ptr %map, i64 24
  %ov = load ptr, ptr %vp1, align 8
  %nv = call ptr @realloc(ptr %ov, i64 %nb)
  store ptr %nv, ptr %vp1, align 8
  store i64 %ncap, ptr %cap_p, align 8
  br label %do_ins
do_ins:
  %sz2 = load i64, ptr %map, align 8
  %kp2 = getelementptr i8, ptr %map, i64 16
  %ka2 = load ptr, ptr %kp2, align 8
  %ks = getelementptr ptr, ptr %ka2, i64 %sz2
  store ptr %key, ptr %ks, align 8
  %vp2 = getelementptr i8, ptr %map, i64 24
  %va2 = load ptr, ptr %vp2, align 8
  %vs = getelementptr i64, ptr %va2, i64 %sz2
  store i64 %val, ptr %vs, align 8
  %sz3 = add i64 %sz2, 1
  store i64 %sz3, ptr %map, align 8
  ret void
}

define i64 @__kml_map_str_get(ptr %map, ptr %key) {
entry:
  %idx = call i64 @__kml_map_str_find(ptr %map, ptr %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %hit, label %miss
hit:
  %vp = getelementptr i8, ptr %map, i64 24
  %va = load ptr, ptr %vp, align 8
  %vs = getelementptr i64, ptr %va, i64 %idx
  %v = load i64, ptr %vs, align 8
  ret i64 %v
miss:
  ret i64 0
}

define i1 @__kml_map_str_has(ptr %map, ptr %key) {
entry:
  %idx = call i64 @__kml_map_str_find(ptr %map, ptr %key)
  %found = icmp sge i64 %idx, 0
  ret i1 %found
}

define i1 @__kml_map_str_delete(ptr %map, ptr %key) {
entry:
  %idx = call i64 @__kml_map_str_find(ptr %map, ptr %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %do_del, label %miss
miss:
  ret i1 false
do_del:
  %size = load i64, ptr %map, align 8
  %last = sub i64 %size, 1
  %is_last = icmp eq i64 %idx, %last
  br i1 %is_last, label %shrink, label %swap
swap:
  %kp = getelementptr i8, ptr %map, i64 16
  %ka = load ptr, ptr %kp, align 8
  %dst_k = getelementptr ptr, ptr %ka, i64 %idx
  %src_k = getelementptr ptr, ptr %ka, i64 %last
  %lk = load ptr, ptr %src_k, align 8
  store ptr %lk, ptr %dst_k, align 8
  %vp = getelementptr i8, ptr %map, i64 24
  %va = load ptr, ptr %vp, align 8
  %dst_v = getelementptr i64, ptr %va, i64 %idx
  %src_v = getelementptr i64, ptr %va, i64 %last
  %lv = load i64, ptr %src_v, align 8
  store i64 %lv, ptr %dst_v, align 8
  br label %shrink
shrink:
  store i64 %last, ptr %map, align 8
  ret i1 true
}

define {ptr, i64} @__kml_map_str_keys(ptr %map) {
entry:
  %size = load i64, ptr %map, align 8
  %kp = getelementptr i8, ptr %map, i64 16
  %k = load ptr, ptr %kp, align 8
  %bytes = mul i64 %size, 8
  %arr = call ptr @malloc(i64 %bytes)
  call ptr @memcpy(ptr %arr, ptr %k, i64 %bytes)
  %r0 = insertvalue {ptr, i64} undef, ptr %arr, 0
  %r1 = insertvalue {ptr, i64} %r0, i64 %size, 1
  ret {ptr, i64} %r1
}

define {ptr, i64} @__kml_map_str_vals(ptr %map) {
entry:
  %size = load i64, ptr %map, align 8
  %vp = getelementptr i8, ptr %map, i64 24
  %v = load ptr, ptr %vp, align 8
  %bytes = mul i64 %size, 8
  %arr = call ptr @malloc(i64 %bytes)
  call ptr @memcpy(ptr %arr, ptr %v, i64 %bytes)
  %r0 = insertvalue {ptr, i64} undef, ptr %arr, 0
  %r1 = insertvalue {ptr, i64} %r0, i64 %size, 1
  ret {ptr, i64} %r1
}`)
}

func (e *Emitter) ensureMapNumHelpers() {
	if e.usedMapNumHelpers {
		return
	}
	e.usedMapNumHelpers = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemcpy()
	e.emitGlobal(`
define ptr @__kml_map_num_create() {
entry:
  %h = call ptr @malloc(i64 32)
  store i64 0, ptr %h, align 8
  %cap_p = getelementptr i8, ptr %h, i64 8
  store i64 8, ptr %cap_p, align 8
  %keys = call ptr @malloc(i64 64)
  %keys_p = getelementptr i8, ptr %h, i64 16
  store ptr %keys, ptr %keys_p, align 8
  %vals = call ptr @malloc(i64 64)
  %vals_p = getelementptr i8, ptr %h, i64 24
  store ptr %vals, ptr %vals_p, align 8
  ret ptr %h
}

define i64 @__kml_map_num_find(ptr %map, i64 %key) {
entry:
  %size = load i64, ptr %map, align 8
  %keys_p = getelementptr i8, ptr %map, i64 16
  %keys = load ptr, ptr %keys_p, align 8
  br label %scan
scan:
  %i = phi i64 [ 0, %entry ], [ %i_next, %cont ]
  %done = icmp sge i64 %i, %size
  br i1 %done, label %miss, label %chk
chk:
  %kslot = getelementptr i64, ptr %keys, i64 %i
  %kval = load i64, ptr %kslot, align 8
  %eq = icmp eq i64 %kval, %key
  br i1 %eq, label %hit, label %cont
hit:
  ret i64 %i
cont:
  %i_next = add i64 %i, 1
  br label %scan
miss:
  ret i64 -1
}

define void @__kml_map_num_set(ptr %map, i64 %key, i64 %val) {
entry:
  %idx = call i64 @__kml_map_num_find(ptr %map, i64 %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %do_update, label %grow_chk
do_update:
  %vp0 = getelementptr i8, ptr %map, i64 24
  %va0 = load ptr, ptr %vp0, align 8
  %vs0 = getelementptr i64, ptr %va0, i64 %idx
  store i64 %val, ptr %vs0, align 8
  ret void
grow_chk:
  %size = load i64, ptr %map, align 8
  %cap_p = getelementptr i8, ptr %map, i64 8
  %cap = load i64, ptr %cap_p, align 8
  %need = icmp sge i64 %size, %cap
  br i1 %need, label %do_grow, label %do_ins
do_grow:
  %ncap = mul i64 %cap, 2
  %nb = mul i64 %ncap, 8
  %kp1 = getelementptr i8, ptr %map, i64 16
  %ok = load ptr, ptr %kp1, align 8
  %nk = call ptr @realloc(ptr %ok, i64 %nb)
  store ptr %nk, ptr %kp1, align 8
  %vp1 = getelementptr i8, ptr %map, i64 24
  %ov = load ptr, ptr %vp1, align 8
  %nv = call ptr @realloc(ptr %ov, i64 %nb)
  store ptr %nv, ptr %vp1, align 8
  store i64 %ncap, ptr %cap_p, align 8
  br label %do_ins
do_ins:
  %sz2 = load i64, ptr %map, align 8
  %kp2 = getelementptr i8, ptr %map, i64 16
  %ka2 = load ptr, ptr %kp2, align 8
  %ks = getelementptr i64, ptr %ka2, i64 %sz2
  store i64 %key, ptr %ks, align 8
  %vp2 = getelementptr i8, ptr %map, i64 24
  %va2 = load ptr, ptr %vp2, align 8
  %vs = getelementptr i64, ptr %va2, i64 %sz2
  store i64 %val, ptr %vs, align 8
  %sz3 = add i64 %sz2, 1
  store i64 %sz3, ptr %map, align 8
  ret void
}

define i64 @__kml_map_num_get(ptr %map, i64 %key) {
entry:
  %idx = call i64 @__kml_map_num_find(ptr %map, i64 %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %hit, label %miss
hit:
  %vp = getelementptr i8, ptr %map, i64 24
  %va = load ptr, ptr %vp, align 8
  %vs = getelementptr i64, ptr %va, i64 %idx
  %v = load i64, ptr %vs, align 8
  ret i64 %v
miss:
  ret i64 0
}

define i1 @__kml_map_num_has(ptr %map, i64 %key) {
entry:
  %idx = call i64 @__kml_map_num_find(ptr %map, i64 %key)
  %found = icmp sge i64 %idx, 0
  ret i1 %found
}

define i1 @__kml_map_num_delete(ptr %map, i64 %key) {
entry:
  %idx = call i64 @__kml_map_num_find(ptr %map, i64 %key)
  %found = icmp sge i64 %idx, 0
  br i1 %found, label %do_del, label %miss
miss:
  ret i1 false
do_del:
  %size = load i64, ptr %map, align 8
  %last = sub i64 %size, 1
  %is_last = icmp eq i64 %idx, %last
  br i1 %is_last, label %shrink, label %swap
swap:
  %kp = getelementptr i8, ptr %map, i64 16
  %ka = load ptr, ptr %kp, align 8
  %dst_k = getelementptr i64, ptr %ka, i64 %idx
  %src_k = getelementptr i64, ptr %ka, i64 %last
  %lk = load i64, ptr %src_k, align 8
  store i64 %lk, ptr %dst_k, align 8
  %vp = getelementptr i8, ptr %map, i64 24
  %va = load ptr, ptr %vp, align 8
  %dst_v = getelementptr i64, ptr %va, i64 %idx
  %src_v = getelementptr i64, ptr %va, i64 %last
  %lv = load i64, ptr %src_v, align 8
  store i64 %lv, ptr %dst_v, align 8
  br label %shrink
shrink:
  store i64 %last, ptr %map, align 8
  ret i1 true
}

define {ptr, i64} @__kml_map_num_keys(ptr %map) {
entry:
  %size = load i64, ptr %map, align 8
  %kp = getelementptr i8, ptr %map, i64 16
  %k = load ptr, ptr %kp, align 8
  %bytes = mul i64 %size, 8
  %arr = call ptr @malloc(i64 %bytes)
  call ptr @memcpy(ptr %arr, ptr %k, i64 %bytes)
  %r0 = insertvalue {ptr, i64} undef, ptr %arr, 0
  %r1 = insertvalue {ptr, i64} %r0, i64 %size, 1
  ret {ptr, i64} %r1
}

define {ptr, i64} @__kml_map_num_vals(ptr %map) {
entry:
  %size = load i64, ptr %map, align 8
  %vp = getelementptr i8, ptr %map, i64 24
  %v = load ptr, ptr %vp, align 8
  %bytes = mul i64 %size, 8
  %arr = call ptr @malloc(i64 %bytes)
  call ptr @memcpy(ptr %arr, ptr %v, i64 %bytes)
  %r0 = insertvalue {ptr, i64} undef, ptr %arr, 0
  %r1 = insertvalue {ptr, i64} %r0, i64 %size, 1
  ret {ptr, i64} %r1
}`)
}

func (e *Emitter) ensureExceptionHelpers() {
	if e.usedExceptionHelpers {
		return
	}
	e.usedExceptionHelpers = true
	e.ensurePrintf()
	e.ensureMalloc()

	e.emitGlobal(`@__kml_thrown  = internal global ptr null, align 8`)
	e.emitGlobal(`@__kml_jmp_stk = internal global [64 x [64 x i64]] zeroinitializer, align 8`)
	e.emitGlobal(`@__kml_jmp_top = internal global i32 0, align 4`)
	e.emitGlobal(`@.kml_unc_fmt  = private unnamed_addr constant [14 x i8] c"Uncaught: %s\0A\00", align 1`)
	e.emitGlobal(`declare i32 @setjmp(ptr) returns_twice`)
	e.emitGlobal(`declare void @longjmp(ptr, i32) noreturn`)
	e.ensureExit()

	e.emitGlobal(`define ptr @__kml_push_jmpbuf() {
  %top = load i32, ptr @__kml_jmp_top, align 4
  %slot = getelementptr [64 x [64 x i64]], ptr @__kml_jmp_stk, i32 0, i32 %top
  %newtop = add i32 %top, 1
  store i32 %newtop, ptr @__kml_jmp_top, align 4
  ret ptr %slot
}`)

	e.emitGlobal(`define void @__kml_pop_jmpbuf() {
  %top = load i32, ptr @__kml_jmp_top, align 4
  %newtop = sub i32 %top, 1
  store i32 %newtop, ptr @__kml_jmp_top, align 4
  ret void
}`)

	e.emitGlobal(`define ptr @__kml_get_thrown() {
  %v = load ptr, ptr @__kml_thrown, align 8
  ret ptr %v
}`)

	e.emitGlobal(`define void @__kml_throw(ptr %errObj) {
entry:
  store ptr %errObj, ptr @__kml_thrown, align 8
  %top = load i32, ptr @__kml_jmp_top, align 4
  %iszero = icmp eq i32 %top, 0
  br i1 %iszero, label %uncaught, label %jump
uncaught:
  %msgPtr = getelementptr { ptr }, ptr %errObj, i32 0, i32 0
  %msg = load ptr, ptr %msgPtr, align 8
  call i32 (ptr, ...) @printf(ptr @.kml_unc_fmt, ptr %msg)
  call void @exit(i32 1)
  unreachable
jump:
  %newtop = sub i32 %top, 1
  store i32 %newtop, ptr @__kml_jmp_top, align 4
  %slot = getelementptr [64 x [64 x i64]], ptr @__kml_jmp_stk, i32 0, i32 %newtop
  call void @longjmp(ptr %slot, i32 1)
  unreachable
}`)
}

// ensureFetch declares __kml_fetch: a blocking GET request via libcurl,
// returning { i64 status, ptr body } (body always a valid, null-terminated,
// possibly-empty string — never null). Numeric CURLOPT_*/CURLINFO_* values
// below were verified directly against curl.h rather than trusted from
// memory (CURLOPT_URL=10002, CURLOPT_WRITEFUNCTION=20011,
// CURLOPT_WRITEDATA=10001, CURLOPT_FOLLOWLOCATION=52, CURLOPT_TIMEOUT=13,
// CURLOPT_NOSIGNAL=99, CURLINFO_RESPONSE_CODE=2097154 — curl's own ABI
// policy freezes these permanently, so hardcoding them here (rather than
// needing curl.h at KML-compile time) is safe long-term, not just today).
//
// A network-level failure (DNS, connection refused, TLS handshake, timeout)
// throws a KML Error via the existing @__kml_throw mechanism, exactly like a
// hand-written `throw new Error(...)` would — this is the same distinction
// real fetch makes: a non-2xx HTTP status still resolves normally (callers
// check .ok), only a request that never got a response at all throws.
func (e *Emitter) ensureFetch() {
	if e.usedFetch {
		return
	}
	e.usedFetch = true
	e.requireLink("curl")
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemcpy()
	e.ensureExceptionHelpers()

	e.emitGlobal("declare void @curl_global_init(i64 noundef)")
	e.emitGlobal("declare ptr @curl_easy_init()")
	e.emitGlobal("declare i32 @curl_easy_setopt(ptr noundef, i32 noundef, ...)")
	e.emitGlobal("declare i32 @curl_easy_perform(ptr noundef)")
	e.emitGlobal("declare i32 @curl_easy_getinfo(ptr noundef, i32 noundef, ...)")
	e.emitGlobal("declare void @curl_easy_cleanup(ptr noundef)")
	e.emitGlobal("declare ptr @curl_easy_strerror(i32 noundef)")
	e.emitGlobal("@__kml_curl_inited = internal global i1 0, align 1")

	// Write callback: libcurl calls this (possibly many times, once per
	// chunk) as the response body streams in. userdata is a ptr to a
	// { ptr data, i64 len, i64 cap } growable buffer this function owns —
	// grown via realloc (doubling, floor 64 bytes), always kept
	// null-terminated so the final body can be handed around as a plain
	// KML string with no extra bookkeeping.
	e.emitGlobal(`
define i64 @__kml_curl_write_cb(ptr %chunk, i64 %size, i64 %nmemb, ptr %ud) {
entry:
  %total = mul i64 %size, %nmemb
  %data_p = getelementptr { ptr, i64, i64 }, ptr %ud, i32 0, i32 0
  %len_p = getelementptr { ptr, i64, i64 }, ptr %ud, i32 0, i32 1
  %cap_p = getelementptr { ptr, i64, i64 }, ptr %ud, i32 0, i32 2
  %curdata = load ptr, ptr %data_p, align 8
  %curlen = load i64, ptr %len_p, align 8
  %curcap = load i64, ptr %cap_p, align 8
  %needed = add i64 %curlen, %total
  %neededp1 = add i64 %needed, 1
  %needgrow = icmp sgt i64 %neededp1, %curcap
  br i1 %needgrow, label %grow, label %copy

grow:
  %cap2 = mul i64 %curcap, 2
  %pick1 = icmp sgt i64 %neededp1, %cap2
  %newcap_a = select i1 %pick1, i64 %neededp1, i64 %cap2
  %atleast64 = icmp sgt i64 %newcap_a, 64
  %newcap = select i1 %atleast64, i64 %newcap_a, i64 64
  %newdata = call ptr @realloc(ptr %curdata, i64 %newcap)
  store ptr %newdata, ptr %data_p, align 8
  store i64 %newcap, ptr %cap_p, align 8
  br label %copy

copy:
  %dataNow = load ptr, ptr %data_p, align 8
  %destptr = getelementptr i8, ptr %dataNow, i64 %curlen
  call ptr @memcpy(ptr %destptr, ptr %chunk, i64 %total)
  %newlen = add i64 %curlen, %total
  store i64 %newlen, ptr %len_p, align 8
  %termptr = getelementptr i8, ptr %dataNow, i64 %newlen
  store i8 0, ptr %termptr, align 1
  ret i64 %total
}`)

	e.emitGlobal(`
define { i64, ptr } @__kml_fetch(ptr %url) {
entry:
  %inited = load i1, ptr @__kml_curl_inited, align 1
  br i1 %inited, label %skipinit, label %doinit

doinit:
  call void @curl_global_init(i64 3)
  store i1 1, ptr @__kml_curl_inited, align 1
  br label %skipinit

skipinit:
  %buf = call ptr @malloc(i64 24)
  %buf_data_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 0
  %buf_len_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 1
  %buf_cap_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 2
  store ptr null, ptr %buf_data_p, align 8
  store i64 0, ptr %buf_len_p, align 8
  store i64 0, ptr %buf_cap_p, align 8

  %curl = call ptr @curl_easy_init()

  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10002, ptr %url)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 20011, ptr @__kml_curl_write_cb)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10001, ptr %buf)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 52, i64 1)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 13, i64 30)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 99, i64 1)

  %perfres = call i32 @curl_easy_perform(ptr %curl)
  %failed = icmp ne i32 %perfres, 0
  br i1 %failed, label %neterror, label %ok

neterror:
  %errstr = call ptr @curl_easy_strerror(i32 %perfres)
  %errobj = call ptr @malloc(i64 8)
  store ptr %errstr, ptr %errobj, align 8
  call void @curl_easy_cleanup(ptr %curl)
  call void @__kml_throw(ptr %errobj)
  unreachable

ok:
  %statusslot = alloca i64, align 8
  store i64 0, ptr %statusslot, align 8
  call i32 (ptr, i32, ...) @curl_easy_getinfo(ptr %curl, i32 2097154, ptr %statusslot)
  %status = load i64, ptr %statusslot, align 8
  call void @curl_easy_cleanup(ptr %curl)

  %finaldata = load ptr, ptr %buf_data_p, align 8
  %isnull = icmp eq ptr %finaldata, null
  br i1 %isnull, label %emptybody, label %havebody

emptybody:
  %emptystr = call ptr @malloc(i64 1)
  store i8 0, ptr %emptystr, align 1
  br label %done

havebody:
  br label %done

done:
  %bodyfinal = phi ptr [ %emptystr, %emptybody ], [ %finaldata, %havebody ]
  %r0 = insertvalue { i64, ptr } undef, i64 %status, 0
  %r1 = insertvalue { i64, ptr } %r0, ptr %bodyfinal, 1
  ret { i64, ptr } %r1
}`)
}

// ensureFetchAsync declares everything a real, non-blocking `await
// fetch(...)` needs (ADR-00050, TDD-00006 Part 2's second real slice, on
// top of ADR-00049's fiber/event-loop mechanism): libcurl's multi
// interface, driven by the same select() loop http.listen already uses, so
// a fetch awaited from inside a connection-handler fiber yields instead of
// blocking the whole process, letting other connections' fibers (and their
// own concurrent fetches) keep making progress.
//
// Numeric CURLOPT_*/CURLINFO_* values not already used by ensureFetch were
// verified directly against curl.h/multi.h on this machine (both are
// present locally), the same "never trust from memory" standard the
// existing blocking fetch's own constants already document:
// CURLOPT_PRIVATE=10103 (CURLOPTTYPE_OBJECTPOINT=10000 + 103),
// CURLINFO_PRIVATE=1048597 (CURLINFO_STRING=0x100000 + 21),
// CURLMSG_DONE=1 (CURLMSG_NONE=0 is the first, unused enum value).
//
// A pending fetch is a malloc'd { ptr easy, ptr buf, i64 done, i64
// httpStatus, i64 curlResult } (40 bytes, every field ptr/i64 — no padding
// ambiguity, same convention the timer queue and connection array already
// follow). buf is the same { ptr, i64, i64 } growable write-buffer
// ensureFetch's own write callback already fills — reused as-is, no new
// callback needed.
//
//	__kml_fetch_async(ptr url) -> ptr
//	  Creates the easy handle (identical setopts to the blocking __kml_fetch:
//	  URL, write callback/data, follow-location, timeout, nosignal), lazily
//	  creates the one global CURLM multi handle on first use, attaches the
//	  pending struct to the easy handle via CURLOPT_PRIVATE (so a later
//	  curl_multi_info_read can match a completed transfer back to it),
//	  curl_multi_add_handle()s it, and calls curl_multi_perform() once to
//	  kick the transfer off. Returns immediately — never blocks.
//	__kml_curl_drain_messages()
//	  Drains curl_multi_info_read()'s completed-transfer queue. For each
//	  CURLMSG_DONE message: retrieves the pending struct via
//	  CURLINFO_PRIVATE, records the HTTP status and CURLcode result into
//	  it, removes+cleans up the easy handle, and sets done=1. Shared by the
//	  event loop (called after every select() wake) and __kml_await_fetch's
//	  own busy-spin fallback path below.
//	__kml_await_fetch(ptr pending) -> { i64 status, ptr body }
//	  Loops until pending->done: if running inside a connection fiber
//	  (@__kml_current_conn_idx >= 0), parks this specific fiber (stores
//	  `pending` into its own connection-array entry's pendingFetch field,
//	  swapcontext back to @__kml_main_ctx — the event loop's resume-scan
//	  already checks this field, see runtime.go's __kml_event_loop_run) and
//	  clears pendingFetch back to null once resumed; otherwise (top-level
//	  code, no event loop/fiber context to yield into) busy-spins by
//	  calling curl_multi_perform + draining messages directly in a tight
//	  loop — behaviorally equivalent to a blocking wait (nothing else could
//	  run concurrently in that case anyway), just implemented via repeated
//	  small multi-interface calls instead of one call to curl_easy_perform.
//	  Once done, throws a catchable Error on a transfer-level failure
//	  (identical shape to __kml_fetch's own neterror path) or returns the
//	  final status/body.
func (e *Emitter) ensureFetchAsync() {
	if e.usedFetchAsync {
		return
	}
	e.usedFetchAsync = true
	e.ensureFetch()
	e.ensureFiberRuntime()
	e.ensureExceptionHelpers()

	e.emitGlobal("declare ptr @curl_multi_init()")
	e.emitGlobal("declare i32 @curl_multi_add_handle(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @curl_multi_remove_handle(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @curl_multi_fdset(ptr noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @curl_multi_perform(ptr noundef, ptr noundef)")
	e.emitGlobal("declare ptr @curl_multi_info_read(ptr noundef, ptr noundef)")
	e.emitGlobal("@__kml_curl_multi = internal global ptr null, align 8")

	e.emitGlobal(`
define ptr @__kml_fetch_async(ptr %url) {
entry:
  %inited = load i1, ptr @__kml_curl_inited, align 1
  br i1 %inited, label %skipinit, label %doinit

doinit:
  call void @curl_global_init(i64 3)
  store i1 1, ptr @__kml_curl_inited, align 1
  br label %skipinit

skipinit:
  %multi = load ptr, ptr @__kml_curl_multi, align 8
  %needmulti = icmp eq ptr %multi, null
  br i1 %needmulti, label %initmulti, label %havemulti

initmulti:
  %newmulti = call ptr @curl_multi_init()
  store ptr %newmulti, ptr @__kml_curl_multi, align 8
  br label %havemulti

havemulti:
  %multi2 = load ptr, ptr @__kml_curl_multi, align 8

  %buf = call ptr @malloc(i64 24)
  %buf_data_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 0
  %buf_len_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 1
  %buf_cap_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 2
  store ptr null, ptr %buf_data_p, align 8
  store i64 0, ptr %buf_len_p, align 8
  store i64 0, ptr %buf_cap_p, align 8

  %curl = call ptr @curl_easy_init()
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10002, ptr %url)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 20011, ptr @__kml_curl_write_cb)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10001, ptr %buf)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 52, i64 1)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 13, i64 30)
  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 99, i64 1)

  %pending = call ptr @malloc(i64 40)
  %p_easy = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 0
  store ptr %curl, ptr %p_easy, align 8
  %p_buf = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 1
  store ptr %buf, ptr %p_buf, align 8
  %p_done = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 2
  store i64 0, ptr %p_done, align 8
  %p_status = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 3
  store i64 0, ptr %p_status, align 8
  %p_result = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 4
  store i64 0, ptr %p_result, align 8

  call i32 (ptr, i32, ...) @curl_easy_setopt(ptr %curl, i32 10103, ptr %pending)
  call i32 @curl_multi_add_handle(ptr %multi2, ptr %curl)
  %runningp = alloca i32, align 4
  call i32 @curl_multi_perform(ptr %multi2, ptr %runningp)

  ret ptr %pending
}`)

	e.emitGlobal(`
define void @__kml_curl_drain_messages() {
entry:
  %multi = load ptr, ptr @__kml_curl_multi, align 8
  %msgsleft = alloca i32, align 4
  %privslot = alloca ptr, align 8
  %statusslot = alloca i64, align 8
  br label %drainloop

drainloop:
  %msg = call ptr @curl_multi_info_read(ptr %multi, ptr %msgsleft)
  %isnull = icmp eq ptr %msg, null
  br i1 %isnull, label %done, label %havemsg

havemsg:
  %msgtype_p = getelementptr i8, ptr %msg, i64 0
  %msgtype = load i32, ptr %msgtype_p, align 4
  %isdone = icmp eq i32 %msgtype, 1
  br i1 %isdone, label %handledone, label %drainloop

handledone:
  %easyh_p = getelementptr i8, ptr %msg, i64 8
  %easyh = load ptr, ptr %easyh_p, align 8
  %result_p = getelementptr i8, ptr %msg, i64 16
  %result32 = load i32, ptr %result_p, align 4
  %result64 = sext i32 %result32 to i64

  call i32 (ptr, i32, ...) @curl_easy_getinfo(ptr %easyh, i32 1048597, ptr %privslot)
  %pending = load ptr, ptr %privslot, align 8

  store i64 0, ptr %statusslot, align 8
  call i32 (ptr, i32, ...) @curl_easy_getinfo(ptr %easyh, i32 2097154, ptr %statusslot)
  %status = load i64, ptr %statusslot, align 8

  %p_status2 = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 3
  store i64 %status, ptr %p_status2, align 8
  %p_result2 = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 4
  store i64 %result64, ptr %p_result2, align 8

  call i32 @curl_multi_remove_handle(ptr %multi, ptr %easyh)
  call void @curl_easy_cleanup(ptr %easyh)

  %p_done2 = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 2
  store i64 1, ptr %p_done2, align 8

  br label %drainloop

done:
  ret void
}`)

	e.emitGlobal(`
define { i64, ptr } @__kml_await_fetch(ptr %pending) {
entry:
  %runningp = alloca i32, align 4
  br label %checkloop

checkloop:
  %done_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 2
  %done = load i64, ptr %done_p, align 8
  %isdone = icmp ne i64 %done, 0
  br i1 %isdone, label %finish, label %maybeyield

maybeyield:
  %curidx = load i64, ptr @__kml_current_conn_idx, align 8
  %onfiber = icmp sge i64 %curidx, 0
  br i1 %onfiber, label %doyield, label %busyspin

doyield:
  %conndata = load ptr, ptr @__kml_conn_data, align 8
  %selfslot = getelementptr { i64, ptr, ptr, ptr }, ptr %conndata, i64 %curidx
  %pf_p = getelementptr { i64, ptr, ptr, ptr }, ptr %selfslot, i32 0, i32 3
  store ptr %pending, ptr %pf_p, align 8
  %ctx_p = getelementptr { i64, ptr, ptr, ptr }, ptr %selfslot, i32 0, i32 1
  %ctxptr = load ptr, ptr %ctx_p, align 8
  call i32 @swapcontext(ptr %ctxptr, ptr @__kml_main_ctx)
  store ptr null, ptr %pf_p, align 8
  br label %checkloop

busyspin:
  %multi = load ptr, ptr @__kml_curl_multi, align 8
  call i32 @curl_multi_perform(ptr %multi, ptr %runningp)
  call void @__kml_curl_drain_messages()
  br label %checkloop

finish:
  %result_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 4
  %result = load i64, ptr %result_p, align 8
  %failed = icmp ne i64 %result, 0
  br i1 %failed, label %neterror, label %ok

neterror:
  %result32b = trunc i64 %result to i32
  %errstr = call ptr @curl_easy_strerror(i32 %result32b)
  %errobj = call ptr @malloc(i64 8)
  store ptr %errstr, ptr %errobj, align 8
  call void @__kml_throw(ptr %errobj)
  unreachable

ok:
  %status_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 3
  %status = load i64, ptr %status_p, align 8
  %buf_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %pending, i32 0, i32 1
  %buf = load ptr, ptr %buf_p, align 8
  %bodyptr_p = getelementptr { ptr, i64, i64 }, ptr %buf, i32 0, i32 0
  %bodyptr = load ptr, ptr %bodyptr_p, align 8

  %isnullbody = icmp eq ptr %bodyptr, null
  br i1 %isnullbody, label %emptybody, label %havebody

emptybody:
  %emptystr = call ptr @malloc(i64 1)
  store i8 0, ptr %emptystr, align 1
  br label %retdone

havebody:
  br label %retdone

retdone:
  %bodyfinal = phi ptr [ %emptystr, %emptybody ], [ %bodyptr, %havebody ]
  %r1 = insertvalue { i64, ptr } undef, i64 %status, 0
  %r2 = insertvalue { i64, ptr } %r1, ptr %bodyfinal, 1
  ret { i64, ptr } %r2
}`)
}

// errnoAccessor returns the C symbol that exposes the current thread's
// errno as an `int*` on the host this compiler itself is running on (and
// will therefore also be clang'ing on — this project doesn't cross-compile
// today). glibc (Linux) and Darwin/BSD (macOS) use different symbol names
// for the same thing, since `errno` is a macro, not a portable global
// symbol — the same class of platform check emitMathRandom already makes
// for arc4random vs a portable fallback.
func errnoAccessor() string {
	switch runtime.GOOS {
	case "darwin", "freebsd", "openbsd", "netbsd", "dragonfly":
		return "__error"
	default:
		return "__errno_location"
	}
}

// ensureErrnoAccessor declares the errnoAccessor() symbol exactly once.
// Extracted as its own singleton after ensureFsThrow and ensureProcessKill
// both independently declared it and collided ("invalid redefinition of
// function '__error'") the first time a program used both fs and
// process.kill — the same class of bug ADR-00023 already found and fixed
// once for fopen/fclose/fwrite; fixed the same way here.
func (e *Emitter) ensureErrnoAccessor() {
	if e.usedErrnoAccessor {
		return
	}
	e.usedErrnoAccessor = true
	e.emitGlobal(fmt.Sprintf("declare ptr @%s()", errnoAccessor()))
}

// ensureStrerror declares C strerror() exactly once — same singleton-sharing
// reasoning as ensureErrnoAccessor above.
func (e *Emitter) ensureStrerror() {
	if e.usedStrerror {
		return
	}
	e.usedStrerror = true
	e.emitGlobal("declare ptr @strerror(i32 noundef)")
}

// ensureFsThrow declares __kml_fs_throw: builds "<opDesc> '<path>': <reason>"
// from the current errno via strerror() and throws it as a KML Error via the
// existing @__kml_throw mechanism (emit_exceptions.go) — the same "let a
// real OS-level failure surface as a catchable Error" approach ADR-00021
// already established for fetch's network failures.
func (e *Emitter) ensureFsThrow() {
	if e.usedFsThrow {
		return
	}
	e.usedFsThrow = true
	e.ensureMalloc()
	e.ensureStrlen()
	e.ensureSprintf()
	e.ensureExceptionHelpers()
	accessor := errnoAccessor()
	e.ensureErrnoAccessor()
	e.ensureStrerror()
	fmtPtr := e.internString("%s '%s': %s")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_throw(ptr %%opdesc, ptr %%path) {
entry:
  %%errno_ptr = call ptr @%s()
  %%errno_val = load i32, ptr %%errno_ptr, align 4
  %%errmsg = call ptr @strerror(i32 %%errno_val)
  %%len_op = call i64 @strlen(ptr %%opdesc)
  %%len_path = call i64 @strlen(ptr %%path)
  %%len_err = call i64 @strlen(ptr %%errmsg)
  %%sum1 = add i64 %%len_op, %%len_path
  %%sum2 = add i64 %%sum1, %%len_err
  %%bufsize = add i64 %%sum2, 32
  %%buf = call ptr @malloc(i64 %%bufsize)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%buf, ptr %s, ptr %%opdesc, ptr %%path, ptr %%errmsg)
  %%errobj = call ptr @malloc(i64 8)
  store ptr %%buf, ptr %%errobj, align 8
  call void @__kml_throw(ptr %%errobj)
  ret void
}`, accessor, fmtPtr))
}

func (e *Emitter) ensureFopen() {
	if e.usedFopen {
		return
	}
	e.usedFopen = true
	e.emitGlobal("declare ptr @fopen(ptr noundef, ptr noundef)")
}

func (e *Emitter) ensureFclose() {
	if e.usedFclose {
		return
	}
	e.usedFclose = true
	e.emitGlobal("declare i32 @fclose(ptr noundef)")
}

func (e *Emitter) ensureFwrite() {
	if e.usedFwrite {
		return
	}
	e.usedFwrite = true
	e.emitGlobal("declare i64 @fwrite(ptr noundef, i64 noundef, i64 noundef, ptr noundef)")
}

// ensureFsReadFile declares __kml_fs_read_file: reads an entire file into a
// malloc'd, null-terminated string. Throws (via __kml_fs_throw) if the file
// can't be opened. Text-only, like every string in this compiler — a file
// containing embedded null bytes will read back shorter than its real size
// (the same, already-documented limitation fetch's response bodies have).
func (e *Emitter) ensureFsReadFile() {
	if e.usedFsReadFile {
		return
	}
	e.usedFsReadFile = true
	e.ensureFsThrow()
	e.ensureMalloc()
	e.ensureFopen()
	e.ensureFclose()
	e.emitGlobal("declare i32 @fseek(ptr noundef, i64 noundef, i32 noundef)")
	e.emitGlobal("declare i64 @ftell(ptr noundef)")
	e.emitGlobal("declare i64 @fread(ptr noundef, i64 noundef, i64 noundef, ptr noundef)")
	modePtr := e.internString("rb")
	opDescPtr := e.internString("cannot open file for reading")
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_fs_read_file(ptr %%path) {
entry:
  %%f = call ptr @fopen(ptr %%path, ptr %s)
  %%isnull = icmp eq ptr %%f, null
  br i1 %%isnull, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  %%seekend = call i32 @fseek(ptr %%f, i64 0, i32 2)
  %%size = call i64 @ftell(ptr %%f)
  %%seekset = call i32 @fseek(ptr %%f, i64 0, i32 0)
  %%sizep1 = add i64 %%size, 1
  %%buf = call ptr @malloc(i64 %%sizep1)
  %%nread = call i64 @fread(ptr %%buf, i64 1, i64 %%size, ptr %%f)
  %%termptr = getelementptr i8, ptr %%buf, i64 %%size
  store i8 0, ptr %%termptr, align 1
  call i32 @fclose(ptr %%f)
  ret ptr %%buf
}`, modePtr, opDescPtr))
}

// ensureFsWriteFile declares __kml_fs_write_file: writes (creating or
// truncating) a file with the given string content. Throws if the file
// can't be opened for writing.
func (e *Emitter) ensureFsWriteFile() {
	e.ensureFsWriteLike(&e.usedFsWriteFile, "__kml_fs_write_file", "wb", "cannot open file for writing")
}

// ensureFsAppendFile declares __kml_fs_append_file: like ensureFsWriteFile,
// but appends (creating the file if it doesn't exist yet) instead of
// truncating.
func (e *Emitter) ensureFsAppendFile() {
	e.ensureFsWriteLike(&e.usedFsAppendFile, "__kml_fs_append_file", "ab", "cannot open file for appending")
}

// ensureFsWriteLike is the shared implementation behind ensureFsWriteFile
// and ensureFsAppendFile — identical shape, differing only in fopen mode,
// the generated function's name, and the error message.
func (e *Emitter) ensureFsWriteLike(used *bool, fnName, mode, opDesc string) {
	if *used {
		return
	}
	*used = true
	e.ensureFsThrow()
	e.ensureStrlen()
	e.ensureFopen()
	e.ensureFclose()
	e.ensureFwrite()
	modePtr := e.internString(mode)
	opDescPtr := e.internString(opDesc)
	e.emitGlobal(fmt.Sprintf(`
define void @%s(ptr %%path, ptr %%data) {
entry:
  %%f = call ptr @fopen(ptr %%path, ptr %s)
  %%isnull = icmp eq ptr %%f, null
  br i1 %%isnull, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  %%len = call i64 @strlen(ptr %%data)
  %%nwritten = call i64 @fwrite(ptr %%data, i64 1, i64 %%len, ptr %%f)
  call i32 @fclose(ptr %%f)
  ret void
}`, fnName, modePtr, opDescPtr))
}

// ensureFsExists declares __kml_fs_exists: a plain existence check via
// POSIX access() — deliberately does NOT throw (matching real Node's
// fs.existsSync, one of the few fs functions that reports "doesn't exist"
// as a plain false rather than an error).
func (e *Emitter) ensureFsExists() {
	if e.usedFsExists {
		return
	}
	e.usedFsExists = true
	e.emitGlobal("declare i32 @access(ptr noundef, i32 noundef)")
	e.emitGlobal(`
define i1 @__kml_fs_exists(ptr %path) {
entry:
  %r = call i32 @access(ptr %path, i32 0)
  %ok = icmp eq i32 %r, 0
  ret i1 %ok
}`)
}

// ensureFsUnlink declares __kml_fs_unlink: deletes a file via the portable
// ANSI C remove() (simpler than POSIX unlink() for this purpose, and
// available identically on every target this compiler supports). Throws on
// failure.
func (e *Emitter) ensureFsUnlink() {
	if e.usedFsUnlink {
		return
	}
	e.usedFsUnlink = true
	e.ensureFsThrow()
	e.emitGlobal("declare i32 @remove(ptr noundef)")
	opDescPtr := e.internString("cannot delete file")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_unlink(ptr %%path) {
entry:
  %%r = call i32 @remove(ptr %%path)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  ret void
}`, opDescPtr))
}

// ensureFsRmdir declares __kml_fs_rmdir: removes an empty directory via
// POSIX rmdir() — deliberately not remove()/unlink() (which would also
// silently accept a plain file, unlike real Node's fs.rmdirSync, which is
// specifically directory-only and fails with ENOTDIR/ENOTEMPTY otherwise).
// No recursive-delete option (matching mkdirSync's lack of {recursive:
// true}) — only ever removes a directory that's already empty.
func (e *Emitter) ensureFsRmdir() {
	if e.usedFsRmdir {
		return
	}
	e.usedFsRmdir = true
	e.ensureFsThrow()
	e.emitGlobal("declare i32 @rmdir(ptr noundef)")
	opDescPtr := e.internString("cannot remove directory")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_rmdir(ptr %%path) {
entry:
  %%r = call i32 @rmdir(ptr %%path)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  ret void
}`, opDescPtr))
}

const base64Alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"

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
	e.emitGlobal(fmt.Sprintf(`@__kml_base64_alphabet = private unnamed_addr constant [64 x i8] c"%s"`, base64Alphabet))
	e.emitGlobal(`
define ptr @__kml_btoa(ptr %str) {
entry:
  %len = call i64 @strlen(ptr %str)
  %len_plus2 = add i64 %len, 2
  %ngroups = udiv i64 %len_plus2, 3
  %outlen = mul i64 %ngroups, 4
  %outlen_plus1 = add i64 %outlen, 1
  %out = call ptr @malloc(i64 %outlen_plus1)
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

// ensureBase64Decode declares __kml_atob: the inverse of __kml_btoa.
// Permissive, not strict: malformed input (length not a multiple of 4)
// silently drops the trailing incomplete group rather than throwing, and
// characters outside the base64 alphabet decode as 0 rather than raising
// an error — simpler than real atob's InvalidCharacterError, a documented
// V1 simplification.
func (e *Emitter) ensureBase64Decode() {
	if e.usedBase64Decode {
		return
	}
	e.usedBase64Decode = true
	e.ensureStrlen()
	e.ensureMalloc()

	table := make([]byte, 256)
	for i, c := range []byte(base64Alphabet) {
		table[c] = byte(i)
	}
	entries := make([]string, 256)
	for i, v := range table {
		entries[i] = fmt.Sprintf("i8 %d", v)
	}
	e.emitGlobal(fmt.Sprintf("@__kml_base64_decode_table = private unnamed_addr constant [256 x i8] [%s]", strings.Join(entries, ", ")))
	e.emitGlobal(`
define ptr @__kml_atob(ptr %str) {
entry:
  %len = call i64 @strlen(ptr %str)
  %ngroups = udiv i64 %len, 4
  %outlen_est = mul i64 %ngroups, 3
  %outlen_est_plus1 = add i64 %outlen_est, 1
  %out = call ptr @malloc(i64 %outlen_est_plus1)
  br label %loopcheck

loopcheck:
  %i = phi i64 [ 0, %entry ], [ %i_next, %loopbody ]
  %oi = phi i64 [ 0, %entry ], [ %oi_next, %loopbody ]
  %i4 = add i64 %i, 4
  %cont = icmp sle i64 %i4, %len
  br i1 %cont, label %loopbody, label %done

loopbody:
  %i1 = add i64 %i, 1
  %i2 = add i64 %i, 2
  %i3 = add i64 %i, 3
  %p0 = getelementptr i8, ptr %str, i64 %i
  %p1 = getelementptr i8, ptr %str, i64 %i1
  %p2 = getelementptr i8, ptr %str, i64 %i2
  %p3 = getelementptr i8, ptr %str, i64 %i3
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
  ret ptr %out
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
	e.emitGlobal(fmt.Sprintf("%s = private unnamed_addr constant [256 x i8] [%s]", tableName, strings.Join(entries, ", ")))
	e.emitGlobal(fmt.Sprintf(`
define ptr @%s(ptr %%str) {
entry:
  %%len = call i64 @strlen(ptr %%str)
  %%len3 = mul i64 %%len, 3
  %%outlen_plus1 = add i64 %%len3, 1
  %%out = call ptr @malloc(i64 %%outlen_plus1)
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
	if *used {
		return
	}
	*used = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureHexDecodeTable()

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
  %%outlen_plus1 = add i64 %%len, 1
  %%out = call ptr @malloc(i64 %%outlen_plus1)
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
  br i1 %%bothok, label %%pctvalid, label %%plain

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

done:
  %%termp = getelementptr i8, ptr %%out, i64 %%oi
  store i8 0, ptr %%termp, align 1
  ret ptr %%out
}`, fnName, pctdoneLabel, pctdoneLabel, reservedBlock))
}

func (e *Emitter) ensureDecodeURIComponent() {
	e.ensurePercentDecode(&e.usedDecodeURIComponent, "__kml_decode_uri_component", false)
}

func (e *Emitter) ensureDecodeURI() {
	e.ensurePercentDecode(&e.usedDecodeURI, "__kml_decode_uri", true)
}

// ensureCryptoRandomBytes declares __kml_crypto_random_bytes(ptr buf, i64 n):
// fills n bytes at buf with cryptographically-secure random data.
// Deliberately NOT the same source Math.random()'s portable fallback uses
// (plain C89 rand(), not cryptographically secure) — crypto.* needs a real
// CSPRNG: arc4random_buf (BSD/macOS, itself a CSPRNG, no seeding needed) or
// getrandom() (Linux, reads from the kernel's CSPRNG), matching the
// STATUS.md roadmap note this was scoped from.
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
// place, byte for byte): this compiler has no ArrayBuffer/TypedArrays yet
// (0% implemented, tracked separately in STATUS.md), so a plain number[]
// stands in as the "buffer," one random byte value per i64 element.
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

// stdinGlobalName returns the actual external symbol backing C's `stdin`
// macro on whatever OS is running this compiler right now (and will
// therefore also run clang moments later). Verified directly rather than
// guessed: on Darwin, `stdin` expands (via the preprocessor) to `__stdinp`,
// a differently-named global `FILE*` — not literally "stdin" at the link
// level at all. glibc (Linux) exposes it as the plain symbol `stdin`
// itself, a long-stable convention. The same class of platform check as
// errnoAccessor/monotonicClockID.
func stdinGlobalName() string {
	if runtime.GOOS == "darwin" {
		return "__stdinp"
	}
	return "stdin"
}

// ensureReadLineSync declares __kml_read_line_sync: reads one line from
// stdin via POSIX getline() (handles arbitrarily long lines, unlike a
// fixed-size fgets buffer), strips a trailing "\n" (and a preceding "\r",
// for input from CRLF-terminated sources), and returns null at EOF — the
// same "possibly-null string, check with ?? or an explicit comparison"
// convention already used for process.env (emit_process.go).
func (e *Emitter) ensureReadLineSync() {
	if e.usedReadLineSync {
		return
	}
	e.usedReadLineSync = true
	e.ensureStrlen()
	stdinName := stdinGlobalName()
	e.emitGlobal(fmt.Sprintf("@%s = external global ptr", stdinName))
	e.emitGlobal("declare i64 @getline(ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal(fmt.Sprintf(`
define ptr @__kml_read_line_sync() {
entry:
  %%lineptr = alloca ptr, align 8
  %%n = alloca i64, align 8
  store ptr null, ptr %%lineptr, align 8
  store i64 0, ptr %%n, align 8
  %%stdinval = load ptr, ptr @%s, align 8
  %%r = call i64 @getline(ptr %%lineptr, ptr %%n, ptr %%stdinval)
  %%iseof = icmp slt i64 %%r, 0
  br i1 %%iseof, label %%eof, label %%ok

eof:
  ret ptr null

ok:
  %%buf = load ptr, ptr %%lineptr, align 8
  %%len = call i64 @strlen(ptr %%buf)
  %%haslen = icmp sgt i64 %%len, 0
  br i1 %%haslen, label %%checknl, label %%done

checknl:
  %%lastidx = sub i64 %%len, 1
  %%lastp = getelementptr i8, ptr %%buf, i64 %%lastidx
  %%lastch = load i8, ptr %%lastp, align 1
  %%isnl = icmp eq i8 %%lastch, 10
  br i1 %%isnl, label %%stripnl, label %%done

stripnl:
  store i8 0, ptr %%lastp, align 1
  %%haslen2 = icmp sgt i64 %%lastidx, 0
  br i1 %%haslen2, label %%checkcr, label %%done

checkcr:
  %%cridx = sub i64 %%lastidx, 1
  %%crp = getelementptr i8, ptr %%buf, i64 %%cridx
  %%crch = load i8, ptr %%crp, align 1
  %%iscr = icmp eq i8 %%crch, 13
  br i1 %%iscr, label %%stripcr, label %%done

stripcr:
  store i8 0, ptr %%crp, align 1
  br label %%done

done:
  ret ptr %%buf
}`, stdinName))
}

// ensureExecFileSync declares __kml_exec_file_sync: fork()s a child process,
// execvp()s it with argv = [file, ...args], captures the child's stdout via
// a pipe into a malloc'd, null-terminated string (grown via realloc
// doubling — the same growable-{ptr,i64,i64}-buffer shape __kml_fetch's
// curl write callback already uses), and waitpid()s for it to finish.
//
// V1 scope, narrowed the same way every other builtin here started narrow:
// stderr is inherited (visible on the terminal live, not captured —
// capturing both streams at once without deadlocking needs select()/poll()
// over two pipes, real complexity for a first pass); a non-zero exit status
// or a signal death throws a plain Error via the existing __kml_throw
// mechanism (same as fs's and fetch's failure paths), not a rich error
// object with .status/.stdout/.stderr fields like real Node's.
//
// The wait-status decoding (low 7 bits == 0 means "exited normally", exit
// code in bits 8-15; otherwise the low 7 bits are the killing signal) is
// the traditional Unix wait-status encoding, valid on both Linux and
// Darwin/BSD, and exhaustive here since waitpid is called with no WUNTRACED
// flag — a child can only ever be reported as exited or signaled, never
// stopped, so there's no third case to get wrong.
func (e *Emitter) ensureExecFileSync() {
	if e.usedExecFileSync {
		return
	}
	e.usedExecFileSync = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureMemcpy()
	e.ensureStrlen()
	e.ensureSprintf()
	e.ensureExceptionHelpers()

	e.emitGlobal("declare i32 @pipe(ptr noundef)")
	e.emitGlobal("declare i32 @fork()")
	e.emitGlobal("declare i32 @dup2(i32 noundef, i32 noundef)")
	e.emitGlobal("declare i32 @close(i32 noundef)")
	e.emitGlobal("declare i32 @execvp(ptr noundef, ptr noundef)")
	e.emitGlobal("declare void @_exit(i32 noundef) noreturn")
	e.emitGlobal("declare i64 @read(i32 noundef, ptr noundef, i64 noundef)")
	e.emitGlobal("declare i32 @waitpid(i32 noundef, ptr noundef, i32 noundef)")

	fmtExit := e.internString("Command failed with exit code %d: %s")
	fmtSig := e.internString("Command was terminated by signal %d: %s")

	part1 := `
define ptr @__kml_exec_file_sync(ptr %file, ptr %argsdata, i64 %argslen) {
entry:
  %argvlen = add i64 %argslen, 2
  %argvbytes = mul i64 %argvlen, 8
  %argv = call ptr @malloc(i64 %argvbytes)
  store ptr %file, ptr %argv, align 8
  %argvoff1 = getelementptr ptr, ptr %argv, i64 1
  %hasargs = icmp sgt i64 %argslen, 0
  br i1 %hasargs, label %copyargs, label %setnull

copyargs:
  %copybytes = mul i64 %argslen, 8
  call ptr @memcpy(ptr %argvoff1, ptr %argsdata, i64 %copybytes)
  br label %setnull

setnull:
  %nullidx = add i64 %argslen, 1
  %nullslot = getelementptr ptr, ptr %argv, i64 %nullidx
  store ptr null, ptr %nullslot, align 8

  %pipefd = alloca [2 x i32], align 4
  %pipeptr = getelementptr [2 x i32], ptr %pipefd, i32 0, i32 0
  %piperes = call i32 @pipe(ptr %pipeptr)
  %readfdp = getelementptr [2 x i32], ptr %pipefd, i32 0, i32 0
  %writefdp = getelementptr [2 x i32], ptr %pipefd, i32 0, i32 1
  %readfd = load i32, ptr %readfdp, align 4
  %writefd = load i32, ptr %writefdp, align 4

  %pid = call i32 @fork()
  %ischild = icmp eq i32 %pid, 0
  br i1 %ischild, label %child, label %parent

child:
  call i32 @close(i32 %readfd)
  call i32 @dup2(i32 %writefd, i32 1)
  call i32 @close(i32 %writefd)
  call i32 @execvp(ptr %file, ptr %argv)
  call void @_exit(i32 127)
  unreachable

parent:
  call i32 @close(i32 %writefd)
  %bufslot = call ptr @malloc(i64 24)
  %data_p = getelementptr { ptr, i64, i64 }, ptr %bufslot, i32 0, i32 0
  %len_p = getelementptr { ptr, i64, i64 }, ptr %bufslot, i32 0, i32 1
  %cap_p = getelementptr { ptr, i64, i64 }, ptr %bufslot, i32 0, i32 2
  store ptr null, ptr %data_p, align 8
  store i64 0, ptr %len_p, align 8
  store i64 0, ptr %cap_p, align 8
  %chunk = alloca [4096 x i8], align 1
  %chunkptr = getelementptr [4096 x i8], ptr %chunk, i32 0, i32 0
  br label %readloop

readloop:
  %n = call i64 @read(i32 %readfd, ptr %chunkptr, i64 4096)
  %hasdata = icmp sgt i64 %n, 0
  br i1 %hasdata, label %append, label %readdone

append:
  %curdata = load ptr, ptr %data_p, align 8
  %curlen = load i64, ptr %len_p, align 8
  %curcap = load i64, ptr %cap_p, align 8
  %needed = add i64 %curlen, %n
  %neededp1 = add i64 %needed, 1
  %needgrow = icmp sgt i64 %neededp1, %curcap
  br i1 %needgrow, label %grow, label %copy

grow:
  %cap2 = mul i64 %curcap, 2
  %pick1 = icmp sgt i64 %neededp1, %cap2
  %newcap_a = select i1 %pick1, i64 %neededp1, i64 %cap2
  %atleast64 = icmp sgt i64 %newcap_a, 64
  %newcap = select i1 %atleast64, i64 %newcap_a, i64 64
  %newdata = call ptr @realloc(ptr %curdata, i64 %newcap)
  store ptr %newdata, ptr %data_p, align 8
  store i64 %newcap, ptr %cap_p, align 8
  br label %copy

copy:
  %dataNow = load ptr, ptr %data_p, align 8
  %destptr = getelementptr i8, ptr %dataNow, i64 %curlen
  call ptr @memcpy(ptr %destptr, ptr %chunkptr, i64 %n)
  %newlen = add i64 %curlen, %n
  store i64 %newlen, ptr %len_p, align 8
  %termptr = getelementptr i8, ptr %dataNow, i64 %newlen
  store i8 0, ptr %termptr, align 1
  br label %readloop

readdone:
  call i32 @close(i32 %readfd)
  %statusslot = alloca i32, align 4
  store i32 0, ptr %statusslot, align 4
  call i32 @waitpid(i32 %pid, ptr %statusslot, i32 0)
  %status = load i32, ptr %statusslot, align 4
  %lowbyte = and i32 %status, 127
  %exitednormally = icmp eq i32 %lowbyte, 0
  br i1 %exitednormally, label %checkexitcode, label %signaled

checkexitcode:
  %exitcode = lshr i32 %status, 8
  %exitcode8 = and i32 %exitcode, 255
  %failed = icmp ne i32 %exitcode8, 0
  br i1 %failed, label %throwexit, label %success

throwexit:
  %msgbuf1len = call i64 @strlen(ptr %file)
  %msgbuf1size = add i64 %msgbuf1len, 64
  %msgbuf1 = call ptr @malloc(i64 %msgbuf1size)
  call i32 (ptr, ptr, ...) @sprintf(ptr %msgbuf1, ptr `

	part2 := `, i32 %exitcode8, ptr %file)
  %errobj1 = call ptr @malloc(i64 8)
  store ptr %msgbuf1, ptr %errobj1, align 8
  call void @__kml_throw(ptr %errobj1)
  unreachable

signaled:
  %sig = and i32 %status, 127
  %msgbuf2len = call i64 @strlen(ptr %file)
  %msgbuf2size = add i64 %msgbuf2len, 64
  %msgbuf2 = call ptr @malloc(i64 %msgbuf2size)
  call i32 (ptr, ptr, ...) @sprintf(ptr %msgbuf2, ptr `

	part3 := `, i32 %sig, ptr %file)
  %errobj2 = call ptr @malloc(i64 8)
  store ptr %msgbuf2, ptr %errobj2, align 8
  call void @__kml_throw(ptr %errobj2)
  unreachable

success:
  %finaldata = load ptr, ptr %data_p, align 8
  %isnull = icmp eq ptr %finaldata, null
  br i1 %isnull, label %emptyresult, label %havebody

emptyresult:
  %emptystr = call ptr @malloc(i64 1)
  store i8 0, ptr %emptystr, align 1
  br label %done

havebody:
  br label %done

done:
  %result = phi ptr [ %emptystr, %emptyresult ], [ %finaldata, %havebody ]
  ret ptr %result
}`

	e.emitGlobal(part1 + fmtExit + part2 + fmtSig + part3)
}

// nodePlatformName maps the Go compiler's own runtime.GOOS to the string
// Node's process.platform would report on that host — a pure compile-time
// mapping, no runtime code at all, following the same "check the Go
// compiler's own OS, since it also runs clang moments later" reasoning as
// errnoAccessor/monotonicClockID/stdinGlobalName.
func nodePlatformName() string {
	switch runtime.GOOS {
	case "windows":
		return "win32"
	default:
		return runtime.GOOS // "darwin", "linux", "freebsd", etc. already match Node's own strings
	}
}

// ensureProcessCwd declares __kml_process_cwd: the current working directory
// via POSIX getcwd(NULL, 0) — the glibc/BSD extension where a NULL buffer
// tells getcwd to malloc a buffer sized exactly as needed itself, avoiding
// the usual "grow a fixed buffer until it fits" loop entirely. Verified
// directly (not assumed) that this auto-allocating form is supported on
// both platforms this compiler targets before relying on it.
func (e *Emitter) ensureProcessCwd() {
	if e.usedProcessCwd {
		return
	}
	e.usedProcessCwd = true
	e.emitGlobal("declare ptr @getcwd(ptr noundef, i64 noundef)")
	e.emitGlobal(`
define ptr @__kml_process_cwd() {
entry:
  %r = call ptr @getcwd(ptr null, i64 0)
  ret ptr %r
}`)
}

// ensureProcessChdir declares __kml_process_chdir: changes the current
// working directory via POSIX chdir(), throwing the same "<opDesc> '<path>':
// <strerror>" Error shape fs's own failures already use (ensureFsThrow is
// generic over any path-taking operation, not fs-specific in what it needs).
func (e *Emitter) ensureProcessChdir() {
	if e.usedProcessChdir {
		return
	}
	e.usedProcessChdir = true
	e.ensureFsThrow()
	e.emitGlobal("declare i32 @chdir(ptr noundef)")
	opDescPtr := e.internString("cannot change directory to")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_process_chdir(ptr %%path) {
entry:
  %%r = call i32 @chdir(ptr %%path)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  ret void
}`, opDescPtr))
}

// ensureGetpid declares __kml_getpid: the current process ID via POSIX
// getpid(), sign-extended from the C int it actually returns to this
// compiler's i64 number representation.
func (e *Emitter) ensureGetpid() {
	if e.usedGetpid {
		return
	}
	e.usedGetpid = true
	e.emitGlobal("declare i32 @getpid()")
	e.emitGlobal(`
define i64 @__kml_getpid() {
entry:
  %r = call i32 @getpid()
  %r64 = sext i32 %r to i64
  ret i64 %r64
}`)
}

// ensureProcessKill declares __kml_process_kill: sends a signal to a process
// via POSIX kill(), throwing a catchable Error built from strerror(errno) on
// failure (e.g. ESRCH for "no such process") — the same "surface a real OS
// failure as a catchable Error" convention as everywhere else, just with a
// numeric pid/signal in the message instead of a path.
func (e *Emitter) ensureProcessKill() {
	if e.usedProcessKill {
		return
	}
	e.usedProcessKill = true
	e.ensureMalloc()
	e.ensureStrlen()
	e.ensureSprintf()
	e.ensureExceptionHelpers()
	e.ensureErrnoAccessor()
	e.ensureStrerror()
	accessor := errnoAccessor()
	e.emitGlobal("declare i32 @kill(i32 noundef, i32 noundef)")
	fmtPtr := e.internString("kill(pid=%lld, signal=%lld): %s")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_process_kill(i64 %%pid, i64 %%sig) {
entry:
  %%pid32 = trunc i64 %%pid to i32
  %%sig32 = trunc i64 %%sig to i32
  %%r = call i32 @kill(i32 %%pid32, i32 %%sig32)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  %%errno_ptr = call ptr @%s()
  %%errno_val = load i32, ptr %%errno_ptr, align 4
  %%errmsg = call ptr @strerror(i32 %%errno_val)
  %%errlen = call i64 @strlen(ptr %%errmsg)
  %%bufsize = add i64 %%errlen, 48
  %%buf = call ptr @malloc(i64 %%bufsize)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%buf, ptr %s, i64 %%pid, i64 %%sig, ptr %%errmsg)
  %%errobj = call ptr @malloc(i64 8)
  store ptr %%buf, ptr %%errobj, align 8
  call void @__kml_throw(ptr %%errobj)
  unreachable

ok:
  ret void
}`, accessor, fmtPtr))
}

// ensureFsMkdir declares __kml_fs_mkdir: creates a directory via POSIX
// mkdir(), mode 0777 (reduced by the process umask as usual — the same
// default real Node's fs.mkdirSync uses without an explicit mode option).
// Throws on failure (e.g. EEXIST if the path already exists, ENOENT if the
// parent doesn't) — matches unlinkSync's exact shape, one path argument.
func (e *Emitter) ensureFsMkdir() {
	if e.usedFsMkdir {
		return
	}
	e.usedFsMkdir = true
	e.ensureFsThrow()
	e.emitGlobal("declare i32 @mkdir(ptr noundef, i32 noundef)")
	opDescPtr := e.internString("cannot create directory")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_mkdir(ptr %%path) {
entry:
  %%r = call i32 @mkdir(ptr %%path, i32 511)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  ret void
}`, opDescPtr))
}

// ensureFsRename declares __kml_fs_rename: renames/moves a file via POSIX
// rename(). Throws on failure, using the same "<opDesc> '<path>': <reason>"
// shape as every other fs.* failure — with the *old* path in the message,
// since that's the argument the caller will recognize.
func (e *Emitter) ensureFsRename() {
	if e.usedFsRename {
		return
	}
	e.usedFsRename = true
	e.ensureFsThrow()
	e.emitGlobal("declare i32 @rename(ptr noundef, ptr noundef)")
	opDescPtr := e.internString("cannot rename")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_fs_rename(ptr %%oldpath, ptr %%newpath) {
entry:
  %%r = call i32 @rename(ptr %%oldpath, ptr %%newpath)
  %%failed = icmp ne i32 %%r, 0
  br i1 %%failed, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%oldpath)
  unreachable

ok:
  ret void
}`, opDescPtr))
}

// direntNameOffset returns struct dirent's d_name field offset (in bytes)
// on the host this compiler itself is running on (and will therefore also
// clang on) — struct dirent has no portable/stable layout across libc
// implementations, only the "d_name is a null-terminated char array
// somewhere in there" guarantee POSIX actually promises.
//
// Verified, not guessed: the Darwin offset (21) was confirmed directly by
// compiling and running a real C program on this project's own dev machine
// (offsetof(struct dirent, d_name) against Xcode's actual <dirent.h>). The
// Linux offset (19) originally came from reading glibc's own source
// (sysdeps/unix/sysv/linux/bits/dirent.h: __ino64_t d_ino (8) + __off64_t
// d_off (8) + unsigned short d_reclen (2) + unsigned char d_type (1), no
// padding before d_name since it's 1-byte-aligned char data), and was later
// independently confirmed by actually compiling and running the same
// offsetof probe inside a real x86-64 Linux container (`docker run
// --platform linux/amd64 ubuntu:24.04`) while investigating ADR-00051's
// ucontext_t bug — this number was correct all along, unlike that one.
// Both numbers assume a 64-bit build, which is this project's only target
// per its own stated scope.
func direntNameOffset() int {
	if runtime.GOOS == "darwin" {
		return 21
	}
	return 19
}

// ensureFsReaddir declares __kml_fs_readdir: lists a directory's entries
// (excluding "." and "..", matching real Node's fs.readdirSync) via POSIX
// opendir/readdir/closedir, returning a {ptr, i64} string[] aggregate grown
// with the same realloc-doubling shape __kml_fetch/__kml_exec_file_sync
// already use for their own growable buffers — just growing an array of
// ptr-sized name slots here instead of raw bytes. Each returned name is a
// malloc'd strdup() copy, independent of the OS's own dirent buffer (which
// readdir() is free to reuse/overwrite on the next call).
func (e *Emitter) ensureFsReaddir() {
	if e.usedFsReaddir {
		return
	}
	e.usedFsReaddir = true
	e.ensureFsThrow()
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureStrcmp()
	e.emitGlobal("declare ptr @opendir(ptr noundef)")
	e.emitGlobal("declare ptr @readdir(ptr noundef)")
	e.emitGlobal("declare i32 @closedir(ptr noundef)")
	e.emitGlobal("declare ptr @strdup(ptr noundef)")
	opDescPtr := e.internString("cannot open directory")
	dotPtr := e.internString(".")
	dotdotPtr := e.internString("..")
	e.emitGlobal(fmt.Sprintf(`
define {ptr, i64} @__kml_fs_readdir(ptr %%path) {
entry:
  %%dir = call ptr @opendir(ptr %%path)
  %%dirisnull = icmp eq ptr %%dir, null
  br i1 %%dirisnull, label %%fail, label %%ok

fail:
  call void @__kml_fs_throw(ptr %s, ptr %%path)
  unreachable

ok:
  %%bufslot = call ptr @malloc(i64 24)
  %%data_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 0
  %%len_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 1
  %%cap_p = getelementptr { ptr, i64, i64 }, ptr %%bufslot, i32 0, i32 2
  store ptr null, ptr %%data_p, align 8
  store i64 0, ptr %%len_p, align 8
  store i64 0, ptr %%cap_p, align 8
  br label %%readloop

readloop:
  %%ent = call ptr @readdir(ptr %%dir)
  %%entisnull = icmp eq ptr %%ent, null
  br i1 %%entisnull, label %%done, label %%gotent

gotent:
  %%nameptr = getelementptr i8, ptr %%ent, i64 %d
  %%isdot = call i32 @strcmp(ptr %%nameptr, ptr %s)
  %%isdotdot = call i32 @strcmp(ptr %%nameptr, ptr %s)
  %%isdotb = icmp eq i32 %%isdot, 0
  %%isdotdotb = icmp eq i32 %%isdotdot, 0
  %%skip = or i1 %%isdotb, %%isdotdotb
  br i1 %%skip, label %%readloop, label %%append

append:
  %%curdata = load ptr, ptr %%data_p, align 8
  %%curlen = load i64, ptr %%len_p, align 8
  %%curcap = load i64, ptr %%cap_p, align 8
  %%neededp1 = add i64 %%curlen, 1
  %%needgrow = icmp sgt i64 %%neededp1, %%curcap
  br i1 %%needgrow, label %%grow, label %%storeit

grow:
  %%cap2 = mul i64 %%curcap, 2
  %%atleast8 = icmp sgt i64 %%cap2, 8
  %%newcap = select i1 %%atleast8, i64 %%cap2, i64 8
  %%newcapbytes = mul i64 %%newcap, 8
  %%newdata = call ptr @realloc(ptr %%curdata, i64 %%newcapbytes)
  store ptr %%newdata, ptr %%data_p, align 8
  store i64 %%newcap, ptr %%cap_p, align 8
  br label %%storeit

storeit:
  %%dataNow = load ptr, ptr %%data_p, align 8
  %%namecopy = call ptr @strdup(ptr %%nameptr)
  %%slot = getelementptr ptr, ptr %%dataNow, i64 %%curlen
  store ptr %%namecopy, ptr %%slot, align 8
  %%newlen = add i64 %%curlen, 1
  store i64 %%newlen, ptr %%len_p, align 8
  br label %%readloop

done:
  call i32 @closedir(ptr %%dir)
  %%finaldata = load ptr, ptr %%data_p, align 8
  %%finallen = load i64, ptr %%len_p, align 8
  %%r0 = insertvalue {ptr, i64} undef, ptr %%finaldata, 0
  %%r1 = insertvalue {ptr, i64} %%r0, i64 %%finallen, 1
  ret {ptr, i64} %%r1
}`, opDescPtr, direntNameOffset(), dotPtr, dotdotPtr))
}

// ensureConsoleGroupDepth declares the hidden global backing
// console.group()/.groupEnd()'s nesting depth — a single process-wide
// counter (real Node's is per-console-instance, but this compiler has only
// ever had one implicit global console, so there's nothing to distinguish).
func (e *Emitter) ensureConsoleGroupDepth() {
	if e.usedConsoleGroupDepth {
		return
	}
	e.usedConsoleGroupDepth = true
	e.emitGlobal("@__kml_console_group_depth = internal global i64 0, align 8")
}

// ensureConsoleTimer declares the hidden global backing console.time()/
// .timeEnd() — a single global monotonic-time slot. V1 scope: only one
// timer can be "running" at a time, regardless of how many distinct labels
// are passed to time()/timeEnd() — real Node tracks each label
// independently. A later pass could switch this to the same Map<string,
// number> shape console.count() already uses below, if that scope ever
// actually gets felt as too narrow in practice.
func (e *Emitter) ensureConsoleTimer() {
	if e.usedConsoleTimer {
		return
	}
	e.usedConsoleTimer = true
	e.ensurePerformanceNow()
	e.emitGlobal("@__kml_console_time_start = internal global double 0.0, align 8")
}

// ensureConsoleCountMap declares the hidden global backing console.count()/
// .countReset() — a lazily-created Map<string, number>, reusing the exact
// same __kml_map_str_* runtime helpers a user-visible Map<string, number>
// already uses (ensureMapStrHelpers), just never exposed as a KML-level
// value. Unlike console.time's single-slot V1 narrowing above, this one
// matches real Node's per-label semantics exactly, since the machinery to
// do so was already sitting right there.
func (e *Emitter) ensureConsoleCountMap() {
	if e.usedConsoleCountMap {
		return
	}
	e.usedConsoleCountMap = true
	e.ensureMapStrHelpers()
	e.emitGlobal("@__kml_console_count_map = internal global ptr null, align 8")
}

// ensureMapFree declares __kml_map_free: frees a Map<K,V>/Set<T>'s own two
// backing buffers (the keys array and the values array, at fixed offsets 16
// and 24 in the 32-byte map header — the same layout ensureMapStrHelpers/
// ensureMapNumHelpers already create, shared identically by Set since a Set
// is just a Map with unit values under the hood) and then the header
// struct itself. Shallow: does NOT free the individual key/value entries
// themselves (e.g. each string key's own buffer) — only the map's own
// implementation-detail allocations, which the program has no other way to
// reach and free itself.
func (e *Emitter) ensureMapFree() {
	if e.usedMapFree {
		return
	}
	e.usedMapFree = true
	e.ensureFree()
	e.emitGlobal(`
define void @__kml_map_free(ptr %map) {
entry:
  %keys_p = getelementptr i8, ptr %map, i64 16
  %keys = load ptr, ptr %keys_p, align 8
  call void @free(ptr %keys)
  %vals_p = getelementptr i8, ptr %map, i64 24
  %vals = load ptr, ptr %vals_p, align 8
  call void @free(ptr %vals)
  call void @free(ptr %map)
  ret void
}`)
}

// ensureClosureFree declares __kml_closure_free: frees a closure's own two
// allocations — its {funcPtr, envPtr} header struct, and (if any variables
// were captured) the environment struct pointed to by the header's second
// word. Deliberately does NOT free the individual captured-variable cells
// the environment holds pointers to: those cells are heap-promoted
// (ADR-00001) specifically so multiple closures — and the enclosing scope
// itself — can share one mutable binding; freeing a cell here could free
// memory still live and in use elsewhere. Shallow free, same as
// ensureMapFree: only this closure's own two allocations, nothing it merely
// points to.
func (e *Emitter) ensureClosureFree() {
	if e.usedClosureFree {
		return
	}
	e.usedClosureFree = true
	e.ensureFree()
	e.emitGlobal(`
define void @__kml_closure_free(ptr %hdr) {
entry:
  %env_p = getelementptr { ptr, ptr }, ptr %hdr, i32 0, i32 1
  %env = load ptr, ptr %env_p, align 8
  %isnull = icmp eq ptr %env, null
  br i1 %isnull, label %skipenv, label %freeenv

freeenv:
  call void @free(ptr %env)
  br label %skipenv

skipenv:
  call void @free(ptr %hdr)
  ret void
}`)
}

// ensureTimerRuntime declares everything setTimeout/clearTimeout/
// setInterval/clearInterval need: the global timer queue (three globals —
// data pointer, length, capacity — the same "separate globals" shape
// process.argv already uses for its own ptr+len pair, rather than one
// malloc'd header struct, since there's only ever one timer queue per
// program), and four functions:
//
//	__kml_timer_schedule(ptr closure, i64 delayMs, i64 intervalMs) -> i64
//	  Appends a new entry (growing the queue via the same realloc-doubling
//	  shape __kml_fetch/__kml_exec_file_sync/__kml_fs_readdir all already
//	  use, just holding fixed-size 32-byte structs this time instead of
//	  bytes or ptrs) and returns its id. intervalMs is 0 for a one-shot
//	  setTimeout, or the repeat cadence for setInterval.
//	__kml_timer_clear(i64 id)
//	  Linear scan by id; sets that entry's intervalMs to -1 (the sentinel
//	  for "cancelled / already fired and done, never consider again" —
//	  chosen over physically removing the entry so the queue never needs
//	  compaction, and over a separate cancelled flag so every field stays
//	  a plain i64/ptr with no padding ambiguity to reason about).
//	__kml_timer_drain()
//	  Runs after the program's own top-level code finishes (see
//	  EmitProgram). Repeatedly: linear-scan for the pending (intervalMs !=
//	  -1) entry with the smallest fire time; if none, return (queue
//	  exhausted, main() can finally end); otherwise sleep via nanosleep()
//	  until it's due, call its closure, then — since the callback may
//	  itself have scheduled/cleared timers and grown/reallocated the queue
//	  — reload the queue pointer and this entry fresh before deciding
//	  whether to reschedule (intervalMs > 0, matching JS's own repeat
//	  behavior) or mark it done (intervalMs = -1).
//
// Entry layout ({ i64 id, i64 fireAtNs, i64 intervalMs, ptr closureHdr },
// 32 bytes, no padding): every field is i64 or ptr, both naturally 8-byte
// aligned, so the struct's total size and field order are unambiguous
// without needing LLVM's sizeof-via-GEP idiom.
func (e *Emitter) ensureTimerRuntime() {
	if e.usedTimers {
		return
	}
	e.usedTimers = true
	e.ensureMalloc()
	e.ensureRealloc()
	e.ensureClockGettime()
	clockID := monotonicClockID()
	e.emitGlobal("declare i32 @nanosleep(ptr noundef, ptr noundef)")
	e.emitGlobal("@__kml_timer_data = internal global ptr null, align 8")
	e.emitGlobal("@__kml_timer_len = internal global i64 0, align 8")
	e.emitGlobal("@__kml_timer_cap = internal global i64 0, align 8")
	e.emitGlobal("@__kml_timer_next_id = internal global i64 1, align 8")

	e.emitGlobal(fmt.Sprintf(`
define i64 @__kml_monotonic_ns() {
entry:
  %%ts = alloca { i64, i64 }, align 8
  %%r = call i32 @clock_gettime(i32 %s, ptr %%ts)
  %%sec_p = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 0
  %%nsec_p = getelementptr { i64, i64 }, ptr %%ts, i32 0, i32 1
  %%sec = load i64, ptr %%sec_p, align 8
  %%nsec = load i64, ptr %%nsec_p, align 8
  %%sec_ns = mul i64 %%sec, 1000000000
  %%total = add i64 %%sec_ns, %%nsec
  ret i64 %%total
}`, clockID))

	e.emitGlobal(`
define i64 @__kml_timer_schedule(ptr %closure, i64 %delayms, i64 %intervalms) {
entry:
  %len = load i64, ptr @__kml_timer_len, align 8
  %cap = load i64, ptr @__kml_timer_cap, align 8
  %data = load ptr, ptr @__kml_timer_data, align 8
  %neededp1 = add i64 %len, 1
  %needgrow = icmp sgt i64 %neededp1, %cap
  br i1 %needgrow, label %grow, label %doappend

grow:
  %cap2 = mul i64 %cap, 2
  %atleast8 = icmp sgt i64 %cap2, 8
  %newcap = select i1 %atleast8, i64 %cap2, i64 8
  %newcapbytes = mul i64 %newcap, 32
  %newdata = call ptr @realloc(ptr %data, i64 %newcapbytes)
  store ptr %newdata, ptr @__kml_timer_data, align 8
  store i64 %newcap, ptr @__kml_timer_cap, align 8
  br label %doappend

doappend:
  %dataNow = load ptr, ptr @__kml_timer_data, align 8
  %slot = getelementptr { i64, i64, i64, ptr }, ptr %dataNow, i64 %len

  %id = load i64, ptr @__kml_timer_next_id, align 8
  %nextid = add i64 %id, 1
  store i64 %nextid, ptr @__kml_timer_next_id, align 8
  %id_p = getelementptr { i64, i64, i64, ptr }, ptr %slot, i32 0, i32 0
  store i64 %id, ptr %id_p, align 8

  %now = call i64 @__kml_monotonic_ns()
  %delayns = mul i64 %delayms, 1000000
  %fireat = add i64 %now, %delayns
  %fireat_p = getelementptr { i64, i64, i64, ptr }, ptr %slot, i32 0, i32 1
  store i64 %fireat, ptr %fireat_p, align 8

  %interval_p = getelementptr { i64, i64, i64, ptr }, ptr %slot, i32 0, i32 2
  store i64 %intervalms, ptr %interval_p, align 8

  %closure_p = getelementptr { i64, i64, i64, ptr }, ptr %slot, i32 0, i32 3
  store ptr %closure, ptr %closure_p, align 8

  %newlen = add i64 %len, 1
  store i64 %newlen, ptr @__kml_timer_len, align 8

  ret i64 %id
}`)

	e.emitGlobal(`
define void @__kml_timer_clear(i64 %id) {
entry:
  %len = load i64, ptr @__kml_timer_len, align 8
  %data = load ptr, ptr @__kml_timer_data, align 8
  %ip = alloca i64, align 8
  store i64 0, ptr %ip, align 8
  br label %loop

loop:
  %i = load i64, ptr %ip, align 8
  %inbounds = icmp slt i64 %i, %len
  br i1 %inbounds, label %body, label %done

body:
  %slot = getelementptr { i64, i64, i64, ptr }, ptr %data, i64 %i
  %id_p = getelementptr { i64, i64, i64, ptr }, ptr %slot, i32 0, i32 0
  %eid = load i64, ptr %id_p, align 8
  %match = icmp eq i64 %eid, %id
  br i1 %match, label %cancelit, label %next

cancelit:
  %interval_p = getelementptr { i64, i64, i64, ptr }, ptr %slot, i32 0, i32 2
  store i64 -1, ptr %interval_p, align 8
  br label %done

next:
  %inext = add i64 %i, 1
  store i64 %inext, ptr %ip, align 8
  br label %loop

done:
  ret void
}`)

	e.emitGlobal(`
define void @__kml_timer_drain() {
entry:
  %besti = alloca i64, align 8
  %bestfire = alloca i64, align 8
  %scani = alloca i64, align 8
  %ts = alloca { i64, i64 }, align 8
  br label %outerloop

outerloop:
  %len = load i64, ptr @__kml_timer_len, align 8
  %data = load ptr, ptr @__kml_timer_data, align 8
  store i64 -1, ptr %besti, align 8
  store i64 0, ptr %bestfire, align 8
  store i64 0, ptr %scani, align 8
  br label %scanloop

scanloop:
  %si = load i64, ptr %scani, align 8
  %sinbounds = icmp slt i64 %si, %len
  br i1 %sinbounds, label %scanbody, label %scandone

scanbody:
  %sslot = getelementptr { i64, i64, i64, ptr }, ptr %data, i64 %si
  %sinterval_p = getelementptr { i64, i64, i64, ptr }, ptr %sslot, i32 0, i32 2
  %sinterval = load i64, ptr %sinterval_p, align 8
  %sdone = icmp eq i64 %sinterval, -1
  br i1 %sdone, label %scannext, label %scanconsider

scanconsider:
  %sfire_p = getelementptr { i64, i64, i64, ptr }, ptr %sslot, i32 0, i32 1
  %sfire = load i64, ptr %sfire_p, align 8
  %curbesti = load i64, ptr %besti, align 8
  %noneyet = icmp eq i64 %curbesti, -1
  br i1 %noneyet, label %scantakebest, label %scancompare

scancompare:
  %curbestfire = load i64, ptr %bestfire, align 8
  %better = icmp slt i64 %sfire, %curbestfire
  br i1 %better, label %scantakebest, label %scannext

scantakebest:
  store i64 %si, ptr %besti, align 8
  store i64 %sfire, ptr %bestfire, align 8
  br label %scannext

scannext:
  %sinext = add i64 %si, 1
  store i64 %sinext, ptr %scani, align 8
  br label %scanloop

scandone:
  %foundbest = load i64, ptr %besti, align 8
  %nomore = icmp eq i64 %foundbest, -1
  br i1 %nomore, label %alldone, label %havebest

havebest:
  %targetfire = load i64, ptr %bestfire, align 8
  %now1 = call i64 @__kml_monotonic_ns()
  %needwait = icmp sgt i64 %targetfire, %now1
  br i1 %needwait, label %dosleep, label %dofire

dosleep:
  %waitns = sub i64 %targetfire, %now1
  %waitsec = sdiv i64 %waitns, 1000000000
  %waitnsrem = srem i64 %waitns, 1000000000
  %ts_sec = getelementptr { i64, i64 }, ptr %ts, i32 0, i32 0
  %ts_nsec = getelementptr { i64, i64 }, ptr %ts, i32 0, i32 1
  store i64 %waitsec, ptr %ts_sec, align 8
  store i64 %waitnsrem, ptr %ts_nsec, align 8
  call i32 @nanosleep(ptr %ts, ptr null)
  br label %dofire

dofire:
  %data2 = load ptr, ptr @__kml_timer_data, align 8
  %fireidx = load i64, ptr %besti, align 8
  %fslot = getelementptr { i64, i64, i64, ptr }, ptr %data2, i64 %fireidx
  %fclosure_p = getelementptr { i64, i64, i64, ptr }, ptr %fslot, i32 0, i32 3
  %fclosure = load ptr, ptr %fclosure_p, align 8
  %fp_p = getelementptr { ptr, ptr }, ptr %fclosure, i32 0, i32 0
  %fp = load ptr, ptr %fp_p, align 8
  %ep_p = getelementptr { ptr, ptr }, ptr %fclosure, i32 0, i32 1
  %ep = load ptr, ptr %ep_p, align 8
  call void (ptr) %fp(ptr %ep)

  %data3 = load ptr, ptr @__kml_timer_data, align 8
  %fslot2 = getelementptr { i64, i64, i64, ptr }, ptr %data3, i64 %fireidx
  %finterval_p = getelementptr { i64, i64, i64, ptr }, ptr %fslot2, i32 0, i32 2
  %finterval = load i64, ptr %finterval_p, align 8
  %stillrepeating = icmp sgt i64 %finterval, 0
  br i1 %stillrepeating, label %reschedule, label %maybemarkdone

reschedule:
  %now2 = call i64 @__kml_monotonic_ns()
  %intervalns = mul i64 %finterval, 1000000
  %newfire = add i64 %now2, %intervalns
  %ffire_p = getelementptr { i64, i64, i64, ptr }, ptr %fslot2, i32 0, i32 1
  store i64 %newfire, ptr %ffire_p, align 8
  br label %outerloop

maybemarkdone:
  %alreadycancelled = icmp eq i64 %finterval, -1
  br i1 %alreadycancelled, label %outerloop, label %markdone

markdone:
  store i64 -1, ptr %finterval_p, align 8
  br label %outerloop

alldone:
  ret void
}`)
}

// httpSockConstants returns the platform-specific setsockopt() level/option
// values for SOL_SOCKET/SO_REUSEADDR — unlike AF_INET (2) and SOCK_STREAM
// (1), which are the same numeric value on every POSIX target this project
// builds for, these two genuinely differ: Linux defines SOL_SOCKET=1,
// SO_REUSEADDR=2, while Darwin/BSD define SOL_SOCKET=0xffff, SO_REUSEADDR=4.
// Same Go-side-runtime.GOOS-branch approach as monotonicClockID()/
// errnoAccessor() above — this compiler always builds and runs on the same
// host, so a compile-time Go-side branch is sufficient, no IR-level
// conditional needed.
func httpSockConstants() (solSocket, soReuseAddr int) {
	if runtime.GOOS == "darwin" {
		return 0xffff, 4
	}
	return 1, 2
}

// httpSockaddrFamilyBytes returns the first two bytes of a struct
// sockaddr_in, which differ by platform even though the struct's total
// size (16 bytes) and every field after it are identical: Linux packs
// sin_family as a plain 2-byte field (family=2 for AF_INET, low byte
// first on this project's little-endian targets); Darwin/BSD instead
// split those same two bytes into sin_len (=16, the struct's own total
// size) followed by a 1-byte sin_family. Port and address fields (offset
// 2 and 4) are identical on both, so only these two bytes need branching.
func httpSockaddrFamilyBytes() (byte0, byte1 int) {
	if runtime.GOOS == "darwin" {
		return 16, 2 // sin_len=16, sin_family=AF_INET
	}
	return 2, 0 // sin_family=AF_INET as a little-endian i16
}

// httpNonblockFlag returns O_NONBLOCK's numeric value — another genuine
// platform difference (Darwin: 0x4, Linux: 0x800 on both x86-64 and arm64,
// the two architectures this project targets), verified on this machine via
// a throwaway C probe (`printf("%x", O_NONBLOCK)`) rather than trusted from
// memory, matching every other libc constant this project hardcodes. Used
// by the event loop's accept path to make a freshly-accepted connection's
// fd non-blocking before handing it to its own fiber.
func httpNonblockFlag() int {
	if runtime.GOOS == "darwin" {
		return 0x4
	}
	return 0x800
}

// httpEagainErrno returns EAGAIN/EWOULDBLOCK's numeric value (35 on Darwin;
// 11 on Linux, where EAGAIN and EWOULDBLOCK are the same value — both
// verified the same way as httpNonblockFlag). A per-connection fiber's read
// loop checks the current errno against this after a failed non-blocking
// read to distinguish "no data yet, yield and retry later" from a real
// error.
func httpEagainErrno() int {
	if runtime.GOOS == "darwin" {
		return 35
	}
	return 11
}

// ucontextLayout returns sizeof(ucontext_t) and the byte offsets of
// uc_stack.ss_sp / uc_stack.ss_size / uc_link needed to hand-build one (see
// ensureFiberRuntime) — a real, confirmed platform difference found the
// hard way: this project's own CI (GitHub Actions' ubuntu-latest, x86-64)
// hung/reset connections under the fiber-based event loop until this was
// fixed, because the original implementation only ever verified these
// numbers on this dev machine (arm64 Darwin, sizeof 880) and assumed they'd
// carry over. They do not: Linux's glibc ucontext_t is a completely
// different struct, and even differs *between Linux architectures*
// (x86-64: 968 bytes; arm64: 4560 bytes — verified directly via a
// throwaway sizeof/offsetof C probe compiled and run in Docker containers
// for each target, `docker run --platform linux/amd64|linux/arm64
// ubuntu:24.04`, the same "never trust from memory" standard every other
// platform constant in this codebase already follows), while the four
// offsets happen to be identical across both Linux architectures (only the
// struct's total size differs, presumably due to a differently-sized
// register/FPU save area later in the struct) but are still completely
// different from Darwin's. Undersizing this buffer on Linux meant
// getcontext/makecontext/swapcontext wrote past the end of a too-small
// malloc'd (or, for @__kml_main_ctx, global) buffer — silent heap/global
// corruption, manifesting unpredictably depending on what happened to be
// laid out next in memory (which is exactly what the observed symptoms —
// connection resets, hangs — looked like).
func ucontextLayout() (size, ssSpOff, ssSizeOff, ucLinkOff int64) {
	if runtime.GOOS == "darwin" {
		return 880, 8, 16, 32
	}
	// Linux (glibc): offsets are identical across architectures; size isn't.
	if runtime.GOARCH == "arm64" {
		return 4560, 16, 32, 8
	}
	return 968, 16, 32, 8 // amd64 and other 64-bit Linux targets
}

// ensureHTTPThrow declares __kml_http_throw: builds "<opdesc>: <reason>"
// from the current errno via strerror() and throws it as a catchable Error
// — same shape as ensureFsThrow, just without a path argument (a bind/listen
// failure has no associated file path to report).
func (e *Emitter) ensureHTTPThrow() {
	if e.usedHTTPThrow {
		return
	}
	e.usedHTTPThrow = true
	e.ensureMalloc()
	e.ensureStrlen()
	e.ensureSprintf()
	e.ensureExceptionHelpers()
	e.ensureErrnoAccessor()
	e.ensureStrerror()
	fmtPtr := e.internString("%s: %s")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_http_throw(ptr %%opdesc) {
entry:
  %%errno_ptr = call ptr @%s()
  %%errno_val = load i32, ptr %%errno_ptr, align 4
  %%errmsg = call ptr @strerror(i32 %%errno_val)
  %%len_op = call i64 @strlen(ptr %%opdesc)
  %%len_err = call i64 @strlen(ptr %%errmsg)
  %%sum = add i64 %%len_op, %%len_err
  %%bufsize = add i64 %%sum, 8
  %%buf = call ptr @malloc(i64 %%bufsize)
  call i32 (ptr, ptr, ...) @sprintf(ptr %%buf, ptr %s, ptr %%opdesc, ptr %%errmsg)
  %%errobj = call ptr @malloc(i64 8)
  store ptr %%buf, ptr %%errobj, align 8
  call void @__kml_throw(ptr %%errobj)
  ret void
}`, errnoAccessor(), fmtPtr))
}

// ensureHTTPRuntime declares everything http.listen needs: raw POSIX socket
// primitives, a bind-and-listen helper that throws a catchable Error on
// failure, an accept-and-parse-request-line helper, a send-response-and-close
// helper, and the generalized event loop (TDD-00006 Part 1) that lets the
// listening socket's readiness and the existing timer queue (ensureTimerRuntime)
// share one select() wait instead of two competing loops.
//
// V1 scope (TDD-00004): single listener (no user-facing "close" — the two
// globals below hold at most one registered listener at a time, matching
// "V1 has no need for multiple servers"), single connection handled fully
// synchronously per accept (no concurrent request handling — TDD-00006's
// Part 2, real async suspension, is what real concurrency would need), GET
// request line only (method + path via sscanf's %s, headers/body ignored).
//
//	__kml_http_bind_and_listen(i32 port) -> i32
//	  socket()+setsockopt(SO_REUSEADDR)+bind()+listen(); throws a catchable
//	  Error (via __kml_http_throw) on any failure instead of returning -1,
//	  so the Go-emitted call site never needs its own error check.
//	__kml_http_accept_and_read(i32 listenfd) -> { i32 connfd, ptr method, ptr path }
//	  Blocking accept(), then a single blocking read() into a fixed 8KB
//	  buffer, then sscanf("%15s %2047s", ...) to pull the method and path
//	  out of the request line — headers/body deliberately ignored, same
//	  scope narrowing fetch() itself started with. connfd is -1 (method/path
//	  null) if accept() or the read came back empty — caller should just
//	  skip this dispatch turn.
//	__kml_http_send_response(i32 connfd, i64 status, ptr body)
//	  Formats a minimal HTTP/1.1 response (fixed "OK" reason phrase
//	  regardless of status — real clients determine success/failure from
//	  the numeric code, not the phrase) with Content-Length/Connection:
//	  close, writes it, closes the connection.
//	__kml_event_loop_run()
//	  The generalized drain loop: each iteration, scans the timer queue for
//	  the earliest-due entry exactly like __kml_timer_drain, builds an
//	  fd_set containing the registered listener (if any, via
//	  @__kml_listen_fd), and calls select() with a timeout computed from
//	  that earliest-due timer (blocking indefinitely if a listener is
//	  registered but no timer is pending, since select() alone can't return
//	  "nothing to wait for" the way an empty queue could return instantly).
//	  On wake: dispatches through @__kml_listen_dispatch if the listener is
//	  ready, then fires/reschedules/retires the due timer exactly like
//	  __kml_timer_drain. Loops forever once a listener is registered
//	  (matching http.listen's own "never returns" contract — no user code
//	  ever unregisters it in V1); with no listener registered it behaves
//	  identically to plain nanosleep-based timer draining, just implemented
//	  via a zero-timeout-capable select() instead.
//
// ensureFiberRuntime declares the fiber-context-switching primitive
// (ucontext.h's getcontext/makecontext/swapcontext, called directly via
// declare/call — no hand-written assembly, confirmed by direct prototyping
// during TDD-00006 Part 2) and the connection-fiber array shared by both
// http.listen (ADR-00049, one entry per accepted connection) and
// await fetch(...) (ADR-00050, reuses whichever connection fiber is
// currently running to yield/resume around an in-flight libcurl transfer —
// there is no separate fiber kind in this compiler, a fetch awaited from
// inside a connection handler just parks and resumes that same fiber).
// Entry layout ({ i64 fd, ptr ctx, ptr stack, ptr pendingFetch }, 32 bytes):
// pendingFetch is null under normal HTTP-read waiting (resume when fd is
// readable, the original ADR-00049 behavior) and non-null while this fiber
// is specifically parked on a still-in-flight fetch (resume when that
// fetch's own "done" flag is set, regardless of fd_set readiness).
func (e *Emitter) ensureFiberRuntime() {
	if e.usedFiber {
		return
	}
	e.usedFiber = true
	e.emitGlobal("declare void @getcontext(ptr noundef)")
	e.emitGlobal("declare void @makecontext(ptr noundef, ptr noundef, i32 noundef, ...)")
	e.emitGlobal("declare i32 @swapcontext(ptr noundef, ptr noundef)")
	ctxSize, _, _, _ := ucontextLayout()
	e.emitGlobal(fmt.Sprintf("@__kml_main_ctx = internal global [%d x i8] zeroinitializer, align 16", ctxSize))
	e.emitGlobal("@__kml_conn_data = internal global ptr null, align 8")
	e.emitGlobal("@__kml_conn_len = internal global i64 0, align 8")
	e.emitGlobal("@__kml_conn_cap = internal global i64 0, align 8")
	e.emitGlobal("@__kml_current_conn_idx = internal global i64 -1, align 8")
}

func (e *Emitter) ensureHTTPRuntime() {
	if e.usedHTTP {
		return
	}
	e.usedHTTP = true
	e.ensureTimerRuntime()
	e.ensureFiberRuntime()
	// __kml_event_loop_run below unconditionally references
	// @__kml_curl_multi/curl_multi_fdset/curl_multi_perform/
	// __kml_curl_drain_messages (its own "does curl have work to do"
	// checks are a runtime branch, not something Go-side codegen can
	// decide in advance — a fetch() call inside this very handler's body
	// is only discovered by buildHTTPDispatcher, called *after* this
	// function). Every symbol the loop's IR mentions must still be
	// declared/defined for the .ll to link, whether or not the program
	// ever actually calls fetch() — so http.listen always pulls in the
	// full async-fetch machinery (and, transitively, libcurl) alongside
	// its own socket runtime, not just when fetch() is textually present.
	e.ensureFetchAsync()
	e.ensureMalloc()
	e.ensureMemset()
	e.ensureFree()
	e.ensureSscanf()
	e.ensureSprintf()
	e.ensureStrlen()
	e.ensureHTTPThrow()

	e.ensureErrnoAccessor()

	e.emitGlobal("declare i32 @socket(i32 noundef, i32 noundef, i32 noundef)")
	e.emitGlobal("declare i32 @setsockopt(i32 noundef, i32 noundef, i32 noundef, ptr noundef, i32 noundef)")
	e.emitGlobal("declare i32 @bind(i32 noundef, ptr noundef, i32 noundef)")
	e.emitGlobal("declare i32 @listen(i32 noundef, i32 noundef)")
	e.emitGlobal("declare i32 @accept(i32 noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i64 @read(i32 noundef, ptr noundef, i64 noundef)")
	e.emitGlobal("declare i64 @write(i32 noundef, ptr noundef, i64 noundef)")
	e.emitGlobal("declare i32 @close(i32 noundef)")
	e.emitGlobal("declare i32 @select(i32 noundef, ptr noundef, ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i16 @htons(i16 noundef)")
	e.emitGlobal("declare i32 @fcntl(i32 noundef, i32 noundef, ...)")

	e.emitGlobal("@__kml_listen_fd = internal global i32 -1, align 4")
	e.emitGlobal("@__kml_listen_dispatch = internal global ptr null, align 8")
	e.emitGlobal("@__kml_listen_handler = internal global ptr null, align 8")

	solSocket, soReuseAddr := httpSockConstants()
	fam0, fam1 := httpSockaddrFamilyBytes()

	e.emitGlobal(fmt.Sprintf(`
define i32 @__kml_http_bind_and_listen(i32 %%port) {
entry:
  %%fd = call i32 @socket(i32 2, i32 1, i32 0)
  %%fdok = icmp sge i32 %%fd, 0
  br i1 %%fdok, label %%setopt, label %%failnofd

setopt:
  %%one = alloca i32, align 4
  store i32 1, ptr %%one, align 4
  call i32 @setsockopt(i32 %%fd, i32 %d, i32 %d, ptr %%one, i32 4)

  %%addr = alloca [16 x i8], align 4
  call ptr @memset(ptr %%addr, i32 0, i64 16)
  store i8 %d, ptr %%addr, align 1
  %%b1p = getelementptr i8, ptr %%addr, i64 1
  store i8 %d, ptr %%b1p, align 1
  %%portu16 = trunc i32 %%port to i16
  %%portn = call i16 @htons(i16 %%portu16)
  %%portp = getelementptr i8, ptr %%addr, i64 2
  store i16 %%portn, ptr %%portp, align 1

  %%bindrc = call i32 @bind(i32 %%fd, ptr %%addr, i32 16)
  %%bindok = icmp eq i32 %%bindrc, 0
  br i1 %%bindok, label %%dolisten, label %%failwithfd

dolisten:
  %%listenrc = call i32 @listen(i32 %%fd, i32 128)
  %%listenok = icmp eq i32 %%listenrc, 0
  br i1 %%listenok, label %%success, label %%failwithfd

success:
  ret i32 %%fd

failwithfd:
  call i32 @close(i32 %%fd)
  call void @__kml_http_throw(ptr %s)
  unreachable

failnofd:
  call void @__kml_http_throw(ptr %s)
  unreachable
}`, solSocket, soReuseAddr, fam0, fam1,
		e.internString("http.listen: failed to bind or listen"),
		e.internString("http.listen: failed to create socket")))

	// __kml_http_append_conn: appends a new { i64 fd, ptr ctx, ptr stack,
	// ptr pendingFetch } entry (growable, realloc-doubling, same shape as
	// the timer queue) for a freshly-accepted connection, builds its fiber
	// (a fresh ucontext_t + a 64KB stack, uc_link back to the main/scheduler
	// context so the fiber function returning normally resumes the
	// scheduler automatically), and immediately swaps into it once — the
	// same "launch it now" step confirmed working in this feature's
	// prototyping spike. The fiber entry point is always
	// @__kml_listen_dispatch's stored pointer (the per-call-site-specialized
	// dispatcher built by emit_http.go). pendingFetch starts null (normal
	// fd-readiness-based waiting) — see ensureFiberRuntime's doc comment.
	// ctxSize/ssSpOff/ssSizeOff/ucLinkOff: see ucontextLayout's doc comment
	// — sizeof(ucontext_t) and its field offsets are NOT portable across
	// platforms (a real bug found via a failing Linux CI run, fixed here).
	ctxSize, ssSpOff, ssSizeOff, ucLinkOff := ucontextLayout()
	e.emitGlobal(`
define void @__kml_http_append_conn(i32 %fd) {
entry:
  %len = load i64, ptr @__kml_conn_len, align 8
  %cap = load i64, ptr @__kml_conn_cap, align 8
  %data = load ptr, ptr @__kml_conn_data, align 8
  %neededp1 = add i64 %len, 1
  %needgrow = icmp sgt i64 %neededp1, %cap
  br i1 %needgrow, label %grow, label %doappend

grow:
  %cap2 = mul i64 %cap, 2
  %atleast8 = icmp sgt i64 %cap2, 8
  %newcap = select i1 %atleast8, i64 %cap2, i64 8
  %newcapbytes = mul i64 %newcap, 32
  %newdata = call ptr @realloc(ptr %data, i64 %newcapbytes)
  store ptr %newdata, ptr @__kml_conn_data, align 8
  store i64 %newcap, ptr @__kml_conn_cap, align 8
  br label %doappend

doappend:
  %dataNow = load ptr, ptr @__kml_conn_data, align 8
  %slot = getelementptr { i64, ptr, ptr, ptr }, ptr %dataNow, i64 %len

  %fd64 = sext i32 %fd to i64
  %fd_p = getelementptr { i64, ptr, ptr, ptr }, ptr %slot, i32 0, i32 0
  store i64 %fd64, ptr %fd_p, align 8

  %ctx = call ptr @malloc(i64 ` + fmt.Sprintf("%d", ctxSize) + `)
  %stack = call ptr @malloc(i64 65536)
  call void @getcontext(ptr %ctx)
  %ss_sp_p = getelementptr i8, ptr %ctx, i64 ` + fmt.Sprintf("%d", ssSpOff) + `
  store ptr %stack, ptr %ss_sp_p, align 8
  %ss_size_p = getelementptr i8, ptr %ctx, i64 ` + fmt.Sprintf("%d", ssSizeOff) + `
  store i64 65536, ptr %ss_size_p, align 8
  %uc_link_p = getelementptr i8, ptr %ctx, i64 ` + fmt.Sprintf("%d", ucLinkOff) + `
  store ptr @__kml_main_ctx, ptr %uc_link_p, align 8
  %dfp = load ptr, ptr @__kml_listen_dispatch, align 8
  call void (ptr, ptr, i32, ...) @makecontext(ptr %ctx, ptr %dfp, i32 0)

  %ctx_p = getelementptr { i64, ptr, ptr, ptr }, ptr %slot, i32 0, i32 1
  store ptr %ctx, ptr %ctx_p, align 8
  %stack_p = getelementptr { i64, ptr, ptr, ptr }, ptr %slot, i32 0, i32 2
  store ptr %stack, ptr %stack_p, align 8
  %pf_p = getelementptr { i64, ptr, ptr, ptr }, ptr %slot, i32 0, i32 3
  store ptr null, ptr %pf_p, align 8

  %newlen = add i64 %len, 1
  store i64 %newlen, ptr @__kml_conn_len, align 8

  store i64 %len, ptr @__kml_current_conn_idx, align 8
  %swaprc = call i32 @swapcontext(ptr @__kml_main_ctx, ptr %ctx)
  ret void
}`)

	respFmt := e.internString("HTTP/1.1 %lld OK\r\nContent-Length: %lld\r\nConnection: close\r\n\r\n%s")
	e.emitGlobal(fmt.Sprintf(`
define void @__kml_http_send_response(i32 %%connfd, i64 %%status, ptr %%body) {
entry:
  %%bodylen = call i64 @strlen(ptr %%body)
  %%bufsize1 = add i64 %%bodylen, 128
  %%respbuf = call ptr @malloc(i64 %%bufsize1)
  %%n = call i32 (ptr, ptr, ...) @sprintf(ptr %%respbuf, ptr %s, i64 %%status, i64 %%bodylen, ptr %%body)
  %%n64 = sext i32 %%n to i64
  call i64 @write(i32 %%connfd, ptr %%respbuf, i64 %%n64)
  call void @free(ptr %%respbuf)
  call i32 @close(i32 %%connfd)
  ret void
}`, respFmt))

	e.emitGlobal(`
define void @__kml_event_loop_run() {
entry:
  %besti = alloca i64, align 8
  %bestfire = alloca i64, align 8
  %scani = alloca i64, align 8
  %fdset = alloca [128 x i8], align 8
  %wfdset = alloca [128 x i8], align 8
  %efdset = alloca [128 x i8], align 8
  %maxfd = alloca i32, align 4
  %fsi = alloca i64, align 8
  %curlmaxfd = alloca i32, align 4
  %tv = alloca { i64, i64 }, align 8
  %runningp2 = alloca i32, align 4
  %rsi = alloca i64, align 8
  br label %outerloop

outerloop:
  %len = load i64, ptr @__kml_timer_len, align 8
  %data = load ptr, ptr @__kml_timer_data, align 8
  store i64 -1, ptr %besti, align 8
  store i64 0, ptr %bestfire, align 8
  store i64 0, ptr %scani, align 8
  br label %scanloop

scanloop:
  %si = load i64, ptr %scani, align 8
  %sinbounds = icmp slt i64 %si, %len
  br i1 %sinbounds, label %scanbody, label %scandone

scanbody:
  %sslot = getelementptr { i64, i64, i64, ptr }, ptr %data, i64 %si
  %sinterval_p = getelementptr { i64, i64, i64, ptr }, ptr %sslot, i32 0, i32 2
  %sinterval = load i64, ptr %sinterval_p, align 8
  %sdone = icmp eq i64 %sinterval, -1
  br i1 %sdone, label %scannext, label %scanconsider

scanconsider:
  %sfire_p = getelementptr { i64, i64, i64, ptr }, ptr %sslot, i32 0, i32 1
  %sfire = load i64, ptr %sfire_p, align 8
  %curbesti = load i64, ptr %besti, align 8
  %noneyet = icmp eq i64 %curbesti, -1
  br i1 %noneyet, label %scantakebest, label %scancompare

scancompare:
  %curbestfire = load i64, ptr %bestfire, align 8
  %better = icmp slt i64 %sfire, %curbestfire
  br i1 %better, label %scantakebest, label %scannext

scantakebest:
  store i64 %si, ptr %besti, align 8
  store i64 %sfire, ptr %bestfire, align 8
  br label %scannext

scannext:
  %sinext = add i64 %si, 1
  store i64 %sinext, ptr %scani, align 8
  br label %scanloop

scandone:
  %foundbest = load i64, ptr %besti, align 8
  %havetimer = icmp ne i64 %foundbest, -1
  %listenfd = load i32, ptr @__kml_listen_fd, align 4
  %haslistener = icmp sge i32 %listenfd, 0
  %anywork = or i1 %havetimer, %haslistener
  br i1 %anywork, label %dowork, label %alldone

dowork:
  call ptr @memset(ptr %fdset, i32 0, i64 128)
  call ptr @memset(ptr %wfdset, i32 0, i64 128)
  call ptr @memset(ptr %efdset, i32 0, i64 128)
  store i32 -1, ptr %maxfd, align 4
  br i1 %haslistener, label %setfd, label %skipsetfd

setfd:
  %fddiv8 = sdiv i32 %listenfd, 8
  %fdmod8 = srem i32 %listenfd, 8
  %fddiv8_64 = sext i32 %fddiv8 to i64
  %byteptr = getelementptr i8, ptr %fdset, i64 %fddiv8_64
  %bitpos8 = trunc i32 %fdmod8 to i8
  %bitmask = shl i8 1, %bitpos8
  %oldbyte = load i8, ptr %byteptr, align 1
  %newbyte = or i8 %oldbyte, %bitmask
  store i8 %newbyte, ptr %byteptr, align 1
  store i32 %listenfd, ptr %maxfd, align 4
  br label %skipsetfd

skipsetfd:
  ; Add every still-active (fd >= 0) connection's fd into the same fd_set,
  ; tracking the overall highest fd for select()'s nfds argument.
  %clen = load i64, ptr @__kml_conn_len, align 8
  %cdata = load ptr, ptr @__kml_conn_data, align 8
  store i64 0, ptr %fsi, align 8
  br label %fsetloop

fsetloop:
  %fi = load i64, ptr %fsi, align 8
  %finb = icmp slt i64 %fi, %clen
  br i1 %finb, label %fsetbody, label %fsetdone

fsetbody:
  %fslot0 = getelementptr { i64, ptr, ptr, ptr }, ptr %cdata, i64 %fi
  %ffd_p = getelementptr { i64, ptr, ptr, ptr }, ptr %fslot0, i32 0, i32 0
  %ffdv = load i64, ptr %ffd_p, align 8
  %factive = icmp sge i64 %ffdv, 0
  br i1 %factive, label %fsetbit, label %fsetnext

fsetbit:
  %ffdiv8 = sdiv i64 %ffdv, 8
  %ffmod8 = srem i64 %ffdv, 8
  %ffbyteptr = getelementptr i8, ptr %fdset, i64 %ffdiv8
  %ffmod8_8 = trunc i64 %ffmod8 to i8
  %ffmask = shl i8 1, %ffmod8_8
  %ffoldbyte = load i8, ptr %ffbyteptr, align 1
  %ffnewbyte = or i8 %ffoldbyte, %ffmask
  store i8 %ffnewbyte, ptr %ffbyteptr, align 1
  %ffdv32 = trunc i64 %ffdv to i32
  %fcurmax = load i32, ptr %maxfd, align 4
  %fisbigger = icmp sgt i32 %ffdv32, %fcurmax
  br i1 %fisbigger, label %fupdatemax, label %fsetnext

fupdatemax:
  store i32 %ffdv32, ptr %maxfd, align 4
  br label %fsetnext

fsetnext:
  %finext = add i64 %fi, 1
  store i64 %finext, ptr %fsi, align 8
  br label %fsetloop

fsetdone:
  ; Merge libcurl's own fd_sets (its in-flight transfers' sockets) into the
  ; same read/write/exc sets, if any await fetch(...) has ever created the
  ; multi handle — curl_multi_fdset ORs its bits in rather than clearing
  ; the sets first, so this is safe to call after our own fds are already
  ; set. See ensureFetchAsync (emit_async.go's fetch-await path).
  %curlmulti = load ptr, ptr @__kml_curl_multi, align 8
  %hascurl = icmp ne ptr %curlmulti, null
  br i1 %hascurl, label %mergecurlfds, label %skipmergecurlfds

mergecurlfds:
  store i32 -1, ptr %curlmaxfd, align 4
  call i32 @curl_multi_fdset(ptr %curlmulti, ptr %fdset, ptr %wfdset, ptr %efdset, ptr %curlmaxfd)
  %curlmaxfdv = load i32, ptr %curlmaxfd, align 4
  %curmaxfd1 = load i32, ptr %maxfd, align 4
  %curlbigger = icmp sgt i32 %curlmaxfdv, %curmaxfd1
  br i1 %curlbigger, label %takecurlmax, label %skipmergecurlfds

takecurlmax:
  store i32 %curlmaxfdv, ptr %maxfd, align 4
  br label %skipmergecurlfds

skipmergecurlfds:
  %maxfdv = load i32, ptr %maxfd, align 4
  %nfds = add i32 %maxfdv, 1

  br i1 %havetimer, label %timeoutpath, label %notimeoutpath

timeoutpath:
  %targetfire = load i64, ptr %bestfire, align 8
  %now1 = call i64 @__kml_monotonic_ns()
  %rawwait = sub i64 %targetfire, %now1
  %negwait = icmp slt i64 %rawwait, 0
  %waitns = select i1 %negwait, i64 0, i64 %rawwait
  %waitsec = sdiv i64 %waitns, 1000000000
  %waitnsrem = srem i64 %waitns, 1000000000
  %waitusec = sdiv i64 %waitnsrem, 1000
  %tv_sec = getelementptr { i64, i64 }, ptr %tv, i32 0, i32 0
  %tv_usec = getelementptr { i64, i64 }, ptr %tv, i32 0, i32 1
  store i64 %waitsec, ptr %tv_sec, align 8
  store i64 %waitusec, ptr %tv_usec, align 8
  %selrc1 = call i32 @select(i32 %nfds, ptr %fdset, ptr %wfdset, ptr %efdset, ptr %tv)
  br label %afterselect

notimeoutpath:
  %selrc2 = call i32 @select(i32 %nfds, ptr %fdset, ptr %wfdset, ptr %efdset, ptr null)
  br label %afterselect

afterselect:
  br i1 %hascurl, label %docurlperform, label %checklistener

docurlperform:
  call i32 @curl_multi_perform(ptr %curlmulti, ptr %runningp2)
  call void @__kml_curl_drain_messages()
  br label %checklistener

checklistener:
  br i1 %haslistener, label %checkisset, label %scanconn

checkisset:
  %fddiv8b = sdiv i32 %listenfd, 8
  %fdmod8b = srem i32 %listenfd, 8
  %fddiv8b_64 = sext i32 %fddiv8b to i64
  %byteptrb = getelementptr i8, ptr %fdset, i64 %fddiv8b_64
  %bitpos8b = trunc i32 %fdmod8b to i8
  %bitmaskb = shl i8 1, %bitpos8b
  %bytevalb = load i8, ptr %byteptrb, align 1
  %maskedb = and i8 %bytevalb, %bitmaskb
  %ready = icmp ne i8 %maskedb, 0
  br i1 %ready, label %doaccept, label %scanconn

doaccept:
  %newfd = call i32 @accept(i32 %listenfd, ptr null, ptr null)
  %acceptok = icmp sge i32 %newfd, 0
  br i1 %acceptok, label %setnonblock, label %scanconn

setnonblock:
  %curflags = call i32 (i32, i32, ...) @fcntl(i32 %newfd, i32 3)
  %newflags = or i32 %curflags, ` + fmt.Sprintf("%d", httpNonblockFlag()) + `
  call i32 (i32, i32, ...) @fcntl(i32 %newfd, i32 4, i32 %newflags)
  call void @__kml_http_append_conn(i32 %newfd)
  br label %scanconn

scanconn:
  ; Resume every connection fiber whose fd came back ready in the fd_set
  ; select() just populated (a fiber that finished sets its own entry's fd
  ; to -1 right before returning, so "still >= 0 after resume" means it
  ; genuinely yielded again and should keep being watched next iteration).
  store i64 0, ptr %rsi, align 8
  br label %rscanloop

rscanloop:
  %ri = load i64, ptr %rsi, align 8
  %rclen = load i64, ptr @__kml_conn_len, align 8
  %rinb = icmp slt i64 %ri, %rclen
  br i1 %rinb, label %rscanbody, label %checktimerfire

rscanbody:
  %rcdata = load ptr, ptr @__kml_conn_data, align 8
  %rslot = getelementptr { i64, ptr, ptr, ptr }, ptr %rcdata, i64 %ri
  %rfd_p = getelementptr { i64, ptr, ptr, ptr }, ptr %rslot, i32 0, i32 0
  %rfdv = load i64, ptr %rfd_p, align 8
  %ractive = icmp sge i64 %rfdv, 0
  br i1 %ractive, label %rcheckpending, label %rscannext

rcheckpending:
  ; A fiber parked on await fetch(...) (pendingFetch != null) is resumed
  ; when that specific fetch is done, regardless of fd_set readiness —
  ; its own connection fd isn't what it's waiting on right now.
  %rpf_p = getelementptr { i64, ptr, ptr, ptr }, ptr %rslot, i32 0, i32 3
  %rpf = load ptr, ptr %rpf_p, align 8
  %rhaspending = icmp ne ptr %rpf, null
  br i1 %rhaspending, label %rcheckfetchdone, label %rcheckready

rcheckfetchdone:
  %rpf_done_p = getelementptr { ptr, ptr, i64, i64, i64 }, ptr %rpf, i32 0, i32 2
  %rpf_done = load i64, ptr %rpf_done_p, align 8
  %rfetchready = icmp ne i64 %rpf_done, 0
  br i1 %rfetchready, label %rresume, label %rscannext

rcheckready:
  %rdiv8 = sdiv i64 %rfdv, 8
  %rmod8 = srem i64 %rfdv, 8
  %rbyteptr = getelementptr i8, ptr %fdset, i64 %rdiv8
  %rmod8_8 = trunc i64 %rmod8 to i8
  %rmask = shl i8 1, %rmod8_8
  %rbyteval = load i8, ptr %rbyteptr, align 1
  %rmasked = and i8 %rbyteval, %rmask
  %rready = icmp ne i8 %rmasked, 0
  br i1 %rready, label %rresume, label %rscannext

rresume:
  store i64 %ri, ptr @__kml_current_conn_idx, align 8
  %rctx_p = getelementptr { i64, ptr, ptr, ptr }, ptr %rslot, i32 0, i32 1
  %rctxptr = load ptr, ptr %rctx_p, align 8
  call i32 @swapcontext(ptr @__kml_main_ctx, ptr %rctxptr)
  br label %rscannext

rscannext:
  %rinext = add i64 %ri, 1
  store i64 %rinext, ptr %rsi, align 8
  br label %rscanloop

checktimerfire:
  br i1 %havetimer, label %checkdue, label %outerloop

checkdue:
  %targetfire2 = load i64, ptr %bestfire, align 8
  %now2 = call i64 @__kml_monotonic_ns()
  %isdue = icmp sge i64 %now2, %targetfire2
  br i1 %isdue, label %dofire, label %outerloop

dofire:
  %data2 = load ptr, ptr @__kml_timer_data, align 8
  %fireidx = load i64, ptr %besti, align 8
  %fslot = getelementptr { i64, i64, i64, ptr }, ptr %data2, i64 %fireidx
  %fclosure_p = getelementptr { i64, i64, i64, ptr }, ptr %fslot, i32 0, i32 3
  %fclosure = load ptr, ptr %fclosure_p, align 8
  %fp_p = getelementptr { ptr, ptr }, ptr %fclosure, i32 0, i32 0
  %fp = load ptr, ptr %fp_p, align 8
  %ep_p = getelementptr { ptr, ptr }, ptr %fclosure, i32 0, i32 1
  %ep = load ptr, ptr %ep_p, align 8
  call void (ptr) %fp(ptr %ep)

  %data3 = load ptr, ptr @__kml_timer_data, align 8
  %fslot2 = getelementptr { i64, i64, i64, ptr }, ptr %data3, i64 %fireidx
  %finterval_p = getelementptr { i64, i64, i64, ptr }, ptr %fslot2, i32 0, i32 2
  %finterval = load i64, ptr %finterval_p, align 8
  %stillrepeating = icmp sgt i64 %finterval, 0
  br i1 %stillrepeating, label %reschedule, label %maybemarkdone

reschedule:
  %now3 = call i64 @__kml_monotonic_ns()
  %intervalns = mul i64 %finterval, 1000000
  %newfire = add i64 %now3, %intervalns
  %ffire_p = getelementptr { i64, i64, i64, ptr }, ptr %fslot2, i32 0, i32 1
  store i64 %newfire, ptr %ffire_p, align 8
  br label %outerloop

maybemarkdone:
  %alreadycancelled = icmp eq i64 %finterval, -1
  br i1 %alreadycancelled, label %outerloop, label %markdone

markdone:
  store i64 -1, ptr %finterval_p, align 8
  br label %outerloop

alldone:
  ret void
}`)
}
