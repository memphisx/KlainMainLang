package tests

import "testing"

// child_process *Sync options + result shape + the execSync ExecException
// (ADR-01080). Node is the oracle: the child programs are `node -e` one-liners
// so both sides run the same child; the only host-specific line is cwd.

const cpSyncNodeChildren = `
import { spawnSync, execSync, execFileSync } from 'node:child_process'
const isWin = process.platform === 'win32'
const node = 'node'
`

func TestE2ECPSpawnSyncResultShapeSameAsNode(t *testing.T) {
	assertSameAsNodeImports(t, cpSyncNodeChildren+`
const r = spawnSync(node, ['-e', "process.stdout.write('out'); process.stderr.write('err'); process.exit(3)"])
console.log(r.status, r.signal, r.stdout.toString(), r.stderr.toString(), r.pid > 0, r.output.length, r.error === undefined)
console.log(Object.keys(r).join(','))
const ok = spawnSync(node, ['-e', "process.stdout.write('fine')"], { encoding: 'utf8' })
console.log(ok.status, ok.signal, ok.stdout, ok.output[1], ok.error === undefined)
`)
}

func TestE2ECPSpawnSyncInputEnvArgv0SameAsNode(t *testing.T) {
	assertSameAsNodeImports(t, cpSyncNodeChildren+`
const r2 = spawnSync(node, ['-e', "let d='';process.stdin.on('data',c=>d+=c).on('end',()=>process.stdout.write('got:'+d))"], { input: 'hello\nworld', encoding: 'utf8' })
console.log(r2.status, r2.stdout)
const r3 = spawnSync(node, ['-e', "console.log(Object.keys(process.env).filter(k=>k==='KML_X'||k==='PATH').sort().join(','))"], { env: { KML_X: '1', PATH: process.env.PATH ?? '' }, encoding: 'utf8' })
console.log(r3.stdout.trim())
const r4 = spawnSync(node, ['-e', "process.stdout.write(process.argv0)"], { argv0: 'renamed-node', encoding: 'utf8' })
console.log(r4.status, isWin ? r4.stdout.length > 0 : r4.stdout)
`)
}

func TestE2ECPSpawnSyncTimeoutMaxBufferSameAsNode(t *testing.T) {
	assertSameAsNodeImports(t, cpSyncNodeChildren+`
const r4 = spawnSync(node, ['-e', 'setTimeout(()=>{}, 5000)'], { timeout: 300, encoding: 'utf8' })
console.log(r4.status, r4.signal, (r4.error as any).code, (r4.error as any).message)
const r5 = spawnSync(node, ['-e', "process.stdout.write('x'.repeat(5000))"], { maxBuffer: 1000, encoding: 'utf8' })
console.log(r5.status, r5.signal, (r5.error as any).code)
const r6 = spawnSync(node, ['-e', 'setTimeout(()=>{}, 5000)'], { timeout: 200, killSignal: 'SIGKILL', encoding: 'utf8' })
console.log(r6.signal, (r6.error as any).code)
`)
}

func TestE2ECPSpawnSyncShellStdioSameAsNode(t *testing.T) {
	assertSameAsNodeImports(t, cpSyncNodeChildren+`
const r6 = spawnSync('echo', ['a', 'b'], { shell: true, encoding: 'utf8' })
console.log(r6.status, r6.stdout.trim())
const r7 = spawnSync(node, ['-e', "console.log('inherited')"], { stdio: 'inherit' })
console.log(r7.status, r7.stdout, r7.output[1])
const r8 = spawnSync(node, ['-e', "console.log('dropped')"], { stdio: ['ignore', 'ignore', 'pipe'], encoding: 'utf8' })
console.log(r8.status, r8.stdout, r8.stderr)
const r9 = spawnSync(node, ['-e', "console.log(process.cwd())"], { cwd: isWin ? 'C:\\' : '/', encoding: 'utf8' })
console.log(r9.stdout.trim())
`)
}

func TestE2ECPSpawnSyncSpawnFailureSameAsNode(t *testing.T) {
	assertSameAsNodeImports(t, cpSyncNodeChildren+`
const r7 = spawnSync('definitely-not-a-cmd-kml', ['x'])
console.log(r7.status, r7.signal, r7.pid, (r7.error as any).code, (r7.error as any).syscall, (r7.error as any).path, r7.error!.message)
`)
}

// A child that never started has `stdout`/`stderr` undefined (keys kept),
// `output` null and `error` as the first key; a method call on the absent
// stream throws Node's TypeError instead of crashing.
const cpSyncAbsentStreams = `
import { spawnSync } from 'node:child_process'
const r = spawnSync('definitely-not-a-cmd-kml', ['x'], { encoding: 'utf8' })
console.log(Object.keys(r).join(','), r.output, r.stdout, r.stderr, typeof r.stdout, 'stderr' in r)
console.log(JSON.stringify(Object.keys(r)), String(r.stdout), ` + "`${r.stderr}`" + `)
try { console.log(r.stdout.trim()) } catch (e: any) { console.log(e instanceof TypeError, e.message) }
try { console.log(r.stderr.length) } catch (e: any) { console.log(e instanceof TypeError, e.message) }
`

func TestE2ECPSpawnSyncAbsentStreamsSameAsNode(t *testing.T) {
	assertSameAsNodeImports(t, cpSyncAbsentStreams+`
const i = spawnSync('node', ['-e', '0'], { stdio: 'inherit', encoding: 'utf8' })
console.log(Object.keys(i).join(','), i.stdout, i.stderr)
try { console.log(i.stdout.trim()) } catch (e: any) { console.log(e instanceof TypeError, e.message) }
`)
}

// The Node-less twin of the above (the Linux CI image has no node).
func TestE2ECPSpawnSyncAbsentStreams(t *testing.T) {
	assertOutputImports(t, cpSyncAbsentStreams, `error,status,signal,output,pid,stdout,stderr null undefined undefined undefined true
["error","status","signal","output","pid","stdout","stderr"] undefined undefined
true Cannot read properties of undefined (reading 'trim')
true Cannot read properties of undefined (reading 'length')
`)
}

func TestE2ECPExecSyncThrowsExecExceptionSameAsNode(t *testing.T) {
	assertSameAsNodeImports(t, cpSyncNodeChildren+`
try {
  execSync(node + " -e \"process.stderr.write('boom'); process.exit(7)\"", { stdio: 'pipe' })
} catch (e: any) {
  console.log(e.message, e.status, e.signal, typeof e.pid, e.stdout.toString().length, e.stderr.toString(), e.output.length)
}
try {
  execFileSync('definitely-not-a-cmd-kml')
} catch (e: any) {
  console.log(e.code, e.message, e.status, e.signal)
}
try {
  execSync(node + " -e \"setTimeout(()=>{},5000)\"", { timeout: 200, stdio: 'pipe' })
} catch (e: any) {
  console.log(e.code, e.signal, e.status)
}
try {
  execFileSync(node, ['-e', "process.stdout.write('x'.repeat(5000))"], { maxBuffer: 1000, stdio: 'pipe' })
} catch (e: any) {
  console.log(e.code, e.signal)
}
console.log(execFileSync(node, ['-e', "console.log('ok')"], { encoding: 'utf8' }).trim())
console.log(execFileSync(node, ['-e', "process.stdout.write(require('fs').readFileSync(0,'utf8'))"], { input: 'piped in', encoding: 'utf8' }))
`)
}

// exec / execFile options (cwd, env, maxBuffer, timeout, killSignal,
// windowsHide) and the ExecException the callback receives — read through an
// `err: any` parameter, which is boxed on entry (ADR-01080).
func TestE2ECPExecAsyncOptionsSameAsNode(t *testing.T) {
	assertSameAsNodeImports(t, `
import { exec, execFile } from 'node:child_process'
const isWin = process.platform === 'win32'
let step = 0
function next() {
  step++
  if (step === 1) {
    exec('node -e "console.log(process.cwd())"', { cwd: isWin ? 'C:\\' : '/' }, (err, out, errOut) => {
      console.log('1', err === null, out.trim(), errOut.length)
      next()
    })
  } else if (step === 2) {
    execFile('node', ['-e', 'console.log(process.env.KML_A)'], { env: { KML_A: 'yes', PATH: process.env.PATH ?? '' } }, (err, out) => {
      console.log('2', err === null, out.trim())
      next()
    })
  } else if (step === 3) {
    exec('node -e "process.stdout.write(\'x\'.repeat(5000))"', { maxBuffer: 1000 }, (err: any, out) => {
      console.log('3', err !== null, err?.code, err?.name, err?.message, err?.killed, out.length <= 5000)
      next()
    })
  } else if (step === 4) {
    execFile('node', ['-e', 'setTimeout(()=>{}, 5000)'], { timeout: 300, killSignal: 'SIGKILL' }, (err: any) => {
      console.log('4', err !== null, err?.killed, err?.signal)
      next()
    })
  } else if (step === 5) {
    execFile('node', ['-e', "process.stderr.write('e'); process.exit(4)"], (err: any, out, errOut) => {
      console.log('5', err !== null, errOut, typeof err?.message, err?.killed, err?.signal)
      next()
    })
  } else if (step === 6) {
    exec('node -e "console.log(\'hidden\')"', { windowsHide: true }, (err, out) => {
      console.log('6', err === null, out.trim())
    })
  }
}
next()
`)
}

func TestE2ECPExecSyncMirrorsStderrWithoutStdio(t *testing.T) {
	// Node's execSync writes the child's stderr to ours unless `stdio` is given.
	// The harness compares stdout only, so route the check through a second
	// child that captures the first one's stderr.
	assertSameAsNodeImports(t, cpSyncNodeChildren+`
const outer = spawnSync(node, ['-e', "require('child_process').execSync(process.argv[1] + \" -e \\\"process.stderr.write('mirrored')\\\"\")", node], { encoding: 'utf8' })
console.log(outer.status, outer.stderr)
`)
}
