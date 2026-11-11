package tests

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
)

// assertSameAsNodeCLIJSFiles writes files, compiles main.js through the CLI
// under -compat=js and compares the binary's output with Node running the
// same main.js in the same directory.
func assertSameAsNodeCLIJSFiles(t *testing.T, files map[string]string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not found in PATH")
	}
	dir := writeMultiFile(t, files)
	entry := filepath.Join(dir, "main.js")
	nb, err := exec.Command(node, entry).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, nb)
	}
	theirs := strings.TrimRight(string(nb), "\n")
	out := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if b, err := exec.Command(buildCLI(t), "-compat=js", "-o", out, entry).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, b)
	}
	raw, err := exec.Command(out).Output()
	if err != nil {
		t.Fatalf("run: %v%s", err, exitStderr(err))
	}
	if ours := strings.TrimRight(string(raw), "\n"); ours != theirs {
		t.Fatalf("output differs from node:\n--- ours ---\n%s\n--- node ---\n%s", ours, theirs)
	}
}

func TestE2ECommonJSModuleExports(t *testing.T) {
	// A CommonJS file's module.exports is what require gives, whichever way
	// it was built: replaced by a function, or grown through exports.
	assertSameAsNodeCLIJSFiles(t, map[string]string{
		"add.js": `function add(a, b) { return a + b; }
module.exports = add;
module.exports.twice = (x) => add(x, x);
`,
		"ctr.js": `let count = 0;
exports.inc = () => ++count;
exports.get = () => count;
exports.name = "ctr";
`,
		"lib/index.js": `exports.v = "from index";
`,
		"main.js": `const add = require("./add");
const { inc, get, name: nm } = require("./ctr");
const libm = require("./lib");
const again = require("./ctr.js");
console.log(add(2, 3), add.twice(4), typeof add);
inc(); again.inc(); console.log(get(), nm, libm.v);
console.log(typeof module, typeof exports, module.exports === exports, JSON.stringify(module.exports));
exports.z = 1; console.log(module.exports.z);
`,
	})
}

func TestE2ENodeGlobalIsGlobalThis(t *testing.T) {
	// Node's global is the global object in every module.
	assertSameAsNodeCLIJS(t, `
console.log(typeof global, global === globalThis);
global.gx = 41; console.log(globalThis.gx + 1);
function f() { return global.gx; } console.log(f());
`)
	assertSameAsNode(t, `
declare const global: any;
console.log(typeof global, global === globalThis);
`)
}
