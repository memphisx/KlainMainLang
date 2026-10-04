// A `number` that receives undefined keeps it, as in JavaScript: through a
// value typed `any`, a call that leaves an argument out, or `x!` over an
// absent value.

const config: any = JSON.parse('{"port": 8080}')
const port: number = config.port
const timeout: number = config.timeout

console.log(port, timeout, typeof timeout, timeout === undefined)
console.log(timeout ?? 30, timeout + 1, JSON.stringify({ port, timeout }))

const handler: any = (status: number, retries: number) =>
    `status ${status}, retries ${retries} (${typeof retries})`
console.log(handler(200))

const scores = [72, 91, 85]
const top = scores.find((s) => s > 95)
console.log(top!, top === undefined)
