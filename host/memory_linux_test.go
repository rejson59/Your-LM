//go:build linux

package main

import "testing"

func TestParseProcCPUTime(t *testing.T) {
	stat := "123 (worker (rpc) bridge) S 1 2 3 4 5 6 7 8 9 10 20 30 0 0 0"
	if got := parseProcCPUTimeMS(stat); got != 500 {
		t.Fatalf("parseProcCPUTimeMS returned %d ms, want 500 ms", got)
	}
	if got := parseProcCPUTimeMS("truncated"); got != 0 {
		t.Fatalf("truncated stat returned %d ms", got)
	}
}
