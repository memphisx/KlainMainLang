// A tiny smoke-test program for docker/Dockerfile's --static build: proves
// the compiled binary really is statically linked by running successfully
// inside a `scratch` container (no libc, no dynamic linker, nothing else at
// all) — if it were dynamically linked, the container couldn't even start
// the process.
console.log("Hello from a statically-linked KlainMainLang binary!")
console.log("PID: " + process.pid)
console.log("Platform: " + process.platform)
console.log("HOSTNAME env: " + (process.env.HOSTNAME ?? "not set"))
