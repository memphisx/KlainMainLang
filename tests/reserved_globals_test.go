package tests

import (
	"strings"
	"testing"
)

// --- TDD-00050: reserved ambient-global names ---
//
// Real browsers/Node genuinely let a program do `const Math = {}`. This
// compiler's default (`-compat=strict`) is deliberately stricter: a
// binding colliding with an ambient global name is a compile error,
// closing the same class of silent-miscompile bug TDD-00049 fixed for
// import-gated built-ins, but for the names that stay ambient by design
// (Math/JSON/console/process/... — Category A/C in TDD-00049's own split).
// `-compat=js` opts back into real JS/browser semantics for Tier 1
// (plain-identifier globals); Tier 2 (Map/Date/RegExp/... — parser-level
// `new`-form built-ins) stays reserved either way, see ADR-00143.

func TestE2EReservedGlobalStrictRejectsTopLevelMath(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"main.ts": `
const Math = { random: (): number => 42 };
console.log(Math.random());
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for redeclaring 'Math', got none")
	}
	if !strings.Contains(err.Error(), "Math") || !strings.Contains(err.Error(), "-compat=js") {
		t.Fatalf("expected the error to mention 'Math' and the -compat=js escape hatch, got: %v", err)
	}
}

func TestE2EReservedGlobalStrictRejectsLocalProcess(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"main.ts": `
function f(process: number): number {
    return process + 1
}
console.log(f(1))
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for a parameter named 'process', got none")
	}
	if !strings.Contains(err.Error(), "process") {
		t.Fatalf("expected the error to mention 'process', got: %v", err)
	}
}

func TestE2EReservedGlobalStrictRejectsDestructuredName(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"main.ts": `
interface Wrapper { fetch: number }
const { fetch } = { fetch: 5 } as Wrapper
console.log(fetch)
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for destructuring a binding named 'fetch', got none")
	}
	if !strings.Contains(err.Error(), "fetch") {
		t.Fatalf("expected the error to mention 'fetch', got: %v", err)
	}
}

func TestE2EReservedGlobalStrictRejectsForOfVariable(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"main.ts": `
const items: number[] = [1, 2, 3]
for (const console of items) {
    console.log(console)
}
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for a for...of variable named 'console', got none")
	}
}

func TestE2EReservedGlobalStrictAllowsNormalUsage(t *testing.T) {
	// The default mode must not restrict ordinary, unshadowed use of these
	// names — only declaring a colliding binding is rejected.
	assertMultiFileOutput(t, map[string]string{
		"main.ts": `
console.log(Math.floor(3.7))
console.log(parseInt("42") + 1)
`,
	}, "main.ts", "3\n43")
}

func TestE2EReservedGlobalTier2AlwaysRejectedEvenPermissive(t *testing.T) {
	for _, mode := range []string{"strict", "permissive"} {
		t.Run(mode, func(t *testing.T) {
			var err error
			if mode == "strict" {
				_, err = resolveMultiFile(t, map[string]string{
					"main.ts": `
let Map: number = 5
console.log(Map)
`,
				}, "main.ts")
			} else {
				_, err = resolveMultiFilePermissive(t, map[string]string{
					"main.ts": `
let Map: number = 5
console.log(Map)
`,
				}, "main.ts")
			}
			if err == nil {
				t.Fatalf("[%s] expected a compile error for redeclaring 'Map' (Tier 2), got none", mode)
			}
			if !strings.Contains(err.Error(), "Map") || !strings.Contains(err.Error(), "new Map") {
				t.Fatalf("[%s] expected the error to explain the new-form limitation, got: %v", mode, err)
			}
		})
	}
}

func TestE2EReservedGlobalPermissiveAllowsTopLevelMathShadow(t *testing.T) {
	assertMultiFileOutputPermissive(t, map[string]string{
		"main.ts": `
const Math = { random: (): number => 42 };
console.log(Math.random());
`,
	}, "main.ts", "42")
}

// TestE2EReservedGlobalPermissiveAllowsLocalMathShadow is the direct
// regression test for the gap TDD-00050 closed: a *function-local* shadow
// (never protected by TDD-00041's incidental top-level-mangling coincidence)
// must correctly call the user's own value under -compat=js,
// exactly matching real JS/browser lexical scoping.
func TestE2EReservedGlobalPermissiveAllowsLocalMathShadow(t *testing.T) {
	assertMultiFileOutputPermissive(t, map[string]string{
		"main.ts": `
function useFakeMath(): number {
    let Math = { random: (): number => 42 }
    return Math.random()
}
console.log(useFakeMath())
`,
	}, "main.ts", "42")
}

func TestE2EReservedGlobalPermissiveAllowsProcessShadow(t *testing.T) {
	assertMultiFileOutputPermissive(t, map[string]string{
		"main.ts": `
function f(): number {
    let process = { argv: (): number => 99 }
    return process.argv()
}
console.log(f())
`,
	}, "main.ts", "99")
}

func TestE2EReservedGlobalPermissiveUnshadowedStillWorks(t *testing.T) {
	// Under permissive mode, a program that never actually shadows anything
	// must behave identically to strict mode.
	assertMultiFileOutputPermissive(t, map[string]string{
		"main.ts": `
console.log(Math.floor(3.7))
console.log(process.argv.length >= 1)
`,
	}, "main.ts", "3\ntrue")
}
