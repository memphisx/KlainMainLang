package llvm

import (
	_ "embed"
	"strings"
)

//go:embed stackguardsrc/stackguard.c
var stackGuardSource string

// StackGuardSource is the stack-overflow handler's C source (TDD-00249).
func StackGuardSource() string { return stackGuardSource }

// markNoTail makes every call in the module notail (TDD-00249). LLVM's
// tail-call elimination would turn a self-call in tail position into a
// loop, and the backend would reuse the caller's frame for a sibling call,
// so unbounded recursion would never run out of stack. Node's does, and
// throws a RangeError the program can catch.
func markNoTail(ir string) string {
	ir = strings.ReplaceAll(ir, " = call ", " = notail call ")
	return strings.ReplaceAll(ir, "\n  call ", "\n  notail call ")
}
