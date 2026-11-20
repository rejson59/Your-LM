package main

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const engineURL = "https://github.com/ggml-org/llama.cpp/releases/download/b12562/llama-b12562-bin-win-cpu-x64.zip"
const engineHash = "30ceff7bafe92af57cc74b8fbf468d150153770989fa52f8e1852b97abede653"

func executable(root string) string {
	name := "llama-server"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	var found string
	filepath.WalkDir(root, func(p string, d os.DirEntry, e error) error {
		if e == nil && !d.IsDir() && d.Name() == name {
			found = p
			return filepath.SkipAll
		}
		return nil
	})
	return found
}
func install(ctx context.Context, root string) error {
	if runtime.GOOS != "windows" {
		return errors.New("Automatyczna instalacja dotyczy Windows x64")
	}
	if executable(root) != "" {
		return nil
	}
	dir := filepath.Dir(root)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	tmp, e := os.MkdirTemp(dir, "install-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(tmp)
	req, e := http.NewRequestWithContext(ctx, "GET", engineURL, nil)
	if e != nil {
		return e
	}
	res, e := (&http.Client{Timeout: 4 * time.Minute}).Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("GitHub: %s", res.Status)
	}
	buf, e := io.ReadAll(io.LimitReader(res.Body, 32<<20))
	if e != nil {
		return e
	}
	if fmt.Sprintf("%x", sha256.Sum256(buf)) != engineHash {
		return errors.New("Suma SHA-256 silnika nie pasuje — przerwano")
	}
	z, e := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if e != nil {
		return e
	}
	for _, f := range z.File {
		if !filepath.IsLocal(f.Name) {
			return errors.New("Niepoprawna ścieżka w ZIP")
		}
		p := filepath.Join(tmp, f.Name)
		if f.FileInfo().IsDir() {
			continue
		}
		if e := os.MkdirAll(filepath.Dir(p), 0700); e != nil {
			return e
		}
		in, e := f.Open()
		if e != nil {
			return e
		}
		out, e := os.Create(p)
		if e != nil {
			in.Close()
			return e
		}
		_, e = io.Copy(out, in)
		in.Close()
		ce := out.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
	}
	if executable(tmp) == "" {
		return errors.New("Brak llama-server.exe w archiwum")
	}
	return os.Rename(tmp, root)
}
