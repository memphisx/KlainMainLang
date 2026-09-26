// Semicolons are optional where JavaScript inserts them: a statement ends at a
// line break, before `}` or at the end of the file. A few places refuse a line
// break outright — so `total` then `++visits` on the next line is two
// statements, and `return` on its own line returns nothing.
let total = 10
let visits = 0
total
++visits
console.log(total, visits)

function greeting(city: string): string | undefined {
  if (city === '') return
  return `Kalimera from ${city}`
}
console.log(greeting('Thessaloniki'), greeting(''))

// A `/` after `)` is a regex where a value is expected, division elsewhere.
const words = ['Ladadika', 'Ano Poli']
if (words.length) /poli/i.test(words[1]) && console.log('found the old town')
console.log(words.length / 2)

// Nested type arguments close with plain `>`s; `>>`, `>>>` and `>=` are
// operators only where an operator can stand.
const grid: Array<Array<number>> = [[1, 2], [3, 4]]
console.log(grid[1][0] >> 1, -8 >>> 28, grid.length >= 2)
