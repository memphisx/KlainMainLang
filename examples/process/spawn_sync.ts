// The blocking child_process family: spawnSync returns the full result
// record; execSync (via /bin/sh -c) and execFileSync (no shell) return the
// captured stdout string directly.
import { spawnSync, execSync, execFileSync } from 'child_process';

const r = spawnSync("echo", ["kalimera", "kosme"]);
console.log("status:", r.status);
console.log("stdout:", r.stdout.trim());

console.log("shell math:", execSync("echo $((6 * 7))").trim());
console.log("no shell:", execFileSync("printf", ["%s!", "Thessaloniki"]));

const failing = spawnSync("false");
console.log("failing status:", failing.status);
