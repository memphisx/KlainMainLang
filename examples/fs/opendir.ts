// fs.opendir — walk a directory through a Dir handle, a batch of entries
// at a time, instead of reading the whole listing at once.
//
// opendirSync's readSync() hands out one Dirent per call (null at the end);
// fs.promises.opendir's Dir is async-iterable and closes itself when the
// loop finishes. `recursive: true` descends into each subdirectory as it is
// reached.
import fs from 'fs'
import { opendir } from 'fs/promises'
import os from 'os'
import path from 'path'

const root = fs.mkdtempSync(path.join(os.tmpdir(), 'kml-opendir-'))
fs.mkdirSync(path.join(root, 'src', 'lib'), { recursive: true })
for (const f of ['README.md', 'src/main.ts', 'src/lib/util.ts', 'src/lib/io.ts']) {
  fs.writeFileSync(path.join(root, f), '')
}

// One level, synchronously.
const dir = fs.opendirSync(root)
const top: string[] = []
for (let e = dir.readSync(); e !== null; e = dir.readSync()) {
  top.push(e.isDirectory() ? e.name + '/' : e.name)
}
dir.closeSync()
console.log('top level:', top.sort())

// Every file below root, as the handle reaches it.
async function sources(): Promise<string[]> {
  const out: string[] = []
  for await (const e of await opendir(root, { recursive: true })) {
    if (e.isFile() && e.name.endsWith('.ts')) out.push(path.relative(root, path.join(e.parentPath, e.name)))
  }
  return out.sort()
}

sources().then((files) => {
  console.log('sources:', files)
  fs.rmSync(root, { recursive: true })
})
