package tests

import "testing"

// --- A class/interface field typed as another nominal class (ADR-00219).
// Previously such a field resolved to i64 (the unknown-name default) because
// class name placeholders were registered after interfaces, so the instance
// pointer was mis-stored and field access failed. ---

func TestE2EClassTypedInterfaceField(t *testing.T) {
	assertOutput(t, `
class Point { x: number = 3; y: number = 4; }
interface Shape { origin: Point; label: string; }
const s: Shape = { origin: new Point(), label: "box" }
console.log(s.origin.x)
console.log(s.origin.y)
console.log(s.label)
`, "3\n4\nbox")
}

func TestE2EClassTypedFieldInspection(t *testing.T) {
	// The inspector renders a nested class-typed field's real fields (via
	// canonicalizeClassTy), not an empty `Point {}`.
	assertOutput(t, `
class Point { x: number = 3; y: number = 4; }
interface Shape { origin: Point; label: string; }
const s: Shape = { origin: new Point(), label: "box" }
console.log(s)
`, "{ origin: Point { x: 3, y: 4 }, label: 'box' }")
}
