// A smoke-test program specifically for verifying whether --static (Stage
// 1, ADR-00032) actually works once libcurl is in the picture — fetch's
// own dependency, and one this project's own docs flagged as a real
// open question: libcurl (and its own transitive deps — OpenSSL, zlib,
// c-ares, nghttp2, ...) all need a statically-linkable build available on
// the Linux build machine for a --static + fetch program to fully succeed.
interface Ip { origin: string }
const r = await fetch('https://httpbin.org/ip')
console.log('status: ' + r.status)
console.log('ok: ' + r.ok)
const info: Ip = r.json()
console.log('origin length > 0: ' + (info.origin.length > 0))
