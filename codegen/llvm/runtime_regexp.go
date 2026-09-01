package llvm

// PCRE2 8-bit API option constants and the "match to end of string"
// zero-terminated-length sentinel. These are C preprocessor macros in
// pcre2.h, not real linkable symbols, so their numeric values are
// hardcoded here — same convention emit_url.go's CURLUPart values
// (curluPartURL etc.) already establish for libcurl. Verified directly
// against the real pcre2.h (extracted from the Ubuntu/Debian
// libpcre2-dev 10.42-4ubuntu2.1 package, not trusted from memory) on the
// x86-64 Linux development machine — re-verify on the Apple Silicon Mac
// before relying on these further, per the project's own standing rule on
// platform-sensitive claims. pcre2_code/pcre2_match_data themselves are
// opaque pointers this code never lays out itself, so unlike past
// cross-platform struct-layout bugs this project has hit (ucontext_t,
// GC_stackbottom), only these numeric constants carry any re-verification
// risk, not a memory layout.
const (
	pcre2ZeroTerminated   = -1 // ~(PCRE2_SIZE)0, i.e. all bits set, as a signed i64
	pcre2CaseLess         = 8
	pcre2Multiline        = 1024
	pcre2Dotall           = 32
	pcre2InfoCaptureCount = 4  // pcre2_pattern_info()'s request-type enum, not an option bit
	pcre2Unset            = -1 // PCRE2_UNSET, ~(PCRE2_SIZE)0 — same bit pattern as pcre2ZeroTerminated but a distinct meaning (an ovector pair that didn't participate in the match, e.g. an optional capture group), kept as its own named constant for clarity at call sites

	// ECMAScript-alignment compile options (TDD-00067 Options A/B). Same
	// hardcoded-macro convention and re-verification rule as the block above:
	// these values were read directly from the real pcre2.h (Homebrew
	// libpcre2 10.47, /opt/homebrew/include/pcre2.h) on the Apple Silicon Mac
	// — re-verify against the linked pcre2.h before relying on them on the
	// x86-64 Linux machine, per the project's platform-sensitive-claim rule.
	pcre2AltBSUX           = 2      // PCRE2_ALT_BSUX (0x00000002) — ECMAScript \uXXXX/\xXX/\0 escapes (Option A)
	pcre2DollarEndOnly     = 16     // PCRE2_DOLLAR_ENDONLY (0x00000010) — $ anchors at true end, applied only when the `m` flag is absent (Option A)
	pcre2MatchUnsetBackref = 512    // PCRE2_MATCH_UNSET_BACKREF (0x00000200) — backref to an unset group matches empty (Option A)
	pcre2UCP               = 131072 // PCRE2_UCP (0x00020000) — Unicode properties for \w/\s/\b. Reserved for Option C: NOT used by es-unicode, since PCRE2's UCP class tables diverge from ES (e.g. UCP \s matches U+180E, which ES dropped in Unicode 6.3) — see regexModeOpts / TDD-00067
	pcre2UTF               = 524288 // PCRE2_UTF (0x00080000) — match on code points, not raw bytes (Option B)
	pcre2NewlineAny        = 4      // PCRE2_NEWLINE_ANY — pcre2_set_newline() value, not an option bit; closest convention to ES's line terminators (Option B)
)

// ensureRegexCompile declares PCRE2's pattern-compilation API — used by
// `new RegExp(...)`/a `/pattern/flags` literal (emit_regexp.go) to compile
// a pattern once at construction time into an opaque pcre2_code* handle
// kept alive for the RegExp object's lifetime (RegexHandleField). Links
// libpcre2-8 only for a program that actually constructs a RegExp — see
// requireLink's doc comment. Match-time declarations (pcre2_match_8 and
// friends) are a later stage's concern, not needed yet.
func (e *Emitter) ensureRegexCompile() {
	if e.usedRegexCompile {
		return
	}
	e.usedRegexCompile = true
	e.requireLink("pcre2-8")
	e.emitGlobal("declare ptr @pcre2_compile_8(ptr noundef, i64 noundef, i32 noundef, ptr noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @pcre2_get_error_message_8(i32 noundef, ptr noundef, i64 noundef)")
	e.ensureRegexParseFlags()
}

// ensureRegexCompileContext declares the PCRE2 compile-context API used by
// the `es-unicode` mode (TDD-00067 Option B) to set PCRE2_NEWLINE_ANY before
// compiling — the newline convention is a compile-context property, not one
// of the option bits threaded through pcre2_compile_8's options argument. A
// context is created, configured, passed as pcre2_compile_8's final (context)
// argument in place of the `ptr null` the other modes pass, and freed right
// after the compile (the compiled pcre2_code copies whatever it needs out of
// the context, so it need not outlive the compile call). Only reached when
// the resolved regex mode actually needs a context, so a program compiled in
// `pcre`/`es-ascii` mode never emits these decls.
func (e *Emitter) ensureRegexCompileContext() {
	if e.usedRegexCompileContext {
		return
	}
	e.usedRegexCompileContext = true
	e.requireLink("pcre2-8")
	e.emitGlobal("declare ptr @pcre2_compile_context_create_8(ptr noundef)")
	e.emitGlobal("declare i32 @pcre2_set_newline_8(ptr noundef, i32 noundef)")
	e.emitGlobal("declare void @pcre2_compile_context_free_8(ptr noundef)")
}

// ensureRegexUTF8Width declares __kml_regex_utf8_width(str, byteOff): the
// number of bytes (1–4) the UTF-8 code point starting at str[byteOff]
// occupies, decoded purely from the lead byte's high bits (a continuation
// byte or any malformed lead is treated as width 1 — permissive, matching
// this project's other malformed-UTF handling). Used by the global empty-
// match advance (both byte-index and es-utf16 modes) to step past a zero-
// length match by a whole code point rather than a single byte, so a
// PCRE2_UTF subject's next start offset never lands mid-code-point, and by
// the byte↔UTF-16 converters below.
func (e *Emitter) ensureRegexUTF8Width() {
	if e.usedRegexUTF8Width {
		return
	}
	e.usedRegexUTF8Width = true
	e.emitGlobal(`
define i64 @__kml_regex_utf8_width(ptr %str, i64 %off) {
entry:
  %p = getelementptr i8, ptr %str, i64 %off
  %b = load i8, ptr %p, align 1
  %bz = zext i8 %b to i32
  %lt80 = icmp ult i32 %bz, 128
  br i1 %lt80, label %one, label %c2
c2:
  %and_e0 = and i32 %bz, 224
  %is_c0 = icmp eq i32 %and_e0, 192
  br i1 %is_c0, label %two, label %c3
c3:
  %and_f0 = and i32 %bz, 240
  %is_e0 = icmp eq i32 %and_f0, 224
  br i1 %is_e0, label %three, label %c4
c4:
  %and_f8 = and i32 %bz, 248
  %is_f0 = icmp eq i32 %and_f8, 240
  br i1 %is_f0, label %four, label %one
one:
  ret i64 1
two:
  ret i64 2
three:
  ret i64 3
four:
  ret i64 4
}`)
}

// ensureRegexUTF16Convert declares the two byte↔UTF-16 code-unit offset
// converters the es-utf16 mode (TDD-00067 Stage 3) applies at every user-
// visible offset boundary. PCRE2_8's ovector offsets — and this compiler's
// whole string layer — are UTF-8 byte positions; es-utf16 reports/consumes
// true UTF-16 code-unit positions instead, so a supplementary code point
// (4-byte UTF-8, one surrogate *pair* in UTF-16) counts as two units.
//
//	__kml_regex_byte_to_utf16(str, byteLen): UTF-16 code units in str[0:byteLen].
//	__kml_regex_utf16_to_byte(str, target):  byte offset of the target-th UTF-16
//	  unit, stopping at the NUL terminator so an out-of-range target clamps to
//	  the string end rather than reading past it. A target landing mid-surrogate
//	  (only reachable via a hand-set lastIndex) resolves to the following code
//	  point's byte start — a best-effort for a pathological input, never a read
//	  past the end.
//
// Both walk code points via __kml_regex_utf8_width; callers must have already
// screened the PCRE2 "no match" sentinel (-1) — byte_to_utf16 assumes a
// non-negative length.
func (e *Emitter) ensureRegexUTF16Convert() {
	if e.usedRegexUTF16Convert {
		return
	}
	e.usedRegexUTF16Convert = true
	e.ensureRegexUTF8Width()
	e.emitGlobal(`
define i64 @__kml_regex_byte_to_utf16(ptr %str, i64 %bytelen) {
entry:
  br label %cond
cond:
  %i = phi i64 [ 0, %entry ], [ %inext, %body ]
  %u = phi i64 [ 0, %entry ], [ %unext, %body ]
  %done = icmp uge i64 %i, %bytelen
  br i1 %done, label %ret, label %body
body:
  %w = call i64 @__kml_regex_utf8_width(ptr %str, i64 %i)
  %is4 = icmp eq i64 %w, 4
  %units = select i1 %is4, i64 2, i64 1
  %unext = add i64 %u, %units
  %inext = add i64 %i, %w
  br label %cond
ret:
  ret i64 %u
}

define i64 @__kml_regex_utf16_to_byte(ptr %str, i64 %target) {
entry:
  br label %cond
cond:
  %i = phi i64 [ 0, %entry ], [ %inext, %body ]
  %u = phi i64 [ 0, %entry ], [ %unext, %body ]
  %reached = icmp uge i64 %u, %target
  br i1 %reached, label %ret, label %chknul
chknul:
  %p = getelementptr i8, ptr %str, i64 %i
  %b = load i8, ptr %p, align 1
  %isnul = icmp eq i8 %b, 0
  br i1 %isnul, label %atnul, label %body
atnul:
  ; Target not yet reached at end of string: extend past the terminator by the
  ; remaining unit deficit (one byte per unit) rather than clamping to strlen.
  ; The global empty-match advance relies on this — after a zero-length match
  ; at the end, its strlen+1 target must map to a byte start > strlen so PCRE2
  ; rejects it and the driver loop terminates (a clamp to strlen would re-match
  ; the same end-of-string empty match forever).
  %deficit = sub i64 %target, %u
  %ext = add i64 %i, %deficit
  ret i64 %ext
body:
  %w = call i64 @__kml_regex_utf8_width(ptr %str, i64 %i)
  %is4 = icmp eq i64 %w, 4
  %units = select i1 %is4, i64 2, i64 1
  %unext = add i64 %u, %units
  %inext = add i64 %i, %w
  br label %cond
ret:
  ret i64 %i
}`)
}

// ensureRegexESNormalize declares __kml_regex_es_normalize(pattern, dotAll):
// the ECMAScript-dialect source normalization pass for `-regex=ecmascript`
// (TDD-00067 Option C), returning a freshly malloc'd, rewritten pattern (the
// caller keeps the original for `.source`). It runs at RUNTIME, before
// pcre2_compile, because a pattern can be a runtime value (`new
// RegExp(someVar)`), not only a literal — a compile-time Go rewrite would
// miss the dynamic case.
//
// v1 performs exactly one transform: an unescaped top-level `.` (outside a
// character class, and only when the `s`/dotAll flag is absent) is rewritten
// to the class matching everything except the four ECMAScript line
// terminators (\n \r U+2028 U+2029) — ES's exact definition of what `.`
// matches. es-unicode approximates this with
// PCRE2_NEWLINE_ANY, which over-excludes `\x0b`/`\x0c`/`\x85`; the rewrite is
// exact. `\uXXXX` in the replacement is interpreted by PCRE2 because
// ecmascript mode also sets PCRE2_ALT_BSUX. Every other byte — escaped
// chars (`\.`, `\[`), class contents (`[.]`), and the `.` under dotAll — is
// copied verbatim, so a pattern with no rewritable `.` comes out byte-
// identical. Remaining Option C sub-items (`\u{…}`/`\p{…}`, Unicode
// `\w`/`\s`/`\b`, Annex-B legacy octal escapes, `$`/`m` exact anchoring, the
// `v` flag) are deferred — see docs/status/REGEXP.md.
//
// The output bound is len*19+1 (19 = the replacement's length, the max any
// single input byte can expand to), so the buffer never overflows.
func (e *Emitter) ensureRegexESNormalize() {
	if e.usedRegexESNormalize {
		return
	}
	e.usedRegexESNormalize = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.emitGlobal(`@.kml_regex_dot_repl = private unnamed_addr constant [19 x i8] c"[^\5Cn\5Cr\5Cu2028\5Cu2029]"`)
	e.emitGlobal(`
define ptr @__kml_regex_es_normalize(ptr %pat, i1 %dotall) {
entry:
  %len = call i64 @strlen(ptr %pat)
  %b0 = mul i64 %len, 19
  %bufsz = add i64 %b0, 1
  %out = call ptr @malloc(i64 %bufsz)
  %si = alloca i64, align 8
  %di = alloca i64, align 8
  %incls = alloca i1, align 1
  store i64 0, ptr %si, align 8
  store i64 0, ptr %di, align 8
  store i1 0, ptr %incls, align 1
  br label %cond

cond:
  %s = load i64, ptr %si, align 8
  %atend = icmp uge i64 %s, %len
  br i1 %atend, label %done, label %body

body:
  %pp = getelementptr i8, ptr %pat, i64 %s
  %c = load i8, ptr %pp, align 1
  %isbs = icmp eq i8 %c, 92
  br i1 %isbs, label %esc, label %notesc

esc:
  %d0 = load i64, ptr %di, align 8
  %od0 = getelementptr i8, ptr %out, i64 %d0
  store i8 92, ptr %od0, align 1
  %d1 = add i64 %d0, 1
  %s1 = add i64 %s, 1
  %hasnext = icmp ult i64 %s1, %len
  br i1 %hasnext, label %escnext, label %escend

escnext:
  %npp = getelementptr i8, ptr %pat, i64 %s1
  %nc = load i8, ptr %npp, align 1
  %od1 = getelementptr i8, ptr %out, i64 %d1
  store i8 %nc, ptr %od1, align 1
  %d2 = add i64 %d1, 1
  store i64 %d2, ptr %di, align 8
  %s2 = add i64 %s, 2
  store i64 %s2, ptr %si, align 8
  br label %cond

escend:
  store i64 %d1, ptr %di, align 8
  store i64 %s1, ptr %si, align 8
  br label %cond

notesc:
  %cls = load i1, ptr %incls, align 1
  %isopen = icmp eq i8 %c, 91
  %isclose = icmp eq i8 %c, 93
  %isdot = icmp eq i8 %c, 46
  %notcls = xor i1 %cls, true
  %notdotall = xor i1 %dotall, true
  %dc0 = and i1 %isdot, %notcls
  %dotcase = and i1 %dc0, %notdotall
  br i1 %dotcase, label %emitrepl, label %copyone

emitrepl:
  %dr = load i64, ptr %di, align 8
  %odr = getelementptr i8, ptr %out, i64 %dr
  call ptr @memcpy(ptr %odr, ptr @.kml_regex_dot_repl, i64 19)
  %drn = add i64 %dr, 19
  store i64 %drn, ptr %di, align 8
  %sdn = add i64 %s, 1
  store i64 %sdn, ptr %si, align 8
  br label %cond

copyone:
  %opencase = and i1 %isopen, %notcls
  %closecase = and i1 %isclose, %cls
  %afteropen = or i1 %cls, %opencase
  %notclosecase = xor i1 %closecase, true
  %newcls = and i1 %afteropen, %notclosecase
  store i1 %newcls, ptr %incls, align 1
  %dco = load i64, ptr %di, align 8
  %odco = getelementptr i8, ptr %out, i64 %dco
  store i8 %c, ptr %odco, align 1
  %dcon = add i64 %dco, 1
  store i64 %dcon, ptr %di, align 8
  %scon = add i64 %s, 1
  store i64 %scon, ptr %si, align 8
  br label %cond

done:
  %dfin = load i64, ptr %di, align 8
  %ofin = getelementptr i8, ptr %out, i64 %dfin
  store i8 0, ptr %ofin, align 1
  ret ptr %out
}`)
}

// ensureRegexMatch declares PCRE2's match-time API — used by `.test(str)`
// (emit_regexp.go's emitRegexTest, Stage 1) and every later stage that
// needs a real match (.exec/.match/.matchAll/.replace/.replaceAll/.split/
// .search, TDD-00035 Stages 2-5). A match_data block is created and freed
// per call rather than cached on the RegExp instance — PCRE2 itself
// recommends reusing one across matches for performance, but this
// compiler's manual memory-management default only leaks safely for a
// bounded, one-time allocation (like Stage 0's compiled-pattern handle,
// kept for the instance's lifetime); a match_data allocated fresh on every
// call inside a loop would otherwise leak once per iteration instead, so
// it's freed immediately after each call reads its result. Calls
// requireLink itself too (idempotent) rather than only relying on
// ensureRegexCompile always having already run first, since nothing
// structurally guarantees that ordering beyond "you can't have a RegExp
// value to call a method on without having constructed one" — true today,
// but this keeps the two ensure*() helpers independently self-sufficient.
// Also declares pcre2_pattern_info_8 (used to discover a compiled
// pattern's capture-group count, needed to size .exec()'s result array —
// Stage 1's `.test()` doesn't need it, but every stage from Stage 2 on
// does) and pcre2_get_ovector_pointer_8 (the match offsets array a
// successful match's captured-group boundaries are read from).
func (e *Emitter) ensureRegexMatch() {
	if e.usedRegexMatch {
		return
	}
	e.usedRegexMatch = true
	e.requireLink("pcre2-8")
	e.emitGlobal("declare ptr @pcre2_match_data_create_from_pattern_8(ptr noundef, ptr noundef)")
	e.emitGlobal("declare i32 @pcre2_match_8(ptr noundef, ptr noundef, i64 noundef, i64 noundef, i32 noundef, ptr noundef, ptr noundef)")
	e.emitGlobal("declare void @pcre2_match_data_free_8(ptr noundef)")
	e.emitGlobal("declare i32 @pcre2_pattern_info_8(ptr noundef, i32 noundef, ptr noundef)")
	e.emitGlobal("declare ptr @pcre2_get_ovector_pointer_8(ptr noundef)")
}

// ensureRegexParseFlags declares __kml_regex_parse_flags: a single-pass
// scan over a flags string (e.g. "gim") producing PCRE2's combined compile
// option bits plus the four decomposed booleans (global/ignoreCase/
// multiline/dotAll) RegExpType stores as real fields, so no later method
// call ever needs to re-parse the flags string itself. Hand-written rather
// than built from repeated strchr calls specifically to avoid redeclaring
// a libc symbol (strchr) that may already be declared elsewhere in the
// same compiled program (runtime_os.go) — LLVM rejects two non-identical-
// looking `declare`s racing to define the same symbol across independently
// emitted globals, so every C library function this compiler calls has
// exactly one owning ensure*() helper; a hand-rolled, uniquely-named
// @__kml_-prefixed function sidesteps that entirely. Unrecognized flag
// letters are silently ignored (permissive, matching atob/decodeURI's
// existing "malformed input" convention) rather than rejected.
// ensureRegexValidateFlags declares __kml_regex_validate_flags: returns 0 for a
// valid flags string and 1 otherwise. Real JS throws a SyntaxError when a flag
// character is not one of the eight valid letters (d,g,i,m,s,u,v,y) or when any
// flag is repeated ("gg"). (u/v/y/d are recognized as VALID here even though
// their behavior isn't implemented — that unimplemented-behavior gap is a
// separate status row; this validation is only about the throw-vs-silent-accept
// distinction.) The caller throws the SyntaxError on a nonzero return.
func (e *Emitter) ensureRegexValidateFlags() {
	if e.usedRegexValidateFlags {
		return
	}
	e.usedRegexValidateFlags = true
	e.emitGlobal(`
define i32 @__kml_regex_validate_flags(ptr %flags) {
entry:
  br label %loop
loop:
  %idx = phi i64 [ 0, %entry ], [ %idxn, %cont ]
  %seen = phi i32 [ 0, %entry ], [ %seenn, %cont ]
  %p = getelementptr i8, ptr %flags, i64 %idx
  %ch = load i8, ptr %p, align 1
  %end = icmp eq i8 %ch, 0
  br i1 %end, label %ok, label %map
map:
  %b_d = icmp eq i8 %ch, 100
  %b_g = icmp eq i8 %ch, 103
  %b_i = icmp eq i8 %ch, 105
  %b_m = icmp eq i8 %ch, 109
  %b_s = icmp eq i8 %ch, 115
  %b_u = icmp eq i8 %ch, 117
  %b_v = icmp eq i8 %ch, 118
  %b_y = icmp eq i8 %ch, 121
  %m0 = select i1 %b_d, i32 1, i32 0
  %m1 = select i1 %b_g, i32 2, i32 %m0
  %m2 = select i1 %b_i, i32 4, i32 %m1
  %m3 = select i1 %b_m, i32 8, i32 %m2
  %m4 = select i1 %b_s, i32 16, i32 %m3
  %m5 = select i1 %b_u, i32 32, i32 %m4
  %m6 = select i1 %b_v, i32 64, i32 %m5
  %bit = select i1 %b_y, i32 128, i32 %m6
  %validchar = icmp ne i32 %bit, 0
  br i1 %validchar, label %dupcheck, label %fail
dupcheck:
  %and = and i32 %seen, %bit
  %dup = icmp ne i32 %and, 0
  br i1 %dup, label %fail, label %cont
cont:
  %seenn = or i32 %seen, %bit
  %idxn = add i64 %idx, 1
  br label %loop
fail:
  ret i32 1
ok:
  ret i32 0
}`)
}

func (e *Emitter) ensureRegexParseFlags() {
	if e.usedRegexParseFlags {
		return
	}
	e.usedRegexParseFlags = true
	e.emitGlobal(`
define void @__kml_regex_parse_flags(ptr %flags, ptr %optout, ptr %gout, ptr %iout, ptr %mout, ptr %sout) {
entry:
  store i32 0, ptr %optout, align 4
  store i1 0, ptr %gout, align 1
  store i1 0, ptr %iout, align 1
  store i1 0, ptr %mout, align 1
  store i1 0, ptr %sout, align 1
  br label %loopcheck

loopcheck:
  %idx = phi i64 [ 0, %entry ], [ %idx.next, %next ]
  %p = getelementptr i8, ptr %flags, i64 %idx
  %ch = load i8, ptr %p, align 1
  %atend = icmp eq i8 %ch, 0
  br i1 %atend, label %done, label %checkg

checkg:
  %isg = icmp eq i8 %ch, 103
  br i1 %isg, label %setg, label %checki
setg:
  store i1 1, ptr %gout, align 1
  br label %next

checki:
  %isi = icmp eq i8 %ch, 105
  br i1 %isi, label %seti, label %checkm
seti:
  store i1 1, ptr %iout, align 1
  %opt.i0 = load i32, ptr %optout, align 4
  %opt.i1 = or i32 %opt.i0, 8
  store i32 %opt.i1, ptr %optout, align 4
  br label %next

checkm:
  %ism = icmp eq i8 %ch, 109
  br i1 %ism, label %setm, label %checks
setm:
  store i1 1, ptr %mout, align 1
  %opt.m0 = load i32, ptr %optout, align 4
  %opt.m1 = or i32 %opt.m0, 1024
  store i32 %opt.m1, ptr %optout, align 4
  br label %next

checks:
  %iss = icmp eq i8 %ch, 115
  br i1 %iss, label %sets, label %next
sets:
  store i1 1, ptr %sout, align 1
  %opt.s0 = load i32, ptr %optout, align 4
  %opt.s1 = or i32 %opt.s0, 32
  store i32 %opt.s1, ptr %optout, align 4
  br label %next

next:
  %idx.next = add i64 %idx, 1
  br label %loopcheck

done:
  ret void
}`)
}
