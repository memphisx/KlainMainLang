// Each call of a function declaring a class makes a class with statics of
// its own: initializers and static members read that call's arguments.

function make(b: number) {
  class D {
    static base = b * 10;
    static count = 0;
    static inc() { D.count++; return D.count + D.base; }
    bump() { D.count += 100; return D.count; }
  }
  class S extends D {
    static extra = b + 0.5;
    static both() { return S.extra + S.base; }
  }
  return [D, S] as const;
}
const [A, AS] = make(1);
const [B, BS] = make(2);
const K: any = A;
console.log(K.base, K.inc(), A.count, B.count);
console.log(new A().bump(), A.count, B.count, new B().bump());
console.log(AS.both(), BS.both(), AS.extra, BS.base);
console.log(Object.keys(A), Object.keys(B));
console.log(A, B.base);
class Top {
  static n = 1;
  static twice() { return this.n * 2; }
  static { this.n += 4; }
}
console.log(Top.twice(), Top.n);
function counter(start: number) {
  return class {
    static v = start;
    static next() { return ++this.v; }
  };
}
const C1 = counter(10), C2 = counter(20);
console.log(C1.next(), C1.next(), C2.next(), C1.v, C2.v);
