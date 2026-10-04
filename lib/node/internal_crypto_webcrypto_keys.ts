// Key material for Web Crypto: Node's KeyObjectHandle (the native side of a
// CryptoKey) over lib/node/crypto.ts's KeyObject, with the DER/JWK/raw
// conversions; the CryptoKey class (lib/internal/crypto/keys.js) and the
// usage-mask helpers of lib/internal/crypto/util.js.
import { inspect } from './internal_util_inspect';
import { NodeTypeError } from './internal_errors';
import { createECDH, createPrivateKey, createPublicKey, createSecretKey, ECDH, KeyObject } from './crypto';
import type { AsymmetricKeyDetails } from './crypto';

// Node's lazyDOMException: a DOMException with a name, and a cause when the
// second argument is { name, cause }.
export function lazyDOMException(message: string, nameOrOptions: any): DOMException {
    if (typeof nameOrOptions === 'string') return new DOMException(message, nameOrOptions as string);
    const e: any = new DOMException(message, nameOrOptions.name as string);
    // Node defines `cause` non-enumerable; Object.defineProperty does not accept
    // an Error here, so it is assigned.
    if (nameOrOptions.cause !== undefined) e.cause = nameOrOptions.cause;
    return e;
}

export function operationError(cause?: any): DOMException {
    return lazyDOMException('The operation failed for an operation-specific reason', { name: 'OperationError', cause });
}

// ---- usages ----

const kCanonicalUsageOrder: string[] = [
    'encrypt', 'decrypt', 'sign', 'verify', 'deriveKey', 'deriveBits', 'wrapKey', 'unwrapKey',
    'encapsulateKey', 'encapsulateBits', 'decapsulateKey', 'decapsulateBits',
];

export function usageMask(usages: string[]): number {
    let mask = 0;
    for (let i = 0; i < usages.length; i++) {
        const at = kCanonicalUsageOrder.indexOf(usages[i]);
        if (at >= 0) mask |= 1 << at;
    }
    return mask;
}

export function usagesFromMask(mask: number): string[] {
    const out: string[] = [];
    for (let n = 0; n < kCanonicalUsageOrder.length; n++) {
        if ((mask & (1 << n)) !== 0) out.push(kCanonicalUsageOrder[n]);
    }
    return out;
}

export function uniqueUsages(usages: string[]): string[] {
    const out: string[] = [];
    for (let i = 0; i < usages.length; i++) if (out.indexOf(usages[i]) < 0) out.push(usages[i]);
    return out;
}

function hasAnyNotIn(set: string[], checks: string[]): boolean {
    for (let i = 0; i < set.length; i++) if (checks.indexOf(set[i]) < 0) return true;
    return false;
}

export function verifyAcceptableKeyUse(subject: string, usages: string[], allowed: string[]): void {
    if (hasAnyNotIn(usages, allowed)) {
        throw lazyDOMException('Unsupported key usage for ' + subject + ' key', 'SyntaxError');
    }
}

// Validates usages against `allowed` and returns them as a set.
export function validateKeyUsages(usages: string[], allowed: string[], subject: string): string[] {
    const set = uniqueUsages(usages);
    verifyAcceptableKeyUse(subject, set, allowed);
    return set;
}

export function validateUsagesNotEmpty(usages: string[]): string[] {
    if (usages.length === 0) throw lazyDOMException('Usages cannot be empty when creating a key.', 'SyntaxError');
    return usages;
}

export class KeyUsageLists {
    publicUsages: string[];
    privateUsages: string[];
    keygen: string[];
    constructor(publicUsages: string[], privateUsages: string[]) {
        this.publicUsages = publicUsages;
        this.privateUsages = privateUsages;
        this.keygen = publicUsages.concat(privateUsages);
    }
}

export function usagesUnion(set: string[], usages: string[]): string[] {
    const out: string[] = [];
    for (let i = 0; i < usages.length; i++) if (set.indexOf(usages[i]) >= 0) out.push(usages[i]);
    return out;
}

// key_ops validation of a JWK against the requested usages.
export function validateKeyOps(keyOps: any, usages: string[] | undefined): void {
    if (keyOps === undefined) return;
    if (!Array.isArray(keyOps)) {
        const e: any = new NodeTypeError('ERR_INVALID_ARG_TYPE', 'The "keyData.key_ops" property must be an instance of Array.');
        throw e;
    }
    let opsMask = 0;
    for (let n = 0; n < keyOps.length; n++) {
        const at = kCanonicalUsageOrder.indexOf(keyOps[n] as string);
        if (at < 0) continue;
        const m = 1 << at;
        if ((opsMask & m) !== 0) throw lazyDOMException('Duplicate key operation', 'DataError');
        opsMask |= m;
    }
    if (usages !== undefined) {
        const want = usageMask(usages);
        if ((opsMask & want) !== want) throw lazyDOMException('Key operations and usage mismatch', 'DataError');
    }
}

// ---- key handles ----

// The web curve names and node's (OpenSSL's) for each.
const kWebCurve: { [k: string]: string } = { prime256v1: 'P-256', secp384r1: 'P-384', secp521r1: 'P-521' };
const kNodeCurve: { [k: string]: string } = { 'P-256': 'prime256v1', 'P-384': 'secp384r1', 'P-521': 'secp521r1' };

function b64u(b: Uint8Array): string {
    return Buffer.from(b).toString('base64url');
}

function unb64u(s: any): Uint8Array | null {
    if (typeof s !== 'string') return null;
    if (!/^[A-Za-z0-9_-]*$/.test(s as string)) return null;
    return Buffer.from(s as string, 'base64url');
}

// Node's KeyObjectHandle: a secret key's bytes, or an asymmetric KeyObject.
export class KeyHandle {
    // 'secret', 'public' or 'private'.
    kind: string;
    // '', or the KeyObject's asymmetricKeyType ('rsa', 'ec', 'ed25519', ...).
    asym: string;
    secret: Uint8Array;
    key: KeyObject | null;

    constructor(kind: string, asym: string, key: KeyObject | null, secret: Uint8Array) {
        this.kind = kind;
        this.asym = asym;
        this.key = key;
        this.secret = secret;
    }

    static secretKey(bytes: Uint8Array): KeyHandle {
        return new KeyHandle('secret', '', null, Uint8Array.from(bytes));
    }

    static fromKeyObject(k: KeyObject): KeyHandle {
        return new KeyHandle(k.type, k.asymmetricKeyType ?? 'other', k, new Uint8Array(0));
    }

    // From SubjectPublicKeyInfo DER; null when it does not parse.
    static fromSpki(der: Uint8Array): KeyHandle | null {
        try {
            return KeyHandle.fromKeyObject(createPublicKey({ key: Buffer.from(der), format: 'der', type: 'spki' }));
        } catch (e) {
            return null;
        }
    }

    // From PKCS#8 PrivateKeyInfo DER; null when it does not parse.
    static fromPkcs8(der: Uint8Array): KeyHandle | null {
        try {
            return KeyHandle.fromKeyObject(createPrivateKey({ key: Buffer.from(der), format: 'der', type: 'pkcs8' }));
        } catch (e) {
            return null;
        }
    }

    getKeyType(): string {
        return this.kind;
    }

    isPublic(): boolean {
        return this.kind === 'public';
    }

    keyObject(): KeyObject {
        return this.key as KeyObject;
    }

    // The public half's KeyObject (a private key's derived one).
    publicKeyObject(): KeyObject {
        const k = this.key as KeyObject;
        return k.type === 'private' ? createPublicKey(k) : k;
    }

    modulusLength(): number {
        return (this.keyObject().asymmetricKeyDetails as AsymmetricKeyDetails).modulusLength as number;
    }

    // The RSA public exponent's big-endian bytes.
    publicExponent(): Uint8Array {
        const e = (this.keyObject().asymmetricKeyDetails as AsymmetricKeyDetails).publicExponent as bigint;
        let hex = e.toString(16);
        if (hex.length % 2 === 1) hex = '0' + hex;
        return Buffer.from(hex, 'hex');
    }

    // The web name of an EC key's curve ('P-256'), or null for another curve.
    curveName(): string | null {
        const c = (this.keyObject().asymmetricKeyDetails as AsymmetricKeyDetails).namedCurve;
        return c !== undefined && kWebCurve[c as string] !== undefined ? kWebCurve[c as string] : null;
    }

    jwk(): any {
        return (this.key as KeyObject).export({ format: 'jwk' });
    }

    // An EC key's public point, uncompressed.
    ecRaw(): Uint8Array {
        const j: any = this.publicKeyObject().export({ format: 'jwk' });
        const x = Buffer.from(j.x as string, 'base64url');
        const y = Buffer.from(j.y as string, 'base64url');
        const out = new Uint8Array(1 + x.length + y.length);
        out[0] = 4;
        out.set(x, 1);
        out.set(y, 1 + x.length);
        return out;
    }

    // An Ed25519/X25519 key's raw bytes: the private scalar, or the public.
    okpRaw(priv: boolean): Uint8Array {
        const j: any = this.jwk();
        return Buffer.from((priv ? j.d : j.x) as string, 'base64url');
    }

    // SubjectPublicKeyInfo DER (the public half of a private key too).
    toSpki(): Uint8Array {
        return this.publicKeyObject().export({ type: 'spki', format: 'der' });
    }

    toPkcs8(): Uint8Array {
        return (this.key as KeyObject).export({ type: 'pkcs8', format: 'der' });
    }
}

// An EC public key from a raw point (uncompressed or compressed) on the web
// curve; null when the point is not on it.
export function ecKeyFromRaw(curve: string, raw: Uint8Array): KeyHandle | null {
    const nodeCurve = kNodeCurve[curve];
    if (nodeCurve === undefined) return null;
    try {
        const pt = ECDH.convertKey(Buffer.from(raw), nodeCurve, undefined, undefined, 'uncompressed') as Buffer;
        const size = (pt.length - 1) / 2;
        const jwk = { kty: 'EC', crv: curve, x: b64u(pt.subarray(1, 1 + size)), y: b64u(pt.subarray(1 + size)) };
        return KeyHandle.fromKeyObject(createPublicKey({ key: jwk, format: 'jwk' }));
    } catch (e) {
        return null;
    }
}

// An Ed25519/X25519 public key from its raw bytes; null when they are not
// one.
export function okpKeyFromRaw(type: string, raw: Uint8Array): KeyHandle | null {
    try {
        const jwk = { kty: 'OKP', crv: type === 'ed25519' ? 'Ed25519' : 'X25519', x: b64u(raw) };
        return KeyHandle.fromKeyObject(createPublicKey({ key: jwk, format: 'jwk' }));
    } catch (e) {
        return null;
    }
}

// RSA/EC/OKP jwk members in the order Node's native export writes them,
// added to params.
export function exportHandleJwk(h: KeyHandle, params: any): any {
    const out: any = params;
    if (h.kind === 'secret') {
        out.kty = 'oct';
        out.k = b64u(h.secret);
        return out;
    }
    const j: any = h.jwk();
    for (const name of Object.keys(j)) out[name] = j[name];
    return out;
}

// A KeyHandle from a JWK; null when a member is not valid base64url or the
// key does not check.
export function importHandleJwk(kty: string, isPublic: boolean, jwk: any): KeyHandle | null {
    if (kty === 'oct') {
        const k = unb64u(jwk.k);
        if (k === null) return null;
        return KeyHandle.secretKey(k);
    }
    const members = kty === 'RSA' ? (isPublic ? ['n', 'e'] : ['n', 'e', 'd', 'p', 'q', 'dp', 'dq', 'qi'])
        : (kty === 'EC' ? (isPublic ? ['x', 'y'] : ['x', 'y', 'd']) : (isPublic ? ['x'] : ['x', 'd']));
    const key: any = { kty };
    if (kty !== 'RSA') key.crv = jwk.crv;
    for (const m of members) {
        if (unb64u(jwk[m]) === null) return null;
        key[m] = jwk[m];
    }
    try {
        const k = isPublic ? createPublicKey({ key, format: 'jwk' }) : createPrivateKey({ key, format: 'jwk' });
        const h = KeyHandle.fromKeyObject(k);
        if (kty === 'EC' && !checkEcKeyData(h)) return null;
        if (kty === 'OKP' && !isPublic && b64u(okpKeyFromRawOf(h)) !== jwk.x) return null;
        return h;
    } catch (e) {
        return null;
    }
}

// The public bytes an OKP private key derives.
function okpKeyFromRawOf(h: KeyHandle): Uint8Array {
    const j: any = h.publicKeyObject().export({ format: 'jwk' });
    return Buffer.from(j.x as string, 'base64url');
}

// EC key sanity as OpenSSL's EC_KEY_check_key: the point is on the curve
// and, for a private key, is d * G.
export function checkEcKeyData(h: KeyHandle): boolean {
    const curve = h.curveName();
    if (curve === null) return true;
    const raw = h.ecRaw();
    if (ecKeyFromRaw(curve, raw) === null) return false;
    if (h.kind !== 'private') return true;
    try {
        const ecdh = createECDH(kNodeCurve[curve]);
        ecdh.setPrivateKey(Buffer.from((h.jwk() as any).d as string, 'base64url'));
        return Buffer.from(ecdh.getPublicKey()).equals(Buffer.from(raw));
    } catch (e) {
        return false;
    }
}

// ---- CryptoKey ----

class CryptoKeySlots {
    handle: KeyHandle;
    algorithm: any;
    usagesMask: number;
    extractable: boolean;
    // The copies the `algorithm` and `usages` getters hand out: made on first
    // read and kept, as Node does, so a change made to one is seen again.
    clonedAlgorithm: any;
    clonedUsages: string[] | null;
    constructor(handle: KeyHandle, algorithm: any, usagesMask: number, extractable: boolean) {
        this.clonedAlgorithm = undefined;
        this.clonedUsages = null;
        this.handle = handle;
        this.algorithm = algorithm;
        this.usagesMask = usagesMask;
        this.extractable = extractable;
    }
}

let allowCryptoKey = false;
let getSlotsOf: (key: CryptoKey) => CryptoKeySlots;

function illegalConstructor(): TypeError {
    const e: any = new TypeError('Illegal constructor');
    e.code = 'ERR_ILLEGAL_CONSTRUCTOR';
    return e;
}

function invalidThis(name: string): TypeError {
    const e: any = new TypeError('Value of "this" must be of type ' + name);
    e.code = 'ERR_INVALID_THIS';
    return e;
}

function cloneAlgorithm(raw: any): any {
    const out: any = {};
    const keys = Object.keys(raw);
    for (let i = 0; i < keys.length; i++) out[keys[i]] = raw[keys[i]];
    if (out.hash !== undefined) {
        const h: any = {};
        const hk = Object.keys(out.hash);
        for (let i = 0; i < hk.length; i++) h[hk[i]] = out.hash[hk[i]];
        out.hash = h;
    }
    if (out.publicExponent !== undefined) out.publicExponent = new Uint8Array(out.publicExponent as Uint8Array);
    return out;
}

export class CryptoKey {
    #slots: CryptoKeySlots;

    constructor(handle?: any, algorithm?: any, usagesMask?: number, extractable?: boolean) {
        if (!allowCryptoKey) throw illegalConstructor();
        allowCryptoKey = false;
        this.#slots = new CryptoKeySlots(handle as KeyHandle, algorithm, usagesMask as number, extractable as boolean);
    }

    static {
        getSlotsOf = (key: CryptoKey): CryptoKeySlots => {
            if (key === null || typeof key !== 'object' || !(key instanceof CryptoKey)) throw invalidThis('CryptoKey');
            return key.#slots;
        };
    }

    [inspect.custom](depth: number, options: any): any {
        if (depth < 0) return this;
        const opts: any = {};
        const ok = Object.keys(options);
        for (let i = 0; i < ok.length; i++) opts[ok[i]] = options[ok[i]];
        opts.depth = options.depth == null ? null : options.depth - 1;
        return 'CryptoKey ' + inspect({
            type: getCryptoKeyType(this),
            extractable: getCryptoKeyExtractable(this),
            algorithm: cloneAlgorithm(getCryptoKeyAlgorithm(this)),
            usages: getCryptoKeyUsages(this),
        }, opts);
    }

    get type(): string {
        return getCryptoKeyType(this);
    }

    // KeyObject.from(cryptoKey): the key's KeyObject (Node's kKeyObject).
    _kmlKeyObject(): KeyObject {
        const h = getSlotsOf(this).handle;
        return h.kind === 'secret' ? createSecretKey(h.secret) : h.keyObject();
    }

    get extractable(): boolean {
        return getCryptoKeyExtractable(this);
    }

    get algorithm(): any {
        const slots = getSlotsOf(this);
        if (slots.clonedAlgorithm === undefined) slots.clonedAlgorithm = cloneAlgorithm(slots.algorithm);
        return slots.clonedAlgorithm;
    }

    get usages(): string[] {
        const slots = getSlotsOf(this);
        if (slots.clonedUsages === null) slots.clonedUsages = usagesFromMask(slots.usagesMask);
        return slots.clonedUsages;
    }

    get [Symbol.toStringTag](): string {
        return 'CryptoKey';
    }
}

export function newCryptoKey(handle: KeyHandle, algorithm: any, usagesMask: number, extractable: boolean): CryptoKey {
    allowCryptoKey = true;
    return new CryptoKey(handle, algorithm, usagesMask, extractable);
}

export function getCryptoKeyHandle(key: CryptoKey): KeyHandle {
    return getSlotsOf(key).handle;
}

export function getCryptoKeyType(key: CryptoKey): string {
    return getSlotsOf(key).handle.kind;
}

export function getCryptoKeyExtractable(key: CryptoKey): boolean {
    return getSlotsOf(key).extractable;
}

export function getCryptoKeyAlgorithm(key: CryptoKey): any {
    return getSlotsOf(key).algorithm;
}

export function getCryptoKeyUsagesMask(key: CryptoKey): number {
    return getSlotsOf(key).usagesMask;
}

export function getCryptoKeyUsages(key: CryptoKey): string[] {
    return usagesFromMask(getSlotsOf(key).usagesMask);
}

export function hasCryptoKeyUsage(key: CryptoKey, usage: string): boolean {
    return (getSlotsOf(key).usagesMask & usageMask([usage])) !== 0;
}

export function isCryptoKey(v: any): boolean {
    return v instanceof CryptoKey;
}
