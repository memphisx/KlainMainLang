// zlib — Node's compression module: gzip/gunzip, deflate/inflate,
// deflateRaw/inflateRaw and unzip, as one-shot calls (*Sync and (err, result)
// callbacks) and as streams (createGzip() and friends), plus crc32.
//
// Input can be a string (encoded as UTF-8), a Buffer/Uint8Array, an
// ArrayBuffer, or a DataView; the result is always a Buffer.

import zlib from 'zlib'
import { pipeline, Readable, Writable } from 'stream'

const dec = new TextDecoder()
const text = "Klain compresses well. ".repeat(20)

// ── gzip / gunzip ─────────────────────────────────────────────────────────
const gz = zlib.gzipSync(text)
console.log("gzip bytes:", gz.length, "(from", text.length + ")")
console.log("gzip magic:", gz[0] === 0x1f && gz[1] === 0x8b)
console.log("gunzip:", dec.decode(zlib.gunzipSync(gz)) === text)

// ── deflate / inflate, with a compression level ───────────────────────────
const best = zlib.deflateSync(text, { level: 9 })
console.log("inflate:", dec.decode(zlib.inflateSync(best)) === text)

// ── raw deflate (no zlib/gzip wrapper) ────────────────────────────────────
const raw = zlib.deflateRawSync(text)
console.log("inflateRaw:", dec.decode(zlib.inflateRawSync(raw)) === text)

// ── unzip auto-detects a gzip or zlib stream ──────────────────────────────
console.log("unzip:", dec.decode(zlib.unzipSync(gz)) === text)

// ── callback form: (err, result) ──────────────────────────────────────────
zlib.gzip(text, (err, out) => {
  zlib.gunzip(out, (e2, back) => {
    console.log("callback roundtrip:", dec.decode(back) === text)
  })
})

// ── streams: gzip then gunzip through a pipeline ──────────────────────────
const chunks: Buffer[] = []
pipeline(
  Readable.from([text.slice(0, 200), text.slice(200)]),
  zlib.createGzip({ level: 6 }),
  zlib.createGunzip(),
  new Writable({ write(c: Buffer, _e: string, cb: () => void) { chunks.push(c); cb() } }),
  (err) => console.log("stream roundtrip:", err, Buffer.concat(chunks).toString() === text),
)

// ── crc32 ─────────────────────────────────────────────────────────────────
console.log("crc32:", zlib.crc32("hello"), zlib.crc32("world", zlib.crc32("hello ")))
