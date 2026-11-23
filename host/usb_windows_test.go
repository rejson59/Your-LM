//go:build windows

package main

import "testing"

func TestParseADBForwardPort(t *testing.T) {
	tests := []struct {
		output string
		want   int
	}{
		{"43123\n", 43123},
		{"* daemon started successfully *\r\n51234\r\n", 51234},
	}
	for _, test := range tests {
		port, err := parseADBForwardPort(test.output)
		if err != nil || port != test.want {
			t.Fatalf("parseADBForwardPort(%q)=%d, %v; want %d", test.output, port, err, test.want)
		}
	}
	if _, err := parseADBForwardPort("no port"); err == nil {
		t.Fatal("accepted ADB output without a port")
	}
}

func TestADBForwardListedRequiresExactOwnedMapping(t *testing.T) {
	list := "OTHER tcp:43123 tcp:45100\r\nR58M123ABC tcp:43123 tcp:45100\r\n"
	if !adbForwardListed(list, "R58M123ABC", 43123, 45100) {
		t.Fatal("owned forward was not found")
	}
	if adbForwardListed(list, "R58M123ABC", 43123, 45101) || adbForwardListed(list, "OTHER", 43123, 45101) {
		t.Fatal("different ADB forward was treated as owned")
	}
}
