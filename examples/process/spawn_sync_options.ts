// child_process *Sync options and the ExecException (ADR-01080): input fed to
// stdin, an environment that replaces the child's, a timeout with its
// ETIMEDOUT error, maxBuffer, shell + args, and what execSync throws.
// The children are `node -e` one-liners so the same program runs under Node.

import { spawnSync, execSync, execFileSync } from 'node:child_process'

// input → the child's stdin; the whole result record is Node's shape.
const r = spawnSync('node', ['-e', "process.stdin.on('data', d => process.stdout.write('got ' + d))"], { input: 'hello', encoding: 'utf8' })
console.log(r.status, r.signal, r.stdout, Object.keys(r).join(',')) // 0 null got hello status,signal,output,pid,stdout,stderr

// env replaces the child's environment (Node's non-merging semantics).
const e = spawnSync('node', ['-e', 'console.log(process.env.GREETING, process.env.HOME === undefined)'], { env: { GREETING: 'kalimera', PATH: process.env.PATH ?? '' }, encoding: 'utf8' })
console.log(e.stdout.trim()) // kalimera true

// A timeout kills the child: status null, the signal name, and an error.
const t = spawnSync('node', ['-e', 'setTimeout(() => {}, 5000)'], { timeout: 200, encoding: 'utf8' })
console.log(t.status, t.signal, (t.error as any).code) // null SIGTERM ETIMEDOUT

// shell: true joins the args into one command line for the platform shell.
console.log(spawnSync('echo', ['a', 'b'], { shell: true, encoding: 'utf8' }).stdout.trim()) // a b

// execSync throws an Error carrying the result fields.
try {
  execSync('node -e "process.stderr.write(\'bad\'); process.exit(3)"', { stdio: 'pipe' })
} catch (err: any) {
  console.log(err.status, err.signal, err.stderr.toString(), err.message.split('\n')[0]) // 3 null bad Command failed: node -e "…"
}

// execFileSync with input and cwd.
console.log(execFileSync('node', ['-e', "process.stdout.write(require('fs').readFileSync(0, 'utf8').toUpperCase())"], { input: 'piped', encoding: 'utf8' })) // PIPED
