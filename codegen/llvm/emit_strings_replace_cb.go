package llvm

import (
	"KlainMainLang/ast"
	"fmt"
)

// emit_strings_replace_cb.go — the function-replacer path for the
// string-literal-search forms of String.prototype.replace() and
// .replaceAll() (ADR-00697). The RegExp-search forms already accept a
// callback (emit_regexp_replace.go); this brings the plain-string-search
// forms to parity. Real JS invokes the replacer with (match, offset,
// string): the matched substring (always identical to the literal search
// string here), the byte offset of the match, and the whole subject
// string. Untyped callback parameters default to (string, number, string),
// the same hint the RegExp path applies via resolveCallbackWithHints.
//
// Algorithm: a 3-pass memmem scan mirroring the RegExp all-matches builder.
// Pass 1 counts occurrences (pure). Pass 2 runs the callback exactly once
// per match — a real side effect that must never be re-run — recording each
// match's start offset and its computed replacement (ptr, len) into parallel
// count-sized arrays. Pass 3 (pure) sums the output length and copies each
// gap-then-replacement segment plus the trailing gap. For replace() (all =
// false) the match count is clamped to at most one.
//
// Narrowings (documented): the offset is a byte offset (identity with JS's
// UTF-16 code-unit index for BMP/ASCII text). An empty search string with a
// function replacer produces zero matches (returns the subject unchanged)
// rather than JS's insert-between-every-position behavior — the string-value
// empty-search path is unaffected.
func (e *Emitter) emitStringReplaceLiteralCallback(objVal Value, searchExpr, cbExpr ast.Expression, all bool, pos ast.Pos) (Value, error) {
	cb, err := e.resolveCallbackWithHints(cbExpr, []Type{TypePtr, TypeI64, TypePtr})
	if err != nil {
		return Value{}, err
	}

	objVal = e.coerce(objVal, TypePtr)
	searchVal, err := e.emitExpr(searchExpr)
	if err != nil {
		return Value{}, err
	}
	searchVal = e.coerce(searchVal, TypePtr)

	e.ensureMalloc()
	e.ensureMemcpy()
	e.ensureStrHeaderRuntime() // memmem + __kml_str_alloc + __kml_str_len

	sLen := e.emitStrLenHeader(objVal.Ref)
	searchLen := e.emitStrLenHeader(searchVal.Ref)

	resultSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", resultSlot))

	// Empty-search narrowing: return the subject unchanged. Also sidesteps the
	// zero-length-needle memmem loop that would never make forward progress.
	isEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isEmpty, searchLen))
	emptyL := e.freshLabel("strrepcb.empty")
	scanL := e.freshLabel("strrepcb.scan")
	mergeL := e.freshLabel("strrepcb.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isEmpty, emptyL, scanL))

	e.emitLabel(emptyL)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", objVal.Ref, resultSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(scanL)

	// --- pass 1: count occurrences (pure) ---
	count := e.emitStringLiteralCountMatches(objVal.Ref, sLen, searchVal.Ref, searchLen)
	if !all {
		// replace() replaces only the first occurrence.
		clamped := e.freshReg()
		hasOne := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ugt i64 %s, 0", hasOne, count))
		e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 1, i64 0", clamped, hasOne))
		count = clamped
	}

	byteCount8 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", byteCount8, count))
	starts := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", starts, byteCount8))
	replPtrs := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", replPtrs, byteCount8))
	replLens := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", replLens, byteCount8))

	// --- pass 2: fill (callback runs exactly once per match) ---
	// A running cursor into the subject and the running match index.
	curAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", curAlloca))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", objVal.Ref, curAlloca))
	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	fillCondL := e.freshLabel("strrepcb.fillcond")
	fillBodyL := e.freshLabel("strrepcb.fillbody")
	fillDoneL := e.freshLabel("strrepcb.filldone")
	e.emitTerminator(fmt.Sprintf("br label %%%s", fillCondL))

	e.emitLabel(fillCondL)
	idxVal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", idxVal, idxAlloca))
	fillDoneReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", fillDoneReg, idxVal, count))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", fillDoneReg, fillDoneL, fillBodyL))

	e.emitLabel(fillBodyL)
	curPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curPtr, curAlloca))
	// Remaining bytes from the cursor to the end of the subject.
	curInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", curInt, curPtr))
	baseInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", baseInt, objVal.Ref))
	consumed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", consumed, curInt, baseInt))
	remaining := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", remaining, sLen, consumed))
	found := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @memmem(ptr %s, i64 %s, ptr %s, i64 %s)", found, curPtr, remaining, searchVal.Ref, searchLen))
	foundInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", foundInt, found))
	startOff := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", startOff, foundInt, baseInt))

	// Record the start offset.
	startGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", startGep, starts, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", startOff, startGep))

	// Invoke the callback with (match, offset, string). The matched substring
	// is exactly the literal search string; offset is its byte position.
	cbArgs := []Value{
		{Ref: searchVal.Ref, Ty: TypePtr},
		{Ref: startOff, Ty: TypeI64},
		{Ref: objVal.Ref, Ty: TypePtr},
	}
	resultVal, err := e.emitCBCall(cb, cbArgs)
	if err != nil {
		return Value{}, err
	}
	resultVal = e.coerce(resultVal, TypePtr)
	e.ensureStrlen()
	replLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", replLen, resultVal.Ref))

	replPtrGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", replPtrGep, replPtrs, idxVal))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", resultVal.Ref, replPtrGep))
	replLenGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", replLenGep, replLens, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", replLen, replLenGep))

	// Advance the cursor past this match.
	nextPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", nextPtr, found, searchLen))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", nextPtr, curAlloca))
	idxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", idxNext, idxVal))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", idxNext, idxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", fillCondL))

	// --- pass 3: pure — sum lengths, then build the output ---
	e.emitLabel(fillDoneL)
	outBuf := e.emitStringLiteralBuildOutput(objVal.Ref, sLen, searchLen, count, starts, replPtrs, replLens)
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", outBuf, resultSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	final := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", final, resultSlot))
	return Value{Ref: final, Ty: TypePtr}, nil
}

// emitStringLiteralCountMatches counts the non-overlapping occurrences of a
// literal search string in the subject via a memmem scan (pure — no callback
// invocation).
func (e *Emitter) emitStringLiteralCountMatches(sRef, sLen, searchRef, searchLen string) string {
	curAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", curAlloca))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", sRef, curAlloca))
	cntAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", cntAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", cntAlloca))

	condL := e.freshLabel("strrepcb.cntcond")
	bodyL := e.freshLabel("strrepcb.cntbody")
	doneL := e.freshLabel("strrepcb.cntdone")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	curPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load ptr, ptr %s, align 8", curPtr, curAlloca))
	curInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", curInt, curPtr))
	baseInt := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = ptrtoint ptr %s to i64", baseInt, sRef))
	consumed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", consumed, curInt, baseInt))
	remaining := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", remaining, sLen, consumed))
	found := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @memmem(ptr %s, i64 %s, ptr %s, i64 %s)", found, curPtr, remaining, searchRef, searchLen))
	hasReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp ne ptr %s, null", hasReg, found))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", hasReg, bodyL, doneL))

	e.emitLabel(bodyL)
	cur := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, cntAlloca))
	cur1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", cur1, cur))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", cur1, cntAlloca))
	nextPtr := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", nextPtr, found, searchLen))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", nextPtr, curAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	count := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", count, cntAlloca))
	return count
}

// emitStringLiteralBuildOutput sums the final output length and builds the
// result string from the recorded match starts and per-match replacements
// (pure — no re-matching or callback re-invocation). Each match consumes
// searchLen subject bytes; gaps are the subject text between consecutive
// matches, plus the trailing text after the last match.
func (e *Emitter) emitStringLiteralBuildOutput(sRef, sLen, searchLen, count, starts, replPtrs, replLens string) string {
	// Sum: trailing subject length is (sLen - count*searchLen) + sum(replLen).
	removed := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, %s", removed, count, searchLen))
	base := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", base, sLen, removed))

	totalAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", totalAlloca))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", base, totalAlloca))
	sumIdxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", sumIdxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", sumIdxAlloca))

	sumCondL := e.freshLabel("strrepcb.sumcond")
	sumBodyL := e.freshLabel("strrepcb.sumbody")
	sumDoneL := e.freshLabel("strrepcb.sumdone")
	e.emitTerminator(fmt.Sprintf("br label %%%s", sumCondL))

	e.emitLabel(sumCondL)
	sumIdx := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", sumIdx, sumIdxAlloca))
	sumDoneReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", sumDoneReg, sumIdx, count))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", sumDoneReg, sumDoneL, sumBodyL))

	e.emitLabel(sumBodyL)
	sumReplLenGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", sumReplLenGep, replLens, sumIdx))
	sumReplLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", sumReplLen, sumReplLenGep))
	curTotal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curTotal, totalAlloca))
	newTotal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newTotal, curTotal, sumReplLen))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newTotal, totalAlloca))
	sumIdxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", sumIdxNext, sumIdx))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", sumIdxNext, sumIdxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", sumCondL))

	e.emitLabel(sumDoneL)
	grandTotal := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", grandTotal, totalAlloca))
	outBuf := e.emitStringAlloc(grandTotal)

	// Copy loop: for each match, copy the gap (subject[lastPos..start]) then
	// the replacement; finally the trailing subject after the last match.
	lastPosAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lastPosAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", lastPosAlloca))
	dstAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", dstAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", dstAlloca))
	copyIdxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", copyIdxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", copyIdxAlloca))

	copyCondL := e.freshLabel("strrepcb.copycond")
	copyBodyL := e.freshLabel("strrepcb.copybody")
	copyDoneL := e.freshLabel("strrepcb.copydone")
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
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", copyCurLastPos, lastPosAlloca))
	copyGapLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", copyGapLen, copyStart, copyCurLastPos))
	copyCurDst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", copyCurDst, dstAlloca))
	gapDstGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", gapDstGep, outBuf, copyCurDst))
	gapSrcGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", gapSrcGep, sRef, copyCurLastPos))
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
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", dstAfterRepl, dstAlloca))

	// lastPos = start + searchLen (past the matched literal).
	newLastPos := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", newLastPos, copyStart, searchLen))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", newLastPos, lastPosAlloca))

	copyIdxNext := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", copyIdxNext, copyIdx))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", copyIdxNext, copyIdxAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", copyCondL))

	e.emitLabel(copyDoneL)
	finalLastPos := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalLastPos, lastPosAlloca))
	finalDst := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalDst, dstAlloca))
	trailingLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", trailingLen, sLen, finalLastPos))
	trailDstGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", trailDstGep, outBuf, finalDst))
	trailSrcGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", trailSrcGep, sRef, finalLastPos))
	e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", trailDstGep, trailSrcGep, trailingLen))
	termGep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", termGep, outBuf, grandTotal))
	e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", termGep))

	return outBuf
}
