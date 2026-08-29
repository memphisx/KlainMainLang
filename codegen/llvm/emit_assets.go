package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emit_assets.go — codegen for the klain:assets module (TDD-00142 Stage 7):
// `embedDir(literalPath)` embeds a directory into the binary at compile time and
// returns an EmbeddedAssets handle; `handle.get(path)` reads an embedded file's
// bytes (an ArrayBuffer over the static blob, no copy).

// emitAssetsModuleCall dispatches `assets__kml_builtin.<member>(...)`.
func (e *Emitter) emitAssetsModuleCall(member string, args []ast.Expression, pos ast.Pos) (Value, error) {
	switch member {
	case "embedDir":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: embedDir(path) takes one argument", pos.Line, pos.Col)
		}
		path, ok := staticStringValue(args[0])
		if !ok {
			return Value{}, fmt.Errorf("%d:%d: embedDir's path must be a string literal (it is read at compile time)", pos.Line, pos.Col)
		}
		sym := e.requireEmbed(path)
		e.ensureEmbedSymbol(sym)
		e.ensureEmbedAssetsRuntime()
		return Value{Ref: "@" + sym, Ty: EmbeddedAssetsType()}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: klain:assets has no member '%s'", pos.Line, pos.Col, member)
}

// emitEmbeddedAssetsMethod dispatches a method on an EmbeddedAssets handle.
func (e *Emitter) emitEmbeddedAssetsMethod(objExpr ast.Expression, method string, args []ast.Expression, pos ast.Pos) (Value, error) {
	blob, err := e.emitExpr(objExpr)
	if err != nil {
		return Value{}, err
	}
	switch method {
	case "get":
		if len(args) != 1 {
			return Value{}, fmt.Errorf("%d:%d: EmbeddedAssets.get(path) takes one argument", pos.Line, pos.Col)
		}
		pathV, err := e.emitExpr(args[0])
		if err != nil {
			return Value{}, err
		}
		e.ensureEmbedAssetsRuntime()
		e.ensureStrlen()
		e.ensureMalloc()
		plen := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call i64 @strlen(ptr %s)", plen, pathV.Ref))
		outLen := e.freshReg()
		e.emitAlloca(fmt.Sprintf("%s = alloca i64, align 8", outLen))
		data := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @__kml_embed_get(ptr %s, ptr %s, i64 %s, ptr %s)", data, blob.Ref, pathV.Ref, plen, outLen))
		lenLd := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = load i64, ptr %s, align 8", lenLd, outLen))
		// ArrayBuffer header { i64 len, ptr data } over the static bytes (no copy;
		// a miss yields length 0 / null data — check byteLength).
		hdr := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = call ptr @malloc(i64 16)", hdr))
		e.emitInstr(fmt.Sprintf("store i64 %s, ptr %s, align 8", lenLd, hdr))
		dataSlot := e.freshReg()
		e.emitInstr(fmt.Sprintf("%s = getelementptr { i64, ptr }, ptr %s, i32 0, i32 1", dataSlot, hdr))
		e.emitInstr(fmt.Sprintf("store ptr %s, ptr %s, align 8", data, dataSlot))
		return Value{Ref: hdr, Ty: ArrayBufferType()}, nil
	}
	return Value{}, fmt.Errorf("%d:%d: EmbeddedAssets has no method '%s'", pos.Line, pos.Col, method)
}
