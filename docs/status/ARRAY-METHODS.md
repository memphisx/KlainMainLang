# Array Methods

> Part of the [Implementation Status](README.md) index.

**Coverage**: ~95% (38/40).

**Caveats**: `.flat()`/`.flatMap()` are blocked on nested-array support — `number[][]`-style literals aren't reliably representable yet (`[[1,2],[3,4]]` fails to compile). `Array.from(iterable)` is done for the array-like overload (a plain array, or a class implementing `next(): T | null`) — see [ADR-00088](../adr/ADR-00088.md); the `mapFn`/`thisArg` arguments and iterating a string/Map/Set directly aren't built.

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
| `.flat(depth?)` | ❌ (blocked on nested-array support — `number[][]`-style literals aren't reliably representable yet: `[[1,2],[3,4]]` fails to compile with "array literal must be used in a variable declaration" for the nested literal. See [ADR-00057](../adr/ADR-00057.md) for where this was found.) |
| `.flatMap(fn)` | ❌ (same nested-array blocker as `.flat()`) |
| `.findLast(fn)` / `.findLastIndex(fn)` | ✅ (genuine reverse iteration, not a forward scan keeping the last match — the callback is invoked starting from the last element, matching real JS's own reverse call order, observable via side effects. See [ADR-00057](../adr/ADR-00057.md).) |
| `.toSorted()` / `.toReversed()` / `.toSpliced()` | ✅ (non-mutating counterparts of `.sort()`/`.reverse()`/`.splice()` — sort/reverse a fresh copy, or build a fresh spliced result, leaving the original array untouched. See [ADR-00057](../adr/ADR-00057.md).) |
| `.with(i, val)` | ✅ (returns a fresh copy with the element at `i` replaced; negative indices count from the end like `.at()`; an index still out of range after normalization throws a catchable Error, matching real JS's `RangeError`. See [ADR-00057](../adr/ADR-00057.md).) |
| `.keys()` / `.values()` / `.entries()` | ✅ (all return materialized arrays, not lazy iterators — this compiler has no general iterator protocol, the same convention `Map`/`Set`'s own `.keys()`/`.values()`/`Map.entries()` already use. `.entries()` returns `{index: number, value: T}[]`, not a real `[index, value]` tuple, for the same no-tuple-type reason `Map.entries()`/`Object.entries()` already document. See [ADR-00057](../adr/ADR-00057.md).) |
| `.copyWithin(target, start?, end?)` | ✅ (in-place, overlap-safe via `memmove` — copying `arr.copyWithin(0, 3)` on a 5-element array is a self-overlapping copy, the same overlap concern `.shift()`/`.unshift()`/`.splice()`'s own tail shifts already handle. See [ADR-00057](../adr/ADR-00057.md).) |
| `Array.isArray(x)` | ✅ |
| `Array.from(iterable)` | ✅ (array-like overload only — a plain array, or a class implementing `next(): T \| null`, see [ADR-00063](../adr/ADR-00063.md); no `mapFn`/`thisArg`, no direct string/Map/Set iteration. See [ADR-00088](../adr/ADR-00088.md).) |
| `Array.of(...items)` | ✅ (unlike an array literal `[...]`, which can currently only appear in variable-declaration position, `Array.of(...)` is a plain call expression usable anywhere — element type inferred from the first argument, same rule `[...]` literals already use. See [ADR-00057](../adr/ADR-00057.md).) |
