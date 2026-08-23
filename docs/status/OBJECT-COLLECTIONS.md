# Object / Collections

> Part of the [Implementation Status](README.md) index.

**Coverage**: 25/28 (~89%) · **Strict Coverage**: 17/28 (~61%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| Object literals `{ a: 1 }` | ✅ | | |
| Field access `obj.field` | ✅ | | |
| Object destructuring | ✅ | | |
| `Object.keys(obj)` | ✅ | | |
| `Object.values(obj)` | ✅ | | |
| `Object.entries(obj)` | ✅ | • Values are stringified — a heterogeneous object's value type is a union not yet representable ([TDD-00066](../tdd/TDD-00066.md)) | • Returns a real `[string, string][]` tuple array; destructure with `for (const [k, v] of Object.entries(obj))` |
| `Object.groupBy(arr, fn)` | ✅ | | |
| `Object.assign(target, ...src)` | ✅ | • Every field a source contributes must already exist on `target`'s struct type — a source field `target`'s type doesn't have is a clean compile error (fixed-shape heap structs), not grafted on as in real JS ([ADR-00054](../adr/ADR-00054.md)) | • Mutates and returns `target` |
| `Object.create()` | ❌ | | • Not implemented |
| `Object.freeze(obj)` | ✅ | | • Real runtime enforcement, not a no-op: tracks `obj`'s heap pointer in a global frozen-object set, checked at every field-write site, so a blocked write throws a catchable Error even through a different alias/function parameter ([ADR-00055](../adr/ADR-00055.md)) |
| `Object.seal(obj)` | ✅ | | • A genuine no-op, not a scope-narrowed approximation: seal's guarantee ("no new/deleted fields") is already structurally impossible for this compiler's fixed-shape objects, so there's nothing further to enforce ([ADR-00055](../adr/ADR-00055.md)) |
| `Object.hasOwn()` / `.hasOwnProperty()` | ✅ | • The key must be a string literal — a runtime-computed key is a clean compile error (no runtime field-name table to check it against) ([ADR-00065](../adr/ADR-00065.md)) | • Object shapes are fully structural/static, so this is a compile-time `FieldIndex` lookup, not a runtime scan |
| `Object.fromEntries()` | ❌ | | • Not implemented |
| Object spread `{ ...obj, key: val }` | ✅ | | |
| Computed property keys `{ [expr]: value }` | ✅ | • `V` is inferred from the first property only<br>• `...spread` combined with a computed key isn't supported yet<br>• The declared-type form (`{ [key: string]: T }`) isn't supported yet | • A *dynamic object* — storage-wise a real `Map<string,V>` reusing `new Map<K,V>()`'s runtime, with `.field`/`[expr]` sugar layered on top ([TDD-00012](../tdd/TDD-00012.md) / [ADR-00066](../adr/ADR-00066.md))<br>• Exception: a well-known-symbol key (`[Symbol.asyncIterator]`/`[Symbol.iterator]`, colon or method-shorthand form) desugars to a reserved *static* key, so the literal stays a static struct and the member drives `for...of`/`for await...of` iteration ([ADR-00279](../adr/ADR-00279.md)) |
| Shorthand property `{ x }` | ✅ | | |
| Method shorthand `{ foo() {...} }` | ✅ | • No `this` binding at all — `this` inside a method-shorthand body is a clean compile-time rejection (an object literal has no nominal type to give `this` a shape, and no dynamic call-site binding machinery exists), not silently wrong<br>• No `async`/generator method shorthand either, matching this compiler's class methods ([ADR-00169](../adr/ADR-00169.md)) | • Desugars to `{ foo: function() {...} }` — a plain anonymous function value, reusing the same closure machinery ([TDD-00060](../tdd/TDD-00060.md)) |
| `Map.set/get/has/delete/keys/values` | ✅ | • A missing key reads as `null` (the compiler's `undefined` stand-in), not a distinct `undefined`; a reference-typed `V` (string/object) returns the `"null"` stand-in on a miss ([TDD-00064](../tdd/TDD-00064.md)/[ADR-00199](../adr/ADR-00199.md)) | • `.get()` on a scalar-valued map returns `V \| null` — a missing key is distinguishable from a stored `0`/`false` via a presence-flagged nullable-scalar representation; fixes the original in-band-zero-sentinel bug found by the audit ([ADR-00166](../adr/ADR-00166.md)) |
| `Map.size` | ✅ | | |
| `Map.entries()` | ✅ | | • Returns a real `[K, V][]` tuple array ([TDD-00066](../tdd/TDD-00066.md)/[ADR-00201](../adr/ADR-00201.md)); iterate with `for (const [k, v] of m.entries())`; replaced the earlier object-shaped stand-in ([ADR-00053](../adr/ADR-00053.md)) |
| `Map.forEach()` | ✅ | • The 3rd `map` argument real JS also passes to the callback is dropped (the same simplification `Array.forEach`'s `(elem, index)` makes) ([ADR-00053](../adr/ADR-00053.md)) | • Calls `fn(value, key)`, matching real JS's argument order |
| `Map.clear()` | ✅ | | • Resets size to 0 in place — doesn't free/reallocate the backing arrays ("leak by design" memory model); immediately reusable afterward ([ADR-00053](../adr/ADR-00053.md)) |
| `new Set(iterable)` | ✅ | • Accepts only an array expression, narrowed from the spec's `Iterable<T>` — the only iterable concept a general expression has here ([ADR-00159](../adr/ADR-00159.md)) | • `new Map(entries)` doesn't yet accept an initial-entries argument, though the `[K, V]` tuple type it needs now exists ([TDD-00066](../tdd/TDD-00066.md)) — a separately-shippable follow-on |
| `Set.add/has/delete/values` | ✅ | | |
| `Set.size` | ✅ | | |
| `Set.forEach()` | ✅ | | • Calls `fn(element[, element])` — real JS's `Set.prototype.forEach` passes the value twice (`(value, value, set)`) for Map/Set callback-shape parity; mirrored here when the callback declares a 2nd parameter ([ADR-00053](../adr/ADR-00053.md)) |
| `Set.clear()` | ✅ | | • Same in-place reset as `Map.clear()` ([ADR-00053](../adr/ADR-00053.md)) |
| `WeakMap` / `WeakSet` / `WeakRef` | ❌ | | • Not implemented — no weak-reference/finalization machinery |

## Known limitations

- Objects are fixed-shape heap structs — no dynamic property add/delete (`Object.freeze`/`.seal` don't need to enforce this; it's already structurally impossible).
