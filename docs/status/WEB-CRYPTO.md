# Cryptography (Web Crypto API)

> Part of the [Implementation Status](README.md) index. `crypto.subtle.*` can delegate to OpenSSL or Apple CommonCrypto via C FFI — none of that is implemented yet. `crypto.getRandomValues`/`randomUUID` needed only a real CSPRNG (`arc4random_buf`/`getrandom()`), no external library.

**Coverage**: 25% (2/8).

**Caveats**: All of `crypto.subtle.*` (digest, encrypt/decrypt, sign/verify, key generation/import/export/derive) is unimplemented — needs an OpenSSL/CommonCrypto FFI binding, not yet started.

| API | Status | Notes |
|---|---|---|
| `crypto.getRandomValues(buffer)` | ✅ | Fills a plain `number[]` (not a real `Uint8Array`) with random byte values, one per element — predates `ArrayBuffer`/TypedArrays ([ADR-00078](../adr/ADR-00078.md)); migrating this to accept a real `Uint8Array` is a separate, not-yet-started follow-up. See [ADR-00024](../adr/ADR-00024.md). |
| `crypto.randomUUID()` | ✅ | RFC 4122 version-4 UUID string. See [ADR-00024](../adr/ADR-00024.md). |
| `crypto.subtle.digest(algo, data)` | ❌ | SHA-1, SHA-256, SHA-384, SHA-512 |
| `crypto.subtle.encrypt` / `.decrypt` | ❌ | AES-GCM, AES-CBC, RSA-OAEP |
| `crypto.subtle.sign` / `.verify` | ❌ | HMAC, ECDSA, RSA-PSS |
| `crypto.subtle.generateKey` | ❌ | Key generation |
| `crypto.subtle.importKey` / `.exportKey` | ❌ | Key serialization |
| `crypto.subtle.deriveKey` / `.deriveBits` | ❌ | PBKDF2, HKDF |
