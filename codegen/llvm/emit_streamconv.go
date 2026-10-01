package llvm

// emit_streamconv.go — convert(ReadableStream<T> → ReadableStream<any>)
// (TDD-00230 P3.3): a web stream whose chunks are T's representation read
// where a stream of boxed chunks is declared (`Readable.fromWeb(blob.stream())`)
// becomes a ReadableStream<any> pulling from the source's reader, each chunk
// boxed as it is enqueued — the stream analogue of emitConvertPromise.

import (
	"fmt"

	"KlainMainLang/ast"
	"KlainMainLang/parser"
	"KlainMainLang/sema"
)

// streamChunksNeedConvert reports a typed web stream read as a stream of
// `any` chunks.
func streamChunksNeedConvert(a, b Type) bool {
	if !a.IsReadableStream || !b.IsReadableStream || a.StreamChunk == nil || b.StreamChunk == nil {
		return false
	}
	c := *a.StreamChunk
	return !isUnconstrainedDynamic(c) && isUnconstrainedDynamic(*b.StreamChunk) && c.IR != "" && c.IR != "void" && !c.IsNever
}

// emitConvertStreamToAny is the ReadableStream<any> over v's reader. v is
// returned unchanged if the conversion cannot be built.
func (e *Emitter) emitConvertStreamToAny(v Value, target Type) Value {
	src := "__kml_rsconv_" + e.freshReg()[1:]
	slot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca ptr, align 8", slot))
	e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", v.Ref, slot))
	e.define(src, Symbol{Ptr: slot, Ty: v.Ty})
	rd := src + "_rd"
	prog, err := parser.Parse(fmt.Sprintf(`%[2]s.getReader();
new ReadableStream<any>({
  async pull(c) { const r = await %[1]s.read(); if (r.done) c.close(); else c.enqueue(r.value); },
  cancel() { %[1]s.cancel(); },
});`, rd, src))
	if err != nil || len(prog.Body) != 2 || sema.Prepare(prog) != nil {
		return v
	}
	getReader, ok1 := prog.Body[0].(*ast.ExpressionStatement)
	es, ok2 := prog.Body[1].(*ast.ExpressionStatement)
	if !ok1 || !ok2 {
		return v
	}
	reader, err := e.emitExpr(getReader.Expr)
	if err != nil {
		return v
	}
	rslot := e.freshReg()
	e.emitAlloca(fmt.Sprintf("%s = alloca %s, align 8", rslot, reader.Ty.IR))
	e.emitInstr(fmt.Sprintf("store %s %s, ptr %s, align 8", reader.Ty.IR, reader.Ref, rslot))
	e.define(rd, Symbol{Ptr: rslot, Ty: reader.Ty})
	out, err := e.emitExpr(es.Expr)
	if err != nil || !out.Ty.IsReadableStream {
		return v
	}
	return Value{Ref: out.Ref, Ty: target}
}
