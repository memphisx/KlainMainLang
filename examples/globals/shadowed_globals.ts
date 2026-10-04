// A program may declare its own binding with a builtin global's name, as
// JavaScript and TypeScript allow: the nearest declaration wins, in a
// function, a block or at the top of the module, and `new` follows it too.

function fakeRandom(): number {
  const Math = { random: (): number => 42 }
  return Math.random()
}

class Map {
  get(key: string): string {
    return 'my map: ' + key
  }
}

function localDate(): string {
  class Date {
    toString(): string {
      return 'a local Date'
    }
  }
  return new Date().toString()
}

console.log(fakeRandom())                    // 42
console.log(new Map().get('k'))              // my map: k
console.log(localDate())                     // a local Date
console.log(Math.floor(3.7))                 // 3 — the builtin, untouched
console.log(new globalThis.Map([[1, 2]]).size) // 1 — the builtin, through the global object
