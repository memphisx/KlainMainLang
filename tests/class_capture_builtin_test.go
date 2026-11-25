package tests

import "testing"

// A strict class declared in a function reads a captured Map local through
// a hidden field typed Map<string, number>, not any.
func TestE2EStrictClassCapturedMap(t *testing.T) {
	assertSameAsNode(t, `function g(): void {
  const m = new Map<string, number>();
  m.set("a", 1);
  class K { constructor() { console.log(m.size, m.get("a")); } }
  new K();
}
g();
`)
}
