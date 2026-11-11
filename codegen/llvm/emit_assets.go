package llvm

import (
	"fmt"

	"KlainMainLang/ast"
)

// emit_assets.go — klain:assets (TDD-00142 Stage 7) is lib/node/klain_assets.ts.
// `embedDir(literalPath)` is its one intrinsic: the directory is read at compile
// time (EmbeddedBlobs packs it), so the call site links the blob, hands it to
// the runtime and makes the module's EmbeddedAssets over it.

func init() {
	intrinsics["klain:assets.embedDir"] = intrinsic{
		emit: (*Emitter).emitEmbedDir,
		ty: func(e *Emitter, ex *ast.CallExpression) Type {
			if m, ok := e.libExports["klain_assets:_kmlEmbedded"]; ok {
				return e.typeOfExpr(ast.NewCallExpression(ast.NewIdentifier(m, ex.GetPos()), nil, ex.GetPos()))
			}
			return TypePtr
		},
	}
}

// emitEmbedDir links the literal directory's blob and returns its
// EmbeddedAssets.
func (e *Emitter) emitEmbedDir(ex *ast.CallExpression) (Value, error) {
	pos := ex.GetPos()
	if len(ex.Args) != 1 {
		return Value{}, internalAt(pos, "embedDir arity")
	}
	path, ok := staticStringValue(ex.Args[0])
	if !ok {
		return Value{}, internalAt(pos, "embedDir path")
	}
	m, ok := e.libExports["klain_assets:_kmlEmbedded"]
	if !ok {
		return Value{}, internalAt(pos, "klain:assets is not linked")
	}
	sym := e.requireEmbed(path)
	e.ensureEmbedSymbol(sym)
	e.ensureAssetsNatives()
	e.declareFn("__kml_embed_push", "declare void @__kml_embed_push("+irPtr+")")
	e.emitInstr(fmt.Sprintf("call void @__kml_embed_push("+irPtr+" @%s)", sym))
	return e.emitExpr(ast.NewCallExpression(ast.NewIdentifier(m, pos), nil, pos))
}

// ensureAssetsNatives links the embedded-asset runtime: the `@link assets`
// of lib/native.d.ts.
func (e *Emitter) ensureAssetsNatives() {
	e.usedEmbeddedAssets = true
}
