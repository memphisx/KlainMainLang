// A class declared inside a function closes over the function's locals:
// every call makes a class of its own, with that call's bindings.

function makeGreeter(greeting: string) {
    let greeted = 0
    class Greeter {
        constructor(private who: string) {}
        greet(): string {
            greeted++
            return `${greeting}, ${this.who}! (#${greeted})`
        }
    }
    const a = new Greeter("Thessaloniki")
    const b = new Greeter("world")
    return [a.greet(), b.greet(), a.greet(), `greeted ${greeted}`]
}

console.log(makeGreeter("Hello"))
console.log(makeGreeter("Γεια"))

function stack(limit: number) {
    class Bounded {
        items: number[] = []
        push(n: number): boolean {
            if (this.items.length >= limit) return false
            this.items.push(n)
            return true
        }
    }
    return new Bounded()
}

const s = stack(2)
console.log(s.push(1), s.push(2), s.push(3), s)
