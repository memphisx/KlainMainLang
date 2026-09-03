// Experimental decorators — property and parameter decorators (TDD-00161
// Stage 1). Both are "observe-only": they run once at class-definition time and
// their return value is discarded, so they're a natural fit for building up a
// registry (validation rules, dependency-injection metadata, serialization
// hints) the way real TypeScript frameworks do.
//
// Class and method decorators (which can *replace* the class or member) and
// factory-call decorators (`@rule(...)`) work too — see the other examples in
// this folder. This one stays focused on the observe-only property/parameter
// forms.

// A simple registry the decorators populate as the class is defined.
const requiredFields: string[] = [];
const injectedParams: string[] = [];

// Property decorator: (target, propertyKey). Marks a field as required.
function required(target: any, key: string): void {
  requiredFields.push(key);
  console.log("registered required field:", key);
}

// Parameter decorator: (target, key, parameterIndex). Records an injection
// point; `key` is undefined for a constructor parameter, the method name for a
// method parameter.
function inject(target: any, key: any, index: number): void {
  const where = key === undefined ? "constructor" : key;
  injectedParams.push(where + "#" + index);
  console.log("registered injection:", where, "param", index);
}

class UserService {
  @required
  name: string = "";

  @required
  email: string = "";

  age: number = 0;

  constructor(@inject db: number, @inject logger: number) {}

  notify(@inject channel: string): void {}
}

console.log("--- class defined, registries populated ---");
console.log("required fields:", requiredFields.join(", "));
console.log("injected params:", injectedParams.join(", "));

// The class itself is entirely normal at runtime — decorators changed nothing
// about its shape, they only observed it.
const svc = new UserService(1, 2);
svc.name = "Ada";
svc.email = "ada@example.com";
console.log("instance:", svc.name, svc.email, svc.age);
