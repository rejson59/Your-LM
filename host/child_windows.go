package main

import (
	"log"
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

var once sync.Once
var job uintptr

func prepareChild(cmd *exec.Cmd) {}
func trackChild(c *exec.Cmd) {
	k := syscall.NewLazyDLL("kernel32.dll")
	once.Do(func() {
		job, _, _ = k.NewProc("CreateJobObjectW").Call(0, 0)
		var limits [144]byte
		*(*uint32)(unsafe.Pointer(&limits[16])) = 0x2000
		ok, _, _ := k.NewProc("SetInformationJobObject").Call(job, 9, uintptr(unsafe.Pointer(&limits[0])), uintptr(len(limits)))
		if ok == 0 {
			log.Print("Nie udało się ustawić zamykania procesów sesji")
		}
	})
	h, e := syscall.OpenProcess(0x101|0x0001, false, uint32(c.Process.Pid))
	if e == nil {
		defer syscall.CloseHandle(h)
		k.NewProc("AssignProcessToJobObject").Call(job, uintptr(h))
	}
}
