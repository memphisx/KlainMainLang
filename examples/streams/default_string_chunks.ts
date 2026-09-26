// Byte-mode streams (the default): a pushed string is stored as a Buffer,
// read() returns what is buffered, setEncoding() decodes it back to strings.
import stream from 'stream';

const r = new stream.Readable({ read() {} });
r.push("alpha");
r.push("beta");
r.push(null);
r.on('data', (c) => { console.log(c, c.toString()); });
r.on('end', () => { console.log("done"); });

// destroy() tears the stream down and emits 'close'.
const short = new stream.Readable({ read() {} });
short.on('close', () => { console.log("short closed"); });
short.destroy();

// Synchronous read(): the whole buffer, or n bytes of it; null when empty.
const q = new stream.Readable({ read() {} });
q.push("queued");
console.log(q.read(3), q.read(), q.read());

// setEncoding: chunks come out as strings, a character split across two
// pushes decoded whole.
const text = new stream.Readable({ read() {} });
text.setEncoding("utf8");
text.on('data', (s) => { console.log("text:", s); });
text.push(Buffer.from([0x4b, 0xce]));
text.push(Buffer.from([0xb1, 0x21]));

// unshift(): put a chunk back at the front (object mode keeps them apart).
const o = new stream.Readable({ objectMode: true, read() {} });
o.push("tail");
o.unshift("head");
console.log(o.read(), o.read());
