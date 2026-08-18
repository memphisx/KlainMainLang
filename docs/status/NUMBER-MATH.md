# Number / Math

> Part of the [Implementation Status](README.md) index.

**Coverage**: 35/35 (100%) · **Strict Coverage**: 23/35 (~66%).

This page follows the shared status-page format ([Status page format](README.md#status-page-format)): **Status** is a bare ✅/❌; **Caveats** lists behavioral divergences from real JS/TS (a non-empty Caveats cell is what excludes an otherwise-✅ row from Strict Coverage); **Notes** carries implementation/representation detail only. One table per index category; each category's figures above derive from its table below.

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `Number.isInteger(x)` | ✅ | • `Number.isInteger(Infinity)`/`(-Infinity)` return `true` — should be `false`; `emitNumberIsInteger` checks `floor(x) == x` only, never finiteness, and `floor(Infinity) == Infinity` is trivially true ([ADR-00166](../adr/ADR-00166.md)) | |
| `Number.isFinite(x)` | ✅ | | |
| `Number.isNaN(x)` | ✅ | | |
| `Number.isSafeInteger(x)` | ✅ | | |
| `Number.parseInt(s)` | ✅ | • Invalid input returns `0` instead of `NaN` — `strtoll`'s C failure signal (0) is indistinguishable from a real zero, and the result type is hardcoded `i64`, which can't represent `NaN` at all<br>• No radix argument defaults `strtoll`'s base to `10` instead of `0`, so `"0x1F"` parses as `0` instead of auto-detecting hex and returning `31` ([ADR-00166](../adr/ADR-00166.md)) | |
| `Number.parseFloat(s)` | ✅ | • Invalid input returns `0` instead of `NaN` — calls `strtod` with a null `endptr`, never checking whether any conversion actually happened; unlike `parseInt`'s issue this one has no structural excuse since `TypeF64` can represent real `NaN` just fine ([ADR-00166](../adr/ADR-00166.md)) | |
| `Number.MAX_SAFE_INTEGER` | ✅ | | |
| `Number.MIN_SAFE_INTEGER` | ✅ | | |
| `Number.EPSILON` | ✅ | | |
| `Number.MAX_VALUE` | ✅ | | |
| `Number.MIN_VALUE` | ✅ | | |
| `Number.POSITIVE_INFINITY` | ✅ | | |
| `Number.NEGATIVE_INFINITY` | ✅ | | |
| `Number.NaN` | ✅ | | |
| `Number.prototype.toFixed(n)` | ✅ | • The `digits` argument is required here, not optional — `(3.14159).toFixed()` hard compile-errors with "toFixed takes exactly 1 argument"; real JS defaults `digits` to `0` ([ADR-00166](../adr/ADR-00166.md)) | |
| `Number.prototype.toString(radix?)` | ✅ | • Truncates a non-integer receiver to its integer part first (real JS renders fractional digits)<br>• Radix trusted, not validated (real JS throws a `RangeError` for an out-of-range radix)<br>• Minor cosmetic deviations from real JS, the same `sprintf`-based exponent padding as the two rows below ([ADR-00065](../adr/ADR-00065.md)) | • Hand-rolled digit loop |
| `Number.prototype.toPrecision(n)` | ✅ | • Exponent pads to 2 digits — `e+05` vs real JS's `e+5` ([ADR-00065](../adr/ADR-00065.md)) | • `sprintf("%#.*g", ...)` |
| `Number.prototype.toExponential(n)` | ✅ | • Same exponent-padding deviation — `e+05` vs `e+5` ([ADR-00065](../adr/ADR-00065.md)) | • `sprintf("%.*e", ...)` |
| `parseInt(s, radix?)` (global) | ✅ | • Same bug as `Number.parseInt(s)` above — invalid input returns `0` not `NaN`, no hex auto-detect when radix is omitted ([ADR-00166](../adr/ADR-00166.md)) | |
| `parseFloat(s)` (global) | ✅ | • Same bug as `Number.parseFloat(s)` above — invalid input returns `0` not `NaN` ([ADR-00166](../adr/ADR-00166.md)) | |
| `isNaN(x)` (global) | ✅ | | |
| `isFinite(x)` (global) | ✅ | | |
| `Math.floor/ceil/round/trunc` | ✅ | • NaN or ±Infinity input produces non-deterministic garbage, not the correct passthrough — `emitMathRound` unconditionally does `fptosi double %rounded to i64` after the libm call, LLVM undefined behavior for a NaN or out-of-i64-range double<br>• `Math.round(-4.5)` returns `-5`, forwarding libm `round()`'s away-from-zero tie-break, where real JS always rounds half toward `+Infinity` (`Math.round(-4.5) === -4`) ([ADR-00166](../adr/ADR-00166.md)) | |
| `Math.abs` | ✅ | | |
| `Math.sqrt/pow/hypot` | ✅ | | |
| `Math.log/log2/log10` | ✅ | | |
| `Math.sin/cos/tan` | ✅ | | |
| `Math.min/max` | ✅ | • `NaN` is not propagated — uses ordered `fcmp ogt`/`fcmp olt`, always `false` against `NaN`, so `Math.max(NaN, 1)` silently returns `1` instead of `NaN`<br>• When the accumulator's type is fixed to `i64` by an integer-literal first argument, a later `NaN` argument triggers undefined-behavior garbage via `fptosi double NaN to i64` ([ADR-00166](../adr/ADR-00166.md)) | |
| `Math.sign` | ✅ | • `Math.sign(NaN)` produces non-deterministic garbage instead of `NaN` — unconditionally coerces to `i64` via `fptosi`, undefined behavior for a NaN double ([ADR-00166](../adr/ADR-00166.md)) | |
| `Math.random()` | ✅ | | |
| `Math.PI/E/LN2/LN10/SQRT2/LOG2E/LOG10E` | ✅ | | |
| `Math.cbrt/expm1/log1p` | ✅ | | |
| `Math.asin/acos/atan/atan2` | ✅ | | |
| `Math.sinh/cosh/tanh` | ✅ | | |
| `Math.clz32/fround/imul` | ✅ | | • `clz32` via LLVM's own `llvm.ctlz.i32` intrinsic; `fround` via an `fptrunc`/`fpext` float32 round-trip; `imul` via 32-bit `mul` + sign-extend, giving real 32-bit-wraparound integer multiplication distinct from plain `*`'s double-precision result ([ADR-00065](../adr/ADR-00065.md)) |
