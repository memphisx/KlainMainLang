// node:sqlite — a synchronous, dependency-free SQL database built into the
// binary (ADR-00540). DatabaseSync/StatementSync block like fs.readFileSync,
// so no async/await is needed.
import { DatabaseSync } from 'node:sqlite';

interface Task {
  id: number;
  title: string;
  done: number;
}

// ':memory:' is an ephemeral in-process database; pass a path for a real file.
const db = new DatabaseSync(':memory:');

db.exec(`
  CREATE TABLE tasks (
    id    INTEGER PRIMARY KEY,
    title TEXT NOT NULL,
    done  INTEGER NOT NULL DEFAULT 0
  )
`);

// Prepared statements are reusable; parameters bind positionally with '?'.
const insert = db.prepare('INSERT INTO tasks (title, done) VALUES (?, ?)');
const first = insert.run('write the compiler', 1);
console.log(`inserted #${first.lastInsertRowid} (${first.changes} row)`);
insert.run('ship node:sqlite', 0);
insert.run('write the docs', 0);

// .all<T>() returns every row as a typed object; .get<T>() returns one or null.
const open = db.prepare('SELECT id, title, done FROM tasks WHERE done = ? ORDER BY id').all<Task>(0);
console.log(`\n${open.length} open task(s):`);
for (const t of open) {
  console.log(`  [${t.done ? 'x' : ' '}] #${t.id} ${t.title}`);
}

const done = db.prepare('SELECT id, title, done FROM tasks WHERE done = 1').get<Task>();
if (done !== null) {
  console.log(`\nmost recently completed: ${done.title}`);
}

// Parameters can also bind by name — pass a single object.
const byName = db.prepare('SELECT title FROM tasks WHERE id = :id').get<{ title: string }>({ id: 2 });
if (byName !== null) {
  console.log(`task #2 by name: ${byName.title}`);
}

// Register a user-defined SQL function — plain TypeScript, callable from SQL.
db.function('shout', (s: string) => s.toUpperCase() + '!');
const loud = db.prepare('SELECT shout(title) AS t FROM tasks WHERE id = 1').get<{ t: string }>();
if (loud !== null) {
  console.log(`custom function: ${loud.t}`);
}

// BLOBs round-trip as Uint8Array.
db.exec('CREATE TABLE blobs (id INTEGER PRIMARY KEY, bytes BLOB)');
db.prepare('INSERT INTO blobs (bytes) VALUES (?)').run(new Uint8Array([0xDE, 0xAD, 0xBE, 0xEF]));
const blob = db.prepare('SELECT bytes FROM blobs').get<{ bytes: Uint8Array }>();
if (blob !== null) {
  console.log(`blob length: ${blob.bytes.length}, first byte: ${blob.bytes[0]}`);
}

db.close();
