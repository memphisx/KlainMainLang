package tests

import (
	"os/exec"
	"strings"
	"testing"
)

// zlib is Node's lib/zlib.js in TypeScript over a native handle per stream
// (klainzlib.c): the one-shot forms, the streams, and their options.
// Compressed bytes are not compared (Node bundles its own zlib); round trips
// and every observable result are.
func TestE2EZlibPortOneShot(t *testing.T) {
	assertSameAsNodeImports(t, `
import * as zlib from 'zlib';
const gz = zlib.gzipSync('hello hello hello');
console.log(gz.length, zlib.gunzipSync(gz).toString());
console.log(zlib.inflateSync(zlib.deflateSync(Buffer.from('abc'), { level: 9 })).toString());
console.log(zlib.inflateRawSync(zlib.deflateRawSync('raw')).toString(), zlib.unzipSync(gz).toString());
console.log(zlib.crc32('hello'), zlib.crc32('world', 123));
try { zlib.gunzipSync(Buffer.from('xx')); } catch (e: any) { console.log(e.message, e.errno, e.code); }
zlib.gzip('async data', (err, buf) => {
    console.log('gzip cb', err, buf.length);
    zlib.gunzip(buf, (err2, out) => console.log('gunzip cb', err2, out.toString()));
});
zlib.inflate(Buffer.from('bad'), (err, out) => console.log('bad', err && err.message, (err as any).code));
console.log('sync first');
const big = 'x'.repeat(100000);
console.log(zlib.inflateSync(zlib.deflateSync(big)).length);
try { zlib.inflateSync(zlib.deflateSync('abc'), { maxOutputLength: 1 }); } catch (e: any) { console.log(e.name, e.code, e.message); }
try { zlib.deflateSync('a', { level: 42 }); } catch (e: any) { console.log(e.name, e.code, e.message); }
console.log(zlib.codes.Z_DATA_ERROR, zlib.codes['-3'], zlib.constants.Z_BEST_COMPRESSION);
`)
}

func TestE2EZlibPortStreamEvents(t *testing.T) {
	assertSameAsNodeImports(t, `
import * as zlib from 'zlib';
const g = zlib.createGzip();
g.on('data', (c: Buffer) => console.log('data', c.length));
g.on('end', () => console.log('end'));
g.on('finish', () => console.log('finish'));
g.on('error', (e: Error) => console.log('error', e.message));
g.on('close', () => console.log('close'));
g.write('hello');
g.end();
`)
}

func TestE2EZlibPortOptions(t *testing.T) {
	assertSameAsNodeImports(t, `
import zlib from 'zlib';
import { pipeline, Readable, Writable } from 'stream';

const big = Buffer.alloc(200000);
for (let i = 0; i < big.length; i++) big[i] = (i * 7) % 251;
const comp = zlib.deflateSync(big, { level: 1, chunkSize: 1024 });
console.log('comp', zlib.inflateSync(comp, { chunkSize: 64 }).equals(big));

// info option
const r: any = zlib.gzipSync('abc', { info: true } as any);
console.log(Object.keys(r), r.buffer.length, r.engine.bytesWritten);

// dictionary
const dict = Buffer.from('hello world dictionary');
const d = zlib.deflateSync('hello world hello', { dictionary: dict });
console.log(zlib.inflateSync(d, { dictionary: dict }).toString());
try { zlib.inflateSync(d); } catch (e: any) { console.log(e.message, e.code, e.errno); }

// windowBits / raw
console.log(zlib.inflateRawSync(zlib.deflateRawSync('xyz', { windowBits: 8 })).toString());
try { zlib.gzipSync('a', { windowBits: 8 }); } catch (e: any) { console.log(e.code, e.message); }

// garbage after end
const gz = zlib.gzipSync('one');
console.log(zlib.gunzipSync(Buffer.concat([gz, Buffer.from([0, 0])])).toString());

// stream with backpressure through pipeline
const chunks: Buffer[] = [];
pipeline(
    Readable.from([big.subarray(0, 100000), big.subarray(100000)]),
    zlib.createGzip(),
    zlib.createGunzip(),
    new Writable({
        write(c: Buffer, _e: string, cb: () => void) { chunks.push(c); cb(); },
    }),
    (err: any) => {
        console.log('pipeline', err, Buffer.concat(chunks).equals(big), chunks.length > 1);
    },
);

// flush()
const def = zlib.createDeflate();
const parts: Buffer[] = [];
def.on('data', (c: Buffer) => parts.push(c));
def.write('partial');
def.flush(() => {
    console.log('flushed', zlib.inflateSync(Buffer.concat(parts), { finishFlush: zlib.constants.Z_SYNC_FLUSH }).toString());
    def.params(9, 0, () => {
        console.log('params ok');
        def.end();
    });
});
def.on('end', () => console.log('def end'));

console.log(new zlib.Unzip() instanceof zlib.Unzip);
console.log(zlib.constants.Z_BEST_SPEED, zlib.constants.Z_MAX_CHUNK);
console.log(zlib.crc32(new Uint16Array([1, 2])), zlib.crc32(new DataView(new ArrayBuffer(4))));
try { zlib.crc32(5 as any); } catch (e: any) { console.log(e.code, e.message); }
try { zlib.deflate('x', 5 as any); } catch (e: any) { console.log(e.code, e.message); }
`)
}

// Handles past the first thousand, and a stream destroyed while its write
// runs on the pool (the handle is freed when the write ends).
func TestE2EZlibPortHandles(t *testing.T) {
	assertSameAsNodeImports(t, `
import * as zlib from 'zlib';
let ok = 0;
for (let i = 0; i < 3000; i++) {
    if (zlib.inflateSync(zlib.deflateSync('n' + i)).toString() === 'n' + i) ok++;
}
console.log('sync', ok);
const streams: any[] = [];
for (let i = 0; i < 1500; i++) streams.push(zlib.createGzip());
console.log('made', streams.length);
const g = zlib.createGzip();
g.on('close', () => console.log('closed mid-write'));
g.write(Buffer.alloc(1 << 20, 1));
g.destroy();
for (const s of streams) s.close();
let done = 0;
for (let i = 0; i < 50; i++) {
    zlib.gzip('x'.repeat(i * 100), (err, b) => {
        zlib.gunzip(b, (e2, out) => { if (out.length === i * 100) done++; if (done === 50) console.log('async all ok'); });
    });
}
`)
}

func TestE2EZlibPortHandlesASan(t *testing.T) {
	bin := buildBinaryASan(t, `
import * as zlib from 'zlib'
const streams: any[] = []
for (let i = 0; i < 40; i++) streams.push(zlib.createGzip())
const g = zlib.createGzip()
g.write(Buffer.alloc(1 << 20, 1))
g.destroy()
for (const s of streams) s.close()
zlib.gzip('x'.repeat(5000), (err, b) => {
  zlib.gunzip(b, (e2, out) => console.log(out.length))
})
console.log(zlib.inflateSync(zlib.deflateSync('abc')).toString())
`)
	out, err := exec.Command(bin).CombinedOutput()
	got := string(out)
	if err != nil {
		t.Fatalf("run: %v\n%s", err, got)
	}
	if strings.Contains(got, "AddressSanitizer") || strings.Contains(got, "runtime error") {
		t.Fatalf("sanitizer error:\n%s", got)
	}
	if got != "abc\n5000\n" {
		t.Fatalf("got %q", got)
	}
}
