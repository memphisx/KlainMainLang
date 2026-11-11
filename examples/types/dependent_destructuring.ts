// Dependent destructuring: the leaves of a pattern read from a discriminated
// union narrow together. Testing the discriminant leaf narrows the others,
// for a `const` and for a parameter that is never reassigned.

type Shape =
  | { kind: 'circle'; size: number }
  | { kind: 'label'; size: string }

function describe({ kind, size }: Shape): string {
  if (kind === 'circle') return 'circle of area ' + (Math.PI * size * size).toFixed(2)
  return 'label "' + size.toUpperCase() + '"'
}
console.log(describe({ kind: 'circle', size: 2 }))  // circle of area 12.57
console.log(describe({ kind: 'label', size: 'hi' })) // label "HI"

// An iterator result: value is the yielded number until done.
function* counter(): Generator<number, string> {
  yield 1
  yield 2
  return 'finished'
}
const it = counter()
for (;;) {
  const { done, value } = it.next()
  if (done) {
    console.log('return value', value.length) // return value 8
    break
  }
  console.log('yielded', value * 10) // yielded 10, then yielded 20
}
