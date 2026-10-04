package llvm

import _ "embed"

//go:embed stringsrc/strings.c
var stringsCSource string

// StringsCSource is the string trim/case/replace/split runtime's C source.
func StringsCSource() string { return stringsCSource }

// UsesStringsC reports whether the program links the strings runtime (strings.c).
func (e *Emitter) UsesStringsC() bool {
	return e.usedWsSpan || e.usedStringTrim || e.usedStringTrimStart || e.usedStringTrimEnd ||
		e.usedStringToLower || e.usedStringReplace || e.usedStringReplaceAll || e.usedStringSplit
}

// ensureWsSpan declares @__kml_ws_span (strings.c): the byte length of the JS
// WhiteSpace/LineTerminator character at p, or 0.
func (e *Emitter) ensureWsSpan() {
	if e.usedWsSpan {
		return
	}
	e.usedWsSpan = true
	e.ensureStrHeaderRuntime() // strings.c allocates through __kml_str_alloc
	e.emitGlobal("declare i64 @__kml_ws_span(ptr)")
}

// ensureStringTrim declares __kml_trim (strings.c).
func (e *Emitter) ensureStringTrim() {
	if e.usedStringTrim {
		return
	}
	e.usedStringTrim = true
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureWsSpan()
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_trim(ptr)")
}

// ensureStringTrimStart declares __kml_trim_start (strings.c).
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
	e.emitGlobal("declare ptr @__kml_trim_start(ptr)")
}

// ensureStringTrimEnd declares __kml_trim_end (strings.c).
func (e *Emitter) ensureStringTrimEnd() {
	if e.usedStringTrimEnd {
		return
	}
	e.usedStringTrimEnd = true
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureWsSpan()
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_trim_end(ptr)")
}

// ensureStringToLower declares __kml_tolower (strings.c).
func (e *Emitter) ensureStringToLower() {
	if e.usedStringToLower {
		return
	}
	e.usedStringToLower = true
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_tolower(ptr)")
}

// ensureStringReplace declares __kml_replace (strings.c). Lengths are explicit
// parameters: internal callers (HTTP header/query parsing, SSE) pass strlen()
// of a raw, headerless buffer, so the header is never read.
func (e *Emitter) ensureStringReplace() {
	if e.usedStringReplace {
		return
	}
	e.usedStringReplace = true
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_replace(ptr, i64, ptr, i64, ptr, i64)")
}

// ensureStringReplaceAll declares __kml_replace_all (strings.c).
func (e *Emitter) ensureStringReplaceAll() {
	if e.usedStringReplaceAll {
		return
	}
	e.usedStringReplaceAll = true
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureStrHeaderRuntime()
	e.emitGlobal("declare ptr @__kml_replace_all(ptr, i64, ptr, i64, ptr, i64)")
}

// ensureStringSplit declares __kml_split. The splitting is __kml_split_parts in
// strings.c; this wrapper returns {ptr, i64}, which the C ABI would pass
// differently on Windows than the generated callers expect.
func (e *Emitter) ensureStringSplit() {
	if e.usedStringSplit {
		return
	}
	e.usedStringSplit = true
	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureStrHeaderRuntime()
	e.emitGlobal(`declare ptr @__kml_split_parts(ptr, i64, ptr, i64, ptr)
define {ptr, i64} @__kml_split(ptr %s, i64 %slen_c, ptr %sep, i64 %sep_len) {
entry:
  %n = alloca i64, align 8
  %arr = call ptr @__kml_split_parts(ptr %s, i64 %slen_c, ptr %sep, i64 %sep_len, ptr %n)
  %len = load i64, ptr %n, align 8
  %r0 = insertvalue {ptr, i64} undef, ptr %arr, 0
  %r1 = insertvalue {ptr, i64} %r0, i64 %len, 1
  ret {ptr, i64} %r1
}`)
}
