// The stream module's other exports: PassThrough (an identity Transform),
// the callback forms of finished() and pipeline() (their promise twins live
// in 'stream/promises'), duplexPair(), and the options form of Duplex.
import { PassThrough, Writable, Duplex, finished, pipeline, duplexPair } from 'stream';

// PassThrough passes each written chunk through.
const echo = new PassThrough();
echo.on("data", (chunk) => { console.log("echo:", chunk.toString()); });
finished(echo, (err) => { console.log("echo finished, clean:", !err); });
echo.write("kalimera");
echo.end("thessaloniki");

// Callback pipeline: source → passthrough → sink.
const src = new PassThrough();
const sink = new Writable({
  write(chunk: Buffer, _enc, cb) { console.log("sink:", chunk.toString()); cb(); }
});
pipeline(src, new PassThrough(), sink, (err) => { console.log("pipeline done", err ? err.message : "ok"); });
src.end("via pipeline");

// duplexPair: what one side writes, the other side reads.
const [clientSide, serverSide] = duplexPair();
serverSide.on("data", (d) => { console.log("server saw:", d.toString()); });
serverSide.on("end", () => { console.log("server side ended"); });
clientSide.end("hello over the pair");

// new Duplex({ read, write, final }): two sides on one stream, independent
// (unlike a Transform, nothing crosses between them).
const dup = new Duplex({
  read() { this.push("from-read"); this.push(null); },
  write(chunk: Buffer, _enc, cb) { console.log("dup sink:", chunk.toString()); cb(); },
  final(cb) { console.log("dup finished"); cb(); },
});
dup.on("data", (c) => { console.log("dup data:", c.toString()); });
dup.write("into-write");
dup.end();
