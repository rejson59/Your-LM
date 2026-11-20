package main

import (
	"fmt"
	"os"
	"strings"
)

func memory() (total, free uint64) {
	b, _ := os.ReadFile("/proc/meminfo")
	for _, l := range strings.Split(string(b), "\n") {
		var v uint64
		var k string
		fmt.Sscan(l, &k, &v)
		if k == "MemTotal:" {
			total = v * 1024
		}
		if k == "MemAvailable:" {
			free = v * 1024
		}
	}
	return
}
