package main

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
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
	Path               string `json:"path"`
	Name               string `json:"name"`
	Size               uint64 `json:"size"`
	Layers             int    `json:"layers"`
	LargestTensorBytes uint64 `json:"largestTensorBytes"`
}

func aligned(value, alignment uint64) (uint64, bool) {
	if alignment == 0 || alignment > 1<<20 || alignment&(alignment-1) != 0 {
		return 0, false
	}
	if value > ^uint64(0)-(alignment-1) {
		return 0, false
	}
	return (value + alignment - 1) &^ (alignment - 1), true
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
	if r.read(&magic) != nil || string(magic[:]) != "GGUF" || r.read(&ver) != nil || (ver != 2 && ver != 3) || r.read(&tensors) != nil || tensors == 0 || tensors > 1<<20 || r.read(&fields) != nil || fields > 65536 {
		return m, errors.New("Niepoprawny nagłówek GGUF v2/v3")
	}
	var layers uint32
	alignment := uint32(32)
	for i := uint64(0); i < fields; i++ {
		key, e := r.str()
		if e != nil {
			return m, e
		}
		var valueType uint32
		if r.read(&valueType) != nil {
			return m, io.ErrUnexpectedEOF
		}
		var count uint64 = 1
		isArray := valueType == 9
		if isArray {
			if r.read(&valueType) != nil || r.read(&count) != nil {
				return m, io.ErrUnexpectedEOF
			}
		}
		if !isArray && valueType == 4 && (strings.HasSuffix(key, ".block_count") || key == "general.alignment") {
			var value uint32
			if r.read(&value) != nil {
				return m, io.ErrUnexpectedEOF
			}
			if strings.HasSuffix(key, ".block_count") {
				if value == 0 || value > 2048 {
					return m, errors.New("Niepoprawna liczba warstw")
				}
				layers = value
			} else {
				alignment = value
			}
			continue
		}
		if e := r.values(valueType, count); e != nil {
			return m, e
		}
	}
	if layers == 0 {
		return m, errors.New("Brak block_count w metadanych GGUF")
	}
	if _, ok := aligned(0, uint64(alignment)); !ok {
		return m, errors.New("Niepoprawne wyrównanie GGUF")
	}

	offsets := make([]uint64, 0, tensors)
	for i := uint64(0); i < tensors; i++ {
		if _, e := r.str(); e != nil {
			return m, e
		}
		var dimensions, tensorType uint32
		if r.read(&dimensions) != nil || dimensions == 0 || dimensions > 8 {
			return m, errors.New("Niepoprawny opis tensora GGUF")
		}
		for d := uint32(0); d < dimensions; d++ {
			var extent uint64
			if r.read(&extent) != nil || extent == 0 {
				return m, errors.New("Niepoprawny wymiar tensora GGUF")
			}
		}
		var offset uint64
		if r.read(&tensorType) != nil || r.read(&offset) != nil {
			return m, io.ErrUnexpectedEOF
		}
		offsets = append(offsets, offset)
	}
	position, e := f.Seek(0, io.SeekCurrent)
	if e != nil {
		return m, e
	}
	dataStart, ok := aligned(uint64(position), uint64(alignment))
	if !ok || dataStart > uint64(st.Size()) {
		return m, errors.New("Niepoprawna sekcja danych GGUF")
	}
	dataBytes := uint64(st.Size()) - dataStart
	sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })
	var largest uint64
	for i, offset := range offsets {
		if offset > dataBytes {
			return m, errors.New("Tensor wykracza poza plik GGUF")
		}
		end := dataBytes
		for j := i + 1; j < len(offsets); j++ {
			if offsets[j] != offset {
				end = offsets[j]
				break
			}
		}
		if end < offset {
			return m, errors.New("Niepoprawne przesunięcie tensora GGUF")
		}
		largest = max(largest, end-offset)
	}
	if largest == 0 {
		return m, errors.New("Brak danych tensorów w GGUF")
	}
	return Model{
		Path:               path,
		Name:               filepath.Base(path),
		Size:               uint64(st.Size()),
		Layers:             int(layers),
		LargestTensorBytes: largest,
	}, nil
}
