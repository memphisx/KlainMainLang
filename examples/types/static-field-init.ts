// Static field initializers (`static x = expr`). Each is lowered to a
// `ClassName.x = expr` assignment run once, in declaration order, in the class's
// static-initialization function at program start — ahead of any `static {}`
// block the class also declares. See docs/status/LANGUAGE-CONSTRUCTS.md.

class Counter {
  static total = 0                    // typed by inference (number)
  static label: string = "counter"    // explicit annotation
  static limit = 5 * 20               // an initializer expression

  static bump(): void { Counter.total++ }   // ++/-- work on a static field (ADR-00376)
}

console.log(Counter.total)   // 0
console.log(Counter.label)   // counter
console.log(Counter.limit)   // 100

Counter.bump()
Counter.bump()
console.log(Counter.total)   // 2

// Static field initializers run before a static {} block, so the block sees
// their values.
class Registry {
  static a = 1
  static b = 2
  static sum = 0
  static { Registry.sum = Registry.a + Registry.b }
}
console.log(Registry.sum)    // 3

// A later static field initializer can read an earlier one.
class Config {
  static base = 10
  static scaled = 0
  static { Config.scaled = Config.base * 4 }
}
console.log(Config.scaled)   // 40
