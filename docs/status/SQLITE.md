<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/sqlite.json; edit the JSON, then run `make status`. -->

# SQLite (node:sqlite)

> Part of the [Implementation Status](README.md) index. Node's built-in `node:sqlite` module — a synchronous, dependency-free SQL database. Import-gated (`import { DatabaseSync } from 'node:sqlite'`). Backed by the system `libsqlite3` (linked only when a program imports the module, the same posture as `fetch`/libcurl); `DatabaseSync`/`StatementSync` block like `fs.readFileSync`, so no `async`/`await` is involved. See [ADR-00540](../adr/ADR-00540.md)/[TDD-00151](../tdd/TDD-00151.md).

**Coverage**: 18/18 (100%) · **Strict Coverage**: 9/18 (50%).

Format: [Status page format](README.md#status-page-format).

| API | Status | Caveats | Notes |
|---|---|---|---|
| `new DatabaseSync(path, options?)` | ✅ | • `options` is read from an object literal (V1): `readOnly`, `open`, `enableForeignKeyConstraints` (default on), `timeout`. A non-literal options argument uses the defaults | |
| `db.exec(sql)` | ✅ | | |
| `db.prepare(sql)` → `StatementSync` | ✅ | | |
| `db.close()` / `db.isOpen` | ✅ | | |
| `db.open()` | ✅ | | • Reopens a handle built with `open: false` (or after `close()`), reapplying the constructor's flags |
| `db.isTransaction` | ✅ | | |
| `db.location(dbName?)` | ✅ | | • Returns the backing file, or `null` for an in-memory/temp database |
| `db.function(name[, options], fn)` (scalar UDF) | ✅ | • Scalar functions only; parameter/return types must be number, integer, bigint, or string (BLOB args and `aggregate()` are later stages). A per-registration trampoline invokes the compiled closure | |
| `stmt.get<T>(...params)` → a row or `null` | ✅ | • Row shape `T` must be given as an explicit type argument (`stmt.get<{ id: number }>()`) or resolvable named type; an untyped read is a compile error — the dynamic-row mode is a later stage | |
| `stmt.all<T>(...params)` → `T[]` | ✅ | • Same explicit-row-type requirement as `get<T>` | |
| `stmt.iterate<T>(...params)` | ✅ | • Materialised eagerly (equivalent to `all<T>()`) so `for…of` works; a lazy `.next()` iterator is a later stage. Same explicit-row-type requirement | |
| `stmt.run(...params)` → `{ changes, lastInsertRowid }` | ✅ | • `changes`/`lastInsertRowid` are `number`; integers beyond 2^53 lose precision (as they do in Node's default number mode) | |
| `stmt.columns()` | ✅ | • `name` and declared `type` are populated; origin `column`/`table`/`database` read `null` (they need `SQLITE_ENABLE_COLUMN_METADATA`, absent from the system libsqlite3)<br>• The origin fields actually surface as `""`, not `null`: `columns()[0].column` → `""` (Node, on a libsqlite3 built with column metadata: `"id"`/`"main"`/`"t"`) | |
| `stmt.sourceSQL` / `stmt.expandedSQL` | ✅ | | |
| `stmt.setReadBigInts()` / `stmt.setAllowBareNamedParameters()` | ✅ | • Accepted for API completeness but effectively no-ops: the statically-typed row field governs integer representation (declare a field `bigint` to read one), and bare named parameters are always accepted | |
| Parameter binding (positional + named) | ✅ | • Positional (`?`) and named (a single object argument → `:name`/`@name`/`$name`, or the bare key). An integral `number` binds as INTEGER, a fractional one as REAL, matching Node | |
| Column value mapping | ✅ | | • `INTEGER`/`REAL` → `number`, `TEXT` → `string`, `BLOB` → `Uint8Array`, `NULL` → `null` for a `T | null`-typed field (a non-nullable field reads `NULL` as its zero); `bigint`-typed field reads `INTEGER` as a `bigint`; a declared field with no matching result column throws |
| Errors → catchable `Error` | ✅ | | • Carries `message` (`sqlite3_errmsg`), `code` (`'ERR_SQLITE_ERROR'`), numeric `errcode`, and `errstr` — matching Node |

### Caveats / Limitations

- Result rows need a statically known shape (an explicit `.all<T>()`/`.get<T>()`/`.iterate<T>()` type argument); `SELECT *` against an unknown schema awaits the dynamic-object mode. To read a nullable column as `null` (rather than its zero), type the field `T | null`.
- `db.aggregate()` (user-defined aggregate functions) is a later stage — scalar `db.function()` is supported.
- A lazy `.next()` iterator and `stmt.columns()` origin fields are later stages.
- Deliberately rejected with a clear compile-time message (the system `libsqlite3` doesn't provide them / out of scope): `createSession`/`applyChangeset` (session extension), `loadExtension`/`enableLoadExtension` (extension loading), and the async `backup()`.
- Byte-for-byte equivalence with real Node is machine-checked by a differential oracle test. Verified on macOS (Apple Silicon M4) and Linux (arm64, Docker `golang:latest` + `libsqlite3-dev`) — both link `libsqlite3` via a bare `-lsqlite3`.
