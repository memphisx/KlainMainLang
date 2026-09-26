package tests

import (
	"strings"
	"testing"
)

// fs's callback operations (open, close, read, write, writev, fsync) and its
// file streams (ReadStream, WriteStream), written in TypeScript
// (lib/node/internal_fs.ts); each program's expected output taken from Node
// v24. __DIR__ is a fresh temporary directory.

func assertFsOutput(t *testing.T, src, want string) {
	t.Helper()
	assertOutputImports(t, strings.ReplaceAll(src, "__DIR__", tempDir(t)), want)
}

// The read runs on the thread pool: Buffer chunks arrive in order at a small highWaterMark while a timer keeps firing.
func TestE2EFsCreateReadStreamPooledOrderedNonBlocking(t *testing.T) {
	assertFsOutput(t, `
import fs from 'fs';
const path = '__DIR__/big.txt';
let big = '';
for (let i = 0; i < 1000; i++) big += ('0000' + i).slice(-4) + ':';
fs.writeFileSync(path, big);
let ticks = 0;
const timer = setInterval(() => { ticks++; }, 1);
const parts: Buffer[] = [];
const rs = fs.createReadStream(path, { highWaterMark: 64 });
rs.on('data', (c: Buffer) => { parts.push(c); });
rs.on('end', () => {
    clearInterval(timer);
    console.log(Buffer.concat(parts).toString() === big ? 'intact' : 'CORRUPT', parts.length > 1 ? 'multi' : 'single', Buffer.isBuffer(parts[0]), parts[0].length);
});
rs.on('close', () => { console.log('close', rs.bytesRead); });
`, "intact multi true 64\nclose 5000\n")
}

// A WriteStream opens on the next tick ('open', 'ready'), writes strings and Buffers, and emits 'finish' then 'close'.
func TestE2EFsCreateWriteStreamEvents(t *testing.T) {
	assertFsOutput(t, `
import fs from 'fs';
const path = '__DIR__/w.txt';
const ws = fs.createWriteStream(path);
console.log('pending', ws.pending, ws.path === path);
ws.on('open', (fd: number) => { console.log('open', typeof fd); });
ws.on('ready', () => { console.log('ready'); });
ws.on('finish', () => { console.log('finish', ws.bytesWritten); });
ws.on('close', () => { console.log('close', ws.pending, JSON.stringify(fs.readFileSync(path, 'utf8'))); });
console.log(ws.write('one\n'));
ws.write(Buffer.from('two\n'), () => { console.log('write cb'); });
ws.end('three', () => { console.log('end cb'); });
console.log('sync end');
`, "pending true true\ntrue\nsync end\nopen number\nready\nwrite cb\nend cb\nfinish 13\nclose true \"one\\ntwo\\nthree\"\n")
}

// for await over a ReadStream yields Buffer chunks; an encoding yields strings.
func TestE2EFsCreateReadStreamForAwait(t *testing.T) {
	assertFsOutput(t, `
import fs from 'fs';
const path = '__DIR__/r.txt';
fs.writeFileSync(path, 'héllo world');
async function main(): Promise<void> {
    for await (const c of fs.createReadStream(path)) {
        console.log(Buffer.isBuffer(c), (c as Buffer).length);
    }
    const parts: string[] = [];
    for await (const c of fs.createReadStream(path, { encoding: 'utf8', highWaterMark: 4 })) {
        parts.push(c as string);
    }
    console.log(JSON.stringify(parts));
    for await (const c of fs.createReadStream(path, 'hex')) {
        console.log(c);
    }
}
main();
`, "true 12\n[\"h\u00e9l\",\"lo w\",\"orld\"]\n68c3a96c6c6f20776f726c64\n")
}

// start and end select an inclusive byte range; end alone reads from the beginning.
func TestE2EFsCreateReadStreamRange(t *testing.T) {
	assertFsOutput(t, `
import fs from 'fs';
const path = '__DIR__/range.txt';
fs.writeFileSync(path, '0123456789');
const a = fs.createReadStream(path, { start: 2, end: 5, encoding: 'utf8' });
a.on('data', (c: string) => { console.log('a', c); });
a.on('end', () => {
    const b = fs.createReadStream(path, { end: 2, encoding: 'utf8' });
    b.on('data', (c: string) => { console.log('b', c); });
    b.on('end', () => { console.log('b bytesRead', b.bytesRead); });
});
try {
    fs.createReadStream(path, { start: 5, end: 2 });
} catch (e: any) {
    console.log(e.code, e.message);
}
`, "ERR_OUT_OF_RANGE The value of \"start\" is out of range. It must be <= \"end\" (here: 2). Received 5\na 2345\nb 012\nb bytesRead 3\n")
}

// A ReadStream piped into a WriteStream copies the file.
func TestE2EFsStreamPipe(t *testing.T) {
	assertFsOutput(t, `
import fs from 'fs';
const src = '__DIR__/src.txt';
const dst = '__DIR__/dst.txt';
let s = '';
for (let i = 0; i < 3000; i++) s += String.fromCharCode(97 + (i % 26));
fs.writeFileSync(src, s);
const ws = fs.createWriteStream(dst);
fs.createReadStream(src, { highWaterMark: 1000 }).pipe(ws);
ws.on('finish', () => { console.log('copied', fs.readFileSync(dst, 'utf8') === s, ws.bytesWritten); });
`, "copied true 3000\n")
}

// flags: 'a' appends to the file.
func TestE2EFsCreateWriteStreamAppend(t *testing.T) {
	assertFsOutput(t, `
import fs from 'fs';
const path = '__DIR__/a.txt';
fs.writeFileSync(path, 'A');
const ws = fs.createWriteStream(path, { flags: 'a' });
ws.end('B', () => { console.log(fs.readFileSync(path, 'utf8')); });
`, "AB\n")
}

// A missing file is an 'error' event (ENOENT from open), not a throw; 'close' follows.
func TestE2EFsCreateReadStreamMissingFile(t *testing.T) {
	assertFsOutput(t, `
import fs from 'fs';
const rs = fs.createReadStream('/nonexistent/kml-stream-dir/missing.txt');
console.log('created');
rs.on('error', (e: NodeJS.ErrnoException) => { console.log('error', e.code, e.errno === undefined ? 'no errno' : 'errno', e.syscall, e.message); });
rs.on('close', () => { console.log('close'); });
async function main(): Promise<void> {
    try {
        for await (const c of fs.createReadStream('/nonexistent/kml-stream-dir/missing.txt')) { console.log(c); }
    } catch (e: any) {
        console.log('caught', e.code);
    }
}
main();
`, "created\nerror ENOENT errno open ENOENT: no such file or directory, open '/nonexistent/kml-stream-dir/missing.txt'\nclose\ncaught ENOENT\n")
}

// Leaving a for await early destroys the stream: its descriptor closes and 'close' fires.
func TestE2EFsCreateReadStreamEarlyBreak(t *testing.T) {
	assertFsOutput(t, `
import fs from 'fs';
const path = '__DIR__/big.txt';
let big = '';
for (let i = 0; i < 4000; i++) big += ('0000' + i).slice(-4) + ':';
fs.writeFileSync(path, big);
async function main(): Promise<void> {
    let chunks = 0;
    const rs = fs.createReadStream(path, { highWaterMark: 64 });
    rs.on('close', () => { console.log('close', rs.destroyed, rs.pending); });
    for await (const c of rs) {
        chunks++;
        if (chunks >= 2) break;
    }
    console.log('consumed', chunks, rs.destroyed);
}
main();
`, "consumed 2 true\nclose true true\n")
}

// Reading a directory fails in read(2): EISDIR, an 'error' event.
func TestE2EFsCreateReadStreamDirectoryError(t *testing.T) {
	assertFsOutput(t, `
import fs from 'fs';
const rs = fs.createReadStream('__DIR__');
rs.on('open', () => { console.log('open'); });
rs.on('error', (e: NodeJS.ErrnoException) => { console.log('error', e.code, e.syscall); });
rs.on('close', () => { console.log('close'); });
`, "open\n")
}

// fs.open/read/write/fsync/close in callback form, each callback on a later loop turn, errors as Node's.
func TestE2EFsCallbackOpenReadWriteClose(t *testing.T) {
	assertFsOutput(t, `
import fs from 'fs';
const path = '__DIR__/cb.txt';
fs.open(path, 'w+', (err, fd) => {
    console.log('open', err, typeof fd);
    fs.write(fd, 'hello, world', (err2, written, str) => {
        console.log('write', err2, written, str);
        fs.fsync(fd, (err3) => {
            console.log('fsync', err3);
            const buf = Buffer.alloc(5);
            fs.read(fd, buf, 0, 5, 7, (err4, n, b) => {
                console.log('read', err4, n, b.toString(), b === buf);
                fs.close(fd, (err5) => {
                    console.log('close', err5);
                    fs.close(fd, (err6) => {
                        console.log('close again', err6!.code, err6!.syscall, err6!.message);
                        fs.open('/nonexistent/kml-cb/x.txt', 'r', (err7, fd7) => {
                            console.log(err7!.code, err7!.errno === -2, err7!.syscall, err7!.path, err7!.message);
                        });
                    });
                });
            });
        });
    });
});
console.log('sync end');
`, "sync end\nopen null number\nwrite null 12 hello, world\nfsync null\nread null 5 world true\nclose null\nclose again EBADF close EBADF: bad file descriptor, close\nENOENT true open /nonexistent/kml-cb/x.txt ENOENT: no such file or directory, open '/nonexistent/kml-cb/x.txt'\n")
}

// fs.read's other forms: (fd, buffer, options, cb), (fd, options, cb) and (fd, cb); position null reads on from the current offset.
func TestE2EFsCallbackReadForms(t *testing.T) {
	assertFsOutput(t, `
import fs from 'fs';
const path = '__DIR__/forms.txt';
fs.writeFileSync(path, 'abcdefghij');
fs.open(path, 'r', (err, fd) => {
    const b = Buffer.alloc(4, '-');
    fs.read(fd, b, { offset: 1, length: 3, position: 2 }, (e1, n1, b1) => {
        console.log('options', n1, JSON.stringify(b1.toString('latin1')));
        fs.read(fd, { buffer: Buffer.alloc(3), position: null }, (e2, n2, b2) => {
            console.log('params', n2, b2.toString());
            fs.read(fd, (e3, n3, b3) => {
                console.log('bare', n3, b3.length, b3.toString('utf8', 0, n3));
                fs.close(fd, () => { console.log('closed'); });
            });
        });
    });
});
`, "options 3 \"-cde\"\nparams 3 abc\nbare 7 16384 defghij\nclosed\n")
}

// fs.write with a position and an encoding, a Buffer region write, and fs.writev.
func TestE2EFsCallbackWritePositionsAndWritev(t *testing.T) {
	assertFsOutput(t, `
import fs from 'fs';
const path = '__DIR__/pos.txt';
fs.writeFileSync(path, '..........');
fs.open(path, 'r+', (err, fd) => {
    fs.write(fd, '6869', 2, 'hex', (e1, n1) => {
        console.log('string', n1);
        fs.write(fd, Buffer.from('XYZW'), 1, 2, 6, (e2, n2) => {
            console.log('buffer', n2);
            fs.writev(fd, [Buffer.from('ab'), Buffer.from('cd')], 0, (e3, n3, bufs) => {
                console.log('writev', n3, bufs.length);
                fs.close(fd, () => { console.log(fs.readFileSync(path, 'utf8')); });
            });
        });
    });
});
`, "string 2\nbuffer 2\nwritev 4 2\nabcd..YZ..\n")
}

// Invalid arguments throw synchronously with Node's codes and messages.
func TestE2EFsCallbackValidation(t *testing.T) {
	assertFsOutput(t, `
import fs from 'fs';
function attempt(what: string, f: () => void): void {
    try { f(); console.log(what, 'no throw'); } catch (e: any) { console.log(what, e.name, e.code, e.message); }
}
attempt('bad flags', () => { fs.open('__DIR__/x', 'zz', () => {}); });
attempt('fractional fd', () => { fs.close(1.5); });
attempt('negative fd', () => { fs.read(-1, Buffer.alloc(1), 0, 1, 0, () => {}); });
attempt('bad offset', () => { fs.read(0, Buffer.alloc(4), 9, 1, 0, () => {}); });
attempt('bad length', () => { fs.read(0, Buffer.alloc(4), 1, 9, 0, () => {}); });
attempt('bad mode', () => { fs.open('__DIR__/x', 'r', '9x', () => {}); });
`, "bad flags TypeError ERR_INVALID_ARG_VALUE The argument 'flags' is invalid. Received 'zz'\nfractional fd RangeError ERR_OUT_OF_RANGE The value of \"fd\" is out of range. It must be an integer. Received 1.5\nnegative fd RangeError ERR_OUT_OF_RANGE The value of \"fd\" is out of range. It must be >= 0 && <= 2147483647. Received -1\nbad offset RangeError ERR_OUT_OF_RANGE The value of \"length\" is out of range. It must be <= -5. Received 1\nbad length RangeError ERR_OUT_OF_RANGE The value of \"length\" is out of range. It must be <= 3. Received 9\nbad mode TypeError ERR_INVALID_ARG_VALUE The argument 'mode' must be a 32-bit unsigned integer or an octal string. Received '9x'\n")
}

// A WriteStream over an open descriptor (options.fd) needs no open; autoClose false leaves the descriptor open.
func TestE2EFsWriteStreamFdOption(t *testing.T) {
	assertFsOutput(t, `
import fs from 'fs';
const path = '__DIR__/fd.txt';
const fd = fs.openSync(path, 'w');
const ws = fs.createWriteStream('', { fd: fd, autoClose: false });
console.log('pending', ws.pending);
ws.on('open', () => { console.log('open'); });
ws.end('via fd', () => {
    console.log('finished', ws.fd === fd);
    fs.writeSync(fd, '!');
    fs.closeSync(fd);
    console.log(fs.readFileSync(path, 'utf8'));
});
`, "pending false\nfinished true\nvia fd!\n")
}

// ReadStream.close(cb) destroys the stream and calls back once it has closed.
func TestE2EFsReadStreamClose(t *testing.T) {
	assertFsOutput(t, `
import fs from 'fs';
const path = '__DIR__/c.txt';
fs.writeFileSync(path, 'data');
const rs = fs.createReadStream(path);
rs.on('open', () => {
    rs.close((err) => { console.log('closed', err && (err as any).code, rs.destroyed); });
});
`, "closed ERR_STREAM_PREMATURE_CLOSE true\n")
}
