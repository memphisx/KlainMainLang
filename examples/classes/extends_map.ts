// class X extends Map / Set: a builtin collection with methods of its own.
// The instance is the Map itself: Map's methods, iteration, instanceof and
// console.log behave as in Node, and it passes where a Map is expected.

class Visits extends Map<string, number> {
  record(city: string): number {
    const n = (this.get(city) ?? 0) + 1;
    this.set(city, n);
    return n;
  }
  busiest(): string {
    let best = "";
    let most = 0;
    for (const [city, n] of this) {
      if (n > most) {
        best = city;
        most = n;
      }
    }
    return best;
  }
}

class Tags extends Set<string> {
  label: string;
  constructor(label: string, tags: string[]) {
    super(tags);
    this.label = label;
  }
  describe(): string {
    return `${this.label}: ${[...this].join(", ")}`;
  }
}

function total(m: Map<string, number>): number {
  let sum = 0;
  m.forEach((n) => (sum += n));
  return sum;
}

const visits = new Visits([["Athens", 1]]);
visits.record("Thessaloniki");
visits.record("Thessaloniki");
visits.record("Athens");
console.log(visits.busiest(), visits.size, total(visits));
console.log(visits instanceof Visits, visits instanceof Map);
console.log(visits);

const tags = new Tags("food", ["bougatsa", "koulouri"]);
tags.add("bougatsa");
console.log(tags.describe(), tags.size, tags.has("koulouri"));
console.log(tags);

const boxed: any = visits;
console.log(boxed.get("Thessaloniki"), boxed instanceof Map, boxed);
