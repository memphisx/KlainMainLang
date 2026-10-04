// The per-algorithm halves of Node's Web Crypto: lib/internal/crypto/aes.js,
// mac.js, hkdf.js, pbkdf2.js, rsa.js, ec.js, cfrg.js, diffiehellman.js'
// ecdhDeriveBits and webcrypto_util.js. Each Node `Job` (a native task whose
// promise settles on the pool) is a function run here and settled after one
// loop turn; its failure is an OperationError, as Node's jobPromise reports.
// Built on lib/node/crypto.ts: createHmac, createCipheriv/createDecipheriv,
// pbkdf2Sync, hkdfSync, generateKeyPairSync, sign and verify.
import {
    constants, createCipheriv, createDecipheriv, createHash, createHmac, diffieHellman, generateKeyPairSync, hkdfSync, pbkdf2Sync,
    privateDecrypt, publicEncrypt, randomBytes, sign as cryptoSign, timingSafeEqual, verify as cryptoVerify,
} from './crypto';
import {
    checkEcKeyData, CryptoKey, ecKeyFromRaw, exportHandleJwk, getCryptoKeyAlgorithm, getCryptoKeyHandle, getCryptoKeyType,
    importHandleJwk, KeyHandle, KeyUsageLists, lazyDOMException, newCryptoKey, okpKeyFromRaw, operationError, uniqueUsages, usageMask,
    usagesUnion, validateKeyOps, validateKeyUsages, validateUsagesNotEmpty, verifyAcceptableKeyUse,
} from './internal_crypto_webcrypto_keys';
import { copyBufferSource } from './internal_crypto_webcrypto_webidl';

// Two or three byte runs joined (Buffers pass as Uint8Arrays one by one; an
// array literal of Buffers is not accepted as Uint8Array[]).
function cat2(a: Uint8Array, b: Uint8Array): Uint8Array {
    const out = new Uint8Array(a.length + b.length);
    out.set(a, 0);
    out.set(b, a.length);
    return out;
}

function cat3(a: Uint8Array, b: Uint8Array, c: Uint8Array): Uint8Array {
    const out = new Uint8Array(a.length + b.length + c.length);
    out.set(a, 0);
    out.set(b, a.length);
    out.set(c, a.length + b.length);
    return out;
}

// ---- jobs ----

function wrapJobError(e: any): any {
    if (e instanceof DOMException) return e;
    return operationError(e);
}

// Runs `fn` now and settles after a loop turn, as a pool job does.
export function job(fn: () => any): Promise<any> {
    let failed = false;
    let error: any = undefined;
    let result: any = undefined;
    try {
        result = fn();
    } catch (e) {
        failed = true;
        error = e;
    }
    return new Promise<any>((resolve, reject) => {
        setImmediate(() => {
            if (failed) reject(wrapJobError(error));
            else resolve(result);
        });
    });
}

// `p.then(f)` with a handler that may throw: a throw inside a `.then` handler is
// not turned into a rejection by this compiler, so the handler runs under
// try/catch here. The handler's argument arrives as `any`.
export function chain(p: Promise<any>, f: (v: any) => any): Promise<any> {
    return new Promise<any>((resolve, reject) => {
        p.then((v: any) => {
            try {
                resolve(f(v));
            } catch (e) {
                reject(e);
            }
        }, (e: any) => reject(e));
    });
}

export function toArrayBuffer(b: Uint8Array): ArrayBuffer {
    const ab = new ArrayBuffer(b.length);
    new Uint8Array(ab).set(b);
    return ab;
}

export function numBitsToBytes(length: number): number {
    return Math.floor(length / 8) + Math.floor((7 + (length % 8)) / 8);
}

// The first `length` bits of `bytes`, the unused low bits of the last byte
// cleared.
export function truncateToBitLength(length: number, bytes: Uint8Array): Uint8Array {
    const n = numBitsToBytes(length);
    const out = bytes.slice(0, n);
    const rem = length % 8;
    if (rem !== 0) out[n - 1] &= (0xff << (8 - rem)) & 0xff;
    return out;
}

// ---- hash names (lib/internal/crypto/hashnames.js) ----

const kJwkRsa = 3;
const kJwkRsaPss = 4;
const kJwkRsaOaep = 5;
const kJwkHmac = 6;

export function nodeHashName(webName: string): string {
    switch (webName) {
        case 'SHA-1': return 'sha1';
        case 'SHA-256': return 'sha256';
        case 'SHA-384': return 'sha384';
        case 'SHA-512': return 'sha512';
    }
    return webName.toLowerCase();
}

function jwkHashAlg(context: number, webName: string): string {
    const bits = webName === 'SHA-1' ? '1' : webName.slice(4);
    if (context === kJwkRsa) return 'RS' + bits;
    if (context === kJwkRsaPss) return 'PS' + bits;
    if (context === kJwkHmac) return 'HS' + bits;
    return webName === 'SHA-1' ? 'RSA-OAEP' : 'RSA-OAEP-' + bits;
}

export function getBlockSize(name: string): number {
    return name === 'SHA-1' || name === 'SHA-256' ? 512 : 1024;
}

export function getDigestSizeInBytes(name: string): number {
    switch (name) {
        case 'SHA-1': return 20;
        case 'SHA-256': return 32;
        case 'SHA-384': return 48;
    }
    return 64;
}

export function digestJob(name: string, data: Uint8Array): Promise<ArrayBuffer> {
    return job((): any => {
        const d = createHash(nodeHashName(name)).update(data).digest();
        return toArrayBuffer(d);
    });
}

// ---- webcrypto_util.js ----

export function validateJwk(keyData: any, kty: string, extractable: boolean, usages: string[], expectedUse: string): void {
    if (typeof keyData.kty !== 'string') throw lazyDOMException('Invalid keyData', 'DataError');
    if (keyData.kty !== kty) throw lazyDOMException('Invalid JWK "kty" Parameter', 'DataError');
    const s = (v: any): boolean => typeof v === 'string';
    switch (kty) {
        case 'RSA':
            if (!s(keyData.n) || !s(keyData.e) || (keyData.d !== undefined && !s(keyData.d))) throw lazyDOMException('Invalid keyData', 'DataError');
            if (s(keyData.d) && (!s(keyData.p) || !s(keyData.q) || !s(keyData.dp) || !s(keyData.dq) || !s(keyData.qi))) {
                throw lazyDOMException('Invalid keyData', 'DataError');
            }
            break;
        case 'EC':
            if (!s(keyData.crv) || !s(keyData.x) || !s(keyData.y) || (keyData.d !== undefined && !s(keyData.d))) {
                throw lazyDOMException('Invalid keyData', 'DataError');
            }
            break;
        case 'OKP':
            if (!s(keyData.crv) || !s(keyData.x) || (keyData.d !== undefined && !s(keyData.d))) throw lazyDOMException('Invalid keyData', 'DataError');
            break;
        case 'oct':
            if (!s(keyData.k)) throw lazyDOMException('Invalid keyData', 'DataError');
            break;
    }
    if (usages.length > 0 && keyData.use !== undefined) {
        if (keyData.use !== expectedUse) throw lazyDOMException('Invalid JWK "use" Parameter', 'DataError');
    }
    validateKeyOps(keyData.key_ops, usages);
    if (keyData.ext !== undefined && keyData.ext === false && extractable === true) {
        throw lazyDOMException('JWK "ext" Parameter and extractable mismatch', 'DataError');
    }
}

function invalidKeyData(cause?: any): DOMException {
    return lazyDOMException('Invalid keyData', { name: 'DataError', cause });
}

function importDerKey(keyData: any, isPublic: boolean): KeyHandle {
    const der = copyBufferSource(keyData);
    const h = isPublic ? KeyHandle.fromSpki(der) : KeyHandle.fromPkcs8(der);
    if (h === null) throw invalidKeyData();
    return h;
}

function importJwkKey(kty: string, isPublic: boolean, keyData: any): KeyHandle {
    const h = importHandleJwk(kty, isPublic, keyData);
    if (h === null) throw invalidKeyData();
    return h;
}

function importSecretKey(keyData: any): KeyHandle {
    return KeyHandle.secretKey(copyBufferSource(keyData));
}

function importJwkSecretKey(keyData: any): KeyHandle {
    const h = importHandleJwk('oct', false, keyData);
    if (h === null) throw invalidKeyData();
    return h;
}

// The key usages one algorithm allows on each half of a pair.
const kOaepUsages = new KeyUsageLists(['encrypt', 'wrapKey'], ['decrypt', 'unwrapKey']);
const kSignVerifyUsages = new KeyUsageLists(['verify'], ['sign']);
const kDeriveUsages = new KeyUsageLists([], ['deriveKey', 'deriveBits']);

function pairUsagesOf(name: string): KeyUsageLists {
    switch (name) {
        case 'RSA-OAEP': return kOaepUsages;
        case 'ECDH':
        case 'X25519': return kDeriveUsages;
    }
    return kSignVerifyUsages;
}

function pairResult(pub: CryptoKey, priv: CryptoKey): any {
    return { publicKey: pub, privateKey: priv };
}

function genPair(type: string, options: any): [KeyHandle, KeyHandle] {
    const pair: any = (generateKeyPairSync as any)(type, options);
    return [KeyHandle.fromKeyObject(pair.publicKey), KeyHandle.fromKeyObject(pair.privateKey)];
}

// ---- AES ----

const kCipherUsages = ['encrypt', 'decrypt', 'wrapKey', 'unwrapKey'];
const kWrapUsages = ['wrapKey', 'unwrapKey'];

function aesUsages(name: string): string[] {
    return name === 'AES-KW' ? kWrapUsages : kCipherUsages;
}

export function aesAlgorithmName(name: string, length: number): string {
    switch (name) {
        case 'AES-CBC': return 'A' + length + 'CBC';
        case 'AES-CTR': return 'A' + length + 'CTR';
        case 'AES-GCM': return 'A' + length + 'GCM';
    }
    return 'A' + length + 'KW';
}

function validateKeyLength(length: number): void {
    if (length !== 128 && length !== 192 && length !== 256) throw lazyDOMException('Invalid key length', 'DataError');
}

function secretKeyGen(length: number): KeyHandle {
    const b = randomBytes(numBitsToBytes(length));
    const bytes = new Uint8Array(b.length);
    bytes.set(b);
    return KeyHandle.secretKey(truncateToBitLength(length, bytes));
}

export function aesGenerateKey(algorithm: any, extractable: boolean, usages: string[]): Promise<CryptoKey> {
    const name: string = algorithm.name;
    const length: number = algorithm.length;
    const set = validateUsagesNotEmpty(validateKeyUsages(usages, aesUsages(name), name));
    return job((): any => newCryptoKey(secretKeyGen(length), { name, length }, usageMask(set), extractable));
}

export function aesImportKey(algorithm: any, format: string, keyData: any, extractable: boolean, usages: string[]): CryptoKey | undefined {
    const name: string = algorithm.name;
    const set = validateKeyUsages(usages, aesUsages(name), name);
    let handle: KeyHandle;
    let length: number;
    if (format === 'raw-secret' || format === 'raw') {
        length = (keyData.byteLength as number) * 8;
        validateKeyLength(length);
        handle = importSecretKey(keyData);
    } else if (format === 'jwk') {
        validateJwk(keyData, 'oct', extractable, set, 'enc');
        handle = importJwkSecretKey(keyData);
        length = handle.secret.length * 8;
        validateKeyLength(length);
        if (keyData.alg !== undefined && keyData.alg !== aesAlgorithmName(name, length)) {
            throw lazyDOMException('JWK "alg" does not match the requested algorithm', 'DataError');
        }
    } else {
        return undefined;
    }
    return newCryptoKey(handle, { name, length }, usageMask(set), extractable);
}

// The counter block's value, and back (public data: plain BigInt math).
function bytesToBigInt(b: Uint8Array): bigint {
    let hex = '';
    for (let i = 0; i < b.length; i++) hex += (b[i] < 16 ? '0' : '') + b[i].toString(16);
    return hex === '' ? 0n : BigInt('0x' + hex);
}

function bigIntToBytes(v: bigint, len: number): Uint8Array {
    let hex = v.toString(16);
    while (hex.length < len * 2) hex = '0' + hex;
    return Buffer.from(hex, 'hex');
}

// AES-CTR over `data` with a counter block whose low `lengthBits` bits wrap.
function ctrCrypt(bits: number, key: Uint8Array, counter: Uint8Array, lengthBits: number, data: Uint8Array): Uint8Array {
    const blocks = Math.ceil(data.length / 16);
    const cipherName = 'aes-' + bits + '-ctr';
    // The counter's low lengthBits bits, and how many blocks fit before they wrap.
    const mod = 1n << BigInt(lengthBits);
    const lowMask = mod - 1n;
    const counterNum = bytesToBigInt(counter);
    const low = counterNum & lowMask;
    if (BigInt(blocks) > mod) throw new Error('counter overflow');
    const before = mod - low;
    const run = (iv: Uint8Array, part: Uint8Array): Uint8Array => {
        const c = createCipheriv(cipherName, key, iv);
        const a = c.update(part);
        const b = c.final();
        const out = new Uint8Array(a.length + b.length);
        out.set(a, 0);
        out.set(b, a.length);
        return out;
    };
    if (BigInt(blocks) <= before) return run(counter, data);
    const firstLen = Number(before) * 16;
    const second = ((counterNum >> BigInt(lengthBits)) << BigInt(lengthBits));
    const iv2 = bigIntToBytes(second, 16);
    return cat2(run(counter, data.subarray(0, firstLen)), run(iv2, data.subarray(firstLen)));
}

// RFC 3394 AES key wrap: OpenSSL's id-aes<bits>-wrap with the default IV.
function aesKw(encrypt: boolean, bits: number, key: Uint8Array, data: Uint8Array): Uint8Array {
    const name = 'id-aes' + bits + '-wrap';
    const iv = Buffer.from('a6a6a6a6a6a6a6a6', 'hex');
    const c = encrypt ? createCipheriv(name, key, iv) : createDecipheriv(name, key, iv);
    const a = c.update(data);
    const b = c.final();
    return cat2(a, b);
}

export function aesCipher(encrypt: boolean, key: CryptoKey, data: any, algorithm: any): Promise<ArrayBuffer> {
    const handle = getCryptoKeyHandle(key);
    const bits: number = getCryptoKeyAlgorithm(key).length;
    const bytes = copyBufferSource(data);
    const k = handle.secret;
    switch (algorithm.name as string) {
        case 'AES-CTR':
            return job((): any => toArrayBuffer(ctrCrypt(bits, k, algorithm.counter as Uint8Array, algorithm.length as number, bytes)));
        case 'AES-CBC':
            return job((): any => {
                const c = encrypt ? createCipheriv('aes-' + bits + '-cbc', k, algorithm.iv as Uint8Array) :
                    createDecipheriv('aes-' + bits + '-cbc', k, algorithm.iv as Uint8Array);
                const a = c.update(bytes);
                const b = c.final();
                return toArrayBuffer(cat2(a, b));
            });
        case 'AES-GCM': {
            const tagLength: number = algorithm.tagLength === undefined ? 128 : algorithm.tagLength;
            const tagBytes = tagLength / 8;
            return job((): any => {
                const iv = algorithm.iv as Uint8Array;
                const aad: Uint8Array | undefined = algorithm.additionalData;
                // Node's native GCM job takes IVs of 12 bytes or more.
                if (iv.length < 12) throw new Error('invalid IV length');
                if (encrypt) {
                    const c = createCipheriv('aes-' + bits + '-gcm', k, iv, { authTagLength: tagBytes });
                    if (aad !== undefined) c.setAAD(aad);
                    const a = c.update(bytes);
                    const b = c.final();
                    return toArrayBuffer(cat3(a, b, c.getAuthTag().subarray(0, tagBytes)));
                }
                if (bytes.length < tagBytes) throw new Error('data too short');
                const cut = bytes.length - tagBytes;
                const c = createDecipheriv('aes-' + bits + '-gcm', k, iv, { authTagLength: tagBytes });
                c.setAuthTag(bytes.subarray(cut));
                if (aad !== undefined) c.setAAD(aad);
                const a = c.update(bytes.subarray(0, cut));
                const b = c.final();
                return toArrayBuffer(cat2(a, b));
            });
        }
    }
    return job((): any => toArrayBuffer(aesKw(encrypt, bits, k, bytes)));
}

// ---- HMAC ----

const kMacUsages = ['sign', 'verify'];

export function hmacGenerateKey(algorithm: any, extractable: boolean, usages: string[]): Promise<CryptoKey> {
    const name: string = algorithm.name;
    const hash: any = algorithm.hash;
    const length: number = algorithm.length === undefined ? getBlockSize(hash.name as string) : algorithm.length;
    const set = validateUsagesNotEmpty(validateKeyUsages(usages, kMacUsages, name));
    return job((): any => newCryptoKey(secretKeyGen(length), { name, length, hash }, usageMask(set), extractable));
}

export function macImportKey(format: string, keyData: any, algorithm: any, extractable: boolean, usages: string[]): CryptoKey | undefined {
    const set = validateKeyUsages(usages, kMacUsages, algorithm.name as string);
    let handle: KeyHandle;
    if (format === 'raw-secret' || format === 'raw') {
        handle = importSecretKey(keyData);
    } else if (format === 'jwk') {
        validateJwk(keyData, 'oct', extractable, set, 'sig');
        if (keyData.alg !== undefined) {
            const expected = jwkHashAlg(kJwkHmac, algorithm.hash.name as string);
            if (keyData.alg !== expected) throw lazyDOMException('JWK "alg" does not match the requested algorithm', 'DataError');
        }
        handle = importJwkSecretKey(keyData);
    } else {
        return undefined;
    }
    let length = handle.secret.length * 8;
    if (length === 0) throw lazyDOMException('Zero-length key is not supported', 'DataError');
    if (algorithm.length !== undefined) {
        const byteLength = numBitsToBytes(algorithm.length as number);
        if (byteLength !== handle.secret.length) throw lazyDOMException('Invalid key length', 'DataError');
        if ((algorithm.length as number) % 8 !== 0) handle = KeyHandle.secretKey(truncateToBitLength(algorithm.length as number, handle.secret));
        length = algorithm.length;
    }
    return newCryptoKey(handle, { name: algorithm.name, length, hash: algorithm.hash }, usageMask(set), extractable);
}

export function hmacSignVerify(key: CryptoKey, data: any, signature: any): Promise<any> {
    const hashName = nodeHashName(getCryptoKeyAlgorithm(key).hash.name as string);
    const secret = getCryptoKeyHandle(key).secret;
    const bytes = copyBufferSource(data);
    const sig = signature === undefined ? undefined : copyBufferSource(signature);
    return job((): any => {
        const mac = createHmac(hashName, secret).update(bytes).digest();
        if (sig === undefined) return toArrayBuffer(mac);
        if (sig.length !== mac.length) return false;
        return timingSafeEqual(mac, sig);
    });
}

// ---- generic secrets, PBKDF2 and HKDF ----

export function importGenericSecretKey(algorithm: any, format: string, keyData: any, extractable: boolean, usages: string[]): CryptoKey | undefined {
    const name: string = algorithm.name;
    const set = uniqueUsages(usages);
    if (extractable) throw lazyDOMException(name + ' keys are not extractable', 'SyntaxError');
    for (let i = 0; i < set.length; i++) {
        if (set[i] !== 'deriveKey' && set[i] !== 'deriveBits') {
            throw lazyDOMException('Unsupported key usage for a ' + name + ' key', 'SyntaxError');
        }
    }
    if (format !== 'raw-secret' && format !== 'raw') return undefined;
    return newCryptoKey(importSecretKey(keyData), { name }, usageMask(set), false);
}

function validateDeriveBitsLength(length: number | null): void {
    if (length === null) throw lazyDOMException('length cannot be null', 'OperationError');
    if (length % 8 !== 0) throw lazyDOMException('length must be a multiple of 8', 'OperationError');
}

export function pbkdf2DeriveBits(algorithm: any, baseKey: CryptoKey, length: number | null): Promise<ArrayBuffer> {
    validateDeriveBitsLength(length);
    if (length === 0) return Promise.resolve(new ArrayBuffer(0));
    const secret = getCryptoKeyHandle(baseKey).secret;
    return job((): any => toArrayBuffer(pbkdf2Sync(secret, algorithm.salt as Uint8Array, algorithm.iterations as number,
        (length as number) / 8, nodeHashName(algorithm.hash.name as string))));
}

export function hkdfDeriveBits(algorithm: any, baseKey: CryptoKey, length: number | null): Promise<ArrayBuffer> {
    validateDeriveBitsLength(length);
    if (length === 0) return Promise.resolve(new ArrayBuffer(0));
    const secret = getCryptoKeyHandle(baseKey).secret;
    return job((): any => {
        const r = hkdfSync(nodeHashName(algorithm.hash.name as string), secret, algorithm.salt as Uint8Array,
            algorithm.info as Uint8Array, (length as number) / 8);
        return r;
    });
}

// ---- RSA ----

function rsaAlgorithm(name: string, handle: KeyHandle, hash: any): any {
    return { name, modulusLength: handle.modulusLength(), publicExponent: handle.publicExponent(), hash };
}

function bigIntArrayToUnsignedInt(input: Uint8Array): number | undefined {
    let result = 0;
    for (let n = 0; n < input.length; ++n) {
        const reversed = input.length - n - 1;
        if (reversed >= 4 && input[n] !== 0) return undefined;
        if (reversed < 4) result = (result | (input[n] << (8 * reversed))) >>> 0;
    }
    return result >>> 0;
}

export function rsaKeyGenerate(algorithm: any, extractable: boolean, usages: string[]): Promise<any> {
    const exponent = bigIntArrayToUnsignedInt(algorithm.publicExponent as Uint8Array);
    if (exponent === undefined) {
        throw lazyDOMException('The publicExponent must be equivalent to an unsigned 32-bit value', 'OperationError');
    }
    const name: string = algorithm.name;
    const allowed = pairUsagesOf(name);
    const set = validateKeyUsages(usages, allowed.keygen, name);
    const keyAlgorithm: any = { name, modulusLength: algorithm.modulusLength, publicExponent: algorithm.publicExponent, hash: algorithm.hash };
    if (exponent < 3 || exponent % 2 === 0) {
        throw lazyDOMException('The operation failed for an operation-specific reason', 'OperationError');
    }
    const pubUsages = usagesUnion(set, allowed.publicUsages);
    const privUsages = validateUsagesNotEmpty(usagesUnion(set, allowed.privateUsages));
    return job((): any => {
        const [pub, priv] = genPair('rsa', { modulusLength: algorithm.modulusLength, publicExponent: exponent });
        return pairResult(newCryptoKey(pub, keyAlgorithm, usageMask(pubUsages), true),
            newCryptoKey(priv, keyAlgorithm, usageMask(privUsages), extractable));
    });
}

export function rsaExportKey(key: CryptoKey, format: string): Uint8Array | undefined {
    const h = getCryptoKeyHandle(key);
    if (format === 'spki') return h.toSpki();
    if (format === 'pkcs8') return h.toPkcs8();
    return undefined;
}

export function rsaImportKey(format: string, keyData: any, algorithm: any, extractable: boolean, usages: string[]): CryptoKey | undefined {
    const name: string = algorithm.name;
    const allowed = pairUsagesOf(name);
    const set = uniqueUsages(usages);
    let handle: KeyHandle;
    if (format === 'spki') {
        verifyAcceptableKeyUse(name, set, allowed.publicUsages);
        handle = importDerKey(keyData, true);
    } else if (format === 'pkcs8') {
        verifyAcceptableKeyUse(name, set, allowed.privateUsages);
        handle = importDerKey(keyData, false);
    } else if (format === 'jwk') {
        validateJwk(keyData, 'RSA', extractable, set, name === 'RSA-OAEP' ? 'enc' : 'sig');
        if (keyData.alg !== undefined) {
            const ctx = name === 'RSASSA-PKCS1-v1_5' ? kJwkRsa : (name === 'RSA-PSS' ? kJwkRsaPss : kJwkRsaOaep);
            const expected = jwkHashAlg(ctx, algorithm.hash.name as string);
            if (keyData.alg !== expected) throw lazyDOMException('JWK "alg" does not match the requested algorithm', 'DataError');
        }
        const isPublic = keyData.d === undefined;
        verifyAcceptableKeyUse(name, set, isPublic ? allowed.publicUsages : allowed.privateUsages);
        handle = importJwkKey('RSA', isPublic, keyData);
    } else {
        return undefined;
    }
    if (handle.asym !== 'rsa') throw lazyDOMException('Invalid key type', 'DataError');
    return newCryptoKey(handle, rsaAlgorithm(name, handle, algorithm.hash), usageMask(set), extractable);
}

export function rsaCipher(encrypt: boolean, key: CryptoKey, data: any, algorithm: any): Promise<ArrayBuffer> {
    if (algorithm.label !== undefined && (algorithm.label as Uint8Array).byteLength > 2147483647) {
        throw lazyDOMException('algorithm.label must be at most 2147483647 bytes', 'OperationError');
    }
    const type = encrypt ? 'public' : 'private';
    if (getCryptoKeyType(key) !== type) {
        throw lazyDOMException('The requested operation is not valid for the provided key', 'InvalidAccessError');
    }
    const handle = getCryptoKeyHandle(key);
    const hash = nodeHashName(getCryptoKeyAlgorithm(key).hash.name as string);
    const bytes = copyBufferSource(data);
    const options: any = { key: handle.keyObject(), padding: constants.RSA_PKCS1_OAEP_PADDING, oaepHash: hash };
    if (algorithm.label !== undefined) options.oaepLabel = copyBufferSource(algorithm.label);
    return job((): any => toArrayBuffer(encrypt ? publicEncrypt(options, bytes) : privateDecrypt(options, bytes)));
}

export function rsaSignVerify(key: CryptoKey, data: any, algorithm: any, signature: any): Promise<any> {
    const isSign = signature === undefined;
    const type = isSign ? 'private' : 'public';
    if (getCryptoKeyType(key) !== type) throw lazyDOMException('Key must be a ' + type + ' key', 'InvalidAccessError');
    const keyAlg = getCryptoKeyAlgorithm(key);
    const saltLength: number = algorithm.saltLength;
    if (keyAlg.name === 'RSA-PSS') {
        const max = Math.ceil(((keyAlg.modulusLength as number) - 1) / 8) - getDigestSizeInBytes(keyAlg.hash.name as string) - 2;
        if (saltLength < 0 || saltLength > max) {
            throw lazyDOMException('The operation failed for an operation-specific reason', 'OperationError');
        }
    }
    const handle = getCryptoKeyHandle(key);
    const hash = nodeHashName(keyAlg.hash.name as string);
    const bytes = copyBufferSource(data);
    const sig = isSign ? undefined : copyBufferSource(signature);
    const pss = keyAlg.name === 'RSA-PSS';
    const options: any = { key: handle.keyObject() };
    if (pss) {
        options.padding = constants.RSA_PKCS1_PSS_PADDING;
        options.saltLength = saltLength;
    }
    return job((): any => {
        if (isSign) return toArrayBuffer(cryptoSign(hash, bytes, options));
        return cryptoVerify(hash, bytes, options, sig as Uint8Array);
    });
}

// ---- EC ----

const kNodeCurveNames: any = { 'P-256': 'prime256v1', 'P-384': 'secp384r1', 'P-521': 'secp521r1' };

export function ecGenerateKey(algorithm: any, extractable: boolean, usages: string[]): Promise<any> {
    const name: string = algorithm.name;
    const namedCurve: string = algorithm.namedCurve;
    const allowed = pairUsagesOf(name);
    const set = validateKeyUsages(usages, allowed.keygen, name);
    const keyAlgorithm: any = { name, namedCurve };
    const pubUsages = usagesUnion(set, allowed.publicUsages);
    const privUsages = validateUsagesNotEmpty(usagesUnion(set, allowed.privateUsages));
    return job((): any => {
        const [pub, priv] = genPair('ec', { namedCurve: kNodeCurveNames[namedCurve] });
        return pairResult(newCryptoKey(pub, keyAlgorithm, usageMask(pubUsages), true),
            newCryptoKey(priv, keyAlgorithm, usageMask(privUsages), extractable));
    });
}

export function ecExportKey(key: CryptoKey, format: string): Uint8Array | undefined {
    const h = getCryptoKeyHandle(key);
    if (format === 'raw') return h.ecRaw();
    if (format === 'spki') return h.toSpki();
    if (format === 'pkcs8') return h.toPkcs8();
    return undefined;
}

export function ecImportKey(format: string, keyData: any, algorithm: any, extractable: boolean, usages: string[]): CryptoKey | undefined {
    const name: string = algorithm.name;
    const namedCurve: string = algorithm.namedCurve;
    const allowed = pairUsagesOf(name);
    const set = uniqueUsages(usages);
    let handle: KeyHandle;
    if (format === 'spki') {
        verifyAcceptableKeyUse(name, set, allowed.publicUsages);
        handle = importDerKey(keyData, true);
    } else if (format === 'pkcs8') {
        verifyAcceptableKeyUse(name, set, allowed.privateUsages);
        handle = importDerKey(keyData, false);
    } else if (format === 'jwk') {
        validateJwk(keyData, 'EC', extractable, set, name === 'ECDH' ? 'enc' : 'sig');
        if (keyData.crv !== namedCurve) throw lazyDOMException('JWK "crv" does not match the requested algorithm', 'DataError');
        if (name === 'ECDSA' && keyData.alg !== undefined) {
            let algCurve = '';
            if (keyData.alg === 'ES256') algCurve = 'P-256';
            else if (keyData.alg === 'ES384') algCurve = 'P-384';
            else if (keyData.alg === 'ES512') algCurve = 'P-521';
            if (algCurve !== namedCurve) throw lazyDOMException('JWK "alg" does not match the requested algorithm', 'DataError');
        }
        const isPublic = keyData.d === undefined;
        verifyAcceptableKeyUse(name, set, isPublic ? allowed.publicUsages : allowed.privateUsages);
        handle = importJwkKey('EC', isPublic, keyData);
    } else if (format === 'raw') {
        verifyAcceptableKeyUse(name, set, allowed.publicUsages);
        const h = ecKeyFromRaw(namedCurve, copyBufferSource(keyData));
        if (h === null) throw invalidKeyData();
        handle = h;
    } else {
        return undefined;
    }
    if (handle.asym !== 'ec') throw lazyDOMException('Invalid key type', 'DataError');
    if (!checkEcKeyData(handle)) throw lazyDOMException('Invalid keyData', 'DataError');
    if (handle.curveName() !== namedCurve) throw lazyDOMException('Named curve mismatch', 'DataError');
    return newCryptoKey(handle, { name, namedCurve }, usageMask(set), extractable);
}

export function ecdsaSignVerify(key: CryptoKey, data: any, algorithm: any, signature: any): Promise<any> {
    const isSign = signature === undefined;
    const type = isSign ? 'private' : 'public';
    if (getCryptoKeyType(key) !== type) throw lazyDOMException('Key must be a ' + type + ' key', 'InvalidAccessError');
    const handle = getCryptoKeyHandle(key);
    const hash = nodeHashName(algorithm.hash.name as string);
    const bytes = copyBufferSource(data);
    const sig = isSign ? undefined : copyBufferSource(signature);
    const options = { key: handle.keyObject(), dsaEncoding: 'ieee-p1363' };
    return job((): any => {
        if (isSign) return toArrayBuffer(cryptoSign(hash, bytes, options as any));
        return cryptoVerify(hash, bytes, options as any, sig as Uint8Array);
    });
}

// ---- CFRG (Ed25519, X25519) ----

export function cfrgGenerateKey(algorithm: any, extractable: boolean, usages: string[]): Promise<any> {
    const name: string = algorithm.name;
    const allowed = pairUsagesOf(name);
    const set = validateKeyUsages(usages, allowed.keygen, name);
    const keyAlgorithm: any = { name };
    const pubUsages = usagesUnion(set, allowed.publicUsages);
    const privUsages = validateUsagesNotEmpty(usagesUnion(set, allowed.privateUsages));
    return job((): any => {
        const [pub, priv] = genPair(name === 'Ed25519' ? 'ed25519' : 'x25519', {});
        return pairResult(newCryptoKey(pub, keyAlgorithm, usageMask(pubUsages), true),
            newCryptoKey(priv, keyAlgorithm, usageMask(privUsages), extractable));
    });
}

export function cfrgExportKey(key: CryptoKey, format: string): Uint8Array | undefined {
    const h = getCryptoKeyHandle(key);
    if (format === 'raw') return h.okpRaw(getCryptoKeyType(key) === 'private');
    if (format === 'spki') return h.toSpki();
    if (format === 'pkcs8') return h.toPkcs8();
    return undefined;
}

export function cfrgImportKey(format: string, keyData: any, algorithm: any, extractable: boolean, usages: string[]): CryptoKey | undefined {
    const name: string = algorithm.name;
    const allowed = pairUsagesOf(name);
    const set = uniqueUsages(usages);
    let handle: KeyHandle;
    if (format === 'spki') {
        verifyAcceptableKeyUse(name, set, allowed.publicUsages);
        handle = importDerKey(keyData, true);
    } else if (format === 'pkcs8') {
        verifyAcceptableKeyUse(name, set, allowed.privateUsages);
        handle = importDerKey(keyData, false);
    } else if (format === 'jwk') {
        validateJwk(keyData, 'OKP', extractable, set, name === 'X25519' ? 'enc' : 'sig');
        if (keyData.crv !== name) throw lazyDOMException('JWK "crv" Parameter and algorithm name mismatch', 'DataError');
        if (keyData.alg !== undefined && name === 'Ed25519') {
            if (keyData.alg !== name && keyData.alg !== 'EdDSA') throw lazyDOMException('JWK "alg" does not match the requested algorithm', 'DataError');
        }
        const isPublic = keyData.d === undefined;
        verifyAcceptableKeyUse(name, set, isPublic ? allowed.publicUsages : allowed.privateUsages);
        handle = importJwkKey('OKP', isPublic, keyData);
    } else if (format === 'raw') {
        verifyAcceptableKeyUse(name, set, allowed.publicUsages);
        const raw = copyBufferSource(keyData);
        if (raw.length !== 32) throw invalidKeyData();
        const h = okpKeyFromRaw(name === 'Ed25519' ? 'ed25519' : 'x25519', raw);
        if (h === null) throw invalidKeyData();
        handle = h;
    } else {
        return undefined;
    }
    if (handle.asym !== name.toLowerCase()) throw lazyDOMException('Invalid key type', 'DataError');
    return newCryptoKey(handle, { name }, usageMask(set), extractable);
}

export function eddsaSignVerify(key: CryptoKey, data: any, algorithm: any, signature: any): Promise<any> {
    const isSign = signature === undefined;
    const type = isSign ? 'private' : 'public';
    if (getCryptoKeyType(key) !== type) throw lazyDOMException('Key must be a ' + type + ' key', 'InvalidAccessError');
    const handle = getCryptoKeyHandle(key);
    const bytes = copyBufferSource(data);
    const sig = isSign ? undefined : copyBufferSource(signature);
    return job((): any => {
        if (isSign) return toArrayBuffer(cryptoSign(null, bytes, handle.keyObject()));
        return cryptoVerify(null, bytes, handle.keyObject(), sig as Uint8Array);
    });
}

// ---- ECDH / X25519 ----

export function ecdhDeriveBits(algorithm: any, baseKey: CryptoKey, length: number | null): Promise<ArrayBuffer> {
    const key: CryptoKey = algorithm.public;
    if (getCryptoKeyType(baseKey) !== 'private') throw lazyDOMException('baseKey must be a private key', 'InvalidAccessError');
    const keyAlgorithm = getCryptoKeyAlgorithm(key);
    const baseKeyAlgorithm = getCryptoKeyAlgorithm(baseKey);
    if (keyAlgorithm.name !== baseKeyAlgorithm.name) {
        throw lazyDOMException('The public and private keys must be of the same type', 'InvalidAccessError');
    }
    if (keyAlgorithm.name === 'ECDH' && keyAlgorithm.namedCurve !== baseKeyAlgorithm.namedCurve) {
        throw lazyDOMException('Named curve mismatch', 'InvalidAccessError');
    }
    const pub = getCryptoKeyHandle(key);
    const priv = getCryptoKeyHandle(baseKey);
    // The secret crosses the job as an ArrayBuffer: a Uint8Array returned
    // through an `any` promise and read by a typed callback loses its length.
    const bits = job((): any => toArrayBuffer(diffieHellman({ privateKey: priv.keyObject(), publicKey: pub.keyObject() })));
    return chain(bits, (boxed: any): ArrayBuffer => {
        const buf = boxed as ArrayBuffer;
        const secret = new Uint8Array(buf);
        if (length === null) return buf;
        const sliceLength = numBitsToBytes(length);
        if (secret.length < sliceLength) throw lazyDOMException('derived bit length is too small', 'OperationError');
        if (length % 8 === 0) return toArrayBuffer(secret.subarray(0, sliceLength));
        return toArrayBuffer(truncateToBitLength(length, secret));
    });
}

// ---- JWK export ----

export function exportJwkAlg(algorithm: any): string | undefined {
    switch (algorithm.name as string) {
        case 'RSASSA-PKCS1-v1_5': return jwkHashAlg(kJwkRsa, algorithm.hash.name as string);
        case 'RSA-PSS': return jwkHashAlg(kJwkRsaPss, algorithm.hash.name as string);
        case 'RSA-OAEP': return jwkHashAlg(kJwkRsaOaep, algorithm.hash.name as string);
        case 'Ed25519': return 'Ed25519';
        case 'AES-CTR':
        case 'AES-CBC':
        case 'AES-GCM':
        case 'AES-KW': return aesAlgorithmName(algorithm.name as string, algorithm.length as number);
        case 'HMAC': return jwkHashAlg(kJwkHmac, algorithm.hash.name as string);
    }
    return undefined;
}

export function exportKeyJwkOf(key: CryptoKey, usages: string[], extractable: boolean): any {
    const algorithm = getCryptoKeyAlgorithm(key);
    const alg = exportJwkAlg(algorithm);
    const params: any = { key_ops: usages.slice(), ext: extractable, alg };
    if (alg === undefined) delete params.alg;
    return exportHandleJwk(getCryptoKeyHandle(key), params);
}
