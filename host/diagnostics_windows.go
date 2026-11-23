//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
)

func windowsHardwareViaCIM() (HardwareDiagnostic, error) {
	const script = `$ErrorActionPreference='Stop'; [Console]::OutputEncoding=[Text.Encoding]::UTF8; $cs=Get-CimInstance Win32_ComputerSystem; $cpus=@(Get-CimInstance Win32_Processor); $gpus=@(Get-CimInstance Win32_VideoController | Where-Object {$_.Name}); $disks=@(Get-CimInstance Win32_DiskDrive | Where-Object {$_.Model}); $os=Get-CimInstance Win32_OperatingSystem; [pscustomobject]@{manufacturer=[string]$cs.Manufacturer;model=[string]$cs.Model;os=[string]$os.Caption;osVersion=([string]$os.Version+' build '+[string]$os.BuildNumber);cpu=(($cpus | ForEach-Object {$_.Name.Trim()}) -join ' + ');physicalCores=[int](($cpus | Measure-Object NumberOfCores -Sum).Sum);logicalCores=[int](($cpus | Measure-Object NumberOfLogicalProcessors -Sum).Sum);maxMHz=[int](($cpus | Measure-Object MaxClockSpeed -Maximum).Maximum);ramTotal=[uint64]$cs.TotalPhysicalMemory;gpus=@($gpus | ForEach-Object {[pscustomobject]@{name=[string]$_.Name;memoryBytes=[uint64]$_.AdapterRAM;driverVersion=[string]$_.DriverVersion}});disks=@($disks | ForEach-Object {[pscustomobject]@{model=[string]$_.Model;mediaType=[string]$_.MediaType;interface=[string]$_.InterfaceType;size=[uint64]$_.Size}})} | ConvertTo-Json -Compress -Depth 5`
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		windowsPowerShell(),
		"-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", script,
	)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	output, err := command.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return HardwareDiagnostic{}, fmt.Errorf("Windows CIM nie odpowiedział w 25 sekund")
	}
	if err != nil {
		return HardwareDiagnostic{}, fmt.Errorf("Windows CIM: %s", strings.TrimSpace(string(output)))
	}
	var result HardwareDiagnostic
	if err := json.Unmarshal(output, &result); err != nil {
		return HardwareDiagnostic{}, fmt.Errorf("odczyt JSON Windows CIM: %w", err)
	}
	return result, nil
}

func platformHardwareSnapshot() HardwareDiagnostic {
	memory := systemMemory()
	result, err := windowsHardwareViaCIM()
	if result.OS == "" {
		result.OS = "Windows"
	}
	if result.CPU == "" {
		result.CPU = strings.TrimSpace(os.Getenv("PROCESSOR_IDENTIFIER"))
	}
	if result.LogicalCores <= 0 {
		result.LogicalCores = runtime.NumCPU()
	}
	result.Architecture = runtime.GOARCH
	result.RAMTotal = max(result.RAMTotal, memory.Total)
	result.RAMAvailable = memory.Available
	if err != nil {
		result.Warning = err.Error()
	}
	return result
}

func adbPhoneInventory(ctx context.Context, adb, serial string) (PhoneDiagnostic, error) {
	const script = `echo manufacturer=$(getprop ro.product.manufacturer); echo model=$(getprop ro.product.model); echo device=$(getprop ro.product.device); echo android=$(getprop ro.build.version.release); echo sdk=$(getprop ro.build.version.sdk); echo soc_manufacturer=$(getprop ro.soc.manufacturer); echo soc_model=$(getprop ro.soc.model); echo board=$(getprop ro.board.platform); echo hardware=$(getprop ro.hardware); echo abis=$(getprop ro.product.cpu.abilist); echo cores=$(getconf _NPROCESSORS_ONLN); awk '/MemTotal:/{print "memory_kib="$2} /MemAvailable:/{print "memory_available_kib="$2}' /proc/meminfo; df -k /data | tail -n 1 | awk '{print "storage_total_kib="$2; print "storage_free_kib="$4}'; echo features=$(grep -m1 -i '^Features' /proc/cpuinfo | cut -d: -f2-); echo frequencies_khz=$(cat /sys/devices/system/cpu/cpu*/cpufreq/cpuinfo_max_freq 2>/dev/null | tr '\n' ','); dumpsys battery | awk -F: '/^[[:space:]]*level:/{gsub(/ /,"",$2);print "battery_level="$2} /^[[:space:]]*temperature:/{gsub(/ /,"",$2);print "battery_temp_tenths_c="$2}'`
	output, err := runADBContext(ctx, 35*time.Second, adb, "-s", serial, "shell", "sh", "-c", script)
	if err != nil {
		return PhoneDiagnostic{}, err
	}
	phone := parseADBPhoneReport(output)
	if phone.Name == "" || phone.RAMTotal == 0 {
		return phone, fmt.Errorf("ADB nie zwrócił pełnej identyfikacji telefonu")
	}
	return phone, nil
}

func adbPhoneStorageBenchmark(ctx context.Context, adb, serial string, phone *PhoneDiagnostic) {
	if phone.StorageFree > 0 && phone.StorageFree < 1*GiB {
		phone.Warning = "Pominięto próbkę pamięci telefonu: mniej niż 1 GiB wolnego miejsca."
		return
	}
	const bytes = uint64(128 << 20)
	const path = "/data/local/tmp/yourlm-storage-benchmark.tmp"
	_, _ = runADB(10*time.Second, adb, "-s", serial, "shell", "rm", "-f", path)
	started := time.Now()
	_, writeErr := runADBContext(ctx, 90*time.Second, adb, "-s", serial, "shell", "dd", "if=/dev/zero", "of="+path, "bs=4194304", "count=32", "conv=fsync")
	writeElapsed := time.Since(started)
	if writeErr == nil && writeElapsed > 0 {
		phone.DiskWriteBps = uint64(float64(bytes) / writeElapsed.Seconds())
	}
	var readErr error
	if ctx.Err() == nil {
		started = time.Now()
		_, readErr = runADBContext(ctx, 90*time.Second, adb, "-s", serial, "shell", "dd", "if="+path, "of=/dev/null", "bs=4194304")
		readElapsed := time.Since(started)
		if readErr == nil && readElapsed > 0 {
			phone.DiskReadBps = uint64(float64(bytes) / readElapsed.Seconds())
		}
	}
	_, _ = runADB(10*time.Second, adb, "-s", serial, "shell", "rm", "-f", path)
	if ctx.Err() == nil && (writeErr != nil || readErr != nil) {
		phone.Warning = "Próbka pamięci UFS nie została ukończona."
	}
}

func collectUSBPhoneDiagnostics(ctx context.Context, adb string, devices []USBDevice) []PhoneDiagnostic {
	var phones []PhoneDiagnostic
	for _, device := range devices {
		if device.State != "device" || ctx.Err() != nil {
			continue
		}
		phone, err := adbPhoneInventory(ctx, adb, device.Serial)
		if err != nil {
			phones = append(phones, PhoneDiagnostic{
				Name:    firstNonEmpty(device.Model, device.Name, "Telefon Android"),
				Model:   device.Model,
				Source:  "USB/ADB",
				Warning: err.Error(),
			})
			continue
		}
		if ctx.Err() == nil {
			adbPhoneStorageBenchmark(ctx, adb, device.Serial, &phone)
		}
		phones = append(phones, phone)
		if ctx.Err() != nil {
			break
		}
	}
	return phones
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
