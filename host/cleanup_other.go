//go:build !windows

package main

import "errors"

func cleanupPlatformSupported() bool {
	return false
}

func scanCleanupApps() ([]CleanupApp, error) {
	return nil, errors.New("lista aplikacji jest dostępna tylko na Windows")
}

func requestCloseCleanupApps([]CleanupApp) (map[int]cleanupCloseRequest, error) {
	return nil, errors.New("zamykanie aplikacji jest dostępne tylko na Windows")
}
