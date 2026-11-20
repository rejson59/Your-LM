package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const releasesAPI = "https://api.github.com/repos/rejson59/Your-LM/releases?per_page=12"

type UpdateState struct {
	Current     string `json:"current"`
	Latest      string `json:"latest,omitempty"`
	Available   bool   `json:"available"`
	Checking    bool   `json:"checking"`
	Downloading bool   `json:"downloading"`
	Ready       bool   `json:"ready"`
	Error       string `json:"error,omitempty"`
	Notes       string `json:"notes,omitempty"`
	PageURL     string `json:"pageUrl,omitempty"`
	Progress    string `json:"progress,omitempty"`
	assetURL    string
	digest      string
	downloaded  string
}

type githubRelease struct {
	TagName    string `json:"tag_name"`
	Name       string `json:"name"`
	Body       string `json:"body"`
	HTMLURL    string `json:"html_url"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Digest             string `json:"digest"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

func versionParts(value string) ([]int, string) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(strings.TrimPrefix(value, "v"), "V")
	main, suffix, _ := strings.Cut(value, "-")
	pieces := strings.Split(main, ".")
	parts := make([]int, 3)
	for i := 0; i < len(parts) && i < len(pieces); i++ {
		parts[i], _ = strconv.Atoi(pieces[i])
	}
	return parts, suffix
}

func prereleaseOrder(value string) (string, int, bool) {
	i := len(value)
	for i > 0 && value[i-1] >= '0' && value[i-1] <= '9' {
		i--
	}
	if i == len(value) {
		return value, 0, false
	}
	number, err := strconv.Atoi(value[i:])
	return value[:i], number, err == nil
}

func newerVersion(candidate, current string) bool {
	a, as := versionParts(candidate)
	b, bs := versionParts(current)
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	if as == bs {
		return false
	}
	if as == "" {
		return true
	}
	if bs == "" {
		return false
	}
	ap, an, aNumber := prereleaseOrder(as)
	bp, bn, bNumber := prereleaseOrder(bs)
	if ap == bp && aNumber && bNumber {
		return an > bn
	}
	return as > bs
}

func releaseAsset(release githubRelease, name string) (string, string) {
	for _, asset := range release.Assets {
		if asset.Name == name {
			return asset.BrowserDownloadURL, asset.Digest
		}
	}
	return "", ""
}

func githubJSON(ctx context.Context, target string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "YourLM/"+buildVersion)
	client := &http.Client{Timeout: 25 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub odpowiedział kodem %d", response.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(response.Body, 3<<20)).Decode(out)
}

func (a *App) checkForUpdates(autoDownload bool) {
	a.Lock()
	if buildVersion == "development" {
		a.update.Current = buildVersion
		a.update.Progress = "Automatyczne aktualizacje działają w buildach wydaniowych."
		a.Unlock()
		return
	}
	if a.update.Checking || a.update.Downloading {
		a.Unlock()
		return
	}
	a.update.Current = buildVersion
	a.update.Checking = true
	a.update.Error = ""
	a.Unlock()

	var releases []githubRelease
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	err := githubJSON(ctx, releasesAPI, &releases)
	cancel()
	var selected *githubRelease
	if err == nil {
		allowPrerelease := strings.Contains(buildVersion, "-")
		for i := range releases {
			release := &releases[i]
			if release.Draft || (release.Prerelease && !allowPrerelease) || !newerVersion(release.TagName, buildVersion) {
				continue
			}
			asset, digest := releaseAsset(*release, "YourLM.exe")
			if runtime.GOOS == "windows" && (asset == "" || !strings.HasPrefix(digest, "sha256:")) {
				continue
			}
			selected = release
			break
		}
	}
	a.Lock()
	a.update.Checking = false
	if err != nil {
		a.update.Error = "Nie udało się sprawdzić aktualizacji: " + err.Error()
	} else if selected == nil {
		a.update.Available = false
		a.update.Latest = buildVersion
		a.update.Progress = "Masz najnowszą wersję."
	} else {
		asset, digest := releaseAsset(*selected, "YourLM.exe")
		latest := strings.TrimPrefix(strings.TrimPrefix(selected.TagName, "v"), "V")
		if latest != a.update.Latest {
			a.update.Ready = false
			a.update.downloaded = ""
		}
		a.update.Latest = latest
		a.update.Available = true
		a.update.PageURL = selected.HTMLURL
		a.update.Notes = strings.TrimSpace(selected.Body)
		if len(a.update.Notes) > 1200 {
			a.update.Notes = a.update.Notes[:1200] + "…"
		}
		a.update.assetURL = asset
		a.update.digest = digest
		a.update.Progress = "Nowa wersja jest dostępna."
	}
	shouldDownload := autoDownload && selected != nil && runtime.GOOS == "windows"
	a.Unlock()
	if shouldDownload {
		a.downloadUpdate()
	}
}

func copyDownload(response *http.Response, path string) (string, error) {
	if response.ContentLength > 200<<20 {
		return "", errors.New("plik aktualizacji jest zbyt duży")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	tmp := path + ".part"
	defer os.Remove(tmp)
	file, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0700)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, (200<<20)+1))
	closeErr := file.Close()
	if copyErr != nil {
		return "", copyErr
	}
	if closeErr != nil {
		return "", closeErr
	}
	if written > 200<<20 {
		return "", errors.New("plik aktualizacji jest zbyt duży")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (a *App) downloadUpdate() error {
	a.Lock()
	if a.update.Ready {
		a.Unlock()
		return nil
	}
	if a.update.Downloading {
		a.Unlock()
		return errors.New("Pobieranie już trwa")
	}
	url, latest, digest := a.update.assetURL, a.update.Latest, a.update.digest
	if url == "" || latest == "" {
		a.Unlock()
		return errors.New("Brak pliku EXE w najnowszym wydaniu")
	}
	a.update.Downloading = true
	a.update.Error = ""
	a.update.Progress = "Pobieranie aktualizacji…"
	a.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err == nil {
		req.Header.Set("User-Agent", "YourLM/"+buildVersion)
	}
	var response *http.Response
	if err == nil {
		response, err = http.DefaultClient.Do(req)
	}
	if err == nil && response.StatusCode != http.StatusOK {
		err = fmt.Errorf("pobieranie zwróciło kod %d", response.StatusCode)
	}
	var path, got string
	if err == nil {
		path = filepath.Join(a.dir, "updates", "YourLM-"+latest+".exe")
		got, err = copyDownload(response, path)
	}
	if response != nil {
		response.Body.Close()
	}
	if err == nil && strings.HasPrefix(digest, "sha256:") && !strings.EqualFold(strings.TrimPrefix(digest, "sha256:"), got) {
		err = errors.New("suma pobranej aktualizacji nie zgadza się z wydaniem GitHub")
		_ = os.Remove(path)
	}
	a.Lock()
	a.update.Downloading = false
	if err != nil {
		a.update.Error = "Aktualizacja nie została pobrana: " + err.Error()
		a.update.Progress = ""
	} else {
		a.update.Ready = true
		a.update.downloaded = path
		a.update.Progress = "Aktualizacja pobrana — uruchom ponownie, aby ją zastosować."
	}
	a.Unlock()
	return err
}

func (a *App) updateAPI(w http.ResponseWriter, op string) {
	switch op {
	case "update-check":
		a.checkForUpdates(false)
	case "update-download":
		if err := a.downloadUpdate(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	case "update-apply":
		a.Lock()
		if a.s != nil || a.off != nil {
			a.Unlock()
			http.Error(w, "Najpierw zatrzymaj model lub worker", http.StatusConflict)
			return
		}
		path := a.update.downloaded
		a.Unlock()
		if path == "" {
			http.Error(w, "Aktualizacja nie jest jeszcze pobrana", http.StatusBadRequest)
			return
		}
		if err := launchDesktopUpdate(path); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		respond(w, map[string]any{"restarting": true})
		go func() {
			time.Sleep(500 * time.Millisecond)
			os.Exit(0)
		}()
		return
	default:
		http.Error(w, "Nieznana operacja aktualizacji", http.StatusNotFound)
		return
	}
	a.Lock()
	defer a.Unlock()
	respond(w, a.state())
}
