package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func storageRelativeKey(relative string) string {
	return strings.ToLower(filepath.ToSlash(filepath.Clean(relative)))
}

func validateStorageTreeEntry(path string, info os.FileInfo) error {
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() && !info.IsDir() {
		return fmt.Errorf("nieobsługiwany wpis magazynu: %s", path)
	}
	return nil
}

func stableStorageFileSHA256(path string, before os.FileInfo) ([sha256.Size]byte, error) {
	var empty [sha256.Size]byte
	fileHash, err := fileSHA256(path)
	if err != nil {
		return empty, err
	}
	after, err := os.Lstat(path)
	if err != nil {
		return empty, err
	}
	if after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() ||
		after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) || !os.SameFile(before, after) {
		return empty, fmt.Errorf("plik zmienił się podczas weryfikacji: %s", path)
	}
	return fileHash, nil
}

func appendReconciledDirectoryManifest(manifest io.Writer, relative string) {
	_, _ = io.WriteString(manifest, "d")
	_, _ = manifest.Write([]byte{0})
	_, _ = io.WriteString(manifest, filepath.ToSlash(relative))
	_, _ = manifest.Write([]byte{0})
}

func appendReconciledFileManifest(manifest io.Writer, relative string, size int64, fileHash [sha256.Size]byte) {
	_, _ = io.WriteString(manifest, "f")
	_, _ = manifest.Write([]byte{0})
	_, _ = io.WriteString(manifest, filepath.ToSlash(relative))
	_, _ = manifest.Write([]byte{0})
	_, _ = io.WriteString(manifest, strconv.FormatInt(size, 10))
	_, _ = manifest.Write([]byte{0})
	_, _ = manifest.Write(fileHash[:])
}

type validatedStoragePath struct {
	path  string
	isDir bool
}

// removeValidatedStorageTree never delegates an unvisited subtree to
// os.RemoveAll. It first walks and validates every entry, then removes regular
// files and empty directories from the leaves upward. A symlink, device, pipe,
// or entry changed during the operation stops cleanup without following it.
func removeValidatedStorageTree(root string, state *StorageMigrationState) error {
	var paths []validatedStoragePath
	if err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := validateStorageTreeEntry(path, info); err != nil {
			return err
		}
		paths = append(paths, validatedStoragePath{path: path, isDir: info.IsDir()})
		return nil
	}); err != nil {
		return err
	}
	for index := len(paths) - 1; index >= 0; index-- {
		entry := paths[index]
		info, err := os.Lstat(entry.path)
		if err != nil {
			return fmt.Errorf("ponowne sprawdzenie wpisu przed usunięciem %s: %w", entry.path, err)
		}
		if err := validateStorageTreeEntry(entry.path, info); err != nil {
			return err
		}
		if info.IsDir() != entry.isDir {
			return fmt.Errorf("typ wpisu zmienił się przed bezpiecznym usunięciem: %s", entry.path)
		}
		if err := os.Remove(entry.path); err != nil {
			return err
		}
		if state != nil {
			state.RemovedEntries++
		}
	}
	return nil
}

func expectedReconciledStorage(source, current string) (map[string]expectedStorageEntry, error) {
	expected := make(map[string]expectedStorageEntry)
	if err := filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." || relative == storageMarker {
			return nil
		}
		if err := validateStorageTreeEntry(path, info); err != nil {
			return err
		}
		expected[storageRelativeKey(relative)] = expectedStorageEntry{
			relative: relative,
			path:     path,
			isDir:    info.IsDir(),
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := filepath.Walk(current, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(current, path)
		if err != nil {
			return err
		}
		if relative == "." || relative == storageMarker {
			return nil
		}
		if err := validateStorageTreeEntry(path, info); err != nil {
			return err
		}
		key := storageRelativeKey(relative)
		if sourceEntry, exists := expected[key]; exists {
			if info.IsDir() && !sourceEntry.isDir {
				return filepath.SkipDir
			}
			// C: is the authoritative active source for every colliding path.
			// Matching directories still have to be traversed so D:-only children
			// survive the reconciliation.
			return nil
		}
		expected[key] = expectedStorageEntry{
			relative: relative,
			path:     path,
			isDir:    info.IsDir(),
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return expected, nil
}

func prepareReconciliationStaging(source, current, staging string, state *StorageMigrationState) error {
	info, err := os.Lstat(staging)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(staging, 0700); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("katalog etapowy scalania nie jest bezpiecznym katalogiem: %s", staging)
	}
	marker := filepath.Join(staging, storageMarker)
	if markerInfo, markerErr := os.Lstat(marker); markerErr == nil {
		if markerInfo.Mode()&os.ModeSymlink != 0 || !markerInfo.Mode().IsRegular() {
			return fmt.Errorf("nieprawidłowy znacznik w katalogu etapowym scalania: %s", marker)
		}
		if err := os.Remove(marker); err != nil {
			return fmt.Errorf("usunięcie przedwczesnego znacznika scalania: %w", err)
		}
	} else if !errors.Is(markerErr, os.ErrNotExist) {
		return markerErr
	}

	expected, err := expectedReconciledStorage(source, current)
	if err != nil {
		return err
	}
	return filepath.Walk(staging, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(staging, path)
		if err != nil {
			return err
		}
		if relative == "." || relative == storageMarker {
			return nil
		}
		if err := validateStorageTreeEntry(path, info); err != nil {
			return err
		}
		if entry, exists := expected[storageRelativeKey(relative)]; exists && entry.isDir == info.IsDir() {
			return nil
		}
		if info.Mode().IsRegular() && strings.HasSuffix(relative, storageMigrationPartSuffix) {
			original := strings.TrimSuffix(relative, storageMigrationPartSuffix)
			if entry, exists := expected[storageRelativeKey(original)]; exists && !entry.isDir {
				return nil // copyStorageFile validates the entire retained prefix.
			}
		}
		if info.IsDir() {
			if err := removeValidatedStorageTree(path, state); err != nil {
				return fmt.Errorf("bezpieczne usunięcie osieroconego poddrzewa scalania %s: %w", path, err)
			}
			return filepath.SkipDir
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("usunięcie osieroconego pliku scalania %s: %w", path, err)
		}
		state.RemovedEntries++
		return nil
	})
}

func ensureReconciliationDirectory(path string, mode os.FileMode, state *StorageMigrationState) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(path, mode.Perm())
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() && !info.IsDir() {
		return fmt.Errorf("niebezpieczny wpis w stagingu scalania: %s", path)
	}
	if info.IsDir() {
		return nil
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	state.RemovedEntries++
	return os.MkdirAll(path, mode.Perm())
}

func overlayStorageTree(root, staging, shadow string, allowHardLinks bool, state *StorageMigrationState) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." || relative == storageMarker {
			return nil
		}
		if err := validateStorageTreeEntry(path, info); err != nil {
			return err
		}
		if shadow != "" {
			shadowInfo, shadowErr := os.Lstat(filepath.Join(shadow, relative))
			if shadowErr == nil {
				if err := validateStorageTreeEntry(filepath.Join(shadow, relative), shadowInfo); err != nil {
					return err
				}
				if info.IsDir() && shadowInfo.IsDir() {
					// Keep walking: children present only in the published D: tree
					// still belong to the reconciled union.
				} else if info.IsDir() {
					return filepath.SkipDir
				} else {
					return nil
				}
			} else if !errors.Is(shadowErr, os.ErrNotExist) {
				return shadowErr
			}
		}
		target := filepath.Join(staging, relative)
		if info.IsDir() {
			return ensureReconciliationDirectory(target, info.Mode(), state)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		if allowHardLinks {
			// D: and its staging sibling are on the same volume. A hard link keeps
			// a D-only file without duplicating a large model; this code never
			// opens that link writable. A colliding C: file is first unlinked by
			// copyStorageFile, so replacing it cannot mutate the published D: inode.
			if _, targetErr := os.Lstat(target); errors.Is(targetErr, os.ErrNotExist) {
				if err := os.Link(path, target); err == nil {
					if err := removeMigrationPart(target+storageMigrationPartSuffix, state); err != nil {
						_ = os.Remove(target)
						return err
					}
					state.ReusedFiles++
					state.ReusedBytes += uint64(info.Size())
					return nil
				}
			} else if targetErr != nil {
				return targetErr
			}
		}
		return copyStorageFile(path, target, info.Mode(), info.ModTime(), state)
	})
}

func verifyReconciledStorageDetailed(source, current, staging string) (storageVerification, error) {
	var result storageVerification
	expected, err := expectedReconciledStorage(source, current)
	if err != nil {
		return result, err
	}
	seen := make(map[string]bool, len(expected))
	manifest := sha256.New()
	err = filepath.Walk(staging, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(staging, path)
		if err != nil {
			return err
		}
		if relative == "." || relative == storageMarker {
			return nil
		}
		if err := validateStorageTreeEntry(path, info); err != nil {
			return err
		}
		key := storageRelativeKey(relative)
		expectedEntry, exists := expected[key]
		if !exists {
			return fmt.Errorf("staging scalania zawiera wpis spoza sumy C: i D:: %s", relative)
		}
		if seen[key] {
			return fmt.Errorf("staging scalania zawiera kolidujące nazwy: %s", relative)
		}
		if expectedEntry.isDir != info.IsDir() {
			return fmt.Errorf("typ wpisu scalania nie odpowiada źródłu: %s", relative)
		}
		seen[key] = true
		if info.IsDir() {
			appendReconciledDirectoryManifest(manifest, relative)
			return nil
		}
		expectedInfo, err := os.Lstat(expectedEntry.path)
		if err != nil {
			return err
		}
		if expectedInfo.Mode()&os.ModeSymlink != 0 || !expectedInfo.Mode().IsRegular() || expectedInfo.Size() != info.Size() {
			return fmt.Errorf("rozmiar lub typ pliku zmienił się podczas scalania: %s", expectedEntry.relative)
		}
		expectedHash, err := stableStorageFileSHA256(expectedEntry.path, expectedInfo)
		if err != nil {
			return err
		}
		stagedHash, err := stableStorageFileSHA256(path, info)
		if err != nil {
			return err
		}
		if expectedHash != stagedHash {
			return fmt.Errorf("plik stagingu scalania ma inną sumę SHA-256: %s", expectedEntry.relative)
		}
		appendReconciledFileManifest(manifest, relative, info.Size(), stagedHash)
		result.Files++
		result.Bytes += uint64(info.Size())
		return nil
	})
	if err != nil {
		return result, err
	}
	for key, entry := range expected {
		if !seen[key] {
			return result, fmt.Errorf("brak wpisu %s w stagingu scalania", entry.relative)
		}
	}
	result.TreeSHA256 = hex.EncodeToString(manifest.Sum(nil))
	return result, nil
}

func readStructuredStorageMarker(directory string) (storageMarkerDocument, error) {
	var marker storageMarkerDocument
	path := filepath.Join(directory, storageMarker)
	info, err := os.Lstat(path)
	if err != nil {
		return marker, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return marker, errors.New("znacznik magazynu nie jest zwykłym plikiem")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return marker, err
	}
	if err := json.Unmarshal(data, &marker); err != nil {
		return marker, err
	}
	if marker.Format != "YourLM storage v1" || marker.TreeSHA256 == "" {
		return marker, errors.New("znacznik nie potwierdza zweryfikowanego magazynu")
	}
	return marker, nil
}

func readReconciledStorageMarker(directory string) (storageMarkerDocument, error) {
	marker, err := readStructuredStorageMarker(directory)
	if err != nil {
		return marker, err
	}
	if !marker.Reconciled {
		return marker, errors.New("znacznik nie potwierdza zweryfikowanego scalania")
	}
	return marker, nil
}

func reconciledStorageMarker(directory string) (bool, error) {
	_, err := readReconciledStorageMarker(directory)
	return err == nil, err
}

func writeStructuredStorageMarker(directory string, marker storageMarkerDocument) error {
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	parent := filepath.Dir(directory)
	temporary, err := os.CreateTemp(parent, ".yourlm-storage-marker-updating-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0600); err != nil {
		_ = temporary.Close()
		return err
	}
	_, writeErr := temporary.Write(data)
	syncErr := temporary.Sync()
	closeErr := temporary.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(temporaryPath, filepath.Join(directory, storageMarker)); err != nil {
		return err
	}
	if err := syncStorageDirectory(directory); err != nil {
		return err
	}
	return syncStorageDirectory(parent)
}

func setReconciledRollbackRetained(directory string, retained bool) error {
	marker, err := readReconciledStorageMarker(directory)
	if err != nil {
		return err
	}
	marker.RollbackRetained = retained
	return writeStructuredStorageMarker(directory, marker)
}

func markReconciledRollbackRetained(directory string) error {
	return setReconciledRollbackRetained(directory, true)
}

func setStorageSourceRetirementPending(directory string, pending bool) error {
	marker, err := readStructuredStorageMarker(directory)
	if err != nil {
		return err
	}
	marker.SourceRetirementPending = pending
	return writeStructuredStorageMarker(directory, marker)
}

func markStorageSourceRetirementPending(directory string) error {
	return setStorageSourceRetirementPending(directory, true)
}

// verifyReconciledStorageMarker re-hashes the already-published tree before an
// interrupted transaction is allowed to discard its previous D: backup.
func verifyReconciledStorageMarker(directory string) (storageVerification, error) {
	var result storageVerification
	marker, err := readReconciledStorageMarker(directory)
	if err != nil {
		return result, err
	}
	manifest := sha256.New()
	err = filepath.Walk(directory, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		if relative == "." || relative == storageMarker {
			return nil
		}
		if err := validateStorageTreeEntry(path, info); err != nil {
			return err
		}
		if info.IsDir() {
			appendReconciledDirectoryManifest(manifest, relative)
			return nil
		}
		fileHash, err := stableStorageFileSHA256(path, info)
		if err != nil {
			return fmt.Errorf("sprawdzanie znacznika scalania dla %s: %w", relative, err)
		}
		appendReconciledFileManifest(manifest, relative, info.Size(), fileHash)
		result.Files++
		result.Bytes += uint64(info.Size())
		return nil
	})
	if err != nil {
		return result, err
	}
	result.TreeSHA256 = hex.EncodeToString(manifest.Sum(nil))
	if result.Files != marker.Files || result.Bytes != marker.Bytes || result.TreeSHA256 != marker.TreeSHA256 {
		return result, errors.New("opublikowana biblioteka nie odpowiada znacznikowi zweryfikowanego scalania")
	}
	return result, nil
}

type storageReconcileRecovery string

const (
	storageReconcileRecoveryNone      storageReconcileRecovery = ""
	storageReconcileRecoveryRestored  storageReconcileRecovery = "restored"
	storageReconcileRecoveryFinalized storageReconcileRecovery = "finalized"
)

func retainReconcileRollback(destination, warning string) (storageReconcileRecovery, string, error) {
	if err := markReconciledRollbackRetained(destination); err != nil {
		return storageReconcileRecoveryNone, "", fmt.Errorf("oznaczenie zachowanej kopii rollback: %w", err)
	}
	return storageReconcileRecoveryFinalized, warning, nil
}

func recoverStorageReconcileSwap(destination string) (storageReconcileRecovery, string, error) {
	backup := destination + storageReconcileBackupSuffix
	backupInfo, backupErr := os.Lstat(backup)
	if errors.Is(backupErr, os.ErrNotExist) {
		if marker, markerErr := readReconciledStorageMarker(destination); markerErr == nil && marker.RollbackRetained {
			if clearErr := setReconciledRollbackRetained(destination, false); clearErr != nil {
				return storageReconcileRecoveryNone, "Nie udało się wyczyścić znacznika usuniętej kopii rollback: " + clearErr.Error(), nil
			}
		}
		return storageReconcileRecoveryNone, "", nil
	}
	if backupErr != nil {
		return storageReconcileRecoveryNone, "", backupErr
	}

	destinationInfo, destinationErr := os.Lstat(destination)
	if errors.Is(destinationErr, os.ErrNotExist) {
		if backupInfo.Mode()&os.ModeSymlink != 0 || !backupInfo.IsDir() {
			return storageReconcileRecoveryNone, "", fmt.Errorf("kopia transakcyjna magazynu nie jest bezpiecznym katalogiem: %s", backup)
		}
		backupValid, _, err := storageMarkerStatus(backup)
		if err != nil {
			return storageReconcileRecoveryNone, "", fmt.Errorf("kopia transakcyjna magazynu nie ma prawidłowego znacznika: %w", err)
		}
		if !backupValid {
			return storageReconcileRecoveryNone, "", errors.New("kopia transakcyjna magazynu nie ma prawidłowego znacznika")
		}
		if err := os.Rename(backup, destination); err != nil {
			return storageReconcileRecoveryNone, "", fmt.Errorf("przywrócenie opublikowanego D: po przerwanym scalaniu: %w", err)
		}
		if err := syncStorageDirectory(filepath.Dir(destination)); err != nil {
			return storageReconcileRecoveryNone, "", err
		}
		return storageReconcileRecoveryRestored, "", nil
	}
	if destinationErr != nil {
		return storageReconcileRecoveryNone, "", destinationErr
	}
	if destinationInfo.Mode()&os.ModeSymlink != 0 || !destinationInfo.IsDir() {
		return storageReconcileRecoveryNone, "", fmt.Errorf("cel po przerwanym scalaniu nie jest bezpiecznym katalogiem: %s", destination)
	}
	marker, markerErr := readReconciledStorageMarker(destination)
	if markerErr != nil {
		return storageReconcileRecoveryNone, "", fmt.Errorf("nie można rozstrzygnąć przerwanego scalania bez znacznika nowej biblioteki: %w", markerErr)
	}
	if _, err := verifyReconciledStorageMarker(destination); err != nil {
		if marker.RollbackRetained {
			return storageReconcileRecoveryFinalized, "Scalona biblioteka D: została wcześniej w pełni zweryfikowana i pozostaje aktywna, ale od tego czasu zmieniła się podczas pracy. Poprzednia kopia " + backup + " pozostaje zachowana i nie zostanie automatycznie usunięta.", nil
		}
		return storageReconcileRecoveryNone, "", fmt.Errorf("nie można rozstrzygnąć przerwanego scalania bez ponownej weryfikacji nowej biblioteki: %w", err)
	}
	if backupInfo.Mode()&os.ModeSymlink != 0 || !backupInfo.IsDir() {
		return retainReconcileRollback(destination, "Zweryfikowana scalona biblioteka D: jest aktywna, ale nie dotknięto niebezpiecznego wpisu pod zastrzeżoną ścieżką "+backup+".")
	}

	// The new D: tree is independently re-verified at this point, so failure to
	// clean an older rollback directory must not force the application onto an
	// empty C: fallback. Keep the backup, report it, and retry cleanup later.
	backupValid, _, backupMarkerErr := storageMarkerStatus(backup)
	if backupMarkerErr != nil || !backupValid {
		return retainReconcileRollback(destination, "Zweryfikowana scalona biblioteka D: jest aktywna, ale nie usunięto nierozpoznanej poprzedniej kopii "+backup+".")
	}
	if err := removeValidatedStorageTree(backup, nil); err != nil {
		return retainReconcileRollback(destination, "Zweryfikowana scalona biblioteka D: jest aktywna, ale poprzednia kopia "+backup+" pozostała do bezpiecznego sprzątnięcia: "+err.Error())
	}
	if err := syncStorageDirectory(filepath.Dir(destination)); err != nil {
		return storageReconcileRecoveryFinalized, "Zweryfikowana scalona biblioteka D: jest aktywna; synchronizacja sprzątnięcia poprzedniej kopii zgłosiła błąd: " + err.Error(), nil
	}
	if marker.RollbackRetained {
		if err := setReconciledRollbackRetained(destination, false); err != nil {
			return storageReconcileRecoveryFinalized, "Poprzednia kopia D: została usunięta, ale nie wyczyszczono jej znacznika: " + err.Error(), nil
		}
	}
	return storageReconcileRecoveryFinalized, "", nil
}

func publishReconciledStorage(staging, destination string) (string, error) {
	backup := destination + storageReconcileBackupSuffix
	if _, err := os.Lstat(backup); !errors.Is(err, os.ErrNotExist) {
		if err == nil {
			return "", fmt.Errorf("pozostała nierozstrzygnięta kopia transakcyjna: %s", backup)
		}
		return "", err
	}
	if err := os.Rename(destination, backup); err != nil {
		return "", fmt.Errorf("odłożenie poprzedniej biblioteki D:: %w", err)
	}
	if err := syncStorageDirectory(filepath.Dir(destination)); err != nil {
		_ = os.Rename(backup, destination)
		return "", err
	}
	if err := os.Rename(staging, destination); err != nil {
		restoreErr := os.Rename(backup, destination)
		if restoreErr != nil {
			return "", fmt.Errorf("publikacja scalonej biblioteki: %v; przywrócenie poprzedniej biblioteki: %w", err, restoreErr)
		}
		return "", fmt.Errorf("publikacja scalonej biblioteki: %w", err)
	}
	if err := syncStorageDirectory(filepath.Dir(destination)); err != nil {
		return "", fmt.Errorf("synchronizacja publikacji scalonej biblioteki: %w", err)
	}
	if _, err := verifyReconciledStorageMarker(destination); err != nil {
		return "", fmt.Errorf("ponowna weryfikacja opublikowanej scalonej biblioteki: %w", err)
	}
	if err := removeValidatedStorageTree(backup, nil); err != nil {
		if markerErr := markReconciledRollbackRetained(destination); markerErr != nil {
			return "", fmt.Errorf("scalona biblioteka jest aktywna, ale nie usunięto poprzedniej kopii D: (%v) ani nie oznaczono jej do zachowania: %w", err, markerErr)
		}
		return backup, fmt.Errorf("scalona biblioteka jest aktywna, ale nie usunięto bezpiecznie poprzedniej kopii D:: %w", err)
	}
	if err := syncStorageDirectory(filepath.Dir(destination)); err != nil {
		return "", err
	}
	return "", nil
}

func reconcilePublishedStorage(source, destination, staging string, state *StorageMigrationState) (storageVerification, string, error) {
	var verified storageVerification
	state.Status = "copying"
	state.Title = "Scalanie zachowanego C: z D:"
	state.Detail = "RC19 buduje nieaktywną sumę obu bibliotek; pliki C: mają pierwszeństwo, a wpisy istniejące tylko na D: pozostają zachowane."
	if err := prepareReconciliationStaging(source, destination, staging, state); err != nil {
		return verified, "", err
	}
	if err := overlayStorageTree(destination, staging, source, true, state); err != nil {
		return verified, "", fmt.Errorf("zachowanie opublikowanych plików D: w stagingu: %w", err)
	}
	if err := overlayStorageTree(source, staging, "", false, state); err != nil {
		return verified, "", fmt.Errorf("nałożenie aktywnych plików C: na staging: %w", err)
	}
	verified, err := verifyReconciledStorageDetailed(source, destination, staging)
	if err != nil {
		return verified, "", fmt.Errorf("końcowa weryfikacja sumy C: i D:: %w", err)
	}
	state.VerifiedFiles = verified.Files
	state.VerifiedBytes = verified.Bytes
	if err := writeStorageMarkerState(staging, source, verified, true); err != nil {
		return verified, "", fmt.Errorf("zapis znacznika zweryfikowanego scalania: %w", err)
	}
	retainedD, err := publishReconciledStorage(staging, destination)
	if err != nil {
		if retainedD != "" {
			state.Reconciled = true
			return verified, "Poprzednia kopia D: pozostała w " + retainedD + ".", nil
		}
		return verified, "", err
	}
	state.Reconciled = true
	return verified, "", nil
}
