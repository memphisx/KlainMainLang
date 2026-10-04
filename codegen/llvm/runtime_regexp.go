package llvm

import _ "embed"

//go:embed stringsrc/regexp.c
var regexpCSource string

// RegexpCSource is the RegExp helper runtime's C source (flag parsing, offset
// converters, the ES source rewrite; TDD-00240).
func RegexpCSource() string { return regexpCSource }

// UsesRegexpC reports whether the program links the RegExp helper runtime.
func (e *Emitter) UsesRegexpC() bool {
	return e.usedRegexUTF8Width || e.usedRegexUTF16Convert || e.usedRegexESNormalize || e.usedRegexESClasses ||
		e.usedRegexFlagsCanon || e.usedRegexSourceNorm || e.usedRegexValidateFlags || e.usedRegexParseFlags
}

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
	pcre2InfoCaptureCount = 4 // pcre2_pattern_info()'s request-type enum, not an option bit
	// PCRE2_ANCHORED (0x80000000u): a pcre2_match option that only allows a
	// match starting exactly at the start offset — the `y`/sticky flag
	// (ADR-01062). Passed as an i32 immediate, so spelled as its signed value.
	pcre2Anchored = -2147483648
	pcre2Unset    = -1 // PCRE2_UNSET, ~(PCRE2_SIZE)0 — same bit pattern as pcre2ZeroTerminated but a distinct meaning (an ovector pair that didn't participate in the match, e.g. an optional capture group), kept as its own named constant for clarity at call sites

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
	e.ensureStrHeaderRuntime() // regexp.c allocates through the header runtime
	e.emitGlobal(`declare i64 @__kml_regex_utf8_width(ptr, i64)`)
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
	e.ensureStrHeaderRuntime() // regexp.c allocates through the header runtime
	e.ensureRegexUTF8Width()
	e.emitGlobal(`declare i64 @__kml_regex_byte_to_utf16(ptr, i64)
declare i64 @__kml_regex_utf16_to_byte(ptr, i64)`)
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
	e.ensureStrHeaderRuntime() // regexp.c allocates through the header runtime
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	e.emitGlobal(`declare ptr @__kml_regex_es_normalize(ptr, i1)`)
}

// ensureRegexESClasses declares __kml_regex_es_classes(pattern): the ES
// dialects' rewrite of `[]` and `[^]`, which PCRE reads otherwise.
func (e *Emitter) ensureRegexESClasses() {
	if e.fnDecls["__kml_regex_es_classes"] {
		return
	}
	e.fnDecls["__kml_regex_es_classes"] = true
	e.usedRegexESClasses = true
	e.ensureStrHeaderRuntime() // regexp.c allocates through the header runtime
	e.emitGlobal(`declare ptr @__kml_regex_es_classes(ptr)`)
}

// ensureRegexFlagsCanon declares __kml_regex_flags_canon(flags): returns a
// freshly headered string with the (already-validated, dup-free) flag bytes
// reordered into JS's canonical `d,g,i,m,s,u,v,y` order, so `.flags` matches
// V8 regardless of the order the constructor was given them in
// (`new RegExp("x", "ig").flags` is `"gi"`, not `"ig"`). Runs at RUNTIME
// because the flags string can be a dynamic value. The output is at most as
// long as the input, so the input-sized buffer never overflows.
func (e *Emitter) ensureRegexFlagsCanon() {
	if e.usedRegexFlagsCanon {
		return
	}
	e.usedRegexFlagsCanon = true
	e.ensureStrHeaderRuntime() // regexp.c allocates through the header runtime
	e.ensureStrlen()
	e.emitGlobal(`declare ptr @__kml_regex_flags_canon(ptr)`)
}

// ensureRegexSourceNorm declares __kml_regex_source_norm(src): returns the
// spec's `.source` non-empty placeholder `(?:)` for an empty pattern (JS's
// `new RegExp("").source` is `"(?:)"`, not `""`), and the source unchanged
// otherwise. Runs at RUNTIME because the pattern can be a dynamic value.
func (e *Emitter) ensureRegexSourceNorm() {
	if e.usedRegexSourceNorm {
		return
	}
	e.usedRegexSourceNorm = true
	e.ensureStrHeaderRuntime() // regexp.c allocates through the header runtime
	e.ensureStrlen()
	e.ensureMemcpy()
	e.emitGlobal(`declare ptr @__kml_regex_source_norm(ptr)`)
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
	e.ensureStrHeaderRuntime() // regexp.c allocates through the header runtime
	e.emitGlobal(`declare i32 @__kml_regex_validate_flags(ptr)`)
}

func (e *Emitter) ensureRegexParseFlags() {
	if e.usedRegexParseFlags {
		return
	}
	e.usedRegexParseFlags = true
	e.ensureStrHeaderRuntime() // regexp.c allocates through the header runtime
	e.emitGlobal(`declare void @__kml_regex_parse_flags(ptr, ptr, ptr, ptr, ptr, ptr, ptr)`)
}
