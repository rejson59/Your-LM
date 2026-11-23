//go:build !windows

package main

func ensureDesktopInstall([]string) (bool, error) { return false, nil }
