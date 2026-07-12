function divide(a: number, b: number): number {
  if (b === 0) {
    throw new Error('division by zero')
  }
  return a / b
}

try {
  const result = divide(10, 2)
  console.log(result)
} catch (e) {
  console.log('caught: ' + e.message)
}

try {
  const result = divide(10, 0)
  console.log(result)
} catch (e) {
  console.log('caught: ' + e.message)
}
