package main

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"

	webview "github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/webviewloader"
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

// runDesktopUI hosts the existing local panel in a real application window.
// The embedded loader remains inside the single EXE; Windows supplies the
// WebView2 runtime. Errors are returned so main can retain a browser fallback
// for older or damaged Windows installations.
func runDesktopUI(url, dataPath string) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("WebView2 nie uruchomił okna: %v", recovered)
		}
	}()
	version, err := webviewloader.GetInstalledVersion()
	if err != nil {
		return fmt.Errorf("nie można sprawdzić WebView2: %w", err)
	}
	if version == "" {
		return errors.New("brak środowiska Microsoft Edge WebView2")
	}
	window := webview.NewWithOptions(webview.WebViewOptions{
		DataPath:  dataPath,
		AutoFocus: true,
		WindowOptions: webview.WindowOptions{
			Title: "YourLM — prywatna AI",
			Width: 1040, Height: 700, Center: true,
		},
	})
	if window == nil {
		return errors.New("WebView2 odmówił utworzenia okna")
	}
	defer window.Destroy()
	window.SetSize(840, 600, webview.HintMin)
	window.Navigate(url)
	window.Run()
	return nil
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
