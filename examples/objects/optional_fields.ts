// Optional fields (`y?: T`) and destructuring absences are real `T | undefined`
// values: an omitted field or a pattern position past the source's length
// reads back as `undefined`, never a stand-in zero. A destructuring default
// keys off genuine absence — a legitimately stored 0 survives it.

interface User {
  name: string;
  age?: number;
  nickname?: string;
}

const anon: User = { name: "Chrysa" };
const full: User = { name: "Stelios", age: 30, nickname: "stel" };

console.log(anon.age);                  // undefined
console.log(full.age);                  // 30
console.log(anon.nickname);             // undefined
console.log(anon.age === undefined);    // true
console.log(anon.age ?? 18);            // 18
console.log(full.age! + 1);             // 31

// Writing an optional field makes it present.
anon.age = 44;
console.log(anon.age);                  // 44

// Destructuring defaults fire on real absence only.
const zero: User = { name: "z", age: 0 };
const { age = 99 } = zero;
console.log(age);                       // 0 — a stored zero is not "absent"
const m: User = { name: "m" };
const { age: missing = 99 } = m;
console.log(missing);                   // 99

// Optional class fields work the same way.
class Task {
  title: string = "untitled";
  deadline?: number;
}
const t = new Task();
console.log(t.deadline);                // undefined

// Array destructuring past the source's length binds undefined.
const parts: string[] = ["host"];
const [host, port] = parts;
console.log(host);                      // host
console.log(port);                      // undefined
console.log(port ?? "8080");            // 8080
