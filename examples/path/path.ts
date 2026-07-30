// path — portable filesystem path manipulation (join/resolve/dirname/
// basename/extname/isAbsolute/parse/format/sep/delimiter). Recognized as a
// pseudo-namespace, like Math/JSON/fs/process — not a real importable
// module. POSIX-only: this compiler doesn't cross-compile, so sep is always
// '/' and delimiter is always ':'.

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

console.log(path.isAbsolute('/foo/bar')) // 1 (true)
console.log(path.isAbsolute('foo/bar'))  // 0 (false)

// parse decomposes a path into its parts; format does the reverse.
const parsed = path.parse('/home/user/dir/file.txt')
console.log(parsed.root) // /
console.log(parsed.dir)  // /home/user/dir
console.log(parsed.base) // file.txt
console.log(parsed.ext)  // .txt
console.log(parsed.name) // file
console.log(path.format(parsed)) // /home/user/dir/file.txt

console.log(path.sep)       // /
console.log(path.delimiter) // :
