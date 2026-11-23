//go:build !windows

package main

import "errors"

func chooseModel() (string, error) { return "", errors.New("Wpisz ścieżkę do GGUF") }
func runDesktopUI(string, string) error {
	return errors.New("natywne okno jest dostępne w systemie Windows")
}
func openURL(string) error { return nil }
