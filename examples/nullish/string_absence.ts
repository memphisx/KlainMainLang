// A string that is undefined (an element past the end) and one that is
// null (from JSON) stay distinct, as in Node.
function describe(label: string, s: string): void {
  console.log(label, typeof s, s === undefined, s === null, s ?? "(absent)");
}

const words = ["alpha"];
describe("past the end:", words[3]);

const rec: { name: string } = JSON.parse('{"name":null}');
describe("JSON null:", rec.name);
console.log(JSON.stringify({ a: words[3], b: rec.name }));
