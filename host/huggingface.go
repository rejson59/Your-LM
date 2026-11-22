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
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	hfRangeChunkSize = int64(64 << 20)
	hfRangeWorkers   = 8
)

var (
	huggingFaceBaseURL = "https://huggingface.co"
	hfRepoPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,95}/[A-Za-z0-9][A-Za-z0-9_.-]{0,191}$`)
	hfQuantPattern     = regexp.MustCompile(`(?i)(IQ[1-4](?:_[A-Z0-9]+)?|Q[2-8](?:_[A-Z0-9]+)*)`)
)

type HFModel struct {
	ID           string   `json:"id"`
	Author       string   `json:"author,omitempty"`
	Downloads    int64    `json:"downloads"`
	Likes        int64    `json:"likes"`
	LastModified string   `json:"lastModified,omitempty"`
	PipelineTag  string   `json:"pipelineTag,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Gated        bool     `json:"gated"`
}

type HFFile struct {
	Repo           string `json:"repo"`
	Path           string `json:"path"`
	Name           string `json:"name"`
	Size           int64  `json:"size"`
	OID            string `json:"oid,omitempty"`
	Quantization   string `json:"quantization,omitempty"`
	Recommended    bool   `json:"recommended"`
	Recommendation string `json:"recommendation,omitempty"`
}

type HFDownload struct {
	Active      bool    `json:"active"`
	Verifying   bool    `json:"verifying"`
	Complete    bool    `json:"complete"`
	Canceled    bool    `json:"canceled"`
	Repo        string  `json:"repo,omitempty"`
	File        string  `json:"file,omitempty"`
	Destination string  `json:"destination,omitempty"`
	Received    int64   `json:"received"`
	Total       int64   `json:"total"`
	BytesPerSec float64 `json:"bytesPerSecond"`
	ETASeconds  int64   `json:"etaSeconds,omitempty"`
	Workers     int     `json:"workers,omitempty"`
	Message     string  `json:"message,omitempty"`
	Error       string  `json:"error,omitempty"`
}

type HFState struct {
	Query     string     `json:"query,omitempty"`
	Searching bool       `json:"searching"`
	Repo      string     `json:"repo,omitempty"`
	Results   []HFModel  `json:"results,omitempty"`
	Files     []HFFile   `json:"files,omitempty"`
	Download  HFDownload `json:"download"`
	Error     string     `json:"error,omitempty"`
}

type hfSearchModel struct {
	ID           string          `json:"id"`
	Author       string          `json:"author"`
	Downloads    int64           `json:"downloads"`
	Likes        int64           `json:"likes"`
	LastModified string          `json:"lastModified"`
	PipelineTag  string          `json:"pipeline_tag"`
	Tags         []string        `json:"tags"`
	Gated        json.RawMessage `json:"gated"`
}

type hfTreeEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	LFS  *struct {
		OID  string `json:"oid"`
		Size int64  `json:"size"`
	} `json:"lfs"`
}

type hfRangePlan struct {
	Source    string `json:"source"`
	Size      int64  `json:"size"`
	ChunkSize int64  `json:"chunkSize"`
	OID       string `json:"oid,omitempty"`
	Done      []bool `json:"done"`
}

func hfHTTPClient(timeout time.Duration) *http.Client {
	transport := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   16,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ReadBufferSize:        256 << 10,
		WriteBufferSize:       256 << 10,
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return errors.New("za dużo przekierowań Hugging Face")
			}
			if req.URL.Scheme != "https" && !strings.HasPrefix(huggingFaceBaseURL, "http://127.0.0.1:") {
				return errors.New("Hugging Face próbował przejść na niezabezpieczone HTTP")
			}
			return nil
		},
	}
}

func hfRequest(ctx context.Context, method, target string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "YourLM/"+buildVersion+" (GGUF browser)")
	return req, nil
}

func hfResponseError(response *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
	message := strings.TrimSpace(string(body))
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
		return errors.New("ten model jest prywatny albo wymaga zaakceptowania licencji na Hugging Face")
	}
	if message == "" {
		message = response.Status
	}
	return fmt.Errorf("Hugging Face: %s", message)
}

func hfGated(raw json.RawMessage) bool {
	if len(raw) == 0 || string(raw) == "false" || string(raw) == "null" || string(raw) == `"false"` {
		return false
	}
	return true
}

func searchHuggingFace(ctx context.Context, query string) ([]HFModel, error) {
	values := url.Values{}
	if strings.TrimSpace(query) != "" {
		values.Set("search", strings.TrimSpace(query))
	}
	values.Set("filter", "gguf")
	values.Set("sort", "downloads")
	values.Set("direction", "-1")
	values.Set("limit", "24")
	target := strings.TrimRight(huggingFaceBaseURL, "/") + "/api/models?" + values.Encode()
	req, err := hfRequest(ctx, http.MethodGet, target)
	if err != nil {
		return nil, err
	}
	response, err := hfHTTPClient(25 * time.Second).Do(req)
	if err != nil {
		return nil, fmt.Errorf("nie udało się połączyć z Hugging Face: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, hfResponseError(response)
	}
	var raw []hfSearchModel
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&raw); err != nil {
		return nil, fmt.Errorf("niepoprawna odpowiedź wyszukiwarki Hugging Face: %w", err)
	}
	models := make([]HFModel, 0, len(raw))
	for _, model := range raw {
		if !hfRepoPattern.MatchString(model.ID) {
			continue
		}
		models = append(models, HFModel{
			ID:           model.ID,
			Author:       model.Author,
			Downloads:    model.Downloads,
			Likes:        model.Likes,
			LastModified: model.LastModified,
			PipelineTag:  model.PipelineTag,
			Tags:         model.Tags,
			Gated:        hfGated(model.Gated),
		})
	}
	return models, nil
}

func safeHFPath(value string) bool {
	if value == "" || strings.Contains(value, "\\") || strings.ContainsRune(value, 0) || strings.HasPrefix(value, "/") {
		return false
	}
	clean := path.Clean(value)
	return clean == value && clean != "." && !strings.HasPrefix(clean, "../")
}

func escapeHFPath(value string) string {
	parts := strings.Split(value, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}

func hfQuantization(name string) string {
	match := hfQuantPattern.FindString(strings.ToUpper(name))
	return strings.ToUpper(match)
}

func hfRecommendation(quantization string) (bool, string, int) {
	switch quantization {
	case "Q4_K_M":
		return true, "Najlepszy start: dobry kompromis jakości, RAM i szybkości", 100
	case "Q5_K_M":
		return false, "Wyższa jakość, ale większy plik i zużycie RAM", 90
	case "Q4_K_S":
		return false, "Mniejszy i szybszy niż Q4_K_M, z nieco niższą jakością", 80
	case "Q6_K":
		return false, "Wysoka jakość, duże wymagania pamięciowe", 70
	case "Q3_K_M":
		return false, "Mały plik kosztem jakości", 60
	default:
		return false, "", 0
	}
}

func listHuggingFaceFiles(ctx context.Context, repo string) ([]HFFile, error) {
	if !hfRepoPattern.MatchString(repo) {
		return nil, errors.New("niepoprawny identyfikator repozytorium Hugging Face")
	}
	target := strings.TrimRight(huggingFaceBaseURL, "/") + "/api/models/" + escapeHFPath(repo) + "/tree/main?recursive=true&expand=false"
	req, err := hfRequest(ctx, http.MethodGet, target)
	if err != nil {
		return nil, err
	}
	response, err := hfHTTPClient(30 * time.Second).Do(req)
	if err != nil {
		return nil, fmt.Errorf("nie udało się pobrać listy plików: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, hfResponseError(response)
	}
	var entries []hfTreeEntry
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&entries); err != nil {
		return nil, fmt.Errorf("niepoprawna lista plików Hugging Face: %w", err)
	}
	files := make([]HFFile, 0)
	for _, entry := range entries {
		lower := strings.ToLower(entry.Path)
		if entry.Type != "file" || !safeHFPath(entry.Path) || !strings.HasSuffix(lower, ".gguf") {
			continue
		}
		if strings.Contains(strings.ToLower(filepath.Base(entry.Path)), "-of-") {
			continue
		}
		size, oid := entry.Size, ""
		if entry.LFS != nil {
			if entry.LFS.Size > 0 {
				size = entry.LFS.Size
			}
			oid = strings.ToLower(strings.TrimPrefix(entry.LFS.OID, "sha256:"))
			if len(oid) != 64 {
				oid = ""
			} else if _, decodeErr := hex.DecodeString(oid); decodeErr != nil {
				oid = ""
			}
		}
		if size <= 0 {
			continue
		}
		quantization := hfQuantization(entry.Path)
		recommended, description, _ := hfRecommendation(quantization)
		files = append(files, HFFile{
			Repo:           repo,
			Path:           entry.Path,
			Name:           filepath.Base(entry.Path),
			Size:           size,
			OID:            oid,
			Quantization:   quantization,
			Recommended:    recommended,
			Recommendation: description,
		})
	}
	sort.SliceStable(files, func(i, j int) bool {
		_, _, left := hfRecommendation(files[i].Quantization)
		_, _, right := hfRecommendation(files[j].Quantization)
		if left != right {
			return left > right
		}
		if files[i].Size != files[j].Size {
			return files[i].Size < files[j].Size
		}
		return files[i].Path < files[j].Path
	})
	if len(files) == 0 {
		return nil, errors.New("repozytorium nie zawiera obsługiwanego jednoplikowego GGUF")
	}
	return files, nil
}

func hfDownloadURL(file HFFile) (string, error) {
	if !hfRepoPattern.MatchString(file.Repo) || !safeHFPath(file.Path) || !strings.HasSuffix(strings.ToLower(file.Path), ".gguf") {
		return "", errors.New("niepoprawny plik Hugging Face")
	}
	return strings.TrimRight(huggingFaceBaseURL, "/") + "/" + escapeHFPath(file.Repo) + "/resolve/main/" + escapeHFPath(file.Path) + "?download=true", nil
}

func hfSafeFilename(file HFFile) string {
	repoName := strings.ReplaceAll(file.Repo, "/", "--")
	name := repoName + "--" + filepath.Base(file.Path)
	var clean strings.Builder
	for _, char := range name {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || strings.ContainsRune("._-", char) {
			clean.WriteRune(char)
		} else {
			clean.WriteByte('_')
		}
	}
	value := clean.String()
	if len(value) <= 180 {
		return value
	}
	sum := sha256.Sum256([]byte(file.Repo + "\n" + file.Path))
	base := strings.TrimSuffix(value, ".gguf")
	if len(base) > 150 {
		base = base[:150]
	}
	return base + "--" + hex.EncodeToString(sum[:4]) + ".gguf"
}

func writeJSONAtomic(filename string, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	part := filename + ".new"
	if err := os.WriteFile(part, append(data, '\n'), 0600); err != nil {
		return err
	}
	_ = os.Remove(filename)
	if err := os.Rename(part, filename); err != nil {
		_ = os.Remove(part)
		return err
	}
	return nil
}

func hfReadPlan(filename, source string, size, chunkSize int64, oid string) hfRangePlan {
	count := int((size + chunkSize - 1) / chunkSize)
	plan := hfRangePlan{Source: source, Size: size, ChunkSize: chunkSize, OID: oid, Done: make([]bool, count)}
	data, err := os.ReadFile(filename)
	if err != nil {
		return plan
	}
	var saved hfRangePlan
	if json.Unmarshal(data, &saved) == nil && saved.Source == source && saved.Size == size && saved.ChunkSize == chunkSize && saved.OID == oid && len(saved.Done) == count {
		return saved
	}
	return plan
}

func hfCompletedBytes(plan hfRangePlan) int64 {
	var total int64
	for index, done := range plan.Done {
		if !done {
			continue
		}
		start := int64(index) * plan.ChunkSize
		end := min(plan.Size, start+plan.ChunkSize)
		total += end - start
	}
	return total
}

func hfRequiredDiskBytes(total, completed int64) int64 {
	remaining := max(int64(0), total-completed)
	if remaining == 0 {
		return 0
	}
	reserve := max(int64(512<<20), total/20)
	return remaining + reserve
}

func ensureHFDiskSpace(directory string, total, completed int64) error {
	available, err := availableDiskBytes(directory)
	if err != nil {
		return fmt.Errorf("nie udało się sprawdzić wolnego miejsca: %w", err)
	}
	required := hfRequiredDiskBytes(total, completed)
	if available < uint64(required) {
		return fmt.Errorf(
			"za mało miejsca na model: dostępne %.2f GiB, potrzeba co najmniej %.2f GiB (w tym zapas na bezpieczne ukończenie)",
			float64(available)/(1<<30),
			float64(required)/(1<<30),
		)
	}
	return nil
}

func hfRemoteSize(ctx context.Context, target string, expected int64) (int64, error) {
	req, err := hfRequest(ctx, http.MethodGet, target)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Range", "bytes=0-0")
	req.Header.Set("Accept", "application/octet-stream")
	response, err := hfHTTPClient(45 * time.Second).Do(req)
	if err != nil {
		return 0, fmt.Errorf("nie udało się rozpocząć pobierania: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent {
		if response.StatusCode != http.StatusOK {
			return 0, hfResponseError(response)
		}
		return 0, errors.New("serwer modelu nie obsługuje wznawianego pobierania zakresami")
	}
	contentRange := response.Header.Get("Content-Range")
	const expectedPrefix = "bytes 0-0/"
	if !strings.HasPrefix(contentRange, expectedPrefix) {
		return 0, errors.New("serwer podał niepoprawny zakres próbny pliku GGUF")
	}
	total, err := strconv.ParseInt(strings.TrimPrefix(contentRange, expectedPrefix), 10, 64)
	if err != nil || total <= 0 {
		return 0, errors.New("niepoprawny rozmiar pliku GGUF")
	}
	if expected > 0 && total != expected {
		return 0, fmt.Errorf("rozmiar pliku zmienił się na Hugging Face (%d zamiast %d B)", total, expected)
	}
	return total, nil
}

type hfOffsetWriter struct {
	file        *os.File
	offset      int64
	written     int64
	transferred *atomic.Int64
}

func (writer *hfOffsetWriter) Write(data []byte) (int, error) {
	count, err := writer.file.WriteAt(data, writer.offset)
	writer.offset += int64(count)
	writer.written += int64(count)
	writer.transferred.Add(int64(count))
	return count, err
}

func downloadHFRanges(ctx context.Context, target, temporary, planFile, source, oid string, size, chunkSize int64, workers int, transferred *atomic.Int64) error {
	if size <= 0 || chunkSize <= 0 || workers <= 0 {
		return errors.New("niepoprawny plan pobierania")
	}
	plan := hfReadPlan(planFile, source, size, chunkSize, oid)
	completed := int64(0)
	if info, statErr := os.Stat(temporary); statErr == nil && info.Size() == size {
		completed = hfCompletedBytes(plan)
	}
	if err := ensureHFDiskSpace(filepath.Dir(temporary), size, completed); err != nil {
		return err
	}
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	if info, statErr := file.Stat(); statErr != nil || info.Size() != size {
		if err := file.Truncate(size); err != nil {
			return fmt.Errorf("brak miejsca na model: %w", err)
		}
		for i := range plan.Done {
			plan.Done[i] = false
		}
		if err := writeJSONAtomic(planFile, &plan); err != nil {
			return err
		}
	}
	transferred.Store(hfCompletedBytes(plan))

	tasks := make(chan int)
	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var group sync.WaitGroup
	var planLock sync.Mutex
	var firstErr error
	setError := func(err error) {
		planLock.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		planLock.Unlock()
	}
	client := hfHTTPClient(30 * time.Minute)
	for worker := 0; worker < min(workers, len(plan.Done)); worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range tasks {
				planLock.Lock()
				done := plan.Done[index]
				planLock.Unlock()
				if done {
					continue
				}
				start := int64(index) * chunkSize
				end := min(size, start+chunkSize) - 1
				req, requestErr := hfRequest(workCtx, http.MethodGet, target)
				if requestErr != nil {
					setError(requestErr)
					return
				}
				req.Header.Set("Accept", "application/octet-stream")
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
				response, requestErr := client.Do(req)
				if requestErr != nil {
					if workCtx.Err() != nil {
						setError(workCtx.Err())
					} else {
						setError(requestErr)
					}
					return
				}
				if response.StatusCode != http.StatusPartialContent {
					response.Body.Close()
					setError(fmt.Errorf("zakres %d: serwer odpowiedział %s", index+1, response.Status))
					return
				}
				expectedRange := fmt.Sprintf("bytes %d-%d/%d", start, end, size)
				if response.Header.Get("Content-Range") != expectedRange {
					response.Body.Close()
					setError(fmt.Errorf("zakres %d ma niepoprawny nagłówek Content-Range", index+1))
					return
				}
				length := end - start + 1
				if response.ContentLength >= 0 && response.ContentLength != length {
					response.Body.Close()
					setError(fmt.Errorf("zakres %d ma niepoprawną długość %d zamiast %d B", index+1, response.ContentLength, length))
					return
				}
				writer := &hfOffsetWriter{
					file:        file,
					offset:      start,
					transferred: transferred,
				}
				written, copyErr := io.Copy(writer, io.LimitReader(response.Body, length))
				closeErr := response.Body.Close()
				if copyErr != nil || closeErr != nil || written != length {
					transferred.Add(-writer.written)
					if copyErr == nil {
						copyErr = closeErr
					}
					if copyErr == nil {
						copyErr = io.ErrUnexpectedEOF
					}
					setError(fmt.Errorf("niepełny zakres %d: %w", index+1, copyErr))
					return
				}
				planLock.Lock()
				plan.Done[index] = true
				planErr := writeJSONAtomic(planFile, &plan)
				planLock.Unlock()
				if planErr != nil {
					setError(planErr)
					return
				}
			}
		}()
	}
dispatch:
	for index, done := range plan.Done {
		if done {
			continue
		}
		select {
		case tasks <- index:
		case <-workCtx.Done():
			break dispatch
		}
	}
	close(tasks)
	group.Wait()
	planLock.Lock()
	err = firstErr
	planLock.Unlock()
	if err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return nil
}

func verifyHFSHA256(ctx context.Context, filename, expected string) error {
	expected = strings.ToLower(strings.TrimPrefix(expected, "sha256:"))
	if expected == "" {
		return nil
	}
	if len(expected) != 64 {
		return errors.New("Hugging Face podał niepoprawny identyfikator SHA-256")
	}
	file, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 8<<20)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			_, _ = hash.Write(buffer[:count])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if actual != expected {
		return fmt.Errorf("SHA-256 modelu nie zgadza się z Hugging Face (otrzymano %s)", actual)
	}
	return nil
}

func (a *App) updateHFProgress(download HFDownload) {
	a.Lock()
	if a.hf.Download.Repo == download.Repo && a.hf.Download.File == download.File {
		a.hf.Download = download
	}
	a.Unlock()
}

func (a *App) downloadHuggingFace(ctx context.Context, file HFFile) {
	target, err := hfDownloadURL(file)
	if err != nil {
		a.finishHFDownload(file, "", err, false)
		return
	}
	destination := filepath.Join(a.library, hfSafeFilename(file))
	temporary := destination + ".part"
	planFile := temporary + ".json"
	download := HFDownload{
		Active:      true,
		Repo:        file.Repo,
		File:        file.Path,
		Destination: destination,
		Total:       file.Size,
		Workers:     hfRangeWorkers,
		Message:     "Sprawdzanie rozmiaru i obsługi wznawiania…",
	}
	a.updateHFProgress(download)

	total, err := hfRemoteSize(ctx, target, file.Size)
	if err != nil {
		a.finishHFDownload(file, destination, err, errors.Is(err, context.Canceled))
		return
	}
	download.Total = total
	var transferred atomic.Int64
	resumePlan := hfReadPlan(planFile, file.Repo+"@main/"+file.Path, total, hfRangeChunkSize, file.OID)
	transferred.Store(hfCompletedBytes(resumePlan))
	download.Received = transferred.Load()
	done := make(chan struct{})
	progressStopped := make(chan struct{})
	go func() {
		defer close(progressStopped)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		lastAt, lastBytes := time.Now(), transferred.Load()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				now := time.Now()
				current := transferred.Load()
				seconds := now.Sub(lastAt).Seconds()
				if seconds > 0 {
					download.BytesPerSec = max(float64(0), float64(current-lastBytes)/seconds)
				}
				download.Received = current
				download.Message = "Pobieranie równoległe z możliwością wznowienia"
				if download.BytesPerSec > 0 && total > current {
					download.ETASeconds = int64(float64(total-current) / download.BytesPerSec)
				}
				a.updateHFProgress(download)
				lastAt, lastBytes = now, current
			}
		}
	}()
	err = downloadHFRanges(ctx, target, temporary, planFile, file.Repo+"@main/"+file.Path, file.OID, total, hfRangeChunkSize, hfRangeWorkers, &transferred)
	close(done)
	<-progressStopped
	download.Received = transferred.Load()
	if err == nil {
		download.Verifying = true
		download.Message = "Sprawdzanie SHA-256 pobranego GGUF…"
		a.updateHFProgress(download)
		err = verifyHFSHA256(ctx, temporary, file.OID)
		if err != nil && !errors.Is(err, context.Canceled) {
			_ = os.Remove(temporary)
			_ = os.Remove(planFile)
		}
	}
	if err == nil {
		_ = os.Remove(destination)
		err = os.Rename(temporary, destination)
	}
	if err == nil {
		if _, inspectErr := inspect(destination); inspectErr != nil {
			err = inspectErr
			_ = os.Remove(destination)
			_ = os.Remove(planFile)
		}
	}
	if err == nil {
		_ = os.Remove(planFile)
	}
	a.finishHFDownload(file, destination, err, errors.Is(err, context.Canceled))
}

func (a *App) finishHFDownload(file HFFile, destination string, err error, canceled bool) {
	var model Model
	if err == nil {
		model, err = inspect(destination)
	}
	a.Lock()
	defer a.Unlock()
	a.hfCancel = nil
	download := a.hf.Download
	download.Active = false
	download.Verifying = false
	download.Canceled = canceled
	download.Complete = err == nil
	download.BytesPerSec = 0
	download.ETASeconds = 0
	if canceled {
		download.Message = "Pobieranie zatrzymane. Pobrane części zostały zachowane — możesz wznowić."
		download.Error = ""
	} else if err != nil {
		download.Message = "Pobieranie nie zostało ukończone."
		download.Error = err.Error()
	} else {
		download.Received = download.Total
		download.Message = "Model pobrany, sprawdzony i wybrany. Możesz go uruchomić."
		download.Error = ""
		a.model = model
	}
	a.hf.Download = download
}

func (a *App) huggingFaceError(w http.ResponseWriter, err error, status int) {
	a.Lock()
	a.hf.Searching = false
	a.hf.Error = err.Error()
	a.Unlock()
	http.Error(w, err.Error(), status)
}

func (a *App) huggingFaceStateResponse(w http.ResponseWriter) {
	a.Lock()
	defer a.Unlock()
	respond(w, a.state())
}

func (a *App) huggingFaceAPI(w http.ResponseWriter, r *http.Request, operation string, in input) {
	switch operation {
	case "hf-search":
		query := strings.TrimSpace(in.Query)
		if len(query) > 120 {
			a.huggingFaceError(w, errors.New("zapytanie jest za długie"), http.StatusBadRequest)
			return
		}
		a.Lock()
		a.hf.Query = query
		a.hf.Searching = true
		a.hf.Error = ""
		a.Unlock()
		results, err := searchHuggingFace(r.Context(), query)
		if err != nil {
			a.huggingFaceError(w, err, http.StatusBadGateway)
			return
		}
		a.Lock()
		a.hf.Searching = false
		a.hf.Results = results
		a.hf.Repo = ""
		a.hf.Files = nil
		a.hf.Error = ""
		a.Unlock()
		a.huggingFaceStateResponse(w)

	case "hf-files":
		repo := strings.TrimSpace(in.Repo)
		files, err := listHuggingFaceFiles(r.Context(), repo)
		if err != nil {
			a.huggingFaceError(w, err, http.StatusBadGateway)
			return
		}
		a.Lock()
		a.hf.Repo = repo
		a.hf.Files = files
		a.hf.Error = ""
		a.Unlock()
		a.huggingFaceStateResponse(w)

	case "hf-download":
		repo, filename := strings.TrimSpace(in.Repo), strings.TrimSpace(in.File)
		a.Lock()
		if a.s != nil {
			a.Unlock()
			a.huggingFaceError(w, errors.New("zatrzymaj model przed pobieraniem nowego GGUF"), http.StatusConflict)
			return
		}
		if a.hf.Download.Active {
			a.Unlock()
			a.huggingFaceError(w, errors.New("inny model jest już pobierany"), http.StatusConflict)
			return
		}
		var selected HFFile
		for _, candidate := range a.hf.Files {
			if candidate.Repo == repo && candidate.Path == filename {
				selected = candidate
				break
			}
		}
		if selected.Repo == "" {
			a.Unlock()
			a.huggingFaceError(w, errors.New("najpierw wybierz plik z aktualnej listy Hugging Face"), http.StatusBadRequest)
			return
		}
		if err := os.MkdirAll(a.library, 0700); err != nil {
			a.Unlock()
			a.huggingFaceError(w, err, http.StatusInternalServerError)
			return
		}
		destination := filepath.Join(a.library, hfSafeFilename(selected))
		if info, err := os.Stat(destination); err == nil {
			if info.Size() == selected.Size {
				if model, inspectErr := inspect(destination); inspectErr == nil {
					a.model = model
					a.hf.Download = HFDownload{
						Complete:    true,
						Repo:        selected.Repo,
						File:        selected.Path,
						Destination: destination,
						Received:    selected.Size,
						Total:       selected.Size,
						Message:     "Model był już pobrany i został wybrany.",
					}
					a.hf.Error = ""
					a.Unlock()
					a.huggingFaceStateResponse(w)
					return
				}
			}
			if removeErr := os.Remove(destination); removeErr != nil {
				a.Unlock()
				a.huggingFaceError(w, fmt.Errorf("nie można zastąpić niepełnego modelu: %w", removeErr), http.StatusInternalServerError)
				return
			}
		}
		ctx, cancel := context.WithCancel(context.Background())
		a.hfCancel = cancel
		a.hf.Download = HFDownload{
			Active:      true,
			Repo:        selected.Repo,
			File:        selected.Path,
			Destination: destination,
			Total:       selected.Size,
			Workers:     hfRangeWorkers,
			Message:     "Przygotowywanie szybkiego pobierania…",
		}
		a.hf.Error = ""
		a.Unlock()
		go a.downloadHuggingFace(ctx, selected)
		a.huggingFaceStateResponse(w)

	case "hf-cancel":
		a.Lock()
		cancel := a.hfCancel
		a.Unlock()
		if cancel != nil {
			cancel()
		}
		a.huggingFaceStateResponse(w)
	}
}
