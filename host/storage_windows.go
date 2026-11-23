//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	storageRegistryPath = `Software\YourLM`
	storageRootValue    = "StorageRoot"
	minimumDataDrive    = 128 * GiB
	minimumDataFree     = 64 * GiB
)

var (
	kernel32Storage = syscall.NewLazyDLL("kernel32.dll")
	getDriveTypeW   = kernel32Storage.NewProc("GetDriveTypeW")
	getVolumeInfoW  = kernel32Storage.NewProc("GetVolumeInformationW")
)

func windowsDriveType(root string) uint32 {
	value, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return 0
	}
	result, _, _ := getDriveTypeW.Call(uintptr(unsafe.Pointer(value)))
	return uint32(result)
}

func windowsVolumeInfo(root string) (label, fileSystem string) {
	path, err := syscall.UTF16PtrFromString(root)
	if err != nil {
		return "", ""
	}
	labelBuffer := make([]uint16, 261)
	fileSystemBuffer := make([]uint16, 261)
	result, _, _ := getVolumeInfoW.Call(
		uintptr(unsafe.Pointer(path)),
		uintptr(unsafe.Pointer(&labelBuffer[0])),
		uintptr(len(labelBuffer)),
		0,
		0,
		0,
		uintptr(unsafe.Pointer(&fileSystemBuffer[0])),
		uintptr(len(fileSystemBuffer)),
	)
	if result == 0 {
		return "", ""
	}
	return syscall.UTF16ToString(labelBuffer), syscall.UTF16ToString(fileSystemBuffer)
}

func windowsDiskSpace(root string) (total, free uint64, err error) {
	path, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return 0, 0, err
	}
	if err := windows.GetDiskFreeSpaceEx(path, &free, &total, nil); err != nil {
		return 0, 0, err
	}
	return total, free, nil
}

func configuredWindowsStorageRoot() string {
	key, err := registry.OpenKey(registry.CURRENT_USER, storageRegistryPath, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	value, _, err := key.GetStringValue(storageRootValue)
	if err != nil || !filepath.IsAbs(value) {
		return ""
	}
	return filepath.Clean(value)
}

func saveWindowsStorageRoot(root string) error {
	key, _, err := registry.CreateKey(registry.CURRENT_USER, storageRegistryPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue(storageRootValue, filepath.Clean(root))
}

func probeWindowsStorageRoot(root string, enforceCapacity bool) bool {
	if root == "" || !filepath.IsAbs(root) {
		return false
	}
	volume := filepath.VolumeName(root)
	if volume == "" {
		return false
	}
	driveRoot := volume + `\`
	if windowsDriveType(driveRoot) != 3 { // DRIVE_FIXED
		return false
	}
	total, free, err := windowsDiskSpace(driveRoot)
	if err != nil || enforceCapacity && (total < minimumDataDrive || free < minimumDataFree) {
		return false
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return false
	}
	probe := filepath.Join(root, fmt.Sprintf(".yourlm-write-test-%d", os.Getpid()))
	if err := os.WriteFile(probe, []byte("YourLM"), 0600); err != nil {
		return false
	}
	_ = os.Remove(probe)
	return true
}

// preferredWindowsStorageRoot is intentionally deterministic for this build:
// an already selected root wins; otherwise a large writable fixed D: drive is
// selected. The user's 355 GB D: volume satisfies this, while small CI scratch
// drives do not. YOURLM_WINDOWS_ROOT exists only for controlled deployments and
// tests and still has to point at a writable fixed drive.
func preferredWindowsStorageRoot() string {
	if os.Getenv("YOURLM_DISABLE_DRIVE_MIGRATION") == "1" {
		return ""
	}
	if overridden := strings.TrimSpace(os.Getenv("YOURLM_WINDOWS_ROOT")); overridden != "" {
		if probeWindowsStorageRoot(overridden, false) {
			return filepath.Clean(overridden)
		}
		return ""
	}
	if configured := configuredWindowsStorageRoot(); configured != "" && probeWindowsStorageRoot(configured, false) {
		return configured
	}
	systemDrive := strings.TrimSuffix(strings.TrimSpace(os.Getenv("SystemDrive")), `\`)
	if systemDrive == "" {
		systemDrive = "C:"
	}
	candidateVolume := "D:"
	if strings.EqualFold(candidateVolume, systemDrive) {
		return ""
	}
	candidate := candidateVolume + `\YourLM`
	if probeWindowsStorageRoot(candidate, true) {
		return candidate
	}
	return ""
}

func windowsDriveKind(value uint32) string {
	switch value {
	case 2:
		return "wymienny"
	case 3:
		return "stały"
	case 4:
		return "sieciowy"
	case 5:
		return "optyczny"
	case 6:
		return "RAM-dysk"
	default:
		return "nieznany"
	}
}

func windowsStorageDrives(dataPath, preferredRoot string) []StorageDrive {
	systemDrive := strings.TrimSuffix(strings.TrimSpace(os.Getenv("SystemDrive")), `\`)
	selectedVolume := filepath.VolumeName(dataPath)
	preferredVolume := filepath.VolumeName(preferredRoot)
	var drives []StorageDrive
	for letter := 'C'; letter <= 'Z'; letter++ {
		volume := fmt.Sprintf("%c:", letter)
		root := volume + `\`
		driveType := windowsDriveType(root)
		if driveType != 2 && driveType != 3 {
			continue
		}
		total, free, err := windowsDiskSpace(root)
		if err != nil || total == 0 {
			continue
		}
		label, fileSystem := windowsVolumeInfo(root)
		drives = append(drives, StorageDrive{
			Letter:      volume,
			Path:        root,
			Label:       label,
			FileSystem:  fileSystem,
			Type:        windowsDriveKind(driveType),
			Total:       total,
			Free:        free,
			Selected:    strings.EqualFold(volume, selectedVolume),
			System:      strings.EqualFold(volume, systemDrive),
			Recommended: preferredVolume != "" && strings.EqualFold(volume, preferredVolume),
		})
	}
	return drives
}

func prepareStorage() (string, StorageState, error) {
	if explicit := strings.TrimSpace(os.Getenv("YOURLM_HOME")); explicit != "" {
		if !filepath.IsAbs(explicit) {
			return "", StorageState{}, errorsNewStorage("YOURLM_HOME musi być ścieżką bezwzględną")
		}
		if err := os.MkdirAll(explicit, 0700); err != nil {
			return "", StorageState{}, err
		}
		state := StorageState{Supported: true, DataPath: filepath.Clean(explicit), Message: "Używam jawnie wskazanego katalogu YOURLM_HOME."}
		state.Migration = StorageMigrationState{
			Status:          "custom",
			Title:           "Jawnie wskazany magazyn",
			Detail:          "YOURLM_HOME wyłącza automatyczny wybór D: dla tego uruchomienia.",
			DestinationPath: state.DataPath,
		}
		state.InstallPath, _ = os.Executable()
		state.Drives = windowsStorageDrives(state.DataPath, "")
		return state.DataPath, state, nil
	}
	roaming := os.Getenv("APPDATA")
	if roaming == "" {
		var err error
		roaming, err = os.UserConfigDir()
		if err != nil {
			return "", StorageState{}, err
		}
	}
	legacy := filepath.Join(roaming, "YourLM")
	root := preferredWindowsStorageRoot()
	state := StorageState{Supported: true, PreferredRoot: root, Automatic: root != ""}
	state.InstallPath, _ = os.Executable()
	if root == "" {
		if err := os.MkdirAll(legacy, 0700); err != nil {
			return "", state, err
		}
		state.DataPath = legacy
		state.Message = "Dysk D: nie spełnia bezpiecznych warunków migracji; dane pozostają w profilu Windows."
		state.Migration = StorageMigrationState{
			Status:         "unavailable",
			Title:          "D: nie spełnia warunków bezpieczeństwa",
			Detail:         "YourLM używa profilu Windows i nie rozpoczyna kopiowania na dysk o niepewnych parametrach.",
			Action:         "Podłącz lub napraw stały, lokalny dysk D: z co najmniej 64 GiB wolnego miejsca, a następnie uruchom YourLM ponownie.",
			SourcePath:     legacy,
			SourceRetained: true,
			RetainedPath:   legacy,
		}
		state.Drives = windowsStorageDrives(state.DataPath, "")
		return legacy, state, nil
	}
	destination := filepath.Join(root, "Data")
	migration, err := migrateDataDirectoryDetailed(legacy, destination)
	state.Migration = migration.state
	if err != nil {
		// A second RC17 process must never fall back to the live C: source while
		// the first process is taking its verified snapshot. Abort that second
		// startup instead; the first window remains responsible for migration.
		if errors.Is(err, errStorageMigrationBusy) {
			return "", state, fmt.Errorf("%s: %w", state.Migration.Action, err)
		}
		if mkdirErr := os.MkdirAll(legacy, 0700); mkdirErr != nil {
			return "", state, fmt.Errorf("migracja na D: %v; powrót do danych C: %w", err, mkdirErr)
		}
		state.DataPath = legacy
		state.Warning = "Migracja na D: została bezpiecznie zatrzymana. YourLM nadal używa oryginalnej biblioteki na C:; katalog Data.migrating nie jest aktywny."
		state.Drives = windowsStorageDrives(state.DataPath, root)
		return legacy, state, nil
	}
	warning := migration.warning
	if err := saveWindowsStorageRoot(root); err != nil {
		warning = strings.TrimSpace(warning + " Nie udało się zapisać wyboru dysku w profilu Windows: " + err.Error())
	}
	state.DataPath = destination
	state.Migrated = migration.migrated
	state.MigrationSource = legacy
	state.Warning = warning
	if migration.migrated {
		state.Message = "YourLM, modele, komponenty, cache, logi, diagnostyka i aktualizacje działają ze zweryfikowanej biblioteki na D:."
	} else {
		state.Message = "YourLM zapisuje modele, komponenty, cache, logi, diagnostykę i aktualizacje na dysku D:."
	}
	state.Drives = windowsStorageDrives(state.DataPath, root)
	return destination, state, nil
}

func errorsNewStorage(message string) error { return fmt.Errorf("%s", message) }
