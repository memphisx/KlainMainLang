package tests

import "testing"

// fs.fchmodSync / fs.statfsSync / process.umask (ADR-01078). Node is the
// oracle: every line below is host state or a Node error message.

func TestE2EFsFchmodSyncSameAsNode(t *testing.T) {
	assertSameAsNodeImports(t, `
import fs from 'fs'
import os from 'os'
import path from 'path'
const p = path.join(os.tmpdir(), 'kml-fchmod-' + process.pid + '.txt')
fs.writeFileSync(p, 'x')
const fd = fs.openSync(p, 'r+')
fs.fchmodSync(fd, 0o444)
console.log((fs.fstatSync(fd).mode & 0o222) === 0)
fs.fchmodSync(fd, 0o644)
console.log((fs.fstatSync(fd).mode & 0o200) !== 0)
fs.closeSync(fd)
fs.unlinkSync(p)
try {
  fs.fchmodSync(9999, 0o644)
} catch (e) {
  console.log((e as any).code, (e as any).syscall, (e as Error).message)
}
`)
}

func TestE2EFsStatfsSyncSameAsNode(t *testing.T) {
	assertSameAsNodeImports(t, `
import fs from 'fs'
import os from 'os'
import path from 'path'
const s = fs.statfsSync(os.tmpdir())
console.log(s.bsize > 0, s.blocks > 0, s.bfree >= 0, s.bavail <= s.blocks, typeof s.type, typeof s.files, typeof s.ffree)
console.log(Object.keys(s).join(','))
const missing = path.join(os.tmpdir(), 'kml-nope-' + process.pid)
try {
  fs.statfsSync(missing)
} catch (e) {
  console.log((e as any).code, (e as any).syscall, (e as any).path === missing)
}
`)
}

func TestE2EFsStatfsSyncValues(t *testing.T) {
	// The Windows answer is GetDiskFreeSpaceW: type/files/ffree are 0 there
	// (as in Node); POSIX statfs fills all seven.
	assertOutputImports(t, `
import fs from 'fs'
import os from 'os'
const s = fs.statfsSync(os.tmpdir())
console.log(s.bsize > 0 && s.blocks > 0 && s.bfree <= s.blocks && s.bavail <= s.bfree)
console.log(process.platform === 'win32' ? s.type === 0 && s.files === 0 && s.ffree === 0 : s.files > 0)
`, "true\ntrue")
}

func TestE2EProcessUmaskSameAsNode(t *testing.T) {
	assertSameAsNode(t, `
const old = process.umask()
console.log(typeof old, old >= 0 && old <= 0o777)
const prev = process.umask(0o077)
console.log(prev === old)
const cur = process.umask()
console.log(process.platform === 'win32' ? typeof cur : cur === 0o077)
console.log(process.umask(old) === (process.platform === 'win32' ? cur : 0o077))
console.log(process.umask() === old)
`)
}

func TestE2EProcessUmaskAffectsCreatedFiles(t *testing.T) {
	skipPOSIXToolsOnWindows(t, "Windows has no creation mask; the CRT's _umask is inert")
	assertOutputImports(t, `
import fs from 'fs'
import os from 'os'
import path from 'path'
const prev = process.umask(0o077)
const p = path.join(os.tmpdir(), 'kml-umask-' + process.pid)
fs.writeFileSync(p, 'x')
console.log((fs.statSync(p).mode & 0o777).toString(8))
fs.unlinkSync(p)
process.umask(prev)
`, "600")
}
