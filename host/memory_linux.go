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

func processReadBytes(pid int) uint64 {
	data, _ := os.ReadFile(fmt.Sprintf("/proc/%d/io", pid))
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "read_bytes" {
			continue
		}
		result, _ := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
		return result
	}
	return 0
}

func parseProcCPUTimeMS(data string) uint64 {
	// comm (field 2) may contain spaces and parentheses, so fields before its
	// final ')' cannot be split safely. Linux exports utime/stime in USER_HZ;
	// USER_HZ is fixed at 100 ticks per second for this procfs interface.
	end := strings.LastIndexByte(data, ')')
	if end < 0 || end+1 >= len(data) {
		return 0
	}
	fields := strings.Fields(data[end+1:])
	if len(fields) <= 12 {
		return 0
	}
	user, _ := strconv.ParseUint(fields[11], 10, 64)
	kernel, _ := strconv.ParseUint(fields[12], 10, 64)
	return (user + kernel) * 10
}

func processCPUTimeMS(pid int) uint64 {
	data, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	return parseProcCPUTimeMS(string(data))
}

func memoryForProcess(pid int) ProcessMemory {
	if pid <= 0 {
		return ProcessMemory{}
	}
	values := procValues(fmt.Sprintf("/proc/%d/status", pid))
	return ProcessMemory{
		Resident:  values["VmRSS"],
		Committed: values["VmRSS"] + values["VmSwap"],
		ReadBytes: processReadBytes(pid),
		CPUTimeMS: processCPUTimeMS(pid),
	}
}
