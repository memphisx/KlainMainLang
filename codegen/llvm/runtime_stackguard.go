package llvm

import (
	_ "embed"
	"strings"
)

//go:embed stackguardsrc/stackguard.c
var stackGuardSource string

// StackGuardSource is the stack-overflow handler's C source (TDD-00249).
func StackGuardSource() string { return stackGuardSource }

// noTailWriter writes IR text into b with every call marked `notail`
// (TDD-00249) as it is written. LLVM's tail-call elimination would turn a
// self-call in tail position into a loop, and the backend would reuse the
// caller's frame for a sibling call, so unbounded recursion would never run
// out of stack. Node's does, and throws a RangeError the program can catch.
//
// nl records whether the text so far ends a line, so a write that starts
// with an instruction line is matched as it would be inside the whole
// module.
type noTailWriter struct {
	b  *strings.Builder
	nl bool
}

func (w *noTailWriter) WriteString(s string) (int, error) {
	n := len(s)
	if s == "" {
		return 0, nil
	}
	if w.nl && strings.HasPrefix(s, "  call ") {
		w.b.WriteString("  notail call ")
		s = s[len("  call "):]
	}
	writeNoTail(w.b, s)
	w.nl = s != "" && s[len(s)-1] == '\n' || s == "" && w.nl
	return n, nil
}

func (w *noTailWriter) Write(p []byte) (int, error) { return w.WriteString(string(p)) }

// writeNoTail writes s into b with `notail` before each call that follows
// ` = ` or a line's two-space indent (a strings.Replacer of the two forms
// does the same, several times slower over a module's text).
func writeNoTail(b *strings.Builder, s string) {
	last := 0
	for off := 0; ; {
		i := strings.Index(s[off:], "call ")
		if i < 0 {
			break
		}
		i += off
		off = i + len("call ")
		if i >= 3 && (s[i-3:i] == " = " || s[i-3:i] == "\n  ") {
			b.WriteString(s[last:i])
			b.WriteString("notail ")
			last = i
		}
	}
	b.WriteString(s[last:])
}
