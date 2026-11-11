package tests

import (
	"path/filepath"
	"testing"
)

// The shadow comparison (KML_SHADOW) types a program a second time through
// the checker. These programs once sent that second typing into a loop.

func TestE2EShadowSelfReadingLiteralMethod(t *testing.T) {
	// A literal's method reads the literal's own binding (ADR-01966).
	t.Setenv("KML_SHADOW", filepath.Join(t.TempDir(), "shadow.jsonl"))
	assertOutput(t, `
const g = () => 1;
const o = { m() { return g(); }, n: function () { return o.m(); } };
class K {
  v: number;
  constructor() { this.v = o.n(); }
}
console.log(new K().v, o.n());
`, "1 1")
}
