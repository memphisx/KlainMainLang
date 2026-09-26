<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/number-math.json; edit the JSON, then run `make status`. -->

# Number / Math

> Part of the [Implementation Status](README.md) index.

**Coverage**: 36/36 (100%) · **Strict Coverage**: 30/36 (~83%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `Number.isInteger(x)` | ✅ | | • `false` for any non-finite value (`Infinity`/`-Infinity`/`NaN`) — the whole-number test is gated on a finiteness check ([ADR-00531](../adr/ADR-00531.md)) |
| `Number.isFinite(x)` | ✅ | | |
| `Number.isNaN(x)` | ✅ | | • All four `Number.isX` predicates are `false` for an absent value — an out-of-range `xs[i]`, a `Map.get` miss, an omitted optional parameter — where the global `isNaN`/`isFinite` ToNumber it first (`isNaN(undefined)` is `true`) ([ADR-01043](../adr/ADR-01043.md)) |
| `Number.isSafeInteger(x)` | ✅ | | |
| `Number.parseInt(s)` | ✅ | | • Returns a double (as real JS) so a no-digits input is a real `NaN` — endptr-checked `strtoll` ([ADR-00287](../adr/ADR-00287.md))<br>• With radix omitted, auto-detects base 16 for a `"0x"`/`"0X"` prefix and base 10 otherwise — no octal auto-detect ([ADR-00530](../adr/ADR-00530.md)) |
| `Number.parseFloat(s)` | ✅ | | • A `"0x10"` hex prefix reads only its leading `0` → `0` (real parseFloat, unlike `Number("0x10")` → `16`) via `__kml_strtod_parsefloat` ([ADR-00545](../adr/ADR-00545.md))<br>• A no-conversion input returns a real `NaN` via the endptr check; only the exact word `"Infinity"` parses to `Infinity` (`"inf"` → `NaN`) ([ADR-00287](../adr/ADR-00287.md)/[ADR-00529](../adr/ADR-00529.md)) |
| `Number.MAX_SAFE_INTEGER` | ✅ | | |
| `Number.MIN_SAFE_INTEGER` | ✅ | | |
| `Number.EPSILON` | ✅ | | |
| `Number.MAX_VALUE` | ✅ | | |
| `Number.MIN_VALUE` | ✅ | | |
| `Number.POSITIVE_INFINITY` | ✅ | | |
| `Number.NEGATIVE_INFINITY` | ✅ | | |
| `Number.NaN` | ✅ | | |
| `Number.prototype.toFixed(n)` | ✅ | | • The spec's algorithm over the double's exact decimal expansion: an exact tie rounds to the larger value (`(2.5).toFixed(0)` is `'3'`), `|x| ≥ 1e21` gives `String(x)`, and a digit count outside 0..100 throws Node's `RangeError` ([ADR-01143](../adr/ADR-01143.md)) |
| `Number.prototype.toString(radix?)` | ✅ | | • Radix 10 is `String(x)`; any other base is V8's own algorithm (`DoubleToRadixCString`): the integer digits exactly and the fraction only as far as the double's precision reaches, so every base matches Node digit for digit ([ADR-01143](../adr/ADR-01143.md))<br>• A radix outside 2..36 throws a `RangeError` as in real JS ([ADR-00552](../adr/ADR-00552.md)) |
| `Number.prototype.toPrecision(n)` | ✅ | | • The spec's algorithm: exact decimal rounding with ties to the larger value, exponential notation below `1e-6` or at `10^precision` and up (`(0.00001).toPrecision(2)` is `'0.000010'`), and Node's `RangeError` outside 1..100 ([ADR-01143](../adr/ADR-01143.md))<br>• The precision argument is optional; `x.toPrecision()` with no argument is exactly `String(x)`, as real JS ([ADR-00534](../adr/ADR-00534.md)) |
| `Number.prototype.toExponential(n?)` | ✅ | | • The spec's algorithm over the exact decimal expansion (ties to the larger value); with no argument, the shortest round-trip mantissa, as in Node ([ADR-01143](../adr/ADR-01143.md)) |
| `parseInt(s, radix?)` (global) | ✅ | | • No-digits input → real `NaN`; with radix omitted, hex auto-detect for a `"0x"` prefix, base 10 otherwise — same as `Number.parseInt(s)` above ([ADR-00287](../adr/ADR-00287.md)/[ADR-00530](../adr/ADR-00530.md)) |
| `parseFloat(s)` (global) | ✅ | | • Same `__kml_strtod_parsefloat` wrapper as `Number.parseFloat(s)` above — a `"0x10"` hex prefix reads only its leading `0` → `0` ([ADR-00545](../adr/ADR-00545.md))<br>• No-conversion input → real `NaN`; only the exact word `"Infinity"` parses to `Infinity` ([ADR-00287](../adr/ADR-00287.md)/[ADR-00529](../adr/ADR-00529.md)) |
| `isNaN(x)` (global) | ✅ | | |
| `isFinite(x)` (global) | ✅ | | |
| `Math.floor/ceil/round/trunc` | ✅ | | • A float input stays a double end-to-end, so `NaN`/`±Infinity` pass through unchanged; `Math.round` uses JS's tie-toward-`+Infinity` (`Math.round(-4.5) === -4`) incl. the `-0` result for `Math.round(-0.5)` ([ADR-00286](../adr/ADR-00286.md)); integer input keeps the exact-i64 path |
| `Math.abs` | ✅ | | |
| `Math.sqrt/pow/hypot` | ✅ | | |
| `Math.log/log2/log10` | ✅ | • Results come from the platform libm, which can differ from V8's fdlibm port in the last binary digit (`Math.tan(1)` prints `1.557407724654902` here, `1.5574077246549023` in Node; macOS) | |
| `Math.sin/cos/tan` | ✅ | • Results come from the platform libm, which can differ from V8's fdlibm port in the last binary digit (`Math.tan(1)` prints `1.557407724654902` here, `1.5574077246549023` in Node; macOS) | |
| `Math.min/max` | ✅ | | • Any float argument promotes the fold to `llvm.minimum`/`llvm.maximum` — `NaN` propagates and `-0.0` orders below `+0.0`, as the JS spec; all-integer calls stay exact i64 ([ADR-00286](../adr/ADR-00286.md)) |
| `Math.sign` | ✅ | | • Float path returns ±1.0 or the input itself (`NaN` stays `NaN`, a signed zero keeps its sign); integer path exact i64 ([ADR-00286](../adr/ADR-00286.md)) |
| `Math.random()` | ✅ | | |
| `Math.PI/E/LN2/LN10/SQRT2/LOG2E/LOG10E` | ✅ | | |
| `Math.cbrt/expm1/log1p` | ✅ | • Results come from the platform libm, which can differ from V8's fdlibm port in the last binary digit (`Math.tan(1)` prints `1.557407724654902` here, `1.5574077246549023` in Node; macOS) | • `cbrt` uses a deterministic, correctly-rounded fdlibm implementation (`@__kml_cbrt`) rather than platform libm, whose runtime `cbrt` is not reliably correctly-rounded and diverged by OS (glibc `cbrt(27)` → `3.0000000000000004`); `expm1`/`log1p` delegate to libm ([ADR-00242](../adr/ADR-00242.md)) |
| `Math.asin/acos/atan/atan2` | ✅ | • Results come from the platform libm, which can differ from V8's fdlibm port in the last binary digit (`Math.tan(1)` prints `1.557407724654902` here, `1.5574077246549023` in Node; macOS) | |
| `Math.sinh/cosh/tanh` | ✅ | • Results come from the platform libm, which can differ from V8's fdlibm port in the last binary digit (`Math.tan(1)` prints `1.557407724654902` here, `1.5574077246549023` in Node; macOS) | |
| `Math.exp` and `Math.asinh/acosh/atanh` | ✅ | • Results come from the platform libm, which can differ from V8's fdlibm port in the last binary digit (`Math.tan(1)` prints `1.557407724654902` here, `1.5574077246549023` in Node; macOS) | |
| `Math.clz32/fround/imul` | ✅ | | • `clz32` via LLVM's own `llvm.ctlz.i32` intrinsic; `fround` via an `fptrunc`/`fpext` float32 round-trip; `imul` via 32-bit `mul` + sign-extend, giving real 32-bit-wraparound integer multiplication distinct from plain `*`'s double-precision result ([ADR-00065](../adr/ADR-00065.md)) |
