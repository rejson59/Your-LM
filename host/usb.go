package main

import (
	"errors"
	"net/http"
	"runtime"
)

type USBDevice struct {
	Serial string `json:"serial"`
	State  string `json:"state"`
	Model  string `json:"model,omitempty"`
	Name   string `json:"name,omitempty"`
}

type USBState struct {
	Supported  bool        `json:"supported"`
	Ready      bool        `json:"ready"`
	Installing bool        `json:"installing"`
	APKReady   bool        `json:"apkReady"`
	Devices    []USBDevice `json:"devices"`
	Message    string      `json:"message,omitempty"`
}

func (a *App) refreshUSB() USBState {
	a.Lock()
	adb := a.adbPath
	apk := a.apkPath
	installing := a.usb.Installing
	a.Unlock()
	state := inspectUSB(adb, apk)
	state.Installing = installing
	a.Lock()
	a.usb = state
	a.Unlock()
	return state
}

func (a *App) installPhone(serial string) error {
	if runtime.GOOS != "windows" {
		return errors.New("Instalacja USB jest dostępna w aplikacji Windows")
	}
	a.Lock()
	if a.usb.Installing {
		a.Unlock()
		return errors.New("Instalacja już trwa")
	}
	if a.s != nil || a.off != nil {
		a.Unlock()
		return errors.New("Najpierw zatrzymaj model lub tryb workera")
	}
	a.usb.Installing = true
	adb, apk := a.adbPath, a.apkPath
	a.Unlock()
	message, err := installUSB(adb, apk, serial)
	state := inspectUSB(adb, apk)
	state.Message = message
	state.Installing = false
	a.Lock()
	a.usb = state
	a.Unlock()
	return err
}

func (a *App) usbAPI(w http.ResponseWriter, op string, in input) {
	switch op {
	case "usb-refresh":
		state := a.refreshUSB()
		a.Lock()
		if a.s == nil {
			for i := range a.nodes {
				if a.nodes[i].Platform == "android" {
					optimizeNodeTransport(a.adbPath, state.Devices, &a.nodes[i])
				}
			}
		}
		defer a.Unlock()
		respond(w, a.state())
	case "usb-install":
		if err := a.installPhone(in.Serial); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		a.Lock()
		defer a.Unlock()
		respond(w, a.state())
	default:
		http.Error(w, "Nieznana operacja USB", http.StatusNotFound)
	}
}
