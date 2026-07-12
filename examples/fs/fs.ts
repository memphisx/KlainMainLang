// fs — synchronous file I/O (readFileSync/writeFileSync/appendFileSync/
// existsSync/unlinkSync/mkdirSync/rmdirSync/renameSync/copyFileSync/
// readdirSync). Recognized as a pseudo-namespace, like Math/JSON/process —
// not a real importable module (there's no `import fs from 'fs'` here, just
// a global `fs` the compiler special-cases).
//
// Everything here is synchronous and blocking — there's no event loop in
// this compiler, so there's no non-blocking variant to offer. Text-only,
// like every string in this language: a file containing embedded null
// bytes would read back shorter than its real size (the same limitation
// fetch's response bodies have, for the same underlying reason — see
// examples/fetch/fetch.ts).

const path: string = '/tmp/kml_fs_example.txt'

console.log(fs.existsSync(path))   // 0 (false) — nothing there yet

fs.writeFileSync(path, 'first line')
console.log(fs.existsSync(path))   // 1 (true)

const content: string = fs.readFileSync(path)
console.log(content)               // first line
console.log(content.length)        // 10

// writeFileSync truncates — a second write replaces the content entirely
fs.writeFileSync(path, 'replaced')
console.log(fs.readFileSync(path)) // replaced

// appendFileSync adds on, creating the file if it doesn't exist yet
fs.appendFileSync(path, '\nsecond line')
console.log(fs.readFileSync(path)) // replaced\nsecond line

// A failed read/write/append/delete throws a catchable Error, built from
// the OS's own reason (via strerror(errno)) — same approach as fetch's
// network-failure handling.
try {
    fs.readFileSync('/definitely/does/not/exist/kml-example.txt')
} catch (e) {
    console.log('caught: ' + e.message)
}

// existsSync itself never throws for a missing path — it's one of the few
// fs functions (matching real Node) that reports "doesn't exist" as a plain
// false rather than an error.
console.log(fs.existsSync('/definitely/does/not/exist/kml-example.txt'))  // 0

fs.unlinkSync(path)
console.log(fs.existsSync(path))   // 0 (false) — cleaned up

// --- mkdirSync / readdirSync / renameSync / copyFileSync ---

const dir: string = '/tmp/kml_fs_example_dir'
fs.mkdirSync(dir)
console.log(fs.existsSync(dir))    // 1 (true)

fs.writeFileSync(dir + '/a.txt', 'file a')
fs.writeFileSync(dir + '/b.txt', 'file b')

// readdirSync lists entries (excluding "." and ".."), in whatever order the
// OS's own readdir() returns them — sort for a deterministic printout.
const entries: string[] = fs.readdirSync(dir)
console.log(entries.length)   // 2
entries.sort()
for (const name of entries) {
    console.log(name)   // a.txt, then b.txt
}

// renameSync moves/renames a file in place
fs.renameSync(dir + '/a.txt', dir + '/a_renamed.txt')
console.log(fs.existsSync(dir + '/a.txt'))            // 0
console.log(fs.existsSync(dir + '/a_renamed.txt'))    // 1

// copyFileSync — reads the source fully, then writes it to dest; both files
// exist independently afterward with the same content
fs.copyFileSync(dir + '/a_renamed.txt', dir + '/a_copy.txt')
console.log(fs.readFileSync(dir + '/a_copy.txt'))   // file a

// mkdirSync throws if the directory already exists (no {recursive: true}
// option in this compiler — always the plain, non-recursive mkdir())
try {
    fs.mkdirSync(dir)
} catch (e) {
    console.log('caught: directory already exists')
}

// clean up
fs.unlinkSync(dir + '/a_renamed.txt')
fs.unlinkSync(dir + '/a_copy.txt')
fs.unlinkSync(dir + '/b.txt')
console.log(fs.readdirSync(dir).length)   // 0 — empty again

// rmdirSync removes an empty directory (no {recursive: true} option — it
// only ever removes a directory that's already empty, just like mkdirSync
// has no recursive-create option)
fs.rmdirSync(dir)
console.log(fs.existsSync(dir))   // 0 (false) — cleaned up
