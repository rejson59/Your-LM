package main

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

type openFilename struct {
	structSize, _                uint32
	owner, instance              uintptr
	filter, customFilter         *uint16
	maxCustomFilter, filterIndex uint32
	file                         *uint16
	maxFile                      uint32
	fileTitle                    *uint16
	maxFileTitle                 uint32
	initialDir, title            *uint16
	flags                        uint32
	fileOffset, fileExtension    uint16
	defaultExtension             *uint16
	customData, hook             uintptr
	templateName                 *uint16
	reserved                     uintptr
	reserved32, flagsEx          uint32
}

func chooseModel() (string, error) {
	file := make([]uint16, 32768)
	filter := []uint16{'G', 'G', 'U', 'F', 0, '*', '.', 'g', 'g', 'u', 'f', 0, 0}
	title, _ := syscall.UTF16PtrFromString("Wybierz model GGUF")
	of := openFilename{
		file:        &file[0],
		maxFile:     uint32(len(file)),
		filter:      &filter[0],
		filterIndex: 1,
		title:       title,
		flags:       0x00080000 | 0x00001000 | 0x00000800 | 0x00000008 | 0x02000000,
	}
	of.structSize = uint32(unsafe.Sizeof(of))
	ok, _, _ := syscall.NewLazyDLL("comdlg32.dll").NewProc("GetOpenFileNameW").Call(uintptr(unsafe.Pointer(&of)))
	if ok != 0 {
		return syscall.UTF16ToString(file), nil
	}
	code, _, _ := syscall.NewLazyDLL("comdlg32.dll").NewProc("CommDlgExtendedError").Call()
	if code == 0 {
		return "", nil // user cancelled
	}
	return "", fmt.Errorf("systemowy wybór pliku nie powiódł się (0x%x)", code)
}

func openURL(url string) error {
	verb, _ := syscall.UTF16PtrFromString("open")
	value, err := syscall.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	result, _, _ := syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW").Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(value)), 0, 0, 1)
	if result <= 32 {
		return errors.New("Windows odrzucił otwarcie adresu")
	}
	return nil
}
