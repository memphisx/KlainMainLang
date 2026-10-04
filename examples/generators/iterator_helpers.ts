// --- Iterator helpers (ES2025) ---
// Every iterator — a generator, an array's .values(), a Map's or Set's —
// has map/filter/take/drop/flatMap (lazy: each returns a new iterator) and
// reduce/toArray/forEach/some/every/find (which consume it).

function* naturals() {
    let n = 1;
    while (true) yield n++;
}

// Lazy all the way down: an infinite generator is fine as long as take()
// bounds it before anything drains it.
const evensSquared = naturals()
    .filter((n) => n % 2 === 0)
    .map((n) => n * n)
    .take(4)
    .toArray();
console.log(evensSquared); // [ 4, 16, 36, 64 ]

// Consumers stop as soon as they know the answer, and close the source.
function* noisy() {
    try {
        console.log("yield 1");
        yield 1;
        console.log("yield -1");
        yield -1;
        console.log("never reached");
        yield 2;
    } finally {
        console.log("closed");
    }
}
console.log(noisy().every((x) => x > 0)); // yield 1, yield -1, closed, false

// Map and Set iterators get the same helpers.
const prices = new Map([["tea", 3], ["cake", 5], ["soup", 7]]);
const total = prices.values().reduce((sum, p) => sum + p, 0);
console.log(total); // 15
console.log(prices.keys().find((k) => k.startsWith("c"))); // cake

// flatMap flattens one level of each mapped iterable.
const pairs = new Set(["ab", "cd"]).values().flatMap((s) => s.split("")).toArray();
console.log(pairs); // [ 'a', 'b', 'c', 'd' ]

// drop skips, forEach visits with an index.
["Thessaloniki", "Athens", "Patras"].values().drop(1).forEach((city, i) => {
    console.log(i, city);
});
