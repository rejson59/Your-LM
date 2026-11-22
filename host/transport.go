package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"
)

func physicalADBDevices(devices []USBDevice) []USBDevice {
	ready := make([]USBDevice, 0, len(devices))
	for _, device := range devices {
		// A wireless-ADB serial is host:port. It must not be mistaken for the
		// cable whose speed we are trying to compare with Wi-Fi.
		if device.State != "device" || strings.Contains(device.Serial, ":") || strings.Contains(device.Serial, "_adb-tls-") || strings.HasPrefix(device.Serial, "emulator-") {
			continue
		}
		ready = append(ready, device)
	}
	return ready
}

func scoreAdvantageOverFivePercent(candidate, baseline uint64) bool {
	return candidate > baseline && candidate-baseline > baseline/20
}

func preferUSBTransport(wifi, usb *LinkBenchmark) bool {
	if usb == nil {
		return false
	}
	if wifi == nil {
		return true
	}
	if scoreAdvantageOverFivePercent(usb.ScoreBps, wifi.ScoreBps) {
		return true
	}
	if scoreAdvantageOverFivePercent(wifi.ScoreBps, usb.ScoreBps) {
		return false
	}
	if usb.LatencyMicros > 0 && wifi.LatencyMicros > 0 && usb.LatencyMicros != wifi.LatencyMicros {
		return usb.LatencyMicros < wifi.LatencyMicros
	}
	return usb.ScoreBps > wifi.ScoreBps
}

func measuredTransportReason(usbSelected bool, wifi, usb *LinkBenchmark) string {
	selected, fallback := "szyfrowane Wi-Fi", "tunel USB/ADB"
	winner, other := wifi, usb
	if usbSelected {
		selected, fallback = "szyfrowany tunel USB/ADB", "Wi-Fi"
		winner, other = usb, wifi
	}
	if scoreAdvantageOverFivePercent(winner.ScoreBps, other.ScoreBps) {
		return fmt.Sprintf("Automatyczny pomiar wybrał %s: ważona przepustowość była o ponad 5%% lepsza; %s pozostaje awaryjne.", selected, fallback)
	}
	if winner.LatencyMicros > 0 && other.LatencyMicros > 0 && winner.LatencyMicros != other.LatencyMicros {
		return fmt.Sprintf("Automatyczny pomiar wybrał %s: ważone przepustowości różniły się najwyżej o 5%%, więc rozstrzygnął niższy medianowy ping; %s pozostaje awaryjne.", selected, fallback)
	}
	return fmt.Sprintf("Automatyczny pomiar wybrał %s po porównaniu zbliżonej przepustowości i opóźnienia; %s pozostaje awaryjne.", selected, fallback)
}

func shortTransportError(err error) string {
	if err == nil {
		return ""
	}
	value := strings.TrimSpace(err.Error())
	runes := []rune(value)
	if len(runes) > 160 {
		value = string(runes[:160]) + "…"
	}
	return value
}

func releaseNodeADB(adb string, node *Node) {
	if node == nil {
		return
	}
	removeADBForward(adb, node.USBSerial, node.usbForwardPort, node.usbRemotePort)
	node.usbAddress = ""
	node.usbForwardPort = 0
	node.usbRemotePort = 0
	node.USBSerial = ""
	node.USBBenchmark = nil
}

func optimizeNodeTransport(adb string, devices []USBDevice, node *Node) {
	if node == nil {
		return
	}
	releaseNodeADB(adb, node)
	node.Transport = "wifi"
	node.TransportCheckedAt = time.Now().UnixMilli()
	node.TransportReason = "Połączenie bezpośrednie przez szyfrowany tunel Wi-Fi/LAN."
	node.WiFiBenchmark = nil
	if node.Platform != "android" {
		return
	}

	candidates := physicalADBDevices(devices)
	if len(candidates) == 0 {
		node.TransportReason = "Wi-Fi — nie wykryto autoryzowanego telefonu na fizycznym USB."
		return
	}
	_, remotePortText, err := net.SplitHostPort(node.Address)
	if err != nil {
		node.TransportReason = "Wi-Fi — adres workera nie pozwala utworzyć tunelu ADB."
		return
	}
	remotePort, err := strconv.Atoi(remotePortText)
	if err != nil || remotePort < 1 || remotePort > 65535 {
		node.TransportReason = "Wi-Fi — worker zwrócił niepoprawny port tunelu."
		return
	}

	wifiResult, wifiErr := node.benchmarkAt(node.Address)
	if wifiErr == nil {
		node.WiFiBenchmark = &wifiResult
	}

	var usbErr error
	for _, device := range candidates {
		endpoint, localPort, forwardErr := createADBForward(adb, device.Serial, remotePort)
		if forwardErr != nil {
			usbErr = forwardErr
			continue
		}
		info, probeErr := node.infoAt(endpoint)
		if probeErr != nil || info.Platform != "android" {
			removeADBForward(adb, device.Serial, localPort, remotePort)
			if probeErr != nil {
				usbErr = probeErr
			} else {
				usbErr = fmt.Errorf("tunel wskazuje worker %q zamiast Androida", info.Platform)
			}
			continue
		}
		node.usbAddress = endpoint
		node.usbForwardPort = localPort
		node.usbRemotePort = remotePort
		node.USBSerial = device.Serial
		usbResult, benchmarkErr := node.benchmarkAt(endpoint)
		if benchmarkErr == nil {
			node.USBBenchmark = &usbResult
		} else {
			usbErr = benchmarkErr
		}
		break
	}

	switch {
	case node.WiFiBenchmark != nil && node.USBBenchmark != nil:
		if preferUSBTransport(node.WiFiBenchmark, node.USBBenchmark) {
			node.Transport = "usb"
			node.TransportReason = measuredTransportReason(true, node.WiFiBenchmark, node.USBBenchmark)
		} else {
			node.Transport = "wifi"
			node.TransportReason = measuredTransportReason(false, node.WiFiBenchmark, node.USBBenchmark)
		}
	case node.WiFiBenchmark != nil:
		node.Transport = "wifi"
		node.TransportReason = "Pomiar USB/ADB nie powiódł się, dlatego wybrano działające Wi-Fi."
	case node.USBBenchmark != nil:
		node.Transport = "usb"
		node.TransportReason = "Wi-Fi nie przeszło pomiaru, dlatego wybrano działający tunel USB/ADB."
	case node.usbAddress != "":
		node.Transport = "usb"
		node.TransportReason = "Tunel USB/ADB potwierdził tożsamość workera, ale pełny pomiar nie został ukończony."
	default:
		node.Transport = "wifi"
		node.TransportReason = "Nie udało się dopasować fizycznego urządzenia ADB do tego workera; pozostaje Wi-Fi."
	}
	if wifiErr != nil && node.WiFiBenchmark == nil {
		node.TransportReason += " Wi-Fi: " + shortTransportError(wifiErr) + "."
	}
	if usbErr != nil && node.USBBenchmark == nil {
		node.TransportReason += " USB: " + shortTransportError(usbErr) + "."
	}
}
