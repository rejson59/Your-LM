package main

// MemorySnapshot separates physical RAM from the system-wide commit/page-file
// pool. A memory-mapped GGUF is not capped by Available: the OS can evict clean
// pages and read them from the model again, while private buffers consume
// commit.
type MemorySnapshot struct {
	Total           uint64 `json:"total"`
	Available       uint64 `json:"available"`
	Used            uint64 `json:"used"`
	CommitLimit     uint64 `json:"commitLimit"`
	CommitAvailable uint64 `json:"commitAvailable"`
	PageFile        uint64 `json:"pageFile"`
}

type ProcessMemory struct {
	Resident  uint64 `json:"resident"`
	Committed uint64 `json:"committed"`
}

func memory() (total, free uint64) {
	stats := systemMemory()
	return stats.Total, stats.Available
}
