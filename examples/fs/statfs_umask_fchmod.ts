// fs.statfsSync / fs.fchmodSync / process.umask (ADR-01078): file-system
// statistics of a volume, permission bits through an open descriptor, and the
// creation mask. Values are host state, so the checks are shape/relations.

import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'

// How much room the temp volume has, in bytes available to this user.
const s = fs.statfsSync(os.tmpdir())
console.log(Object.keys(s).join(',')) // type,bsize,frsize,blocks,bfree,bavail,files,ffree
console.log(s.bavail * s.bsize > 0, s.bavail <= s.blocks) // true true

// fchmodSync on an open fd; the mode reads back through fstatSync.
const p = path.join(os.tmpdir(), 'kml-fchmod-example-' + process.pid + '.txt')
fs.writeFileSync(p, 'x')
const fd = fs.openSync(p, 'r+')
fs.fchmodSync(fd, 0o444)
console.log((fs.fstatSync(fd).mode & 0o222) === 0) // true — read-only now
fs.fchmodSync(fd, 0o644)
fs.closeSync(fd)
fs.unlinkSync(p)

// The creation mask: read, set (returns the previous), restore.
const prev = process.umask()
const before = process.umask(0o077)
console.log(before === prev, typeof process.umask()) // true number
process.umask(prev)

// A missing path is Node's ENOENT with syscall 'statfs'.
try {
  fs.statfsSync(path.join(os.tmpdir(), 'kml-does-not-exist-' + process.pid))
} catch (err: any) {
  console.log(err.code, err.syscall) // ENOENT statfs
}
