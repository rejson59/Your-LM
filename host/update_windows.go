//go:build windows

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

func copyExecutable(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0700)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func helperArguments(args []string) (string, int, bool) {
	var target string
	pid := 0
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--apply-update":
			if i+1 < len(args) {
				target = args[i+1]
				i++
			}
		case "--wait-pid":
			if i+1 < len(args) {
				pid, _ = strconv.Atoi(args[i+1])
				i++
			}
		}
	}
	return target, pid, target != ""
}

func waitForUpdateParent(pid int, timeout time.Duration) error {
	if pid <= 0 || uint64(pid) > uint64(^uint32(0)) {
		return errors.New("brak prawidłowego PID poprzedniej wersji")
	}
	if pid == os.Getpid() {
		return errors.New("helper aktualizacji nie może czekać na własny proces")
	}
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		// The parent can finish between launching this helper and OpenProcess.
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return nil
		}
		return fmt.Errorf("otwarcie poprzedniego procesu: %w", err)
	}
	defer windows.CloseHandle(handle)
	milliseconds := timeout.Milliseconds()
	if milliseconds <= 0 {
		milliseconds = 1
	}
	if milliseconds > int64(^uint32(0)-1) {
		milliseconds = int64(^uint32(0) - 1)
	}
	result, err := windows.WaitForSingleObject(handle, uint32(milliseconds))
	if err != nil {
		return fmt.Errorf("oczekiwanie na poprzednią wersję: %w", err)
	}
	if result == uint32(windows.WAIT_TIMEOUT) {
		return fmt.Errorf("poprzednia wersja nie zakończyła pracy w ciągu %s", timeout.Round(time.Millisecond))
	}
	if result != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("oczekiwanie na poprzednią wersję zwróciło kod %d", result)
	}
	return nil
}

func handleUpdateHelper(args []string) (bool, error) {
	target, pid, active := helperArguments(args)
	if !active {
		return false, nil
	}
	absolute, err := filepath.Abs(target)
	if err != nil || !strings.EqualFold(filepath.Ext(absolute), ".exe") {
		return true, errors.New("nieprawidłowa ścieżka aktualizowanego EXE")
	}
	self, err := os.Executable()
	if err != nil {
		return true, err
	}
	// The old process owns the storage instance lock until Windows has fully
	// terminated it. Waiting here (rather than merely retrying the EXE rename)
	// prevents the freshly installed process from racing into the migration
	// lock dialog during a normal in-app update.
	if err := waitForUpdateParent(pid, 30*time.Second); err != nil {
		return true, err
	}
	next := absolute + ".next"
	old := absolute + ".old"
	_ = os.Remove(next)
	if err := copyExecutable(self, next); err != nil {
		return true, err
	}
	_ = os.Remove(old)
	var moved bool
	for i := 0; i < 120; i++ {
		if err := os.Rename(absolute, old); err == nil {
			moved = true
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if !moved {
		_ = os.Remove(next)
		return true, errors.New("stara wersja nie zakończyła pracy")
	}
	if err := os.Rename(next, absolute); err != nil {
		_ = os.Rename(old, absolute)
		return true, err
	}
	command := exec.Command(absolute)
	command.Dir = filepath.Dir(absolute)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: false}
	if err := command.Start(); err != nil {
		return true, fmt.Errorf("nowa wersja została zainstalowana, ale nie uruchomiła się: %w", err)
	}
	return true, nil
}

func launchDesktopUpdate(downloaded string) error {
	info, err := os.Stat(downloaded)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("pobrany plik aktualizacji zniknął")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	command := exec.Command(downloaded, "--apply-update", self, "--wait-pid", strconv.Itoa(os.Getpid()))
	command.Dir = filepath.Dir(downloaded)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return command.Start()
}

func cleanupDesktopUpdate() {
	self, err := os.Executable()
	if err != nil {
		return
	}
	_ = os.Remove(self + ".old")
	_ = os.Remove(self + ".next")
}
