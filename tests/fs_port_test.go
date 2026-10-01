package tests

import (
	"strings"
	"testing"
)

// fs is Node's lib/fs.js in TypeScript over one libuv-shaped request per
// operation (klainfs.c): synchronous, or on the thread pool with its
// callback or promise. Each program runs under Node too and must print the
// same; paths print relative to the directory the program makes.
func fsPortProgram(dir, body string) string {
	return strings.ReplaceAll(`
import * as fs from 'fs';
import * as os from 'os';
import * as path from 'path';
import { promises as fsp } from 'fs';
import * as fsp2 from 'fs/promises';
const base = path.join(os.tmpdir(), 'DIR');
fs.rmSync(base, { recursive: true, force: true });
fs.mkdirSync(base);
const rel = (p: any) => typeof p === 'string' ? p.split(base).join('<base>') : p;
const t = (name: string, f: () => any) => {
    try { console.log(name, 'ok', f()); }
    catch (e: any) { console.log(name, e.name, e.code, rel(e.message), e.syscall, rel(e.path), rel(e.dest), e.errno); }
};
`, "DIR", dir) + body
}

func TestE2EFsPortSyncErrors(t *testing.T) {
	assertSameAsNodeImports(t, fsPortProgram("kml_fsport_errors", `
fs.writeFileSync(base + '/f', 'abc');
t('rm-dir', () => fs.rmSync(base));
t('rm-missing', () => fs.rmSync(base + '/nope'));
t('rm-force', () => fs.rmSync(base + '/nope', { force: true }));
t('rename', () => fs.renameSync(base + '/nope', base + '/x'));
t('copy-excl', () => { fs.copyFileSync(base + '/f', base + '/g'); fs.copyFileSync(base + '/f', base + '/g', fs.constants.COPYFILE_EXCL); });
t('copy-mode', () => fs.copyFileSync(base + '/f', base + '/g', 8));
t('readdir-file', () => fs.readdirSync(base + '/f'));
t('read-dir', () => fs.readFileSync(base));
t('mk-file-parent', () => fs.mkdirSync(base + '/f/x', { recursive: true }));
t('mk-exists', () => fs.mkdirSync(base + '/f'));
t('write-num', () => fs.writeFileSync(base + '/w', 5 as any));
t('link', () => fs.linkSync(base + '/nope', base + '/l'));
t('stat-noent', () => fs.statSync(base + '/nope', { throwIfNoEntry: false }));
t('close-bad', () => fs.closeSync(999));
t('fstat-bad', () => fs.fstatSync(999));
t('access', () => fs.accessSync(base + '/nope'));
t('access-mode', () => fs.accessSync(base + '/f', 8));
t('chmod-bad', () => fs.chmodSync(base + '/f', '9'));
t('mkdtemp-bad', () => fs.mkdtempSync(base + '/nope/x'));
t('utimes', () => fs.utimesSync(base + '/nope', 1, 2));
t('truncate', () => fs.truncateSync(base + '/nope'));
t('readlink', () => fs.readlinkSync(base + '/f'));
t('realpath', () => fs.realpathSync(base + '/nope'));
t('open-flags', () => fs.openSync(base + '/f', 'bogus'));
t('read-cb', () => (fs.readFile as any)(base + '/f'));
t('symlink-type', () => fs.symlinkSync('a', base + '/b', 'bad' as any));
`))
}

func TestE2EFsPortSyncSurface(t *testing.T) {
	assertSameAsNodeImports(t, fsPortProgram("kml_fsport_surface", `
fs.writeFileSync(base + '/f', 'hello');
t('read', () => fs.readFileSync(base + '/f', 'utf8'));
t('read-buf', () => fs.readFileSync(base + '/f').toString('hex'));
t('read-opt', () => fs.readFileSync(base + '/f', { encoding: 'base64' }));
t('append', () => { fs.appendFileSync(base + '/f', ' world'); return fs.readFileSync(base + '/f', 'latin1'); });
t('exists', () => [fs.existsSync(base + '/f'), fs.existsSync(base + '/nope')]);
t('stat', () => { const s = fs.statSync(base + '/f'); return [s.size, s.isFile(), s.isDirectory(), s.isSymbolicLink(), s.mtime instanceof Date, s.mtime.getTime() === Math.round(s.mtimeMs)]; });
t('keys', () => Object.keys(fs.statSync(base + '/f')));
t('mk-rec', () => rel(fs.mkdirSync(base + '/a/b/c', { recursive: true })));
t('mk-rec-again', () => fs.mkdirSync(base + '/a/b/c', { recursive: true }));
fs.writeFileSync(base + '/a/b/x.txt', 'x');
t('readdir', () => fs.readdirSync(base));
t('readdir-rec', () => fs.readdirSync(base + '/a', { recursive: true }));
t('readdir-types', () => fs.readdirSync(base + '/a/b', { withFileTypes: true }).map((d) => [d.name, rel(d.parentPath), d.isDirectory(), d.isFile()]));
t('readdir-buf', () => fs.readdirSync(base + '/a', 'buffer').map((b) => b.toString()));
t('symlink', () => { fs.symlinkSync('f', base + '/s'); return [fs.readlinkSync(base + '/s'), fs.lstatSync(base + '/s').isSymbolicLink(), fs.statSync(base + '/s').isFile()]; });
t('realpath', () => rel(fs.realpathSync(base + '/s')) === rel(fs.realpathSync.native(base + '/f')));
t('link', () => { fs.linkSync(base + '/f', base + '/h'); return fs.statSync(base + '/f').nlink; });
t('rename', () => { fs.renameSync(base + '/h', base + '/h2'); return fs.existsSync(base + '/h2'); });
t('copy', () => { fs.copyFileSync(base + '/f', base + '/c'); return fs.readFileSync(base + '/c', 'utf8'); });
t('chmod', () => { fs.chmodSync(base + '/c', 0o640); return (fs.statSync(base + '/c').mode & 0o777).toString(8); });
t('truncate', () => { fs.truncateSync(base + '/c', 2); return fs.readFileSync(base + '/c', 'utf8'); });
t('utimes', () => { fs.utimesSync(base + '/c', 1000, new Date(2000500)); const s = fs.statSync(base + '/c'); return [s.atimeMs, s.mtimeMs, s.mtime.toISOString()]; });
t('fd', () => {
    const fd = fs.openSync(base + '/fd', 'w+');
    const n = fs.writeSync(fd, 'héllo');
    const m = fs.writeSync(fd, Buffer.from('!!'), 0, 2, 20);
    const b = Buffer.alloc(8);
    const r = fs.readSync(fd, b, { position: 0 });
    fs.ftruncateSync(fd, 3);
    fs.fsyncSync(fd);
    const size = fs.fstatSync(fd).size;
    fs.closeSync(fd);
    return [n, m, r, b.toString('utf8', 0, 6), size];
});
t('rm-rec', () => { fs.rmSync(base + '/a', { recursive: true }); return fs.existsSync(base + '/a'); });
t('unlink', () => { fs.unlinkSync(base + '/c'); return fs.existsSync(base + '/c'); });
t('consts', () => [fs.constants.F_OK, fs.constants.R_OK, fs.constants.W_OK, fs.constants.X_OK, fs.constants.COPYFILE_EXCL, fs.constants.S_IFDIR]);
t('statfs', () => { const s = fs.statfsSync(base); return [Object.keys(s), s.bsize > 0]; });
`))
}

func TestE2EFsPortCallbacksAndPromises(t *testing.T) {
	assertSameAsNodeImports(t, fsPortProgram("kml_fsport_async", `
fs.writeFileSync(base + '/f', 'data');
// One operation at a time: the pool finishes them in any order.
const steps: Array<() => Promise<void>> = [
    () => new Promise((res) => fs.readFile(base + '/f', 'utf8', (err, d) => { console.log('readFile', err, d); res(); })),
    () => new Promise((res) => fs.stat(base + '/nope', (err) => { console.log('stat', err?.code, rel(err?.message)); res(); })),
    () => new Promise((res) => fs.mkdir(base + '/p/q', { recursive: true }, (err, p) => { console.log('mkdir', err, rel(p)); res(); })),
    () => new Promise((res) => fs.readdir(base, (err, files) => { console.log('readdir', err, files); res(); })),
    () => new Promise((res) => fs.writeFile(base + '/w', 'abc', (err) => { console.log('writeFile', err, fs.readFileSync(base + '/w', 'utf8')); res(); })),
    () => new Promise((res) => fs.rm(base + '/p', { recursive: true }, (err) => { console.log('rm', err, fs.existsSync(base + '/p')); res(); })),
    () => new Promise((res) => fs.access(base + '/nope', (err) => { console.log('access', err?.code); res(); })),
    () => new Promise((res) => fs.exists(base + '/f', (ok) => { console.log('exists', ok); res(); })),
    async () => { const s = await fsp.stat(base + '/f'); console.log('p-stat', s.size, s.isFile()); },
    async () => { try { await fsp.readFile(base + '/nope'); } catch (e: any) { console.log('p-err', e.code, rel(e.message), e instanceof Error); } },
    async () => { console.log('p-readdir', (await fsp2.readdir(base, { withFileTypes: true })).map((d) => d.name)); },
    async () => { await fsp.writeFile(base + '/pw', Buffer.from([1, 2, 3])); console.log('p-write', (await fsp.readFile(base + '/pw')).length); },
    async () => { const h = await fsp.open(base + '/f'); const r = await h.read(Buffer.alloc(4), 0, 4, 0); await h.close(); console.log('p-open', r.bytesRead, r.buffer.toString()); },
];
(async () => { for (const s of steps) await s(); })();
`))
}

func TestE2EFsPortWatch(t *testing.T) {
	assertSameAsNodeImports(t, fsPortProgram("kml_fsport_watch", `
fs.writeFileSync(base + '/f.txt', 'a');
const w = fs.watch(base + '/f.txt', (ev, name) => {
    console.log('event', ev, name);
    w.close();
});
w.on('close', () => console.log('closed'));
setTimeout(() => fs.appendFileSync(base + '/f.txt', 'b'), 100);
t('watch-missing', () => fs.watch(base + '/nope'));
`))
}
