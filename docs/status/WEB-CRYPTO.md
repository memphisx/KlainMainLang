# Cryptography (Web Crypto API)

> Part of the [Implementation Status](README.md) index. `crypto.subtle.*` can delegate to OpenSSL or Apple CommonCrypto via C FFI — none of that is implemented yet. `crypto.getRandomValues`/`randomUUID` needed only a real CSPRNG (`arc4random_buf`/`getrandom()`), no external library.

**Coverage**: 2/8 (~25%) · **Strict Coverage**: 1/8 (~13%).

Format: [Status page format](README.md#status-page-format).

| Feature | Status | Caveats | Notes |
|---|---|---|---|
| `crypto.getRandomValues(buffer)` | ✅ | • Fills a plain `number[]` (not a real `Uint8Array`) with random byte values, one per element | • Predates `ArrayBuffer`/TypedArrays ([ADR-00078](../adr/ADR-00078.md)); migrating this to accept a real `Uint8Array` is a separate, not-yet-started follow-up<br>• See [ADR-00024](../adr/ADR-00024.md) |
| `crypto.randomUUID()` | ✅ | | • RFC 4122 version-4 UUID string<br>• See [ADR-00024](../adr/ADR-00024.md) |
| `crypto.subtle.digest(algo, data)` | ❌ | | • SHA-1, SHA-256, SHA-384, SHA-512<br>• Needs an OpenSSL/CommonCrypto FFI binding, not yet started |
| `crypto.subtle.encrypt` / `.decrypt` | ❌ | | • AES-GCM, AES-CBC, RSA-OAEP<br>• Needs an OpenSSL/CommonCrypto FFI binding, not yet started |
| `crypto.subtle.sign` / `.verify` | ❌ | | • HMAC, ECDSA, RSA-PSS<br>• Needs an OpenSSL/CommonCrypto FFI binding, not yet started |
| `crypto.subtle.generateKey` | ❌ | | • Key generation<br>• Needs an OpenSSL/CommonCrypto FFI binding, not yet started |
| `crypto.subtle.importKey` / `.exportKey` | ❌ | | • Key serialization<br>• Needs an OpenSSL/CommonCrypto FFI binding, not yet started |
| `crypto.subtle.deriveKey` / `.deriveBits` | ❌ | | • PBKDF2, HKDF<br>• Needs an OpenSSL/CommonCrypto FFI binding, not yet started |
