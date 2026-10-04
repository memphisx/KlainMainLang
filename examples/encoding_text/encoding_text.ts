// TextEncoder / TextDecoder — UTF-8 string <-> raw bytes.
//
// V1 scope (see docs/status/ENCODING-TEXT.md): UTF-8 only. This compiler's
// strings are already raw UTF-8 byte sequences (the same premise btoa/atob
// already rely on), so encode/decode are direct byte copies, no real
// transcoding involved. TextDecoder's optional label argument is accepted
// (evaluated, then ignored) rather than validated against a table of
// recognized encodings.

const encoder = new TextEncoder()
const decoder = new TextDecoder()

// encode(): string -> Uint8Array
const bytes = encoder.encode("Hello, KlainMainLang!")
console.log(bytes.length)
console.log(bytes[0]) // 72 ('H')

// decode(): Uint8Array -> string
console.log(decoder.decode(bytes))

// decode() also accepts an ArrayBuffer directly (e.g. from fs.readFileSync's
// binary-aware siblings or a fetch response's .arrayBuffer()).
const buf = new ArrayBuffer(5)
const view: Uint8Array = new Uint8Array(buf)
view[0] = 72  // H
view[1] = 101 // e
view[2] = 108 // l
view[3] = 108 // l
view[4] = 111 // o
console.log(decoder.decode(buf))

// atob validates its input: a character outside the base64 alphabet throws
// a real InvalidCharacterError DOMException, matching WHATWG atob.
try {
    atob("not base64!");
} catch (e) {
    console.log((e as Error).name);
}

// TextDecoder speaks UTF-16 and windows-1252 (the `latin1` label) too.
console.log(new TextDecoder("utf-16le").decode(new Uint8Array([0x54, 0x00, 0x68, 0x00, 0xAC, 0x20])));  // Th€
const latin = new TextDecoder("latin1");
console.log(latin.encoding, latin.decode(new Uint8Array([0x63, 0x61, 0x66, 0xE9])));                     // windows-1252 café

// A multi-byte character split across chunks: { stream: true } holds the
// partial sequence until the next call.
const streaming = new TextDecoder();
const euro = new TextEncoder().encode("€");                                                                  // 3 bytes
console.log(JSON.stringify(streaming.decode(euro.subarray(0, 2), { stream: true })));                         // ""
console.log(streaming.decode(euro.subarray(2)));                                                              // €

// fatal: invalid bytes throw instead of becoming U+FFFD.
try {
    new TextDecoder("utf-8", { fatal: true }).decode(new Uint8Array([0xFF]));
} catch (e: any) {
    console.log(e.code);                                                                                       // ERR_ENCODING_INVALID_ENCODED_DATA
}
