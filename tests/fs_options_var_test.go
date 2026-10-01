package tests

import "testing"

// fs.readFileSync / fs.readFile / fs.promises.readFile take their options
// as a variable too: an encoding string, `{ encoding: enc }`, or an options
// object without one (a Buffer), the codec chosen at run time.
func TestE2EFsReadOptionsVariable(t *testing.T) {
	assertSameAsNodeImports(t, `
import fs from 'fs'
import os from 'os'
const f = os.tmpdir() + "/kml_fo.txt"
fs.writeFileSync(f, "héllo")
const ro = { encoding: "utf8" as BufferEncoding, flag: "r" }
console.log(fs.readFileSync(f, ro))
const hex: BufferEncoding = "hex"
console.log(fs.readFileSync(f, hex), fs.readFileSync(f, { encoding: hex }))
const plain = { flag: "r" }
const b = fs.readFileSync(f, plain)
console.log(b.length, typeof b)
function read(path: string, enc: BufferEncoding): string { return fs.readFileSync(path, enc) }
console.log(read(f, "base64"))
`)
	assertSameAsNodeImports(t, `
import fs from 'fs'
import os from 'os'
const f = os.tmpdir() + "/kml_fo2.txt"
fs.writeFileSync(f, "abc")
const opts = { encoding: "hex" as BufferEncoding }
console.log(await fs.promises.readFile(f, opts))
await new Promise<void>((done) => fs.readFile(f, opts, (err, data) => { console.log(err, data); done() }))
const enc: BufferEncoding = "base64"
console.log(await fs.promises.readFile(f, enc))
`)
}
