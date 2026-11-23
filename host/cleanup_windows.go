//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	cleanupGWOwner = 4
	cleanupWMClose = 0x0010
)

var (
	cleanupUser32                 = syscall.NewLazyDLL("user32.dll")
	cleanupEnumWindows            = cleanupUser32.NewProc("EnumWindows")
	cleanupIsWindowVisible        = cleanupUser32.NewProc("IsWindowVisible")
	cleanupGetWindow              = cleanupUser32.NewProc("GetWindow")
	cleanupGetWindowTextLength    = cleanupUser32.NewProc("GetWindowTextLengthW")
	cleanupGetWindowText          = cleanupUser32.NewProc("GetWindowTextW")
	cleanupGetWindowThreadProcess = cleanupUser32.NewProc("GetWindowThreadProcessId")
	cleanupGetShellWindow         = cleanupUser32.NewProc("GetShellWindow")
	cleanupPostMessage            = cleanupUser32.NewProc("PostMessageW")
)

type cleanupWindow struct {
	Handle uintptr
	PID    int
	Title  string
}

type cleanupProcess struct {
	PID      int
	Parent   int
	Name     string
	Elevated bool
}

func cleanupPlatformSupported() bool {
	return true
}

func cleanupWindows() ([]cleanupWindow, error) {
	if err := cleanupEnumWindows.Find(); err != nil {
		return nil, err
	}
	shell, _, _ := cleanupGetShellWindow.Call()
	items := make([]cleanupWindow, 0, 24)
	callback := syscall.NewCallback(func(window, _ uintptr) uintptr {
		if window == 0 || window == shell {
			return 1
		}
		visible, _, _ := cleanupIsWindowVisible.Call(window)
		if visible == 0 {
			return 1
		}
		owner, _, _ := cleanupGetWindow.Call(window, cleanupGWOwner)
		if owner != 0 {
			return 1
		}
		length, _, _ := cleanupGetWindowTextLength.Call(window)
		if length == 0 || length > 4096 {
			return 1
		}
		buffer := make([]uint16, int(length)+1)
		copied, _, _ := cleanupGetWindowText.Call(
			window,
			uintptr(unsafe.Pointer(&buffer[0])),
			uintptr(len(buffer)),
		)
		if copied == 0 {
			return 1
		}
		var pid uint32
		cleanupGetWindowThreadProcess.Call(window, uintptr(unsafe.Pointer(&pid)))
		if pid == 0 {
			return 1
		}
		title := strings.TrimSpace(windows.UTF16ToString(buffer))
		if title == "" {
			return 1
		}
		runes := []rune(title)
		if len(runes) > 180 {
			title = string(runes[:180]) + "…"
		}
		items = append(items, cleanupWindow{Handle: window, PID: int(pid), Title: title})
		return 1
	})
	result, _, callErr := cleanupEnumWindows.Call(callback, 0)
	if result == 0 {
		if callErr != nil && callErr != syscall.Errno(0) {
			return nil, callErr
		}
		return nil, errors.New("Windows nie zwrócił listy otwartych okien")
	}
	return items, nil
}

func cleanupProcessSnapshot() (map[int]cleanupProcess, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	items := make(map[int]cleanupProcess)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}
	for {
		pid := int(entry.ProcessID)
		items[pid] = cleanupProcess{
			PID:    pid,
			Parent: int(entry.ParentProcessID),
			Name:   windows.UTF16ToString(entry.ExeFile[:]),
		}
		entry.Size = uint32(unsafe.Sizeof(entry))
		err = windows.Process32Next(snapshot, &entry)
		if err == windows.ERROR_NO_MORE_FILES {
			break
		}
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func cleanupCurrentUser() (*windows.SID, uint32, func(), error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return nil, 0, func() {}, err
	}
	user, err := token.GetTokenUser()
	if err != nil {
		token.Close()
		return nil, 0, func() {}, err
	}
	var session uint32
	if err := windows.ProcessIdToSessionId(uint32(os.Getpid()), &session); err != nil {
		token.Close()
		return nil, 0, func() {}, err
	}
	return user.User.Sid, session, func() { _ = token.Close() }, nil
}

func cleanupProcessIdentity(pid int, wantedUser *windows.SID, wantedSession uint32) (bool, bool) {
	if pid <= 0 {
		return false, false
	}
	var session uint32
	if windows.ProcessIdToSessionId(uint32(pid), &session) != nil || session != wantedSession {
		return false, false
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false, false
	}
	defer windows.CloseHandle(handle)
	var token windows.Token
	if err := windows.OpenProcessToken(handle, windows.TOKEN_QUERY, &token); err != nil {
		return false, false
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil || !user.User.Sid.Equals(wantedUser) {
		return false, false
	}
	return true, token.IsElevated()
}

func cleanupRootForProcess(pid int, processes map[int]cleanupProcess, roots map[int][]cleanupWindow) int {
	seen := make(map[int]bool, 8)
	for depth := 0; depth < 64 && pid > 0 && !seen[pid]; depth++ {
		if len(roots[pid]) > 0 {
			return pid
		}
		seen[pid] = true
		process, ok := processes[pid]
		if !ok || process.Parent == pid {
			return 0
		}
		pid = process.Parent
	}
	return 0
}

func scanCleanupApps() ([]CleanupApp, error) {
	windowList, err := cleanupWindows()
	if err != nil {
		return nil, fmt.Errorf("nie udało się odczytać okien Windows: %w", err)
	}
	processes, err := cleanupProcessSnapshot()
	if err != nil {
		return nil, fmt.Errorf("nie udało się odczytać procesów Windows: %w", err)
	}
	user, session, closeToken, err := cleanupCurrentUser()
	if err != nil {
		return nil, fmt.Errorf("nie udało się potwierdzić użytkownika procesów: %w", err)
	}
	defer closeToken()

	roots := make(map[int][]cleanupWindow)
	for _, window := range windowList {
		if _, ok := processes[window.PID]; !ok {
			continue
		}
		if sameUser, _ := cleanupProcessIdentity(window.PID, user, session); !sameUser {
			continue
		}
		roots[window.PID] = append(roots[window.PID], window)
	}
	type aggregate struct {
		resident, committed uint64
		processes           int
	}
	aggregates := make(map[int]aggregate)
	identity := make(map[int]bool, len(processes))
	elevated := make(map[int]bool, len(roots))
	for pid := range processes {
		sameUser, isElevated := cleanupProcessIdentity(pid, user, session)
		identity[pid] = sameUser
		if !sameUser {
			continue
		}
		if len(roots[pid]) > 0 {
			elevated[pid] = isElevated
		}
		root := cleanupRootForProcess(pid, processes, roots)
		if root == 0 {
			continue
		}
		memory := memoryForProcess(pid)
		value := aggregates[root]
		value.resident += memory.Resident
		value.committed += memory.Committed
		value.processes++
		aggregates[root] = value
	}

	apps := make([]CleanupApp, 0, len(roots))
	for pid, appWindows := range roots {
		if !identity[pid] {
			continue
		}
		process := processes[pid]
		title := appWindows[0].Title
		for _, window := range appWindows[1:] {
			if len([]rune(window.Title)) > len([]rune(title)) {
				title = window.Title
			}
		}
		memory := aggregates[pid]
		reason := cleanupProtection(process.Name, title, pid, os.Getpid(), elevated[pid])
		apps = append(apps, CleanupApp{
			PID:             pid,
			Name:            process.Name,
			Title:           title,
			Resident:        memory.resident,
			Committed:       memory.committed,
			Processes:       max(1, memory.processes),
			Windows:         len(appWindows),
			Closable:        reason == "",
			ProtectedReason: reason,
		})
	}
	sort.Slice(apps, func(i, j int) bool {
		if apps[i].Resident != apps[j].Resident {
			return apps[i].Resident > apps[j].Resident
		}
		return strings.ToLower(apps[i].Title) < strings.ToLower(apps[j].Title)
	})
	if len(apps) > 40 {
		apps = apps[:40]
	}
	return apps, nil
}

func requestCloseCleanupApps(apps []CleanupApp) (map[int]cleanupCloseRequest, error) {
	windowsNow, err := cleanupWindows()
	if err != nil {
		return nil, err
	}
	processes, err := cleanupProcessSnapshot()
	if err != nil {
		return nil, err
	}
	user, session, closeToken, err := cleanupCurrentUser()
	if err != nil {
		return nil, err
	}
	defer closeToken()
	selected := make(map[int]CleanupApp, len(apps))
	requests := make(map[int]cleanupCloseRequest, len(apps))
	eligible := make(map[int]bool, len(apps))
	for _, app := range apps {
		selected[app.PID] = app
		requests[app.PID] = cleanupCloseRequest{}
		process, exists := processes[app.PID]
		sameUser, elevated := cleanupProcessIdentity(app.PID, user, session)
		eligible[app.PID] = exists && sameUser && strings.EqualFold(process.Name, app.Name) && cleanupProtection(process.Name, app.Title, app.PID, os.Getpid(), elevated) == ""
	}
	// Treat a newly protected title as protecting the whole application, not
	// merely that one window. This is a final identity/protection check after
	// the user confirmation and immediately before any WM_CLOSE is posted.
	for _, window := range windowsNow {
		app, ok := selected[window.PID]
		if !ok || !eligible[window.PID] {
			continue
		}
		process := processes[window.PID]
		_, elevated := cleanupProcessIdentity(window.PID, user, session)
		if cleanupProtection(process.Name, window.Title, app.PID, os.Getpid(), elevated) != "" {
			eligible[window.PID] = false
		}
	}
	for _, window := range windowsNow {
		app, ok := selected[window.PID]
		process, exists := processes[window.PID]
		if !ok || !eligible[window.PID] || !exists || !strings.EqualFold(process.Name, app.Name) {
			continue
		}
		request := requests[window.PID]
		result, _, _ := cleanupPostMessage.Call(window.Handle, cleanupWMClose, 0, 0)
		if result == 0 {
			request.Failed++
		} else {
			request.Posted++
		}
		requests[window.PID] = request
	}
	postedApps, failedWindows := 0, 0
	for _, request := range requests {
		if request.Posted > 0 {
			postedApps++
		}
		failedWindows += request.Failed
	}
	if postedApps == 0 {
		return requests, errors.New("Windows nie przyjął prośby o zamknięcie żadnego wybranego okna")
	}
	if failedWindows > 0 || postedApps < len(apps) {
		return requests, fmt.Errorf(
			"%d aplikacji i %d dodatkowych okien nie odpowiedziało na WM_CLOSE",
			len(apps)-postedApps,
			failedWindows,
		)
	}
	return requests, nil
}
