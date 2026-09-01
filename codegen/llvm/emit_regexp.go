package llvm

import (
	"KlainMainLang/ast"
	"fmt"
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
		// es-unicode matching plus the Option C source normalization pass. Byte-
		// indexed like es-unicode (NOT utf16Index): the normalization aligns the
		// pattern *dialect*, while the index space stays consistent with the
		// byte-indexed string layer (.charCodeAt/.slice). A program that also
		// wants UTF-16 code-unit offsets selects es-utf16 explicitly — see
		// TDD-00067 and ADR-00208 for why ecmascript keeps byte indices.
		return regexModeOpts{baseOpt: pcre2AltBSUX | pcre2MatchUnsetBackref | pcre2UTF, dollarEndOnly: true, newline: pcre2NewlineAny, utfMatching: true, normalize: true}
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
	e.ensureRegexCompile()
	e.ensureMalloc()
	e.ensureExceptionHelpers()

	patternVal, err := e.emitExpr(ex.Pattern)
	if err != nil {
		return Value{}, err
	}
	patternVal = e.coerce(patternVal, TypePtr)

	var flagsVal Value
	if ex.Flags != nil {
		flagsVal, err = e.emitExpr(ex.Flags)
		if err != nil {
			return Value{}, err
		}
		flagsVal = e.coerce(flagsVal, TypePtr)
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
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", flagsErr))
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
	e.emitInstr(fmt.Sprintf("call void @__kml_regex_parse_flags(ptr %s, ptr %s, ptr %s, ptr %s, ptr %s, ptr %s)",
		flagsVal.Ref, optSlot, globalSlot, ignoreCaseSlot, multilineSlot, dotAllSlot))
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
	if modeOpts.normalize {
		e.ensureRegexESNormalize()
		dotAllForNorm := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i1, ptr %s, align 1", dotAllForNorm, dotAllSlot))
		normPat := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_regex_es_normalize(ptr %s, i1 %s)", normPat, patternVal.Ref, dotAllForNorm))
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
	errReg := e.buildErrorObj(errorKindIDs["SyntaxError"], msgBuf, e.internString("SyntaxError"))
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errReg))
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

	ty := RegExpType()
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %d)", dataReg, ty.StructSize()))
	structIR := ty.StructIR()

	storeField := func(name, ir, val string, align int) {
		idx, _, _ := ty.FieldIndex(name)
		gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, structIR, dataReg, idx))
		e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align %d", ir, val, gep, align))
	}
	storeField(RegexHandleField, "ptr", handleReg, 8)
	storeField("source", "ptr", patternVal.Ref, 8)
	storeField("flags", "ptr", flagsVal.Ref, 8)
	storeField("global", "i1", globalReg, 1)
	storeField("ignoreCase", "i1", ignoreCaseReg, 1)
	storeField("multiline", "i1", multilineReg, 1)
	storeField("dotAll", "i1", dotAllReg, 1)
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
	strVal = e.coerce(strVal, TypePtr)

	e.ensureRegexMatch()
	handleReg := e.emitRegexHandleLoad(objVal)
	globalReg := e.emitRegexLoadField(objVal, "global", "i1", 1)
	lastIndexReg := e.emitRegexLoadField(objVal, "lastIndex", "i64", 8)

	// lastIndex is stored in the mode's own index space (UTF-16 code units for
	// es-utf16, bytes otherwise); PCRE2 wants a byte start offset — convert on
	// the way in (identity in non-es-utf16 modes), and only honor it when
	// `global` is set.
	lastIndexByteReg := e.regexUTF16ToByte(strVal.Ref, lastIndexReg)
	startOffsetReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", startOffsetReg, globalReg, lastIndexByteReg))

	matchDataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @pcre2_match_data_create_from_pattern_8(ptr %s, ptr null)", matchDataReg, handleReg))

	rcReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @pcre2_match_8(ptr %s, ptr %s, i64 %d, i64 %s, i32 0, ptr %s, ptr null)",
		rcReg, handleReg, strVal.Ref, pcre2ZeroTerminated, startOffsetReg, matchDataReg))
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
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", advancedLastIndexReg, globalReg, endStoredReg, lastIndexReg))
	e.emitRegexStoreLastIndex(objVal, advancedLastIndexReg)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(nomatchL)
	resetLastIndexReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", resetLastIndexReg, globalReg, lastIndexReg))
	e.emitRegexStoreLastIndex(objVal, resetLastIndexReg)
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	e.emitInstr(fmt.Sprintf("call void @pcre2_match_data_free_8(ptr %s)", matchDataReg))
	return Value{Ref: matchedReg, Ty: TypeBool}, nil
}

// regExpExecResultType returns `.exec(str)`'s return type: `string[] | null`
// — a real, reusable `Nullable` array (Type.Nullable, already-existing
// machinery for T | null), not a new representation. "null" here means a
// {ptr,i64} aggregate with a null data pointer and zero length (this
// compiler has no other way to represent "absent" for an aggregate value —
// unlike a plain ptr-typed T | null, where a real null pointer already
// works) — see emitRegexExec.
func regExpExecResultType() Type {
	ty := ArrayOf(TypePtr)
	ty.Nullable = true
	return ty
}

// emitRegexStoreLastIndex GEPs/stores a new value into an already-evaluated
// RegExp instance's lastIndex field — shared by emitRegexExec's two
// branches (advance on a successful global/sticky match, reset to 0 on a
// failed one).
func (e *Emitter) emitRegexStoreLastIndex(objVal Value, newVal string) {
	idx, _, _ := objVal.Ty.FieldIndex("lastIndex")
	gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr %s, ptr %s, i32 0, i32 %d", gep, objVal.Ty.StructIR(), objVal.Ref, idx))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newVal, gep))
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
	strVal = e.coerce(strVal, TypePtr)
	result, _, _ := e.emitRegexSingleMatchCore(objVal, strVal)
	return result, nil
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
// nullable-string representation. Real JS's exec-result-is-an-array-with-
// extra-properties shape (`.index`/`.input`/`.groups`) is not built —
// this compiler's arrays are plain `{ptr,i64}` aggregates with no room for
// bolted-on named properties.
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
	e.ensureRegexMatch()
	e.ensureMalloc()
	e.ensureMemcpy()

	handleReg := e.emitRegexHandleLoad(objVal)
	globalReg := e.emitRegexLoadField(objVal, "global", "i1", 1)
	lastIndexReg := e.emitRegexLoadField(objVal, "lastIndex", "i64", 8)

	// lastIndex is stored in the mode's own index space (UTF-16 code units for
	// es-utf16, bytes otherwise); PCRE2 always wants a byte start offset, so
	// convert on the way in (identity in non-es-utf16 modes).
	lastIndexByteReg := e.regexUTF16ToByte(strVal.Ref, lastIndexReg)
	startOffsetReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 0", startOffsetReg, globalReg, lastIndexByteReg))

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
	e.emitInstr(fmt.Sprintf("%s = call i32 @pcre2_match_8(ptr %s, ptr %s, i64 %d, i64 %s, i32 0, ptr %s, ptr null)",
		rcReg, handleReg, strVal.Ref, pcre2ZeroTerminated, startOffsetReg, matchDataReg))
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
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 %s, i64 %s", advancedLastIndexReg, globalReg, endStoredReg, lastIndexReg))
	e.emitRegexStoreLastIndex(objVal, advancedLastIndexReg)

	byteCountReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", byteCountReg, groupCountReg))
	dataReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", dataReg, byteCountReg))

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
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", e.internString(""), elemSlot))
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
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", termGep))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", subBuf, elemSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", elemMergeL))

	e.emitLabel(elemMergeL)
	elemVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", elemVal, elemSlot))
	dstGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", dstGep, dataReg, idxVal))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", elemVal, dstGep))
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	e.emitInstr(fmt.Sprintf("call void @pcre2_match_data_free_8(ptr %s)", matchDataReg))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", dataReg, resultPtrSlot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", groupCountReg, resultLenSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	// --- no-match branch: reset lastIndex (if global), return the null sentinel ---
	e.emitLabel(nomatchL)
	e.emitInstr(fmt.Sprintf("call void @pcre2_match_data_free_8(ptr %s)", matchDataReg))
	resetLastIndexReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", resetLastIndexReg, globalReg, lastIndexReg))
	e.emitRegexStoreLastIndex(objVal, resetLastIndexReg)
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", resultPtrSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", resultLenSlot))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", matchStartSlot))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", matchEndSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	finalPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", finalPtr, resultPtrSlot))
	finalLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalLen, resultLenSlot))
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, finalPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, finalLen))

	finalStart := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalStart, matchStartSlot))
	finalEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalEnd, matchEndSlot))

	return Value{Ref: r1, Ty: regExpExecResultType()}, finalStart, finalEnd
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
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", m1Ptr, m1.Ref))
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
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", data, byteCount))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	fillBodyL := e.freshLabel("regex.collect.fillbody")
	fillDoneL := e.freshLabel("regex.collect.filldone")
	e.emitTerminator(fmt.Sprintf("br label %%%s", fillBodyL))

	e.emitLabel(fillBodyL)
	m2, m2Start, m2End := e.emitRegexSingleMatchCore(regexVal, strVal)
	m2Ptr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", m2Ptr, m2.Ref))
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
// No implicit string-to-RegExp coercion — real JS coerces a non-RegExp
// `match()` argument, but this compiler has no such coercion path (the
// same documented scope narrowing `.search()` already established, see
// docs/status/STRING-METHODS.md) — a non-RegExp argument is a compile-time
// error here instead.
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
	strVal = e.coerce(strVal, TypePtr)
	if !e.inferExprType(args[0]).IsRegExp {
		return Value{}, fmt.Errorf("%d:%d: match requires a RegExp argument (implicit string-to-RegExp coercion is not supported)", pos.Line, pos.Col)
	}
	regexVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}

	globalReg := e.emitRegexLoadField(regexVal, "global", "i1", 1)

	resultPtrSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resultPtrSlot))
	resultLenSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", resultLenSlot))

	globalL := e.freshLabel("regex.match.global")
	singleL := e.freshLabel("regex.match.single")
	mergeL := e.freshLabel("regex.match.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", globalReg, globalL, singleL))

	// --- non-global: identical to regexp.exec(str) ---
	e.emitLabel(singleL)
	single, _, _ := e.emitRegexSingleMatchCore(regexVal, strVal)
	singlePtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", singlePtr, single.Ref))
	singleLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", singleLen, single.Ref))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", singlePtr, resultPtrSlot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", singleLen, resultLenSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	// --- global: collect every match's full-text string, null if none ---
	e.emitLabel(globalL)
	data, count := e.emitRegexCollectGlobalMatches(regexVal, strVal, func(destSlot string, m Value) {
		mPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", mPtr, m.Ref))
		elem0Gep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 0", elem0Gep, mPtr))
		elem0 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", elem0, elem0Gep))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", elem0, destSlot))
	})
	zeroCountReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", zeroCountReg, count))
	zeroL := e.freshLabel("regex.match.zero")
	nonzeroL := e.freshLabel("regex.match.nonzero")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", zeroCountReg, zeroL, nonzeroL))

	e.emitLabel(zeroL)
	e.emitInstr(fmt.Sprintf("store ptr null, ptr %s, align 8", resultPtrSlot))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", resultLenSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(nonzeroL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", data, resultPtrSlot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", count, resultLenSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	finalPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", finalPtr, resultPtrSlot))
	finalLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalLen, resultLenSlot))
	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, finalPtr))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, finalLen))

	return Value{Ref: r1, Ty: regExpExecResultType()}, nil
}

// emitStringMatchAll implements `str.matchAll(regexp): string[][]`
// (TDD-00035 Stage 3) — scoped down to an eager array of match arrays
// rather than a real lazy iterator (`%RegExpStringIteratorPrototype%`): no
// lazy-iteration infrastructure exists anywhere in this compiler today,
// every `for...of` is already over an eager array/Map/Set/class-defined
// iterator, so building one here would be new infrastructure disproportionate
// to this feature. Each inner `string[]` is exactly one `.exec()`-shaped
// match array (full match + capture groups), stored boxed into the outer
// array via the existing nested-array machinery (storeArrayElem,
// TDD-00029) — no new boxing logic needed here at all.
//
// Requires the `global` flag, throwing a catchable TypeError otherwise —
// matches real JS's own hard requirement (`matchAll` throws synchronously
// for a non-global RegExp, since its whole point is iterating every match).
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
	strVal = e.coerce(strVal, TypePtr)
	if !e.inferExprType(args[0]).IsRegExp {
		return Value{}, fmt.Errorf("%d:%d: matchAll requires a RegExp argument (implicit string-to-RegExp coercion is not supported)", pos.Line, pos.Col)
	}
	regexVal, err := e.emitExpr(args[0])
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
	e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errReg))
	e.emitTerminator("unreachable")

	e.emitLabel(okL)
	innerTy := ArrayOf(TypePtr)
	data, count := e.emitRegexCollectGlobalMatches(regexVal, strVal, func(destSlot string, m Value) {
		e.storeArrayElem(destSlot, innerTy, m)
	})

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, data))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, count))

	return Value{Ref: r1, Ty: ArrayOf(innerTy)}, nil
}
