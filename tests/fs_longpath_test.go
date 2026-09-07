package tests

import "testing"

// A filesystem path longer than Windows' historical MAX_PATH (260) works:
// the Win32 layer normalizes such a path and adds the `\\?\` extended-length
// prefix (ADR-00748), so create/write/read/stat/unlink all succeed where they
// would otherwise fail with a path-too-long error. POSIX handles long paths
// natively (well under PATH_MAX), so the test runs on every host.
func TestE2EFsLongPathOps(t *testing.T) {
	// os.tmpdir() + a nested tree whose full path clears 260 chars. Each
	// segment is 40 chars; six of them plus the tmpdir root is > 300.
	assertOutputImports(t, `
import * as fs from 'fs'
import os from 'os'
const seg = "kmllongpath_0123456789012345678901234567"
const base = fs.mkdtempSync(os.tmpdir() + '/kmllp-')
const deep = base + '/' + seg + '/' + seg + '/' + seg + '/' + seg + '/' + seg + '/' + seg
fs.mkdirSync(deep, { recursive: true })
const f = deep + '/data.txt'
fs.writeFileSync(f, 'long-path-ok')
console.log(f.length > 260)
console.log(fs.existsSync(f))
console.log(fs.readFileSync(f))
console.log(fs.statSync(f).size)
fs.unlinkSync(f)
console.log(fs.existsSync(f))
fs.rmSync(base, { recursive: true, force: true })
`, "true\ntrue\nlong-path-ok\n12\nfalse")
}
