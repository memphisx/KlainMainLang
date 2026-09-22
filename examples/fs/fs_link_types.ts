// fs.symlinkSync(target, path, type) — the link type.
//
// The third argument only means something on Windows, where a *symlink* needs
// Developer Mode or elevation but a *junction* — a directory link — needs
// nothing. Node ignores it on POSIX, so the same program runs everywhere: the
// idiom for "link this directory, portably" is type 'junction'.
import fs from 'fs'
import os from 'os'
import path from 'path'

const root = fs.mkdtempSync(path.join(os.tmpdir(), 'kml-links-'))
const real = path.join(root, 'real')
const link = path.join(root, 'link')

fs.mkdirSync(real)
fs.writeFileSync(path.join(real, 'note.txt'), 'read through the link')

fs.symlinkSync(real, link, 'junction')

console.log('is a link:', fs.lstatSync(link).isSymbolicLink())
console.log('content:', fs.readFileSync(path.join(link, 'note.txt'), 'utf8'))

// readlink reports where it points. A junction stores an absolute path with a
// trailing separator; a POSIX symlink stores exactly what it was given.
const target = fs.readlinkSync(link)
console.log('points at real:', path.resolve(target) === path.resolve(real))

// Removing the link leaves the directory it pointed at alone.
fs.unlinkSync(link)
console.log('link gone:', !fs.existsSync(link), '- target kept:', fs.existsSync(path.join(real, 'note.txt')))

fs.rmSync(root, { recursive: true, force: true })
