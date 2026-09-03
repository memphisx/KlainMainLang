// Factory (parameterized) decorators — `@dec(...)` (TDD-00161). The decorator
// expression is a *call*: the factory runs first and returns the actual
// decorator, which then decorates the target. This is the shape almost every
// real framework uses — `@Controller('/api')`, `@Get('/users')`,
// `@Injectable()`, `@Column({ type: 'text' })`.
//
// Works when the returned decorator is a named function or a typed closure. A
// returned `any`-typed closure that captures the factory's arguments hits a
// separate capture limitation — annotate the factory's return type to route it
// through the typed-closure path (as `route` does below).

const routes: string[] = [];

// A class decorator factory: records a controller's route prefix.
function Controller(prefix: string): (target: any) => void {
  return function (target: any): void {
    routes.push(prefix);
    console.log("controller registered at", prefix);
  };
}

// A method decorator factory: records a route path (typed return so the
// captured `path` rides the typed-closure path).
function Get(path: string): (target: any, key: string, desc: any) => void {
  return function (target: any, key: string, desc: any): void {
    console.log("GET", path, "->", key);
  };
}

// A property decorator factory.
function Column(kind: string): (target: any, key: string) => void {
  return function (target: any, key: string): void {
    console.log("column", key, "is", kind);
  };
}

@Controller("/users")
class UserController {
  @Column("uuid")
  id: string = "";

  @Get("/")
  list(): string {
    return "all users";
  }

  @Get("/:id")
  find(): string {
    return "one user";
  }
}

console.log("--- defined; routes:", routes.join(", "), "---");
const c = new UserController();
console.log(c.list(), "/", c.find());
