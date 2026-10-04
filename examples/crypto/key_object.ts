// KeyObject — node:crypto's handle on a key. Pairs come back as KeyObjects
// unless an encoding is asked for; a KeyObject exports to PEM, DER or JWK,
// imports from any of them, and signs directly.
import crypto from 'crypto';

// A P-256 key pair as two KeyObjects.
const { publicKey, privateKey } = crypto.generateKeyPairSync('ec', { namedCurve: 'P-256' });
console.log(publicKey.type, privateKey.type);         // public private
console.log(publicKey.asymmetricKeyType);             // ec
console.log(publicKey.asymmetricKeyDetails);          // { namedCurve: 'prime256v1' }

// Export in Node's encodings.
const pem = privateKey.export({ type: 'pkcs8', format: 'pem' }) as string;
console.log(pem.split('\n')[0]);                      // -----BEGIN PRIVATE KEY-----
const sec1 = privateKey.export({ type: 'sec1', format: 'pem' }) as string;
console.log(sec1.split('\n')[0]);                     // -----BEGIN EC PRIVATE KEY-----
const jwk = publicKey.export({ format: 'jwk' });
console.log(Object.keys(jwk).join(','), jwk.crv);     // kty,x,y,crv P-256

// ...and back: the same key, whichever form it travelled in.
console.log(crypto.createPrivateKey(pem).equals(privateKey));                        // true
console.log(crypto.createPublicKey({ key: jwk, format: 'jwk' }).equals(publicKey));  // true
console.log(crypto.createPublicKey(privateKey).equals(publicKey));                   // true

// An encrypted PKCS#8 key needs its passphrase.
const locked = privateKey.export({ type: 'pkcs8', format: 'pem', cipher: 'aes-256-cbc', passphrase: 'Thessaloniki' }) as string;
console.log(crypto.createPrivateKey({ key: locked, passphrase: 'Thessaloniki' }).equals(privateKey)); // true
try {
  crypto.createPrivateKey({ key: locked, passphrase: 'wrong' });
} catch (e: any) {
  console.log(e.code);                                // ERR_OSSL_BAD_DECRYPT
}

// Signing with a KeyObject; ECDSA signatures as DER or as IEEE P1363 r || s.
const msg = Buffer.from('signed in Thessaloniki');
const der = crypto.sign('sha256', msg, privateKey);
console.log(crypto.verify('sha256', msg, publicKey, der));                             // true
const raw = crypto.sign('sha256', msg, { key: privateKey, dsaEncoding: 'ieee-p1363' });
console.log(raw.length, crypto.verify('sha256', msg, { key: publicKey, dsaEncoding: 'ieee-p1363' }, raw)); // 64 true

// A secret key holds raw bytes.
const secret = crypto.createSecretKey(Buffer.from('a shared secret'));
console.log(secret.type, secret.symmetricKeySize, secret.export({ format: 'jwk' }).kty); // secret 15 oct
