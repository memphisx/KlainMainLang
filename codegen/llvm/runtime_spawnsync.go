package llvm

import _ "embed"

//go:embed childprocsrc/spawnsync.c
var spawnSyncSource string

// UsesSpawnSync reports whether the program used a blocking child_process
// *Sync form, so the driver links the embedded C implementation.
func (e *Emitter) UsesSpawnSync() bool { return e.usedSpawnSync }

// SpawnSyncSource is the embedded C implementation behind
// child_process.spawnSync/execSync/execFileSync: fork + execvp with the
// piped stdio captured to completion (poll-multiplexed, so a child filling
// stderr while stdout is being drained can't deadlock), an optional `input`
// written to its stdin, a `timeout`/`maxBuffer` kill, then a blocking
// waitpid. Result strings use the length-prefixed layout of
// runtime_strheader.go ([i64 len][bytes][NUL], value ptr = base+8).
//
// lib/node/child_process.ts reaches it through the native spawnSync
// declarations (lib/native.d.ts).
func SpawnSyncSource() string { return spawnSyncSource }
