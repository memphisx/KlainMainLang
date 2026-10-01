// A typed REST-client wrapper: `init?: RequestInit` passed through to fetch,
// as TypeScript types it (ADR-01217). Talks to the local httpbin-lite fixture
// `make examples` starts (tools/httpbin-lite/).

const BASE = 'http://127.0.0.1:8765'

async function api(path: string, init?: RequestInit): Promise<string> {
    const res = await fetch(BASE + path, init)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    return res.text()
}

// No init: a GET.
console.log((await api('/get')).length > 0)                         // true

// A JSON body with its own Content-Type.
const echoed = await api('/post', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ city: 'Thessaloniki' }),
})
console.log(echoed.includes('Thessaloniki'))                        // true

// A form body: URLSearchParams sends application/x-www-form-urlencoded.
const form = await api('/post', { method: 'POST', body: new URLSearchParams({ q: 'white tower' }) })
console.log(form.includes('q=white+tower'))                         // true

// Bytes: a Uint8Array is sent as its raw bytes, with no Content-Type.
const bytes = await api('/post', { method: 'POST', body: new Uint8Array([104, 105]) })
console.log(bytes.includes('hi'))                                   // true

// An options object built elsewhere, with fields of its own, is still a
// RequestInit.
const opts = { method: 'DELETE', headers: [['X-Trace', 'abc']] as [string, string][], retries: 3 }
console.log((await api('/delete', opts)).length > 0, opts.retries)  // true 3
