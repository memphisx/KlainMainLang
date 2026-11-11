package tests

import (
	"strings"
	"testing"
)

// An array is not assignable where TypeScript rejects it (each was once
// accepted, then compiled to invalid IR).
func TestE2EArrayRelationsRejected(t *testing.T) {
	cases := []struct{ name, src, want string }{
		{"string index signature", `
interface H { [k: string]: string | number | string[] | undefined }
const h: H = ["a", "b"]
console.log(h)
`, "is not assignable to type 'H'"},
		{"Iterable element", `
const seq: number[] = [1]
function f(x: Iterable<number>) { return 1 }
console.log(f([seq]))
`, "is not assignable"},
		{"union of object types", `
const seq: number[] = [1]
function f(x: ArrayLike<number> | ArrayBuffer) { return 1 }
console.log(f([seq]))
`, "is not assignable"},
		{"constrained overload", `
const seq: number[] = [0xC0]
const data = new Uint8Array([seq])
console.log(data)
`, "no overload matches this call"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseAndCompile(c.src)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want %q, got %v", c.want, err)
			}
		})
	}
}

// The forms TypeScript accepts still compile and run as Node does.
func TestE2EArrayRelationsAccepted(t *testing.T) {
	assertSameAsNode(t, `
const a = new Uint8Array([1, 2, 3])
const b = new Float64Array(4)
const c = new Uint8Array(a.buffer, 0, 2)
const d = new Int16Array(a)
function f(x: Iterable<number>) { let n = 0; for (const v of x) n += v; return n }
const h: { [k: string]: number } = { a: 1 }
console.log(a, b.length, c, d, f([1, 2, 3]), h)
`)
}

// `// @ts-ignore` and `// @ts-expect-error` cover the next code line's type
// errors, as tsc's do; an expect-error over none is TS2578.
func TestE2ETsCommentDirectives(t *testing.T) {
	assertSameAsNode(t, `
function f(a: number) { return a * 2; }
// @ts-expect-error
console.log(f(1, 2));
// @ts-ignore

// a note between
console.log(f(3, 4, 5));
`)
	_, err := parseAndCompile(`
// @ts-expect-error
const fine = 2;
console.log(fine);
`)
	if err == nil || !strings.Contains(err.Error(), "Unused '@ts-expect-error' directive") {
		t.Fatalf("want TS2578, got %v", err)
	}
}
