# URL

> Part of the [Implementation Status](README.md) index.

**Coverage**: ~67% (2/3).

**Strict Coverage**: 0/3, 0% — a row only counts here if it was independently repro-verified with zero known caveats or bugs, of any severity. See the 2026-08-11 audit ([ADR-00166](../adr/ADR-00166.md)) that produced this number; no false ✅ claims found on this page — both rows here (single-argument-only `URL`, single-value-per-key `URLSearchParams`) were already honestly caveated before the audit.

**Caveats**:

- `URLPattern` isn't implemented.
- `URLSearchParams` keeps only one value per key — see Known Limitations below.

| API | Status | Notes |
|---|---|---|
| `URL` | ✅ | `new URL(url)` — single-argument form only, no base-URL resolution. Parses via libcurl's URL API (`curl_url`/`curl_url_set`/`curl_url_get`) rather than a hand-rolled parser — the same "reuse the already-linked C library" approach `fetch` established. Fields: `href`, `protocol`, `host`, `hostname`, `port`, `pathname`, `search`, `hash`, `origin`, `searchParams` — all plain reads via the ordinary object field-access path, no dispatched methods. A part absent from the URL (no port/query/fragment) reads as `""`, matching real `URL`'s own default. Throws a catchable Error ("Invalid URL") on a malformed URL. See [ADR-00076](../adr/ADR-00076.md). |
| `URLSearchParams` | ✅ | `new URLSearchParams()` / `new URLSearchParams(init)` (tolerates a leading `?`). Backed by a plain `Map<string,string>` (the same simplification `http.listen`'s own `req.query` already makes) — `get`/`set`/`has`/`delete`/`size`/`keys()`/`values()`/`entries()`/`forEach()` all come for free from the existing Map machinery, plus `toString()` (percent-encodes back into a query string) and `getAll()` (0 or 1 elements — see the limitation below) added specifically for this type. **Single value per key**: a repeated query-string key keeps only its last value, so `.getAll()` never returns more than one element and there is no true multi-value `.append()`. See [ADR-00076](../adr/ADR-00076.md). |
| `URLPattern` | ❌ | Pattern matching against URL structures |

## Known Limitations

| Limitation | Notes |
|---|---|
| `URLSearchParams` keeps only one value per key | A repeated query-string key (`?a=1&a=2`) silently keeps only the last value — `.get()`/`.getAll()` never see the first one. Backed by a plain `Map<string,string>` (the same simplification `http.listen`'s `req.query` already makes), not a true multi-value store; there is also no real `.append()` since it can't be represented faithfully on top of that backing structure. Deliberate, documented scope narrowing, not a bug — see [ADR-00076](../adr/ADR-00076.md). |
