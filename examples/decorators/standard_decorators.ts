// Standard (TC39) decorators — TDD-00161 Stage 5. Compile with the dialect
// flag:
//   klainmain -decorators=standard standard_decorators.ts
//
// In the TC39 dialect every decorator is called `(value, context)` — unlike the
// experimental `(target, key, descriptor)` dialect (the default; see the other
// examples in this folder). `context` carries `{ kind, name, static, private }`.
// A decorator returns the (possibly replaced) value, or `undefined` to keep it.
//
// Class decorators observe (a returned *replacement* class is refused at
// runtime — the static-class model has no runtime class creation); method
// decorators, instance or static, may return a replacement the method's calls
// then reach; field decorators, instance or static, may return an initializer
// that transforms the field's initial value.

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

// Static members: a static method decorator's replacement is what `Clock.now`
// reads and calls; a static field decorator's initializer runs once, as the
// class's statics initialize.
function fixed(value: any, context: any): any {
  console.log("fixing", context.kind, context.name, "static:", context.static);
  return function (): string {
    return "12:00";
  };
}

function inMinutes(value: any, context: any): any {
  console.log("converting", context.kind, context.name, "static:", context.static);
  return function (hours: number): number {
    return hours * 60;
  };
}

class Clock {
  @fixed
  static now(): string {
    return new Date().toISOString();
  }

  @inMinutes
  static day: number = 24;
}

console.log("Clock.now() ->", Clock.now());
console.log("Clock.day   ->", Clock.day);
