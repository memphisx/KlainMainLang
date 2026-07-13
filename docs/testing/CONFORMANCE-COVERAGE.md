# External conformance coverage

Tracks how much of two official external conformance suites has been reviewed for this project, category by category, as an external benchmark for this compiler's own E2E test suite (`compiler_test.go`): Microsoft's TypeScript conformance suite (`microsoft/TypeScript`, `tests/cases/conformance/`) and TC39's Test262 (`tc39/test262`, `test/`). Full rationale for why the two suites need different treatment (one is type-checker output, the other is execution-based) — see `docs/tdd/TDD-00008.md`.

**How to read the Status column**: "Scanned" means representative upstream files were read for edge-case ideas; it does not necessarily mean tests were ported — the TypeScript suite isn't directly portable at all (see the TDD), while Test262 often is. "Tests derived" counts new `compiler_test.go` E2E tests written *because of* something noticed while scanning a category, not a 1:1 port count.

## TypeScript conformance suite (`tests/cases/conformance/`)

Type-checker output only — no expected-stdout oracle anywhere in the suite. Categories here are reviewed for *edge-case ideas*, never ported directly.

| Category | In scope? | Status | Tests derived | Notes |
|---|---|---|---|---|
| `expressions/binaryOperators/arithmeticOperator` | Yes | Scanned | 0 | Upstream files are pure type-declaration matrices (`any`/`number` × every operator), no concrete executed values. The exhaustive any/number × `<<`/`>>`/`>>>`/etc. layout directly maps to the shift-semantics bug already found and tracked in `STATUS.md`'s Known Limitations table — worth deriving concrete shift/arithmetic edge-case tests from once that bug is fixed (Test262's `left-shift`/`right-shift`/`unsigned-right-shift`, below, are the actual source for those values). |
| `expressions/binaryOperators/additionOperator` | Yes | Scanned | 0 | Same pattern: type-only, no concrete string+number coercion values to port directly. Worth hand-deriving concrete `1 + "2"`-style tests separately. |
| `expressions/binaryOperators/comparisonOperator` | Yes | Scanned | 0 | Same pattern; mostly about type-assignability of `==`/`<`/etc. between structurally different object types (classes, interfaces) — largely not applicable since it's assignability-focused, not value-focused. |
| `expressions/binaryOperators/logicalAndOperator`, `logicalOrOperator` | Yes | Not started | 0 | Not yet read in detail. |
| `expressions/binaryOperators/inOperator` | No (not yet) | Out of scope | 0 | `in` operator is ❌ in `STATUS.md`. |
| `expressions/binaryOperators/instanceofOperator` | No (not yet) | Out of scope | 0 | `instanceof` is ❌ in `STATUS.md`. |
| `statements/forStatements` | Yes | Scanned | 0 | Upstream file is entirely empty-bodied `for(var x: T = v;;){}` declarations testing loop-variable type compatibility, several using generic classes — nothing executable to derive from as-is. |
| `statements/switchStatements` | Yes | Scanned | 0 | Upstream file is a single `switch` exercising case-type-assignability across classes/interfaces/generics with no `case` bodies at all — not runtime-observable, and mostly exercises out-of-scope features (classes, generics). |
| `statements/tryStatements` | Yes | Scanned | 0 | `catchClauseWithTypeAnnotation.ts` tests which catch-clause type annotations the checker accepts (`any`/`unknown` only) via comments (`// should be OK`), not runtime assertions. Loosely relevant to this compiler's own `any`/`unknown` catch-clause handling but not portable as-is. |
| `statements/for-ofStatements`, `for-inStatements` | Yes | Not started | 0 | Not yet read. |
| `types/any`, `types/unknown` | Yes | Not started | 0 | Highest-relevance unscanned category — this compiler has its own Staged V1 `any`/`unknown` (`docs/adr/ADR-00008.md`) with known scope boundaries; upstream test names may still suggest edge cases worth checking even though upstream assertions themselves won't port. |
| `types/union` | Partial (`T \| null` only) | Not started | 0 | This compiler doesn't support general unions yet (❌ in `STATUS.md`); upstream tests here are almost entirely about multi-member unions, likely mostly out of scope until then. |
| `controlFlow` (narrowing) | No (not really) | Out of scope for now | 0 | Sampled category description confirms it's entirely about the type checker's flow-based type narrowing (e.g. `typeof`/truthiness guards refining a union's members) — this compiler doesn't do general type narrowing, so almost the entire category is inapplicable until it does. |
| `types/{tuple,mapped,conditional,intersection,keyof,typeParameters,...}` | No | Out of scope | 0 | Generics/advanced type-system features this compiler doesn't implement. |
| `classes`, `decorators`, `esDecorators` | No | Out of scope | 0 | `class` is 0% implemented. |
| `moduleResolution`, `internalModules`, `externalModules` | No (mostly) | Out of scope | 0 | This compiler's module support (`docs/adr/ADR-00022.md`) is a deliberately narrow whole-program-compile subset; upstream module-resolution edge cases (ambient modules, `paths`, re-exports) are almost all out of scope. |

## Test262 (`test/`)

Execution-based — most files assert concrete runtime values, a direct structural match for this project's own E2E convention. Categories exercising JS's dynamic/prototype-based object model (which this compiler deliberately doesn't have) are marked out of scope rather than chased.

| Category | In scope? | Status | Tests derived | Notes |
|---|---|---|---|---|
| `language/expressions/left-shift` | Yes | Ported | 3 | 41 of 45 files use the simple, harness-free `if (x !== y) throw new Test262Error(...)` pattern — directly portable. `S9.5_A2.1_T1.js`'s and `S11.7.1_A4_T2.js`'s `ToInt32` wraparound values (e.g. `2147483648 << 0 === -2147483648`) used directly as verification values for `docs/adr/ADR-00047.md`'s shift-semantics fix. |
| `language/expressions/right-shift` | Yes | Ported | 2 | Same portability profile (33 of 37 harness-free). `>>` (`ashr`)-side values used for `ADR-00047`'s fix (sign-extending arithmetic shift, e.g. `-8 >> 1 === -4`). |
| `language/expressions/unsigned-right-shift` | Yes | Ported | 1 | Same portability profile (41 of 45 harness-free); `S9.6_A2.1.js`'s `ToUint32` wraparound values (`-1 >>> 0 === 4294967295`, `4294967296 >>> 0 === 0`) used directly as verification values for `ADR-00047`'s fix. |
| `language/expressions/bitwise-and`, `bitwise-or`, `bitwise-xor` | Yes | Scanned | 0 | Same portability profile (26 of 30 harness-free per category). Not yet needed for a known bug (no divergence found there so far), but cheap to add once the shift fix's test infrastructure exists. |
| `language/expressions/bitwise-not` | Yes | Scanned | 0 | Same profile (15 of 16 harness-free), smaller category. |
| `built-ins/Array/length` | No (mostly) | Scanned | 0 | Wrong category for the array-bounds bug: 13 of 30 files test JS's *dynamic, writable* `.length` property (`RangeError` on overflow, `defineProperty`/`writable`/`configurable` semantics) — behavior with no equivalent in this compiler's fixed-buffer arrays (no writable `.length`, no sparse holes, no `RangeError`). A bounds-check fix for the out-of-bounds bug should almost certainly reject/throw, not replicate JS's silent dynamic growth — this category would be actively misleading as a source of "correct" behavior to match. |
| `built-ins/Array/from` | No | Out of scope | 0 | `Array.from` isn't implemented at all (❌ in `STATUS.md`); 26 of 47 files are `Symbol`/`Proxy`/`Iterator`-protocol-dependent regardless. |
| `built-ins/Array/isArray` | No (not useful) | Out of scope | 0 | Tests a dynamic-typing runtime check; this compiler's array-ness is a static type on every variable, not a runtime tag to query. |

## Next up

Test262's shift categories are done — their pre-computed values were ported into `compiler_test.go` as part of `docs/adr/ADR-00047.md`'s shift-semantics fix. The `bitwise-and`/`bitwise-or`/`bitwise-xor`/`bitwise-not` categories are still just scanned, not ported (no divergence found there so far, so no forcing function yet) — cheap to pick up opportunistically. On the TypeScript-suite side, finish scanning `expressions/binaryOperators/{logicalAndOperator,logicalOrOperator}`, `statements/{for-ofStatements,for-inStatements}`, and `types/{any,unknown}`.
