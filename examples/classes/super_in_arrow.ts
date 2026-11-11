// An arrow function inside a method shares the method's `super`, just as it
// shares its `this`.
class Parent {
  greet(n: number): string {
    return "parent " + n;
  }
}

class Child extends Parent {
  greet(n: number): string {
    const once = () => super.greet(n + 1);
    const many = (k: number) => [1, 2].map((i) => super.greet(i * k));
    return once() + " | " + many(10).join(",");
  }
}

console.log(new Child().greet(1));
