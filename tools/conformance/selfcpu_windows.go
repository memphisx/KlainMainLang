package main

import "time"

// selfCPU is not measured on Windows.
func selfCPU() (time.Duration, bool) { return 0, false }
