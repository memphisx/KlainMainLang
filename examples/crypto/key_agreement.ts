// Key agreement and RSA encryption with node:crypto: ECDH between two
// parties, diffieHellman over KeyObjects, and publicEncrypt/privateDecrypt.
import crypto from 'crypto';

// ECDH: each side publishes a point; both derive the same secret.
const alice = crypto.createECDH('prime256v1');
const bob = crypto.createECDH('prime256v1');
const alicePub = alice.generateKeys();
const bobPub = bob.generateKeys('hex', 'compressed');      // 33-byte compressed point, as hex
const s1 = alice.computeSecret(bobPub, 'hex');
const s2 = bob.computeSecret(alicePub);
console.log(s1.length, s1.equals(s2));                      // 32 true

// A compressed point converts back to the uncompressed form.
console.log(crypto.ECDH.convertKey(bobPub, 'prime256v1', 'hex', 'hex', 'uncompressed').length); // 130
console.log(crypto.getCurves().includes('secp384r1'));      // true

// diffieHellman: the same agreement over two X25519 KeyObjects.
const a = crypto.generateKeyPairSync('x25519');
const b = crypto.generateKeyPairSync('x25519');
const k1 = crypto.diffieHellman({ privateKey: a.privateKey, publicKey: b.publicKey });
const k2 = crypto.diffieHellman({ privateKey: b.privateKey, publicKey: a.publicKey });
console.log(k1.length, k1.equals(k2));                      // 32 true

// RSA-OAEP: encrypt to a public key, decrypt with the private one.
const { publicKey, privateKey } = crypto.generateKeyPairSync('rsa', { modulusLength: 2048 });
const ct = crypto.publicEncrypt({ key: publicKey, oaepHash: 'sha256' }, Buffer.from('meet me in Thessaloniki'));
console.log(ct.length);                                     // 256
console.log(crypto.privateDecrypt({ key: privateKey, oaepHash: 'sha256' }, ct).toString()); // meet me in Thessaloniki

// A wrong label is a decoding error, not garbage plaintext.
try {
  crypto.privateDecrypt({ key: privateKey, oaepHash: 'sha256', oaepLabel: Buffer.from('other') }, ct);
} catch (e: any) {
  console.log(e.code);                                      // ERR_OSSL_RSA_OAEP_DECODING_ERROR
}
