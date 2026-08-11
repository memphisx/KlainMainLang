# Number / Math

> Part of the [Implementation Status](README.md) index.

**Coverage**: 100% (35/35).

**Strict Coverage**: 22/35, ~63% — a row only counts here if it was independently repro-verified with zero known caveats or bugs, of any severity. See the 2026-08-11 audit ([ADR-00166](../adr/ADR-00166.md)) that produced this number and the new caveats below; every caveat found by that audit excludes the row from this count even though the row stays ✅ in the Coverage column above.

**Caveats**: None open — `Number.prototype.toString(radix)`/`toPrecision`/`toExponential` are `sprintf`-based with a couple of cosmetic deviations from real JS (exponent padding, e.g. `e+05` vs `e+5`) — see [ADR-00065](../adr/ADR-00065.md).

| Feature | Status |
|---|---|
| `Number.isInteger(x)` | ✅ (`Number.isInteger(Infinity)`/`(-Infinity)` return `true` — should be `false`. `emitNumberIsInteger` checks `floor(x) == x` only, never finiteness, and `floor(Infinity) == Infinity` is trivially true. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md).) |
| `Number.isFinite(x)` | ✅ |
| `Number.isNaN(x)` | ✅ |
| `Number.isSafeInteger(x)` | ✅ |
| `Number.parseInt(s)` | ✅ (invalid input returns `0` instead of `NaN` — `strtoll`'s C failure signal (0) is indistinguishable from a real zero, and the result type is hardcoded `i64`, which can't represent `NaN` at all; no radix argument defaults `strtoll`'s base to `10` instead of `0`, so `"0x1F"` parses as `0` instead of auto-detecting hex and returning `31`. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md).) |
| `Number.parseFloat(s)` | ✅ (invalid input returns `0` instead of `NaN` — calls `strtod` with a null `endptr`, never checking whether any conversion actually happened, unlike `parseInt`'s issue this one has no structural excuse since `TypeF64` can represent real `NaN` just fine. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md).) |
| `Number.MAX_SAFE_INTEGER` | ✅ |
| `Number.MIN_SAFE_INTEGER` | ✅ |
| `Number.EPSILON` | ✅ |
| `Number.MAX_VALUE` | ✅ |
| `Number.MIN_VALUE` | ✅ |
| `Number.POSITIVE_INFINITY` | ✅ |
| `Number.NEGATIVE_INFINITY` | ✅ |
| `Number.NaN` | ✅ |
| `Number.prototype.toFixed(n)` | ✅ (the `digits` argument is required here, not optional — `(3.14159).toFixed()` hard compile-errors with "toFixed takes exactly 1 argument"; real JS defaults `digits` to `0`. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md).) |
| `Number.prototype.toString(radix?)` | ✅ (hand-rolled digit loop; truncates a non-integer receiver to its integer part first; radix trusted, not validated. See [ADR-00065](../adr/ADR-00065.md) for this and the two rows below — same ADR, same `sprintf`-based approach, same cosmetic-only deviations from real JS.) |
| `Number.prototype.toPrecision(n)` | ✅ (`sprintf("%#.*g", ...)`; exponent pads to 2 digits — `e+05` vs real JS's `e+5`.) |
| `Number.prototype.toExponential(n)` | ✅ (`sprintf("%.*e", ...)`; same exponent-padding deviation.) |
| `parseInt(s, radix?)` (global) | ✅ (same bug as `Number.parseInt(s)` above — invalid input returns `0` not `NaN`, no hex auto-detect when radix is omitted. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md).) |
| `parseFloat(s)` (global) | ✅ (same bug as `Number.parseFloat(s)` above — invalid input returns `0` not `NaN`. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md).) |
| `isNaN(x)` (global) | ✅ |
| `isFinite(x)` (global) | ✅ |
| `Math.floor/ceil/round/trunc` | ✅ (NaN or ±Infinity input produces non-deterministic garbage, not the correct passthrough — `emitMathRound` unconditionally does `fptosi double %rounded to i64` after the libm call, which is LLVM undefined behavior for a NaN or out-of-i64-range double. Also, independent of that: `Math.round(-4.5)` returns `-5`, forwarding libm `round()`'s away-from-zero tie-break, where real JS always rounds half toward `+Infinity` (`Math.round(-4.5) === -4`). Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md).) |
| `Math.abs` | ✅ |
| `Math.sqrt/pow/hypot` | ✅ |
| `Math.log/log2/log10` | ✅ |
| `Math.sin/cos/tan` | ✅ |
| `Math.min/max` | ✅ (`NaN` is not propagated — uses ordered `fcmp ogt`/`fcmp olt`, which are always `false` against `NaN`, so `Math.max(NaN, 1)` silently returns `1` instead of `NaN`. When the accumulator's type is fixed to `i64` by an integer-literal first argument, a later `NaN` argument also triggers undefined-behavior garbage via `fptosi double NaN to i64`. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md).) |
| `Math.sign` | ✅ (`Math.sign(NaN)` produces non-deterministic garbage instead of `NaN` — unconditionally coerces to `i64` via `fptosi`, undefined behavior for a NaN double. Found by the 2026-08-11 audit. See [ADR-00166](../adr/ADR-00166.md).) |
| `Math.random()` | ✅ |
| `Math.PI/E/LN2/LN10/SQRT2/LOG2E/LOG10E` | ✅ |
| `Math.cbrt/expm1/log1p` | ✅ |
| `Math.asin/acos/atan/atan2` | ✅ |
| `Math.sinh/cosh/tanh` | ✅ |
| `Math.clz32/fround/imul` | ✅ (`clz32` via LLVM's own `llvm.ctlz.i32` intrinsic; `fround` via an `fptrunc`/`fpext` float32 round-trip; `imul` via 32-bit `mul` + sign-extend, giving real 32-bit-wraparound integer multiplication distinct from plain `*`'s double-precision result. See [ADR-00065](../adr/ADR-00065.md).) |
