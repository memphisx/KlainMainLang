# Array Methods

> Part of the [Implementation Status](README.md) index.

**Coverage**: 35/35 (100%) · **Strict Coverage**: 25/35 (~71%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| Literal `[a, b, c]` | ✅ | | |
| `new Array<T>(n)` | ✅ | | |
| `.length` | ✅ | • `a.length = 2` (real JS's array-truncation idiom) hard compile-errors with "field assignment on non-object" — length is read-only in practice ([ADR-00166](../adr/ADR-00166.md)) | |
| `.push(...items)` | ✅ | | • Variadic (incl. the zero-argument call), and works on any mutable receiver — a variable, an object/class array field (`this.items.push(x)`), or a nested-array element (`matrix[0].push(x)`) — see [ADR-00284](../adr/ADR-00284.md) |
| `.pop()` | ✅ | • On an empty array returns the element type's zero value (length stays 0) — real JS returns `undefined`; this compiler has no undefined sentinel for a concrete scalar type ([ADR-00157](../adr/ADR-00157.md) convention) | • Works on any mutable receiver (variable, object/class field, nested-array element — [ADR-00284](../adr/ADR-00284.md)). See [ADR-00167](../adr/ADR-00167.md) |
| `.shift()` | ✅ | • Same empty-array zero-value-instead-of-`undefined` convention as `.pop()` ([ADR-00157](../adr/ADR-00157.md)) | • Works on any mutable receiver ([ADR-00284](../adr/ADR-00284.md)). See [ADR-00167](../adr/ADR-00167.md) |
| `.unshift(...items)` | ✅ | | • Variadic (incl. the zero-argument call), any mutable receiver ([ADR-00284](../adr/ADR-00284.md)) |
| `.splice(start, delete?, ...items)` | ✅ | | • Works on any mutable receiver ([ADR-00284](../adr/ADR-00284.md))<br>• `delete` clamps to `[0, len - start]` and `start` normalizes negative indices, matching real JS<br>• [ADR-00056](../adr/ADR-00056.md) |
| `.slice(start, end?)` | ✅ | | |
| `.at(i)` | ✅ | | |
| `.indexOf(item)` | ✅ | • Rejects a nested-array element (`number[][]`) — compares a bare register, no callback ([ADR-00152](../adr/ADR-00152.md)) | |
| `.includes(item)` | ✅ | • Rejects a nested-array element (`number[][]`) — compares a bare register, no callback ([ADR-00152](../adr/ADR-00152.md)) | |
| `.find(fn)` | ✅ | | |
| `.findIndex(fn)` | ✅ | | |
| `.some(fn)` | ✅ | | |
| `.every(fn)` | ✅ | | |
| `.map(fn)` | ✅ | | |
| `.filter(fn)` | ✅ | | |
| `.reduce(fn, init?)` | ✅ | | |
| `.forEach(fn)` | ✅ | | |
| `.join(sep?)` | ✅ | • Rejects a nested-array element (`number[][]`) — stringifies a bare register, no callback ([ADR-00152](../adr/ADR-00152.md)) | |
| `.sort(fn?)` | ✅ | • The no-comparator default sort is numeric, not real JS's actual default — real JS stringifies every element and compares lexicographically even for numbers, e.g. `[10,1,21,2].sort()` is `[1,10,2,21]` in real JS, `[1,2,10,21]` here<br>• `const sorted = arr.sort()` with no type annotation fails to typecheck as an array — `"sort"` is missing from the array-type-preserving method list `emit_exprs_types.go` uses; works with an explicit `: number[]` annotation ([ADR-00166](../adr/ADR-00166.md))<br>• Rejects a nested-array element (`number[][]`) — the custom comparator is a C-ABI `qsort()` trampoline with one fixed variant per element kind ([ADR-00152](../adr/ADR-00152.md)) | |
| `.reverse()` | ✅ | | |
| `.fill(val, start?, end?)` | ✅ | | |
| `.concat(...arrays)` | ✅ | | |
| `.flat(depth?)` | ✅ | • `depth` must be a compile-time constant integer or `Infinity` — this compiler's arrays have a fixed nesting depth at the type level, so the result's element type has to be known at compile time ([TDD-00029](../tdd/TDD-00029.md)/[ADR-00107](../adr/ADR-00107.md)) | • `depth` defaults to 1, matching real JS<br>• `Infinity` flattens as deep as the receiver's own static type actually nests<br>• Always returns a fresh array, even when nothing gets flattened (`depth` 0, or a non-nested receiver) ([ADR-00057](../adr/ADR-00057.md)) |
| `.flatMap(fn)` | ✅ | | • `.map(fn)` followed by exactly one level of flattening — like real JS, no `depth` parameter<br>• A callback that doesn't return an array per element is just a plain map, matching real JS ([ADR-00107](../adr/ADR-00107.md)) |
| `.findLast(fn)` / `.findLastIndex(fn)` | ✅ | | • Genuine reverse iteration, not a forward scan keeping the last match — the callback is invoked starting from the last element, matching real JS's reverse call order, observable via side effects ([ADR-00057](../adr/ADR-00057.md)) |
| `.toSorted()` / `.toReversed()` / `.toSpliced()` | ✅ | | • Non-mutating counterparts of `.sort()`/`.reverse()`/`.splice()` — sort/reverse a fresh copy, or build a fresh spliced result, leaving the original array untouched ([ADR-00057](../adr/ADR-00057.md)) |
| `.with(i, val)` | ✅ | | • Returns a fresh copy with the element at `i` replaced; negative indices count from the end like `.at()`; an index still out of range after normalization throws a catchable Error, matching real JS's `RangeError` ([ADR-00057](../adr/ADR-00057.md)) |
| `.keys()` / `.values()` / `.entries()` | ✅ | • All return materialized arrays, not lazy iterators — this compiler has no general iterator protocol (the same convention `Map`/`Set`'s own `.keys()`/`.values()`/`Map.entries()` use) ([ADR-00057](../adr/ADR-00057.md)) | • `.entries()` returns a real `[number, T][]` tuple array ([TDD-00066](../tdd/TDD-00066.md)/[ADR-00201](../adr/ADR-00201.md)) — destructure with `for (const [i, v] of arr.entries())` |
| `.copyWithin(target, start?, end?)` | ✅ | | • In-place, overlap-safe via `memmove` — a self-overlapping copy such as `arr.copyWithin(0, 3)` on a 5-element array, the same overlap concern `.shift()`/`.unshift()`/`.splice()`'s tail shifts already handle ([ADR-00057](../adr/ADR-00057.md)) |
| `Array.isArray(x)` | ✅ | | |
| `Array.from(iterable)` | ✅ | • Array-like overload only — a plain array, or a class implementing `next(): T \| null`; no `mapFn`/`thisArg`, no direct string/Map/Set iteration ([ADR-00063](../adr/ADR-00063.md)/[ADR-00088](../adr/ADR-00088.md)) | |
| `Array.of(...items)` | ✅ | | • A plain call expression, usable anywhere an array literal `[...]` also is ([TDD-00028](../tdd/TDD-00028.md)/[ADR-00104](../adr/ADR-00104.md)); element type inferred from the first argument, the same rule `[...]` literals use ([ADR-00057](../adr/ADR-00057.md)) |

## Known limitations

- `.reduceRight()` (right-to-left fold) is not implemented at all — no row above.
- `Object.groupBy` also rejects a nested-array element (`number[][]`) — its buckets store each element as a raw `i64` ([ADR-00152](../adr/ADR-00152.md)).
