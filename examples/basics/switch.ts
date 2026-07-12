// switch dispatches on a value. Cases are compared with ===.
// Execution falls through unless stopped with break or return.
// default runs when no case matches.

// --- Integer switch with break ---
function dayName(d: number): string {
    switch (d) {
        case 1: return 'Mon'
        case 2: return 'Tue'
        case 3: return 'Wed'
        case 4: return 'Thu'
        case 5: return 'Fri'
        default: return 'Weekend'
    }
}
console.log(dayName(1))   // Mon
console.log(dayName(3))   // Wed
console.log(dayName(5))   // Fri
console.log(dayName(7))   // Weekend

// --- String switch ---
function httpStatus(code: number): string {
    switch (code) {
        case 200: return 'OK'
        case 201: return 'Created'
        case 400: return 'Bad Request'
        case 401: return 'Unauthorized'
        case 404: return 'Not Found'
        case 500: return 'Internal Server Error'
        default:  return 'Unknown'
    }
}
console.log(httpStatus(200))   // OK
console.log(httpStatus(404))   // Not Found
console.log(httpStatus(500))   // Internal Server Error
console.log(httpStatus(418))   // Unknown

// --- Switch on a string discriminant ---
let lang: string = 'typescript'
switch (lang) {
    case 'go':
        console.log(1)
        break
    case 'typescript':
        console.log(2)
        break
    case 'rust':
        console.log(3)
        break
    default:
        console.log(0)
}
// 2

// --- Switch with explicit break ---
let x: number = 3
let result: number = 0
switch (x) {
    case 1:
        result = 10
        break
    case 2:
        result = 20
        break
    case 3:
        result = 30
        break
    default:
        result = -1
}
console.log(result)  // 30

// --- Switch with no matching case (hits default) ---
let y: number = 99
switch (y) {
    case 1:
        console.log(100)
        break
    case 2:
        console.log(200)
        break
    default:
        console.log(999)
}
// 999

// --- Switch without a default ---
let z: number = 5
switch (z) {
    case 1:
        console.log(1)
        break
    case 5:
        console.log(5)
        break
}
// 5

// --- Fallthrough: no break means execution continues into the next case ---
let grade: number = 2
switch (grade) {
    case 1:
        console.log(10)   // not printed (grade != 1)
    case 2:
        console.log(20)   // printed (grade == 2)
        // no break: falls through
    case 3:
        console.log(30)   // also printed (fallthrough from case 2)
        break
    case 4:
        console.log(40)   // not printed (break stopped execution)
}
// 20 then 30

// --- default in a non-last position ---
let code: number = 7
switch (code) {
    case 1:
        console.log(1)
        break
    default:
        console.log(0)  // printed first (code doesn't match 1 or 2)
        break
    case 2:
        console.log(2)
        break
}
// 0

// --- break in a for loop ---
let found: number = -1
for (let i = 0; i < 20; i++) {
    if (i * i > 50) {
        found = i
        break
    }
}
console.log(found)  // 8  (8*8=64 > 50)

// --- break in a while loop ---
let count: number = 0
while (count < 100) {
    count = count + 1
    if (count === 7) {
        break
    }
}
console.log(count)  // 7

// --- break in nested loops exits only the inner loop ---
let hits: number = 0
for (let i = 0; i < 5; i++) {
    for (let j = 0; j < 5; j++) {
        if (j === 2) {
            break  // exits inner loop only
        }
        hits = hits + 1
    }
}
console.log(hits)  // 10  (5 outer iterations × 2 inner before break)

// --- switch as a compute function ---
function quadrant(x: number, y: number): number {
    let xSign: number = x >= 0 ? 1 : 0
    let ySign: number = y >= 0 ? 1 : 0
    let key: number = xSign * 2 + ySign
    switch (key) {
        case 3: return 1   // x>=0, y>=0
        case 2: return 4   // x>=0, y<0
        case 1: return 2   // x<0,  y>=0
        default: return 3  // x<0,  y<0
    }
}
console.log(quadrant(1, 1))    // 1
console.log(quadrant(1, -1))   // 4
console.log(quadrant(-1, 1))   // 2
console.log(quadrant(-1, -1))  // 3
