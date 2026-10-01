package tests

import "testing"

// `path` as Node's lib/path.js in TypeScript (ADR-01290), compared with
// Node's output.

func TestE2EPathPort(t *testing.T) {
	assertOutputImports(t, `
import path from 'path'
import { join, posix, win32, sep } from 'path'
const cases = ['/a/b/../c/./d', 'a//b/', '', '.', '..', '/', 'C:\\x\\..\\y', '//server/share/x', 'a/b.tar.gz', '.bashrc', 'dir/']
for (const c of cases) {
  console.log(JSON.stringify([path.normalize(c || '.'), path.dirname(c), path.basename(c), path.extname(c), path.isAbsolute(c), path.parse(c)]))
  console.log(JSON.stringify([win32.normalize(c || '.'), win32.dirname(c), win32.basename(c), win32.extname(c), win32.isAbsolute(c), win32.parse(c)]))
}
console.log(join('a', 'b', '../c'), path.join(), posix.join('/x', 'y'), win32.join('C:\\', 'a', '..', 'b'), sep, path.delimiter, win32.sep)
console.log(path.relative('/data/orandea/test/aaa', '/data/orandea/impl/bbb'), win32.relative('C:\\orandea\\test\\aaa', 'C:\\orandea\\impl\\bbb'))
console.log(path.resolve('/foo/bar', './baz'), path.resolve('/foo/bar', '/tmp/file/'), path.format({ dir: '/home/user/dir', base: 'file.txt' }), path.format({ root: '/', name: 'file', ext: 'txt' }))
console.log(path.basename('/foo/bar/quux.html', '.html'), path.posix === posix, path.win32 === win32, posix.win32 === win32, path.toNamespacedPath('/x'), win32.toNamespacedPath('C:\\x'))
`, `["/a/c/d","/a/b/../c/.","d","",true,{"root":"/","dir":"/a/b/../c/.","base":"d","ext":"","name":"d"}]
["\\a\\c\\d","/a/b/../c/.","d","",true,{"root":"/","dir":"/a/b/../c/.","base":"d","ext":"","name":"d"}]
["a/b/","a/","b","",false,{"root":"","dir":"a/","base":"b","ext":"","name":"b"}]
["a\\b\\","a/","b","",false,{"root":"","dir":"a/","base":"b","ext":"","name":"b"}]
[".",".","","",false,{"root":"","dir":"","base":"","ext":"","name":""}]
[".",".","","",false,{"root":"","dir":"","base":"","ext":"","name":""}]
[".",".",".","",false,{"root":"","dir":"","base":".","ext":"","name":"."}]
[".",".",".","",false,{"root":"","dir":"","base":".","ext":"","name":"."}]
["..",".","..","",false,{"root":"","dir":"","base":"..","ext":"","name":".."}]
["..",".","..","",false,{"root":"","dir":"","base":"..","ext":"","name":".."}]
["/","/","","",true,{"root":"/","dir":"/","base":"","ext":"","name":""}]
["\\","/","","",true,{"root":"/","dir":"/","base":"","ext":"","name":""}]
["C:\\x\\..\\y",".","C:\\x\\..\\y",".\\y",false,{"root":"","dir":"","base":"C:\\x\\..\\y","ext":".\\y","name":"C:\\x\\."}]
["C:\\y","C:\\x\\..","y","",true,{"root":"C:\\","dir":"C:\\x\\..","base":"y","ext":"","name":"y"}]
["/server/share/x","//server/share","x","",true,{"root":"/","dir":"//server/share","base":"x","ext":"","name":"x"}]
["\\\\server\\share\\x","//server/share/","x","",true,{"root":"//server/share/","dir":"//server/share/","base":"x","ext":"","name":"x"}]
["a/b.tar.gz","a","b.tar.gz",".gz",false,{"root":"","dir":"a","base":"b.tar.gz","ext":".gz","name":"b.tar"}]
["a\\b.tar.gz","a","b.tar.gz",".gz",false,{"root":"","dir":"a","base":"b.tar.gz","ext":".gz","name":"b.tar"}]
[".bashrc",".",".bashrc","",false,{"root":"","dir":"","base":".bashrc","ext":"","name":".bashrc"}]
[".bashrc",".",".bashrc","",false,{"root":"","dir":"","base":".bashrc","ext":"","name":".bashrc"}]
["dir/",".","dir","",false,{"root":"","dir":"","base":"dir","ext":"","name":"dir"}]
["dir\\",".","dir","",false,{"root":"","dir":"","base":"dir","ext":"","name":"dir"}]
a/c . /x/y C:\b / : \
../../impl/bbb ..\..\impl\bbb
/foo/bar/baz /tmp/file /home/user/dir/file.txt /file.txt
quux true true true /x \\?\C:\x`)
}

func TestE2EPathFlavourModules(t *testing.T) {
	assertOutputImports(t, `
import pp from 'path/posix'
import { join } from 'path/win32'
console.log(pp.join('a', 'b'), join('a', 'b'), pp.sep)
`, "a/b a\\b /")
}

func TestE2EObjectFunctionFieldNamedLikeBuiltin(t *testing.T) {
	assertOutput(t, `
const o = { join: (a: string, b: string): string => a + b, map: (x: number): number => x * 2 }
console.log(o.join('a', 'b'), o.map(21))
`, "ab 42")
}

func TestE2EPathFlavoursAsValues(t *testing.T) {
	assertOutputImports(t, `
import path from 'path'
import type { PlatformPath } from 'path'
const p = path.win32
console.log(p.join('a', 'b'), p.sep)
function use(q: PlatformPath) { return q.basename('/x/y.z') }
function use2(q: typeof path.posix) { return q.extname('/x/y.z') }
console.log(use(path.posix), use2(path.posix))
`, "a\\b \\\ny.z .z")
}
