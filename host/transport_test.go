package main

import (
	"strings"
	"testing"
)

func TestPhysicalADBDevicesExcludesWirelessAndEmulators(t *testing.T) {
	devices := []USBDevice{
		{Serial: "R58M123ABC", State: "device"},
		{Serial: "192.168.1.8:5555", State: "device"},
		{Serial: "adb-R58M123ABC-xYz_adb-tls-connect._tcp", State: "device"},
		{Serial: "emulator-5554", State: "device"},
		{Serial: "unauthorized-phone", State: "unauthorized"},
	}
	got := physicalADBDevices(devices)
	if len(got) != 1 || got[0].Serial != "R58M123ABC" {
		t.Fatalf("physical devices=%+v", got)
	}
}

func TestPreferUSBTransportUsesMeasuredWeightedScore(t *testing.T) {
	wifi := &LinkBenchmark{UploadBps: 30, DownloadBps: 10}
	wifi.ScoreBps = benchmarkScore(wifi.UploadBps, wifi.DownloadBps)
	usb := &LinkBenchmark{UploadBps: 20, DownloadBps: 20}
	usb.ScoreBps = benchmarkScore(usb.UploadBps, usb.DownloadBps)
	if preferUSBTransport(wifi, usb) {
		t.Fatalf("USB was selected despite lower weighted score: wifi=%+v usb=%+v", wifi, usb)
	}
	usb.UploadBps = 40
	usb.ScoreBps = benchmarkScore(usb.UploadBps, usb.DownloadBps)
	if !preferUSBTransport(wifi, usb) {
		t.Fatalf("faster USB was not selected: wifi=%+v usb=%+v", wifi, usb)
	}
	usb.ScoreBps = wifi.ScoreBps
	wifi.LatencyMicros = 5000
	usb.LatencyMicros = 2000
	if !preferUSBTransport(wifi, usb) {
		t.Fatalf("lower-latency USB did not win a close throughput result: wifi=%+v usb=%+v", wifi, usb)
	}
	if !preferUSBTransport(nil, usb) || preferUSBTransport(wifi, nil) {
		t.Fatal("single available transport was not selected")
	}
}

func TestPreferUSBTransportUsesLatencyOnlyInsideFivePercentWindow(t *testing.T) {
	wifi := &LinkBenchmark{ScoreBps: 100, LatencyMicros: 1000}
	usb := &LinkBenchmark{ScoreBps: 105, LatencyMicros: 5000}
	if preferUSBTransport(wifi, usb) {
		t.Fatal("exactly five percent faster USB ignored the lower Wi-Fi latency")
	}
	usb.ScoreBps = 106
	if !preferUSBTransport(wifi, usb) {
		t.Fatal("USB throughput advantage above five percent did not win")
	}

	wifi.ScoreBps = 105
	wifi.LatencyMicros = 5000
	usb.ScoreBps = 100
	usb.LatencyMicros = 1000
	if !preferUSBTransport(wifi, usb) {
		t.Fatal("exactly five percent slower USB ignored its lower latency")
	}
	wifi.ScoreBps = 106
	if preferUSBTransport(wifi, usb) {
		t.Fatal("Wi-Fi throughput advantage above five percent did not win")
	}
	if reason := measuredTransportReason(false, wifi, usb); !strings.Contains(reason, "ponad 5%") {
		t.Fatalf("throughput decision was not explained: %q", reason)
	}
	wifi.ScoreBps = 104
	if reason := measuredTransportReason(true, wifi, usb); !strings.Contains(reason, "medianowy ping") {
		t.Fatalf("latency decision was not explained: %q", reason)
	}
}
