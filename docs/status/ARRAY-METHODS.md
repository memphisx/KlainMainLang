# Array Methods

> Part of the [Implementation Status](README.md) index.

**Coverage**: ~95% (38/40).

**Caveats**: `.flat()`/`.flatMap()` are blocked on nested-array support — `number[][]`-style literals aren't reliably representable yet (`[[1,2],[3,4]]` gives a clean, deliberate compile error), tracked in [TDD-00029](../tdd/TDD-00029.md). Array/Map/Set/EventEmitter literals themselves are now fully general expressions — usable as a call argument, return value, object-literal field value, or reassignment target, not just a `const`/`let` initializer — see [TDD-00028](../tdd/TDD-00028.md)/[ADR-00104](../adr/ADR-00104.md). `Array.from(iterable)` is done for the array-like overload (a plain array, or a class implementing `next(): T | null`) — see [ADR-00088](../adr/ADR-00088.md); the `mapFn`/`thisArg` arguments and iterating a string/Map/Set directly aren't built.

| Method | Status |
|---|---|
| Literal `[a, b, c]` | ✅ |
| `new Array<T>(n)` | ✅ |
| `.length` | ✅ |
| `.push(...items)` | ✅ |
| `.pop()` | ✅ |
| `.shift()` | ✅ |
| `.unshift(...items)` | ✅ |
| `.splice(start, delete?, ...items)` | ✅ (`delete` clamps to `[0, len - start]` and `start` normalizes negative indices, matching real JS — an over-large `delete` used to read past the backing allocation and corrupt the array's own length to negative, a real memory-safety bug, not just a wrong-answer one; `...items` insertion wasn't implemented at all despite the row already claiming it. Both fixed together. See [ADR-00056](../adr/ADR-00056.md).) |
| `.slice(start, end?)` | ✅ |
| `.at(i)` | ✅ |
| `.indexOf(item)` | ✅ |
| `.includes(item)` | ✅ |
| `.find(fn)` | ✅ |
| `.findIndex(fn)` | ✅ |
| `.some(fn)` | ✅ |
| `.every(fn)` | ✅ |
| `.map(fn)` | ✅ |
| `.filter(fn)` | ✅ |
| `.reduce(fn, init?)` | ✅ |
| `.forEach(fn)` | ✅ |
| `.join(sep?)` | ✅ |
| `.sort(fn?)` | ✅ |
| `.reverse()` | ✅ |
| `.fill(val, start?, end?)` | ✅ |
| `.concat(...arrays)` | ✅ |
| `.flat(depth?)` | ❌ (blocked on nested-array support — `number[][]`-style literals aren't reliably representable yet: `[[1,2],[3,4]]` gives a clean, deliberate compile error, "nested arrays (array-of-arrays) are not yet supported." Array literals are otherwise fully general expressions now ([TDD-00028](../tdd/TDD-00028.md)/[ADR-00104](../adr/ADR-00104.md)) — this is a separate, real gap: an array's backing buffer is flat, fixed-width slots, but a nested array element's own value is a 16-byte pair that doesn't fit in one such slot without a boxing/indirection layer this compiler doesn't have. Tracked in [TDD-00029](../tdd/TDD-00029.md). First found in [ADR-00057](../adr/ADR-00057.md).) |
| `.flatMap(fn)` | ❌ (same nested-array blocker as `.flat()` — see [TDD-00029](../tdd/TDD-00029.md)) |
| `.findLast(fn)` / `.findLastIndex(fn)` | ✅ (genuine reverse iteration, not a forward scan keeping the last match — the callback is invoked starting from the last element, matching real JS's own reverse call order, observable via side effects. See [ADR-00057](../adr/ADR-00057.md).) |
| `.toSorted()` / `.toReversed()` / `.toSpliced()` | ✅ (non-mutating counterparts of `.sort()`/`.reverse()`/`.splice()` — sort/reverse a fresh copy, or build a fresh spliced result, leaving the original array untouched. See [ADR-00057](../adr/ADR-00057.md).) |
| `.with(i, val)` | ✅ (returns a fresh copy with the element at `i` replaced; negative indices count from the end like `.at()`; an index still out of range after normalization throws a catchable Error, matching real JS's `RangeError`. See [ADR-00057](../adr/ADR-00057.md).) |
| `.keys()` / `.values()` / `.entries()` | ✅ (all return materialized arrays, not lazy iterators — this compiler has no general iterator protocol, the same convention `Map`/`Set`'s own `.keys()`/`.values()`/`Map.entries()` already use. `.entries()` returns `{index: number, value: T}[]`, not a real `[index, value]` tuple, for the same no-tuple-type reason `Map.entries()`/`Object.entries()` already document. See [ADR-00057](../adr/ADR-00057.md).) |
| `.copyWithin(target, start?, end?)` | ✅ (in-place, overlap-safe via `memmove` — copying `arr.copyWithin(0, 3)` on a 5-element array is a self-overlapping copy, the same overlap concern `.shift()`/`.unshift()`/`.splice()`'s own tail shifts already handle. See [ADR-00057](../adr/ADR-00057.md).) |
| `Array.isArray(x)` | ✅ |
| `Array.from(iterable)` | ✅ (array-like overload only — a plain array, or a class implementing `next(): T \| null`, see [ADR-00063](../adr/ADR-00063.md); no `mapFn`/`thisArg`, no direct string/Map/Set iteration. See [ADR-00088](../adr/ADR-00088.md).) |
| `Array.of(...items)` | ✅ (a plain call expression, usable anywhere an array literal `[...]` now also is — see [TDD-00028](../tdd/TDD-00028.md)/[ADR-00104](../adr/ADR-00104.md) — element type inferred from the first argument, same rule `[...]` literals already use. See [ADR-00057](../adr/ADR-00057.md).) |
