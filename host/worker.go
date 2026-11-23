package main

import (
	"bufio"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

const transportBenchmarkBytes = 4 << 20

type LinkBenchmark struct {
	UploadBps     uint64 `json:"uploadBps"`
	DownloadBps   uint64 `json:"downloadBps"`
	ScoreBps      uint64 `json:"scoreBps"`
	LatencyMicros uint64 `json:"latencyMicros"`
}

type Node struct {
	Name               string         `json:"name"`
	Platform           string         `json:"platform,omitempty"`
	Total              uint64         `json:"total"`
	Free               uint64         `json:"free"`
	Used               uint64         `json:"used"`
	Allocation         uint64         `json:"allocation"`
	Cores              int            `json:"cores"`
	Build              string         `json:"build"`
	Alive              bool           `json:"alive"`
	Recovering         bool           `json:"recovering,omitempty"`
	RestartAttempts    int            `json:"restartAttempts,omitempty"`
	RestartCount       int            `json:"restartCount,omitempty"`
	RecoveryError      string         `json:"recoveryError,omitempty"`
	Address            string         `json:"address"`
	Transport          string         `json:"transport,omitempty"`
	TransportReason    string         `json:"transportReason,omitempty"`
	TransportCheckedAt int64          `json:"transportCheckedAt,omitempty"`
	USBSerial          string         `json:"-"`
	WiFiBenchmark      *LinkBenchmark `json:"wifiBenchmark,omitempty"`
	USBBenchmark       *LinkBenchmark `json:"usbBenchmark,omitempty"`
	pin, secret        string
	usbAddress         string
	usbForwardPort     int
	usbRemotePort      int
}

func parseCode(s string) (Node, error) {
	p := strings.Split(strings.TrimSpace(s), "|")
	bad := errors.New("Niepoprawny kod sesji YourLM")
	if len(p) != 4 || p[0] != "YLM1" {
		return Node{}, bad
	}
	h, port, e := net.SplitHostPort(p[1])
	ip := net.ParseIP(h)
	n, e2 := strconv.Atoi(port)
	if e != nil || e2 != nil || n < 1 || n > 65535 || ip.To4() == nil || !ip.IsPrivate() {
		return Node{}, bad
	}
	for i, size := range []int{32, 24} {
		v, e := hex.DecodeString(p[i+2])
		if e != nil || len(v) != size {
			return Node{}, bad
		}
	}
	return Node{Address: p[1], pin: p[2], secret: p[3]}, nil
}
func tuneTCP(conn net.Conn) {
	if tlsConn, ok := conn.(*tls.Conn); ok {
		conn = tlsConn.NetConn()
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
		_ = tcp.SetReadBuffer(1 << 20)
		_ = tcp.SetWriteBuffer(1 << 20)
	}
}

func (n Node) dialAt(address, mode string, timeout time.Duration) (net.Conn, error) {
	pin, _ := hex.DecodeString(n.pin)
	c, e := tls.DialWithDialer(&net.Dialer{Timeout: min(timeout, 5*time.Second)}, "tcp4", address, &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: true, // Trust the certificate fingerprint transferred out of band, not a public CA.
		VerifyConnection: func(s tls.ConnectionState) error {
			if len(s.PeerCertificates) > 0 {
				v := sha256.Sum256(s.PeerCertificates[0].Raw)
				if subtle.ConstantTimeCompare(v[:], pin) == 1 {
					return nil
				}
			}
			return errors.New("Certyfikat workera nie pasuje do kodu QR")
		}})
	if e != nil {
		return nil, e
	}
	tuneTCP(c)
	_ = c.SetDeadline(time.Now().Add(timeout))
	if _, e = fmt.Fprintf(c, "%s %s\n", mode, n.secret); e != nil {
		c.Close()
		return nil, e
	}
	return c, nil
}

func (n Node) dialEndpoint(mode string) (net.Conn, string, error) {
	addresses := []string{n.Address}
	if n.usbAddress != "" {
		if n.Transport == "usb" {
			addresses = []string{n.usbAddress, n.Address}
		} else {
			addresses = append(addresses, n.usbAddress)
		}
	}
	var last error
	for _, address := range addresses {
		c, err := n.dialAt(address, mode, 6*time.Second)
		if err == nil {
			return c, address, nil
		}
		last = err
	}
	return nil, "", last
}

func (n Node) dial(mode string) (net.Conn, error) {
	c, _, err := n.dialEndpoint(mode)
	return c, err
}

func benchmarkRate(bytes int64, elapsed time.Duration) uint64 {
	if bytes <= 0 || elapsed <= 0 {
		return 0
	}
	return uint64(float64(bytes) / elapsed.Seconds())
}

func benchmarkScore(upload, download uint64) uint64 {
	// Initial tensor placement is overwhelmingly host-to-worker, while token
	// generation still needs both directions. Weight upload accordingly.
	return (upload*3 + download) / 4
}

func (n Node) benchmarkAt(address string) (LinkBenchmark, error) {
	var result LinkBenchmark
	c, err := n.dialAt(address, "BENCH-PING", 12*time.Second)
	if err != nil {
		return result, err
	}
	latencies := make([]time.Duration, 0, 5)
	var ping [1]byte
	for i := byte(1); i <= 5; i++ {
		ping[0] = i
		started := time.Now()
		if _, err = c.Write(ping[:]); err != nil {
			c.Close()
			return result, err
		}
		if _, err = io.ReadFull(c, ping[:]); err != nil || ping[0] != i {
			c.Close()
			if err == nil {
				err = errors.New("worker nie potwierdził testu opóźnienia")
			}
			return result, err
		}
		latencies = append(latencies, time.Since(started))
	}
	c.Close()
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	result.LatencyMicros = max(uint64(1), uint64(latencies[len(latencies)/2].Microseconds()))

	payload := make([]byte, 256<<10)
	c, err = n.dialAt(address, "BENCH-UP", 12*time.Second)
	if err != nil {
		return result, err
	}
	started := time.Now()
	remaining := int64(transportBenchmarkBytes)
	for remaining > 0 {
		chunk := int64(len(payload))
		if chunk > remaining {
			chunk = remaining
		}
		if _, err = c.Write(payload[:chunk]); err != nil {
			c.Close()
			return result, err
		}
		remaining -= chunk
	}
	var ack [1]byte
	if _, err = io.ReadFull(c, ack[:]); err != nil || ack[0] != '!' {
		c.Close()
		if err == nil {
			err = errors.New("worker nie potwierdził testu wysyłania")
		}
		return result, err
	}
	result.UploadBps = benchmarkRate(transportBenchmarkBytes, time.Since(started))
	c.Close()

	c, err = n.dialAt(address, "BENCH-DOWN", 12*time.Second)
	if err != nil {
		return result, err
	}
	started = time.Now()
	if _, err = io.CopyN(io.Discard, c, transportBenchmarkBytes); err != nil {
		c.Close()
		return result, err
	}
	result.DownloadBps = benchmarkRate(transportBenchmarkBytes, time.Since(started))
	result.ScoreBps = benchmarkScore(result.UploadBps, result.DownloadBps)
	c.Close()
	if result.UploadBps == 0 || result.DownloadBps == 0 {
		return result, errors.New("test połączenia nie zwrócił przepustowości")
	}
	return result, nil
}

const (
	GiB            uint64 = 1 << 30
	rpcWorkerBuild        = "b12562-ylm-rpc5"
)

func validateWorkerMetadata(info Node) error {
	if info.Build != rpcWorkerBuild {
		return fmt.Errorf("Niekompatybilny worker %q — zaktualizuj YourLM na urządzeniu do tej samej wersji", info.Build)
	}
	if info.Total == 0 || info.Free > info.Total || info.Cores <= 0 {
		return errors.New("Niekompatybilny worker")
	}
	return nil
}

func workerRuntimeError(info Node) error {
	if info.Alive {
		return nil
	}
	if info.Recovering {
		return fmt.Errorf("worker odtwarza proces natywny (próba %d)", info.RestartAttempts)
	}
	if info.RecoveryError != "" {
		return fmt.Errorf("odzyskanie procesu natywnego nie powiodło się: %s", info.RecoveryError)
	}
	return errors.New("natywny worker RPC nie działa — uruchom worker ponownie na telefonie")
}

func validateWorkerInfo(info Node) error {
	if err := validateWorkerMetadata(info); err != nil {
		return err
	}
	return workerRuntimeError(info)
}

func (n Node) infoAt(address string) (Node, error) {
	c, err := n.dialAt(address, "INFO", 6*time.Second)
	if err != nil {
		return Node{}, err
	}
	defer c.Close()
	var info Node
	if err = json.NewDecoder(io.LimitReader(c, 4096)).Decode(&info); err != nil {
		return Node{}, err
	}
	if err = validateWorkerMetadata(info); err != nil {
		return Node{}, err
	}
	return info, nil
}

func (n *Node) refresh() error {
	c, endpoint, err := n.dialEndpoint("INFO")
	if err != nil {
		return err
	}
	defer c.Close()
	var info Node
	if err = json.NewDecoder(io.LimitReader(c, 4096)).Decode(&info); err != nil {
		return err
	}
	if err = validateWorkerMetadata(info); err != nil {
		return err
	}
	if info.Used == 0 || info.Used > info.Total {
		info.Used = info.Total - info.Free
	}
	if info.Allocation > info.Total {
		info.Allocation = 0
	}
	n.Name, n.Platform, n.Free, n.Total, n.Used, n.Allocation, n.Cores, n.Build, n.Alive =
		info.Name, info.Platform, info.Free, info.Total, info.Used, info.Allocation, info.Cores, info.Build, info.Alive
	n.Recovering, n.RestartAttempts, n.RestartCount, n.RecoveryError =
		info.Recovering, info.RestartAttempts, info.RestartCount, info.RecoveryError
	previousTransport := n.Transport
	if endpoint == n.usbAddress && n.usbAddress != "" {
		n.Transport = "usb"
		if previousTransport == "wifi" {
			n.TransportReason = "Wybrane Wi-Fi było niedostępne; użyto awaryjnego szyfrowanego tunelu USB/ADB."
		}
	} else if endpoint == n.Address {
		n.Transport = "wifi"
		if previousTransport == "usb" {
			n.TransportReason = "Tunel USB/ADB był niedostępny; użyto awaryjnego szyfrowanego Wi-Fi."
		} else if n.TransportReason == "" {
			n.TransportReason = "Połączenie bezpośrednie przez szyfrowany tunel Wi-Fi/LAN."
		}
	}
	return workerRuntimeError(info)
}

func (n Node) diagnostics() ([]byte, error) {
	c, err := n.dial("DIAG")
	if err != nil {
		return nil, err
	}
	defer c.Close()
	var report map[string]any
	if err = json.NewDecoder(io.LimitReader(c, 256<<10)).Decode(&report); err != nil {
		return nil, err
	}
	return json.MarshalIndent(report, "", "  ")
}

func workerMemoryAssessment(report []byte) string {
	var snapshot struct {
		MemoryAvailableBytes uint64 `json:"memoryAvailableBytes"`
		MemoryLow            bool   `json:"memoryLow"`
		RestartAttempts      int    `json:"restartAttempts"`
		RestartCount         int    `json:"restartCount"`
		RecoveryError        string `json:"recoveryError"`
		ExitHistory          []struct {
			Reason string `json:"reason"`
		} `json:"exitHistory"`
	}
	if json.Unmarshal(report, &snapshot) != nil {
		return ""
	}
	lowMemoryExits := 0
	for _, exit := range snapshot.ExitHistory {
		if exit.Reason == "LOW_MEMORY" {
			lowMemoryExits++
		}
	}
	var assessment strings.Builder
	if snapshot.RestartAttempts > 0 {
		fmt.Fprintf(
			&assessment,
			"Odzyskiwanie workera: %d prób, %d udanych ponownych uruchomień procesu natywnego.\n",
			snapshot.RestartAttempts,
			snapshot.RestartCount,
		)
	}
	if snapshot.RecoveryError != "" {
		fmt.Fprintf(&assessment, "Ostatni błąd odzyskiwania: %s\n", snapshot.RecoveryError)
	}
	memoryLooksSafe := !snapshot.MemoryLow && lowMemoryExits == 0 &&
		(snapshot.MemoryAvailableBytes == 0 || snapshot.MemoryAvailableBytes >= GiB)
	if memoryLooksSafe {
		return assessment.String()
	}
	fmt.Fprintf(
		&assessment,
		"Ocena pamięci: ostatnia migawka miała %.2f GiB dostępnego RAM; memoryLow=%v; historia Androida zawiera %d zakończeń LOW_MEMORY. To wskazuje na presję pamięci telefonu, a nie sam limit przepustowości Wi-Fi.\n",
		float64(snapshot.MemoryAvailableBytes)/float64(GiB),
		snapshot.MemoryLow,
		lowMemoryExits,
	)
	return assessment.String()
}

func linkBenchmarkText(value *LinkBenchmark) string {
	if value == nil {
		return "brak pomiaru"
	}
	return fmt.Sprintf(
		"wysyłanie %.1f MiB/s, odbiór %.1f MiB/s, ping %.1f ms",
		float64(value.UploadBps)/float64(1<<20),
		float64(value.DownloadBps)/float64(1<<20),
		float64(value.LatencyMicros)/1000,
	)
}

func workerDiagnosticsReport(nodes []Node, cached map[string]WorkerDiagnosticSnapshot) string {
	if len(nodes) == 0 {
		return ""
	}
	var out strings.Builder
	out.WriteString("\n\n===== DIAGNOSTYKA WORKERÓW =====\n")
	for i, node := range nodes {
		name := node.Name
		if name == "" {
			name = "worker"
		}
		fmt.Fprintf(
			&out,
			"\n[%d] %s (%s)\nTransport: %s — %s\nPomiar Wi-Fi: %s; USB/ADB: %s\nOstatni pomiar: RAM %.2f/%.2f GiB dostępne, proces %.2f GiB, build %s\n",
			i+1,
			name,
			node.Address,
			node.Transport,
			node.TransportReason,
			linkBenchmarkText(node.WiFiBenchmark),
			linkBenchmarkText(node.USBBenchmark),
			float64(node.Free)/float64(GiB),
			float64(node.Total)/float64(GiB),
			float64(node.Allocation)/float64(GiB),
			node.Build,
		)
		report, err := node.diagnostics()
		if err != nil {
			fmt.Fprintf(
				&out,
				"Nie udało się pobrać raportu po awarii: %v. Mogło zostać zakończone całe YourLM, a nie tylko natywny worker.\n",
				err,
			)
			snapshot := cached[node.Address]
			report = snapshot.report
			if len(report) == 0 {
				out.WriteString("Brak wcześniejszej migawki diagnostycznej.\n")
				continue
			}
			fmt.Fprintf(
				&out,
				"Ostatnia udana migawka pobrana przez Windows podczas ładowania: %s\n",
				snapshot.receivedAt.Format(time.RFC3339),
			)
		} else {
			out.WriteString("Raport pobrany bezpośrednio po awarii:\n")
		}
		out.WriteString(workerMemoryAssessment(report))
		out.Write(report)
		out.WriteByte('\n')
	}
	return out.String()
}

type rpcCountingWriter struct {
	io.Writer
	transferred *atomic.Uint64
}

func (w rpcCountingWriter) Write(data []byte) (int, error) {
	written, err := w.Writer.Write(data)
	if written > 0 && w.transferred != nil {
		w.transferred.Add(uint64(written))
	}
	return written, err
}

func copyRPC(dst io.Writer, src io.Reader, transferred *atomic.Uint64) {
	_, _ = io.CopyBuffer(rpcCountingWriter{Writer: dst, transferred: transferred}, src, make([]byte, 256<<10))
}

func serveTransportPing(conn net.Conn, input io.Reader) {
	_ = conn.SetDeadline(time.Now().Add(12 * time.Second))
	var value [1]byte
	for i := 0; i < 5; i++ {
		if _, err := io.ReadFull(input, value[:]); err != nil {
			return
		}
		if _, err := conn.Write(value[:]); err != nil {
			return
		}
	}
}

func serveTransportBenchmark(conn net.Conn, input io.Reader, upload bool) {
	_ = conn.SetDeadline(time.Now().Add(12 * time.Second))
	if upload {
		if _, err := io.CopyN(io.Discard, input, transportBenchmarkBytes); err == nil {
			_, _ = conn.Write([]byte{'!'})
		}
		return
	}
	payload := make([]byte, 256<<10)
	remaining := transportBenchmarkBytes
	for remaining > 0 {
		chunk := min(len(payload), remaining)
		if _, err := conn.Write(payload[:chunk]); err != nil {
			return
		}
		remaining -= chunk
	}
}

func (n Node) bridge(sent, received *atomic.Uint64) (net.Listener, error) {
	l, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return nil, e
	}
	go func() {
		for {
			local, e := l.Accept()
			if e != nil {
				return
			}
			go func() {
				defer local.Close()
				tuneTCP(local)
				remote, e := n.dial("RPC")
				if e != nil {
					return
				}
				defer remote.Close()
				r := bufio.NewReader(remote)
				b, e := r.ReadByte()
				if e != nil || b != '!' {
					return
				}
				remote.SetDeadline(time.Time{})
				go func() { copyRPC(remote, local, sent); remote.Close() }()
				copyRPC(local, r, received)
			}()
		}
	}()
	return l, nil
}
