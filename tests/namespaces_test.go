package tests

import "testing"

// TS namespace declarations + function merging (TDD-00095/ADR-00290): the
// bare-call form and the namespace-member form coexist on one name, and a
// plain namespace's exported functions/consts resolve via `X.member`.
func TestE2ENamespaceFunctionMerging(t *testing.T) {
	assertOutput(t, `
function greet(name: string): string { return "hi " + name; }
namespace greet {
  export function loud(name: string): string { return "HI " + name; }
  export const version = 3;
}
console.log(greet("ann"));
console.log(greet.loud("bob"));
console.log(greet.version);
`, "hi ann\nHI bob\n3")
}

func TestE2ENamespacePlain(t *testing.T) {
	assertOutput(t, `
namespace util {
  export function twice(n: number): number { return n * 2; }
  export function shout(s: string): string { return s + "!"; }
}
console.log(util.twice(21));
console.log(util.shout("hey"));
`, "42\nhey!")
}

func TestE2ENamespaceMemberRequiresExport(t *testing.T) {
	_, err := parseAndCompile(`
namespace util {
  function hidden(): void {}
}
`)
	if err == nil {
		t.Fatal("expected a compile error for a non-exported namespace member")
	}
}
