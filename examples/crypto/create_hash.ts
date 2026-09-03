// crypto.createHash (TDD-00159) — Node's Hash object: createHash(algorithm)
// then .update(data).digest(encoding). Accumulates every update and digests
// over the whole input via OpenSSL/CommonCrypto. md5/sha1/sha256/sha384/sha512;
// 'hex'/'base64' encodings, or a Buffer when the encoding is omitted.
import crypto from 'crypto'

// Content hashing — a checksum / etag.
console.log('sha256(hello):', crypto.createHash('sha256').update('hello').digest('hex'))

// md5, still supported for non-security digests (Node keeps it too).
console.log('md5(hello):', crypto.createHash('md5').update('hello').digest('hex'))

// Chained and multi-update are equivalent — the digest is over the whole input.
const h = crypto.createHash('sha1')
h.update('the sample ')
h.update('nonce')
console.log('sha1(streamed):', h.digest('hex'))

// The RFC 6455 §1.3 Sec-WebSocket-Accept, by hand — this is exactly what a
// faithful Node WebSocket server computes inside its `upgrade` handler.
const key = 'dGhlIHNhbXBsZSBub25jZQ=='
const accept = crypto.createHash('sha1')
  .update(key + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11')
  .digest('base64')
console.log('ws accept:', accept)

// No encoding → a Buffer (Uint8Array) of the raw digest bytes.
const digest = crypto.createHash('sha256').update('abc').digest()
console.log('sha256(abc) bytes:', digest.length)

// base64url (URL-safe, unpadded) — common for tokens.
console.log('sha256(abc) b64url:', crypto.createHash('sha256').update('abc').digest('base64url'))

// crypto.createHmac(algorithm, key) — a keyed MAC, same update/digest surface.
const sig = crypto.createHmac('sha256', 'my-secret-key')
  .update('message to authenticate')
  .digest('hex')
console.log('hmac-sha256:', sig)

