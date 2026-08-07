package llvm

// PCRE2 8-bit API option constants and the "match to end of string"
// zero-terminated-length sentinel. These are C preprocessor macros in
// pcre2.h, not real linkable symbols, so their numeric values are
// hardcoded here — same convention emit_url.go's CURLUPart values
// (curluPartURL etc.) already establish for libcurl. Verified directly
// against the real pcre2.h (extracted from the Ubuntu/Debian
// libpcre2-dev 10.42-4ubuntu2.1 package, not trusted from memory) on the
// x86-64 Linux development machine — re-verify on the Apple Silicon Mac
// before relying on these further, per CLAUDE.md's standing rule on
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
