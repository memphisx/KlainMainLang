// The stream module's named function exports: PassThrough (identity
// Transform, string chunks by default), the callback forms of finished()
// and pipeline() (their Promise twins live in 'stream/promises'), and
// duplexPair() — two cross-wired Duplex handles.
import { PassThrough, Writable, finished, pipeline, duplexPair } from 'stream';

// PassThrough echoes writes to its readable side untouched.
const echo = new PassThrough();
echo.on("data", (chunk) => { console.log("echo:", chunk); });
finished(echo, (err) => { console.log("echo finished, clean:", err === null); });
echo.write("kalimera");
echo.end("thessaloniki");

// Callback pipeline: source → passthrough → sink.
const src = new PassThrough();
const sink = new Writable<string>({
  write: (chunk: string) => { console.log("sink:", chunk); }
});
pipeline(src, new PassThrough(), sink, () => { console.log("pipeline done"); });
src.end("via pipeline");

// duplexPair: what one side writes, the other side reads.
const [clientSide, serverSide] = duplexPair();
serverSide.on("data", (d) => { console.log("server saw:", d); });
serverSide.on("end", () => { console.log("server side ended"); });
clientSide.end("hello over the pair");
