// Node's `crypto` (lib/crypto.js and lib/internal/crypto/*): Hash, Hmac,
// Cipheriv/Decipheriv, Sign/Verify, the KDFs, random values and key-pair
// generation, over libcrypto handles (cryptosrc/crypto_openssl.c). A pooled
// operation runs on the thread pool and calls back on the loop thread, as
// Node's crypto jobs do. The Web Crypto API is the global `crypto`.
// kml:default-namespace — `import crypto from 'crypto'` reads this module's
// exports.
import { Transform } from 'stream';
import type { TransformOptions } from 'stream';
import { NodeError, NodeTypeError, NodeRangeError } from './internal_errors';

export type BinaryLike = string | NodeJS.ArrayBufferView;
export type BinaryToTextEncoding = 'base64' | 'base64url' | 'hex' | 'binary';
export type CharacterEncoding = 'utf8' | 'utf-8' | 'utf16le' | 'utf-16le' | 'latin1';
export type Encoding = BinaryToTextEncoding | CharacterEncoding;
export type KeyFormat = 'pem' | 'der' | 'jwk';
export type UUID = `${string}-${string}-${string}-${string}-${string}`;
export type CipherKey = BinaryLike;

function received(value: any): string {
    if (value === null) return 'Received null';
    if (value === undefined) return 'Received undefined';
    if (typeof value === 'function') return 'Received function ' + (value.name || '<anonymous>');
    if (typeof value === 'object') {
        const ctor = value.constructor;
        if (ctor !== undefined && ctor !== null && typeof ctor.name === 'string' && ctor.name !== '') return 'Received an instance of ' + ctor.name;
        return 'Received ' + String(value);
    }
    let shown = String(value);
    if (typeof value === 'string') {
        if (shown.length > 28) shown = shown.slice(0, 25) + '...';
        shown = "'" + shown + "'";
    }
    return 'Received type ' + typeof value + ' (' + shown + ')';
}

function invalidArgType(name: string, expected: string, value: any): NodeTypeError {
    return new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "' + name + '" argument must be ' + expected + '. ' + received(value));
}

function outOfRange(name: string, range: string, value: any): NodeRangeError {
    return new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "' + name + '" is out of range. It must be ' + range + '. Received ' + String(value));
}

function validateString(value: any, name: string): void {
    if (typeof value !== 'string') throw invalidArgType(name, 'of type string', value);
}

function validateFunction(value: any, name: string): void {
    if (typeof value !== 'function') throw invalidArgType(name, 'of type function', value);
}

function validateInt32(value: any, name: string, min: number, max: number): void {
    if (typeof value !== 'number') throw invalidArgType(name, 'of type number', value);
    if (!Number.isInteger(value)) throw outOfRange(name, 'an integer', value);
    if (value < min || value > max) throw outOfRange(name, '>= ' + min + ' && <= ' + max, value);
}

function isView(v: any): boolean {
    return ArrayBuffer.isView(v);
}

// A view's bytes, Uint8Array-shaped (a copy for any other view).
function viewBytes(v: any): Uint8Array {
    if (v instanceof Uint8Array) return v as Uint8Array;
    if (v instanceof DataView) {
        const dv = v as DataView;
        return new Uint8Array(dv.buffer, dv.byteOffset, dv.byteLength);
    }
    const out = Buffer.alloc(v.byteLength);
    __kml_native.typedBytes(v, out);
    return out;
}

// Node's getArrayBufferOrView: a string in its encoding, or a view's bytes.
function toBytes(data: any, name: string, encoding?: any): Uint8Array {
    if (typeof data === 'string') return Buffer.from(data as string, (encoding || 'utf8') as BufferEncoding);
    if (isView(data)) return viewBytes(data);
    if (data instanceof ArrayBuffer) return new Uint8Array(data as ArrayBuffer);
    throw invalidArgType(name, 'of type string or an instance of ArrayBuffer, Buffer, TypedArray, or DataView', data);
}

// An OpenSSL failure as Node reports it: code ERR_OSSL_<REASON>, library,
// reason.
class OpenSSLError extends Error {
    code: string;
    library: string;
    reason: string;
    constructor(code: string, message: string, library: string, reason: string) {
        super(message);
        this.code = code;
        this.library = library;
        this.reason = reason;
    }
}

// A key generation failure: Node throws the OpenSSL text in a plain Error
// (no code), or ERR_CRYPTO_INVALID_CURVE for an unknown EC curve.
function keygenError(kind: number): Error {
    const text = __kml_native.cryptoLastError();
    if (kind === 1 && text.indexOf('unknown object name') >= 0) {
        return new NodeTypeError('ERR_CRYPTO_INVALID_CURVE', 'Invalid EC curve name');
    }
    return new Error(text === '' ? 'Key generation failed' : text);
}

// crypto_util.cc's OSSL_ERROR_CODES_MAP: the libraries whose name prefixes
// the error code (ERR_OSSL_PEM_…); any other library's code is the reason's.
const kOsslLibPrefix: Record<string, string> = {
    'system library': 'SYS_', 'bignum routines': 'BN_', 'rsa routines': 'RSA_',
    'Diffie-Hellman routines': 'DH_', 'digital envelope routines': 'EVP_', 'memory buffer routines': 'BUF_',
    'object identifier routines': 'OBJ_', 'PEM routines': 'PEM_', 'dsa routines': 'DSA_',
    'x509 certificate routines': 'X509_', 'asn1 encoding routines': 'ASN1_', 'configuration file routines': 'CONF_',
    'common libcrypto routines': 'CRYPTO_', 'elliptic curve routines': 'EC_', 'SSL routines': 'SSL_',
    'BIO routines': 'BIO_', 'PKCS7 routines': 'PKCS7_', 'X509 V3 routines': 'X509V3_', 'PKCS12 routines': 'PKCS12_',
    'random number generator': 'RAND_', 'DSO support routines': 'DSO_', 'engine routines': 'ENGINE_',
    'OCSP routines': 'OCSP_', 'UI routines': 'UI_', 'ECDSA routines': 'ECDSA_', 'ECDH routines': 'ECDH_',
    'STORE routines': 'OSSL_STORE_', 'FIPS routines': 'FIPS_', 'CMS routines': 'CMS_', 'time stamp routines': 'TS_',
    'HMAC routines': 'HMAC_', 'CT routines': 'CT_', 'ASYNC routines': 'ASYNC_', 'KDF routines': 'KDF_',
    'SM2 routines': 'SM2_',
};

function opensslError(fallback: string): Error {
    const text = __kml_native.cryptoLastError();
    if (text === '') return new Error(fallback);
    const parts = text.split(':');
    const reason = parts.length > 4 ? parts.slice(4).join(':') : text;
    const library = parts.length > 2 ? parts[2] : '';
    const prefix = kOsslLibPrefix[library] ?? '';
    return new OpenSSLError('ERR_OSSL_' + prefix + reason.toUpperCase().split(' ').join('_'), text, library, reason);
}

function encodeOut(buf: Buffer, encoding: any): any {
    if (encoding && encoding !== 'buffer') return buf.toString(encoding);
    return buf;
}

// ---- Hash / Hmac (lib/internal/crypto/hash.js) ----

export interface HashOptions extends TransformOptions {
    outputLength?: number | undefined;
}

export class Hash extends Transform {
    private _kmlHandle: number;
    private _kmlFinalized = false;

    constructor(algorithm: string | Hash, options?: HashOptions) {
        super(options);
        if (algorithm instanceof Hash) {
            this._kmlHandle = __kml_native.cryptoHashCopy((algorithm as Hash)._kmlHandle);
            return;
        }
        validateString(algorithm, 'algorithm');
        const xofLen = options?.outputLength;
        if (xofLen !== undefined) validateInt32(xofLen, 'options.outputLength', 0, 4294967295);
        this._kmlHandle = __kml_native.cryptoHashNew(algorithm as string, xofLen ?? 0);
        if (this._kmlHandle < 0) throw new Error('Digest method not supported');
    }

    copy(options?: HashOptions): Hash {
        if (this._kmlFinalized) throw new NodeError('ERR_CRYPTO_HASH_FINALIZED', 'Digest already called');
        return new Hash(this, options);
    }

    update(data: BinaryLike, inputEncoding?: Encoding): Hash;
    update(data: any, inputEncoding?: any): Hash {
        if (this._kmlFinalized) throw new NodeError('ERR_CRYPTO_HASH_FINALIZED', 'Digest already called');
        if (typeof data !== 'string' && !isView(data)) {
            throw invalidArgType('data', 'of type string or an instance of Buffer, TypedArray, or DataView', data);
        }
        if (__kml_native.cryptoHashUpdate(this._kmlHandle, toBytes(data, 'data', inputEncoding)) === 0) {
            throw new NodeError('ERR_CRYPTO_HASH_UPDATE_FAILED', 'Hash update failed');
        }
        return this;
    }

    digest(): Buffer;
    digest(encoding: BinaryToTextEncoding): string;
    digest(encoding?: any): any {
        if (this._kmlFinalized) throw new NodeError('ERR_CRYPTO_HASH_FINALIZED', 'Digest already called');
        this._kmlFinalized = true;
        const out = Buffer.alloc(__kml_native.cryptoHashSize(this._kmlHandle));
        const n = __kml_native.cryptoHashDigest(this._kmlHandle, out);
        return encodeOut(n < 0 ? Buffer.alloc(0) : out.subarray(0, n), encoding);
    }

    _transform(chunk: any, encoding: BufferEncoding, callback: (error?: any) => void): void {
        __kml_native.cryptoHashUpdate(this._kmlHandle, toBytes(chunk, 'data', encoding));
        callback();
    }

    _flush(callback: (error?: any) => void): void {
        this.push(this.digest());
        callback();
    }
}

export class Hmac extends Transform {
    private _kmlHandle: number;
    private _kmlFinalized = false;

    constructor(hmac: string, key: BinaryLike, options?: TransformOptions) {
        super(options);
        validateString(hmac, 'hmac');
        const k = toBytes(key, 'key');
        this._kmlHandle = __kml_native.cryptoHmacNew(hmac, k);
        if (this._kmlHandle < 0) throw new NodeTypeError('ERR_CRYPTO_INVALID_DIGEST', 'Invalid digest: ' + hmac);
    }

    update(data: BinaryLike, inputEncoding?: Encoding): Hmac;
    update(data: any, inputEncoding?: any): Hmac {
        if (this._kmlFinalized) throw new NodeError('ERR_CRYPTO_HASH_FINALIZED', 'Digest already called');
        if (typeof data !== 'string' && !isView(data)) {
            throw invalidArgType('data', 'of type string or an instance of Buffer, TypedArray, or DataView', data);
        }
        if (__kml_native.cryptoHashUpdate(this._kmlHandle, toBytes(data, 'data', inputEncoding)) === 0) {
            throw new NodeError('ERR_CRYPTO_HASH_UPDATE_FAILED', 'Hash update failed');
        }
        return this;
    }

    digest(): Buffer;
    digest(encoding: BinaryToTextEncoding): string;
    digest(encoding?: any): any {
        // A second digest is empty, not an error (Node's Hmac).
        if (this._kmlFinalized) return encodeOut(Buffer.alloc(0), encoding);
        this._kmlFinalized = true;
        const out = Buffer.alloc(__kml_native.cryptoHashSize(this._kmlHandle));
        const n = __kml_native.cryptoHashDigest(this._kmlHandle, out);
        return encodeOut(n < 0 ? Buffer.alloc(0) : out.subarray(0, n), encoding);
    }

    _transform(chunk: any, encoding: BufferEncoding, callback: (error?: any) => void): void {
        __kml_native.cryptoHashUpdate(this._kmlHandle, toBytes(chunk, 'data', encoding));
        callback();
    }

    _flush(callback: (error?: any) => void): void {
        this.push(this.digest());
        callback();
    }
}

export function createHash(algorithm: string, options?: HashOptions): Hash {
    return new Hash(algorithm, options);
}

export function createHmac(algorithm: string, key: BinaryLike, options?: TransformOptions): Hmac {
    return new Hmac(algorithm, key, options);
}

// crypto.hash(algorithm, data[, outputEncoding]): a one-shot digest, hex by
// default.
export function hash(algorithm: string, data: BinaryLike, outputEncoding?: BinaryToTextEncoding | 'buffer'): any {
    validateString(algorithm, 'algorithm');
    if (typeof data !== 'string' && !isView(data)) {
        throw invalidArgType('data', 'of type string or an instance of Buffer, TypedArray, or DataView', data);
    }
    const h = new Hash(algorithm);
    h.update(data);
    return h.digest((outputEncoding ?? 'hex') as any);
}

// lib/internal/util.js's filterDuplicateStrings: one name per lowercase
// spelling (the last one seen), sorted.
function filterDuplicateStrings(items: string[], low: boolean): string[] {
    const map = new Map<string, string>();
    for (const item of items) {
        const key = item.toLowerCase();
        map.set(key, low ? key : item);
    }
    return Array.from(map.values()).sort();
}

export function getHashes(): string[] {
    return filterDuplicateStrings(__kml_native.cryptoNames(0).split(',').filter((n: string) => n.length > 0), false);
}

export function getCiphers(): string[] {
    return filterDuplicateStrings(__kml_native.cryptoNames(1).split(',').filter((n: string) => n.length > 0), false);
}

// getCurves: the built-in EC curves' names, sorted and deduplicated.
export function getCurves(): string[] {
    const names = __kml_native.cryptoNames(3).split(',').filter((n: string) => n.length > 0);
    const out: string[] = [];
    for (const n of names.sort()) if (out.length === 0 || out[out.length - 1] !== n) out.push(n);
    return out;
}

// ---- random values (lib/internal/crypto/random.js) ----

const kMaxLength = 2147483647;

function assertSize(size: any, name: string): number {
    if (typeof size !== 'number') throw invalidArgType(name, 'of type number', size);
    if (!Number.isInteger(size) || size < 0 || size > kMaxLength) throw outOfRange(name, '>= 0 && <= ' + kMaxLength, size);
    return size;
}

export function randomBytes(size: number): Buffer;
export function randomBytes(size: number, callback: (err: Error | null, buf: Buffer) => void): void;
export function randomBytes(size: any, callback?: any): any {
    const n = assertSize(size, 'size');
    if (callback !== undefined) validateFunction(callback, 'callback');
    const buf = Buffer.alloc(n);
    if (callback === undefined) {
        __kml_native.cryptoRandomFill(buf, 0, n);
        return buf;
    }
    const cb = callback as (err: Error | null, buf: Buffer) => void;
    __kml_native.cryptoRandomFillAsync(buf, 0, n, (status: number, unused: number) => {
        cb(status === 0 ? null : opensslError('random generation failed'), buf);
    });
}

export function randomFillSync<T extends NodeJS.ArrayBufferView>(buffer: T, offset?: number, size?: number): T;
export function randomFillSync(buffer: any, offset?: number, size?: number): any {
    if (!isView(buffer)) throw invalidArgType('buf', 'an instance of ArrayBuffer or ArrayBufferView', buffer);
    const bytes = viewBytes(buffer);
    const off = offset ?? 0;
    const len = size ?? bytes.byteLength - off;
    if (off < 0 || off > bytes.byteLength) throw outOfRange('offset', '>= 0 && <= ' + bytes.byteLength, off);
    if (len < 0 || off + len > bytes.byteLength) throw outOfRange('size + offset', '<= ' + bytes.byteLength, len + off);
    __kml_native.cryptoRandomFill(bytes, off, len);
    if (!(buffer instanceof Uint8Array) && !(buffer instanceof DataView)) __kml_native.typedBytesBack(bytes, buffer);
    return buffer;
}

export function randomFill<T extends NodeJS.ArrayBufferView>(buffer: T, callback: (err: Error | null, buf: T) => void): void;
export function randomFill<T extends NodeJS.ArrayBufferView>(buffer: T, offset: number, callback: (err: Error | null, buf: T) => void): void;
export function randomFill<T extends NodeJS.ArrayBufferView>(buffer: T, offset: number, size: number, callback: (err: Error | null, buf: T) => void): void;
export function randomFill(buffer: any, ...rest: any[]): void {
    const callback: any = rest[rest.length - 1];
    validateFunction(callback, 'callback');
    const offset: number = rest.length > 1 ? rest[0] : 0;
    const bytes = viewBytes(buffer);
    const size: number = rest.length > 2 ? rest[1] : bytes.byteLength - offset;
    __kml_native.cryptoRandomFillAsync(bytes, offset, size, (status: number, unused: number) => {
        callback(status === 0 ? null : opensslError('random generation failed'), buffer);
    });
}

// randomInt([min, ]max[, callback]): a uniform integer in [min, max), by
// rejection sampling of 48-bit values, as Node draws them.
export function randomInt(max: number): number;
export function randomInt(min: number, max: number): number;
export function randomInt(max: number, callback: (err: Error | null, value: number) => void): void;
export function randomInt(min: number, max: number, callback: (err: Error | null, value: number) => void): void;
export function randomInt(a: any, b?: any, c?: any): any {
    let min = 0;
    let max: any = a;
    let callback: any = undefined;
    if (typeof b === 'function') {
        callback = b;
    } else if (b !== undefined) {
        min = a;
        max = b;
        callback = c;
    }
    if (!Number.isSafeInteger(min)) throw invalidArgType('min', 'a safe integer', min);
    if (!Number.isSafeInteger(max)) throw invalidArgType('max', 'a safe integer', max);
    if (max <= min) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "max" is out of range. It must be greater than the value of "min" (' + min + '). Received ' + max);
    }
    const range = max - min;
    if (range > 281474976710655) {
        throw new NodeRangeError('ERR_OUT_OF_RANGE', 'The value of "max - min" is out of range. It must be <= 281474976710655. Received ' + range);
    }
    const randLimit = 281474976710656 - (281474976710656 % range);
    const draw = (): number => {
        const b6 = Buffer.alloc(6);
        while (true) {
            __kml_native.cryptoRandomFill(b6, 0, 6);
            const x = b6.readUInt16BE(0) * 4294967296 + b6.readUInt32BE(2);
            if (x < randLimit) return min + (x % range);
        }
    };
    if (callback === undefined) return draw();
    validateFunction(callback, 'callback');
    const v = draw();
    process.nextTick(() => { callback(undefined, v); });
}

export function randomUUID(options?: { disableEntropyCache?: boolean }): UUID {
    return globalThis.crypto.randomUUID() as UUID;
}

export function getRandomValues<T extends NodeJS.ArrayBufferView>(typedArray: T): T {
    return globalThis.crypto.getRandomValues(typedArray as any) as any;
}

export function timingSafeEqual(a: NodeJS.ArrayBufferView, b: NodeJS.ArrayBufferView): boolean {
    if (!isView(a)) throw invalidArgType('buf1', 'an instance of ArrayBuffer, Buffer, TypedArray, or DataView', a);
    if (!isView(b)) throw invalidArgType('buf2', 'an instance of ArrayBuffer, Buffer, TypedArray, or DataView', b);
    const x = viewBytes(a);
    const y = viewBytes(b);
    if (x.byteLength !== y.byteLength) {
        throw new NodeRangeError('ERR_CRYPTO_TIMING_SAFE_EQUAL_LENGTH', 'Input buffers must have the same byte length');
    }
    return __kml_native.cryptoTimingEqual(x, y) === 1;
}

// ---- key derivation (pbkdf2.js, scrypt.js, hkdf.js) ----

const KDF_PBKDF2 = 0;
const KDF_SCRYPT = 1;
const KDF_HKDF = 2;
const empty = Buffer.alloc(0);

function kdfStatusError(status: number, digest: string): Error {
    if (status === 2) return new NodeTypeError('ERR_CRYPTO_INVALID_DIGEST', 'Invalid digest: ' + digest);
    return opensslError('Key derivation failed');
}

function validatePbkdf2(password: any, salt: any, iterations: any, keylen: any, digest: any): [Uint8Array, Uint8Array] {
    validateString(digest, 'digest');
    const pw = toBytes(password, 'password');
    const s = toBytes(salt, 'salt');
    validateInt32(iterations, 'iterations', 1, 2147483647);
    validateInt32(keylen, 'keylen', 0, 2147483647);
    return [pw, s];
}

export function pbkdf2Sync(password: BinaryLike, salt: BinaryLike, iterations: number, keylen: number, digest: string): Buffer {
    const [pw, s] = validatePbkdf2(password, salt, iterations, keylen, digest);
    const out = Buffer.alloc(keylen);
    const st = __kml_native.cryptoKdf(KDF_PBKDF2, pw, s, empty, iterations, 0, 0, 0, digest, out);
    if (st !== 0) throw kdfStatusError(st, digest);
    return out;
}

export function pbkdf2(password: BinaryLike, salt: BinaryLike, iterations: number, keylen: number, digest: string,
                       callback: (err: Error | null, derivedKey: Buffer) => void): void {
    validateFunction(callback, 'callback');
    const [pw, s] = validatePbkdf2(password, salt, iterations, keylen, digest);
    const out = Buffer.alloc(keylen);
    __kml_native.cryptoKdfAsync(KDF_PBKDF2, pw, s, empty, iterations, 0, 0, 0, digest, out, (status: number, unused: number) => {
        if (status !== 0) callback(kdfStatusError(status, digest), undefined as any);
        else callback(null, out);
    });
}

export interface ScryptOptions {
    cost?: number | undefined;
    blockSize?: number | undefined;
    parallelization?: number | undefined;
    N?: number | undefined;
    r?: number | undefined;
    p?: number | undefined;
    maxmem?: number | undefined;
}

function scryptParams(options: any): [number, number, number, number] {
    const o: any = options ?? {};
    const N = o.N ?? o.cost ?? 16384;
    const r = o.r ?? o.blockSize ?? 8;
    const p = o.p ?? o.parallelization ?? 1;
    const maxmem = o.maxmem ?? 32 * 1024 * 1024;
    if (N < 2 || (N & (N - 1)) !== 0) {
        throw new NodeRangeError('ERR_CRYPTO_INVALID_SCRYPT_PARAMS', 'Invalid scrypt params: N must be a power of 2 greater than 1');
    }
    return [N, r, p, maxmem];
}

export function scryptSync(password: BinaryLike, salt: BinaryLike, keylen: number, options?: ScryptOptions): Buffer {
    const pw = toBytes(password, 'password');
    const s = toBytes(salt, 'salt');
    validateInt32(keylen, 'keylen', 0, 2147483647);
    const [N, r, p, maxmem] = scryptParams(options);
    const out = Buffer.alloc(keylen);
    const st = __kml_native.cryptoKdf(KDF_SCRYPT, pw, s, empty, N, r, p, maxmem, '', out);
    if (st !== 0) throw new NodeRangeError('ERR_CRYPTO_INVALID_SCRYPT_PARAMS', 'Invalid scrypt params: ' + __kml_native.cryptoLastError());
    return out;
}

export function scrypt(password: BinaryLike, salt: BinaryLike, keylen: number, callback: (err: Error | null, derivedKey: Buffer) => void): void;
export function scrypt(password: BinaryLike, salt: BinaryLike, keylen: number, options: ScryptOptions, callback: (err: Error | null, derivedKey: Buffer) => void): void;
export function scrypt(password: any, salt: any, keylen: any, options: any, callback?: any): void {
    if (typeof options === 'function') {
        callback = options;
        options = undefined;
    }
    validateFunction(callback, 'callback');
    const pw = toBytes(password, 'password');
    const s = toBytes(salt, 'salt');
    validateInt32(keylen, 'keylen', 0, 2147483647);
    const [N, r, p, maxmem] = scryptParams(options);
    const out = Buffer.alloc(keylen);
    __kml_native.cryptoKdfAsync(KDF_SCRYPT, pw, s, empty, N, r, p, maxmem, '', out, (status: number, unused: number) => {
        if (status !== 0) callback(new NodeRangeError('ERR_CRYPTO_INVALID_SCRYPT_PARAMS', 'Invalid scrypt params'), undefined);
        else callback(null, out);
    });
}

// lib/internal/crypto/hkdf.js: at most 255 blocks of the digest.
function validateHkdfLength(digest: string, keylen: number): void {
    const h = __kml_native.cryptoHashNew(digest.toLowerCase(), 0);
    if (h < 0) throw new NodeTypeError('ERR_CRYPTO_INVALID_DIGEST', 'Invalid digest: ' + digest);
    const size = __kml_native.cryptoHashSize(h);
    __kml_native.cryptoFree(h);
    if (keylen > size * 255) throw new NodeRangeError('ERR_CRYPTO_INVALID_KEYLEN', 'Invalid key length');
}

export function hkdfSync(digest: string, ikm: BinaryLike, salt: BinaryLike, info: BinaryLike, keylen: number): ArrayBuffer {
    validateString(digest, 'digest');
    const k = toBytes(ikm, 'ikm');
    const s = toBytes(salt, 'salt');
    const i = toBytes(info, 'info');
    validateInt32(keylen, 'length', 0, 2147483647);
    validateHkdfLength(digest, keylen);
    const out = Buffer.alloc(keylen);
    const st = __kml_native.cryptoKdf(KDF_HKDF, k, s, i, 0, 0, 0, 0, digest, out);
    if (st !== 0) throw kdfStatusError(st, digest);
    const ab = new ArrayBuffer(keylen);
    new Uint8Array(ab).set(out);
    return ab;
}

export function hkdf(digest: string, ikm: BinaryLike, salt: BinaryLike, info: BinaryLike, keylen: number,
                     callback: (err: Error | null, derivedKey: ArrayBuffer) => void): void {
    validateFunction(callback, 'callback');
    validateString(digest, 'digest');
    const k = toBytes(ikm, 'ikm');
    const s = toBytes(salt, 'salt');
    const i = toBytes(info, 'info');
    validateInt32(keylen, 'length', 0, 2147483647);
    validateHkdfLength(digest, keylen);
    const out = Buffer.alloc(keylen);
    __kml_native.cryptoKdfAsync(KDF_HKDF, k, s, i, 0, 0, 0, 0, digest, out, (status: number, unused: number) => {
        if (status !== 0) {
            callback(kdfStatusError(status, digest), undefined as any);
            return;
        }
        const ab = new ArrayBuffer(keylen);
        new Uint8Array(ab).set(out);
        callback(null, ab);
    });
}

// ---- Cipheriv / Decipheriv (lib/internal/crypto/cipher.js) ----

export interface CipherOptions extends TransformOptions {
    authTagLength?: number | undefined;
}

class CipherBase extends Transform {
    protected _kmlHandle: number;
    protected _kmlTag: Buffer | null = null;
    private _kmlEncrypt: boolean;
    private _kmlDone = false;
    // GCM, CCM, OCB or ChaCha20-Poly1305: the modes with an auth tag.
    protected _kmlAead: boolean;
    protected _kmlAuthTagLength: number;

    constructor(encrypt: boolean, cipher: string, key: CipherKey, iv: BinaryLike | null, options?: CipherOptions) {
        super(options);
        this._kmlEncrypt = encrypt;
        validateString(cipher, 'cipher');
        const lower = cipher.toLowerCase();
        this._kmlAead = lower.indexOf('gcm') >= 0 || lower.indexOf('ccm') >= 0 || lower.indexOf('ocb') >= 0 ||
            lower === 'chacha20-poly1305';
        this._kmlAuthTagLength = options?.authTagLength ?? 0;
        const k = toBytes(key, 'key');
        const v = iv === null ? empty : toBytes(iv, 'iv');
        this._kmlHandle = __kml_native.cryptoCipherNew(cipher, k, v, encrypt ? 1 : 0, options?.authTagLength ?? 0);
        if (this._kmlHandle === -1) throw new NodeError('ERR_CRYPTO_UNKNOWN_CIPHER', 'Unknown cipher');
        if (this._kmlHandle === -2) throw new NodeRangeError('ERR_CRYPTO_INVALID_KEYLEN', 'Invalid key length');
        if (this._kmlHandle === -3) throw new NodeTypeError('ERR_CRYPTO_INVALID_IV', 'Invalid initialization vector');
    }

    private out(bytes: Buffer, n: number, outputEncoding: any): any {
        const b = bytes.subarray(0, n < 0 ? 0 : n);
        if (outputEncoding && outputEncoding !== 'buffer') {
            // Node keeps a StringDecoder for a multi-byte output encoding.
            return b.toString(outputEncoding);
        }
        return b;
    }

    update(data: BinaryLike): Buffer;
    update(data: string, inputEncoding: Encoding): Buffer;
    update(data: NodeJS.ArrayBufferView, inputEncoding: undefined, outputEncoding: Encoding): string;
    update(data: string, inputEncoding: Encoding | undefined, outputEncoding: Encoding): string;
    update(data: any, inputEncoding?: any, outputEncoding?: any): any {
        if (this._kmlDone) throw new NodeError('ERR_CRYPTO_INVALID_STATE', 'Invalid state for operation update');
        if (typeof data !== 'string' && !isView(data)) {
            throw invalidArgType('data', 'of type string or an instance of Buffer, TypedArray, or DataView', data);
        }
        const inBytes = toBytes(data, 'data', inputEncoding);
        const outBytes = Buffer.alloc(inBytes.byteLength + __kml_native.cryptoCipherBlockSize(this._kmlHandle));
        const n = __kml_native.cryptoCipherUpdate(this._kmlHandle, inBytes, outBytes);
        if (n < 0) {
            // Node's CipherBase::Update pops the error queue before it
            // throws, so the failure is always this message.
            __kml_native.cryptoLastError();
            throw new Error('Trying to add data in unsupported state');
        }
        return this.out(outBytes, n, outputEncoding);
    }

    final(): Buffer;
    final(outputEncoding: BufferEncoding): string;
    final(outputEncoding?: any): any {
        if (this._kmlDone) throw new NodeError('ERR_CRYPTO_INVALID_STATE', 'Invalid state for operation final');
        this._kmlDone = true;
        const outBytes = Buffer.alloc(Math.max(__kml_native.cryptoCipherBlockSize(this._kmlHandle), 16));
        const n = __kml_native.cryptoCipherFinal(this._kmlHandle, outBytes);
        if (n < 0) throw opensslError('Unsupported state or unable to authenticate data');
        if (this._kmlEncrypt && this._kmlAead) {
            // An AEAD cipher's tag, for getAuthTag().
            const tag = Buffer.alloc(16);
            if (__kml_native.cryptoCipherGetTag(this._kmlHandle, tag) > 0) this._kmlTag = tag;
        }
        return this.out(outBytes, n, outputEncoding);
    }

    setAutoPadding(autoPadding?: boolean): this {
        __kml_native.cryptoCipherSetPadding(this._kmlHandle, autoPadding === false ? 0 : 1);
        return this;
    }

    setAAD(buffer: NodeJS.ArrayBufferView, options?: { plaintextLength: number }): this {
        if (!this._kmlAead || __kml_native.cryptoCipherSetAAD(this._kmlHandle, viewBytes(buffer)) === 0) {
            throw new NodeError('ERR_CRYPTO_INVALID_STATE', 'Invalid state for operation setAAD');
        }
        return this;
    }

    _transform(chunk: any, encoding: BufferEncoding, callback: (error?: any) => void): void {
        this.push(this.update(chunk, encoding as any));
        callback();
    }

    _flush(callback: (error?: any) => void): void {
        try {
            this.push(this.final());
        } catch (err) {
            callback(err);
            return;
        }
        callback();
    }
}

export class Cipheriv extends CipherBase {
    constructor(cipher: string, key: CipherKey, iv: BinaryLike | null, options?: CipherOptions) {
        super(true, cipher, key, iv, options);
    }

    getAuthTag(): Buffer {
        if (this._kmlTag === null) throw new NodeError('ERR_CRYPTO_INVALID_STATE', 'Invalid state for operation getAuthTag');
        return this._kmlTag;
    }
}

// node_crypto's once-per-process DEP0182.
let shortGcmTagWarned = false;

export class Decipheriv extends CipherBase {
    private _kmlAlgorithm: string;

    constructor(decipher: string, key: CipherKey, iv: BinaryLike | null, options?: CipherOptions) {
        super(false, decipher, key, iv, options);
        this._kmlAlgorithm = decipher.toLowerCase();
    }

    setAuthTag(buffer: NodeJS.ArrayBufferView): this {
        const tag = viewBytes(buffer);
        if (!this._kmlAead || __kml_native.cryptoCipherSetTag(this._kmlHandle, tag) === 0) {
            throw new NodeError('ERR_CRYPTO_INVALID_STATE', 'Invalid state for operation setAuthTag');
        }
        if (tag.byteLength < 16 && this._kmlAuthTagLength === 0 && !shortGcmTagWarned &&
            this._kmlAlgorithm.indexOf('gcm') >= 0) {
            shortGcmTagWarned = true;
            process.emitWarning('Using AES-GCM authentication tags of less than 128 bits without specifying the ' +
                'authTagLength option when initializing decryption is deprecated.', 'DeprecationWarning', 'DEP0182');
        }
        return this;
    }
}

export function createCipheriv(algorithm: string, key: CipherKey, iv: BinaryLike | null, options?: CipherOptions): Cipheriv {
    return new Cipheriv(algorithm, key, iv, options);
}

export function createDecipheriv(algorithm: string, key: CipherKey, iv: BinaryLike | null, options?: CipherOptions): Decipheriv {
    return new Decipheriv(algorithm, key, iv, options);
}

// ---- KeyObject (lib/internal/crypto/keys.js) ----

// A key's material: a secret key's bytes, or an asymmetric key's canonical
// PEM (PKCS#8 private, SPKI public), which the natives read.
class KeyObjectHandle {
    bytes: Buffer;
    pem: string;
    constructor(bytes: Buffer, pem: string) {
        this.bytes = bytes;
        this.pem = pem;
    }
}

export interface AsymmetricKeyDetails {
    modulusLength?: number | undefined;
    publicExponent?: bigint | undefined;
    hashAlgorithm?: string | undefined;
    mgf1HashAlgorithm?: string | undefined;
    saltLength?: number | undefined;
    divisorLength?: number | undefined;
    namedCurve?: string | undefined;
}

export type KeyObjectType = 'secret' | 'public' | 'private';
export type KeyType = 'rsa' | 'rsa-pss' | 'dsa' | 'ec' | 'ed25519' | 'ed448' | 'x25519' | 'x448';

export interface KeyExportOptions<T extends KeyFormat> {
    type: 'pkcs1' | 'spki' | 'pkcs8' | 'sec1';
    format: T;
    cipher?: string | undefined;
    passphrase?: string | Buffer | undefined;
}

export interface JwkKeyExportOptions {
    format: 'jwk';
}

function invalidArgValue(name: string, value: any, reason?: string): NodeTypeError {
    const shown = typeof value === 'string' ? "'" + value + "'" : String(value);
    const kind = name.indexOf('.') >= 0 ? 'property' : 'argument';
    return new NodeTypeError('ERR_INVALID_ARG_VALUE', 'The ' + kind + " '" + name + "' " + (reason ?? 'is invalid') + '. Received ' + shown);
}

function incompatibleKeyOptions(encoding: string, message: string): NodeError {
    return new NodeError('ERR_CRYPTO_INCOMPATIBLE_KEY_OPTIONS', 'The selected key encoding ' + encoding + ' ' + message + '.');
}

function validateObject(value: any, name: string): void {
    if (value === null || typeof value !== 'object' || Array.isArray(value)) throw invalidArgType(name, 'of type object', value);
}

const kFormats: { [k: string]: number } = { pem: 0, der: 1 };
const kEncodings: { [k: string]: number } = { pkcs1: 1, spki: 2, pkcs8: 3, sec1: 4 };

export class KeyObject {
    #type: KeyObjectType;
    #handle: KeyObjectHandle;

    constructor(type: KeyObjectType, handle: any) {
        if (type !== 'secret' && type !== 'public' && type !== 'private') throw invalidArgValue('type', type);
        if (!(handle instanceof KeyObjectHandle)) throw invalidArgType('handle', 'of type object', handle);
        this.#type = type;
        this.#handle = handle as KeyObjectHandle;
    }

    get type(): KeyObjectType {
        return this.#type;
    }

    // The key's material, for this module (Node's kHandle).
    _kmlHandle(): KeyObjectHandle {
        return this.#handle;
    }

    get symmetricKeySize(): number | undefined {
        return undefined;
    }

    get asymmetricKeyType(): KeyType | undefined {
        return undefined;
    }

    get asymmetricKeyDetails(): AsymmetricKeyDetails | undefined {
        return undefined;
    }

    get [Symbol.toStringTag](): string {
        return 'KeyObject';
    }

    static from(key: any): KeyObject {
        // A CryptoKey (lib/node/internal_crypto_webcrypto_keys.ts) holds its
        // KeyObject's material.
        if (key !== null && typeof key === 'object' && Object.prototype.toString.call(key) === '[object CryptoKey]' &&
            typeof key._kmlKeyObject === 'function') {
            return key._kmlKeyObject();
        }
        throw invalidArgType('key', 'an instance of CryptoKey', key);
    }

    equals(otherKeyObject: KeyObject): boolean;
    equals(otherKeyObject: any): boolean {
        if (!(otherKeyObject instanceof KeyObject)) throw invalidArgType('otherKeyObject', 'an instance of KeyObject', otherKeyObject);
        const other = otherKeyObject as KeyObject;
        if (other.#type !== this.#type) return false;
        if (this.#type === 'secret') return this.#handle.bytes.equals(other.#handle.bytes);
        return this.#handle.pem === other.#handle.pem;
    }

    export(options: KeyExportOptions<'pem'>): string | Buffer;
    export(options?: KeyExportOptions<'der'>): Buffer;
    export(options?: JwkKeyExportOptions): JsonWebKey;
    export(options?: any): any {
        throw new NodeError('ERR_METHOD_NOT_IMPLEMENTED', 'The export() method is not implemented');
    }
}

function keyHandle(key: KeyObject): KeyObjectHandle {
    return key._kmlHandle();
}

class SecretKeyObject extends KeyObject {
    constructor(handle: KeyObjectHandle) {
        super('secret', handle);
    }

    get symmetricKeySize(): number | undefined {
        return this._kmlHandle().bytes.length;
    }

    export(options: KeyExportOptions<'pem'>): string | Buffer;
    export(options?: KeyExportOptions<'der'>): Buffer;
    export(options?: JwkKeyExportOptions): JsonWebKey;
    export(options?: any): any {
        if (options !== undefined) {
            validateObject(options, 'options');
            const format = options.format;
            if (format !== undefined && format !== 'buffer' && format !== 'jwk') {
                throw invalidArgValue('options.format', format, "must be one of: undefined, 'buffer', 'jwk'");
            }
            if (format === 'jwk') return { kty: 'oct', k: this._kmlHandle().bytes.toString('base64url') };
        }
        return Buffer.from(this._kmlHandle().bytes);
    }
}

class AsymmetricKeyObject extends KeyObject {
    #info: string[] | null = null;

    constructor(type: KeyObjectType, handle: KeyObjectHandle) {
        super(type, handle);
    }

    _kmlInfo(): string[] {
        if (this.#info === null) this.#info = __kml_native.cryptoKeyInfo(this._kmlHandle().pem).split('\t');
        return this.#info as string[];
    }

    get asymmetricKeyType(): KeyType | undefined {
        const t = this._kmlInfo()[0];
        return t === '' ? undefined : t as KeyType;
    }

    get asymmetricKeyDetails(): AsymmetricKeyDetails | undefined {
        const [type, bits, exp, curve, hash, mgf1, salt, div] = this._kmlInfo();
        switch (type) {
            case 'rsa':
                return { modulusLength: Number(bits), publicExponent: BigInt(exp) };
            case 'rsa-pss': {
                const d: AsymmetricKeyDetails = { modulusLength: Number(bits), publicExponent: BigInt(exp) };
                if (hash !== '') {
                    d.hashAlgorithm = hash;
                    d.mgf1HashAlgorithm = mgf1;
                    d.saltLength = Number(salt);
                }
                return d;
            }
            case 'dsa':
                return { modulusLength: Number(bits), divisorLength: Number(div) };
            case 'ec':
                return { namedCurve: curve };
        }
        return {};
    }

    // export() for this key's own type: a JWK, or a PEM/DER encoding.
    _kmlExport(options: any, isPrivate: boolean): any {
        if (options !== undefined && options !== null && typeof options === 'object' && options.format === 'jwk') {
            const text = __kml_native.cryptoKeyJwk(this._kmlHandle().pem, isPrivate ? 1 : 0);
            if (text === '!curve') throw new NodeError('ERR_CRYPTO_JWK_UNSUPPORTED_CURVE', 'Unsupported JWK EC curve: ' + this.asymmetricKeyDetails!.namedCurve + '.');
            if (text === '!type' || text === '') throw new NodeError('ERR_CRYPTO_JWK_UNSUPPORTED_KEY_TYPE', 'Unsupported JWK Key Type.');
            const jwk: any = {};
            for (const member of text.split('\t')) {
                const at = member.indexOf('=');
                jwk[member.slice(0, at)] = member.slice(at + 1);
            }
            return jwk;
        }
        validateObject(options, 'options');
        const format = options.format;
        if (format !== 'pem' && format !== 'der') throw invalidArgValue('options.format', format);
        const type = options.type;
        const allowed = isPrivate ? ['pkcs1', 'pkcs8', 'sec1'] : ['pkcs1', 'spki'];
        if (typeof type !== 'string' || allowed.indexOf(type) < 0) throw invalidArgValue('options.type', type);
        const keyType = this.asymmetricKeyType;
        if (type === 'pkcs1' && keyType !== 'rsa') throw incompatibleKeyOptions('pkcs1', 'can only be used for RSA keys');
        if (type === 'sec1' && keyType !== 'ec') throw incompatibleKeyOptions('sec1', 'can only be used for EC keys');
        let cipher = '';
        let pass: Uint8Array = empty;
        let hasPass = 0;
        if (isPrivate && options.cipher !== undefined) {
            if (typeof options.cipher !== 'string') throw invalidArgValue('options.cipher', options.cipher);
            if (format === 'der' && type !== 'pkcs8') throw incompatibleKeyOptions(type, 'does not support encryption');
            if (options.passphrase === undefined) throw invalidArgValue('options.passphrase', options.passphrase);
            cipher = options.cipher;
            pass = toBytes(options.passphrase, 'options.passphrase');
            hasPass = 1;
        }
        let out = Buffer.alloc(4096);
        let n = __kml_native.cryptoKeyExport(this._kmlHandle().pem, isPrivate ? 1 : 0, kFormats[format], kEncodings[type], cipher, pass, hasPass, out);
        if (n <= -2) {
            out = Buffer.alloc(-n - 2);
            n = __kml_native.cryptoKeyExport(this._kmlHandle().pem, isPrivate ? 1 : 0, kFormats[format], kEncodings[type], cipher, pass, hasPass, out);
        }
        if (n < 0) {
            if (cipher !== '' && getCiphers().indexOf(cipher.toLowerCase()) < 0) throw new NodeTypeError('ERR_CRYPTO_UNKNOWN_CIPHER', 'Unknown cipher');
            throw opensslError('Failed to encode key');
        }
        const bytes = out.subarray(0, n);
        return format === 'pem' ? bytes.toString() : Buffer.from(bytes);
    }
}

class PublicKeyObject extends AsymmetricKeyObject {
    constructor(handle: KeyObjectHandle) {
        super('public', handle);
    }

    export(options: KeyExportOptions<'pem'>): string | Buffer;
    export(options?: KeyExportOptions<'der'>): Buffer;
    export(options?: JwkKeyExportOptions): JsonWebKey;
    export(options?: any): any {
        return this._kmlExport(options, false);
    }
}

class PrivateKeyObject extends AsymmetricKeyObject {
    constructor(handle: KeyObjectHandle) {
        super('private', handle);
    }

    export(options: KeyExportOptions<'pem'>): string | Buffer;
    export(options?: KeyExportOptions<'der'>): Buffer;
    export(options?: JwkKeyExportOptions): JsonWebKey;
    export(options?: any): any {
        return this._kmlExport(options, true);
    }
}

function publicKeyFromPem(pem: string): PublicKeyObject {
    return new PublicKeyObject(new KeyObjectHandle(empty, pem));
}

function privateKeyFromPem(pem: string): PrivateKeyObject {
    return new PrivateKeyObject(new KeyObjectHandle(empty, pem));
}

function isStringOrView(v: any): boolean {
    return typeof v === 'string' || isView(v) || v instanceof ArrayBuffer;
}

// A JWK's key (getKeyObjectHandleFromJwk): its canonical PEM.
function pemFromJwk(jwk: any, wantPrivate: boolean): string {
    validateObject(jwk, 'key.key');
    const kty = jwk.kty;
    const str = (v: any, name: string): string => {
        if (typeof v !== 'string') throw invalidArgType('key.' + name, 'of type string', v);
        return v;
    };
    const opt = (v: any): string => typeof v === 'string' ? v : '';
    let pem = '';
    if (kty === 'RSA') {
        str(jwk.n, 'n');
        str(jwk.e, 'e');
        if (wantPrivate) for (const m of ['d', 'p', 'q', 'dp', 'dq', 'qi']) str(jwk[m], m);
        pem = __kml_native.cryptoKeyFromJwk(0, '', jwk.n, jwk.e, opt(jwk.d), opt(jwk.p), opt(jwk.q), opt(jwk.dp), opt(jwk.dq), opt(jwk.qi), '', '', wantPrivate ? 1 : 0);
        if (pem === '') throw new NodeTypeError('ERR_CRYPTO_INVALID_JWK', 'Invalid JWK RSA key');
    } else if (kty === 'EC') {
        if (['P-256', 'secp256k1', 'P-384', 'P-521'].indexOf(jwk.crv) < 0) throw invalidArgValue('key.crv', jwk.crv, "must be one of: 'P-256', 'secp256k1', 'P-384', 'P-521'");
        str(jwk.x, 'x');
        str(jwk.y, 'y');
        if (wantPrivate) str(jwk.d, 'd');
        pem = __kml_native.cryptoKeyFromJwk(1, jwk.crv, '', '', opt(jwk.d), '', '', '', '', '', jwk.x, jwk.y, wantPrivate ? 1 : 0);
        if (pem === '') throw new NodeTypeError('ERR_CRYPTO_INVALID_JWK', 'Invalid JWK EC key');
    } else if (kty === 'OKP') {
        if (['Ed25519', 'Ed448', 'X25519', 'X448'].indexOf(jwk.crv) < 0) throw invalidArgValue('key.crv', jwk.crv, "must be one of: 'Ed25519', 'Ed448', 'X25519', 'X448'");
        str(jwk.x, 'x');
        if (wantPrivate) str(jwk.d, 'd');
        pem = __kml_native.cryptoKeyFromJwk(2, jwk.crv, '', '', opt(jwk.d), '', '', '', '', '', jwk.x, '', wantPrivate ? 1 : 0);
        if (pem === '') throw new NodeTypeError('ERR_CRYPTO_INVALID_JWK', 'Invalid JWK OKP key');
    } else {
        throw new NodeTypeError('ERR_CRYPTO_INVALID_JWK', String(kty) + ' is not a supported JWK key type');
    }
    return pem;
}

// prepareAsymmetricKey for createPrivateKey/createPublicKey and the sign
// paths: the key's canonical PEM.
function asymmetricPem(key: any, wantPrivate: boolean, name: string): string {
    const expected = 'of type string or an instance of ArrayBuffer, Buffer, TypedArray, or DataView';
    if (isStringOrView(key)) return parsePem(toBytes(key, name), 0, 0, wantPrivate, empty, 0);
    if (key === null || typeof key !== 'object') throw invalidArgType(name, expected, key);
    if (key instanceof KeyObject) throw invalidArgType(name, expected, key);
    const data = key.key;
    if (data instanceof KeyObject) throw invalidArgType(name + '.key', expected, data);
    const format = key.format ?? 'pem';
    if (format === 'jwk') return pemFromJwk(data, wantPrivate);
    if (format !== 'pem' && format !== 'der') throw invalidArgValue(name + '.format', format);
    if (!isStringOrView(data)) throw invalidArgType(name + '.key', expected, data);
    let type = 0;
    if (format === 'der') {
        const allowed = wantPrivate ? ['pkcs1', 'pkcs8', 'sec1'] : ['pkcs1', 'spki', 'pkcs8', 'sec1'];
        if (typeof key.type !== 'string' || allowed.indexOf(key.type) < 0) throw invalidArgValue(name + '.type', key.type);
        type = kEncodings[key.type];
    }
    let pass: Uint8Array = empty;
    let hasPass = 0;
    if (key.passphrase !== undefined && key.passphrase !== null) {
        pass = toBytes(key.passphrase, name + '.passphrase');
        hasPass = 1;
    }
    const bytes = typeof data === 'string' ? Buffer.from(data as string, (key.encoding || 'utf8') as BufferEncoding) : toBytes(data, name + '.key');
    return parsePem(bytes, kFormats[format], type, wantPrivate, pass, hasPass);
}

function parsePem(bytes: Uint8Array, format: number, type: number, wantPrivate: boolean, pass: Uint8Array, hasPass: number): string {
    const pem = __kml_native.cryptoKeyParse(bytes, format, type, wantPrivate ? 1 : 0, pass, hasPass);
    if (pem === '') throw opensslError('Failed to read asymmetric key');
    return pem;
}

export type KeyLike = string | Buffer | KeyObject;

export interface PrivateKeyInput {
    key: string | Buffer;
    format?: KeyFormat | undefined;
    type?: 'pkcs1' | 'pkcs8' | 'sec1' | undefined;
    passphrase?: string | Buffer | undefined;
    encoding?: string | undefined;
}

export interface PublicKeyInput {
    key: string | Buffer;
    format?: KeyFormat | undefined;
    type?: 'pkcs1' | 'spki' | undefined;
    encoding?: string | undefined;
}

export interface JsonWebKeyInput {
    key: JsonWebKey;
    format: 'jwk';
}

export function createSecretKey(key: NodeJS.ArrayBufferView | ArrayBuffer): KeyObject;
export function createSecretKey(key: string, encoding: BufferEncoding): KeyObject;
export function createSecretKey(key: any, encoding?: any): KeyObject {
    let bytes: Uint8Array;
    if (typeof key === 'string') bytes = Buffer.from(key as string, (encoding || 'utf8') as BufferEncoding);
    else if (isView(key)) bytes = viewBytes(key);
    else if (key instanceof ArrayBuffer) bytes = new Uint8Array(key as ArrayBuffer);
    else throw invalidArgType('key', 'an instance of ArrayBuffer, Buffer, TypedArray, or DataView', key);
    return new SecretKeyObject(new KeyObjectHandle(Buffer.from(bytes), ''));
}

export function createPrivateKey(key: PrivateKeyInput | string | Buffer | JsonWebKeyInput): KeyObject {
    return privateKeyFromPem(asymmetricPem(key, true, 'key'));
}

export function createPublicKey(key: PublicKeyInput | string | Buffer | KeyObject | JsonWebKeyInput): KeyObject {
    if (key instanceof KeyObject) {
        const k = key as KeyObject;
        if (k.type !== 'private') throw new NodeTypeError('ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE', 'Invalid key object type ' + k.type + ', expected private.');
        return publicKeyFromPem(parsePem(Buffer.from(keyHandle(k).pem), 0, 0, false, empty, 0));
    }
    const k: any = key;
    if (k !== null && typeof k === 'object' && k.key instanceof KeyObject) return createPublicKey(k.key as KeyObject);
    return publicKeyFromPem(asymmetricPem(key, false, 'key'));
}

// ---- Sign / Verify (lib/internal/crypto/sig.js) ----

// A sign/verify key: [pem, passphrase, hasPassphrase]. A KeyObject's own
// PEM needs no passphrase.
function signKey(key: any, wantPrivate: boolean): [string, Uint8Array, number] {
    const k = key !== null && typeof key === 'object' && !(key instanceof KeyObject) && !isView(key) && key.key !== undefined ? key.key : key;
    if (k instanceof KeyObject) {
        const ko = k as KeyObject;
        if (wantPrivate && ko.type !== 'private') throw new NodeTypeError('ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE', 'Invalid key object type ' + ko.type + ', expected private.');
        if (ko.type === 'secret') throw new NodeTypeError('ERR_CRYPTO_INVALID_KEY_OBJECT_TYPE', 'Invalid key object type secret, expected private.');
        return [keyHandle(ko).pem, empty, 0];
    }
    if (typeof k === 'string') {
        if (key !== null && typeof key === 'object' && key.passphrase !== undefined) return [k as string, toBytes(key.passphrase, 'key.passphrase'), 1];
        return [k as string, empty, 0];
    }
    if (isView(k)) {
        const pem = Buffer.from(viewBytes(k)).toString();
        if (key !== null && typeof key === 'object' && key.passphrase !== undefined) return [pem, toBytes(key.passphrase, 'key.passphrase'), 1];
        return [pem, empty, 0];
    }
    throw invalidArgType('key', 'of type string or an instance of ArrayBuffer, Buffer, TypedArray, DataView, or KeyObject', key);
}

// The key's sign options: [padding, saltLength, dsaEncoding] (-1 and 1e9
// for absent; dsaEncoding 0 DER, 1 IEEE P1363).
function signOptions(key: any): [number, number, number] {
    if (key === null || typeof key !== 'object' || key instanceof KeyObject || isView(key)) return [-1, 1e9, 0];
    let padding = -1;
    let salt = 1e9;
    let dsa = 0;
    if (key.padding !== undefined) {
        if (typeof key.padding !== 'number' || !Number.isInteger(key.padding)) throw invalidArgValue('options.padding', key.padding);
        padding = key.padding;
    }
    if (key.saltLength !== undefined) {
        if (typeof key.saltLength !== 'number' || !Number.isInteger(key.saltLength)) throw invalidArgValue('options.saltLength', key.saltLength);
        salt = key.saltLength;
    }
    if (key.dsaEncoding !== undefined) {
        if (key.dsaEncoding !== 'der' && key.dsaEncoding !== 'ieee-p1363') throw invalidArgValue('options.dsaEncoding', key.dsaEncoding);
        dsa = key.dsaEncoding === 'ieee-p1363' ? 1 : 0;
    }
    return [padding, salt, dsa];
}

function signBytes(digest: string, key: any, data: Uint8Array): Buffer {
    const [pem, pass, hasPass] = signKey(key, true);
    const [padding, salt, dsa] = signOptions(key);
    let out = Buffer.alloc(512);
    let n = __kml_native.cryptoSign(digest, pem, pass, hasPass, padding, salt, dsa, data, out);
    if (n <= -2) {
        out = Buffer.alloc(-n - 2);
        n = __kml_native.cryptoSign(digest, pem, pass, hasPass, padding, salt, dsa, data, out);
    }
    if (n < 0) throw opensslError('Invalid key');
    return out.subarray(0, n);
}

function verifyBytes(digest: string, key: any, data: Uint8Array, sig: Uint8Array): boolean {
    const [pem, pass, hasPass] = signKey(key, false);
    const [padding, salt, dsa] = signOptions(key);
    const r = __kml_native.cryptoVerify(digest, pem, pass, hasPass, padding, salt, dsa, data, sig);
    if (r < 0) throw opensslError('Invalid key');
    return r === 1;
}

export class Sign extends Transform {
    private _kmlChunks: Buffer[] = [];
    private _kmlDigest: string;

    constructor(algorithm: string, options?: TransformOptions) {
        super(options);
        validateString(algorithm, 'algorithm');
        this._kmlDigest = algorithm;
    }

    update(data: BinaryLike, inputEncoding?: Encoding): this;
    update(data: any, inputEncoding?: any): this {
        this._kmlChunks.push(Buffer.from(toBytes(data, 'data', inputEncoding)));
        return this;
    }

    sign(privateKey: KeyLike): Buffer;
    sign(privateKey: KeyLike, outputFormat: BinaryToTextEncoding): string;
    sign(privateKey: any, outputFormat?: any): any {
        return encodeOut(signBytes(this._kmlDigest, privateKey, Buffer.concat(this._kmlChunks)), outputFormat);
    }

    _transform(chunk: any, encoding: BufferEncoding, callback: (error?: any) => void): void {
        this.update(chunk, encoding as any);
        callback();
    }
}

export class Verify extends Transform {
    private _kmlChunks: Buffer[] = [];
    private _kmlDigest: string;

    constructor(algorithm: string, options?: TransformOptions) {
        super(options);
        validateString(algorithm, 'algorithm');
        this._kmlDigest = algorithm;
    }

    update(data: BinaryLike, inputEncoding?: Encoding): this;
    update(data: any, inputEncoding?: any): this {
        this._kmlChunks.push(Buffer.from(toBytes(data, 'data', inputEncoding)));
        return this;
    }

    verify(object: KeyLike, signature: NodeJS.ArrayBufferView): boolean;
    verify(object: KeyLike, signature: string, signatureEncoding?: BinaryToTextEncoding): boolean;
    verify(object: any, signature: any, signatureEncoding?: any): boolean {
        const sig = typeof signature === 'string' ? Buffer.from(signature as string, (signatureEncoding || 'utf8') as BufferEncoding) : viewBytes(signature);
        return verifyBytes(this._kmlDigest, object, Buffer.concat(this._kmlChunks), sig);
    }

    _transform(chunk: any, encoding: BufferEncoding, callback: (error?: any) => void): void {
        this.update(chunk, encoding as any);
        callback();
    }
}

export function createSign(algorithm: string, options?: TransformOptions): Sign {
    return new Sign(algorithm, options);
}

export function createVerify(algorithm: string, options?: TransformOptions): Verify {
    return new Verify(algorithm, options);
}

// crypto.sign(algorithm, data, key[, callback]): the one-shot form; a null
// algorithm for a key type that fixes its own (Ed25519).
export function sign(algorithm: string | null | undefined, data: NodeJS.ArrayBufferView, key: KeyLike): Buffer;
export function sign(algorithm: string | null | undefined, data: NodeJS.ArrayBufferView, key: KeyLike, callback: (error: Error | null, data: Buffer) => void): void;
export function sign(algorithm: any, data: any, key: any, callback?: any): any {
    const sig = signBytes(algorithm ?? '', key, viewBytes(data));
    if (callback === undefined) return sig;
    validateFunction(callback, 'callback');
    process.nextTick(() => { callback(null, sig); });
}

export function verify(algorithm: string | null | undefined, data: NodeJS.ArrayBufferView, key: KeyLike, signature: NodeJS.ArrayBufferView): boolean;
export function verify(algorithm: string | null | undefined, data: NodeJS.ArrayBufferView, key: KeyLike, signature: NodeJS.ArrayBufferView, callback: (error: Error | null, result: boolean) => void): void;
export function verify(algorithm: any, data: any, key: any, signature: any, callback?: any): any {
    const ok = verifyBytes(algorithm ?? '', key, viewBytes(data), viewBytes(signature));
    if (callback === undefined) return ok;
    validateFunction(callback, 'callback');
    process.nextTick(() => { callback(null, ok); });
}

// ---- publicEncrypt / privateDecrypt / privateEncrypt / publicDecrypt
// (lib/internal/crypto/cipher.js's rsaFunctionFor) ----

export interface RsaPublicKey {
    key: KeyLike;
    padding?: number | undefined;
}

export interface RsaPrivateKey {
    key: KeyLike;
    passphrase?: string | undefined;
    oaepHash?: string | undefined;
    oaepLabel?: NodeJS.TypedArray | undefined;
    padding?: number | undefined;
}

function rsaCrypt(op: number, defaultPadding: number, options: any, buffer: any): Buffer {
    const [pem, pass, hasPass] = signKey(options, op === 1 || op === 2);
    const opts: any = options !== null && typeof options === 'object' && !(options instanceof KeyObject) && !isView(options) ? options : {};
    const padding = opts.padding || defaultPadding;
    let oaepHash = '';
    if (opts.oaepHash !== undefined) {
        validateString(opts.oaepHash, 'key.oaepHash');
        oaepHash = opts.oaepHash;
    }
    const label = opts.oaepLabel !== undefined ? toBytes(opts.oaepLabel, 'key.oaepLabel', opts.encoding) : empty;
    const data = toBytes(buffer, 'buffer', opts.encoding);
    let out = Buffer.alloc(1024);
    let n = __kml_native.cryptoPkeyCrypt(op, pem, pass, hasPass, padding, oaepHash, label, data, out);
    if (n <= -4) {
        out = Buffer.alloc(-n - 2);
        n = __kml_native.cryptoPkeyCrypt(op, pem, pass, hasPass, padding, oaepHash, label, data, out);
    }
    if (n === -3) throw new NodeError('ERR_OSSL_EVP_INVALID_DIGEST', 'Invalid digest used');
    if (n < 0) throw opensslError('RSA operation failed');
    return Buffer.from(out.subarray(0, n));
}

export function publicEncrypt(key: RsaPublicKey | RsaPrivateKey | KeyLike, buffer: NodeJS.ArrayBufferView | string): Buffer;
export function publicEncrypt(key: any, buffer: any): Buffer {
    return rsaCrypt(0, constants.RSA_PKCS1_OAEP_PADDING, key, buffer);
}

export function privateDecrypt(privateKey: RsaPrivateKey | KeyLike, buffer: NodeJS.ArrayBufferView | string): Buffer;
export function privateDecrypt(privateKey: any, buffer: any): Buffer {
    return rsaCrypt(1, constants.RSA_PKCS1_OAEP_PADDING, privateKey, buffer);
}

export function privateEncrypt(privateKey: RsaPrivateKey | KeyLike, buffer: NodeJS.ArrayBufferView | string): Buffer;
export function privateEncrypt(privateKey: any, buffer: any): Buffer {
    return rsaCrypt(2, constants.RSA_PKCS1_PADDING, privateKey, buffer);
}

export function publicDecrypt(key: RsaPublicKey | RsaPrivateKey | KeyLike, buffer: NodeJS.ArrayBufferView | string): Buffer;
export function publicDecrypt(key: any, buffer: any): Buffer {
    return rsaCrypt(3, constants.RSA_PKCS1_PADDING, key, buffer);
}

// ---- ECDH and diffieHellman (lib/internal/crypto/diffiehellman.js) ----

export type ECDHKeyFormat = 'compressed' | 'uncompressed' | 'hybrid';

function ecdhFormat(format: any): number {
    if (format) {
        if (format === 'compressed') return constants.POINT_CONVERSION_COMPRESSED;
        if (format === 'hybrid') return constants.POINT_CONVERSION_HYBRID;
        if (format !== 'uncompressed') throw new NodeTypeError('ERR_CRYPTO_ECDH_INVALID_FORMAT', 'Invalid ECDH format: ' + format);
    }
    return constants.POINT_CONVERSION_UNCOMPRESSED;
}

// A native ECDH op's bytes, or the error it reports.
function ecdhOp(op: number, curve: string, priv: Uint8Array, pub: Uint8Array, format: number): Buffer {
    const out = Buffer.alloc(200);
    const n = __kml_native.cryptoEcdh(op, curve, priv, pub, format, out);
    if (n === -8) throw new NodeTypeError('ERR_CRYPTO_INVALID_CURVE', 'Invalid EC curve name');
    if (n === -6) throw new NodeError('ERR_CRYPTO_ECDH_INVALID_PUBLIC_KEY', 'Public key is not valid for specified curve');
    if (n === -7) throw new NodeRangeError('ERR_CRYPTO_INVALID_KEYTYPE', 'Private key is not valid for specified curve.');
    if (n < 0) throw new NodeError('ERR_CRYPTO_OPERATION_FAILED', 'ECDH operation failed');
    return Buffer.from(out.subarray(0, n));
}

export class ECDH {
    #curve: string;
    #privateKey: Buffer | null = null;
    #publicKey: Buffer | null = null;

    constructor(curve: string) {
        validateString(curve, 'curve');
        if (__kml_native.cryptoEcdh(4, curve, Buffer.alloc(1, 1), empty, 0, Buffer.alloc(0)) === -8) {
            throw new NodeTypeError('ERR_CRYPTO_INVALID_CURVE', 'Invalid EC curve name');
        }
        this.#curve = curve;
    }

    static convertKey(key: BinaryLike, curve: string, inputEncoding?: BinaryToTextEncoding, outputEncoding?: 'latin1' | 'hex' | 'base64' | 'base64url', format?: 'uncompressed' | 'compressed' | 'hybrid'): Buffer | string;
    static convertKey(key: any, curve: any, inputEncoding?: any, outputEncoding?: any, format?: any): any {
        validateString(curve, 'curve');
        const bytes = toBytes(key, 'key', inputEncoding);
        return encodeOut(ecdhOp(3, curve, empty, bytes, ecdhFormat(format)), outputEncoding);
    }

    generateKeys(): Buffer;
    generateKeys(encoding: BinaryToTextEncoding, format?: ECDHKeyFormat): string;
    generateKeys(encoding?: any, format?: any): any {
        const priv = ecdhOp(0, this.#curve, empty, empty, 0);
        this.#privateKey = priv;
        this.#publicKey = ecdhOp(1, this.#curve, priv, empty, constants.POINT_CONVERSION_UNCOMPRESSED);
        return this.getPublicKey(encoding, format);
    }

    computeSecret(otherPublicKey: NodeJS.ArrayBufferView): Buffer;
    computeSecret(otherPublicKey: string, inputEncoding: BinaryToTextEncoding): Buffer;
    computeSecret(otherPublicKey: NodeJS.ArrayBufferView, outputEncoding: BinaryToTextEncoding): string;
    computeSecret(otherPublicKey: string, inputEncoding: BinaryToTextEncoding, outputEncoding: BinaryToTextEncoding): string;
    computeSecret(otherPublicKey: any, inputEncoding?: any, outputEncoding?: any): any {
        const key = toBytes(otherPublicKey, 'key', inputEncoding);
        if (this.#privateKey === null) throw new NodeError('ERR_CRYPTO_OPERATION_FAILED', 'Failed to get ECDH private key');
        const secret = ecdhOp(2, this.#curve, this.#privateKey as Buffer, key, 0);
        return typeof outputEncoding === 'string' && outputEncoding !== 'buffer' ? secret.toString(outputEncoding as BufferEncoding) : secret;
    }

    getPrivateKey(): Buffer;
    getPrivateKey(encoding: BinaryToTextEncoding): string;
    getPrivateKey(encoding?: any): any {
        if (this.#privateKey === null) throw new NodeError('ERR_CRYPTO_OPERATION_FAILED', 'Failed to get ECDH private key');
        return encodeOut(Buffer.from(this.#privateKey as Buffer), encoding);
    }

    getPublicKey(encoding?: null, format?: ECDHKeyFormat): Buffer;
    getPublicKey(encoding: BinaryToTextEncoding, format?: ECDHKeyFormat): string;
    getPublicKey(encoding?: any, format?: any): any {
        const f = ecdhFormat(format);
        if (this.#publicKey === null) throw new NodeError('ERR_CRYPTO_OPERATION_FAILED', 'Failed to get ECDH public key');
        return encodeOut(ecdhOp(3, this.#curve, empty, this.#publicKey as Buffer, f), encoding);
    }

    setPrivateKey(privateKey: NodeJS.ArrayBufferView): this;
    setPrivateKey(privateKey: string, encoding: BinaryToTextEncoding): this;
    setPrivateKey(privateKey: any, encoding?: any): this {
        const key = Buffer.from(toBytes(privateKey, 'key', encoding));
        this.#publicKey = ecdhOp(1, this.#curve, key, empty, constants.POINT_CONVERSION_UNCOMPRESSED);
        this.#privateKey = key;
        return this;
    }

    setPublicKey(publicKey: NodeJS.ArrayBufferView): this;
    setPublicKey(publicKey: string, encoding: BinaryToTextEncoding): this;
    setPublicKey(publicKey: any, encoding?: any): this {
        const key = Buffer.from(toBytes(publicKey, 'key', encoding));
        this.#publicKey = ecdhOp(3, this.#curve, empty, key, constants.POINT_CONVERSION_UNCOMPRESSED);
        return this;
    }
}

export function createECDH(curveName: string): ECDH {
    return new ECDH(curveName);
}

// crypto.diffieHellman({ privateKey, publicKey }): the secret two
// KeyObjects of one type (ec on one curve, x25519, x448) share.
export function diffieHellman(options: { privateKey: KeyObject; publicKey: KeyObject }): Buffer;
export function diffieHellman(options: any): Buffer {
    validateObject(options, 'options');
    const priv = options.privateKey;
    const pub = options.publicKey;
    const [privPem] = signKey(priv, true);
    const [pubPem] = signKey(pub, false);
    if (priv instanceof KeyObject && pub instanceof KeyObject) {
        const a = (priv as KeyObject).asymmetricKeyType;
        const b = (pub as KeyObject).asymmetricKeyType;
        if (a !== b) throw new NodeError('ERR_CRYPTO_INCOMPATIBLE_KEY', 'Incompatible key types for Diffie-Hellman: ' + a + ' and ' + b);
    }
    let out = Buffer.alloc(256);
    let n = __kml_native.cryptoDeriveSecret(privPem, pubPem, out);
    if (n <= -2) {
        out = Buffer.alloc(-n - 2);
        n = __kml_native.cryptoDeriveSecret(privPem, pubPem, out);
    }
    if (n < 0) throw opensslError('Failed to derive the shared secret');
    return Buffer.from(out.subarray(0, n));
}

// ---- generateKeyPair (lib/internal/crypto/keygen.js) ----

export interface KeyPairSyncResult<T1 extends string | Buffer, T2 extends string | Buffer> {
    publicKey: T1;
    privateKey: T2;
}

export interface KeyPairKeyObjectResult {
    publicKey: KeyObject;
    privateKey: KeyObject;
}

export interface BasePrivateKeyEncodingOptions<T extends KeyFormat> {
    format: T;
    cipher?: string | undefined;
    passphrase?: string | undefined;
}

interface KeyEncodingOptions<T extends KeyFormat> {
    type: string;
    format: T;
    cipher?: string | undefined;
    passphrase?: string | undefined;
}

// Every key type's options in one shape: each type reads its own members
// (modulusLength for RSA/RSA-PSS/DSA, namedCurve for EC, none for the
// Edwards and Montgomery curves).
interface KeyPairOptionsBase {
    modulusLength?: number | undefined;
    publicExponent?: number | undefined;
    hashAlgorithm?: string | undefined;
    mgf1HashAlgorithm?: string | undefined;
    saltLength?: number | undefined;
    divisorLength?: number | undefined;
    namedCurve?: string | undefined;
}

export interface KeyPairOptions<PubF extends KeyFormat, PrivF extends KeyFormat> extends KeyPairOptionsBase {
    publicKeyEncoding: KeyEncodingOptions<PubF>;
    privateKeyEncoding: KeyEncodingOptions<PrivF>;
}

export interface KeyPairKeyObjectOptions extends KeyPairOptionsBase {
    publicKeyEncoding?: undefined;
    privateKeyEncoding?: undefined;
}

export type RSAKeyPairOptions<PubF extends KeyFormat = 'pem', PrivF extends KeyFormat = 'pem'> = KeyPairOptions<PubF, PrivF>;
export type ECKeyPairOptions<PubF extends KeyFormat = 'pem', PrivF extends KeyFormat = 'pem'> = KeyPairOptions<PubF, PrivF>;
export type ED25519KeyPairOptions<PubF extends KeyFormat = 'pem', PrivF extends KeyFormat = 'pem'> = KeyPairOptions<PubF, PrivF>;

const kKeygenKinds: { [k: string]: number } = { rsa: 0, ec: 1, ed25519: 2, x25519: 3, 'rsa-pss': 4, dsa: 5, ed448: 6, x448: 7 };

function validateUint32(value: any, name: string): void {
    if (typeof value !== 'number') throw invalidArgType(name, 'of type number', value);
    if (!Number.isInteger(value)) throw outOfRange(name, 'an integer', value);
    if (value < 0 || value > 4294967295) throw outOfRange(name, '>= 0 && <= 4294967295', value);
}

// The native keygen's arguments for (type, options): [kind, bits,
// exponent, curve] — RSA-PSS's restrictions ride in curve as
// "hash\tmgf1\tsalt", DSA's divisor length in exponent.
function keygenArgs(type: any, options: any): [number, number, number, string] {
    validateString(type, 'type');
    const kind = kKeygenKinds[type as string];
    if (kind === undefined) {
        throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'type' must be a supported key type. Received '" + type + "'");
    }
    const o: any = options ?? {};
    if (options !== undefined) validateObject(options, 'options');
    switch (type) {
        case 'rsa':
        case 'rsa-pss': {
            validateUint32(o.modulusLength, 'options.modulusLength');
            let exponent = 65537;
            if (o.publicExponent !== undefined) {
                validateUint32(o.publicExponent, 'options.publicExponent');
                exponent = o.publicExponent;
            }
            if (type === 'rsa') return [kind, o.modulusLength, exponent, ''];
            const hash = o.hashAlgorithm ?? '';
            const mgf1 = o.mgf1HashAlgorithm ?? '';
            if (typeof hash !== 'string') throw invalidArgType('options.hashAlgorithm', 'of type string', hash);
            if (typeof mgf1 !== 'string') throw invalidArgType('options.mgf1HashAlgorithm', 'of type string', mgf1);
            let salt = '';
            if (o.saltLength !== undefined) {
                validateInt32(o.saltLength, 'options.saltLength', 0, 2147483647);
                salt = String(o.saltLength);
            }
            return [kind, o.modulusLength, exponent, hash + '\t' + mgf1 + '\t' + salt];
        }
        case 'dsa': {
            validateUint32(o.modulusLength, 'options.modulusLength');
            let divisor = 0;
            if (o.divisorLength !== undefined && o.divisorLength !== null) {
                validateInt32(o.divisorLength, 'options.divisorLength', 0, 2147483647);
                divisor = o.divisorLength;
            }
            return [kind, o.modulusLength, divisor, ''];
        }
        case 'ec':
            validateString(o.namedCurve, 'options.namedCurve');
            return [kind, 0, 0, o.namedCurve];
    }
    return [kind, 0, 0, ''];
}

// One half of a generated pair in its requested encoding, or the KeyObject.
// SPKI and unencrypted PKCS#8 PEM are the key's own canonical form.
function encodeKeygenHalf(key: KeyObject, encoding: any, name: string): any {
    if (encoding === undefined) return key;
    validateObject(encoding, 'options.' + name);
    const own = key.type === 'public' ? 'spki' : 'pkcs8';
    if (encoding.format === 'pem' && encoding.type === own && encoding.cipher === undefined) return keyHandle(key).pem;
    return key.export(encoding as any);
}

function keygenResult(pem: string, options: any): any {
    const at = pem.indexOf('-----BEGIN PRIVATE KEY-----');
    const publicKey = publicKeyFromPem(pem.slice(0, at));
    const privateKey = privateKeyFromPem(pem.slice(at));
    const o: any = options ?? {};
    return {
        publicKey: encodeKeygenHalf(publicKey, o.publicKeyEncoding, 'publicKeyEncoding'),
        privateKey: encodeKeygenHalf(privateKey, o.privateKeyEncoding, 'privateKeyEncoding'),
    };
}

export function generateKeyPairSync(type: KeyType, options: KeyPairOptions<'pem', 'pem'>): KeyPairSyncResult<string, string>;
export function generateKeyPairSync(type: KeyType, options: KeyPairOptions<'pem', 'der'>): KeyPairSyncResult<string, Buffer>;
export function generateKeyPairSync(type: KeyType, options: KeyPairOptions<'der', 'pem'>): KeyPairSyncResult<Buffer, string>;
export function generateKeyPairSync(type: KeyType, options: KeyPairOptions<'der', 'der'>): KeyPairSyncResult<Buffer, Buffer>;
export function generateKeyPairSync(type: KeyType, options?: KeyPairKeyObjectOptions): KeyPairKeyObjectResult;
export function generateKeyPairSync(type: any, options?: any): any {
    const [kind, bits, exponent, curve] = keygenArgs(type, options);
    const pem = __kml_native.cryptoKeygen(kind, bits, exponent, curve);
    if (pem === '') throw keygenError(kind);
    return keygenResult(pem, options);
}

export function generateKeyPair(type: KeyType, options: KeyPairOptions<'pem', 'pem'>, callback: (err: Error | null, publicKey: string, privateKey: string) => void): void;
export function generateKeyPair(type: KeyType, options: KeyPairOptions<'pem', 'der'>, callback: (err: Error | null, publicKey: string, privateKey: Buffer) => void): void;
export function generateKeyPair(type: KeyType, options: KeyPairOptions<'der', 'pem'>, callback: (err: Error | null, publicKey: Buffer, privateKey: string) => void): void;
export function generateKeyPair(type: KeyType, options: KeyPairOptions<'der', 'der'>, callback: (err: Error | null, publicKey: Buffer, privateKey: Buffer) => void): void;
export function generateKeyPair(type: KeyType, options: KeyPairKeyObjectOptions | undefined, callback: (err: Error | null, publicKey: KeyObject, privateKey: KeyObject) => void): void;
export function generateKeyPair(type: any, options: any, callback: any): void {
    validateFunction(callback, 'callback');
    const [kind, bits, exponent, curve] = keygenArgs(type, options);
    __kml_native.cryptoKeygenAsync(kind, bits, exponent, curve, (status: number, result: number) => {
        const pem = __kml_native.cryptoKeygenTake(result);
        if (status !== 0 || pem === '') {
            callback(keygenError(kind), undefined, undefined);
            return;
        }
        let pair: any;
        try {
            pair = keygenResult(pem, options);
        } catch (err) {
            callback(err, undefined, undefined);
            return;
        }
        callback(null, pair.publicKey, pair.privateKey);
    });
}

// ---- constants ----

const DEFAULT_CIPHER_LIST = 'TLS_AES_256_GCM_SHA384:TLS_CHACHA20_POLY1305_SHA256:TLS_AES_128_GCM_SHA256:ECDHE-RSA-AES128-GCM-SHA256:ECDHE-ECDSA-AES128-GCM-SHA256:ECDHE-RSA-AES256-GCM-SHA384:ECDHE-ECDSA-AES256-GCM-SHA384:DHE-RSA-AES128-GCM-SHA256:ECDHE-RSA-AES128-SHA256:DHE-RSA-AES128-SHA256:ECDHE-RSA-AES256-SHA384:DHE-RSA-AES256-SHA384:ECDHE-RSA-AES256-SHA256:DHE-RSA-AES256-SHA256:HIGH:!aNULL:!eNULL:!EXPORT:!DES:!RC4:!MD5:!PSK:!SRP:!CAMELLIA';

// lib/internal/crypto/util.js's binding constants (node_constants.cc).
// OPENSSL_VERSION_NUMBER is the linked libcrypto's.
export const constants = {
    OPENSSL_VERSION_NUMBER: Number(__kml_native.cryptoNames(2)),
    SSL_OP_ALL: 2147485776,
    SSL_OP_ALLOW_NO_DHE_KEX: 1024,
    SSL_OP_ALLOW_UNSAFE_LEGACY_RENEGOTIATION: 262144,
    SSL_OP_CIPHER_SERVER_PREFERENCE: 4194304,
    SSL_OP_CISCO_ANYCONNECT: 32768,
    SSL_OP_COOKIE_EXCHANGE: 8192,
    SSL_OP_CRYPTOPRO_TLSEXT_BUG: 2147483648,
    SSL_OP_DONT_INSERT_EMPTY_FRAGMENTS: 2048,
    SSL_OP_LEGACY_SERVER_CONNECT: 4,
    SSL_OP_NO_COMPRESSION: 131072,
    SSL_OP_NO_ENCRYPT_THEN_MAC: 524288,
    SSL_OP_NO_QUERY_MTU: 4096,
    SSL_OP_NO_RENEGOTIATION: 1073741824,
    SSL_OP_NO_SESSION_RESUMPTION_ON_RENEGOTIATION: 65536,
    SSL_OP_NO_SSLv2: 0,
    SSL_OP_NO_SSLv3: 33554432,
    SSL_OP_NO_TICKET: 16384,
    SSL_OP_NO_TLSv1: 67108864,
    SSL_OP_NO_TLSv1_1: 268435456,
    SSL_OP_NO_TLSv1_2: 134217728,
    SSL_OP_NO_TLSv1_3: 536870912,
    SSL_OP_PRIORITIZE_CHACHA: 2097152,
    SSL_OP_TLS_ROLLBACK_BUG: 8388608,
    ENGINE_METHOD_RSA: 1,
    ENGINE_METHOD_DSA: 2,
    ENGINE_METHOD_DH: 4,
    ENGINE_METHOD_RAND: 8,
    ENGINE_METHOD_EC: 2048,
    ENGINE_METHOD_CIPHERS: 64,
    ENGINE_METHOD_DIGESTS: 128,
    ENGINE_METHOD_PKEY_METHS: 512,
    ENGINE_METHOD_PKEY_ASN1_METHS: 1024,
    ENGINE_METHOD_ALL: 65535,
    ENGINE_METHOD_NONE: 0,
    DH_CHECK_P_NOT_SAFE_PRIME: 2,
    DH_CHECK_P_NOT_PRIME: 1,
    DH_UNABLE_TO_CHECK_GENERATOR: 4,
    DH_NOT_SUITABLE_GENERATOR: 8,
    RSA_PKCS1_PADDING: 1,
    RSA_NO_PADDING: 3,
    RSA_PKCS1_OAEP_PADDING: 4,
    RSA_X931_PADDING: 5,
    RSA_PKCS1_PSS_PADDING: 6,
    RSA_PSS_SALTLEN_DIGEST: -1,
    RSA_PSS_SALTLEN_MAX_SIGN: -2,
    RSA_PSS_SALTLEN_AUTO: -2,
    defaultCoreCipherList: DEFAULT_CIPHER_LIST,
    TLS1_VERSION: 769,
    TLS1_1_VERSION: 770,
    TLS1_2_VERSION: 771,
    TLS1_3_VERSION: 772,
    POINT_CONVERSION_COMPRESSED: 2,
    POINT_CONVERSION_UNCOMPRESSED: 4,
    POINT_CONVERSION_HYBRID: 6,
    defaultCipherList: DEFAULT_CIPHER_LIST,
};
