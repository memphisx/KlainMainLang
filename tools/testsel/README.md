# tools/testsel — targeted test runs

The E2E suite (`tests/`) takes ~25 min on Windows and ~10 min on Linux; these scripts
run the slice a change touches. Run from the repo root; Python 3 (`py` on Windows).

- `batch.py <selector> [batch-size] [only-batches]` — runs E2E tests in batches of 300
  and prints failures. `selector` is a regex over each test's **source body**
  (`'TypedArray|Symbol'`), or `file:typedarrays,symbol` for every test in those
  `tests/<area>_test.go` files — the test files are the area map. Network-named
  tests are skipped (they need the loopback interceptor off on Windows).
- `netbatch.py [batch-size] [only-batches]` — the network-named tests, with per-batch
  logs in `$KML_SCRATCH`.
- `sel.py <regex> [count]` — prints the `-run` regex for tests whose body matches.
- `irdiff.py --a <klainmain A> [--b <klainmain B>] [--env-b NAME=VAL]` — compiles every
  example to IR with two compilers/environments and lists the files whose IR differs.

`probes/` is scratch (ignored).
