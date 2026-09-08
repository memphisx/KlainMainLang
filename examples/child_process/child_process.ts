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

import { spawn, exec, execFile, execSync, spawnSync } from 'child_process'

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

// ── spawn error: a command that can't start emits 'error', not 'exit' ─────
const missing = spawn("no-such-command-kml", [])
missing.on('error', (err) => {
  console.log("spawn error:", err.message)          // spawn <reason>
  console.log("code:", (err as any).code)           // ENOENT
  console.log("errno:", (err as any).errno)         // -2 on POSIX (negative libuv errno)
})
// 'exit' never fires for a failed spawn; 'close' still does. The Error carries
// Node's `err.code`/`err.errno`, so `err.code === 'ENOENT'` distinguishes a
// missing command from other spawn failures.

// ── execSync: blocking, returns stdout, throws on a nonzero exit ──────────
console.log("execSync:", execSync("echo synchronous").trim())
try {
  execSync("exit 2")   // nonzero status → throws, like Node
} catch (e) {
  console.log("execSync threw:", (e as Error).message)   // Command failed: exit 2
}

// ── exit codes: POSIX truncates to 8 bits, Windows keeps the full 32 ──────
// `exit 300` reports 300 & 0xff = 44 here (POSIX exit codes are 8-bit, as
// Node also reports on Linux/macOS); on Windows the child's exit code is the
// full 32-bit value, so `.status` is 300 (ADR-00759).
const wide = spawnSync("sh", ["-c", "exit 300"])
console.log("wide exit status:", wide.status)

// ── env: a custom child environment (fully replaces, like Node) ───────────
const envChild = spawn("sh", ["-c", "echo $GREETING"], { env: { GREETING: "hi from a custom env" } })
let envOut = ""
envChild.stdout.on('data', (c: Uint8Array) => { envOut = envOut + dec.decode(c) })
envChild.on('close', () => { console.log("env child said:", envOut.trim()) })

// ── windowsHide: suppress the child's console window on Windows ────────────
// A no-op on POSIX (like Node); on Windows it passes CREATE_NO_WINDOW so a
// spawned console child doesn't flash a window. stdio is unaffected.
const quiet = spawn("sh", ["-c", "echo no window"], { windowsHide: true })
let quietOut = ""
quiet.stdout.on('data', (c: Uint8Array) => { quietOut = quietOut + dec.decode(c) })
quiet.on('close', () => { console.log("windowsHide child:", quietOut.trim()) })

// ── stdio: per-fd control (string or a 3-element array) ───────────────────
// 'inherit' wires the fd straight to the parent's; 'ignore' sends it to
// /dev/null (NUL on Windows); 'pipe' (default) keeps the readable/writable
// stream. Here stdout is inherited (prints directly) and stderr ignored.
const io = spawn("sh", ["-c", "echo direct to our stdout; echo hush 1>&2"], { stdio: ["pipe", "inherit", "ignore"] })
io.on('close', () => { console.log("stdio child closed") })

// ── detached + unref: a child that outlives the parent ────────────────────
// detached is setsid (POSIX) / DETACHED_PROCESS (Windows); unref() drops the
// child from the event-loop keepalive so the parent can exit without waiting.
// (This example still reads its output, so it does wait here.)
const bg = spawn("sh", ["-c", "echo running independently"], { detached: true })
let bgOut = ""
bg.stdout.on('data', (c: Uint8Array) => { bgOut = bgOut + dec.decode(c) })
bg.on('close', () => { console.log("detached child:", bgOut.trim()) })
// bg.unref()  // uncomment to let this program exit without waiting for bg

// ── timeout: kill a child that runs too long ──────────────────────────────
// The event loop kills the child after `timeout` ms with `killSignal`
// (default SIGTERM); the exit event then reports (null, '<signal>').
const slow = spawn("sleep", ["10"], { timeout: 300, killSignal: "SIGTERM" })
slow.on('exit', (code: number | null, signal: string | null) => {
  console.log("timed-out child:", code, signal)  // null SIGTERM
})

// ── kill: 'exit'/'close' fire as (code, signal) ───────────────────────────
// A child terminated by a signal reports code=null and the signal name, the
// way Node does; a normal exit reports its integer code and signal=null.
// child.kill accepts the signal name ('SIGTERM') or a number. POSIX recovers
// the signal from the wait status; Windows from the recorded kill (ADR-00761).
const victim = spawn("sh", ["-c", "sleep 5"])
victim.on('exit', (code: number | null, signal: string | null) => {
  console.log("killed child: code=" + code + " signal=" + signal)  // null SIGTERM
})
setTimeout(() => { victim.kill('SIGTERM') }, 200)

// ── stdin: pipe data into a child ─────────────────────────────────────────
const upper = spawn("tr", ["a-z", "A-Z"])
let up = ""
upper.stdout.on('data', (c: Uint8Array) => { up = up + dec.decode(c) })
upper.on('close', () => { console.log("uppercased:", up.trim()) })
upper.stdin.write("shout this")
upper.stdin.end()
