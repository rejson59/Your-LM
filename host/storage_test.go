package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func storageMigrationFixture(t *testing.T) (source, destination, model string, payload []byte) {
	t.Helper()
	root := t.TempDir()
	source = filepath.Join(root, "C", "YourLM")
	destination = filepath.Join(root, "D", "YourLM", "Data")
	model = filepath.Join("models", "llmfan46--gemma-4-26B-A4B-it-ultra-uncensored-heretic-GGUF--gemma-4-26B-A4B-it-ultra-uncensored-heretic-Q4_K_M.gguf")
	payload = bytes.Repeat([]byte("YourLM verified restartable migration\n"), 32*1024)
	if err := os.MkdirAll(filepath.Join(source, "models"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, model), payload, 0600); err != nil {
		t.Fatal(err)
	}
	return source, destination, model, payload
}

func TestMigrationResumesPreExistingMigratingPart(t *testing.T) {
	source, destination, model, payload := storageMigrationFixture(t)
	part := filepath.Join(destination+".migrating", model) + storageMigrationPartSuffix
	if err := os.MkdirAll(filepath.Dir(part), 0700); err != nil {
		t.Fatal(err)
	}
	prefix := payload[:len(payload)*2/5]
	if err := os.WriteFile(part, prefix, 0600); err != nil {
		t.Fatal(err)
	}

	result, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil {
		t.Fatalf("pre-existing partial copy was not resumed: %v", err)
	}
	if !result.migrated || result.state.Status != "recovered" {
		t.Fatalf("unexpected recovery result: %+v", result)
	}
	if result.state.ResumedFiles != 1 || result.state.ResumedBytes != uint64(len(prefix)) {
		t.Fatalf("partial recovery was not reported: %+v", result.state)
	}
	got, err := os.ReadFile(filepath.Join(destination, model))
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("published file differs after resume: bytes=%d err=%v", len(got), err)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("verified source was not retired: %v", err)
	}
	if _, err := os.Stat(part); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging part was published or left behind: %v", err)
	}
}

func TestMigrationReusesCompleteStagingFile(t *testing.T) {
	source, destination, model, payload := storageMigrationFixture(t)
	staged := filepath.Join(destination+".migrating", model)
	if err := os.MkdirAll(filepath.Dir(staged), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, payload, 0600); err != nil {
		t.Fatal(err)
	}

	result, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !result.migrated {
		t.Fatalf("complete staged file was not reused: result=%+v err=%v", result, err)
	}
	if result.state.ReusedFiles != 1 || result.state.ReusedBytes != uint64(len(payload)) {
		t.Fatalf("staged-file reuse was not reported: %+v", result.state)
	}
}

func TestMigrationReplacesStaleCompletedAndOrphanedStagingEntries(t *testing.T) {
	source, destination, model, payload := storageMigrationFixture(t)
	staging := destination + ".migrating"
	staged := filepath.Join(staging, model)
	if err := os.MkdirAll(filepath.Dir(staged), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(staged, []byte("an older completed copy"), 0600); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(staging, "obsolete", "removed-model.gguf")
	if err := os.MkdirAll(filepath.Dir(orphan), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, []byte("no longer present in source"), 0600); err != nil {
		t.Fatal(err)
	}

	result, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !result.migrated {
		t.Fatalf("stale completed staging data was not recovered: result=%+v err=%v", result, err)
	}
	if result.state.ReplacedFiles != 1 || result.state.RemovedEntries < 1 || result.state.Status != "recovered" {
		t.Fatalf("stale staging cleanup was not explicit: %+v", result.state)
	}
	got, readErr := os.ReadFile(filepath.Join(destination, model))
	if readErr != nil || !bytes.Equal(got, payload) {
		t.Fatalf("refreshed staged file differs: bytes=%d err=%v", len(got), readErr)
	}
	if _, orphanErr := os.Stat(filepath.Join(destination, "obsolete")); !errors.Is(orphanErr, os.ErrNotExist) {
		t.Fatalf("orphaned staging entry was published: %v", orphanErr)
	}
}

func TestMigrationRejectsCorruptPartThenRestartsIt(t *testing.T) {
	source, destination, model, payload := storageMigrationFixture(t)
	part := filepath.Join(destination+".migrating", model) + storageMigrationPartSuffix
	if err := os.MkdirAll(filepath.Dir(part), 0700); err != nil {
		t.Fatal(err)
	}
	corrupt := bytes.Repeat([]byte{0xa5}, len(payload)/3)
	if err := os.WriteFile(part, corrupt, 0600); err != nil {
		t.Fatal(err)
	}

	result, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !result.migrated {
		t.Fatalf("corrupt staging part was not recovered safely: result=%+v err=%v", result, err)
	}
	if result.state.RecoveredParts != 1 || result.state.ResumedFiles != 0 {
		t.Fatalf("corrupt part recovery was not explicit: %+v", result.state)
	}
	got, readErr := os.ReadFile(filepath.Join(destination, model))
	if readErr != nil || !bytes.Equal(got, payload) {
		t.Fatalf("replacement copy differs: bytes=%d err=%v", len(got), readErr)
	}
}

func TestUnsafeStagingMarkerNeverBecomesPublishedData(t *testing.T) {
	source, destination, model, _ := storageMigrationFixture(t)
	staging := destination + ".migrating"
	// A marker is trusted only after final SHA-256 verification and must be a
	// regular synced file. A directory at that control path is never removed or
	// interpreted as evidence that staging is ready.
	if err := os.MkdirAll(filepath.Join(staging, storageMarker), 0700); err != nil {
		t.Fatal(err)
	}

	result, err := migrateDataDirectoryDetailed(source, destination)
	if err == nil || result.migrated {
		t.Fatalf("unsafe staging tree was published: result=%+v err=%v", result, err)
	}
	if result.state.Status != "fallback" || !result.state.SourceRetained {
		t.Fatalf("safe fallback was not reported: %+v", result.state)
	}
	if _, sourceErr := os.Stat(filepath.Join(source, model)); sourceErr != nil {
		t.Fatalf("source changed after rejected staging tree: %v", sourceErr)
	}
	if _, destinationErr := os.Stat(destination); !errors.Is(destinationErr, os.ErrNotExist) {
		t.Fatalf("unverified destination became visible: %v", destinationErr)
	}
	if markerInfo, markerErr := os.Stat(filepath.Join(staging, storageMarker)); markerErr != nil || !markerInfo.IsDir() {
		t.Fatalf("unsafe control entry was unexpectedly trusted or changed: info=%v err=%v", markerInfo, markerErr)
	}
}

func TestConcurrentMigrationIsBlockedWithoutUsingStaging(t *testing.T) {
	source, destination, model, _ := storageMigrationFixture(t)
	lock, _, err := acquireStorageMigrationLock(source, destination)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.release()

	result, err := migrateDataDirectoryDetailed(source, destination)
	if !errors.Is(err, errStorageMigrationBusy) {
		t.Fatalf("second migration did not receive the busy guard: result=%+v err=%v", result, err)
	}
	if result.state.Status != "busy" || !result.state.SourceRetained {
		t.Fatalf("second instance status is not actionable: %+v", result.state)
	}
	if _, sourceErr := os.Stat(filepath.Join(source, model)); sourceErr != nil {
		t.Fatalf("second migration touched the source: %v", sourceErr)
	}
	if _, stageErr := os.Stat(destination + ".migrating"); !errors.Is(stageErr, os.ErrNotExist) {
		t.Fatalf("second migration touched staging: %v", stageErr)
	}
}

func TestUnlockedCrashMetadataIsRecovered(t *testing.T) {
	source, destination, _, _ := storageMigrationFixture(t)
	lockPath := filepath.Join(filepath.Dir(destination), storageMigrationLockName)
	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lockPath, []byte(`{"pid":4242,"startedAt":"crashed"}`), 0600); err != nil {
		t.Fatal(err)
	}

	result, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !result.migrated {
		t.Fatalf("stale unlocked migration metadata blocked recovery: result=%+v err=%v", result, err)
	}
	if !result.state.RecoveredLock || result.state.Status != "recovered" {
		t.Fatalf("stale lock recovery was not surfaced: %+v", result.state)
	}
	data, readErr := os.ReadFile(lockPath)
	if readErr != nil || strings.TrimSpace(string(data)) != "" {
		t.Fatalf("released lock retained active metadata: %q err=%v", data, readErr)
	}
}
