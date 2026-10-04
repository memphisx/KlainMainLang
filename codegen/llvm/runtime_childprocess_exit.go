// runtime_childprocess_exit.go — child exit as an event (TDD-00223 §5).
//
// The event loop reaps children with waitpid(WNOHANG) every iteration
// (__kml_cp_reap); what this file supplies is the *wake*: the loop must leave
// select() the moment a child ends, even when the child's stdio is silent,
// closed, or held open by a grandchild. libuv's mechanism on each platform:
//
//   - POSIX: a SIGCHLD handler writes one byte to a non-blocking self-pipe
//     whose read end sits in the loop's read set. A self-pipe rather than the
//     EINTR that select() returns on a signal: a signal landing between the
//     loop's last check and its select() call interrupts nothing, and the
//     exit would go unnoticed until some unrelated wake.
//   - Windows: a registered wait on the process handle posts a packet to the
//     reactor's completion port (win32proc.c __kml_win_child_watch).
//
// The hooks live in childprocsrc/childproc.c (TDD-00240); the child_process
// runtime calls:
//
//	__kml_cp_watch_init()        before a spawn — POSIX installs pipe+handler
//	__kml_cp_watch_pid(i32 pid)  after a spawn — Windows registers the wait
//	__kml_cp_wake_fdset_add(fdset, maxfd) -> i1
//	                             adds the self-pipe; true once after a new
//	                             registration, forcing one non-blocking pass so
//	                             a child that died before the watch existed is
//	                             still reaped at once
//	__kml_cp_wake_drain()        empties the self-pipe
package llvm
