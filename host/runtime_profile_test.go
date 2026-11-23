package main

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

func balancedTestModel(path string) Model {
	return Model{
		Path:             path,
		Name:             "Qwen3.6-35B-A3B.gguf",
		Size:             14 * GiB,
		ModifiedUnixNano: 1234,
		Architecture:     "qwen35moe",
		Recipe:           goldenModelID + "/v1",
		ExpertCount:      256,
		ExpertUsedCount:  8,
	}
}

func TestBalancedPlanIsMetadataGated(t *testing.T) {
	model := balancedTestModel("/models/balanced.gguf")
	capability := runtimeCapabilities{
		OS:             "android",
		TotalMemory:    8 * GiB,
		CPUs:           8,
		Frequencies:    []uint64{1000, 1100, 1200, 1300, 1400, 1500, 2800, 3000},
		DotProduct:     true,
		SmolExecutable: true,
		CacheRoot:      t.TempDir(),
		DiskAvailable:  40 * GiB,
	}
	plan := makeRuntimePlan(model, capability)
	if !plan.UseSmol || plan.ID != "qwen36-balanced" || plan.ContextSize != 4096 || plan.ExpertPool != 80 {
		t.Fatalf("unexpected balanced plan: %+v", plan)
	}
	if plan.Threads != 2 || plan.ComputeMask != "C0" || plan.ReaderMask != "3F" {
		t.Fatalf("unexpected CPU placement: %+v", plan)
	}
	model.Recipe = ""
	if plan := makeRuntimePlan(model, capability); plan.UseSmol || plan.ID != "standard" {
		t.Fatalf("filename/architecture enabled an unmarked model-specific pager: %+v", plan)
	}
}

func TestBalancedRuntimeEnvironmentIsFixed(t *testing.T) {
	t.Setenv("GGML_MOE_POOL", "999")
	t.Setenv("GGML_MOE_ROUTER16", "1")
	t.Setenv("LLAMA_ARG_N_GPU_LAYERS", "999")
	plan := RuntimePlan{UseSmol: true, ExpertPool: 80, ReaderMask: "3F", cachePrefix: "/private/cache"}
	environment := approvedRuntimeEnvironment(plan)
	joined := "\n" + strings.Join(environment, "\n") + "\n"
	for _, forbidden := range []string{"\nGGML_MOE_POOL=999\n", "\nGGML_MOE_ROUTER16=1\n", "\nLLAMA_ARG_N_GPU_LAYERS=999\n"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("untrusted environment survived: %q", forbidden)
		}
	}
	for _, required := range []string{"\nGGML_MOE_POOL=80\n", "\nGGML_MOE_BIAS=0.005\n", "\nGGML_REPACK_CACHE=/private/cache\n", "\nGGML_MOE_READER_CPUS=3F\n"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("missing approved setting: %q", required)
		}
	}
}

func TestBalancedArgumentsKeepMTPDisabled(t *testing.T) {
	plan := RuntimePlan{UseSmol: true, ContextSize: 4096, Threads: 2, ComputeMask: "C0"}
	arguments := engineArgumentsForPlan("model.gguf", "127.0.0.1", 8080, "secret", nil, 0, plan)
	joined := " " + strings.Join(arguments, " ") + " "
	for _, required := range []string{" --ctx-size 4096 ", " -t 2 ", " -tb 2 ", " -C C0 ", " --cpu-strict 1 ", " -fa on ", " --fit off "} {
		if !strings.Contains(joined, required) {
			t.Fatalf("missing argument %q in %q", required, joined)
		}
	}
	if strings.Contains(joined, "--spec-type") || strings.Contains(joined, "draft-mtp") {
		t.Fatalf("MTP was enabled without a device benchmark: %q", joined)
	}
}

func TestGoldenRecipeRejectsDifferentCheckpoint(t *testing.T) {
	metadata := map[string][]byte{
		"general.architecture":   {8, 0, 0, 0, 9, 0, 0, 0, 0, 0, 0, 0, 'q', 'w', 'e', 'n', '3', '5', 'm', 'o', 'e'},
		"tokenizer.ggml.model":   {8, 0, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 'b', 'p', 'e'},
		"qwen35moe.expert_count": {4, 0, 0, 0, 0, 1, 0, 0},
	}
	cloneMetadata := func() map[string][]byte {
		out := make(map[string][]byte, len(metadata))
		for key, value := range metadata {
			out[key] = append([]byte(nil), value...)
		}
		return out
	}
	q4 := recipeGGUFHeader{
		Architecture: "qwen35moe",
		Alignment:    32,
		Metadata:     cloneMetadata(),
		Tensors: []recipeTensor{
			{Name: "blk.0.attn.weight", Dimensions: []uint64{8, 8}, Type: 12, SourceOffset: 0, Span: 96},
			{Name: "blk.0.ffn_down_exps.weight", Dimensions: []uint64{8, 8, 256}, Type: 12, SourceOffset: 96, Span: 224},
		},
	}
	q2 := q4
	q2.Metadata = cloneMetadata()
	q2.Tensors = []recipeTensor{
		{Name: "blk.0.attn.weight", Dimensions: []uint64{8, 8}, Type: 10, SourceOffset: 0, Span: 64},
		{Name: "blk.0.ffn_down_exps.weight", Dimensions: []uint64{8, 8, 256}, Type: 11, SourceOffset: 64, Span: 128},
	}
	layout, err := buildGoldenTensorLayout(q4, q2)
	if err != nil {
		t.Fatal(err)
	}
	if len(layout) != 2 || layout[0].Source != 0 || layout[1].Source != 1 || layout[1].DestinationOffset != 96 {
		t.Fatalf("wrong Q4/non-expert + Q2/expert layout: %+v", layout)
	}
	q2.Metadata["tokenizer.ggml.model"] = append([]byte(nil), q2.Metadata["tokenizer.ggml.model"]...)
	q2.Metadata["tokenizer.ggml.model"][len(q2.Metadata["tokenizer.ggml.model"])-1] = 'x'
	if _, err := buildGoldenTensorLayout(q4, q2); err == nil {
		t.Fatal("accepted two different tokenizer/checkpoint metadata sets")
	}
}

func TestPinnedGoldenRemoteHeaders(t *testing.T) {
	if os.Getenv("TEST_HF_GOLDEN") != "1" {
		t.Skip("set TEST_HF_GOLDEN=1 for the immutable remote-header gate")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	q4File := HFFile{Repo: goldenModelRepo, Path: goldenModelQ4, Name: goldenModelQ4, Size: goldenModelQ4Size, OID: goldenModelQ4OID, Quantization: "Q4_K"}
	q2File := HFFile{Repo: goldenModelRepo, Path: goldenModelQ2, Name: goldenModelQ2, Size: goldenModelQ2Size, OID: goldenModelQ2OID, Quantization: "Q2_K"}
	if err := validateGoldenSourceFiles(q4File, q2File); err != nil {
		t.Fatal(err)
	}
	q4Header, _, err := fetchRecipeHeader(ctx, q4File)
	if err != nil {
		t.Fatal(err)
	}
	q2Header, _, err := fetchRecipeHeader(ctx, q2File)
	if err != nil {
		t.Fatal(err)
	}
	layout, err := buildGoldenTensorLayout(q4Header, q2Header)
	if err != nil {
		t.Fatal(err)
	}
	header, size, err := buildGoldenHeader(q4Header, layout)
	if err != nil {
		t.Fatal(err)
	}
	if len(layout) == 0 || len(header) == 0 || size <= int64(len(header)) || size >= goldenModelQ4Size {
		t.Fatalf("unexpected mixed artifact dimensions: tensors=%d header=%d size=%d", len(layout), len(header), size)
	}
}

func TestGoldenHeaderCarriesAuditMetadata(t *testing.T) {
	base := recipeGGUFHeader{Version: 3, FieldCount: 0, Alignment: 32}
	tensors := []recipeTensor{{Name: "output.weight", Dimensions: []uint64{8, 8}, Type: 12, Span: 64}}
	header, size, err := buildGoldenHeader(base, tensors)
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(header))+64 {
		t.Fatalf("unexpected output size %d for header %d", size, len(header))
	}
	for _, value := range [][]byte{[]byte("yourlm.recipe"), []byte(goldenModelRevision), []byte(goldenModelQ4OID), []byte(goldenModelQ2OID)} {
		if !bytes.Contains(header, value) {
			t.Fatalf("header lacks audit value %q", value)
		}
	}
}
