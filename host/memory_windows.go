package main

import (
	"syscall"
	"unsafe"
)

func memory() (total, free uint64) {
	var s struct {
		Length, Load                                                  uint32
		Total, Free, Page, AvailPage, Virtual, AvailVirtual, Extended uint64
	}
	s.Length = uint32(unsafe.Sizeof(s))
	r, _, _ := syscall.NewLazyDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx").Call(uintptr(unsafe.Pointer(&s)))
	if r == 0 {
		return 0, 0
	}
	return s.Total, s.Free
}
