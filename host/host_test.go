package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
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
	put(uint64(20))
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
	path := filepath.Join(t.TempDir(), "test.gguf")
	os.WriteFile(path, b.Bytes(), 0600)
	m, e := inspect(path)
	if e != nil || m.Layers != 32 {
		t.Fatalf("%+v %v", m, e)
	}
	os.WriteFile(path, b.Bytes()[:b.Len()-1], 0600)
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

	cpu := engineArguments(`C:\models\large.gguf`, "127.0.0.1", 8080, "key", nil)
	if !containsPair(cpu, "--fit", "off") {
		t.Fatalf("CPU-only launch must bypass the physical-RAM fit rejection: %q", cpu)
	}
	if contains(cpu, "--fit-target") || contains(cpu, "--rpc") {
		t.Fatalf("CPU-only launch unexpectedly has an RPC memory target: %q", cpu)
	}

	rpc := engineArguments(`C:\models\large.gguf`, "127.0.0.1", 8080, "key", []string{"127.0.0.1:50052"})
	for key, value := range map[string]string{
		"--rpc":        "127.0.0.1:50052",
		"--split-mode": "layer",
		"--fit":        "on",
		"--fit-target": "0",
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
