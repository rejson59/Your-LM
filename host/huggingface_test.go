package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHuggingFaceSearchAndGGUFFiles(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/models":
			if r.URL.Query().Get("filter") != "gguf" || r.URL.Query().Get("search") != "gemma" {
				t.Fatalf("unexpected search query: %s", r.URL.RawQuery)
			}
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[{"id":"example/gemma-GGUF","author":"example","downloads":42,"likes":7,"gated":false,"tags":["gguf"],"pipeline_tag":"text-generation"},{"id":"private/gemma-GGUF","gated":"manual"}]`)
		case r.URL.Path == "/api/models/example/gemma-GGUF/tree/main":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `[
				{"type":"file","path":"gemma-Q8_0.gguf","size":800},
				{"type":"file","path":"gemma-Q4_K_M.gguf","size":400,"lfs":{"oid":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","size":444}},
				{"type":"file","path":"gemma-00001-of-00002.gguf","size":200},
				{"type":"file","path":"README.md","size":20}
			]`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	oldBase := huggingFaceBaseURL
	huggingFaceBaseURL = server.URL
	defer func() { huggingFaceBaseURL = oldBase }()

	models, err := searchHuggingFace(context.Background(), "gemma")
	if err != nil || len(models) != 2 {
		t.Fatalf("search returned %+v, %v", models, err)
	}
	if models[0].ID != "example/gemma-GGUF" || models[0].Gated || !models[1].Gated {
		t.Fatalf("search metadata was not preserved: %+v", models)
	}
	files, err := listHuggingFaceFiles(context.Background(), models[0].ID)
	if err != nil || len(files) != 2 {
		t.Fatalf("files returned %+v, %v", files, err)
	}
	if files[0].Quantization != "Q4_K_M" || !files[0].Recommended || files[0].Size != 444 {
		t.Fatalf("recommended Q4 file was not ranked first: %+v", files)
	}
	if files[0].OID != strings.Repeat("a", 64) {
		t.Fatalf("LFS digest missing: %+v", files[0])
	}
}

func TestParallelHuggingFaceDownloadResumesAndVerifies(t *testing.T) {
	content := make([]byte, 2<<20)
	for index := range content {
		content[index] = byte((index*31 + 17) % 251)
	}
	var active, maximum, requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := strings.TrimPrefix(r.Header.Get("Range"), "bytes=")
		parts := strings.Split(value, "-")
		if len(parts) != 2 {
			http.Error(w, "range required", http.StatusBadRequest)
			return
		}
		start, firstErr := strconv.ParseInt(parts[0], 10, 64)
		end, secondErr := strconv.ParseInt(parts[1], 10, 64)
		if firstErr != nil || secondErr != nil || start < 0 || end < start || end >= int64(len(content)) {
			http.Error(w, "bad range", http.StatusRequestedRangeNotSatisfiable)
			return
		}
		requests.Add(1)
		now := active.Add(1)
		for {
			old := maximum.Load()
			if now <= old || maximum.CompareAndSwap(old, now) {
				break
			}
		}
		defer active.Add(-1)
		time.Sleep(8 * time.Millisecond)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(content)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(content[start : end+1])
	}))
	defer server.Close()

	directory := t.TempDir()
	temporary := filepath.Join(directory, "model.part")
	planFile := temporary + ".json"
	chunkSize := int64(128 << 10)
	plan := hfRangePlan{
		Source:    "example/model@main/model.gguf",
		Size:      int64(len(content)),
		ChunkSize: chunkSize,
		Done:      make([]bool, (int64(len(content))+chunkSize-1)/chunkSize),
	}
	plan.Done[0] = true
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err = file.Truncate(int64(len(content))); err == nil {
		_, err = file.WriteAt(content[:chunkSize], 0)
	}
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomic(planFile, &plan); err != nil {
		t.Fatal(err)
	}

	digest := sha256.Sum256(content)
	var transferred atomic.Int64
	err = downloadHFRanges(
		context.Background(),
		server.URL,
		temporary,
		planFile,
		plan.Source,
		"",
		int64(len(content)),
		chunkSize,
		4,
		&transferred,
	)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(temporary)
	if err != nil || !bytes.Equal(actual, content) {
		t.Fatalf("download mismatch: %v", err)
	}
	if transferred.Load() != int64(len(content)) {
		t.Fatalf("progress=%d, want %d", transferred.Load(), len(content))
	}
	if requests.Load() != int64(len(plan.Done)-1) {
		t.Fatalf("downloaded %d ranges; resume should fetch %d", requests.Load(), len(plan.Done)-1)
	}
	if maximum.Load() < 2 {
		t.Fatalf("ranges were not downloaded concurrently; max=%d", maximum.Load())
	}
	if err := verifyHFSHA256(context.Background(), temporary, hex.EncodeToString(digest[:])); err != nil {
		t.Fatal(err)
	}
}

func TestHuggingFaceDownloadRejectsWrongRangeAndHash(t *testing.T) {
	content := bytes.Repeat([]byte("range-data"), 2048)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 1-%d/%d", len(content), len(content)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(content)
	}))
	defer server.Close()
	directory := t.TempDir()
	var transferred atomic.Int64
	err := downloadHFRanges(
		context.Background(),
		server.URL,
		filepath.Join(directory, "bad.part"),
		filepath.Join(directory, "bad.part.json"),
		"example/bad@main/bad.gguf",
		"",
		int64(len(content)),
		int64(len(content)),
		1,
		&transferred,
	)
	if err == nil || !strings.Contains(err.Error(), "Content-Range") {
		t.Fatalf("wrong Content-Range error=%v", err)
	}
	if transferred.Load() != 0 {
		t.Fatalf("rejected response advanced progress to %d", transferred.Load())
	}

	file := filepath.Join(directory, "hash.gguf")
	if err := os.WriteFile(file, content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := verifyHFSHA256(context.Background(), file, strings.Repeat("0", 64)); err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("hash mismatch error=%v", err)
	}
}

func TestHuggingFaceDownloadCancellationKeepsResumeState(t *testing.T) {
	requestStarted := make(chan struct{})
	var once atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if once.CompareAndSwap(false, true) {
			close(requestStarted)
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	directory := t.TempDir()
	temporary := filepath.Join(directory, "cancel.part")
	planFile := temporary + ".json"
	var transferred atomic.Int64
	finished := make(chan error, 1)
	go func() {
		finished <- downloadHFRanges(
			ctx,
			server.URL,
			temporary,
			planFile,
			"example/cancel@main/cancel.gguf",
			"",
			1<<20,
			256<<10,
			2,
			&transferred,
		)
	}()
	select {
	case <-requestStarted:
		cancel()
	case <-time.After(5 * time.Second):
		t.Fatal("range request did not start")
	}
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("download did not stop after cancellation")
	}
	if _, err := os.Stat(temporary); err != nil {
		t.Fatalf("partial file was not preserved: %v", err)
	}
	plan := hfReadPlan(planFile, "example/cancel@main/cancel.gguf", 1<<20, 256<<10, "")
	if hfCompletedBytes(plan) != 0 {
		t.Fatalf("incomplete ranges were marked done: %+v", plan.Done)
	}
}

func TestHuggingFaceDiskRequirement(t *testing.T) {
	if got := hfRequiredDiskBytes(10<<30, 4<<30); got != (6<<30)+(512<<20) {
		t.Fatalf("required bytes=%d", got)
	}
	if got := hfRequiredDiskBytes(20<<30, 19<<30); got != 2<<30 {
		t.Fatalf("five-percent reserve was not applied: %d", got)
	}
	if got := hfRequiredDiskBytes(2<<30, 2<<30); got != 0 {
		t.Fatalf("complete resume should need no download capacity: %d", got)
	}
}

func TestHuggingFacePathValidation(t *testing.T) {
	for _, invalid := range []string{"../model.gguf", "/model.gguf", `folder\\model.gguf`, "model.bin"} {
		_, err := hfDownloadURL(HFFile{Repo: "example/model", Path: invalid})
		if err == nil {
			t.Fatalf("accepted unsafe Hugging Face path %q", invalid)
		}
	}
	if _, err := hfDownloadURL(HFFile{Repo: "example/model", Path: "quant/model-Q4_K_M.gguf"}); err != nil {
		t.Fatal(err)
	}
}
