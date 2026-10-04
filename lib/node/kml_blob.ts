// kml:global
// Blob — a global — as Node has it (lib/internal/blob.js): an immutable run
// of bytes with a MIME type, built from strings (UTF-8), buffers, views and
// other Blobs, read whole as text, bytes or an ArrayBuffer, or streamed part by
// part. A program that names Blob without declaring it imports this module.

import { inspect } from './internal_util_inspect';
import { NodeTypeError } from './internal_errors';

// A MIME type is kept lowercased when every character is printable ASCII
// (U+0020-U+007E), and is the empty string otherwise (the File API's rule).
function normalizeType(t: string): string {
    for (let i = 0; i < t.length; i++) {
        const c = t.charCodeAt(i);
        if (c < 0x20 || c > 0x7e) return '';
    }
    return t.toLowerCase();
}

// A part's bytes, copied: a string as UTF-8 (with `endings: 'native'`, its
// line feeds and CRLFs as the platform's end of line), a buffer or a view as
// its range, a Blob as its contents, anything else as its string.
function partBytes(p: any, native: boolean): Uint8Array {
    if (typeof p === 'string') {
        const s = native ? p.replace(/\n|\r\n/g, process.platform === 'win32' ? '\r\n' : '\n') : p;
        return new TextEncoder().encode(s);
    }
    if (p instanceof Blob) return (p as Blob).kmlBytes();
    if (p instanceof ArrayBuffer || p instanceof SharedArrayBuffer) return new Uint8Array(p).slice();
    if (ArrayBuffer.isView(p)) return new Uint8Array(p.buffer, p.byteOffset, p.byteLength).slice();
    return new TextEncoder().encode(String(p));
}

// A slice bound: a negative one counts from the end; both clamp to [0, size].
function relative(i: number, size: number): number {
    const n = Math.trunc(Number(i)) || 0;
    return n < 0 ? Math.max(size + n, 0) : Math.min(n, size);
}

export class Blob {
    #parts: Uint8Array[];
    #size: number;
    #type: string;

    constructor(blobParts: any = [], options: any = {}) {
        if (typeof blobParts !== 'object' || blobParts === null || typeof blobParts[Symbol.iterator] !== 'function') {
            throw new NodeTypeError('ERR_INVALID_ARG_TYPE', 'Value cannot be converted to sequence.');
        }
        const endings = options === null || options === undefined ? undefined : options.endings;
        if (endings !== undefined && endings !== 'transparent' && endings !== 'native') {
            throw new NodeTypeError('ERR_INVALID_ARG_VALUE', `The property 'options.endings' is invalid. Received ${inspect(endings)}`);
        }
        this.#parts = [];
        this.#size = 0;
        for (const p of blobParts) {
            // A Blob part contributes its own parts, as Node keeps them.
            const bs = p instanceof Blob ? (p as Blob).kmlParts() : [partBytes(p, endings === 'native')];
            for (const b of bs) {
                this.#parts.push(b);
                this.#size += b.length;
            }
        }
        const type = options === null || options === undefined ? undefined : options.type;
        this.#type = type === undefined ? '' : normalizeType(String(type));
    }

    get size(): number {
        return this.#size;
    }

    get type(): string {
        return this.#type;
    }

    // The parts, as they were given.
    kmlParts(): Uint8Array[] {
        return this.#parts.slice();
    }

    // The contents in one buffer (the native readers' view of a Blob).
    kmlBytes(): Uint8Array {
        const out = new Uint8Array(this.#size);
        let o = 0;
        for (const p of this.#parts) {
            out.set(p, o);
            o += p.length;
        }
        return out;
    }

    slice(start: number = 0, end: number = this.#size, contentType: string = ''): Blob {
        const s = relative(start, this.#size);
        const e = relative(end, this.#size);
        return new Blob([this.kmlBytes().slice(s, Math.max(s, e))], { type: contentType });
    }

    // Node's reader takes two microtasks per part (none for a Blob of no
    // parts) before arrayBuffer() settles; bytes() and text() are that
    // promise's `then`, one more.
    arrayBuffer(): Promise<ArrayBuffer> {
        const ab = this.kmlBytes().buffer as ArrayBuffer;
        let p: Promise<ArrayBuffer> = Promise.resolve(ab);
        for (let i = 1; i < 2 * this.#parts.length; i++) p = p.then((v) => v);
        return p;
    }

    bytes(): Promise<Uint8Array> {
        return this.arrayBuffer().then((ab) => new Uint8Array(ab));
    }

    text(): Promise<string> {
        return this.arrayBuffer().then((ab) => new TextDecoder().decode(ab));
    }

    // One chunk per part, as Node's reader yields them.
    stream(): ReadableStream<Uint8Array> {
        const parts = this.#parts.slice();
        let i = 0;
        return new ReadableStream<Uint8Array>({
            pull(controller) {
                while (i < parts.length && parts[i].length === 0) i++;
                if (i < parts.length) controller.enqueue(parts[i++].slice());
                else controller.close();
            },
        });
    }

    [inspect.custom](depth: number, options: any): string {
        if (depth < 0) return '[Blob]';
        return 'Blob ' + inspect({ size: this.#size, type: this.#type }, options);
    }

    get [Symbol.toStringTag](): string {
        return 'Blob';
    }
}
