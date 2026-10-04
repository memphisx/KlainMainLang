// The WebIDL converters of lib/internal/webidl.js and
// lib/internal/crypto/webidl.js that Web Crypto uses, and normalizeAlgorithm
// of lib/internal/crypto/util.js. Errors carry the messages and codes Node's
// converters give. Algorithms Node 24 marks experimental (SHA-3, Ed448, X448,
// ML-DSA, ML-KEM, KMAC, cSHAKE, Argon2, AES-OCB, ChaCha20-Poly1305,
// TurboSHAKE, KangarooTwelve) are not in the registry here.
import { lazyDOMException, CryptoKey, getCryptoKeyAlgorithm, getCryptoKeyType } from './internal_crypto_webcrypto_keys';

export type Converter = (v: any, opts: any) => any;

export class Member {
    key: string;
    converter: Converter;
    required: boolean;
    validator: ((v: any, dict: any) => void) | null;
    constructor(key: string, converter: Converter, required: boolean, validator: ((v: any, dict: any) => void) | null) {
        this.key = key;
        this.converter = converter;
        this.required = required;
        this.validator = validator;
    }
}

function codedTypeError(message: string, code: string): TypeError {
    const e: any = new TypeError(message);
    e.code = code;
    return e;
}

function makeException(message: string, opts: any): TypeError {
    const prefix = opts.prefix ? opts.prefix + ': ' : '';
    const context = opts.context !== undefined && opts.context.length === 0 ? '' : (opts.context ?? 'Value') + ' ';
    return codedTypeError(prefix + context + message, opts.code || 'ERR_INVALID_ARG_TYPE');
}

function makeOptions(opts: any, context?: string, code?: string): any {
    return {
        prefix: opts.prefix,
        context: context === undefined ? opts.context : context,
        code: code === undefined ? opts.code : code,
        enforceRange: opts.enforceRange,
    };
}

export function webidlType(v: any): string {
    switch (typeof v) {
        case 'undefined': return 'Undefined';
        case 'boolean': return 'Boolean';
        case 'string': return 'String';
        case 'symbol': return 'Symbol';
        case 'number': return 'Number';
        case 'bigint': return 'BigInt';
    }
    if (v === null) return 'Null';
    return 'Object';
}

export function requiredArguments(length: number, required: number, opts: any): void {
    if (length < required) {
        throw makeException(required + ' argument' + (required === 1 ? '' : 's') + ' required, but only ' + length + ' present.',
            makeOptions(opts, '', 'ERR_MISSING_ARGS'));
    }
}

function toStr(v: any, opts: any): string {
    if (typeof v === 'symbol') throw makeException('is a Symbol and cannot be converted to a string.', opts);
    return String(v);
}

export function convDOMString(v: any, opts: any): string {
    return toStr(v, opts);
}

export function convBoolean(v: any, opts: any): boolean {
    return !!v;
}

export function convObject(v: any, opts: any): any {
    if (webidlType(v) !== 'Object') throw makeException('is not an object.', opts);
    return v;
}

function toNum(v: any, opts: any): number {
    if (typeof v === 'bigint') throw makeException('is a BigInt and cannot be converted to a number.', opts);
    if (typeof v === 'symbol') throw makeException('is a Symbol and cannot be converted to a number.', opts);
    return +v;
}

function intConverter(bitLength: number): Converter {
    const upper = bitLength === 8 ? 0xff : (bitLength === 16 ? 0xffff : 0xffffffff);
    return (v: any, opts: any): number => {
        const x0 = typeof v === 'number' ? (v as number) : toNum(v, opts);
        let x = x0 === 0 ? 0 : x0;
        if (opts.enforceRange) {
            if (!isFinite(x)) throw makeException('is not a finite number.', opts);
            x = Math.trunc(x);
            if (x === 0) x = 0;
            if (x < 0 || x > upper) {
                throw makeException('is outside the expected range of 0 to ' + upper + '.', makeOptions(opts, opts.context, 'ERR_OUT_OF_RANGE'));
            }
            return x;
        }
        if (!isFinite(x) || x === 0) return 0;
        x = Math.trunc(x);
        if (x >= 0 && x <= upper) return x;
        const m = Math.pow(2, bitLength);
        let r = x % m;
        if (r < 0) r += m;
        return r;
    };
}

export const convOctet: Converter = intConverter(8);
export const convUnsignedShort: Converter = intConverter(16);
export const convUnsignedLong: Converter = intConverter(32);

function enforceRangeOptions(opts: any): any {
    return { prefix: opts.prefix, context: opts.context, code: opts.code, enforceRange: true };
}

export function createEnumConverter(name: string, values: string[]): Converter {
    return (v: any, opts: any): string => {
        const s = toStr(v, opts);
        if (values.indexOf(s) < 0) {
            throw makeException("'" + s + "' is not a valid enum value of type " + name + '.', makeOptions(opts, opts.context, 'ERR_INVALID_ARG_VALUE'));
        }
        return s;
    };
}

export function createSequenceConverter(converter: Converter): Converter {
    return (v: any, opts: any): any[] => {
        if (webidlType(v) !== 'Object') throw makeException('cannot be converted to sequence.', opts);
        const method = v[Symbol.iterator];
        if (typeof method !== 'function') throw makeException('cannot be converted to sequence.', opts);
        const out: any[] = [];
        let i = 0;
        for (const item of v as any) {
            out.push(converter(item, makeOptions(opts, (opts.context ?? 'Value') + '[' + i + ']')));
            i++;
        }
        return out;
    };
}

function dictionaryMemberContext(key: string, opts: any): string {
    return opts.context ? key + ' in ' + opts.context : key;
}

// A dictionary converter over one member list per dictionary level, each
// level sorted by member name as the spec orders them. `nameOverride`
// replaces the `name` member's source value, as Node converts
// { __proto__: algorithm, name: canonicalName }.
export function createDictionaryConverter(dictionaryName: string, levels: Member[][]): (jsDict: any, opts: any, nameOverride?: string) => any {
    const sorted: Member[][] = [];
    for (let i = 0; i < levels.length; i++) {
        sorted.push(levels[i].slice().sort((a: Member, b: Member) => (a.key === b.key ? 0 : (a.key < b.key ? -1 : 1))));
    }
    return (jsDict: any, opts: any, nameOverride?: string): any => {
        if (jsDict != null && webidlType(jsDict) !== 'Object') {
            throw makeException('cannot be converted to a dictionary', opts);
        }
        const out: any = {};
        for (let i = 0; i < sorted.length; i++) {
            const members = sorted[i];
            for (let j = 0; j < members.length; j++) {
                const m = members[j];
                const raw: any = jsDict == null ? undefined : (m.key === 'name' && nameOverride !== undefined ? nameOverride : jsDict[m.key]);
                if (raw !== undefined) {
                    const value = m.converter(raw, makeOptions(opts, dictionaryMemberContext(m.key, opts)));
                    if (m.validator !== null) m.validator(value, jsDict);
                    out[m.key] = value;
                } else if (m.required) {
                    throw makeException("cannot be converted to '" + dictionaryName + "' because '" + m.key + "' is required in '" +
                        dictionaryName + "'.", makeOptions(opts, opts.context, 'ERR_MISSING_OPTION'));
                }
            }
        }
        return out;
    };
}

function isView(v: any): boolean {
    return ArrayBuffer.isView(v);
}

export function convBufferSource(v: any, opts: any): any {
    if (isView(v)) return v;
    if (v instanceof ArrayBuffer) return v;
    throw makeException('is not instance of ArrayBuffer, Buffer, TypedArray, or DataView.', opts);
}

export function convBigInteger(v: any, opts: any): any {
    if (!isView(v) || !(v instanceof Uint8Array)) throw makeException('is not an Uint8Array object.', opts);
    return v;
}

export function convAlgorithmIdentifier(v: any, opts: any): any {
    if (webidlType(v) === 'Object') return convObject(v, opts);
    return convDOMString(v, opts);
}

export function convCryptoKey(v: any, opts: any): any {
    if (v instanceof CryptoKey) return v;
    throw makeException('is not of type CryptoKey.', opts);
}

export const convKeyFormat: Converter = createEnumConverter('KeyFormat', [
    'raw', 'raw-public', 'raw-seed', 'raw-secret', 'raw-private', 'pkcs8', 'spki', 'jwk',
]);

export const convKeyUsage: Converter = createEnumConverter('KeyUsage', [
    'encrypt', 'decrypt', 'sign', 'verify', 'deriveKey', 'deriveBits', 'wrapKey', 'unwrapKey',
    'encapsulateBits', 'decapsulateBits', 'encapsulateKey', 'decapsulateKey',
]);

export const convKeyUsages: Converter = createSequenceConverter(convKeyUsage);

// ---- validators ----

export function byteLengthOf(v: any): number {
    return (v as any).byteLength as number;
}

function validateByteLength(v: any, name: string, target: number): void {
    if (byteLengthOf(v) !== target) throw lazyDOMException(name + ' must contain exactly ' + target + ' bytes', 'OperationError');
}

function validateMaxBufferLength(v: any, name: string, max: number): void {
    if (byteLengthOf(v) > max) throw lazyDOMException(name + ' must be at most ' + max + ' bytes', 'OperationError');
}

function aesLengthValidator(v: any, dict: any): void {
    if (v !== 128 && v !== 192 && v !== 256) throw lazyDOMException('AES key length must be 128, 192, or 256 bits', 'OperationError');
}

function namedCurveValidator(v: any, dict: any): void {
    if (v !== 'P-256' && v !== 'P-384' && v !== 'P-521') throw lazyDOMException('Unrecognized namedCurve', 'NotSupportedError');
}

function ensureSHA(label: string): (v: any, dict: any) => void {
    return (v: any, dict: any): void => {
        const name = typeof v === 'string' ? v : v.name;
        if (typeof name !== 'string' || (name as string).toLowerCase().indexOf('sha') !== 0) {
            throw lazyDOMException('Only SHA hashes are supported in ' + label, 'NotSupportedError');
        }
    };
}

function enforceUnsignedLong(): Converter {
    return (v: any, opts: any): any => convUnsignedLong(v, enforceRangeOptions(opts));
}

const dictAlgorithm: Member[] = [new Member('name', convDOMString, true, null)];

function hashMember(label: string): Member {
    return new Member('hash', convAlgorithmIdentifier, true, ensureSHA(label));
}

const rsaKeyGen: Member[] = [
    new Member('modulusLength', enforceUnsignedLong(), true, null),
    new Member('publicExponent', convBigInteger, true, null),
];

export const dictionaries: any = {};

dictionaries.Algorithm = createDictionaryConverter('Algorithm', [dictAlgorithm]);
dictionaries.RsaHashedKeyGenParams = createDictionaryConverter('RsaHashedKeyGenParams', [
    dictAlgorithm, rsaKeyGen, [hashMember('RsaHashedKeyGenParams')]]);
dictionaries.RsaHashedImportParams = createDictionaryConverter('RsaHashedImportParams', [
    dictAlgorithm, [hashMember('RsaHashedImportParams')]]);
dictionaries.EcKeyImportParams = createDictionaryConverter('EcKeyImportParams', [
    dictAlgorithm, [new Member('namedCurve', convDOMString, true, namedCurveValidator)]]);
dictionaries.EcKeyGenParams = createDictionaryConverter('EcKeyGenParams', [
    dictAlgorithm, [new Member('namedCurve', convDOMString, true, namedCurveValidator)]]);
dictionaries.AesKeyGenParams = createDictionaryConverter('AesKeyGenParams', [
    dictAlgorithm, [new Member('length', (v: any, o: any): any => convUnsignedShort(v, enforceRangeOptions(o)), true, aesLengthValidator)]]);
dictionaries.AesDerivedKeyParams = createDictionaryConverter('AesDerivedKeyParams', [
    dictAlgorithm, [new Member('length', (v: any, o: any): any => convUnsignedShort(v, enforceRangeOptions(o)), true, aesLengthValidator)]]);
dictionaries.RsaPssParams = createDictionaryConverter('RsaPssParams', [
    dictAlgorithm, [new Member('saltLength', enforceUnsignedLong(), true, null)]]);
dictionaries.RsaOaepParams = createDictionaryConverter('RsaOaepParams', [
    dictAlgorithm, [new Member('label', convBufferSource, false, null)]]);
dictionaries.EcdsaParams = createDictionaryConverter('EcdsaParams', [
    dictAlgorithm, [hashMember('EcdsaParams')]]);

function hmacDict(name: string, zeroError: string): (j: any, o: any, n?: string) => any {
    return createDictionaryConverter(name, [dictAlgorithm, [
        hashMember(name),
        new Member('length', enforceUnsignedLong(), false, (v: any, dict: any): void => {
            if (v === 0) throw lazyDOMException(name + '.length cannot be 0', zeroError);
        }),
    ]]);
}

dictionaries.HmacKeyGenParams = hmacDict('HmacKeyGenParams', 'OperationError');
dictionaries.HmacImportParams = hmacDict('HmacImportParams', 'DataError');

dictionaries.HkdfParams = createDictionaryConverter('HkdfParams', [dictAlgorithm, [
    hashMember('HkdfParams'),
    new Member('salt', convBufferSource, true, null),
    new Member('info', convBufferSource, true, (v: any, d: any): void => validateMaxBufferLength(v, 'algorithm.info', 1024)),
]]);

dictionaries.Pbkdf2Params = createDictionaryConverter('Pbkdf2Params', [dictAlgorithm, [
    new Member('salt', convBufferSource, true, null),
    new Member('iterations', enforceUnsignedLong(), true, (v: any, d: any): void => {
        if (v === 0) throw lazyDOMException('iterations cannot be zero', 'OperationError');
    }),
    hashMember('Pbkdf2Params'),
]]);

dictionaries.AesCbcParams = createDictionaryConverter('AesCbcParams', [dictAlgorithm, [
    new Member('iv', convBufferSource, true, (v: any, d: any): void => validateByteLength(v, 'algorithm.iv', 16)),
]]);

dictionaries.AeadParams = createDictionaryConverter('AeadParams', [dictAlgorithm, [
    new Member('iv', convBufferSource, true, (v: any, d: any): void => {
        if ((d.name as string).toLowerCase() === 'aes-gcm') validateMaxBufferLength(v, 'algorithm.iv', 2147483647);
    }),
    new Member('additionalData', convBufferSource, false, (v: any, d: any): void => validateMaxBufferLength(v, 'algorithm.additionalData', 2147483647)),
    new Member('tagLength', (v: any, o: any): any => convOctet(v, enforceRangeOptions(o)), false, (v: any, d: any): void => {
        if ((d.name as string).toLowerCase() === 'aes-gcm' && [32, 64, 96, 104, 112, 120, 128].indexOf(v as number) < 0) {
            throw lazyDOMException(v + ' is not a valid AES-GCM tag length', 'OperationError');
        }
    }),
]]);

dictionaries.AesCtrParams = createDictionaryConverter('AesCtrParams', [dictAlgorithm, [
    new Member('counter', convBufferSource, true, (v: any, d: any): void => validateByteLength(v, 'algorithm.counter', 16)),
    new Member('length', (v: any, o: any): any => convOctet(v, enforceRangeOptions(o)), true, (v: any, d: any): void => {
        if (v === 0 || v > 128) throw lazyDOMException('AES-CTR algorithm.length must be between 1 and 128', 'OperationError');
    }),
]]);

dictionaries.EcdhKeyDeriveParams = createDictionaryConverter('EcdhKeyDeriveParams', [dictAlgorithm, [
    new Member('public', convCryptoKey, true, (v: any, d: any): void => {
        if (getCryptoKeyType(v as CryptoKey) !== 'public') throw lazyDOMException('algorithm.public must be a public key', 'InvalidAccessError');
        if ((getCryptoKeyAlgorithm(v as CryptoKey).name as string).toLowerCase() !== (d.name as string).toLowerCase()) {
            throw lazyDOMException('key algorithm mismatch', 'InvalidAccessError');
        }
    }),
]]);

const rsaOtherPrimesInfo = createDictionaryConverter('RsaOtherPrimesInfo', [[
    new Member('r', convDOMString, false, null),
    new Member('d', convDOMString, false, null),
    new Member('t', convDOMString, false, null),
]]);

export const convJsonWebKey = createDictionaryConverter('JsonWebKey', [[
    new Member('kty', convDOMString, false, null),
    new Member('use', convDOMString, false, null),
    new Member('key_ops', createSequenceConverter(convDOMString), false, null),
    new Member('alg', convDOMString, false, null),
    new Member('ext', convBoolean, false, null),
    new Member('crv', convDOMString, false, null),
    new Member('x', convDOMString, false, null),
    new Member('y', convDOMString, false, null),
    new Member('d', convDOMString, false, null),
    new Member('n', convDOMString, false, null),
    new Member('e', convDOMString, false, null),
    new Member('p', convDOMString, false, null),
    new Member('q', convDOMString, false, null),
    new Member('dp', convDOMString, false, null),
    new Member('dq', convDOMString, false, null),
    new Member('qi', convDOMString, false, null),
    new Member('oth', createSequenceConverter(rsaOtherPrimesInfo), false, null),
    new Member('k', convDOMString, false, null),
    new Member('pub', convDOMString, false, null),
    new Member('priv', convDOMString, false, null),
]]);

// ---- the algorithm registry ----

// operation -> algorithm -> dictionary name, or '' for none.
const kSupportedAlgorithms: any = {
    'digest': { 'SHA-1': '', 'SHA-256': '', 'SHA-384': '', 'SHA-512': '' },
    'generateKey': {
        'AES-CBC': 'AesKeyGenParams', 'AES-CTR': 'AesKeyGenParams', 'AES-GCM': 'AesKeyGenParams', 'AES-KW': 'AesKeyGenParams',
        'ECDH': 'EcKeyGenParams', 'ECDSA': 'EcKeyGenParams', 'Ed25519': '', 'HMAC': 'HmacKeyGenParams',
        'RSA-OAEP': 'RsaHashedKeyGenParams', 'RSA-PSS': 'RsaHashedKeyGenParams', 'RSASSA-PKCS1-v1_5': 'RsaHashedKeyGenParams',
        'X25519': '',
    },
    'exportKey': {
        'AES-CBC': '', 'AES-CTR': '', 'AES-GCM': '', 'AES-KW': '', 'ECDH': '', 'ECDSA': '', 'Ed25519': '', 'HMAC': '',
        'RSA-OAEP': '', 'RSA-PSS': '', 'RSASSA-PKCS1-v1_5': '', 'X25519': '',
    },
    'importKey': {
        'AES-CBC': '', 'AES-CTR': '', 'AES-GCM': '', 'AES-KW': '', 'ECDH': 'EcKeyImportParams', 'ECDSA': 'EcKeyImportParams',
        'Ed25519': '', 'HKDF': '', 'HMAC': 'HmacImportParams', 'PBKDF2': '', 'RSA-OAEP': 'RsaHashedImportParams',
        'RSA-PSS': 'RsaHashedImportParams', 'RSASSA-PKCS1-v1_5': 'RsaHashedImportParams', 'X25519': '',
    },
    'encrypt': { 'AES-CBC': 'AesCbcParams', 'AES-CTR': 'AesCtrParams', 'AES-GCM': 'AeadParams', 'RSA-OAEP': 'RsaOaepParams' },
    'decrypt': { 'AES-CBC': 'AesCbcParams', 'AES-CTR': 'AesCtrParams', 'AES-GCM': 'AeadParams', 'RSA-OAEP': 'RsaOaepParams' },
    'sign': { 'ECDSA': 'EcdsaParams', 'Ed25519': '', 'HMAC': '', 'RSA-PSS': 'RsaPssParams', 'RSASSA-PKCS1-v1_5': '' },
    'verify': { 'ECDSA': 'EcdsaParams', 'Ed25519': '', 'HMAC': '', 'RSA-PSS': 'RsaPssParams', 'RSASSA-PKCS1-v1_5': '' },
    'deriveBits': { 'ECDH': 'EcdhKeyDeriveParams', 'HKDF': 'HkdfParams', 'PBKDF2': 'Pbkdf2Params', 'X25519': 'EcdhKeyDeriveParams' },
    'get key length': {
        'AES-CBC': 'AesDerivedKeyParams', 'AES-CTR': 'AesDerivedKeyParams', 'AES-GCM': 'AesDerivedKeyParams',
        'AES-KW': 'AesDerivedKeyParams', 'HKDF': '', 'HMAC': 'HmacImportParams', 'PBKDF2': '',
    },
    'wrapKey': { 'AES-KW': '' },
    'unwrapKey': { 'AES-KW': '' },
};

// The BufferSource and hash members of each dictionary, which normalizeAlgorithm
// copies or normalizes.
const kBufferMembers: any = {
    AesCbcParams: ['iv'], AesCtrParams: ['counter'], AeadParams: ['iv', 'additionalData'],
    RsaHashedKeyGenParams: ['publicExponent'], HkdfParams: ['salt', 'info'], Pbkdf2Params: ['salt'],
    RsaOaepParams: ['label'],
};

const kHashMembers: any = {
    RsaHashedKeyGenParams: true, HmacKeyGenParams: true, EcdsaParams: true, HmacImportParams: true, HkdfParams: true,
    Pbkdf2Params: true, RsaHashedImportParams: true,
};

const kNormalizeAlgorithmOpts: any = { prefix: 'Failed to normalize algorithm', context: 'passed algorithm' };

function canonicalName(op: string, name: string): string | undefined {
    const table = kSupportedAlgorithms[op];
    if (table === undefined) return undefined;
    const upper = name.toUpperCase();
    const names = Object.keys(table);
    for (let i = 0; i < names.length; i++) if (names[i].toUpperCase() === upper) return names[i];
    return undefined;
}

// Copies the bytes a BufferSource views. A boxed typed array's `.buffer` and
// `.byteOffset` are not readable dynamically, so a Uint8Array is copied
// directly and any other view through the native byte copy crypto.ts uses.
export function copyBufferSource(v: any): Uint8Array {
    if (v instanceof ArrayBuffer) {
        const src = new Uint8Array(v as ArrayBuffer);
        const out = new Uint8Array(src.length);
        out.set(src);
        return out;
    }
    if (v instanceof Uint8Array) return (v as Uint8Array).slice();
    if (v instanceof DataView) {
        const dv = v as DataView;
        const view = new Uint8Array(dv.buffer, dv.byteOffset, dv.byteLength);
        const out = new Uint8Array(view.length);
        out.set(view);
        return out;
    }
    const out = new Uint8Array(v.byteLength as number);
    __kml_native.typedBytes(v, out);
    return out;
}

// https://w3c.github.io/webcrypto/#algorithm-normalization-normalize-an-algorithm
export function normalizeAlgorithm(algorithm: any, op: string): any {
    if (typeof algorithm === 'string') return normalizeAlgorithm({ name: algorithm }, op);
    const registered = kSupportedAlgorithms[op];
    const initial = dictionaries.Algorithm(algorithm, kNormalizeAlgorithmOpts);
    const algName = canonicalName(op, initial.name as string);
    if (algName === undefined) throw lazyDOMException('Unrecognized algorithm name', 'NotSupportedError');
    const desired: string = registered[algName];
    if (desired === '') return { name: algName };
    const normalized: any = dictionaries[desired](algorithm, kNormalizeAlgorithmOpts, algName);
    normalized.name = algName;
    const buffers: string[] | undefined = kBufferMembers[desired];
    if (buffers !== undefined) {
        for (let i = 0; i < buffers.length; i++) {
            const m = buffers[i];
            const v = normalized[m];
            if (v) normalized[m] = copyBufferSource(v);
        }
    }
    if (kHashMembers[desired] === true) normalized.hash = normalizeAlgorithm(normalized.hash, 'digest');
    return normalized;
}
