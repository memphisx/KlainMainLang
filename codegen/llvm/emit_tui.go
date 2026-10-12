package llvm

// emit_tui.go — TDD-00150: klain:tui is lib/node/klain_tui.ts; its `@link
// tui` compiles in the painter runtime (tui.go) and the vendored Yoga engine.

// ensureTui compiles the painter runtime in.
func (e *Emitter) ensureTui() { e.usedTui = true }
