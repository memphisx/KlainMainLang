package tests

import "testing"

// fs/promises open() and its FileHandle, and the file streams' fd (a
// FileHandle), fs and signal options (lib/node/internal_fs.ts); expected
// output taken from Node v24.

// open() resolves a FileHandle: read, stat, writeFile, close.
func TestE2EFsFileHandleOperations(t *testing.T) {
	assertOutputImports(t, `
import * as fs from 'fs';
import { open } from 'fs/promises';
import * as os from 'os';
import * as path from 'path';
async function main() {
  const p = path.join(os.tmpdir(), 'kml_fh_ops_' + process.pid + '.txt');
  fs.writeFileSync(p, 'hello file handle');
  const fh = await open(p, 'r');
  console.log(typeof fh.fd, fh.fd > 2);
  const r = await fh.read(Buffer.alloc(5), 0, 5, 0);
  console.log(r.bytesRead, r.buffer.toString());
  console.log((await fh.stat()).size);
  await fh.close();
  console.log('closed', fh.fd);
  const w = await open(p, 'w');
  await w.writeFile('rewritten');
  await w.close();
  console.log(fs.readFileSync(p, 'utf8'));
  fs.unlinkSync(p);
}
main();
`, "number true\n5 hello\n17\nclosed -1\nrewritten\n")
}

// a FileHandle's createReadStream, a custom fs, and the signal option.
func TestE2EFsStreamFileHandleFsAndSignalOptions(t *testing.T) {
	assertOutputImports(t, `
import * as fs from 'fs';
import { open } from 'fs/promises';
import * as os from 'os';
import * as path from 'path';
async function main() {
  const p = path.join(os.tmpdir(), 'kml_fh_stream_' + process.pid + '.txt');
  fs.writeFileSync(p, 'hello file handle');
  const fh = await open(p, 'r');
  const rs = fh.createReadStream({ start: 6, encoding: 'utf8' });
  for await (const c of rs) console.log('chunk', c);
  console.log('handle closed', fh.fd);
  const calls: string[] = [];
  const custom = {
    open: (pp: any, f: any, m: any, cb: any) => { calls.push('open'); fs.open(pp, f, m, cb); },
    read: (fd: any, b: any, o: any, l: any, pos: any, cb: any) => { calls.push('read'); fs.read(fd, b, o, l, pos, cb); },
    close: (fd: any, cb: any) => { calls.push('close'); fs.close(fd, cb); },
  };
  for await (const c of fs.createReadStream(p, { fs: custom, encoding: 'utf8' })) console.log('custom', c);
  await new Promise((res) => setTimeout(res, 10));
  console.log(calls.join(','));
  const ac = new AbortController();
  const s3 = fs.createReadStream(p, { signal: ac.signal });
  s3.on('error', (e: any) => console.log('signal', e.name));
  ac.abort();
  await new Promise((res) => setTimeout(res, 10));
  fs.unlinkSync(p);
}
main();
`, "chunk file handle\nhandle closed -1\ncustom hello file handle\nopen,read,read,close\nsignal AbortError\n")
}
