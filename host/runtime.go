package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
)

// The single Windows executable expands its pinned runtime into the user's
// private application-data directory. Before every launch the controller checks
// the selected program against a digest compiled into the outer executable.
const runtimeDir = "runtime"

// Set with Go linker -X by the reproducible Windows package build. A plain
// source build safely refuses to launch unpinned sidecar programs.
var serverHash string
var rpcHash string
var buildVersion = "development"
var buildCommit = "unknown"

func executable(root string) string {
	name := "llama-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var found string
	filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e == nil && !d.IsDir() && d.Name() == name {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

func (a *App) runtimePath(worker bool) string {
	if runtime.GOOS != "windows" {
		if worker {
			return filepath.Join(filepath.Dir(a.binary()), "ggml-rpc-server")
		}
		return a.binary()
	}
	name := "llama-server.exe"
	if worker {
		name = "ggml-rpc-server.exe"
	}
	if a.runtimeRoot == "" {
		return ""
	}
	return filepath.Join(a.runtimeRoot, name)
}

func verifyProgram(path, want string) error {
	if path == "" || len(want) != sha256.Size*2 {
		return errors.New("Pakiet nie zawiera kompletnego, zweryfikowanego silnika")
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("Brak zwykłego pliku silnika: %s", filepath.Base(path))
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, copyErr := io.Copy(h, f)
	closeErr := f.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if fmt.Sprintf("%x", h.Sum(nil)) != want {
		return fmt.Errorf("Plik %s został zmieniony — uruchom YourLM ponownie, aby odtworzyć silnik", filepath.Base(path))
	}
	return nil
}

func (a *App) launchModelPath(plan RuntimePlan) (string, error) {
	if plan.UseSmol {
		if runtime.GOOS != "android" || !regularExecutable(a.smolEngine) {
			return "", errors.New("Eksperymentalny runtime smolmaxxing jest niedostępny; uruchom ponownie aplikację albo użyj silnika standardowego")
		}
		return a.smolEngine, nil
	}
	return a.launchPath(false)
}

func (a *App) launchPath(worker bool) (string, error) {
	path := a.runtimePath(worker)
	if runtime.GOOS != "windows" {
		if path == "" {
			return "", errors.New("Najpierw zainstaluj silnik")
		}
		return path, nil
	}
	want := serverHash
	if worker {
		want = rpcHash
	}
	if err := verifyProgram(path, want); err != nil {
		return "", err
	}
	return path, nil
}

func (a *App) hasRuntime() bool {
	if runtime.GOOS != "windows" {
		return a.binary() != ""
	}
	for _, worker := range []bool{false, true} {
		info, err := os.Lstat(a.runtimePath(worker))
		if err != nil || !info.Mode().IsRegular() {
			return false
		}
	}
	return true
}
