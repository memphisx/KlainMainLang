# Encoding / Text

> Part of the [Implementation Status](README.md) index.

**Coverage**: 0% (0/2) — not started.

**Caveats**: `TextEncoder`/`TextDecoder` aren't implemented; can be built on C `iconv` or hand-rolled UTF-8 routines. (`atob`/`btoa` and `encodeURI(Component)`/`decodeURI(Component)` are already implemented — tracked as bare globals in [GLOBAL-FUNCTIONS.md](GLOBAL-FUNCTIONS.md), not repeated here.)

| API | Status | Notes |
|---|---|---|
| `TextEncoder` | ❌ | UTF-8 encode string → `Uint8Array` |
| `TextDecoder` | ❌ | Decode bytes → string; supports UTF-8, UTF-16, Latin-1 |
