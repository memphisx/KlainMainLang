// klmpm Stage 1: compiler-side package resolution (TDD-00054) — a bare
// specifier (`import x from 'pkg'`, no `./`/`../`) resolves against
// klain_modules/<name>/klain.json's "main" field, found by walking upward
// from this file's own directory. klmpm itself (the tool that would fetch
// a real dependency into klain_modules/) doesn't exist yet — the
// klain_modules/greeter directory alongside this file was hand-written to
// exercise the exact same resolution path a real fetched package would.

import { greet } from 'greeter'

console.log(greet("world"))
