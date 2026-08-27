import { Point } from "./shapes"
/** @param {import("./shapes").Point} p */
function mag(p) { return p.x + p.y }
console.log(mag({ x: 8, y: 9 }))
