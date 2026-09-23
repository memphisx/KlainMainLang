//go:build !windows && !linux

package main

// availableCommitBytes is unknown here (macOS compresses and swaps freely, so
// a commit figure would not predict a failure); the CPU-derived default stands.
func availableCommitBytes() uint64 { return 0 }
