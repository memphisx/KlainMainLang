# Array Methods

> Part of the [Implementation Status](README.md) index.

**Coverage**: 100% (40/40).

**Strict Coverage**: 25/40, ~63% — a row only counts here if it was independently repro-verified with zero known caveats or bugs, of any severity. See the 2026-08-11 audit ([ADR-00166](../adr/ADR-00166.md)) that produced this number and the new caveats below; every caveat found by that audit excludes the row from this count even though the row stays ✅ in the Coverage column above.

**Caveats**:

- `.reduceRight()` (right-to-left fold) is not implemented at all — not counted in the 40/40 below.
- A nested-array element (`number[][]`) is still rejected by three methods, each for its own reason: `.sort()`'s custom comparator (a C-ABI `qsort()` trampoline, one fixed variant per element kind), `.indexOf()`/`.includes()`/`.join()` (compare/stringify a bare register, no callback), and `Object.groupBy` (buckets store each element as a raw `i64`). Every other callback-invoking method accepts one — see [ADR-00152](../adr/ADR-00152.md).
- `.flat(depth)`'s `depth` must be a compile-time constant (a literal or `Infinity`), since array nesting depth is fixed at the type level — see [TDD-00029](../tdd/TDD-00029.md)/[ADR-00107](../adr/ADR-00107.md).
- `Array.from` supports the array-like overload only (a plain array, or a class implementing `next(): T | null`); no `mapFn`/`thisArg`, no direct string/Map/Set iteration — see [ADR-00088](../adr/ADR-00088.md).

| Method | Status |
|---|---|
| Literal `[a, b, c]` | ✅ |
| `new Array<T>(n)` | ✅ |
| `.length` | ✅ (read-only in practice — `a.length = 2`, real JS's array-truncation idiom, hard compile-errors with "field assignment on non-object". Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md).) |
| `.push(...items)` | ✅ (single-argument form only — `arr.push(20, 30)`, the row's own documented `...items` variadic signature, hard compile-errors with "push takes exactly one argument"; only `arr.push(20)` works. Also requires a bare local-variable receiver — `this.field.push(x)` inside a class method hard compile-errors with "push requires an array variable", so this doesn't work on a class-field array at all, one of the single most common real-world array-mutation patterns. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md).) |
| `.pop()` | ✅ (Requires a bare local-variable receiver — doesn't work on `this.field.pop()`. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md). The empty-array crash (garbage return, `.length` → `-1`) was fixed in [ADR-00167](../adr/ADR-00167.md).) |
| `.shift()` | ✅ (Requires a bare local-variable receiver — doesn't work on `this.field.shift()`. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md). The empty-array crash (garbage return, `.length` → `-1`, unguarded memmove) was fixed in [ADR-00167](../adr/ADR-00167.md).) |
| `.unshift(...items)` | ✅ (single-argument form only, same variadic gap as `.push()` above — the row's own `...items` signature is aspirational, not built. Also requires a bare local-variable receiver. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md).) |
| `.splice(start, delete?, ...items)` | ✅ (`delete` clamps to `[0, len - start]` and `start` normalizes negative indices, matching real JS — an over-large `delete` used to read past the backing allocation and corrupt the array's own length to negative, a real memory-safety bug, not just a wrong-answer one; `...items` insertion wasn't implemented at all despite the row already claiming it. Both fixed together. See [ADR-00056](../adr/ADR-00056.md). Also requires a bare local-variable receiver, the same restriction found on `.push()`/`.pop()`/`.shift()`/`.unshift()` above — doesn't work on `this.field.splice(...)`. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md).) |
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
| `.sort(fn?)` | ✅ (the no-comparator default sort is numeric, not real JS's actual default — real JS stringifies every element and compares lexicographically even for numbers, e.g. `[10,1,21,2].sort()` is `[1,10,2,21]` in real JS, `[1,2,10,21]` here. Also, `const sorted = arr.sort()` with no type annotation fails to typecheck as an array — `"sort"` is missing from the array-type-preserving method list `emit_exprs_types.go` uses for `.reverse()`/`.toSorted()`/etc.; works fine with an explicit `: number[]` annotation. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md).) |
| `.reverse()` | ✅ |
| `.fill(val, start?, end?)` | ✅ |
| `.concat(...arrays)` | ✅ |
| `.flat(depth?)` | ✅ (`depth` defaults to 1, matching real JS; must be a compile-time constant integer or `Infinity` — this compiler's arrays have a fixed nesting depth at the type level, so the result's element type has to be known at compile time. `Infinity` flattens as deep as the receiver's own static type actually nests. Always returns a fresh array, even when nothing gets flattened (`depth` 0, or a non-nested receiver). See [TDD-00029](../tdd/TDD-00029.md)/[ADR-00107](../adr/ADR-00107.md). First found in [ADR-00057](../adr/ADR-00057.md).) |
| `.flatMap(fn)` | ✅ (`.map(fn)` followed by exactly one level of flattening — real JS's `flatMap` has no `depth` parameter at all. A callback that doesn't return an array per element is just a plain map, matching real JS. See [ADR-00107](../adr/ADR-00107.md).) |
| `.findLast(fn)` / `.findLastIndex(fn)` | ✅ (genuine reverse iteration, not a forward scan keeping the last match — the callback is invoked starting from the last element, matching real JS's own reverse call order, observable via side effects. See [ADR-00057](../adr/ADR-00057.md).) |
| `.toSorted()` / `.toReversed()` / `.toSpliced()` | ✅ (non-mutating counterparts of `.sort()`/`.reverse()`/`.splice()` — sort/reverse a fresh copy, or build a fresh spliced result, leaving the original array untouched. See [ADR-00057](../adr/ADR-00057.md).) |
| `.with(i, val)` | ✅ (returns a fresh copy with the element at `i` replaced; negative indices count from the end like `.at()`; an index still out of range after normalization throws a catchable Error, matching real JS's `RangeError`. See [ADR-00057](../adr/ADR-00057.md).) |
| `.keys()` / `.values()` / `.entries()` | ✅ (all return materialized arrays, not lazy iterators — this compiler has no general iterator protocol, the same convention `Map`/`Set`'s own `.keys()`/`.values()`/`Map.entries()` already use. `.entries()` returns a real `[number, T][]` tuple array since [TDD-00066](../tdd/TDD-00066.md)/[ADR-00201](../adr/ADR-00201.md) — destructure with `for (const [i, v] of arr.entries())`. See [ADR-00057](../adr/ADR-00057.md) for the original object-shaped stand-in this replaced.) |
| `.copyWithin(target, start?, end?)` | ✅ (in-place, overlap-safe via `memmove` — copying `arr.copyWithin(0, 3)` on a 5-element array is a self-overlapping copy, the same overlap concern `.shift()`/`.unshift()`/`.splice()`'s own tail shifts already handle. See [ADR-00057](../adr/ADR-00057.md).) |
| `Array.isArray(x)` | ✅ |
| `Array.from(iterable)` | ✅ (array-like overload only — a plain array, or a class implementing `next(): T \| null`, see [ADR-00063](../adr/ADR-00063.md); no `mapFn`/`thisArg`, no direct string/Map/Set iteration. See [ADR-00088](../adr/ADR-00088.md).) |
| `Array.of(...items)` | ✅ (a plain call expression, usable anywhere an array literal `[...]` now also is — see [TDD-00028](../tdd/TDD-00028.md)/[ADR-00104](../adr/ADR-00104.md) — element type inferred from the first argument, same rule `[...]` literals already use. See [ADR-00057](../adr/ADR-00057.md).) |
