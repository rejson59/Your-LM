//go:build windows

package main

import "testing"

func TestCleanupRootForProcess(t *testing.T) {
	processes := map[int]cleanupProcess{
		100: {PID: 100, Parent: 1, Name: "browser.exe"},
		101: {PID: 101, Parent: 100, Name: "browser.exe"},
		102: {PID: 102, Parent: 101, Name: "browser.exe"},
		200: {PID: 200, Parent: 201, Name: "cycle.exe"},
		201: {PID: 201, Parent: 200, Name: "cycle.exe"},
	}
	roots := map[int][]cleanupWindow{100: {{PID: 100, Title: "Browser"}}}
	if got := cleanupRootForProcess(102, processes, roots); got != 100 {
		t.Fatalf("root=%d, want 100", got)
	}
	if got := cleanupRootForProcess(200, processes, roots); got != 0 {
		t.Fatalf("cyclic process tree root=%d, want 0", got)
	}
}
