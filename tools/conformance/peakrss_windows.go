package main

import "os"

// peakRSS is unknown here: the run's job object caps the clangs instead.
func peakRSS(ps *os.ProcessState) (int64, bool) { return 0, false }
