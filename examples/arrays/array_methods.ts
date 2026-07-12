// =============================================================================
// push
// =============================================================================

// Start empty (size 0), push into it
let arr = new Array<number>(0)
arr.push(10)
arr.push(20)
arr.push(30)
console.log(arr.length)  // 3
console.log(arr[0])      // 10
console.log(arr[1])      // 20
console.log(arr[2])      // 30

// push returns the new length
let len: number = arr.push(40)
console.log(len)         // 4
console.log(arr[3])      // 40

// Push into a literal array
let nums = [1, 2, 3]
nums.push(4)
nums.push(5)
console.log(nums.length) // 5
console.log(nums[4])     // 5

// Push inside a loop to build a dynamic collection
let evens = new Array<number>(0)
for (let i = 0; i < 5; i++) {
    evens.push(i * 2)
}
console.log(evens.length) // 5
console.log(evens[0])     // 0
console.log(evens[4])     // 8

// Function that filters positives into a new array
function positives(arr: number[]): number[] {
    let out = new Array<number>(0)
    for (let i = 0; i < arr.length; i++) {
        if (arr[i] > 0) {
            out.push(arr[i])
        }
    }
    return out
}

let mixed = [-3, 1, -1, 4, 0, 7]
let pos = positives(mixed)
console.log(pos.length)  // 3
console.log(pos[0])      // 1
console.log(pos[1])      // 4
console.log(pos[2])      // 7

// =============================================================================
// pop
// =============================================================================

let stack = [10, 20, 30, 40, 50]

let a = stack.pop()
console.log(a)            // 50
console.log(stack.length) // 4

let b = stack.pop()
console.log(b)            // 40
console.log(stack.length) // 3

// push then pop round-trips
stack.push(99)
console.log(stack.length) // 4
console.log(stack.pop())  // 99
console.log(stack.length) // 3

// Use pop to drain in a loop (stack pattern)
let s = new Array<number>(0)
s.push(1)
s.push(2)
s.push(3)

let total: number = 0
while (s.length > 0) {
    total = total + s.pop()
}
console.log(total)        // 6
console.log(s.length)     // 0

// =============================================================================
// shift / unshift
// =============================================================================

let queue = [10, 20, 30, 40, 50]

// shift removes and returns the first element
let first = queue.shift()
console.log(first)        // 10
console.log(queue.length) // 4
console.log(queue[0])     // 20

let second = queue.shift()
console.log(second)       // 20
console.log(queue.length) // 3
console.log(queue[0])     // 30

// unshift prepends, returns new length
let newLen: number = queue.unshift(5)
console.log(newLen)       // 4
console.log(queue[0])     // 5
console.log(queue[1])     // 30
console.log(queue.length) // 4

queue.unshift(1)
queue.unshift(0)
console.log(queue.length) // 6
console.log(queue[0])     // 0
console.log(queue[1])     // 1
console.log(queue[2])     // 5

// Sliding window: push to back, shift from front
let win = [1, 2, 3]
win.push(4)
win.shift()
console.log(win.length)   // 3
console.log(win[0])       // 2
console.log(win[2])       // 4

// =============================================================================
// splice
// =============================================================================

let src = [10, 20, 30, 40, 50]

// Remove 2 elements from index 1
let removed = src.splice(1, 2)
console.log(removed.length) // 2
console.log(removed[0])     // 20
console.log(removed[1])     // 30
console.log(src.length)     // 3
console.log(src[0])         // 10
console.log(src[1])         // 40
console.log(src[2])         // 50

// Remove 1 element from the front
let head = src.splice(0, 1)
console.log(head[0])        // 10
console.log(src.length)     // 2
console.log(src[0])         // 40

// Remove 1 element from the end
let tail = src.splice(1, 1)
console.log(tail[0])        // 50
console.log(src.length)     // 1
console.log(src[0])         // 40

// splice then use returned array
let big = [1, 2, 3, 4, 5, 6]
let mid = big.splice(2, 3)   // removes [3,4,5], big becomes [1,2,6]
console.log(big.length)      // 3
console.log(big[0])          // 1
console.log(big[2])          // 6
console.log(mid.length)      // 3
console.log(mid[0])          // 3
console.log(mid[2])          // 5

// splice with explicit type annotation
let typed = [100, 200, 300, 400]
let chunk: number[] = typed.splice(1, 2)
console.log(chunk[0])        // 200
console.log(chunk[1])        // 300
console.log(typed.length)    // 2
