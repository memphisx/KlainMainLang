// child_process — async process spawning. Import-gated (a virtual built-in
// module, not a real file). Three call shapes:
//
//   spawn(cmd, args)     streaming: a ChildProcess whose stdout/stderr are
//                        EventEmitters ('data'/'end'), with 'close'/'exit' on
//                        the child itself, and a writable child.stdin
//   exec(cmd, cb)        run cmd through `/bin/sh -c`, buffer its output, then
//                        cb(err, stdout, stderr)
//   execFile(f, args, cb) like exec but no shell — execvp(f, args) directly
//
// The child's pipes fold into the same event loop as timers/fetch/workers, so
// the callbacks fire asynchronously, after the synchronous top-level code.

import { spawn, exec, execFile } from 'child_process'

const dec = new TextDecoder()

// ── spawn: stream a child's stdout line by line ───────────────────────────
const child = spawn("printf", ["one\ntwo\nthree\n"])
let collected = ""
child.stdout.on('data', (chunk: Uint8Array) => {
  collected = collected + dec.decode(chunk)
})
child.on('close', (code: number) => {
  console.log("spawn exited with", code, "and", collected.split("\n").length - 1, "lines")
})

// ── exec: run a shell command and buffer the result ───────────────────────
exec("echo hello && echo world", (err, stdout, stderr) => {
  console.log("exec ok:", err === null)
  console.log("exec output:", stdout.trim().split("\n").length, "lines")
})

// ── execFile: no shell, arguments passed straight to the program ──────────
execFile("echo", ["direct", "argv"], (err, stdout, stderr) => {
  console.log("execFile:", stdout.trim())
})

// ── stdin: pipe data into a child ─────────────────────────────────────────
const upper = spawn("tr", ["a-z", "A-Z"])
let up = ""
upper.stdout.on('data', (c: Uint8Array) => { up = up + dec.decode(c) })
upper.on('close', () => { console.log("uppercased:", up.trim()) })
upper.stdin.write("shout this")
upper.stdin.end()
