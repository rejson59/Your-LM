//go:build !windows

package main

func inspectUSB(string, string) USBState {
	return USBState{Supported: false, Message: "Instalacja USB jest dostępna w aplikacji Windows"}
}

func installUSB(string, string, string) (string, error) {
	return "", nil
}
