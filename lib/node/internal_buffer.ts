// The part of Node's `buffer` module (lib/buffer.js) beyond its globals:
// `Buffer`, `Blob`, `atob` and `btoa` are the globals; this file holds the
// constants, isUtf8, isAscii, transcode and SlowBuffer.

class NodeTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

export const kMaxLength = 9007199254740991;
export const kStringMaxLength = 536870888;
export const constants = { MAX_LENGTH: kMaxLength, MAX_STRING_LENGTH: kStringMaxLength };
export let INSPECT_MAX_BYTES = 50;

function received(value: any): string {
    if (value === null || value === undefined) return 'Received ' + String(value);
    if (typeof value === 'function') return 'Received function ' + (value as Function).name;
    if (typeof value === 'object') return 'Received an instance of ' + (Array.isArray(value) ? 'Array' : 'Object');
    const shown = typeof value === 'string' ? "'" + (value as string) + "'" : String(value);
    return 'Received type ' + typeof value + ' (' + shown + ')';
}

// The bytes of an ArrayBuffer, a Buffer or another TypedArray or DataView.
function bytesOf(input: any): Uint8Array {
    if (input instanceof Uint8Array) return input as Uint8Array;
    if (input instanceof ArrayBuffer) return new Uint8Array(input as ArrayBuffer);
    if (ArrayBuffer.isView(input)) {
        const view: any = input;
        return new Uint8Array(view.buffer as ArrayBuffer, view.byteOffset as number, view.byteLength as number);
    }
    throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "input" argument must be an instance of ArrayBuffer, Buffer, or TypedArray. ' + received(input));
}

// Whether the bytes are well-formed UTF-8 (no overlong forms, surrogates or
// code points past U+10FFFF), as simdutf's validate_utf8 answers.
export function isUtf8(input: any): boolean {
    const b = bytesOf(input);
    const n = b.length;
    let i = 0;
    while (i < n) {
        const c = b[i];
        if (c < 0x80) {
            i++;
            continue;
        }
        let need = 0;
        let min = 0;
        let cp = 0;
        if (c >= 0xc2 && c <= 0xdf) {
            need = 1;
            min = 0x80;
            cp = c & 0x1f;
        } else if (c >= 0xe0 && c <= 0xef) {
            need = 2;
            min = 0x800;
            cp = c & 0x0f;
        } else if (c >= 0xf0 && c <= 0xf4) {
            need = 3;
            min = 0x10000;
            cp = c & 0x07;
        } else {
            return false;
        }
        if (i + need >= n) return false;
        for (let k = 1; k <= need; k++) {
            const d = b[i + k];
            if ((d & 0xc0) !== 0x80) return false;
            cp = (cp << 6) | (d & 0x3f);
        }
        if (cp < min || cp > 0x10ffff || (cp >= 0xd800 && cp <= 0xdfff)) return false;
        i += need + 1;
    }
    return true;
}

// Whether every byte is ASCII.
export function isAscii(input: any): boolean {
    const b = bytesOf(input);
    for (let i = 0; i < b.length; i++) {
        if (b[i] >= 0x80) return false;
    }
    return true;
}

// The bytes re-encoded from one encoding to another.
export function transcode(source: Uint8Array, fromEnc: BufferEncoding, toEnc: BufferEncoding): Buffer {
    return Buffer.from(Buffer.from(source).toString(fromEnc), toEnc);
}

// Deprecated (DEP0030): Buffer.allocUnsafeSlow.
export function SlowBuffer(size: number): Buffer {
    return Buffer.allocUnsafeSlow(size);
}
