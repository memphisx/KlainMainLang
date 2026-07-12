// Array.sort — default (ascending) and custom comparator

// Default numeric sort (ascending)
const nums: number[] = [3, 1, 4, 1, 5, 9, 2, 6]
nums.sort()
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
