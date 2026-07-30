# Language Constructs

> Part of the [Implementation Status](README.md) index. Covers control flow, operators, variable declarations, functions/closures, async/Promise, enums, interfaces, and classes/OOP.

**Coverage**: 50/58 rows on this page, ~86% (see the index's Coverage Summary for the breakdown by sub-category).

**Caveats**: `class` inheritance (`extends`/`super`) is the biggest missing piece — [TDD-00009](../tdd/TDD-00009.md) Stage 3, the only stage that needs new dynamic-dispatch machinery. `class` also has no `static` members, private fields (`#x`), `static {}` blocks, or `implements`/`abstract` — the parser deliberately defers all of these (`parser/parser_classes.go`'s own comment: *"deferred to later"*), not just inheritance. `async`/`await` is a synchronous resolved-slot read for everything except `await fetch(...)`, which is genuinely non-blocking (yields via a fiber inside an `http.listen` handler) — see [TDD-00006](../tdd/TDD-00006.md). A handful of newer-but-common syntax has no tracking anywhere else either: logical assignment (`&&=`/`\|\|=`/`??=`), getters/setters, optional catch binding, tagged templates, numeric separators, and destructured function parameters — all confirmed absent directly against the lexer/parser, not just undocumented.

| Feature | Status | Notes |
|---|---|---|
| `const` / `let` / `var` declarations | ✅ | All three treated as mutable allocas |
| Numeric literals (`42`, `3.14`, `0xFF`, `0b101`, `0o77`) | ✅ | |
| Numeric separators (`1_000_000`) | ❌ | Confirmed absent: `lexer.go`'s `readNumber` has no `_`-skipping logic in either the decimal or the hex/binary/octal branches — `1_000` lexes as `1` followed by a bad `_000` token, not as `1000`. |
| String literals (single/double quote) | ✅ | |
| Boolean literals (`true` / `false`) | ✅ | |
| `null` literal | ✅ | `T \| null` union type supported |
| `undefined` literal | ✅ | |
| Template literals `` `Hello ${name}` `` | ✅ | Arbitrary interpolation depth |
| Tagged template literals (`` tag`Hello ${x}` ``) | ❌ | No `TaggedTemplate` AST node or parsing support found anywhere — a plain template literal only, never passed to a preceding function |
| Arithmetic operators `+ - * / % **` | ✅ | |
| Comparison operators `== === != !== < > <= >=` | ✅ | String comparison via `strcmp` |
| Logical operators `&& \|\| !` | ✅ | Short-circuit evaluation |
| Bitwise operators `& \| ^ ~ << >> >>>` | ✅ | |
| Assignment operators `+= -= *= /= %= &= \|= ^= <<= >>= >>>=` | ✅ | |
| Logical assignment operators `&&= \|\|= ??=` (ES2021) | ❌ | `lexer/token.go` only defines `AND_ASSIGN`/`OR_ASSIGN`/`XOR_ASSIGN` for the bitwise `&= \|= ^=` above — no logical-assignment tokens exist at all, confirmed directly against the token table |
| Increment / decrement `++ --` (prefix & postfix) | ✅ | |
| Ternary `cond ? a : b` | ✅ | |
| Nullish coalescing `??` | ✅ | Works on `T \| null` and string |
| Optional chaining `?.` | ✅ | Null-guards ptr fields; returns null on null receiver |
| `typeof` operator | ✅ | Compile-time constant; resolved from inferred type |
| `if` / `else if` / `else` | ✅ | |
| `while` loop | ✅ | |
| `do…while` loop | ✅ | |
| `for (init; cond; update)` | ✅ | |
| `for…of` over arrays, `Map` (iterates values), `Set` (iterates elements), and a class implementing `next(): T \| null` | ✅ | No `[key,value]` destructuring in for-of, so Map iterates values, not entries — use `.keys()` for keys; see [ADR-00011](../adr/ADR-00011.md). The class case ([TDD-00009](../tdd/TDD-00009.md) Stage 1a) is a compile-time structural check (no runtime `Symbol.iterator`), and reuses the same "0/null doubles as absent" sentinel convention `.find()` already uses — see [ADR-00063](../adr/ADR-00063.md). |
| `for…in` over object keys | ✅ | |
| `switch` / `case` / `default` / `break` | ✅ | Numeric, string, and boolean discriminants |
| `break` / `continue` in loops, including labeled (`outer: for (...) { break outer; }`) | ✅ | See [ADR-00010](../adr/ADR-00010.md) |
| `return` | ✅ | Typed; `void` implicit return handled |
| `throw new Error(msg)` | ✅ | Via `setjmp` / `longjmp` |
| `try` / `catch` / `finally` | ✅ | Single catch variable; `finally` always runs |
| Optional catch binding (`try {} catch {}`, no bound param) — ES2019 | ❌ | `parser/parser_stmts.go`'s `catch` parsing unconditionally `expect`s `LPAREN` right after the `catch` keyword — a paramless `catch {}` is a parse error, not silently accepted |
| Function declarations (top-level) | ✅ | Named, typed params, typed return |
| Arrow functions / lambdas | ✅ | Full closures; captures via heap-allocated env struct |
| Default parameter values | ✅ | |
| Optional parameters (`param?`) | ✅ | |
| Rest parameters (`...args: number[]`) | ✅ | |
| Spread in array literals `[...a, ...b]` | ✅ | |
| Array destructuring `const [a, b] = arr` | ✅ | Holes are supported (`const [, b] = arr` skips index 0) — but confirmed no default values (`[a = 1]`), no nested patterns (`[[a, b], c]`), and no rest element (`[a, ...rest]`); statement-level only (`let`/`const`/`var` immediately followed by `[`), not usable as a for-of loop variable or anywhere else a pattern is legal in real JS. |
| Object destructuring `const { x, y } = obj` | ✅ | Renaming is supported (`{ x: y }`) — but confirmed no default values (`{ x = 1 }`), no nested patterns, no rest element (`{ ...rest }`), and keys must be a plain identifier (no computed or string-literal keys); same statement-level-only restriction as array destructuring above. |
| Destructured function parameters (`function f({ x, y }) {}` / `function f([a, b]) {}`) | ❌ | `parser/parser_stmts.go`'s `parseParamList` unconditionally `expect`s `IDENT` for every parameter — a destructuring pattern in parameter position is a parse error |
| `async` functions | ✅ | Returns `Promise<T>`; malloc-based slot. Named `async function` declarations and `async (...) => ...` arrow functions both supported (the arrow-function case was a real gap found and fixed alongside [ADR-00050](../adr/ADR-00050.md) — it silently returned its value unwrapped instead of via the Promise slot). |
| `await` expressions | ✅ | Loads value from slot, frees it — except `await` on `fetch()`'s own `Promise<Response>`, which really waits (yielding via a fiber if inside an `http.listen` connection handler) since [ADR-00050](../adr/ADR-00050.md), not just an already-resolved read. |
| `Promise.all` / `.race` / `.allSettled` | ✅ | Over `Array<Promise<Response>>` (fetch()'s own Promise type): real concurrency — N in-flight fetches waited on together via the event loop, not one at a time. Over any other `Array<Promise<T>>`: every element is already resolved by construction (this compiler has no pending promises outside `fetch()`), so these honestly collect/pick/report already-settled values rather than faking parallelism. Each combinator call consumes (frees) every element's own Promise slot, same as a plain `await` — the same array can't be fed to a second combinator call. See [ADR-00073](../adr/ADR-00073.md)/[TDD-00016](../tdd/TDD-00016.md). |
| Enums (numeric) | ✅ | Auto-increment, explicit values |
| Enums (string) | ✅ | |
| Interfaces (structural) | ✅ | Heap-allocated objects |
| Type aliases | ✅ | |
| Object literals `{ key: value }` | ✅ | |
| Getters / setters (`get x() {}` / `set x(v) {}`) on object literals and classes | ❌ | No accessor-property parsing found anywhere in `parser_literals.go` or `parser_classes.go` — `get`/`set` are not recognized as anything other than plain identifiers |
| `new Error(msg)` | ✅ | |
| `new Array<T>(n)` | ✅ | |
| `new Map<K,V>()` | ✅ | |
| `new Set<T>()` | ✅ | |
| `class` (fields, constructor, methods, `this`, `new ClassName(args)`) | ✅ | [TDD-00009](../tdd/TDD-00009.md) Stage 1 — instances reuse the same heap-object/GEP machinery interfaces already use; methods compile to plain static calls (`this` as an implicit first arg), no closure indirection. A class with fields requires an explicit constructor (no field initializer syntax yet — every field must be set explicitly, same philosophy object literals already enforce). See [ADR-00063](../adr/ADR-00063.md). |
| `class` `static` members, private fields (`#x`), `static {}` blocks, `implements`/`abstract` | ❌ | `parser/parser_classes.go`'s own comment lists these as *"deferred to later"* — none are parsed at all today, not just unimplemented at codegen time |
| `class` inheritance (`extends`/`super`) | ❌ | Staged design in [TDD-00009](../tdd/TDD-00009.md) — the only remaining stage that needs new dynamic-dispatch machinery |
| `instanceof` (against user-defined classes) | ✅ | [TDD-00009](../tdd/TDD-00009.md) Stage 2 — every instance carries a hidden runtime type tag. Before inheritance exists, a class-typed variable's concrete class is already known statically, so this folds to a compile-time constant except for an `any`/`unknown`-typed value, where the tag is read back at runtime — the one case that does real work. See [ADR-00067](../adr/ADR-00067.md) for the built-in-type/unregistered-class compile-error behavior and the `Error` subtypes follow-on in [TDD-00013](../tdd/TDD-00013.md). |

## Known Limitations

| Limitation | Notes |
|---|---|
| An unannotated function calling another unannotated function *declared later in the same file*, when the callee returns an object/array/closure/Date | `function makeA() { return makeB() }; function makeB() { return { x: 1 } }` — `makeA`'s own inferred return type is computed once, in source order, before `makeB` has been registered, so `makeA`'s inference sees a not-yet-known callee and falls back to void. Fails cleanly (`field access on non-object`), not silently — a known, accepted boundary of [ADR-00041](../adr/ADR-00041.md)'s single-pass, best-effort inference, not a general fixed-point/multi-pass type inference system. Reorder the declarations, or add an explicit return-type annotation to `makeA`, to work around it. |
| Dividing the minimum representable `i64` value by `-1` is still undefined behavior | `codegen/llvm/emit_exprs.go`'s `sdiv`/`srem` codegen guards against a zero divisor ([ADR-00069](../adr/ADR-00069.md)), but LLVM documents a *second*, separate UB case for signed division/remainder: dividend exactly `i64`'s minimum value (`-9223372036854775808`) with divisor `-1` (the mathematical result, `2^63`, doesn't fit back in an `i64`). Not fixed here — found by inspection while scoping [TDD-00014](../tdd/TDD-00014.md)'s codegen fuzzer's arithmetic oracle, not by an actual repro; reaching this exact value by chance is astronomically unlikely (1-in-2^64), so it wasn't something worth guarding preemptively in this pass. Would need the same zero-check-style guard `emitDivZeroGuard` already uses, extended to also special-case this one input pair. |
