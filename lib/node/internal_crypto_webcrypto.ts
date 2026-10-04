// kml:global
// Node's lib/internal/crypto/webcrypto.js: SubtleCrypto, over the algorithm
// files in internal_crypto_webcrypto_algs.ts and the converters in
// internal_crypto_webcrypto_webidl.ts. `crypto.subtle` is the one
// SubtleCrypto; each method returns a promise and rejects with the errors
// (TypeError or DOMException) Node 24 gives.
//
// Not ported: subtle.supports, getPublicKey, the encapsulate/decapsulate
// methods, and the algorithms Node marks experimental (SHA-3, cSHAKE, Ed448,
// X448, ML-DSA, ML-KEM, KMAC, Argon2, AES-OCB, ChaCha20-Poly1305).
import { CryptoKey, getCryptoKeyAlgorithm, getCryptoKeyExtractable, getCryptoKeyHandle, getCryptoKeyType, getCryptoKeyUsages,
    getCryptoKeyUsagesMask, hasCryptoKeyUsage, lazyDOMException } from './internal_crypto_webcrypto_keys';
import {
    convAlgorithmIdentifier, convBufferSource, convCryptoKey, convBoolean, convJsonWebKey, convKeyFormat, convKeyUsages,
    convUnsignedLong, copyBufferSource, normalizeAlgorithm, requiredArguments,
} from './internal_crypto_webcrypto_webidl';
import * as A from './internal_crypto_webcrypto_algs';

export { CryptoKey } from './internal_crypto_webcrypto_keys';

const kArgumentContexts = ['1st argument', '2nd argument', '3rd argument', '4th argument', '5th argument', '6th argument', '7th argument'];

function illegalConstructor(): TypeError {
    const e: any = new TypeError('Illegal constructor');
    e.code = 'ERR_ILLEGAL_CONSTRUCTOR';
    return e;
}

function invalidThis(): TypeError {
    const e: any = new TypeError('Value of "this" must be of type SubtleCrypto');
    e.code = 'ERR_INVALID_THIS';
    return e;
}

let subtleObj: SubtleCrypto | null = null;
let creatingSubtle = false;

// Synchronous failures reject, and a synchronous result resolves.
function callSubtle(fn: () => any): Promise<any> {
    try {
        const result = fn();
        if (result instanceof Promise) return result as Promise<any>;
        return Promise.resolve(result);
    } catch (err) {
        return Promise.reject(err);
    }
}

function prepare(receiver: any, method: string, argLength: number, required: number): string {
    if (receiver !== subtleObj) throw invalidThis();
    const prefix = "Failed to execute '" + method + "' on 'SubtleCrypto'";
    requiredArguments(argLength, required, { prefix });
    return prefix;
}

function convertArg(prefix: string, converter: (v: any, opts: any) => any, value: any, index: number): any {
    return converter(value, { prefix, context: kArgumentContexts[index] });
}

function validateMaxBufferLength(data: any, name: string): void {
    if ((data.byteLength as number) > 2147483647) {
        throw lazyDOMException(name + ' must be at most 2147483647 bytes', 'OperationError');
    }
}

// ---- generateKey ----

function generateKeyImpl(self: any, args: any[]): any {
    const prefix = prepare(self, 'generateKey', args.length, 3);
    const algorithm = convertArg(prefix, convAlgorithmIdentifier, args[0], 0);
    const extractable: boolean = convertArg(prefix, convBoolean, args[1], 1);
    const usages: string[] = convertArg(prefix, convKeyUsages, args[2], 2);
    const n = normalizeAlgorithm(algorithm, 'generateKey');
    switch (n.name as string) {
        case 'RSASSA-PKCS1-v1_5':
        case 'RSA-PSS':
        case 'RSA-OAEP':
            return A.rsaKeyGenerate(n, extractable, usages);
        case 'Ed25519':
        case 'X25519':
            return A.cfrgGenerateKey(n, extractable, usages);
        case 'ECDSA':
        case 'ECDH':
            return A.ecGenerateKey(n, extractable, usages);
        case 'HMAC':
            return A.hmacGenerateKey(n, extractable, usages);
    }
    return A.aesGenerateKey(n, extractable, usages);
}

// ---- deriveBits / deriveKey ----

function deriveBitsOf(n: any, baseKey: CryptoKey, length: number | null): Promise<ArrayBuffer> {
    switch (n.name as string) {
        case 'X25519':
        case 'ECDH':
            return A.ecdhDeriveBits(n, baseKey, length);
        case 'HKDF':
            return A.hkdfDeriveBits(n, baseKey, length);
    }
    return A.pbkdf2DeriveBits(n, baseKey, length);
}

function deriveBitsImpl(self: any, args: any[]): any {
    const prefix = prepare(self, 'deriveBits', args.length, 2);
    const algorithm = convertArg(prefix, convAlgorithmIdentifier, args[0], 0);
    const baseKey: CryptoKey = convertArg(prefix, convCryptoKey, args[1], 1);
    let length: number | null = args[2] === undefined ? null : args[2];
    if (length !== null) length = convertArg(prefix, convUnsignedLong, length, 2);
    const n = normalizeAlgorithm(algorithm, 'deriveBits');
    if (!hasCryptoKeyUsage(baseKey, 'deriveBits')) throw lazyDOMException('baseKey does not have deriveBits usage', 'InvalidAccessError');
    if (getCryptoKeyAlgorithm(baseKey).name !== n.name) throw lazyDOMException('Key algorithm mismatch', 'InvalidAccessError');
    return deriveBitsOf(n, baseKey, length);
}

function getKeyLength(n: any): number | null {
    switch (n.name as string) {
        case 'AES-CTR':
        case 'AES-CBC':
        case 'AES-GCM':
        case 'AES-KW':
            if (n.length !== 128 && n.length !== 192 && n.length !== 256) throw lazyDOMException('Invalid key length', 'OperationError');
            return n.length;
        case 'HMAC':
            if (n.length === undefined) return A.getBlockSize(n.hash.name as string);
            if (typeof n.length === 'number' && n.length !== 0) return n.length;
            throw lazyDOMException('Invalid key length', 'OperationError');
    }
    return null;
}

function deriveKeyImpl(self: any, args: any[]): any {
    const prefix = prepare(self, 'deriveKey', args.length, 5);
    const algorithm = convertArg(prefix, convAlgorithmIdentifier, args[0], 0);
    const baseKey: CryptoKey = convertArg(prefix, convCryptoKey, args[1], 1);
    const derivedKeyType = convertArg(prefix, convAlgorithmIdentifier, args[2], 2);
    const extractable: boolean = convertArg(prefix, convBoolean, args[3], 3);
    const usages: string[] = convertArg(prefix, convKeyUsages, args[4], 4);
    const n = normalizeAlgorithm(algorithm, 'deriveBits');
    const importAlg = normalizeAlgorithm(derivedKeyType, 'importKey');
    const lengthAlg = normalizeAlgorithm(derivedKeyType, 'get key length');
    if (!hasCryptoKeyUsage(baseKey, 'deriveKey')) throw lazyDOMException('baseKey does not have deriveKey usage', 'InvalidAccessError');
    if (getCryptoKeyAlgorithm(baseKey).name !== n.name) throw lazyDOMException('Key algorithm mismatch', 'InvalidAccessError');
    const length = getKeyLength(lengthAlg);
    const secret = deriveBitsOf(n, baseKey, length);
    return A.chain(secret, (bits: any): CryptoKey => importKeySync('raw-secret', bits, importAlg, extractable, usages));
}

// ---- exportKey ----

function exportKeyBytes(key: CryptoKey, format: string): Uint8Array | undefined {
    const name: string = getCryptoKeyAlgorithm(key).name;
    const type = getCryptoKeyType(key);
    if (format === 'spki' || format === 'pkcs8') {
        switch (name) {
            case 'RSASSA-PKCS1-v1_5':
            case 'RSA-PSS':
            case 'RSA-OAEP':
                return A.rsaExportKey(key, format);
            case 'ECDSA':
            case 'ECDH':
                return A.ecExportKey(key, format);
            case 'Ed25519':
            case 'X25519':
                return A.cfrgExportKey(key, format);
        }
        return undefined;
    }
    if (format === 'raw-public' || (format === 'raw' && type === 'public')) {
        switch (name) {
            case 'ECDSA':
            case 'ECDH':
                return A.ecExportKey(key, 'raw');
            case 'Ed25519':
            case 'X25519':
                return A.cfrgExportKey(key, 'raw');
        }
        return undefined;
    }
    switch (name) {
        case 'AES-CTR':
        case 'AES-CBC':
        case 'AES-GCM':
        case 'AES-KW':
        case 'HMAC':
            return getCryptoKeyHandle(key).secret;
    }
    return undefined;
}

function exportKeySync(format: string, key: CryptoKey): any {
    const algorithm = getCryptoKeyAlgorithm(key);
    try {
        normalizeAlgorithm(algorithm, 'exportKey');
    } catch (e) {
        throw lazyDOMException(algorithm.name + ' key export is not supported', 'NotSupportedError');
    }
    if (!getCryptoKeyExtractable(key)) throw lazyDOMException('key is not extractable', 'InvalidAccessError');
    const type = getCryptoKeyType(key);
    let result: any = undefined;
    switch (format) {
        case 'spki':
            if (type === 'public') result = exportKeyBytes(key, format);
            break;
        case 'pkcs8':
            if (type === 'private') result = exportKeyBytes(key, format);
            break;
        case 'jwk':
            result = A.exportKeyJwkOf(key, getCryptoKeyUsages(key), getCryptoKeyExtractable(key));
            break;
        case 'raw-secret':
            if (type === 'secret') result = exportKeyBytes(key, format);
            break;
        case 'raw-public':
            if (type === 'public') result = exportKeyBytes(key, format);
            break;
        case 'raw':
            if (type === 'secret' || type === 'public') result = exportKeyBytes(key, format);
            break;
    }
    if (result === undefined) {
        throw lazyDOMException('Unable to export ' + algorithm.name + ' ' + type + ' key using ' + format + ' format', 'NotSupportedError');
    }
    if (format === 'jwk') return result;
    return A.toArrayBuffer(result as Uint8Array);
}

function exportKeyImpl(self: any, args: any[]): any {
    const prefix = prepare(self, 'exportKey', args.length, 2);
    const format: string = convertArg(prefix, convKeyFormat, args[0], 0);
    const key: CryptoKey = convertArg(prefix, convCryptoKey, args[1], 1);
    return exportKeySync(format, key);
}

// ---- importKey ----

function aliasKeyFormat(format: string, alias: string): string {
    return format === alias ? 'raw' : format;
}

function importKeySync(format: string, keyData: any, algorithm: any, extractable: boolean, usages: string[]): CryptoKey {
    let result: CryptoKey | undefined = undefined;
    switch (algorithm.name as string) {
        case 'RSASSA-PKCS1-v1_5':
        case 'RSA-PSS':
        case 'RSA-OAEP':
            result = A.rsaImportKey(format, keyData, algorithm, extractable, usages);
            break;
        case 'ECDSA':
        case 'ECDH':
            format = aliasKeyFormat(format, 'raw-public');
            result = A.ecImportKey(format, keyData, algorithm, extractable, usages);
            break;
        case 'Ed25519':
        case 'X25519':
            format = aliasKeyFormat(format, 'raw-public');
            result = A.cfrgImportKey(format, keyData, algorithm, extractable, usages);
            break;
        case 'HMAC':
            result = A.macImportKey(format, keyData, algorithm, extractable, usages);
            break;
        case 'AES-CTR':
        case 'AES-CBC':
        case 'AES-GCM':
        case 'AES-KW':
            result = A.aesImportKey(algorithm, format, keyData, extractable, usages);
            break;
        case 'HKDF':
        case 'PBKDF2':
            format = aliasKeyFormat(format, 'raw-secret');
            result = A.importGenericSecretKey(algorithm, format, keyData, extractable, usages);
            break;
    }
    if (result === undefined) {
        throw lazyDOMException('Unable to import ' + algorithm.name + ' using ' + format + ' format', 'NotSupportedError');
    }
    const type = getCryptoKeyType(result);
    if ((type === 'secret' || type === 'private') && getCryptoKeyUsagesMask(result) === 0) {
        throw lazyDOMException('Usages cannot be empty when importing a ' + type + ' key.', 'SyntaxError');
    }
    return result;
}

function importKeyImpl(self: any, args: any[]): any {
    const prefix = prepare(self, 'importKey', args.length, 5);
    const format: string = convertArg(prefix, convKeyFormat, args[0], 0);
    const keyData = convertArg(prefix, format === 'jwk' ? convJsonWebKey : convBufferSource, args[1], 1);
    const algorithm = convertArg(prefix, convAlgorithmIdentifier, args[2], 2);
    const extractable: boolean = convertArg(prefix, convBoolean, args[3], 3);
    const usages: string[] = convertArg(prefix, convKeyUsages, args[4], 4);
    const n = normalizeAlgorithm(algorithm, 'importKey');
    return importKeySync(format, keyData, n, extractable, usages);
}

// ---- wrapKey / unwrapKey ----

function parseJwk(data: ArrayBuffer): any {
    let key: any;
    try {
        const bytes = copyBufferSource(data);
        const json = Buffer.from(bytes).toString('utf8');
        key = convJsonWebKey(JSON.parse(json), {});
    } catch (err) {
        throw lazyDOMException('Invalid wrapped JWK key', { name: 'DataError', cause: err });
    }
    if (key.kty === undefined) throw lazyDOMException('Invalid wrapped JWK key', 'DataError');
    return key;
}

function normalizeWrapAlgorithm(algorithm: any, wrapOp: string, cipherOp: string): any {
    try {
        return normalizeAlgorithm(algorithm, wrapOp);
    } catch (e) {
        return normalizeAlgorithm(algorithm, cipherOp);
    }
}

function wrapKeyImpl(self: any, args: any[]): any {
    const prefix = prepare(self, 'wrapKey', args.length, 4);
    const format: string = convertArg(prefix, convKeyFormat, args[0], 0);
    const key: CryptoKey = convertArg(prefix, convCryptoKey, args[1], 1);
    const wrappingKey: CryptoKey = convertArg(prefix, convCryptoKey, args[2], 2);
    const algorithm = convertArg(prefix, convAlgorithmIdentifier, args[3], 3);
    const n = normalizeWrapAlgorithm(algorithm, 'wrapKey', 'encrypt');
    if (n.name !== getCryptoKeyAlgorithm(wrappingKey).name) throw lazyDOMException('Key algorithm mismatch', 'InvalidAccessError');
    if (!hasCryptoKeyUsage(wrappingKey, 'wrapKey')) throw lazyDOMException('Unable to use this key to wrapKey', 'InvalidAccessError');
    const exported = exportKeySync(format, key);
    let bytes: Uint8Array;
    if (format === 'jwk') {
        let json: string = JSON.stringify(exported);
        if (n.name === 'AES-KW' && json.length % 8 !== 0) json = json + ' '.repeat(8 - (json.length % 8));
        const b = Buffer.from(json, 'utf8');
        bytes = new Uint8Array(b.length);
        bytes.set(b);
    } else {
        bytes = new Uint8Array(exported as ArrayBuffer);
    }
    return cipherOrWrap(true, n, wrappingKey, bytes);
}

function unwrapKeyImpl(self: any, args: any[]): any {
    const prefix = prepare(self, 'unwrapKey', args.length, 7);
    const format: string = convertArg(prefix, convKeyFormat, args[0], 0);
    const wrapped = convertArg(prefix, convBufferSource, args[1], 1);
    const unwrappingKey: CryptoKey = convertArg(prefix, convCryptoKey, args[2], 2);
    const algorithm = convertArg(prefix, convAlgorithmIdentifier, args[3], 3);
    const unwrappedKeyAlgorithm = convertArg(prefix, convAlgorithmIdentifier, args[4], 4);
    const extractable: boolean = convertArg(prefix, convBoolean, args[5], 5);
    const usages: string[] = convertArg(prefix, convKeyUsages, args[6], 6);
    const n = normalizeWrapAlgorithm(algorithm, 'unwrapKey', 'decrypt');
    const keyAlgorithm = normalizeAlgorithm(unwrappedKeyAlgorithm, 'importKey');
    if (n.name !== getCryptoKeyAlgorithm(unwrappingKey).name) throw lazyDOMException('Key algorithm mismatch', 'InvalidAccessError');
    if (!hasCryptoKeyUsage(unwrappingKey, 'unwrapKey')) throw lazyDOMException('Unable to use this key to unwrapKey', 'InvalidAccessError');
    const bytes = cipherOrWrap(false, n, unwrappingKey, wrapped);
    return A.chain(bytes, (out: any): CryptoKey => {
        const keyData: any = format === 'jwk' ? parseJwk(out) : out;
        return importKeySync(format, keyData, keyAlgorithm, extractable, usages);
    });
}

// ---- sign / verify ----

function signVerify(algorithm: any, key: CryptoKey, data: any, signature?: any): Promise<any> {
    const operation = signature !== undefined ? 'verify' : 'sign';
    const n = normalizeAlgorithm(algorithm, operation);
    if (n.name !== getCryptoKeyAlgorithm(key).name) throw lazyDOMException('Key algorithm mismatch', 'InvalidAccessError');
    if (!hasCryptoKeyUsage(key, operation)) throw lazyDOMException('Unable to use this key to ' + operation, 'InvalidAccessError');
    switch (n.name as string) {
        case 'RSA-PSS':
        case 'RSASSA-PKCS1-v1_5':
            return A.rsaSignVerify(key, data, n, signature);
        case 'ECDSA':
            return A.ecdsaSignVerify(key, data, n, signature);
        case 'Ed25519':
            return A.eddsaSignVerify(key, data, n, signature);
    }
    return A.hmacSignVerify(key, data, signature);
}

function signImpl(self: any, args: any[]): any {
    const prefix = prepare(self, 'sign', args.length, 3);
    const algorithm = convertArg(prefix, convAlgorithmIdentifier, args[0], 0);
    const key: CryptoKey = convertArg(prefix, convCryptoKey, args[1], 1);
    const data = convertArg(prefix, convBufferSource, args[2], 2);
    return signVerify(algorithm, key, data);
}

function verifyImpl(self: any, args: any[]): any {
    const prefix = prepare(self, 'verify', args.length, 4);
    const algorithm = convertArg(prefix, convAlgorithmIdentifier, args[0], 0);
    const key: CryptoKey = convertArg(prefix, convCryptoKey, args[1], 1);
    const signature = convertArg(prefix, convBufferSource, args[2], 2);
    const data = convertArg(prefix, convBufferSource, args[3], 3);
    return signVerify(algorithm, key, data, signature);
}

// ---- encrypt / decrypt ----

function cipherOrWrap(encrypt: boolean, n: any, key: CryptoKey, data: any): Promise<ArrayBuffer> {
    validateMaxBufferLength(data, 'data');
    if (n.name === 'RSA-OAEP') return A.rsaCipher(encrypt, key, data, n);
    return A.aesCipher(encrypt, key, data, n);
}

function encryptImpl(self: any, args: any[]): any {
    const prefix = prepare(self, 'encrypt', args.length, 3);
    const algorithm = convertArg(prefix, convAlgorithmIdentifier, args[0], 0);
    const key: CryptoKey = convertArg(prefix, convCryptoKey, args[1], 1);
    const data = convertArg(prefix, convBufferSource, args[2], 2);
    const n = normalizeAlgorithm(algorithm, 'encrypt');
    if (n.name !== getCryptoKeyAlgorithm(key).name) throw lazyDOMException('Key algorithm mismatch', 'InvalidAccessError');
    if (!hasCryptoKeyUsage(key, 'encrypt')) throw lazyDOMException('Unable to use this key to encrypt', 'InvalidAccessError');
    return cipherOrWrap(true, n, key, data);
}

function decryptImpl(self: any, args: any[]): any {
    const prefix = prepare(self, 'decrypt', args.length, 3);
    const algorithm = convertArg(prefix, convAlgorithmIdentifier, args[0], 0);
    const key: CryptoKey = convertArg(prefix, convCryptoKey, args[1], 1);
    const data = convertArg(prefix, convBufferSource, args[2], 2);
    const n = normalizeAlgorithm(algorithm, 'decrypt');
    if (n.name !== getCryptoKeyAlgorithm(key).name) throw lazyDOMException('Key algorithm mismatch', 'InvalidAccessError');
    if (!hasCryptoKeyUsage(key, 'decrypt')) throw lazyDOMException('Unable to use this key to decrypt', 'InvalidAccessError');
    return cipherOrWrap(false, n, key, data);
}

// ---- digest ----

function digestImpl(self: any, args: any[]): any {
    const prefix = prepare(self, 'digest', args.length, 2);
    const algorithm = convertArg(prefix, convAlgorithmIdentifier, args[0], 0);
    const data = convertArg(prefix, convBufferSource, args[1], 1);
    const n = normalizeAlgorithm(algorithm, 'digest');
    validateMaxBufferLength(data, 'data');
    return A.digestJob(n.name as string, copyBufferSource(data));
}

// The SubtleCrypto and Crypto classes are defined as part of the Web Crypto
// API standard: https://www.w3.org/TR/WebCryptoAPI/
export class SubtleCrypto {
    constructor() {
        if (!creatingSubtle) throw illegalConstructor();
    }

    encrypt(algorithm: any, key: CryptoKey, data: any): Promise<ArrayBuffer>;
    encrypt(...args: any[]): Promise<any> {
        return callSubtle(() => encryptImpl(this, args));
    }

    decrypt(algorithm: any, key: CryptoKey, data: any): Promise<ArrayBuffer>;
    decrypt(...args: any[]): Promise<any> {
        return callSubtle(() => decryptImpl(this, args));
    }

    sign(algorithm: any, key: CryptoKey, data: any): Promise<ArrayBuffer>;
    sign(...args: any[]): Promise<any> {
        return callSubtle(() => signImpl(this, args));
    }

    verify(algorithm: any, key: CryptoKey, signature: any, data: any): Promise<boolean>;
    verify(...args: any[]): Promise<any> {
        return callSubtle(() => verifyImpl(this, args));
    }

    digest(algorithm: any, data: any): Promise<ArrayBuffer>;
    digest(...args: any[]): Promise<any> {
        return callSubtle(() => digestImpl(this, args));
    }

    generateKey(algorithm: any, extractable: boolean, keyUsages: string[]): Promise<any>;
    generateKey(...args: any[]): Promise<any> {
        return callSubtle(() => generateKeyImpl(this, args));
    }

    deriveKey(algorithm: any, baseKey: CryptoKey, derivedKeyType: any, extractable: boolean, keyUsages: string[]): Promise<CryptoKey>;
    deriveKey(...args: any[]): Promise<any> {
        return callSubtle(() => deriveKeyImpl(this, args));
    }

    deriveBits(algorithm: any, baseKey: CryptoKey, length?: number | null): Promise<ArrayBuffer>;
    deriveBits(...args: any[]): Promise<any> {
        return callSubtle(() => deriveBitsImpl(this, args));
    }

    importKey(format: string, keyData: any, algorithm: any, extractable: boolean, keyUsages: string[]): Promise<CryptoKey>;
    importKey(...args: any[]): Promise<any> {
        return callSubtle(() => importKeyImpl(this, args));
    }

    exportKey(format: string, key: CryptoKey): Promise<any>;
    exportKey(...args: any[]): Promise<any> {
        return callSubtle(() => exportKeyImpl(this, args));
    }

    wrapKey(format: string, key: CryptoKey, wrappingKey: CryptoKey, wrapAlgorithm: any): Promise<ArrayBuffer>;
    wrapKey(...args: any[]): Promise<any> {
        return callSubtle(() => wrapKeyImpl(this, args));
    }

    unwrapKey(format: string, wrappedKey: any, unwrappingKey: CryptoKey, unwrapAlgorithm: any, unwrappedKeyAlgorithm: any,
              extractable: boolean, keyUsages: string[]): Promise<CryptoKey>;
    unwrapKey(...args: any[]): Promise<any> {
        return callSubtle(() => unwrapKeyImpl(this, args));
    }

    get [Symbol.toStringTag](): string {
        return 'SubtleCrypto';
    }
}

export function _kmlSubtle(): SubtleCrypto {
    if (subtleObj === null) {
        creatingSubtle = true;
        subtleObj = new SubtleCrypto();
        creatingSubtle = false;
    }
    return subtleObj;
}
