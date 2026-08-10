// os module (TDD-00024): operating-system information — platform, user/
// temp directories, hostname, memory, and per-core CPU info. Import-gated
// (TDD-00049) — a virtual built-in module, not a real file. Most values
// here are environment-dependent, so this example checks properties
// (non-empty, positive, consistent) rather than exact values.

import os from 'os'
import path from 'path'

console.log(os.platform().length > 0); // true

const home = os.homedir();
console.log(home.length > 0); // true

const tmp = os.tmpdir();
console.log(tmp.length > 0); // true

const host = os.hostname();
console.log(host.length > 0); // true

const total = os.totalmem();
const free = os.freemem();
console.log(total > 0); // true
console.log(free > 0 && free <= total); // true

const cpus = os.cpus();
console.log(cpus.length > 0); // true
console.log(cpus[0].model.length > 0); // true

let totalIdleMs = 0;
for (let i = 0; i < cpus.length; i = i + 1) {
  totalIdleMs = totalIdleMs + cpus[i].times.idle;
}
console.log(totalIdleMs > 0); // true

// A config-file-location pattern this module is meant to unlock — join
// os.homedir() with path.join for a real, portable app-config path.
const configPath = path.join(home, ".config", "klainmain-example.json");
console.log(configPath.length > home.length); // true
console.log(configPath.indexOf(".config") >= 0); // true
