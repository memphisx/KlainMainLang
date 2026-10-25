package tests

import "os"

// maxRSS reports no peak resident set size: Windows has no getrusage.
func maxRSS(ps *os.ProcessState) (int64, bool) { return 0, false }
