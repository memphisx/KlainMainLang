package tests

import (
	"testing"
)

// node:sqlite — synchronous DatabaseSync/StatementSync (ADR-00540, TDD-00151).
// These assert fixed expected outputs; the byte-for-byte equivalence with real
// Node is machine-checked separately by the differential oracle in
// sqlite_oracle_test.go (which runs the same logic through a real node binary).

func TestE2ESqliteInsertSelectRun(t *testing.T) {
	assertOutputImports(t, `
import { DatabaseSync } from 'node:sqlite';
interface Row { id: number; name: string; }
const db = new DatabaseSync(':memory:');
db.exec('CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)');
const ins = db.prepare('INSERT INTO users (name) VALUES (?)');
const r = ins.run('Alice');
console.log(r.changes, r.lastInsertRowid);
ins.run('Bob');
const rows = db.prepare('SELECT id, name FROM users ORDER BY id').all();
for (const row of rows) console.log(row.id, row.name);
db.close();
`, "1 1\n1 Alice\n2 Bob")
}

func TestE2ESqliteGetInlineRowType(t *testing.T) {
	assertOutputImports(t, `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
db.exec('CREATE TABLE t (id INTEGER, name TEXT)');
db.exec("INSERT INTO t VALUES (2, 'Bob')");
const one = db.prepare('SELECT id, name FROM t WHERE id = ?').get(2);
console.log(one === null ? 'null' : one.name);
const none = db.prepare('SELECT id, name FROM t WHERE id = ?').get(99);
console.log(none === undefined ? 'undefined' : 'row');
`, "Bob\nundefined")
}

func TestE2ESqliteColumnTypesAndNull(t *testing.T) {
	assertOutputImports(t, `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
db.exec('CREATE TABLE m (id INTEGER PRIMARY KEY, score REAL, note TEXT)');
const ins = db.prepare('INSERT INTO m (score, note) VALUES (?, ?)');
ins.run(3.5, 'hi');
ins.run(9.25, null);
const rows = db.prepare('SELECT id, score, note FROM m ORDER BY id').all();
for (const r of rows) console.log(r.id, r.score, r.note === null ? 'NULL' : r.note);
`, "1 3.5 hi\n2 9.25 NULL")
}

func TestE2ESqliteIsOpen(t *testing.T) {
	assertOutputImports(t, `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
console.log(db.isOpen);
db.close();
console.log(db.isOpen);
`, "true\nfalse")
}

func TestE2ESqliteErrorIsCatchable(t *testing.T) {
	assertOutputImports(t, `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
try {
  db.prepare('SELECT * FROM nonexistent').all();
} catch (e) {
  console.log('caught:', (e as Error).message);
}
`, "caught: no such table: nonexistent")
}

func TestE2ESqliteReadOnlyOption(t *testing.T) {
	// A read-only connection to a fresh in-memory db has no table, so a write
	// fails — proving the readOnly flag reached sqlite3_open_v2.
	assertOutputImports(t, `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:', { readOnly: true });
try {
  db.exec('CREATE TABLE t (id INTEGER)');
  console.log('no error');
} catch (e) {
  console.log('caught a write rejection');
}
`, "caught a write rejection")
}

func TestE2ESqliteNamedParameters(t *testing.T) {
	assertOutputImports(t, `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
db.exec('CREATE TABLE u (id INTEGER PRIMARY KEY, name TEXT, age INTEGER)');
const ins = db.prepare('INSERT INTO u (name, age) VALUES (:name, :age)');
ins.run({ name: 'Ada', age: 36 });
ins.run({ name: 'Bob', age: 42 });
const rows = db.prepare('SELECT name, age FROM u WHERE age > :min ORDER BY id').all({ min: 40 });
for (const r of rows) console.log(r.name, r.age);
const one = db.prepare('SELECT name FROM u WHERE id = $id').get({ id: 1 });
console.log(one === null ? 'null' : one.name);
`, "Bob 42\nAda")
}

func TestE2ESqliteUnknownNamedParameterThrows(t *testing.T) {
	assertOutputImports(t, `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
db.exec('CREATE TABLE u (id INTEGER, name TEXT)');
try {
  db.prepare('INSERT INTO u VALUES (:id, :name)').run({ id: 1, nope: 'x' });
} catch (e) {
  console.log('caught:', (e as Error).message);
}
`, "caught: Unknown named parameter 'nope'")
}

func TestE2ESqliteBlobAndBigInt(t *testing.T) {
	assertOutputImports(t, `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
db.exec('CREATE TABLE b (id INTEGER PRIMARY KEY, big INTEGER, data BLOB)');
db.prepare('INSERT INTO b (big, data) VALUES (?, ?)').run(9007199254740993n, new Uint8Array([1, 2, 3, 255]));
const sel = db.prepare('SELECT big, data FROM b');
sel.setReadBigInts(true);
const row = sel.get();
if (row !== undefined) {
  console.log(row.big.toString());
  console.log(row.data.length, row.data[0], row.data[3]);
}
`, "9007199254740993\n4 1 255")
}

func TestE2ESqliteExpandedAndTransactionState(t *testing.T) {
	assertOutputImports(t, `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
db.exec('CREATE TABLE t (id INTEGER)');
const s = db.prepare('SELECT * FROM t WHERE id = ?');
s.get(1);
console.log(s.expandedSQL);
console.log(db.isTransaction);
db.exec('BEGIN');
console.log(db.isTransaction);
db.exec('COMMIT');
`, "SELECT * FROM t WHERE id = 1.0\nfalse\ntrue")
}

func TestE2ESqliteOpenFalseThenOpen(t *testing.T) {
	assertOutputImports(t, `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:', { open: false });
console.log(db.isOpen);
db.open();
console.log(db.isOpen);
db.exec('CREATE TABLE t (x INTEGER)');
console.log('ok');
`, "false\ntrue\nok")
}

func TestE2ESqliteColumnsAndIterate(t *testing.T) {
	assertOutputImports(t, `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
db.exec('CREATE TABLE t (id INTEGER PRIMARY KEY, name TEXT)');
const ins = db.prepare('INSERT INTO t (name) VALUES (?)');
ins.run('a'); ins.run('b'); ins.run('c');
let sum = 0;
for (const r of db.prepare('SELECT id FROM t').iterate()) sum += r.id;
console.log(sum);
const cols = db.prepare('SELECT id, name AS label FROM t').columns();
for (const c of cols) console.log(c.name, c.type === null ? 'null' : c.type);
`, "6\nid INTEGER\nlabel TEXT")
}

func TestE2ESqliteUserDefinedFunction(t *testing.T) {
	assertOutputImports(t, `
import { DatabaseSync } from 'node:sqlite';
const db = new DatabaseSync(':memory:');
db.function('triple', (x: number) => x * 3);
db.function('shout', (s: string) => s + '!');
const a = db.prepare('SELECT triple(7) AS v').get();
if (a !== null) console.log(a.v);
const b = db.prepare("SELECT shout('hi') AS v").get();
if (b !== null) console.log(b.v);
db.exec('CREATE TABLE t (n INTEGER)');
db.exec('INSERT INTO t VALUES (1),(2),(3)');
const c = db.prepare('SELECT SUM(triple(n)) AS v FROM t').get();
if (c !== null) console.log(c.v);
`, "21\nhi!\n18")
}
