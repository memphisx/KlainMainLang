package llvm

import "fmt"

// emit_regexp_split.go — str.split(regexp)/str.search(regexp) (TDD-00035
// Stage 5), split out into its own file alongside emit_regexp_replace.go
// for the same reason that one was: keeps emit_regexp.go from continuing
// to grow indefinitely as each stage adds real, self-contained complexity.
//
// Both methods need a genuinely different match primitive than every
// earlier stage: real JS's own @@split algorithm (and @@search, per spec)
// deliberately does NOT read or write the RegExp instance's `lastIndex`
// property at all — split() tracks its own local search position
// regardless of the `global` flag (a non-global regex still finds every
// match when used with .split()), and search() always searches from
// offset 0 and restores whatever `lastIndex` held beforehand, so from the
// caller's perspective a `.search()` call is invisible to any later
// `.exec()`/`.test()` iteration. emitRegexSingleMatchCore's whole
// lastIndex-driven behavior (the right primitive for `.exec()`/
// `.test()`/`.match()`/`.matchAll()`/`.replace()`/`.replaceAll()`, all of
// which real JS *does* thread through the same RegExpExec/lastIndex
// machinery) is the wrong tool here — emitRegexMatchAt below is a bare,
// stateless single-match primitive instead: no RegExp instance is even
// evaluated, just a compiled-pattern handle and an explicit start offset.

// emitRegexMatchAt runs a single PCRE2 match attempt at an explicit start
// offset, touching no RegExp-instance state at all (see the file doc
// comment for why `.split()`/`.search()` need this instead of
// emitRegexSingleMatchCore). Returns whether it matched (as an i1 SSA
// value, valid at the call site — computed before any internal branching)
// plus the match's start/end byte offsets (-1/-1 on no match).
func (e *Emitter) emitRegexMatchAt(handleReg string, strVal Value, startOffsetReg string) (matchedReg, startReg, endReg string) {
	matched, start, end, md := e.emitRegexMatchAtData(handleReg, strVal, startOffsetReg)
	e.emitInstr(fmt.Sprintf("call void @pcre2_match_data_free_8(ptr %s)", md))
	return matched, start, end
}

// emitRegexMatchAtData is emitRegexMatchAt keeping the match data, whose
// ovector holds the capture groups; the caller frees it.
func (e *Emitter) emitRegexMatchAtData(handleReg string, strVal Value, startOffsetReg string) (matchedReg, startReg, endReg, matchDataReg string) {
	e.ensureRegexMatch()
	matchDataReg = e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @pcre2_match_data_create_from_pattern_8(ptr %s, ptr null)", matchDataReg, handleReg))
	rcReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i32 @pcre2_match_8(ptr %s, ptr %s, i64 %d, i64 %s, i32 0, ptr %s, ptr null)",
		rcReg, handleReg, strVal.Ref, pcre2ZeroTerminated, startOffsetReg, matchDataReg))
	matched := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i32 %s, 0", matched, rcReg))

	startSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", startSlot))
	endSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", endSlot))

	matchedL := e.freshLabel("regex.matchat.matched")
	nomatchL := e.freshLabel("regex.matchat.nomatch")
	mergeL := e.freshLabel("regex.matchat.merge")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", matched, matchedL, nomatchL))

	e.emitLabel(matchedL)
	ovecReg := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @pcre2_get_ovector_pointer_8(ptr %s)", ovecReg, matchDataReg))
	s0Gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 0", s0Gep, ovecReg))
	s0 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", s0, s0Gep))
	e1Gep := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 1", e1Gep, ovecReg))
	e1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", e1, e1Gep))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", s0, startSlot))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", e1, endSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(nomatchL)
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", startSlot))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", endSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	finalStart := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalStart, startSlot))
	finalEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalEnd, endSlot))
	return matched, finalStart, finalEnd, matchDataReg
}

// emitRegexSearch implements `str.search(regexp): number`, replacing the
// existing pure-.indexOf() delegation (emit_strings.go's
// emitStringSearch) once the argument is confirmed a RegExp. Matches real
// JS's own @@search algorithm precisely: always searches from offset 0
// regardless of the regex's `global` flag or current `lastIndex`, and
// restores whatever `lastIndex` held beforehand — a `.search()` call is
// invisible to any later `.exec()`/`.test()` iteration. Returns -1 on no
// match, matching real JS (and, conveniently, exactly the sentinel
// emitRegexSingleMatchCore's own matchStart already uses for "no match" —
// no separate select/branch needed here at all).
func (e *Emitter) emitRegexSearch(strVal, regexVal Value) Value {
	originalLastIndex := e.emitRegexLoadField(regexVal, "lastIndex", "i64", 8)
	e.emitRegexStoreLastIndex(regexVal, "0")
	_, startReg, _ := e.emitRegexSingleMatchCore(regexVal, strVal)
	e.emitRegexStoreLastIndex(regexVal, originalLastIndex)
	// startReg is a byte offset (or -1 for no match); report it in the mode's
	// index space — UTF-16 code units for es-utf16, passing -1 through.
	// The search index is a Number in JS (TDD-00123 Stage 3).
	return e.countToNumber(Value{Ref: e.regexByteToUTF16Signed(strVal.Ref, startReg), Ty: TypeI64})
}

// emitRegexSplitScan runs str.split()'s own local search loop once,
// calling onSegment(gapStartReg, gapEndReg) for each split point — the
// boundaries of the *text between* the previous split point and this
// match, not the match itself. An empty match splits as @@split does:
// unless it ends where the previous piece ended, or at the end. Stateless with respect
// to the RegExp instance (emitRegexMatchAt touches no object state at
// all), so calling this twice (a count pass, then a build pass) is safe —
// the same established two-pass convention every other multi-match stage
// already uses. Returns the final "last split point" position so the
// caller can handle the trailing segment (from there to the end of the
// subject) uniformly, the same way every real match loop's tail segment
// works.
func (e *Emitter) emitRegexSplitScan(handleReg string, strVal Value, subjectLenReg string, onSegment func(gapStartReg, gapEndReg, matchDataReg string)) (finalLastSplitReg string) {
	searchAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", searchAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", searchAlloca))
	lastSplitAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", lastSplitAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", lastSplitAlloca))

	condL := e.freshLabel("regex.split.cond")
	bodyL := e.freshLabel("regex.split.body")
	doneL := e.freshLabel("regex.split.done")
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(condL)
	searchPos := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", searchPos, searchAlloca))
	tooFar := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sgt i64 %s, %s", tooFar, searchPos, subjectLenReg))
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", tooFar, doneL, bodyL))

	e.emitLabel(bodyL)
	matched, mStart, mEnd, md := e.emitRegexMatchAtData(handleReg, strVal, searchPos)
	noMatchL := e.freshLabel("regex.split.nomatch")
	checkZeroL := e.freshLabel("regex.split.checkzero")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", matched, checkZeroL, noMatchL))

	e.emitLabel(noMatchL)
	e.emitInstr(fmt.Sprintf("call void @pcre2_match_data_free_8(ptr %s)", md))
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(checkZeroL)
	isZero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", isZero, mStart, mEnd))
	zeroL := e.freshLabel("regex.split.zerolen")
	realL := e.freshLabel("regex.split.real")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isZero, zeroL, realL))

	e.emitLabel(zeroL)
	// An empty match splits too (@@split), unless it ends where the last
	// piece ended or sits at the end of the string.
	zeroLast := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", zeroLast, lastSplitAlloca))
	atLast := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", atLast, mStart, zeroLast))
	atEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp sge i64 %s, %s", atEnd, mStart, subjectLenReg))
	noSplit := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = or i1 %s, %s", noSplit, atLast, atEnd))
	zeroSplitL := e.freshLabel("regex.split.zerosplit")
	zeroAdvL := e.freshLabel("regex.split.zeroadv")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", noSplit, zeroAdvL, zeroSplitL))
	e.emitLabel(zeroSplitL)
	onSegment(zeroLast, mStart, md)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", mStart, lastSplitAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", zeroAdvL))
	e.emitLabel(zeroAdvL)
	e.emitInstr(fmt.Sprintf("call void @pcre2_match_data_free_8(ptr %s)", md))
	// Step past the zero-length match by a whole code point in the UTF-matching
	// modes (a mid-code-point start offset makes PCRE2_UTF reject the next
	// match and truncate the scan early), a single byte in the raw-byte modes.
	zeroAdvWidth := "1"
	if e.regexModeOpts().utfMatching {
		e.ensureRegexUTF8Width()
		w := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @__kml_regex_utf8_width(ptr %s, i64 %s)", w, strVal.Ref, mStart))
		zeroAdvWidth = w
	}
	advanced := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", advanced, mStart, zeroAdvWidth))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", advanced, searchAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(realL)
	lastSplit := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lastSplit, lastSplitAlloca))
	onSegment(lastSplit, mStart, md)
	e.emitInstr(fmt.Sprintf("call void @pcre2_match_data_free_8(ptr %s)", md))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", mEnd, lastSplitAlloca))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", mEnd, searchAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	finalLastSplit := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalLastSplit, lastSplitAlloca))
	return finalLastSplit
}

// emitRegexSplit implements `str.split(regexp): string[]` (TDD-00035
// Stage 5). The separator's capture groups follow each piece they end. A
// regex that never matches returns a single-element array containing
// the whole subject, matching real JS.
func (e *Emitter) emitRegexSplit(strVal, regexVal Value) Value {
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()

	handleReg := e.emitRegexHandleLoad(regexVal)
	subjectLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", subjectLen, strVal.Ref))

	// Each split point also contributes the separator's capture groups
	// (@@split), an unmatched one as undefined.
	capSlot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i32, align 4", capSlot))
	e.emitInstr(fmt.Sprintf("call i32 @pcre2_pattern_info_8(ptr %s, i32 %d, ptr %s)", handleReg, pcre2InfoCaptureCount, capSlot))
	cap32 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i32, ptr %s, align 4", cap32, capSlot))
	nCap := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = zext i32 %s to i64", nCap, cap32))
	perSplit := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", perSplit, nCap))

	// --- pass 1: count the split points ---
	countAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", countAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", countAlloca))
	e.emitRegexSplitScan(handleReg, strVal, subjectLen, func(gapStartReg, gapEndReg, _ string) {
		cur := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, countAlloca))
		next := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, %s", next, cur, perSplit))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", next, countAlloca))
	})
	matchCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", matchCount, countAlloca))
	totalCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", totalCount, matchCount)) // + trailing segment

	// --- pass 2: build the result array ---
	byteCount := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 8", byteCount, totalCount))
	data := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", data, byteCount))

	idxAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", idxAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", idxAlloca))

	var storeSegment func(startReg, endReg string)
	storePtr := func(p string) {
		curIdx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curIdx, idxAlloca))
		dstGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", dstGep, data, curIdx))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", p, dstGep))
		nextIdx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nextIdx, curIdx))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nextIdx, idxAlloca))
	}
	// storeCaptures appends groups 1..nCap of a split point's match.
	storeCaptures := func(md string) {
		ov := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @pcre2_get_ovector_pointer_8(ptr %s)", ov, md))
		gi := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", gi))
		e.emitInstr(fmt.Sprintf("store i64 1, ptr %s, align 8", gi))
		condL, bodyL, setL, unsetL, nextL, doneL := e.freshLabel("split.cap.cond"), e.freshLabel("split.cap.body"), e.freshLabel("split.cap.set"), e.freshLabel("split.cap.unset"), e.freshLabel("split.cap.next"), e.freshLabel("split.cap.done")
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(condL)
		g := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", g, gi))
		over := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp ugt i64 %s, %s", over, g, nCap))
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", over, doneL, bodyL))
		e.emitLabel(bodyL)
		i0, i1 := e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = mul i64 %s, 2", i0, g))
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", i1, i0))
		p0, p1, s, en := e.freshReg(), e.freshReg(), e.freshReg(), e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", p0, ov, i0))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", s, p0))
		e.emitInstr(fmt.Sprintf("%s = getelementptr i64, ptr %s, i64 %s", p1, ov, i1))
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", en, p1))
		unset := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, -1", unset, s)) // PCRE2_UNSET
		e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", unset, unsetL, setL))
		e.emitLabel(setL)
		storeSegment(s, en)
		e.emitTerminator(fmt.Sprintf("br label %%%s", nextL))
		e.emitLabel(unsetL)
		storePtr("null") // undefined
		e.emitTerminator(fmt.Sprintf("br label %%%s", nextL))
		e.emitLabel(nextL)
		gn := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", gn, g))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", gn, gi))
		e.emitTerminator(fmt.Sprintf("br label %%%s", condL))
		e.emitLabel(doneL)
	}

	storeSegment = func(startReg, endReg string) {
		segLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", segLen, endReg, startReg))
		buf := e.emitStringAlloc(segLen) // TDD-00120: length-prefixed
		srcPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", srcPtr, strVal.Ref, startReg))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", buf, srcPtr, segLen))
		termGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", termGep, buf, segLen))
		e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", termGep))
		storePtr(buf)
	}

	finalLastSplit := e.emitRegexSplitScan(handleReg, strVal, subjectLen, func(gs, ge, md string) {
		storeSegment(gs, ge)
		storeCaptures(md)
	})
	storeSegment(finalLastSplit, subjectLen) // trailing segment

	// The empty string splits into no pieces when the separator matches it.
	matchedEmpty, _, _ := e.emitRegexMatchAt(handleReg, strVal, "0")
	isEmpty := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, 0", isEmpty, subjectLen))
	noPieces := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = and i1 %s, %s", noPieces, isEmpty, matchedEmpty))
	length := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = select i1 %s, i64 0, i64 %s", length, noPieces, totalCount))

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, data))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, length))
	return Value{Ref: r1, Ty: ArrayOf(TypePtr)}
}
