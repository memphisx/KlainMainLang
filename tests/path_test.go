package tests

import (
	"runtime"
	"testing"
)

// --- path (join/resolve/dirname/basename/extname/isAbsolute/parse/format/sep/delimiter) ---
//
// A bare `path` is the host's flavour (TDD-00178): win32 on Windows, posix
// elsewhere — so each bare-`path` test carries a Windows expectation. The
// explicit `path.posix` / `path.win32` tests below run identically on every
// host; they are what exercises the win32 C port on the Linux and macOS lanes.

// pathHost picks the expectation for the host's `path` flavour.
func pathHost(posix, win32 string) string {
	if runtime.GOOS == "windows" {
		return win32
	}
	return posix
}

func TestE2EPathJoin(t *testing.T) {
	src := `
import path from 'path'
console.log(path.join("a", "b", "c"))
console.log(path.join("/a", "b", "../c"))
console.log(path.join("a", "./b", "."))
console.log(path.join())
console.log(path.join("a"))
console.log(path.join("", "foo"))
console.log(path.join("a//b///c"))
`
	assertOutputImports(t, src, pathHost(
		"a/b/c\n/a/c\na/b\n.\na\nfoo\na/b/c",
		"a\\b\\c\n\\a\\c\na\\b\n.\na\nfoo\na\\b\\c"))
}

func TestE2EPathResolve(t *testing.T) {
	src := `
import path from 'path'
console.log(path.resolve("/foo", "bar", "baz"))
console.log(path.resolve("/foo", "/bar", "baz"))
console.log(path.resolve("/a", "..", "..", "b"))
`
	if runtime.GOOS == "windows" {
		// A rooted-but-driveless segment takes the drive of process.cwd().
		src = `
import path from 'path'
const drive = process.cwd().slice(0, 2)
console.log(path.resolve("/foo", "bar", "baz") === drive + "\\foo\\bar\\baz")
console.log(path.resolve("/foo", "/bar", "baz") === drive + "\\bar\\baz")
console.log(path.resolve("/a", "..", "..", "b") === drive + "\\b")
`
		assertOutputImports(t, src, "true\ntrue\ntrue")
		return
	}
	assertOutputImports(t, src, "/foo/bar/baz\n/bar/baz\n/b")
}

func TestE2EPathDirname(t *testing.T) {
	src := `
import path from 'path'
console.log(path.dirname("/a/b/c"))
console.log(path.dirname("a/b"))
console.log(path.dirname("a"))
console.log(path.dirname("/a"))
console.log(path.dirname("/"))
console.log(path.dirname("/a/b/c/"))
`
	// win32 dirname keeps the input's own separators (it slices, never rewrites).
	assertOutputImports(t, src, "/a/b\na\n.\n/\n/\n/a/b")
}

func TestE2EPathBasename(t *testing.T) {
	src := `
import path from 'path'
console.log(path.basename("/foo/bar/baz.js"))
console.log(path.basename("/foo/bar/baz.js", ".js"))
console.log(path.basename("/foo/.js", ".js"))
console.log(path.basename(".js", ".js"))
console.log(path.basename("/"))
console.log(path.basename(""))
`
	assertOutputImports(t, src, "baz.js\nbaz\n.js\n\n\n")
}

func TestE2EPathExtname(t *testing.T) {
	src := `
import path from 'path'
console.log(path.extname("index.html"))
console.log(path.extname("index."))
console.log(path.extname("index"))
console.log(path.extname(".index"))
console.log(path.extname("index.coffee.md"))
console.log(path.extname("..test"))
`
	assertOutputImports(t, src, ".html\n.\n\n\n.md\n.test")
}

func TestE2EPathIsAbsolute(t *testing.T) {
	src := `
import path from 'path'
console.log(path.isAbsolute("/foo/bar"))
console.log(path.isAbsolute("foo/bar"))
console.log(path.isAbsolute("C:\\foo"))
`
	// A drive-letter path is absolute only to the win32 flavour.
	assertOutputImports(t, src, pathHost("true\nfalse\nfalse", "true\nfalse\ntrue"))
}

func TestE2EPathParseFormat(t *testing.T) {
	src := `
import path from 'path'
const p = path.parse("/home/user/dir/file.txt")
console.log(p.root)
console.log(p.dir)
console.log(p.base)
console.log(p.ext)
console.log(p.name)
console.log(path.format(p))
`
	assertOutputImports(t, src, pathHost(
		"/\n/home/user/dir\nfile.txt\n.txt\nfile\n/home/user/dir/file.txt",
		"/\n/home/user/dir\nfile.txt\n.txt\nfile\n/home/user/dir\\file.txt"))
}

func TestE2EPathSepDelimiter(t *testing.T) {
	src := `
import path from 'path'
console.log(path.sep)
console.log(path.delimiter)
`
	assertOutputImports(t, src, pathHost("/\n:", "\\\n;"))
}

// --- explicit flavours: path.posix / path.win32 (TDD-00178), host-independent ---

func TestE2EPathPosixExplicit(t *testing.T) {
	src := `
import path from 'path'
console.log(path.posix.sep + path.posix.delimiter)
console.log(path.posix.join("/a", "b", "../c"))
console.log(path.posix.dirname("/a/b/c"))
console.log(path.posix.basename("/foo/bar/baz.js", ".js"))
console.log(path.posix.extname("index.coffee.md"))
console.log(path.posix.isAbsolute("C:\\x"))
console.log(path.posix.resolve("/foo", "/bar", "baz"))
const p = path.posix.parse("/home/user/dir/file.txt")
console.log(p.root + "|" + p.dir + "|" + p.base + "|" + p.ext + "|" + p.name)
console.log(path.posix.format(p))
`
	assertOutputImports(t, src, "/:\n/a/c\n/a/b\nbaz\n.md\nfalse\n/bar/baz\n/|/home/user/dir|file.txt|.txt|file\n/home/user/dir/file.txt")
}

func TestE2EPathWin32Join(t *testing.T) {
	src := `
import path from 'path'
console.log(path.win32.sep + path.win32.delimiter)
console.log(path.win32.join("C:\\Users", "me", "..", "you"))
console.log(path.win32.join("C:/Users", "me"))
console.log(path.win32.join("a//b///c"))
console.log(path.win32.join("foo", "bar\\"))
console.log(path.win32.join("\\\\server\\share", "x"))
console.log(path.win32.join("//server/share", "x"))
console.log(path.win32.join("C:", "foo"))
console.log(path.win32.join("C:foo", "bar"))
console.log(path.win32.join("..", "..", "a"))
console.log(path.win32.join("a", "..", ".."))
console.log(path.win32.join("c:\\a\\b", "..\\..\\..\\c"))
console.log(path.win32.join())
console.log(path.win32.join("", ""))
`
	assertOutputImports(t, src, "\\;\nC:\\Users\\you\nC:\\Users\\me\na\\b\\c\nfoo\\bar\\\n\\\\server\\share\\x\n\\\\server\\share\\x\nC:\\foo\nC:foo\\bar\n..\\..\\a\n..\nc:\\c\n.\n.")
}

func TestE2EPathWin32Resolve(t *testing.T) {
	// Every case pins its device or is rooted on one, so process.cwd() never
	// leaks into the result and the expectation holds on any host.
	src := `
import path from 'path'
console.log(path.win32.resolve("C:\\home", "foo", "bar", "..", "baz"))
console.log(path.win32.resolve("C:\\home", "D:\\x", "y"))
console.log(path.win32.resolve("C:\\home", "C:\\a", "D:\\b", "c"))
console.log(path.win32.resolve("C:\\home", "\\abs"))
console.log(path.win32.resolve("C:\\home", "..", "..", "..", "q"))
console.log(path.win32.resolve("\\\\srv\\share\\dir", "f"))
console.log(path.win32.resolve("C:\\home", "a\\"))
console.log(path.win32.resolve("C:\\home", "c:x", "y"))
console.log(path.win32.resolve("q").length > 2)
`
	assertOutputImports(t, src, "C:\\home\\foo\\baz\nD:\\x\\y\nD:\\b\\c\nC:\\abs\nC:\\q\n\\\\srv\\share\\dir\\f\nC:\\home\\a\nc:\\home\\x\\y\ntrue")
}

func TestE2EPathWin32DirnameBasenameExtname(t *testing.T) {
	src := `
import path from 'path'
console.log(path.win32.dirname("C:\\a\\b\\c"))
console.log(path.win32.dirname("C:\\a"))
console.log(path.win32.dirname("C:\\"))
console.log(path.win32.dirname("C:foo"))
console.log(path.win32.dirname("a\\b"))
console.log(path.win32.dirname("\\\\server\\share\\x\\y"))
console.log(path.win32.dirname("\\\\server\\share"))
console.log(path.win32.dirname("//server/share/x/y/"))
console.log(path.win32.basename("C:\\foo\\bar\\baz.js"))
console.log(path.win32.basename("C:\\foo\\bar\\baz.js", ".js"))
console.log(path.win32.basename("C:\\foo\\.js", ".js"))
console.log(path.win32.basename("C:\\"))
console.log(path.win32.basename("C:foo.txt", ".txt"))
console.log(path.win32.basename("aaa\\bbb", "bb"))
console.log(path.win32.extname("C:\\a\\b.c"))
console.log(path.win32.extname("index.coffee.md"))
console.log(path.win32.extname(".index"))
console.log(path.win32.extname("a.b\\"))
`
	// A UNC root (`\\server\share`) is its own dirname, as in Node.
	assertOutputImports(t, src, "C:\\a\\b\nC:\\\nC:\\\nC:\na\n\\\\server\\share\\x\n\\\\server\\share\n//server/share/x\nbaz.js\nbaz\n.js\n\nfoo\nb\n.c\n.md\n\n.b")
}

func TestE2EPathWin32IsAbsoluteParseFormat(t *testing.T) {
	src := `
import path from 'path'
console.log(path.win32.isAbsolute("C:\\x"))
console.log(path.win32.isAbsolute("C:x"))
console.log(path.win32.isAbsolute("\\x"))
console.log(path.win32.isAbsolute("/x"))
console.log(path.win32.isAbsolute("x"))
console.log(path.win32.isAbsolute("\\\\srv\\share"))
const p = path.win32.parse("C:\\home\\user\\dir\\file.txt")
console.log(p.root + "|" + p.dir + "|" + p.base + "|" + p.ext + "|" + p.name)
console.log(path.win32.format(p))
const q = path.win32.parse("\\\\server\\share\\a.b")
console.log(q.root + "|" + q.dir + "|" + q.base + "|" + q.ext + "|" + q.name)
const r = path.win32.parse("C:foo")
console.log(r.root + "|" + r.dir + "|" + r.base + "|" + r.ext + "|" + r.name)
const s = path.win32.parse(".bashrc")
console.log(s.root + "|" + s.dir + "|" + s.base + "|" + s.ext + "|" + s.name)
console.log(path.win32.format({ root: "C:\\", dir: "C:\\", base: "file.txt", ext: ".txt", name: "file" }))
console.log(path.win32.format({ root: "", dir: "", base: "", ext: "txt", name: "file" }))
`
	// parse of a UNC path reports the share root with its trailing separator
	// as both root and dir (`\\server\share\`), as in Node.
	assertOutputImports(t, src, "true\nfalse\ntrue\ntrue\nfalse\ntrue\nC:\\|C:\\home\\user\\dir|file.txt|.txt|file\nC:\\home\\user\\dir\\file.txt\n\\\\server\\share\\|\\\\server\\share\\|a.b|.b|a\nC:|C:|foo||foo\n||.bashrc||.bashrc\nC:\\file.txt\nfile.txt")
}

// normalize / relative / toNamespacedPath, both flavours (ADR-00723).

func TestE2EPathNormalizeBothFlavours(t *testing.T) {
	src := `
import path from 'path'
console.log(path.posix.normalize("/a/../b/./c"))
console.log(path.posix.normalize("a//b/"))
console.log(path.posix.normalize(""))
console.log(path.posix.normalize("./"))
console.log(path.posix.normalize("a/../.."))
console.log(path.posix.normalize("/a/../.."))
console.log(path.win32.normalize("C:\\a\\..\\b\\.\\c"))
console.log(path.win32.normalize("C:/a//b/"))
console.log(path.win32.normalize("\\\\server\\share\\a\\..\\b"))
console.log(path.win32.normalize("..\\..\\a"))
console.log(path.win32.normalize("C:"))
console.log(path.win32.normalize("COM1:\\x"))
console.log(path.normalize("a/../b/"))
`
	assertOutputImports(t, src, "/b/c\na/b/\n.\n./\n..\n/\nC:\\b\\c\nC:\\a\\b\\\n\\\\server\\share\\b\n..\\..\\a\nC:.\n.\\COM1:x\n"+pathHost("b/", "b\\"))
}

func TestE2EPathRelativeBothFlavours(t *testing.T) {
	// Absolute inputs only, so process.cwd() never enters the result.
	src := `
import path from 'path'
console.log(path.posix.relative("/a/b", "/a/b/c/d"))
console.log(path.posix.relative("/a/b/c/d", "/a/b"))
console.log(path.posix.relative("/a", "/a"))
console.log(path.posix.relative("/a/b", "/a/c"))
console.log(path.posix.relative("/", "/a"))
console.log(path.posix.relative("/aaa", "/aab"))
console.log(path.win32.relative("C:\\a\\b", "C:\\a\\b\\c\\d"))
console.log(path.win32.relative("C:\\a\\b\\c\\d", "C:\\a\\b"))
console.log(path.win32.relative("C:\\a\\b", "D:\\x"))
console.log(path.win32.relative("c:\\A\\B", "C:\\a\\b\\c"))
console.log(path.win32.relative("C:\\", "C:\\a"))
console.log(path.win32.relative("\\\\srv\\sh\\a", "\\\\srv\\sh\\b\\c"))
console.log(path.win32.relative("C:\\aaa\\b", "C:\\aa"))
console.log(path.relative("/a/b", "/a/b/c/d"))
`
	assertOutputImports(t, src, "c/d\n../..\n\n../c\na\n../aab\nc\\d\n..\\..\nD:\\x\nc\na\n..\\b\\c\n..\\..\\aa\n"+pathHost("c/d", "c\\d"))
}

func TestE2EPathToNamespacedPathBothFlavours(t *testing.T) {
	src := `
import path from 'path'
console.log(path.posix.toNamespacedPath("/a/b"))
console.log(path.posix.toNamespacedPath("x"))
console.log(path.win32.toNamespacedPath("C:\\a\\b"))
console.log(path.win32.toNamespacedPath("\\\\srv\\sh\\x"))
console.log(path.win32.toNamespacedPath("\\\\?\\C:\\x"))
console.log(path.win32.toNamespacedPath("\\\\.\\pipe\\p"))
console.log(path.win32.toNamespacedPath(""))
console.log(path.win32.toNamespacedPath("c").length > 5)
`
	assertOutputImports(t, src, "/a/b\nx\n\\\\?\\C:\\a\\b\n\\\\?\\UNC\\srv\\sh\\x\n\\\\?\\C:\\x\n\\\\.\\pipe\\p\n\ntrue")
}

func TestE2EPathNamedFlavourImport(t *testing.T) {
	src := `
import { win32, posix } from 'path'
console.log(win32.join("a", "b"))
console.log(posix.join("a", "b"))
console.log(win32.sep + posix.sep)
`
	assertOutputImports(t, src, "a\\b\na/b\n\\/")
}
