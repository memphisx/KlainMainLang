# Codegen file map

Every `ensure*()`-heavy or dispatch-heavy domain is split into a small
`<domain>.go` (or bare dispatcher) plus several `<domain>_<subarea>.go`
files once it outgrows one file — see [ADR-00075](adr/ADR-00075.md)
for the file-count history. Emitter-side codegen (`emit_*.go`) and its
paired runtime-declaration side (`runtime_*.go`) are split independently,
so a domain's codegen and its C-runtime declarations don't have to land in
the same number of files.

| File(s) | Responsibility |
|---|---|
| `emitter.go` | `Emitter` struct, `freshReg`/`freshLabel`, `emitInstr`/`emitAlloca`/`emitTerminator`/`emitLabel`, scope stack |
| `types.go` | `Type` struct, `ArrayOf`, `ObjectOf`, IR helpers, `Align()` |
| `emit_exprs.go` (core dispatch `emitExpr`/literals) + `emit_exprs_operators.go` (binary/unary/update/ternary/`??`) + `emit_exprs_assign.go` (`emitAssign`) + `emit_exprs_member.go` (`emitIndexPtr`, `emitMember`, `?.`) + `emit_exprs_types.go` (`inferExprType` and friends) + `emit_exprs_coerce.go` (`coerce`/`toBool`) + `emit_exprs_vardecl.go` (`emitVarDecl`) | Expression evaluation, member/index access, static type inference, scalar coercion, variable declarations |
| `emit_stmts.go` | Statement dispatch (`emitStmt`), all loop forms, break/continue stacks |
| `emit_call.go` (dispatch router only) + `emit_call_console.go` + `emit_call_json.go` + `emit_call_math.go` + `emit_call_number.go` + `emit_call_encoding.go` + `emit_structured_clone.go` | Call dispatch router (routes to every other domain's own call implementation) plus console.\*/JSON.\*/Math.\*/Number.\*/btoa-atob-encodeURI-crypto-TextEncoder/TextDecoder/structuredClone, the built-ins with nowhere else to live |
| `emit_func.go` | Function declarations, closures, environment structs |
| `emit_arrays_core.go` (var decl, destructuring, `resolveArrayForHOF`) + `emit_arrays_mutate.go` (push/pop/shift/unshift/splice) + `emit_arrays_hof.go` (map/filter/reduce/find/some/every/forEach) + `emit_arrays_sort.go` (join/sort/slice) + `emit_arrays_search.go` (indexOf/includes/findIndex/findLast\*) + `emit_arrays_transform.go` (concat/reverse/fill/at/with) + `emit_arrays_iter.go` (keys/values/entries/of/copyWithin) | All array operations |
| `emit_strings.go` | String operations, `emitNormalizeSliceIdx`, `emitStringExtract` |
| `emit_objects.go` | Object/struct heap allocation, GEP field access, `Object.*` statics |
| `emit_classes.go` | `class`: field/method/constructor registration, `this`, `new ClassName(args)`, method-call dispatch, class-based `for...of` iterator protocol |
| `emit_collections.go` | `Map<K,V>` and `Set<T>` |
| `emit_eventemitter.go` | `EventEmitter<T>` — standalone and `class X extends EventEmitter<T>` dispatch |
| `emit_exceptions.go` | `try`/`catch`/`throw`/`new Error()` via setjmp/longjmp |
| `emit_process.go` | `process.argv`, `process.exit(code)`, `process.env.KEY` / `process.env["KEY"]` |
| `emit_date.go` | `Date`: construction, getters/setters, `parse`, arithmetic, formatting |
| `emit_dynamic.go` | `any`/`unknown` as a runtime-tagged `{tag, payload}` value |
| `emit_async.go` | `async`/`await`, `Promise<T>` (synchronous V1 — no event loop yet) |
| `emit_promise.go` | `Promise.all`/`.race`/`.allSettled` |
| `emit_fetch.go` | `fetch(url)`/`fetch(url, init)`/`fetch(request)` and `Response` (backed by libcurl) |
| `emit_headers.go` | `Headers` — a case-insensitive `Map<string,string>` wrapper (`get`/`set`/`has`/`delete`/`append`; `forEach`/`entries`/`keys`/`values` reuse `Map`'s own) |
| `emit_fetch_request.go` | `Request` (the client-side fetch class — not `http.listen`'s server-side `HttpRequest`, see `emit_http.go`) |
| `emit_xhr.go` | `XMLHttpRequest` — legacy synchronous-style client built on `fetch`'s own non-blocking primitives |
| `emit_fs.go` | `fs.readFileSync`/`writeFileSync`/`appendFileSync`/`existsSync`/`unlinkSync` |
| `emit_url.go` | `URL`/`URLSearchParams` (backed by libcurl's URL API + the existing `Map<string,string>` machinery), plus `emitMapStrToQueryString` (shared with `emit_querystring.go`) |
| `emit_querystring.go` | `querystring.parse`/`.stringify` — thin wrappers over `emit_url.go`'s query-string machinery |
| `emit_assert.go` | `assert` module (`.ok`/bare `assert(...)`/`.equal`/`.strictEqual`/`.notEqual`/`.notStrictEqual`/`.fail`/`.throws`) — reuses `emit_exceptions.go`'s throw machinery and `emit_exprs_operators.go`'s `emitBinary` for comparisons |
| `emit_arraybuffer.go` | `ArrayBuffer` + TypedArrays (`Int8Array`…`Float64Array`) — construction, `.set()`/`.subarray()`/`.byteLength`; everything else reuses `emit_arrays_*.go` unchanged |
| `emit_memory.go` | `Memory.free` (manual memory-management escape hatch) |
| `emit_timers.go` | `setTimeout`/`setInterval`/`clearTimeout`/`clearInterval`, plus their `ensureTimerRuntime` C-runtime backing store (kept together, not under `runtime_*.go`, since this domain's runtime queue has only ever had one caller) |
| `emit_http.go` | `http.listen`, request/response handling — the server-side request-object type annotation is `HttpRequest` (not `Request`, which is the client-side `fetch` class, see `emit_fetch_request.go`) |
| `emit_os.go` | `os.platform`/`.homedir`/`.tmpdir`/`.hostname`/`.totalmem`/`.freemem`/`.cpus`/`.EOL` — platform selection (Linux vs. Darwin) is a Go-side `runtime.GOOS` branch, not runtime IR |
| `gclocate.go` / `gcshim.go` | Locating and shimming the Boehm GC library for `-mm=gc` |
| `runtime_core.go` | Universal libc primitives (malloc/free/memcpy/strlen/…), math/random/qsort, errno/strerror |
| `runtime_strings.go` | String C-runtime helpers (trim/case/replace/split) |
| `runtime_json.go` | JSON stringify/parse C-runtime helpers |
| `runtime_dynamic.go` | `any`/`unknown` equality (`__kml_any_eq`) |
| `runtime_date.go` | Date C-runtime helpers (clock, decompose/compose, parse, name tables) |
| `runtime_collections.go` | `Object.groupBy`, sort comparators/trampolines, `Map`/`Set`/frozen-set C-runtime helpers |
| `runtime_eventemitter.go` | `EventEmitter<T>` listener-list C-runtime helpers (reuses `runtime_collections.go`'s `Map<string,ptr>` helpers directly for the event-name lookup) |
| `runtime_exceptions.go` | `try`/`catch`/`throw` C-runtime helpers |
| `runtime_fetch.go` | `fetch` (sync + async) and Promise-combinator C-runtime helpers |
| `runtime_fs.go` | File I/O C-runtime helpers |
| `runtime_encoding.go` | base64/hex/URI-encoding C-runtime helpers |
| `runtime_crypto.go` | `crypto.*` C-runtime helpers |
| `runtime_process.go` | `process.*`/readline/execFileSync C-runtime helpers |
| `runtime_url.go` | libcurl URL API declarations (`curl_url*`) |
| `runtime_misc.go` | console group/timer/count-map state, closure/map-free helpers |
| `runtime_http.go` | HTTP server + fiber-scheduler C-runtime helpers |
| `runtime_os.go` | `os` module's substantial C-runtime helpers: growable procfs reading, and the Linux (`/proc/cpuinfo`/`/proc/stat` parsing)/Darwin (Mach `host_processor_info`, unverified on real hardware) implementations of `os.cpus()` |
