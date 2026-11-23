//go:build windows

package main

import (
	"syscall"
	"unsafe"
)

var (
	user32StorageNotice = syscall.NewLazyDLL("user32.dll")
	messageBoxWStorage  = user32StorageNotice.NewProc("MessageBoxW")
)

func showStorageStartupError(message string) {
	text, textErr := syscall.UTF16PtrFromString(message)
	title, titleErr := syscall.UTF16PtrFromString("YourLM — bezpieczna migracja magazynu")
	if textErr != nil || titleErr != nil {
		return
	}
	const mbOKIconWarningForeground = 0x00000000 | 0x00000030 | 0x00010000
	_, _, _ = messageBoxWStorage.Call(
		0,
		uintptr(unsafe.Pointer(text)),
		uintptr(unsafe.Pointer(title)),
		mbOKIconWarningForeground,
	)
}
