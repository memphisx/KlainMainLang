// Node's `crypto` (lib/crypto.js and lib/internal/crypto/*): Hash, Hmac,
// Cipheriv/Decipheriv, Sign/Verify, the KDFs, random values and key-pair
// generation, over libcrypto handles (cryptosrc/crypto_openssl.c). A pooled
// operation runs on the thread pool and calls back on the loop thread, as
// Node's crypto jobs do. The Web Crypto API is the global `crypto`.
// kml:default-namespace — `import crypto from 'crypto'` reads this module's
// exports.
import { Transform } from 'stream';
import type { TransformOptions } from 'stream';

export type BinaryLike = string | NodeJS.ArrayBufferView;
export type BinaryToTextEncoding = 'base64' | 'base64url' | 'hex' | 'binary';
export type CharacterEncoding = 'utf8' | 'utf-8' | 'utf16le' | 'utf-16le' | 'latin1';
export type Encoding = BinaryToTextEncoding | CharacterEncoding;
export type KeyFormat = 'pem' | 'der' | 'jwk';
export type UUID = `${string}-${string}-${string}-${string}-${string}`;
export type CipherKey = BinaryLike;

class NodeError extends Error {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

class NodeTypeError extends TypeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

class NodeRangeError extends RangeError {
    code: string;
    constructor(code: string, message: string) {
        super(message);
        this.code = code;
    }
}

function received(value: any): string {
    if (value === null) return 'Received null';
    if (value === undefined) return 'Received undefined';
    if (typeof value === 'function') return 'Received function ' + (value.name || '<anonymous>');
    if (typeof value === 'object') {
        if (Array.isArray(value)) return 'Received an instance of Array';
        return 'Received an instance of Object';
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

export function getHashes(): string[] {
    return __kml_native.cryptoNames(0).split(',').filter((n: string) => n.length > 0);
}

export function getCiphers(): string[] {
    return __kml_native.cryptoNames(1).split(',').filter((n: string) => n.length > 0).map((n: string) => n.toLowerCase());
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
        if (n < 0) throw opensslError('Trying to add data in unsupported state');
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

// ---- Sign / Verify (lib/internal/crypto/sig.js), PEM keys ----

type KeyLike = string | Buffer | { key: string | Buffer; passphrase?: string | Buffer };

function keyPem(key: any, name: string): string {
    if (typeof key === 'string') return key;
    if (Buffer.isBuffer(key)) return (key as Buffer).toString();
    if (key !== null && typeof key === 'object' && key.key !== undefined) return keyPem(key.key, name);
    throw invalidArgType(name, 'of type string or an instance of Buffer, TypedArray, DataView, or KeyObject', key);
}

// A `{ key, passphrase }` key's passphrase: [bytes, 1], or [empty, 0].
function keyPassphrase(key: any): [Uint8Array, number] {
    if (key === null || typeof key !== 'object' || Buffer.isBuffer(key) || key.passphrase === undefined) return [empty, 0];
    return [toBytes(key.passphrase, 'key.passphrase'), 1];
}

function signBytes(digest: string, key: any, data: Uint8Array): Buffer {
    const pem = keyPem(key, 'key');
    const [pass, hasPass] = keyPassphrase(key);
    let out = Buffer.alloc(512);
    let n = __kml_native.cryptoSign(digest, pem, pass, hasPass, data, out);
    if (n <= -2) {
        out = Buffer.alloc(-n - 2);
        n = __kml_native.cryptoSign(digest, pem, pass, hasPass, data, out);
    }
    if (n < 0) throw opensslError('Invalid key');
    return out.subarray(0, n);
}

function verifyBytes(digest: string, key: any, data: Uint8Array, sig: Uint8Array): boolean {
    const [pass, hasPass] = keyPassphrase(key);
    const r = __kml_native.cryptoVerify(digest, keyPem(key, 'key'), pass, hasPass, data, sig);
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

// ---- generateKeyPair (lib/internal/crypto/keygen.js), PEM encodings ----

export interface KeyPairSyncResult<T1 extends string | Buffer, T2 extends string | Buffer> {
    publicKey: T1;
    privateKey: T2;
}

interface KeyEncoding {
    type: string;
    format: string;
    cipher?: string | undefined;
    passphrase?: string | undefined;
}

export interface RSAKeyPairOptions {
    modulusLength: number;
    publicExponent?: number | undefined;
    publicKeyEncoding: KeyEncoding;
    privateKeyEncoding: KeyEncoding;
}

export interface ECKeyPairOptions {
    namedCurve: string;
    publicKeyEncoding: KeyEncoding;
    privateKeyEncoding: KeyEncoding;
}

export interface ED25519KeyPairOptions {
    publicKeyEncoding: KeyEncoding;
    privateKeyEncoding: KeyEncoding;
}

// The native keygen's arguments for (type, options): [kind, bits, exponent,
// curve].
function keygenArgs(type: any, options: any): [number, number, number, string] {
    validateString(type, 'type');
    const o: any = options ?? {};
    const pub = o.publicKeyEncoding;
    const priv = o.privateKeyEncoding;
    if (pub === undefined || priv === undefined) {
        throw new NodeError('ERR_FEATURE_UNAVAILABLE_ON_PLATFORM', 'KeyObject results are not supported: pass publicKeyEncoding and privateKeyEncoding');
    }
    if (pub.format !== 'pem' || priv.format !== 'pem' || pub.type !== 'spki' || priv.type !== 'pkcs8' || priv.cipher !== undefined) {
        throw new NodeError('ERR_FEATURE_UNAVAILABLE_ON_PLATFORM', "Only { type: 'spki', format: 'pem' } and { type: 'pkcs8', format: 'pem' } key encodings are supported");
    }
    switch (type) {
        case 'rsa':
            validateInt32(o.modulusLength, 'options.modulusLength', 0, 2147483647);
            return [0, o.modulusLength, o.publicExponent ?? 65537, ''];
        case 'ec':
            validateString(o.namedCurve, 'options.namedCurve');
            return [1, 0, 0, o.namedCurve];
        case 'ed25519':
            return [2, 0, 0, ''];
        case 'x25519':
            return [3, 0, 0, ''];
    }
    throw new NodeTypeError('ERR_INVALID_ARG_VALUE', "The argument 'type' must be a supported key type. Received '" + type + "'");
}

function splitPemPair(pem: string): KeyPairSyncResult<string, string> {
    const at = pem.indexOf('-----BEGIN PRIVATE KEY-----');
    return { publicKey: pem.slice(0, at), privateKey: pem.slice(at) };
}

export function generateKeyPairSync(type: 'rsa', options: RSAKeyPairOptions): KeyPairSyncResult<string, string>;
export function generateKeyPairSync(type: 'ec', options: ECKeyPairOptions): KeyPairSyncResult<string, string>;
export function generateKeyPairSync(type: 'ed25519' | 'x25519', options: ED25519KeyPairOptions): KeyPairSyncResult<string, string>;
export function generateKeyPairSync(type: any, options: any): KeyPairSyncResult<string, string> {
    const [kind, bits, exponent, curve] = keygenArgs(type, options);
    const pem = __kml_native.cryptoKeygen(kind, bits, exponent, curve);
    if (pem === '') throw keygenError(kind);
    return splitPemPair(pem);
}

export function generateKeyPair(type: 'rsa', options: RSAKeyPairOptions, callback: (err: Error | null, publicKey: string, privateKey: string) => void): void;
export function generateKeyPair(type: 'ec', options: ECKeyPairOptions, callback: (err: Error | null, publicKey: string, privateKey: string) => void): void;
export function generateKeyPair(type: 'ed25519' | 'x25519', options: ED25519KeyPairOptions, callback: (err: Error | null, publicKey: string, privateKey: string) => void): void;
export function generateKeyPair(type: any, options: any, callback: any): void {
    validateFunction(callback, 'callback');
    const [kind, bits, exponent, curve] = keygenArgs(type, options);
    __kml_native.cryptoKeygenAsync(kind, bits, exponent, curve, (status: number, result: number) => {
        const pem = __kml_native.cryptoKeygenTake(result);
        if (status !== 0 || pem === '') {
            callback(keygenError(kind), undefined, undefined);
            return;
        }
        const pair = splitPemPair(pem);
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
