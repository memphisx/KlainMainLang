// A function that returns a class makes a new class on every call, each
// closing over that call's arguments.

function makeUnit(symbol: string, factor: number) {
    let created = 0
    class Unit {
        value: number
        constructor(value: number) {
            this.value = value
            created++
        }
        toBase(): number {
            return this.value * factor
        }
        toString(): string {
            return `${this.value}${symbol} (${created} made)`
        }
    }
    return Unit
}

const Km = makeUnit("km", 1000)
const Mi = makeUnit("mi", 1609.344)

const trip = new Km(42)
const run = new Mi(3)
console.log(String(trip), trip.toBase(), String(run), run.toBase())
console.log(trip instanceof Km, trip instanceof Mi, Km === Mi, Km.name)
console.log(String(new Km(1)))
