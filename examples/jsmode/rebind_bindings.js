// Rebinding a builtin, a function, or a class from outside its body.
parseInt = 5;
console.log(parseInt);
class C { m() { C = 42; } }
try { new C().m(); } catch (e) { console.log(e.message); }
C = null;
console.log(C);
