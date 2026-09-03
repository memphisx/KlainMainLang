// Class decorators (TDD-00161 Stage 4). A class decorator runs at
// class-definition time with the class (its per-class decorator target). The
// common shape — registration: @Component / @Injectable / @Controller / @Entity
// — observes the class and records it somewhere, returning nothing; that runs
// faithfully. (A class decorator that *returns a replacement constructor* is
// refused at runtime for now; the static-class model has no constructor-
// replacement routing yet.)
//
// Multiple class decorators apply bottom-up, and after any member decorators.

// A registry of everything marked @table, built as the classes are defined.
const entities: string[] = [];

// A registration class decorator.
function table(target: any): void {
  entities.push("registered");
  console.log("registered an entity");
}

// A second class decorator, to show bottom-up ordering.
function auditable(target: any): void {
  console.log("marked auditable");
}

@table
@auditable
class User {
  id: number = 0;
  name: string = "";

  describe(): string {
    return "User #" + this.id;
  }
}

@table
class Product {
  sku: string = "";
}

console.log("entities registered:", entities.length);

// The decorated classes are entirely normal at runtime.
const u = new User();
u.id = 7;
console.log(u.describe());
const p = new Product();
p.sku = "ABC-1";
console.log("product sku:", p.sku);
