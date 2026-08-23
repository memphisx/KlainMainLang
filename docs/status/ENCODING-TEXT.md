# Encoding / Text

> Part of the [Implementation Status](README.md) index.

**Coverage**: 2/2 (100%) · **Strict Coverage**: 1/2 (~50%).

Format: [Status page format](README.md#status-page-format).

`atob`/`btoa` and `encodeURI(Component)`/`decodeURI(Component)` are tracked as bare globals in [GLOBAL-FUNCTIONS.md](GLOBAL-FUNCTIONS.md), not here.

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `TextEncoder` | ✅ | | • `new TextEncoder().encode(str): Uint8Array` — UTF-8 only, which matches the spec (`TextEncoder` is UTF-8-only by spec, not a narrowing)<br>• Strings are already raw UTF-8 byte sequences, so `encode`/`decode` are direct byte copies with no real transcoding<br>• See [ADR-00112](../adr/ADR-00112.md) |
| `TextDecoder` | ✅ | • UTF-8 only (V1 scope) — non-UTF-8 support (Latin-1/windows-1252, UTF-16, Greek, the rest of the WHATWG label list) is a staged, low-priority follow-on in [TDD-00034](../tdd/TDD-00034.md), not started<br>• The optional `label` argument is evaluated (for side effects) and then ignored, not validated — always decodes as UTF-8, permissive rather than throwing a `RangeError` for an unrecognized label | • `new TextDecoder(label?).decode(bytes): string` — accepts a `Uint8Array` or `ArrayBuffer`<br>• See [ADR-00112](../adr/ADR-00112.md) |
