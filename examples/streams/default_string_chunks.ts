// The un-parameterized Node stream constructors default to string chunks,
// matching Node's non-objectMode semantics. Use `<T>` for numeric or
// typed-array chunk streams.
import stream from 'stream';

const r = new stream.Readable({ read() {} });
r.push("alpha");
r.push("beta");
r.push(null);
r.on('data', (c) => { console.log(c.toString()); });
r.on('end', () => { console.log("done"); });

// destroy() tears the stream down; setEncoding('utf8') is a no-op on the
// string-chunk default.
const short = new stream.Readable({ read() {} });
short.setEncoding("utf8");
short.on('close', () => { console.log("short closed"); });
short.destroy();

// Synchronous read(): pops queued chunks, null when empty.
const q = new stream.Readable({ read() {} });
q.push("queued");
console.log(q.read(), q.read() === null);

// unshift(): put a chunk back at the front.
q.push("tail");
q.unshift("head");
console.log(q.read(), q.read());
