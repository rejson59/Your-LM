//go:build windows

package main

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func tryLockStorageFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		&overlapped,
	)
}

func unlockStorageFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &overlapped)
}

func storageLockIsBusy(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_SHARING_VIOLATION)
}

// FlushFileBuffers has already persisted every file and the marker. Windows
// does not expose a generally usable directory fsync through os.File, while
// Rename is atomic on the fixed local NTFS volume used by managed storage.
func syncStorageDirectory(string) error { return nil }
