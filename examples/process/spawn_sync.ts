// The blocking child_process family: spawnSync returns the full result
// record (its stdout a string with an encoding, else a Buffer); execSync (via
// /bin/sh -c) and execFileSync (no shell) return the captured stdout directly.
import { spawnSync, execSync, execFileSync } from 'child_process';

const r = spawnSync("echo", ["kalimera", "kosme"], { encoding: "utf8" });
console.log("status:", r.status);
console.log("stdout:", r.stdout.trim());

console.log("shell math:", execSync("echo $((6 * 7))", { encoding: "utf8" }).trim());
console.log("no shell:", execFileSync("printf", ["%s!", "Thessaloniki"], { encoding: "utf8" }));

const failing = spawnSync("false");
console.log("failing status:", failing.status);
