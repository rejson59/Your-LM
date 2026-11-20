//go:build !windows

package main

import "errors"

func handleUpdateHelper([]string) (bool, error) { return false, nil }
func cleanupDesktopUpdate()                     {}
func launchDesktopUpdate(string) error {
	return errors.New("Aktualizacja EXE jest dostępna tylko w Windows")
}
