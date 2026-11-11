package llvm

// runtime_assets.go — extern declarations for the embedded-assets static server
// (TDD-00142 Stage 7). The implementations live in embedassetssrc/embed_assets.c
// (linked via EmbeddedCSources when UsesEmbeddedAssets); this only declares the
// ABI the emitted IR calls, and the per-blob external symbol references.

// ensureEmbedSymbol emits an `external global` declaration for a packed blob's
// linked symbol exactly once, so the IR can take its address.
func (e *Emitter) ensureEmbedSymbol(sym string) {
	if e.embedSymbols == nil {
		e.embedSymbols = map[string]bool{}
	}
	if e.embedSymbols[sym] {
		return
	}
	e.embedSymbols[sym] = true
	e.emitGlobal("@" + sym + " = external" + irGlobalAS + " global i8")
}
