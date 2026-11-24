package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestChatTokenLimit(t *testing.T) {
	if got, err := chatTokenLimit(0); err != nil || got != 64 {
		t.Fatalf("default chat limit=%d, %v", got, err)
	}
	if got, err := chatTokenLimit(128); err != nil || got != 128 {
		t.Fatalf("explicit chat limit=%d, %v", got, err)
	}
	for _, invalid := range []int{-1, 513} {
		if _, err := chatTokenLimit(invalid); err == nil {
			t.Fatalf("accepted invalid chat limit %d", invalid)
		}
	}
}

func TestChatStreamsEngineResponse(t *testing.T) {
	requestSeen := make(chan error, 1)
	engine := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer engine-key" || r.Header.Get("Accept") != "text/event-stream" {
			requestSeen <- fmt.Errorf("unexpected engine headers: %#v", r.Header)
			return
		}
		var payload struct {
			MaxTokens     int `json:"max_tokens"`
			Stream        bool
			StreamOptions struct {
				IncludeUsage bool `json:"include_usage"`
			} `json:"stream_options"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			requestSeen <- err
			return
		}
		if payload.MaxTokens != 64 || !payload.Stream || !payload.StreamOptions.IncludeUsage {
			requestSeen <- fmt.Errorf("unexpected engine payload: %+v", payload)
			return
		}
		requestSeen <- nil
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Działa\"}}]}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer engine.Close()

	app := &App{s: &Session{ready: true, url: engine.URL, key: "engine-key"}}
	var in input
	if err := json.Unmarshal([]byte(`{"maxTokens":64,"messages":[{"role":"user","content":"test"}]}`), &in); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/chat", nil)
	response := httptest.NewRecorder()
	app.chat(response, request, in)
	select {
	case err := <-requestSeen:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("chat proxy did not reach the engine")
	}
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "text/event-stream") || !response.Flushed {
		t.Fatalf("unexpected chat response: status=%d flushed=%v headers=%v", response.Code, response.Flushed, response.Header())
	}
	body := response.Body.String()
	if !strings.Contains(body, `"content":"Działa"`) || !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("streamed response was not proxied: %q", body)
	}
}

func TestMetadata(t *testing.T) {
	var b bytes.Buffer
	put := func(x any) {
		if e := binary.Write(&b, binary.LittleEndian, x); e != nil {
			t.Fatal(e)
		}
	}
	str := func(s string) { put(uint64(len(s))); b.WriteString(s) }
	b.WriteString("GGUF")
	put(uint32(3))
	put(uint64(2))
	put(uint64(2))
	str("tokenizer.ggml.tokens")
	put(uint32(9))
	put(uint32(8))
	put(uint64(2))
	str("token")
	str("token2")
	str("llama.block_count")
	put(uint32(4))
	put(uint32(32))
	tensorInfoStart := b.Len()
	str("small.weight")
	put(uint32(1))
	put(uint64(16))
	put(uint32(0))
	put(uint64(0))
	str("large.weight")
	put(uint32(1))
	put(uint64(32))
	put(uint32(0))
	put(uint64(64))
	for b.Len()%32 != 0 {
		b.WriteByte(0)
	}
	b.Write(make([]byte, 64+128))
	path := filepath.Join(t.TempDir(), "test.gguf")
	os.WriteFile(path, b.Bytes(), 0600)
	m, e := inspect(path)
	if e != nil || m.Layers != 32 || m.LargestTensorBytes != 128 {
		t.Fatalf("%+v %v", m, e)
	}
	os.WriteFile(path, b.Bytes()[:tensorInfoStart+1], 0600)
	if _, e = inspect(path); e == nil {
		t.Fatal("accepted truncated GGUF")
	}
}
func TestVersionOrdering(t *testing.T) {
	for _, test := range []struct {
		candidate, current string
		newer              bool
	}{
		{"0.2.0", "0.1.2", true},
		{"v0.2.1", "0.2.0", true},
		{"0.2.0", "0.2.0-rc1", true},
		{"0.2.0-rc2", "0.2.0-rc1", true},
		{"0.2.0-rc10", "0.2.0-rc2", true},
		{"0.2.0-rc11", "0.2.0-rc10", true},
		{"0.2.0-rc12", "0.2.0-rc11", true},
		{"0.2.0-rc13", "0.2.0-rc12", true},
		{"0.2.0-rc14", "0.2.0-rc13", true},
		{"0.2.0-rc15", "0.2.0-rc14", true},
		{"0.2.0-rc16", "0.2.0-rc15", true},
		{"0.2.0-rc17", "0.2.0-rc16", true},
		{"0.2.0-rc18", "0.2.0-rc17", true},
		{"0.2.0-rc19", "0.2.0-rc18", true},
		{"0.2.0-rc1", "0.2.0", false},
		{"0.1.9", "0.2.0", false},
		{"0.2.0", "0.2.0", false},
	} {
		if got := newerVersion(test.candidate, test.current); got != test.newer {
			t.Fatalf("newerVersion(%q, %q)=%v, want %v", test.candidate, test.current, got, test.newer)
		}
	}
}

func TestEngineArgumentsDoNotBlockLargeModels(t *testing.T) {
	containsPair := func(args []string, key, value string) bool {
		for i := 0; i+1 < len(args); i++ {
			if args[i] == key && args[i+1] == value {
				return true
			}
		}
		return false
	}
	contains := func(args []string, value string) bool {
		for _, arg := range args {
			if arg == value {
				return true
			}
		}
		return false
	}

	cpu := engineArguments(`C:\models\large.gguf`, "127.0.0.1", 8080, "key", nil, 0)
	if !containsPair(cpu, "--fit", "off") {
		t.Fatalf("CPU-only launch must bypass the physical-RAM fit rejection: %q", cpu)
	}
	if contains(cpu, "--fit-target") || contains(cpu, "--rpc") {
		t.Fatalf("CPU-only launch unexpectedly has an RPC memory target: %q", cpu)
	}

	rpc := engineArguments(`C:\models\large.gguf`, "127.0.0.1", 8080, "key", []string{"127.0.0.1:50052"}, 2048)
	for key, value := range map[string]string{
		"--rpc":        "127.0.0.1:50052",
		"--split-mode": "layer",
		"--fit":        "on",
		"--fit-target": "2048",
	} {
		if !containsPair(rpc, key, value) {
			t.Fatalf("RPC launch missing %s %s: %q", key, value, rpc)
		}
	}
	for _, forbidden := range []string{"-ngl", "--n-gpu-layers", "--tensor-split"} {
		if contains(rpc, forbidden) {
			t.Fatalf("RPC launch pins placement with %s: %q", forbidden, rpc)
		}
	}
}

func TestWorkerFitTargetAccountsForTensorLoadingPeak(t *testing.T) {
	for _, test := range []struct {
		largest uint64
		want    int
	}{
		{0, 2048},
		{208 << 20, 2048},
		{300 << 20, 2304},
		{512 << 20, 3072},
	} {
		if got := workerFitTargetMiB(test.largest); got != test.want {
			t.Fatalf("workerFitTargetMiB(%d)=%d, want %d", test.largest, got, test.want)
		}
	}
}

func TestWorkerMemoryGuardRequiresThreeConsecutiveLowSamples(t *testing.T) {
	node := Node{Platform: "android", Alive: true, Total: 8 * GiB, Free: workerMemoryGuardFloor - 1}
	samples := 0
	for i := 1; i <= workerMemoryGuardSamples; i++ {
		var stop bool
		samples, stop = advanceWorkerMemoryGuard(samples, node, nil, true)
		if stop != (i == workerMemoryGuardSamples) {
			t.Fatalf("sample %d stop=%v, want %v", i, stop, i == workerMemoryGuardSamples)
		}
	}
	node.Free = workerMemoryGuardFloor
	if samples, stop := advanceWorkerMemoryGuard(samples, node, nil, true); samples != 0 || stop {
		t.Fatalf("recovered memory did not reset guard: samples=%d stop=%v", samples, stop)
	}
	if samples, stop := advanceWorkerMemoryGuard(2, node, errors.New("probe failed"), true); samples != 0 || stop {
		t.Fatalf("failed probe armed guard: samples=%d stop=%v", samples, stop)
	}
	node.Free = workerMemoryGuardFloor - 1
	if samples, stop := advanceWorkerMemoryGuard(2, node, nil, false); samples != 0 || stop {
		t.Fatalf("completed load did not disable guard: samples=%d stop=%v", samples, stop)
	}
}

func TestEngineProgressPhases(t *testing.T) {
	for _, test := range []struct {
		log, want string
		ready     bool
	}{
		{"", "Uruchamianie silnika llama.cpp", false},
		{"srv load_model: loading model", "Odczyt metadanych i tokenizera GGUF", false},
		{"tensor overrides to CPU are used with mmap", "Planowanie podziału pamięci", false},
		{"load_tensors: offloaded 8 layers", "Ładowanie i przesyłanie wag", false},
		{"llama_kv_cache: size", "Tworzenie kontekstu i buforów", false},
		{"model loaded", "Finalizowanie lokalnego API", false},
		{"anything", "Model gotowy", true},
	} {
		if got := enginePhase(test.log, test.ready); got != test.want {
			t.Fatalf("enginePhase(%q, %v)=%q, want %q", test.log, test.ready, got, test.want)
		}
	}
	progress := engineProgress("load_tensors: loading", false)
	if progress.Step != 4 || progress.Steps != 6 || progress.Ready {
		t.Fatalf("unexpected tensor-loading progress: %+v", progress)
	}
	ready := engineProgress("", true)
	if ready.Step != ready.Steps || !ready.Ready {
		t.Fatalf("ready progress is not complete: %+v", ready)
	}
	if got := advanceEngineProgress(progress, engineProgress("", false)); got.Step != progress.Step {
		t.Fatalf("engine progress regressed: %+v", got)
	}
	if got := advanceEngineProgress(progress, ready); !got.Ready {
		t.Fatalf("ready state did not replace prior progress: %+v", got)
	}
	if got := cleanEngineLog("one\r\ntwo\x00\rthree"); got != "one\ntwo\nthree" {
		t.Fatalf("cleanEngineLog returned %q", got)
	}
	path := filepath.Join(t.TempDir(), "engine.log")
	if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := engineLogTail(path, 4); got != "6789" {
		t.Fatalf("engineLogTail returned %q", got)
	}
}

func TestCompactEngineLogKeepsFailureAndBoundsRepetition(t *testing.T) {
	var log strings.Builder
	log.WriteString("[YourLM] worker 192.168.0.2 transport=usb\n")
	if home, err := os.UserHomeDir(); err == nil {
		fmt.Fprintf(&log, "load_model: loading model '%s/models/test.gguf'\n", home)
		fmt.Fprintf(&log, "load_model: loading model '%s/models/case-test.gguf'\n", strings.ToUpper(home))
	}
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&log, "[set_tensor] block=%d\n", i)
	}
	log.WriteString("Remote RPC server crashed or returned malformed response\n")
	compact := compactEngineLog(log.String())
	if !strings.Contains(compact, "transport=usb") || !strings.Contains(compact, "Remote RPC server crashed") {
		t.Fatalf("compact engine report lost key evidence: %s", compact)
	}
	if home, err := os.UserHomeDir(); err == nil && (strings.Contains(compact, home) || strings.Contains(compact, strings.ToUpper(home))) {
		t.Fatalf("compact engine report exposed the user profile path: %s", compact)
	}
	if strings.Count(compact, "[set_tensor]") > 24 || len(compact) > 12<<10 {
		t.Fatalf("compact engine report retained repetitive transfer lines: bytes=%d", len(compact))
	}
}

func TestRuntimeIntegrity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.exe")
	body := []byte("known executable fixture")
	if e := os.WriteFile(path, body, 0600); e != nil {
		t.Fatal(e)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(body))
	if e := verifyProgram(path, want); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(path, append(body, '!'), 0600); e != nil {
		t.Fatal(e)
	}
	if e := verifyProgram(path, want); e == nil {
		t.Fatal("accepted modified runtime")
	}
	if e := verifyProgram(path, ""); e == nil {
		t.Fatal("accepted runtime without a pinned digest")
	}
}

func TestMemoryAccounting(t *testing.T) {
	stats := systemMemory()
	if stats.Total == 0 || stats.Available > stats.Total || stats.Used > stats.Total {
		t.Fatalf("invalid system memory snapshot: %+v", stats)
	}
	process := memoryForProcess(os.Getpid())
	if process.Resident == 0 {
		t.Fatalf("current process has no resident-memory measurement: %+v", process)
	}
}

func TestChildEnvironment(t *testing.T) {
	t.Setenv("LLAMA_ARG_AGENT", "1")
	t.Setenv("GGML_BACKEND_PATH", "untrusted")
	t.Setenv("YOURLM_TEST_SAFE", "kept")
	joined := "\n" + strings.Join(childEnv(), "\n") + "\n"
	if strings.Contains(joined, "\nLLAMA_ARG_AGENT=") || strings.Contains(joined, "\nGGML_BACKEND_PATH=") {
		t.Fatal("runtime control variable leaked into child")
	}
	if !strings.Contains(joined, "\nYOURLM_TEST_SAFE=kept\n") || !strings.Contains(joined, "\nGGML_RPC_NO_COMM=1\n") {
		t.Fatal("safe or mandatory child variable missing")
	}
}

func TestWorkerRequiresPatchedRuntime(t *testing.T) {
	valid := Node{Build: rpcWorkerBuild, Total: 8 * GiB, Free: 4 * GiB, Cores: 8, Alive: true}
	if err := validateWorkerInfo(valid); err != nil {
		t.Fatal(err)
	}
	valid.Build = "b12562"
	if err := validateWorkerInfo(valid); err == nil || !strings.Contains(err.Error(), "zaktualizuj") {
		t.Fatalf("old worker was not rejected with update guidance: %v", err)
	}
	valid.Build = rpcWorkerBuild
	valid.Alive = false
	if err := validateWorkerInfo(valid); err == nil || !strings.Contains(err.Error(), "uruchom worker ponownie") {
		t.Fatalf("stopped native worker was accepted: %v", err)
	}
	valid.Recovering = true
	valid.RestartAttempts = 2
	if err := validateWorkerInfo(valid); err == nil || !strings.Contains(err.Error(), "próba 2") {
		t.Fatalf("worker recovery was not signaled: %v", err)
	}
}

func TestRemoveStaleRPCParts(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "rpc")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(nested, ".incoming-7.part")
	complete := filepath.Join(nested, "0123456789abcdef")
	if err := os.WriteFile(partial, []byte("partial"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(complete, []byte("complete"), 0600); err != nil {
		t.Fatal(err)
	}
	removeStaleRPCParts(root)
	if _, err := os.Stat(partial); !os.IsNotExist(err) {
		t.Fatalf("stale partial cache was not removed: %v", err)
	}
	if _, err := os.Stat(complete); err != nil {
		t.Fatalf("complete cache was removed: %v", err)
	}
}

func TestEngineFailureMessage(t *testing.T) {
	rpc := engineFailureMessage(errors.New("exit status 3"), "Remote RPC server crashed or returned malformed response")
	if !strings.Contains(rpc, "workerem RPC") || !strings.Contains(rpc, "Redmi") {
		t.Fatalf("RPC failure is not actionable: %q", rpc)
	}
	generic := engineFailureMessage(errors.New("exit status 2"), "other failure")
	if !strings.Contains(generic, "exit status 2") {
		t.Fatalf("generic failure lost its cause: %q", generic)
	}
}

func TestRPCTrafficCounter(t *testing.T) {
	payload := bytes.Repeat([]byte("rpc"), 4096)
	var output bytes.Buffer
	var transferred atomic.Uint64
	copyRPC(&output, bytes.NewReader(payload), &transferred)
	if !bytes.Equal(output.Bytes(), payload) || transferred.Load() != uint64(len(payload)) {
		t.Fatalf("RPC copy counted %d bytes for %d-byte payload", transferred.Load(), len(payload))
	}
}

func TestLocalClientRejectsRedirect(t *testing.T) {
	visited := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { visited = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.RedirectHandler(target.URL, http.StatusFound))
	defer redirect.Close()
	res, e := localClient(time.Second).Get(redirect.URL)
	if e != nil {
		t.Fatal(e)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusFound || visited {
		t.Fatal("local client followed a redirect")
	}
}

func TestPanelBoundary(t *testing.T) {
	h := guard("127.0.0.1:3030", "test", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, x := range []struct {
		host, origin, key string
		want              int
	}{{"127.0.0.1:3030", "", "test", 204}, {"elsewhere", "", "test", 403}, {"127.0.0.1:3030", "https://other", "test", 403}, {"127.0.0.1:3030", "", "", 403}} {
		r := httptest.NewRequest("GET", "http://"+x.host+"/api/state", nil)
		r.Header.Set("Origin", x.origin)
		r.Header.Set("X-YourLM", x.key)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != x.want {
			t.Fatal(w.Code)
		}
	}
}
func TestCode(t *testing.T) {
	suffix := "|" + strings.Repeat("aa", 32) + "|" + strings.Repeat("bb", 24)
	for _, bad := range []string{"YLM1|1.1.1.1:44" + suffix, "YLM1|192.168.1.2:0" + suffix, "YLM1|host:80" + suffix, "YLM1|192.168.0.1:123|oops"} {
		if _, e := parseCode(bad); e == nil {
			t.Fatal("accepted", bad)
		}
	}
	if _, e := parseCode("YLM1|192.168.1.4:1234" + suffix); e != nil {
		t.Fatal(e)
	}
}

// Real TLS with the production desktop worker handshake, then an actual TCP relay.
func testNode(t *testing.T, rpc string) Node {
	return testNodeWithSecret(t, rpc, token())
}

func testNodeWithSecret(t *testing.T, rpc, secret string) Node {
	t.Helper()
	temp := httptest.NewTLSServer(nil)
	cfg := temp.TLS
	temp.Close()
	l, e := tls.Listen("tcp4", "127.0.0.1:0", cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { l.Close() })
	o := Offer{secret: secret, slots: make(chan struct{}, 16), local: rpc}
	go func() {
		for {
			c, e := l.Accept()
			if e != nil {
				return
			}
			o.slots <- struct{}{}
			go o.serve(c)
		}
	}()
	return Node{Address: l.Addr().String(), pin: fmt.Sprintf("%x", sha256.Sum256(cfg.Certificates[0].Certificate[0])), secret: o.secret}
}
func TestManualPairing(t *testing.T) {
	key := "ABCDE-FGHJK-LMNPQ-RSTIV"
	canonical, err := canonicalManual(key)
	// I and O are deliberately absent so they cannot be confused with digits.
	if err == nil {
		t.Fatal("accepted ambiguous manual key", canonical)
	}
	key = "ABCDE-FGHJK-LMNPQ-RSTVW"
	canonical, err = canonicalManual(key)
	if err != nil {
		t.Fatal(err)
	}
	n := testNodeWithSecret(t, "127.0.0.1:1", derivedSessionSecret(canonical))
	pair, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pair.Close()
	go func() {
		for {
			c, err := pair.Accept()
			if err != nil {
				return
			}
			go serveManualPair(c, canonical, n.Address, n.pin)
		}
	}()
	got, err := pairManual(pair.Addr().String(), strings.ToLower(key))
	if err != nil {
		t.Fatal(err)
	}
	if got.Address != n.Address || got.pin != n.pin || got.secret != derivedSessionSecret(canonical) {
		t.Fatalf("unexpected manual pairing result: %#v", got)
	}
	if err = got.refresh(); err != nil {
		t.Fatal(err)
	}
	if _, err = pairManual(pair.Addr().String(), "ABCDE-FGHJK-LMNPQ-RSTVX"); err == nil || !strings.Contains(err.Error(), "nie pasuje") {
		t.Fatal("wrong key was not rejected", err)
	}
}

func TestWorkerDiagnosticsRetriesAfterPreferredEndpointEOF(t *testing.T) {
	temp := httptest.NewTLSServer(nil)
	config := temp.TLS
	temp.Close()
	wifi, err := tls.Listen("tcp4", "127.0.0.1:0", config)
	if err != nil {
		t.Fatal(err)
	}
	defer wifi.Close()
	usb, err := tls.Listen("tcp4", "127.0.0.1:0", config)
	if err != nil {
		t.Fatal(err)
	}
	defer usb.Close()
	secret := token()
	offer := Offer{secret: secret, slots: make(chan struct{}, 16), local: "127.0.0.1:1"}
	go func() {
		for {
			connection, acceptErr := wifi.Accept()
			if acceptErr != nil {
				return
			}
			offer.slots <- struct{}{}
			go offer.serve(connection)
		}
	}()
	go func() {
		for {
			connection, acceptErr := usb.Accept()
			if acceptErr != nil {
				return
			}
			go func() {
				defer connection.Close()
				_, _ = bufio.NewReader(connection).ReadString('\n')
			}()
		}
	}()
	node := Node{
		Address:    wifi.Addr().String(),
		usbAddress: usb.Addr().String(),
		Transport:  "usb",
		pin:        fmt.Sprintf("%x", sha256.Sum256(config.Certificates[0].Certificate[0])),
		secret:     secret,
	}
	report, err := node.diagnostics()
	if err != nil || !bytes.Contains(report, []byte(rpcWorkerBuild)) {
		t.Fatalf("diagnostics did not retry Wi-Fi after USB EOF: %s, %v", report, err)
	}
}

func TestWorkerDiagnosticsUsesCachedSnapshot(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	received := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	cached := []byte(`{"workerBuild":"cached-test","rpcAlive":true,"memoryAvailableBytes":614088704,"exitHistory":[{"reason":"LOW_MEMORY"}]}`)
	report, support := workerDiagnosticsReports(
		[]Node{{Name: "Redmi", Address: address, Allocation: 1500 << 20, SwapBytes: 3 * GiB}},
		map[string]WorkerDiagnosticSnapshot{
			address: {report: cached, receivedAt: received},
		},
	)
	for _, expected := range []string{
		"Nie udało się pobrać raportu po awarii",
		"Ostatnia udana migawka pobrana przez Windows podczas ładowania: 2026-11-20T12:00:00Z",
		"Ocena pamięci: ostatnia migawka miała 0.57 GiB dostępnego RAM",
		"proces PSS/RSS 1.46 GiB + swap 3.00 GiB",
		`"workerBuild":"cached-test"`,
	} {
		if !strings.Contains(report, expected) {
			t.Fatalf("cached worker report is missing %q: %s", expected, report)
		}
	}
	if !strings.Contains(support, "SKRÓT DIAGNOSTYKI WORKERÓW") || !strings.Contains(support, "cached-test") || strings.Contains(support, `"exitHistory"`) {
		t.Fatalf("unexpected support report: %s", support)
	}
}

func TestCompactWorkerDiagnosticKeepsSignalsWithoutRepeatingStacks(t *testing.T) {
	var native strings.Builder
	for i := 0; i < 200; i++ {
		fmt.Fprintf(&native, "[set_tensor] tensor block %d transferred\n", i)
	}
	native.WriteString("fatal: remote allocation failed at block 200\n")
	report, err := json.Marshal(map[string]any{
		"device": "Xiaomi test", "android": "16", "workerBuild": rpcWorkerBuild,
		"rpcAlive": true, "rpcSwapBytes": uint64(3 * GiB), "memoryAvailableBytes": uint64(600 << 20),
		"nativeLogBytes": native.Len(), "nativeLog": native.String(),
		"workerEvents": "100 native_start worker\n101 forwarding_error socket closed\n at java.net.Socket.read\n102 native_exit exit_137_SIGKILL\n",
		"exitHistory":  []map[string]any{{"process": "pl.yourlm.node", "reason": "LOW_MEMORY", "timestampEpochMs": 99}},
	})
	if err != nil {
		t.Fatal(err)
	}
	compact := compactWorkerDiagnostic(report)
	for _, expected := range []string{"Xiaomi test", rpcWorkerBuild, "remote allocation failed", "exit_137_SIGKILL", "LOW_MEMORY", `"swapBytes": 3221225472`} {
		if !strings.Contains(compact, expected) {
			t.Fatalf("compact report is missing %q: %s", expected, compact)
		}
	}
	if strings.Contains(compact, "at java.net.Socket.read") || strings.Count(compact, "tensor block") > 30 || len(compact) > 12<<10 {
		t.Fatalf("compact report retained repetitive details: bytes=%d\n%s", len(compact), compact)
	}
}

func TestMonitorNodesCachesDiagnostics(t *testing.T) {
	node := testNode(t, "127.0.0.1:1")
	session := &Session{
		workers:       1,
		nodes:         []Node{node},
		workerReports: make(map[string]WorkerDiagnosticSnapshot),
		done:          make(chan struct{}),
	}
	a := &App{nodes: []Node{node}, s: session}
	finished := make(chan struct{})
	go func() {
		a.monitorNodes(session)
		close(finished)
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		a.Lock()
		snapshot, ok := session.workerReports[node.Address]
		a.Unlock()
		if ok {
			if len(snapshot.report) == 0 || snapshot.receivedAt.IsZero() {
				t.Fatalf("invalid cached worker snapshot: %+v", snapshot)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("monitor did not cache an authenticated worker diagnostic snapshot")
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.Lock()
	a.s = nil
	a.Unlock()
	close(session.done)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("worker monitor did not stop")
	}
}

func TestPairAndBridge(t *testing.T) {
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	go func() {
		c, e := l.Accept()
		if e == nil {
			defer c.Close()
			io.Copy(c, c)
		}
	}()
	n := testNode(t, l.Addr().String())
	if e := n.refresh(); e != nil {
		t.Fatal(e)
	}
	diagnostic, e := n.diagnostics()
	if e != nil || !bytes.Contains(diagnostic, []byte(`"workerBuild"`)) || !bytes.Contains(diagnostic, []byte(rpcWorkerBuild)) {
		t.Fatalf("worker diagnostics unavailable: %s, %v", diagnostic, e)
	}
	combined := workerDiagnosticsReport([]Node{n}, nil)
	if !strings.Contains(combined, "DIAGNOSTYKA WORKERÓW") || !strings.Contains(combined, rpcWorkerBuild) {
		t.Fatalf("combined diagnostics missing worker data: %s", combined)
	}
	benchmark, e := n.benchmarkAt(n.Address)
	if e != nil || benchmark.UploadBps == 0 || benchmark.DownloadBps == 0 || benchmark.ScoreBps == 0 || benchmark.LatencyMicros == 0 {
		t.Fatalf("authenticated transport benchmark failed: %+v, %v", benchmark, e)
	}
	unauthenticated := n
	unauthenticated.secret = "wrong"
	if _, e = unauthenticated.benchmarkAt(unauthenticated.Address); e == nil {
		t.Fatal("transport benchmark accepted an incorrect session secret")
	}
	fallback := n
	fallback.Transport = "usb"
	fallback.usbAddress = "127.0.0.1:1"
	if e = fallback.refresh(); e != nil || fallback.Transport != "wifi" || !strings.Contains(fallback.TransportReason, "awaryjnego") {
		t.Fatalf("USB failure did not fall back to authenticated Wi-Fi: %+v, %v", fallback, e)
	}
	bad := n
	bad.pin = strings.Repeat("00", 32)
	if bad.refresh() == nil {
		t.Fatal("accepted incorrect pin")
	}
	bad = n
	bad.secret = "wrong"
	if bad.refresh() == nil {
		t.Fatal("accepted incorrect token")
	}
	var sent, received atomic.Uint64
	bridge, e := n.bridge(&sent, &received)
	if e != nil {
		t.Fatal(e)
	}
	defer bridge.Close()
	c, e := net.Dial("tcp", bridge.Addr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(time.Second * 5))
	fmt.Fprintln(c, "tensor bytes")
	v, e := bufio.NewReader(c).ReadString('\n')
	if e != nil || v != "tensor bytes\n" {
		t.Fatal(v, e)
	}
	countDeadline := time.Now().Add(time.Second)
	for (sent.Load() == 0 || received.Load() == 0) && time.Now().Before(countDeadline) {
		time.Sleep(time.Millisecond)
	}
	if sent.Load() == 0 || received.Load() == 0 {
		t.Fatalf("bridge did not count bidirectional traffic: sent=%d received=%d", sent.Load(), received.Load())
	}
}

// Optional integration: external RPC worker + the real llama-server, native or emulated.
func TestRealCluster(t *testing.T) {
	exe, model, rpc := os.Getenv("TEST_ENGINE"), os.Getenv("TEST_MODEL"), os.Getenv("TEST_RPC")
	if exe == "" || model == "" || rpc == "" {
		t.Skip("requires a llama.cpp server, worker, and tiny GGUF")
	}
	m, e := inspect(model)
	if e != nil {
		t.Fatal(e)
	}
	a := App{dir: t.TempDir(), engine: exe, model: m, nodes: []Node{testNode(t, rpc)}}
	p, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	port := p.Addr().(*net.TCPAddr).Port
	p.Close()
	a.Lock()
	e = a.start("127.0.0.1", port)
	s := a.s
	a.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	defer func() { s.cmd.Process.Kill(); <-s.done }()
	until := time.Now().Add(30 * time.Second)
	for {
		a.Lock()
		ready := s.ready
		a.Unlock()
		if ready {
			break
		}
		if time.Now().After(until) {
			b, _ := os.ReadFile(filepath.Join(a.dir, "engine.log"))
			t.Fatalf("engine not ready: %s", b)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if s.layers < 1 || s.layers >= m.Layers {
		t.Fatal("missing split")
	}
	tools, _ := http.NewRequest("GET", s.url+"/tools", nil)
	tools.Header.Set("Authorization", "Bearer "+s.key)
	toolsRes, e := (&http.Client{Timeout: 5 * time.Second}).Do(tools)
	if e != nil {
		t.Fatal(e)
	}
	toolsBody, _ := io.ReadAll(io.LimitReader(toolsRes.Body, 1<<20))
	toolsRes.Body.Close()
	for _, dangerous := range []string{"exec_shell_command", "write_file", "edit_file"} {
		if bytes.Contains(toolsBody, []byte(dangerous)) {
			t.Fatalf("dangerous server tool enabled: %s", dangerous)
		}
	}
	t.Logf("server tools disabled: status %d", toolsRes.StatusCode)
	r, _ := http.NewRequest("POST", s.url+"/v1/chat/completions", strings.NewReader(`{"model":"yourlm","messages":[{"role":"user","content":"test"}],"max_tokens":3}`))
	r.Header.Set("Authorization", "Bearer "+s.key)
	r.Header.Set("Content-Type", "application/json")
	res, e := (&http.Client{Timeout: 30 * time.Second}).Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer res.Body.Close()
	var reply struct{ Choices []json.RawMessage }
	if e = json.NewDecoder(res.Body).Decode(&reply); e != nil || res.StatusCode != 200 || len(reply.Choices) != 1 {
		t.Fatal("chat failed", res.Status, e)
	}
	t.Logf("real GGUF chat: %d/%d worker layers via pinned TLS", s.layers, m.Layers)
}
