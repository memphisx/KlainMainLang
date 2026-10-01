package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"KlainMainLang/codegen/llvm"
)

// Untyped JavaScript (`-compat=js`): a `function` that reads its own `this`
// takes the receiver its caller supplies — an emitter's listeners, a
// server's `listen` callback — through a typed callback parameter and back,
// and keeps its identity for removeListener. Node, running the same
// CommonJS file, is the oracle.

// assertJSFileSameAsNode compiles src as a `.js` program under -compat=js
// with the CLI and compares its output with `node` running the same file.
func assertJSFileSameAsNode(t *testing.T, src string) {
	t.Helper()
	node := nodeStripTypesBin(t)
	bin := buildCLI(t)
	dir := tempDir(t)
	file := filepath.Join(dir, "main.js")
	if err := os.WriteFile(file, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	want, err := exec.Command(node, file).CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v\n%s", err, want)
	}
	out := filepath.Join(dir, "prog"+llvm.HostExeSuffix())
	if b, err := exec.Command(bin, "-compat=js", "-o", out, file).CombinedOutput(); err != nil {
		t.Fatalf("compile: %v\n%s", err, b)
	}
	got, err := exec.Command(out).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	compareLines(t, strings.TrimRight(string(got), "\n"), strings.TrimRight(string(want), "\n"))
}

func TestE2EJSThisInFunctionExpressionCallbacks(t *testing.T) {
	assertJSFileSameAsNode(t, `'use strict';
const net = require('net');
const { EventEmitter } = require('events');
const server = net.createServer();
server.listen(0, function () {
  console.log(typeof this.address().port, this === server);
  this.close();
});
const e = new EventEmitter();
e.on('x', function (v) { console.log('x', v, this === e); });
e.emit('x', 5);
[1].forEach(function () { console.log(this === undefined); });
`)
}

func TestE2EJSThisInFunctionDeclarationCallbacks(t *testing.T) {
	assertJSFileSameAsNode(t, `'use strict';
const net = require('net');
const { EventEmitter } = require('events');
const server = net.createServer();
server.listen(0, onListen);
function onListen() {
  console.log(typeof this.address().port, this === server);
  this.close();
}
const e = new EventEmitter();
function onX(v) { console.log('x', v, this === e); }
e.on('x', onX);
e.emit('x', 5);
e.removeListener('x', onX);
console.log(e.listenerCount('x'));
function outer() {
  function inner() { return this === e; }
  e.once('y', function () { console.log('y', inner(), this === e); });
  e.emit('y');
}
outer();
`)
}

func TestE2EJSThisCallApplyBind(t *testing.T) {
	assertJSFileSameAsNode(t, `'use strict';
function who(a, b) { return (this && this.name) + ':' + a + ':' + b; }
const o = { name: 'o' };
console.log(who.call(o, 1, 2));
console.log(who.apply(o, [3, 4]));
const args = [9, 10];
console.log(who.apply(o, args));
const bound = who.bind(o, 5);
console.log(bound(6), bound.length);
const f = function () { return this === undefined; };
console.log(f(), f.call(undefined));
const holder = { name: 'h', who: who };
console.log(holder.who(7, 8));
const add = (a, b) => a + b;
console.log(add.call(null, 1, 2), add.bind(null, 3)(4));
`)
}
