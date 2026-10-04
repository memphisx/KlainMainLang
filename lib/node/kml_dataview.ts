// kml:global
// DataView — a global — as V8 has it: get/set of every integer width, the
// float widths (16, 32, 64) and the 64-bit bigints at any byte offset of an
// ArrayBuffer or SharedArrayBuffer range, big-endian unless asked otherwise.
// A program that names DataView without declaring it imports this module.

import { inspect } from './internal_util_inspect';

// One 8-byte scratch area seen as every element type: a value is moved
// through it byte by byte, in the order the call's endianness asks for.
const kScratch = new ArrayBuffer(8);
const kU8 = new Uint8Array(kScratch);
const kF32 = new Float32Array(kScratch);
const kF64 = new Float64Array(kScratch);
const kI64 = new BigInt64Array(kScratch);
const kU64 = new BigUint64Array(kScratch);

// ToIndex's integer part (ToIntegerOrInfinity): NaN and -0 are +0, the
// rest truncate toward zero.
function toIndex(v: number): number {
    const n = Math.trunc(Number(v));
    return Number.isNaN(n) || n === 0 ? 0 : n;
}

// Round half to even.
function roundEven(x: number): number {
    const r = Math.round(x);
    return Math.abs(x % 1) === 0.5 ? 2 * Math.round(x / 2) : r;
}

// An IEEE 754 binary16's bits as a number.
function fromHalf(h: number): number {
    const sign = h & 0x8000 ? -1 : 1;
    const exp = (h >> 10) & 0x1f;
    const frac = h & 0x3ff;
    if (exp === 0) return sign * frac * Math.pow(2, -24);
    if (exp === 0x1f) return frac ? NaN : sign * Infinity;
    return sign * (1 + frac / 1024) * Math.pow(2, exp - 15);
}

// A number rounded to the nearest binary16, as its bits.
function toHalf(v: number): number {
    if (Number.isNaN(v)) return 0x7e00;
    const sign = v < 0 || Object.is(v, -0) ? 0x8000 : 0;
    const a = Math.abs(v);
    if (a === Infinity) return sign | 0x7c00;
    if (a < Math.pow(2, -14)) return sign | roundEven(a / Math.pow(2, -24));
    let e = Math.floor(Math.log2(a));
    if (Math.pow(2, e) > a) e--;
    if (Math.pow(2, e + 1) <= a) e++;
    let m = roundEven((a / Math.pow(2, e) - 1) * 1024);
    if (m === 1024) {
        m = 0;
        e++;
    }
    if (e > 15) return sign | 0x7c00;
    return sign | ((e + 15) << 10) | m;
}

export class DataView {
    readonly buffer: ArrayBuffer;
    readonly byteOffset: number;
    readonly byteLength: number;
    #u8: Uint8Array;

    // byteLength is any: ToIndex takes the value as given, so null is 0
    // while undefined means the rest of the buffer.
    constructor(buffer: ArrayBuffer, byteOffset: number = 0, byteLength?: any) {
        if (!(buffer instanceof ArrayBuffer) && !((buffer as any) instanceof SharedArrayBuffer)) {
            throw new TypeError('First argument to DataView constructor must be an ArrayBuffer');
        }
        const off = toIndex(byteOffset);
        if (off < 0 || off > buffer.byteLength) {
            throw new RangeError(`Start offset ${off} is outside the bounds of the buffer`);
        }
        const len = byteLength === undefined ? buffer.byteLength - off : toIndex(byteLength);
        if (len < 0 || off + len > buffer.byteLength) {
            throw new RangeError(`Invalid DataView length ${len}`);
        }
        this.buffer = buffer;
        this.byteOffset = off;
        this.byteLength = len;
        this.#u8 = new Uint8Array(buffer, off, len);
    }

    // The checked index of a size-byte access at offset.
    #at(offset: number, size: number): number {
        const i = toIndex(offset);
        if (i < 0 || i + size > this.byteLength) {
            throw new RangeError('Offset is outside the bounds of the DataView');
        }
        return i;
    }

    // Copies size bytes at offset into the scratch area, little-endian first.
    #load(offset: number, size: number, little: boolean): void {
        const i = this.#at(offset, size);
        for (let k = 0; k < size; k++) kU8[k] = this.#u8[i + (little ? k : size - 1 - k)];
    }

    // Copies size bytes of the scratch area to offset.
    #store(offset: number, size: number, little: boolean): void {
        const i = this.#at(offset, size);
        for (let k = 0; k < size; k++) this.#u8[i + (little ? k : size - 1 - k)] = kU8[k];
    }

    getInt8(byteOffset: number = 0): number {
        const v = this.#u8[this.#at(byteOffset, 1)];
        return v > 127 ? v - 256 : v;
    }
    getUint8(byteOffset: number = 0): number {
        return this.#u8[this.#at(byteOffset, 1)];
    }
    getUint16(byteOffset: number = 0, littleEndian: boolean = false): number {
        this.#load(byteOffset, 2, littleEndian);
        return kU8[0] | (kU8[1] << 8);
    }
    getInt16(byteOffset: number = 0, littleEndian: boolean = false): number {
        const v = this.getUint16(byteOffset, littleEndian);
        return v > 32767 ? v - 65536 : v;
    }
    getUint32(byteOffset: number = 0, littleEndian: boolean = false): number {
        this.#load(byteOffset, 4, littleEndian);
        return (kU8[0] | (kU8[1] << 8) | (kU8[2] << 16) | (kU8[3] << 24)) >>> 0;
    }
    getInt32(byteOffset: number = 0, littleEndian: boolean = false): number {
        return this.getUint32(byteOffset, littleEndian) | 0;
    }
    getFloat16(byteOffset: number = 0, littleEndian: boolean = false): number {
        return fromHalf(this.getUint16(byteOffset, littleEndian));
    }
    getFloat32(byteOffset: number = 0, littleEndian: boolean = false): number {
        this.#load(byteOffset, 4, littleEndian);
        return kF32[0];
    }
    getFloat64(byteOffset: number = 0, littleEndian: boolean = false): number {
        this.#load(byteOffset, 8, littleEndian);
        return kF64[0];
    }
    getBigInt64(byteOffset: number = 0, littleEndian: boolean = false): bigint {
        this.#load(byteOffset, 8, littleEndian);
        return kI64[0];
    }
    getBigUint64(byteOffset: number = 0, littleEndian: boolean = false): bigint {
        this.#load(byteOffset, 8, littleEndian);
        return kU64[0];
    }

    setInt8(byteOffset: number, value: number): void {
        this.#u8[this.#at(byteOffset, 1)] = value;
    }
    setUint8(byteOffset: number, value: number): void {
        this.#u8[this.#at(byteOffset, 1)] = value;
    }
    setUint16(byteOffset: number, value: number, littleEndian: boolean = false): void {
        kU8[0] = value & 255;
        kU8[1] = (value >> 8) & 255;
        this.#store(byteOffset, 2, littleEndian);
    }
    setInt16(byteOffset: number, value: number, littleEndian: boolean = false): void {
        this.setUint16(byteOffset, value, littleEndian);
    }
    setUint32(byteOffset: number, value: number, littleEndian: boolean = false): void {
        kU8[0] = value & 255;
        kU8[1] = (value >> 8) & 255;
        kU8[2] = (value >> 16) & 255;
        kU8[3] = (value >>> 24) & 255;
        this.#store(byteOffset, 4, littleEndian);
    }
    setInt32(byteOffset: number, value: number, littleEndian: boolean = false): void {
        this.setUint32(byteOffset, value, littleEndian);
    }
    setFloat16(byteOffset: number, value: number, littleEndian: boolean = false): void {
        this.setUint16(byteOffset, toHalf(value), littleEndian);
    }
    setFloat32(byteOffset: number, value: number, littleEndian: boolean = false): void {
        kF32[0] = value;
        this.#store(byteOffset, 4, littleEndian);
    }
    setFloat64(byteOffset: number, value: number, littleEndian: boolean = false): void {
        kF64[0] = value;
        this.#store(byteOffset, 8, littleEndian);
    }
    setBigInt64(byteOffset: number, value: bigint, littleEndian: boolean = false): void {
        kI64[0] = value;
        this.#store(byteOffset, 8, littleEndian);
    }
    setBigUint64(byteOffset: number, value: bigint, littleEndian: boolean = false): void {
        kU64[0] = value;
        this.#store(byteOffset, 8, littleEndian);
    }

    // V8's rendering: the three internal slots in brackets, on one line when
    // they fit in 80 columns (util.inspect's breakLength), else one per line.
    [inspect.custom](depth: number, options: any): string {
        if (depth < 0) return '[DataView]';
        const entries = [
            `[byteLength]: ${this.byteLength}`,
            `[byteOffset]: ${this.byteOffset}`,
            `[buffer]: ${inspect(this.buffer, { ...options, depth: depth - 1 })}`,
        ];
        let total = entries.length + 'DataView'.length;
        let multiline = false;
        for (const e of entries) {
            total += e.length;
            if (e.includes('\n')) multiline = true;
        }
        if (!multiline && total <= 80) return `DataView { ${entries.join(', ')} }`;
        return `DataView {\n  ${entries.map((e) => e.split('\n').join('\n  ')).join(',\n  ')}\n}`;
    }

    get [Symbol.toStringTag](): string {
        return 'DataView';
    }
}
