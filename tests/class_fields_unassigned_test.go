package tests

import "testing"

// A declared field nothing assigned (`o!: T`, or one a method assigns) is
// an own property holding undefined until assigned, as in Node.
func TestE2EClassFieldUnassignedReadsUndefined(t *testing.T) {
	assertOutput(t, `
interface P { x: number }
class C {
  o!: P; n!: number; list!: number[]; flag!: boolean; s!: string;
  setup() { this.o = { x: 5 }; this.n = 3; this.list = [1, 2]; this.flag = true; this.s = "k"; }
}
const c = new C();
console.log(c.o === undefined, typeof c.o, typeof c.list, typeof c.flag, c.n + 1, c.s === undefined);
console.log(JSON.stringify(c));
console.log(c);
c.setup();
console.log(c.o.x + c.n + c.list.length, c.flag, c.s);
`, "true undefined undefined undefined NaN true\n{}\nC {\n  o: undefined,\n  n: undefined,\n  list: undefined,\n  flag: undefined,\n  s: undefined\n}\n10 true k\n")
}

// A destructured parameter's default fills an absent or undefined field,
// so the binding (and the arrow returning it) is the field's present type.
func TestE2EDestructuredParamDefaultIsPresent(t *testing.T) {
	assertOutput(t, `
var af = ({x = 1}) => x;
const bf = ({x = 1}: {x?: number}) => { const y = x; return y; };
console.log(typeof af, af({}), af({x: 2}), bf({}), bf({x: undefined}), bf({x: 7}));
`, "function 1 2 1 1 7\n")
}
