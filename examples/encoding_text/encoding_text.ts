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
    console.log(e.name);
}
