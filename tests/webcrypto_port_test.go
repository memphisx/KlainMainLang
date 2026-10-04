package tests

import "testing"

// crypto.subtle is Node's lib/internal/crypto/webcrypto.js in TypeScript
// over node:crypto (ADR-01347): every algorithm Node ships unflagged, run-time
// algorithm normalization, promise rejections with Node's errors, and
// crypto.subtle as a value. Checked against the local Node.
func TestE2ESubtleEveryAlgorithm(t *testing.T) {
	assertSameAsNodeImports(t, `
import util from 'util';
const enc = new TextEncoder();
const hex = (b: ArrayBuffer) => Buffer.from(b).toString('hex');
async function t(label: string, f: () => Promise<any>) { try { const r = await f(); console.log(label, 'ok', r); } catch (e: any) { console.log(label, e.name, e.message); } }
async function main() {
  const s = crypto.subtle;
  await t('sha1', async () => hex(await s.digest('SHA-1', enc.encode('abc'))));
  await t('sha512', async () => hex(await s.digest({ name: 'SHA-512' }, enc.encode('abc'))).slice(0, 16));
  const hk = await s.importKey('raw', enc.encode('key'), { name: 'HMAC', hash: 'SHA-384' }, true, ['sign', 'verify']);
  await t('hmac384', async () => hex(await s.sign('HMAC', hk, enc.encode('m'))).slice(0, 16));
  await t('hmac-alg', async () => JSON.stringify(hk.algorithm));
  const ctrk = await s.importKey('raw', new Uint8Array(16).fill(1), 'AES-CTR', true, ['encrypt', 'decrypt']);
  await t('ctr', async () => hex(await s.encrypt({ name: 'AES-CTR', counter: new Uint8Array(16), length: 64 }, ctrk, enc.encode('hello ctr'))));
  const kwk = await s.importKey('raw', new Uint8Array(16).fill(2), 'AES-KW', true, ['wrapKey', 'unwrapKey']);
  await t('kw-wrap', async () => hex(await s.wrapKey('raw', ctrk, kwk, 'AES-KW')));
  await t('kw-unwrap', async () => { const w = await s.wrapKey('raw', ctrk, kwk, 'AES-KW'); const k = await s.unwrapKey('raw', w, kwk, 'AES-KW', 'AES-CTR', true, ['encrypt']); return hex(await s.exportKey('raw', k)); });
  const pk1 = await s.generateKey({ name: 'RSASSA-PKCS1-v1_5', modulusLength: 1024, publicExponent: new Uint8Array([1, 0, 1]), hash: 'SHA-256' }, true, ['sign', 'verify']);
  await t('pkcs1', async () => { const sig = await s.sign('RSASSA-PKCS1-v1_5', pk1.privateKey, enc.encode('x')); return [sig.byteLength, await s.verify('RSASSA-PKCS1-v1_5', pk1.publicKey, sig, enc.encode('x'))]; });
  await t('rsa-alg', async () => { const a: any = pk1.publicKey.algorithm; return [a.name, a.modulusLength, Array.from(a.publicExponent as Uint8Array), a.hash.name]; });
  await t('rsa-e3', async () => (await s.generateKey({ name: 'RSA-PSS', modulusLength: 1024, publicExponent: new Uint8Array([3]), hash: 'SHA-256' }, true, ['sign'])).privateKey.type);
  const ed = await s.generateKey('Ed25519', true, ['sign', 'verify']) as CryptoKeyPair;
  await t('ed25519', async () => { const sig = await s.sign('Ed25519', ed.privateKey, enc.encode('x')); return [sig.byteLength, await s.verify('Ed25519', ed.publicKey, sig, enc.encode('x'))]; });
  await t('ed-raw', async () => (await s.exportKey('raw', ed.publicKey)).byteLength);
  await t('ed-jwk', async () => Object.keys(await s.exportKey('jwk', ed.privateKey)).join(','));
  const x1 = await s.generateKey({ name: 'X25519' }, true, ['deriveBits']) as CryptoKeyPair;
  const x2 = await s.generateKey({ name: 'X25519' }, true, ['deriveBits']) as CryptoKeyPair;
  await t('x25519', async () => { const a = await s.deriveBits({ name: 'X25519', public: x2.publicKey }, x1.privateKey, 256); const b = await s.deriveBits({ name: 'X25519', public: x1.publicKey }, x2.privateKey, 256); return hex(a) === hex(b); });
  const e1 = await s.generateKey({ name: 'ECDH', namedCurve: 'P-384' }, true, ['deriveKey', 'deriveBits']) as CryptoKeyPair;
  const e2 = await s.generateKey({ name: 'ECDH', namedCurve: 'P-384' }, true, ['deriveKey', 'deriveBits']) as CryptoKeyPair;
  await t('ecdh', async () => { const k = await s.deriveKey({ name: 'ECDH', public: e2.publicKey }, e1.privateKey, { name: 'AES-GCM', length: 256 }, true, ['encrypt']); return (await s.exportKey('raw', k)).byteLength; });
  await t('ecdh-compressed', async () => { const raw = new Uint8Array(await s.exportKey('raw', e1.publicKey)); const c = new Uint8Array(49); c[0] = 2 + (raw[96] & 1); c.set(raw.subarray(1, 49), 1); const k = await s.importKey('raw', c, { name: 'ECDH', namedCurve: 'P-384' }, true, []); return (await s.exportKey('raw', k)).byteLength; });
  await t('hkdf', async () => { const b = await s.importKey('raw', enc.encode('ikm'), 'HKDF', false, ['deriveBits']); return hex(await s.deriveBits({ name: 'HKDF', hash: 'SHA-256', salt: enc.encode('s'), info: enc.encode('i') }, b, 128)); });
  await t('bad-usage', async () => s.generateKey({ name: 'AES-GCM', length: 256 }, true, ['sign']));
  await t('bad-alg', async () => s.generateKey({ name: 'Nope' } as any, true, ['sign']));
  await t('keyops', async () => { const k = await s.importKey('jwk', { kty: 'oct', k: 'AAAAAAAAAAAAAAAAAAAAAA', key_ops: ['encrypt'] }, 'AES-GCM', true, ['decrypt']); return k.usages; });
  await t('inspect', async () => util.inspect(hk).split('\n')[0]);
}
main();
`)
}

// Node's validation: non-extractable KDF base keys, JWK kty/ext/alg checks,
// non-extractable export (ADR-01347).
func TestE2ESubtleValidation(t *testing.T) {
	assertSameAsNode(t, `
const enc = new TextEncoder();
async function t(label: string, f: () => Promise<any>) { try { const r = await f(); console.log(label, 'ok', r); } catch (e: any) { console.log(label, e.name, e.message); } }
async function main() {
  const s = crypto.subtle;
  await t('pbkdf2-extractable', async () => (await s.importKey('raw', enc.encode('pw'), 'PBKDF2', true, ['deriveBits'])).type);
  await t('hkdf-extractable', async () => (await s.importKey('raw', enc.encode('pw'), 'HKDF', true, ['deriveBits'])).type);
  const aes = await s.generateKey({ name: 'AES-GCM', length: 128 }, false, ['encrypt']);
  await t('nonextractable-export', async () => s.exportKey('raw', aes));
  await t('jwk-bad-kty', async () => s.importKey('jwk', { kty: 'RSA', k: 'AAAA' } as any, 'AES-GCM', true, ['encrypt']));
  await t('jwk-ext-false', async () => s.importKey('jwk', { kty: 'oct', k: 'AAAAAAAAAAAAAAAAAAAAAA', ext: false }, 'AES-GCM', true, ['encrypt']));
  await t('jwk-alg', async () => s.importKey('jwk', { kty: 'oct', k: 'AAAAAAAAAAAAAAAAAAAAAA', alg: 'A256GCM' }, 'AES-GCM', true, ['encrypt']));
  await t('supports', async () => typeof (s as any).supports);
  await t('str', async () => Object.prototype.toString.call(s));
}
main();
`)
}

// KeyObject.from(cryptoKey) (ADR-01347).
func TestE2EKeyObjectFromCryptoKey(t *testing.T) {
	assertSameAsNodeImports(t, `
import { KeyObject } from 'crypto';
async function main() {
  const k = await crypto.subtle.generateKey({ name: 'HMAC', hash: 'SHA-256' }, true, ['sign']);
  const ko = KeyObject.from(k);
  console.log(ko.type, ko.symmetricKeySize);
  const p = await crypto.subtle.generateKey({ name: 'ECDSA', namedCurve: 'P-256' }, true, ['sign', 'verify']) as CryptoKeyPair;
  const pub = KeyObject.from(p.publicKey);
  console.log(pub.type, pub.asymmetricKeyType, pub.asymmetricKeyDetails);
  try { KeyObject.from({} as any); } catch (e: any) { console.log(e.code, e.message); }
}
main();
`)
}

// TypedArray.from/of, written in TypeScript and reached through @lower
// (ADR-01347).
func TestE2ETypedArrayFromAndOf(t *testing.T) {
	assertSameAsNode(t, `
console.log(Int16Array.from([1, 2, 3], (v) => v * 1000));
console.log(Uint8Array.of(1, 256, -1), Float32Array.of(0.1));
console.log(Uint8Array.from(new Set([7, 8])), Uint8Array.from('123' as any), Uint8ClampedArray.from([300, -5, 1.5]));
console.log(Int32Array.from({ length: 3, 0: 5, 2: 9 } as any));
const ctx = { m: 10 };
console.log(Uint16Array.from([1, 2], function (this: any, v: number, k: number) { return v * this.m + k; }, ctx));
try { Uint8Array.from([1], 5 as any); } catch (e: any) { console.log(e instanceof TypeError); }
try { Uint8Array.from(null as any); } catch (e: any) { console.log(e instanceof TypeError); }
`)
}

// BigInt(string) is StringToBigInt: prefixes, white space, the empty
// string, and a catchable SyntaxError (ADR-01347).
func TestE2EBigIntFromString(t *testing.T) {
	assertSameAsNode(t, `
console.log(BigInt('0x10'), BigInt('0X1f'), BigInt('0o17'), BigInt('0b101'), BigInt('  42  '), BigInt(''), BigInt('-7'));
try { BigInt('-0x10'); } catch (e: any) { console.log(e.name, e.message); }
try { BigInt('1.5'); } catch (e: any) { console.log(e.name, e.message); }
`)
}

// An async callback passed where () => Promise<any> is expected keeps an
// array or object result (ADR-01347).
func TestE2EAsyncCallbackPromiseResultAdapted(t *testing.T) {
	assertSameAsNode(t, `
function u(f: () => any) { console.log(f()); }
u(() => [1, true]);
async function w(f: () => Promise<any>) { console.log(await f()); }
w(async () => [2, true]).then(() => w(async () => [3, 4])).then(() => w(async () => 'str')).then(() => w(async () => ({ a: 1 })));
`)
}

// TextDecoder.decode of an ArrayBuffer held in any (ADR-01347).
func TestE2ETextDecoderAnyArrayBuffer(t *testing.T) {
	assertSameAsNode(t, `
const ab = new ArrayBuffer(2);
new Uint8Array(ab).set([104, 105]);
const held: any = ab;
console.log(new TextDecoder().decode(ab));
console.log(new TextDecoder().decode(held));
`)
}
