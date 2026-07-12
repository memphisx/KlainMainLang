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
