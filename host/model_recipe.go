package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	goldenModelID       = "qwen36-35b-a3b-abliterated-balanced"
	goldenModelRepo     = "huihui-ai/Huihui-Qwen3.6-35B-A3B-abliterated-MTP-GGUF"
	goldenModelQ2       = "Huihui-Qwen3.6-35B-A3B-abliterated-ggml-model-Q2_K.gguf"
	goldenModelQ4       = "Huihui-Qwen3.6-35B-A3B-abliterated-ggml-model-Q4_K.gguf"
	goldenModelFilename = "YourLM-Qwen3.6-35B-A3B-Abliterated-Balanced-Q2Q4.gguf"
	goldenModelRevision = "1fbb5305060b5365c84879612a619d0d2c0b137a"
	goldenModelQ2OID    = "85f446fff19406aa81bd8333433fa11921038ba1b80523c2432d82623b73fb73"
	goldenModelQ4OID    = "63b75afb4b68fc61059c78115b580037074de88146e0762f4a496081e410cf81"
	goldenModelQ2Size   = int64(13246129376)
	goldenModelQ4Size   = int64(21712409824)
	recipeChunkSize     = int64(64 << 20)
)

type CuratedModel struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Repo            string `json:"repo"`
	Output          string `json:"output"`
	Description     string `json:"description"`
	ApproxSizeBytes int64  `json:"approxSizeBytes"`
	Experimental    bool   `json:"experimental"`
}

var curatedModels = []CuratedModel{{
	ID:              goldenModelID,
	Name:            "Qwen3.6-35B-A3B Abliterated · Balanced Q2/Q4",
	Repo:            goldenModelRepo,
	Output:          goldenModelFilename,
	Description:     "Pełny head i tensory wspólne Q4_K, eksperci Q2_K zapisani na końcu pliku dla pagera. MTP pozostaje w modelu, ale domyślnie jest wyłączone do czasu pomiaru na urządzeniu.",
	ApproxSizeBytes: 14 << 30,
	Experimental:    true,
}}

type recipeBufferReader struct {
	data []byte
	pos  int
}

func (reader *recipeBufferReader) take(count uint64) ([]byte, error) {
	if count > uint64(len(reader.data)) || reader.pos < 0 || uint64(reader.pos) > uint64(len(reader.data))-count {
		return nil, io.ErrUnexpectedEOF
	}
	start := reader.pos
	reader.pos += int(count)
	return reader.data[start:reader.pos], nil
}

func (reader *recipeBufferReader) u32() (uint32, error) {
	data, err := reader.take(4)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint32(data), nil
}

func (reader *recipeBufferReader) u64() (uint64, error) {
	data, err := reader.take(8)
	if err != nil {
		return 0, err
	}
	return binary.LittleEndian.Uint64(data), nil
}

func (reader *recipeBufferReader) str(limit uint64) (string, error) {
	length, err := reader.u64()
	if err != nil {
		return "", err
	}
	if length > limit {
		return "", errors.New("niepoprawny ciąg w nagłówku GGUF")
	}
	data, err := reader.take(length)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func recipeScalarSize(valueType uint32) (uint64, bool) {
	sizes := [...]uint64{1, 1, 2, 2, 4, 4, 4, 1, 0, 0, 8, 8, 8}
	if int(valueType) >= len(sizes) || sizes[valueType] == 0 {
		return 0, false
	}
	return sizes[valueType], true
}

func (reader *recipeBufferReader) skipValue(valueType uint32, depth int) error {
	if depth > 2 {
		return errors.New("zbyt głęboka wartość GGUF")
	}
	if valueType == 8 {
		_, err := reader.str(16 << 20)
		return err
	}
	if valueType == 9 {
		elementType, err := reader.u32()
		if err != nil {
			return err
		}
		count, err := reader.u64()
		if err != nil {
			return err
		}
		if count > 1<<28 {
			return errors.New("niepoprawna tablica GGUF")
		}
		if size, ok := recipeScalarSize(elementType); ok {
			_, err = reader.take(size * count)
			return err
		}
		for index := uint64(0); index < count; index++ {
			if err := reader.skipValue(elementType, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	size, ok := recipeScalarSize(valueType)
	if !ok {
		return errors.New("nieznany typ wartości GGUF")
	}
	_, err := reader.take(size)
	return err
}

type recipeTensor struct {
	Name              string
	Dimensions        []uint64
	Type              uint32
	Source            int
	SourceOffset      int64
	Span              int64
	DestinationOffset int64
}

type recipeGGUFHeader struct {
	Version      uint32
	FieldCount   uint64
	KVRaw        []byte
	Alignment    uint64
	Architecture string
	Metadata     map[string][]byte
	DataStart    int64
	Size         int64
	Tensors      []recipeTensor
}

func recipeInteger(data []byte, valueType uint32) (uint64, bool) {
	switch valueType {
	case 0, 7:
		if len(data) >= 1 {
			return uint64(data[0]), true
		}
	case 2:
		if len(data) >= 2 {
			return uint64(binary.LittleEndian.Uint16(data)), true
		}
	case 4:
		if len(data) >= 4 {
			return uint64(binary.LittleEndian.Uint32(data)), true
		}
	case 10:
		if len(data) >= 8 {
			return binary.LittleEndian.Uint64(data), true
		}
	}
	return 0, false
}

func parseRecipeGGUFHeader(data []byte, fileSize int64) (recipeGGUFHeader, error) {
	var header recipeGGUFHeader
	reader := recipeBufferReader{data: data}
	magic, err := reader.take(4)
	if err != nil {
		return header, err
	}
	if string(magic) != "GGUF" {
		return header, errors.New("źródło receptury nie jest plikiem GGUF")
	}
	header.Version, err = reader.u32()
	if err != nil {
		return header, err
	}
	if header.Version != 2 && header.Version != 3 {
		return header, errors.New("receptura wymaga GGUF v2 albo v3")
	}
	tensorCount, err := reader.u64()
	if err != nil {
		return header, err
	}
	if tensorCount == 0 || tensorCount > 1<<20 {
		return header, errors.New("niepoprawna liczba tensorów źródłowego GGUF")
	}
	header.FieldCount, err = reader.u64()
	if err != nil {
		return header, err
	}
	if header.FieldCount > 65536 {
		return header, errors.New("niepoprawna liczba metadanych źródłowego GGUF")
	}
	header.Alignment = 32
	header.Metadata = make(map[string][]byte, header.FieldCount)
	kvStart := reader.pos
	for index := uint64(0); index < header.FieldCount; index++ {
		key, keyErr := reader.str(4096)
		if keyErr != nil {
			return header, keyErr
		}
		valueType, typeErr := reader.u32()
		if typeErr != nil {
			return header, typeErr
		}
		valueStart := reader.pos
		if err := reader.skipValue(valueType, 0); err != nil {
			return header, err
		}
		value := data[valueStart:reader.pos]
		if _, duplicate := header.Metadata[key]; duplicate {
			return header, fmt.Errorf("powtórzony klucz metadanych GGUF: %s", key)
		}
		encodedValue := make([]byte, 4+len(value))
		binary.LittleEndian.PutUint32(encodedValue, valueType)
		copy(encodedValue[4:], value)
		header.Metadata[key] = encodedValue
		if key == "general.alignment" {
			if parsed, ok := recipeInteger(value, valueType); ok {
				header.Alignment = parsed
			}
		}
		if key == "general.architecture" && valueType == 8 {
			valueReader := recipeBufferReader{data: value}
			header.Architecture, err = valueReader.str(128)
			if err != nil {
				return header, err
			}
			header.Architecture = strings.ToLower(strings.TrimSpace(header.Architecture))
		}
	}
	header.KVRaw = append([]byte(nil), data[kvStart:reader.pos]...)
	if _, ok := aligned(0, header.Alignment); !ok {
		return header, errors.New("niepoprawne wyrównanie źródłowego GGUF")
	}
	header.Tensors = make([]recipeTensor, 0, tensorCount)
	for index := uint64(0); index < tensorCount; index++ {
		name, nameErr := reader.str(4096)
		if nameErr != nil {
			return header, nameErr
		}
		dimensions, dimensionErr := reader.u32()
		if dimensionErr != nil {
			return header, dimensionErr
		}
		if dimensions == 0 || dimensions > 8 {
			return header, errors.New("niepoprawny tensor źródłowego GGUF")
		}
		extents := make([]uint64, dimensions)
		for dimension := range extents {
			extents[dimension], err = reader.u64()
			if err != nil {
				return header, err
			}
			if extents[dimension] == 0 {
				return header, errors.New("niepoprawny wymiar tensora źródłowego GGUF")
			}
		}
		tensorType, typeErr := reader.u32()
		offset, offsetErr := reader.u64()
		if typeErr != nil || offsetErr != nil || offset > uint64(^uint64(0)>>1) {
			return header, io.ErrUnexpectedEOF
		}
		header.Tensors = append(header.Tensors, recipeTensor{Name: name, Dimensions: extents, Type: tensorType, SourceOffset: int64(offset)})
	}
	dataStart, ok := aligned(uint64(reader.pos), header.Alignment)
	if !ok || dataStart > uint64(fileSize) {
		return header, errors.New("niepoprawna sekcja danych źródłowego GGUF")
	}
	header.DataStart = int64(dataStart)
	header.Size = fileSize
	sort.SliceStable(header.Tensors, func(i, j int) bool {
		return header.Tensors[i].SourceOffset < header.Tensors[j].SourceOffset
	})
	seen := make(map[string]bool, len(header.Tensors))
	for index := range header.Tensors {
		tensor := &header.Tensors[index]
		if seen[tensor.Name] {
			return header, fmt.Errorf("powtórzona nazwa tensora %q", tensor.Name)
		}
		seen[tensor.Name] = true
		end := fileSize - header.DataStart
		if index+1 < len(header.Tensors) {
			end = header.Tensors[index+1].SourceOffset
		}
		if tensor.SourceOffset < 0 || end <= tensor.SourceOffset || end > fileSize-header.DataStart {
			return header, errors.New("niepoprawny zakres danych tensora GGUF")
		}
		tensor.Span = end - tensor.SourceOffset
	}
	return header, nil
}

func fetchHFRangeBytes(ctx context.Context, target string, start, length, total int64) ([]byte, error) {
	if start < 0 || length <= 0 || total <= 0 || start > total-length {
		return nil, errors.New("niepoprawny zakres źródła GGUF")
	}
	end := start + length - 1
	req, err := hfRequest(ctx, http.MethodGet, target)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
	response, err := hfHTTPClient(10 * time.Minute).Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusPartialContent {
		return nil, fmt.Errorf("Hugging Face odpowiedział %s dla zakresu receptury", response.Status)
	}
	if response.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", start, end, total) {
		return nil, errors.New("Hugging Face zwrócił niepoprawny Content-Range receptury")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, length+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) != length {
		return nil, io.ErrUnexpectedEOF
	}
	return data, nil
}

func goldenDownloadURL(file HFFile) (string, error) {
	if file.Repo != goldenModelRepo || !safeHFPath(file.Path) {
		return "", errors.New("niepoprawne przypięte źródło receptury")
	}
	return strings.TrimRight(huggingFaceBaseURL, "/") + "/" + escapeHFPath(file.Repo) + "/resolve/" + goldenModelRevision + "/" + escapeHFPath(file.Path) + "?download=true", nil
}

func fetchRecipeHeader(ctx context.Context, file HFFile) (recipeGGUFHeader, string, error) {
	target, err := goldenDownloadURL(file)
	if err != nil {
		return recipeGGUFHeader{}, "", err
	}
	total, err := hfRemoteSize(ctx, target, file.Size)
	if err != nil {
		return recipeGGUFHeader{}, "", err
	}
	for size := int64(8 << 20); size <= 256<<20; size *= 2 {
		length := min(size, total)
		data, fetchErr := fetchHFRangeBytes(ctx, target, 0, length, total)
		if fetchErr != nil {
			return recipeGGUFHeader{}, "", fetchErr
		}
		header, parseErr := parseRecipeGGUFHeader(data, total)
		if parseErr == nil {
			return header, target, nil
		}
		if !errors.Is(parseErr, io.ErrUnexpectedEOF) || length == total {
			return recipeGGUFHeader{}, "", parseErr
		}
	}
	return recipeGGUFHeader{}, "", errors.New("nagłówek GGUF jest zbyt duży dla bezpiecznej receptury")
}

func sameDimensions(left, right []uint64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func isExpertTensor(name string) bool {
	return strings.Contains(name, "_exps.")
}

func recipeMetadataMustMatch(key string) bool {
	if key == "general.file_type" || strings.HasPrefix(key, "quantize.") {
		return false
	}
	return strings.HasPrefix(key, "tokenizer.") || strings.HasPrefix(key, "qwen35moe.") || strings.HasPrefix(key, "general.")
}

func validateGoldenSourceFiles(q4File, q2File HFFile) error {
	expected := []struct {
		file HFFile
		path string
		size int64
		oid  string
	}{
		{q4File, goldenModelQ4, goldenModelQ4Size, goldenModelQ4OID},
		{q2File, goldenModelQ2, goldenModelQ2Size, goldenModelQ2OID},
	}
	for _, item := range expected {
		if !validSHA256(item.oid) || item.file.Repo != goldenModelRepo || item.file.Path != item.path || item.file.Size != item.size || !strings.EqualFold(item.file.OID, item.oid) {
			return errors.New("przypięta tożsamość źródła receptury została zmieniona")
		}
	}
	return nil
}

func buildGoldenTensorLayout(q4, q2 recipeGGUFHeader) ([]recipeTensor, error) {
	if q4.Architecture == "" || q4.Architecture != q2.Architecture || q4.Architecture != "qwen35moe" {
		return nil, fmt.Errorf("źródła receptury mają niezgodne architektury (%q i %q)", q4.Architecture, q2.Architecture)
	}
	for key, q4Value := range q4.Metadata {
		if !recipeMetadataMustMatch(key) {
			continue
		}
		q2Value, found := q2.Metadata[key]
		if !found || !bytes.Equal(q4Value, q2Value) {
			return nil, fmt.Errorf("źródła różnią się metadanymi checkpointu: %s", key)
		}
	}
	for key := range q2.Metadata {
		if recipeMetadataMustMatch(key) {
			if _, found := q4.Metadata[key]; !found {
				return nil, fmt.Errorf("źródło Q2_K ma dodatkowe metadane checkpointu: %s", key)
			}
		}
	}
	if len(q4.Tensors) != len(q2.Tensors) {
		return nil, errors.New("źródła mają różne schematy tensorów")
	}
	q2ByName := make(map[string]recipeTensor, len(q2.Tensors))
	q2Experts := make(map[string]recipeTensor)
	for _, tensor := range q2.Tensors {
		tensor.Source = 1
		q2ByName[tensor.Name] = tensor
		if isExpertTensor(tensor.Name) {
			// A llama.cpp MOSTLY_Q2_K file deliberately stores FFN-down
			// tensors as Q3_K while gate/up remain Q2_K. Both are part of the
			// pinned Q2_K recipe; accepting arbitrary higher types would hide a
			// changed quantization policy.
			if tensor.Type != 10 && tensor.Type != 11 { // Q2_K / Q3_K
				return nil, fmt.Errorf("ekspert %s ma nieoczekiwany typ w przypiętym źródle Q2_K", tensor.Name)
			}
			q2Experts[tensor.Name] = tensor
		}
	}
	if len(q2Experts) == 0 {
		return nil, errors.New("źródło Q2_K nie zawiera tensorów ekspertów")
	}
	result := make([]recipeTensor, 0, len(q4.Tensors))
	usedExperts := 0
	for _, tensor := range q4.Tensors {
		tensor.Source = 0
		peer, found := q2ByName[tensor.Name]
		if !found || !sameDimensions(tensor.Dimensions, peer.Dimensions) {
			return nil, fmt.Errorf("niezgodny schemat tensora Q2_K/Q4_K: %s", tensor.Name)
		}
		if isExpertTensor(tensor.Name) {
			replacement, found := q2Experts[tensor.Name]
			if !found || !sameDimensions(tensor.Dimensions, replacement.Dimensions) {
				return nil, fmt.Errorf("brak zgodnego eksperta Q2_K: %s", tensor.Name)
			}
			result = append(result, replacement)
			usedExperts++
			continue
		}
		result = append(result, tensor)
	}
	if usedExperts != len(q2Experts) || usedExperts == 0 {
		return nil, errors.New("zestawy ekspertów Q2_K i Q4_K różnią się")
	}
	// Experts-last is the layout expected by the phone pager and also keeps the
	// full Q4 head/MTP/non-expert prefix contiguous.
	sort.SliceStable(result, func(i, j int) bool {
		leftExpert, rightExpert := isExpertTensor(result[i].Name), isExpertTensor(result[j].Name)
		if leftExpert != rightExpert {
			return !leftExpert
		}
		if result[i].Source != result[j].Source {
			return result[i].Source < result[j].Source
		}
		return result[i].SourceOffset < result[j].SourceOffset
	})
	var offset uint64
	for index := range result {
		alignedOffset, ok := aligned(offset, q4.Alignment)
		if !ok || result[index].Span <= 0 {
			return nil, errors.New("receptura przekroczyła bezpieczny rozmiar GGUF")
		}
		result[index].DestinationOffset = int64(alignedOffset)
		offset = alignedOffset + uint64(result[index].Span)
	}
	return result, nil
}

func writeRecipeString(buffer *bytes.Buffer, value string) {
	_ = binary.Write(buffer, binary.LittleEndian, uint64(len(value)))
	_, _ = buffer.WriteString(value)
}

func writeRecipeStringMetadata(buffer *bytes.Buffer, key, value string) {
	writeRecipeString(buffer, key)
	_ = binary.Write(buffer, binary.LittleEndian, uint32(8))
	writeRecipeString(buffer, value)
}

func buildGoldenHeader(base recipeGGUFHeader, tensors []recipeTensor) ([]byte, int64, error) {
	extraMetadata := [][2]string{
		{"yourlm.recipe", goldenModelID + "/v1"},
		{"yourlm.quantization", "non-experts from Q4_K variant; experts from MOSTLY_Q2_K variant (Q2_K/Q3_K policy); experts-last"},
		{"yourlm.source.repository", goldenModelRepo},
		{"yourlm.source.revision", goldenModelRevision},
		{"yourlm.source.q4_sha256", goldenModelQ4OID},
		{"yourlm.source.q2_sha256", goldenModelQ2OID},
	}
	var buffer bytes.Buffer
	_, _ = buffer.WriteString("GGUF")
	_ = binary.Write(&buffer, binary.LittleEndian, base.Version)
	_ = binary.Write(&buffer, binary.LittleEndian, uint64(len(tensors)))
	_ = binary.Write(&buffer, binary.LittleEndian, base.FieldCount+uint64(len(extraMetadata)))
	_, _ = buffer.Write(base.KVRaw)
	for _, item := range extraMetadata {
		writeRecipeStringMetadata(&buffer, item[0], item[1])
	}
	for _, tensor := range tensors {
		writeRecipeString(&buffer, tensor.Name)
		_ = binary.Write(&buffer, binary.LittleEndian, uint32(len(tensor.Dimensions)))
		for _, extent := range tensor.Dimensions {
			_ = binary.Write(&buffer, binary.LittleEndian, extent)
		}
		_ = binary.Write(&buffer, binary.LittleEndian, tensor.Type)
		_ = binary.Write(&buffer, binary.LittleEndian, uint64(tensor.DestinationOffset))
	}
	dataStart, ok := aligned(uint64(buffer.Len()), base.Alignment)
	if !ok || dataStart > 256<<20 {
		return nil, 0, errors.New("wynikowy nagłówek GGUF jest niepoprawny")
	}
	_, _ = buffer.Write(make([]byte, int(dataStart)-buffer.Len()))
	var dataSize int64
	if len(tensors) > 0 {
		last := tensors[len(tensors)-1]
		dataSize = last.DestinationOffset + last.Span
	}
	if dataSize <= 0 || dataSize > int64(^uint64(0)>>1)-int64(dataStart) {
		return nil, 0, errors.New("wynikowa receptura GGUF ma niepoprawny rozmiar")
	}
	return buffer.Bytes(), int64(dataStart) + dataSize, nil
}

type recipeChunk struct {
	Source            int    `json:"source"`
	SourceOffset      int64  `json:"sourceOffset"`
	DestinationOffset int64  `json:"destinationOffset"`
	Length            int64  `json:"length"`
	Done              bool   `json:"done"`
	SHA256            string `json:"sha256,omitempty"`
}

type recipePlan struct {
	RecipeID    string        `json:"recipeId"`
	Fingerprint string        `json:"fingerprint"`
	Size        int64         `json:"size"`
	HeaderHash  string        `json:"headerHash"`
	Chunks      []recipeChunk `json:"chunks"`
}

func recipeFingerprint(files []HFFile, headers []recipeGGUFHeader) string {
	hash := sha256.New()
	for index, file := range files {
		fmt.Fprintf(hash, "%s\n%s\n%s\n%d\n%s\n", file.Repo, file.Path, file.OID, file.Size, headers[index].Architecture)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func recipeChunks(tensors []recipeTensor, headers []recipeGGUFHeader, outputDataStart int64) []recipeChunk {
	var ranges []recipeChunk
	for _, tensor := range tensors {
		chunk := recipeChunk{
			Source:            tensor.Source,
			SourceOffset:      headers[tensor.Source].DataStart + tensor.SourceOffset,
			DestinationOffset: outputDataStart + tensor.DestinationOffset,
			Length:            tensor.Span,
		}
		if len(ranges) > 0 {
			last := &ranges[len(ranges)-1]
			if last.Source == chunk.Source && last.SourceOffset+last.Length == chunk.SourceOffset && last.DestinationOffset+last.Length == chunk.DestinationOffset {
				last.Length += chunk.Length
				continue
			}
		}
		ranges = append(ranges, chunk)
	}
	var chunks []recipeChunk
	for _, sourceRange := range ranges {
		for offset := int64(0); offset < sourceRange.Length; offset += recipeChunkSize {
			length := min(recipeChunkSize, sourceRange.Length-offset)
			chunks = append(chunks, recipeChunk{
				Source:            sourceRange.Source,
				SourceOffset:      sourceRange.SourceOffset + offset,
				DestinationOffset: sourceRange.DestinationOffset + offset,
				Length:            length,
			})
		}
	}
	return chunks
}

func sameRecipeChunk(left, right recipeChunk) bool {
	return left.Source == right.Source && left.SourceOffset == right.SourceOffset && left.DestinationOffset == right.DestinationOffset && left.Length == right.Length
}

func readRecipePlan(path string, fresh recipePlan) recipePlan {
	data, err := os.ReadFile(path)
	if err != nil {
		return fresh
	}
	var saved recipePlan
	if json.Unmarshal(data, &saved) != nil || saved.RecipeID != fresh.RecipeID || saved.Fingerprint != fresh.Fingerprint || saved.Size != fresh.Size || saved.HeaderHash != fresh.HeaderHash || len(saved.Chunks) != len(fresh.Chunks) {
		return fresh
	}
	for index := range fresh.Chunks {
		if !sameRecipeChunk(saved.Chunks[index], fresh.Chunks[index]) {
			return fresh
		}
		fresh.Chunks[index].Done = saved.Chunks[index].Done
		fresh.Chunks[index].SHA256 = saved.Chunks[index].SHA256
	}
	return fresh
}

func recipeCompletedBytes(plan recipePlan, headerBytes int64) int64 {
	total := headerBytes
	for _, chunk := range plan.Chunks {
		if chunk.Done {
			total += chunk.Length
		}
	}
	return total
}

type recipeOffsetWriter struct {
	file        *os.File
	offset      int64
	written     int64
	transferred *atomic.Int64
}

func (writer *recipeOffsetWriter) Write(data []byte) (int, error) {
	count, err := writer.file.WriteAt(data, writer.offset)
	writer.offset += int64(count)
	writer.written += int64(count)
	writer.transferred.Add(int64(count))
	return count, err
}

func recipeChunkMatches(file *os.File, chunk recipeChunk) bool {
	if !chunk.Done || len(chunk.SHA256) != 64 || chunk.Length <= 0 {
		return false
	}
	hash := sha256.New()
	section := io.NewSectionReader(file, chunk.DestinationOffset, chunk.Length)
	if written, err := io.CopyBuffer(hash, section, make([]byte, 4<<20)); err != nil || written != chunk.Length {
		return false
	}
	return strings.EqualFold(hex.EncodeToString(hash.Sum(nil)), chunk.SHA256)
}

func downloadRecipeChunks(ctx context.Context, urls []string, totals []int64, temporary, planPath string, plan recipePlan, header []byte, workers int, transferred *atomic.Int64) error {
	if len(urls) != len(totals) || workers <= 0 || len(plan.Chunks) == 0 {
		return errors.New("niepoprawny plan receptury GGUF")
	}
	completed := recipeCompletedBytes(plan, int64(len(header)))
	if err := ensureHFDiskSpace(filepath.Dir(temporary), plan.Size, completed); err != nil {
		return err
	}
	file, err := os.OpenFile(temporary, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	reset := false
	if info, statErr := file.Stat(); statErr != nil || info.Size() != plan.Size {
		if err := file.Truncate(plan.Size); err != nil {
			return fmt.Errorf("brak miejsca na model wynikowy: %w", err)
		}
		reset = true
	}
	for index := range plan.Chunks {
		if reset || (plan.Chunks[index].Done && !recipeChunkMatches(file, plan.Chunks[index])) {
			plan.Chunks[index].Done = false
			plan.Chunks[index].SHA256 = ""
		}
	}
	if _, err := file.WriteAt(header, 0); err != nil {
		return err
	}
	if err := writeJSONAtomic(planPath, &plan); err != nil {
		return err
	}
	completed = recipeCompletedBytes(plan, int64(len(header)))
	if err := ensureHFDiskSpace(filepath.Dir(temporary), plan.Size, completed); err != nil {
		return err
	}
	transferred.Store(completed)

	workCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	tasks := make(chan int)
	var group sync.WaitGroup
	var planLock sync.Mutex
	var firstErr error
	setError := func(value error) {
		planLock.Lock()
		if firstErr == nil {
			firstErr = value
			cancel()
		}
		planLock.Unlock()
	}
	client := hfHTTPClient(30 * time.Minute)
	for worker := 0; worker < min(workers, len(plan.Chunks)); worker++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for index := range tasks {
				planLock.Lock()
				done := plan.Chunks[index].Done
				chunk := plan.Chunks[index]
				planLock.Unlock()
				if done {
					continue
				}
				end := chunk.SourceOffset + chunk.Length - 1
				req, requestErr := hfRequest(workCtx, http.MethodGet, urls[chunk.Source])
				if requestErr != nil {
					setError(requestErr)
					return
				}
				req.Header.Set("Accept", "application/octet-stream")
				req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", chunk.SourceOffset, end))
				response, requestErr := client.Do(req)
				if requestErr != nil {
					if workCtx.Err() != nil {
						requestErr = workCtx.Err()
					}
					setError(requestErr)
					return
				}
				expectedRange := fmt.Sprintf("bytes %d-%d/%d", chunk.SourceOffset, end, totals[chunk.Source])
				if response.StatusCode != http.StatusPartialContent || response.Header.Get("Content-Range") != expectedRange {
					response.Body.Close()
					setError(fmt.Errorf("zakres receptury %d ma niepoprawną odpowiedź HTTP", index+1))
					return
				}
				writer := &recipeOffsetWriter{file: file, offset: chunk.DestinationOffset, transferred: transferred}
				chunkHash := sha256.New()
				written, copyErr := io.Copy(io.MultiWriter(writer, chunkHash), io.LimitReader(response.Body, chunk.Length))
				closeErr := response.Body.Close()
				if copyErr != nil || closeErr != nil || written != chunk.Length {
					transferred.Add(-writer.written)
					if copyErr == nil {
						copyErr = closeErr
					}
					if copyErr == nil {
						copyErr = io.ErrUnexpectedEOF
					}
					setError(fmt.Errorf("niepełny zakres receptury %d: %w", index+1, copyErr))
					return
				}
				planLock.Lock()
				plan.Chunks[index].Done = true
				plan.Chunks[index].SHA256 = hex.EncodeToString(chunkHash.Sum(nil))
				planErr := writeJSONAtomic(planPath, &plan)
				planLock.Unlock()
				if planErr != nil {
					setError(planErr)
					return
				}
			}
		}()
	}

dispatch:
	for index, chunk := range plan.Chunks {
		if chunk.Done {
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
	return file.Sync()
}

func calculateRecipeSHA256(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 8<<20)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		count, readErr := file.Read(buffer)
		if count > 0 {
			_, _ = hash.Write(buffer[:count])
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func writeRecipeDigest(path, digest string) error {
	if !validSHA256(digest) {
		return errors.New("niepoprawny SHA-256 modelu wynikowego")
	}
	temporary := path + ".new"
	content := digest + "  " + filepath.Base(strings.TrimSuffix(path, ".sha256")) + "\n"
	if err := os.WriteFile(temporary, []byte(content), 0600); err != nil {
		return err
	}
	_ = os.Remove(path)
	if err := os.Rename(temporary, path); err != nil {
		_ = os.Remove(temporary)
		return err
	}
	return nil
}

func (a *App) updateGoldenProgress(download HFDownload) {
	a.Lock()
	if a.hf.Download.RecipeID == goldenModelID {
		a.hf.Download = download
	}
	a.Unlock()
}

func (a *App) finishGoldenDownload(destination string, err error, canceled bool) {
	var model Model
	if err == nil {
		model, err = inspect(destination)
		if err == nil && !balancedQwen36(model) {
			err = errors.New("zbudowany GGUF nie ma oczekiwanej architektury Qwen3.6 MoE")
		}
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
		download.Message = "Budowanie zatrzymane. Gotowe zakresy zostały zachowane — możesz wznowić."
		download.Error = ""
	} else if err != nil {
		download.Message = "Nie udało się ukończyć modelu Balanced."
		download.Error = err.Error()
	} else {
		download.Received = download.Total
		download.Message = "Model Balanced Q2/Q4 został zbudowany, sprawdzony i wybrany."
		download.Error = ""
		a.model = model
	}
	a.hf.Download = download
}

func (a *App) downloadGoldenModel(ctx context.Context) {
	destination := filepath.Join(a.library, goldenModelFilename)
	temporary := destination + ".part.gguf"
	planPath := temporary + ".json"
	download := HFDownload{
		Active:      true,
		Repo:        goldenModelRepo,
		File:        goldenModelFilename,
		Destination: destination,
		Workers:     hfRangeWorkers,
		Message:     "Odczytuję nagłówki Q4_K i Q2_K bez pobierania obu pełnych modeli…",
		RecipeID:    goldenModelID,
	}
	a.updateGoldenProgress(download)

	q4File := HFFile{Repo: goldenModelRepo, Path: goldenModelQ4, Name: goldenModelQ4, Size: goldenModelQ4Size, OID: goldenModelQ4OID, Quantization: "Q4_K"}
	q2File := HFFile{Repo: goldenModelRepo, Path: goldenModelQ2, Name: goldenModelQ2, Size: goldenModelQ2Size, OID: goldenModelQ2OID, Quantization: "Q2_K"}
	if err := validateGoldenSourceFiles(q4File, q2File); err != nil {
		a.finishGoldenDownload(destination, err, false)
		return
	}
	q4Header, q4URL, err := fetchRecipeHeader(ctx, q4File)
	if err != nil {
		a.finishGoldenDownload(destination, err, errors.Is(err, context.Canceled))
		return
	}
	q2Header, q2URL, err := fetchRecipeHeader(ctx, q2File)
	if err != nil {
		a.finishGoldenDownload(destination, err, errors.Is(err, context.Canceled))
		return
	}
	tensors, err := buildGoldenTensorLayout(q4Header, q2Header)
	if err != nil {
		a.finishGoldenDownload(destination, err, false)
		return
	}
	header, outputSize, err := buildGoldenHeader(q4Header, tensors)
	if err != nil {
		a.finishGoldenDownload(destination, err, false)
		return
	}
	headerDigest := sha256.Sum256(header)
	sources := []HFFile{q4File, q2File}
	headers := []recipeGGUFHeader{q4Header, q2Header}
	freshPlan := recipePlan{
		RecipeID:    goldenModelID,
		Fingerprint: recipeFingerprint(sources, headers),
		Size:        outputSize,
		HeaderHash:  hex.EncodeToString(headerDigest[:]),
		Chunks:      recipeChunks(tensors, headers, int64(len(header))),
	}
	plan := readRecipePlan(planPath, freshPlan)
	download.Total = outputSize
	download.Received = recipeCompletedBytes(plan, int64(len(header)))
	download.Message = "Składam pełny head i wspólne Q4_K z ekspertami Q2_K; pobieranie można wznowić."
	a.updateGoldenProgress(download)

	var transferred atomic.Int64
	transferred.Store(download.Received)
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
				if download.BytesPerSec > 0 && outputSize > current {
					download.ETASeconds = int64(float64(outputSize-current) / download.BytesPerSec)
				}
				a.updateGoldenProgress(download)
				lastAt, lastBytes = now, current
			}
		}
	}()
	err = downloadRecipeChunks(ctx, []string{q4URL, q2URL}, []int64{q4Header.Size, q2Header.Size}, temporary, planPath, plan, header, hfRangeWorkers, &transferred)
	close(done)
	<-progressStopped
	if err == nil {
		download.Verifying = true
		download.Received = outputSize
		download.Message = "Sprawdzam strukturę i metadane złożonego GGUF…"
		a.updateGoldenProgress(download)
		var model Model
		model, err = inspect(temporary)
		if err == nil && !balancedQwen36(model) {
			err = errors.New("wynik receptury nie jest oczekiwanym Qwen3.6 MoE")
		}
	}
	if err == nil {
		download.Message = "Wyliczam publikowalny SHA-256 gotowego modelu…"
		a.updateGoldenProgress(download)
		download.SHA256, err = calculateRecipeSHA256(ctx, temporary)
		if err == nil {
			a.updateGoldenProgress(download)
		}
	}
	if err == nil {
		_ = os.Remove(destination)
		err = os.Rename(temporary, destination)
	}
	if err == nil {
		err = writeRecipeDigest(destination+".sha256", download.SHA256)
	}
	if err == nil {
		_ = os.Remove(planPath)
	}
	a.finishGoldenDownload(destination, err, errors.Is(err, context.Canceled))
}

func (a *App) startGoldenDownload(w http.ResponseWriter) {
	a.Lock()
	if a.s != nil {
		a.Unlock()
		a.huggingFaceError(w, errors.New("zatrzymaj model przed budowaniem nowego GGUF"), http.StatusConflict)
		return
	}
	if a.hf.Download.Active {
		a.Unlock()
		a.huggingFaceError(w, errors.New("inny model jest już pobierany"), http.StatusConflict)
		return
	}
	if err := os.MkdirAll(a.library, 0700); err != nil {
		a.Unlock()
		a.huggingFaceError(w, err, http.StatusInternalServerError)
		return
	}
	destination := filepath.Join(a.library, goldenModelFilename)
	temporary := destination + ".part.gguf"
	if model, err := inspect(destination); err == nil && balancedQwen36(model) {
		digest := ""
		if data, readErr := os.ReadFile(destination + ".sha256"); readErr == nil {
			fields := strings.Fields(string(data))
			if len(fields) > 0 && validSHA256(fields[0]) {
				digest = strings.ToLower(fields[0])
			}
		}
		if digest != "" {
			a.model = model
			a.hf.Download = HFDownload{
				Complete:    true,
				Repo:        goldenModelRepo,
				File:        goldenModelFilename,
				Destination: destination,
				Received:    int64(model.Size),
				Total:       int64(model.Size),
				Message:     "Model Balanced był już gotowy i został wybrany.",
				RecipeID:    goldenModelID,
				SHA256:      digest,
			}
			a.hf.Error = ""
			a.Unlock()
			a.huggingFaceStateResponse(w)
			return
		}
		// A completed model without its digest is an interrupted finalization,
		// not a publishable artifact. Move it back to the resumable path so the
		// background verifier can calculate and atomically publish SHA-256.
		_ = os.Remove(temporary)
		if err := os.Rename(destination, temporary); err != nil {
			a.Unlock()
			a.huggingFaceError(w, fmt.Errorf("nie można dokończyć sumy modelu: %w", err), http.StatusInternalServerError)
			return
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.hfCancel = cancel
	a.hf.Download = HFDownload{
		Active:      true,
		Repo:        goldenModelRepo,
		File:        goldenModelFilename,
		Destination: destination,
		Workers:     hfRangeWorkers,
		Message:     "Przygotowywanie receptury Balanced Q2/Q4…",
		RecipeID:    goldenModelID,
	}
	a.hf.Error = ""
	a.Unlock()
	go a.downloadGoldenModel(ctx)
	a.huggingFaceStateResponse(w)
}
