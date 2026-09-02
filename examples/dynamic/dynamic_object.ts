// D1 dynamic object model, Stage 1: an object literal bound to `any` becomes
// a real runtime property bag — properties can be added, updated, deleted,
// enumerated, and read through runtime-computed keys.

let config: any = { host: "thessaloniki.example", port: 8080, secure: true };

// Member and bracket reads, including a missing key.
console.log(config.host);          // thessaloniki.example
console.log(config["port"]);       // 8080
console.log(config.timeout);       // undefined

// Dynamic add + update through dot and runtime-computed bracket keys.
config.timeout = 30;
config["re" + "tries"] = 3;
config.port = 9090;
console.log(config.timeout, config.retries, config.port); // 30 3 9090

// Membership, deletion, enumeration.
console.log("secure" in config);   // true
delete config.secure;
console.log("secure" in config);   // false
console.log(Object.keys(config));  // [ 'host', 'port', 'timeout', 'retries' ]
for (const key in config) {
  console.log(key, "=", config[key]);
}

// Nesting and spread (shallow, like JS).
let base: any = { level: "info", sink: { path: "/var/log/app.log" } };
let merged: any = { ...base, level: "debug" };
console.log(merged.level, merged.sink.path); // debug /var/log/app.log

// Reference semantics + identity.
let alias: any = config;
alias.port = 1234;
console.log(config.port, config === alias); // 1234 true

// Stage 2: untyped JSON.parse — the whole tree is dynamic, arrays included.
const pkg = JSON.parse('{"name":"klainmain","deps":["curl","pcre2"],"opts":{"gc":false}}');
console.log(pkg.name, pkg.deps.length, pkg.deps[1], pkg.opts.gc);

// Dynamic arrays mutate with JS extension semantics (holes read undefined).
pkg.deps[3] = "yoga";
console.log(pkg.deps.length, pkg.deps[2], pkg.deps[3]); // 4 undefined yoga
console.log(`deps: ${pkg.deps}`);                       // deps: curl,pcre2,,yoga

// JSON.stringify round-trips the dynamic tree; undefined values are skipped.
pkg.opts.debug = undefined;
console.log(JSON.stringify(pkg));
const round: any = JSON.parse(JSON.stringify(pkg));
console.log(round.opts.gc); // false

// Heterogeneous literals are representable in an any context.
let mixed: any = [1, "two", { three: 3 }, [4]];
console.log(JSON.stringify(mixed)); // [1,"two",{"three":3},[4]]

// Stage 3: prototype chains. Reads and `in` walk the chain; writes, keys,
// and stringify stay own — like JS.
const defaults: any = { level: "info", color: true };
const options: any = Object.create(defaults);
options.level = "debug";                       // own shadow
console.log(options.level, options.color);     // debug true
console.log(Object.keys(options));             // [ 'level' ] — own only
console.log("color" in options, Object.hasOwn(options, "color")); // true false
delete options.level;
console.log(options.level);                    // info — proto shines through
console.log(options.__proto__ === defaults);   // true

// The literal form and re-linking.
const themed: any = { __proto__: defaults, theme: "dark" };
console.log(themed.color, themed.theme);       // true dark
Object.setPrototypeOf(themed, null);
console.log(themed.color);                     // undefined
