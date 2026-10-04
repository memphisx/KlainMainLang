// Function.prototype.bind fixes leading arguments, whatever the
// parameter list: a rest parameter, an array, an optional value.

function total(tax: number, ...prices: number[]): number {
    return prices.reduce((sum, p) => sum + p, 0) * (1 + tax)
}
const withVat = total.bind(null, 0.24)
console.log(withVat(10, 20), withVat())

const atLeastZero = Math.max.bind(null, 0)
console.log(atLeastZero(-5, -2), atLeastZero(3))

function describe(tags: string[], sep?: string): string {
    return tags.join(sep ?? ", ")
}
const cities = describe.bind(null, ["Thessaloniki", "Athens"])
console.log(cities(), cities(" | "), cities.name, cities.length)
