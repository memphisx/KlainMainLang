// JSON.stringify of nullable and control-character strings, as Node writes
// them: a `string | null` field holding null is `null`, and a control
// character is escaped as `\u00XX` (or `\b`, `\f`, `\n`, `\r`, `\t`).

interface Profile { name: string | null; score: number | null; tag?: string }

const anon: Profile = { name: null, score: null };
const jo: Profile = { name: "jo", score: 7, tag: "vip" };
console.log(JSON.stringify(anon));        // {"name":null,"score":null}
console.log(JSON.stringify(jo));          // {"name":"jo","score":7,"tag":"vip"}
console.log(JSON.stringify([anon, jo]));  // [{"name":null,"score":null},{"name":"jo","score":7,"tag":"vip"}]

const weird = "a\u0001b\u001f\tc";
console.log(JSON.stringify(weird));       // "a\u0001b\u001f\tc"
console.log(JSON.stringify({ weird }));   // {"weird":"a\u0001b\u001f\tc"}
