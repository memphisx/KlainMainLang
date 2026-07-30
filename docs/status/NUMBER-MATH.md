# Number / Math

> Part of the [Implementation Status](README.md) index.

**Coverage**: 100% (35/35).

**Caveats**: None open — `Number.prototype.toString(radix)`/`toPrecision`/`toExponential` are `sprintf`-based with a couple of cosmetic deviations from real JS (exponent padding, e.g. `e+05` vs `e+5`) — see [ADR-00065](../adr/ADR-00065.md).

| Feature | Status |
|---|---|
| `Number.isInteger(x)` | ✅ |
| `Number.isFinite(x)` | ✅ |
| `Number.isNaN(x)` | ✅ |
| `Number.isSafeInteger(x)` | ✅ |
| `Number.parseInt(s)` | ✅ |
| `Number.parseFloat(s)` | ✅ |
| `Number.MAX_SAFE_INTEGER` | ✅ |
| `Number.MIN_SAFE_INTEGER` | ✅ |
| `Number.EPSILON` | ✅ |
| `Number.MAX_VALUE` | ✅ |
| `Number.MIN_VALUE` | ✅ |
| `Number.POSITIVE_INFINITY` | ✅ |
| `Number.NEGATIVE_INFINITY` | ✅ |
| `Number.NaN` | ✅ |
| `Number.prototype.toFixed(n)` | ✅ |
| `Number.prototype.toString(radix?)` | ✅ (hand-rolled digit loop; truncates a non-integer receiver to its integer part first; radix trusted, not validated. See [ADR-00065](../adr/ADR-00065.md) for this and the two rows below — same ADR, same `sprintf`-based approach, same cosmetic-only deviations from real JS.) |
| `Number.prototype.toPrecision(n)` | ✅ (`sprintf("%#.*g", ...)`; exponent pads to 2 digits — `e+05` vs real JS's `e+5`.) |
| `Number.prototype.toExponential(n)` | ✅ (`sprintf("%.*e", ...)`; same exponent-padding deviation.) |
| `parseInt(s, radix?)` (global) | ✅ |
| `parseFloat(s)` (global) | ✅ |
| `isNaN(x)` (global) | ✅ |
| `isFinite(x)` (global) | ✅ |
| `Math.floor/ceil/round/trunc` | ✅ |
| `Math.abs` | ✅ |
| `Math.sqrt/pow/hypot` | ✅ |
| `Math.log/log2/log10` | ✅ |
| `Math.sin/cos/tan` | ✅ |
| `Math.min/max` | ✅ |
| `Math.sign` | ✅ |
| `Math.random()` | ✅ |
| `Math.PI/E/LN2/LN10/SQRT2/LOG2E/LOG10E` | ✅ |
| `Math.cbrt/expm1/log1p` | ✅ |
| `Math.asin/acos/atan/atan2` | ✅ |
| `Math.sinh/cosh/tanh` | ✅ |
| `Math.clz32/fround/imul` | ✅ (`clz32` via LLVM's own `llvm.ctlz.i32` intrinsic; `fround` via an `fptrunc`/`fpext` float32 round-trip; `imul` via 32-bit `mul` + sign-extend, giving real 32-bit-wraparound integer multiplication distinct from plain `*`'s double-precision result. See [ADR-00065](../adr/ADR-00065.md).) |
