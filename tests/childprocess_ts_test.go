package tests

import "testing"

// child_process as a TypeScript module (TDD-00234): stdio are streams
// (async iteration, pipe), AbortSignal kills the child, exec's error code.
func TestE2EChildProcessStreamsAndSignal(t *testing.T) {
	skipPOSIXToolsOnWindows(t, "printf/tr/sleep")
	assertSameAsNodeImports(t, `
import { spawn, exec } from 'child_process'
async function main(): Promise<void> {
  const a = spawn('printf', ['one\ntwo\n'])
  let got = ''
  for await (const chunk of a.stdout) got += chunk.toString()
  console.log('iter', JSON.stringify(got))
  const up = spawn('tr', ['a-z', 'A-Z'])
  const src = spawn('printf', ['piped'])
  src.stdout.pipe(up.stdin)
  up.stdout.setEncoding('utf8')
  up.stdout.on('data', (d: string) => console.log('pipe', d))
  await new Promise<void>((r) => up.on('close', () => r()))
  const ac = new AbortController()
  const s = spawn('sleep', ['5'], { signal: ac.signal })
  s.on('error', (e: Error) => console.log('abort', e.name))
  s.on('exit', (code, sig) => console.log('exit', code, sig))
  ac.abort()
  exec('exit 3', (err) => console.log('exec code', (err as any).code))
}
main()
`)
}
