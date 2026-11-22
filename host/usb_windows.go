//go:build windows

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func runADB(timeout time.Duration, adb string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, adb, args...)
	cmd.Dir = filepath.Dir(adb)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if ctx.Err() == context.DeadlineExceeded {
		return text, errors.New("ADB nie odpowiedział w wyznaczonym czasie")
	}
	if err != nil {
		if text == "" {
			text = err.Error()
		}
		return text, fmt.Errorf("ADB: %s", text)
	}
	return text, nil
}

func parseADBDevices(output string) []USBDevice {
	var devices []USBDevice
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List of devices") || strings.HasPrefix(line, "*") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		device := USBDevice{Serial: fields[0], State: fields[1]}
		for _, field := range fields[2:] {
			key, value, ok := strings.Cut(field, ":")
			if !ok {
				continue
			}
			switch key {
			case "model":
				device.Model = strings.ReplaceAll(value, "_", " ")
			case "product", "device":
				if device.Name == "" {
					device.Name = strings.ReplaceAll(value, "_", " ")
				}
			}
		}
		devices = append(devices, device)
	}
	return devices
}

func inspectUSB(adb, apk string) USBState {
	state := USBState{Supported: true}
	if info, err := os.Stat(apk); err == nil && info.Mode().IsRegular() {
		state.APKReady = true
	}
	if info, err := os.Stat(adb); err != nil || !info.Mode().IsRegular() {
		state.Message = "Wbudowane ADB nie jest jeszcze gotowe"
		return state
	}
	output, err := runADB(15*time.Second, adb, "devices", "-l")
	if err != nil {
		state.Message = err.Error()
		return state
	}
	state.Devices = parseADBDevices(output)
	for _, device := range state.Devices {
		if device.State == "device" {
			state.Ready = true
		}
	}
	if len(state.Devices) == 0 {
		state.Message = "Podłącz telefon kablem USB i włącz debugowanie USB"
	} else if !state.Ready {
		state.Message = "Odblokuj telefon i zaakceptuj pytanie o debugowanie USB"
	} else {
		state.Message = "Telefon jest gotowy do instalacji"
	}
	return state
}

func installUSB(adb, apk, serial string) (string, error) {
	state := inspectUSB(adb, apk)
	if !state.APKReady {
		return "", errors.New("EXE nie zawiera gotowego APK")
	}
	var ready []USBDevice
	for _, device := range state.Devices {
		if device.State == "device" && (serial == "" || device.Serial == serial) {
			ready = append(ready, device)
		}
	}
	if len(ready) == 0 {
		return state.Message, errors.New(state.Message)
	}
	if serial == "" && len(ready) > 1 {
		return "", errors.New("Wybierz telefon z listy")
	}
	serial = ready[0].Serial
	output, err := runADB(5*time.Minute, adb, "-s", serial, "install", "-r", apk)
	if err != nil {
		return output, err
	}
	if !strings.Contains(strings.ToLower(output), "success") {
		return output, fmt.Errorf("Android nie potwierdził instalacji: %s", output)
	}
	_, _ = runADB(20*time.Second, adb, "-s", serial, "shell", "am", "start", "-n", "pl.yourlm.node/.NodeActivity")
	return "YourLM został zainstalowany lub zaktualizowany i uruchomiony na telefonie.", nil
}

func parseADBForwardPort(output string) (int, error) {
	lines := strings.Fields(strings.ReplaceAll(output, "\r", ""))
	for i := len(lines) - 1; i >= 0; i-- {
		port, err := strconv.Atoi(lines[i])
		if err == nil && port >= 1024 && port <= 65535 {
			return port, nil
		}
	}
	return 0, fmt.Errorf("ADB nie zwrócił lokalnego portu tunelu: %q", strings.TrimSpace(output))
}

func createADBForward(adb, serial string, remotePort int) (string, int, error) {
	if serial == "" || remotePort < 1 || remotePort > 65535 {
		return "", 0, errors.New("niepoprawne parametry tunelu ADB")
	}
	output, err := runADB(
		10*time.Second,
		adb,
		"-s", serial,
		"forward", "tcp:0", fmt.Sprintf("tcp:%d", remotePort),
	)
	if err != nil {
		return "", 0, err
	}
	port, err := parseADBForwardPort(output)
	if err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("127.0.0.1:%d", port), port, nil
}

func adbForwardListed(output, serial string, localPort, remotePort int) bool {
	local := fmt.Sprintf("tcp:%d", localPort)
	remote := fmt.Sprintf("tcp:%d", remotePort)
	for _, line := range strings.Split(strings.ReplaceAll(output, "\r", ""), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == serial && fields[1] == local && fields[2] == remote {
			return true
		}
	}
	return false
}

func removeADBForward(adb, serial string, localPort, remotePort int) {
	if adb == "" || serial == "" || localPort < 1 || localPort > 65535 || remotePort < 1 || remotePort > 65535 {
		return
	}
	listed, err := runADB(2*time.Second, adb, "forward", "--list")
	if err != nil || !adbForwardListed(listed, serial, localPort, remotePort) {
		return
	}
	_, _ = runADB(
		2*time.Second,
		adb,
		"-s", serial,
		"forward", "--remove", fmt.Sprintf("tcp:%d", localPort),
	)
}
