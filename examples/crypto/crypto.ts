// Web Crypto — crypto.getRandomValues / crypto.randomUUID (in-house CSPRNG:
// arc4random_buf on macOS/BSD, getrandom() on Linux) and crypto.subtle
// (Node's own Web Crypto implementation, written over node:crypto).

// ── crypto.getRandomValues(view) ────────────────────────────────────────────
// Fills a TypedArray's (or ArrayBuffer's) bytes in place, per the real API.
const bytes = new Uint8Array(16)
crypto.getRandomValues(bytes)
console.log(bytes.length)   // 16

let anyNonZero = false
for (let i = 0; i < bytes.length; i++) {
    if (bytes[i] !== 0) { anyNonZero = true }
}
console.log(anyNonZero)     // true (16 zero bytes from a CSPRNG: p ≈ 2^-128)

// Only an integer TypedArray is filled; anything else is a TypeMismatchError.
try {
  crypto.getRandomValues(new Float64Array(2) as any)
} catch (e: any) {
  console.log(e.name)     // TypeMismatchError
}

// ── crypto.randomUUID() ─────────────────────────────────────────────────────
// A standard RFC 4122 version-4 UUID string:
// "xxxxxxxx-xxxx-4xxx-yxxx-xxxxxxxxxxxx" — the "4" and the "y" (one of
// 8/9/a/b) are fixed by the version/variant bits, not random.
const id1: string = crypto.randomUUID()
const id2: string = crypto.randomUUID()
console.log(id1.length)      // 36
console.log(id1 !== id2)     // true — two calls give two different UUIDs
console.log(id1[14])         // 4 (the version nibble, always 4)

// ── crypto.subtle.digest(algorithm, data) ───────────────────────────────────
// SHA-1 / SHA-256 / SHA-384 / SHA-512 over a TypedArray or ArrayBuffer,
// returning a Promise<ArrayBuffer>.
function toHex(view: Uint8Array): string {
    let hex = ""
    for (let i = 0; i < view.length; i++) {
        hex += view[i].toString(16).padStart(2, "0")
    }
    return hex
}

async function digests(): Promise<void> {
    const data = new TextEncoder().encode("abc")
    const sha256 = new Uint8Array(await crypto.subtle.digest("SHA-256", data))
    // The NIST test vector for SHA-256("abc"):
    console.log(toHex(sha256)) // ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad
    const sha1 = new Uint8Array(await crypto.subtle.digest({ name: "SHA-1" }, data))
    console.log(toHex(sha1))   // a9993e364706816aba3e25717850c26c9cd0d89d
    console.log(sha256.length, sha1.length) // 32 20
}

// ── HMAC sign/verify + AES-GCM encrypt/decrypt (CryptoKey) ─────────────────
async function symmetric(): Promise<void> {
    // Keys are imported ("raw" bytes or "jwk") or generated; usages are
    // enforced (using a key outside them rejects with InvalidAccessError).
    const rawKey = new TextEncoder().encode("Jefe")
    const hmacKey = await crypto.subtle.importKey("raw", rawKey,
        { name: "HMAC", hash: "SHA-256" }, true, ["sign", "verify"])
    console.log(hmacKey.type, hmacKey.extractable) // secret true
    const msg = new TextEncoder().encode("what do ya want for nothing?")
    const sig = new Uint8Array(await crypto.subtle.sign("HMAC", hmacKey, msg))
    console.log(toHex(sig)) // 5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843 (RFC 4231)
    console.log(await crypto.subtle.verify("HMAC", hmacKey, sig, msg)) // true

    // JWK export/import: symmetric keys use { kty: "oct", k: base64url },
    // returned as a plain JsonWebKey object.
    const jwk = await crypto.subtle.exportKey("jwk", hmacKey)
    console.log(jwk.kty, jwk.k) // oct SmVmZQ

    // AES-GCM: authenticated encryption; decrypting tampered data rejects
    // with OperationError.
    const aesKey = await crypto.subtle.generateKey(
        { name: "AES-GCM", length: 256 }, false, ["encrypt", "decrypt"])
    const iv = new Uint8Array(12)
    crypto.getRandomValues(iv)
    const secret = new TextEncoder().encode("meet me in Thessaloniki")
    const ct = await crypto.subtle.encrypt({ name: "AES-GCM", iv: iv }, aesKey, secret)
    const pt = await crypto.subtle.decrypt({ name: "AES-GCM", iv: iv }, aesKey, ct)
    console.log(new TextDecoder().decode(pt)) // meet me in Thessaloniki
}

// ── RSA + ECDSA (CryptoKeyPair) ─────────────────────────────────────────────
async function asymmetric(): Promise<void> {
    // ECDSA P-256: sign with the private key, verify with the public one.
    // Signatures are the Web Crypto raw r||s form (64 bytes for P-256).
    const ecPair = await crypto.subtle.generateKey(
        { name: "ECDSA", namedCurve: "P-256" }, true, ["sign", "verify"])
    const msg = new TextEncoder().encode("signed in Thessaloniki")
    const sig = await crypto.subtle.sign({ name: "ECDSA", hash: "SHA-256" }, ecPair.privateKey, msg)
    console.log(sig.byteLength) // 64
    console.log(await crypto.subtle.verify({ name: "ECDSA", hash: "SHA-256" }, ecPair.publicKey, sig, msg)) // true

    // Keys travel as raw points, PKCS#8/SPKI DER, or JWK objects.
    const jwk = await crypto.subtle.exportKey("jwk", ecPair.publicKey)
    console.log(jwk.kty, jwk.crv) // EC P-256

    // RSA-OAEP: encrypt with the public key, decrypt with the private one.
    const rsaPair = await crypto.subtle.generateKey(
        { name: "RSA-OAEP", modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: "SHA-256" }, false, ["encrypt", "decrypt"])
    const ct = await crypto.subtle.encrypt({ name: "RSA-OAEP" }, rsaPair.publicKey, msg)
    const pt = await crypto.subtle.decrypt({ name: "RSA-OAEP" }, rsaPair.privateKey, ct)
    console.log(new TextDecoder().decode(pt)) // signed in Thessaloniki
}

// ── Ed25519, X25519 and AES-KW ──────────────────────────────────────────────
async function modern(): Promise<void> {
    // Ed25519 signatures are 64 bytes; the raw public key is 32.
    const ed = await crypto.subtle.generateKey('Ed25519', true, ['sign', 'verify']) as CryptoKeyPair;
    const msg = new TextEncoder().encode('Thessaloniki')
    const sig = await crypto.subtle.sign('Ed25519', ed.privateKey, msg);
    console.log(sig.byteLength, await crypto.subtle.verify('Ed25519', ed.publicKey, sig, msg)); // 64 true
    console.log((await crypto.subtle.exportKey('raw', ed.publicKey)).byteLength);             // 32

    // X25519 key agreement: both sides derive the same 32 bytes.
    const a = await crypto.subtle.generateKey({ name: 'X25519' }, false, ['deriveBits']) as CryptoKeyPair;
    const b = await crypto.subtle.generateKey({ name: 'X25519' }, false, ['deriveBits']) as CryptoKeyPair;
    const ab = new Uint8Array(await crypto.subtle.deriveBits({ name: 'X25519', public: b.publicKey }, a.privateKey, 256));
    const ba = new Uint8Array(await crypto.subtle.deriveBits({ name: 'X25519', public: a.publicKey }, b.privateKey, 256));
    console.log(toHex(ab) === toHex(ba));                                                     // true

    // AES-KW (RFC 3394) wraps another key for storage or transport.
    const kek = await crypto.subtle.generateKey({ name: 'AES-KW', length: 256 }, false, ['wrapKey', 'unwrapKey']);
    const dataKey = await crypto.subtle.generateKey({ name: 'AES-GCM', length: 128 }, true, ['encrypt']);
    const wrapped = await crypto.subtle.wrapKey('raw', dataKey, kek, 'AES-KW');
    console.log(wrapped.byteLength);                                                          // 24
    const back = await crypto.subtle.unwrapKey('raw', wrapped, kek, 'AES-KW', 'AES-GCM', true, ['encrypt']);
    console.log(back.algorithm);                                                              // { name: 'AES-GCM', length: 128 }
}

// ── deriveKey / deriveBits (PBKDF2, HKDF) ───────────────────────────────────
async function derive(): Promise<void> {
    // Password → AES key, the classic PBKDF2 flow.
    const pw = new TextEncoder().encode("correct horse battery staple")
    const base = await crypto.subtle.importKey("raw", pw, "PBKDF2", false, ["deriveKey", "deriveBits"])
    const salt = new Uint8Array(16)
    crypto.getRandomValues(salt)
    const aes = await crypto.subtle.deriveKey(
        { name: "PBKDF2", salt: salt, iterations: 100000, hash: "SHA-256" },
        base, { name: "AES-GCM", length: 256 }, false, ["encrypt", "decrypt"])
    console.log(aes.type) // secret

    // Raw bits too — RFC 6070's PBKDF2-HMAC-SHA1 test vector:
    const kat = await crypto.subtle.importKey("raw", new TextEncoder().encode("password"), "PBKDF2", false, ["deriveBits"])
    const bits = new Uint8Array(await crypto.subtle.deriveBits(
        { name: "PBKDF2", salt: new TextEncoder().encode("salt"), iterations: 2, hash: "SHA-1" }, kat, 160))
    console.log(toHex(bits)) // ea6c014dc72d6f8ccd1ed92ace1d41f0d8de8957
}

async function main(): Promise<void> {
    await digests()
    await symmetric()
    await asymmetric()
    await modern()
    await derive()
}
main()
