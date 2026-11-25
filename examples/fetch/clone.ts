// Response.clone() and Request.clone(): the body is teed, so the original
// and the copy are each read on their own; the copy has its own Headers.
// A body already read can't be cloned.

async function main(): Promise<void> {
    const res = new Response('{"city":"Thessaloniki"}', { headers: { 'content-type': 'application/json' } })
    const copy = res.clone()
    copy.headers.set('x-copy', 'yes')

    console.log(await copy.json())                 // { city: 'Thessaloniki' }
    console.log(await res.text())                  // {"city":"Thessaloniki"}
    console.log(res.headers.get('x-copy'))         // null — the copy's Headers are its own

    try {
        res.clone()
    } catch (e) {
        console.log((e as Error).message)          // Response.clone: Body has already been consumed.
    }

    const req = new Request('http://localhost/', { method: 'POST', body: 'hello' })
    const again = req.clone()
    console.log(await req.text(), await again.text())  // hello hello
}

main()
