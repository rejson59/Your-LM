package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed web/*
var web embed.FS

func token() string {
	var b [24]byte
	if _, e := rand.Read(b[:]); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b[:])
}
func budget(total, free uint64) uint64 {
	if free <= 2*GiB {
		return 0
	}
	return min(total*2/3, free-2*GiB)
}
func ips() []string {
	out := []string{"127.0.0.1"}
	if ip := net.ParseIP(os.Getenv("YOURLM_LAN")); ip.To4() != nil && ip.IsPrivate() {
		return append(out, ip.String())
	}
	as, _ := net.InterfaceAddrs()
	for _, a := range as {
		ip, _, _ := net.ParseCIDR(a.String())
		if ip.To4() != nil && ip.IsPrivate() {
			out = append(out, ip.String())
		}
	}
	return out
}

type Session struct {
	stopping  bool
	cmd       *exec.Cmd
	listeners []net.Listener
	url, key  string
	layers    int
	name      string
	ready     bool
	done      chan struct{}
}
type App struct {
	off *Offer
	sync.Mutex
	dir, engine, library, last string
	nodes                      []Node
	model                      Model
	s                          *Session
}

func (a *App) binary() string {
	if a.engine != "" {
		return a.engine
	}
	return executable(filepath.Join(a.dir, runtimeDir))
}
func (a *App) start(bind string, port int) error {
	if a.s != nil || a.off != nil {
		return errors.New("Najpierw zatrzymaj serwer")
	}
	ok := false
	for _, ip := range ips() {
		if bind == ip {
			ok = true
		}
	}
	if !ok || port < 1024 || port > 65535 {
		return errors.New("Niepoprawny adres lub port API")
	}
	exe, e := a.launchPath(false)
	if e != nil {
		return e
	}
	m, e := inspect(a.model.Path)
	if e != nil {
		return e
	}
	for i := range a.nodes {
		if e := a.nodes[i].refresh(); e != nil {
			return fmt.Errorf("Worker %s niedostępny: %w", a.nodes[i].Address, e)
		}
	}
	total, free := memory()
	layers, split, e := plan(m, budget(total, free), a.nodes)
	if e != nil {
		return e
	}
	address := net.JoinHostPort(bind, strconv.Itoa(port))
	probe, e := net.Listen("tcp4", address)
	if e != nil {
		return fmt.Errorf("Port API niedostępny: %w", e)
	}
	probe.Close()
	s := &Session{url: "http://" + address, key: token(), layers: layers, name: m.Name, done: make(chan struct{})}
	args := []string{"--model", m.Path, "--offline", "--no-webui", "--no-agent", "--no-ui-mcp-proxy", "--cors-origins", "localhost", "--host", bind, "--port", strconv.Itoa(port), "--api-key", s.key, "--alias", "yourlm", "--ctx-size", "2048", "--parallel", "1", "-t", strconv.Itoa(max(1, runtime.NumCPU()/2)), "--jinja", "--fit", "off", "-ngl", strconv.Itoa(layers)}
	defer func() {
		if a.s != s {
			for _, l := range s.listeners {
				l.Close()
			}
		}
	}()
	var addresses []string
	for _, n := range a.nodes {
		l, e := n.bridge()
		if e != nil {
			return e
		}
		s.listeners = append(s.listeners, l)
		addresses = append(addresses, l.Addr().String())
	}
	if len(addresses) > 0 {
		args = append(args, "--rpc", strings.Join(addresses, ","), "--split-mode", "layer", "--tensor-split", split)
	}
	s.cmd = exec.Command(exe, args...)
	// Runtime-specific environment variables can enable downloads, plugins,
	// built-in shell/file tools or unauthenticated transports. Only explicit
	// controller arguments are trusted. RPC peer-to-peer sockets would bypass
	// the authenticated bridges.
	s.cmd.Env = childEnv()
	s.cmd.Dir = filepath.Dir(exe)
	logfile, e := os.OpenFile(filepath.Join(a.dir, "engine.log"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if e != nil {
		return e
	}
	s.cmd.Stdout = logfile
	s.cmd.Stderr = logfile
	prepareChild(s.cmd)
	if e = s.cmd.Start(); e != nil {
		logfile.Close()
		return e
	}
	trackChild(s.cmd)
	a.s = s
	a.last = ""
	go func() {
		e := s.cmd.Wait()
		logfile.Close()
		for _, l := range s.listeners {
			l.Close()
		}
		a.Lock()
		if e != nil && !s.stopping {
			a.last = "Proces zakończony: " + e.Error() + ". Diagnostyka: " + filepath.Join(a.dir, "engine.log")
		}
		a.s = nil
		a.Unlock()
		close(s.done)
	}()
	go a.check(s)
	return nil
}

func childEnv() []string {
	out := make([]string, 0, len(os.Environ())+1)
	for _, value := range os.Environ() {
		key, _, _ := strings.Cut(value, "=")
		key = strings.ToUpper(key)
		if strings.HasPrefix(key, "LLAMA_") || strings.HasPrefix(key, "GGML_") {
			continue
		}
		out = append(out, value)
	}
	return append(out, "GGML_RPC_NO_COMM=1")
}

var localTransport = &http.Transport{Proxy: nil}

// Requests with prompts or API credentials must never follow engine redirects.
func localClient(timeout time.Duration) *http.Client {
	return &http.Client{Transport: localTransport, Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func (a *App) check(s *Session) {
	cl := localClient(2 * time.Second)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			req, _ := http.NewRequest("GET", s.url+"/v1/models", nil)
			req.Header.Set("Authorization", "Bearer "+s.key)
			res, e := cl.Do(req)
			if e == nil {
				ok := res.StatusCode == 200
				res.Body.Close()
				if ok {
					a.Lock()
					s.ready = true
					a.Unlock()
					return
				}
			}
		}
	}
}
func (a *App) state() map[string]any {
	total, free := memory()
	hostname, _ := os.Hostname()
	models := []string{}
	entries, _ := os.ReadDir(a.library)
	for _, d := range entries {
		if !d.IsDir() && strings.EqualFold(filepath.Ext(d.Name()), ".gguf") {
			models = append(models, filepath.Join(a.library, d.Name()))
		}
	}
	var session any
	if s := a.s; s != nil {
		session = map[string]any{"url": s.url + "/v1", "key": s.key, "layers": s.layers, "name": s.name, "ready": s.ready}
	}
	code, offerAddress, offerKey := "", "", ""
	if a.off != nil {
		code = a.off.code
		offerAddress = a.off.pairAt
		offerKey = a.off.manualKey
	}
	advice := ""
	if a.model.Name != "" && a.s == nil {
		layers, _, err := plan(a.model, budget(total, free), a.nodes)
		if err != nil {
			advice = err.Error()
		} else {
			advice = fmt.Sprintf("Szacunkowo mieści się w RAM: %d warstw lokalnie, %d na workerach. Kontekst: 2048 tokenów.", a.model.Layers-layers, layers)
		}
	}
	return map[string]any{"version": buildVersion, "commit": buildCommit, "advice": advice, "offer": code, "offerAddress": offerAddress, "offerKey": offerKey, "host": hostname, "os": runtime.GOOS, "cores": runtime.NumCPU(), "total": total, "free": free, "budget": budget(total, free), "nodes": a.nodes, "ips": ips(), "model": a.model, "models": models, "library": a.library, "runtime": a.hasRuntime(), "session": session, "last": a.last}
}

type input struct {
	Code, Path, Address, Bind, PairAddress, PairKey string
	Port                                            int
	Messages                                        []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
}

func respond(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
func (a *App) api(w http.ResponseWriter, r *http.Request) {
	op := strings.TrimPrefix(r.URL.Path, "/api/")
	if op == "state" && r.Method == "GET" {
		a.Lock()
		defer a.Unlock()
		respond(w, a.state())
		return
	}
	typ, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if r.Method != "POST" || typ != "application/json" {
		http.Error(w, "Wymagany POST JSON", 405)
		return
	}
	var in input
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); e != nil {
		http.Error(w, "Niepoprawny JSON", 400)
		return
	}
	if op == "chat" {
		a.chat(w, r, in)
		return
	}
	a.Lock()
	defer a.Unlock()
	var e error
	switch op {
	case "offer":
		e = a.offer(in.Bind)
	case "unoffer":
		if a.off != nil {
			a.off.close()
		}
	case "start":
		e = a.start(in.Bind, in.Port)
	case "stop":
		if a.s != nil {
			a.s.stopping = true
			e = a.s.cmd.Process.Kill()
		}
	case "pair", "pair-manual", "remove", "model", "pick":
		if a.s != nil {
			e = errors.New("Zatrzymaj model przed zmianą konfiguracji")
			break
		}
		switch op {
		case "pair", "pair-manual":
			var n Node
			if op == "pair" {
				n, e = parseCode(in.Code)
			} else {
				n, e = pairManual(in.PairAddress, in.PairKey)
			}
			if e == nil {
				for _, x := range a.nodes {
					if x.Address == n.Address {
						e = errors.New("Ten worker jest już dodany")
					}
				}
			}
			if e == nil && len(a.nodes) >= 8 {
				e = errors.New("Limit ośmiu workerów")
			}
			if e == nil {
				e = n.refresh()
			}
			if e == nil {
				a.nodes = append(a.nodes, n)
			}
		case "remove":
			for i, n := range a.nodes {
				if n.Address == in.Address {
					a.nodes = append(a.nodes[:i], a.nodes[i+1:]...)
					break
				}
			}
		case "pick", "model":
			if op == "pick" {
				in.Path, e = chooseModel()
				if e != nil || in.Path == "" {
					break
				}
			}
			var m Model
			m, e = inspect(strings.Trim(strings.TrimSpace(in.Path), "\""))
			if e == nil {
				a.model = m
			}
		}
	default:
		http.NotFound(w, r)
		return
	}
	if e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	respond(w, a.state())
}
func (a *App) chat(w http.ResponseWriter, r *http.Request, in input) {
	a.Lock()
	s := a.s
	ready := s != nil && s.ready
	a.Unlock()
	if !ready {
		http.Error(w, "Model nie jest gotowy", 409)
		return
	}
	if len(in.Messages) == 0 || len(in.Messages) > 128 {
		http.Error(w, "Za długa lub pusta rozmowa", 400)
		return
	}
	b, _ := json.Marshal(map[string]any{"model": "yourlm", "messages": in.Messages, "max_tokens": 512})
	req, _ := http.NewRequestWithContext(r.Context(), "POST", s.url+"/v1/chat/completions", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.key)
	res, e := localClient(30 * time.Minute).Do(req)
	if e != nil {
		http.Error(w, e.Error(), 502)
		return
	}
	defer res.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(res.StatusCode)
	io.Copy(w, io.LimitReader(res.Body, 8<<20))
}
func guard(host, secret string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != host || (r.Header.Get("Origin") != "" && r.Header.Get("Origin") != "http://"+host) {
			http.Error(w, "Niedozwolony host", 403)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Permissions-Policy", "camera=(self), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; media-src 'self' blob:; object-src 'none'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		if strings.HasPrefix(r.URL.Path, "/api/") && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-YourLM")), []byte(secret)) != 1 {
			http.Error(w, "Otwórz adres z konsoli YourLM", 403)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func main() {
	nobrowser := flag.Bool("no-browser", false, "do not launch the browser")
	flag.Parse()
	dir, _ := os.UserConfigDir()
	dir = filepath.Join(dir, "YourLM")
	if d := os.Getenv("YOURLM_HOME"); runtime.GOOS != "windows" && d != "" {
		dir = d
	}
	if e := os.MkdirAll(dir, 0700); e != nil {
		log.Fatal(e)
	}
	a := &App{dir: dir, library: filepath.Join(dir, "models")}
	if runtime.GOOS != "windows" {
		a.engine = os.Getenv("YOURLM_ENGINE")
	}
	os.MkdirAll(a.library, 0700)
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		log.Fatal(e)
	}
	secret := token()
	addr := l.Addr().String()
	url := "http://" + addr + "/#" + secret
	fmt.Println(url)
	mux := http.NewServeMux()
	mux.HandleFunc("/api/", a.api)
	assets, _ := fs.Sub(web, "web")
	mux.Handle("/", http.FileServer(http.FS(assets)))
	srv := &http.Server{Handler: guard(addr, secret, mux), ReadHeaderTimeout: 10 * time.Second}
	if !*nobrowser && runtime.GOOS == "windows" {
		if e := openURL(url); e != nil {
			log.Print("Nie udało się otworzyć przeglądarki: ", e)
		}
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-signals
		a.Lock()
		if a.off != nil {
			a.off.close()
		}
		if a.s != nil {
			a.s.cmd.Process.Kill()
		}
		a.Unlock()
		srv.Close()
	}()
	if e := srv.Serve(l); e != nil && e != http.ErrServerClosed {
		log.Fatal(e)
	}
}
