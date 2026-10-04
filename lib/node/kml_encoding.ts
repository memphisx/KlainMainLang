// kml:global
// TextEncoder and TextDecoder — globals — as Node has them
// (lib/internal/encoding.js): the WHATWG Encoding Standard's UTF-8 encoder,
// and decoders for UTF-8, UTF-16LE/BE, x-user-defined and every single-byte
// encoding of the WHATWG list (the ISO-8859 and windows code pages, KOI8,
// IBM866, macintosh), with `fatal`, `ignoreBOM` and streaming. A program that names either without declaring
// it imports this module.

import { inspect } from './internal_util_inspect';
import { NodeRangeError, NodeTypeError } from './internal_errors';
import { kEncodingLabels, kSingleByte } from './internal_encoding_tables';

// The encodings decoded here: UTF-8, UTF-16LE/BE, x-user-defined and every
// single-byte encoding of the WHATWG list (internal_encoding_tables.ts, from
// Node's own tables). The CJK encodings are not.
const kMultiByte = ['gbk', 'gb18030', 'big5', 'euc-jp', 'euc-kr', 'iso-2022-jp', 'shift_jis'];

// The bytes of an ArrayBuffer, SharedArrayBuffer or ArrayBufferView.
function inputBytes(input: any): Uint8Array {
    if (input instanceof Uint8Array) return input as Uint8Array;
    if (input instanceof ArrayBuffer) return new Uint8Array(input as ArrayBuffer);
    if (input instanceof DataView) {
        const dv = input as DataView;
        return new Uint8Array(dv.buffer, dv.byteOffset, dv.byteLength);
    }
    if (ArrayBuffer.isView(input)) {
        const out = new Uint8Array(input.byteLength);
        __kml_native.typedBytes(input, out);
        return out;
    }
    throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "list" argument must be an instance of SharedArrayBuffer, ArrayBuffer or ArrayBufferView.');
}

function concat(a: Uint8Array, b: Uint8Array): Uint8Array {
    if (a.length === 0) return b;
    const out = new Uint8Array(a.length + b.length);
    out.set(a, 0);
    out.set(b, a.length);
    return out;
}

// The length of bytes' valid UTF-8 prefix, ending before a sequence that is
// complete-but-invalid (-1 if one is found and fatal) or incomplete at the
// end (its start is returned, the bytes after it held for the next call).
// Returns [validEnd, firstInvalid]: firstInvalid is -1 when every complete
// sequence is valid.
function utf8Scan(bytes: Uint8Array): [number, number] {
    let i = 0;
    const n = bytes.length;
    while (i < n) {
        const b = bytes[i];
        if (b < 0x80) {
            i++;
            continue;
        }
        let need = 0;
        let lower = 0x80;
        let upper = 0xbf;
        if (b >= 0xc2 && b <= 0xdf) {
            need = 1;
        } else if (b >= 0xe0 && b <= 0xef) {
            need = 2;
            if (b === 0xe0) lower = 0xa0;
            if (b === 0xed) upper = 0x9f;
        } else if (b >= 0xf0 && b <= 0xf4) {
            need = 3;
            if (b === 0xf0) lower = 0x90;
            if (b === 0xf4) upper = 0x8f;
        } else {
            return [i, i];
        }
        let j = 1;
        while (j <= need) {
            if (i + j >= n) return [i, -1]; // incomplete at the end
            const c = bytes[i + j];
            const lo = j === 1 ? lower : 0x80;
            const hi = j === 1 ? upper : 0xbf;
            if (c < lo || c > hi) return [i, i];
            j++;
        }
        i += need + 1;
    }
    return [n, -1];
}

// How many trailing bytes are the valid start of a sequence the input ends
// before completing (0–3).
function incompleteTail(bytes: Uint8Array): number {
    const n = bytes.length;
    for (let k = 1; k <= 3 && k <= n; k++) {
        const b = bytes[n - k];
        if (b < 0x80) return 0;
        if (b < 0xc0) continue; // a continuation byte: look further back
        const need = b >= 0xc2 && b <= 0xdf ? 1 : b >= 0xe0 && b <= 0xef ? 2 : b >= 0xf0 && b <= 0xf4 ? 3 : -1;
        if (need < k) return 0; // complete, or not a lead byte at all
        const [end] = utf8Scan(bytes.subarray(n - k));
        return end === 0 ? k : 0;
    }
    return 0;
}

function invalidData(encoding: string): NodeTypeError {
    return new NodeTypeError('ERR_ENCODING_INVALID_ENCODED_DATA', 'The encoded data was not valid for encoding ' + encoding);
}

export class TextEncoder {
    get encoding(): string {
        return 'utf-8';
    }

    encode(input: string = ''): Uint8Array {
        const b = Buffer.from(String(input), 'utf8');
        const out = new Uint8Array(b.length);
        out.set(b, 0);
        return out;
    }

    // Writes as many whole code points of source as fit; read counts UTF-16
    // code units, written bytes.
    encodeInto(source: string, destination: Uint8Array): { read: number; written: number } {
        const bytes = Buffer.from(String(source), 'utf8');
        let read = 0;
        let written = 0;
        let i = 0;
        while (i < bytes.length) {
            const b = bytes[i];
            const len = b < 0x80 ? 1 : b < 0xe0 ? 2 : b < 0xf0 ? 3 : 4;
            if (written + len > destination.length) break;
            for (let k = 0; k < len; k++) destination[written + k] = bytes[i + k];
            written += len;
            read += len === 4 ? 2 : 1;
            i += len;
        }
        return { read, written };
    }

    [inspect.custom](depth: number, options: any): string {
        return inspect({ encoding: 'utf-8' }, options);
    }

    get [Symbol.toStringTag](): string {
        return 'TextEncoder';
    }
}

export interface TextDecodeOptions {
    stream?: boolean;
}

export interface TextDecoderOptions {
    fatal?: boolean;
    ignoreBOM?: boolean;
}

export class TextDecoder {
    #encoding: string;
    #fatal: boolean;
    #ignoreBOM: boolean;
    #pending: Uint8Array = new Uint8Array(0);
    #bomSeen = false;

    constructor(encoding: string = 'utf-8', options: TextDecoderOptions = {}) {
        const label = String(encoding).trim().toLowerCase();
        const name = kEncodingLabels[label];
        if (name === 'replacement') {
            throw new NodeRangeError('ERR_ENCODING_NOT_SUPPORTED', 'The "replacement" encoding is not supported');
        }
        if (name === undefined || kMultiByte.indexOf(name) >= 0) {
            throw new NodeRangeError('ERR_ENCODING_NOT_SUPPORTED', 'The "' + String(encoding) + '" encoding is not supported');
        }
        this.#encoding = name;
        const o: any = options ?? {};
        this.#fatal = Boolean(o.fatal);
        this.#ignoreBOM = Boolean(o.ignoreBOM);
    }

    get encoding(): string {
        return this.#encoding;
    }

    get fatal(): boolean {
        return this.#fatal;
    }

    get ignoreBOM(): boolean {
        return this.#ignoreBOM;
    }

    decode(input?: any, options: TextDecodeOptions = {}): string {
        const stream = Boolean((options as any)?.stream);
        let bytes = input === undefined ? new Uint8Array(0) : inputBytes(input);
        bytes = concat(this.#pending, bytes);
        this.#pending = new Uint8Array(0);
        let out: string;
        const e = this.#encoding;
        if (e === 'utf-8') out = this.#utf8(bytes, stream);
        else if (e === 'utf-16le' || e === 'utf-16be') out = this.#utf16(bytes, stream, e === 'utf-16be');
        else if (e === 'x-user-defined') out = userDefined(bytes);
        else out = this.#singleByte(bytes, kSingleByte[e]);
        if (!stream) this.#bomSeen = false;
        return out;
    }

    // Drops a leading byte order mark once per stream, unless ignoreBOM: the
    // BOM's bytes at the start of the stream's first non-empty chunk.
    #stripBOM(bytes: Uint8Array): Uint8Array {
        if (this.#bomSeen || bytes.length === 0) return bytes;
        this.#bomSeen = true;
        if (this.#ignoreBOM) return bytes;
        const e = this.#encoding;
        if (e === 'utf-8' && bytes.length >= 3 && bytes[0] === 0xef && bytes[1] === 0xbb && bytes[2] === 0xbf) return bytes.subarray(3);
        if (e === 'utf-16le' && bytes.length >= 2 && bytes[0] === 0xff && bytes[1] === 0xfe) return bytes.subarray(2);
        if (e === 'utf-16be' && bytes.length >= 2 && bytes[0] === 0xfe && bytes[1] === 0xff) return bytes.subarray(2);
        return bytes;
    }

    #utf8(bytes: Uint8Array, stream: boolean): string {
        let body = bytes;
        if (stream) {
            // An incomplete final sequence waits for the next chunk.
            const t = incompleteTail(bytes);
            if (t > 0) {
                this.#pending = bytes.slice(bytes.length - t);
                body = bytes.subarray(0, bytes.length - t);
            }
        }
        if (this.#fatal) {
            const [end, bad] = utf8Scan(body);
            if (bad >= 0 || end < body.length) {
                this.#bomSeen = false;
                this.#pending = new Uint8Array(0);
                throw invalidData('utf-8');
            }
        }
        return Buffer.from(this.#stripBOM(body)).toString('utf8');
    }

    #utf16(input: Uint8Array, stream: boolean, bigEndian: boolean): string {
        const bytes = this.#stripBOM(input);
        let n = bytes.length;
        let s = '';
        let i = 0;
        const unitAt = (k: number): number => bigEndian ? (bytes[k] << 8) | bytes[k + 1] : bytes[k] | (bytes[k + 1] << 8);
        while (i + 1 < n) {
            const u = unitAt(i);
            if (u >= 0xd800 && u <= 0xdbff) {
                if (i + 3 < n) {
                    const v = unitAt(i + 2);
                    if (v >= 0xdc00 && v <= 0xdfff) {
                        s += String.fromCodePoint(0x10000 + ((u - 0xd800) << 10) + (v - 0xdc00));
                        i += 4;
                        continue;
                    }
                } else if (stream) {
                    break; // a high surrogate waiting for its pair
                }
                if (this.#fatal) throw invalidData(this.#encoding);
                s += '�';
                i += 2;
                continue;
            }
            if (u >= 0xdc00 && u <= 0xdfff) {
                if (this.#fatal) throw invalidData(this.#encoding);
                s += '�';
                i += 2;
                continue;
            }
            s += String.fromCodePoint(u);
            i += 2;
        }
        if (i < n) {
            if (stream) {
                this.#pending = bytes.slice(i);
            } else {
                if (this.#fatal) throw invalidData(this.#encoding);
                s += '�';
            }
        }
        return s;
    }

    // A single-byte encoding: each byte through its table; an unmapped one is
    // U+FFFD, or an error when fatal.
    #singleByte(bytes: Uint8Array, table: number[]): string {
        let s = '';
        for (let i = 0; i < bytes.length; i++) {
            const b = bytes[i];
            const cp = b < 0x80 ? b : table[b - 0x80];
            if (cp < 0) {
                if (this.#fatal) throw invalidData(this.#encoding);
                s += '\ufffd';
            } else {
                s += String.fromCodePoint(cp);
            }
        }
        return s;
    }

    [inspect.custom](depth: number, options: any): string {
        return 'TextDecoder ' + inspect({ encoding: this.#encoding, fatal: this.#fatal, ignoreBOM: this.#ignoreBOM }, options);
    }

    get [Symbol.toStringTag](): string {
        return 'TextDecoder';
    }
}

// x-user-defined: 0x80-0xFF are U+F780-U+F7FF.
function userDefined(bytes: Uint8Array): string {
    let s = '';
    for (let i = 0; i < bytes.length; i++) {
        const b = bytes[i];
        s += String.fromCodePoint(b < 0x80 ? b : 0xf700 + b);
    }
    return s;
}
