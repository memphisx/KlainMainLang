# Array Methods

> Part of the [Implementation Status](README.md) index.

**Coverage**: 100% (40/40).

**Caveats**: Array-of-arrays (nested arrays, `number[][]`) are now real — construction, indexing (read/write), destructuring, `for...of`, `.at()`/`.with()`/`.fill()`/`.push()`/`.pop()`/`.shift()`/`.unshift()`/`.splice()`/`.concat()`/`.slice()`/`.reverse()`/`.copyWithin()`/`.flat()`/`.flatMap()`, and `JSON.stringify` all support a nested-array element — see [TDD-00029](../tdd/TDD-00029.md)/[ADR-00105](../adr/ADR-00105.md)/[ADR-00107](../adr/ADR-00107.md). `.map()`/`.filter()`/`.forEach()`/`.reduce()`/`.find()`/`.findIndex()`/`.findLast()`/`.findLastIndex()`/`.some()`/`.every()` now all accept a nested-array element as the callback's own parameter too — closures previously never decomposed an array-typed parameter into `(ptr, i64)` the way a named function call's own ABI already did; fixed on a follow-up pass after tagged template literals first exposed the gap, see [ADR-00152](../adr/ADR-00152.md) (extending [ADR-00151](../adr/ADR-00151.md)/[TDD-00059](../tdd/TDD-00059.md)). Three genuinely different mechanisms still reject a nested-array element, each for its own unrelated reason: `.sort()`'s custom comparator runs through a C-ABI `qsort()` trampoline with one fixed variant per element kind; `.indexOf()`/`.includes()`/`.join()` compare/stringify a bare register directly with no callback involved at all; `Object.groupBy`'s buckets store every element as a raw `i64`, a different storage scheme than a plain array's backing buffer. `.flat()`/`.flatMap()` were never affected either way, since neither invokes a callback with a nested array as its own parameter. `.flat(depth)`'s `depth` must be a compile-time constant (a literal, or `Infinity`) rather than a general runtime expression — this compiler's arrays have a fixed nesting depth at the type level, so the result's element type has to be knowable at compile time. Array/Map/Set/EventEmitter literals themselves are fully general expressions — usable as a call argument, return value, object-literal field value, or reassignment target, not just a `const`/`let` initializer — see [TDD-00028](../tdd/TDD-00028.md)/[ADR-00104](../adr/ADR-00104.md). `Array.from(iterable)` is done for the array-like overload (a plain array, or a class implementing `next(): T | null`) — see [ADR-00088](../adr/ADR-00088.md); the `mapFn`/`thisArg` arguments and iterating a string/Map/Set directly aren't built. `.reduce()` with no initial value is now done too — the array's own first element seeds the accumulator, matching real JS — see [ADR-00163](../adr/ADR-00163.md). `.reduceRight()` (right-to-left fold) isn't implemented at all — not counted in the 40/40 below, a newly-identified gap found alongside the `.reduce()` fix, not a narrower version of it.

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
| `.flat(depth?)` | ✅ (`depth` defaults to 1, matching real JS; must be a compile-time constant integer or `Infinity` — this compiler's arrays have a fixed nesting depth at the type level, so the result's element type has to be known at compile time. `Infinity` flattens as deep as the receiver's own static type actually nests. Always returns a fresh array, even when nothing gets flattened (`depth` 0, or a non-nested receiver). See [TDD-00029](../tdd/TDD-00029.md)/[ADR-00107](../adr/ADR-00107.md). First found in [ADR-00057](../adr/ADR-00057.md).) |
| `.flatMap(fn)` | ✅ (`.map(fn)` followed by exactly one level of flattening — real JS's `flatMap` has no `depth` parameter at all. A callback that doesn't return an array per element is just a plain map, matching real JS. See [ADR-00107](../adr/ADR-00107.md).) |
| `.findLast(fn)` / `.findLastIndex(fn)` | ✅ (genuine reverse iteration, not a forward scan keeping the last match — the callback is invoked starting from the last element, matching real JS's own reverse call order, observable via side effects. See [ADR-00057](../adr/ADR-00057.md).) |
| `.toSorted()` / `.toReversed()` / `.toSpliced()` | ✅ (non-mutating counterparts of `.sort()`/`.reverse()`/`.splice()` — sort/reverse a fresh copy, or build a fresh spliced result, leaving the original array untouched. See [ADR-00057](../adr/ADR-00057.md).) |
| `.with(i, val)` | ✅ (returns a fresh copy with the element at `i` replaced; negative indices count from the end like `.at()`; an index still out of range after normalization throws a catchable Error, matching real JS's `RangeError`. See [ADR-00057](../adr/ADR-00057.md).) |
| `.keys()` / `.values()` / `.entries()` | ✅ (all return materialized arrays, not lazy iterators — this compiler has no general iterator protocol, the same convention `Map`/`Set`'s own `.keys()`/`.values()`/`Map.entries()` already use. `.entries()` returns `{index: number, value: T}[]`, not a real `[index, value]` tuple, for the same no-tuple-type reason `Map.entries()`/`Object.entries()` already document. See [ADR-00057](../adr/ADR-00057.md).) |
| `.copyWithin(target, start?, end?)` | ✅ (in-place, overlap-safe via `memmove` — copying `arr.copyWithin(0, 3)` on a 5-element array is a self-overlapping copy, the same overlap concern `.shift()`/`.unshift()`/`.splice()`'s own tail shifts already handle. See [ADR-00057](../adr/ADR-00057.md).) |
| `Array.isArray(x)` | ✅ |
| `Array.from(iterable)` | ✅ (array-like overload only — a plain array, or a class implementing `next(): T \| null`, see [ADR-00063](../adr/ADR-00063.md); no `mapFn`/`thisArg`, no direct string/Map/Set iteration. See [ADR-00088](../adr/ADR-00088.md).) |
| `Array.of(...items)` | ✅ (a plain call expression, usable anywhere an array literal `[...]` now also is — see [TDD-00028](../tdd/TDD-00028.md)/[ADR-00104](../adr/ADR-00104.md) — element type inferred from the first argument, same rule `[...]` literals already use. See [ADR-00057](../adr/ADR-00057.md).) |
