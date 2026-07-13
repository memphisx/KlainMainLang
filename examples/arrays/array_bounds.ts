const arr: number[] = [10, 20, 30]

console.log(arr[0])
console.log(arr[2])

try {
  console.log(arr[5])
} catch (e) {
  console.log('caught: ' + e.message)
}

try {
  arr[-1] = 99
} catch (e) {
  console.log('caught: ' + e.message)
}

arr[1] = 99
console.log(arr[1])
