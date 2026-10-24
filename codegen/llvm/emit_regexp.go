package llvm

import (
	"KlainMainLang/ast"
	"fmt"
	"strings"
)

// emit_regexp.go — RegExp construction (`new RegExp(pattern, flags?)` and
// the `/pattern/flags` literal, which desugars to the same
// ast.NewRegExpExpression node at parse time — see its doc comment). This
// is docs/tdd/TDD-00035.md's Stage 0: methods (.test/.exec/...) are later
// stages, not implemented here.

// regexModeOpts is the per-mode PCRE2 compile configuration derived from the
// resolved `-regex` mode (TDD-00067). baseOpt is OR-ed unconditionally into
// the compiled options word; dollarEndOnly adds PCRE2_DOLLAR_ENDONLY only when
// the `m` flag is absent (with `m`, ES and PCRE2 already agree `$` matches at
// line boundaries); newline (0 == leave PCRE2's build-time default untouched)
// is a pcre2_set_newline value applied via a compile context.
type regexModeOpts struct {
	baseOpt       int
	dollarEndOnly bool
	newline       int
	// utf16Index makes every user-visible offset boundary (str.search's return,
	// a replace callback's `offset` argument, and the RegExp's own lastIndex
	// field) report/consume true UTF-16 code-unit positions instead of the
	// PCRE2_8 byte offsets the rest of this compiler's string layer uses
	// (es-utf16, TDD-00067 Stage 3). Matching itself is identical to
	// es-unicode; only the offsets crossing back to user code are converted.
	utf16Index bool
	// utfMatching is true whenever PCRE2_UTF is on (es-unicode/es-utf16), so
	// the global empty-match advance steps by a whole UTF-8 code point rather
	// than a single byte (a mid-code-point start offset makes PCRE2_UTF reject
	// the next match). es-ascii/pcre match raw bytes, so they advance by 1.
	utfMatching bool
	// normalize runs the ECMAScript source-normalization pass
	// (__kml_regex_es_normalize) over the pattern before compiling it —
	// ecmascript mode only (TDD-00067 Option C). The original pattern is still
	// stored as `.source`; only the compiled form is normalized.
	normalize bool
}

// regexModeOpts maps the resolved compile-wide RegExp dialect to its PCRE2
// compile configuration. es-ascii is Option A (compile-option alignment only,
// no context); es-unicode is Option B (adds code-point matching via PCRE2_UTF
// and the NEWLINE_ANY convention); pcre is today's raw behavior. The mode is a
// whole-program compile-time constant, so this is resolved once per RegExp
// construction with no runtime branching on the mode itself.
//
// PCRE2_UCP is deliberately NOT set here (TDD-00067): it makes \w/\s/\b use
// PCRE2's Unicode-property tables, which diverge from ECMAScript's — e.g. UCP
// \s matches U+180E, which ES dropped as whitespace in Unicode 6.3, failing
// Test262's built-ins/RegExp/u180e.js and netting a conformance loss. The ES
// Unicode class semantics belong to Option C's normalization pass; es-unicode
// keeps \w/\s/\b non-Unicode for now (documented caveat in REGEXP.md).
func (e *Emitter) regexModeOpts() regexModeOpts {
	switch e.resolveRegexMode() {
	case "pcre":
		return regexModeOpts{}
	case "es-ascii":
		return regexModeOpts{baseOpt: pcre2AltBSUX | pcre2MatchUnsetBackref, dollarEndOnly: true}
	case "es-unicode":
		return regexModeOpts{baseOpt: pcre2AltBSUX | pcre2MatchUnsetBackref | pcre2UTF, dollarEndOnly: true, newline: pcre2NewlineAny, utfMatching: true}
	case "es-utf16":
		// Identical matching to es-unicode (same option bits, same newline
		// convention), plus UTF-16 code-unit index reporting at every user-
		// visible offset boundary — see regexModeOpts.utf16Index (TDD-00067
		// Stage 3).
		return regexModeOpts{baseOpt: pcre2AltBSUX | pcre2MatchUnsetBackref | pcre2UTF, dollarEndOnly: true, newline: pcre2NewlineAny, utfMatching: true, utf16Index: true}
	case "ecmascript":
		// es-unicode matching plus the Option C source normalization pass,
		// reporting UTF-16 code-unit offsets as the string layer indexes
		// (.charCodeAt/.slice, ADR-01826).
		return regexModeOpts{baseOpt: pcre2AltBSUX | pcre2MatchUnsetBackref | pcre2UTF, dollarEndOnly: true, newline: pcre2NewlineAny, utfMatching: true, normalize: true, utf16Index: true}
	default:
		// resolveRegexMode only ever yields one of the above; a mode added
		// without a matching case should fail loudly here rather than silently
		// fall back to raw PCRE (which would be a wrong, hard-to-spot dialect).
		panic("unhandled regex mode: " + e.resolveRegexMode())
	}
}

// regexByteToUTF16 converts a non-negative PCRE2 byte offset into a UTF-16
// code-unit offset when the resolved mode reports UTF-16 indices (es-utf16),
// and is an identity passthrough in every other mode — so a single call site
// stays correct across all dialects without a mode branch of its own. The
// byte offset must be >= 0 (a match end offset, a match start offset, etc.);
// use regexByteToUTF16Signed where the -1 "no match" sentinel can appear.
func (e *Emitter) regexByteToUTF16(strRef, byteReg string) string {
	if !e.regexModeOpts().utf16Index {
		return byteReg
	}
	e.ensureRegexUTF16Convert()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_regex_byte_to_utf16(ptr %s, i64 %s)", r, strRef, byteReg))
	return r
}

// regexByteToUTF16Signed is regexByteToUTF16 that first passes the -1 "no
// match" sentinel through unchanged (str.search returns -1 for no match, and
// -1 is a code-unit-space-agnostic sentinel), converting only a real >= 0
// offset. Identity passthrough in non-es-utf16 modes.
func (e *Emitter) regexByteToUTF16Signed(strRef, byteReg string) string {
	if !e.regexModeOpts().utf16Index {
		return byteReg
	}
	e.ensureRegexUTF16Convert()
	isNeg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, 0", isNeg, byteReg))
	negL := e.freshLabel("regex.u16.neg")
	convL := e.freshLabel("regex.u16.conv")
	mergeL := e.freshLabel("regex.u16.merge")
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", slot))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNeg, negL, convL))
	e.emitLabel(negL)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", byteReg, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(convL)
	conv := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_regex_byte_to_utf16(ptr %s, i64 %s)", conv, strRef, byteReg))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", conv, slot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))
	e.emitLabel(mergeL)
	out := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", out, slot))
	return out
}

// regexUTF16ToByte converts a UTF-16 code-unit offset (the es-utf16 mode's
// stored lastIndex space) back into the PCRE2 byte offset a match needs as
// its start. Identity passthrough in every non-es-utf16 mode.
func (e *Emitter) regexUTF16ToByte(strRef, u16Reg string) string {
	if !e.regexModeOpts().utf16Index {
		return u16Reg
	}
	e.ensureRegexUTF16Convert()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_regex_utf16_to_byte(ptr %s, i64 %s)", r, strRef, u16Reg))
	return r
}

// emitRegexAdvancePastEmpty is the global-iteration empty-match advance
// real JS's String.prototype.match/matchAll/replace algorithms perform
// (AdvanceStringIndex): after a *zero-length* match, the RegExp's lastIndex
// must be bumped past the current position, or the next iteration re-finds
// the same empty match at the same offset and the driver loop never
// terminates. (A `.exec()` used by hand in a `while` loop has no such
// advance — that footgun is real-JS-faithful — so this lives only in the
// multi-match driver loops, not in emitRegexSingleMatchCore.) The step is a
// whole UTF-8 code point in the UTF-matching modes (es-unicode/es-utf16),
// keeping the next start offset off a mid-code-point byte PCRE2_UTF would
// reject, and a single byte in the raw-byte modes (es-ascii/pcre). The
// stored value is written back in the mode's own index space (UTF-16 units
// for es-utf16, bytes otherwise). startByteReg/endByteReg are the match's
// raw byte offsets from emitRegexSingleMatchCore.
func (e *Emitter) emitRegexAdvancePastEmpty(objVal, strVal Value, startByteReg, endByteReg string) {
	opts := e.regexModeOpts()
	isEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", isEmpty, startByteReg, endByteReg))
	advL := e.freshLabel("regex.empty.adv")
	skipL := e.freshLabel("regex.empty.skip")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEmpty, advL, skipL))

	e.emitLabel(advL)
	width := "1"
	if opts.utfMatching {
		e.ensureRegexUTF8Width()
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_regex_utf8_width(ptr %s, i64 %s)", w, strVal.Ref, endByteReg))
		width = w
	}
	newByte := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newByte, endByteReg, width))
	stored := e.regexByteToUTF16(strVal.Ref, newByte)
	e.emitRegexStoreLastIndex(objVal, stored)
	e.emitTerminator(fmt.Sprintf("br label %%%s", skipL))

	e.emitLabel(skipL)
}

// emitNewRegExpExpression implements `new RegExp(pattern, flags?)`.
// Compiles the pattern once via PCRE2 into an opaque pcre2_code* handle
// stored in the RegExp object's hidden RegexHandleField, decomposes the
// flags string into real global/ignoreCase/multiline/dotAll fields (see
// ensureRegexParseFlags), and initializes lastIndex to 0. Throws a
// catchable KML SyntaxError on an invalid pattern — the same "surface a
// bad combination as a catchable Error" convention Invalid-URL/array-out-
// of-bounds already use, matching real JS's own `new RegExp("(")` throwing
// a real SyntaxError.
func (e *Emitter) emitNewRegExpExpression(ex *ast.NewRegExpExpression) (Value, error) {
	return e.emitNewRegExpInto(ex, "")
}

// emitNewRegExpInto builds a RegExp's body at body, or, when body is "", in
// a fresh allocation whose header word precedes the body (TDD-00247): a
// RegExp boxes as that header, and a class extending RegExp builds its
// body in place after its own tag word.
func (e *Emitter) emitNewRegExpInto(ex *ast.NewRegExpExpression, body string) (Value, error) {
	e.ensureRegexCompile()
	e.ensureMalloc()
	e.ensureExceptionHelpers()

	patternVal, err := e.emitExpr(ex.Pattern)
	if err != nil {
		return Value{}, err
	}
	// `new RegExp(undefined)` is the empty pattern `/(?:)/`; a non-string,
	// non-undefined pattern (e.g. a number) is ToString'd — faithful to JS, and
	// avoids leaving a non-`ptr` word where the compiler expects the pattern
	// string (the RegExp A1 invalid-IR files).
	var srcFlags string // a RegExp pattern's own flags, the default
	if info, ok := e.classes[patternVal.Ty.ClassName]; ok && patternVal.Ty.IsClass() && info.HostBase != nil && info.HostBase.IsRegExp() {
		// An instance of a class extending RegExp: the RegExp at its body.
		body := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", body, patternVal.Ref))
		patternVal = Value{Ref: body, Ty: *info.HostBase}
	}
	if patternVal.Ty.IsRegExp() && !patternVal.Ty.IsDynamic() {
		// `new RegExp(re[, flags])`: re's source, and its flags unless given.
		srcFlags = e.emitRegexLoadField(patternVal, "flags", "ptr", 8)
		patternVal = Value{Ref: e.emitRegexLoadField(patternVal, "source", "ptr", 8), Ty: TypePtr}
	} else if patternVal.Ty.IsUndefined() || patternVal.Ty.IR == "void" {
		patternVal = Value{Ref: e.internString(""), Ty: TypePtr}
	} else if isUnconstrainedDynamic(patternVal.Ty) {
		// A boxed RegExp gives its source and flags; a boxed undefined is
		// the empty pattern; anything else is ToString'd.
		patSlot, flagSlot := e.freshReg(), e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", patSlot))
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", flagSlot))
		isRe := e.emitDynHostInstanceOf(patternVal, "RegExp")
		reL, otherL, doneL := e.freshLabel("regex.src.re"), e.freshLabel("regex.src.other"), e.freshLabel("regex.src.done")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isRe.Ref, reL, otherL))
		e.emitLabel(reL)
		re := e.emitUnboxHost(patternVal, RegExpType())
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.emitRegexLoadField(re, "source", "ptr", 8), patSlot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.emitRegexLoadField(re, "flags", "ptr", 8), flagSlot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(otherL)
		tag, _ := e.emitUnboxTagPayload(patternVal)
		str, err := e.emitValueToString(patternVal)
		if err != nil {
			return Value{}, err
		}
		isU, sel := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, %d", isU, tag, kmlTagUndefined))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", sel, isU, e.internString(""), e.convert(str, TypePtr).Ref))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", sel, patSlot))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(""), flagSlot))
		e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))
		e.emitLabel(doneL)
		pat, fl := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", pat, patSlot))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fl, flagSlot))
		patternVal, srcFlags = Value{Ref: pat, Ty: TypePtr}, fl
	} else if !isStringTy(patternVal.Ty) {
		patternVal, err = e.emitValueToString(patternVal)
		if err != nil {
			return Value{}, err
		}
	} else {
		patternVal = e.convert(patternVal, TypePtr)
	}

	var flagsVal Value
	if ex.Flags != nil {
		flagsVal, err = e.emitExpr(ex.Flags)
		if err != nil {
			return Value{}, err
		}
		// `new RegExp(p, undefined)` / `null` means no flags (real JS: the empty
		// string). Handled at compile time by type so an `undefined`-typed value
		// (an i64 sentinel) never reaches the ptr flag validator as invalid IR;
		// a non-string flags value is otherwise ToString'd.
		switch {
		case (flagsVal.Ty.IsUndefined() || flagsVal.Ty.IsNull()) && !isStringTy(flagsVal.Ty) || flagsVal.Ty.IR == "void":
			flagsVal = Value{Ref: e.internString(""), Ty: TypePtr}
		case !isStringTy(flagsVal.Ty):
			flagsVal, err = e.emitValueToString(flagsVal)
			if err != nil {
				return Value{}, err
			}
		default:
			flagsVal = e.convert(flagsVal, TypePtr)
			// A null pointer reaching the flag validator is undefined behaviour
			// (a crash on Linux, a self-loop on Windows) — map it to "".
			flagsNull := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", flagsNull, flagsVal.Ref))
			flagsSafe := e.freshReg()
			e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr %s, ptr %s", flagsSafe, flagsNull, e.internString(""), flagsVal.Ref))
			flagsVal = Value{Ref: flagsSafe, Ty: TypePtr}
		}
	} else if srcFlags != "" {
		flagsVal = Value{Ref: srcFlags, Ty: TypePtr}
	} else {
		flagsVal = Value{Ref: e.internString(""), Ty: TypePtr}
	}

	// Validate the flags string as real JS does: an unrecognized flag character
	// or a repeated flag is a SyntaxError, not a silent accept (ADR-00549).
	e.ensureRegexValidateFlags()
	flagsOkReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @__kml_regex_validate_flags(ptr %s)", flagsOkReg, flagsVal.Ref))
	flagsBad := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne i32 %s, 0", flagsBad, flagsOkReg))
	flagsBadL := e.freshLabel("regex.badflags")
	flagsOkL := e.freshLabel("regex.okflags")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", flagsBad, flagsBadL, flagsOkL))
	e.emitLabel(flagsBadL)
	e.ensureStrHeaderRuntime()
	flagsMsg := e.internString("Invalid flags supplied to RegExp constructor")
	flagsErr := e.buildErrorObj(errorKindIDs["SyntaxError"], flagsMsg, e.internString("SyntaxError"))
	e.excThrow(flagsErr)
	e.emitTerminator("unreachable")
	e.emitLabel(flagsOkL)

	optSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", optSlot))
	globalSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", globalSlot))
	ignoreCaseSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", ignoreCaseSlot))
	multilineSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", multilineSlot))
	dotAllSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", dotAllSlot))
	stickySlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i1, align 1", stickySlot))
	e.emitInstr(fmt.Sprintf("call void @__kml_regex_parse_flags(ptr %s, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s)",
		flagsVal.Ref, optSlot, globalSlot, ignoreCaseSlot, multilineSlot, dotAllSlot, stickySlot))
	optReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", optReg, optSlot))

	// Apply the compile-wide RegExp dialect mode (TDD-00067): OR in the mode's
	// base option bits (a compile-time immediate) and, when the mode calls for
	// it, PCRE2_DOLLAR_ENDONLY gated on the runtime-parsed `m` flag.
	modeOpts := e.regexModeOpts()
	if modeOpts.baseOpt != 0 {
		withBase := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i32 %s, %d", withBase, optReg, modeOpts.baseOpt))
		optReg = withBase
	}
	if modeOpts.dollarEndOnly {
		mForOpt := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", mForOpt, multilineSlot))
		dollarBit := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i32 0, i32 %d", dollarBit, mForOpt, pcre2DollarEndOnly))
		withDollar := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = or i32 %s, %s", withDollar, optReg, dollarBit))
		optReg = withDollar
	}

	// es-unicode sets the newline convention, which lives on a compile context
	// rather than in the options word — build/configure one, pass it in place
	// of the `ptr null` the other modes use, and free it right after compiling
	// (the compiled pattern copies out what it needs; see ensureRegexCompileContext).
	compileCtx := "null"
	var ctxReg string
	if modeOpts.newline != 0 {
		e.ensureRegexCompileContext()
		ctxReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @pcre2_compile_context_create_8(ptr null)", ctxReg))
		e.emitInstr(fmt.Sprintf("call i32 @pcre2_set_newline_8(ptr %s, i32 %d)", ctxReg, modeOpts.newline))
		compileCtx = ctxReg
	}

	// ecmascript mode (Option C): normalize the pattern source to the ES
	// dialect before compiling, using the runtime-parsed dotAll flag. The
	// original pattern is kept for `.source`; only the compiled form changes.
	compilePatternRef := patternVal.Ref
	esMode := e.resolveRegexMode() != "pcre"
	if esMode {
		e.ensureRegexESClasses()
		vErrSlot := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", vErrSlot))
		norm := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_regex_es_classes(ptr %s, ptr %s, ptr %s)", norm, compilePatternRef, flagsVal.Ref, vErrSlot))
		// A `v`-mode class set syntax error: Node's SyntaxError.
		vBad := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", vBad, norm))
		vBadL, vOkL := e.freshLabel("regex.vbad"), e.freshLabel("regex.vok")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", vBad, vBadL, vOkL))
		e.emitLabel(vBadL)
		vMsg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", vMsg, vErrSlot))
		e.excThrow(e.buildErrorObj(errorKindIDs["SyntaxError"], vMsg, e.internString("SyntaxError")))
		e.emitTerminator("unreachable")
		e.emitLabel(vOkL)
		compilePatternRef = norm
	}
	if modeOpts.normalize {
		e.ensureRegexESNormalize()
		dotAllForNorm := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", dotAllForNorm, dotAllSlot))
		normPat := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_regex_es_normalize(ptr %s, i1 %s)", normPat, compilePatternRef, dotAllForNorm))
		compilePatternRef = normPat
	}

	errCodeSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", errCodeSlot))
	errOffSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", errOffSlot))

	handleReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @pcre2_compile_8(ptr %s, i64 %d, i32 %s, ptr %s, ptr %s, ptr %s)",
		handleReg, compilePatternRef, pcre2ZeroTerminated, optReg, errCodeSlot, errOffSlot, compileCtx))
	if modeOpts.newline != 0 {
		e.emitInstr(fmt.Sprintf("call void @pcre2_compile_context_free_8(ptr %s)", ctxReg))
	}
	if esMode {
		e.emitInstr(fmt.Sprintf("call void @__kml_regex_note_optional(ptr %s, ptr %s)", handleReg, compilePatternRef))
	}

	badReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", badReg, handleReg))
	badL := e.freshLabel("regex.bad")
	okL := e.freshLabel("regex.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", badReg, badL, okL))

	e.emitLabel(badL)
	errCodeReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", errCodeReg, errCodeSlot))
	msgBuf := e.freshReg()
	e.ensureStrHeaderRuntime() // error .message must be headered for concat/=== (TDD-00120)
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_str_alloc(i64 256)", msgBuf))
	e.emitInstr(fmt.Sprintf("call i32 @pcre2_get_error_message_8(i32 %s, ptr %s, i64 256)", errCodeReg, msgBuf))
	e.emitInstr(fmt.Sprintf("call void @__kml_str_finalize(ptr %s)", msgBuf))
	if esMode {
		// V8's wording, with the pattern and flags.
		esMsg := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_regex_syntax_message(i32 %s, ptr %s, ptr %s, ptr %s)", esMsg, errCodeReg, msgBuf, patternVal.Ref, flagsVal.Ref))
		msgBuf = esMsg
	}
	errReg := e.buildErrorObj(errorKindIDs["SyntaxError"], msgBuf, e.internString("SyntaxError"))
	e.excThrow(errReg)
	e.emitTerminator("unreachable")

	e.emitLabel(okL)
	globalReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", globalReg, globalSlot))
	ignoreCaseReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", ignoreCaseReg, ignoreCaseSlot))
	multilineReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", multilineReg, multilineSlot))
	dotAllReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", dotAllReg, dotAllSlot))
	stickyReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", stickyReg, stickySlot))

	ty := RegExpType()
	dataReg := body
	if dataReg == "" {
		base := e.freshReg()
		e.heapMallocInto(base, fmt.Sprint(ty.StructSize()+8))
		e.heapStore("i64", fmt.Sprint(e.hostHeaderWord(ty)), base, 8)
		dataReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 8", dataReg, base))
	}
	structIR := ty.StructIR()

	storeField := func(name, ir, val string, align int) {
		idx, _, _ := ty.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, dataReg, idx))
		e.heapStore(ir, val, gep, align)
	}
	// `.source`/`.flags` follow JS's observable shapes: an empty pattern reads
	// back as the `(?:)` placeholder, and the flag bytes are re-sorted into the
	// canonical `d,g,i,m,s,u,v,y` order regardless of construction order.
	e.ensureRegexSourceNorm()
	srcNorm := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_regex_source_norm(ptr %s)", srcNorm, patternVal.Ref))
	e.ensureRegexFlagsCanon()
	flagsCanon := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_regex_flags_canon(ptr %s)", flagsCanon, flagsVal.Ref))
	storeField(RegexHandleField, "ptr", handleReg, 8)
	storeField("source", "ptr", srcNorm, 8)
	storeField("flags", "ptr", flagsCanon, 8)
	storeField("global", "i1", globalReg, 1)
	storeField("ignoreCase", "i1", ignoreCaseReg, 1)
	storeField("multiline", "i1", multilineReg, 1)
	storeField("dotAll", "i1", dotAllReg, 1)
	storeField("sticky", "i1", stickyReg, 1)
	storeField("lastIndex", "i64", "0", 8)

	return Value{Ref: dataReg, Ty: ty}, nil
}

// emitRegexHandleLoad loads the hidden compiled pcre2_code* handle out of an
// already-evaluated RegExp instance — shared by every RegExp method
// (.test/.exec/... — this and every later stage), not just this one.
func (e *Emitter) emitRegexHandleLoad(objVal Value) string {
	return e.emitRegexLoadField(objVal, RegexHandleField, "ptr", 8)
}

// emitRegexLoadField GEPs/loads a single named field out of an already-
// evaluated RegExp instance — shared by every RegExp method that needs to
// read more than just the hidden compiled-pattern handle.
func (e *Emitter) emitRegexLoadField(objVal Value, name, ir string, align int) string {
	idx, _, _ := objVal.Ty.FieldIndex(name)
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, objVal.Ty.StructIR(), objVal.Ref, idx))
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align %d", r, ir, gep, align))
	return r
}

// emitRegexTest implements `regexp.test(str): boolean` (TDD-00035). Shares
// `.exec()`'s `lastIndex`-driven stateful iteration for a global regex (real
// JS's `.test()` and `.exec()` run the same RegExpBuiltinExec algorithm): a
// global RegExp reads `lastIndex` as the start offset, advances it to the
// match end on success, and resets it to 0 on failure — so a `while
// (re.test(s))` loop over a global pattern now terminates and steps through
// every match, exactly as `.exec()` does. A non-global RegExp never reads or
// touches `lastIndex`, always matching from offset 0. (This compiler has no
// `y`/sticky flag — see TDD-00035's flag scope table — so only `global`
// drives the iteration.) A single match_data block is created and freed
// within this call. Any negative pcre2_match_8 return is treated as "no
// match" — permissive, matching this project's existing convention for
// unusual/malformed input elsewhere (atob, decodeURI).
// regexSubjectToString normalizes a `.test()`/`.exec()` subject argument to a
// plain string pointer. A subject that is already a string is coerced as before;
// any other type is run through ToString — faithful to JS, where
// `re.test(123)` matches against `"123"` and `re.test(undefined)` against
// `"undefined"`. Previously a non-string subject (e.g. an uninitialized
// `var s;`) left a non-`ptr` word where pcre2_match_8 wants a `ptr`, emitting
// invalid IR (the RegExp A1 invalid-IR files).
func (e *Emitter) regexSubjectToString(v Value) (Value, error) {
	// The general builtin-argument ToString (emit_exprs_types.go) — a subject
	// that is already a string coerces unchanged, undefined/void becomes
	// "undefined", anything else is ToString'd (`re.test(123)` matches "123").
	return e.emitArgToString(v)
}

// emitRegexMatchStart is the lastIndex half of RegExpBuiltinExec shared by
// `.test()` and emitRegexSingleMatchCore (ADR-01062): `lastIndex` is read as
// the start offset when `global` OR `sticky` is set (else 0), converted from
// the mode's index space to PCRE2's byte offset, and a sticky regex matches
// with PCRE2_ANCHORED so it can only succeed exactly at that offset. Returns
// the `useLastIndex` flag the caller uses to advance/reset lastIndex after
// the match, the original lastIndex, the byte start offset, and the
// pcre2_match option word.
func (e *Emitter) emitRegexMatchStart(objVal, strVal Value) (useLastIndexReg, lastIndexReg, startOffsetReg, matchOptsReg string) {
	globalReg := e.emitRegexLoadField(objVal, "global", "i1", 1)
	stickyReg := e.emitRegexLoadField(objVal, "sticky", "i1", 1)
	lastIndexReg = e.emitRegexLoadField(objVal, "lastIndex", "i64", 8)
	useLastIndexReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", useLastIndexReg, globalReg, stickyReg))
	// lastIndex is stored in the mode's own index space (UTF-16 code units for
	// es-utf16, bytes otherwise); PCRE2 always wants a byte start offset, so
	// convert on the way in (identity in non-es-utf16 modes).
	lastIndexByteReg := e.regexUTF16ToByte(strVal.Ref, lastIndexReg)
	startOffsetReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", startOffsetReg, useLastIndexReg, lastIndexByteReg))
	matchOptsReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i32 %d, i32 0", matchOptsReg, stickyReg, pcre2Anchored))
	return
}

func (e *Emitter) emitRegexTest(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: test takes exactly 1 argument", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	strVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	strVal, err = e.regexSubjectToString(strVal)
	if err != nil {
		return Value{}, err
	}

	e.ensureRegexMatch()
	handleReg := e.emitRegexHandleLoad(objVal)
	useLastIdxReg, lastIndexReg, startOffsetReg, matchOptsReg := e.emitRegexMatchStart(objVal, strVal)

	matchDataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @pcre2_match_data_create_from_pattern_8(ptr %s, ptr null)", matchDataReg, handleReg))

	rcReg := e.freshReg()
	mctx := e.emitRegexMatchContext()
	e.emitInstr(fmt.Sprintf("%s = call i32 @pcre2_match_8(ptr %s, ptr %s, i64 %d, i64 %s, i32 %s, ptr %s, ptr %s)",
		rcReg, handleReg, strVal.Ref, pcre2ZeroTerminated, startOffsetReg, matchOptsReg, matchDataReg, mctx))
	e.emitRegexFixOptional(handleReg, matchDataReg, rcReg)
	matchedReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i32 %s, 0", matchedReg, rcReg))

	// Advance/reset lastIndex (global only), mirroring emitRegexSingleMatchCore.
	// On a match, read the match end (ovector[1]) and store it back in the
	// mode's index space; on no match, reset to 0. A non-global regex keeps its
	// original lastIndex verbatim in both branches.
	matchedL := e.freshLabel("regex.test.matched")
	nomatchL := e.freshLabel("regex.test.nomatch")
	mergeL := e.freshLabel("regex.test.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", matchedReg, matchedL, nomatchL))

	e.emitLabel(matchedL)
	ovecReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @pcre2_get_ovector_pointer_8(ptr %s)", ovecReg, matchDataReg))
	endOfMatchGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 1", endOfMatchGep, ovecReg))
	endOfMatchReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", endOfMatchReg, endOfMatchGep))
	endStoredReg := e.regexByteToUTF16(strVal.Ref, endOfMatchReg)
	advancedLastIndexReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", advancedLastIndexReg, useLastIdxReg, endStoredReg, lastIndexReg))
	e.emitRegexStoreLastIndex(objVal, advancedLastIndexReg)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(nomatchL)
	resetLastIndexReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", resetLastIndexReg, useLastIdxReg, lastIndexReg))
	e.emitRegexStoreLastIndex(objVal, resetLastIndexReg)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	e.emitInstr(fmt.Sprintf("call void @pcre2_match_data_free_8(ptr %s)", matchDataReg))
	return Value{Ref: matchedReg, Ty: TypeBool}, nil
}

// regExpExecResultType returns `.exec(str)`'s return type: `string[] | null`
// — a real, reusable `Nullable` array (Type.Nullable, already-existing
// machinery for T | null), not a new representation. "null" here means a
// the null array header — see emitRegexExec.
func regExpExecResultType() Type {
	ty := ArrayOf(TypePtr)
	ty.setNullable(true)
	return ty
}

// execArrayType is exec()'s result: the match array whose header carries
// index, input and groups.
func execArrayType() Type {
	ty := regExpExecResultType()
	ty.ExecArray = true
	return ty
}

// matchArrayType is match()'s result (RegExpMatchArray): exec's header,
// or a global match's plain array of full matches (its index -1).
func matchArrayType() Type {
	ty := execArrayType()
	ty.ExecMaybePlain = true
	return ty
}

// execArrayMemberType is the type of an exec() result's own index, input or
// groups (a null-prototype dictionary of the named groups, or undefined).
func execArrayMemberType(prop string) (Type, int, bool) {
	m, ok := execMembers[prop]
	if !ok {
		return Type{}, 0, false
	}
	return m.ty(), m.off, true
}

// execMember is one of an exec() result's own members: its type, its
// header offset, and how a global match's plain array lacks it — index -1
// (absentIndex) or a null input (absentNull).
type execMember struct {
	ty                      func() Type
	off                     int
	absentIndex, absentNull bool
}

var execMembers = map[string]execMember{
	"index": {ty: func() Type { return TypeI64 }, off: 24, absentIndex: true},
	"input": {ty: func() Type { return TypePtr }, off: 32, absentNull: true},
	"groups": {ty: func() Type {
		key, val := TypePtr, undefinedableElem(TypePtr)
		return Type{IR: "ptr", Host: hostDynamicObject, MapKey: &key, MapVal: &val, Absent: absenceOf(true, true, false)}
	}, off: 40},
}

// execMemberRead reads prop off an exec-shaped header as its value: for a
// match result that may be a global match's plain array, index -1 and a
// null input are undefined.
func (e *Emitter) execMemberRead(header string, prop string, maybePlain bool) Value {
	ty, off, _ := execArrayMemberType(prop)
	gep, r := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", gep, header, off))
	e.emitInstr(fmt.Sprintf("%s = load %s, ptr %s, align 8", r, ty.IR, gep))
	v := Value{Ref: r, Ty: ty}
	if !maybePlain {
		return v
	}
	m := execMembers[prop]
	if m.absentIndex {
		present := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, 0", present, r))
		return e.wrapUndefinedable(v, present)
	}
	if m.absentNull {
		v.Ty.setNullable(true)
		v.Ty.setUndefined(true)
	}
	return v
}

// emitExecArrayMember reads an exec() result's index, input or groups off
// its header.
func (e *Emitter) emitExecArrayMember(ex *ast.MemberExpression) (Value, bool, error) {
	_, _, ok := execArrayMemberType(ex.Property)
	ot := e.inferExprType(ex.Object)
	if !ok || !ot.ExecArray {
		return Value{}, false, nil
	}
	v, err := e.emitExpr(ex.Object)
	if err != nil {
		return Value{}, false, err
	}
	return e.execMemberRead(v.Ref, ex.Property, ot.ExecMaybePlain || v.Ty.ExecMaybePlain), true, nil
}

// emitRegexStoreLastIndex GEPs/stores a new value into an already-evaluated
// RegExp instance's lastIndex field — shared by emitRegexExec's two
// branches (advance on a successful global/sticky match, reset to 0 on a
// failed one).
func (e *Emitter) emitRegexStoreLastIndex(objVal Value, newVal string) {
	idx, _, _ := objVal.Ty.FieldIndex("lastIndex")
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, objVal.Ty.StructIR(), objVal.Ref, idx))
	e.heapStore("i64", newVal, gep, 8)
}

// emitRegexExec implements `regexp.exec(str): string[] | null` (TDD-00035
// Stage 2) — a thin wrapper over emitRegexSingleMatchCore, the real
// prerequisite Stage 3's `str.match()`/`str.matchAll()` also call directly
// (see the TDD's Design section).
func (e *Emitter) emitRegexExec(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: exec takes exactly 1 argument", pos.Line, pos.Col)
	}
	objVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	strVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	strVal, err = e.regexSubjectToString(strVal)
	if err != nil {
		return Value{}, err
	}
	return e.emitExecResult(objVal, strVal), nil
}

// emitRegexSingleMatchCore runs exactly one PCRE2 match attempt of objVal
// (an already-evaluated RegExp instance) against strVal, implementing real
// JS's actual `RegExpBuiltinExec` algorithm — shared by `.exec()` itself
// and Stage 3/4's `str.match()`/`str.matchAll()`/`str.replace()`/
// `str.replaceAll()` internal iteration loops, which call this directly
// rather than going through emitRegexExec's own argument-evaluation
// wrapper. Besides the exec()-shaped result array, also returns the raw
// match start/end byte offsets (`-1`/`-1` when there was no match) —
// `.exec()` itself has no use for these (its own result array carries no
// index, see below), but Stage 4's replace machinery needs them to know
// exactly where in the subject to splice the replacement text.
//
// Result scope (a documented V1 narrowing, not full JS parity): on a
// match, returns index 0 = the full match text, indices 1..N = numbered
// capture groups as plain strings — an unmatched *optional* group (e.g.
// `(a)?` that didn't participate) becomes `""` rather than a true per-
// element `null`, since this compiler's arrays have no per-element-
// nullable-string representation. The exec-result's `.index`/`.input`/
// `.groups` are attached to its header afterwards (execResultFrom).
//
// lastIndex handling matches real JS's actual RegExpBuiltinExec algorithm
// precisely: read before matching (as the start offset) only when
// `global` is set (this compiler has no `y`/sticky flag yet — see
// docs/tdd/TDD-00035.md's flag scope table — so that half of the real
// condition doesn't apply), written after a successful match to the
// match's end offset, and reset to 0 after a failed one — but only when
// `global`; a non-global RegExp's `lastIndex` is never read or touched,
// always searching from offset 0. Deliberately does NOT special-case a
// zero-length match by advancing an extra character the way `matchAll`/
// `replaceAll`/`split`'s own internal loops will need to (Stage 3-5) —
// real JS's exec() itself has no such special case either (a hand-written
// `while (re.exec(s))` loop over a pattern that can match empty is a
// well-known real-JS footgun too, not something this compiler owes exec()
// specifically).
func (e *Emitter) emitRegexSingleMatchCore(objVal, strVal Value) (result Value, matchStartReg, matchEndReg string) {
	result, matchStartReg, matchEndReg, _ = e.emitRegexMatch(objVal, strVal, false)
	return
}

// emitRegexMatch is emitRegexSingleMatchCore, also building exec's `groups`
// (the named groups' dictionary, or null) when wantGroups.
func (e *Emitter) emitRegexMatch(objVal, strVal Value, wantGroups bool) (result Value, matchStartReg, matchEndReg, groupsReg string) {
	e.ensureRegexMatch()
	e.ensureMalloc()
	e.ensureMemcpy()

	handleReg := e.emitRegexHandleLoad(objVal)
	useLastIdxReg, lastIndexReg, startOffsetReg, matchOptsReg := e.emitRegexMatchStart(objVal, strVal)

	captureSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", captureSlot))
	e.emitInstr(fmt.Sprintf("call i32 @pcre2_pattern_info_8(ptr %s, i32 %d, ptr %s)", handleReg, pcre2InfoCaptureCount, captureSlot))
	captureCount32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", captureCount32, captureSlot))
	captureCountReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i32 %s to i64", captureCountReg, captureCount32))
	groupCountReg := e.freshReg() // full match + capture groups
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", groupCountReg, captureCountReg))

	matchDataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @pcre2_match_data_create_from_pattern_8(ptr %s, ptr null)", matchDataReg, handleReg))
	rcReg := e.freshReg()
	mctx := e.emitRegexMatchContext()
	e.emitInstr(fmt.Sprintf("%s = call i32 @pcre2_match_8(ptr %s, ptr %s, i64 %d, i64 %s, i32 %s, ptr %s, ptr %s)",
		rcReg, handleReg, strVal.Ref, pcre2ZeroTerminated, startOffsetReg, matchOptsReg, matchDataReg, mctx))
	e.emitRegexFixOptional(handleReg, matchDataReg, rcReg)
	matchedReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i32 %s, 0", matchedReg, rcReg))

	resultPtrSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resultPtrSlot))
	resultLenSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resultLenSlot))
	matchStartSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", matchStartSlot))
	matchEndSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", matchEndSlot))
	groupsSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", groupsSlot))

	matchedL := e.freshLabel("regex.exec.matched")
	nomatchL := e.freshLabel("regex.exec.nomatch")
	mergeL := e.freshLabel("regex.exec.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", matchedReg, matchedL, nomatchL))

	// --- matched branch: advance lastIndex (if global), build the result array ---
	e.emitLabel(matchedL)
	ovecReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @pcre2_get_ovector_pointer_8(ptr %s)", ovecReg, matchDataReg))

	matchStartGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 0", matchStartGep, ovecReg))
	matchStartVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", matchStartVal, matchStartGep))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", matchStartVal, matchStartSlot))

	endOfMatchGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 1", endOfMatchGep, ovecReg))
	endOfMatchReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", endOfMatchReg, endOfMatchGep))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", endOfMatchReg, matchEndSlot))
	// Write lastIndex back in the mode's own index space — the match end is a
	// byte offset, so convert to UTF-16 code units for es-utf16 (identity
	// elsewhere). The non-global branch keeps the original stored value
	// verbatim (already in that space), so it is never converted.
	endStoredReg := e.regexByteToUTF16(strVal.Ref, endOfMatchReg)
	advancedLastIndexReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", advancedLastIndexReg, useLastIdxReg, endStoredReg, lastIndexReg))
	e.emitRegexStoreLastIndex(objVal, advancedLastIndexReg)

	byteCountReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", byteCountReg, groupCountReg))
	dataReg := e.freshReg()
	e.heapMallocInto(dataReg, byteCountReg)

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	condL := e.freshLabel("regex.exec.cond")
	bodyL := e.freshLabel("regex.exec.body")
	doneL := e.freshLabel("regex.exec.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	idxVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	loopDoneReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", loopDoneReg, idxVal, groupCountReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", loopDoneReg, doneL, bodyL))

	e.emitLabel(bodyL)
	pairBase := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 2", pairBase, idxVal))
	startGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", startGep, ovecReg, pairBase))
	startOff := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", startOff, startGep))
	endPairIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", endPairIdx, pairBase))
	endGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", endGep, ovecReg, endPairIdx))
	endOff := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", endOff, endGep))

	isUnsetReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isUnsetReg, startOff, pcre2Unset))
	unsetL := e.freshLabel("regex.exec.unset")
	setL := e.freshLabel("regex.exec.set")
	elemMergeL := e.freshLabel("regex.exec.elemmerge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isUnsetReg, unsetL, setL))

	elemSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", elemSlot))

	e.emitLabel(unsetL)
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", elemSlot)) // an unmatched group is undefined
	e.emitTerminator(fmt.Sprintf("br label %%%s", elemMergeL))

	e.emitLabel(setL)
	subLenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", subLenReg, endOff, startOff))
	subBuf := e.emitStringAlloc(subLenReg) // TDD-00120: capture substrings are length-prefixed
	srcPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", srcPtr, strVal.Ref, startOff))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", subBuf, srcPtr, subLenReg))
	termGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", termGep, subBuf, subLenReg))
	e.heapStore("i8", "0", termGep, 1)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", subBuf, elemSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", elemMergeL))

	e.emitLabel(elemMergeL)
	elemVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", elemVal, elemSlot))
	dstGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", dstGep, dataReg, idxVal))
	e.heapStore("ptr", elemVal, dstGep, 8)
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	if wantGroups {
		e.ensureStringC()
		if !e.declaredRegexGroups {
			e.declaredRegexGroups = true
			e.emitGlobal("declare ptr @__kml_regex_groups(i32, i32, ptr, ptr, ptr, i32)")
		}
		// The name table (PCRE2_INFO_NAMECOUNT 17, NAMEENTRYSIZE 18,
		// NAMETABLE 19).
		cnt, esz, tbl := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", cnt))
		e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", esz))
		e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", tbl))
		e.emitInstr(fmt.Sprintf("store i32 0, ptr %s, align 4", cnt))
		e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", tbl))
		e.emitInstr(fmt.Sprintf("call i32 @pcre2_pattern_info_8(ptr %s, i32 17, ptr %s)", handleReg, cnt))
		e.emitInstr(fmt.Sprintf("call i32 @pcre2_pattern_info_8(ptr %s, i32 18, ptr %s)", handleReg, esz))
		e.emitInstr(fmt.Sprintf("call i32 @pcre2_pattern_info_8(ptr %s, i32 19, ptr %s)", handleReg, tbl))
		c, z, t := e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", c, cnt))
		e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", z, esz))
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", t, tbl))
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_regex_groups(i32 %s, i32 %s, ptr %s, ptr %s, ptr %s, i32 %s)", g, c, z, t, strVal.Ref, ovecReg, rcReg))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", g, groupsSlot))
	}
	e.emitInstr(fmt.Sprintf("call void @pcre2_match_data_free_8(ptr %s)", matchDataReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataReg, resultPtrSlot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", groupCountReg, resultLenSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	// --- no-match branch: reset lastIndex (if global), return the null sentinel ---
	e.emitLabel(nomatchL)
	e.emitInstr(fmt.Sprintf("call void @pcre2_match_data_free_8(ptr %s)", matchDataReg))
	resetLastIndexReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", resetLastIndexReg, useLastIdxReg, lastIndexReg))
	e.emitRegexStoreLastIndex(objVal, resetLastIndexReg)
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", resultPtrSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", resultLenSlot))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", matchStartSlot))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", matchEndSlot))
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", groupsSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	finalPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", finalPtr, resultPtrSlot))
	finalLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalLen, resultLenSlot))
	// A match always has element 0, so null data is no match: the null header.
	made := e.newArrayHeader(finalPtr, finalLen)
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr null, ptr %s", r1, e.ptrIsNull(finalPtr), made))

	finalStart := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalStart, matchStartSlot))
	finalEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalEnd, matchEndSlot))
	if wantGroups {
		groupsReg = e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", groupsReg, groupsSlot))
	}

	return Value{Ref: r1, Ty: regExpExecResultType()}, finalStart, finalEnd, groupsReg
}

// emitRegexCountGlobalMatches resets regexVal's lastIndex to 0, then runs
// its [[RegExpExec]]-equivalent (emitRegexSingleMatchCore) repeatedly,
// purely counting, until a call returns null — the shared first phase
// every global multi-match operation needs (str.match()/str.matchAll()'s
// two-pass emitRegexCollectGlobalMatches, and Stage 4's replace machinery,
// which needs a real match count up front to size its own parallel
// per-match arrays before doing a single further real pass that actually
// computes replacements). Side-effect-free (no replacement/callback logic
// runs here at all), so safe to always run once as its own phase — after
// this returns, lastIndex is guaranteed back at 0 (the final failed call's
// own reset), ready for a fresh second pass to start from the beginning.
func (e *Emitter) emitRegexCountGlobalMatches(regexVal, strVal Value) string {
	e.emitRegexStoreLastIndex(regexVal, "0")

	countAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", countAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", countAlloca))

	countBodyL := e.freshLabel("regex.count.body")
	countDoneL := e.freshLabel("regex.count.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", countBodyL))

	e.emitLabel(countBodyL)
	m1, m1Start, m1End := e.emitRegexSingleMatchCore(regexVal, strVal)
	m1Ptr := e.freshReg()
	m1Ptr = e.arrayData(m1)
	m1IsNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", m1IsNull, m1Ptr))
	countContinueL := e.freshLabel("regex.count.continue")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", m1IsNull, countDoneL, countContinueL))

	e.emitLabel(countContinueL)
	e.emitRegexAdvancePastEmpty(regexVal, strVal, m1Start, m1End)
	curCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curCount, countAlloca))
	nextCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nextCount, curCount))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nextCount, countAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", countBodyL))

	e.emitLabel(countDoneL)
	finalCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalCount, countAlloca))
	return finalCount
}

// emitRegexCollectGlobalMatches counts (emitRegexCountGlobalMatches), then
// runs a second real pass re-matching from the start — real JS's own
// iteration shape for both a global `str.match()` and `str.matchAll()`.
// The same "runtime counts, then builds the array" shape `__kml_split`
// already uses for a C-runtime-text helper, unrolled here as directly
// emitted IR instead, since each pass needs to call back into
// emitRegexSingleMatchCore, an already-Go-emitted function, not something
// a separate hand-written LLVM-text helper could invoke. Real PCRE2 work
// happens twice per match rather than once, an accepted simplicity/
// performance tradeoff (this project's `README.md` disclaimer: "not
// production-ready, never expected to be").
//
// storeElem is called once per real (non-null) match, given the
// same "runtime counts, then builds the array" shape `__kml_split`
// already uses for a C-runtime-text helper — unrolled here as directly
// emitted IR instead, since each pass needs to call back into
// emitRegexSingleMatchCore, an already-Go-emitted function, not something
// a separate hand-written LLVM-text helper could invoke). Each pass
// re-runs the identical deterministic sequence of matches (same pattern,
// same subject, lastIndex naturally ending at 0 again after the first
// pass's own final failed call, exactly like a single real `.exec()`
// would) — real PCRE2 work happens twice per match rather than once, an
// accepted simplicity/performance tradeoff (this project's `README.md`
// disclaimer: "not production-ready, never expected to be").
//
// storeElem is called once per real (non-null) match, given the
// destination slot's already-GEP'd ptr and that match's own
// regExpExecResultType()-shaped Value — it decides what actually goes into
// the slot (str.match()'s global branch stores just the full-match string,
// str.matchAll() stores the whole match array, boxed). Returns the built
// buffer and the real match count (0 and a still-valid, unused ptr if
// there were no matches at all — callers decide whether that should
// surface as `null` or an empty array).
func (e *Emitter) emitRegexCollectGlobalMatches(regexVal, strVal Value, storeElem func(destSlot string, match Value)) (dataReg, countReg string) {
	finalCount := e.emitRegexCountGlobalMatches(regexVal, strVal)

	e.ensureMalloc()
	byteCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", byteCount, finalCount))
	data := e.freshReg()
	e.heapMallocInto(data, byteCount)

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	fillBodyL := e.freshLabel("regex.collect.fillbody")
	fillDoneL := e.freshLabel("regex.collect.filldone")
	e.emitTerminator(fmt.Sprintf("br label %%%s", fillBodyL))

	e.emitLabel(fillBodyL)
	m2, m2Start, m2End := e.emitRegexSingleMatchCore(regexVal, strVal)
	m2Ptr := e.freshReg()
	m2Ptr = e.arrayData(m2)
	m2IsNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", m2IsNull, m2Ptr))
	fillContinueL := e.freshLabel("regex.collect.fillcontinue")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", m2IsNull, fillDoneL, fillContinueL))

	e.emitLabel(fillContinueL)
	e.emitRegexAdvancePastEmpty(regexVal, strVal, m2Start, m2End)
	curIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curIdx, idxAlloca))
	destSlot := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", destSlot, data, curIdx))
	storeElem(destSlot, m2)
	nextIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nextIdx, curIdx))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nextIdx, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", fillBodyL))

	e.emitLabel(fillDoneL)
	return data, finalCount
}

// emitStringMatch implements `str.match(regexp): string[] | null`
// (TDD-00035 Stage 3). Dual behavior, matching real JS's own
// %Symbol.match% exactly, decided at RUNTIME by the RegExp's `global`
// field (never known statically — a non-literal RegExp variable's flags
// aren't compile-time-known): without `g`, identical to
// `regexp.exec(str)` (one match, full array with capture groups, or
// `null`); with `g`, collects every match's *full text only* (no capture
// groups — real JS's own `g`-mode `.match()` behavior) via
// emitRegexCollectGlobalMatches, returning `null` if zero matches were
// found (matching real JS precisely — not an empty array; an earlier
// design note in docs/tdd/TDD-00035.md's Stage 3 section said "never
// null," which turned out to be a real mistake in the design writeup,
// corrected during implementation per usual practice — see ADR-00117).
//
// A non-RegExp argument is compiled as `new RegExp(String(arg))`, as real JS
// coerces it (emitRegExpArg).
// emitRegExpArg is the RegExp a match/matchAll/search argument denotes: a
// RegExp itself, or anything else as `new RegExp(String(arg), flags)` — the
// spec's coercion (matchAll's is global).
func (e *Emitter) emitRegExpArg(arg ast.Expression, flags string) (Value, error) {
	ty := e.inferExprType(arg)
	if ty.IsRegExp() {
		return e.emitExpr(arg)
	}
	pattern := arg
	if !isStringTy(ty) {
		pattern = stringCall(arg, arg.GetPos())
	}
	re := &ast.NewRegExpExpression{Pattern: pattern}
	if flags != "" {
		re.Flags = ast.NewStringLiteral(flags, arg.GetPos())
	}
	return e.emitNewRegExpExpression(re)
}

func (e *Emitter) emitStringMatch(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: match takes exactly 1 argument", pos.Line, pos.Col)
	}
	strVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(strVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: match is only supported on strings", pos.Line, pos.Col)
	}
	strVal = e.convert(strVal, TypePtr)
	args, err = e.hostRegExpArgs(args)
	if err != nil {
		return Value{}, err
	}
	regexVal, err := e.emitRegExpArg(args[0], "")
	if err != nil {
		return Value{}, err
	}
	if matchIsExec(args[0]) {
		// A pattern known not to be global: match is exec, with its index,
		// input and groups.
		return e.emitExecResult(regexVal, strVal), nil
	}

	globalReg := e.emitRegexLoadField(regexVal, "global", "i1", 1)

	// Either way the result is an exec-shaped header (null for no match): a
	// non-global match's carries index, input and groups; a global one's
	// is the plain array of full matches, index -1 marking it.
	hdrSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", hdrSlot))
	globalL := e.freshLabel("regex.match.global")
	singleL := e.freshLabel("regex.match.single")
	mergeL := e.freshLabel("regex.match.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", globalReg, globalL, singleL))

	// --- non-global: regexp.exec(str) ---
	e.emitLabel(singleL)
	single := e.emitExecResult(regexVal, strVal)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", single.Ref, hdrSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	// --- global: every match's full text, null if none ---
	e.emitLabel(globalL)
	data, count := e.emitRegexCollectGlobalMatches(regexVal, strVal, func(destSlot string, m Value) {
		mPtr := e.freshReg()
		mPtr = e.arrayData(m)
		elem0Gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 0", elem0Gep, mPtr))
		elem0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", elem0, elem0Gep))
		e.heapStore("ptr", elem0, destSlot, 8)
	})
	ghdr := e.allocArrayHeaderSized(48, true)
	for i, f := range []string{"ptr " + data, "i64 " + count, "ptr null", "i64 -1"} {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", gep, ghdr, i*8))
		e.heapStoreTV(f, gep, 8)
	}
	none, gsel := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", none, count))
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr null, ptr %s", gsel, none, ghdr))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", gsel, hdrSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	hdr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", hdr, hdrSlot))
	ty := execArrayType()
	ty.ExecMaybePlain = true
	return e.loadArrayHeaderOrAbsent(hdr, ty), nil
}

// emitStringMatchAll implements `str.matchAll(regexp)`: a RegExp String
// Iterator over exec results of a clone of the RegExp (ADR-01339). Requires
// the `global` flag, throwing a catchable TypeError otherwise, as JavaScript
// does.
func (e *Emitter) emitStringMatchAll(mem *ast.MemberExpression, args []ast.Expression, pos ast.Pos) (Value, error) {
	if len(args) != 1 {
		return Value{}, fmt.Errorf("%d:%d: matchAll takes exactly 1 argument", pos.Line, pos.Col)
	}
	strVal, err := e.emitExpr(mem.Object)
	if err != nil {
		return Value{}, err
	}
	if !isStringTy(strVal.Ty) {
		return Value{}, fmt.Errorf("%d:%d: matchAll is only supported on strings", pos.Line, pos.Col)
	}
	strVal = e.convert(strVal, TypePtr)
	args, err = e.hostRegExpArgs(args)
	if err != nil {
		return Value{}, err
	}
	regexVal, err := e.emitRegExpArg(args[0], "g")
	if err != nil {
		return Value{}, err
	}

	e.ensureExceptionHelpers()
	globalReg := e.emitRegexLoadField(regexVal, "global", "i1", 1)
	badL := e.freshLabel("regex.matchall.notglobal")
	okL := e.freshLabel("regex.matchall.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", globalReg, okL, badL))

	e.emitLabel(badL)
	errReg := e.buildErrorObj(errorKindIDs["TypeError"], e.internString("String.prototype.matchAll called with a non-global RegExp argument"), e.internString("TypeError"))
	e.excThrow(errReg)
	e.emitTerminator("unreachable")

	e.emitLabel(okL)
	// The spec's matchAll runs exec on a clone of the RegExp (its source and
	// flags, starting at its lastIndex), each step an exec result. The
	// string is immutable and the clone private, so the steps are taken
	// here, in order, and the iterator walks them.
	e.pushScope()
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", regexVal.Ref, slot))
	e.define("__kml_matchall_re", Symbol{Ptr: slot, Ty: regexVal.Ty})
	clone, err := e.emitNewRegExpExpression(&ast.NewRegExpExpression{Pattern: ast.NewIdentifier("__kml_matchall_re", pos)})
	e.popScope()
	if err != nil {
		return Value{}, err
	}
	lastIdx := e.emitRegexLoadField(regexVal, "lastIndex", "i64", 8)
	idx, _, _ := clone.Ty.FieldIndex("lastIndex")
	lgep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", lgep, clone.Ty.StructIR(), clone.Ref, idx))
	e.heapStore("i64", lastIdx, lgep, 8)

	// At most as many steps as global matches from the start.
	count := e.emitRegexCountGlobalMatches(clone, strVal)
	e.heapStore("i64", lastIdx, lgep, 8)
	e.ensureMalloc()
	bytes, data := e.freshReg(), e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", bytes, count))
	e.heapMallocInto(data, bytes)
	nSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", nSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", nSlot))
	loopL, stepL, doneL := e.freshLabel("matchall.loop"), e.freshLabel("matchall.step"), e.freshLabel("matchall.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))
	e.emitLabel(loopL)
	n := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", n, nSlot))
	full := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp uge i64 %s, %s", full, n, count))
	execL := e.freshLabel("matchall.exec")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", full, doneL, execL))
	e.emitLabel(execL)
	step, start, end := e.emitExecStep(clone, strVal)
	isNull := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNull, step.Ref))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNull, doneL, stepL))
	e.emitLabel(stepL)
	e.emitRegexAdvancePastEmpty(clone, strVal, start, end)
	dest := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", dest, data, n))
	e.heapStore("ptr", step.Ref, dest, 8)
	n1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", n1, n))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", n1, nSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopL))
	e.emitLabel(doneL)
	got := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", got, nSlot))
	header := e.newArrayHeader(data, got)
	steps := e.arrayValueFromHeaderReg(header, ArrayOf(execArrayType()))
	return e.emitArrayIterOpenValue(steps, mapIterValues, true), nil
}

// emitExecStep is one exec() of objVal against strVal, as emitExecResult,
// with the match's raw byte offsets.
func (e *Emitter) emitExecStep(objVal, strVal Value) (Value, string, string) {
	result, start, end, groups := e.emitRegexMatch(objVal, strVal, true)
	return e.execResultFrom(result, start, groups, strVal), start, end
}

// emitExecResult runs one exec() of objVal against strVal: the match array,
// whose header carries index, input and groups after its {data, len}
// (execArrayType); no match is the null header.
func (e *Emitter) emitExecResult(objVal, strVal Value) Value {
	result, start, _, groups := e.emitRegexMatch(objVal, strVal, true)
	return e.execResultFrom(result, start, groups, strVal)
}

// execResultFrom builds an exec result from a match.
func (e *Emitter) execResultFrom(result Value, start, groups string, strVal Value) Value {
	// The result array's header carries index, input and groups after its
	// {data, len} (execArrayHeaderTy); no match is the null header.
	data, n := e.arrayParts(result)
	hdr := e.allocArrayHeaderSized(48, false)
	idx := e.regexByteToUTF16Signed(strVal.Ref, start)
	for i, f := range []string{"ptr " + data, "i64 " + n, "ptr null", "i64 " + idx, "ptr " + strVal.Ref, "ptr " + groups} {
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %d", gep, hdr, i*8))
		e.heapStoreTV(f, gep, 8)
	}
	header := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, ptr null, ptr %s", header, e.emitArrayIsAbsent(result), hdr))
	ty := result.Ty
	ty.ExecArray = true
	return Value{Ref: header, Ty: ty}
}

// matchIsExec reports a str.match() argument known not to be global — a
// regex literal (or new RegExp with literal flags) without the g flag — so
// the call is exec and its result carries index, input and groups.
func matchIsExec(arg ast.Expression) bool {
	re, ok := arg.(*ast.NewRegExpExpression)
	if !ok {
		return false
	}
	if re.Flags == nil {
		return true
	}
	lit, ok := re.Flags.(*ast.StringLiteral)
	return ok && !strings.Contains(lit.Value, "g")
}

// execMemberValueType is the type execMemberRead gives prop.
func execMemberValueType(prop string, maybePlain bool) Type {
	ty, _, _ := execArrayMemberType(prop)
	if maybePlain {
		m := execMembers[prop]
		if m.absentIndex {
			return undefinedableElem(ty)
		}
		if m.absentNull {
			ty.setNullable(true)
			ty.setUndefined(true)
		}
	}
	return ty
}

// emitRegexMatchContext is the match context argument of a match: in the
// ES dialect, regexp.c's, whose callout rejects an empty iteration.
func (e *Emitter) emitRegexMatchContext() string {
	if e.resolveRegexMode() == "pcre" {
		return "null"
	}
	e.ensureRegexMatch()
	r := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_regex_mctx()", r))
	return r
}

// emitRegexFixOptional applies ECMAScript's empty-iteration rule to the
// captures of a match (regexp.c's __kml_regex_fix_optional).
func (e *Emitter) emitRegexFixOptional(handle, md, rc string) {
	e.emitInstr(fmt.Sprintf("call void @__kml_regex_fix_optional(ptr %s, ptr %s, i32 %s)", handle, md, rc))
}
