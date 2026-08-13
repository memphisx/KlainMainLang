package tests

import "testing"

// --- path (join/resolve/dirname/basename/extname/isAbsolute/parse/format/sep/delimiter) ---

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
	assertOutputImports(t, src, "a/b/c\n/a/c\na/b\n.\na\nfoo\na/b/c")
}

func TestE2EPathResolve(t *testing.T) {
	src := `
import path from 'path'
console.log(path.resolve("/foo", "bar", "baz"))
console.log(path.resolve("/foo", "/bar", "baz"))
console.log(path.resolve("/a", "..", "..", "b"))
`
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
`
	assertOutputImports(t, src, "true\nfalse")
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
	assertOutputImports(t, src, "/\n/home/user/dir\nfile.txt\n.txt\nfile\n/home/user/dir/file.txt")
}

func TestE2EPathSepDelimiter(t *testing.T) {
	src := `
import path from 'path'
console.log(path.sep)
console.log(path.delimiter)
`
	assertOutputImports(t, src, "/\n:")
}
