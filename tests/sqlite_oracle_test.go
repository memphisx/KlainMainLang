package tests

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// node:sqlite differential oracle (ADR-00540). Each case pairs a typed program
// for this compiler with the equivalent untyped program for a real node binary;
// both must print byte-identical stdout. This is what makes "identical to Node"
// machine-checked rather than asserted from memory. Skips cleanly when node (or
// its node:sqlite module) is unavailable, so CI without node stays green.

func nodeSupportsSqlite(t *testing.T) string {
	t.Helper()
	nodeBin, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not on PATH — skipping node:sqlite oracle")
	}
	out, err := exec.Command(nodeBin, "-e",
		"try{require('node:sqlite');process.stdout.write('ok')}catch(e){process.stdout.write('no')}").CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) != "ok" {
		t.Skip("this node build lacks node:sqlite — skipping oracle")
	}
	return nodeBin
}

// assertMatchesNode compiles+runs tsSrc through this compiler and runs mjsSrc
// through node, requiring identical stdout.
func assertMatchesNode(t *testing.T, tsSrc, mjsSrc string) {
	t.Helper()
	nodeBin := nodeSupportsSqlite(t)

	ours := strings.TrimRight(compileAndRunImports(t, tsSrc), "\n")

	dir := t.TempDir()
	mjs := filepath.Join(dir, "prog.mjs")
	if err := os.WriteFile(mjs, []byte(mjsSrc), 0644); err != nil {
		t.Fatalf("write mjs: %v", err)
	}
	raw, err := exec.Command(nodeBin, mjs).CombinedOutput()
	if err != nil {
		t.Fatalf("node run failed: %v\n%s", err, raw)
	}
	theirs := strings.TrimRight(string(raw), "\n")

	if ours != theirs {
		t.Fatalf("output differs from node:\n--- ours ---\n%s\n--- node ---\n%s", ours, theirs)
	}
}

func TestOracleSqliteValuesAndNulls(t *testing.T) {
	ts := `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
db.exec('CREATE TABLE t (id INTEGER PRIMARY KEY, num REAL, txt TEXT, blb BLOB)');
db.exec("INSERT INTO t (num, txt, blb) VALUES (1.5, 'hi', x'01ff')");
db.exec('INSERT INTO t (num, txt, blb) VALUES (NULL, NULL, NULL)');
const rows = db.prepare('SELECT id, num, txt, blb FROM t ORDER BY id').all<{ id: number; num: number | null; txt: string | null; blb: Uint8Array | null }>();
for (const r of rows) {
  if (r.num === null) console.log('num=null'); else console.log('num=' + r.num);
  if (r.txt === null) console.log('txt=null'); else console.log('txt=' + r.txt);
  if (r.blb === null) console.log('blb=null'); else console.log('blb=' + r.blb.length);
}
`
	mjs := `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
db.exec('CREATE TABLE t (id INTEGER PRIMARY KEY, num REAL, txt TEXT, blb BLOB)');
db.exec("INSERT INTO t (num, txt, blb) VALUES (1.5, 'hi', x'01ff')");
db.exec('INSERT INTO t (num, txt, blb) VALUES (NULL, NULL, NULL)');
const rows = db.prepare('SELECT id, num, txt, blb FROM t ORDER BY id').all();
for (const r of rows) {
  if (r.num === null) console.log('num=null'); else console.log('num=' + r.num);
  if (r.txt === null) console.log('txt=null'); else console.log('txt=' + r.txt);
  if (r.blb === null) console.log('blb=null'); else console.log('blb=' + r.blb.length);
}
`
	assertMatchesNode(t, ts, mjs)
}

func TestOracleSqliteRunAndParams(t *testing.T) {
	ts := `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
db.exec('CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)');
const ins = db.prepare('INSERT INTO t (name, age) VALUES (:name, :age)');
const a = ins.run({ name: 'Ada', age: 36 });
console.log('changes=' + a.changes + ' rowid=' + a.lastInsertRowid);
ins.run({ name: 'Bob', age: 42 });
const rows = db.prepare('SELECT name, age FROM t WHERE age > ? ORDER BY id').all<{ name: string; age: number }>(40);
for (const r of rows) console.log(r.name + ':' + r.age);
`
	mjs := `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
db.exec('CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)');
const ins = db.prepare('INSERT INTO t (name, age) VALUES (:name, :age)');
const a = ins.run({ name: 'Ada', age: 36 });
console.log('changes=' + a.changes + ' rowid=' + a.lastInsertRowid);
ins.run({ name: 'Bob', age: 42 });
const rows = db.prepare('SELECT name, age FROM t WHERE age > ? ORDER BY id').all(40);
for (const r of rows) console.log(r.name + ':' + r.age);
`
	assertMatchesNode(t, ts, mjs)
}

func TestOracleSqliteErrorCode(t *testing.T) {
	ts := `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
try {
  db.prepare('SELECT * FROM nope').all<{ x: number }>();
} catch (e) {
  const err = e as { code: string; errcode: number; errstr: string; message: string };
  console.log(err.code);
  console.log(err.errcode);
  console.log(err.errstr);
  console.log(err.message);
}
`
	mjs := `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
try {
  db.prepare('SELECT * FROM nope').all();
} catch (e) {
  console.log(e.code);
  console.log(e.errcode);
  console.log(e.errstr);
  console.log(e.message);
}
`
	assertMatchesNode(t, ts, mjs)
}

func TestOracleSqliteUserFunction(t *testing.T) {
	ts := `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
db.function('triple', (x: number) => x * 3);
db.exec('CREATE TABLE t (n INTEGER)');
db.exec('INSERT INTO t VALUES (1),(2),(3)');
const r = db.prepare('SELECT SUM(triple(n)) AS v FROM t').get<{ v: number }>();
if (r !== null) console.log(r.v);
`
	mjs := `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
db.function('triple', (x) => x * 3);
db.exec('CREATE TABLE t (n INTEGER)');
db.exec('INSERT INTO t VALUES (1),(2),(3)');
const r = db.prepare('SELECT SUM(triple(n)) AS v FROM t').get();
console.log(r.v);
`
	assertMatchesNode(t, ts, mjs)
}
