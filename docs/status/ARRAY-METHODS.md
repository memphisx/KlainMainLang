<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/array-methods.json; edit the JSON, then run `make status`. -->

# Array Methods

> Part of the [Implementation Status](README.md) index.

**Coverage**: 36/36 (100%) · **Strict Coverage**: 20/36 (~56%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| Literal `[a, b, c]` | ✅ | | |
| `new Array<T>(n?)` | ✅ | • A preallocated `new Array<T>(n)` fills real zero-valued slots, not holes — `new Array<number>(3)[0]` is `0` (Node: `undefined`), and `map`/`forEach` visit those slots instead of skipping holes. | • Zero-arg `new Array<T>()` is an empty array ([ADR-00463](../adr/ADR-00463.md)) |
| `.length` | ✅ | • `a.length = 2` (real JS's array-truncation idiom) hard compile-errors with "field assignment on non-object" — length is read-only in practice ([ADR-00166](../adr/ADR-00166.md)) | |
| `.push(...items)` | ✅ | • An array passed as an **object field** or **array element** (`obj.items`, `grid[i]`), or as a higher-order-callback element, still crosses as a copy, so a length change through those is not seen by the caller ([TDD-00127](../tdd/TDD-00127.md)) | • Variadic (incl. the zero-argument call), and works on any mutable receiver — a variable, an object/class array field (`this.items.push(x)`), or a nested-array element (`matrix[0].push(x)`) — see [ADR-00284](../adr/ADR-00284.md) |
| `.pop()` | ✅ | • A nested-array element (`T[][]` receiver) still zero-fills the miss (`{null,0}`) instead of `undefined` — the {ptr,i64} aggregate has no absent state ([ADR-00246](../adr/ADR-00246.md))<br>• When the array is passed as an **object field**/**array element** (`obj.items`, `grid[i]`) or a HOF-callback element, a length change in a callee is not seen by the caller — those still pass a copy ([TDD-00127](../tdd/TDD-00127.md)) | • Absence result is a real `T | undefined` (TDD-00187 Stage 1, [ADR-00778](../adr/ADR-00778.md)): strict mode requires narrowing/`??`/`!` at bare-`T` boundaries, `-compat=js` auto-widens<br>• Works on any mutable receiver (variable, object/class field, nested-array element — [ADR-00284](../adr/ADR-00284.md)). See [ADR-00167](../adr/ADR-00167.md) |
| `.shift()` | ✅ | • A nested-array element (`T[][]` receiver) still zero-fills the miss (`{null,0}`) instead of `undefined` — the {ptr,i64} aggregate has no absent state ([ADR-00246](../adr/ADR-00246.md))<br>• When the array is passed as an **object field**/**array element** (`obj.items`, `grid[i]`) or a HOF-callback element, a length change in a callee is not seen by the caller — those still pass a copy ([TDD-00127](../tdd/TDD-00127.md)) | • Absence result is a real `T | undefined` (TDD-00187 Stage 1, [ADR-00778](../adr/ADR-00778.md)): strict mode requires narrowing/`??`/`!` at bare-`T` boundaries, `-compat=js` auto-widens<br>• Works on any mutable receiver ([ADR-00284](../adr/ADR-00284.md)). See [ADR-00167](../adr/ADR-00167.md) |
| `.unshift(...items)` | ✅ | • When the array is passed as an **object field**/**array element** (`obj.items`, `grid[i]`) or a HOF-callback element, a length change in a callee is not seen by the caller — those still pass a copy ([TDD-00127](../tdd/TDD-00127.md)) | • Variadic (incl. the zero-argument call), any mutable receiver ([ADR-00284](../adr/ADR-00284.md)) |
| `.splice(start, delete?, ...items)` | ✅ | • When the array is passed as an **object field**/**array element** (`obj.items`, `grid[i]`) or a HOF-callback element, a length change in a callee is not seen by the caller — those still pass a copy ([TDD-00127](../tdd/TDD-00127.md)) | • Works on any mutable receiver ([ADR-00284](../adr/ADR-00284.md))<br>• `delete` clamps to `[0, len - start]` and `start` normalizes negative indices, matching real JS<br>• [ADR-00056](../adr/ADR-00056.md) |
| `.slice(start, end?)` | ✅ | | |
| `.at(i)` | ✅ | • A nested-array element (`T[][]` receiver) still zero-fills the miss (`{null,0}`) instead of `undefined` — the {ptr,i64} aggregate has no absent state ([ADR-00246](../adr/ADR-00246.md)) | • Absence result is a real `T | undefined` (TDD-00187 Stage 1, [ADR-00778](../adr/ADR-00778.md)): strict mode requires narrowing/`??`/`!` at bare-`T` boundaries, `-compat=js` auto-widens. A negative index past the start is out of range (`undefined`), matching Node |
| `.indexOf(item)` | ✅ | • Rejects a nested-array element (`number[][]`) — compares a bare register, no callback ([ADR-00152](../adr/ADR-00152.md)) | |
| `.includes(item)` | ✅ | • Rejects a nested-array element (`number[][]`) — compares a bare register, no callback ([ADR-00152](../adr/ADR-00152.md)) | |
| `.find(fn)` | ✅ | • A nested-array element (`T[][]` receiver) still zero-fills the miss (`{null,0}`) instead of `undefined` — the {ptr,i64} aggregate has no absent state ([ADR-00246](../adr/ADR-00246.md)) | • Absence result is a real `T | undefined` (TDD-00187 Stage 1, [ADR-00778](../adr/ADR-00778.md)): strict mode requires narrowing/`??`/`!` at bare-`T` boundaries, `-compat=js` auto-widens |
| `.findIndex(fn)` | ✅ | | |
| `.some(fn)` | ✅ | | |
| `.every(fn)` | ✅ | | |
| `.map(fn)` | ✅ | | |
| `.filter(fn)` | ✅ | | |
| `.reduce(fn, init?)` | ✅ | | |
| `.reduceRight(fn, init?)` | ✅ | | • Right-to-left fold sharing `.reduce`'s machinery — only the traversal direction reverses ([ADR-00776](../adr/ADR-00776.md)) |
| `.forEach(fn)` | ✅ | | |
| `.join(sep?)` | ✅ | | • A nested-array element is unboxed and rendered as its own comma-joined string (real JS's recursive `Array.prototype.toString`), so `[[1,2],[3,4]].join("-")` is `"1,2-3,4"` — shares the array→string coercion path (`String(arr)`/`` `${arr}` ``) added in [ADR-00528](../adr/ADR-00528.md) |
| `.sort(fn?)` | ✅ | • Rejects a nested-array element (`number[][]`) — the custom comparator is a C-ABI `qsort()` trampoline with one fixed variant per element kind ([ADR-00152](../adr/ADR-00152.md)) | • The no-comparator default sort is JS-faithful: elements are stringified and compared lexicographically even for numbers, so `[10,1,21,2].sort()` is `[1,10,2,21]` ([ADR-00546](../adr/ADR-00546.md)) |
| `.reverse()` | ✅ | | |
| `.fill(val, start?, end?)` | ✅ | | |
| `.concat(...arrays)` | ✅ | | • Any number of arguments — arrays flatten one level, plain values append; zero arguments copy the receiver ([ADR-00463](../adr/ADR-00463.md)) |
| `.flat(depth?)` | ✅ | • `depth` must be a compile-time constant integer or `Infinity` — this compiler's arrays have a fixed nesting depth at the type level, so the result's element type has to be known at compile time ([TDD-00029](../tdd/TDD-00029.md)/[ADR-00107](../adr/ADR-00107.md)) | • `depth` defaults to 1, matching real JS<br>• `Infinity` flattens as deep as the receiver's own static type actually nests<br>• Always returns a fresh array, even when nothing gets flattened (`depth` 0, or a non-nested receiver) ([ADR-00057](../adr/ADR-00057.md)) |
| `.flatMap(fn)` | ✅ | | • `.map(fn)` followed by exactly one level of flattening — like real JS, no `depth` parameter<br>• A callback that doesn't return an array per element is just a plain map, matching real JS ([ADR-00107](../adr/ADR-00107.md)) |
| `.findLast(fn)` / `.findLastIndex(fn)` | ✅ | • A nested-array element (`T[][]` receiver) still zero-fills the miss (`{null,0}`) instead of `undefined` — the {ptr,i64} aggregate has no absent state ([ADR-00246](../adr/ADR-00246.md)) | • Absence result is a real `T | undefined` (TDD-00187 Stage 1, [ADR-00778](../adr/ADR-00778.md)): strict mode requires narrowing/`??`/`!` at bare-`T` boundaries, `-compat=js` auto-widens<br>• Genuine reverse iteration, not a forward scan keeping the last match — the callback is invoked starting from the last element, matching real JS's reverse call order, observable via side effects ([ADR-00057](../adr/ADR-00057.md)) |
| `.toSorted()` / `.toReversed()` / `.toSpliced()` | ✅ | | • Non-mutating counterparts of `.sort()`/`.reverse()`/`.splice()` — sort/reverse a fresh copy, or build a fresh spliced result, leaving the original array untouched ([ADR-00057](../adr/ADR-00057.md)) |
| `.with(i, val)` | ✅ | | • Returns a fresh copy with the element at `i` replaced; negative indices count from the end like `.at()`; an index still out of range after normalization throws a catchable Error, matching real JS's `RangeError` ([ADR-00057](../adr/ADR-00057.md)) |
| `.keys()` / `.values()` / `.entries()` | ✅ | • All return materialized arrays, not lazy iterators — this compiler has no general iterator protocol (the same convention `Map`/`Set`'s own `.keys()`/`.values()`/`Map.entries()` use) ([ADR-00057](../adr/ADR-00057.md)) | • `.entries()` returns a real `[number, T][]` tuple array ([TDD-00066](../tdd/TDD-00066.md)/[ADR-00201](../adr/ADR-00201.md)) — destructure with `for (const [i, v] of arr.entries())` |
| `.copyWithin(target, start?, end?)` | ✅ | | • In-place, overlap-safe via `memmove` — a self-overlapping copy such as `arr.copyWithin(0, 3)` on a 5-element array, the same overlap concern `.shift()`/`.unshift()`/`.splice()`'s tail shifts already handle ([ADR-00057](../adr/ADR-00057.md)) |
| `Array.isArray(x)` | ✅ | | |
| `Array.from(iterable, mapFn?)` | ✅ | • Iterates arrays, Sets, Maps (entries), strings, and classes implementing `next(): T \| null` — generators and `thisArg` are not supported ([ADR-00482](../adr/ADR-00482.md)/[ADR-00491](../adr/ADR-00491.md)) | |
| `Array.of(...items)` | ✅ | | • A plain call expression, usable anywhere an array literal `[...]` also is ([TDD-00028](../tdd/TDD-00028.md)/[ADR-00104](../adr/ADR-00104.md)); element type inferred from the first argument, the same rule `[...]` literals use ([ADR-00057](../adr/ADR-00057.md)) |

## Known limitations

- `Object.groupBy` also rejects a nested-array element (`number[][]`) — its buckets store each element as a raw `i64` ([ADR-00152](../adr/ADR-00152.md)).
