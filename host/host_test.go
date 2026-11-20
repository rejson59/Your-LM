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
func TestPlan(t *testing.T) {
	m := Model{Size: 12 * GiB, Layers: 40}
	n := Node{Budget: 4 * GiB, Cores: 4}
	if _, _, e := plan(m, 10*GiB, nil); e == nil {
		t.Fatal("oversized model")
	}
	c, s, e := plan(m, 10*GiB, []Node{n})
	if e != nil || c < 1 || c >= 40 || s != fmt.Sprint(c) {
		t.Fatalf("%d %s %v", c, s, e)
	}
	if budget(16*GiB, GiB) != 0 {
		t.Fatal("underflow")
	}
	n.Budget = 1
	if _, _, e = plan(m, 10*GiB, []Node{n}); e == nil {
		t.Fatal("insufficient worker RAM")
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
	t.Helper()
	temp := httptest.NewTLSServer(nil)
	cfg := temp.TLS
	temp.Close()
	l, e := tls.Listen("tcp4", "127.0.0.1:0", cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { l.Close() })
	o := Offer{secret: token(), slots: make(chan struct{}, 16), local: rpc}
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
	bridge, e := n.bridge()
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
