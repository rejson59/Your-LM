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
	SwapBytes          uint64         `json:"swapBytes,omitempty"`
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

func (n Node) endpointAddresses() []string {
	addresses := []string{n.Address}
	if n.usbAddress != "" && n.usbAddress != n.Address {
		if n.Transport == "usb" {
			addresses = []string{n.usbAddress, n.Address}
		} else {
			addresses = append(addresses, n.usbAddress)
		}
	}
	return addresses
}

func (n Node) dialEndpoint(mode string) (net.Conn, string, error) {
	var last error
	for _, address := range n.endpointAddresses() {
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
	rpcWorkerBuild        = "b12562-ylm-rpc6"
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
	n.Name, n.Platform, n.Free, n.Total, n.Used, n.Allocation, n.SwapBytes, n.Cores, n.Build, n.Alive =
		info.Name, info.Platform, info.Free, info.Total, info.Used, info.Allocation, info.SwapBytes, info.Cores, info.Build, info.Alive
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
	var last error
	for _, address := range n.endpointAddresses() {
		c, err := n.dialAt(address, "DIAG", 15*time.Second)
		if err != nil {
			last = err
			continue
		}
		var report map[string]any
		err = json.NewDecoder(io.LimitReader(c, 256<<10)).Decode(&report)
		_ = c.Close()
		if err != nil {
			// ADB forwards can accept TLS and then disappear while the larger
			// diagnostic response is in flight. Retry the independently
			// authenticated Wi-Fi endpoint (or vice versa), not just connect.
			last = err
			continue
		}
		return json.MarshalIndent(report, "", "  ")
	}
	if last == nil {
		last = errors.New("brak adresu diagnostycznego workera")
	}
	return nil, last
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

func compactTextTail(value string, maxBytes int) string {
	value = strings.TrimSpace(value)
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	value = value[len(value)-maxBytes:]
	if newline := strings.IndexByte(value, '\n'); newline >= 0 {
		value = value[newline+1:]
	}
	return "…\n" + strings.ToValidUTF8(value, "�")
}

func compactRelevantLog(value string, tailLines, maxBytes int, keywords ...string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	lines := strings.Split(strings.TrimSpace(value), "\n")
	selected := make(map[int]bool)
	for i := max(0, len(lines)-tailLines); i < len(lines); i++ {
		selected[i] = true
	}
	for i, line := range lines {
		lower := strings.ToLower(line)
		for _, keyword := range keywords {
			if strings.Contains(lower, strings.ToLower(keyword)) {
				selected[i] = true
				break
			}
		}
	}
	var out strings.Builder
	previous := -1
	for i, line := range lines {
		if !selected[i] {
			continue
		}
		if previous >= 0 && i > previous+1 {
			fmt.Fprintf(&out, "… pominięto %d wierszy …\n", i-previous-1)
		}
		out.WriteString(strings.TrimSpace(line))
		out.WriteByte('\n')
		previous = i
	}
	return compactTextTail(out.String(), maxBytes)
}

func recentWorkerEventHeaders(value string, limit int) []string {
	var events []string
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r", ""), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if _, err := strconv.ParseInt(fields[0], 10, 64); err != nil {
			continue
		}
		if !strings.Contains(fields[1], "native_") && fields[1] != "forwarding_error" {
			continue
		}
		events = append(events, strings.TrimSpace(line))
	}
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	return events
}

func compactWorkerDiagnostic(report []byte) string {
	var snapshot struct {
		Device                       string `json:"device"`
		Android                      string `json:"android"`
		SDK                          int    `json:"sdk"`
		WorkerBuild                  string `json:"workerBuild"`
		CapturedAtEpochMs            int64  `json:"capturedAtEpochMs"`
		RPCAlive                     bool   `json:"rpcAlive"`
		RPCPID                       int    `json:"rpcPid"`
		RPCExitSummary               string `json:"rpcExitSummary"`
		RPCResidentBytes             uint64 `json:"rpcResidentBytes"`
		RPCPSSBytes                  uint64 `json:"rpcPssBytes"`
		RPCPeakResidentBytes         uint64 `json:"rpcPeakResidentBytes"`
		RPCSwapBytes                 uint64 `json:"rpcSwapBytes"`
		AppPSSBytes                  uint64 `json:"appPssBytes"`
		AppSwapBytes                 uint64 `json:"appSwapBytes"`
		CombinedProcessFootprint     uint64 `json:"combinedProcessFootprintBytes"`
		RPCStartedAtEpochMs          int64  `json:"rpcStartedAtEpochMs"`
		RPCStoppedAtEpochMs          int64  `json:"rpcStoppedAtEpochMs"`
		Recovering                   bool   `json:"recovering"`
		RestartAttempts              int    `json:"restartAttempts"`
		RestartCount                 int    `json:"restartCount"`
		RecoveryError                string `json:"recoveryError"`
		BridgeConnections            uint64 `json:"bridgeConnections"`
		BridgeActiveSockets          int    `json:"bridgeActiveSockets"`
		BridgeHostToNativeBytes      uint64 `json:"bridgeHostToNativeBytes"`
		BridgeNativeToHostBytes      uint64 `json:"bridgeNativeToHostBytes"`
		ADBTLSBridgeAvailable        bool   `json:"adbTlsBridgeAvailable"`
		MemoryTotalBytes             uint64 `json:"memoryTotalBytes"`
		MemoryAvailableBytes         uint64 `json:"memoryAvailableBytes"`
		MemoryLow                    bool   `json:"memoryLow"`
		MemoryLowThresholdBytes      uint64 `json:"memoryLowThresholdBytes"`
		CacheBytes                   uint64 `json:"cacheBytes"`
		CacheFiles                   uint64 `json:"cacheFiles"`
		CachePartialFiles            uint64 `json:"cachePartialFiles"`
		NativeLogBytes               uint64 `json:"nativeLogBytes"`
		NativeLog                    string `json:"nativeLog"`
		LastForwardingError          string `json:"lastForwardingError"`
		LastForwardingErrorAtEpochMs int64  `json:"lastForwardingErrorAtEpochMs"`
		WorkerEvents                 string `json:"workerEvents"`
		WiFiFrequencyMHz             int    `json:"wifiFrequencyMHz"`
		WiFiLinkSpeedMbps            int    `json:"wifiLinkSpeedMbps"`
		WiFiRSSIDBm                  int    `json:"wifiRssiDbm"`
		ExitHistory                  []struct {
			Process          string `json:"process"`
			Reason           string `json:"reason"`
			Status           int    `json:"status"`
			TimestampEpochMs int64  `json:"timestampEpochMs"`
			Description      string `json:"description"`
			PSSKiB           uint64 `json:"pssKiB"`
			RSSKiB           uint64 `json:"rssKiB"`
		} `json:"exitHistory"`
	}
	if err := json.Unmarshal(report, &snapshot); err != nil {
		return "Nie można skrócić migawki workera: " + err.Error()
	}
	exits := make([]map[string]any, 0, min(4, len(snapshot.ExitHistory)))
	for _, exit := range snapshot.ExitHistory[:min(4, len(snapshot.ExitHistory))] {
		exits = append(exits, map[string]any{
			"process": exit.Process, "reason": exit.Reason, "status": exit.Status,
			"timestampEpochMs": exit.TimestampEpochMs, "description": exit.Description,
			"pssKiB": exit.PSSKiB, "rssKiB": exit.RSSKiB,
		})
	}
	device := "worker"
	if snapshot.Android != "" {
		device = snapshot.Device
	}
	summary := map[string]any{
		"device": device, "android": snapshot.Android, "sdk": snapshot.SDK,
		"workerBuild": snapshot.WorkerBuild, "capturedAtEpochMs": snapshot.CapturedAtEpochMs,
		"rpc": map[string]any{
			"alive": snapshot.RPCAlive, "pid": snapshot.RPCPID, "exitSummary": snapshot.RPCExitSummary,
			"residentBytes": snapshot.RPCResidentBytes, "pssBytes": snapshot.RPCPSSBytes,
			"peakResidentBytes": snapshot.RPCPeakResidentBytes, "swapBytes": snapshot.RPCSwapBytes,
			"startedAtEpochMs": snapshot.RPCStartedAtEpochMs, "stoppedAtEpochMs": snapshot.RPCStoppedAtEpochMs,
		},
		"app":                           map[string]any{"pssBytes": snapshot.AppPSSBytes, "swapBytes": snapshot.AppSwapBytes},
		"combinedProcessFootprintBytes": snapshot.CombinedProcessFootprint,
		"recovery": map[string]any{
			"recovering": snapshot.Recovering, "attempts": snapshot.RestartAttempts,
			"successfulRestarts": snapshot.RestartCount, "error": snapshot.RecoveryError,
		},
		"bridge": map[string]any{
			"adbAvailable": snapshot.ADBTLSBridgeAvailable, "connections": snapshot.BridgeConnections,
			"activeSockets": snapshot.BridgeActiveSockets, "hostToNativeBytes": snapshot.BridgeHostToNativeBytes,
			"nativeToHostBytes": snapshot.BridgeNativeToHostBytes,
		},
		"memory": map[string]any{
			"totalBytes": snapshot.MemoryTotalBytes, "availableBytes": snapshot.MemoryAvailableBytes,
			"low": snapshot.MemoryLow, "lowThresholdBytes": snapshot.MemoryLowThresholdBytes,
		},
		"cache": map[string]any{
			"bytes": snapshot.CacheBytes, "files": snapshot.CacheFiles, "partialFiles": snapshot.CachePartialFiles,
		},
		"wifi": map[string]any{
			"frequencyMHz": snapshot.WiFiFrequencyMHz, "linkSpeedMbps": snapshot.WiFiLinkSpeedMbps,
			"rssiDbm": snapshot.WiFiRSSIDBm,
		},
		"lastForwardingError":          snapshot.LastForwardingError,
		"lastForwardingErrorAtEpochMs": snapshot.LastForwardingErrorAtEpochMs,
		"recentExitHistory":            exits,
		"nativeLogBytes":               snapshot.NativeLogBytes,
		"nativeLogTail": compactRelevantLog(
			snapshot.NativeLog, 24, 8<<10,
			"error", "fail", "fatal", "crash", "killed", "signal", "out of buffer", "malformed", "memory",
		),
		"recentWorkerEvents": recentWorkerEventHeaders(snapshot.WorkerEvents, 12),
	}
	data, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return "Nie można zapisać skrótu workera: " + err.Error()
	}
	return string(data)
}

func workerDiagnosticsReport(nodes []Node, cached map[string]WorkerDiagnosticSnapshot) string {
	full, _ := workerDiagnosticsReports(nodes, cached)
	return full
}

func workerDiagnosticsReports(nodes []Node, cached map[string]WorkerDiagnosticSnapshot) (string, string) {
	if len(nodes) == 0 {
		return "", ""
	}
	var out, support strings.Builder
	out.WriteString("\n\n===== DIAGNOSTYKA WORKERÓW =====\n")
	support.WriteString("\n\n===== SKRÓT DIAGNOSTYKI WORKERÓW =====\n")
	for i, node := range nodes {
		name := node.Name
		if name == "" {
			name = "worker"
		}
		supportName := name
		if node.Platform != "android" {
			supportName = "worker"
		}
		fmt.Fprintf(
			&out,
			"\n[%d] %s (%s)\nTransport: %s — %s\nPomiar Wi-Fi: %s; USB/ADB: %s\nOstatni pomiar: RAM %.2f/%.2f GiB dostępne, proces PSS/RSS %.2f GiB + swap %.2f GiB, build %s\n",
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
			float64(node.SwapBytes)/float64(GiB),
			node.Build,
		)
		fmt.Fprintf(
			&support,
			"\n[%d] %s (%s)\nTransport: %s — %s\nPomiar Wi-Fi: %s; USB/ADB: %s\nOstatni pomiar: RAM %.2f/%.2f GiB dostępne, proces PSS/RSS %.2f GiB + swap %.2f GiB, build %s\n",
			i+1,
			supportName,
			node.Address,
			node.Transport,
			node.TransportReason,
			linkBenchmarkText(node.WiFiBenchmark),
			linkBenchmarkText(node.USBBenchmark),
			float64(node.Free)/float64(GiB),
			float64(node.Total)/float64(GiB),
			float64(node.Allocation)/float64(GiB),
			float64(node.SwapBytes)/float64(GiB),
			node.Build,
		)
		report, err := node.diagnostics()
		if err != nil {
			message := fmt.Sprintf("Nie udało się pobrać raportu po awarii: %v. Mogło zostać zakończone całe YourLM, a nie tylko natywny worker.\n", err)
			out.WriteString(message)
			support.WriteString(message)
			snapshot := cached[node.Address]
			report = snapshot.report
			if len(report) == 0 {
				out.WriteString("Brak wcześniejszej migawki diagnostycznej.\n")
				support.WriteString("Brak wcześniejszej migawki diagnostycznej.\n")
				continue
			}
			snapshotMessage := fmt.Sprintf(
				"Ostatnia udana migawka pobrana przez Windows podczas ładowania: %s\n",
				snapshot.receivedAt.Format(time.RFC3339),
			)
			out.WriteString(snapshotMessage)
			support.WriteString(snapshotMessage)
		} else {
			out.WriteString("Raport pobrany bezpośrednio po awarii:\n")
			support.WriteString("Raport pobrany bezpośrednio po awarii:\n")
		}
		assessment := workerMemoryAssessment(report)
		out.WriteString(assessment)
		support.WriteString(assessment)
		out.Write(report)
		out.WriteByte('\n')
		support.WriteString(compactWorkerDiagnostic(report))
		support.WriteByte('\n')
	}
	return out.String(), compactTextTail(support.String(), 64<<10)
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
