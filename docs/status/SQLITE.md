<!-- GENERATED FILE — do not edit. Source of truth: docs/status/data/sqlite.json; edit the JSON, then run `make status`. -->

# SQLite (node:sqlite)

> Part of the [Implementation Status](README.md) index. Node's built-in `node:sqlite` module — a synchronous, dependency-free SQL database. Import-gated (`import { DatabaseSync } from 'node:sqlite'`). Backed by the system `libsqlite3` (linked only when a program imports the module, the same posture as `fetch`/libcurl); `DatabaseSync`/`StatementSync` block like `fs.readFileSync`, so no `async`/`await` is involved. See [ADR-00540](../adr/ADR-00540.md)/[TDD-00151](../tdd/TDD-00151.md).

**Coverage**: 18/18 (100%) · **Strict Coverage**: 16/18 (~89%).

Format: [Status page format](README.md#status-page-format).

| API | Status | Caveats | Notes |
|---|---|---|---|
| `new DatabaseSync(path, options?)` | ✅ | | • `lib/node/sqlite.ts`, ported from Node v24's src/node_sqlite.cc ([ADR-01304](../adr/ADR-01304.md)): path as a string, `Uint8Array` or `file:` URL; `open`, `readOnly`, `enableForeignKeyConstraints`, `enableDoubleQuotedStringLiterals`, `allowExtension`, `timeout`, `readBigInts`, `returnArrays`, `allowBareNamedParameters`, `allowUnknownNamedParameters`, `defensive`, validated with Node's errors |
| `db.open()` / `db.close()` / `db.isOpen` / `db.isTransaction` / `db[Symbol.dispose]()` | ✅ | | • Closing finalizes the database's statements: using one after is `ERR_INVALID_STATE` (`statement has been finalized`) |
| `db.exec(sql)` / `db.prepare(sql)` → `StatementSync` | ✅ | | |
| `db.location(dbName?)` | ✅ | | • The backing file, or `null` for an in-memory/temp database |
| `db.function(name[, options], fn)` | ✅ | | • `deterministic`, `directOnly`, `useBigIntArguments`, `varargs`; arguments and results as Node maps them; an exception in the function is rethrown from the statement |
| `db.aggregate(name, options)` | ✅ | | • `start` (value or function), `step`, `result`, `inverse` (a window function), `useBigIntArguments`, `varargs`, `directOnly` |
| `db.setAuthorizer(callback)` | ✅ | | |
| `db.createSession()` / `db.applyChangeset()` / `Session` | ✅ | • Available only where the system `libsqlite3` is built with sessions (macOS's is); elsewhere `ERR_INVALID_STATE` names the omission — Node bundles its own SQLite | |
| `db.enableLoadExtension()` / `db.loadExtension()` | ✅ | • The system `libsqlite3` on macOS omits extension loading, so loading an extension there is `ERR_INVALID_STATE`; Node bundles its own SQLite | |
| `db.serialize()` / `db.deserialize()` / `db.enableDefensive()` | ✅ | | |
| `db.createTagStore()` → `SQLTagStore` | ✅ | | • Statements cached by template, least recently used evicted |
| `stmt.run()` / `stmt.get()` / `stmt.all()` / `stmt.iterate()` | ✅ | | • Rows are null-prototype objects (arrays with `returnArrays`); `get()` is `undefined` without a row; `iterate()` steps lazily; `run()` gives `{ changes, lastInsertRowid }` |
| `stmt.columns()` / `stmt.sourceSQL` / `stmt.expandedSQL` | ✅ | | |
| `stmt.setReadBigInts()` / `setReturnArrays()` / `setAllowBareNamedParameters()` / `setAllowUnknownNamedParameters()` | ✅ | | |
| Parameter binding (positional + named) | ✅ | | • A leading plain object binds named parameters (`:x`/`@x`/`$x`, or the bare key); a number binds as REAL and a bigint as INTEGER, as in Node; unbindable values are `ERR_INVALID_ARG_TYPE` |
| Column value mapping | ✅ | | • INTEGER → number (bigint with `readBigInts`; past 2^53 `ERR_OUT_OF_RANGE`), REAL → number, TEXT → string, BLOB → `Uint8Array`, NULL → null |
| Errors | ✅ | | • `ERR_SQLITE_ERROR` with `errcode` and `errstr`, as Node's |
| `sqlite.backup(db, path, options?)` / `sqlite.constants` | ✅ | | • Copies `rate` pages per turn of the loop, reporting `progress`; resolves the page count |
