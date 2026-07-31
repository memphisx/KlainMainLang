# Language Constructs

> Part of the [Implementation Status](README.md) index. Covers control flow, operators, variable declarations, functions/closures, async/Promise, enums, interfaces, and classes/OOP.

**Coverage**: 56/60 rows on this page, ~93% (see the index's Coverage Summary for the breakdown by sub-category).

**Caveats**: TDD-00009 is now fully implemented (Stages 0-4) — `class` has real single inheritance (`extends`/`super`), `static` members/`static {}` blocks, `private`/`protected` visibility (compile-time-only, matching real TypeScript's own erasure), `abstract` classes/methods, and `implements` (a compile-time-only structural self-check, not polymorphic dispatch through an interface type) — see [ADR-00083](../adr/ADR-00083.md) (Stage 3) and [ADR-00084](../adr/ADR-00084.md) (Stage 4). Real JS/TS `#x` runtime-private field syntax is a *different* mechanism from the `private` keyword modifier and remains unimplemented — TDD-00009 Stage 4 explicitly scoped the keyword-modifier form as the intended mechanism, matching real TypeScript's own compile-time-only privacy. `async`/`await` is a synchronous resolved-slot read for everything except `await fetch(...)`, which is genuinely non-blocking (yields via a fiber inside an `http.listen` handler) — see [TDD-00006](../tdd/TDD-00006.md). Numeric separators, optional catch binding, and logical assignment operators are now done — see [ADR-00085](../adr/ADR-00085.md)/[ADR-00086](../adr/ADR-00086.md)/[ADR-00087](../adr/ADR-00087.md). Getters/setters, tagged templates, and destructured function parameters remain confirmed absent directly against the lexer/parser, with no tracking anywhere else yet.

| Feature | Status | Notes |
|---|---|---|
| `const` / `let` / `var` declarations | ✅ | All three treated as mutable allocas |
| Numeric literals (`42`, `3.14`, `0xFF`, `0b101`, `0o77`) | ✅ | |
| Numeric separators (`1_000_000`) | ✅ | Stripped at lex time — every downstream consumer (parser, codegen) sees a clean literal, never the `_`. A leading/trailing/doubled separator is a clean lexer error. See [ADR-00085](../adr/ADR-00085.md). |
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
| Logical assignment operators `&&= \|\|= ??=` (ES2021) | ✅ | Genuinely short-circuiting (unlike this compiler's own `&&`/`\|\|`, which eagerly evaluate both sides) — the right side is only evaluated down the branch the operator's own rule requires. Works against scalar variables, array elements, object fields, and static class fields; computed-key dynamic-object bracket assignment is a clean compile error, not built. See [ADR-00087](../adr/ADR-00087.md). |
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
| Optional catch binding (`try {} catch {}`, no bound param) — ES2019 | ✅ | The `(e)` clause is now optional — `emit_exceptions.go` already handled an empty `Param` correctly, so this was a parser-only change. See [ADR-00086](../adr/ADR-00086.md). |
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
| Built-in `Error` subtypes (`new TypeError(msg)`, `RangeError`, `SyntaxError`, `EvalError`, `URIError`, `ReferenceError`) and `instanceof` against them | ✅ | [TDD-00013](../tdd/TDD-00013.md) Option A — a small fixed kind enum (a hidden runtime tag on the same shared `Error` object shape, not a real class hierarchy), plus a real `.name` field. `instanceof Error` matches any kind; `instanceof TypeError` etc. matches only that one. No user-definable `class X extends Error` — built-in types still aren't valid `extends` targets even now that class inheritance itself exists ([TDD-00009](../tdd/TDD-00009.md) Stage 3, [ADR-00083](../adr/ADR-00083.md)); Error subtyping stays on its own independent Option A mechanism. See [ADR-00082](../adr/ADR-00082.md). |
| `new Array<T>(n)` | ✅ | |
| `new Map<K,V>()` | ✅ | |
| `new Set<T>()` | ✅ | |
| `class` (fields, constructor, methods, `this`, `new ClassName(args)`) | ✅ | [TDD-00009](../tdd/TDD-00009.md) Stage 1 — instances reuse the same heap-object/GEP machinery interfaces already use; methods compile to plain static calls (`this` as an implicit first arg), no closure indirection. A class with fields requires an explicit constructor (no field initializer syntax yet — every field must be set explicitly, same philosophy object literals already enforce). See [ADR-00063](../adr/ADR-00063.md). |
| `class` `static` members/`static {}` blocks, `private`/`protected` visibility, `abstract` classes/methods, `implements` | ✅ | [TDD-00009](../tdd/TDD-00009.md) Stage 4 — `static` members are inherited through `extends` like real JS/TS (a non-redeclared static field shares its base's storage, not a per-subclass copy); `private`/`protected` are compile-time-only (zero runtime check, matching real TypeScript's own erasure) and correctly reject `super.privateMethod()`; `abstract` reuses Stage 3's override-detection machinery almost entirely for free; `implements` is a compile-time-only structural self-check against a new interface-method-signature grammar, not a mechanism for polymorphic dispatch through an interface-typed reference. See [ADR-00084](../adr/ADR-00084.md). |
| Real JS/TS `#x` runtime-private field syntax | ❌ | A distinct mechanism from the `private` keyword modifier above (which TDD-00009 Stage 4 explicitly scoped as the intended privacy mechanism, matching real TypeScript's own compile-time-only erasure) — no lexer support for `#`-prefixed identifiers at all. Scoped, not started — see [TDD-00021](../tdd/TDD-00021.md). |
| `class` inheritance (`extends`/`super`) | ✅ | [TDD-00009](../tdd/TDD-00009.md) Stage 3 — single inheritance, base-first field layout, `super(args)`/`super.method(args)`, an implicit pass-through constructor when a derived class adds no fields of its own. Dispatch is static (a plain direct call, identical to Stage 1/2) except for a method provably overridden somewhere in the whole program, which goes through a per-tree vtable instead — decided once at compile time via whole-program override analysis, not per call site. See [ADR-00083](../adr/ADR-00083.md). |
| `instanceof` (against user-defined classes) | ✅ | [TDD-00009](../tdd/TDD-00009.md) Stages 2-3 — every instance carries a hidden runtime type tag. A class-typed variable whose static class is `T` itself or an ancestor of `T` folds to a compile-time constant; one whose static class is an *ancestor* of the right-hand class (e.g. `const s: Shape = new Circle(...); s instanceof Circle`) needs a real runtime tag read, since the concrete subtype isn't known until then — same real check an `any`/`unknown`-typed value already needed. See [ADR-00067](../adr/ADR-00067.md) for the built-in-type/unregistered-class compile-error behavior and [ADR-00083](../adr/ADR-00083.md) for the inheritance generalization; `instanceof` against `Error`/`TypeError`/etc. is a separate, sibling mechanism keyed off a different hidden tag — see the `Error` subtypes row above and [TDD-00013](../tdd/TDD-00013.md). |

## Known Limitations

| Limitation | Notes |
|---|---|
| An unannotated function calling another unannotated function *declared later in the same file*, when the callee returns an object/array/closure/Date | `function makeA() { return makeB() }; function makeB() { return { x: 1 } }` — `makeA`'s own inferred return type is computed once, in source order, before `makeB` has been registered, so `makeA`'s inference sees a not-yet-known callee and falls back to void. Fails cleanly (`field access on non-object`), not silently — a known, accepted boundary of [ADR-00041](../adr/ADR-00041.md)'s single-pass, best-effort inference, not a general fixed-point/multi-pass type inference system. Reorder the declarations, or add an explicit return-type annotation to `makeA`, to work around it. |
| Dividing the minimum representable `i64` value by `-1` is still undefined behavior | `codegen/llvm/emit_exprs.go`'s `sdiv`/`srem` codegen guards against a zero divisor ([ADR-00069](../adr/ADR-00069.md)), but LLVM documents a *second*, separate UB case for signed division/remainder: dividend exactly `i64`'s minimum value (`-9223372036854775808`) with divisor `-1` (the mathematical result, `2^63`, doesn't fit back in an `i64`). Not fixed here — found by inspection while scoping [TDD-00014](../tdd/TDD-00014.md)'s codegen fuzzer's arithmetic oracle, not by an actual repro; reaching this exact value by chance is astronomically unlikely (1-in-2^64), so it wasn't something worth guarding preemptively in this pass. Would need the same zero-check-style guard `emitDivZeroGuard` already uses, extended to also special-case this one input pair. |
| A `T \| null` where `T` is a non-pointer type (e.g. `number \| null`) has no real "is this null" runtime check | Both bare `??` (`emitNullCoalesce`) and the new `??=` ([ADR-00087](../adr/ADR-00087.md)) treat a non-pointer-typed value as never-null, so `let x: number \| null = null; x ?? 42` silently evaluates to `0`, not `42`. Found while building `??=`, not fixed — inherited as-is from `??`'s pre-existing behavior. A genuine fix needs a different runtime representation for nullable non-pointer types, not a narrow patch. |
| A class iterator's `next(): number \| null` whose legitimate first value is exactly `0` terminates immediately | The Stage 1a iterator protocol ([ADR-00063](../adr/ADR-00063.md)) uses a bare `0` as both "the number zero" and "iteration done," since nullable numbers have no separate null representation (same root cause as the row above). Affects both plain `for...of` over such a class and [`Array.from`](ARRAY-METHODS.md) draining one ([ADR-00088](../adr/ADR-00088.md)). Found while verifying `Array.from`, not fixed — work around by choosing an iterator whose real values never include `0` (e.g. start counting from 1), or fix would need the same different nullable-number representation as the row above. |
