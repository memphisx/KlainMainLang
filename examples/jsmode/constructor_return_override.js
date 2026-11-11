// A base constructor that returns another object makes it the derived
// class's `this`: a Proxy wrapper, a registry entry, a plain object. The
// derived class's private fields, methods and getters live on that object.
// Run with:  klainmain -compat=js constructor_return_override.js
const seen = [];
class Logged {
  constructor() {
    return new Proxy(this, {
      get(target, key) { seen.push(String(key)); return target[key]; },
    });
  }
}
class Account extends Logged {
  #balance = 0;
  deposit(n) { this.#balance += n; return this.#balance; }
}
const acct = new Account();
console.log(acct.deposit(5), acct.deposit(7), seen.join(","), acct instanceof Account);

class Adopt { constructor(o) { return o; } }
class Tagged extends Adopt {
  #tag;
  constructor(o, tag) { super(o); this.#tag = tag; }
  static tagOf(o) { return #tag in o ? o.#tag : "untagged"; }
}
const city = { name: "Thessaloniki" };
new Tagged(city, "home");
console.log(Tagged.tagOf(city), Tagged.tagOf({}));
try { new Tagged(city, "again"); } catch (e) { console.log(e.message); }

function wrap(Base) { class Wrapped extends Base {} return Wrapped; }
const Bytes = wrap(Uint8Array);
console.log(new Bytes(3).length);
