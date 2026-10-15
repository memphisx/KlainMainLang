package tests

import (
	"runtime"
	"testing"
)

// server.listen(path): a named-pipe server on Windows (`\\.\pipe\name`),
// as Node's is (ADR-01609): several clients, one after another and at once,
// and a second server on a name already taken. Compared with Node on the
// same machine.
func TestE2ENetPipeServerWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows named pipes (POSIX: Unix sockets, TestE2ENetUnixSocket*)")
	}
	assertSameAsNodeImports(t, `
import net from 'net'
const path = '\\\\.\\pipe\\kml-test-' + process.pid
let served = 0
const server = net.createServer((s) => {
  served++
  s.on('data', (d) => { s.write('echo:' + String(d)) })
  s.on('end', () => { s.end() })
})
function client(msg: string): Promise<string> {
  return new Promise((resolve) => {
    let got = ''
    const c = net.connect(path, () => { c.write(msg) })
    c.on('data', (d) => { got += String(d); c.end() })
    c.on('close', () => resolve(got))
  })
}
server.listen(path, async () => {
  console.log('listening', server.address() === path)
  console.log(await client('one'))
  console.log(await client('two'))
  const all = await Promise.all([client('a'), client('b'), client('c')])
  console.log(all.sort().join(' '))
  const second = net.createServer(() => {})
  second.on('error', (e: any) => {
    console.log('second', e.code, e.syscall)
    server.close(() => console.log('closed, served', served))
  })
  second.listen(path)
})
`)
}
