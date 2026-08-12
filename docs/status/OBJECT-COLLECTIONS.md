# Object / Collections

> Part of the [Implementation Status](README.md) index.

**Coverage**: ~89% (24/27).

**Strict Coverage**: 12/27, ~44% — a row only counts here if it was independently repro-verified with zero known caveats or bugs, of any severity. See the 2026-08-11 audit ([ADR-00166](../adr/ADR-00166.md)) that produced this number and the new caveat below; every caveat found by that audit excludes the row from this count even though the row stays ✅ in the Coverage column above.

**Caveats**: Objects are fixed-shape heap structs — no dynamic property add/delete (`Object.freeze`/`.seal` don't need to enforce this since it's already structurally impossible). `Object.create()`/`Object.fromEntries()` aren't implemented. `WeakMap`/`WeakSet`/`WeakRef` aren't implemented (no weak-reference/finalization machinery).

| Feature | Status |
|---|---|
| Object literals `{ a: 1 }` | ✅ |
| Field access `obj.field` | ✅ |
| Object destructuring | ✅ |
| `Object.keys(obj)` | ✅ |
| `Object.values(obj)` | ✅ |
| `Object.entries(obj)` | ✅ |
| `Object.groupBy(arr, fn)` | ✅ |
| `Object.assign(target, ...src)` | ✅ (mutates and returns `target`; every field a source contributes must already exist on `target`'s own struct type — this compiler's objects are fixed-shape heap structs, not a dynamic property bag, so a source field target's type doesn't have is a clean compile error, not silently dropped or grafted on. See [ADR-00054](../adr/ADR-00054.md).) |
| `Object.create()` | ❌ |
| `Object.freeze(obj)` | ✅ (real runtime enforcement, not a no-op — tracks `obj`'s heap pointer in a global frozen-object set, checked at every field-write site, so a blocked write throws a catchable Error even through a different alias/function parameter, not just through the variable that called `freeze`. See [ADR-00055](../adr/ADR-00055.md).) |
| `Object.seal(obj)` | ✅ (a genuine no-op, not a scope-narrowed approximation of one — seal's real guarantee is "no new/deleted fields," which this compiler's fixed-shape objects already can't do at all, frozen or not, so there's nothing further to enforce. See [ADR-00055](../adr/ADR-00055.md).) |
| `Object.hasOwn()` / `.hasOwnProperty()` | ✅ (object shapes are fully structural/static, so this is a compile-time `FieldIndex` lookup, not a runtime scan — the key must be a string literal; a runtime-computed key is a clean compile error, since there's no field-name table at runtime to check it against. See [ADR-00065](../adr/ADR-00065.md).) |
| `Object.fromEntries()` | ❌ |
| Object spread `{ ...obj, key: val }` | ✅ |
| Computed property keys `{ [expr]: value }` | ✅ (a *dynamic object* — storage-wise a real `Map<string,V>` reusing `new Map<K,V>()`'s own runtime, with `.field`/`[expr]` sugar layered on top; `V` inferred from the first property only. `...spread` combined with a computed key, and a declared-type form (`{ [key: string]: T }`), aren't supported yet. See [TDD-00012](../tdd/TDD-00012.md) / [ADR-00066](../adr/ADR-00066.md) for full scope.) |
| Shorthand property `{ x }` | ✅ |
| Method shorthand `{ foo() {...} }` | ✅ (desugars to `{ foo: function() {...} }` — a plain anonymous function value, reusing the same closure machinery [TDD-00060](../tdd/TDD-00060.md) already built. No `this` binding at all: a class method's `this` has a known static type from its enclosing class; an object literal has no nominal type to give `this` a shape, and no dynamic call-site binding machinery exists — `this` inside a method-shorthand body is a clean compile-time rejection, not silently wrong. No `async`/generator method shorthand either, matching this compiler's own class methods (neither is supported there yet). See [ADR-00169](../adr/ADR-00169.md).) |
| `Map.set/get/has/delete/keys/values` | ✅ (`.get()` on a missing key silently returns `0` instead of `undefined` when `V` is a numeric type — `Map<string, number>` has no runtime tag distinguishing "missing" from a real stored `0`, the same fake in-band-zero-sentinel limitation already documented for `number \| null` elsewhere, just never carried onto this row. A reference-typed `V` (string/object) correctly returns the `"null"` stand-in on a miss. `.has()`/`.delete()`/`.keys()`/`.values()` are all unaffected. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md).) |
| `Map.size` | ✅ |
| `Map.entries()` | ✅ (`{key: K, value: V}[]`, not a real `[key, value]` tuple — this compiler has no tuple type. Same convention `Object.entries()` already uses; iterate with `for (const e of m.entries())` then read `e.key`/`e.value`. See [ADR-00053](../adr/ADR-00053.md).) |
| `Map.forEach()` | ✅ (calls `fn(value, key)`, matching real JS's argument order — the 3rd `map` argument real JS also passes is dropped, the same simplification `Array.forEach`'s `(elem, index)` already makes. See [ADR-00053](../adr/ADR-00053.md).) |
| `Map.clear()` | ✅ (resets size to 0 in place — doesn't free/reallocate the backing arrays, matching this compiler's "leak by design" memory model; the map is immediately reusable afterward. See [ADR-00053](../adr/ADR-00053.md).) |
| `new Set(iterable)` | ✅ (an array expression, narrowed from the real spec's `Iterable<T>` — the only iterable concept a general expression has here; `new Map(entries)` still doesn't accept an initial-entries argument — each entry would need a real `[K, V]` tuple type, which this compiler doesn't have. See [ADR-00159](../adr/ADR-00159.md).) |
| `Set.add/has/delete/values` | ✅ |
| `Set.size` | ✅ |
| `Set.forEach()` | ✅ (calls `fn(element[, element])` — real JS's own `Set.prototype.forEach` passes the value twice, `(value, value, set)`, for Map/Set callback-shape parity; mirrored here when the callback declares a 2nd parameter. See [ADR-00053](../adr/ADR-00053.md).) |
| `Set.clear()` | ✅ (same in-place reset as `Map.clear()`. See [ADR-00053](../adr/ADR-00053.md).) |
| `WeakMap` / `WeakSet` / `WeakRef` | ❌ |
