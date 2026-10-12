// Class fields keyed by a symbol the program declares (TDD-00246): state a
// class keeps out of its string keys, as Node's own classes do.

const kState = Symbol('state')
const kHits = Symbol('hits')

class Counter {
  name: string;
  [kState]: 'idle' | 'running' = 'idle';
  [kHits]: number = 0

  constructor(name: string) {
    this.name = name
  }

  hit(): number {
    this[kState] = 'running'
    this[kHits] += 1
    return this[kHits]
  }
}

const c = new Counter('requests')
c.hit()
c.hit()
console.log(c[kHits], c[kState])
console.log(Object.keys(c), JSON.stringify(c))
console.log(c)
