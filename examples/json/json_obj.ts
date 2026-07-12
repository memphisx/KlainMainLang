// JSON.stringify with objects — flat and nested

const user = { name: 'Alice', age: 30 }
console.log(JSON.stringify(user))
// {"name":"Alice","age":30}

const point = { x: 10, y: 20 }
console.log(JSON.stringify(point))
// {"x":10,"y":20}

const flag = { enabled: true, count: 5 }
console.log(JSON.stringify(flag))
// {"enabled":true,"count":5}

// nested object literal inline
const person = { name: 'Alexandros', address: { city: 'Thessaloniki', zip: 10001 } }
console.log(JSON.stringify(person))
// {"name":"Alexandros","address":{"city":"Thessaloniki","zip":10001}}

// float-typed fields (literal-inferred, not via an interface — see STATUS.md's
// Known Limitations for the separate interface-float-field gap)
const result = { score: 9.5 }
console.log(JSON.stringify(result))
// {"score":9.5}

console.log(JSON.stringify(9.5))
// 9.5

// Date fields serialize as an ISO string (toJSON()/toISOString()), not the
// raw millisecond timestamp
const epoch = new Date(0)
console.log(JSON.stringify({ when: epoch }))
// {"when":"1970-01-01T00:00:00.000Z"}

console.log(JSON.stringify(epoch))
// "1970-01-01T00:00:00.000Z"
