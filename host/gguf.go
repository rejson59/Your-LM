package main

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Inspect only metadata. Never allocate based on untrusted tensor or array sizes.
type headerReader struct {
	f   *os.File
	end int64
}

func (r headerReader) skip(n uint64) error {
	pos, e := r.f.Seek(0, 1)
	if e != nil || pos > r.end || n > uint64(r.end-pos) {
		return io.ErrUnexpectedEOF
	}
	_, e = r.f.Seek(int64(n), 1)
	return e
}
func (r headerReader) read(v any) error { return binary.Read(r.f, binary.LittleEndian, v) }
func (r headerReader) str() (string, error) {
	var n uint64
	if r.read(&n) != nil || n > 1024 {
		return "", errors.New("Nieprawidłowe pole GGUF")
	}
	b := make([]byte, n)
	_, e := io.ReadFull(r.f, b)
	return string(b), e
}
func (r headerReader) values(t uint32, n uint64) error {
	sizes := []uint64{1, 1, 2, 2, 4, 4, 4, 1, 0, 0, 8, 8, 8}
	if int(t) >= len(sizes) || n > 1<<26 {
		return errors.New("Nieznany typ GGUF")
	}
	if t == 8 {
		for i := uint64(0); i < n; i++ {
			var s uint64
			if r.read(&s) != nil {
				return io.ErrUnexpectedEOF
			}
			if e := r.skip(s); e != nil {
				return e
			}
		}
		return nil
	}
	if t == 9 {
		return errors.New("Zagnieżdżona tablica GGUF")
	}
	return r.skip(sizes[t] * n)
}

type Model struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Size   uint64 `json:"size"`
	Layers int    `json:"layers"`
}

func inspect(path string) (Model, error) {
	var m Model
	if !strings.EqualFold(filepath.Ext(path), ".gguf") {
		return m, errors.New("Wybierz lokalny plik .gguf")
	}
	if strings.Contains(filepath.Base(path), "-of-") {
		return m, errors.New("Ta wersja obsługuje jednoplikowe GGUF")
	}
	f, e := os.Open(path)
	if e != nil {
		return m, e
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return m, errors.New("Nie jest to zwykły plik")
	}
	r := headerReader{f, min(st.Size(), 256<<20)}
	var magic [4]byte
	var ver uint32
	var tensors, fields uint64
	if r.read(&magic) != nil || string(magic[:]) != "GGUF" || r.read(&ver) != nil || (ver != 2 && ver != 3) || r.read(&tensors) != nil || tensors == 0 || r.read(&fields) != nil || fields > 65536 {
		return m, errors.New("Niepoprawny nagłówek GGUF v2/v3")
	}
	for i := uint64(0); i < fields; i++ {
		key, e := r.str()
		if e != nil {
			return m, e
		}
		var t uint32
		if r.read(&t) != nil {
			return m, io.ErrUnexpectedEOF
		}
		if strings.HasSuffix(key, ".block_count") && t == 4 {
			var layers uint32
			if r.read(&layers) != nil || layers == 0 || layers > 2048 {
				return m, errors.New("Niepoprawna liczba warstw")
			}
			return Model{path, filepath.Base(path), uint64(st.Size()), int(layers)}, nil
		}
		var n uint64 = 1
		if t == 9 {
			if r.read(&t) != nil || r.read(&n) != nil {
				return m, io.ErrUnexpectedEOF
			}
		}
		if e := r.values(t, n); e != nil {
			return m, e
		}
	}
	return m, errors.New("Brak block_count w metadanych GGUF")
}
