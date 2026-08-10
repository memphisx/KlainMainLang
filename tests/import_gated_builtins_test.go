package tests

import (
	"strings"
	"testing"
)

// --- TDD-00049 Stage 1: import-gated built-in bindings (fs/path/os/...) ---
//
// Before this feature, codegen recognized fs/path/os/querystring/assert/
// http/cluster/Memory by bare AST-identifier-text matching, with no
// awareness of scope at all — a local variable sharing one of those names
// got silently miscompiled into a call to the built-in instead of the
// user's own code (see TDD-00049's Context section for the original,
// empirically-reproduced repro). These tests cover the fix: the built-in is
// now only reachable in a file that actually imported it, and real lexical
// scoping (a local variable of the same name) correctly shadows it. Stage
// 2 (named per-member imports) has its own section further down.

func TestE2EImportGatedFsDefaultImportWorks(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"main.ts": `
import fs from 'fs'

fs.writeFileSync("kml_test_import_fs.txt", "hello")
console.log(fs.readFileSync("kml_test_import_fs.txt"))
fs.unlinkSync("kml_test_import_fs.txt")
`,
	}, "main.ts", "hello")
}

func TestE2EImportGatedNamespaceStarFormWorks(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"main.ts": `
import * as fs from 'fs'

fs.writeFileSync("kml_test_import_fs_ns.txt", "namespace form")
console.log(fs.readFileSync("kml_test_import_fs_ns.txt"))
fs.unlinkSync("kml_test_import_fs_ns.txt")
`,
	}, "main.ts", "namespace form")
}

func TestE2EImportGatedAliasedLocalNameWorks(t *testing.T) {
	// The whole point of gating these behind import: the local binding name
	// is the program's own choice, not a fixed reserved word.
	assertMultiFileOutput(t, map[string]string{
		"main.ts": `
import myFileSystem from 'fs'

myFileSystem.writeFileSync("kml_test_import_fs_alias.txt", "aliased")
console.log(myFileSystem.readFileSync("kml_test_import_fs_alias.txt"))
myFileSystem.unlinkSync("kml_test_import_fs_alias.txt")
`,
	}, "main.ts", "aliased")
}

func TestE2EImportGatedMemoryFreeWorks(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"main.ts": `
import Memory from 'memory'

let arr: number[] = [1, 2, 3]
console.log(arr.length)
Memory.free(arr)
console.log("freed")
`,
	}, "main.ts", "3\nfreed")
}

func TestE2EImportGatedPathModuleWorks(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"main.ts": `
import path from 'path'

console.log(path.join("a", "b", "c.txt"))
console.log(path.extname("file.ts"))
`,
	}, "main.ts", "a/b/c.txt\n.ts")
}

// TestE2EImportGatedLocalShadowNoLongerMiscompiles is the direct regression
// test for the bug TDD-00049 found and empirically reproduced: a
// function-local variable named "fs" (never importing the real fs module)
// used to get silently routed to the real built-in fs.readFileSync instead
// of the user's own value, because dispatch matched on bare AST identifier
// text with no scope awareness. It must now correctly call the user's own
// function — matching real JS/TS lexical scoping, where a local variable
// always shadows an outer/ambient binding of the same name.
func TestE2EImportGatedLocalShadowNoLongerMiscompiles(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"main.ts": `
interface FakeFs {
    readFileSync: (path: string) => string
}

function useFakeFs(): void {
    let fs: FakeFs = {
        readFileSync: (path: string): string => {
            return "fake-fs:" + path
        }
    }
    console.log(fs.readFileSync("nope.txt"))
}

useFakeFs()
`,
	}, "main.ts", "fake-fs:nope.txt")
}

// TestE2EImportGatedShadowCoexistsWithRealImport confirms the fix holds
// even when the *same file* both imports the real fs module at module
// scope and separately shadows the name inside a function — real lexical
// scoping distinguishes the two by position, not by whether an import
// exists anywhere in the file.
func TestE2EImportGatedShadowCoexistsWithRealImport(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"main.ts": `
import fs from 'fs'

interface FakeFs {
    readFileSync: (path: string) => string
}

function useFakeFs(): void {
    let fs: FakeFs = {
        readFileSync: (path: string): string => {
            return "fake-fs:" + path
        }
    }
    console.log(fs.readFileSync("nope.txt"))
}

fs.writeFileSync("kml_test_import_fs_coexist.txt", "real-fs")
console.log(fs.readFileSync("kml_test_import_fs_coexist.txt"))
useFakeFs()
fs.unlinkSync("kml_test_import_fs_coexist.txt")
`,
	}, "main.ts", "real-fs\nfake-fs:nope.txt")
}

func TestE2EImportGatedMissingImportRejected(t *testing.T) {
	err := resolveAndEmitMultiFile(t, map[string]string{
		"main.ts": `
console.log(fs.readFileSync("whatever.txt"))
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for using fs.readFileSync without importing 'fs', got none")
	}
	if !strings.Contains(err.Error(), "fs") {
		t.Fatalf("expected the error to mention 'fs', got: %v", err)
	}
}

// --- TDD-00049 Stage 2: named per-member imports ---
//
// import { readFileSync } from 'fs' resolves entirely at compile time into
// a synthesized fs__kml_builtin.readFileSync member access (rename.go's
// rewriteExpr, *ast.Identifier case) — indistinguishable after resolution
// from Stage 1's hand-written `fs.readFileSync`, so it reaches every
// existing dispatch site with no codegen changes at all. These tests cover
// both call-shaped members (readFileSync) and plain-value members (os.EOL),
// aliasing, mixing a default and a named import in one statement, shadowing
// a named import with a real local variable, and rejecting an unknown
// member name.

func TestE2EImportGatedNamedImportFunctionMemberWorks(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"main.ts": `
import { readFileSync, writeFileSync, unlinkSync } from 'fs'

writeFileSync("kml_test_named_fn.txt", "hello named import")
console.log(readFileSync("kml_test_named_fn.txt"))
unlinkSync("kml_test_named_fn.txt")
`,
	}, "main.ts", "hello named import")
}

func TestE2EImportGatedNamedImportValueMemberWorks(t *testing.T) {
	// os.EOL isn't a method — a named import of a plain-value member (not
	// just a callable one) needs the same synthesized-member-access
	// treatment, exercised separately from the function-member case above.
	assertMultiFileOutput(t, map[string]string{
		"main.ts": `
import { EOL } from 'os'
console.log(EOL === "\n")
`,
	}, "main.ts", "1")
}

func TestE2EImportGatedNamedImportAliasedWorks(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"main.ts": `
import { readFileSync as rfs, writeFileSync as wfs, unlinkSync as del } from 'fs'

wfs("kml_test_named_alias.txt", "aliased named import")
console.log(rfs("kml_test_named_alias.txt"))
del("kml_test_named_alias.txt")
`,
	}, "main.ts", "aliased named import")
}

func TestE2EImportGatedNamedImportCombinedWithDefaultWorks(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"main.ts": `
import fs, { existsSync } from 'fs'

console.log(existsSync("kml_test_named_combined.txt"))
fs.writeFileSync("kml_test_named_combined.txt", "combined")
console.log(existsSync("kml_test_named_combined.txt"))
fs.unlinkSync("kml_test_named_combined.txt")
`,
	}, "main.ts", "0\n1")
}

func TestE2EImportGatedNamedImportShadowedByLocalWorks(t *testing.T) {
	assertMultiFileOutput(t, map[string]string{
		"main.ts": `
import { readFileSync } from 'fs'

function useFake(): void {
    let readFileSync: (path: string) => string = (path: string): string => {
        return "shadowed:" + path
    }
    console.log(readFileSync("nope.txt"))
}

useFake()
`,
	}, "main.ts", "shadowed:nope.txt")
}

func TestE2EImportGatedNamedImportUnknownMemberRejected(t *testing.T) {
	_, err := resolveMultiFile(t, map[string]string{
		"main.ts": `
import { bogusMethod } from 'fs'
console.log(bogusMethod())
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for importing a member 'fs' doesn't have, got none")
	}
	if !strings.Contains(err.Error(), "bogusMethod") {
		t.Fatalf("expected the error to mention 'bogusMethod', got: %v", err)
	}
}
