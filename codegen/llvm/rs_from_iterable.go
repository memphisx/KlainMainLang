package llvm

import (
	"fmt"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
	"KlainMainLang/sema"
)

// rsFromIterableSrc is ReadableStream.from over an iterable that is no
// array: the spec's ReadableStreamFromIterable. Its iterator is the
// iterable's async one, else its sync one (each value awaited); a stream of
// high-water mark 0 pulls it one step per pull and closes it on cancel. It
// returns undefined for a value with neither (the caller throws Node's
// ERR_ARG_NOT_ITERABLE, formatted as Node formats it).
const rsFromIterableSrc = `(function (iterable) {
	if (iterable === null || iterable === undefined) throw new TypeError("Cannot read properties of " + iterable + " (reading 'Symbol(Symbol.asyncIterator)')");
	const am = iterable[Symbol.asyncIterator];
	let it;
	if (am !== undefined && am !== null) {
		it = am.call(iterable);
	} else {
		const sm = iterable[Symbol.iterator];
		if (typeof sm !== "function") return undefined;
		const sit = sm.call(iterable);
		const snext = sit.next;
		// CreateAsyncFromSyncIterator: each value awaited; closed (its
		// return called) only when the stream is cancelled at a value.
		it = (async function* () {
			let atValue = false;
			try {
				while (true) {
					const r = snext.call(sit);
					if (r.done) { await r.value; return; }
					atValue = true;
					yield r.value;
					atValue = false;
				}
			} finally {
				if (atValue) {
					const ret = sit.return;
					if (ret !== undefined && ret !== null) ret.call(sit);
				}
			}
		})();
	}
	if (it === null || (typeof it !== "object" && typeof it !== "function")) throw new TypeError("Result of the Symbol.asyncIterator method is not an object");
	// (The wrapper of a sync iterator is called as a method.)
	const next = am !== undefined && am !== null ? it.next : undefined;
	return new ReadableStream({
		async pull(c) {
			const r = await (next !== undefined ? next.call(it) : it.next());
			if (r === null || typeof r !== "object") throw new TypeError("Invalid state: The promise returned by the iterator.next() method must fulfill with an object");
			if (r.done) c.close(); else c.enqueue(r.value);
		},
		async cancel(reason) {
			if (next === undefined) {
				await it.return(reason);
				return;
			}
			const ret = it.return;
			if (ret === undefined || ret === null) return;
			const r = await ret.call(it, reason);
			if (r === null || typeof r !== "object") throw new TypeError("Invalid state: The promise returned by the iterator.return() method must fulfill with an object");
		},
	}, { highWaterMark: 0 });
})`

// emitReadableStreamFromIterable is ReadableStream.from(arg) for an arg
// that is no array.
func (e *Emitter) emitReadableStreamFromIterable(arg ast.Expression, pos ast.Pos) (Value, error) {
	src, err := e.receiverTemp(arg)
	if err != nil {
		return Value{}, err
	}
	prog, err := parser.Parse(rsFromIterableSrc)
	if err == nil {
		err = sema.Prepare(prog)
	}
	if err != nil {
		return Value{}, internalAt(pos, "ReadableStream.from runtime: %v", err)
	}
	fn, err := e.emitDynFunctionExpression(prog.Body[0].(*ast.ExpressionStatement).Expr.(*ast.FunctionExpression), pos)
	if err != nil {
		return Value{}, err
	}
	fb, err := e.emitBoxValue(fn)
	if err != nil {
		return Value{}, err
	}
	r, err := e.emitDynCallValue(fb, []ast.Expression{src}, pos)
	if err != nil {
		return Value{}, err
	}
	isU := e.freshReg()
	e.emitInstr(fmt.Sprintf("%s = icmp eq i64 %s, %d", isU, r.Ref, nbUndefined))
	badL, okL := e.freshLabel("rs.from.notiter"), e.freshLabel("rs.from.ok")
	e.emitTerminator(fmt.Sprintf("br i1 %s, label %%%s, label %%%s", isU, badL, okL))
	e.emitLabel(badL)
	shown, err := e.emitUtilFormatArg('s', src, pos)
	if err != nil {
		return Value{}, err
	}
	msg, err := e.emitStringConcat(shown, Value{Ref: e.internString(" must be iterable"), Ty: TypePtr})
	if err != nil {
		return Value{}, err
	}
	e.emitThrowCoded("TypeError", "ERR_ARG_NOT_ITERABLE", msg.Ref)
	e.emitLabel(okL)
	return r, nil
}
