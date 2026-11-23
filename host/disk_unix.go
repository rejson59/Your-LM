//go:build !windows

package main

import "syscall"

func availableDiskBytes(directory string) (uint64, error) {
	var statistics syscall.Statfs_t
	if err := syscall.Statfs(directory, &statistics); err != nil {
		return 0, err
	}
	return uint64(statistics.Bavail) * uint64(statistics.Bsize), nil
}
