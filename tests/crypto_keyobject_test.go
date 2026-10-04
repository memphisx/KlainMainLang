package tests

import "testing"

// KeyObject (Node's lib/internal/crypto/keys.js): createSecretKey,
// createPublicKey, createPrivateKey over PEM, DER and JWK, export in each
// encoding, the details, equals, KeyObject sign/verify with the RSA-PSS and
// IEEE P1363 options, and generateKeyPair's KeyObject results (ADR-01345).
// Checked against the local Node.
func TestE2ECryptoKeyObject(t *testing.T) {
	assertSameAsNodeImports(t, `
import crypto from 'crypto';
const t = (label: string, f: () => any) => { try { const r = f(); console.log(label, 'ok', typeof r === 'object' && r !== null ? Object.prototype.toString.call(r) : r); } catch (e: any) { console.log(label, e.name, e.code, JSON.stringify(e.message)); } };
const s = crypto.createSecretKey(Buffer.from('secret!'));
console.log(s.type, s.symmetricKeySize, s.asymmetricKeyType, s.export().toString(), JSON.stringify(s.export({ format: 'jwk' })));
const kinds: [string, any][] = [['rsa', { modulusLength: 1024 }], ['ec', { namedCurve: 'P-256' }], ['ed25519', {}], ['x25519', {}], ['rsa-pss', { modulusLength: 1024, hashAlgorithm: 'sha256', mgf1HashAlgorithm: 'sha256', saltLength: 16 }]];
for (const [ty, o] of kinds) {
  const { publicKey, privateKey } = crypto.generateKeyPairSync(ty as any, o);
  console.log(ty, publicKey.type, privateKey.type, publicKey.asymmetricKeyType, privateKey.symmetricKeySize, JSON.stringify(publicKey.asymmetricKeyDetails, (k, v) => typeof v === 'bigint' ? v.toString() + 'n' : v));
  t('jwkpriv', () => Object.keys(privateKey.export({ format: 'jwk' })).join(','));
  t('jwkpub', () => Object.keys(publicKey.export({ format: 'jwk' })).join(','));
  const pem = privateKey.export({ type: 'pkcs8', format: 'pem' });
  t('roundtrip', () => [crypto.createPrivateKey(pem).equals(privateKey), crypto.createPublicKey(pem).equals(publicKey), crypto.createPublicKey(privateKey).equals(publicKey)].join());
  console.log(publicKey.export({ type: 'spki', format: 'der' }).length > 0, String(publicKey));
}
t('1', () => crypto.createPrivateKey('nope'));
t('2', () => crypto.createPublicKey('nope'));
t('3', () => s.export({ format: 'pem' } as any));
t('5', () => new crypto.KeyObject('secret', {}));
t('6', () => new crypto.KeyObject('bogus' as any, {}));
const { publicKey, privateKey } = crypto.generateKeyPairSync('ec', { namedCurve: 'P-256' });
t('7', () => (publicKey as any).export());
t('8', () => publicKey.export({ format: 'pem' } as any));
t('9', () => publicKey.export({ format: 'pem', type: 'pkcs8' }));
t('10', () => publicKey.export({ format: 'pem', type: 'pkcs1' }));
t('11', () => privateKey.export({ format: 'der', type: 'pkcs1' }));
t('12', () => (privateKey.export({ format: 'pem', type: 'sec1' }) as string).split('\n')[0]);
t('13', () => privateKey.export({ format: 'der', type: 'pkcs8', cipher: 'aes-256-cbc' }));
t('14', () => privateKey.export({ format: 'der', type: 'pkcs8', cipher: 'aes-256-cbc', passphrase: 'p' }).length > 0);
t('15', () => (privateKey.export({ format: 'pem', type: 'pkcs8', cipher: 'aes-256-cbc', passphrase: 'p' }) as string).split('\n')[0]);
t('16', () => crypto.createPublicKey(publicKey));
t('17', () => crypto.createPrivateKey(publicKey as any));
t('18', () => crypto.createPublicKey(s));
t('19', () => crypto.createSecretKey('abc', 'hex').symmetricKeySize);
t('20', () => crypto.createSecretKey(123 as any));
t('21', () => publicKey.equals(s));
t('22', () => publicKey.equals(123 as any));
t('23', () => s.equals(crypto.createSecretKey(Buffer.from('secret!'))));
t('24', () => crypto.createPrivateKey({ key: privateKey.export({ format: 'jwk' }), format: 'jwk' }).equals(privateKey));
t('25', () => crypto.createPublicKey({ key: publicKey.export({ format: 'jwk' }), format: 'jwk' }).equals(publicKey));
t('26', () => crypto.createPrivateKey({ key: { kty: 'nope' }, format: 'jwk' }));
t('27', () => crypto.createPrivateKey({ key: privateKey.export({ type: 'pkcs8', format: 'der' }), format: 'der', type: 'pkcs8' }).type);
t('28', () => crypto.createPrivateKey({ key: privateKey.export({ type: 'pkcs8', format: 'der' }), format: 'der' }));
const enc = privateKey.export({ format: 'pem', type: 'pkcs8', cipher: 'aes-256-cbc', passphrase: 'p' }) as string;
t('29', () => crypto.createPrivateKey(enc));
t('30', () => crypto.createPrivateKey({ key: enc, passphrase: 'wrong' }));
t('31', () => crypto.createPrivateKey({ key: enc, passphrase: 'p' }).type);
t('32', () => crypto.sign('sha256', Buffer.from('x'), privateKey).length > 0);
t('33', () => crypto.verify('sha256', Buffer.from('x'), publicKey, crypto.sign('sha256', Buffer.from('x'), privateKey)));
t('34', () => crypto.sign('sha256', Buffer.from('x'), { key: privateKey, dsaEncoding: 'ieee-p1363' }).length);
const p1363 = crypto.sign('sha256', Buffer.from('x'), { key: privateKey, dsaEncoding: 'ieee-p1363' });
t('35', () => crypto.verify('sha256', Buffer.from('x'), { key: publicKey, dsaEncoding: 'ieee-p1363' }, p1363));
const rsa = crypto.generateKeyPairSync('rsa', { modulusLength: 1024 });
const pss = crypto.sign('sha256', Buffer.from('x'), { key: rsa.privateKey, padding: crypto.constants.RSA_PKCS1_PSS_PADDING, saltLength: 20 });
t('36', () => crypto.verify('sha256', Buffer.from('x'), { key: rsa.publicKey, padding: crypto.constants.RSA_PKCS1_PSS_PADDING, saltLength: 20 }, pss));
t('37', () => crypto.verify('sha256', Buffer.from('x'), rsa.publicKey, pss));
t('38', () => crypto.createPrivateKey((rsa.privateKey.export({ type: 'pkcs1', format: 'pem' }) as string)).equals(rsa.privateKey));
t('39', () => crypto.createPublicKey({ key: rsa.publicKey.export({ type: 'pkcs1', format: 'der' }), format: 'der', type: 'pkcs1' }).equals(rsa.publicKey));
console.log(s.constructor.name, publicKey.constructor.name, privateKey.constructor.name);
console.log(s, publicKey);
const pp = crypto.generateKeyPairSync('ed25519', { publicKeyEncoding: { type: 'spki', format: 'der' }, privateKeyEncoding: { type: 'pkcs8', format: 'pem' } });
console.log(Buffer.isBuffer(pp.publicKey), typeof pp.privateKey);
crypto.generateKeyPair('ec', { namedCurve: 'P-384' }, (err, pub, priv) => console.log('async', err, pub.type, priv.asymmetricKeyDetails));
`)
}

// Compiler fixes the KeyObject port found (ADR-01345).
func TestE2EKeyObjectPortCompilerFixes(t *testing.T) {
	assertSameAsNode(t, `
function f(x: 'a'): { p: string; q: string };
function f(x: 'b'): { p: number; q: number };
function f(x: any): any { return x === 'a' ? { p: 's', q: 't' } : { p: 1, q: 2 }; }
const k: any = 'a';
const { p, q } = f(k);
console.log(p, q);
class A {
  #h = 1;
  get [Symbol.toStringTag](): string { return 'Tagged'; }
  eq(o: A): boolean { if (!(o instanceof A)) throw new TypeError('not an A'); return (o as A).#h === this.#h; }
}
class B extends A {}
const b = new B();
try { b.eq(123 as any); } catch (e: any) { console.log(e.message); }
console.log(b.eq(new A()));
const held: any = b;
console.log(String(held), `+"`${held}`"+`, Object.prototype.toString.call(held));
class FooBar { get [Symbol.toStringTag]() { return 'Bar'; } }
class Qux { get [Symbol.toStringTag]() { return 'Bar'; } }
console.log(new FooBar(), new Qux(), [new FooBar()]);
const fb: any = new FooBar();
console.log(fb);
`)
}

// publicEncrypt/privateDecrypt/privateEncrypt/publicDecrypt (ADR-01346).
func TestE2ECryptoRsaCipherFunctions(t *testing.T) {
	assertSameAsNodeImports(t, `
import crypto from 'crypto';
const t = (label: string, f: () => any) => { try { const r = f(); console.log(label, 'ok', r); } catch (e: any) { console.log(label, e.name, e.code, JSON.stringify(e.message)); } };
const { publicKey, privateKey } = crypto.generateKeyPairSync('rsa', { modulusLength: 1024 });
const pubPem = publicKey.export({ type: 'spki', format: 'pem' }) as string;
const privPem = privateKey.export({ type: 'pkcs8', format: 'pem' }) as string;
const msg = Buffer.from('hello Thessaloniki');
const ct = crypto.publicEncrypt(publicKey, msg);
t('len', () => ct.length);
t('dec', () => crypto.privateDecrypt(privateKey, ct).toString());
t('dec-pem', () => crypto.privateDecrypt(privPem, crypto.publicEncrypt(pubPem, msg)).toString());
const ct256 = crypto.publicEncrypt({ key: publicKey, oaepHash: 'sha256', oaepLabel: Buffer.from('lbl') }, msg);
t('dec256', () => crypto.privateDecrypt({ key: privateKey, oaepHash: 'sha256', oaepLabel: Buffer.from('lbl') }, ct256).toString());
t('dec256-wrong', () => crypto.privateDecrypt({ key: privateKey, oaepHash: 'sha256' }, ct256).toString());
const pk1 = crypto.publicEncrypt({ key: publicKey, padding: crypto.constants.RSA_PKCS1_PADDING }, msg);
t('pkcs1-dec', () => crypto.privateDecrypt({ key: privateKey, padding: crypto.constants.RSA_PKCS1_PADDING }, pk1).toString());
const se = crypto.privateEncrypt(privateKey, msg);
t('privenc', () => se.length);
t('pubdec', () => crypto.publicDecrypt(publicKey, se).toString());
t('pubdec-priv', () => crypto.publicDecrypt(privateKey, se).toString());
t('nopad', () => crypto.publicEncrypt({ key: publicKey, padding: crypto.constants.RSA_NO_PADDING }, Buffer.alloc(128, 1)).length);
t('nopad-short', () => crypto.publicEncrypt({ key: publicKey, padding: crypto.constants.RSA_NO_PADDING }, msg));
t('toolong', () => crypto.publicEncrypt(publicKey, Buffer.alloc(200)));
t('ec', () => crypto.publicEncrypt(crypto.generateKeyPairSync('ec', { namedCurve: 'P-256' }).publicKey, msg));
t('badhash', () => crypto.publicEncrypt({ key: publicKey, oaepHash: 'nope' }, msg));
t('str', () => crypto.privateDecrypt(privateKey, crypto.publicEncrypt(publicKey, 'a string' as any)).toString());
t('badbuf', () => crypto.publicEncrypt(publicKey, 5 as any));
t('garbage', () => crypto.privateDecrypt(privateKey, Buffer.alloc(128, 7)));
`)
}

// ECDH, ECDH.convertKey, getCurves and diffieHellman (ADR-01346).
func TestE2ECryptoECDHAndDiffieHellman(t *testing.T) {
	assertSameAsNodeImports(t, `
import crypto from 'crypto';
const t = (label: string, f: () => any) => { try { const r = f(); console.log(label, 'ok', r); } catch (e: any) { console.log(label, e.name, e.code, JSON.stringify(e.message)); } };
const a = crypto.createECDH('prime256v1');
const b = crypto.createECDH('prime256v1');
const ak = a.generateKeys();
const bk = b.generateKeys('hex', 'compressed');
t('lens', () => [ak.length, bk.length, a.getPrivateKey().length, a.getPublicKey('hex', 'compressed').length]);
t('same', () => a.computeSecret(b.getPublicKey()).equals(b.computeSecret(ak)));
t('hex-in', () => a.computeSecret(bk, 'hex', 'hex').length);
t('setpriv', () => { const c = crypto.createECDH('prime256v1'); c.setPrivateKey(a.getPrivateKey()); return c.getPublicKey().equals(a.getPublicKey()); });
t('convert', () => crypto.ECDH.convertKey(bk, 'prime256v1', 'hex', 'hex', 'uncompressed').length);
t('convert-buf', () => Buffer.isBuffer(crypto.ECDH.convertKey(a.getPublicKey(), 'prime256v1')));
t('badcurve', () => crypto.createECDH('nope'));
t('badpub', () => a.computeSecret(Buffer.from([4, 1, 2, 3])));
t('badpriv', () => crypto.createECDH('prime256v1').setPrivateKey(Buffer.alloc(32)));
t('nokeys', () => crypto.createECDH('prime256v1').getPublicKey());
t('nopriv', () => crypto.createECDH('prime256v1').getPrivateKey());
t('badfmt', () => a.getPublicKey('hex', 'bogus' as any));
t('curves', () => crypto.getCurves().includes('prime256v1') && crypto.getCurves().includes('secp384r1'));
const x1 = crypto.generateKeyPairSync('x25519'), x2 = crypto.generateKeyPairSync('x25519');
t('dh-x25519', () => crypto.diffieHellman({ privateKey: x1.privateKey, publicKey: x2.publicKey }).equals(crypto.diffieHellman({ privateKey: x2.privateKey, publicKey: x1.publicKey })));
const e1 = crypto.generateKeyPairSync('ec', { namedCurve: 'P-384' }), e2 = crypto.generateKeyPairSync('ec', { namedCurve: 'P-384' });
t('dh-ec', () => crypto.diffieHellman({ privateKey: e1.privateKey, publicKey: e2.publicKey }).length);
t('dh-mismatch', () => crypto.diffieHellman({ privateKey: e1.privateKey, publicKey: x2.publicKey }));
t('dh-notobj', () => crypto.diffieHellman({ privateKey: 'x' as any, publicKey: x2.publicKey }));
t('dh-wrongtype', () => crypto.diffieHellman({ privateKey: x1.publicKey, publicKey: x2.publicKey }));
const k = crypto.createECDH('secp256k1'); k.generateKeys();
t('k1', () => k.getPublicKey().length);
console.log(Object.prototype.toString.call(a), typeof a.setPublicKey);
`)
}

// An overloaded call with an any argument keeps the implementation's value:
// the overload picked is the first that fits, not the run-time one
// (ADR-01346).
func TestE2EOverloadCallWithAnyArgumentKeepsItsValue(t *testing.T) {
	assertSameAsNode(t, `
class C {
  get(enc?: null): Buffer;
  get(enc: string): string;
  get(enc?: any): any { const b = Buffer.from('hi'); return enc ? b.toString(enc) : b; }
  wrap(enc?: any): any { const r = this.get(enc); console.log(typeof r); return r; }
}
const c = new C();
console.log(c.wrap('hex'));
console.log(c.wrap());
`)
}

// AES key wrap (id-aes*-wrap, RFC 3394) through createCipheriv, and an update
// failure's message as Node's CipherBase::Update gives it (ADR-01346).
func TestE2ECryptoAesKeyWrap(t *testing.T) {
	assertSameAsNodeImports(t, `
import crypto from 'crypto';
const t = (label: string, f: () => any) => { try { const r = f(); console.log(label, 'ok', r); } catch (e: any) { console.log(label, e.name, e.code, JSON.stringify(e.message)); } };
const kek = Buffer.alloc(32, 7);
const keyData = Buffer.alloc(16, 9);
const iv = Buffer.from('A6A6A6A6A6A6A6A6', 'hex');
t('wrap', () => { const c = crypto.createCipheriv('id-aes256-wrap', kek, iv); return Buffer.concat([c.update(keyData), c.final()]).toString('hex'); });
t('unwrap', () => { const c = crypto.createCipheriv('id-aes256-wrap', kek, iv); const w = Buffer.concat([c.update(keyData), c.final()]); const d = crypto.createDecipheriv('id-aes256-wrap', kek, iv); return Buffer.concat([d.update(w), d.final()]).equals(keyData); });
t('aes128-wrap', () => { const c = crypto.createCipheriv('aes128-wrap', Buffer.alloc(16, 1), iv); return Buffer.concat([c.update(keyData), c.final()]).length; });
t('bad-unwrap', () => { const d = crypto.createDecipheriv('id-aes256-wrap', kek, iv); return Buffer.concat([d.update(Buffer.alloc(24, 1)), d.final()]); });
t('in-list', () => ['id-aes128-wrap', 'id-aes192-wrap', 'id-aes256-wrap'].every((n) => crypto.getCiphers().includes(n)));
`)
}
