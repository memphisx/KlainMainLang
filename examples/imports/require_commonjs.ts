// CommonJS `require` — Node-style module loading. A top-level
// `require('<literal>')` is desugared into the equivalent ES import at parse
// time (see docs/adr/00/ADR-00369.md); a `require` anywhere else is the same
// module, linked statically, and `require` itself is a value that answers
// from the modules the program links.

// `const x = require('mod')` binds the whole module object — the namespace form,
// which (unlike a default import) also works for a module that only has named
// exports, matching CommonJS's "require returns module.exports".
const path = require('path')

// `const { a, b } = require('mod')` binds individual members — a named import.
const { basename, extname } = require('path')

// require and import interoperate freely in the same file.
import assert from 'assert'

assert.strictEqual(path.basename('/usr/local/bin/klainmain'), 'klainmain')
assert.strictEqual(basename('/a/b/report.md'), 'report.md')
assert.strictEqual(extname('report.md'), '.md')
assert.strictEqual(path.basename('archive.tar.gz', '.gz'), 'archive.tar')

// A `require` inside a function, and one by a computed name.
function platformOf(): string {
    const os = require('os')
    return typeof os.platform()
}
const moduleName = ['pa', 'th'].join('')
assert.strictEqual(require(moduleName), require('node:path'))
assert.strictEqual(platformOf(), 'string')

console.log('require works: ' + basename('/home/user/notes.txt'))
