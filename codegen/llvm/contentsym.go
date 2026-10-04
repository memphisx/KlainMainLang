package llvm

import (
	"fmt"
	"hash/fnv"

	"KlainMainLang/ast"
)

// contentSymbol is prefix followed by a hash of content: a symbol for a
// generated constant or helper that is spelled the same in every unit that
// generates the same one (TDD-00238 Stage 4).
func contentSymbol(prefix, content string) string {
	h := fnv.New64a()
	h.Write([]byte(content))
	return fmt.Sprintf("%s%016x", prefix, h.Sum64())
}

// defineContentNamed writes a generated helper, named by what it is
// (prefix plus a hash of its signature and body) and defined linkonce_odr,
// so every unit that generates the same helper links to one copy. A helper
// already written is not written again. It returns the helper's symbol.
func (e *Emitter) defineContentNamed(prefix, retIR, params, body string) string {
	name := contentSymbol(prefix, retIR+"("+params+")\n"+body)
	if e.contentDefined == nil {
		e.contentDefined = map[string]bool{}
	}
	if !e.contentDefined[name] {
		e.contentDefined[name] = true
		e.functions.WriteString(fmt.Sprintf("\ndefine linkonce_odr hidden %s %s(%s) {\nentry:\n%s}\n", retIR, name, params, body))
	}
	return name
}

// closureSymbol names the function of a closure literal at pos. In a
// builtin module it is named after the module and the position, so the
// module's code names it the same in every program; a literal emitted more
// than once (a generic instantiation) numbers the later copies.
func (e *Emitter) closureSymbol(pos ast.Pos) string {
	if e.inLib == "" {
		name := fmt.Sprintf("@__closure_%d", e.closureCtr)
		e.closureCtr++
		return name
	}
	return e.literalSymbol("@__closure", pos)
}

// literalSymbol is prefix.<module>.<line>_<col>: the function of a literal
// at pos in the builtin module being emitted (with the instantiation's type
// arguments inside a generic one), numbered past the first copy.
// Outside the library it is prefix_<n>.
func (e *Emitter) literalSymbol(prefix string, pos ast.Pos) string {
	if e.inLib == "" {
		e.dynFnCtr++
		return fmt.Sprintf("%s_%d", prefix, e.dynFnCtr-1)
	}
	name := fmt.Sprintf("%s.%s.%d_%d", prefix, e.inLib, pos.Line, pos.Col)
	if e.genericInst != "" {
		// A literal in a generic instantiation: one copy per instantiation.
		name = fmt.Sprintf("%s.%s.%s.%d_%d", prefix, e.inLib, llvmSafeSymbol(e.genericInst), pos.Line, pos.Col)
	}
	if e.libClosures == nil {
		e.libClosures = map[string]int{}
	}
	n := e.libClosures[name]
	e.libClosures[name]++
	if n > 0 {
		name += fmt.Sprintf(".%d", n)
	}
	return name
}

// contentNamedHelper is the generated helper memo[key], written on first
// use by emit (in a detached function whose parameters are params) and
// named by its content (defineContentNamed). A helper whose emission
// reaches itself again, directly or through another helper, cannot be
// named by a body that names it; it keeps an internal name.
func (e *Emitter) contentNamedHelper(memo map[string]string, key, prefix, retIR, params string, emit func()) string {
	if fn, ok := memo[key]; ok {
		if _, pending := e.pendingHelpers[fn]; pending {
			e.pendingHelpers[fn] = true
		}
		return fn
	}
	if e.pendingHelpers == nil {
		e.pendingHelpers = map[string]bool{}
	}
	placeholder := fmt.Sprintf("%s%d", prefix, len(e.pendingHelpers))
	memo[key] = placeholder
	e.pendingHelpers[placeholder] = false
	restore := e.beginDetachedFunc()
	emit()
	body := e.allocas.String() + e.body.String()
	restore()
	if e.pendingHelpers[placeholder] {
		e.functions.WriteString(fmt.Sprintf("\ndefine internal %s %s(%s) {\nentry:\n%s}\n", retIR, placeholder, params, body))
		return placeholder
	}
	delete(e.pendingHelpers, placeholder)
	name := e.defineContentNamed(prefix, retIR, params, body)
	memo[key] = name
	return name
}
