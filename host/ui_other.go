//go:build !windows

package main

import "errors"

func chooseModel() (string, error) { return "", errors.New("Wpisz ścieżkę do GGUF") }
func openURL(string) error         { return nil }
