// TypeScript overload signatures: body-less declarations that narrow the
// call-site view of a single implementation. Signatures are erased at
// compile time — only the implementation is compiled; calls type-check
// against the implementation's parameter list.

function describe(x: string): string;
function describe(x: number): string;
function describe(x: any): string {
    return "value=" + String(x);
}

class Logger {
    prefix: string;
    constructor(prefix: string);
    constructor(prefix: any) { this.prefix = String(prefix); }

    log(msg: string): string;
    log(msg: number): string;
    log(msg: any): string {
        return this.prefix + ": " + String(msg);
    }
}

console.log(describe("Thessaloniki"));
console.log(describe(42));

const l = new Logger("app");
console.log(l.log("started"));
console.log(l.log(7));
