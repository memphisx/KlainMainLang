// Cross-compilation to another platform (a Sailfish OS phone here).
//
// This is an ordinary CLI tool — it runs on the host with a plain
// `klainmain examples/cross_compile/sailfish_cli.ts`. The point of the example
// is the build recipe: the same source cross-compiles to a 64-bit-ARM Sailfish
// binary without changing a line, using the --target preset + a --sysroot that
// points at the target device's root filesystem:
//
//   klainmain --target sfos-aarch64 \
//             --sysroot /path/to/sailfish-aarch64-rootfs \
//             -o greet examples/cross_compile/sailfish_cli.ts
//
//   # then copy `greet` to the phone and run it there:
//   scp greet defaultuser@phone:/home/defaultuser/ && ssh defaultuser@phone ./greet Kyriakos
//
// Or package it as an installable Sailfish RPM (Harbour naming) — the .spec +
// build tree are always written, and rpmbuild runs when present (e.g. on a
// Linux/SDK build host), producing harbour-<name>-<ver>.aarch64.rpm:
//
//   klainmain --target sfos-aarch64 --sysroot /path/to/rootfs \
//             -package=rpm:harbour -app-name greet -app-license BSD-3-Clause \
//             -o greet examples/cross_compile/sailfish_cli.ts
//
// --target also takes a full clang triple directly; the sfos-aarch64 preset
// expands to Sailfish's native aarch64-meego-linux-gnu (that exact vendor field
// is what lets clang find the target's gcc/crt and /usr/lib64 in the sysroot).
// --sysroot is always required:
// it is where the target's headers and shared libraries are found, so a
// genuinely different device links against its own libc, not the host's. A
// cross-arch link needs `lld` on PATH (used automatically when present).

// In a compiled binary argv[0] is the program itself and the first user
// argument is argv[1] (there is no Node "script path" slot in between).
const who = process.argv[1] ?? "world";
console.log(`Hello, ${who}!`);
console.log(`running on ${process.platform}/${process.arch}`);
