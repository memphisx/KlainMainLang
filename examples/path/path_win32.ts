// path.win32 / path.posix — Node exposes both flavours of the `path` module
// on every platform (TDD-00178). A bare `path` is the host's flavour: on
// Windows it *is* path.win32 (backslash separator, `;` delimiter, drive
// letters, UNC roots); on Linux/macOS it is path.posix. Naming a flavour
// explicitly gives the same answers on every host, which is what this
// example does so its output never depends on where it runs.

import path from 'path'

console.log(path.win32.sep + ' ' + path.win32.delimiter) // \ ;
console.log(path.posix.sep + ' ' + path.posix.delimiter) // / :

// Mixed separators are accepted and normalised to `\`; a trailing separator
// is kept, and `..` resolves against a drive root without escaping it.
console.log(path.win32.join('C:\\Users', 'me', '..', 'you'))   // C:\Users\you
console.log(path.win32.join('C:/Users', 'me'))                 // C:\Users\me
console.log(path.win32.join('foo', 'bar\\'))                   // foo\bar\
console.log(path.win32.join('c:\\a\\b', '..\\..\\..\\c'))      // c:\c
console.log(path.win32.join('\\\\server\\share', 'x'))         // \\server\share\x

// resolve walks right to left and keeps the last device it sees; a segment
// rooted on another drive wins outright.
console.log(path.win32.resolve('C:\\home', 'foo', '..', 'baz')) // C:\home\foo\baz -> C:\home\baz
console.log(path.win32.resolve('C:\\home', 'D:\\x', 'y'))       // D:\x\y
console.log(path.win32.resolve('C:\\home', '\\abs'))            // C:\abs

console.log(path.win32.dirname('C:\\a\\b\\c'))                 // C:\a\b
console.log(path.win32.dirname('\\\\server\\share'))           // \\server\share (a UNC root is its own dirname)
console.log(path.win32.basename('C:\\foo\\bar\\baz.js', '.js')) // baz
console.log(path.win32.extname('C:\\a\\b.c'))                  // .c

// Drive-relative (`C:x`) is not absolute; `\x`, `/x` and `C:\x` are.
console.log(path.win32.isAbsolute('C:x'))    // false
console.log(path.win32.isAbsolute('C:\\x'))  // true
console.log(path.win32.isAbsolute('/x'))     // true

const p = path.win32.parse('C:\\home\\user\\dir\\file.txt')
console.log(p.root) // C:\
console.log(p.dir)  // C:\home\user\dir
console.log(p.base) // file.txt
console.log(p.ext)  // .txt
console.log(p.name) // file
console.log(path.win32.format(p)) // C:\home\user\dir\file.txt

// normalize keeps a trailing separator (unlike join's posix caveat), relative
// resolves both ends first and walks up with `..`, and toNamespacedPath gives
// the `\\?\` long-path form (a no-op in the posix flavour).
console.log(path.win32.normalize('C:/a//b/'))                     // C:\a\b\
console.log(path.win32.relative('C:\\a\\b\\c\\d', 'C:\\a\\b'))    // ..\..
console.log(path.win32.relative('C:\\a\\b', 'D:\\x'))             // D:\x (different drive: absolute)
console.log(path.win32.toNamespacedPath('\\\\srv\\sh\\x'))        // \\?\UNC\srv\sh\x
console.log(path.posix.normalize('/a/../b/./c/'))                 // /b/c/
console.log(path.posix.relative('/a/b', '/a/c'))                  // ../c
console.log(path.posix.toNamespacedPath('/a/b'))                  // /a/b

// The posix flavour, named explicitly, ignores drive letters entirely.
console.log(path.posix.isAbsolute('C:\\x'))  // false
console.log(path.posix.join('/a', 'b', '../c')) // /a/c
