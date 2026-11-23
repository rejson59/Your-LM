//go:build !windows

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func prepareStorage() (string, StorageState, error) {
	root := strings.TrimSpace(os.Getenv("YOURLM_HOME"))
	if root == "" {
		config, err := os.UserConfigDir()
		if err != nil {
			return "", StorageState{}, err
		}
		root = filepath.Join(config, "YourLM")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		return "", StorageState{}, err
	}
	message := "Używam lokalnego katalogu danych YourLM wskazanego dla tego systemu."
	if runtime.GOOS == "android" {
		message = "Android przechowuje modele i cache w prywatnych danych aplikacji."
	}
	state := StorageState{
		Supported: runtime.GOOS == "android",
		DataPath:  root,
		Message:   message,
		Migration: StorageMigrationState{
			Status:          "local",
			Title:           "Prywatny magazyn lokalny",
			Detail:          message,
			DestinationPath: root,
		},
	}
	state.InstallPath, _ = os.Executable()
	return root, state, nil
}
