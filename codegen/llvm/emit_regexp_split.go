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
	e.ensureRegexMatch()
	matchDataReg := e.freshReg()
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
	e.emitInstr(fmt.Sprintf("call void @pcre2_match_data_free_8(ptr %s)", matchDataReg))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(nomatchL)
	e.emitInstr(fmt.Sprintf("call void @pcre2_match_data_free_8(ptr %s)", matchDataReg))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", startSlot))
	e.emitInstr(fmt.Sprintf("store i64 -1, ptr %s, align 8", endSlot))
	e.emitTerminator(fmt.Sprintf("br label %%%s", mergeL))

	e.emitLabel(mergeL)
	finalStart := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalStart, startSlot))
	finalEnd := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalEnd, endSlot))
	return matched, finalStart, finalEnd
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
	return Value{Ref: e.regexByteToUTF16Signed(strVal.Ref, startReg), Ty: TypeI64}
}

// emitRegexSplitScan runs str.split()'s own local search loop once,
// calling onSegment(gapStartReg, gapEndReg) for each real (non-zero-
// length) match found — the boundaries of the *text between* the previous
// split point and this match, not the match itself. Zero-length matches
// are skipped entirely (never produce a split) rather than replicating
// real JS's own more intricate zero-length-match handling — a documented
// V1 narrowing (see emitRegexSplit's doc comment). Stateless with respect
// to the RegExp instance (emitRegexMatchAt touches no object state at
// all), so calling this twice (a count pass, then a build pass) is safe —
// the same established two-pass convention every other multi-match stage
// already uses. Returns the final "last split point" position so the
// caller can handle the trailing segment (from there to the end of the
// subject) uniformly, the same way every real match loop's tail segment
// works.
func (e *Emitter) emitRegexSplitScan(handleReg string, strVal Value, subjectLenReg string, onSegment func(gapStartReg, gapEndReg string)) (finalLastSplitReg string) {
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
	matched, mStart, mEnd := e.emitRegexMatchAt(handleReg, strVal, searchPos)
	noMatchL := e.freshLabel("regex.split.nomatch")
	checkZeroL := e.freshLabel("regex.split.checkzero")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", matched, checkZeroL, noMatchL))

	e.emitLabel(noMatchL)
	e.emitTerminator(fmt.Sprintf("br label %%%s", doneL))

	e.emitLabel(checkZeroL)
	isZero := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %s", isZero, mStart, mEnd))
	zeroL := e.freshLabel("regex.split.zerolen")
	realL := e.freshLabel("regex.split.real")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isZero, zeroL, realL))

	e.emitLabel(zeroL)
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
	onSegment(lastSplit, mStart)
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", mEnd, lastSplitAlloca))
	e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", mEnd, searchAlloca))
	e.emitTerminator(fmt.Sprintf("br label %%%s", condL))

	e.emitLabel(doneL)
	finalLastSplit := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", finalLastSplit, lastSplitAlloca))
	return finalLastSplit
}

// emitRegexSplit implements `str.split(regexp): string[]` (TDD-00035
// Stage 5). Scope, beyond the already-decided "no capture-group splicing
// into the result" narrowing: only non-zero-length matches produce a
// split (see emitRegexSplitScan) — real JS does split on some zero-length
// matches (e.g. a lookahead-only pattern), a genuine, documented V1
// divergence traded for materially simpler loop logic, matching this
// stage's "smallest remaining surface" framing in docs/tdd/TDD-00035.md.
// A regex that never matches returns a single-element array containing
// the whole subject, matching real JS.
func (e *Emitter) emitRegexSplit(strVal, regexVal Value) Value {
	e.ensureStrlen()
	e.ensureMalloc()
	e.ensureMemcpy()

	handleReg := e.emitRegexHandleLoad(regexVal)
	subjectLen := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", subjectLen, strVal.Ref))

	// --- pass 1: count real (non-zero-length) matches ---
	countAlloca := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", countAlloca))
	e.emitInstr(fmt.Sprintf("store i64 0, ptr %s, align 8", countAlloca))
	e.emitRegexSplitScan(handleReg, strVal, subjectLen, func(gapStartReg, gapEndReg string) {
		cur := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", cur, countAlloca))
		next := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", next, cur))
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

	storeSegment := func(startReg, endReg string) {
		segLen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = sub i64 %s, %s", segLen, endReg, startReg))
		segLenPlus1 := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", segLenPlus1, segLen))
		buf := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 %s)", buf, segLenPlus1))
		srcPtr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", srcPtr, strVal.Ref, startReg))
		e.emitInstr(fmt.Sprintf("call ptr @memcpy(ptr %s, ptr %s, i64 %s)", buf, srcPtr, segLen))
		termGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr i8, ptr %s, i64 %s", termGep, buf, segLen))
		e.emitInstr(fmt.Sprintf("store i8 0, ptr %s, align 1", termGep))

		curIdx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", curIdx, idxAlloca))
		dstGep := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr ptr, ptr %s, i64 %s", dstGep, data, curIdx))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", buf, dstGep))
		nextIdx := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = add i64 %s, 1", nextIdx, curIdx))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", nextIdx, idxAlloca))
	}

	finalLastSplit := e.emitRegexSplitScan(handleReg, strVal, subjectLen, storeSegment)
	storeSegment(finalLastSplit, subjectLen) // trailing segment

	r0 := e.freshReg()
	r1 := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} undef, ptr %s, 0", r0, data))
	e.emitInstr(fmt.Sprintf("%s = insertvalue {ptr, i64} %s, i64 %s, 1", r1, r0, totalCount))
	return Value{Ref: r1, Ty: ArrayOf(TypePtr)}
}
