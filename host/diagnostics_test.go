package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseADBPhoneReport(t *testing.T) {
	report := strings.Join([]string{
		"manufacturer=Xiaomi",
		"model=2312DRA50G",
		"device=garnet",
		"android=15",
		"sdk=35",
		"soc_manufacturer=Qualcomm",
		"soc_model=SM7435-AB",
		"board=crow",
		"abis=arm64-v8a,armeabi-v7a",
		"cores=8",
		"memory_kib=7999488",
		"memory_available_kib=4012345",
		"storage_total_kib=245760000",
		"storage_free_kib=102400000",
		"features=fp asimd aes asimddp",
		"frequencies_khz=1800000,2200000,2918400,",
		"battery_level=73",
		"battery_temp_tenths_c=324",
	}, "\n")
	phone := parseADBPhoneReport(report)
	if phone.Name != "Xiaomi 2312DRA50G" || phone.SoC != "Qualcomm SM7435-AB" {
		t.Fatalf("unexpected phone identity: %+v", phone)
	}
	if phone.SDK != 35 || phone.LogicalCores != 8 || !phone.DotProduct {
		t.Fatalf("unexpected phone CPU data: %+v", phone)
	}
	if phone.RAMTotal != 7999488<<10 || phone.StorageFree != 102400000<<10 {
		t.Fatalf("unexpected phone memory data: %+v", phone)
	}
	if len(phone.MaxFrequenciesKHz) != 3 || phone.MaxFrequenciesKHz[2] != 2918400 {
		t.Fatalf("unexpected frequencies: %v", phone.MaxFrequenciesKHz)
	}
	if phone.BatteryLevel != 73 || phone.BatteryTempC != 32.4 {
		t.Fatalf("unexpected battery data: %+v", phone)
	}
}

func TestSamePhoneMergesUSBAndWorkerInventory(t *testing.T) {
	usb := PhoneDiagnostic{Manufacturer: "Xiaomi", Model: "2312DRA50G", Name: "Xiaomi 2312DRA50G"}
	worker := PhoneDiagnostic{Model: "2312DRA50G", Name: "2312DRA50G"}
	if !samePhone(usb, worker) {
		t.Fatal("the same physical Redmi was duplicated across USB and worker inventory")
	}
	if samePhone(PhoneDiagnostic{}, PhoneDiagnostic{}) {
		t.Fatal("empty phone records must not be merged")
	}
}

func TestSamePhoneMergesVendorPrefixedWorkerSnapshot(t *testing.T) {
	usb := PhoneDiagnostic{
		Name:              "24090RA29G",
		Model:             "24090RA29G",
		SoC:               "Mediatek MT6878",
		LogicalCores:      8,
		MaxFrequenciesKHz: []uint64{2500000},
		DotProduct:        true,
		RAMTotal:          7784628224,
		BatteryLevel:      63,
		Source:            "USB/ADB",
	}
	worker := PhoneDiagnostic{
		Name:         "Xiaomi 24090RA29G",
		Model:        "Xiaomi 24090RA29G",
		Android:      "16",
		SDK:          36,
		RAMTotal:     7784628224,
		StorageTotal: 243739713536,
		Source:       "sparowany worker",
	}
	if !samePhone(usb, worker) {
		t.Fatal("vendor-prefixed worker snapshot duplicated the physical Redmi")
	}
	mergePhone(&usb, worker)
	if usb.Name != "Xiaomi 24090RA29G" || usb.SoC != "Mediatek MT6878" || !usb.DotProduct {
		t.Fatalf("merged phone lost its richest identity or ADB CPU facts: %+v", usb)
	}
	if !strings.Contains(usb.Source, "USB/ADB") || !strings.Contains(usb.Source, "sparowany worker") {
		t.Fatalf("merged phone did not retain both local sources: %+v", usb)
	}
}

func TestAutopilotKeepsOneAuditedPracticalChoice(t *testing.T) {
	if newDiagnosticsState().Recommendation.ReadyToApply {
		t.Fatal("autopilot must collect a hardware run before applying the recipe")
	}
	storage := StorageState{Drives: []StorageDrive{{Selected: true, Free: 120 * GiB}}}
	host := HardwareDiagnostic{RAMTotal: 32 * GiB}
	phone := PhoneDiagnostic{RAMTotal: 8 * GiB, StorageFree: 100 * GiB, DotProduct: true}
	benchmark := HostBenchmark{DiskReadBps: 900 << 20}
	model := DiagnosticModelBenchmark{Complete: true, TokensPerSecond: 5.2}
	recommendation := recommendAutopilot(storage, host, []PhoneDiagnostic{phone}, benchmark, model)
	if recommendation.SelectedID != goldenModelID || !recommendation.ReadyToApply {
		t.Fatalf("autopilot did not preserve the audited practical recipe: %+v", recommendation)
	}
	if !strings.Contains(recommendation.MaximumStatus, "przechodzi") || !strings.Contains(recommendation.MaximumStatus, "osobnej receptury") {
		t.Fatalf("80B qualification lost its recipe gate: %+v", recommendation)
	}

	constrained := recommendAutopilot(storage, host, []PhoneDiagnostic{{RAMTotal: 4 * GiB}}, benchmark, DiagnosticModelBenchmark{})
	if !strings.Contains(constrained.MaximumStatus, "nie jest teraz praktycznym") || !strings.Contains(constrained.MaximumStatus, "RAM") {
		t.Fatalf("constrained phone was not rejected for 80B: %+v", constrained)
	}
}

func TestVerifiedStorageMigration(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "old", "YourLM")
	destination := filepath.Join(root, "new", "Data")
	if err := os.MkdirAll(filepath.Join(source, "models"), 0700); err != nil {
		t.Fatal(err)
	}
	model := []byte("verified model fragment")
	if err := os.WriteFile(filepath.Join(source, "models", "test.gguf.part"), model, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "engine.log"), []byte("log"), 0600); err != nil {
		t.Fatal(err)
	}
	migrated, warning, err := migrateDataDirectory(source, destination)
	if err != nil || !migrated || warning != "" {
		t.Fatalf("migration failed: migrated=%v warning=%q err=%v", migrated, warning, err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("verified source was not retired: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "models", "test.gguf.part"))
	if err != nil || string(got) != string(model) {
		t.Fatalf("migrated model differs: %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(destination, storageMarker)); err != nil {
		t.Fatalf("migration marker missing: %v", err)
	}
}

func TestExistingMigrationMarkerCannotDiscardDifferentSource(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "old", "YourLM")
	destination := filepath.Join(root, "new", "Data")
	for _, directory := range []string{filepath.Join(source, "models"), filepath.Join(destination, "models")} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			t.Fatal(err)
		}
	}
	sourceModel := filepath.Join(source, "models", "model.gguf.part")
	destinationModel := filepath.Join(destination, "models", "model.gguf.part")
	if err := os.WriteFile(sourceModel, []byte("authoritative source"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destinationModel, []byte("different target"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, storageMarker), []byte("YourLM storage v1\n"), 0600); err != nil {
		t.Fatal(err)
	}
	migrated, _, err := migrateDataDirectory(source, destination)
	if err == nil || migrated {
		t.Fatalf("different marked target was accepted: migrated=%v err=%v", migrated, err)
	}
	if got, readErr := os.ReadFile(sourceModel); readErr != nil || string(got) != "authoritative source" {
		t.Fatalf("source changed after rejected migration: %q, %v", got, readErr)
	}
	if err := os.WriteFile(destinationModel, []byte("authoritative source"), 0600); err != nil {
		t.Fatal(err)
	}
	migrated, warning, err := migrateDataDirectory(source, destination)
	if err != nil || !migrated || warning != "" {
		t.Fatalf("verified marked target did not resume: migrated=%v warning=%q err=%v", migrated, warning, err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("verified legacy source was not retired: %v", err)
	}
}

func TestDiskDiagnosticUsesOnlyOwnedTemporaryFile(t *testing.T) {
	directory := t.TempDir()
	writeRate, readRate, used, err := benchmarkDisk(context.Background(), directory, 2<<20)
	if err != nil || writeRate == 0 || readRate == 0 || used != 2<<20 {
		t.Fatalf("disk benchmark failed: write=%d read=%d used=%d err=%v", writeRate, readRate, used, err)
	}
	if _, err := os.Stat(filepath.Join(directory, "diagnostics", ".disk-benchmark.tmp")); !os.IsNotExist(err) {
		t.Fatalf("temporary benchmark file remained: %v", err)
	}
}
