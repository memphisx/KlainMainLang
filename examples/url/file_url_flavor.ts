// url.fileURLToPath / pathToFileURL with `{ windows }` (ADR-01079): a
// cross-platform tool can convert the *other* platform's file URLs and paths
// on any host — a literal picks the flavor at compile time, a run-time boolean
// runs both halves; `undefined` means the host's own.

import url from 'node:url'

// Windows drive and UNC URLs, on any host.
console.log(url.fileURLToPath('file:///C:/Users/me/a%20b.txt', { windows: true })) // C:\Users\me\a b.txt
console.log(url.fileURLToPath('file://server/share/dir/f.txt', { windows: true })) // \\server\share\dir\f.txt

// POSIX, on any host (a drive letter is just a path segment there).
console.log(url.fileURLToPath('file:///home/me/a%20b.txt', { windows: false })) // /home/me/a b.txt
console.log(url.fileURLToPath('file:///C:/x/y', { windows: false })) // /C:/x/y

// The reverse direction, both flavors.
console.log(url.pathToFileURL('C:\\Users\\me\\a b.txt', { windows: true }).href) // file:///C:/Users/me/a%20b.txt
console.log(url.pathToFileURL('/tmp/a b#c.txt', { windows: false }).href) // file:///tmp/a%20b%23c.txt

// A run-time choice: both halves are compiled, the boolean picks one.
const asWindows = process.platform === 'win32'
console.log(url.fileURLToPath('file:///C:/dyn/z', { windows: asWindows }) === url.fileURLToPath('file:///C:/dyn/z')) // true

// Node's errors, per flavor.
try { url.fileURLToPath('file:///home/x', { windows: true }) } catch (e) { console.log((e as Error).message) } // File URL path must be absolute
try { url.fileURLToPath('file://host/x', { windows: false }) } catch (e) { console.log((e as Error).message) } // File URL host must be "localhost" or empty on <platform>
