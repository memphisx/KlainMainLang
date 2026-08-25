// Static CommonJS `require('<literal>')` — Node-style module loading, accepted
// at the top level and desugared into the equivalent ES import at parse time
// (see docs/adr/ADR-00369.md), so it resolves into exactly the same dispatch as
// an `import`. Only the *static* form (a string-literal module path) is
// supported; a dynamic `require(variable)` is a clean compile error, because
// runtime/lazy module loading is a separate, deferred capability.

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

console.log('require works: ' + basename('/home/user/notes.txt'))
