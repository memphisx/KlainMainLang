// process.argv, process.env, process.exit — CLI/process interaction.
// Run with extra args to see them printed, e.g.:
//   ./process hello world

// --- process.argv ---
// argv[0] is always the compiled binary's own path (like C's argv, not
// Node's two-prefix convention); anything after that is user-supplied args.
const args: string[] = process.argv
console.log(args.length >= 1)  // true
for (const a of args) {
  console.log(a)
}

// --- process.env ---
const path: string = process.env.PATH ?? "no PATH set"
console.log(path.length > 0)  // true on virtually every system

const key: string = "PATH"
const pathAgain: string = process.env[key] ?? "no PATH set"
console.log(pathAgain === path)  // true

const missing = process.env.KML_EXAMPLE_DOES_NOT_EXIST ?? "default"
console.log(missing)  // default

// --- process.readLineSync ---
// Reads one line from stdin, or null at EOF — try it with:
//   echo "hi there" | ./process
// With no piped input (or under `make examples`, which redirects stdin from
// /dev/null so this never blocks waiting on a real terminal), this hits EOF
// immediately and prints "no input" below.
const line = process.readLineSync()
if (line === null) {
    console.log("no input")
} else {
    console.log("read: " + line)
}

// --- process.cwd / process.chdir ---
const startDir: string = process.cwd()
console.log(startDir.length > 0)   // true

process.chdir("/tmp")
console.log(process.cwd() !== startDir)   // true — actually moved

process.chdir(startDir)
console.log(process.cwd() === startDir)   // true — moved back

try {
    process.chdir("/definitely/does/not/exist/kml-example-dir")
    console.log("never printed")
} catch (e) {
    console.log("caught: cannot chdir")
}

// --- process.pid ---
console.log(process.pid > 0)   // true

// --- process.platform ---
// One of "darwin"/"linux"/"win32"/... — whatever the compiler itself ran on
// (this compiler doesn't cross-compile, so that's always the same platform
// the output binary targets too).
console.log(process.platform.length > 0)   // true

// --- process.kill ---
// Signal 0 is the POSIX "existence check" convention: no signal is actually
// delivered, kill() just reports whether it *could* have been (i.e. whether
// the target process exists and is killable).
process.kill(process.pid, 0)
console.log("process.kill(self, 0) did not throw")

try {
    process.kill(999999999, 0)
    console.log("never printed")
} catch (e) {
    console.log("caught: no such process")
}

// --- process.exit ---
console.log("done")
process.exit(0)
console.log("never printed")
