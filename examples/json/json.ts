
// Map-backed dicts stringify by key iteration.
interface Counts { [k: string]: number; }
const counts: Counts = {};
counts["thessaloniki"] = 2;
console.log(JSON.stringify(counts));
