// --- Allocation-site widening: a fresh object into an `any` slot ---
// TDD-00155 Stage 6: a brand-new `new C()` of a data-only (method-free) class
// flowing straight into an `any` position is realized as a real D1 dynamic
// object rather than an opaque box — so member access, indexing, Object.keys,
// JSON, and dynamic property add all work on it, exactly as in JS. Because the
// widened object is the single representation of that instance (there was no
// prior binding to alias), reference identity and shared mutation stay
// faithful.

class Point {
    x: number = 1;
    y: number = 2;
}

class Config {
    origin: Point = new Point();
    tags: string[] = ["cli", "fast"];
    port: number = 8080;
    host: string = "localhost";
}

// A fresh instance flows straight into an `any` binding: it becomes dynamic.
const cfg: any = new Config();

// Member access — including into a nested object and an array field.
console.log(cfg.host, cfg.port);          // localhost 8080
console.log(cfg.origin.x, cfg.origin.y);  // 1 2
console.log(cfg.tags[0], cfg.tags.length); // cli 2

// Enumeration and serialization see the real shape.
console.log(Object.keys(cfg).join(","));  // origin,tags,port,host
console.log(JSON.stringify(cfg));

// Dynamic property add works, as on any JS object.
cfg.debug = true;
console.log(cfg.debug);                   // true

// The widened object is a single shared representation: an alias observes
// mutations and `===` holds, matching JS reference semantics.
const alias = cfg;
alias.port = 9090;
console.log(cfg.port, cfg === alias);     // 9090 true

// Widening applies at every boundary a fresh object crosses into `any` —
// here, a function that returns `any` and one that takes an `any` parameter.
function makeConfig(): any {
    return new Config();
}
function report(c: any): void {
    console.log(c.host + ":" + c.port);   // localhost:8080
}
console.log((makeConfig() as any).port);  // 8080
report(new Config());
