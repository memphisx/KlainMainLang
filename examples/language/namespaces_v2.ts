// Namespaces V2: the `module X {}` synonym, non-exported members (usable by
// siblings, hidden outside), class/interface/type/enum members, and bare
// sibling references between members.
module Geometry {
    const SCALE: number = 2;
    function area(w: number, h: number): number {
        return w * h * SCALE;
    }
    export function describe(w: number, h: number): string {
        return "area=" + String(area(w, h));
    }
    export class Point {
        constructor(public x: number, public y: number) {}
        sum(): number { return this.x + this.y; }
    }
    export enum Kind { Flat, Tall }
}

console.log(Geometry.describe(3, 4));
const p = new Geometry.Point(1, 2);
console.log(p.sum());
const k: Kind = Kind.Tall;
console.log(k);

// V3: nested namespaces + dotted declarations + relative references.
module App {
    export module Config {
        export const port: number = 8080;
    }
    export function describePort(): string { return "port " + String(Config.port); }
}
console.log(App.Config.port);
console.log(App.describePort());

// import-equals aliases (`import X = Y.Z`).
import Cfg = App.Config;
console.log(Cfg.port);

// Namespace bodies may hold executable statements (run at initialization).
let bootCount = 0;
module Boot {
    bootCount = 1;
    export function count(): number { return bootCount; }
}
console.log(Boot.count());

// Qualified type references resolve through the namespace.
const origin: Geometry.Point = new Geometry.Point(0, 0);
console.log(origin.sum());

// Namespace type-member chains: enum members resolve through the qualifier.
console.log(Geometry.Kind.Tall);
