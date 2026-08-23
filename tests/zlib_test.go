package tests

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"io"
	"strconv"
	"strings"
	"testing"
)

// bytesToU8Literal renders raw bytes as a `new Uint8Array([...])` initializer —
// the round-trip-safe way to hand arbitrary binary (a gzip stream) to TS
// source, since a plain string literal + TextEncoder would re-encode any
// byte > 0x7f as multi-byte UTF-8.
func bytesToU8Literal(b []byte) string {
	parts := make([]string, len(b))
	for i, x := range b {
		parts[i] = strconv.Itoa(int(x))
	}
	return "new Uint8Array([" + strings.Join(parts, ",") + "])"
}

// parseDecimalBytes reverses the comma-joined decimal byte codes a test program
// prints back into the raw bytes.
func parseDecimalBytes(t *testing.T, s string) []byte {
	t.Helper()
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	fields := strings.Split(s, ",")
	out := make([]byte, len(fields))
	for i, f := range fields {
		n, err := strconv.Atoi(strings.TrimSpace(f))
		if err != nil {
			t.Fatalf("parseDecimalBytes: %q: %v", f, err)
		}
		out[i] = byte(n)
	}
	return out
}

// --- Node `zlib` core module (see docs/adr/ADR-00321.md) ---
//
// One-shot gzip/gunzip, deflate/inflate, deflateRaw/inflateRaw, unzip in both
// the *Sync and (err, result) callback forms, over the shared libz runtime
// helper @__kml_zlib_oneshot. Round-trips, every accepted input type, the
// { level } option, error handling, and Go cross-implementation interop.

func TestE2EZlibGzipRoundtripSync(t *testing.T) {
	assertOutputImports(t, `
import zlib from 'zlib'
const dec = new TextDecoder()
const text = "compress me ".repeat(50)
const gz = zlib.gzipSync(text)
console.log("magic:", gz[0] === 0x1f && gz[1] === 0x8b)
console.log("smaller:", gz.length < text.length)
console.log("roundtrip:", dec.decode(zlib.gunzipSync(gz)) === text)
`, "magic: true\nsmaller: true\nroundtrip: true")
}

func TestE2EZlibDeflateInflateSync(t *testing.T) {
	assertOutputImports(t, `
import zlib from 'zlib'
const dec = new TextDecoder()
const text = "deflate me ".repeat(40)
const df = zlib.deflateSync(text)
const raw = zlib.deflateRawSync(text)
console.log("deflate:", dec.decode(zlib.inflateSync(df)) === text)
console.log("raw:", dec.decode(zlib.inflateRawSync(raw)) === text)
console.log("unzip:", dec.decode(zlib.unzipSync(zlib.gzipSync(text))) === text)
`, "deflate: true\nraw: true\nunzip: true")
}

func TestE2EZlibLevelOption(t *testing.T) {
	// A higher compression level must not break the round-trip; both levels
	// decode back to the same text.
	assertOutputImports(t, `
import zlib from 'zlib'
const dec = new TextDecoder()
const text = "level test ".repeat(80)
const fast = zlib.deflateSync(text, { level: 1 })
const best = zlib.deflateSync(text, { level: 9 })
console.log("fast ok:", dec.decode(zlib.inflateSync(fast)) === text)
console.log("best ok:", dec.decode(zlib.inflateSync(best)) === text)
console.log("best <= fast:", best.length <= fast.length)
`, "fast ok: true\nbest ok: true\nbest <= fast: true")
}

func TestE2EZlibBinaryInputTypes(t *testing.T) {
	// A Uint8Array, an ArrayBuffer, and a DataView are all accepted inputs,
	// matching Node.
	assertOutputImports(t, `
import zlib from 'zlib'
const enc = new TextEncoder()
const dec = new TextDecoder()
const text = "binary input ".repeat(30)
const u8 = enc.encode(text)
console.log("uint8:", dec.decode(zlib.gunzipSync(zlib.gzipSync(u8))) === text)
const ab = new ArrayBuffer(u8.length)
const dv = new DataView(ab)
for (let i = 0; i < u8.length; i++) { dv.setUint8(i, u8[i]) }
console.log("arraybuffer:", dec.decode(zlib.gunzipSync(zlib.gzipSync(ab))) === text)
console.log("dataview:", dec.decode(zlib.gunzipSync(zlib.gzipSync(dv))) === text)
`, "uint8: true\narraybuffer: true\ndataview: true")
}

func TestE2EZlibCallbackForm(t *testing.T) {
	assertOutputImports(t, `
import zlib from 'zlib'
const dec = new TextDecoder()
const text = "callback form ".repeat(25)
zlib.gzip(text, (err, gz) => {
  console.log("gz err null:", err === null)
  zlib.gunzip(gz, (e2, back) => {
    console.log("roundtrip:", dec.decode(back) === text)
  })
})
`, "gz err null: true\nroundtrip: true")
}

func TestE2EZlibCallbackErrorOnCorruptInput(t *testing.T) {
	// Corrupt gzip input surfaces a non-null error to the callback, not a
	// crash.
	assertOutputImports(t, `
import zlib from 'zlib'
const enc = new TextEncoder()
const bad = enc.encode("this is definitely not gzip data")
zlib.gunzip(bad, (err, out) => {
  console.log("err set:", err !== null)
})
`, "err set: true")
}

func TestE2EZlibSyncThrowsOnCorruptInput(t *testing.T) {
	assertOutputImports(t, `
import zlib from 'zlib'
const enc = new TextEncoder()
const bad = enc.encode("not compressed at all, just text")
try {
  zlib.inflateSync(bad)
  console.log("no throw")
} catch (e) {
  console.log("threw")
}
`, "threw")
}

func TestE2EZlibNamedImports(t *testing.T) {
	assertOutputImports(t, `
import { gzipSync, gunzipSync } from 'zlib'
const dec = new TextDecoder()
const text = "named import ".repeat(20)
console.log(dec.decode(gunzipSync(gzipSync(text))) === text)
`, "true")
}

func TestE2EZlibMissingImportRejected(t *testing.T) {
	err := resolveAndEmitMultiFile(t, map[string]string{
		"main.ts": `
console.log(zlib.gzipSync("hi").length)
`,
	}, "main.ts")
	if err == nil {
		t.Fatal("expected a compile error for using zlib.gzipSync without importing 'zlib', got none")
	}
}

// --- Go cross-implementation interop: a payload produced by Go's standard
// compress/* packages must decompress in KlainMainLang, and vice versa. ---

func TestE2EZlibGunzipsGoGzip(t *testing.T) {
	payload := strings.Repeat("go->kml gzip interop\n", 300)
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	zw.Write([]byte(payload))
	zw.Close()

	src := `
import zlib from 'zlib'
const dec = new TextDecoder()
const gz = ` + bytesToU8Literal(buf.Bytes()) + `
console.log(dec.decode(zlib.gunzipSync(gz)).length === ` + strconv.Itoa(len(payload)) + `)
`
	assertOutputImports(t, src, "true")
}

func TestE2EZlibGoReadsKmlGzip(t *testing.T) {
	// KlainMainLang gzips a known string and writes the raw bytes as decimal
	// codes; Go's gzip reader must decode them back to the original.
	payload := "kml->go gzip interop payload, repeated. "
	out := compileAndRunImports(t, `
import zlib from 'zlib'
const gz = zlib.gzipSync(`+"`"+strings.Repeat(payload, 50)+"`"+`)
let s = ""
for (let i = 0; i < gz.length; i++) {
  s = s + gz[i] + (i < gz.length - 1 ? "," : "")
}
console.log(s)
`)
	gzBytes := parseDecimalBytes(t, out)
	zr, err := gzip.NewReader(bytes.NewReader(gzBytes))
	if err != nil {
		t.Fatalf("go gzip.NewReader on kml output: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("go gzip read: %v", err)
	}
	want := strings.Repeat(payload, 50)
	if string(got) != want {
		t.Fatalf("go decode mismatch: got %d bytes, want %d", len(got), len(want))
	}
}

func TestE2EZlibInflatesGoZlibAndFlate(t *testing.T) {
	payload := strings.Repeat("zlib+flate interop\n", 200)
	var zbuf bytes.Buffer
	zw := zlib.NewWriter(&zbuf)
	zw.Write([]byte(payload))
	zw.Close()
	var fbuf bytes.Buffer
	fw, _ := flate.NewWriter(&fbuf, flate.DefaultCompression)
	fw.Write([]byte(payload))
	fw.Close()

	src := `
import zlib from 'zlib'
const dec = new TextDecoder()
const z = ` + bytesToU8Literal(zbuf.Bytes()) + `
const f = ` + bytesToU8Literal(fbuf.Bytes()) + `
console.log("zlib:", dec.decode(zlib.inflateSync(z)).length === ` + strconv.Itoa(len(payload)) + `)
console.log("raw:", dec.decode(zlib.inflateRawSync(f)).length === ` + strconv.Itoa(len(payload)) + `)
`
	assertOutputImports(t, src, "zlib: true\nraw: true")
}
