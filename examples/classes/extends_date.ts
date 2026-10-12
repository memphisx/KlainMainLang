// A class extending Date: the instance is the Date itself.

class Day extends Date {
  label: string;

  constructor(year: number, month: number, day: number, label: string) {
    super(Date.UTC(year, month, day));
    this.label = label;
  }

  override toString(): string {
    return `${this.label} (${this.toISOString().slice(0, 10)})`;
  }

  next(label: string): Day {
    const d = new Day(this.getUTCFullYear(), this.getUTCMonth(), this.getUTCDate() + 1, label);
    return d;
  }
}

const opening = new Day(2024, 1, 28, "opening");
const after = opening.next("day two");
console.log(String(opening));
console.log(`${after}`);
console.log(after instanceof Date, after instanceof Day);

// Date's setters act on the instance, wherever it is held.
const schedule: { first: Day } = { first: opening };
schedule.first.setUTCHours(18, 30);
console.log(opening.toISOString());

// Passed where a Date is expected, it is the same object.
function daysBetween(a: Date, b: Date): number {
  return (b.getTime() - a.getTime()) / 86400000;
}
console.log(daysBetween(opening, after));
console.log(JSON.stringify({ opening }));
console.log(opening);
