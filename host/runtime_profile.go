package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

const (
	balancedMoEBias          = "0.005"
	balancedMoEPrefetchK     = 8
	balancedMoEPrefetchAhead = 2
	balancedMoEReaders       = 4
)

// RuntimePlan is the single automatic launch decision shown to the user. It is
// intentionally derived from trusted GGUF metadata and local capabilities, not
// from environment variables or text embedded in a model name.
type RuntimePlan struct {
	ID                 string `json:"id"`
	Label              string `json:"label"`
	Engine             string `json:"engine"`
	Description        string `json:"description"`
	UseSmol            bool   `json:"useSmol"`
	SmolAvailable      bool   `json:"smolAvailable"`
	ContextSize        int    `json:"contextSize"`
	Threads            int    `json:"threads"`
	ComputeMask        string `json:"computeMask,omitempty"`
	ReaderMask         string `json:"readerMask,omitempty"`
	Readers            int    `json:"readers,omitempty"`
	ExpertPool         int    `json:"expertPool,omitempty"`
	CacheReady         bool   `json:"cacheReady,omitempty"`
	CacheRequiredBytes uint64 `json:"cacheRequiredBytes,omitempty"`
	DiskAvailableBytes uint64 `json:"diskAvailableBytes,omitempty"`
	Warning            string `json:"warning,omitempty"`
	cachePrefix        string
}

type runtimeCapabilities struct {
	OS             string
	TotalMemory    uint64
	CPUs           int
	Frequencies    []uint64
	DotProduct     bool
	SmolExecutable bool
	CacheRoot      string
	DiskAvailable  uint64
}

func balancedQwen36(model Model) bool {
	architecture := strings.ToLower(strings.TrimSpace(model.Architecture))
	// Never enable model-specific kernels from a filename alone. These values
	// come from bounded GGUF metadata parsing and describe Qwen3.6-35B-A3B.
	return model.Size >= 10*GiB && model.Recipe == goldenModelID+"/v1" && architecture == "qwen35moe" && model.ExpertCount >= 128 && model.ExpertUsedCount > 0 && model.ExpertUsedCount <= 16
}

func performanceCPUPlan(frequencies []uint64, count int) (threads int, computeMask, readerMask string) {
	if count <= 0 {
		count = 1
	}
	if count > 64 {
		count = 64
	}
	type cpuFrequency struct {
		index int
		value uint64
	}
	cpus := make([]cpuFrequency, count)
	for index := range cpus {
		cpus[index] = cpuFrequency{index: index}
		if index < len(frequencies) {
			cpus[index].value = frequencies[index]
		}
	}
	sort.SliceStable(cpus, func(i, j int) bool {
		if cpus[i].value != cpus[j].value {
			return cpus[i].value > cpus[j].value
		}
		// Android commonly numbers performance cores last. This also makes the
		// no-cpufreq fallback match the measured 0xC0 layout on an 8-core phone.
		return cpus[i].index > cpus[j].index
	})
	threads = min(2, count)
	var compute uint64
	for _, cpu := range cpus[:threads] {
		compute |= uint64(1) << cpu.index
	}
	all := uint64(1)<<count - 1
	readers := all &^ compute
	if readers == 0 {
		readers = compute
	}
	return threads, strings.ToUpper(strconv.FormatUint(compute, 16)), strings.ToUpper(strconv.FormatUint(readers, 16))
}

func smolCachePrefix(root string, model Model) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\n%d\n%d", model.Path, model.Size, model.ModifiedUnixNano)))
	return filepath.Join(root, "qwen36-"+hex.EncodeToString(sum[:8]))
}

func smolCacheReady(prefix string) bool {
	markers, _ := filepath.Glob(prefix + ".*.ok")
	for _, marker := range markers {
		data := strings.TrimSuffix(marker, ".ok")
		if info, err := os.Stat(data); err == nil && info.Mode().IsRegular() && info.Size() > 0 {
			return true
		}
	}
	return false
}

func cacheBuildRequirement(modelSize uint64) uint64 {
	return modelSize + max(uint64(GiB), modelSize/10)
}

func makeRuntimePlan(model Model, capability runtimeCapabilities) RuntimePlan {
	cpus := max(1, capability.CPUs)
	plan := RuntimePlan{
		ID:          "standard",
		Label:       "Standardowy llama.cpp",
		Engine:      "llama.cpp b12562",
		Description: "Uniwersalny profil zgodności dla lokalnego CPU i workerów RPC.",
		ContextSize: 2048,
		Threads:     max(1, cpus/2),
	}
	if !balancedQwen36(model) {
		return plan
	}

	plan.ID = "qwen36-balanced"
	plan.Label = "Qwen3.6 Balanced"
	plan.Description = "Automatyczny profil MoE: mały aktywny podzbiór ekspertów, pełny head i zachowawcza konfiguracja pamięci."
	if capability.OS != "android" {
		plan.Engine = "llama.cpp b12562 · tryb zgodności"
		plan.Warning = "Pager smolmaxxing działa tylko wtedy, gdy ten GGUF jest zapisany i uruchamiany bezpośrednio w aplikacji Android. Worker RPC pozostaje na bezpiecznym silniku standardowym."
		return plan
	}

	plan.SmolAvailable = capability.SmolExecutable && capability.DotProduct
	if !capability.SmolExecutable {
		plan.Warning = "Pakiet nie zawiera eksperymentalnego runtime smolmaxxing; używam silnika standardowego."
		return plan
	}
	if !capability.DotProduct {
		plan.Warning = "Procesor nie zgłasza ARM dot-product (asimddp), więc zoptymalizowany runtime nie zostanie uruchomiony."
		return plan
	}

	plan.UseSmol = true
	plan.Engine = "smolmaxxing 8d12bb1 · llama.cpp v0.8.0"
	plan.Description = "Eksperci są stronicowani z UFS, gorący zestaw zostaje w RAM, a odczyty następnej warstwy są pobierane z wyprzedzeniem."
	plan.Threads, plan.ComputeMask, plan.ReaderMask = performanceCPUPlan(capability.Frequencies, cpus)
	plan.Readers = min(balancedMoEReaders, max(1, cpus-plan.Threads))
	switch {
	case capability.TotalMemory >= 10*GiB:
		plan.ContextSize, plan.ExpertPool = 8192, 96
	case capability.TotalMemory >= 7*GiB:
		plan.ContextSize, plan.ExpertPool = 4096, 80
	case capability.TotalMemory >= 6*GiB:
		plan.ContextSize, plan.ExpertPool = 3072, 64
	default:
		plan.ContextSize, plan.ExpertPool = 2048, 48
	}
	plan.cachePrefix = smolCachePrefix(capability.CacheRoot, model)
	plan.CacheReady = smolCacheReady(plan.cachePrefix)
	plan.DiskAvailableBytes = capability.DiskAvailable
	if !plan.CacheReady {
		plan.CacheRequiredBytes = cacheBuildRequirement(model.Size)
		if capability.DiskAvailable > 0 && capability.DiskAvailable < plan.CacheRequiredBytes {
			plan.Warning = fmt.Sprintf(
				"Pierwszy start potrzebuje około %.1f GiB wolnego miejsca na bezpieczny repack-cache; dostępne jest %.1f GiB.",
				float64(plan.CacheRequiredBytes)/float64(GiB),
				float64(capability.DiskAvailable)/float64(GiB),
			)
		}
	}
	return plan
}

func cpuMaximumFrequencies(count int) []uint64 {
	out := make([]uint64, max(1, count))
	for index := range out {
		paths := []string{
			fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/cpuinfo_max_freq", index),
			fmt.Sprintf("/sys/devices/system/cpu/cpu%d/cpufreq/scaling_max_freq", index),
		}
		for _, path := range paths {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			value, err := strconv.ParseUint(strings.TrimSpace(string(data)), 10, 64)
			if err == nil && value > 0 {
				out[index] = value
				break
			}
		}
	}
	return out
}

func cpuHasDotProduct() bool {
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return false
	}
	text := strings.ToLower(string(data))
	return strings.Contains(text, "asimddp") || strings.Contains(text, "dotprod")
}

func regularExecutable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0111 != 0 && info.Size() > 0
}

func (a *App) modelRuntimePlan(model Model) RuntimePlan {
	memory := systemMemory()
	disk, _ := availableDiskBytes(a.dir)
	cacheRoot := filepath.Join(a.dir, "smol-cache")
	plan := makeRuntimePlan(model, runtimeCapabilities{
		OS:             runtime.GOOS,
		TotalMemory:    memory.Total,
		CPUs:           runtime.NumCPU(),
		Frequencies:    cpuMaximumFrequencies(runtime.NumCPU()),
		DotProduct:     cpuHasDotProduct(),
		SmolExecutable: regularExecutable(a.smolEngine),
		CacheRoot:      cacheRoot,
		DiskAvailable:  disk,
	})
	if plan.UseSmol && len(a.nodes) > 0 {
		plan.UseSmol = false
		plan.Engine = "llama.cpp b12562 · RPC"
		plan.ContextSize = 2048
		plan.Threads = max(1, runtime.NumCPU()/2)
		plan.Warning = "Eksperymentalny pager wymaga modelu i obliczeń bezpośrednio na telefonie. Połączone workery używają zgodnego silnika RPC; odłącz je, aby uruchomić profil smolmaxxing na Androidzie."
	}
	return plan
}

func approvedRuntimeEnvironment(plan RuntimePlan) []string {
	environment := childEnv()
	if !plan.UseSmol {
		return environment
	}
	readers := plan.Readers
	if readers <= 0 {
		readers = balancedMoEReaders
	}
	return append(environment,
		"GGML_MOE_PAGER=1",
		"GGML_REPACK_CACHE="+plan.cachePrefix,
		"GGML_MOE_POOL="+strconv.Itoa(plan.ExpertPool),
		"GGML_MOE_BIAS="+balancedMoEBias,
		"GGML_MOE_PREFETCH_K="+strconv.Itoa(balancedMoEPrefetchK),
		"GGML_MOE_PREFETCH_AHEAD="+strconv.Itoa(balancedMoEPrefetchAhead),
		"GGML_MOE_READERS="+strconv.Itoa(readers),
		"GGML_MOE_READER_CPUS="+plan.ReaderMask,
		"GGML_MOE_STATS=1",
	)
}
