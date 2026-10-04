<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/encoding-text.json; edit the JSON, then run `make status`. -->

# Encoding / Text

> Part of the [Implementation Status](README.md) index.

**Coverage**: 2/2 (100%) · **Strict Coverage**: 1/2 (50%).

Format: [Status page format](README.md#status-page-format).

`atob`/`btoa` and `encodeURI(Component)`/`decodeURI(Component)` are tracked as bare globals in [GLOBAL-FUNCTIONS.md](GLOBAL-FUNCTIONS.md), not here.

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `TextEncoder` | ✅ | | • `encode(str)` returns a `Uint8Array`; `encodeInto(src, dest)` writes whole code points and returns `{ read, written }`; `encoding` is `"utf-8"`; `util.inspect` prints Node's `{ encoding: 'utf-8' }`<br>• Node's `lib/internal/encoding.js` in TypeScript (`lib/node/kml_encoding.ts`)<br>• See [ADR-00112](../adr/ADR-00112.md), [ADR-01348](../adr/ADR-01348.md) |
| `TextDecoder` | ✅ | • The CJK encodings (GBK, GB18030, Big5, EUC-JP, EUC-KR, ISO-2022-JP, Shift_JIS), which Node decodes through ICU, throw `ERR_ENCODING_NOT_SUPPORTED` | • `decode(input?, { stream })` over any ArrayBuffer, SharedArrayBuffer or ArrayBufferView (one held in `any` too) in UTF-8, UTF-16LE/BE, x-user-defined and every single-byte encoding of the WHATWG list (the ISO-8859 and windows code pages, KOI8, IBM866, macintosh; tables generated from Node's): invalid or unmapped bytes are U+FFFD, or `ERR_ENCODING_INVALID_ENCODED_DATA` with `fatal`; a leading byte order mark is dropped unless `ignoreBOM`; `stream` keeps an incomplete sequence for the next call<br>• Labels are WHATWG-normalized; an unknown one is `ERR_ENCODING_NOT_SUPPORTED`; `encoding`/`fatal`/`ignoreBOM` read back, and `util.inspect` prints Node's form<br>• Node's `lib/internal/encoding.js` in TypeScript (`lib/node/kml_encoding.ts`)<br>• See [ADR-00112](../adr/ADR-00112.md), [ADR-01348](../adr/ADR-01348.md) |
