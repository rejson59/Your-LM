package main

import (
	"syscall"
	"unsafe"
)

var (
	kernel32Memory = syscall.NewLazyDLL("kernel32.dll")
	globalMemory   = kernel32Memory.NewProc("GlobalMemoryStatusEx")
	openProcess    = kernel32Memory.NewProc("OpenProcess")
	closeHandle    = kernel32Memory.NewProc("CloseHandle")
	processMemory  = kernel32Memory.NewProc("K32GetProcessMemoryInfo")
)

func systemMemory() MemorySnapshot {
	var status struct {
		Length, Load                                                  uint32
		Total, Free, Page, AvailPage, Virtual, AvailVirtual, Extended uint64
	}
	status.Length = uint32(unsafe.Sizeof(status))
	result, _, _ := globalMemory.Call(uintptr(unsafe.Pointer(&status)))
	if result == 0 {
		return MemorySnapshot{}
	}
	pageFile := uint64(0)
	if status.Page > status.Total {
		// GlobalMemoryStatusEx reports the commit limit (physical RAM plus
		// paging files) as TotalPageFile. Show the paging-file contribution
		// separately rather than mislabelling the whole commit pool.
		pageFile = status.Page - status.Total
	}
	return MemorySnapshot{
		Total:           status.Total,
		Available:       status.Free,
		Used:            status.Total - status.Free,
		CommitLimit:     status.Page,
		CommitAvailable: status.AvailPage,
		PageFile:        pageFile,
	}
}

func memoryForProcess(pid int) ProcessMemory {
	if pid <= 0 {
		return ProcessMemory{}
	}
	const (
		processVMRead                  = 0x0010
		processQueryLimitedInformation = 0x1000
	)
	handle, _, _ := openProcess.Call(processVMRead|processQueryLimitedInformation, 0, uintptr(uint32(pid)))
	if handle == 0 {
		return ProcessMemory{}
	}
	defer closeHandle.Call(handle)
	var counters struct {
		Size                       uint32
		PageFaultCount             uint32
		PeakWorkingSetSize         uintptr
		WorkingSetSize             uintptr
		QuotaPeakPagedPoolUsage    uintptr
		QuotaPagedPoolUsage        uintptr
		QuotaPeakNonPagedPoolUsage uintptr
		QuotaNonPagedPoolUsage     uintptr
		PagefileUsage              uintptr
		PeakPagefileUsage          uintptr
		PrivateUsage               uintptr
	}
	counters.Size = uint32(unsafe.Sizeof(counters))
	result, _, _ := processMemory.Call(
		handle,
		uintptr(unsafe.Pointer(&counters)),
		uintptr(counters.Size),
	)
	if result == 0 {
		return ProcessMemory{}
	}
	return ProcessMemory{
		Resident:  uint64(counters.WorkingSetSize),
		Committed: uint64(counters.PrivateUsage),
	}
}
