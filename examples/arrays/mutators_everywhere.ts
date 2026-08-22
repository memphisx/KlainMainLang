// Array mutators on every mutable receiver shape, plus variadic push/unshift,
// and the NaN-correct Math/parse/charCode behavior.

class TaskList {
  titles: string[] = [];
  add(t: string): number {
    return this.titles.push(t);
  }
}

const list = new TaskList();
list.add("write docs");
list.add("fix bugs");
list.titles.unshift("wake up", "coffee in Thessaloniki");
console.log(list.titles.join(" -> "));
const done = list.titles.shift();
console.log("done:", done, "| remaining:", list.titles.length);

const grid: number[][] = [[1, 2], [3, 4]];
grid[0].push(99);
console.log(grid[0].join(","));

const nums: number[] = [5];
console.log(nums.push(6, 7, 8), nums.join(","));

console.log(Math.floor(NaN), Math.min(1, NaN), Math.sign(-Infinity));
console.log(Math.round(-0.5), Math.round(2.5));
console.log(parseInt("abc"), parseInt("12px"), parseFloat("3.5kg"));
console.log("abc".charCodeAt(1), "abc".charCodeAt(42));
