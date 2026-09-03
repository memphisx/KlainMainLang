// Standard (TC39) decorators — TDD-00161 Stage 5. Compile with the dialect
// flag:
//   klainmain -decorators=standard standard_decorators.ts
//
// In the TC39 dialect every decorator is called `(value, context)` — unlike the
// experimental `(target, key, descriptor)` dialect (the default; see the other
// examples in this folder). `context` carries `{ kind, name, static, private }`.
// A decorator returns the (possibly replaced) value, or `undefined` to keep it.
//
// This slice supports class decorators (observe; a returned *replacement* class
// is refused at runtime — the static-class model has no runtime class creation)
// and method decorators (a returned function re-routes the method's calls).
// Field, accessor, and getter/setter decorators, and context.addInitializer,
// are the standard dialect's remaining sub-stage.

// A class decorator: observe and register.
function registered(value: any, context: any): any {
  console.log("registered", context.kind, context.name);
  return value; // identity — keep the class
}

// A method decorator that wraps: replace the method with a logging stub.
function traced(value: any, context: any): any {
  console.log("tracing", context.kind, context.name);
  return function (): string {
    return "traced result";
  };
}

@registered
class Service {
  @traced
  run(): string {
    return "real result";
  }

  plain(): string {
    return "plain result";
  }
}

console.log("--- class defined ---");
const s = new Service();
console.log("run()   ->", s.run()); // replaced by the traced stub
console.log("plain() ->", s.plain()); // untouched
