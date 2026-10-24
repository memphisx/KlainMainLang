async function add(a: number, b: number): Promise<number> {
    return a + b
}

async function greet(name: string): Promise<string> {
    return `Hello, ${name}!`
}

async function logAndReturn(n: number): Promise<number> {
    console.log("computing...")
    return n * n
}

async function doNothing(): Promise<void> {
    console.log("done")
}

const sum = await add(10, 32)
console.log(sum)

const msg = await greet("TypeGo")
console.log(msg)

const sq = await logAndReturn(7)
console.log(sq)

await doNothing()

// A `finally` block runs on an early `return` inside an async function — the
// cleanup happens before the promise settles, and the finally may itself await
// (ADR-00612). Runs identically under Node.js.
async function fetchWithCleanup(id: number): Promise<number> {
    console.log("open", id)
    try {
        return id * 10
    } finally {
        console.log("close", id)
    }
}
const result = await fetchWithCleanup(3)
console.log("result", result)  // open 3 / close 3 / result 30

// An async arrow with one bare parameter, typed from where it is used.
const doubled = await Promise.all([1, 2, 3].map(async n => n * 2))
console.log("doubled", doubled.join(","))  // doubled 2,4,6
