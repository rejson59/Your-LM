package main

import (
	"strings"
	"testing"
)

func TestCleanupProtection(t *testing.T) {
	tests := []struct {
		name, title string
		pid, self   int
		elevated    bool
		protected   bool
	}{
		{name: "YourLM.exe", title: "YourLM", pid: 10, self: 10, protected: true},
		{name: "explorer.exe", title: "Dokumenty", pid: 20, self: 10, protected: true},
		{name: "powershell.exe", title: "ważne zadanie", pid: 21, self: 10, protected: true},
		{name: "setup.exe", title: "Instalacja", pid: 22, self: 10, protected: true},
		{name: "editor.exe", title: "YourLM · Model i API", pid: 23, self: 10, protected: true},
		{name: "admin-tool.exe", title: "Narzędzie", pid: 24, self: 10, elevated: true, protected: true},
		{name: "AvastUI.exe", title: "Avast", pid: 25, self: 10, protected: true},
		{name: "vendor.exe", title: "Endpoint Security", pid: 26, self: 10, protected: true},
		{name: "editor.exe", title: "Notatki", pid: 27, self: 10, protected: false},
	}
	for _, test := range tests {
		reason := cleanupProtection(test.name, test.title, test.pid, test.self, test.elevated)
		if (reason != "") != test.protected {
			t.Fatalf("protection for %s/%s = %q", test.name, test.title, reason)
		}
	}
}

func TestMatchCleanupSelectionRejectsStaleAndProtectedProcesses(t *testing.T) {
	cached := []CleanupApp{
		{PID: 101, Name: "editor.exe", Closable: true, Resident: 500},
		{PID: 102, Name: "explorer.exe", Closable: false},
	}
	current := []CleanupApp{
		{PID: 101, Name: "editor.exe", Closable: true, Resident: 600},
		{PID: 102, Name: "explorer.exe", Closable: false},
	}
	selected, err := matchCleanupSelection(cached, current, []int{101})
	if err != nil || len(selected) != 1 || selected[0].Resident != 600 {
		t.Fatalf("valid selection=%+v, %v", selected, err)
	}
	if _, err := matchCleanupSelection(cached, current, []int{102}); err == nil {
		t.Fatal("protected process was accepted")
	}
	current[0].Name = "different.exe"
	if _, err := matchCleanupSelection(cached, current, []int{101}); err == nil || !strings.Contains(err.Error(), "odśwież") {
		t.Fatalf("PID reuse was not rejected: %v", err)
	}
	if _, err := matchCleanupSelection(cached, current, []int{101, 101}); err == nil {
		t.Fatal("duplicate PID was accepted")
	}
}

func TestCleanupOutcomesMatchPIDIdentityAndRequest(t *testing.T) {
	selected := []CleanupApp{
		{PID: 7, Name: "one.exe", Title: "One"},
		{PID: 8, Name: "two.exe", Title: "Two"},
		{PID: 9, Name: "three.exe", Title: "Three"},
	}
	after := []CleanupApp{
		{PID: 7, Name: "replacement.exe"},
		{PID: 8, Name: "TWO.EXE"},
		{PID: 9, Name: "three.exe"},
	}
	requests := map[int]cleanupCloseRequest{
		7: {Posted: 1},
		8: {Posted: 2},
	}
	outcomes, requested, closed, remaining := cleanupOutcomes(selected, after, requests)
	if requested != 2 || closed != 1 || remaining != 2 {
		t.Fatalf("requested=%d closed=%d remaining=%d", requested, closed, remaining)
	}
	if len(outcomes) != 3 || outcomes[0].Status != "closed" || outcomes[1].Status != "remaining" || outcomes[2].Status != "not-requested" {
		t.Fatalf("unexpected outcomes: %+v", outcomes)
	}
}
