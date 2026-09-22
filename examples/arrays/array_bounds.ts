// Reading past the end of an array is `undefined`, exactly as in Node.js —
// not an error. Using that `undefined` as an object is the usual TypeError.
interface Row { name: string }

const arr: number[] = [10, 20, 30]
const rows: Row[] = [{ name: 'first' }]

console.log(arr[0])
console.log(arr[2])
console.log(arr[5], arr[-1])
console.log(arr[5] === undefined, typeof arr[5])
console.log(arr[5] ?? 'no such element')
console.log(rows[3]?.name)

try {
  console.log(rows[3].name)
} catch (e) {
  console.log('caught: ' + (e as Error).message)
}

arr[1] = 99
console.log(arr[1])
