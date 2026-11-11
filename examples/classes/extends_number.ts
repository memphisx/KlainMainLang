// Classes extending Number, String and Boolean: each instance is the
// primitive's wrapper object, with the class's own members on top.

class Money extends Number {
  constructor(amount: number, readonly currency: string) {
    super(amount);
  }

  format(): string {
    return `${this.toFixed(2)} ${this.currency}`;
  }
}

class Slug extends String {
  constructor(text: string) {
    super(text.toLowerCase().replace(/\s+/g, "-"));
  }
}

class Flag extends Boolean {}

const price = new Money(12.5, "EUR");
console.log(price.format(), price.valueOf() * 2, price instanceof Number);
console.log(price);

const slug = new Slug("Hello Thessaloniki");
console.log(`${slug}`, slug.length, slug.toUpperCase(), JSON.stringify({ slug }));

const flag = new Flag(0);
console.log(flag.valueOf(), flag instanceof Boolean, typeof flag);
