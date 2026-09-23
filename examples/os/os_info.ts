// os module, host-information half (ADR-01077): identity strings, uptime /
// load / parallelism, the current user, the network interfaces — and
// process.env as an enumerable object. Every value is host state, so this
// example checks shapes and relations rather than exact values (the E2E
// tests compare each answer against Node on the same machine).

import os from 'os'

// uname-shaped identity: "Linux" / "Darwin" / "Windows_NT" and friends.
console.log(os.type().length > 0, os.release().length > 0, os.machine().length > 0); // true true true
console.log(os.arch() === process.arch, os.endianness()); // true LE
console.log(os.devNull); // /dev/null (POSIX) or \\.\nul (Windows)

// Seconds since boot, the three load averages, the CPUs we may run on.
console.log(os.uptime() > 0, os.loadavg().length, os.availableParallelism() >= 1); // true 3 true

// The current user; uid/gid are -1 and shell is null on Windows.
const me = os.userInfo();
console.log(me.username.length > 0, me.homedir === os.homedir()); // true true

// Interfaces keyed by name; IPv6 entries carry a scopeid, IPv4 ones do not.
const nis = os.networkInterfaces();
let loopbacks = 0;
for (const name of Object.keys(nis)) {
  for (const ni of nis[name]) {
    if (ni.internal) loopbacks++;
  }
}
console.log(loopbacks > 0); // true

// process.env as a whole object — enumeration, spread, entries.
process.env.OS_INFO_EXAMPLE = "on";
console.log(Object.keys(process.env).includes("OS_INFO_EXAMPLE")); // true
const childEnv = { ...process.env, NODE_ENV: "production" };
console.log(childEnv.OS_INFO_EXAMPLE, childEnv.NODE_ENV); // on production
for (const [k, v] of Object.entries(process.env)) {
  if (k === "OS_INFO_EXAMPLE") console.log(k, v); // OS_INFO_EXAMPLE on
}
