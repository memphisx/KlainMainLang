package llvm

import _ "embed"

//go:embed stringsrc/regexp.c
var regexpCSource string

// RegexpCSource is the RegExp helper runtime's C source (flag parsing, offset
// converters, the ES source rewrite; TDD-00240).
func RegexpCSource() string { return regexpCSource }

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
	// PCRE2_MATCH_INVALID_UTF (0x04000000, PCRE2 10.34+): a subject holding a
	// lone surrogate (WTF-8, not UTF-8) is matched around it rather than
	// failing every match.
	pcre2MatchInvalidUTF = 0x04000000
	pcre2NewlineAny      = 4 // PCRE2_NEWLINE_ANY — pcre2_set_newline() value, not an option bit; closest convention to ES's line terminators (Option B)
)
