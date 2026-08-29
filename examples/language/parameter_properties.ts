// TypeScript constructor parameter properties: an accessibility modifier
// (public/private/protected) or `readonly` on a constructor parameter
// declares a same-named class field and assigns it automatically — no
// separate field declaration or `this.x = x` needed.

class City {
    constructor(public name: string, public population: number, private countryCode: string, readonly founded: number) {}

    describe(): string {
        return this.name + " (" + this.countryCode + "), pop " + String(this.population) + ", founded " + String(this.founded);
    }
}

const c = new City("Thessaloniki", 1030000, "GR", -315);
console.log(c.describe());
console.log(c.name);
console.log(c.founded);
