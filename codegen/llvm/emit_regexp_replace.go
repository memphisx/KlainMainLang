package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emit_regexp_replace.go — str.replace(regexp, replacement)/
// str.replaceAll(regexp, replacement) (TDD-00035 Stage 4), split out of
// emit_regexp.go once this stage's own complexity (template-backreference
// expansion, callback resolution, two distinct replace-builder shapes)
// made that file too large to keep growing — same convention this
// project's other domains already follow once a single file gets big
// enough (see the project's own Codegen file map).
//
// replacement can be a literal string (with $1-$9/$&/$$ backreference
// expansion — a documented V1 narrowing: $`/$' are out of scope, and only
// single-digit group numbers are recognized) or a callback closure,
// invoked with (match, offset, string) — deliberately NOT the real JS
// shape's variadic "...capturedGroups" in the middle, since a closure's
// arity is fixed at compile time but a pattern's capture count is only
// known at runtime; there is no way to always supply exactly the
// callback's own declared arity of meaningful values otherwise. A
// callback declaring more than 3 parameters is a compile-time error.

// regexReplacer bundles however replace()/replaceAll() were asked to
// compute one match's replacement text.
type regexReplacer struct {
	isCallback bool
	cb         Callback // valid when isCallback
	template   Value    // valid when !isCallback
}

// resolveRegexReplacer evaluates replace()/replaceAll()'s second argument
// exactly once (matching real JS: the replacer expression itself — an
// arrow function literal, a closure variable, or a plain string — is
// evaluated a single time regardless of how many matches end up being
// replaced).
func (e *Emitter) resolveRegexReplacer(args []ast.Expression, pos ast.Pos) (regexReplacer, error) {
	if e.inferExprType(args[1]).IsFunc {
		// The replacer callback's signature is (match, offset, string): the
		// first argument is the matched substring (a string), the middle
		// offset argument is a number, and the last is the whole subject
		// string. Untyped arrow-function parameters default to `number`
		// otherwise, which mis-dispatches string methods on `match` and emits
		// invalid IR (a ptr in a double slot). Seed those defaults with hints
		// so an untyped `(m) => m.toUpperCase()` types `m` as a string.
		cb, err := e.resolveCallbackWithHints(args[1], []Type{TypePtr, TypeI64, TypePtr})
		if err != nil {
			return regexReplacer{}, err
		}
		if cb.arity() > 3 {
			return regexReplacer{}, fmt.Errorf("%d:%d: a replace()/replaceAll() callback supports at most 3 parameters (match, offset, string) — capture groups aren't passed positionally, since a callback's arity is fixed at compile time but a pattern's capture count isn't known until runtime", pos.Line, pos.Col)
		}
		return regexReplacer{isCallback: true, cb: cb}, nil
	}
	templateVal, err := e.emitExpr(args[1])
	if err != nil {
		return regexReplacer{}, err
	}
	templateVal = e.coerce(templateVal, TypePtr)
	return regexReplacer{template: templateVal}, nil
}

// emitRegexComputeOneReplacement computes the replacement text for exactly
// one match — either by calling the resolved callback exactly once (a real
// side effect, must never be re-run) or by expanding a literal template's
// backreferences (pure, emitRegexExpandTemplate is free to do its own
// internal scan without any single-invocation constraint).
func (e *Emitter) emitRegexComputeOneReplacement(replacer regexReplacer, match, strVal Value, matchStartReg string) (replPtr, replLen string, err error) {
	if !replacer.isCallback {
		p, l := e.emitRegexExpandTemplate(replacer.template, match)
		return p, l, nil
	}

	matchPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", matchPtr, match.Ref))
	elem0Gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 0", elem0Gep, matchPtr))
	fullMatch := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", fullMatch, elem0Gep))

	cbArgs := []Value{{Ref: fullMatch, Ty: TypePtr}}
	if replacer.cb.arity() >= 2 {
		// The `offset` argument is user-visible, so report it in the mode's
		// index space — UTF-16 code units for es-utf16 (identity elsewhere).
		offsetReg := e.regexByteToUTF16(strVal.Ref, matchStartReg)
		cbArgs = append(cbArgs, Value{Ref: offsetReg, Ty: TypeI64})
	}
	if replacer.cb.arity() >= 3 {
		cbArgs = append(cbArgs, strVal)
	}
	resultVal, err := e.emitCBCall(replacer.cb, cbArgs)
	if err != nil {
		return "", "", err
	}
	resultVal = e.coerce(resultVal, TypePtr)
	e.ensureStrlen()
	lenReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", lenReg, resultVal.Ref))
	return resultVal.Ref, lenReg, nil
}

// emitRegexExpandTemplate expands $1-$9/$&/$$ backreferences in a literal
// replacement template against one specific match's already-extracted
// result array (match) — $`/$' (pre-/post-match text) are deliberately
// out of scope (a documented V1 narrowing), and $N where N is not a valid
// capture-group number for this pattern (either not a digit 1-9, or >=
// the pattern's own capture count) is left as literal text, matching real
// JS. Single-pass, sized against a safe (over-)estimate of the expanded
// output's length (the template's own length plus the sum of every
// group's length — the worst-case bound if every 2-character "$N"
// sequence in the template expanded to its group's full text) rather than
// a true two-pass count, since computing that bound only needs one small
// additional pure loop over match's own elements, cheaper than scanning
// the template itself twice.
func (e *Emitter) emitRegexExpandTemplate(templateVal, match Value) (replPtr, replLen string) {
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()

	dataPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", dataPtr, match.Ref))
	groupCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 1", groupCount, match.Ref))

	// Sum every group's length (a safe upper bound on the expanded output).
	sumAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", sumAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", sumAlloca))
	sumIdxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", sumIdxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", sumIdxAlloca))

	sumCondL := e.freshLabel("regex.repltpl.sumcond")
	sumBodyL := e.freshLabel("regex.repltpl.sumbody")
	sumDoneL := e.freshLabel("regex.repltpl.sumdone")
	e.emitTerminator(fmt.Sprintf("br label %%%s", sumCondL))

	e.emitLabel(sumCondL)
	sumIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", sumIdx, sumIdxAlloca))
	sumDoneReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", sumDoneReg, sumIdx, groupCount))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", sumDoneReg, sumDoneL, sumBodyL))

	e.emitLabel(sumBodyL)
	sumElemGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", sumElemGep, dataPtr, sumIdx))
	sumElemPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", sumElemPtr, sumElemGep))
	sumElemLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", sumElemLen, sumElemPtr))
	curSum := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curSum, sumAlloca))
	newSum := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newSum, curSum, sumElemLen))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newSum, sumAlloca))
	sumIdxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", sumIdxNext, sumIdx))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sumIdxNext, sumIdxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", sumCondL))

	e.emitLabel(sumDoneL)
	totalGroupLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", totalGroupLen, sumAlloca))

	templateLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", templateLen, templateVal.Ref))
	upperBound := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", upperBound, templateLen, totalGroupLen))
	buf := e.emitStringScratchReg(upperBound) // TDD-00120: len set at return

	srcAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", srcAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", srcAlloca))
	dstAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", dstAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", dstAlloca))

	copyGroup := func(groupIdxReg string) {
		elemGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", elemGep, dataPtr, groupIdxReg))
		elemPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", elemPtr, elemGep))
		elemLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", elemLen, elemPtr))
		curDst := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curDst, dstAlloca))
		dstGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", dstGep, buf, curDst))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", dstGep, elemPtr, elemLen))
		newDst := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newDst, curDst, elemLen))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newDst, dstAlloca))
	}
	writeLiteralByte := func(byteVal string) {
		curDst := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curDst, dstAlloca))
		dstGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", dstGep, buf, curDst))
		e.emitInstr(fmt.Sprintf("store i8 %s, ptr %s, align 1", byteVal, dstGep))
		newDst := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", newDst, curDst))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newDst, dstAlloca))
	}
	advanceSrcTo := func(newSrcReg string) {
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newSrcReg, srcAlloca))
	}
	advanceSrcBy1 := func(fromReg string) {
		newSrc := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", newSrc, fromReg))
		advanceSrcTo(newSrc)
	}

	scanCondL := e.freshLabel("regex.repltpl.scancond")
	scanBodyL := e.freshLabel("regex.repltpl.scanbody")
	scanDoneL := e.freshLabel("regex.repltpl.scandone")
	e.emitTerminator(fmt.Sprintf("br label %%%s", scanCondL))

	e.emitLabel(scanCondL)
	srcIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", srcIdx, srcAlloca))
	scanDoneReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", scanDoneReg, srcIdx, templateLen))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", scanDoneReg, scanDoneL, scanBodyL))

	e.emitLabel(scanBodyL)
	chGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", chGep, templateVal.Ref, srcIdx))
	ch := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", ch, chGep))
	isDollar := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, 36", isDollar, ch))
	dollarL := e.freshLabel("regex.repltpl.dollar")
	plainL := e.freshLabel("regex.repltpl.plain")
	loopBackL := e.freshLabel("regex.repltpl.loopback")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isDollar, dollarL, plainL))

	e.emitLabel(plainL)
	writeLiteralByte(ch)
	advanceSrcBy1(srcIdx)
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopBackL))

	e.emitLabel(dollarL)
	nextIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nextIdx, srcIdx))
	hasNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", hasNext, nextIdx, templateLen))
	hasNextL := e.freshLabel("regex.repltpl.hasnext")
	noNextL := e.freshLabel("regex.repltpl.nonext")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasNext, hasNextL, noNextL))

	e.emitLabel(noNextL)
	writeLiteralByte("36")
	advanceSrcBy1(srcIdx)
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopBackL))

	e.emitLabel(hasNextL)
	nextChGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", nextChGep, templateVal.Ref, nextIdx))
	nextCh := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i8, ptr %s, align 1", nextCh, nextChGep))

	isDollarDollar := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, 36", isDollarDollar, nextCh))
	ddL := e.freshLabel("regex.repltpl.dd")
	checkAmpL := e.freshLabel("regex.repltpl.checkamp")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isDollarDollar, ddL, checkAmpL))

	e.emitLabel(ddL)
	writeLiteralByte("36")
	advanceSrcBy1(nextIdx)
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopBackL))

	e.emitLabel(checkAmpL)
	isAmp := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i8 %s, 38", isAmp, nextCh))
	ampL := e.freshLabel("regex.repltpl.amp")
	checkDigitL := e.freshLabel("regex.repltpl.checkdigit")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isAmp, ampL, checkDigitL))

	e.emitLabel(ampL)
	copyGroup("0")
	advanceSrcBy1(nextIdx)
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopBackL))

	e.emitLabel(checkDigitL)
	isDigitLowReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i8 %s, 49", isDigitLowReg, nextCh)) // >= '1'
	isDigitHighReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sle i8 %s, 57", isDigitHighReg, nextCh)) // <= '9'
	isDigitReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", isDigitReg, isDigitLowReg, isDigitHighReg))
	digitL := e.freshLabel("regex.repltpl.digit")
	notSpecialL := e.freshLabel("regex.repltpl.notspecial")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isDigitReg, digitL, notSpecialL))

	e.emitLabel(notSpecialL)
	writeLiteralByte("36")
	advanceSrcBy1(srcIdx)
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopBackL))

	e.emitLabel(digitL)
	digitVal8 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i8 %s, 48", digitVal8, nextCh))
	digitVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i8 %s to i64", digitVal, digitVal8))
	validGroup := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp slt i64 %s, %s", validGroup, digitVal, groupCount))
	validL := e.freshLabel("regex.repltpl.validgroup")
	invalidL := e.freshLabel("regex.repltpl.invalidgroup")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", validGroup, validL, invalidL))

	e.emitLabel(invalidL)
	writeLiteralByte("36")
	advanceSrcBy1(srcIdx)
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopBackL))

	e.emitLabel(validL)
	copyGroup(digitVal)
	advanceSrcBy1(nextIdx)
	e.emitTerminator(fmt.Sprintf("br label %%%s", loopBackL))

	e.emitLabel(loopBackL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", scanCondL))

	e.emitLabel(scanDoneL)
	finalDst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalDst, dstAlloca))
	termGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", termGep, buf, finalDst))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", termGep))
	e.emitStringSetLen(buf, finalDst) // TDD-00120: exact expanded length

	return buf, finalDst
}

// emitRegexReplaceSingleMatch implements the non-global str.replace()
// case: at most one replacement, matching real JS's own non-global
// %Symbol.replace% algorithm exactly (a single RegExpExec call). No match
// returns the original subject string unchanged.
func (e *Emitter) emitRegexReplaceSingleMatch(strVal, regexVal Value, replacer regexReplacer) (Value, error) {
	match, startReg, endReg := e.emitRegexSingleMatchCore(regexVal, strVal)
	matchPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = extractvalue {ptr, i64} %s, 0", matchPtr, match.Ref))
	isNullReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq ptr %s, null", isNullReg, matchPtr))

	noMatchL := e.freshLabel("regex.replace1.nomatch")
	matchedL := e.freshLabel("regex.replace1.matched")
	mergeL := e.freshLabel("regex.replace1.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isNullReg, noMatchL, matchedL))

	resultSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resultSlot))

	e.emitLabel(noMatchL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", strVal.Ref, resultSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(matchedL)
	replPtr, replLenReg, err := e.emitRegexComputeOneReplacement(replacer, match, strVal, startReg)
	if err != nil {
		return Value{}, err
	}
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()
	subjectLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", subjectLen, strVal.Ref))
	tailLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", tailLen, subjectLen, endReg))
	prefixPlusRepl := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", prefixPlusRepl, startReg, replLenReg))
	outLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", outLen, prefixPlusRepl, tailLen))
	outBuf := e.emitStringAlloc(outLen) // TDD-00120: length-prefixed

	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", outBuf, strVal.Ref, startReg))
	replDst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", replDst, outBuf, startReg))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", replDst, replPtr, replLenReg))
	tailDstOff := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", tailDstOff, startReg, replLenReg))
	tailDst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", tailDst, outBuf, tailDstOff))
	tailSrc := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", tailSrc, strVal.Ref, endReg))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", tailDst, tailSrc, tailLen))
	termGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", termGep, outBuf, outLen))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", termGep))

	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", outBuf, resultSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	final := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", final, resultSlot))
	return Value{Ref: final, Ty: TypePtr}, nil
}

// emitRegexReplaceAllMatches implements the global-replace case shared by
// `str.replace()` (when the RegExp's `global` flag is true) and
// `str.replaceAll()` (which requires it). A genuine 3-pass algorithm,
// unlike emitRegexCollectGlobalMatches's 2-pass shape — replace's second
// pass must compute each match's replacement text exactly once (a
// callback is a real side effect that must never run twice), so counting
// (pass 1, pure, emitRegexCountGlobalMatches) and computing (pass 2) are
// kept strictly separate: pass 2 runs the real match-and-replace logic
// exactly once per match, storing each match's (start, end, replacement
// ptr, replacement len) into four count-sized parallel arrays; pass 3
// (pure, no re-matching or re-invocation at all) sums the final output
// length from those already-computed pieces and builds the result string
// by copying each gap-then-replacement segment, plus the trailing gap
// after the last match.
func (e *Emitter) emitRegexReplaceAllMatches(strVal, regexVal Value, replacer regexReplacer) (Value, error) {
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()

	count := e.emitRegexCountGlobalMatches(regexVal, strVal)

	byteCount8 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", byteCount8, count))
	starts := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", starts, byteCount8))
	ends := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", ends, byteCount8))
	replPtrs := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", replPtrs, byteCount8))
	replLens := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", replLens, byteCount8))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	fillCondL := e.freshLabel("regex.replaceall.fillcond")
	fillBodyL := e.freshLabel("regex.replaceall.fillbody")
	fillDoneL := e.freshLabel("regex.replaceall.filldone")
	e.emitTerminator(fmt.Sprintf("br label %%%s", fillCondL))

	e.emitLabel(fillCondL)
	idxVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	fillDoneReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", fillDoneReg, idxVal, count))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", fillDoneReg, fillDoneL, fillBodyL))

	e.emitLabel(fillBodyL)
	match, startReg, endReg := e.emitRegexSingleMatchCore(regexVal, strVal)
	replPtr, replLenReg, err := e.emitRegexComputeOneReplacement(replacer, match, strVal, startReg)
	if err != nil {
		return Value{}, err
	}
	startGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", startGep, starts, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", startReg, startGep))
	endGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", endGep, ends, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", endReg, endGep))
	replPtrGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", replPtrGep, replPtrs, idxVal))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", replPtr, replPtrGep))
	replLenGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", replLenGep, replLens, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", replLenReg, replLenGep))

	// Advance past a zero-length match so this fill pass iterates in lockstep
	// with emitRegexCountGlobalMatches's count pass (both share the same
	// AdvanceStringIndex-style empty-match advance) and never re-finds the
	// same empty match forever — see emitRegexAdvancePastEmpty.
	e.emitRegexAdvancePastEmpty(regexVal, strVal, startReg, endReg)

	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", fillCondL))

	// --- pass 3: pure — sum lengths, then build the output string ---
	e.emitLabel(fillDoneL)
	subjectLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", subjectLen, strVal.Ref))

	totalAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", totalAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", totalAlloca))
	lastPosAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lastPosAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", lastPosAlloca))
	sumIdxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", sumIdxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", sumIdxAlloca))

	sumCondL := e.freshLabel("regex.replaceall.sumcond")
	sumBodyL := e.freshLabel("regex.replaceall.sumbody")
	sumDoneL := e.freshLabel("regex.replaceall.sumdone")
	e.emitTerminator(fmt.Sprintf("br label %%%s", sumCondL))

	e.emitLabel(sumCondL)
	sumIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", sumIdx, sumIdxAlloca))
	sumDoneReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", sumDoneReg, sumIdx, count))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", sumDoneReg, sumDoneL, sumBodyL))

	e.emitLabel(sumBodyL)
	sumStartGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", sumStartGep, starts, sumIdx))
	sumStart := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", sumStart, sumStartGep))
	sumReplLenGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", sumReplLenGep, replLens, sumIdx))
	sumReplLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", sumReplLen, sumReplLenGep))
	curLastPos := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curLastPos, lastPosAlloca))
	gapLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", gapLen, sumStart, curLastPos))
	curTotal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curTotal, totalAlloca))
	totalPlusGap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", totalPlusGap, curTotal, gapLen))
	newTotal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newTotal, totalPlusGap, sumReplLen))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newTotal, totalAlloca))
	sumEndGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", sumEndGep, ends, sumIdx))
	sumEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", sumEnd, sumEndGep))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sumEnd, lastPosAlloca))
	sumIdxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", sumIdxNext, sumIdx))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sumIdxNext, sumIdxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", sumCondL))

	e.emitLabel(sumDoneL)
	finalLastPos := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalLastPos, lastPosAlloca))
	trailingLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", trailingLen, subjectLen, finalLastPos))
	preTrailingTotal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", preTrailingTotal, totalAlloca))
	grandTotal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", grandTotal, preTrailingTotal, trailingLen))
	outBuf := e.emitStringAlloc(grandTotal) // TDD-00120: length-prefixed

	// Copy loop: gap-then-replacement per match, then the trailing gap.
	copyLastPosAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", copyLastPosAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", copyLastPosAlloca))
	copyDstAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", copyDstAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", copyDstAlloca))
	copyIdxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", copyIdxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", copyIdxAlloca))

	copyCondL := e.freshLabel("regex.replaceall.copycond")
	copyBodyL := e.freshLabel("regex.replaceall.copybody")
	copyDoneL := e.freshLabel("regex.replaceall.copydone")
	e.emitTerminator(fmt.Sprintf("br label %%%s", copyCondL))

	e.emitLabel(copyCondL)
	copyIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", copyIdx, copyIdxAlloca))
	copyDoneReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", copyDoneReg, copyIdx, count))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", copyDoneReg, copyDoneL, copyBodyL))

	e.emitLabel(copyBodyL)
	copyStartGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", copyStartGep, starts, copyIdx))
	copyStart := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", copyStart, copyStartGep))
	copyCurLastPos := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", copyCurLastPos, copyLastPosAlloca))
	copyGapLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", copyGapLen, copyStart, copyCurLastPos))
	copyCurDst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", copyCurDst, copyDstAlloca))
	gapDstGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", gapDstGep, outBuf, copyCurDst))
	gapSrcGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", gapSrcGep, strVal.Ref, copyCurLastPos))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", gapDstGep, gapSrcGep, copyGapLen))
	dstAfterGap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", dstAfterGap, copyCurDst, copyGapLen))

	copyReplPtrGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", copyReplPtrGep, replPtrs, copyIdx))
	copyReplPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", copyReplPtr, copyReplPtrGep))
	copyReplLenGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", copyReplLenGep, replLens, copyIdx))
	copyReplLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", copyReplLen, copyReplLenGep))
	replDstGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", replDstGep, outBuf, dstAfterGap))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", replDstGep, copyReplPtr, copyReplLen))
	dstAfterRepl := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", dstAfterRepl, dstAfterGap, copyReplLen))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", dstAfterRepl, copyDstAlloca))

	copyEndGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", copyEndGep, ends, copyIdx))
	copyEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", copyEnd, copyEndGep))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", copyEnd, copyLastPosAlloca))

	copyIdxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", copyIdxNext, copyIdx))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", copyIdxNext, copyIdxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", copyCondL))

	e.emitLabel(copyDoneL)
	finalCopyLastPos := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalCopyLastPos, copyLastPosAlloca))
	finalCopyDst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalCopyDst, copyDstAlloca))
	trailDstGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", trailDstGep, outBuf, finalCopyDst))
	trailSrcGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", trailSrcGep, strVal.Ref, finalCopyLastPos))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", trailDstGep, trailSrcGep, trailingLen))
	termGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", termGep, outBuf, grandTotal))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", termGep))

	return Value{Ref: outBuf, Ty: TypePtr}, nil
}

// emitRegexReplace is the shared entry point for both `str.replace()` and
// `str.replaceAll()` once their argument is confirmed to be a RegExp —
// called from emit_strings.go's emitStringReplace/emitStringReplaceAll
// ahead of their existing literal-string path. requireGlobal is true only
// for replaceAll(), which throws a catchable TypeError for a non-global
// RegExp (matching real JS); replace() itself never throws for this and
// instead branches on the `global` flag at runtime (never known statically
// for a non-literal RegExp variable) between a single-match replace and
// the same all-matches algorithm replaceAll() always uses.
func (e *Emitter) emitRegexReplace(strVal Value, args []ast.Expression, pos ast.Pos, requireGlobal bool) (Value, error) {
	strVal = e.coerce(strVal, TypePtr)
	regexVal, err := e.emitExpr(args[0])
	if err != nil {
		return Value{}, err
	}
	globalReg := e.emitRegexLoadField(regexVal, "global", "i1", 1)

	if requireGlobal {
		e.ensureExceptionHelpers()
		okL := e.freshLabel("regex.replaceall.ok")
		badL := e.freshLabel("regex.replaceall.notglobal")
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", globalReg, okL, badL))

		e.emitLabel(badL)
		errReg := e.buildErrorObj(errorKindIDs["TypeError"], e.internString("String.prototype.replaceAll called with a non-global RegExp argument"), e.internString("TypeError"))
		e.emitInstr(fmt.Sprintf("call void @__kml_throw(ptr %s)", errReg))
		e.emitTerminator("unreachable")

		e.emitLabel(okL)
	}

	replacer, err := e.resolveRegexReplacer(args, pos)
	if err != nil {
		return Value{}, err
	}

	if requireGlobal {
		return e.emitRegexReplaceAllMatches(strVal, regexVal, replacer)
	}

	resultSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resultSlot))

	allL := e.freshLabel("regex.replace.all")
	singleL := e.freshLabel("regex.replace.single")
	mergeL := e.freshLabel("regex.replace.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", globalReg, allL, singleL))

	e.emitLabel(singleL)
	singleResult, err := e.emitRegexReplaceSingleMatch(strVal, regexVal, replacer)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", singleResult.Ref, resultSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(allL)
	allResult, err := e.emitRegexReplaceAllMatches(strVal, regexVal, replacer)
	if err != nil {
		return Value{}, err
	}
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", allResult.Ref, resultSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	final := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", final, resultSlot))
	return Value{Ref: final, Ty: TypePtr}, nil
}
