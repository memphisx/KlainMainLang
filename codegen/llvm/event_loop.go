package llvm

// loopUser is the line a unit that waits on the event loop carries: its
// reference to loop.c, so the link takes the loop with it (and main() then
// runs it, hooks.c).
const loopUser = "extern char __kml_loop_linked;\n__attribute__((used)) static char *const kml_loop_user = &__kml_loop_linked;\n"
