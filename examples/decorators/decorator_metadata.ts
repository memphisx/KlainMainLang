// emitDecoratorMetadata (TDD-00161 Stage 3). Compile with the flag:
//   klainmain -emit-decorator-metadata decorator_metadata.ts
// so decorated members get design:type / design:paramtypes / design:returntype
// reflection metadata, readable through Reflect.getMetadata — the mechanism
// dependency-injection frameworks build on. Without the flag the design:*
// metadata is simply absent (the reads below are guarded), and the direct
// Reflect.defineMetadata / getMetadata calls still work.
//
// Design-type values are name-carrying descriptor objects ({ name: "Number" },
// { name: "Dep" }, …): they report the correct type name but are not the real
// runtime constructors, so inspect them by `.name`, not identity.

class Repository {}

// A property decorator that records each field's declared type.
function column(target: any, key: string): void {
  const type: any = Reflect.getMetadata("design:type", target, key);
  if (type) {
    console.log("column", key, "of type", type.name);
  }
}

// A method decorator that inspects the full signature.
function handler(target: any, key: string, descriptor: any): void {
  const ret: any = Reflect.getMetadata("design:returntype", target, key);
  const params: any = Reflect.getMetadata("design:paramtypes", target, key);
  if (ret && params) {
    // find(id, active) has two parameters; read their design types directly.
    const p0: any = params[0];
    const p1: any = params[1];
    console.log("handler", key + "(" + p0.name + ", " + p1.name + ") ->", ret.name);
  }
}

class UserController {
  @column
  id: number = 0;

  @column
  name: string = "";

  @column
  repo: Repository = new Repository();

  @handler
  find(id: number, active: boolean): string {
    return "user " + id;
  }
}

new UserController();

// Metadata can also be written and read directly, framework-style — this works
// regardless of the flag. The target must be a plain (dynamic) object.
const routes: any = {};
Reflect.defineMetadata("route", "/users", routes, "find");
console.log("route for find:", Reflect.getMetadata("route", routes, "find"));
console.log("has route:", Reflect.hasMetadata("route", routes, "find"));
