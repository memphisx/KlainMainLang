// Class members named by string and numeric literals, and numeric keys
// named by their value as JavaScript writes it (0x10 is the key "16").
class Grid {
  "row-count" = 2;
  0 = "origin";
  0x10 = "sixteen";
  "cell at"(r: number, c: number): string {
    return `(${r}, ${c})`;
  }
  1(): string {
    return "one";
  }
}

const g = new Grid();
const key = "row-count";
console.log(g[key], g[0], g[16], g["cell at"](1, 2), g[1]());
console.log(Object.keys(g));
console.log(g);

const sizes = { 1.50: "one and a half", 1e3: "a thousand" };
console.log(Object.keys(sizes), sizes[1.5], sizes["1000"]);
