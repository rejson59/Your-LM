package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"runtime"
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

func TestPublishedDestinationReconcilesRetainedSourceAndPreservesDOnlyFiles(t *testing.T) {
	source, destination, model, payload := storageMigrationFixture(t)
	settings := filepath.Join("config", "settings.json")
	if err := os.MkdirAll(filepath.Join(source, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, settings), []byte("old D settings"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !first.migrated {
		t.Fatalf("fixture publication failed: result=%+v err=%v", first, err)
	}

	dOnly := filepath.Join("models", "downloaded-only-on-D.gguf")
	if err := os.WriteFile(filepath.Join(destination, dOnly), []byte("keep this D-only model"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "components", "0.2.0-rc13-e9711eb1537f4f64"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "models"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, model), payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, settings), []byte("new active C settings"), 0600); err != nil {
		t.Fatal(err)
	}
	component := filepath.Join("components", "0.2.0-rc13-e9711eb1537f4f64", "runtime.dll")
	if err := os.WriteFile(filepath.Join(source, component), []byte("retained component"), 0600); err != nil {
		t.Fatal(err)
	}
	part := filepath.Join(destination+".migrating", component) + storageMigrationPartSuffix
	if err := os.MkdirAll(filepath.Dir(part), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}

	result, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !result.migrated {
		t.Fatalf("published D: and retained C: were not reconciled: result=%+v err=%v", result, err)
	}
	if result.state.Status != "reconciled" || !result.state.Reconciled {
		t.Fatalf("reconciliation was not surfaced: %+v", result.state)
	}
	if result.state.ResumedFiles != 1 || result.state.ResumedBytes != uint64(len("retained")) {
		t.Fatalf("retained reconciliation prefix was not resumed: %+v", result.state)
	}
	checks := map[string]string{
		model:     string(payload),
		settings:  "new active C settings",
		component: "retained component",
		dOnly:     "keep this D-only model",
	}
	for relative, want := range checks {
		got, readErr := os.ReadFile(filepath.Join(destination, relative))
		if readErr != nil || string(got) != want {
			t.Fatalf("reconciled %s = %q, err=%v; want %q", relative, got, readErr, want)
		}
	}
	if reconciled, markerErr := reconciledStorageMarker(destination); markerErr != nil || !reconciled {
		t.Fatalf("published union lacks a reconciled marker: reconciled=%v err=%v", reconciled, markerErr)
	}
	unexpectedDirectory := filepath.Join(destination, "unexpected-empty-directory")
	if err := os.Mkdir(unexpectedDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyReconciledStorageMarker(destination); err == nil {
		t.Fatal("reconciled marker did not cover the complete directory union")
	}
	if err := os.Remove(unexpectedDirectory); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyReconciledStorageMarker(destination); err != nil {
		t.Fatalf("reconciled marker did not verify after restoring the exact tree: %v", err)
	}
	if _, sourceErr := os.Stat(source); !errors.Is(sourceErr, os.ErrNotExist) {
		t.Fatalf("verified retained source was not retired: %v", sourceErr)
	}
	if _, stagingErr := os.Stat(destination + ".migrating"); !errors.Is(stagingErr, os.ErrNotExist) {
		t.Fatalf("reconciliation staging remained: %v", stagingErr)
	}
	if _, backupErr := os.Stat(destination + storageReconcileBackupSuffix); !errors.Is(backupErr, os.ErrNotExist) {
		t.Fatalf("transactional D: backup remained: %v", backupErr)
	}
}

func TestBlockedSourceRetirementIsRecordedBeforeDBecomesActive(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACL behavior is covered by the runtime gate; chmod does not block rename")
	}
	source, destination, model, _ := storageMigrationFixture(t)
	sourceParent := filepath.Dir(source)
	if err := os.Chmod(sourceParent, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sourceParent, 0700) })

	result, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !result.migrated {
		t.Fatalf("verified publication did not survive blocked source retirement: result=%+v err=%v", result, err)
	}
	if result.state.Status != "retained" || !sameCleanPath(result.state.RetainedPath, source) {
		t.Fatalf("blocked inactive source was not reported: %+v", result.state)
	}
	marker, err := readStructuredStorageMarker(destination)
	if err != nil || !marker.SourceRetirementPending {
		t.Fatalf("inactive-source authority was not persisted: marker=%+v err=%v", marker, err)
	}
	if err := os.Chmod(sourceParent, 0700); err != nil {
		t.Fatal(err)
	}
	evolved := []byte("D evolved while the inactive C copy was retained")
	if err := os.WriteFile(filepath.Join(destination, model), evolved, 0600); err != nil {
		t.Fatal(err)
	}
	retried, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !retried.migrated {
		t.Fatalf("recorded inactive source was not retired on retry: result=%+v err=%v", retried, err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, model)); err != nil || !bytes.Equal(got, evolved) {
		t.Fatalf("retry replayed stale C: over active D:: %q err=%v", got, err)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inactive source remained after its lock was released: %v", err)
	}
	marker, err = readStructuredStorageMarker(destination)
	if err != nil || marker.SourceRetirementPending {
		t.Fatalf("completed source retirement remained armed: marker=%+v err=%v", marker, err)
	}
}

func TestPendingSourceRetirementNeverReplaysInactiveCOverEvolvedD(t *testing.T) {
	source, destination, model, payload := storageMigrationFixture(t)
	first, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !first.migrated {
		t.Fatalf("fixture publication failed: result=%+v err=%v", first, err)
	}
	if err := os.MkdirAll(filepath.Join(source, filepath.Dir(model)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, model), payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "inactive-C-only.txt"), []byte("must not be replayed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := markStorageSourceRetirementPending(destination); err != nil {
		t.Fatal(err)
	}
	evolved := []byte("active D changed after its verified publication")
	if err := os.WriteFile(filepath.Join(destination, model), evolved, 0600); err != nil {
		t.Fatal(err)
	}

	result, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !result.migrated {
		t.Fatalf("pending inactive-source cleanup failed: result=%+v err=%v", result, err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, model)); err != nil || !bytes.Equal(got, evolved) {
		t.Fatalf("inactive C: overwrote the evolved D: library: %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(destination, "inactive-C-only.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("inactive C-only file was replayed into D:: %v", err)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending inactive C: source was not retired: %v", err)
	}
}

func TestReconciliationBuildDoesNotOverwritePublishedDOrActiveC(t *testing.T) {
	source, destination, _, _ := storageMigrationFixture(t)
	collision := filepath.Join("config", "settings.json")
	if err := os.MkdirAll(filepath.Join(source, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, collision), []byte("published D value"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !first.migrated {
		t.Fatalf("fixture publication failed: result=%+v err=%v", first, err)
	}
	if err := os.MkdirAll(filepath.Join(source, "config"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, collision), []byte("authoritative active C value"), 0600); err != nil {
		t.Fatal(err)
	}

	staging := destination + ".migrating"
	state := StorageMigrationState{}
	if err := prepareReconciliationStaging(source, destination, staging, &state); err != nil {
		t.Fatal(err)
	}
	if err := overlayStorageTree(destination, staging, source, true, &state); err != nil {
		t.Fatal(err)
	}
	if err := overlayStorageTree(source, staging, "", false, &state); err != nil {
		t.Fatal(err)
	}
	checks := map[string]string{
		filepath.Join(destination, collision): "published D value",
		filepath.Join(source, collision):      "authoritative active C value",
		filepath.Join(staging, collision):     "authoritative active C value",
	}
	for path, want := range checks {
		got, err := os.ReadFile(path)
		if err != nil || string(got) != want {
			t.Fatalf("reconciliation build changed %s: %q err=%v, want %q", path, got, err, want)
		}
	}
	if _, err := verifyReconciledStorageDetailed(source, destination, staging); err != nil {
		t.Fatalf("independently built staging union did not verify: %v", err)
	}
}

func TestReconciliationUsesActiveCForFileDirectoryTypeCollisions(t *testing.T) {
	source, destination, model, payload := storageMigrationFixture(t)
	fileOnC := filepath.Join("shape-c-file", "old-d-child.txt")
	directoryOnC := "shape-c-directory"
	if err := os.MkdirAll(filepath.Join(source, filepath.Dir(fileOnC)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, fileOnC), []byte("old D subtree"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, directoryOnC), []byte("old D file"), 0600); err != nil {
		t.Fatal(err)
	}
	first, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !first.migrated {
		t.Fatalf("fixture publication failed: result=%+v err=%v", first, err)
	}

	if err := os.MkdirAll(filepath.Join(source, directoryOnC), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "shape-c-file"), []byte("authoritative C file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, directoryOnC, "new-c-child.txt"), []byte("authoritative C subtree"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !result.migrated || !result.state.Reconciled {
		t.Fatalf("type collisions were not reconciled: result=%+v err=%v", result, err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, "shape-c-file")); err != nil || string(got) != "authoritative C file" {
		t.Fatalf("C: file did not replace the D: directory: %q err=%v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, directoryOnC, "new-c-child.txt")); err != nil || string(got) != "authoritative C subtree" {
		t.Fatalf("C: directory did not replace the D: file: %q err=%v", got, err)
	}
	if _, err := os.Stat(filepath.Join(destination, fileOnC)); err == nil {
		t.Fatal("shadowed D: subtree survived an authoritative C: file")
	}
	if got, err := os.ReadFile(filepath.Join(destination, model)); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("unrelated D-only model was not preserved: bytes=%d err=%v", len(got), err)
	}
}

func TestVerifiedReconciledDestinationStaysActiveWhenOldBackupCannotBeCleaned(t *testing.T) {
	source, destination, model, payload := storageMigrationFixture(t)
	first, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !first.migrated {
		t.Fatalf("fixture publication failed: result=%+v err=%v", first, err)
	}
	component := filepath.Join("components", "0.2.0-rc13-field", "runtime.dll")
	if err := os.MkdirAll(filepath.Join(source, filepath.Dir(component)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, filepath.Dir(model)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, model), payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, component), []byte("retained component"), 0600); err != nil {
		t.Fatal(err)
	}
	merged, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !merged.migrated || !merged.state.Reconciled {
		t.Fatalf("fixture reconciliation failed: result=%+v err=%v", merged, err)
	}

	backup := destination + storageReconcileBackupSuffix
	if err := os.MkdirAll(backup, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(backup, storageMarker), []byte("unrecognized rollback marker"), 0600); err != nil {
		t.Fatal(err)
	}
	result, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil {
		t.Fatalf("cleanup-only backup problem forced a fallback: result=%+v err=%v", result, err)
	}
	if result.state.Status != "reconciled" || !result.state.Reconciled || result.warning == "" {
		t.Fatalf("verified D: was not retained with an intelligible cleanup warning: %+v", result)
	}
	if got, err := os.ReadFile(filepath.Join(destination, component)); err != nil || string(got) != "retained component" {
		t.Fatalf("verified reconciled destination stopped being active: %q err=%v", got, err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("unrecognized backup was deleted despite cleanup warning: %v", err)
	}
	marker, err := readReconciledStorageMarker(destination)
	if err != nil || !marker.RollbackRetained {
		t.Fatalf("retained rollback was not durably recorded: marker=%+v err=%v", marker, err)
	}
	if err := os.WriteFile(filepath.Join(destination, component), []byte("active D evolved after publication"), 0600); err != nil {
		t.Fatal(err)
	}
	retried, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || retried.state.Status != "reconciled" || retried.warning == "" {
		t.Fatalf("known retained rollback forced an empty C: fallback after active D: changed: result=%+v err=%v", retried, err)
	}
	if got, err := os.ReadFile(filepath.Join(destination, component)); err != nil || string(got) != "active D evolved after publication" {
		t.Fatalf("retry changed the active evolved D: library: %q err=%v", got, err)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("retained rollback disappeared after the active tree evolved: %v", err)
	}
	if err := removeValidatedStorageTree(backup, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := migrateDataDirectoryDetailed(source, destination); err != nil {
		t.Fatalf("clearing a resolved rollback marker failed: %v", err)
	}
	marker, err = readReconciledStorageMarker(destination)
	if err != nil || marker.RollbackRetained {
		t.Fatalf("resolved rollback remained armed: marker=%+v err=%v", marker, err)
	}
}

func TestInterruptedReconciliationSwapRestoresPublishedDestination(t *testing.T) {
	source, destination, model, _ := storageMigrationFixture(t)
	first, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !first.migrated {
		t.Fatalf("fixture publication failed: result=%+v err=%v", first, err)
	}
	backup := destination + storageReconcileBackupSuffix
	if err := os.Rename(destination, backup); err != nil {
		t.Fatal(err)
	}

	recovery, warning, err := recoverStorageReconcileSwap(destination)
	if err != nil || recovery != storageReconcileRecoveryRestored || warning != "" {
		t.Fatalf("interrupted directory swap was not restored: recovery=%s warning=%q err=%v", recovery, warning, err)
	}
	if _, err := os.Stat(filepath.Join(destination, model)); err != nil {
		t.Fatalf("published destination was not restored: %v", err)
	}
	if _, err := os.Stat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("backup path remained after restoration: %v", err)
	}
}

func TestInterruptedReconciliationSwapVerifiesNewTreeBeforeDiscardingBackup(t *testing.T) {
	source, destination, model, payload := storageMigrationFixture(t)
	first, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !first.migrated {
		t.Fatalf("fixture publication failed: result=%+v err=%v", first, err)
	}
	component := filepath.Join("components", "0.2.0-rc13-field", "runtime.dll")
	componentPayload := []byte("component retained on active C")
	if err := os.MkdirAll(filepath.Join(source, filepath.Dir(component)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, filepath.Dir(model)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, model), payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, component), componentPayload, 0600); err != nil {
		t.Fatal(err)
	}

	staging := destination + ".migrating"
	state := StorageMigrationState{}
	if err := prepareReconciliationStaging(source, destination, staging, &state); err != nil {
		t.Fatal(err)
	}
	if err := overlayStorageTree(destination, staging, source, true, &state); err != nil {
		t.Fatal(err)
	}
	if err := overlayStorageTree(source, staging, "", false, &state); err != nil {
		t.Fatal(err)
	}
	verified, err := verifyReconciledStorageDetailed(source, destination, staging)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeStorageMarkerState(staging, source, verified, true); err != nil {
		t.Fatal(err)
	}
	backup := destination + storageReconcileBackupSuffix
	if err := os.Rename(destination, backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staging, destination); err != nil {
		t.Fatal(err)
	}

	corrupt := bytes.Repeat([]byte{0x7f}, len(componentPayload))
	if err := os.WriteFile(filepath.Join(destination, component), corrupt, 0600); err != nil {
		t.Fatal(err)
	}
	if recovery, warning, err := recoverStorageReconcileSwap(destination); err == nil || recovery != storageReconcileRecoveryNone || warning != "" {
		t.Fatalf("corrupt new publication discarded its rollback copy: recovery=%s warning=%q err=%v", recovery, warning, err)
	}
	if _, err := os.Stat(filepath.Join(backup, model)); err != nil {
		t.Fatalf("rollback copy was not retained after failed verification: %v", err)
	}
	if _, err := os.Stat(filepath.Join(source, component)); err != nil {
		t.Fatalf("active C: source changed during failed recovery: %v", err)
	}

	if err := os.WriteFile(filepath.Join(destination, component), componentPayload, 0600); err != nil {
		t.Fatal(err)
	}
	if recovery, warning, err := recoverStorageReconcileSwap(destination); err != nil || recovery != storageReconcileRecoveryFinalized || warning != "" {
		t.Fatalf("valid published tree did not finish interrupted swap: recovery=%s warning=%q err=%v", recovery, warning, err)
	}
	if _, err := os.Stat(backup); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("verified rollback copy remained: %v", err)
	}
}

func TestReconciliationRejectsUnsafeOrphanedStagingSubtreeWithoutDeletingIt(t *testing.T) {
	source, destination, model, payload := storageMigrationFixture(t)
	first, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !first.migrated {
		t.Fatalf("fixture publication failed: result=%+v err=%v", first, err)
	}
	component := filepath.Join("components", "0.2.0-rc13-field", "runtime.dll")
	if err := os.MkdirAll(filepath.Join(source, filepath.Dir(component)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, filepath.Dir(model)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, model), payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, component), []byte("C-only component"), 0600); err != nil {
		t.Fatal(err)
	}
	guard := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(guard, []byte("must remain"), 0600); err != nil {
		t.Fatal(err)
	}
	orphan := filepath.Join(destination+".migrating", "orphan")
	if err := os.MkdirAll(orphan, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(orphan, "unsafe-link")
	if err := os.Symlink(guard, link); err != nil {
		t.Skipf("symlinks unavailable for unsafe-staging regression: %v", err)
	}

	result, err := migrateDataDirectoryDetailed(source, destination)
	if err == nil || result.migrated {
		t.Fatalf("unsafe staging subtree was accepted: result=%+v err=%v", result, err)
	}
	if result.state.Status != "fallback" || !result.state.SourceRetained {
		t.Fatalf("unsafe staging did not preserve active C: fallback: %+v", result.state)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("unvisited unsafe entry was deleted or changed: info=%v err=%v", info, err)
	}
	if got, err := os.ReadFile(guard); err != nil || string(got) != "must remain" {
		t.Fatalf("unsafe staging cleanup touched external target: %q err=%v", got, err)
	}
}

func TestStructuredMarkerDoesNotBypassRetainedSourceVerification(t *testing.T) {
	source, destination, model, payload := storageMigrationFixture(t)
	first, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !first.migrated {
		t.Fatalf("fixture migration failed: result=%+v err=%v", first, err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(source, model)), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, model), payload, 0600); err != nil {
		t.Fatal(err)
	}
	corrupt := bytes.Repeat([]byte{0x5a}, len(payload))
	if err := os.WriteFile(filepath.Join(destination, model), corrupt, 0600); err != nil {
		t.Fatal(err)
	}

	result, err := migrateDataDirectoryDetailed(source, destination)
	if err != nil || !result.migrated {
		t.Fatalf("structured marker prevented verified repair: result=%+v err=%v", result, err)
	}
	if result.state.Status != "reconciled" || !result.state.Reconciled {
		t.Fatalf("marker bypassed retained-source verification instead of reconciliation: %+v", result.state)
	}
	got, readErr := os.ReadFile(filepath.Join(destination, model))
	if readErr != nil || !bytes.Equal(got, payload) {
		t.Fatalf("active C: file did not replace conflicting D: file: bytes=%d err=%v", len(got), readErr)
	}
	if _, sourceErr := os.Stat(source); !errors.Is(sourceErr, os.ErrNotExist) {
		t.Fatalf("verified source was not retired after repair: %v", sourceErr)
	}
}

func TestInitialMigrationRejectsUnsafeDescendantBeforeTypeConflictCleanup(t *testing.T) {
	source, destination, _, _ := storageMigrationFixture(t)
	collision := filepath.Join(source, "collision")
	if err := os.WriteFile(collision, []byte("authoritative source file"), 0600); err != nil {
		t.Fatal(err)
	}
	guard := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(guard, []byte("must remain"), 0600); err != nil {
		t.Fatal(err)
	}
	staleDirectory := filepath.Join(destination+".migrating", "collision")
	if err := os.MkdirAll(staleDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(staleDirectory, "unsafe-link")
	if err := os.Symlink(guard, link); err != nil {
		t.Skipf("symlinks unavailable for unsafe-staging regression: %v", err)
	}

	result, err := migrateDataDirectoryDetailed(source, destination)
	if err == nil || result.migrated {
		t.Fatalf("unsafe type-conflict subtree was removed and published: result=%+v err=%v", result, err)
	}
	if _, err := os.Stat(collision); err != nil {
		t.Fatalf("source changed after unsafe staging rejection: %v", err)
	}
	if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("unsafe descendant was deleted before validation: info=%v err=%v", info, err)
	}
	if got, err := os.ReadFile(guard); err != nil || string(got) != "must remain" {
		t.Fatalf("unsafe cleanup touched external target: %q err=%v", got, err)
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

func TestStorageProcessLockRejectsConcurrentOwnerAndReleases(t *testing.T) {
	if storageProcessLock != nil {
		releaseStorageProcessLock()
	}
	t.Cleanup(releaseStorageProcessLock)

	root := t.TempDir()
	path := filepath.Join(root, storageInstanceLockName)
	if err := acquireStorageProcessLock(path, "source", "destination"); err != nil {
		t.Fatal(err)
	}
	owner := storageProcessLock
	if err := acquireStorageProcessLock(path, "source", "destination"); err != nil {
		t.Fatalf("same process should reuse its process lock: %v", err)
	}
	if storageProcessLock != owner {
		t.Fatal("same process replaced its active process lock")
	}
	if _, _, err := acquireStorageFileLock(path, "source", "destination"); !errors.Is(err, errStorageMigrationBusy) {
		t.Fatalf("second owner was not rejected: %v", err)
	}

	releaseStorageProcessLock()
	lock, _, err := acquireStorageFileLock(path, "source", "destination")
	if err != nil {
		t.Fatalf("lock was not released with the process session: %v", err)
	}
	lock.release()
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
