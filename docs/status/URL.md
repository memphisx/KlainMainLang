# URL

> Part of the [Implementation Status](README.md) index.

**Coverage**: 2/3 (~67%) · **Strict Coverage**: 0/3 (0%).

This page follows the shared status-page format ([Status page format](README.md#status-page-format)): **Status** is a bare ✅/❌; **Caveats** lists behavioral divergences from real JS/TS (a non-empty Caveats cell is what excludes an otherwise-✅ row from Strict Coverage); **Notes** carries implementation/representation detail only. One table per index category; each category's figures above derive from its table below.

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `URL` | ✅ | • `new URL(url)` — single-argument form only, no base-URL resolution | • Parses via libcurl's URL API (`curl_url`/`curl_url_set`/`curl_url_get`) rather than a hand-rolled parser — the same "reuse the already-linked C library" approach `fetch` established<br>• Fields: `href`, `protocol`, `host`, `hostname`, `port`, `pathname`, `search`, `hash`, `origin`, `searchParams` — all plain reads via the ordinary object field-access path, no dispatched methods<br>• A part absent from the URL (no port/query/fragment) reads as `""`, matching real `URL`'s own default<br>• Throws a catchable Error ("Invalid URL") on a malformed URL<br>• See [ADR-00076](../adr/ADR-00076.md) |
| `URLSearchParams` | ✅ | • **Single value per key**: a repeated query-string key (`?a=1&a=2`) silently keeps only its last value — `.get()`/`.getAll()` never see the first one, so `.getAll()` never returns more than one element<br>• There is no true multi-value `.append()`, since it can't be represented faithfully on top of the `Map<string,string>` backing structure | • `new URLSearchParams()` / `new URLSearchParams(init)` (tolerates a leading `?`)<br>• Backed by a plain `Map<string,string>` (the same simplification `http.listen`'s own `req.query` already makes) — `get`/`set`/`has`/`delete`/`size`/`keys()`/`values()`/`entries()`/`forEach()` all come for free from the existing Map machinery, plus `toString()` (percent-encodes back into a query string) and `getAll()` (0 or 1 elements) added specifically for this type<br>• See [ADR-00076](../adr/ADR-00076.md) |
| `URLPattern` | ❌ | | • Pattern matching against URL structures |
