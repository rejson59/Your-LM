//go:build windows

package main

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const uninstallRegistryPath = `Software\Microsoft\Windows\CurrentVersion\Uninstall\YourLM`

func desktopInstallPaths() (string, string, error) {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		var err error
		local, err = os.UserCacheDir()
		if err != nil {
			return "", "", err
		}
	}
	roaming := os.Getenv("APPDATA")
	if roaming == "" {
		var err error
		roaming, err = os.UserConfigDir()
		if err != nil {
			return "", "", err
		}
	}
	installDirectory := filepath.Join(local, "Programs", "YourLM")
	if root := preferredWindowsStorageRoot(); root != "" {
		installDirectory = filepath.Join(root, "App")
	}
	return filepath.Join(installDirectory, "YourLM.exe"),
		filepath.Join(roaming, "Microsoft", "Windows", "Start Menu", "Programs", "YourLM.lnk"), nil
}

func argumentPresent(args []string, wanted string) bool {
	for _, arg := range args {
		if arg == wanted {
			return true
		}
	}
	return false
}

func regularFile(path string) bool {
	info, err := os.Lstat(path)
	return err == nil && info.Mode().IsRegular()
}

func windowsPowerShell() string {
	root := os.Getenv("SystemRoot")
	if root == "" {
		root = os.Getenv("WINDIR")
	}
	if root != "" {
		path := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
		if regularFile(path) {
			return path
		}
	}
	return "powershell.exe"
}

func sameExecutablePath(first, second string) bool {
	if strings.EqualFold(filepath.Clean(first), filepath.Clean(second)) {
		return true
	}
	left, leftErr := os.Stat(first)
	right, rightErr := os.Stat(second)
	return leftErr == nil && rightErr == nil && os.SameFile(left, right)
}

func equalFiles(first, second string) bool {
	left, err := os.Open(first)
	if err != nil {
		return false
	}
	defer left.Close()
	right, err := os.Open(second)
	if err != nil {
		return false
	}
	defer right.Close()
	leftHash, rightHash := sha256.New(), sha256.New()
	if _, err = io.Copy(leftHash, left); err != nil {
		return false
	}
	if _, err = io.Copy(rightHash, right); err != nil {
		return false
	}
	return string(leftHash.Sum(nil)) == string(rightHash.Sum(nil))
}

func installedVersion() string {
	key, err := registry.OpenKey(registry.CURRENT_USER, uninstallRegistryPath, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	value, _, _ := key.GetStringValue("DisplayVersion")
	return value
}

func registeredDesktopTarget() string {
	key, err := registry.OpenKey(registry.CURRENT_USER, uninstallRegistryPath, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer key.Close()
	location, _, err := key.GetStringValue("InstallLocation")
	if err != nil || !filepath.IsAbs(location) {
		return ""
	}
	return filepath.Join(location, "YourLM.exe")
}

func legacyDesktopTarget(path string) bool {
	local := os.Getenv("LOCALAPPDATA")
	if local == "" {
		return false
	}
	wanted := filepath.Join(local, "Programs", "YourLM", "YourLM.exe")
	return sameExecutablePath(path, wanted) || strings.EqualFold(filepath.Clean(path), filepath.Clean(wanted))
}

func replaceInstalledExecutable(source, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return err
	}
	tmp, old := target+".installing", target+".previous"
	_ = os.Remove(tmp)
	_ = os.Remove(old)
	if err := copyExecutable(source, tmp); err != nil {
		return err
	}
	if regularFile(target) {
		if err := os.Rename(target, old); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("zamknięcie poprzedniej instalacji: %w", err)
		}
	}
	if err := os.Rename(tmp, target); err != nil {
		_ = os.Rename(old, target)
		_ = os.Remove(tmp)
		return err
	}
	_ = os.Remove(old)
	return nil
}

func createStartMenuShortcut(shortcut, target string) error {
	if err := os.MkdirAll(filepath.Dir(shortcut), 0700); err != nil {
		return err
	}
	const script = `$ErrorActionPreference='Stop'; $shell=New-Object -ComObject WScript.Shell; $link=$shell.CreateShortcut($env:YOURLM_SHORTCUT_PATH); $link.TargetPath=$env:YOURLM_INSTALL_TARGET; $link.WorkingDirectory=$env:YOURLM_INSTALL_DIRECTORY; $link.Description='YourLM — prywatna lokalna AI'; $link.IconLocation=$env:YOURLM_INSTALL_TARGET+',0'; $link.Save()`
	command := exec.Command(
		windowsPowerShell(), "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden",
		"-Command", script,
	)
	command.Env = append(os.Environ(),
		"YOURLM_SHORTCUT_PATH="+shortcut,
		"YOURLM_INSTALL_TARGET="+target,
		"YOURLM_INSTALL_DIRECTORY="+filepath.Dir(target),
	)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("utworzenie skrótu Menu Start: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	if !regularFile(shortcut) {
		return errors.New("Windows nie utworzył skrótu Menu Start")
	}
	return nil
}

func registerDesktopInstall(target, shortcut, version string) error {
	// Recreate the shortcut even when it already exists: RC17 can move the
	// installed executable from LocalAppData on C: to the managed D: root.
	if err := createStartMenuShortcut(shortcut, target); err != nil {
		return err
	}
	key, _, err := registry.CreateKey(
		registry.CURRENT_USER,
		uninstallRegistryPath,
		registry.SET_VALUE|registry.QUERY_VALUE,
	)
	if err != nil {
		return err
	}
	defer key.Close()
	values := map[string]string{
		"DisplayName":          "YourLM",
		"DisplayVersion":       version,
		"Publisher":            "YourLM open-source project",
		"InstallLocation":      filepath.Dir(target),
		"DisplayIcon":          target + ",0",
		"UninstallString":      `"` + target + `" --uninstall`,
		"QuietUninstallString": `"` + target + `" --uninstall`,
		"URLInfoAbout":         "https://github.com/rejson59/Your-LM",
	}
	for name, value := range values {
		if err := key.SetStringValue(name, value); err != nil {
			return err
		}
	}
	for name, value := range map[string]uint32{"NoModify": 1, "NoRepair": 1} {
		if err := key.SetDWordValue(name, value); err != nil {
			return err
		}
	}
	if info, err := os.Stat(target); err == nil {
		_ = key.SetDWordValue("EstimatedSize", uint32((info.Size()+1023)/1024))
	}
	return nil
}

func launchInstalled(target string, args []string) error {
	forwarded := make([]string, 0, len(args))
	for _, arg := range args {
		if arg != "--install-only" && arg != "--uninstall" {
			forwarded = append(forwarded, arg)
		}
	}
	command := exec.Command(target, forwarded...)
	command.Dir = filepath.Dir(target)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: false}
	return command.Start()
}

func retireLegacyDesktopInstall(oldTarget, newTarget string) error {
	if oldTarget == "" || sameExecutablePath(oldTarget, newTarget) || !legacyDesktopTarget(oldTarget) {
		return nil
	}
	const script = `$pidToWait=[int]$env:YOURLM_MIGRATION_PID; Wait-Process -Id $pidToWait -Timeout 30 -ErrorAction SilentlyContinue; Remove-Item -LiteralPath $env:YOURLM_OLD_INSTALL_DIRECTORY -Recurse -Force -ErrorAction SilentlyContinue`
	command := exec.Command(
		windowsPowerShell(), "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden",
		"-Command", script,
	)
	command.Env = append(os.Environ(),
		"YOURLM_MIGRATION_PID="+strconv.Itoa(os.Getpid()),
		"YOURLM_OLD_INSTALL_DIRECTORY="+filepath.Dir(oldTarget),
	)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return command.Start()
}

func uninstallDesktop(target, shortcut string) error {
	_ = os.Remove(shortcut)
	_ = registry.DeleteKey(registry.CURRENT_USER, uninstallRegistryPath)
	const script = `$pidToWait=[int]$env:YOURLM_UNINSTALL_PID; Wait-Process -Id $pidToWait -Timeout 30 -ErrorAction SilentlyContinue; Remove-Item -LiteralPath $env:YOURLM_UNINSTALL_TARGET -Force -ErrorAction SilentlyContinue; Remove-Item -LiteralPath $env:YOURLM_UNINSTALL_DIRECTORY -Recurse -Force -ErrorAction SilentlyContinue`
	command := exec.Command(
		windowsPowerShell(), "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden",
		"-Command", script,
	)
	command.Env = append(os.Environ(),
		"YOURLM_UNINSTALL_PID="+strconv.Itoa(os.Getpid()),
		"YOURLM_UNINSTALL_TARGET="+target,
		"YOURLM_UNINSTALL_DIRECTORY="+filepath.Dir(target),
	)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return command.Start()
}

// ensureDesktopInstall turns the downloaded single EXE into a normal per-user
// Windows application. It needs no administrator rights and keeps models and
// logs in the existing roaming YourLM directory.
func ensureDesktopInstall(args []string) (bool, error) {
	if argumentPresent(args, "--portable") || buildVersion == "development" {
		return false, nil
	}
	oldTarget := registeredDesktopTarget()
	target, shortcut, err := desktopInstallPaths()
	if err != nil {
		return argumentPresent(args, "--install-only") || argumentPresent(args, "--uninstall"), err
	}
	if argumentPresent(args, "--uninstall") {
		return true, uninstallDesktop(target, shortcut)
	}
	self, err := os.Executable()
	if err != nil {
		return argumentPresent(args, "--install-only"), err
	}
	self, _ = filepath.Abs(self)
	target, _ = filepath.Abs(target)
	installed := sameExecutablePath(self, target)
	registeredVersion := buildVersion
	if !installed {
		currentInstalledVersion := installedVersion()
		keepNewer := regularFile(target) && newerVersion(currentInstalledVersion, buildVersion)
		if keepNewer {
			registeredVersion = currentInstalledVersion
		} else if !regularFile(target) || !equalFiles(self, target) {
			if err := replaceInstalledExecutable(self, target); err != nil {
				if argumentPresent(args, "--install-only") {
					return true, err
				}
				return false, err
			}
		}
	}
	if err := registerDesktopInstall(target, shortcut, registeredVersion); err != nil {
		if argumentPresent(args, "--install-only") {
			return true, err
		}
		if installed {
			return false, err
		}
		return false, err
	}
	if argumentPresent(args, "--install-only") {
		if err := retireLegacyDesktopInstall(oldTarget, target); err != nil {
			return true, err
		}
		return true, nil
	}
	if installed {
		return false, nil
	}
	if err := launchInstalled(target, args); err != nil {
		return true, err
	}
	if err := retireLegacyDesktopInstall(oldTarget, target); err != nil {
		return true, err
	}
	return true, nil
}
