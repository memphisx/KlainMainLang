package tests

import "testing"

// 'beforeExit' is emitted when the loop drains, again after work its
// listeners schedule, and not on process.exit.
func TestE2EProcessBeforeExit(t *testing.T) {
	assertSameAsNode(t, `let n = 0;
process.on('beforeExit', (code) => {
  console.log('beforeExit', code, n);
  if (n++ < 2) setTimeout(() => console.log('timer', n), 1);
});
process.on('exit', (c) => console.log('exit', c));
console.log('main');
`)
	assertSameAsNode(t, "process.on('beforeExit', () => console.log('never'));\nprocess.exit(0);\n")
}

// process's emitter members, title and the fork channel's members are its
// emitter object's; hrtime and memoryUsage are functions with their own
// members (bigint, rss).
func TestE2EProcessHeldMembersAndFunctionValues(t *testing.T) {
	assertSameAsNode(t, `process.on('exit', (c) => console.log('exit', c));
process.once('beforeExit', () => console.log('before'));
console.log(process.listenerCount('exit'), process.eventNames().length > 0, typeof process.emit);
process.title = 'kml'; console.log(process.title);
console.log(process.connected, typeof process.send);
const w0 = process.listenerCount('warning'); const f = () => {}; process.on('warning', f); console.log(process.listenerCount('warning') - w0); process.off('warning', f); console.log(process.listenerCount('warning') - w0);
`)
	assertSameAsNode(t, `const t = process.hrtime(); const d = process.hrtime(t);
console.log(t.length, d.length, typeof process.hrtime.bigint(), typeof process.hrtime, typeof process.hrtime.bigint);
const h = process.hrtime; console.log(Array.isArray(h()), process.hrtime.bigint() > 0n, typeof process.memoryUsage.rss());
try { process.hrtime([1] as any); } catch (e: any) { console.log(e.code); }
const m = process.memoryUsage(); console.log(typeof m.rss, typeof process.memoryUsage.rss(), typeof process.memoryUsage); const f = process.memoryUsage; console.log(typeof f().heapUsed);
`)
}

// Builtin properties a declaration lowers to a module variable or to an
// accessor of the held process object read and write through it.
func TestE2EProcessExitCodeAndPoolSize(t *testing.T) {
	assertSameAsNode(t, `
console.log(Buffer.poolSize); Buffer.poolSize = 1024; Buffer.poolSize += 1; console.log(Buffer.poolSize);
console.log(process.exitCode); process.exitCode = 3; console.log(process.exitCode);
const x = (process.exitCode = 5); console.log(x, process.exitCode);
process.exitCode = undefined; console.log(process.exitCode);
process.on("exit", (c) => console.log("exit", c));
`)
}
