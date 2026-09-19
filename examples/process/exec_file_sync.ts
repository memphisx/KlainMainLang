// process.execFileSync — spawn a child process and capture its stdout,
// synchronously. Modeled on Node's child_process.execFileSync, not
// execSync: the given file is fork()'d + execvp()'d directly, with no
// shell involved — shell metacharacters in args are passed through
// literally, not interpreted (no injection risk from untrusted arguments,
// unlike execSync's shell-interpolation behavior).
//
// V1 scope: no options object (no cwd/env/timeout/stdio overrides yet);
// stdout only — stderr is inherited (goes straight to this program's own
// stderr as the child runs, not captured into the return value). A
// non-zero exit status, or the child dying to a signal, throws a plain
// catchable Error.

// On Windows there is no /bin/echo or /bin/sh; the same execFileSync surface
// (a direct fork+exec of a real .exe, no implicit shell) is shown via cmd.exe
// and where.exe from System32. The POSIX demo below runs everywhere else.
const isWin: boolean = process.platform === 'win32'
if (isWin) {
  console.log(process.execFileSync('cmd', ['/c', 'echo', 'hello', 'from', 'execFileSync']).trim())
  console.log(process.execFileSync('cmd', ['/c', 'echo.']).length > 0)          // true
  console.log(process.execFileSync('cmd', ['/c', 'cd'], { cwd: 'C:\\' }).trim()) // C:\
  console.log(process.execFileSync('where', ['cmd']).length > 0)                 // true — PATH lookup
  try {
    process.execFileSync('cmd', ['/c', 'exit 1'])
    console.log('never printed')
  } catch (e) {
    console.log(e.message)   // Command failed with exit code 1: cmd
  }
  console.log(process.execFileSync('cmd', ['/c', 'echo', 'shell-was-explicit']).trim())
} else {

// ── basic capture ───────────────────────────────────────────────────────────
const echoArgs: string[] = ['hello', 'from', 'execFileSync']
const out: string = process.execFileSync('/bin/echo', echoArgs)
console.log(out)   // hello from execFileSync

// ── args is optional ─────────────────────────────────────────────────────────
const bare: string = process.execFileSync('/bin/echo')
console.log(bare.length > 0)   // true — just the newline echo prints alone

// ── the { cwd } option runs the child in a different working directory ───────
const where: string = process.execFileSync('pwd', [], { cwd: '/tmp' })
console.log(where.trim())      // /tmp

// ── no shell: metacharacters come back out verbatim ─────────────────────────
const literalArgs: string[] = ['$(whoami); rm -rf /tmp/nothing']
console.log(process.execFileSync('/bin/echo', literalArgs))
// $(whoami); rm -rf /tmp/nothing   — never expanded or executed, just echoed

// ── bare command names resolve via $PATH, like execvp always has ───────────
const pathArgs: string[] = ['resolved', 'via', 'PATH']
console.log(process.execFileSync('echo', pathArgs))
// resolved via PATH

// ── a non-zero exit status throws a catchable Error ─────────────────────────
try {
    process.execFileSync('/usr/bin/false')
    console.log('never printed')
} catch (e) {
    console.log(e.message)   // Command failed with exit code 1: /usr/bin/false
}

// ── explicitly invoking a shell still works — it's just not automatic ──────
const shellArgs: string[] = ['-c', 'echo shell-was-explicit']
console.log(process.execFileSync('/bin/sh', shellArgs))
// shell-was-explicit

}
