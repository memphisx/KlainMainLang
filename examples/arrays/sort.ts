// Array.sort — default (lexicographic) and custom comparator

// Default sort stringifies each element and compares lexicographically, just
// like real JS — so multi-digit numbers do NOT sort numerically.
const codes: number[] = [10, 1, 21, 2]
codes.sort()
console.log(codes.join(','))  // 1,10,2,21  (NOT 1,2,10,21)

// For a numeric ascending sort, pass a comparator.
const nums: number[] = [3, 1, 4, 1, 5, 9, 2, 6]
nums.sort((a: number, b: number) => a - b)
console.log(nums[0])  // 1
console.log(nums[1])  // 1
console.log(nums[2])  // 2
console.log(nums[7])  // 9

// Custom sort (descending)
const desc: number[] = [3, 1, 4, 1, 5, 9, 2, 6]
desc.sort((a: number, b: number) => b - a)
console.log(desc[0])  // 9
console.log(desc[7])  // 1

// String sort (default lexicographic)
const words: string[] = ['banana', 'apple', 'cherry', 'avocado']
words.sort()
console.log(words[0])  // apple
console.log(words[1])  // avocado
console.log(words[2])  // banana
console.log(words[3])  // cherry
