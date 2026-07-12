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
