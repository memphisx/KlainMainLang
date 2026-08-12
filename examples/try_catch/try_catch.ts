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

// Built-in Error subtypes (TDD-00013 Option A): a small fixed kind enum
// (Error, TypeError, RangeError, SyntaxError, EvalError, URIError,
// ReferenceError) — construct one directly, or narrow a caught value with
// instanceof, same as real TypeScript/JavaScript.
function validateAge(age: number): void {
  if (age < 0) {
    throw new RangeError('age cannot be negative')
  }
  throw new TypeError('age must be a number')
}

try {
  validateAge(-5)
} catch (e) {
  if (e instanceof RangeError) {
    console.log('range error: ' + e.message)
  } else if (e instanceof TypeError) {
    console.log('type error: ' + e.message)
  }
  console.log('e.name = ' + e.name)
  console.log('instanceof Error: ' + (e instanceof Error))
}

// Optional catch binding: `catch { ... }` with no `(e)` at all — useful when
// only the fact that something threw matters, not the error value itself.
try {
  throw new Error('ignored')
} catch {
  console.log('caught, no binding needed')
}

// Destructured catch binding: `catch ({ message, name })` pulls the caught
// error's own fields directly into locals, renaming with `key: local` same
// as any other object destructuring.
try {
  throw new TypeError('bad type')
} catch ({ message, name: kind }) {
  console.log(message + ' (' + kind + ')')
}
