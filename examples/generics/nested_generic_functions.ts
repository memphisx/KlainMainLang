// A generic function declared inside another function: hoisted like any
// function declaration, it closes over the enclosing function's locals.

function summarize(label: string, scores: number[]) {
  const best = last(sorted(scores));

  function sorted<T>(xs: T[]): T[] {
    return [...xs].sort();
  }
  function last<T>(xs: T[]): T | undefined {
    return xs[xs.length - 1];
  }
  function tagged<T>(value: T): string {
    return `${label}: ${value}`;
  }

  return [tagged(best), tagged(scores.length), tagged(last(["a", "b"]))];
}

console.log(summarize("Thessaloniki", [3, 9, 4]));
