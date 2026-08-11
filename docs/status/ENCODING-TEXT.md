# Encoding / Text

> Part of the [Implementation Status](README.md) index.

**Coverage**: 100% (2/2) — done ([ADR-00112](../adr/ADR-00112.md)).

**Strict Coverage**: 1/2, 50% — a row only counts here if it was independently repro-verified with zero known caveats or bugs, of any severity. See the 2026-08-11 audit ([ADR-00166](../adr/ADR-00166.md)) that produced this number; no false ✅ claims found on this page — `TextDecoder`'s ignored `label` argument was already honestly caveated before the audit (`TextEncoder`'s UTF-8-only scope matches spec, not a narrowing).

**Caveats**: UTF-8 only (V1 scope) — this compiler's strings are already raw UTF-8 byte sequences, so `encode`/`decode` are direct byte copies with no real transcoding involved. `TextDecoder`'s optional `label` argument is evaluated (for side effects) and then ignored rather than validated against a table of recognized encodings — always decodes as UTF-8, permissive rather than throwing a `RangeError` for an unrecognized label, the same documented simplification `atob`/`decodeURI` already use for malformed input. Non-UTF-8 `TextDecoder` support (Latin-1/windows-1252, UTF-16, Greek, and the rest of the WHATWG label list) is scoped as a staged, low-priority follow-on in [TDD-00034](../tdd/TDD-00034.md) — not started; `TextEncoder` needs no further work regardless, since real `TextEncoder` is UTF-8-only by spec. (`atob`/`btoa` and `encodeURI(Component)`/`decodeURI(Component)` are implemented separately — tracked as bare globals in [GLOBAL-FUNCTIONS.md](GLOBAL-FUNCTIONS.md), not repeated here.)

| API | Status | Notes |
|---|---|---|
| `TextEncoder` | ✅ | `new TextEncoder().encode(str): Uint8Array` — UTF-8 only |
| `TextDecoder` | ✅ | `new TextDecoder(label?).decode(bytes): string` — accepts a `Uint8Array` or `ArrayBuffer`; UTF-8 only, `label` accepted but ignored |
