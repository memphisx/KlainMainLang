# Number / Math

> Part of the [Implementation Status](README.md) index.

**Coverage**: 35/35 (100%) · **Strict Coverage**: 26/35 (~74%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `Number.isInteger(x)` | ✅ | • `Number.isInteger(Infinity)`/`(-Infinity)` return `true` — should be `false`; `emitNumberIsInteger` checks `floor(x) == x` only, never finiteness, and `floor(Infinity) == Infinity` is trivially true ([ADR-00166](../adr/ADR-00166.md)) | |
| `Number.isFinite(x)` | ✅ | | |
| `Number.isNaN(x)` | ✅ | | |
| `Number.isSafeInteger(x)` | ✅ | | |
| `Number.parseInt(s)` | ✅ | • No hex auto-detect when radix is omitted — `"0x1F"` parses as `0`, not `31` ([ADR-00287](../adr/ADR-00287.md)) | • Returns a double (as real JS) so a no-digits input is a real `NaN` — endptr-checked `strtoll` ([ADR-00287](../adr/ADR-00287.md)) |
| `Number.parseFloat(s)` | ✅ | • Inherits `strtod` extras — `"inf"` parses to `Infinity` (JS only accepts the full word `Infinity`) and C hex-float syntax parses ([ADR-00287](../adr/ADR-00287.md)) | • A no-conversion input returns a real `NaN` via the endptr check ([ADR-00287](../adr/ADR-00287.md)) |
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
| `parseInt(s, radix?)` (global) | ✅ | • Same residual as `Number.parseInt(s)` above — no hex auto-detect when radix is omitted ([ADR-00287](../adr/ADR-00287.md)) | • No-digits input → real `NaN` ([ADR-00287](../adr/ADR-00287.md)) |
| `parseFloat(s)` (global) | ✅ | • Same `strtod` extras as `Number.parseFloat(s)` above ([ADR-00287](../adr/ADR-00287.md)) | • No-conversion input → real `NaN` ([ADR-00287](../adr/ADR-00287.md)) |
| `isNaN(x)` (global) | ✅ | | |
| `isFinite(x)` (global) | ✅ | | |
| `Math.floor/ceil/round/trunc` | ✅ | | • A float input stays a double end-to-end, so `NaN`/`±Infinity` pass through unchanged; `Math.round` uses JS's tie-toward-`+Infinity` (`Math.round(-4.5) === -4`) incl. the `-0` result for `Math.round(-0.5)` ([ADR-00286](../adr/ADR-00286.md)); integer input keeps the exact-i64 path |
| `Math.abs` | ✅ | | |
| `Math.sqrt/pow/hypot` | ✅ | | |
| `Math.log/log2/log10` | ✅ | | |
| `Math.sin/cos/tan` | ✅ | | |
| `Math.min/max` | ✅ | | • Any float argument promotes the fold to `llvm.minimum`/`llvm.maximum` — `NaN` propagates and `-0.0` orders below `+0.0`, as the JS spec; all-integer calls stay exact i64 ([ADR-00286](../adr/ADR-00286.md)) |
| `Math.sign` | ✅ | | • Float path returns ±1.0 or the input itself (`NaN` stays `NaN`, a signed zero keeps its sign); integer path exact i64 ([ADR-00286](../adr/ADR-00286.md)) |
| `Math.random()` | ✅ | | |
| `Math.PI/E/LN2/LN10/SQRT2/LOG2E/LOG10E` | ✅ | | |
| `Math.cbrt/expm1/log1p` | ✅ | | • `cbrt` uses a deterministic, correctly-rounded fdlibm implementation (`@__kml_cbrt`) rather than platform libm, whose runtime `cbrt` is not reliably correctly-rounded and diverged by OS (glibc `cbrt(27)` → `3.0000000000000004`); `expm1`/`log1p` delegate to libm ([ADR-00242](../adr/ADR-00242.md)) |
| `Math.asin/acos/atan/atan2` | ✅ | | |
| `Math.sinh/cosh/tanh` | ✅ | | |
| `Math.clz32/fround/imul` | ✅ | | • `clz32` via LLVM's own `llvm.ctlz.i32` intrinsic; `fround` via an `fptrunc`/`fpext` float32 round-trip; `imul` via 32-bit `mul` + sign-extend, giving real 32-bit-wraparound integer multiplication distinct from plain `*`'s double-precision result ([ADR-00065](../adr/ADR-00065.md)) |
