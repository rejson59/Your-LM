package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
)

const (
	diagnosticDiskBytes = int64(256 << 20)
	diagnosticCPUTime   = 1400 * time.Millisecond
)

type DiagnosticGPU struct {
	Name          string `json:"name"`
	MemoryBytes   uint64 `json:"memoryBytes,omitempty"`
	DriverVersion string `json:"driverVersion,omitempty"`
}

type DiagnosticDisk struct {
	Model     string `json:"model"`
	MediaType string `json:"mediaType,omitempty"`
	Interface string `json:"interface,omitempty"`
	Size      uint64 `json:"size,omitempty"`
}

type HardwareDiagnostic struct {
	Manufacturer  string           `json:"manufacturer,omitempty"`
	Model         string           `json:"model,omitempty"`
	OS            string           `json:"os"`
	OSVersion     string           `json:"osVersion,omitempty"`
	CPU           string           `json:"cpu"`
	PhysicalCores int              `json:"physicalCores,omitempty"`
	LogicalCores  int              `json:"logicalCores"`
	MaxMHz        int              `json:"maxMHz,omitempty"`
	Architecture  string           `json:"architecture"`
	RAMTotal      uint64           `json:"ramTotal"`
	RAMAvailable  uint64           `json:"ramAvailable"`
	GPUs          []DiagnosticGPU  `json:"gpus,omitempty"`
	Disks         []DiagnosticDisk `json:"disks,omitempty"`
	Warning       string           `json:"warning,omitempty"`
}

type PhoneDiagnostic struct {
	Name              string   `json:"name"`
	Manufacturer      string   `json:"manufacturer,omitempty"`
	Model             string   `json:"model,omitempty"`
	Device            string   `json:"device,omitempty"`
	Android           string   `json:"android,omitempty"`
	SDK               int      `json:"sdk,omitempty"`
	SoC               string   `json:"soc,omitempty"`
	Board             string   `json:"board,omitempty"`
	ABIs              string   `json:"abis,omitempty"`
	LogicalCores      int      `json:"logicalCores,omitempty"`
	MaxFrequenciesKHz []uint64 `json:"maxFrequenciesKHz,omitempty"`
	DotProduct        bool     `json:"dotProduct"`
	RAMTotal          uint64   `json:"ramTotal,omitempty"`
	RAMAvailable      uint64   `json:"ramAvailable,omitempty"`
	StorageTotal      uint64   `json:"storageTotal,omitempty"`
	StorageFree       uint64   `json:"storageFree,omitempty"`
	BatteryLevel      int      `json:"batteryLevel,omitempty"`
	BatteryTempC      float64  `json:"batteryTempC,omitempty"`
	DiskWriteBps      uint64   `json:"diskWriteBps,omitempty"`
	DiskReadBps       uint64   `json:"diskReadBps,omitempty"`
	Source            string   `json:"source"`
	Warning           string   `json:"warning,omitempty"`
}

type HostBenchmark struct {
	CPUHashBps   uint64 `json:"cpuHashBps,omitempty"`
	CPUWorkers   int    `json:"cpuWorkers,omitempty"`
	DiskWriteBps uint64 `json:"diskWriteBps,omitempty"`
	DiskReadBps  uint64 `json:"diskReadBps,omitempty"`
	DiskBytes    uint64 `json:"diskBytes,omitempty"`
	DiskPath     string `json:"diskPath,omitempty"`
	Warning      string `json:"warning,omitempty"`
}

type DiagnosticLink struct {
	Name      string         `json:"name"`
	Transport string         `json:"transport"`
	WiFi      *LinkBenchmark `json:"wifi,omitempty"`
	USB       *LinkBenchmark `json:"usb,omitempty"`
	Warning   string         `json:"warning,omitempty"`
}

type DiagnosticModelBenchmark struct {
	Running          bool    `json:"running"`
	Complete         bool    `json:"complete"`
	Model            string  `json:"model,omitempty"`
	PromptTokens     int     `json:"promptTokens,omitempty"`
	GeneratedTokens  int     `json:"generatedTokens,omitempty"`
	PromptTokensPerS float64 `json:"promptTokensPerSecond,omitempty"`
	TokensPerSecond  float64 `json:"tokensPerSecond,omitempty"`
	ElapsedMS        int64   `json:"elapsedMs,omitempty"`
	InstructionOK    bool    `json:"instructionOK"`
	Preview          string  `json:"preview,omitempty"`
	Error            string  `json:"error,omitempty"`
}

type AutopilotRecommendation struct {
	SelectedID       string `json:"selectedId"`
	SelectedName     string `json:"selectedName"`
	Reason           string `json:"reason"`
	ReadyToApply     bool   `json:"readyToApply"`
	MaximumCandidate string `json:"maximumCandidate"`
	MaximumStatus    string `json:"maximumStatus"`
	NextStep         string `json:"nextStep"`
}

type DiagnosticsState struct {
	Supported      bool                     `json:"supported"`
	Running        bool                     `json:"running"`
	Complete       bool                     `json:"complete"`
	Revision       int                      `json:"revision"`
	Step           int                      `json:"step"`
	Steps          int                      `json:"steps"`
	Message        string                   `json:"message"`
	Error          string                   `json:"error,omitempty"`
	StartedAt      int64                    `json:"startedAt,omitempty"`
	CompletedAt    int64                    `json:"completedAt,omitempty"`
	Host           HardwareDiagnostic       `json:"host"`
	Phones         []PhoneDiagnostic        `json:"phones,omitempty"`
	Benchmark      HostBenchmark            `json:"benchmark"`
	Links          []DiagnosticLink         `json:"links,omitempty"`
	ModelBenchmark DiagnosticModelBenchmark `json:"modelBenchmark"`
	Recommendation AutopilotRecommendation  `json:"recommendation"`
	Report         string                   `json:"report,omitempty"`
	ReportPath     string                   `json:"reportPath,omitempty"`
}

func newDiagnosticsState() DiagnosticsState {
	return DiagnosticsState{
		Supported: true,
		Steps:     6,
		Message:   "Uruchom test, aby zmierzyć Lenovo, dysk D:, Redmi i połączenia bez wysyłania danych do chmury.",
		Recommendation: AutopilotRecommendation{
			SelectedID:       goldenModelID,
			SelectedName:     "Qwen3.6-35B-A3B Abliterated · Balanced Q2/Q4",
			ReadyToApply:     false,
			Reason:           "To obecny audytowalny profil praktyczny; uruchom pełny test, aby potwierdzić magazyn i ocenić cięższy Coder-Next 80B.",
			MaximumCandidate: "Qwen3-Coder-Next 80B-A3B Abliterated · IQ3_XS experts-last",
			MaximumStatus:    "Czeka na pomiar fizycznego Redmi i nową, osobno zweryfikowaną recepturę pagera.",
			NextStep:         "Uruchom pełny test sprzętu.",
		},
	}
}

func benchmarkCPU(ctx context.Context, duration time.Duration) (uint64, int) {
	workers := min(max(1, runtime.NumCPU()), 8)
	deadline := time.Now().Add(duration)
	var processed atomic.Uint64
	done := make(chan struct{}, workers)
	for worker := 0; worker < workers; worker++ {
		go func(seed byte) {
			defer func() { done <- struct{}{} }()
			buffer := bytes.Repeat([]byte{seed + 1}, 1<<20)
			var local uint64
			for time.Now().Before(deadline) {
				select {
				case <-ctx.Done():
					processed.Add(local)
					return
				default:
				}
				sum := sha256.Sum256(buffer)
				buffer[(int(sum[0])<<12)%len(buffer)] ^= sum[1]
				local += uint64(len(buffer))
			}
			processed.Add(local)
		}(byte(worker))
	}
	started := time.Now()
	for range workers {
		<-done
	}
	elapsed := time.Since(started)
	if elapsed <= 0 {
		return 0, workers
	}
	return uint64(float64(processed.Load()) / elapsed.Seconds()), workers
}

func diagnosticBytesPerSecond(bytes int64, elapsed time.Duration) uint64 {
	if bytes <= 0 {
		return 0
	}
	// A small cached read can complete within one clock tick on Windows. The
	// transfer still happened, so report it against the smallest representable
	// interval instead of returning a misleading zero throughput.
	if elapsed <= 0 {
		elapsed = time.Nanosecond
	}
	return uint64(float64(bytes) / elapsed.Seconds())
}

func benchmarkDisk(ctx context.Context, directory string, size int64) (writeBps, readBps uint64, used int64, err error) {
	if size <= 0 {
		return 0, 0, 0, errors.New("niepoprawny rozmiar testu dysku")
	}
	available, availableErr := availableDiskBytes(directory)
	if availableErr != nil {
		return 0, 0, 0, availableErr
	}
	if available < uint64(size)+512<<20 {
		size = 64 << 20
	}
	if available < uint64(size)+128<<20 {
		return 0, 0, 0, errors.New("za mało wolnego miejsca na bezpieczny test dysku")
	}
	benchDir := filepath.Join(directory, "diagnostics")
	if err := os.MkdirAll(benchDir, 0700); err != nil {
		return 0, 0, 0, err
	}
	path := filepath.Join(benchDir, ".disk-benchmark.tmp")
	_ = os.Remove(path)
	defer os.Remove(path)
	buffer := make([]byte, 4<<20)
	for index := range buffer {
		buffer[index] = byte(index*31 + 17)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return 0, 0, 0, err
	}
	started := time.Now()
	var written int64
	for written < size {
		select {
		case <-ctx.Done():
			file.Close()
			return 0, 0, written, ctx.Err()
		default:
		}
		chunk := min(int64(len(buffer)), size-written)
		count, writeErr := file.Write(buffer[:chunk])
		written += int64(count)
		if writeErr != nil {
			file.Close()
			return 0, 0, written, writeErr
		}
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return 0, 0, written, err
	}
	if err := file.Close(); err != nil {
		return 0, 0, written, err
	}
	writeElapsed := time.Since(started)
	writeBps = diagnosticBytesPerSecond(written, writeElapsed)
	file, err = os.Open(path)
	if err != nil {
		return writeBps, 0, written, err
	}
	started = time.Now()
	var read int64
	var checksum byte
	for {
		select {
		case <-ctx.Done():
			file.Close()
			return writeBps, 0, written, ctx.Err()
		default:
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			read += int64(count)
			checksum ^= buffer[count/2]
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			file.Close()
			return writeBps, 0, written, readErr
		}
	}
	_ = checksum
	if err := file.Close(); err != nil {
		return writeBps, 0, written, err
	}
	readElapsed := time.Since(started)
	readBps = diagnosticBytesPerSecond(read, readElapsed)
	return writeBps, readBps, written, nil
}

func parseKeyValueReport(value string) map[string]string {
	result := make(map[string]string)
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r", ""), "\n") {
		key, item, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok && key != "" {
			result[strings.TrimSpace(key)] = strings.TrimSpace(item)
		}
	}
	return result
}

func parseUnsigned(value string) uint64 {
	result, _ := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	return result
}

func parseADBPhoneReport(value string) PhoneDiagnostic {
	items := parseKeyValueReport(value)
	manufacturer := items["manufacturer"]
	model := items["model"]
	phone := PhoneDiagnostic{
		Manufacturer: manufacturer,
		Model:        model,
		Name:         strings.TrimSpace(manufacturer + " " + model),
		Device:       items["device"],
		Android:      items["android"],
		SDK:          int(parseUnsigned(items["sdk"])),
		Board:        items["board"],
		ABIs:         items["abis"],
		LogicalCores: int(parseUnsigned(items["cores"])),
		DotProduct:   strings.Contains(strings.ToLower(items["features"]), "asimddp") || strings.Contains(strings.ToLower(items["features"]), "dotprod"),
		RAMTotal:     parseUnsigned(items["memory_kib"]) << 10,
		RAMAvailable: parseUnsigned(items["memory_available_kib"]) << 10,
		StorageTotal: parseUnsigned(items["storage_total_kib"]) << 10,
		StorageFree:  parseUnsigned(items["storage_free_kib"]) << 10,
		BatteryLevel: int(parseUnsigned(items["battery_level"])),
		BatteryTempC: float64(parseUnsigned(items["battery_temp_tenths_c"])) / 10,
		Source:       "USB/ADB · inwentaryzacja i kontrolowana próbka pamięci",
	}
	if phone.Name == "" {
		phone.Name = items["device"]
	}
	soc := strings.TrimSpace(items["soc_manufacturer"] + " " + items["soc_model"])
	if soc == "" {
		soc = items["hardware"]
	}
	phone.SoC = soc
	for _, raw := range strings.Split(items["frequencies_khz"], ",") {
		if frequency := parseUnsigned(raw); frequency > 0 {
			phone.MaxFrequenciesKHz = append(phone.MaxFrequenciesKHz, frequency)
		}
	}
	sort.Slice(phone.MaxFrequenciesKHz, func(i, j int) bool { return phone.MaxFrequenciesKHz[i] < phone.MaxFrequenciesKHz[j] })
	return phone
}

func phoneFromWorker(node Node, report []byte) PhoneDiagnostic {
	var snapshot struct {
		Device                string `json:"device"`
		Android               string `json:"android"`
		SDK                   int    `json:"sdk"`
		MemoryTotalBytes      uint64 `json:"memoryTotalBytes"`
		MemoryAvailableBytes  uint64 `json:"memoryAvailableBytes"`
		StorageTotalBytes     uint64 `json:"storageTotalBytes"`
		StorageAvailableBytes uint64 `json:"storageAvailableBytes"`
	}
	_ = json.Unmarshal(report, &snapshot)
	name := snapshot.Device
	if name == "" {
		name = node.Name
	}
	return PhoneDiagnostic{
		Name:         name,
		Model:        snapshot.Device,
		Android:      snapshot.Android,
		SDK:          snapshot.SDK,
		LogicalCores: node.Cores,
		RAMTotal:     max(snapshot.MemoryTotalBytes, node.Total),
		RAMAvailable: max(snapshot.MemoryAvailableBytes, node.Free),
		StorageTotal: snapshot.StorageTotalBytes,
		StorageFree:  snapshot.StorageAvailableBytes,
		Source:       "sparowany worker · szyfrowana diagnostyka",
	}
}

func normalizedPhoneIdentity(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Map(func(character rune) rune {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			return character
		}
		return -1
	}, value)
	for _, prefix := range []string{"xiaomiredmi", "xiaomi", "redmi", "poco"} {
		if strings.HasPrefix(value, prefix) && len(value) > len(prefix) {
			return strings.TrimPrefix(value, prefix)
		}
	}
	return value
}

func phoneIdentities(phone PhoneDiagnostic) []string {
	values := []string{
		phone.Model,
		phone.Device,
		phone.Name,
		strings.TrimSpace(phone.Manufacturer + " " + phone.Model),
	}
	seen := make(map[string]bool)
	identities := make([]string, 0, len(values))
	for _, value := range values {
		identity := normalizedPhoneIdentity(value)
		if identity != "" && !seen[identity] {
			seen[identity] = true
			identities = append(identities, identity)
		}
	}
	return identities
}

func samePhone(first, second PhoneDiagnostic) bool {
	left := phoneIdentities(first)
	right := phoneIdentities(second)
	for _, firstIdentity := range left {
		for _, secondIdentity := range right {
			if firstIdentity == secondIdentity {
				return true
			}
			// Real Xiaomi worker snapshots sometimes prefix the exact regulatory
			// model with a vendor string while getprop returns only the model. A
			// sufficiently long suffix match avoids duplicating that same phone
			// without merging generic names such as "Android".
			if min(len(firstIdentity), len(secondIdentity)) >= 8 &&
				(strings.HasSuffix(firstIdentity, secondIdentity) || strings.HasSuffix(secondIdentity, firstIdentity)) {
				return true
			}
		}
	}
	return false
}

func mergePhone(existing *PhoneDiagnostic, extra PhoneDiagnostic) {
	if existing.Name == "" || len(strings.TrimSpace(extra.Name)) > len(strings.TrimSpace(existing.Name)) {
		existing.Name = extra.Name
	}
	if existing.Manufacturer == "" {
		existing.Manufacturer = extra.Manufacturer
	}
	if existing.Model == "" {
		existing.Model = extra.Model
	}
	if existing.Device == "" {
		existing.Device = extra.Device
	}
	if existing.Android == "" {
		existing.Android = extra.Android
	}
	if existing.SDK == 0 {
		existing.SDK = extra.SDK
	}
	if existing.SoC == "" {
		existing.SoC = extra.SoC
	}
	if existing.Board == "" {
		existing.Board = extra.Board
	}
	if existing.ABIs == "" {
		existing.ABIs = extra.ABIs
	}
	if existing.LogicalCores == 0 {
		existing.LogicalCores = extra.LogicalCores
	}
	if len(extra.MaxFrequenciesKHz) > len(existing.MaxFrequenciesKHz) {
		existing.MaxFrequenciesKHz = append([]uint64(nil), extra.MaxFrequenciesKHz...)
	}
	existing.DotProduct = existing.DotProduct || extra.DotProduct
	existing.RAMTotal = max(existing.RAMTotal, extra.RAMTotal)
	existing.RAMAvailable = max(existing.RAMAvailable, extra.RAMAvailable)
	existing.StorageTotal = max(existing.StorageTotal, extra.StorageTotal)
	existing.StorageFree = max(existing.StorageFree, extra.StorageFree)
	existing.DiskWriteBps = max(existing.DiskWriteBps, extra.DiskWriteBps)
	existing.DiskReadBps = max(existing.DiskReadBps, extra.DiskReadBps)
	if existing.BatteryLevel <= 0 && extra.BatteryLevel > 0 {
		existing.BatteryLevel = extra.BatteryLevel
	}
	if existing.BatteryTempC <= 0 && extra.BatteryTempC > 0 {
		existing.BatteryTempC = extra.BatteryTempC
	}
	if existing.Source == "" {
		existing.Source = extra.Source
	} else if extra.Source != "" && !strings.Contains(existing.Source, extra.Source) {
		existing.Source += " + " + extra.Source
	}
	if existing.Warning == "" {
		existing.Warning = extra.Warning
	} else if extra.Warning != "" && !strings.Contains(existing.Warning, extra.Warning) {
		existing.Warning += " " + extra.Warning
	}
}

func selectedDriveFree(storage StorageState) uint64 {
	for _, drive := range storage.Drives {
		if drive.Selected {
			return drive.Free
		}
	}
	return 0
}

func recommendAutopilot(storage StorageState, host HardwareDiagnostic, phones []PhoneDiagnostic, benchmark HostBenchmark, model DiagnosticModelBenchmark) AutopilotRecommendation {
	recommendation := AutopilotRecommendation{
		SelectedID:       goldenModelID,
		SelectedName:     "Qwen3.6-35B-A3B Abliterated · Balanced Q2/Q4",
		ReadyToApply:     true,
		MaximumCandidate: "Qwen3-Coder-Next 80B-A3B Abliterated · IQ3_XS experts-last",
	}
	facts := []string{}
	if host.RAMTotal > 0 {
		facts = append(facts, fmt.Sprintf("Lenovo ma %.1f GiB fizycznego RAM i %d wątków CPU", float64(host.RAMTotal)/float64(GiB), host.LogicalCores))
	}
	free := selectedDriveFree(storage)
	if free >= 64*GiB {
		facts = append(facts, fmt.Sprintf("magazyn modelu ma %.0f GiB wolnego miejsca", float64(free)/float64(GiB)))
	} else if free > 0 {
		facts = append(facts, fmt.Sprintf("magazyn modelu ma tylko %.0f GiB wolnego miejsca", float64(free)/float64(GiB)))
	}
	if benchmark.DiskReadBps > 0 {
		facts = append(facts, fmt.Sprintf("zmierzony odczyt magazynu to %.0f MiB/s", float64(benchmark.DiskReadBps)/(1<<20)))
	}
	if len(phones) == 0 {
		facts = append(facts, "Redmi nie był dostępny przez USB ani jako worker")
		recommendation.MaximumStatus = "Nie można uczciwie zatwierdzić 80B bez fizycznego RAM-u, pamięci UFS i cech CPU Redmi."
	} else {
		phone := phones[0]
		facts = append(facts, fmt.Sprintf("telefon zgłasza %.1f GiB fizycznego RAM", float64(phone.RAMTotal)/float64(GiB)))
		eligible := free >= 90*GiB && phone.RAMTotal >= 7*GiB && phone.StorageFree >= 65*GiB && phone.DotProduct
		if eligible {
			if model.Complete && model.TokensPerSecond >= 4.5 {
				recommendation.MaximumStatus = "Sprzęt przechodzi wstępne progi 80B, ale Coder-Next nadal wymaga osobnej receptury qwen3next i bezpośredniego pomiaru IQ3_XS; nie zostanie pobrany w ciemno."
			} else {
				recommendation.MaximumStatus = "Sprzęt przechodzi progi pojemności 80B. Najpierw zmierz rzeczywiste tokeny/s modelu 35B; 80B nie zostanie automatycznie wybrane bez tej kontroli jakości i szybkości."
			}
		} else {
			var missing []string
			if free < 90*GiB {
				missing = append(missing, "90 GiB zapasu na dysku hosta")
			}
			if phone.RAMTotal < 7*GiB {
				missing = append(missing, "co najmniej 7 GiB fizycznego RAM telefonu")
			}
			if phone.StorageFree < 65*GiB {
				missing = append(missing, "65 GiB wolnej pamięci telefonu")
			}
			if !phone.DotProduct {
				missing = append(missing, "potwierdzenie ARM dot-product")
			}
			recommendation.MaximumStatus = "80B nie jest teraz praktycznym wyborem: brakuje " + strings.Join(missing, ", ") + "."
		}
	}
	recommendation.Reason = "Autopilot wybiera jeden bezpieczny złoty środek: " + strings.Join(facts, "; ") + ". Nie używa niszczącej pełnej kwantyzacji Q2 dla całego modelu."
	if model.Complete {
		recommendation.NextStep = fmt.Sprintf("Zmierzono %.2f tokena/s. Zachowaj raport; ten wynik posłuży do decyzji o ewentualnym 80B.", model.TokensPerSecond)
	} else {
		recommendation.NextStep = "Zastosuj profil 35B, uruchom model, a potem wykonaj test modelu w tej zakładce."
	}
	if storage.Migration.Status == "fallback" || storage.Migration.Status == "unavailable" {
		recommendation.ReadyToApply = false
		recommendation.NextStep = "Najpierw dokończ bezpieczną migrację na D: zgodnie z komunikatem magazynu, a dopiero potem pobierz profil 35B."
	}
	return recommendation
}

func diagnosticRate(value uint64) string {
	if value == 0 {
		return "brak pomiaru"
	}
	return fmt.Sprintf("%.1f MiB/s", float64(value)/(1<<20))
}

func sanitizeDiagnosticPath(value string) string {
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(strings.ToLower(value), strings.ToLower(home)) {
		return "%USERPROFILE%" + value[len(home):]
	}
	return value
}

func diagnosticsReport(state DiagnosticsState, storage StorageState) string {
	var out strings.Builder
	fmt.Fprintf(&out, "YOURLM %s — RAPORT SPRZĘTU I WYDAJNOŚCI\n", buildVersion)
	fmt.Fprintf(&out, "Wygenerowano: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&out, "Dane pozostawały lokalnie. Raport nie zawiera numeru seryjnego USB, kodu parowania ani klucza API.\n\n")
	fmt.Fprintf(&out, "LENOVO / HOST\n")
	fmt.Fprintf(&out, "Urządzenie: %s %s\n", state.Host.Manufacturer, state.Host.Model)
	fmt.Fprintf(&out, "System: %s %s · architektura %s\n", state.Host.OS, state.Host.OSVersion, state.Host.Architecture)
	fmt.Fprintf(&out, "CPU: %s · rdzenie %d fizyczne / %d logiczne · maks. %d MHz\n", state.Host.CPU, state.Host.PhysicalCores, state.Host.LogicalCores, state.Host.MaxMHz)
	fmt.Fprintf(&out, "RAM: %.2f GiB fizyczne · %.2f GiB dostępne podczas testu\n", float64(state.Host.RAMTotal)/float64(GiB), float64(state.Host.RAMAvailable)/float64(GiB))
	for _, gpu := range state.Host.GPUs {
		fmt.Fprintf(&out, "GPU: %s · pamięć %.2f GiB · sterownik %s\n", gpu.Name, float64(gpu.MemoryBytes)/float64(GiB), gpu.DriverVersion)
	}
	for _, disk := range state.Host.Disks {
		fmt.Fprintf(&out, "Dysk fizyczny: %s · %s/%s · %.1f GiB\n", disk.Model, disk.MediaType, disk.Interface, float64(disk.Size)/float64(GiB))
	}
	fmt.Fprintf(&out, "CPU SHA-256 (%d wątków testu): %s\n", state.Benchmark.CPUWorkers, diagnosticRate(state.Benchmark.CPUHashBps))
	fmt.Fprintf(&out, "Magazyn %s: zapis %s · odczyt ciepły %s · próbka %.0f MiB\n\n", sanitizeDiagnosticPath(state.Benchmark.DiskPath), diagnosticRate(state.Benchmark.DiskWriteBps), diagnosticRate(state.Benchmark.DiskReadBps), float64(state.Benchmark.DiskBytes)/(1<<20))
	fmt.Fprintf(&out, "MAGAZYN YOURLM\n")
	fmt.Fprintf(&out, "Dane: %s\n", sanitizeDiagnosticPath(storage.DataPath))
	fmt.Fprintf(&out, "Program: %s\n", sanitizeDiagnosticPath(storage.InstallPath))
	if storage.Migration.Status != "" {
		fmt.Fprintf(&out, "Migracja: %s · status=%s\n", storage.Migration.Title, storage.Migration.Status)
		if storage.Migration.Detail != "" {
			fmt.Fprintf(&out, "Stan: %s\n", storage.Migration.Detail)
		}
		if storage.Migration.Reconciled {
			fmt.Fprintln(&out, "Scalanie: zachowano wpisy D: i nałożono aktywną bibliotekę C: w nieaktywnym stagingu.")
		}
		if storage.Migration.ResumedFiles > 0 || storage.Migration.ReusedFiles > 0 || storage.Migration.RecoveredParts > 0 || storage.Migration.ReplacedFiles > 0 || storage.Migration.RemovedEntries > 0 {
			fmt.Fprintf(&out, "Odzyskiwanie: wznowiono %d plików / %.2f GiB; użyto %d gotowych; odrzucono %d uszkodzonych części; odświeżono %d plików; uprzątnięto %d wpisów stagingu\n", storage.Migration.ResumedFiles, float64(storage.Migration.ResumedBytes)/float64(GiB), storage.Migration.ReusedFiles, storage.Migration.RecoveredParts, storage.Migration.ReplacedFiles, storage.Migration.RemovedEntries)
		}
		if storage.Migration.VerifiedFiles > 0 {
			fmt.Fprintf(&out, "Weryfikacja: %d plików / %.2f GiB · SHA-256\n", storage.Migration.VerifiedFiles, float64(storage.Migration.VerifiedBytes)/float64(GiB))
		}
		if storage.Migration.SourceRetained {
			fmt.Fprintf(&out, "Źródło zachowane: %s\n", sanitizeDiagnosticPath(storage.Migration.RetainedPath))
		}
		if storage.Migration.Action != "" {
			fmt.Fprintf(&out, "Działanie: %s\n", storage.Migration.Action)
		}
		if storage.Migration.Error != "" {
			fmt.Fprintf(&out, "Szczegóły techniczne: %s\n", storage.Migration.Error)
		}
	}
	if storage.Warning != "" {
		fmt.Fprintf(&out, "Ostrzeżenie magazynu: %s\n", storage.Warning)
	}
	for _, drive := range storage.Drives {
		fmt.Fprintf(&out, "%s %s: %.1f / %.1f GiB wolne%s%s\n", drive.Letter, drive.Label, float64(drive.Free)/float64(GiB), float64(drive.Total)/float64(GiB), map[bool]string{true: " · WYBRANY"}[drive.Selected], map[bool]string{true: " · SYSTEMOWY"}[drive.System])
	}
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "REDMI / TELEFON")
	if len(state.Phones) == 0 {
		fmt.Fprintln(&out, "Telefon nie był dostępny podczas testu.")
	}
	for _, phone := range state.Phones {
		maxFrequency := uint64(0)
		for _, frequency := range phone.MaxFrequenciesKHz {
			maxFrequency = max(maxFrequency, frequency)
		}
		fmt.Fprintf(&out, "%s · Android %s (SDK %d)\n", phone.Name, phone.Android, phone.SDK)
		fmt.Fprintf(&out, "SoC: %s · %d rdzeni · maks. %.2f GHz · dot-product=%v\n", phone.SoC, phone.LogicalCores, float64(maxFrequency)/1e6, phone.DotProduct)
		fmt.Fprintf(&out, "RAM: %.2f GiB fizyczne · %.2f GiB dostępne\n", float64(phone.RAMTotal)/float64(GiB), float64(phone.RAMAvailable)/float64(GiB))
		fmt.Fprintf(&out, "Pamięć: %.1f / %.1f GiB wolne · zapis %s · odczyt ciepły %s\n", float64(phone.StorageFree)/float64(GiB), float64(phone.StorageTotal)/float64(GiB), diagnosticRate(phone.DiskWriteBps), diagnosticRate(phone.DiskReadBps))
		fmt.Fprintf(&out, "Bateria: %d%% · %.1f°C · źródło %s\n", phone.BatteryLevel, phone.BatteryTempC, phone.Source)
		if phone.Warning != "" {
			fmt.Fprintf(&out, "Uwaga: %s\n", phone.Warning)
		}
	}
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "ŁĄCZA")
	if len(state.Links) == 0 {
		fmt.Fprintln(&out, "Brak sparowanego workera do testu szyfrowanego łącza.")
	}
	for _, link := range state.Links {
		fmt.Fprintf(&out, "%s · wybrany %s · Wi-Fi %s · USB %s", link.Name, link.Transport, linkBenchmarkText(link.WiFi), linkBenchmarkText(link.USB))
		if link.Warning != "" {
			fmt.Fprintf(&out, " · %s", link.Warning)
		}
		fmt.Fprintln(&out)
	}
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "MODEL")
	if state.ModelBenchmark.Complete {
		fmt.Fprintf(&out, "%s · %.2f tokena/s · prompt %.2f tokena/s · instrukcja testowa=%v\n", state.ModelBenchmark.Model, state.ModelBenchmark.TokensPerSecond, state.ModelBenchmark.PromptTokensPerS, state.ModelBenchmark.InstructionOK)
	} else {
		fmt.Fprintln(&out, "Rzeczywisty test modelu nie został jeszcze wykonany.")
	}
	fmt.Fprintln(&out)
	fmt.Fprintln(&out, "AUTOPILOT")
	fmt.Fprintf(&out, "Wybrany złoty środek: %s\n", state.Recommendation.SelectedName)
	fmt.Fprintf(&out, "Powód: %s\n", state.Recommendation.Reason)
	fmt.Fprintf(&out, "Kandydat maksimum: %s\n", state.Recommendation.MaximumCandidate)
	fmt.Fprintf(&out, "Status maksimum: %s\n", state.Recommendation.MaximumStatus)
	fmt.Fprintf(&out, "Następny krok: %s\n", state.Recommendation.NextStep)
	return out.String()
}

type diagnosticsRunInput struct {
	dir     string
	storage StorageState
	adb     string
	apk     string
	nodes   []Node
}

func (a *App) updateDiagnosticRun(revision, step int, message string, apply func(*DiagnosticsState)) bool {
	a.Lock()
	defer a.Unlock()
	if a.diagnostics.Revision != revision || !a.diagnostics.Running {
		return false
	}
	if step > 0 {
		a.diagnostics.Step = step
	}
	if message != "" {
		a.diagnostics.Message = message
	}
	if apply != nil {
		apply(&a.diagnostics)
	}
	return true
}

func (a *App) startDiagnostics() error {
	a.Lock()
	if a.diagnostics.Running || a.diagnostics.ModelBenchmark.Running {
		a.Unlock()
		return errors.New("test diagnostyczny już działa")
	}
	if a.s != nil {
		a.Unlock()
		return errors.New("zatrzymaj model przed pełnym testem sprzętu; osobny test modelu uruchomisz po ponownym starcie")
	}
	if a.hf.Download.Active {
		a.Unlock()
		return errors.New("poczekaj na zakończenie pobierania lub budowania modelu")
	}
	if a.diagnosticsCancel != nil {
		a.diagnosticsCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.diagnosticsCancel = cancel
	revision := a.diagnostics.Revision + 1
	previousModel := a.diagnostics.ModelBenchmark
	a.diagnostics = newDiagnosticsState()
	a.diagnostics.Revision = revision
	a.diagnostics.Running = true
	a.diagnostics.ModelBenchmark = previousModel
	a.diagnostics.StartedAt = time.Now().UnixMilli()
	a.diagnostics.Step = 1
	a.diagnostics.Message = "Odczytuję identyfikację Lenovo, Windows, CPU, RAM i GPU…"
	refreshStorageFree(&a.storage)
	input := diagnosticsRunInput{
		dir:     a.dir,
		storage: a.storage,
		adb:     a.adbPath,
		apk:     a.apkPath,
		nodes:   append([]Node(nil), a.nodes...),
	}
	a.Unlock()
	go a.runDiagnostics(ctx, revision, input)
	return nil
}

func (a *App) finishDiagnostics(revision int, runError error) {
	a.Lock()
	defer a.Unlock()
	if a.diagnostics.Revision != revision {
		return
	}
	a.diagnostics.Running = false
	a.diagnostics.CompletedAt = time.Now().UnixMilli()
	if runError != nil {
		if errors.Is(runError, context.Canceled) {
			a.diagnostics.Message = "Test zatrzymany. Częściowe wyniki pozostały widoczne."
		} else {
			a.diagnostics.Error = runError.Error()
			a.diagnostics.Message = "Test zakończył się błędem; częściowe wyniki pozostały widoczne."
		}
		return
	}
	a.diagnostics.Complete = true
	a.diagnostics.Step = a.diagnostics.Steps
	a.diagnostics.Message = "Test zakończony. Autopilot wybrał jeden praktyczny profil i przygotował raport bez sekretów."
}

func (a *App) runDiagnostics(ctx context.Context, revision int, input diagnosticsRunInput) {
	var runError error
	defer func() { a.finishDiagnostics(revision, runError) }()
	host := platformHardwareSnapshot()
	if !a.updateDiagnosticRun(revision, 1, "Inwentaryzacja Lenovo zakończona. Mierzę CPU krótkim, lokalnym testem…", func(state *DiagnosticsState) {
		state.Host = host
	}) {
		return
	}
	if err := ctx.Err(); err != nil {
		runError = err
		return
	}
	cpuRate, workers := benchmarkCPU(ctx, diagnosticCPUTime)
	if !a.updateDiagnosticRun(revision, 2, "CPU zmierzony. Testuję bezpiecznie aktualny magazyn YourLM…", func(state *DiagnosticsState) {
		state.Benchmark.CPUHashBps = cpuRate
		state.Benchmark.CPUWorkers = workers
	}) {
		return
	}
	writeRate, readRate, diskBytes, diskErr := benchmarkDisk(ctx, input.dir, diagnosticDiskBytes)
	if errors.Is(diskErr, context.Canceled) {
		runError = diskErr
		return
	}
	if !a.updateDiagnosticRun(revision, 3, "Dysk zmierzony. Odczytuję Redmi przez autoryzowane USB/ADB lub sparowany worker…", func(state *DiagnosticsState) {
		state.Benchmark.DiskWriteBps = writeRate
		state.Benchmark.DiskReadBps = readRate
		state.Benchmark.DiskBytes = uint64(max(int64(0), diskBytes))
		state.Benchmark.DiskPath = input.dir
		if diskErr != nil {
			state.Benchmark.Warning = diskErr.Error()
		}
	}) {
		return
	}
	usb := inspectUSB(input.adb, input.apk)
	if usb.Supported {
		a.Lock()
		usb.Installing = a.usb.Installing
		a.usb = usb
		a.Unlock()
	}
	phones := collectUSBPhoneDiagnostics(ctx, input.adb, usb.Devices)
	for _, node := range input.nodes {
		report, err := node.diagnostics()
		if err != nil {
			continue
		}
		workerPhone := phoneFromWorker(node, report)
		merged := false
		for index := range phones {
			if samePhone(phones[index], workerPhone) {
				mergePhone(&phones[index], workerPhone)
				merged = true
				break
			}
		}
		if !merged {
			phones = append(phones, workerPhone)
		}
	}
	if !a.updateDiagnosticRun(revision, 4, "Redmi odczytany. Sprawdzam rzeczywiste szyfrowane łącza do sparowanych workerów…", func(state *DiagnosticsState) {
		state.Phones = phones
	}) {
		return
	}
	links := make([]DiagnosticLink, 0, len(input.nodes))
	for _, node := range input.nodes {
		link := DiagnosticLink{Name: node.Name, Transport: node.Transport, WiFi: node.WiFiBenchmark, USB: node.USBBenchmark}
		if ctx.Err() == nil {
			if result, err := node.benchmarkAt(node.Address); err == nil {
				link.WiFi = &result
			} else {
				link.Warning = "Wi-Fi: " + err.Error()
			}
			if node.usbAddress != "" {
				if result, err := node.benchmarkAt(node.usbAddress); err == nil {
					link.USB = &result
				} else {
					link.Warning = strings.TrimSpace(link.Warning + " USB: " + err.Error())
				}
			}
		}
		links = append(links, link)
	}
	if err := ctx.Err(); err != nil {
		runError = err
		return
	}
	if !a.updateDiagnosticRun(revision, 5, "Łącza zmierzone. Autopilot ocenia jakość, pojemność i ryzyko nadmiernej kwantyzacji…", func(state *DiagnosticsState) {
		state.Links = links
	}) {
		return
	}
	a.Lock()
	if a.diagnostics.Revision != revision || !a.diagnostics.Running {
		a.Unlock()
		return
	}
	a.diagnostics.Recommendation = recommendAutopilot(input.storage, a.diagnostics.Host, a.diagnostics.Phones, a.diagnostics.Benchmark, a.diagnostics.ModelBenchmark)
	a.diagnostics.Report = diagnosticsReport(a.diagnostics, input.storage)
	reportPath := filepath.Join(input.dir, "diagnostics", "latest-report.txt")
	if err := os.MkdirAll(filepath.Dir(reportPath), 0700); err == nil {
		if err = os.WriteFile(reportPath, []byte(a.diagnostics.Report), 0600); err == nil {
			a.diagnostics.ReportPath = reportPath
		} else {
			a.diagnostics.Error = "Nie udało się zapisać kopii raportu: " + err.Error()
		}
	}
	a.diagnostics.Step = 6
	a.diagnostics.Message = "Raport gotowy. Nic nie zostało wysłane do Internetu."
	a.Unlock()
}

func modelTimingFloat(raw map[string]any, keys ...string) float64 {
	for _, key := range keys {
		if value, ok := raw[key].(float64); ok {
			return value
		}
	}
	return 0
}

func (a *App) startModelDiagnostic() error {
	a.Lock()
	if a.diagnostics.Running || a.diagnostics.ModelBenchmark.Running {
		a.Unlock()
		return errors.New("inny test już działa")
	}
	session := a.s
	if session == nil || !session.ready {
		a.Unlock()
		return errors.New("najpierw uruchom model i poczekaj na stan Gotowy")
	}
	a.diagnostics.ModelBenchmark = DiagnosticModelBenchmark{Running: true, Model: session.name}
	url, key, modelName := session.url, session.key, session.name
	a.Unlock()
	go a.runModelDiagnostic(url, key, modelName)
	return nil
}

func (a *App) runModelDiagnostic(url, key, modelName string) {
	const expected = "YOURLM-OK-731"
	payload, _ := json.Marshal(map[string]any{
		"model": "yourlm",
		"messages": []map[string]string{{
			"role":    "user",
			"content": "To lokalny test wykonywania instrukcji. Odpowiedz wyłącznie dokładnym tekstem YOURLM-OK-731, bez cudzysłowu, komentarza ani znaków przed lub po.",
		}},
		"temperature": 0,
		"max_tokens":  32,
		"stream":      false,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPost, url+"/v1/chat/completions", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	started := time.Now()
	response, err := localClient(10 * time.Minute).Do(request)
	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Timings map[string]any `json:"timings"`
	}
	if err == nil {
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(io.LimitReader(response.Body, 16<<10))
			err = fmt.Errorf("silnik zwrócił %s: %s", response.Status, strings.TrimSpace(string(body)))
		} else {
			err = json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&result)
		}
	}
	benchmark := DiagnosticModelBenchmark{Model: modelName, ElapsedMS: time.Since(started).Milliseconds()}
	if err != nil {
		benchmark.Error = err.Error()
	} else if len(result.Choices) == 0 {
		benchmark.Error = "silnik nie zwrócił odpowiedzi testowej"
	} else {
		content := strings.TrimSpace(result.Choices[0].Message.Content)
		benchmark.Complete = true
		benchmark.Preview = content
		if len(benchmark.Preview) > 240 {
			benchmark.Preview = benchmark.Preview[:240] + "…"
		}
		benchmark.InstructionOK = content == expected
		benchmark.PromptTokens = int(modelTimingFloat(result.Timings, "prompt_n", "tokens_evaluated"))
		benchmark.GeneratedTokens = int(modelTimingFloat(result.Timings, "predicted_n", "tokens_predicted"))
		benchmark.PromptTokensPerS = modelTimingFloat(result.Timings, "prompt_per_second")
		benchmark.TokensPerSecond = modelTimingFloat(result.Timings, "predicted_per_second")
	}
	a.Lock()
	benchmark.Running = false
	a.diagnostics.ModelBenchmark = benchmark
	a.diagnostics.Recommendation = recommendAutopilot(a.storage, a.diagnostics.Host, a.diagnostics.Phones, a.diagnostics.Benchmark, benchmark)
	if a.diagnostics.Complete {
		a.diagnostics.Report = diagnosticsReport(a.diagnostics, a.storage)
		path := filepath.Join(a.dir, "diagnostics", "latest-report.txt")
		if writeErr := os.WriteFile(path, []byte(a.diagnostics.Report), 0600); writeErr == nil {
			a.diagnostics.ReportPath = path
		}
	}
	a.Unlock()
}

func (a *App) diagnosticsAPI(w http.ResponseWriter, op string) {
	var err error
	switch op {
	case "diagnostics-start":
		err = a.startDiagnostics()
	case "diagnostics-cancel":
		a.Lock()
		if a.diagnosticsCancel != nil {
			a.diagnosticsCancel()
		}
		a.Unlock()
	case "diagnostics-model":
		err = a.startModelDiagnostic()
	default:
		http.Error(w, "Nieznana operacja diagnostyczna", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	a.Lock()
	defer a.Unlock()
	respond(w, a.state())
}
