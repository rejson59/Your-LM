//go:build !windows

package main

import (
	"bufio"
	"context"
	"os"
	"runtime"
	"strings"
)

func procCPUValue(keys ...string) string {
	file, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer file.Close()
	wanted := make(map[string]bool, len(keys))
	for _, key := range keys {
		wanted[strings.ToLower(key)] = true
	}
	found := make(map[string]string, len(keys))
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), ":")
		key = strings.ToLower(strings.TrimSpace(key))
		if ok && wanted[key] && found[key] == "" {
			found[key] = strings.TrimSpace(value)
		}
	}
	for _, key := range keys {
		if value := found[strings.ToLower(key)]; value != "" {
			return value
		}
	}
	return ""
}

func platformHardwareSnapshot() HardwareDiagnostic {
	memory := systemMemory()
	cpu := procCPUValue("model name", "hardware", "processor")
	if cpu == "" {
		cpu = runtime.GOARCH
	}
	osName := runtime.GOOS
	if runtime.GOOS == "android" {
		osName = "Android"
	}
	return HardwareDiagnostic{
		OS:           osName,
		CPU:          cpu,
		LogicalCores: runtime.NumCPU(),
		Architecture: runtime.GOARCH,
		RAMTotal:     memory.Total,
		RAMAvailable: memory.Available,
	}
}

func collectUSBPhoneDiagnostics(context.Context, string, []USBDevice) []PhoneDiagnostic {
	return nil
}
