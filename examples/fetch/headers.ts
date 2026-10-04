// Headers as the Fetch standard has it (undici's, ported to TypeScript):
// names keep the case they were given for console.log, lookups are
// case-insensitive, iteration is sorted by name with values combined, and
// set-cookie's values stay apart.

const h = new Headers({ 'Content-Type': 'text/plain', 'X-Trace': ' a ' })
h.append('x-trace', 'b')
h.append('Set-Cookie', 'session=1')
h.append('Set-Cookie', 'theme=dark')

console.log(h)                     // Headers { 'Content-Type': 'text/plain', 'X-Trace': 'a, b', 'Set-Cookie': 'session=1, theme=dark' }
console.log(h.get('x-trace'))      // a, b — whitespace trimmed, values combined
console.log(h.getSetCookie())      // [ 'session=1', 'theme=dark' ]
for (const [name, value] of h) {
    console.log(`${name}: ${value}`)   // sorted by name; each cookie on its own line
}

// Invalid names and values are TypeErrors.
try {
    h.set('bad name', 'x')
} catch (e) {
    console.log((e as Error).message)   // Headers.set: "bad name" is an invalid header name.
}

// A response's headers are immutable when it is an error or a redirect.
try {
    Response.redirect('http://example.com/', 302).headers.set('x', '1')
} catch (e) {
    console.log((e as Error).message)   // immutable
}

// Headers is a class like any other: it can be extended.
class TracedHeaders extends Headers {
    trace(): string { return this.get('x-trace') ?? 'none' }
}
console.log(new TracedHeaders([['X-Trace', 't1']]).trace())   // t1
