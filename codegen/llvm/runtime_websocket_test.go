package llvm

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildAndRunIR assembles globals (the runtime helpers under test, already
// emitted onto an Emitter via its ensure* methods) with a hand-written
// mainIR (a complete `define i32 @main() {...}` plus whatever private
// constants/helper functions it needs), compiles the result with clang, runs
// it, and returns stdout. This is the "Go-level unit test" TDD-00039 Stage 0
// calls for: these runtime helpers have no AST/parser hook yet (that's
// Stage 1/3), so there's no TypeScript source that could reach them the way
// every other tests/*_test.go harness (buildBinary et al.) works.
func buildAndRunIR(t *testing.T, globals, mainIR string) string {
	t.Helper()
	if _, err := exec.LookPath("clang"); err != nil {
		t.Skip("clang not found in PATH")
	}

	var ir strings.Builder
	ir.WriteString("; test harness IR — codegen/llvm/runtime_websocket_test.go\n\n")
	ir.WriteString(globals)
	ir.WriteString("\n\n")
	ir.WriteString(mainIR)

	dir := t.TempDir()
	llFile := filepath.Join(dir, "harness.ll")
	binFile := filepath.Join(dir, "harness")
	if err := os.WriteFile(llFile, []byte(ir.String()), 0644); err != nil {
		t.Fatalf("write IR: %v", err)
	}
	out, err := exec.Command("clang", "-O1", llFile, "-o", binFile).CombinedOutput()
	if err != nil {
		t.Fatalf("clang: %v\n%s\n--- IR ---\n%s", err, out, ir.String())
	}
	runOut, err := exec.Command(binFile).CombinedOutput()
	if err != nil {
		t.Fatalf("run: %v\n%s", err, runOut)
	}
	return string(runOut)
}

// wsHexDumpHelpers is the shared IR scaffolding (printf declare, format
// string constants, and a hex-dump helper) every test in this file builds
// its own `main` on top of.
const wsHexDumpHelpers = `
@.fmt_hex = private unnamed_addr constant [5 x i8] c"%02x\00"
@.fmt_nl = private unnamed_addr constant [2 x i8] c"\0A\00"
@.fmt_pstr = private unnamed_addr constant [6 x i8] c"%.*s\0A\00"
@.fmt_d = private unnamed_addr constant [4 x i8] c"%d\0A\00"
@.fmt_dd = private unnamed_addr constant [7 x i8] c"%d %d\0A\00"
@.fmt_s = private unnamed_addr constant [4 x i8] c"%s\0A\00"

define void @dumpHex(ptr %p, i64 %len) {
entry:
  br label %loop
loop:
  %i = phi i64 [ 0, %entry ], [ %inext, %body ]
  %cont = icmp slt i64 %i, %len
  br i1 %cont, label %body, label %done
body:
  %bp = getelementptr i8, ptr %p, i64 %i
  %bv = load i8, ptr %bp, align 1
  %bv32 = zext i8 %bv to i32
  call i32 (ptr, ...) @printf(ptr @.fmt_hex, i32 %bv32)
  %inext = add i64 %i, 1
  br label %loop
done:
  call i32 (ptr, ...) @printf(ptr @.fmt_nl)
  ret void
}
`

// TestWSSHA1KnownAnswers checks __kml_ws_sha1 against the standard FIPS
// 180-4 example digests (empty string, "abc", and the two-block 56-byte
// example — all three independently confirmed via Python's hashlib, not
// recalled from memory) plus the real RFC 6455 §1.3 handshake worked
// example end-to-end (SHA-1 then base64, exactly what Sec-WebSocket-Accept
// needs).
func TestWSSHA1KnownAnswers(t *testing.T) {
	e := NewEmitter()
	e.ensurePrintf()
	e.ensureWSSHA1()
	e.ensureBase64Encode()

	mainIR := wsHexDumpHelpers + `
@.s_empty = private unnamed_addr constant [1 x i8] c"\00"
@.s_abc = private unnamed_addr constant [4 x i8] c"abc\00"
@.s_two = private unnamed_addr constant [57 x i8] c"abcdbcdecdefdefgefghfghighijhijkijkljklmklmnlmnomnopnopq\00"
@.s_handshake = private unnamed_addr constant [61 x i8] c"dGhlIHNhbXBsZSBub25jZQ==258EAFA5-E914-47DA-95CA-C5AB0DC85B11\00"

define i32 @main() {
entry:
  %d1 = alloca [20 x i8], align 1
  call void @__kml_ws_sha1(ptr @.s_empty, i64 0, ptr %d1)
  call void @dumpHex(ptr %d1, i64 20)

  %d2 = alloca [20 x i8], align 1
  call void @__kml_ws_sha1(ptr @.s_abc, i64 3, ptr %d2)
  call void @dumpHex(ptr %d2, i64 20)

  %d3 = alloca [20 x i8], align 1
  call void @__kml_ws_sha1(ptr @.s_two, i64 56, ptr %d3)
  call void @dumpHex(ptr %d3, i64 20)

  ; __kml_btoa is strlen-based (runtime_encoding.go), not length-aware — a
  ; real SHA-1 digest can legitimately contain an embedded 0x00 byte for
  ; some inputs, which Stage 1's real Sec-WebSocket-Accept computation will
  ; need a length-aware base64 encode to handle safely. For this
  ; known-answer test specifically it's safe to lean on strlen: the 20
  ; digest bytes for this exact input are independently confirmed
  ; (Python's hashlib) to contain no zero byte, and the 21st byte is
  ; explicitly zeroed here to terminate the scan exactly at the digest's end.
  %d4 = alloca [21 x i8], align 1
  %d4end = getelementptr [21 x i8], ptr %d4, i64 0, i64 20
  store i8 0, ptr %d4end, align 1
  call void @__kml_ws_sha1(ptr @.s_handshake, i64 60, ptr %d4)
  %accept = call ptr @__kml_btoa(ptr %d4)
  call i32 (ptr, ...) @printf(ptr @.fmt_s, ptr %accept)

  ret i32 0
}`

	out := buildAndRunIR(t, e.globals.String(), mainIR)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 lines of output, got %d: %q", len(lines), out)
	}
	// Every value below was independently confirmed via Python's hashlib
	// (see this test's own investigation), not typed from memory.
	want := []string{
		"da39a3ee5e6b4b0d3255bfef95601890afd80709", // SHA1("")
		"a9993e364706816aba3e25717850c26c9cd0d89d", // SHA1("abc")
		"84983e441c3bd26ebaae4aa1f95129e5e54670f1", // SHA1(FIPS two-block example)
		"s3pPLMBiTxaQ9kYGzzhZRbK+xOo=",             // RFC 6455 §1.3 worked example
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d: got %q, want %q", i, lines[i], w)
		}
	}
}

// TestWSFrameRoundTrip checks __kml_ws_frame_encode/__kml_ws_frame_decode
// against RFC 6455 §5.7's own worked example (a masked "Hello" text frame,
// byte-for-byte — the mask key and expected wire bytes independently
// verified via Python, not recalled from memory) plus an unmasked,
// extended-16-bit-length (>125 bytes) binary frame to exercise the
// mid-length header form, and confirms decode correctly reports
// "incomplete" against a truncated buffer.
func TestWSFrameRoundTrip(t *testing.T) {
	e := NewEmitter()
	e.ensurePrintf()
	e.ensureWSFrameEncode()
	e.ensureWSFrameDecode()

	longPayload := strings.Repeat("A", 200)

	mainIR := fmt.Sprintf(`%s
@.s_hello = private unnamed_addr constant [5 x i8] c"Hello"
@.s_long = private unnamed_addr constant [200 x i8] c"%s"

define i32 @main() {
entry:
  ; RFC 6455 5.7 example: client "Hello", mask key 0x37FA213D (939139389) ->
  ; 81 85 37 FA 21 3D 7F 9F 4D 51 58 (11 bytes total)
  %%enc1 = call { ptr, i64 } @__kml_ws_frame_encode(i32 1, i1 true, ptr @.s_hello, i64 5, i32 939139389)
  %%enc1buf = extractvalue { ptr, i64 } %%enc1, 0
  %%enc1len = extractvalue { ptr, i64 } %%enc1, 1
  %%enc1len32 = trunc i64 %%enc1len to i32
  call void @dumpHex(ptr %%enc1buf, i64 %%enc1len)
  call i32 (ptr, ...) @printf(ptr @.fmt_d, i32 %%enc1len32)

  %%dec1 = call { i32, i32, ptr, i64, i64 } @__kml_ws_frame_decode(ptr %%enc1buf, i64 %%enc1len)
  %%dec1status = extractvalue { i32, i32, ptr, i64, i64 } %%dec1, 0
  %%dec1opcode = extractvalue { i32, i32, ptr, i64, i64 } %%dec1, 1
  %%dec1payload = extractvalue { i32, i32, ptr, i64, i64 } %%dec1, 2
  %%dec1plen = extractvalue { i32, i32, ptr, i64, i64 } %%dec1, 3
  %%dec1consumed = extractvalue { i32, i32, ptr, i64, i64 } %%dec1, 4
  call i32 (ptr, ...) @printf(ptr @.fmt_dd, i32 %%dec1status, i32 %%dec1opcode)
  %%dec1plen32 = trunc i64 %%dec1plen to i32
  call i32 (ptr, ...) @printf(ptr @.fmt_pstr, i32 %%dec1plen32, ptr %%dec1payload)
  %%dec1consumed32 = trunc i64 %%dec1consumed to i32
  call i32 (ptr, ...) @printf(ptr @.fmt_d, i32 %%dec1consumed32)

  ; Unmasked >125-byte frame — exercises the extended 16-bit length form.
  %%enc2 = call { ptr, i64 } @__kml_ws_frame_encode(i32 2, i1 false, ptr @.s_long, i64 200, i32 0)
  %%enc2buf = extractvalue { ptr, i64 } %%enc2, 0
  %%enc2len = extractvalue { ptr, i64 } %%enc2, 1
  %%enc2len32 = trunc i64 %%enc2len to i32
  call i32 (ptr, ...) @printf(ptr @.fmt_d, i32 %%enc2len32)
  call void @dumpHex(ptr %%enc2buf, i64 4)

  %%dec2 = call { i32, i32, ptr, i64, i64 } @__kml_ws_frame_decode(ptr %%enc2buf, i64 %%enc2len)
  %%dec2status = extractvalue { i32, i32, ptr, i64, i64 } %%dec2, 0
  %%dec2plen = extractvalue { i32, i32, ptr, i64, i64 } %%dec2, 3
  %%dec2consumed = extractvalue { i32, i32, ptr, i64, i64 } %%dec2, 4
  call i32 (ptr, ...) @printf(ptr @.fmt_d, i32 %%dec2status)
  %%dec2plen32 = trunc i64 %%dec2plen to i32
  call i32 (ptr, ...) @printf(ptr @.fmt_d, i32 %%dec2plen32)
  %%dec2consumed32 = trunc i64 %%dec2consumed to i32
  call i32 (ptr, ...) @printf(ptr @.fmt_d, i32 %%dec2consumed32)

  ; Truncated buffer (only 1 byte available) must report incomplete (0).
  %%dec3 = call { i32, i32, ptr, i64, i64 } @__kml_ws_frame_decode(ptr %%enc1buf, i64 1)
  %%dec3status = extractvalue { i32, i32, ptr, i64, i64 } %%dec3, 0
  call i32 (ptr, ...) @printf(ptr @.fmt_d, i32 %%dec3status)

  ret i32 0
}`, wsHexDumpHelpers, longPayload)

	out := buildAndRunIR(t, e.globals.String(), mainIR)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 11 {
		t.Fatalf("expected 11 lines of output, got %d: %q", len(lines), out)
	}
	// Frame bytes/length independently verified via Python (XOR-masking
	// "Hello" with 0x37FA213D cyclically), matching RFC 6455 §5.7 exactly.
	checks := []struct {
		idx  int
		want string
		desc string
	}{
		{0, "818537fa213d7f9f4d5158", "RFC 6455 §5.7 masked \"Hello\" frame bytes"},
		{1, "11", "encoded frame length (2 header + 4 mask + 5 payload)"},
		{2, "1 1", "decode status=ok, opcode=text"},
		{3, "Hello", "decoded (unmasked) payload"},
		{4, "11", "decode consumed"},
		{5, "204", "mid-length frame total (2 header + 2 ext-length + 200 payload)"},
		{6, "827e00c8", "mid-length header bytes (opcode=2, len126, 0x00C8=200)"},
		{7, "1", "mid-frame decode status"},
		{8, "200", "mid-frame decoded payload length"},
		{9, "204", "mid-frame decode consumed"},
		{10, "0", "truncated-buffer decode status (incomplete)"},
	}
	for _, c := range checks {
		if lines[c.idx] != c.want {
			t.Errorf("%s: got %q, want %q", c.desc, lines[c.idx], c.want)
		}
	}
}
