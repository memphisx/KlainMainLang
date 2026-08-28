// A string index signature `{ [k: string]: V }` types a dictionary of arbitrary
// string keys (TDD-00130). It's backed by a map, so bracket read/write and
// Object.keys all work — no fixed field set required.

interface Scores {
  [player: string]: number;
}

const scores: Scores = { alice: 10, bob: 7 };
scores["carol"] = 12;

console.log(scores["alice"]);            // 10
console.log(scores["carol"]);            // 12

let total = 0;
for (const name of Object.keys(scores)) {
  total += scores[name];
}
console.log(total);                      // 29

// The inline object-type form works the same way.
type Env = { [key: string]: string };
const env: Env = { HOME: "/root" };
env["SHELL"] = "/bin/sh";
console.log(env["HOME"] + " " + env["SHELL"]); // /root /bin/sh
