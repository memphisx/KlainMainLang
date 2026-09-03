// Method decorators (TDD-00161 Stage 2). A method decorator runs at
// class-definition time with (target, propertyKey, descriptor), where
// `descriptor.value` is the method as a callable. It can:
//   - observe (log/register) and leave the method unchanged, or
//   - replace `descriptor.value` (or return a whole new descriptor) — calls to
//     the method then route to the replacement.
//
// Supported: plain instance methods on a non-generic class. Accessor (get/set),
// static, and generator method decorators are a clean compile-time rejection.

const registered: string[] = [];

// Observe-only: record that the method exists, leave it running as written.
function route(target: any, key: string, descriptor: any): void {
  registered.push(key);
  console.log("registered route:", key);
}

// Replacement: swap the implementation for a fixed stub (a plain,
// non-capturing replacement — the common shape that doesn't need the original).
function stubbed(target: any, key: string, descriptor: any): void {
  descriptor.value = function (): string {
    return "stubbed!";
  };
}

class Api {
  @route
  index(): string {
    return "index page";
  }

  @route
  @stubbed
  health(): string {
    return "real health check";
  }
}

const api = new Api();
console.log("routes:", registered.join(", "));
console.log("index() ->", api.index());   // unchanged
console.log("health() ->", api.health()); // replaced by the stub
