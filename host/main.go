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
	workers   int
	name      string
	ready     bool
	started   time.Time
	logPath   string
	done      chan struct{}
}
type App struct {
	off *Offer
	sync.Mutex
	dir, engine, library, last    string
	lastEngineLog, lastEnginePath string
	runtimeRoot, adbPath, apkPath string
	nodes                         []Node
	model                         Model
	s                             *Session
	usb                           USBState
	update                        UpdateState
}

func (a *App) binary() string {
	if a.engine != "" {
		return a.engine
	}
	root := a.runtimeRoot
	if root == "" {
		root = filepath.Join(a.dir, runtimeDir)
	}
	return executable(root)
}
func engineArguments(modelPath, bind string, port int, key string, addresses []string) []string {
	args := []string{"--model", modelPath, "--offline", "--no-webui", "--no-agent", "--no-ui-mcp-proxy", "--cors-origins", "localhost", "--host", bind, "--port", strconv.Itoa(port), "--api-key", key, "--alias", "yourlm", "--ctx-size", "2048", "--parallel", "1", "-t", strconv.Itoa(max(1, runtime.NumCPU()/2)), "--jinja"}
	if len(addresses) == 0 {
		// Native --fit compares a CPU-only model with physical RAM and rejects it
		// before Windows can use the GGUF mapping/page file. CPU-only mode must
		// therefore load directly and let the operating system page file-backed
		// weights normally.
		return append(args, "--fit", "off")
	}
	// With RPC devices, native fit measures every worker and places as many
	// tensors as currently fit there. A zero target means YourLM does not hold
	// back an additional artificial reserve; unassigned weights remain mapped on
	// the host.
	return append(args, "--rpc", strings.Join(addresses, ","), "--split-mode", "layer", "--fit", "on", "--fit-target", "0")
}

func engineLogTail(path string, maxBytes int64) string {
	if maxBytes <= 0 {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	start := max(info.Size()-maxBytes, 0)
	if _, err = file.Seek(start, io.SeekStart); err != nil {
		return ""
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBytes))
	if err != nil {
		return ""
	}
	return string(data)
}

func cleanEngineLog(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\t' || r >= 32 {
			return r
		}
		return -1
	}, value)
	return strings.TrimSpace(value)
}

func enginePhase(engineLog string, ready bool) string {
	if ready {
		return "Model gotowy"
	}
	text := strings.ToLower(engineLog)
	switch {
	case strings.Contains(text, "listening on http") || strings.Contains(text, "server is listening"):
		return "Finalizowanie lokalnego API"
	case strings.Contains(text, "kv cache") || strings.Contains(text, "kv_cache") || strings.Contains(text, "llama_context"):
		return "Tworzenie kontekstu i buforów"
	case strings.Contains(text, "load_tensors") || strings.Contains(text, "offload") || strings.Contains(text, "fit"):
		return "Rozmieszczanie wag na urządzeniach"
	case strings.Contains(text, "loading model") || strings.Contains(text, "model_loader") || strings.Contains(text, "load_model"):
		return "Odczytywanie modelu GGUF"
	default:
		return "Uruchamianie silnika llama.cpp"
	}
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
	// Never reject a model through the old static planner. With RPC workers,
	// llama.cpp measures their live memory and places tensors natively. Without
	// workers, Windows receives the model directly so mmap/page-file fallback is
	// not pre-empted by llama.cpp's physical-RAM fit check.
	layers := 0
	if len(a.nodes) > 0 {
		layers = -1 // automatic; the exact placement is written to engine.log
	}
	address := net.JoinHostPort(bind, strconv.Itoa(port))
	probe, e := net.Listen("tcp4", address)
	if e != nil {
		return fmt.Errorf("Port API niedostępny: %w", e)
	}
	probe.Close()
	s := &Session{
		url:     "http://" + address,
		key:     token(),
		layers:  layers,
		workers: len(a.nodes),
		name:    m.Name,
		logPath: filepath.Join(a.dir, "engine.log"),
		done:    make(chan struct{}),
	}
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
	args := engineArguments(m.Path, bind, port, s.key, addresses)
	s.cmd = exec.Command(exe, args...)
	// Runtime-specific environment variables can enable downloads, plugins,
	// built-in shell/file tools or unauthenticated transports. Only explicit
	// controller arguments are trusted. RPC peer-to-peer sockets would bypass
	// the authenticated bridges.
	s.cmd.Env = childEnv()
	s.cmd.Dir = filepath.Dir(exe)
	logfile, e := os.OpenFile(s.logPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
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
	s.started = time.Now()
	a.s = s
	a.last = ""
	a.lastEngineLog = ""
	a.lastEnginePath = ""
	go func() {
		e := s.cmd.Wait()
		logfile.Close()
		engineLog := cleanEngineLog(engineLogTail(s.logPath, 24<<10))
		for _, l := range s.listeners {
			l.Close()
		}
		a.Lock()
		a.lastEngineLog = engineLog
		a.lastEnginePath = s.logPath
		if e != nil && !s.stopping {
			a.last = "Model nie został uruchomiony: " + e.Error() + ". Otwórz diagnostykę silnika poniżej i skopiuj log."
		}
		a.s = nil
		a.Unlock()
		close(s.done)
	}()
	go a.check(s)
	go a.monitorNodes(s)
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
func (a *App) monitorNodes(session *Session) {
	if session.workers == 0 {
		return
	}
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-session.done:
			return
		case <-ticker.C:
			a.Lock()
			if a.s != session {
				a.Unlock()
				return
			}
			nodes := append([]Node(nil), a.nodes...)
			a.Unlock()
			for i := range nodes {
				if err := nodes[i].refresh(); err != nil {
					continue
				}
				a.Lock()
				if a.s == session {
					for j := range a.nodes {
						if a.nodes[j].Address == nodes[i].Address {
							a.nodes[j] = nodes[i]
							break
						}
					}
				}
				a.Unlock()
			}
		}
	}
}

func (a *App) state() map[string]any {
	hostMemory := systemMemory()
	hostname, _ := os.Hostname()
	models := []string{}
	entries, _ := os.ReadDir(a.library)
	for _, d := range entries {
		if !d.IsDir() && strings.EqualFold(filepath.Ext(d.Name()), ".gguf") {
			models = append(models, filepath.Join(a.library, d.Name()))
		}
	}
	var session, engine any
	hostProcess := ProcessMemory{}
	if s := a.s; s != nil {
		if s.cmd != nil && s.cmd.Process != nil {
			hostProcess = memoryForProcess(s.cmd.Process.Pid)
		}
		engineLog := cleanEngineLog(engineLogTail(s.logPath, 24<<10))
		elapsed := int(time.Since(s.started).Seconds())
		mode := "Windows mmap · bez testu fizycznego RAM"
		if s.workers > 0 {
			mode = fmt.Sprintf("%d workerów · automatyczny podział · rezerwa 0 MiB · trwała pamięć podręczna RPC", s.workers)
		}
		session = map[string]any{"url": s.url + "/v1", "key": s.key, "layers": s.layers, "name": s.name, "ready": s.ready}
		engine = map[string]any{
			"active": true, "ready": s.ready, "phase": enginePhase(engineLog, s.ready),
			"elapsed": elapsed, "log": engineLog, "path": s.logPath, "mode": mode,
		}
	} else if a.lastEngineLog != "" || a.lastEnginePath != "" {
		phase := "Ostatnia sesja zakończona"
		if a.last != "" {
			phase = "Uruchomienie modelu nie powiodło się"
		}
		engine = map[string]any{
			"active": false, "ready": false, "phase": phase,
			"elapsed": 0, "log": a.lastEngineLog, "path": a.lastEnginePath,
		}
	}
	code, offerAddress, offerKey := "", "", ""
	if a.off != nil {
		code = a.off.code
		offerAddress = a.off.pairAt
		offerKey = a.off.manualKey
		if a.off.cmd != nil && a.off.cmd.Process != nil {
			hostProcess = memoryForProcess(a.off.cmd.Process.Pid)
		}
	}
	advice := ""
	available := hostMemory.Available
	physical := hostMemory.Total
	osUsed := hostMemory.Used
	workerAllocation := uint64(0)
	for _, node := range a.nodes {
		available += node.Free
		physical += node.Total
		if node.Used > 0 {
			osUsed += node.Used
		} else if node.Total >= node.Free {
			osUsed += node.Total - node.Free
		}
		workerAllocation += node.Allocation
	}
	actualAllocation := hostProcess.Resident + workerAllocation
	mappedModel := a.model.Size
	if a.model.Name != "" && a.s == nil {
		if len(a.nodes) == 0 {
			advice = fmt.Sprintf(
				"Model %.1f GiB jest odblokowany. %.1f GiB to cały fizyczny RAM hosta; %.1f GiB zajmują teraz Windows i aplikacje, a %.1f GiB jest natychmiast dostępne. To nie jest limit rozmiaru modelu: cały GGUF zostanie zmapowany z dysku przez mmap, a Windows ma jeszcze %.1f GiB wolnego limitu zatwierdzeń. YourLM nie wykona konserwatywnego odrzucenia.",
				float64(a.model.Size)/float64(GiB),
				float64(physical)/float64(GiB),
				float64(osUsed)/float64(GiB),
				float64(available)/float64(GiB),
				float64(hostMemory.CommitAvailable)/float64(GiB),
			)
		} else {
			advice = fmt.Sprintf(
				"Model %.1f GiB jest odblokowany. Lenovo i workery mają razem %.1f GiB fizycznego RAM: %.1f GiB zajmują teraz systemy i aplikacje, a %.1f GiB jest natychmiast dostępne. Dostępne teraz nie jest limitem całej puli: rezerwa YourLM wynosi 0 MiB, worker wykorzysta maksymalny praktyczny przydział, a reszta GGUF pozostanie w mmap Windows. Pamięć RPC przyspieszy kolejne uruchomienia.",
				float64(a.model.Size)/float64(GiB),
				float64(physical)/float64(GiB),
				float64(osUsed)/float64(GiB),
				float64(available)/float64(GiB),
			)
		}
	}
	memoryState := map[string]any{
		"host": hostMemory, "hostProcess": hostProcess,
		"physical": physical, "osUsed": osUsed, "available": available,
		"mappedModel": mappedModel, "actualAllocation": actualAllocation,
		"workerAllocation": workerAllocation,
		"commitLimit":      hostMemory.CommitLimit,
		"commitAvailable":  hostMemory.CommitAvailable,
		"pageFile":         hostMemory.PageFile,
	}
	return map[string]any{
		"version": buildVersion, "commit": buildCommit, "advice": advice,
		"modelWarning": false, "offer": code, "offerAddress": offerAddress,
		"offerKey": offerKey, "host": hostname, "os": runtime.GOOS,
		"cores": runtime.NumCPU(), "total": hostMemory.Total,
		"free": hostMemory.Available, "available": available,
		"physical": physical, "fitReserveMiB": 0, "memory": memoryState,
		"nodes": a.nodes, "ips": ips(), "model": a.model, "models": models,
		"library": a.library, "runtime": a.hasRuntime(), "session": session,
		"engine": engine, "usb": a.usb, "update": a.update, "last": a.last,
	}
}

type input struct {
	Code, Path, Address, Bind, PairAddress, PairKey, Serial string
	Port                                                    int
	Messages                                                []struct {
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
	if op == "usb-refresh" || op == "usb-install" {
		a.usbAPI(w, op, in)
		return
	}
	if op == "update-check" || op == "update-download" || op == "update-apply" {
		a.updateAPI(w, op)
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
	if handled, err := handleUpdateHelper(os.Args[1:]); handled {
		if err != nil {
			log.Print("Aktualizacja nie powiodła się: ", err)
		}
		return
	}
	cleanupDesktopUpdate()
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
	components, e := prepareBundledAssets(dir)
	if e != nil {
		log.Fatal("Nie udało się przygotować składników aplikacji: ", e)
	}
	a := &App{
		dir:         dir,
		library:     filepath.Join(dir, "models"),
		runtimeRoot: components.runtimeRoot,
		adbPath:     components.adbPath,
		apkPath:     components.apkPath,
	}
	if runtime.GOOS != "windows" {
		a.engine = os.Getenv("YOURLM_ENGINE")
	} else {
		a.usb = USBState{Supported: true, APKReady: a.apkPath != "", Message: "Szukam telefonu przez USB…"}
		go a.refreshUSB()
	}
	a.update = UpdateState{Current: buildVersion, Progress: "Aktualizacje są sprawdzane automatycznie."}
	go a.checkForUpdates(true)
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
	serveResult := make(chan error, 1)
	go func() {
		err := srv.Serve(l)
		if err == http.ErrServerClosed {
			err = nil
		}
		serveResult <- err
	}()
	var stopOnce sync.Once
	shutdown := func() {
		stopOnce.Do(func() {
			a.Lock()
			if a.off != nil {
				a.off.close()
			}
			if a.s != nil && a.s.cmd != nil && a.s.cmd.Process != nil {
				_ = a.s.cmd.Process.Kill()
			}
			a.Unlock()
			_ = srv.Close()
		})
	}
	defer shutdown()

	if !*nobrowser && runtime.GOOS == "windows" {
		runtime.LockOSThread()
		err := runDesktopUI(url, filepath.Join(dir, "webview2"))
		runtime.UnlockOSThread()
		if err == nil {
			return // Closing the native window closes the local controller.
		}
		log.Print("Natywne okno jest niedostępne, używam przeglądarki awaryjnie: ", err)
		if err = openURL(url); err != nil {
			log.Print("Nie udało się otworzyć interfejsu: ", err)
		}
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	select {
	case <-signals:
		return
	case err := <-serveResult:
		if err != nil {
			log.Print("Lokalny interfejs zakończył pracę: ", err)
		}
	}
}
