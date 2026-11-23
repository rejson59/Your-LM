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
	"time"
)

const (
	storageMarker              = ".yourlm-storage-v1"
	storageMigrationPartSuffix = ".migrating-part"
	storageMigrationLockName   = ".yourlm-data-migration.lock"
)

var errStorageMigrationBusy = errors.New("migracja magazynu działa w innym procesie YourLM")

type StorageDrive struct {
	Letter      string `json:"letter"`
	Path        string `json:"path"`
	Label       string `json:"label,omitempty"`
	FileSystem  string `json:"fileSystem,omitempty"`
	Type        string `json:"type,omitempty"`
	Total       uint64 `json:"total"`
	Free        uint64 `json:"free"`
	Selected    bool   `json:"selected"`
	System      bool   `json:"system"`
	Recommended bool   `json:"recommended"`
}

type StorageMigrationState struct {
	Status          string `json:"status,omitempty"`
	Title           string `json:"title,omitempty"`
	Detail          string `json:"detail,omitempty"`
	Action          string `json:"action,omitempty"`
	Error           string `json:"error,omitempty"`
	SourcePath      string `json:"sourcePath,omitempty"`
	DestinationPath string `json:"destinationPath,omitempty"`
	StagingPath     string `json:"stagingPath,omitempty"`
	RetainedPath    string `json:"retainedPath,omitempty"`
	SourceRetained  bool   `json:"sourceRetained"`
	RecoveredLock   bool   `json:"recoveredLock"`
	ResumedFiles    int    `json:"resumedFiles,omitempty"`
	ResumedBytes    uint64 `json:"resumedBytes,omitempty"`
	ReusedFiles     int    `json:"reusedFiles,omitempty"`
	ReusedBytes     uint64 `json:"reusedBytes,omitempty"`
	RecoveredParts  int    `json:"recoveredParts,omitempty"`
	ReplacedFiles   int    `json:"replacedFiles,omitempty"`
	RemovedEntries  int    `json:"removedEntries,omitempty"`
	CopiedFiles     int    `json:"copiedFiles,omitempty"`
	VerifiedFiles   int    `json:"verifiedFiles,omitempty"`
	VerifiedBytes   uint64 `json:"verifiedBytes,omitempty"`
}

type StorageState struct {
	Supported       bool                  `json:"supported"`
	DataPath        string                `json:"dataPath"`
	InstallPath     string                `json:"installPath,omitempty"`
	PreferredRoot   string                `json:"preferredRoot,omitempty"`
	Automatic       bool                  `json:"automatic"`
	Migrated        bool                  `json:"migrated"`
	MigrationSource string                `json:"migrationSource,omitempty"`
	Migration       StorageMigrationState `json:"migration"`
	Message         string                `json:"message,omitempty"`
	Warning         string                `json:"warning,omitempty"`
	Drives          []StorageDrive        `json:"drives,omitempty"`
}

type storageVerification struct {
	Files      int
	Bytes      uint64
	TreeSHA256 string
}

type storageMarkerDocument struct {
	Format       string `json:"format"`
	VerifiedAt   string `json:"verifiedAt"`
	Source       string `json:"source,omitempty"`
	Files        int    `json:"files"`
	Bytes        uint64 `json:"bytes"`
	TreeSHA256   string `json:"treeSha256"`
	YourLM       string `json:"yourlm,omitempty"`
}

type storageMigrationResult struct {
	migrated bool
	warning  string
	state    StorageMigrationState
}

type storageMigrationLockMetadata struct {
	PID         int    `json:"pid"`
	StartedAt   string `json:"startedAt"`
	Source      string `json:"source"`
	Destination string `json:"destination"`
}

type storageMigrationLock struct {
	file *os.File
	path string
}

func refreshStorageFree(state *StorageState) {
	for index := range state.Drives {
		if free, err := availableDiskBytes(state.Drives[index].Path); err == nil {
			state.Drives[index].Free = free
		}
	}
}

func sameCleanPath(first, second string) bool {
	left, leftErr := filepath.Abs(first)
	right, rightErr := filepath.Abs(second)
	if leftErr != nil || rightErr != nil {
		return false
	}
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}

func pathInside(path, parent string) bool {
	absolutePath, pathErr := filepath.Abs(path)
	absoluteParent, parentErr := filepath.Abs(parent)
	if pathErr != nil || parentErr != nil {
		return false
	}
	relative, err := filepath.Rel(absoluteParent, absolutePath)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func fileSHA256(path string) ([sha256.Size]byte, error) {
	var empty [sha256.Size]byte
	file, err := os.Open(path)
	if err != nil {
		return empty, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return empty, err
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result, nil
}

func writeSyncedFile(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode.Perm())
	if err != nil {
		return err
	}
	_, writeErr := file.Write(data)
	syncErr := file.Sync()
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}

func acquireStorageMigrationLock(source, destination string) (*storageMigrationLock, bool, error) {
	path := filepath.Join(filepath.Dir(destination), storageMigrationLockName)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, false, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, err
	}
	if err := tryLockStorageFile(file); err != nil {
		_ = file.Close()
		if storageLockIsBusy(err) {
			return nil, false, fmt.Errorf("%w; poczekaj, aż pierwsze okno zakończy kopiowanie", errStorageMigrationBusy)
		}
		return nil, false, fmt.Errorf("blokada migracji: %w", err)
	}
	previous, _ := io.ReadAll(io.LimitReader(file, 64<<10))
	recovered := len(strings.TrimSpace(string(previous))) > 0
	metadata, _ := json.Marshal(storageMigrationLockMetadata{
		PID:         os.Getpid(),
		StartedAt:   time.Now().Format(time.RFC3339Nano),
		Source:      source,
		Destination: destination,
	})
	if err := file.Truncate(0); err == nil {
		_, err = file.Seek(0, io.SeekStart)
	}
	if err == nil {
		_, err = file.Write(append(metadata, '\n'))
	}
	if err == nil {
		err = file.Sync()
	}
	if err != nil {
		_ = unlockStorageFile(file)
		_ = file.Close()
		return nil, false, fmt.Errorf("zapis blokady migracji: %w", err)
	}
	return &storageMigrationLock{file: file, path: path}, recovered, nil
}

func (lock *storageMigrationLock) release() {
	if lock == nil || lock.file == nil {
		return
	}
	// An empty, unlocked file means the previous owner exited normally. If the
	// process crashes, JSON remains and the next owner can report stale-lock
	// recovery while the operating-system lock itself is released automatically.
	_ = lock.file.Truncate(0)
	_, _ = lock.file.Seek(0, io.SeekStart)
	_ = lock.file.Sync()
	_ = unlockStorageFile(lock.file)
	_ = lock.file.Close()
	lock.file = nil
}

func removeMigrationPart(path string, state *StorageMigrationState) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("plik etapowy nie jest zwykłym plikiem i nie zostanie usunięty: %s", path)
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("nie można odblokować starego pliku etapowego %s: %w", path, err)
	}
	state.RecoveredParts++
	return nil
}

func matchingFilePrefix(source, part string, size int64) (bool, error) {
	if size == 0 {
		return true, nil
	}
	left, err := os.Open(source)
	if err != nil {
		return false, err
	}
	defer left.Close()
	right, err := os.Open(part)
	if err != nil {
		return false, err
	}
	defer right.Close()
	leftBuffer := make([]byte, 4<<20)
	rightBuffer := make([]byte, len(leftBuffer))
	remaining := size
	for remaining > 0 {
		chunk := min(int64(len(leftBuffer)), remaining)
		if _, err := io.ReadFull(left, leftBuffer[:chunk]); err != nil {
			return false, err
		}
		if _, err := io.ReadFull(right, rightBuffer[:chunk]); err != nil {
			return false, err
		}
		if !equalBytes(leftBuffer[:chunk], rightBuffer[:chunk]) {
			return false, nil
		}
		remaining -= chunk
	}
	return true, nil
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func copyStorageFile(source, destination string, mode os.FileMode, modified time.Time, state *StorageMigrationState) error {
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !sourceInfo.Mode().IsRegular() {
		return fmt.Errorf("źródłowy wpis nie jest zwykłym plikiem: %s", source)
	}
	part := destination + storageMigrationPartSuffix
	if current, statErr := os.Lstat(destination); statErr == nil {
		if current.Mode()&os.ModeSymlink != 0 || !current.Mode().IsRegular() {
			return fmt.Errorf("istniejący wpis etapowy nie jest bezpiecznym zwykłym plikiem: %s", destination)
		}
		matches := false
		if current.Size() == sourceInfo.Size() {
			left, leftErr := fileSHA256(source)
			right, rightErr := fileSHA256(destination)
			if leftErr != nil {
				return leftErr
			}
			if rightErr != nil {
				return fmt.Errorf("sprawdzanie gotowego pliku etapowego %s: %w", destination, rightErr)
			}
			matches = left == right
		}
		if matches {
			if err := removeMigrationPart(part, state); err != nil {
				return err
			}
			state.ReusedFiles++
			state.ReusedBytes += uint64(current.Size())
			return nil
		}
		// This is a regular file inside the explicitly non-published staging
		// tree, but it no longer matches the authoritative source. Replace only
		// this validated stale entry; never remove the source or whole staging.
		if err := os.Remove(destination); err != nil {
			return fmt.Errorf("zastąpienie niezgodnego pliku etapowego %s: %w", destination, err)
		}
		state.ReplacedFiles++
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}

	var offset int64
	partExists := false
	if partInfo, partErr := os.Lstat(part); partErr == nil {
		if partInfo.Mode()&os.ModeSymlink != 0 || !partInfo.Mode().IsRegular() {
			return fmt.Errorf("istniejący plik etapowy nie jest bezpiecznym zwykłym plikiem: %s", part)
		}
		partExists = true
		if partInfo.Size() <= sourceInfo.Size() {
			matches, compareErr := matchingFilePrefix(source, part, partInfo.Size())
			if compareErr != nil {
				return fmt.Errorf("sprawdzanie częściowej kopii %s: %w", part, compareErr)
			}
			if matches {
				offset = partInfo.Size()
				if offset > 0 {
					state.ResumedFiles++
					state.ResumedBytes += uint64(offset)
				}
			} else if err := removeMigrationPart(part, state); err != nil {
				return err
			} else {
				partExists = false
			}
		} else if err := removeMigrationPart(part, state); err != nil {
			return err
		} else {
			partExists = false
		}
	} else if !errors.Is(partErr, os.ErrNotExist) {
		return partErr
	}

	input, err := os.Open(source)
	if err != nil {
		return err
	}
	if _, err := input.Seek(offset, io.SeekStart); err != nil {
		_ = input.Close()
		return err
	}
	flags := os.O_WRONLY | os.O_APPEND
	if !partExists {
		flags = os.O_CREATE | os.O_EXCL | os.O_WRONLY
	}
	output, err := os.OpenFile(part, flags, mode.Perm())
	if err != nil {
		_ = input.Close()
		return fmt.Errorf("otwarcie wznawialnej kopii %s: %w", part, err)
	}
	remaining := sourceInfo.Size() - offset
	written, copyErr := io.CopyN(output, input, remaining)
	inputErr := input.Close()
	syncErr := output.Sync()
	outputErr := output.Close()
	// Keep a valid prefix after an interruption or full disk. The next run first
	// compares it byte-for-byte with the source and resumes only if it still
	// matches; no failure path removes an unverified source file.
	if copyErr != nil {
		return fmt.Errorf("kopiowanie %s zatrzymało się po %d z %d nowych bajtów (część zachowana do wznowienia): %w", source, written, remaining, copyErr)
	}
	if inputErr != nil {
		return inputErr
	}
	if syncErr != nil {
		return fmt.Errorf("synchronizacja częściowej kopii %s: %w", part, syncErr)
	}
	if outputErr != nil {
		return outputErr
	}
	if written != remaining {
		return fmt.Errorf("niepełna kopia %s: %d z %d nowych bajtów", source, written, remaining)
	}
	sourceAfter, err := os.Stat(source)
	if err != nil {
		return err
	}
	if sourceAfter.Size() != sourceInfo.Size() || !sourceAfter.ModTime().Equal(sourceInfo.ModTime()) {
		return fmt.Errorf("plik źródłowy zmienił się podczas kopiowania; zachowano go i kopię etapową: %s", source)
	}
	sourceHash, sourceHashErr := fileSHA256(source)
	copiedHash, copiedHashErr := fileSHA256(part)
	if sourceHashErr != nil {
		return sourceHashErr
	}
	if copiedHashErr != nil {
		return copiedHashErr
	}
	if copiedHash != sourceHash {
		if err := removeMigrationPart(part, state); err != nil {
			return fmt.Errorf("weryfikacja SHA-256 kopii nie powiodła się, a wadliwej części nie można odłożyć: %w", err)
		}
		return fmt.Errorf("weryfikacja SHA-256 kopii nie powiodła się: %s", source)
	}
	_ = os.Chmod(part, mode.Perm())
	_ = os.Chtimes(part, modified, modified)
	if err := os.Rename(part, destination); err != nil {
		return fmt.Errorf("zatwierdzenie zweryfikowanej kopii %s: %w", destination, err)
	}
	if err := syncStorageDirectory(filepath.Dir(destination)); err != nil {
		return fmt.Errorf("synchronizacja katalogu docelowego: %w", err)
	}
	state.CopiedFiles++
	return nil
}

func cleanStaleStorageEntries(source, staging string, state *StorageMigrationState) error {
	var directories []string
	err := filepath.Walk(staging, func(path string, info os.FileInfo, walkErr error) error {
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
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() && !info.IsDir() {
			return fmt.Errorf("katalog etapowy zawiera nieobsługiwany wpis: %s", path)
		}
		sourcePath := filepath.Join(source, relative)
		sourceInfo, sourceErr := os.Lstat(sourcePath)
		if sourceErr == nil {
			if sourceInfo.Mode()&os.ModeSymlink != 0 || !sourceInfo.Mode().IsRegular() && !sourceInfo.IsDir() {
				return fmt.Errorf("źródło zawiera nieobsługiwany wpis: %s", sourcePath)
			}
			if sourceInfo.IsDir() == info.IsDir() {
				return nil
			}
			if info.IsDir() {
				// The authoritative source now has a file where an old staging
				// attempt had a directory. The whole subtree is necessarily stale.
				if err := os.RemoveAll(path); err != nil {
					return fmt.Errorf("usunięcie niezgodnego poddrzewa etapowego %s: %w", path, err)
				}
				state.RemovedEntries++
				return filepath.SkipDir
			}
			if err := os.Remove(path); err != nil {
				return fmt.Errorf("usunięcie niezgodnego wpisu etapowego %s: %w", path, err)
			}
			state.RemovedEntries++
			return nil
		}
		if !errors.Is(sourceErr, os.ErrNotExist) {
			return sourceErr
		}
		if info.Mode().IsRegular() && strings.HasSuffix(relative, storageMigrationPartSuffix) {
			original := strings.TrimSuffix(relative, storageMigrationPartSuffix)
			originalInfo, originalErr := os.Lstat(filepath.Join(source, original))
			if originalErr == nil && originalInfo.Mode().IsRegular() && originalInfo.Mode()&os.ModeSymlink == 0 {
				return nil // Valid candidate part; copyStorageFile will verify its prefix.
			}
			if originalErr != nil && !errors.Is(originalErr, os.ErrNotExist) {
				return originalErr
			}
		}
		if info.IsDir() {
			directories = append(directories, path)
			return nil
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("usunięcie osieroconego wpisu etapowego %s: %w", path, err)
		}
		state.RemovedEntries++
		return nil
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := os.Remove(directories[index]); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("usunięcie pustego katalogu etapowego %s: %w", directories[index], err)
		}
		state.RemovedEntries++
	}
	return nil
}

func prepareStorageStaging(source, staging string, state *StorageMigrationState) error {
	info, err := os.Lstat(staging)
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(staging, 0700)
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("katalog etapowy migracji nie jest bezpiecznym katalogiem: %s", staging)
	}
	marker := filepath.Join(staging, storageMarker)
	if markerInfo, markerErr := os.Lstat(marker); markerErr == nil {
		if markerInfo.Mode()&os.ModeSymlink != 0 || !markerInfo.Mode().IsRegular() {
			return fmt.Errorf("nieprawidłowy znacznik w katalogu etapowym: %s", marker)
		}
		if err := os.Remove(marker); err != nil {
			return fmt.Errorf("usunięcie przedwczesnego znacznika etapowego: %w", err)
		}
	} else if !errors.Is(markerErr, os.ErrNotExist) {
		return markerErr
	}
	return cleanStaleStorageEntries(source, staging, state)
}

func copyStorageTree(source, destination string, state *StorageMigrationState) error {
	if err := prepareStorageStaging(source, destination, state); err != nil {
		return err
	}
	return filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if relative == storageMarker {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() && !info.IsDir() {
			return fmt.Errorf("migracja odrzuciła nieobsługiwany wpis: %s", path)
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyStorageFile(path, target, info.Mode(), info.ModTime(), state)
	})
}

func verifyStorageTreeDetailed(source, destination string, exact bool) (storageVerification, error) {
	var result storageVerification
	manifest := sha256.New()
	err := filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
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
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() && !info.IsDir() {
			return fmt.Errorf("weryfikacja migracji odrzuciła nieobsługiwany wpis: %s", path)
		}
		target := filepath.Join(destination, relative)
		targetInfo, err := os.Lstat(target)
		if err != nil {
			return fmt.Errorf("brak wpisu %s w przeniesionej bibliotece: %w", relative, err)
		}
		if info.IsDir() {
			if !targetInfo.IsDir() || targetInfo.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("docelowy wpis nie jest katalogiem: %s", relative)
			}
			return nil
		}
		if !targetInfo.Mode().IsRegular() || targetInfo.Mode()&os.ModeSymlink != 0 || targetInfo.Size() != info.Size() {
			return fmt.Errorf("docelowy plik różni się rozmiarem lub typem: %s", relative)
		}
		sourceHash, sourceErr := fileSHA256(path)
		targetHash, targetErr := fileSHA256(target)
		if sourceErr != nil {
			return sourceErr
		}
		if targetErr != nil {
			return targetErr
		}
		if sourceHash != targetHash {
			return fmt.Errorf("docelowy plik ma inną sumę SHA-256: %s", relative)
		}
		normalized := filepath.ToSlash(relative)
		_, _ = io.WriteString(manifest, normalized)
		_, _ = manifest.Write([]byte{0})
		_, _ = io.WriteString(manifest, strconv.FormatInt(info.Size(), 10))
		_, _ = manifest.Write([]byte{0})
		_, _ = manifest.Write(sourceHash[:])
		result.Files++
		result.Bytes += uint64(info.Size())
		return nil
	})
	if err != nil {
		return result, err
	}
	if exact {
		err = filepath.Walk(destination, func(path string, info os.FileInfo, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			relative, err := filepath.Rel(destination, path)
			if err != nil {
				return err
			}
			if relative == "." || relative == storageMarker {
				return nil
			}
			if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() && !info.IsDir() {
				return fmt.Errorf("katalog etapowy zawiera nieobsługiwany wpis: %s", relative)
			}
			sourceInfo, sourceErr := os.Lstat(filepath.Join(source, relative))
			if errors.Is(sourceErr, os.ErrNotExist) {
				return fmt.Errorf("katalog etapowy zawiera wpis spoza źródła i nie zostanie opublikowany: %s", relative)
			}
			if sourceErr != nil {
				return sourceErr
			}
			if sourceInfo.IsDir() != info.IsDir() || sourceInfo.Mode().IsRegular() != info.Mode().IsRegular() {
				return fmt.Errorf("typ wpisu etapowego nie odpowiada źródłu: %s", relative)
			}
			return nil
		})
		if err != nil {
			return result, err
		}
	}
	result.TreeSHA256 = hex.EncodeToString(manifest.Sum(nil))
	return result, nil
}

func verifyStorageTree(source, destination string) error {
	_, err := verifyStorageTreeDetailed(source, destination, false)
	return err
}

func storageMarkerStatus(destination string) (valid, structured bool, err error) {
	path := filepath.Join(destination, storageMarker)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return false, false, fmt.Errorf("znacznik magazynu nie jest zwykłym plikiem")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false, err
	}
	if strings.TrimSpace(string(data)) == "YourLM storage v1" {
		return true, false, nil
	}
	var marker storageMarkerDocument
	if json.Unmarshal(data, &marker) == nil && marker.Format == "YourLM storage v1" && marker.TreeSHA256 != "" {
		return true, true, nil
	}
	return false, false, fmt.Errorf("znacznik magazynu YourLM jest uszkodzony")
}

func writeStorageMarker(directory, source string, verified storageVerification) error {
	document := storageMarkerDocument{
		Format:     "YourLM storage v1",
		VerifiedAt: time.Now().Format(time.RFC3339Nano),
		Source:     source,
		Files:      verified.Files,
		Bytes:      verified.Bytes,
		TreeSHA256: verified.TreeSHA256,
		YourLM:     buildVersion,
	}
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(directory, storageMarker)
	if err := writeSyncedFile(path, data, 0600); err != nil {
		return err
	}
	return syncStorageDirectory(directory)
}

func directoryHasEntries(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return len(entries) > 0, nil
}

func retireStorageSource(source string) (retainedPath, warning string) {
	backup := fmt.Sprintf("%s.migrated-backup-%d-%d", source, time.Now().Unix(), os.Getpid())
	if err := os.Rename(source, backup); err != nil {
		return source, "Biblioteka na D: jest zweryfikowana i aktywna, ale Windows zachował oryginał na C:, ponieważ nie można go jeszcze odłożyć: " + err.Error()
	}
	_ = syncStorageDirectory(filepath.Dir(source))
	if err := os.RemoveAll(backup); err != nil {
		return backup, "Biblioteka na D: jest zweryfikowana i aktywna, ale kopia zapasowa na C: nie została jeszcze w pełni usunięta: " + err.Error()
	}
	return "", ""
}

func migrationAction(err error) string {
	if errors.Is(err, errStorageMigrationBusy) {
		return "Migrację prowadzi już inne okno YourLM. Zamknij drugą kopię i poczekaj na zakończenie pierwszej; nie usuwaj danych z C:."
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, strings.ToLower(storageMigrationPartSuffix)), strings.Contains(text, "plik etapowy"):
		return "Zamknij inne okna YourLM oraz program, który może skanować lub blokować ten plik, a następnie uruchom RC17 ponownie. Zweryfikowana część zostanie wznowiona; danych z C: nie usuwaj."
	case strings.Contains(text, "za mało"), strings.Contains(text, "not enough space"), strings.Contains(text, "disk full"):
		return "Zwolnij miejsce na D: i uruchom YourLM ponownie. Oryginał na C: pozostaje bezpieczny."
	case strings.Contains(text, "access is denied"), strings.Contains(text, "permission denied"), strings.Contains(text, "odmowa dostępu"):
		return "Zamknij inne okna YourLM i sprawdź uprawnienia D:\\YourLM, potem uruchom program ponownie jako zwykły użytkownik. Nie usuwaj źródła z C:."
	default:
		return "Uruchom YourLM ponownie po zamknięciu drugiej kopii. Jeśli komunikat wróci, skopiuj szczegóły techniczne; oryginał na C: pozostaje nietknięty."
	}
}

// migrateDataDirectoryDetailed performs a verified, restartable cross-volume
// copy. A per-destination operating-system lock prevents two processes from
// publishing the staging tree. Every existing part is prefix-validated before
// append, every final file is compared by SHA-256, and the source is retired
// only after a synced marker and atomic staging rename make D: authoritative.
func migrateDataDirectoryDetailed(source, destination string) (result storageMigrationResult, err error) {
	result.state = StorageMigrationState{
		Status:          "checking",
		Title:           "Sprawdzanie magazynu",
		SourcePath:      source,
		DestinationPath: destination,
		StagingPath:     destination + ".migrating",
	}
	defer func() {
		if err == nil {
			return
		}
		result.state.Error = err.Error()
		result.state.Action = migrationAction(err)
		if errors.Is(err, errStorageMigrationBusy) {
			result.state.Status = "busy"
			result.state.Title = "Migracja działa w drugim oknie"
			result.state.Detail = "Druga kopia nie użyje katalogu etapowego ani nie uruchomi równoległej migracji."
		} else {
			result.state.Status = "fallback"
			result.state.Title = "Bezpieczny powrót do C:"
			result.state.Detail = "Publikacja na D: została zatrzymana; katalog etapowy nie jest używany jako biblioteka."
		}
		if _, sourceErr := os.Stat(source); sourceErr == nil {
			result.state.SourceRetained = true
			result.state.RetainedPath = source
		}
	}()
	if source == "" || destination == "" {
		return result, errors.New("brak ścieżki migracji")
	}
	if sameCleanPath(source, destination) {
		if err = os.MkdirAll(destination, 0700); err != nil {
			return result, err
		}
		result.state.Status = "custom"
		result.state.Title = "Magazyn lokalny jest gotowy"
		result.state.Detail = "Źródło i cel wskazują ten sam katalog; migracja nie jest potrzebna."
		return result, nil
	}
	if pathInside(destination, source) || pathInside(source, destination) {
		return result, errors.New("źródłowy i docelowy katalog danych nie mogą być w sobie zagnieżdżone")
	}
	lock, recoveredLock, lockErr := acquireStorageMigrationLock(source, destination)
	if lockErr != nil {
		return result, lockErr
	}
	result.state.RecoveredLock = recoveredLock
	defer lock.release()

	sourceInfo, sourceErr := os.Stat(source)
	sourceExists := sourceErr == nil
	if sourceErr != nil && !errors.Is(sourceErr, os.ErrNotExist) {
		return result, sourceErr
	}
	if sourceExists && !sourceInfo.IsDir() {
		return result, errors.New("dotychczasowa ścieżka danych nie jest katalogiem")
	}

	markerValid, markerStructured, markerErr := storageMarkerStatus(destination)
	if markerErr != nil {
		return result, markerErr
	}
	if markerValid {
		result.state.Status = "ready"
		result.state.Title = "Magazyn D: jest aktywny"
		result.state.Detail = "Znacznik opublikowanej biblioteki jest prawidłowy; katalog Data.migrating nie jest używany."
		if sourceExists {
			if !markerStructured {
				verified, verifyErr := verifyStorageTreeDetailed(source, destination, false)
				if verifyErr != nil {
					return result, fmt.Errorf("istniejąca biblioteka docelowa nie odpowiada źródłu; stary katalog pozostaje nietknięty: %w", verifyErr)
				}
				result.state.VerifiedFiles = verified.Files
				result.state.VerifiedBytes = verified.Bytes
				if err = writeStorageMarker(destination, source, verified); err != nil {
					return result, fmt.Errorf("aktualizacja znacznika zweryfikowanej biblioteki: %w", err)
				}
			}
			result.migrated = true
			retained, warning := retireStorageSource(source)
			result.warning = warning
			if retained != "" {
				result.state.Status = "retained"
				result.state.Title = "D: działa; kopia C: została zachowana"
				result.state.Detail = "Biblioteka D: jest opublikowana i aktywna. Windows nie usunął jeszcze starej, nieaktywnej kopii."
				result.state.SourceRetained = true
				result.state.RetainedPath = retained
				result.state.Action = "Po zamknięciu programów korzystających ze starego katalogu uruchom YourLM ponownie. Nie usuwaj aktywnego D:\\YourLM\\Data."
			}
		}
		return result, nil
	}

	if destinationInfo, destinationErr := os.Lstat(destination); destinationErr == nil {
		if destinationInfo.Mode()&os.ModeSymlink != 0 || !destinationInfo.IsDir() {
			return result, errors.New("docelowa ścieżka danych nie jest bezpiecznym katalogiem")
		}
		hasEntries, readErr := directoryHasEntries(destination)
		if readErr != nil {
			return result, readErr
		}
		if hasEntries {
			return result, errors.New("docelowy katalog istnieje bez prawidłowego znacznika YourLM; nie zostanie nadpisany")
		}
	} else if !errors.Is(destinationErr, os.ErrNotExist) {
		return result, destinationErr
	}

	stagingHasEntries, stagingErr := directoryHasEntries(result.state.StagingPath)
	if stagingErr != nil {
		return result, stagingErr
	}
	if !sourceExists {
		if stagingHasEntries {
			return result, errors.New("brak źródła, ale istnieje nieopublikowany katalog Data.migrating; nie zostanie uznany za bibliotekę bez weryfikacji")
		}
		if err = os.MkdirAll(destination, 0700); err != nil {
			return result, err
		}
		emptyHash := sha256.Sum256(nil)
		verified := storageVerification{TreeSHA256: hex.EncodeToString(emptyHash[:])}
		if err = writeStorageMarker(destination, source, verified); err != nil {
			return result, err
		}
		result.state.Status = "ready"
		result.state.Title = "Magazyn D: jest gotowy"
		result.state.Detail = "Nie było wcześniejszej biblioteki do przeniesienia; utworzono pusty, zarządzany magazyn."
		return result, nil
	}

	result.state.Status = "copying"
	result.state.Title = "Wznawianie migracji na D:"
	result.state.Detail = "Istniejące pliki i części są sprawdzane przed kopiowaniem; C: pozostaje źródłem aż do publikacji."
	if err = os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return result, err
	}
	if err = copyStorageTree(source, result.state.StagingPath, &result.state); err != nil {
		return result, fmt.Errorf("kopiowanie danych na większy dysk: %w", err)
	}
	verified, verifyErr := verifyStorageTreeDetailed(source, result.state.StagingPath, true)
	if verifyErr != nil {
		return result, fmt.Errorf("końcowa weryfikacja katalogu etapowego: %w", verifyErr)
	}
	result.state.VerifiedFiles = verified.Files
	result.state.VerifiedBytes = verified.Bytes
	if err = writeStorageMarker(result.state.StagingPath, source, verified); err != nil {
		return result, fmt.Errorf("zapis znacznika po weryfikacji: %w", err)
	}
	if err = os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return result, err
	}
	if err = os.Rename(result.state.StagingPath, destination); err != nil {
		return result, fmt.Errorf("publikacja przeniesionej biblioteki: %w", err)
	}
	if err = syncStorageDirectory(filepath.Dir(destination)); err != nil {
		return result, fmt.Errorf("synchronizacja publikacji biblioteki: %w", err)
	}
	result.migrated = true
	recovered := result.state.RecoveredLock || result.state.ResumedFiles > 0 || result.state.ReusedFiles > 0 || result.state.RecoveredParts > 0 || result.state.ReplacedFiles > 0 || result.state.RemovedEntries > 0
	if recovered {
		result.state.Status = "recovered"
		result.state.Title = "Migracja odzyskana i zweryfikowana"
		result.state.Detail = "RC17 bezpiecznie wykorzystał prawidłowe dane etapowe, porównał całość SHA-256 i dopiero wtedy aktywował D:."
	} else {
		result.state.Status = "completed"
		result.state.Title = "Migracja zakończona i zweryfikowana"
		result.state.Detail = "Wszystkie pliki zsynchronizowano i porównano SHA-256 przed atomową publikacją D:."
	}
	retained, warning := retireStorageSource(source)
	result.warning = warning
	if retained != "" {
		result.state.Status = "retained"
		result.state.Title = "D: działa; kopia C: została zachowana"
		result.state.Detail = "Zweryfikowana biblioteka na D: jest aktywna, ale Windows nie usunął jeszcze nieaktywnej kopii źródłowej."
		result.state.SourceRetained = true
		result.state.RetainedPath = retained
		result.state.Action = "Zamknij programy korzystające ze starego katalogu i uruchom YourLM ponownie. Danych z D: nie usuwaj."
	}
	return result, nil
}

// migrateDataDirectory preserves the original test-facing API while the
// Windows storage state consumes the detailed recovery result above.
func migrateDataDirectory(source, destination string) (migrated bool, warning string, err error) {
	result, err := migrateDataDirectoryDetailed(source, destination)
	return result.migrated, result.warning, err
}
