package tests

import (
	"strings"
	"testing"
)

// Web Crypto crypto.subtle tests (TDD-00104). Known-answer digests are the
// standard "abc" NIST vectors; every deterministic test runs under both
// backends (openssl everywhere, commoncrypto matrix-skipped off macOS) and
// must produce identical output.

const subtleDigestVectorsSrc = `
function toHex(view: Uint8Array): string {
  let hex = "";
  for (let i = 0; i < view.length; i++) {
    hex += view[i].toString(16).padStart(2, "0");
  }
  return hex;
}
async function run(): Promise<void> {
  const data = new TextEncoder().encode("abc");
  const v1 = new Uint8Array(await crypto.subtle.digest("SHA-1", data));
  console.log(toHex(v1));
  const v256 = new Uint8Array(await crypto.subtle.digest("SHA-256", data));
  console.log(toHex(v256));
  const v384 = new Uint8Array(await crypto.subtle.digest({ name: "SHA-384" }, data));
  console.log(toHex(v384));
  const v512 = new Uint8Array(await crypto.subtle.digest("SHA-512", data));
  console.log(toHex(v512));
}
run();
`

const subtleDigestVectorsWant = `a9993e364706816aba3e25717850c26c9cd0d89d
ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad
cb00753f45a35e8bb5a03d699ac65007272c32ab0eded1631a8b605a43ff5bed8086072ba1e7cc2358baeca134c825a7
ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f`

func TestE2ESubtleDigestVectors(t *testing.T) {
	for _, backend := range []string{"openssl", "commoncrypto"} {
		t.Run(backend, func(t *testing.T) {
			got := compileAndRunCryptoMode(t, subtleDigestVectorsSrc, backend)
			compareLines(t, got, subtleDigestVectorsWant)
		})
	}
}

func TestE2ESubtleDigestThenChain(t *testing.T) {
	src := `
crypto.subtle.digest("SHA-256", new TextEncoder().encode("abc")).then((d) => {
  const v = new Uint8Array(d);
  let hex = "";
  for (let i = 0; i < v.length; i++) {
    hex += v[i].toString(16).padStart(2, "0");
  }
  console.log(hex);
});
`
	got := compileAndRun(t, src)
	compareLines(t, got, "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad")
}

func TestE2ESubtleDigestArrayBufferInput(t *testing.T) {
	// digest over an ArrayBuffer (a prior digest's result) instead of a view.
	src := `
async function run(): Promise<void> {
  const d1 = await crypto.subtle.digest("SHA-256", new TextEncoder().encode("abc"));
  const d2 = new Uint8Array(await crypto.subtle.digest("SHA-256", d1));
  console.log(d2.length);
}
run();
`
	got := compileAndRun(t, src)
	compareLines(t, got, "32")
}

func TestSubtleDigestUnknownAlgoIsCompileError(t *testing.T) {
	_, err := parseAndCompile(`
async function run(): Promise<void> {
  await crypto.subtle.digest("MD5", new Uint8Array(4));
}
run();
`)
	if err == nil || !strings.Contains(err.Error(), "unsupported digest algorithm") {
		t.Fatalf("expected unsupported-algorithm compile error, got: %v", err)
	}
}

func TestSubtleNonLiteralAlgoIsCompileError(t *testing.T) {
	_, err := parseAndCompile(`
async function run(): Promise<void> {
  const algo = "SHA-256";
  await crypto.subtle.digest(algo, new Uint8Array(4));
}
run();
`)
	if err == nil || !strings.Contains(err.Error(), "string literal") {
		t.Fatalf("expected literal-algorithm compile error, got: %v", err)
	}
}

// HMAC-SHA256 known-answer (RFC 4231 test case 2) + verify + tamper-fails +
// JWK roundtrip in both directions (Map and object-literal form).
const subtleSymmetricSrc = `
function toHex(v: Uint8Array): string {
  let h = "";
  for (let i = 0; i < v.length; i++) h += v[i].toString(16).padStart(2, "0");
  return h;
}
async function run(): Promise<void> {
  const rawKey = new TextEncoder().encode("Jefe");
  const key = await crypto.subtle.importKey("raw", rawKey, { name: "HMAC", hash: "SHA-256" }, true, ["sign", "verify"]);
  console.log(key.type, key.extractable);
  const data = new TextEncoder().encode("what do ya want for nothing?");
  const sigBuf = await crypto.subtle.sign("HMAC", key, data);
  const sig = new Uint8Array(sigBuf);
  console.log(toHex(sig));
  console.log(await crypto.subtle.verify("HMAC", key, sig, data));
  const bad = new Uint8Array(sigBuf);
  bad[0] = bad[0] ^ 1;
  console.log(await crypto.subtle.verify("HMAC", key, bad, data));
  const jwk = await crypto.subtle.exportKey("jwk", key);
  console.log(jwk.get("kty"), jwk.get("k"));
  const key2 = await crypto.subtle.importKey("jwk", jwk, { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const sig2 = new Uint8Array(await crypto.subtle.sign("HMAC", key2, data));
  console.log(toHex(sig2));
  const key3 = await crypto.subtle.importKey("jwk", { kty: "oct", k: "SmVmZQ" }, { name: "HMAC", hash: "SHA-256" }, false, ["sign"]);
  const sig3 = new Uint8Array(await crypto.subtle.sign("HMAC", key3, data));
  console.log(toHex(sig3));
}
run();
`

const subtleSymmetricWant = `secret true
5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843
true
false
oct SmVmZQ
5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843
5bdcc146bf60754e6a042426089575c75a003f089d2739839dec58b964ec3843`

func TestE2ESubtleHmacJwk(t *testing.T) {
	for _, backend := range []string{"openssl", "commoncrypto"} {
		t.Run(backend, func(t *testing.T) {
			got := compileAndRunCryptoMode(t, subtleSymmetricSrc, backend)
			compareLines(t, got, subtleSymmetricWant)
		})
	}
}

func TestE2ESubtleAesRoundTrips(t *testing.T) {
	src := `
async function run(): Promise<void> {
  const gk = await crypto.subtle.generateKey({ name: "AES-GCM", length: 256 }, true, ["encrypt", "decrypt"]);
  const iv = new Uint8Array(12);
  crypto.getRandomValues(iv);
  const aad = new TextEncoder().encode("header");
  const pt = new TextEncoder().encode("hello aes");
  const ct = await crypto.subtle.encrypt({ name: "AES-GCM", iv: iv, additionalData: aad }, gk, pt);
  const back = await crypto.subtle.decrypt({ name: "AES-GCM", iv: iv, additionalData: aad }, gk, ct);
  console.log(new TextDecoder().decode(back));
  const rawBuf = await crypto.subtle.exportKey("raw", gk);
  console.log(rawBuf.byteLength);
  const ck = await crypto.subtle.generateKey({ name: "AES-CBC", length: 128 }, false, ["encrypt", "decrypt"]);
  const iv16 = new Uint8Array(16);
  crypto.getRandomValues(iv16);
  const ct2 = await crypto.subtle.encrypt({ name: "AES-CBC", iv: iv16 }, ck, pt);
  const back2 = await crypto.subtle.decrypt({ name: "AES-CBC", iv: iv16 }, ck, ct2);
  console.log(new TextDecoder().decode(back2));
}
run();
`
	got := compileAndRun(t, src)
	compareLines(t, got, "hello aes\n32\nhello aes")
}

func TestE2ESubtleErrorNames(t *testing.T) {
	src := `
async function run(): Promise<void> {
  const raw = new Uint8Array(32);
  crypto.getRandomValues(raw);
  const key = await crypto.subtle.importKey("raw", raw, { name: "AES-GCM" }, false, ["encrypt"]);
  const iv = new Uint8Array(12);
  const pt = new TextEncoder().encode("secret");
  const ct = await crypto.subtle.encrypt({ name: "AES-GCM", iv: iv }, key, pt);
  try {
    await crypto.subtle.decrypt({ name: "AES-GCM", iv: iv }, key, ct);
  } catch (err) {
    console.log("usage:", err.name);
  }
  const key2 = await crypto.subtle.importKey("raw", raw, { name: "AES-GCM" }, false, ["decrypt"]);
  const bad = new Uint8Array(ct);
  bad[2] = bad[2] ^ 255;
  try {
    await crypto.subtle.decrypt({ name: "AES-GCM", iv: iv }, key2, bad);
  } catch (err) {
    console.log("tamper:", err.name);
  }
  try {
    await crypto.subtle.exportKey("raw", key);
  } catch (err) {
    console.log("export:", err.name);
  }
}
run();
`
	got := compileAndRun(t, src)
	compareLines(t, got, "usage: InvalidAccessError\ntamper: OperationError\nexport: InvalidAccessError")
}

// Asymmetric surface: ECDSA (all formats), RSA-OAEP, RSA-PSS — roundtrips
// and tamper checks, run under both backends.
const subtleAsymSrc = `
async function run(): Promise<void> {
  const msg = new TextEncoder().encode("attack at dawn");
  const ecPair = await crypto.subtle.generateKey({ name: "ECDSA", namedCurve: "P-256" }, true, ["sign", "verify"]);
  console.log(ecPair.publicKey.type, ecPair.privateKey.type);
  const ecSig = await crypto.subtle.sign({ name: "ECDSA", hash: "SHA-256" }, ecPair.privateKey, msg);
  console.log(ecSig.byteLength);
  console.log(await crypto.subtle.verify({ name: "ECDSA", hash: "SHA-256" }, ecPair.publicKey, ecSig, msg));
  const badSig = new Uint8Array(64);
  const sigView = new Uint8Array(ecSig);
  for (let i = 0; i < 64; i++) badSig[i] = sigView[i];
  badSig[3] = badSig[3] ^ 1;
  console.log(await crypto.subtle.verify({ name: "ECDSA", hash: "SHA-256" }, ecPair.publicKey, badSig, msg));
  const rawPub = await crypto.subtle.exportKey("raw", ecPair.publicKey);
  console.log(rawPub.byteLength);
  const pub2 = await crypto.subtle.importKey("raw", rawPub, { name: "ECDSA", namedCurve: "P-256" }, true, ["verify"]);
  console.log(await crypto.subtle.verify({ name: "ECDSA", hash: "SHA-256" }, pub2, ecSig, msg));
  const jwkPriv = await crypto.subtle.exportKey("jwk", ecPair.privateKey);
  console.log(jwkPriv.get("kty"), jwkPriv.get("crv"));
  const priv2 = await crypto.subtle.importKey("jwk", jwkPriv, { name: "ECDSA", namedCurve: "P-256" }, false, ["sign"]);
  const sig2 = await crypto.subtle.sign({ name: "ECDSA", hash: "SHA-256" }, priv2, msg);
  console.log(await crypto.subtle.verify({ name: "ECDSA", hash: "SHA-256" }, ecPair.publicKey, sig2, msg));
  const rsaPair = await crypto.subtle.generateKey(
    { name: "RSA-OAEP", modulusLength: 2048, publicExponent: new Uint8Array([1, 0, 1]), hash: "SHA-256" },
    true, ["encrypt", "decrypt"]);
  const ct = await crypto.subtle.encrypt({ name: "RSA-OAEP" }, rsaPair.publicKey, msg);
  console.log(ct.byteLength);
  const back = await crypto.subtle.decrypt({ name: "RSA-OAEP" }, rsaPair.privateKey, ct);
  console.log(new TextDecoder().decode(back));
  const p8 = await crypto.subtle.exportKey("pkcs8", rsaPair.privateKey);
  const priv3 = await crypto.subtle.importKey("pkcs8", p8, { name: "RSA-OAEP", hash: "SHA-256" }, false, ["decrypt"]);
  const back2 = await crypto.subtle.decrypt({ name: "RSA-OAEP" }, priv3, ct);
  console.log(new TextDecoder().decode(back2));
  const pssPair = await crypto.subtle.generateKey(
    { name: "RSA-PSS", modulusLength: 2048, hash: "SHA-256" }, true, ["sign", "verify"]);
  const pssSig = await crypto.subtle.sign({ name: "RSA-PSS", saltLength: 32 }, pssPair.privateKey, msg);
  console.log(await crypto.subtle.verify({ name: "RSA-PSS", saltLength: 32 }, pssPair.publicKey, pssSig, msg));
  const jwkPub = await crypto.subtle.exportKey("jwk", pssPair.publicKey);
  console.log(jwkPub.get("kty"), jwkPub.get("e"));
  const pub3 = await crypto.subtle.importKey("jwk", jwkPub, { name: "RSA-PSS", hash: "SHA-256" }, false, ["verify"]);
  console.log(await crypto.subtle.verify({ name: "RSA-PSS", saltLength: 32 }, pub3, pssSig, msg));
}
run();
`

const subtleAsymWant = `public private
64
true
false
65
true
EC P-256
true
256
attack at dawn
attack at dawn
true
RSA AQAB
true`

func TestE2ESubtleAsymmetric(t *testing.T) {
	for _, backend := range []string{"openssl", "commoncrypto"} {
		t.Run(backend, func(t *testing.T) {
			got := compileAndRunCryptoMode(t, subtleAsymSrc, backend)
			compareLines(t, got, subtleAsymWant)
		})
	}
}

func TestE2ESubtleExportKindMismatch(t *testing.T) {
	src := `
async function run(): Promise<void> {
  const pair = await crypto.subtle.generateKey({ name: "ECDSA", namedCurve: "P-256" }, true, ["sign"]);
  try {
    await crypto.subtle.exportKey("pkcs8", pair.publicKey);
  } catch (err) {
    console.log("pkcs8-of-public:", err.name);
  }
  try {
    await crypto.subtle.exportKey("spki", pair.privateKey);
  } catch (err) {
    console.log("spki-of-private:", err.name);
  }
}
run();
`
	got := compileAndRun(t, src)
	compareLines(t, got, "pkcs8-of-public: InvalidAccessError\nspki-of-private: InvalidAccessError")
}

// deriveBits/deriveKey known answers: RFC 6070 PBKDF2 case 2 and RFC 5869
// HKDF case 1, plus a deriveKey→AES-GCM roundtrip.
const subtleDeriveSrc = `
function toHex(v: Uint8Array): string {
  let h = "";
  for (let i = 0; i < v.length; i++) h += v[i].toString(16).padStart(2, "0");
  return h;
}
async function run(): Promise<void> {
  const pw = new TextEncoder().encode("password");
  const base = await crypto.subtle.importKey("raw", pw, "PBKDF2", false, ["deriveBits", "deriveKey"]);
  const salt = new TextEncoder().encode("salt");
  const bits = await crypto.subtle.deriveBits({ name: "PBKDF2", salt: salt, iterations: 2, hash: "SHA-1" }, base, 160);
  const v = new Uint8Array(bits);
  console.log(toHex(v));
  const aes = await crypto.subtle.deriveKey(
    { name: "PBKDF2", salt: salt, iterations: 1000, hash: "SHA-256" },
    base, { name: "AES-GCM", length: 256 }, true, ["encrypt", "decrypt"]);
  console.log(aes.type);
  const iv = new Uint8Array(12);
  crypto.getRandomValues(iv);
  const msg = new TextEncoder().encode("derived secret");
  const ct = await crypto.subtle.encrypt({ name: "AES-GCM", iv: iv }, aes, msg);
  const pt = await crypto.subtle.decrypt({ name: "AES-GCM", iv: iv }, aes, ct);
  console.log(new TextDecoder().decode(pt));
  const ikm = new Uint8Array(22);
  for (let i = 0; i < 22; i++) ikm[i] = 0x0b;
  const hsalt = new Uint8Array(13);
  for (let i = 0; i < 13; i++) hsalt[i] = i;
  const info = new Uint8Array(10);
  for (let i = 0; i < 10; i++) info[i] = 0xf0 + i;
  const hbase = await crypto.subtle.importKey("raw", ikm, "HKDF", false, ["deriveBits"]);
  const okm = new Uint8Array(await crypto.subtle.deriveBits({ name: "HKDF", salt: hsalt, info: info, hash: "SHA-256" }, hbase, 336));
  console.log(toHex(okm));
}
run();
`

const subtleDeriveWant = `ea6c014dc72d6f8ccd1ed92ace1d41f0d8de8957
secret
derived secret
3cb25f25faacd57a90434f64d0362f2a2d2d0a90cf1a5a4c5db02d56ecc4c5bf34007208d5b887185865`

func TestE2ESubtleDerive(t *testing.T) {
	for _, backend := range []string{"openssl", "commoncrypto"} {
		t.Run(backend, func(t *testing.T) {
			got := compileAndRunCryptoMode(t, subtleDeriveSrc, backend)
			compareLines(t, got, subtleDeriveWant)
		})
	}
}

func TestE2EGetRandomValuesTypedArray(t *testing.T) {
	src := `
const u8 = new Uint8Array(32);
const ret = crypto.getRandomValues(u8);
let sum = 0;
for (let i = 0; i < u8.length; i++) sum += u8[i];
console.log(u8.length, ret.length, sum > 0 ? "filled" : "all-zero");
const u32 = new Uint32Array(8);
crypto.getRandomValues(u32);
let sum32 = 0;
for (let i = 0; i < u32.length; i++) sum32 += u32[i];
console.log(u32.length, sum32 > 0 ? "filled" : "all-zero");
const buf = new ArrayBuffer(16);
crypto.getRandomValues(buf);
console.log(buf.byteLength);
`
	got := compileAndRun(t, src)
	compareLines(t, got, "32 32 filled\n8 filled\n16")
}
