package main

import "testing"

// TestWhatIfRequiresChange is a test func whose name shares the filler
// words ("what if ... change") of the I2 repro query; it must never win
// resolution over the production dispatch symbols.
func TestWhatIfRequiresChange(t *testing.T) {}
