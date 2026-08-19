// Intersection types A & B & ... (TDD-00078). An object-type intersection is a
// value that satisfies every member at once, which this compiler represents by
// merging the members' fields into one struct — no runtime tag (unlike a
// union, which is a runtime-tagged value that is *one of* its members). V1
// scope: every member must be an object type (named interface, inline {...}, or
// a type alias of either). A non-object member (a scalar/function/array, which
// intersects to `never`) and a field declared with conflicting types across
// members are both clean compile-time errors under the default -compat=strict.
// See docs/status/TYPE-SYSTEM.md.

// --- two named interfaces, merged into one shape ---
interface HasName { name: string }
interface HasAge { age: number }

const p: HasName & HasAge = { name: "Kyriakos", age: 34 }
console.log(p.name)          // Kyriakos
console.log(p.age)           // 34

// --- the same intersection behind a type alias ---
type Person = HasName & HasAge
const q: Person = { name: "Ada", age: 36 }
console.log(q.name + " is " + q.age)  // Ada is 36

// --- inline object types, three members, as a function parameter/return ---
interface HasId { id: number }
interface HasLabel { label: string }
interface HasQty { qty: number }

function render(item: HasId & HasLabel & HasQty): string {
	return item.label + " #" + item.id + " x" + item.qty
}
console.log(render({ id: 7, label: "widget", qty: 3 }))  // widget #7 x3

// --- an array of an intersection ---
const rows: (HasId & HasLabel)[] = [
	{ id: 1, label: "alpha" },
	{ id: 2, label: "beta" },
]
for (const r of rows) console.log(r.id + ":" + r.label)  // 1:alpha / 2:beta

// --- a nullable intersection narrows on a null check ---
const maybe: (HasName & HasAge) | null = { name: "Zoe", age: 29 }
if (maybe !== null) {
	console.log(maybe.name + "/" + maybe.age)  // Zoe/29
}

// --- `&` binds tighter than `|`, so combining shapes composes naturally with
// the config-extension pattern intersections are most used for ---
interface BaseConfig { retries: number }
interface TimeoutConfig { timeoutMs: number }

function connect(cfg: BaseConfig & TimeoutConfig): string {
	return "retries=" + cfg.retries + " timeout=" + cfg.timeoutMs
}
console.log(connect({ retries: 3, timeoutMs: 500 }))  // retries=3 timeout=500
