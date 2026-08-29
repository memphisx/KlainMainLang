package tests

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// assets_test.go — TDD-00142 Stage 7: compile-time SPA embedding (klain:assets +
// Webview({ serve })). The fixture is tests/testdata/embedfixture (a tiny dist:
// index.html + a binary assets/blob.bin), embedded at emit time. embedDir
// resolves its path against the compiler's CWD, which differs between harnesses
// (`go test ./tests/` runs in tests/, but the compiled test binary used by
// `make test-par` runs from the repo root), so the tests anchor an absolute
// fixture path to this file's own location instead of a CWD-relative one.

// embedFixtureDir returns the absolute path of the embed fixture, independent of
// the process CWD.
func embedFixtureDir() string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "testdata", "embedfixture")
}

// TestE2EEmbedGetByteExact embeds the fixture and reads its files back through
// embedDir/get, asserting the byte-exact length + checksum of a BINARY asset
// (the whole point vs a string body: fonts/images must survive verbatim).
func TestE2EEmbedGetByteExact(t *testing.T) {
	out := compileAndRunImports(t, `
import { embedDir } from 'klain:assets'
const d = embedDir(`+strconv.Quote(embedFixtureDir())+`)
const html = d.get("/index.html")
const blob = d.get("/assets/blob.bin")
console.log("html=" + html.byteLength + " blob=" + blob.byteLength)
const bytes = new Uint8Array(blob)
let sum = 0
for (let i = 0; i < bytes.length; i = i + 1) { sum = sum + bytes[i] }
console.log("sum=" + sum)
const miss = d.get("/nope")
console.log("miss=" + miss.byteLength)
`)
	for _, want := range []string{"html=48 blob=14", "sum=1016", "miss=0"} {
		if !strings.Contains(out, want) {
			t.Fatalf("embed get: missing %q in:\n%s", want, out)
		}
	}
}

// TestE2EWebviewServeCompiles asserts the single-file `serve` one-liner compiles
// and links (the .incbin blob + the embedded static server).
func TestE2EWebviewServeCompiles(t *testing.T) {
	src := `
import { Webview } from 'klain:webview'
const w = new Webview({ title: "Emb", width: 400, height: 300, serve: ` + strconv.Quote(embedFixtureDir()) + ` })
w.html("<h1>x</h1>")
w.terminate()
`
	bin := buildBinaryImports(t, src)
	if _, err := os.Stat(bin); err != nil {
		t.Fatalf("serve binary not produced: %v", err)
	}
}

// TestE2EEmbedNonLiteralRejected asserts a non-literal embedDir path is a clean
// compile-time rejection (the directory is read at compile time).
func TestE2EEmbedNonLiteralRejected(t *testing.T) {
	err := resolveAndEmitMultiFile(t, map[string]string{"main.ts": `
import { embedDir } from 'klain:assets'
const p: string = "./testdata/embedfixture"
const d = embedDir(p)
`}, "main.ts")
	if err == nil || !strings.Contains(err.Error(), "string literal") {
		t.Fatalf("expected a non-literal embedDir rejection, got: %v", err)
	}
}

// TestE2EWebviewServeSmoke is the Stage 7 windowed run: `serve` embeds the
// fixture, starts the in-binary server, navigates, and the window renders it
// (the probe reads the embedded page's <title>). Gated behind KML_WEBVIEW_SMOKE.
func TestE2EWebviewServeSmoke(t *testing.T) {
	if os.Getenv("KML_WEBVIEW_SMOKE") != "1" {
		t.Skip("windowed smoke test — set KML_WEBVIEW_SMOKE=1 to run")
	}
	src := `
import { Webview } from 'klain:webview'
const w = new Webview({ title: "Emb", width: 400, height: 300, serve: ` + strconv.Quote(embedFixtureDir()) + ` })
w.bind("done", (raw: string): string => {
  console.log("page title: " + raw)
  w.terminate()
  return "null"
})
w.init("window.addEventListener('load', () => { setTimeout(() => window.done(document.title), 100) })")
w.run()
console.log("run returned")
`
	runWebviewWindow(t, src, "Fixture", "run returned")
}
