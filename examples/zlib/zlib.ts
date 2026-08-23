// zlib — Node's one-shot compression module: gzip/gunzip, deflate/inflate,
// deflateRaw/inflateRaw, and unzip, in both the *Sync and (err, result)
// callback forms. Import-gated (a virtual built-in module, not a real file).
//
// The same libz backend that powers CompressionStream/DecompressionStream —
// here exposed as whole-buffer calls. Input can be a string (encoded as UTF-8),
// a Buffer/Uint8Array, an ArrayBuffer, or a DataView; the result is always a
// Buffer.

import zlib from 'zlib'

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
