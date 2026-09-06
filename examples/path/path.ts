// path — portable filesystem path manipulation (join/resolve/dirname/
// basename/extname/isAbsolute/parse/format/sep/delimiter). Import-gated
// (TDD-00049) — a virtual built-in module, not a real file. A bare `path`
// is the host's flavour, as in Node: path.posix on Linux/macOS (what the
// comments below show), path.win32 on Windows (backslashes, `;`). See
// path_win32.ts for the explicit, host-independent flavour objects.

import path from 'path'

// join concatenates segments with '/' and normalizes the result — collapsing
// repeated slashes, dropping '.' segments, and resolving '..' against
// whatever came before it in the same call.
console.log(path.join('a', 'b', 'c'))          // a/b/c
console.log(path.join('/a', 'b', '../c'))      // /a/c
console.log(path.join('a//b///c'))             // a/b/c

// resolve is like join, but always returns an absolute path — starting from
// process.cwd() and walking left to right, with any segment that itself
// starts with '/' resetting the result so far (matching real Node: the last
// absolute segment wins over everything before it, including cwd).
console.log(path.resolve('/foo', 'bar', 'baz'))  // /foo/bar/baz
console.log(path.resolve('/foo', '/bar', 'baz')) // /bar/baz

console.log(path.dirname('/foo/bar/baz.js'))   // /foo/bar
console.log(path.basename('/foo/bar/baz.js'))  // baz.js
console.log(path.basename('/foo/bar/baz.js', '.js')) // baz
console.log(path.extname('/foo/bar/baz.js'))   // .js

console.log(path.isAbsolute('/foo/bar')) // true
console.log(path.isAbsolute('foo/bar'))  // false

// parse decomposes a path into its parts; format does the reverse.
const parsed = path.parse('/home/user/dir/file.txt')
console.log(parsed.root) // /
console.log(parsed.dir)  // /home/user/dir
console.log(parsed.base) // file.txt
console.log(parsed.ext)  // .txt
console.log(parsed.name) // file
console.log(path.format(parsed)) // /home/user/dir/file.txt

// normalize keeps a trailing separator; relative walks from one absolute
// path to another; toNamespacedPath is the identity here (win32 adds \\?\).
console.log(path.normalize('/a/../b/./c/'))     // /b/c/
console.log(path.relative('/a/b/c/d', '/a/b'))  // ../..
console.log(path.toNamespacedPath('/a/b'))      // /a/b

console.log(path.sep)       // /
console.log(path.delimiter) // :
