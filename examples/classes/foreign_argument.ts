// A parameter typed as a class receives whatever object a dynamic call
// passes: an emitter's listener, or a function held as `any`, reads the
// other object's own properties and methods.
import { EventEmitter } from "events";

class Celsius { degrees = 21; unit = "C"; describe() { return "celsius"; } }
class Kelvin { kelvins = 294; unit = "K"; describe() { return "kelvin"; } }

const em = new EventEmitter();
em.on("reading", (r: Celsius) => console.log("arrow", r.unit, r.describe(), r.degrees));
em.on("reading", function (r: Celsius) { console.log("function", r.unit, r.describe()); });
function show(r: Celsius) { console.log("named", r.unit, r.describe(), r.degrees); }
em.on("reading", show);

em.emit("reading", new Celsius());
em.emit("reading", new Kelvin() as any);

const anyShow: any = show;
anyShow(new Kelvin());
const rest: (...args: any[]) => void = (r: Celsius) => console.log("rest", r.unit, r.describe());
rest(new Kelvin());
show(new Celsius());
