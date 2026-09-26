# irdiff — front-end equivalence corpus

Compiles every `.ts`/`.js` under `examples/`, `tools/`, `apps/` and Test262's
`test/language` to IR (`-emit-llvm`) with a given binary and records a hash of
exit status + IR + diagnostics per file. Each `.js` file is compiled in both
emitter lanes, `-compat=js` under its own path and `-compat=strict` as
`<path>#strict`. Comparing two runs proves a front-end
change behaviour-neutral, or lists exactly what changed.

```sh
cp klainmain /tmp/km.before           # before the change
python3 tools/irdiff/irdiff.py /tmp/km.before /tmp/before.json
go build -o klainmain .               # after
python3 tools/irdiff/irdiff.py $PWD/klainmain /tmp/after.json
python3 tools/irdiff/cmp.py /tmp/before.json /tmp/after.json
```

About 20 minutes on the M4 for 24k files (47k compiles with both lanes). The useful signal is the transition
counts — compiled→fails must be zero — and any change in a compiling file's IR.
