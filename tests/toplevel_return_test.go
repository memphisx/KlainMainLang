package tests

import (
	"strings"
	"testing"
)

// A `return` at a CommonJS module's top level ends the module's own
// evaluation; the process carries on (its pending timer still fires), as in
// Node. The returned value is evaluated and discarded.
func TestE2ETopLevelReturnEndsModule(t *testing.T) {
	assertJSFileSameAsNode(t, `
console.log('start');
setTimeout(() => console.log('timer'), 1);
function note(s) { console.log(s); return s.length; }
if (process.argv.length > 0) {
  return note('returning');
}
console.log('never');
`)
}

// TypeScript rejects a module-level return (TS1108).
func TestE2ETopLevelReturnStrictIsError(t *testing.T) {
	_, err := parseAndCompile(`
console.log('a')
if (process.argv.length > 0) {
  return
}
`)
	if err == nil || !strings.Contains(err.Error(), "A 'return' statement can only be used within a function body.") {
		t.Fatalf("want TS1108, got %v", err)
	}
}

// Code spliced in from a constant eval string is plain JavaScript that
// tsc never sees: its comma operands are not TS2695.
func TestE2EStaticEvalSkipsSyntacticChecks(t *testing.T) {
	if _, err := parseAndCompile(`console.log(eval("false,\ttrue"))`); err != nil {
		t.Fatalf("eval code rejected: %v", err)
	}
}
