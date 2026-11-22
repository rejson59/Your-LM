//go:build !windows

package main

import "errors"

func inspectUSB(string, string) USBState {
	return USBState{Supported: false, Message: "Instalacja USB jest dostępna w aplikacji Windows"}
}

func installUSB(string, string, string) (string, error) {
	return "", nil
}

func createADBForward(string, string, int) (string, int, error) {
	return "", 0, errors.New("tunel ADB jest dostępny tylko na Windows")
}

func removeADBForward(string, string, int, int) {}
