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

func handleUpdateHelper(args []string) (bool, error) {
	target, _, active := helperArguments(args)
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
