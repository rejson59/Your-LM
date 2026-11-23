package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const storageMarker = ".yourlm-storage-v1"

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

type StorageState struct {
	Supported       bool           `json:"supported"`
	DataPath        string         `json:"dataPath"`
	InstallPath     string         `json:"installPath,omitempty"`
	PreferredRoot   string         `json:"preferredRoot,omitempty"`
	Automatic       bool           `json:"automatic"`
	Migrated        bool           `json:"migrated"`
	MigrationSource string         `json:"migrationSource,omitempty"`
	Message         string         `json:"message,omitempty"`
	Warning         string         `json:"warning,omitempty"`
	Drives          []StorageDrive `json:"drives,omitempty"`
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

func copyStorageFile(source, destination string, mode os.FileMode, modified time.Time) error {
	sourceInfo, err := os.Stat(source)
	if err != nil {
		return err
	}
	if current, statErr := os.Stat(destination); statErr == nil {
		if !current.Mode().IsRegular() || current.Size() != sourceInfo.Size() {
			return fmt.Errorf("docelowy plik już istnieje i różni się rozmiarem: %s", destination)
		}
		left, leftErr := fileSHA256(source)
		right, rightErr := fileSHA256(destination)
		if leftErr != nil || rightErr != nil || left != right {
			return fmt.Errorf("docelowy plik już istnieje i ma inną zawartość: %s", destination)
		}
		return nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return err
	}
	part := destination + ".migrating-part"
	_ = os.Remove(part)
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	output, err := os.OpenFile(part, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode.Perm())
	if err != nil {
		input.Close()
		return err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(output, hash), input)
	inputErr := input.Close()
	syncErr := output.Sync()
	outputErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(part)
		return copyErr
	}
	if inputErr != nil {
		_ = os.Remove(part)
		return inputErr
	}
	if syncErr != nil {
		_ = os.Remove(part)
		return syncErr
	}
	if outputErr != nil {
		_ = os.Remove(part)
		return outputErr
	}
	if written != sourceInfo.Size() {
		_ = os.Remove(part)
		return fmt.Errorf("niepełna kopia %s: %d z %d bajtów", source, written, sourceInfo.Size())
	}
	var sourceHash [sha256.Size]byte
	copy(sourceHash[:], hash.Sum(nil))
	copiedHash, err := fileSHA256(part)
	if err != nil || copiedHash != sourceHash {
		_ = os.Remove(part)
		if err != nil {
			return err
		}
		return fmt.Errorf("weryfikacja SHA-256 kopii nie powiodła się: %s", source)
	}
	_ = os.Chtimes(part, modified, modified)
	if err := os.Rename(part, destination); err != nil {
		_ = os.Remove(part)
		return err
	}
	return nil
}

func copyStorageTree(source, destination string) error {
	return filepath.Walk(source, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return os.MkdirAll(destination, 0700)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() && !info.IsDir() {
			return fmt.Errorf("migracja odrzuciła nieobsługiwany wpis: %s", path)
		}
		target := filepath.Join(destination, relative)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode().Perm())
		}
		return copyStorageFile(path, target, info.Mode(), info.ModTime())
	})
}

func verifyStorageTree(source, destination string) error {
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
		if !targetInfo.Mode().IsRegular() || targetInfo.Size() != info.Size() {
			return fmt.Errorf("docelowy plik różni się rozmiarem: %s", relative)
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
		return nil
	})
}

// migrateDataDirectory performs a verified, restartable cross-volume copy. The
// source is removed only after every file has been copied, fsynced and compared
// by SHA-256. A failed migration therefore leaves the original usable.
func migrateDataDirectory(source, destination string) (migrated bool, warning string, err error) {
	if source == "" || destination == "" {
		return false, "", errors.New("brak ścieżki migracji")
	}
	if sameCleanPath(source, destination) {
		if err := os.MkdirAll(destination, 0700); err != nil {
			return false, "", err
		}
		return false, "", nil
	}
	if pathInside(destination, source) || pathInside(source, destination) {
		return false, "", errors.New("źródłowy i docelowy katalog danych nie mogą być w sobie zagnieżdżone")
	}
	sourceInfo, sourceErr := os.Stat(source)
	if errors.Is(sourceErr, os.ErrNotExist) {
		if err := os.MkdirAll(destination, 0700); err != nil {
			return false, "", err
		}
		if err := os.WriteFile(filepath.Join(destination, storageMarker), []byte("YourLM storage v1\n"), 0600); err != nil {
			return false, "", err
		}
		return false, "", nil
	}
	if sourceErr != nil {
		return false, "", sourceErr
	}
	if !sourceInfo.IsDir() {
		return false, "", errors.New("dotychczasowa ścieżka danych nie jest katalogiem")
	}
	if marker, markerErr := os.Stat(filepath.Join(destination, storageMarker)); markerErr == nil && marker.Mode().IsRegular() {
		if verifyErr := verifyStorageTree(source, destination); verifyErr != nil {
			return false, "", fmt.Errorf("istniejąca biblioteka docelowa nie odpowiada źródłu; stary katalog pozostaje nietknięty: %w", verifyErr)
		}
		if removeErr := os.RemoveAll(source); removeErr != nil {
			return true, "Nowa biblioteka jest gotowa, ale Windows nie usunął jeszcze starej kopii z dysku systemowego: " + removeErr.Error(), nil
		}
		return true, "", nil
	}
	if destinationInfo, destinationErr := os.Stat(destination); destinationErr == nil {
		if !destinationInfo.IsDir() {
			return false, "", errors.New("docelowa ścieżka danych nie jest katalogiem")
		}
		entries, readErr := os.ReadDir(destination)
		if readErr != nil {
			return false, "", readErr
		}
		if len(entries) > 0 {
			return false, "", errors.New("docelowy katalog istnieje bez znacznika YourLM; nie zostanie nadpisany")
		}
	}
	staging := destination + ".migrating"
	if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
		return false, "", err
	}
	if err := copyStorageTree(source, staging); err != nil {
		return false, "", fmt.Errorf("kopiowanie danych na większy dysk: %w", err)
	}
	if err := os.WriteFile(filepath.Join(staging, storageMarker), []byte("YourLM storage v1\n"), 0600); err != nil {
		return false, "", err
	}
	if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, "", err
	}
	if err := os.Rename(staging, destination); err != nil {
		return false, "", fmt.Errorf("publikacja przeniesionej biblioteki: %w", err)
	}
	if err := os.RemoveAll(source); err != nil {
		return true, "Dane są już używane z większego dysku, ale stara kopia nie została jeszcze usunięta: " + err.Error(), nil
	}
	return true, "", nil
}
