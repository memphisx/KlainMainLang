package tests

import "testing"

// A program's declaration of a builtin's name is its own binding, in every
// lane, as in tsc and Node (ADR-01333): the builtin is reached again only
// outside that binding's scope, or through globalThis.

func shadowCase(t *testing.T, src, want string) {
	t.Helper()
	t.Run("strict", func(t *testing.T) {
		assertMultiFileOutput(t, map[string]string{"main.ts": src}, "main.ts", want)
	})
	t.Run("js", func(t *testing.T) {
		assertMultiFileOutputPermissive(t, map[string]string{"main.ts": src}, "main.ts", want)
	})
}

func TestE2EShadowedGlobalTopLevelMath(t *testing.T) {
	shadowCase(t, `
const Math = { random: (): number => 42 };
console.log(Math.random());
export {}
`, "42")
}

func TestE2EShadowedGlobalProcessParam(t *testing.T) {
	shadowCase(t, `
function f(process: number): number {
    return process + 1
}
console.log(f(1), typeof process.argv)
`, "2 object")
}

func TestE2EShadowedGlobalDestructuredFetch(t *testing.T) {
	shadowCase(t, `
interface Wrapper { fetch: number }
const { fetch } = { fetch: 5 } as Wrapper
console.log(fetch + 1)
export {}
`, "6")
}

func TestE2EShadowedGlobalForOfConsole(t *testing.T) {
	shadowCase(t, `
const items: number[] = [1, 2, 3]
let sum = 0
for (const console of items) {
    sum += console
}
console.log(sum)
`, "6")
}

func TestE2EShadowedGlobalMapLocal(t *testing.T) {
	shadowCase(t, `
function f(): number {
    let Map: number = 5
    return Map + 1
}
const m = new Map<string, number>([["a", 1]])
console.log(f(), m.get("a"), new globalThis.Map([[1, 2]]).size)
`, "6 1 1")
}

func TestE2EShadowedGlobalLocalMath(t *testing.T) {
	shadowCase(t, `
function useFakeMath(): number {
    let Math = { random: (): number => 42 }
    return Math.random()
}
console.log(useFakeMath(), Math.floor(3.7))
`, "42 3")
}

func TestE2EShadowedGlobalProcessLocal(t *testing.T) {
	shadowCase(t, `
function f(): number {
    let process = { argv: (): number => 99 }
    return process.argv()
}
console.log(f(), process.argv.length >= 1)
`, "99 true")
}

func TestE2EShadowedGlobalUnshadowed(t *testing.T) {
	shadowCase(t, `
console.log(Math.floor(3.7))
console.log(parseInt("42") + 1)
`, "3\n43")
}
