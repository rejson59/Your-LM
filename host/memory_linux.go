package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func procValues(path string) map[string]uint64 {
	values := make(map[string]uint64)
	data, _ := os.ReadFile(path)
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err == nil {
			// Linux memory status values used here are all expressed in KiB.
			values[strings.TrimSuffix(fields[0], ":")] = value * 1024
		}
	}
	return values
}

func systemMemory() MemorySnapshot {
	values := procValues("/proc/meminfo")
	total := values["MemTotal"]
	available := values["MemAvailable"]
	limit := values["CommitLimit"]
	committed := values["Committed_AS"]
	commitAvailable := uint64(0)
	if limit > committed {
		commitAvailable = limit - committed
	}
	used := uint64(0)
	if total > available {
		used = total - available
	}
	return MemorySnapshot{
		Total:           total,
		Available:       available,
		Used:            used,
		CommitLimit:     limit,
		CommitAvailable: commitAvailable,
		PageFile:        values["SwapTotal"],
	}
}

func memoryForProcess(pid int) ProcessMemory {
	if pid <= 0 {
		return ProcessMemory{}
	}
	values := procValues(fmt.Sprintf("/proc/%d/status", pid))
	return ProcessMemory{
		Resident:  values["VmRSS"],
		Committed: values["VmRSS"] + values["VmSwap"],
	}
}
